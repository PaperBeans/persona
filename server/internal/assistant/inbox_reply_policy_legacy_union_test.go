package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

func legacyUnionSetClassification(it *InboxItem, category, route string) {
	replyPolicyTestSetCategory(it, category)
	replyPolicyTestSetRoute(it, route)
	replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
	replyPolicyTestSetPolicyRevision(it, "test-v1")
	replyPolicyTestSetReviewed(it, false)
}

func TestInboxReplyPolicyRoutes(t *testing.T) {
	cfg := replyPolicyTestConfig()
	cfg.Sources = []string{"lark"}
	tests := []struct{ category, want string }{
		{"personal_transaction", ReplyRouteOwnerOnly}, {"security_compliance", ReplyRouteOwnerOnly},
		{"approval_permission", ReplyRouteOwnerOnly}, {"decision_delegation", ReplyRouteOwnerOnly},
		{"technical_qa", ReplyRouteBot}, {"research", ReplyRouteBot}, {"alert_triage", ReplyRouteBot}, {"ppe_deploy", ReplyRouteBot}, {"mr_precheck", ReplyRouteBot},
		{"ordinary_coordination", ReplyRouteOwnerDraft}, {"logistical_followup", ReplyRouteOwnerDraft}, {"acknowledgement", ReplyRouteOwnerDraft},
		{"no_reply", ReplyRouteNoReply}, {"unknown", ReplyRouteOwnerOnly}, {"", ReplyRouteOwnerOnly}, {"not-a-category", ReplyRouteOwnerOnly},
	}
	for _, tc := range tests {
		if got := replyRoute(tc.category, cfg); got != tc.want {
			t.Errorf("replyRoute(%q) = %q, want %q", tc.category, got, tc.want)
		}
	}
	if got := replyRoute("ordinary_coordination", assistantwire.InboxReplyPolicyConfig{}); got != "" {
		t.Fatalf("nil policy route = %q, want legacy behavior", got)
	}
	cfg.Enabled = false
	if got := replyRoute("ordinary_coordination", cfg); got != "" {
		t.Fatalf("disabled policy route = %q, want legacy behavior", got)
	}

	// Upstream categories are owner-configured rather than a second legacy safety enum. Custom names and route changes
	// are valid, and an empty explicit revision still gets a content digest; the legacy unknown_mode is categories["unknown"].
	cfg = replyPolicyTestConfig()
	cfg.Revision = ""
	cfg.Categories["approval_permission"] = ReplyRouteOwnerDraft
	cfg.Categories["technical_qa"] = ReplyRouteOwnerDraft
	cfg.Categories["unknown"] = ReplyRouteOwnerDraft
	cfg.Categories["no_reply"] = ReplyRouteOwnerDraft
	cfg.Categories["custom_owner_action"] = ReplyRouteOwnerOnly
	if revision := compileReplyPolicy(cfg).revision; revision == "" {
		t.Fatal("digest-only policy revision is empty")
	}
	for _, tc := range []struct{ category, want string }{
		{"approval_permission", ReplyRouteOwnerDraft}, {"technical_qa", ReplyRouteOwnerDraft},
		{"unknown", ReplyRouteOwnerDraft}, {"no_reply", ReplyRouteOwnerDraft},
		{"custom_owner_action", ReplyRouteOwnerOnly}, {"ordinary_coordination", ReplyRouteOwnerDraft},
	} {
		if got := replyRoute(tc.category, cfg); got != tc.want {
			t.Errorf("custom mapping %s = %q, want %q", tc.category, got, tc.want)
		}
	}
	unknownRevision := compileReplyPolicy(cfg).revision
	cfg.Categories["unknown"] = ReplyRouteOwnerOnly
	if compileReplyPolicy(cfg).revision == unknownRevision {
		t.Fatal("changing categories[unknown] did not change the effective revision")
	}

	cfg = replyPolicyTestConfig()
	cfg.Categories["research"] = "send"
	if got := replyRoute("research", cfg); got != ReplyRouteOwnerOnly {
		t.Fatalf("invalid configured route = %q, want fail-closed owner_only", got)
	}
	if got := replyRoute("ordinary_coordination", cfg); got != ReplyRouteOwnerDraft {
		t.Fatalf("one invalid custom route invalidated an unrelated valid category: %q", got)
	}
	if got := replyRoute("not-configured-anywhere", cfg); got != ReplyRouteOwnerOnly {
		t.Fatalf("unknown classifier output = %q, want fail-closed owner_only", got)
	}
}

func TestInboxReplyPolicyOverridesTwinFirst(t *testing.T) {
	h := agentHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
	it := actionItem("lark:queued:twin-first", time.Now())
	it.Source = "lark"
	it.Lark = &InboxLark{FeedID: "synthetic-chat", IsGroup: true}
	it.Agent = &InboxAgent{State: AgentQueued}
	legacyUnionSetClassification(it, "approval_permission", ReplyRouteOwnerOnly)
	h.inbox.items[it.ID] = it
	h.inbox.handing.Lock()
	t.Cleanup(h.inbox.handing.Unlock)
	setPolicyFor(t, h, InboxPolicy{Source: "lark", Tag: "chat", Value: "synthetic-chat", Twin: TwinFirst})
	before := len(conversationStarts(h.f))
	h.startInboxAgents(context.Background(), InboxAgentConfig{Enabled: true})
	checkReplyPolicyReleased(t, it, h.inbox.userLang())
	if got := len(conversationStarts(h.f)); got != before {
		t.Fatalf("TwinFirst overrode owner_only and started %d conversations", got-before)
	}
}

func TestInboxReplyPolicyFreshAdmissionAndHandback(t *testing.T) {
	t.Run("queue admission observes a newly enabled policy", func(t *testing.T) {
		h := agentHub(t)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
		h.inbox.cfgMu.Lock()
		h.inbox.cfg = assistantwire.InboxConfig{}
		h.inbox.cfgAt = time.Now()
		h.inbox.cfgMu.Unlock()
		it := actionItem("lark:fresh-queue", time.Now())
		it.Source = "lark"
		it.Agent = &InboxAgent{State: AgentQueued}
		h.inbox.items[it.ID] = it
		before := len(conversationStarts(h.f))
		h.startInboxAgents(context.Background(), InboxAgentConfig{Enabled: true})
		if got := len(conversationStarts(h.f)); got != before {
			t.Fatalf("stale disabled cache admitted %d conversations", got-before)
		}
		// Upstream releases a newly denied queued item rather than leaving it queued. Keep the original no-start
		// assertion and strengthen the adapted state assertion to verify the complete fail-closed release.
		checkReplyPolicyReleased(t, it, h.inbox.userLang())
	})
	t.Run("suggestion observes a newly enabled policy", func(t *testing.T) {
		h, _, it := handledHub(t)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
		h.inbox.cfgMu.Lock()
		h.inbox.cfg = assistantwire.InboxConfig{}
		h.inbox.cfgAt = time.Now()
		h.inbox.cfgMu.Unlock()
		// A restricted response makes the fresh policy observable: a stale disabled cache would publish this reply,
		// while a live policy must reject it rather than merely failing because no classifier was configured.
		h.in.model = fakeModel(t, `{"category":"approval_permission"}`)
		if _, err := suggestTool(t, h, `{"kind":"reply","text":"Freshly forbidden synthetic draft"}`); !replyPolicyDenied(err) {
			t.Fatalf("stale disabled cache published a newly forbidden reply: %v", err)
		}
		if currentSuggestion(it) != nil {
			t.Fatalf("freshly forbidden suggestion was stored: %+v", currentSuggestion(it))
		}
	})
	t.Run("policy changes while a suggestion waits for agent association", func(t *testing.T) {
		h := agentHub(t)
		oldWait := inboxDraftWait
		inboxDraftWait = 200 * time.Millisecond
		t.Cleanup(func() { inboxDraftWait = oldWait })
		h.inbox.cfgMu.Lock()
		h.inbox.cfg = assistantwire.InboxConfig{}
		h.inbox.cfgAt = time.Now()
		h.inbox.cfgMu.Unlock()
		h.in.mu.Lock()
		h.in.configQueue = []assistantwire.InboxConfig{{}, {ReplyPolicy: replyPolicyTestConfig()}}
		h.in.mu.Unlock()
		h.in.model = fakeModel(t, `{"category":"approval_permission"}`)
		it := actionItem("lark:association-policy-race", time.Now())
		it.Source = "lark"
		it.Agent = &InboxAgent{State: AgentRunning, Task: "synthetic-task"}
		h.inbox.items[it.ID] = it
		h.inbox.waitHook = func() {
			h.inbox.mu.Lock()
			it.Agent.SessionID = "session-policy-association"
			h.inbox.mu.Unlock()
		}
		_, _, err := h.suggest(context.Background(), "session-policy-association", suggestIn{Kind: SuggestReply, Text: "Synthetic draft across a policy change"})
		if !replyPolicyDenied(err) {
			t.Fatalf("suggestion used the pre-wait policy, error = %v", err)
		}
		if currentSuggestion(it) != nil {
			t.Fatalf("policy-enabled suggestion was published after the association wait: %+v", currentSuggestion(it))
		}
	})
	t.Run("association cannot change during the post-wait fresh read", func(t *testing.T) {
		h := agentHub(t)
		oldWait := inboxDraftWait
		inboxDraftWait = 200 * time.Millisecond
		t.Cleanup(func() { inboxDraftWait = oldWait })
		h.inbox.cfgMu.Lock()
		h.inbox.cfg = assistantwire.InboxConfig{}
		h.inbox.cfgAt = time.Now()
		h.inbox.cfgMu.Unlock()
		h.in.mu.Lock()
		h.in.configQueue = []assistantwire.InboxConfig{{}, {}}
		reads := 0
		h.in.configHook = func() {
			reads++
			if reads == 2 {
				h.inbox.mu.Lock()
				for _, item := range h.inbox.items {
					if item.ID == "lark:association-binding-race" {
						item.Agent.SessionID = "session-B"
					}
				}
				h.inbox.mu.Unlock()
			}
		}
		h.in.mu.Unlock()
		it := actionItem("lark:association-binding-race", time.Now())
		it.Source = "lark"
		it.Agent = &InboxAgent{State: AgentRunning, Task: "synthetic-task"}
		h.inbox.items[it.ID] = it
		h.inbox.waitHook = func() {
			h.inbox.mu.Lock()
			it.Agent.SessionID = "session-A"
			h.inbox.mu.Unlock()
		}
		_, _, err := h.suggest(context.Background(), "session-A", suggestIn{Kind: SuggestReply, Text: "Delayed synthetic suggestion"})
		if !errors.Is(err, errNotProcessing) {
			t.Fatalf("suggestion lost its session binding during the fresh read, error = %v", err)
		}
		if currentSuggestion(it) != nil {
			t.Fatalf("session A published against session B's item: %+v", currentSuggestion(it))
		}
	})
	t.Run("handback confirmation is not exempt", func(t *testing.T) {
		h := agentHub(t)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
		it := disabledTwinItem(h, "policy-handback", HandlingDone, AgentSkipped, "parent-session")
		it.Source = "lark"
		legacyUnionSetClassification(it, "ordinary_coordination", ReplyRouteOwnerDraft)
		g := &InboxSuggestion{Version: 1, Kind: SuggestReply, Text: "Synthetic handback", Handback: true, At: time.Now(), Conversation: &SessionRef{MachineID: "server", SessionID: "handoff-session"}}
		snapshotReplyPolicy(g, it, replyPolicyTestConfig())
		it.Suggestion = g
		replyPolicyTestSetCategory(it, "no_reply")
		replyPolicyTestSetRoute(it, ReplyRouteNoReply)
		if _, err := h.actOnSuggestion(context.Background(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm}, true); !replyPolicyDenied(err) {
			t.Fatalf("revoked handback confirmation error = %v", err)
		}
		if currentSuggestion(it) != g {
			t.Fatal("revoked handback card was consumed")
		}
		h.inbox.mu.Lock()
		err := verifyReplyPolicySuggestionLocked(it, g, nil, compileReplyPolicy(replyPolicyTestConfig()))
		h.inbox.mu.Unlock()
		if err == nil {
			t.Fatalf("revoked handback final send gate = %v", err)
		}
	})
	t.Run("stale disabled cache does not skip an edited handback veto", func(t *testing.T) {
		h := agentHub(t)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
		h.inbox.cfgMu.Lock()
		h.inbox.cfg = assistantwire.InboxConfig{}
		h.inbox.cfgAt = time.Now()
		h.inbox.cfgMu.Unlock()
		it := disabledTwinItem(h, "policy-handback-edit", HandlingDone, AgentSkipped, "parent-session")
		it.Source = "lark"
		it.Context = "Synthetic original handback conversation"
		legacyUnionSetClassification(it, "ordinary_coordination", ReplyRouteOwnerDraft)
		g := &InboxSuggestion{Version: 1, Kind: SuggestReply, Text: "Synthetic handback", Handback: true, At: time.Now(), Conversation: &SessionRef{MachineID: "server", SessionID: "handoff-session"}}
		snapshotReplyPolicy(g, it, replyPolicyTestConfig())
		it.Suggestion = g
		h.in.model = fakeModel(t, `{"category":"approval_permission"}`)
		edited := "I approve the synthetic restricted handback"
		if _, err := h.actOnSuggestion(context.Background(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm, Text: &edited}, true); !replyPolicyDenied(err) {
			t.Fatalf("stale-cache edited handback error = %v", err)
		}
		if currentSuggestion(it) != g {
			t.Fatal("restricted edited handback card was consumed")
		}
	})
	t.Run("policy changes during an edited handback veto", func(t *testing.T) {
		h := agentHub(t)
		oldCfg := assistantwire.InboxConfig{ReplyPolicy: replyPolicyTestConfig()}
		newCfg := assistantwire.InboxConfig{ReplyPolicy: replyPolicyTestConfig()}
		newCfg.ReplyPolicy.Revision = "test-v2"
		h.inbox.cfgMu.Lock()
		h.inbox.cfg = oldCfg
		h.inbox.cfgAt = time.Now()
		h.inbox.cfgMu.Unlock()
		h.in.mu.Lock()
		h.in.configQueue = []assistantwire.InboxConfig{oldCfg, newCfg}
		h.in.mu.Unlock()
		it := disabledTwinItem(h, "policy-handback-revision", HandlingDone, AgentSkipped, "parent-session")
		it.Source = "lark"
		legacyUnionSetClassification(it, "ordinary_coordination", ReplyRouteOwnerDraft)
		g := &InboxSuggestion{Version: 1, Kind: SuggestReply, Text: "Synthetic handback", Handback: true, At: time.Now(), Conversation: &SessionRef{MachineID: "server", SessionID: "handoff-session"}}
		snapshotReplyPolicy(g, it, replyPolicyTestConfig())
		it.Suggestion = g
		h.in.model = fakeModel(t, `{"category":"ordinary_coordination"}`)
		edited := "An ordinary synthetic handback edit"
		if _, err := h.actOnSuggestion(context.Background(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm, Text: &edited}, true); !replyPolicyDenied(err) {
			t.Fatalf("policy revision changed during handback veto, error = %v", err)
		}
		if currentSuggestion(it) != g {
			t.Fatal("handback card was consumed across a policy revision")
		}
	})
}

func TestInboxReplyPolicyRefreshesUnattemptedCardSnapshot(t *testing.T) {
	h, _, it := handledHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
	legacyUnionSetClassification(it, "ordinary_coordination", ReplyRouteOwnerDraft)
	if _, err := suggestOrdinaryReplyForFixture(t, h, `{"kind":"reply","text":"Same synthetic coordination reply"}`); err != nil {
		t.Fatal(err)
	}
	replyPolicyTestSetPolicyRevision(it, "test-v2")
	h.setConfig(func(c *assistantwire.InboxConfig) {
		c.ReplyPolicy = replyPolicyTestConfig()
		c.ReplyPolicy.Revision = "test-v2"
	})
	h.in.model = fakeModel(t, `{"category":"ordinary_coordination"}`)
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"Same synthetic coordination reply"}`); err != nil {
		t.Fatal(err)
	}
	g := currentSuggestion(it)
	if g == nil || g.Version != 2 || replyPolicyTestPolicyRevision(g) != "test-v2" || it.SuggestSeq != 2 {
		t.Fatalf("identical text kept a stale unattempted card: suggestion=%+v seq=%d", g, it.SuggestSeq)
	}
}

func TestInboxReplyPolicyAgentConfirmationGate(t *testing.T) {
	for _, tc := range []struct {
		name       string
		legacy     bool
		restricted bool
	}{
		{name: "legacy Agent suggestion", legacy: true},
		{name: "category became restricted", restricted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := agentHub(t)
			h.setConfig(func(c *assistantwire.InboxConfig) {
				c.Agent.Enabled = true
				c.ReplyPolicy = replyPolicyTestConfig()
			})
			it := disabledTwinItem(h, "policy-agent", HandlingDone, AgentSkipped, "parent-session")
			it.Source = "lark"
			legacyUnionSetClassification(it, "ordinary_coordination", ReplyRouteOwnerDraft)
			g := &InboxSuggestion{Version: 1, Kind: SuggestAgent, Text: "Inspect the synthetic request", At: time.Now(), MachineID: "server"}
			if !tc.legacy {
				snapshotReplyPolicy(g, it, replyPolicyTestConfig())
			}
			it.Suggestion = g
			if tc.restricted {
				replyPolicyTestSetCategory(it, "approval_permission")
				replyPolicyTestSetRoute(it, ReplyRouteOwnerOnly)
			}
			h.inbox.mu.Lock()
			gateErr := verifyReplyPolicySuggestionLocked(it, g, nil, compileReplyPolicy(h.inboxConfig(context.Background()).ReplyPolicy))
			h.inbox.mu.Unlock()
			if gateErr == nil {
				t.Fatalf("test setup did not restrict the Agent suggestion: %v, source=%q policy=%+v", gateErr, it.Source, h.inboxConfig(context.Background()).ReplyPolicy)
			}
			if _, err := h.actOnSuggestion(context.Background(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm}, true); !replyPolicyDenied(err) {
				t.Fatalf("restricted Agent confirmation error = %v", err)
			}
			if got := currentSuggestion(it); got != g {
				t.Fatalf("denied Agent confirmation consumed its draft: %+v", got)
			}
			h.f.mu.Lock()
			starts := len(h.f.starts)
			h.f.mu.Unlock()
			if starts != 0 {
				t.Fatalf("restricted Agent confirmation started %d Agents", starts)
			}
		})
	}
	t.Run("handed-off Agent rechecks fresh policy", func(t *testing.T) {
		h := agentHub(t)
		h.setConfig(func(c *assistantwire.InboxConfig) {
			c.Agent.Enabled = true
			c.ReplyPolicy = replyPolicyTestConfig()
		})
		it := disabledTwinItem(h, "policy-handed-agent", HandlingDone, AgentSkipped, "parent-session")
		it.Source = "lark"
		legacyUnionSetClassification(it, "ordinary_coordination", ReplyRouteOwnerDraft)
		g := &InboxSuggestion{Version: 1, Kind: SuggestAgent, Text: "Inspect the synthetic request", At: time.Now(), MachineID: "server"}
		snapshotReplyPolicy(g, it, replyPolicyTestConfig())
		replyPolicyTestSetCategory(it, "approval_permission")
		replyPolicyTestSetRoute(it, ReplyRouteOwnerOnly)
		_, err := h.confirmHandedAgent(context.Background(), it, g, g.Text, false, SessionRef{MachineID: "server", SessionID: "handoff-session"}, "Synthetic")
		if !replyPolicyDenied(err) {
			t.Fatalf("handed-off restricted Agent error = %v", err)
		}
		h.f.mu.Lock()
		starts := len(h.f.starts)
		h.f.mu.Unlock()
		if starts != 0 {
			t.Fatalf("handed-off restricted Agent started %d Agents", starts)
		}
	})
}

func TestInboxReplyPolicyWorkbenchTransientDraftStaysLegacy(t *testing.T) {
	needsLarkLayer(t) // the integrated Workbench renderer and actions are the lark module's
	for _, tc := range []struct {
		name   string
		policy *assistantwire.InboxReplyPolicyConfig
	}{
		{name: "nil policy"},
		{name: "disabled policy", policy: &assistantwire.InboxReplyPolicyConfig{Enabled: false, Revision: "unused-v1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, lark, it := handledHub(t)
			h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = tc.policy })
			if mirroredLegacyReply(it) != nil {
				t.Fatal("test setup unexpectedly has a mirror")
			}
			if _, err := h.replyInboxFrom(t.Context(), it.ID, "Owner-edited transient workbench text", "feishu-workbench-synthetic-transient", true); err != nil {
				t.Fatalf("legacy transient workbench send failed: %v", err)
			}
			if len(lark.got) != 1 {
				t.Fatalf("legacy transient workbench sender calls = %+v", lark.got)
			}
		})
	}
}

func TestInboxReplyPolicyDirectOwnerCardStaysLegacy(t *testing.T) {
	h, lark, it := handledHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
	if mirroredLegacyReply(it) != nil {
		t.Fatal("test setup unexpectedly has a mirror")
	}
	if _, err := h.replyInboxFrom(t.Context(), it.ID, "Direct owner text on an old-App card", "old-app-owner-card", true); err != nil {
		t.Fatalf("direct owner-authored old-App card reply was gated: %v", err)
	}
	if len(lark.got) != 1 {
		t.Fatalf("direct owner-authored old-App card sender calls = %+v", lark.got)
	}
}

func TestInboxReplyPolicyDraftDoesNotReturnUnpersistedText(t *testing.T) {
	h, _, it := handledHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
	legacyUnionSetClassification(it, "ordinary_coordination", ReplyRouteOwnerDraft)
	replyPolicyOrdinaryDraftModel(t, h, "Draft whose publication write fails")
	st := &replyPolicyDraftFailStore{inboxMemStore: h.st}
	st.before = func() {
		// Another device rejects the mirror while this write is outstanding. Its newer state prevents rollback, so
		// the exact suggestion created by this request was never persisted even though the old mirror is gone.
		h.inbox.mu.Lock()
		it.Suggestion = nil
		it.Agent.State, it.Agent.Draft = AgentSkipped, ""
		h.inbox.mu.Unlock()
	}
	h.store = func() Store { return st }
	text, err := h.draftReply(t.Context(), it.ID, "")
	if err == nil || text != "" {
		t.Fatalf("failed publication returned sendable obsolete text: text=%q err=%v", text, err)
	}
}

func TestInboxReplyPolicyClassificationRejectsContextChangeDuringModelCall(t *testing.T) {
	h := agentHub(t)
	it := actionItem("lark:classification-context-race", time.Now())
	it.Source = "lark"
	it.Agent = &InboxAgent{State: AgentQueued}
	h.inbox.items[it.ID] = it
	var changed atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if changed.CompareAndSwap(false, true) {
			h.inbox.mu.Lock()
			it.Context += "\n\nSynthetic restricted context arrived during classification"
			h.inbox.mu.Unlock()
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"content": []any{map[string]any{"type": "text", "text": `{"category":"ordinary_coordination"}`}}})
	}))
	t.Cleanup(srv.Close)
	h.in.model = modelAt(t, srv.URL)
	cfg := replyPolicyTestConfig()
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
	h.classifyInboxReplyPolicies(context.Background(), cfg)
	if !changed.Load() {
		t.Fatal("test model did not change the context")
	}
	if replyPolicyTestContentVersion(it) != "" || replyPolicyTestPolicyRevision(it) != "" || replyPolicyTestCategory(it) != "" || replyPolicyTestRoute(it) != "" {
		t.Fatalf("classification from the old context was committed after a merge: %+v", it)
	}
	if replyPolicyClassificationCurrent(it, cfg) {
		t.Fatal("changed context was treated as currently classified")
	}
}

func TestInboxReplyPolicyLegacyMirroredDraftGate(t *testing.T) {
	newDraft := func(t *testing.T, enabled bool) (*inboxHub, *extensionLark, *InboxItem) {
		h, lark, it := handledHub(t)
		if enabled {
			h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
			legacyUnionSetClassification(it, "ordinary_coordination", ReplyRouteOwnerDraft)
		}
		if _, err := suggestOrdinaryReplyForFixture(t, h, `{"kind":"reply","text":"Mirrored synthetic AI draft"}`); err != nil {
			t.Fatal(err)
		}
		if mirroredLegacyReply(it) == nil {
			t.Fatalf("test setup did not expose the old-client mirror: agent=%+v suggestion=%+v", it.Agent, currentSuggestion(it))
		}
		return h, lark, it
	}
	t.Run("pre-policy mirrored draft cannot send after enablement", func(t *testing.T) {
		h, lark, it := newDraft(t, false)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
		if _, err := h.replyInboxChecked(context.Background(), it.ID, currentSuggestion(it).Text, "", false, nil, nil); err == nil {
			t.Fatal("old client sent a pre-policy AI draft without a policy snapshot")
		}
		if len(lark.got) != 0 {
			t.Fatalf("pre-policy old-client draft called sender: %+v", lark.got)
		}
	})
	for _, tc := range []struct {
		name   string
		mutate func(*inboxHub, *InboxItem)
	}{
		{name: "route revoked", mutate: func(_ *inboxHub, it *InboxItem) {
			replyPolicyTestSetCategory(it, "approval_permission")
			replyPolicyTestSetRoute(it, ReplyRouteOwnerOnly)
		}},
		{name: "revision revoked", mutate: func(h *inboxHub, _ *InboxItem) {
			h.setConfig(func(c *assistantwire.InboxConfig) {
				c.ReplyPolicy = replyPolicyTestConfig()
				c.ReplyPolicy.Revision = "test-v2"
			})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, lark, it := newDraft(t, true)
			tc.mutate(h, it)
			if _, err := h.replyInboxChecked(context.Background(), it.ID, currentSuggestion(it).Text, "", false, nil, nil); err == nil {
				t.Fatal("old client sent after its policy authorization was revoked")
			}
			if len(lark.got) != 0 {
				t.Fatalf("revoked old-client draft called sender: %+v", lark.got)
			}
		})
	}
	t.Run("edited approval is vetoed", func(t *testing.T) {
		h, lark, it := newDraft(t, true)
		h.in.model = fakeModel(t, `{"category":"approval_permission"}`)
		if _, err := h.replyInboxChecked(context.Background(), it.ID, "I approve the synthetic restricted request", "", false, nil, nil); !replyPolicyDenied(err) {
			t.Fatalf("old-client edited approval error = %v", err)
		}
		if len(lark.got) != 0 {
			t.Fatalf("old-client edited approval called sender: %+v", lark.got)
		}
	})
	t.Run("authorized owner draft sends", func(t *testing.T) {
		h, lark, it := newDraft(t, true)
		h.in.model = fakeModel(t, `{"category":"ordinary_coordination"}`)
		if _, err := h.replyInboxChecked(context.Background(), it.ID, currentSuggestion(it).Text, "", false, nil, nil); err != nil {
			t.Fatalf("authorized old-client owner draft failed: %v", err)
		}
		if len(lark.got) != 1 {
			t.Fatalf("authorized old-client sender calls = %+v", lark.got)
		}
	})
	t.Run("direct owner-authored reply stays legacy", func(t *testing.T) {
		h, lark, it := handledHub(t)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
		if mirroredLegacyReply(it) != nil {
			t.Fatal("test setup unexpectedly has an AI mirror")
		}
		if _, err := h.replyInboxChecked(context.Background(), it.ID, "Direct owner-authored synthetic reply", "", false, nil, nil); err != nil {
			t.Fatalf("direct owner-authored reply was gated: %v", err)
		}
		if len(lark.got) != 1 {
			t.Fatalf("direct owner-authored sender calls = %+v", lark.got)
		}
	})
	for _, tc := range []struct {
		name   string
		policy *assistantwire.InboxReplyPolicyConfig
	}{
		{name: "nil policy"},
		{name: "disabled policy", policy: &assistantwire.InboxReplyPolicyConfig{Enabled: false, Revision: "unused-v1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, lark, it := newDraft(t, false)
			h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = tc.policy })
			if _, err := h.replyInboxChecked(context.Background(), it.ID, currentSuggestion(it).Text, "", false, nil, nil); err != nil {
				t.Fatalf("legacy mirrored draft failed with %s: %v", tc.name, err)
			}
			if len(lark.got) != 1 {
				t.Fatalf("legacy mirrored sender calls = %+v", lark.got)
			}
		})
	}
}

func TestInboxReplyPolicyConfigFailureDoesNotBlockOtherSuggestions(t *testing.T) {
	for _, kind := range []string{SuggestConclusion, SuggestTodo} {
		t.Run(kind, func(t *testing.T) {
			h, _, it := handledHub(t)
			it.Suggestion = &InboxSuggestion{Version: 1, Kind: kind, Text: "Synthetic non-reply action", At: time.Now()}
			h.in.mu.Lock()
			h.in.configErr = errors.New("synthetic reply-policy configuration outage")
			h.in.mu.Unlock()
			if _, err := h.actOnSuggestion(t.Context(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm}, true); err != nil {
				t.Fatalf("reply-policy configuration outage blocked %s confirmation: %v", kind, err)
			}
		})
	}
}

func TestInboxReplyPolicyClassificationFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name          string
		requireReview bool
		emptyContext  bool
	}{
		{name: "independent review failure", requireReview: true},
		{name: "empty input", emptyContext: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := agentHub(t)
			cfg := replyPolicyTestConfig()
			cfg.RequireIndependentReview = tc.requireReview
			h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
			it := actionItem("lark:classification", time.Now())
			it.Source = "lark"
			it.Agent = &InboxAgent{State: AgentQueued}
			if tc.emptyContext {
				it.Context = ""
			}
			h.inbox.items[it.ID] = it
			var reviewCalls atomic.Int32
			if tc.requireReview {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					reply := `{"category":"ordinary_coordination"}`
					if reviewCalls.Add(1) == 2 {
						reply = "malformed independent reviewer result"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"content": []any{map[string]any{"type": "text", "text": reply}}})
				}))
				t.Cleanup(srv.Close)
				h.in.model = modelAt(t, srv.URL)
			} else {
				// Use an actual model failure rather than caller cancellation: upstream intentionally does not persist a new
				// decision after its caller is canceled, while a failed classification must still become durably owner-only.
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusBadGateway)
				}))
				t.Cleanup(srv.Close)
				h.in.model = modelAt(t, srv.URL)
			}
			h.classifyInboxReplyPolicies(context.Background(), cfg)
			if replyPolicyTestCategory(it) != "unknown" || replyPolicyTestRoute(it) != ReplyRouteOwnerOnly || replyPolicyTestContentVersion(it) != replyPolicyContentVersion(it) || replyPolicyTestPolicyRevision(it) != "test-v1" || replyPolicyTestReviewed(it) {
				t.Fatalf("classification failure did not fail closed: %+v", it)
			}
			if tc.requireReview && reviewCalls.Load() != 2 {
				t.Fatalf("independent review failure made %d model calls, want successful primary plus failed reviewer", reviewCalls.Load())
			}
			before := len(conversationStarts(h.f))
			h.startInboxAgents(context.Background(), InboxAgentConfig{Enabled: true})
			checkReplyPolicyReleased(t, it, h.inbox.userLang())
			if got := len(conversationStarts(h.f)); got != before {
				t.Fatalf("failed classification started %d handling conversations", got-before)
			}
		})
	}
}

func TestInboxReplyPolicyReviewRequirementChangeReclassifies(t *testing.T) {
	h := agentHub(t)
	model, calls := captureModel(t, `{"category":"ordinary_coordination"}`)
	h.in.model = model
	cfg := replyPolicyTestConfig()
	cfg.RequireIndependentReview = true
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
	it := actionItem("lark:review-required", time.Now())
	it.Source = "lark"
	legacyUnionSetClassification(it, "ordinary_coordination", ReplyRouteOwnerDraft)
	h.inbox.items[it.ID] = it
	h.classifyInboxReplyPolicies(context.Background(), cfg)
	if !replyPolicyTestReviewed(it) || !replyPolicyClassificationCurrent(it, cfg) {
		t.Fatalf("enabling independent review left an unreviewed owner draft current: %+v", it)
	}
	got := calls()
	classifierSystem := replyPolicyTestClassifierSystem(h, cfg)
	if len(got) != 2 || got[0].System != classifierSystem || got[1].System != classifierSystem {
		t.Fatalf("review requirement did not run primary plus fully instructed independent review: %+v", got)
	}
}

func TestInboxReplyPolicyIndependentReviewDisagreementFailsClosed(t *testing.T) {
	h := agentHub(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		category := "ordinary_coordination"
		if calls.Add(1) == 2 {
			category = "approval_permission"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"content": []any{map[string]any{"type": "text", "text": `{"category":"` + category + `"}`}}})
	}))
	t.Cleanup(srv.Close)
	h.in.model = modelAt(t, srv.URL)
	cfg := replyPolicyTestConfig()
	cfg.RequireIndependentReview = true
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
	it := actionItem("lark:review-disagreement", time.Now())
	it.Source = "lark"
	it.Agent = &InboxAgent{State: AgentQueued}
	h.inbox.items[it.ID] = it
	h.classifyInboxReplyPolicies(context.Background(), cfg)
	if calls.Load() != 2 {
		t.Fatalf("independent review made %d model calls, want 2", calls.Load())
	}
	if replyPolicyTestCategory(it) != "unknown" || replyPolicyTestRoute(it) != ReplyRouteOwnerOnly || replyPolicyTestReviewed(it) {
		t.Fatalf("review disagreement did not fail closed: %+v", it)
	}
	before := len(conversationStarts(h.f))
	h.startInboxAgents(context.Background(), InboxAgentConfig{Enabled: true})
	checkReplyPolicyReleased(t, it, h.inbox.userLang())
	if got := len(conversationStarts(h.f)); got != before {
		t.Fatalf("review disagreement started %d handling conversations", got-before)
	}
}
