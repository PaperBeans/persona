package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

func legacyReplyPolicyTestConfig() *assistantwire.InboxReplyPolicyConfig {
	return &assistantwire.InboxReplyPolicyConfig{
		Enabled: true, Revision: "test-v1", Sources: []string{"lark"}, UnknownMode: "owner_only",
		Routes: map[string]string{
			"personal_transaction": "owner_only", "security_compliance": "owner_only",
			"approval_permission": "owner_only", "decision_delegation": "owner_only",
			"technical_qa": "bot", "research": "bot", "alert_triage": "bot", "ppe_deploy": "bot", "mr_precheck": "bot",
			"no_reply": "no_reply", "ordinary_coordination": "owner_draft", "logistical_followup": "owner_draft",
			"acknowledgement": "owner_draft", "other_owner_reply": "owner_draft", "unknown": "owner_only",
		},
	}
}

func TestInboxReplyPolicyRoutesLegacySchema(t *testing.T) {
	cfg := legacyReplyPolicyTestConfig()
	tests := []struct{ category, want string }{
		{"personal_transaction", "owner_only"}, {"security_compliance", "owner_only"},
		{"approval_permission", "owner_only"}, {"decision_delegation", "owner_only"},
		{"technical_qa", "bot"}, {"research", "bot"}, {"alert_triage", "bot"}, {"ppe_deploy", "bot"}, {"mr_precheck", "bot"},
		{"ordinary_coordination", "owner_draft"}, {"logistical_followup", "owner_draft"}, {"acknowledgement", "owner_draft"},
		{"no_reply", "no_reply"}, {"unknown", "owner_only"}, {"", "owner_only"}, {"not-a-category", "owner_only"},
	}
	for _, tc := range tests {
		if got := replyRoute(tc.category, cfg); got != tc.want {
			t.Errorf("replyRoute(%q) = %q, want %q", tc.category, got, tc.want)
		}
	}
	if got := replyRoute("ordinary_coordination", nil); got != "" {
		t.Fatalf("nil policy route = %q, want legacy behavior", got)
	}
	cfg.Enabled = false
	if got := replyRoute("ordinary_coordination", cfg); got != "" {
		t.Fatalf("disabled policy route = %q, want legacy behavior", got)
	}
	cfg = legacyReplyPolicyTestConfig()
	cfg.Routes["approval_permission"] = "owner_draft"
	cfg.Routes["technical_qa"] = "owner_draft"
	cfg.Routes["unknown"] = "owner_draft"
	cfg.Routes["no_reply"] = "owner_draft"
	cfg.UnknownMode = "owner_draft"
	for _, tc := range []struct{ category, want string }{
		{"approval_permission", "owner_only"}, {"technical_qa", "bot"},
		{"unknown", "owner_only"}, {"no_reply", "no_reply"}, {"ordinary_coordination", "owner_draft"},
	} {
		if got := replyRoute(tc.category, cfg); got != tc.want {
			t.Errorf("unsafe override %s = %q, want %q", tc.category, got, tc.want)
		}
	}
	cfg = legacyReplyPolicyTestConfig()
	cfg.Revision = ""
	for _, category := range []string{"approval_permission", "technical_qa", "ordinary_coordination", "unknown"} {
		if got := replyRoute(category, cfg); got != "owner_only" {
			t.Errorf("empty revision %s = %q, want owner_only", category, got)
		}
	}
	for _, tc := range []struct{ name, key, value string }{
		{"invalid category route", "research", "send"},
		{"unknown category key", "misspelled_category", "owner_draft"},
		{"invalid unknown mode", "", "send"},
	} {
		cfg = legacyReplyPolicyTestConfig()
		if tc.key == "" {
			cfg.UnknownMode = tc.value
		} else {
			cfg.Routes[tc.key] = tc.value
		}
		for _, category := range []string{"technical_qa", "ordinary_coordination", "unknown"} {
			if got := replyRoute(category, cfg); got != "owner_only" {
				t.Errorf("%s: %s = %q, want owner_only", tc.name, category, got)
			}
		}
	}
}

func TestInboxReplyPolicyQueueGate(t *testing.T) {
	for _, route := range []string{"owner_only", "bot", "no_reply"} {
		t.Run(route, func(t *testing.T) {
			h := agentHub(t)
			h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
			it := actionItem("lark:queued:"+route, time.Now())
			it.Source = "lark"
			it.ReplyCategory, it.ReplyMode = map[string]string{"owner_only": "approval_permission", "bot": "technical_qa", "no_reply": "no_reply"}[route], route
			it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
			it.Agent = &InboxAgent{State: AgentQueued}
			h.inbox.items[it.ID] = it
			before := len(conversationStarts(h.f))
			h.startInboxAgents(context.Background(), InboxAgentConfig{Enabled: true})
			checkPolicyReleased(t, it, h.inbox.userLang())
			if got := len(conversationStarts(h.f)); got != before {
				t.Fatalf("policy route %s started %d handling conversations", route, got-before)
			}
		})
	}
	t.Run("unclassified waits for a later classification pass", func(t *testing.T) {
		h := agentHub(t)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
		it := actionItem("lark:queued:unclassified", time.Now())
		it.Source = "lark"
		it.Agent = &InboxAgent{State: AgentQueued}
		h.inbox.items[it.ID] = it
		before := len(conversationStarts(h.f))
		h.startInboxAgents(context.Background(), InboxAgentConfig{Enabled: true})
		if got := len(conversationStarts(h.f)); got != before {
			t.Fatalf("unclassified item started %d handling conversations", got-before)
		}
		if it.Agent.State != AgentQueued || it.Handling != nil && it.Handling.State == HandlingDone {
			t.Fatalf("unclassified item was released instead of waiting: agent=%+v handling=%+v", it.Agent, it.Handling)
		}
	})
}

func TestInboxReplyPolicyRestrictedHandPassSkipsTaskSideEffectsLegacySchema(t *testing.T) {
	for _, tc := range []struct {
		name, category, route string
	}{
		{name: "owner only", category: "approval_permission", route: ReplyRouteOwnerOnly},
		{name: "bot", category: "technical_qa", route: ReplyRouteBot},
		{name: "no reply", category: "no_reply", route: ReplyRouteNoReply},
		{name: "ordinary positive control", category: "ordinary_coordination", route: ReplyRouteOwnerDraft},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			h := agentHub(t)
			turnInboxSignalsOn(t, h.Service)
			todo := supervisedTodo(t, h.Service, "Review the change")
			h.setConfig(func(c *assistantwire.InboxConfig) {
				c.Agent.Enabled = true
				c.ReplyPolicy = legacyReplyPolicyTestConfig()
			})
			model, calls := captureModel(t, `{"task_id":"`+todo.ID+`","evidence":"Review the change"}`)
			h.in.setModel(model)
			it := actionItem("lark:hand-pass:"+tc.category, time.Now())
			it.Source = "lark"
			it.ReplyCategory, it.ReplyMode = tc.category, tc.route
			it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
			h.inbox.items[it.ID] = it
			h.handPass(ctx)
			allowed := tc.route == ReplyRouteOwnerDraft
			wantCalls := 0
			if allowed {
				wantCalls = 1
			}
			if got := len(calls()); got != wantCalls {
				t.Fatalf("%s item reached the task judge %d times, want %d", tc.route, got, wantCalls)
			}
			if allowed {
				if it.Agent == nil || it.Agent.TodoID != todo.ID {
					t.Fatalf("ordinary item did not retain task linking: %+v", it.Agent)
				}
				return
			}
			checkPolicyReleased(t, it, h.inbox.userLang())
			if it.Agent != nil && it.Agent.TodoID != "" {
				t.Fatalf("%s item linked to task %s", tc.route, it.Agent.TodoID)
			}
			if g, ok := pendingInbox(h.Service, todo.ID); ok {
				t.Fatalf("%s item raised a supervised Inbox signal: %+v", tc.route, g.Inbox)
			}
		})
	}
}

func TestInboxReplyPolicyOverridesTwinFirstLegacySchema(t *testing.T) {
	h := agentHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
	it := actionItem("lark:queued:twin-first", time.Now())
	it.Source = "lark"
	it.Lark = &InboxLark{FeedID: "synthetic-chat", IsGroup: true}
	it.ReplyCategory, it.ReplyMode = "approval_permission", "owner_only"
	it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
	it.Agent = &InboxAgent{State: AgentQueued}
	h.inbox.items[it.ID] = it
	h.inbox.handing.Lock()
	t.Cleanup(h.inbox.handing.Unlock)
	setPolicyFor(t, h, InboxPolicy{Source: "lark", Tag: "chat", Value: "synthetic-chat", Twin: TwinFirst})
	before := len(conversationStarts(h.f))
	h.startInboxAgents(context.Background(), InboxAgentConfig{Enabled: true})
	checkPolicyReleased(t, it, h.inbox.userLang())
	if got := len(conversationStarts(h.f)); got != before {
		t.Fatalf("TwinFirst overrode owner_only and started %d conversations", got-before)
	}
}

func TestInboxReplyPolicyClassifiesTwinFirstFYIBeforeQueueLegacySchema(t *testing.T) {
	h := agentHub(t)
	h.in.model = fakeModel(t, `{"category":"no_reply"}`)
	h.setConfig(func(c *assistantwire.InboxConfig) {
		c.Agent.Enabled = true
		c.ReplyPolicy = legacyReplyPolicyTestConfig()
	})
	it := actionItem("lark:queued:twin-first-fyi", time.Now())
	it.Source, it.Tier, it.TriagedAt = "lark", TierFYI, it.Last
	it.Lark = &InboxLark{FeedID: "synthetic-fyi-chat", IsGroup: true}
	h.inbox.items[it.ID] = it
	h.inbox.handing.Lock()
	t.Cleanup(h.inbox.handing.Unlock)
	setPolicyFor(t, h, InboxPolicy{Source: "lark", Tag: "chat", Value: "synthetic-fyi-chat", Twin: TwinFirst})
	h.handPass(context.Background())
	if it.ReplyCategory != "no_reply" || it.ReplyMode != "no_reply" || !replyPolicyClassificationCurrent(it, legacyReplyPolicyTestConfig()) {
		t.Fatalf("TwinFirst FYI remained unclassifiable: %+v", it)
	}
	h.inbox.escalate(time.Now().Add(-time.Hour), time.Now())
	before := len(conversationStarts(h.f))
	h.startInboxAgents(context.Background(), InboxAgentConfig{Enabled: true})
	checkPolicyReleased(t, it, h.inbox.userLang())
	if got := len(conversationStarts(h.f)); got != before {
		t.Fatalf("classified TwinFirst FYI started %d conversations", got-before)
	}
}

func TestInboxReplyPolicyFreshAdmissionAndHandbackLegacySchema(t *testing.T) {
	t.Run("queue admission observes a newly enabled policy", func(t *testing.T) {
		h := agentHub(t)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
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
		if it.Agent.State != AgentQueued {
			t.Fatalf("unclassified newly enabled item left the queue: %+v", it.Agent)
		}
	})
	t.Run("suggestion observes a newly enabled policy", func(t *testing.T) {
		h, _, it := handledHub(t)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
		h.inbox.cfgMu.Lock()
		h.inbox.cfg = assistantwire.InboxConfig{}
		h.inbox.cfgAt = time.Now()
		h.inbox.cfgMu.Unlock()
		if _, err := suggestTool(t, h, `{"kind":"reply","text":"Freshly forbidden synthetic draft"}`); err == nil {
			t.Fatal("stale disabled cache published an unclassified reply")
		}
		if currentSuggestion(it) != nil {
			t.Fatalf("freshly forbidden suggestion was stored: %+v", currentSuggestion(it))
		}
	})
	t.Run("policy changes while a suggestion waits for agent association", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := agentHub(t)
			oldWait := inboxDraftWait
			inboxDraftWait = time.Second
			t.Cleanup(func() { inboxDraftWait = oldWait })
			h.inbox.cfgMu.Lock()
			h.inbox.cfg = assistantwire.InboxConfig{}
			h.inbox.cfgAt = time.Now()
			h.inbox.cfgMu.Unlock()
			h.in.mu.Lock()
			h.in.configQueue = []assistantwire.InboxConfig{{}, {ReplyPolicy: legacyReplyPolicyTestConfig()}}
			h.in.mu.Unlock()
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
			if !errors.Is(err, errReplyPolicyDenied) {
				t.Fatalf("suggestion used the pre-wait policy, error = %v", err)
			}
			if currentSuggestion(it) != nil {
				t.Fatalf("policy-enabled suggestion was published after the association wait: %+v", currentSuggestion(it))
			}
		})
	})
	t.Run("association cannot change during the post-wait fresh read", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := agentHub(t)
			oldWait := inboxDraftWait
			inboxDraftWait = time.Second
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
	})
	t.Run("handback confirmation is not exempt", func(t *testing.T) {
		h := agentHub(t)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
		it := disabledTwinItem(h, "policy-handback", HandlingDone, AgentSkipped, "parent-session")
		it.Source = "lark"
		it.ReplyCategory, it.ReplyMode, it.ReplyClassifiedOf, it.ReplyPolicyRev = "ordinary_coordination", "owner_draft", replyPolicyContentVersion(it), "test-v1"
		g := &InboxSuggestion{Version: 1, Kind: SuggestReply, Text: "Synthetic handback", Handback: true, At: time.Now(), Conversation: &SessionRef{MachineID: "server", SessionID: "handoff-session"}}
		snapshotReplyPolicy(g, it, legacyReplyPolicyTestConfig())
		it.Suggestion = g
		it.ReplyCategory, it.ReplyMode = "no_reply", "no_reply"
		if _, err := h.actOnSuggestion(context.Background(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm}, true); !errors.Is(err, errReplyPolicyDenied) {
			t.Fatalf("revoked handback confirmation error = %v", err)
		}
		if currentSuggestion(it) != g {
			t.Fatal("revoked handback card was consumed")
		}
		if err := replyPolicySendGate(it, g, legacyReplyPolicyTestConfig()); !errors.Is(err, errReplyPolicyDenied) {
			t.Fatalf("revoked handback final send gate = %v", err)
		}
	})
	t.Run("stale disabled cache does not skip an edited handback veto", func(t *testing.T) {
		h := agentHub(t)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
		h.inbox.cfgMu.Lock()
		h.inbox.cfg = assistantwire.InboxConfig{}
		h.inbox.cfgAt = time.Now()
		h.inbox.cfgMu.Unlock()
		it := disabledTwinItem(h, "policy-handback-edit", HandlingDone, AgentSkipped, "parent-session")
		it.Source = "lark"
		it.Context = "Synthetic original handback conversation"
		it.ReplyCategory, it.ReplyMode, it.ReplyClassifiedOf, it.ReplyPolicyRev = "ordinary_coordination", "owner_draft", replyPolicyContentVersion(it), "test-v1"
		g := &InboxSuggestion{Version: 1, Kind: SuggestReply, Text: "Synthetic handback", Handback: true, At: time.Now(), Conversation: &SessionRef{MachineID: "server", SessionID: "handoff-session"}}
		snapshotReplyPolicy(g, it, legacyReplyPolicyTestConfig())
		it.Suggestion = g
		h.in.model = fakeModel(t, `{"category":"approval_permission"}`)
		edited := "I approve the synthetic restricted handback"
		if _, err := h.actOnSuggestion(context.Background(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm, Text: &edited}, true); !errors.Is(err, errReplyPolicyDenied) {
			t.Fatalf("stale-cache edited handback error = %v", err)
		}
		if currentSuggestion(it) != g {
			t.Fatal("restricted edited handback card was consumed")
		}
	})
	t.Run("policy changes during an edited handback veto", func(t *testing.T) {
		h := agentHub(t)
		oldCfg := assistantwire.InboxConfig{ReplyPolicy: legacyReplyPolicyTestConfig()}
		newCfg := assistantwire.InboxConfig{ReplyPolicy: legacyReplyPolicyTestConfig()}
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
		it.ReplyCategory, it.ReplyMode, it.ReplyClassifiedOf, it.ReplyPolicyRev = "ordinary_coordination", "owner_draft", replyPolicyContentVersion(it), "test-v1"
		g := &InboxSuggestion{Version: 1, Kind: SuggestReply, Text: "Synthetic handback", Handback: true, At: time.Now(), Conversation: &SessionRef{MachineID: "server", SessionID: "handoff-session"}}
		snapshotReplyPolicy(g, it, legacyReplyPolicyTestConfig())
		it.Suggestion = g
		h.in.model = fakeModel(t, `{"category":"ordinary_coordination"}`)
		edited := "An ordinary synthetic handback edit"
		if _, err := h.actOnSuggestion(context.Background(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm, Text: &edited}, true); !errors.Is(err, errReplyPolicyDenied) {
			t.Fatalf("policy revision changed during handback veto, error = %v", err)
		}
		if currentSuggestion(it) != g {
			t.Fatal("handback card was consumed across a policy revision")
		}
	})
}

func TestInboxReplySuggestionGate(t *testing.T) {
	for _, tc := range []struct {
		category, mode string
		allowed        bool
	}{
		{"personal_transaction", "owner_only", false}, {"security_compliance", "owner_only", false},
		{"approval_permission", "owner_only", false}, {"decision_delegation", "owner_only", false},
		{"technical_qa", "bot", false}, {"research", "bot", false}, {"alert_triage", "bot", false},
		{"ppe_deploy", "bot", false}, {"mr_precheck", "bot", false}, {"unknown", "owner_only", false},
		{"ordinary_coordination", "owner_draft", true},
	} {
		t.Run(tc.category, func(t *testing.T) {
			h, _, it := handledHub(t)
			h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
			it.ReplyCategory, it.ReplyMode, it.ReplyClassifiedOf, it.ReplyPolicyRev = tc.category, tc.mode, replyPolicyContentVersion(it), "test-v1"
			_, err := suggestTool(t, h, `{"kind":"reply","text":"Synthetic coordination reply"}`)
			if tc.allowed && err != nil {
				t.Fatalf("owner draft refused: %v", err)
			}
			if !tc.allowed && err == nil {
				t.Fatal("non-owner-draft reply was accepted")
			}
			if !tc.allowed && currentSuggestion(it) != nil {
				t.Fatalf("refused suggestion was stored: %+v", currentSuggestion(it))
			}
			if tc.allowed {
				g := currentSuggestion(it)
				if g == nil || g.ReplyCategory != tc.category || g.ReplyMode != "owner_draft" || g.ReplyClassifiedOf != replyPolicyContentVersion(it) || g.ReplyPolicyRev != "test-v1" {
					t.Fatalf("server did not snapshot trusted policy fields: %+v", g)
				}
			}
		})
	}
}

func TestInboxReplyPolicyRefreshesUnattemptedCardSnapshotLegacySchema(t *testing.T) {
	h, _, it := handledHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
	it.ReplyCategory, it.ReplyMode, it.ReplyClassifiedOf, it.ReplyPolicyRev = "ordinary_coordination", "owner_draft", replyPolicyContentVersion(it), "test-v1"
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"Same synthetic coordination reply"}`); err != nil {
		t.Fatal(err)
	}
	it.ReplyPolicyRev = "test-v2"
	h.setConfig(func(c *assistantwire.InboxConfig) {
		c.ReplyPolicy = legacyReplyPolicyTestConfig()
		c.ReplyPolicy.Revision = "test-v2"
	})
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"Same synthetic coordination reply"}`); err != nil {
		t.Fatal(err)
	}
	g := currentSuggestion(it)
	if g == nil || g.Version != 2 || g.ReplyPolicyRev != "test-v2" || it.SuggestSeq != 2 {
		t.Fatalf("identical text kept a stale unattempted card: suggestion=%+v seq=%d", g, it.SuggestSeq)
	}
}

func TestInboxReplyPolicyAgentConfirmationGateLegacySchema(t *testing.T) {
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
				c.ReplyPolicy = legacyReplyPolicyTestConfig()
			})
			it := disabledTwinItem(h, "policy-agent", HandlingDone, AgentSkipped, "parent-session")
			it.Source = "lark"
			it.ReplyCategory, it.ReplyMode, it.ReplyClassifiedOf, it.ReplyPolicyRev = "ordinary_coordination", "owner_draft", replyPolicyContentVersion(it), "test-v1"
			g := &InboxSuggestion{Version: 1, Kind: SuggestAgent, Text: "Inspect the synthetic request", At: time.Now(), MachineID: "server"}
			if !tc.legacy {
				snapshotReplyPolicy(g, it, legacyReplyPolicyTestConfig())
			}
			it.Suggestion = g
			if tc.restricted {
				it.ReplyCategory, it.ReplyMode = "approval_permission", "owner_only"
			}
			if err := replyPolicyActionGate(it, g, h.inboxConfig(context.Background()).ReplyPolicy); !errors.Is(err, errReplyPolicyDenied) {
				t.Fatalf("test setup did not restrict the Agent suggestion: %v, source=%q policy=%+v", err, it.Source, h.inboxConfig(context.Background()).ReplyPolicy)
			}
			if _, err := h.actOnSuggestion(context.Background(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm}, true); !errors.Is(err, errReplyPolicyDenied) {
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
			c.ReplyPolicy = legacyReplyPolicyTestConfig()
		})
		it := disabledTwinItem(h, "policy-handed-agent", HandlingDone, AgentSkipped, "parent-session")
		it.Source = "lark"
		it.ReplyCategory, it.ReplyMode, it.ReplyClassifiedOf, it.ReplyPolicyRev = "ordinary_coordination", "owner_draft", replyPolicyContentVersion(it), "test-v1"
		g := &InboxSuggestion{Version: 1, Kind: SuggestAgent, Text: "Inspect the synthetic request", At: time.Now(), MachineID: "server"}
		snapshotReplyPolicy(g, it, legacyReplyPolicyTestConfig())
		it.ReplyCategory, it.ReplyMode = "approval_permission", "owner_only"
		_, err := h.confirmHandedAgent(context.Background(), it, g, g.Text, false, SessionRef{MachineID: "server", SessionID: "handoff-session"}, "Synthetic")
		if !errors.Is(err, errReplyPolicyDenied) {
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

func TestInboxReplySuggestionSendGateLegacySchema(t *testing.T) {
	newDraft := func(t *testing.T) (*inboxHub, *extensionLark, *InboxItem) {
		h, lark, it := handledHub(t)
		h.in.model = fakeModel(t, `{"category":"ordinary_coordination"}`)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
		it.ReplyCategory, it.ReplyMode, it.ReplyClassifiedOf, it.ReplyPolicyRev = "ordinary_coordination", "owner_draft", replyPolicyContentVersion(it), "test-v1"
		if _, err := suggestTool(t, h, `{"kind":"reply","text":"The original synthetic reply"}`); err != nil {
			t.Fatal(err)
		}
		return h, lark, it
	}
	t.Run("ordinary draft stores proposal actual and category", func(t *testing.T) {
		h, lark, it := newDraft(t)
		edited := "The owner-edited synthetic reply"
		if _, err := h.actOnSuggestion(context.Background(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm, Text: &edited}, true); err != nil {
			t.Fatal(err)
		}
		if len(lark.got) != 1 || lark.got[0].Text != edited {
			t.Fatalf("confirmed sender calls = %+v", lark.got)
		}
		a := suggestionAttempt(it, 1)
		if a == nil || a.Status != suggestionSent || a.Text != edited || a.Proposal == nil || a.Proposal.Text != "The original synthetic reply" ||
			a.Proposal.ReplyCategory != "ordinary_coordination" || a.Proposal.ReplyMode != "owner_draft" || a.Proposal.ReplyPolicyRev != "test-v1" {
			t.Fatalf("attempt snapshot = %+v", a)
		}
	})
	for _, tc := range []struct {
		name   string
		mutate func(*InboxItem)
	}{
		{"legacy draft has no category", func(it *InboxItem) {
			it.ReplyCategory, it.ReplyMode, it.ReplyClassifiedOf, it.ReplyPolicyRev = "", "", "", ""
			it.Suggestion.ReplyCategory, it.Suggestion.ReplyMode, it.Suggestion.ReplyClassifiedOf, it.Suggestion.ReplyPolicyRev = "", "", "", ""
		}},
		{"policy revision changed", func(it *InboxItem) { it.ReplyPolicyRev = "test-v2" }},
		{"content changed", func(it *InboxItem) { it.Last = "newer-content" }},
		{"route is no longer owner draft", func(it *InboxItem) { it.ReplyCategory, it.ReplyMode = "technical_qa", "bot" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, lark, it := newDraft(t)
			tc.mutate(it)
			if _, err := h.actOnSuggestion(context.Background(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm}, true); err == nil {
				t.Fatal("stale or forbidden reply was confirmed")
			}
			if len(lark.got) != 0 {
				t.Fatalf("refused confirmation called sender: %+v", lark.got)
			}
		})
	}
	t.Run("policy changes while the reservation is saved", func(t *testing.T) {
		h, lark, it := newDraft(t)
		st := &recoveryBeforeWriteStore{inboxMemStore: h.st, before: func() {
			h.inbox.mu.Lock()
			defer h.inbox.mu.Unlock()
			it.ReplyCategory, it.ReplyMode = "approval_permission", "owner_only"
		}}
		h.setStore(func() Store { return st })
		if _, err := h.actOnSuggestion(context.Background(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm}, true); err == nil {
			t.Fatal("reply was confirmed after its policy became forbidden during persistence")
		}
		if len(lark.got) != 0 {
			t.Fatalf("pre-send recheck missed the policy change and called sender: %+v", lark.got)
		}
	})
	t.Run("policy revision changes while the reservation is saved", func(t *testing.T) {
		h, lark, it := newDraft(t)
		st := &recoveryBeforeWriteStore{inboxMemStore: h.st, before: func() {
			// Deliberately keep the manager's five-second cache alive. Only the fresh outbound read may observe this.
			h.in.mu.Lock()
			cfg := legacyReplyPolicyTestConfig()
			cfg.Revision = "test-v2"
			h.in.cfg.ReplyPolicy = cfg
			h.in.mu.Unlock()
		}}
		h.setStore(func() Store { return st })
		if _, err := h.actOnSuggestion(context.Background(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm}, true); err == nil {
			t.Fatal("reply was confirmed against a superseded policy revision during persistence")
		}
		if len(lark.got) != 0 {
			t.Fatalf("pre-send recheck missed the revision change and called sender: %+v", lark.got)
		}
	})
	t.Run("fresh configuration failure is known not sent", func(t *testing.T) {
		h, lark, it := newDraft(t)
		st := &recoveryBeforeWriteStore{inboxMemStore: h.st, before: func() {
			h.in.mu.Lock()
			h.in.configErr = errors.New("synthetic configuration outage")
			h.in.mu.Unlock()
		}}
		h.setStore(func() Store { return st })
		_, err := h.actOnSuggestion(context.Background(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm}, true)
		var rpcErr *rpc.Error
		if !errors.As(err, &rpcErr) || rpcErr.Code != assistantwire.CodeInboxSendFailed {
			t.Fatalf("fresh configuration failure error = %v", err)
		}
		if len(lark.got) != 0 {
			t.Fatalf("configuration outage called sender: %+v", lark.got)
		}
		a := suggestionAttempt(it, 1)
		if a == nil || a.Status != suggestionFailed || !a.Retryable || a.Code != assistantwire.CodeInboxSendFailed {
			t.Fatalf("known-none configuration failure was not retryable: %+v", a)
		}
	})
	t.Run("empty or null configuration is not authorization", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			raw  json.RawMessage
		}{
			{name: "empty", raw: json.RawMessage{}},
			{name: "null", raw: json.RawMessage("null")},
		} {
			t.Run(tc.name, func(t *testing.T) {
				h, lark, it := newDraft(t)
				st := &recoveryBeforeWriteStore{inboxMemStore: h.st, before: func() {
					h.in.mu.Lock()
					h.in.configRaw = tc.raw
					h.in.mu.Unlock()
				}}
				h.setStore(func() Store { return st })
				_, err := h.actOnSuggestion(context.Background(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm}, true)
				var rpcErr *rpc.Error
				if !errors.As(err, &rpcErr) || rpcErr.Code != assistantwire.CodeInboxSendFailed {
					t.Fatalf("absent configuration error = %v", err)
				}
				if len(lark.got) != 0 {
					t.Fatalf("absent configuration called sender: %+v", lark.got)
				}
			})
		}
	})
	t.Run("inert disabled configuration changes preserve legacy sends", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			before assistantwire.InboxConfig
			after  assistantwire.InboxConfig
		}{
			{name: "nil to disabled", after: assistantwire.InboxConfig{ReplyPolicy: &assistantwire.InboxReplyPolicyConfig{Enabled: false}}},
			{name: "disabled revision", before: assistantwire.InboxConfig{ReplyPolicy: &assistantwire.InboxReplyPolicyConfig{Revision: "unused-v1"}},
				after: assistantwire.InboxConfig{ReplyPolicy: &assistantwire.InboxReplyPolicyConfig{Revision: "unused-v2"}}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				h, lark, it := handledHub(t)
				if _, err := suggestTool(t, h, `{"kind":"reply","text":"Legacy synthetic reply"}`); err != nil {
					t.Fatal(err)
				}
				st := &recoveryBeforeWriteStore{inboxMemStore: h.st, before: func() {
					h.in.mu.Lock()
					h.in.configQueue = []assistantwire.InboxConfig{tc.before, tc.after}
					h.in.mu.Unlock()
				}}
				h.setStore(func() Store { return st })
				if _, err := h.actOnSuggestion(context.Background(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm}, true); err != nil {
					t.Fatalf("inert disabled configuration rejected a legacy send: %v", err)
				}
				if len(lark.got) != 1 {
					t.Fatalf("legacy sender calls = %+v", lark.got)
				}
			})
		}
	})
	t.Run("policy changes between the two final fresh reads", func(t *testing.T) {
		h, lark, it := newDraft(t)
		st := &recoveryBeforeWriteStore{inboxMemStore: h.st, before: func() {
			oldCfg := assistantwire.InboxConfig{ReplyPolicy: legacyReplyPolicyTestConfig()}
			newCfg := assistantwire.InboxConfig{ReplyPolicy: legacyReplyPolicyTestConfig()}
			newCfg.ReplyPolicy.Revision = "test-v2"
			h.in.mu.Lock()
			h.in.configQueue = []assistantwire.InboxConfig{oldCfg, newCfg}
			h.in.mu.Unlock()
		}}
		h.setStore(func() Store { return st })
		if _, err := h.actOnSuggestion(context.Background(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm}, true); err == nil {
			t.Fatal("reply used the pre-veto policy after the final fresh read changed")
		}
		if len(lark.got) != 0 {
			t.Fatalf("second fresh-read race called sender: %+v", lark.got)
		}
	})
	t.Run("a stale edit veto does not reject a newly legacy send", func(t *testing.T) {
		h, lark, it := newDraft(t)
		h.in.mu.Lock()
		h.in.cfg.ReplyPolicy = nil
		h.in.mu.Unlock()
		h.in.model = fakeModel(t, `{"category":"approval_permission"}`)
		edited := "I approve the synthetic legacy request"
		if _, err := h.actOnSuggestion(context.Background(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm, Text: &edited}, true); err != nil {
			t.Fatalf("stale cached veto rejected a newly disabled policy: %v", err)
		}
		if len(lark.got) != 1 {
			t.Fatalf("newly legacy sender calls = %+v", lark.got)
		}
	})
	t.Run("policy snapshots do not change uncertain send identity", func(t *testing.T) {
		h, lark, it := newDraft(t)
		g := currentSuggestion(it)
		old := copySuggestion(g)
		old.ReplyCategory, old.ReplyMode, old.ReplyPolicyRev, old.ReplyReviewed = "acknowledgement", "owner_draft", "test-v0", true
		old.ReplyClassifiedOf = "synthetic-previous-content-version"
		proof := &InboxSuggestionAttempt{Version: 0, Status: suggestionUnknown, Text: g.Text, ClientID: namedClientID("same synthetic send"),
			Proposal: old, Target: suggestionReplyTarget(it)}
		keepSuggestionAttempt(it, proof)
		if _, err := h.actOnSuggestion(context.Background(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm}, true); err == nil {
			t.Error("an uncertain equivalent reply was allowed to send again")
		}
		if len(lark.got) != 0 {
			t.Fatalf("policy snapshot change defeated send-once protection: %+v", lark.got)
		}
		a := suggestionAttempt(it, 1)
		if a == nil || a.ClientID != proof.ClientID || a.Status != suggestionUnknown {
			t.Fatalf("uncertain receipt was not inherited: %+v", a)
		}
	})
	t.Run("owner edit is classified with its original conversation", func(t *testing.T) {
		h, lark, it := newDraft(t)
		it.Context = "Synthetic scheduling context with a conditional request for access: ORIGINAL-AUTHORITY-MARKER"
		model, calls := captureModel(t, `{"category":"approval_permission"}`)
		h.in.model = model
		edited := "Yes, go ahead: EDIT-ASSENT-MARKER"
		_, err := h.actOnSuggestion(context.Background(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm, Text: &edited}, true)
		if !errors.Is(err, errReplyPolicyDenied) {
			t.Fatalf("contextual owner edit error = %v", err)
		}
		call := onlyCall(t, calls())
		if !strings.Contains(call.User, "ORIGINAL-AUTHORITY-MARKER") || !strings.Contains(call.User, "EDIT-ASSENT-MARKER") {
			t.Fatalf("edit veto lost its context or replacement text: %q", call.User)
		}
		if len(lark.got) != 0 {
			t.Fatalf("contextual approval edit called sender: %+v", lark.got)
		}
	})
	t.Run("owner edit introduces approval", func(t *testing.T) {
		h, lark, it := newDraft(t)
		h.in.model = fakeModel(t, `{"category":"approval_permission"}`)
		edited := "I approve access and authorize the synthetic request"
		_, err := h.actOnSuggestion(context.Background(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm, Text: &edited}, true)
		if err == nil || !errors.Is(err, errReplyPolicyDenied) {
			t.Fatalf("edited approval confirmation error = %v", err)
		}
		if len(lark.got) != 0 {
			t.Fatalf("edited approval called sender: %+v", lark.got)
		}
	})
}

func TestInboxReplyPolicyOnDemandDraftGateLegacySchema(t *testing.T) {
	t.Run("nil policy keeps the draft transient", func(t *testing.T) {
		h, _, it := handledHub(t)
		h.in.model = fakeModel(t, "Legacy transient on-demand draft")
		if _, err := h.draftReply(t.Context(), it.ID, ""); err != nil {
			t.Fatalf("legacy on-demand draft failed: %v", err)
		}
		if currentSuggestion(it) != nil || mirroredLegacyReply(it) != nil {
			t.Fatalf("nil-policy on-demand draft changed legacy state: %+v", currentSuggestion(it))
		}
	})
	t.Run("owner-only draft is refused before the model", func(t *testing.T) {
		h, lark, it := handledHub(t)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
		it.ReplyCategory, it.ReplyMode = "approval_permission", "owner_only"
		it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
		model, calls := captureModel(t, "Synthetic owner-voice approval")
		h.in.model = model
		code, _ := inboxCall(t, h.routes(), "POST", "/api/v1/inbox/"+it.ID+"/draft", `{}`)
		if code != http.StatusForbidden {
			t.Fatalf("owner-only on-demand draft status = %d, want %d", code, http.StatusForbidden)
		}
		if got := len(calls()); got != 0 {
			t.Fatalf("owner-only on-demand draft made %d model calls", got)
		}
		if currentSuggestion(it) != nil || mirroredLegacyReply(it) != nil {
			t.Fatalf("owner-only on-demand draft left a sendable suggestion: %+v", currentSuggestion(it))
		}
		if len(lark.got) != 0 {
			t.Fatalf("owner-only on-demand draft called sender: %+v", lark.got)
		}
	})
	t.Run("policy revision changes while the model runs", func(t *testing.T) {
		h, lark, it := handledHub(t)
		it.ReplyCategory, it.ReplyMode = "ordinary_coordination", "owner_draft"
		it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
		model, calls := captureModel(t, "Synthetic draft from the revoked policy revision")
		h.in.model = model
		next := legacyReplyPolicyTestConfig()
		next.Revision = "test-v2"
		h.in.mu.Lock()
		h.in.configQueue = []assistantwire.InboxConfig{{ReplyPolicy: legacyReplyPolicyTestConfig()}, {ReplyPolicy: next}}
		h.in.mu.Unlock()
		if _, err := h.draftReply(t.Context(), it.ID, ""); !errors.Is(err, errReplyPolicyDenied) {
			t.Fatalf("draft from the old policy revision error = %v", err)
		}
		if got := len(calls()); got != 1 {
			t.Fatalf("revision-race draft made %d model calls, want 1", got)
		}
		if currentSuggestion(it) != nil || mirroredLegacyReply(it) != nil {
			t.Fatalf("revoked policy revision published a sendable draft: %+v", currentSuggestion(it))
		}
		if len(lark.got) != 0 {
			t.Fatalf("revoked policy revision called sender: %+v", lark.got)
		}
	})
	t.Run("another device publishes while the model runs", func(t *testing.T) {
		h, lark, it := handledHub(t)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
		it.ReplyCategory, it.ReplyMode = "ordinary_coordination", "owner_draft"
		it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
		var changed atomic.Bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if changed.CompareAndSwap(false, true) {
				h.inbox.mu.Lock()
				now := time.Now().UTC()
				g := &InboxSuggestion{Version: 1, Kind: SuggestReply, Text: "Newer draft from another device", At: now, SourceOf: suggestionReplyTarget(it)}
				snapshotReplyPolicy(g, it, legacyReplyPolicyTestConfig())
				it.SuggestSeq, it.Suggestion = 1, g
				h.inbox.setHandling(it, HandlingDone, "", now)
				h.inbox.mu.Unlock()
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"content": []any{map[string]any{"type": "text", "text": "Stale on-demand draft"}}})
		}))
		t.Cleanup(srv.Close)
		h.in.model = modelAt(t, srv.URL)
		if _, err := h.draftReply(t.Context(), it.ID, ""); !errors.Is(err, errSuggestionStale) {
			t.Fatalf("on-demand draft replaced a newer device draft: %v", err)
		}
		if !changed.Load() {
			t.Fatal("test model did not publish the other device's draft")
		}
		if g := currentSuggestion(it); g == nil || g.Text != "Newer draft from another device" || g.Version != 1 {
			t.Fatalf("on-demand draft overwrote the newer device state: %+v", g)
		}
		if len(lark.got) != 0 {
			t.Fatalf("concurrent on-demand draft called sender: %+v", lark.got)
		}
	})
	for _, tc := range []struct {
		name     string
		fromCard bool
	}{
		{name: "direct reply"},
		{name: "workbench card reply", fromCard: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, lark, it := handledHub(t)
			h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
			it.ReplyCategory, it.ReplyMode = "ordinary_coordination", "owner_draft"
			it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
			h.in.model = fakeModel(t, "Synthetic on-demand coordination draft")
			text, err := h.draftReply(t.Context(), it.ID, "")
			if err != nil {
				t.Fatalf("ordinary on-demand draft failed: %v", err)
			}
			g := mirroredLegacyReply(it)
			if g == nil || g.Text != text || !replyPolicySnapshotCurrent(it, legacyReplyPolicyTestConfig()) {
				t.Fatalf("on-demand draft has no current policy-bound mirror: agent=%+v suggestion=%+v", it.Agent, currentSuggestion(it))
			}
			h.in.model = fakeModel(t, `{"category":"approval_permission"}`)
			edited := "I approve the synthetic on-demand permission request"
			if tc.fromCard {
				_, err = h.replyInboxFromVersion(t.Context(), it.ID, edited, "feishu-workbench-synthetic-send", true, 1)
			} else {
				_, err = h.replyInboxChecked(t.Context(), it.ID, edited, "", false, nil, nil)
			}
			if !errors.Is(err, errReplyPolicyDenied) {
				t.Fatalf("edited on-demand draft error = %v", err)
			}
			if len(lark.got) != 0 {
				t.Fatalf("edited on-demand draft reached sender through the direct-owner path: %+v", lark.got)
			}
		})
	}
	t.Run("unmodified on-demand draft sends through its mirror", func(t *testing.T) {
		h, lark, it := handledHub(t)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
		it.ReplyCategory, it.ReplyMode = "ordinary_coordination", "owner_draft"
		it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
		h.in.model = fakeModel(t, "Synthetic confirmed on-demand draft")
		text, err := h.draftReply(t.Context(), it.ID, "")
		if err != nil {
			t.Fatalf("ordinary on-demand draft failed: %v", err)
		}
		if mirroredLegacyReply(it) == nil {
			t.Fatal("on-demand draft did not use the old-client mirror")
		}
		if _, err := h.replyInboxChecked(t.Context(), it.ID, text, "", false, nil, nil); err != nil {
			t.Fatalf("confirmed on-demand draft failed: %v", err)
		}
		if len(lark.got) != 1 || lark.got[0].Text != text {
			t.Fatalf("confirmed on-demand sender calls = %+v", lark.got)
		}
	})
}

func TestInboxReplyPolicyWorkbenchDraftRejectedBeforeSendLegacySchema(t *testing.T) {
	h, lark, it := handledHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
	it.ReplyCategory, it.ReplyMode = "ordinary_coordination", "owner_draft"
	it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"Workbench draft rejected before its send"}`); err != nil {
		t.Fatal(err)
	}
	text := currentSuggestion(it).Text
	if _, err := h.actOnSuggestion(t.Context(), it.ID, suggestionReq{Version: 1, Action: suggestReject}, true); err != nil {
		t.Fatal(err)
	}
	if mirroredLegacyReply(it) != nil {
		t.Fatal("test rejection left a mirror")
	}
	if _, err := h.replyInboxFrom(t.Context(), it.ID, text, "feishu-workbench-synthetic-after-reject", true); !errors.Is(err, errSuggestionStale) {
		t.Fatalf("retained workbench text sent after its suggestion was rejected: %v", err)
	}
	if len(lark.got) != 0 {
		t.Fatalf("rejected workbench draft reached sender before its send began: %+v", lark.got)
	}
}

func TestInboxReplyPolicyWorkbenchTransientDraftStaysLegacyLegacySchema(t *testing.T) {
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

func TestInboxReplyPolicyDirectOwnerCardStaysLegacyLegacySchema(t *testing.T) {
	h, lark, it := handledHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
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

func TestInboxReplyPolicyMirroredSendUsesDurableAttemptLegacySchema(t *testing.T) {
	for _, fromCard := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "workbench"}[fromCard], func(t *testing.T) {
			c := &recoveryReplier{err: rpc.ErrDropped}
			h, _, it := handledHub(t)
			h.in.mu.Lock()
			h.in.conns[0] = c
			h.in.mu.Unlock()
			h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
			it.ReplyCategory, it.ReplyMode = "ordinary_coordination", "owner_draft"
			it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
			if _, err := suggestTool(t, h, `{"kind":"reply","text":"Mirrored send with a lost acknowledgement"}`); err != nil {
				t.Fatal(err)
			}
			text := currentSuggestion(it).Text
			firstID := recoveryFirstID
			var firstErr error
			if fromCard {
				firstID = "feishu-workbench-" + recoveryFirstID
				_, firstErr = h.replyInboxFromVersion(t.Context(), it.ID, text, firstID, true, 1)
			} else {
				_, firstErr = h.replyInboxChecked(t.Context(), it.ID, text, firstID, false, nil, nil)
			}
			if !replyInDoubt(firstErr) {
				t.Fatalf("first mirrored send error = %v, want an uncertain outcome", firstErr)
			}
			// Restart from the actual persisted row, then retry through the same old-client route under a fresh ID.
			// A live-item-only check would miss an in-memory reservation, and actOnSuggestion would still see the mirror.
			restarted := New(Options{Hub: h.f, Store: func() Store { return h.st }, Logf: testLogf(t)})
			if err := restarted.inboxReady(t.Context()); err != nil {
				t.Fatal(err)
			}
			restarted.inbox.mu.Lock()
			loaded := restarted.inbox.items[it.ID]
			a := suggestionAttempt(loaded, 1)
			restarted.inbox.mu.Unlock()
			wantAttemptID := firstID
			if fromCard {
				// Workbench's routing prefix is not a client UUID, so the durable attempt uses the existing
				// deterministic suggestion identity rather than persisting that prefix as the caller ID.
				wantAttemptID = namedClientID("suggestion\n" + it.ID + "\n1")
			}
			if a == nil || a.Status != suggestionUnknown || a.ClientID != wantAttemptID {
				t.Fatalf("mirrored send has no persisted uncertain reservation after reload: %+v", a)
			}
			c.answer(nil)
			var secondErr error
			if fromCard {
				_, secondErr = restarted.replyInboxFromVersion(t.Context(), it.ID, text, "feishu-workbench-"+recoveryFreshID, true, 1)
			} else {
				_, secondErr = restarted.replyInboxChecked(t.Context(), it.ID, text, recoveryFreshID, false, nil, nil)
			}
			if got := len(c.calls()); got != 1 {
				t.Fatalf("uncertain mirrored /reply was repeated under a fresh ID after reload: calls=%d first=%v second=%v", got, firstErr, secondErr)
			}
			if !replyInDoubt(secondErr) {
				t.Fatalf("second old-client /reply error = %v, want the persisted uncertain outcome", secondErr)
			}
		})
	}
}

func TestInboxReplyPolicyMirroredSendChecksRecipientBindingLegacySchema(t *testing.T) {
	for _, fromCard := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "workbench"}[fromCard], func(t *testing.T) {
			h, lark, it := handledHub(t)
			h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
			it.ReplyCategory, it.ReplyMode = "ordinary_coordination", "owner_draft"
			it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
			if _, err := suggestTool(t, h, `{"kind":"reply","text":"Draft bound to the original recipient"}`); err != nil {
				t.Fatal(err)
			}
			text := currentSuggestion(it).Text
			h.inbox.mu.Lock()
			it.Lark.ReplyTo = "synthetic-changed-recipient"
			h.inbox.mu.Unlock()
			var err error
			if fromCard {
				_, err = h.replyInboxFromVersion(t.Context(), it.ID, text, "feishu-workbench-synthetic-target-card", true, 1)
			} else {
				_, err = h.replyInboxChecked(t.Context(), it.ID, text, "synthetic-target-direct", false, nil, nil)
			}
			if !errors.Is(err, errSuggestionStale) {
				t.Fatalf("mirror authorized a changed recipient: %v", err)
			}
			if len(lark.got) != 0 {
				t.Fatalf("changed recipient received the approved text: %+v", lark.got)
			}
		})
	}
}

func TestInboxReplyPolicyInstructedDraftClassifiesGeneratedTextLegacySchema(t *testing.T) {
	for _, tc := range []struct {
		name     string
		category string
		denied   bool
	}{
		{name: "restricted instruction", category: "approval_permission", denied: true},
		{name: "ordinary instruction", category: "ordinary_coordination"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, lark, it := handledHub(t)
			h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
			it.Context = "Synthetic original conversation marker"
			it.ReplyCategory, it.ReplyMode = "ordinary_coordination", "owner_draft"
			it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
			generated := "Synthetic generated draft marker"
			instruction := "Synthetic owner instruction marker"
			var modelCalls []capturedCall
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					System   string `json:"system"`
					Messages []struct {
						Content string `json:"content"`
					} `json:"messages"`
				}
				_ = json.NewDecoder(r.Body).Decode(&in)
				call := capturedCall{System: in.System}
				if len(in.Messages) > 0 {
					call.User = in.Messages[0].Content
				}
				h.in.mu.Lock()
				modelCalls = append(modelCalls, call)
				n := len(modelCalls)
				h.in.mu.Unlock()
				reply := generated
				if n > 1 {
					reply = `{"category":"` + tc.category + `"}`
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"content": []any{map[string]any{"type": "text", "text": reply}}})
			}))
			t.Cleanup(srv.Close)
			h.in.model = modelAt(t, srv.URL)
			text, err := h.draftReply(t.Context(), it.ID, instruction)
			if tc.denied {
				if !errors.Is(err, errReplyPolicyDenied) || text != "" {
					t.Fatalf("restricted instructed draft was published: text=%q err=%v", text, err)
				}
				if currentSuggestion(it) != nil || mirroredLegacyReply(it) != nil {
					t.Fatalf("restricted instructed draft left a sendable mirror: %+v", currentSuggestion(it))
				}
			} else {
				if err != nil || mirroredLegacyReply(it) == nil {
					t.Fatalf("ordinary instructed draft was not published safely: text=%q err=%v", text, err)
				}
			}
			h.in.mu.Lock()
			calls := append([]capturedCall(nil), modelCalls...)
			h.in.mu.Unlock()
			if len(calls) != 2 {
				t.Fatalf("instructed draft made %d model calls, want generation plus generated-text classification", len(calls))
			}
			if calls[1].System != prompts.RenderJSONOnly(prompts.RenderInboxReplyPolicy()) {
				t.Fatalf("second call did not use the reply-policy classifier: %+v", calls[1])
			}
			for _, marker := range []string{generated, instruction, "Synthetic original conversation marker"} {
				if !strings.Contains(calls[1].User, marker) {
					t.Fatalf("classifier input lacks %q: %+v", marker, calls[1])
				}
			}
			if len(lark.got) != 0 {
				t.Fatalf("instructed draft unexpectedly called sender: %+v", lark.got)
			}
		})
	}
}

func TestInboxReplyPolicyDraftDoesNotReturnUnpersistedTextLegacySchema(t *testing.T) {
	h, _, it := handledHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
	it.ReplyCategory, it.ReplyMode = "ordinary_coordination", "owner_draft"
	it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
	h.in.model = fakeModel(t, "Draft whose publication write fails")
	st := &replyPolicyDraftFailStore{inboxMemStore: h.st}
	st.before = func() {
		// Another device rejects the mirror while this write is outstanding. Its newer state prevents rollback, so
		// w.undone stays false even though this request's exact suggestion was never persisted.
		h.inbox.mu.Lock()
		it.Suggestion = nil
		it.Agent.State, it.Agent.Draft = AgentSkipped, ""
		h.inbox.mu.Unlock()
	}
	h.setStore(func() Store { return st })
	text, err := h.draftReply(t.Context(), it.ID, "")
	if err == nil || text != "" {
		t.Fatalf("failed publication returned sendable obsolete text: text=%q err=%v", text, err)
	}
}

func TestInboxReplyPolicyDraftFailedWriteWithoutRollbackDoesNotReturnTextLegacySchema(t *testing.T) {
	h, _, it := handledHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
	it.ReplyCategory, it.ReplyMode = "ordinary_coordination", "owner_draft"
	it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
	h.in.model = fakeModel(t, "Draft whose only publication write fails")
	st := &replyPolicyDraftFailStore{inboxMemStore: h.st}
	st.before = func() {
		// A concurrent same-valued update advances the mutation stamp without changing the stored item. It prevents
		// rollback while the mirror and full-state freshness checks still match, so only the failed-write result
		// proves that this exact suggestion was not persisted.
		h.inbox.mu.Lock()
		h.inbox.touched(it.ID)
		h.inbox.mu.Unlock()
	}
	h.setStore(func() Store { return st })
	text, err := h.draftReply(t.Context(), it.ID, "")
	if err == nil || text != "" {
		t.Fatalf("unpersisted intact mirror returned a successful draft: text=%q err=%v", text, err)
	}
	if !errors.Is(err, errInboxNotPersisted) {
		t.Fatalf("unpersisted intact mirror returned the wrong persistence error: %v", err)
	}
}

func TestInboxReplyPolicyDraftDoesNotReturnReplacedTextLegacySchema(t *testing.T) {
	h, _, it := handledHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
	it.ReplyCategory, it.ReplyMode = "ordinary_coordination", "owner_draft"
	it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
	h.in.model = fakeModel(t, "Draft replaced just after its successful write")
	st := &replyPolicyDraftReplacementStore{inboxMemStore: h.st}
	st.before = func() {
		h.inbox.mu.Lock()
		it.Suggestion = nil
		it.Agent.State, it.Agent.Draft = AgentSkipped, ""
		h.inbox.mu.Unlock()
	}
	h.setStore(func() Store { return st })
	text, err := h.draftReply(t.Context(), it.ID, "")
	if !errors.Is(err, errSuggestionStale) || text != "" {
		t.Fatalf("successful write returned text whose mirror was replaced: text=%q err=%v", text, err)
	}
}

func TestInboxReplyPolicyMergedContentVersionIsUnambiguousLegacySchema(t *testing.T) {
	// The old merged encoding appended source/truncation fields directly after Context. This ordinary Context is exactly
	// the byte sequence produced for the truncated item below, so the two different authorization states collided.
	suffix := "\x00sources\x001\x001\x00a\x00truncated\x00true"
	ordinary := &InboxItem{Source: "a", Last: "1", Context: "x" + suffix}
	truncated := &InboxItem{Source: "a", Last: "1", Context: "x", ReplyContextTruncated: true}
	if replyPolicyContentVersion(ordinary) == replyPolicyContentVersion(truncated) {
		t.Fatal("ordinary and truncated content share an authorization version through ambiguous concatenation")
	}
}

func TestInboxReplyPolicyMergedContextInvalidatesDraftLegacySchema(t *testing.T) {
	h, lark, it := handledHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
	// A full survivor used to clip the entire appended member away, leaving both the text and its authorization hash unchanged.
	it.Context = strings.Repeat("s", mergeCtxMax)
	it.ReplyCategory, it.ReplyMode, it.ReplyClassifiedOf, it.ReplyPolicyRev = "ordinary_coordination", "owner_draft", replyPolicyContentVersion(it), "test-v1"
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"Synthetic scheduling assent"}`); err != nil {
		t.Fatal(err)
	}
	oldLast := it.Last
	member := actionItem("lark:restricted-member", it.UpdatedAt.Add(-time.Second))
	member.Source = "lark"
	member.Context = "Synthetic request to approve access and authorize a restricted permission change"
	h.inbox.mu.Lock()
	h.inbox.items[member.ID] = member
	survivor, _ := h.inbox.merge([]*InboxItem{it, member}, time.Now())
	h.inbox.mu.Unlock()
	if survivor != it || it.Last != oldLast {
		t.Fatalf("test merge unexpectedly changed the survivor or Last: %+v", it)
	}
	if !it.ReplyContextTruncated {
		t.Fatal("merge discarded the clipped member's truncation boundary")
	}
	if _, err := h.actOnSuggestion(context.Background(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm}, true); !errors.Is(err, errReplyPolicyDenied) {
		t.Fatalf("clipped restricted member retained its old authorization: %v", err)
	}
	if len(lark.got) != 0 {
		t.Fatalf("merged restricted context called sender: %+v", lark.got)
	}
	model, calls := captureModel(t, `{"category":"ordinary_coordination"}`)
	h.in.model = model
	cfg := legacyReplyPolicyTestConfig()
	for _, revision := range []string{"test-v1", "test-v2"} {
		cfg.Revision = revision
		h.classifyInboxReplyPolicies(t.Context(), cfg)
		if it.ReplyCategory != "unknown" || it.ReplyMode != "owner_only" {
			t.Fatalf("clipped merge was reauthorized under %s: category=%q mode=%q", revision, it.ReplyCategory, it.ReplyMode)
		}
	}
	if got := len(calls()); got != 0 {
		t.Fatalf("clipped merge made %d classification calls instead of failing closed", got)
	}
}

func TestInboxReplyPolicyMergePropagatesTruncationLegacySchema(t *testing.T) {
	t.Run("member with identical context", func(t *testing.T) {
		h, _, it := handledHub(t)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
		member := actionItem("lark:already-truncated-member", it.UpdatedAt.Add(-time.Second))
		member.Source = "lark"
		member.Context = it.Context
		member.ReplyContextTruncated = true
		h.inbox.mu.Lock()
		h.inbox.items[member.ID] = member
		h.inbox.merge([]*InboxItem{it, member}, time.Now())
		h.inbox.mu.Unlock()
		if !it.ReplyContextTruncated {
			t.Error("merging an already-truncated member with identical context dropped its truncation boundary")
		}
		model, calls := captureModel(t, `{"category":"ordinary_coordination"}`)
		h.in.model = model
		h.classifyInboxReplyPolicies(t.Context(), legacyReplyPolicyTestConfig())
		if it.ReplyCategory != "unknown" || it.ReplyMode != "owner_only" || len(calls()) != 0 {
			t.Fatalf("incomplete member text regained owner-draft authorization: category=%q mode=%q calls=%d", it.ReplyCategory, it.ReplyMode, len(calls()))
		}
	})
	t.Run("pre-upgrade clipped merged row", func(t *testing.T) {
		h, _, it := handledHub(t)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
		it.Context = strings.Repeat("s", mergeCtxMax)
		it.ReplyContextTruncated = false // a row clipped before this persistent field existed
		member := actionItem("lark:pre-upgrade-clipped-member", it.UpdatedAt.Add(-time.Second))
		member.Source = "lark"
		member.MergedInto = it.ID
		it.MergedFrom = []InboxMergeRef{{ID: member.ID, Source: member.Source}}
		h.inbox.mu.Lock()
		h.inbox.items[member.ID] = member
		h.inbox.mu.Unlock()
		model, calls := captureModel(t, `{"category":"ordinary_coordination"}`)
		h.in.model = model
		h.classifyInboxReplyPolicies(t.Context(), legacyReplyPolicyTestConfig())
		if it.ReplyCategory != "unknown" || it.ReplyMode != "owner_only" || len(calls()) != 0 {
			t.Fatalf("pre-upgrade clipped row was authorized from its retained prefix: category=%q mode=%q calls=%d", it.ReplyCategory, it.ReplyMode, len(calls()))
		}
		h.inbox.mu.Lock()
		h.inbox.unmerge(it, time.Now())
		h.inbox.mu.Unlock()
		if !it.ReplyContextTruncated {
			t.Fatal("undo discarded the inferred pre-upgrade truncation boundary")
		}
	})
}

func TestInboxReplyPolicyCrossSourceMergeKeepsMemberScopeLegacySchema(t *testing.T) {
	newMerged := func(t *testing.T) (*inboxHub, *extensionLark, *InboxItem) {
		h, lark, it := handledHub(t)
		h.setConfig(func(c *assistantwire.InboxConfig) {
			c.ReplyPolicy = legacyReplyPolicyTestConfig()
			c.ReplyPolicy.Sources = []string{"mail"}
		})
		// The survivor itself is outside the configured source list, so this suggestion has no policy snapshot.
		if _, err := suggestTool(t, h, `{"kind":"reply","text":"Synthetic cross-source owner draft"}`); err != nil {
			t.Fatal(err)
		}
		member := actionItem("mail:restricted-member", it.UpdatedAt.Add(-time.Second))
		member.Source = "mail"
		member.Context = "Synthetic request to approve access and authorize a restricted permission change"
		h.inbox.mu.Lock()
		h.inbox.items[member.ID] = member
		survivor, _ := h.inbox.merge([]*InboxItem{it, member}, time.Now())
		h.inbox.mu.Unlock()
		if survivor != it || survivor.Source != "lark" || len(survivor.MergedFrom) != 1 || survivor.MergedFrom[0].Source != "mail" {
			t.Fatalf("test did not build a cross-source merge: %+v", survivor)
		}
		return h, lark, it
	}
	t.Run("existing draft cannot be confirmed", func(t *testing.T) {
		h, lark, it := newMerged(t)
		if _, err := h.actOnSuggestion(t.Context(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm}, true); !errors.Is(err, errReplyPolicyDenied) {
			t.Fatalf("cross-source merged draft confirmation error = %v", err)
		}
		if len(lark.got) != 0 {
			t.Fatalf("restricted member escaped its source policy through a merge: %+v", lark.got)
		}
	})
	t.Run("new draft cannot be published without classifying the member", func(t *testing.T) {
		h, lark, _ := newMerged(t)
		if _, err := suggestTool(t, h, `{"kind":"reply","text":"Replacement cross-source owner draft"}`); !errors.Is(err, errReplyPolicyDenied) {
			t.Fatalf("cross-source replacement suggestion error = %v", err)
		}
		if len(lark.got) != 0 {
			t.Fatalf("cross-source replacement suggestion called sender: %+v", lark.got)
		}
	})
	t.Run("undo retains the source boundary for retained member text", func(t *testing.T) {
		h, lark, it := newMerged(t)
		h.inbox.mu.Lock()
		h.inbox.unmerge(it, time.Now())
		h.inbox.mu.Unlock()
		if !strings.Contains(it.Context, "restricted permission change") {
			t.Fatal("test undo unexpectedly removed the member's retained context")
		}
		if _, err := suggestTool(t, h, `{"kind":"reply","text":"Post-undo cross-source owner draft"}`); !errors.Is(err, errReplyPolicyDenied) {
			t.Fatalf("post-undo retained member text escaped its source policy: %v", err)
		}
		if len(lark.got) != 0 {
			t.Fatalf("post-undo cross-source suggestion called sender: %+v", lark.got)
		}
	})
	t.Run("undo preserves a pre-upgrade member source", func(t *testing.T) {
		h, lark, it := handledHub(t)
		h.setConfig(func(c *assistantwire.InboxConfig) {
			c.ReplyPolicy = legacyReplyPolicyTestConfig()
			c.ReplyPolicy.Sources = []string{"mail"}
		})
		member := actionItem("mail:pre-upgrade-member", it.UpdatedAt.Add(-time.Second))
		member.Source = "mail"
		member.Context = "Synthetic restricted text retained by a merge made before upgrade"
		member.MergedInto = it.ID
		it.MergedFrom = []InboxMergeRef{{ID: member.ID, Source: member.Source}}
		it.ReplyContentSources = nil // rows written by the old version have no durable source union yet
		it.Context += "\n\n" + member.Context
		h.inbox.mu.Lock()
		h.inbox.items[member.ID] = member
		h.inbox.unmerge(it, time.Now())
		h.inbox.mu.Unlock()
		if _, err := suggestTool(t, h, `{"kind":"reply","text":"Post-upgrade undo owner draft"}`); !errors.Is(err, errReplyPolicyDenied) {
			t.Fatalf("pre-upgrade member text lost its policy scope on undo: %v", err)
		}
		if len(lark.got) != 0 {
			t.Fatalf("pre-upgrade undo suggestion called sender: %+v", lark.got)
		}
	})
}

func TestInboxReplyPolicyClassificationRejectsContextChangeDuringModelCallLegacySchema(t *testing.T) {
	h := agentHub(t)
	it := actionItem("lark:classification-context-race", time.Now())
	it.Source = "lark"
	it.Agent = &InboxAgent{State: AgentQueued}
	h.inbox.items[it.ID] = it
	var changed atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if changed.CompareAndSwap(false, true) {
			h.inbox.mu.Lock()
			it.Context += "\n\nSynthetic restricted context arrived during classification"
			h.inbox.mu.Unlock()
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"content": []any{map[string]any{"type": "text", "text": `{"category":"ordinary_coordination"}`}}})
	}))
	t.Cleanup(srv.Close)
	h.in.model = modelAt(t, srv.URL)
	cfg := legacyReplyPolicyTestConfig()
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
	h.classifyInboxReplyPolicies(context.Background(), cfg)
	if !changed.Load() {
		t.Fatal("test model did not change the context")
	}
	if it.ReplyClassifiedOf != "" || it.ReplyPolicyRev != "" || it.ReplyCategory != "" || it.ReplyMode != "" {
		t.Fatalf("classification from the old context was committed after a merge: %+v", it)
	}
	if replyPolicyClassificationCurrent(it, cfg) {
		t.Fatal("changed context was treated as currently classified")
	}
}

func TestInboxReplyPolicyLegacyMirroredDraftGateLegacySchema(t *testing.T) {
	newDraft := func(t *testing.T, enabled bool) (*inboxHub, *extensionLark, *InboxItem) {
		h, lark, it := handledHub(t)
		if enabled {
			h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
			it.ReplyCategory, it.ReplyMode, it.ReplyClassifiedOf, it.ReplyPolicyRev = "ordinary_coordination", "owner_draft", replyPolicyContentVersion(it), "test-v1"
		}
		if _, err := suggestTool(t, h, `{"kind":"reply","text":"Mirrored synthetic AI draft"}`); err != nil {
			t.Fatal(err)
		}
		if mirroredLegacyReply(it) == nil {
			t.Fatalf("test setup did not expose the old-client mirror: agent=%+v suggestion=%+v", it.Agent, currentSuggestion(it))
		}
		return h, lark, it
	}
	t.Run("pre-policy mirrored draft cannot send after enablement", func(t *testing.T) {
		h, lark, it := newDraft(t, false)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
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
			it.ReplyCategory, it.ReplyMode = "approval_permission", "owner_only"
		}},
		{name: "revision revoked", mutate: func(h *inboxHub, _ *InboxItem) {
			h.setConfig(func(c *assistantwire.InboxConfig) {
				c.ReplyPolicy = legacyReplyPolicyTestConfig()
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
		if _, err := h.replyInboxChecked(context.Background(), it.ID, "I approve the synthetic restricted request", "", false, nil, nil); !errors.Is(err, errReplyPolicyDenied) {
			t.Fatalf("old-client edited approval error = %v", err)
		}
		if len(lark.got) != 0 {
			t.Fatalf("old-client edited approval called sender: %+v", lark.got)
		}
	})
	t.Run("authorized owner draft sends", func(t *testing.T) {
		h, lark, it := newDraft(t, true)
		if _, err := h.replyInboxChecked(context.Background(), it.ID, currentSuggestion(it).Text, "", false, nil, nil); err != nil {
			t.Fatalf("authorized old-client owner draft failed: %v", err)
		}
		if len(lark.got) != 1 {
			t.Fatalf("authorized old-client sender calls = %+v", lark.got)
		}
	})
	t.Run("direct owner-authored reply stays legacy", func(t *testing.T) {
		h, lark, it := handledHub(t)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
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

func TestInboxReplyPolicyLegacyDraftRejectedWhileSendWaitsLegacySchema(t *testing.T) {
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
			if _, err := suggestTool(t, h, `{"kind":"reply","text":"Mirrored draft rejected on another device"}`); err != nil {
				t.Fatal(err)
			}
			text := currentSuggestion(it).Text
			entered := make(chan struct{})
			once := &inboxReply{beforeLock: func() { close(entered) }}
			once.mu.Lock()
			unlocked := false
			defer func() {
				if !unlocked {
					once.mu.Unlock()
				}
			}()
			h.inbox.replies.Store(it.ID, once)
			result := make(chan error, 1)
			go func() {
				_, err := h.replyInboxChecked(t.Context(), it.ID, text, "", false, nil, nil)
				result <- err
			}()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("legacy send did not reach the per-item send lock")
			}
			// Rejection removes the mirror while the old-client request is queued behind the send mutex.
			if _, err := h.actOnSuggestion(t.Context(), it.ID, suggestionReq{Version: 1, Action: suggestReject}, true); err != nil {
				t.Fatalf("rejecting the mirrored draft failed: %v", err)
			}
			once.mu.Unlock()
			unlocked = true
			select {
			case err := <-result:
				if !errors.Is(err, errSuggestionStale) {
					t.Fatalf("queued legacy request error = %v, want stale after its mirror was rejected", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("queued legacy send did not finish")
			}
			if len(lark.got) != 0 {
				t.Fatalf("rejected legacy draft reached sender after the mirror disappeared: %+v", lark.got)
			}
		})
	}
}

func TestInboxReplyPolicyDeniedEditSurvivesConcurrentDisableLegacySchema(t *testing.T) {
	h, lark, it := handledHub(t)
	h.in.model = fakeModel(t, `{"category":"approval_permission"}`)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
	it.ReplyCategory, it.ReplyMode, it.ReplyClassifiedOf, it.ReplyPolicyRev = "ordinary_coordination", "owner_draft", replyPolicyContentVersion(it), "test-v1"
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"Original synthetic reply"}`); err != nil {
		t.Fatal(err)
	}
	// Prime the display cache, then make the veto's fresh read enabled and the second fresh read disabled. Calling the
	// complete action proves that the post-veto read cannot discard an established denial and reach the real send path.
	h.inboxConfig(t.Context())
	h.in.mu.Lock()
	h.in.configQueue = []assistantwire.InboxConfig{{ReplyPolicy: legacyReplyPolicyTestConfig()}, {}}
	h.in.mu.Unlock()
	edited := "I approve the synthetic permission request"
	before := append([]byte(nil), itemState(it)...)
	_, err := h.actOnSuggestion(t.Context(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm, Text: &edited}, true)
	if !errors.Is(err, errReplyPolicyDenied) {
		t.Fatalf("concurrent disable cleared an established edit veto: %v", err)
	}
	if len(lark.got) != 0 {
		t.Fatalf("rejected owner edit reached the sender through confirmation: %+v", lark.got)
	}
	if after := itemState(it); !bytes.Equal(after, before) {
		t.Fatalf("denied edit consumed or reserved suggestion state before the final policy read:\nbefore=%s\nafter=%s", before, after)
	}
	restarted := New(Options{Hub: h.f, Store: func() Store { return h.st }, Logf: testLogf(t)})
	if err := restarted.inboxReady(t.Context()); err != nil {
		t.Fatal(err)
	}
	restarted.inbox.mu.Lock()
	loaded := restarted.inbox.items[it.ID]
	persisted := append([]byte(nil), itemState(loaded)...)
	attempt := suggestionAttempt(loaded, 1)
	restarted.inbox.mu.Unlock()
	if !bytes.Equal(persisted, before) || attempt != nil {
		t.Fatalf("denied edit left persisted reservation/state despite live rollback:\nbefore=%s\nafter=%s\nattempt=%+v", before, persisted, attempt)
	}
}

func TestInboxReplyPolicyConfigFailureBlocksOtherSuggestions(t *testing.T) {
	for _, kind := range []string{SuggestConclusion, SuggestTodo} {
		t.Run(kind, func(t *testing.T) {
			h, _, it := handledHub(t)
			it.Suggestion = &InboxSuggestion{Version: 1, Kind: kind, Text: "Synthetic non-reply action", At: time.Now()}
			// Cache a visibility boundary that does not exclude this item. The outage itself, rather than an already-known
			// exclusion, must prevent the local operation; the no-visibility compatibility case is covered upstream.
			h.setConfig(func(c *assistantwire.InboxConfig) {
				c.Visibility = &assistantwire.InboxVisibilityConfig{Sources: []string{"lark"}, Chats: []string{"another-private"}}
			})
			h.inboxConfig(t.Context())
			h.in.mu.Lock()
			h.in.configErr = errors.New("synthetic visibility configuration outage")
			h.in.mu.Unlock()
			before := append([]byte(nil), itemState(it)...)
			_, err := h.actOnSuggestion(t.Context(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm}, true)
			if !errors.Is(err, errCannotReply) {
				t.Fatalf("visibility configuration outage permitted %s confirmation: %v", kind, err)
			}
			if after := itemState(it); !bytes.Equal(after, before) {
				t.Fatalf("configuration outage consumed %s suggestion state", kind)
			}
		})
	}
}

func TestInboxReplyPolicyClassificationFailClosedLegacySchema(t *testing.T) {
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
			cfg := legacyReplyPolicyTestConfig()
			cfg.RequireIndependentReview = tc.requireReview
			h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
			it := actionItem("lark:classification", time.Now())
			it.Source = "lark"
			it.Agent = &InboxAgent{State: AgentQueued}
			if tc.emptyContext {
				it.Context = ""
			}
			h.inbox.items[it.ID] = it
			ctx := context.Background()
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
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			h.classifyInboxReplyPolicies(ctx, cfg)
			if it.ReplyCategory != "unknown" || it.ReplyMode != "owner_only" || it.ReplyClassifiedOf != replyPolicyContentVersion(it) || it.ReplyPolicyRev != "test-v1" || it.ReplyReviewed {
				t.Fatalf("classification failure did not fail closed: %+v", it)
			}
			if tc.requireReview && reviewCalls.Load() != 2 {
				t.Fatalf("independent review failure made %d model calls, want successful primary plus failed reviewer", reviewCalls.Load())
			}
			before := len(conversationStarts(h.f))
			h.startInboxAgents(context.Background(), InboxAgentConfig{Enabled: true})
			checkPolicyReleased(t, it, h.inbox.userLang())
			if got := len(conversationStarts(h.f)); got != before {
				t.Fatalf("failed classification started %d handling conversations", got-before)
			}
		})
	}
}

func TestInboxReplyPolicyReviewRequirementChangeReclassifiesLegacySchema(t *testing.T) {
	h := agentHub(t)
	model, calls := captureModel(t, `{"category":"ordinary_coordination"}`)
	h.in.model = model
	cfg := legacyReplyPolicyTestConfig()
	cfg.RequireIndependentReview = true
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
	it := actionItem("lark:review-required", time.Now())
	it.Source = "lark"
	it.ReplyCategory, it.ReplyMode = "ordinary_coordination", "owner_draft"
	it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
	h.inbox.items[it.ID] = it
	h.classifyInboxReplyPolicies(context.Background(), cfg)
	if !it.ReplyReviewed || !replyPolicyClassificationCurrent(it, cfg) {
		t.Fatalf("enabling independent review left an unreviewed owner draft current: %+v", it)
	}
	got := calls()
	if len(got) != 2 || got[0].System != prompts.RenderJSONOnly(prompts.RenderInboxReplyPolicy()) || got[1].System != prompts.RenderJSONOnly(prompts.RenderInboxReplyPolicyReview()) {
		t.Fatalf("review requirement did not run primary plus independent review: %+v", got)
	}
}

func TestInboxReplyPolicyIndependentReviewDisagreementFailsClosedLegacySchema(t *testing.T) {
	h := agentHub(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		category := "ordinary_coordination"
		if calls.Add(1) == 2 {
			category = "approval_permission"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"content": []any{map[string]any{"type": "text", "text": `{"category":"` + category + `"}`}}})
	}))
	t.Cleanup(srv.Close)
	h.in.model = modelAt(t, srv.URL)
	cfg := legacyReplyPolicyTestConfig()
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
	if it.ReplyCategory != "unknown" || it.ReplyMode != "owner_only" || it.ReplyReviewed {
		t.Fatalf("review disagreement did not fail closed: %+v", it)
	}
	before := len(conversationStarts(h.f))
	h.startInboxAgents(context.Background(), InboxAgentConfig{Enabled: true})
	checkPolicyReleased(t, it, h.inbox.userLang())
	if got := len(conversationStarts(h.f)); got != before {
		t.Fatalf("review disagreement started %d handling conversations", got-before)
	}
}

// replyPolicyClassifierModel serves dynamic JSON responses to inbox.triage classifier calls.
// Zero sleeps: timeouts block on r.Context().Done() so tests remain deterministic and fast.
func replyPolicyClassifierModel(t *testing.T, fn func(call int, system, user string, r *http.Request) (int, string)) (*ai.Client, func() int) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			http.NotFound(w, r)
			return
		}
		var in struct {
			System   string `json:"system"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		user := ""
		if len(in.Messages) > 0 {
			user = in.Messages[0].Content
		}
		n := int(calls.Add(1))
		status, reply := fn(n, in.System, user, r)
		if status == 0 {
			status = http.StatusOK
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(reply))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []any{map[string]any{"type": "text", "text": reply}},
		})
	}))
	t.Cleanup(srv.Close)
	return modelAt(t, srv.URL), func() int { return int(calls.Load()) }
}

func replyPolicyTestItem(id, last, text string) *InboxItem {
	now := time.Now().UTC()
	return &InboxItem{
		ID:        id,
		Source:    "lark",
		Kind:      KindDM,
		Account:   "feishu",
		Title:     "Example contact",
		Sender:    "Example contact",
		Preview:   text,
		Last:      last,
		Count:     1,
		StartedAt: now.Add(-time.Minute),
		UpdatedAt: now,
		Tier:      TierAction,
		TriagedAt: last,
		Summary:   "Example request",
		Context:   "Example contact: " + text,
		Lark:      &InboxLark{AccountID: 1, FeedID: "feed-" + id, StartPosition: last},
	}
}

func newReplyPolicyHub(t *testing.T, cfg assistantwire.InboxReplyPolicyConfig, model *ai.Client) (*inboxHub, *extensionLark) {
	t.Helper()
	h := agentHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) {
		c.Agent.Enabled = true
		c.ReplyPolicy = &cfg
	})
	lark := &extensionLark{}
	h.addConnector(lark, true)
	if model != nil {
		h.in.models[purposeTriage] = model
	}
	return h, lark
}

func seedHandledReplyPolicyItem(t *testing.T, h *inboxHub, it *InboxItem) {
	t.Helper()
	now := time.Now().UTC()
	h.inbox.mu.Lock()
	h.inbox.items[it.ID] = it
	it.Agent = &InboxAgent{
		State:          AgentRunning,
		MachineID:      "server",
		SessionID:      "s1",
		Of:             it.Last,
		StartedAt:      &now,
		ProcessingTurn: "turn-1",
	}
	h.inbox.setHandling(it, HandlingLooking, "", now)
	h.inbox.mu.Unlock()
	h.f.setSessions("server", proto.Session{SessionID: "s1", Status: proto.StatusIdle})
	h.saveInbox(context.Background(), []*InboxItem{it})
}

func suggestReplyWithTurn(t *testing.T, h *inboxHub, turn, text string) (string, error) {
	t.Helper()
	args, _ := json.Marshal(map[string]any{
		"kind":            SuggestReply,
		"text":            text,
		"processing_turn": turn,
	})
	return h.toolInboxSuggest(context.Background(), "s1", args)
}

func TestInboxReplyPolicyDisabledAndEmptyPreservesBehavior(t *testing.T) {
	cases := []struct {
		name string
		cfg  assistantwire.InboxReplyPolicyConfig
	}{
		{name: "empty", cfg: assistantwire.InboxReplyPolicyConfig{}},
		{name: "disabled_with_categories", cfg: assistantwire.InboxReplyPolicyConfig{
			Enabled:    false,
			Revision:   "rev-off",
			Categories: map[string]string{ReplyCategoryApprovalCommitment: assistantwire.ReplyRouteOwnerOnly},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model, callCount := replyPolicyClassifierModel(t, func(_ int, _, _ string, _ *http.Request) (int, string) {
				t.Error("classifier must not be called when reply_policy is disabled or empty")
				return http.StatusInternalServerError, "unexpected call"
			})
			h, lark := newReplyPolicyHub(t, tc.cfg, model)
			it := replyPolicyTestItem("lark:off:1", "10", "Can you share the status update?")
			h.inbox.mu.Lock()
			h.inbox.items[it.ID] = it
			h.inbox.mu.Unlock()

			// 1. Queue path works unchanged without calling classifier.
			h.handPass(context.Background())
			if h.f.made != 1 {
				t.Fatalf("expected 1 started conversation, got %d", h.f.made)
			}
			h.inbox.mu.Lock()
			turn := it.Agent.ProcessingTurn
			sessionID := it.Agent.SessionID
			h.inbox.mu.Unlock()
			if sessionID == "" {
				t.Fatal("expected handling conversation to start when policy is disabled")
			}

			// 2. Suggestion path works unchanged without policy snapshot.
			args, _ := json.Marshal(map[string]any{
				"kind":            SuggestReply,
				"text":            "On track for this afternoon.",
				"processing_turn": turn,
			})
			if _, err := h.toolInboxSuggest(context.Background(), sessionID, args); err != nil {
				t.Fatalf("inbox_suggest failed while policy disabled: %v", err)
			}
			h.inbox.mu.Lock()
			g := it.Suggestion
			h.inbox.mu.Unlock()
			if g == nil || g.ReplyPolicy != nil || it.ReplyPolicy != nil {
				t.Fatalf("disabled policy must leave reply policy snapshots nil: item=%+v suggestion=%+v", it.ReplyPolicy, g)
			}

			// 3. Confirmation sends normally and keeps attempt policy fields empty.
			code, out := suggestionCall(t, h, it.ID, `{"version":1,"action":"confirm"}`)
			if code != http.StatusOK {
				t.Fatalf("confirm failed: %d %v", code, out)
			}
			if len(lark.got) != 1 || lark.got[0].Text != "On track for this afternoon." {
				t.Fatalf("unexpected sent replies: %+v", lark.got)
			}
			h.inbox.mu.Lock()
			attempt := suggestionAttempt(it, 1)
			h.inbox.mu.Unlock()
			if attempt == nil || attempt.Status != suggestionSent {
				t.Fatalf("unexpected attempt: %+v", attempt)
			}
			if attempt.Category != "" || attempt.Route != "" || attempt.ContentVersion != "" || attempt.PolicyRevision != "" {
				t.Fatalf("disabled policy should leave attempt policy fields empty: %+v", attempt)
			}
			if callCount() != 0 {
				t.Fatalf("expected 0 classifier calls when disabled, got %d", callCount())
			}
		})
	}
}

func TestInboxReplyPolicyServerCategoryMappingAndFailClosed(t *testing.T) {
	if got := len(OwnerOnlyReplyPolicyCategories()); got != 4 {
		t.Fatalf("expected 4 owner-only categories, got %d", got)
	}
	if got := len(BotReplyPolicyCategories()); got != 5 {
		t.Fatalf("expected 5 bot categories, got %d", got)
	}

	spec := compileReplyPolicy(assistantwire.InboxReplyPolicyConfig{Enabled: true, Revision: "v1"})
	if !spec.enabled || !strings.HasPrefix(spec.revision, "v1@") {
		t.Fatalf("unexpected compiled policy: %+v", spec)
	}

	if cat, route, err := spec.resolve(ReplyCategoryRoutineReply); err != nil || cat != ReplyCategoryRoutineReply || route != assistantwire.ReplyRouteOwnerDraft {
		t.Fatalf("routine_reply resolution failed: cat=%q route=%q err=%v", cat, route, err)
	}
	for _, cat := range OwnerOnlyReplyPolicyCategories() {
		if _, route, err := spec.resolve(cat); err != nil || route != assistantwire.ReplyRouteOwnerOnly {
			t.Fatalf("owner-only category %q resolved to route=%q err=%v", cat, route, err)
		}
	}
	for _, cat := range BotReplyPolicyCategories() {
		if _, route, err := spec.resolve(cat); err != nil || route != assistantwire.ReplyRouteBot {
			t.Fatalf("bot category %q resolved to route=%q err=%v", cat, route, err)
		}
	}
	if _, route, err := spec.resolve(ReplyCategoryNoReply); err != nil || route != assistantwire.ReplyRouteNoReply {
		t.Fatalf("no_reply resolution failed: route=%q err=%v", route, err)
	}
	for _, unknown := range []string{"", "   ", "unknown_category", assistantwire.ReplyRouteOwnerDraft} {
		if _, route, err := spec.resolve(unknown); err == nil || route != "" {
			t.Fatalf("unknown category %q must fail closed: route=%q err=%v", unknown, route, err)
		}
	}

	invalidRouteSpec := compileReplyPolicy(assistantwire.InboxReplyPolicyConfig{
		Enabled:    true,
		Categories: map[string]string{"custom_cat": "auto_send"},
	})
	if _, route, err := invalidRouteSpec.resolve("custom_cat"); err == nil || route != "" {
		t.Fatalf("invalid route must fail closed: route=%q err=%v", route, err)
	}

	ambiguousSpec := compileReplyPolicy(assistantwire.InboxReplyPolicyConfig{
		Enabled: true,
		Categories: map[string]string{
			"Routine": assistantwire.ReplyRouteOwnerDraft,
			"routine": assistantwire.ReplyRouteOwnerOnly,
		},
	})
	if _, route, err := ambiguousSpec.resolve("routine"); err == nil || route != "" {
		t.Fatalf("conflicting normalized category keys must fail closed: route=%q err=%v", route, err)
	}

	spec2 := compileReplyPolicy(assistantwire.InboxReplyPolicyConfig{Enabled: true, Revision: "v2"})
	if spec.revision == spec2.revision {
		t.Fatalf("different revisions must produce different effective revisions: %q", spec.revision)
	}
	specEdited := compileReplyPolicy(assistantwire.InboxReplyPolicyConfig{
		Enabled:    true,
		Revision:   "v1",
		Categories: map[string]string{ReplyCategoryRoutineReply: assistantwire.ReplyRouteOwnerOnly},
	})
	if spec.revision == specEdited.revision {
		t.Fatalf("editing categories must change effective revision even with same explicit revision: %q", spec.revision)
	}
}

// Focused Gate 1 (queue gate) tests: removing the queue gate causes these tests to fail.
func TestInboxReplyPolicyCompiledQueueGate(t *testing.T) {
	blockedCategories := make([]string, 0, 11)
	blockedCategories = append(blockedCategories, OwnerOnlyReplyPolicyCategories()...)
	blockedCategories = append(blockedCategories, BotReplyPolicyCategories()...)
	blockedCategories = append(blockedCategories, ReplyCategoryNoReply, "unlisted_mystery_category")

	for _, cat := range blockedCategories {
		t.Run("blocks_"+cat, func(t *testing.T) {
			// Also include a rogue "route":"owner_draft" field to prove the model cannot choose its own route.
			payload, _ := json.Marshal(map[string]any{
				"category": cat,
				"route":    assistantwire.ReplyRouteOwnerDraft,
				"reason":   "classified as " + cat,
			})
			model, calls := replyPolicyClassifierModel(t, func(_ int, _, _ string, _ *http.Request) (int, string) {
				return http.StatusOK, string(payload)
			})
			h, _ := newReplyPolicyHub(t, assistantwire.InboxReplyPolicyConfig{Enabled: true, Revision: "q-v1"}, model)
			it := replyPolicyTestItem("lark:q-"+cat+":1", "1", "Message for "+cat)
			h.inbox.mu.Lock()
			h.inbox.items[it.ID] = it
			h.inbox.mu.Unlock()

			h.handPass(context.Background())

			if h.f.made != 0 {
				t.Fatalf("category %q started %d handling conversations, want 0", cat, h.f.made)
			}
			h.inbox.mu.Lock()
			agent := it.Agent
			handling := it.Handling
			snap := it.ReplyPolicy
			h.inbox.mu.Unlock()
			if agent != nil || handling != nil {
				t.Fatalf("category %q must not queue or handle: agent=%+v handling=%+v", cat, agent, handling)
			}
			if snap == nil || snap.Route == assistantwire.ReplyRouteOwnerDraft {
				t.Fatalf("unexpected snapshot for blocked category %q: %+v", cat, snap)
			}
			if calls() != 1 {
				t.Fatalf("expected 1 classifier call, got %d", calls())
			}
		})
	}

	t.Run("fails_closed_on_invalid_route_error_and_timeout", func(t *testing.T) {
		for _, mode := range []string{"invalid_route", "http_error", "timeout"} {
			t.Run(mode, func(t *testing.T) {
				cfg := assistantwire.InboxReplyPolicyConfig{Enabled: true, Revision: "q-err"}
				if mode == "invalid_route" {
					cfg.Categories = map[string]string{ReplyCategoryRoutineReply: "invented_route"}
				}
				var model *ai.Client
				switch mode {
				case "invalid_route":
					model, _ = replyPolicyClassifierModel(t, func(_ int, _, _ string, _ *http.Request) (int, string) {
						return http.StatusOK, `{"category":"routine_reply","route":"owner_draft","reason":"try bypass"}`
					})
				case "http_error":
					model, _ = replyPolicyClassifierModel(t, func(_ int, _, _ string, _ *http.Request) (int, string) {
						return http.StatusInternalServerError, `{"error":"upstream failure"}`
					})
				case "timeout":
					cfg.TimeoutSeconds = 1
					model, _ = replyPolicyClassifierModel(t, func(_ int, _, _ string, r *http.Request) (int, string) {
						<-r.Context().Done()
						return http.StatusRequestTimeout, ""
					})
				}
				h, _ := newReplyPolicyHub(t, cfg, model)
				it := replyPolicyTestItem("lark:q-"+mode+":1", "1", "Message for "+mode)
				h.inbox.mu.Lock()
				h.inbox.items[it.ID] = it
				h.inbox.mu.Unlock()

				ctx := context.Background()
				if mode == "timeout" {
					// Use a short deadline on a sub-context? Notice classifyReplyPolicy checks cctx.Err() with ctx.Err()==nil
					// when policy.timeout expires. Keep policy.timeout small via TimeoutSeconds=1 (already set) or override ctx.
					// To avoid waiting 1s in unit tests, cancel via a tight policy timeout below.
				}
				// Execute queue gate directly with a tight timeout for the timeout subcase so test is instant.
				if mode == "timeout" {
					spec := compileReplyPolicy(cfg)
					spec.timeout = 20 * time.Millisecond
					h.gateReplyPolicyQueue(ctx, spec, time.Now().Add(-time.Hour))
					escalated := h.inbox.escalateWithPolicy(time.Now().Add(-time.Hour), time.Now(), spec)
					if len(escalated) != 0 {
						t.Fatalf("timeout mode escalated %d items, want 0", len(escalated))
					}
					if it.ReplyPolicy == nil || !strings.Contains(it.ReplyPolicy.Error, "timed out") {
						t.Fatalf("expected timeout error snapshot, got %+v", it.ReplyPolicy)
					}
					return
				}

				h.handPass(ctx)
				if h.f.made != 0 || it.Agent != nil {
					t.Fatalf("%s must fail closed without starting AI handling: starts=%d agent=%+v", mode, h.f.made, it.Agent)
				}
			})
		}
	})

	t.Run("releases_prequeued_item_when_policy_revision_changes", func(t *testing.T) {
		model, _ := replyPolicyClassifierModel(t, func(_ int, _, _ string, _ *http.Request) (int, string) {
			return http.StatusOK, `{"category":"routine_reply","reason":"normal"}`
		})
		h, _ := newReplyPolicyHub(t, assistantwire.InboxReplyPolicyConfig{Enabled: true, Revision: "v1"}, model)
		it := replyPolicyTestItem("lark:q-stale:1", "1", "Please check this")
		h.inbox.mu.Lock()
		h.inbox.items[it.ID] = it
		specV1 := compileReplyPolicy(h.in.cfg.ReplyPolicy)
		it.ReplyPolicy = &InboxReplyPolicySnapshot{
			Category:       ReplyCategoryRoutineReply,
			Route:          assistantwire.ReplyRouteOwnerDraft,
			ContentVersion: replyPolicyContentVersion(it),
			PolicyRevision: specV1.revision,
		}
		h.inbox.mu.Unlock()
		if ws := h.inbox.escalateWithPolicy(time.Now().Add(-time.Hour), time.Now(), specV1); len(ws) != 1 {
			t.Fatalf("expected item to queue under v1, got %d", len(ws))
		}

		// Policy revision changes before startInboxAgents runs; stale queued item must be released, not started.
		h.setConfig(func(c *assistantwire.InboxConfig) {
			c.ReplyPolicy.Revision = "v2"
		})
		h.startInboxAgents(context.Background(), InboxAgentConfig{Enabled: true})
		if h.f.made != 0 {
			t.Fatalf("stale queued item started under changed policy revision: starts=%d", h.f.made)
		}
		h.inbox.mu.Lock()
		state := it.Agent.State
		handlingState := it.Handling.State
		h.inbox.mu.Unlock()
		if state != AgentSkipped || handlingState != HandlingDone {
			t.Fatalf("expected released queue item, got agent=%s handling=%s", state, handlingState)
		}
	})

	t.Run("allows_owner_draft_category", func(t *testing.T) {
		model, _ := replyPolicyClassifierModel(t, func(_ int, _, _ string, _ *http.Request) (int, string) {
			return http.StatusOK, `{"category":"routine_reply","reason":"ordinary coordination"}`
		})
		h, _ := newReplyPolicyHub(t, assistantwire.InboxReplyPolicyConfig{Enabled: true, Revision: "q-ok"}, model)
		it := replyPolicyTestItem("lark:q-ok:1", "1", "Can you share the timeline?")
		h.inbox.mu.Lock()
		h.inbox.items[it.ID] = it
		h.inbox.mu.Unlock()

		h.handPass(context.Background())
		if h.f.made != 1 {
			t.Fatalf("owner_draft category should start 1 conversation, got %d", h.f.made)
		}
		h.inbox.mu.Lock()
		snap := it.ReplyPolicy
		h.inbox.mu.Unlock()
		if snap == nil || snap.Category != ReplyCategoryRoutineReply || snap.Route != assistantwire.ReplyRouteOwnerDraft {
			t.Fatalf("unexpected reply policy snapshot: %+v", snap)
		}
	})
}

// Focused Gate 2 (suggestion gate) tests: starts from an already-handled item (bypassing Gate 1).
// Removing the suggestion gate causes these tests to fail.
func TestInboxReplyPolicySuggestionGate(t *testing.T) {
	blockedCategories := make([]string, 0, 12)
	blockedCategories = append(blockedCategories, OwnerOnlyReplyPolicyCategories()...)
	blockedCategories = append(blockedCategories, BotReplyPolicyCategories()...)
	blockedCategories = append(blockedCategories, ReplyCategoryNoReply, "totally_unknown_category", assistantwire.ReplyRouteOwnerDraft)

	for _, cat := range blockedCategories {
		t.Run("refuses_"+cat, func(t *testing.T) {
			// Include rogue "route":"owner_draft" to prove the model cannot choose its own route.
			payload, _ := json.Marshal(map[string]any{
				"category": cat,
				"route":    assistantwire.ReplyRouteOwnerDraft,
				"reason":   "classified as " + cat,
			})
			model, _ := replyPolicyClassifierModel(t, func(_ int, _, _ string, _ *http.Request) (int, string) {
				return http.StatusOK, string(payload)
			})
			h, _ := newReplyPolicyHub(t, assistantwire.InboxReplyPolicyConfig{Enabled: true, Revision: "s-v1"}, model)
			it := replyPolicyTestItem("lark:s-"+cat+":1", "1", "Incoming message for "+cat)
			seedHandledReplyPolicyItem(t, h, it)

			out, err := suggestReplyWithTurn(t, h, "turn-1", "Draft reply text")
			if err == nil {
				t.Fatalf("category %q must refuse inbox_suggest reply, got %q", cat, out)
			}
			h.inbox.mu.Lock()
			sug := it.Suggestion
			draft := it.Agent.Draft
			seq := it.SuggestSeq
			h.inbox.mu.Unlock()
			if sug != nil || draft != "" || seq != 0 {
				t.Fatalf("refused category %q created a draft: suggestion=%+v draft=%q seq=%d", cat, sug, draft, seq)
			}

			// Legacy inbox_draft tool must also refuse and propagate the error.
			draftArgs, _ := json.Marshal(map[string]any{"text": "Draft reply text", "processing_turn": "turn-1"})
			if _, err := h.runTool(context.Background(), "server", "s1", "inbox_draft", draftArgs); err == nil {
				t.Fatalf("legacy inbox_draft must refuse category %q", cat)
			}
			h.inbox.mu.Lock()
			if it.Suggestion != nil || it.Agent.Draft != "" {
				h.inbox.mu.Unlock()
				t.Fatalf("legacy inbox_draft created a draft for %q", cat)
			}
			h.inbox.mu.Unlock()
		})
	}

	t.Run("refuses_invalid_route_error_timeout_and_stale_revision_mid_call", func(t *testing.T) {
		for _, mode := range []string{"invalid_route", "http_error", "timeout", "stale_revision", "content_changed"} {
			t.Run(mode, func(t *testing.T) {
				cfg := assistantwire.InboxReplyPolicyConfig{Enabled: true, Revision: "s-v1"}
				if mode == "invalid_route" {
					cfg.Categories = map[string]string{ReplyCategoryRoutineReply: "not_a_real_route"}
				}
				var h *inboxHub
				var it *InboxItem
				model, _ := replyPolicyClassifierModel(t, func(_ int, _, _ string, r *http.Request) (int, string) {
					switch mode {
					case "http_error":
						return http.StatusBadGateway, `{"error":"down"}`
					case "timeout":
						<-r.Context().Done()
						return http.StatusRequestTimeout, ""
					case "stale_revision":
						h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy.Revision = "s-v2" })
						return http.StatusOK, `{"category":"routine_reply","reason":"ok"}`
					case "content_changed":
						h.inbox.mu.Lock()
						it.Last = "2"
						it.Context = "Updated urgent approval request"
						h.inbox.mu.Unlock()
						return http.StatusOK, `{"category":"routine_reply","reason":"ok"}`
					default:
						return http.StatusOK, `{"category":"routine_reply","route":"owner_draft","reason":"ok"}`
					}
				})
				h, _ = newReplyPolicyHub(t, cfg, model)
				it = replyPolicyTestItem("lark:s-"+mode+":1", "1", "Initial message")
				seedHandledReplyPolicyItem(t, h, it)

				if mode == "timeout" {
					// Bound the caller context so timeout completes immediately without sleeping.
					ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
					defer cancel()
					args, _ := json.Marshal(map[string]any{"kind": SuggestReply, "text": "Draft", "processing_turn": "turn-1"})
					if _, err := h.toolInboxSuggest(ctx, "s1", args); err == nil {
						t.Fatal("timeout must refuse reply suggestion")
					}
				} else {
					if _, err := suggestReplyWithTurn(t, h, "turn-1", "Draft"); err == nil {
						t.Fatalf("%s must refuse reply suggestion", mode)
					}
				}
				h.inbox.mu.Lock()
				sug := it.Suggestion
				h.inbox.mu.Unlock()
				if sug != nil {
					t.Fatalf("%s created suggestion: %+v", mode, sug)
				}
			})
		}
	})

	t.Run("refuses_handed_off_conversation_reply_draft", func(t *testing.T) {
		model, _ := replyPolicyClassifierModel(t, func(_ int, _, _ string, _ *http.Request) (int, string) {
			return http.StatusOK, `{"category":"sensitive_personal","reason":"personal matter"}`
		})
		h, _ := newReplyPolicyHub(t, assistantwire.InboxReplyPolicyConfig{Enabled: true, Revision: "h-v1"}, model)
		it := replyPolicyTestItem("lark:s-handoff:1", "1", "Private personal question")
		h.inbox.mu.Lock()
		h.inbox.items[it.ID] = it
		it.Handoffs = []InboxHandoff{{MachineID: "server", SessionID: "main-1", At: time.Now().UTC(), Op: "op-1"}}
		h.inbox.mu.Unlock()

		args, _ := json.Marshal(map[string]any{"item": it.ID, "kind": SuggestReply, "text": "Drafted reply"})
		if _, err := h.toolInboxSuggest(context.Background(), "main-1", args); err == nil {
			t.Fatal("handed-off conversation must not create an owner reply suggestion for owner_only category")
		}
		h.inbox.mu.Lock()
		sug := it.Suggestion
		h.inbox.mu.Unlock()
		if sug != nil {
			t.Fatalf("unexpected suggestion created from handoff: %+v", sug)
		}
	})

	t.Run("allows_owner_draft_and_snapshots_metadata", func(t *testing.T) {
		model, _ := replyPolicyClassifierModel(t, func(_ int, _, _ string, _ *http.Request) (int, string) {
			return http.StatusOK, `{"category":"routine_reply","reason":"ordinary update"}`
		})
		cfg := assistantwire.InboxReplyPolicyConfig{Enabled: true, Revision: "s-ok"}
		h, _ := newReplyPolicyHub(t, cfg, model)
		it := replyPolicyTestItem("lark:s-ok:1", "1", "When is the sync?")
		seedHandledReplyPolicyItem(t, h, it)

		if _, err := suggestReplyWithTurn(t, h, "turn-1", "Sync is at 3pm."); err != nil {
			t.Fatalf("owner_draft suggestion should succeed: %v", err)
		}
		h.inbox.mu.Lock()
		g := it.Suggestion
		cv := replyPolicyContentVersion(it)
		h.inbox.mu.Unlock()
		wantRev := compileReplyPolicy(cfg).revision
		if g == nil || g.ReplyPolicy == nil {
			t.Fatalf("expected suggestion with ReplyPolicy snapshot, got %+v", g)
		}
		if g.ReplyPolicy.Category != ReplyCategoryRoutineReply ||
			g.ReplyPolicy.Route != assistantwire.ReplyRouteOwnerDraft ||
			g.ReplyPolicy.ContentVersion != cv ||
			g.ReplyPolicy.PolicyRevision != wantRev {
			t.Fatalf("unexpected suggestion policy snapshot: %+v (want cv=%s rev=%s)", g.ReplyPolicy, cv, wantRev)
		}
	})
}

// Focused Gate 3 (send gate) tests: starts from an already-accepted reply suggestion (bypassing Gate 1 & Gate 2).
// Removing the send gate causes these tests to fail.
func TestInboxReplyPolicySendGate(t *testing.T) {
	setupAcceptedSuggestion := func(t *testing.T, cfg assistantwire.InboxReplyPolicyConfig, model *ai.Client, withSnapshot bool) (*inboxHub, *extensionLark, *InboxItem) {
		t.Helper()
		h, lark := newReplyPolicyHub(t, cfg, model)
		it := replyPolicyTestItem("lark:send:1", "1", "Can you share the status?")
		seedHandledReplyPolicyItem(t, h, it)
		now := time.Now().UTC()
		spec := compileReplyPolicy(cfg)
		h.inbox.mu.Lock()
		cv := replyPolicyContentVersion(it)
		g := &InboxSuggestion{
			Version:  1,
			Kind:     SuggestReply,
			Text:     "I will check and follow up.",
			At:       now,
			Owner:    &SessionRef{MachineID: "server", SessionID: "s1"},
			SourceOf: suggestionReplyTarget(it),
		}
		if withSnapshot {
			g.ReplyPolicy = &InboxReplyPolicySnapshot{
				Category:       ReplyCategoryRoutineReply,
				Route:          assistantwire.ReplyRouteOwnerDraft,
				ContentVersion: cv,
				PolicyRevision: spec.revision,
				At:             now,
			}
			it.ReplyPolicy = cloneReplyPolicySnapshot(g.ReplyPolicy)
		}
		it.SuggestSeq = 1
		it.Suggestion = g
		h.inbox.setHandling(it, HandlingDone, "", now)
		h.inbox.mu.Unlock()
		h.saveInbox(context.Background(), []*InboxItem{it})
		return h, lark, it
	}

	t.Run("final_text_veto_on_edited_text_zero_sender_calls", func(t *testing.T) {
		var sawEditedText atomic.Bool
		model, calls := replyPolicyClassifierModel(t, func(_ int, _, user string, _ *http.Request) (int, string) {
			if strings.Contains(user, "Approved for full budget sign-off") {
				sawEditedText.Store(true)
			}
			return http.StatusOK, `{"category":"approval_commitment","route":"owner_draft","reason":"budget approval in final text"}`
		})
		cfg := assistantwire.InboxReplyPolicyConfig{Enabled: true, Revision: "send-v1"}
		h, lark, it := setupAcceptedSuggestion(t, cfg, model, true)

		code, out := suggestionCall(t, h, it.ID, `{"version":1,"action":"confirm","text":"Approved for full budget sign-off."}`)
		if code != http.StatusConflict || out["code"] != assistantwire.CodeInboxReplyPolicyBlocked || out["delivered"] != "none" || out["retryable"] != false {
			t.Fatalf("expected 409 reply policy veto, got %d %v", code, out)
		}
		if len(lark.got) != 0 {
			t.Fatalf("final-text veto must make zero sender calls, got %d (%+v)", len(lark.got), lark.got)
		}
		if calls() != 1 || !sawEditedText.Load() {
			t.Fatalf("expected final-text classifier to inspect edited text once: calls=%d saw=%v", calls(), sawEditedText.Load())
		}
		h.inbox.mu.Lock()
		a := suggestionAttempt(it, 1)
		h.inbox.mu.Unlock()
		if a == nil || a.Status != suggestionFailed || a.Code != assistantwire.CodeInboxReplyPolicyBlocked || a.Retryable {
			t.Fatalf("unexpected vetoed attempt: %+v", a)
		}
		if a.Proposal == nil || a.Proposal.Text != "I will check and follow up." || a.Text != "Approved for full budget sign-off." {
			t.Fatalf("vetoed attempt did not retain proposal and actual text: %+v", a)
		}
		if a.Category != ReplyCategoryApprovalCommitment || a.Route != assistantwire.ReplyRouteOwnerOnly || a.PolicyRevision != compileReplyPolicy(cfg).revision {
			t.Fatalf("vetoed attempt missing policy metadata: %+v", a)
		}
	})

	t.Run("final_text_veto_on_all_disallowed_outcomes_zero_sender_calls", func(t *testing.T) {
		outcomes := []struct {
			name   string
			status int
			body   string
		}{
			{"owner_only", http.StatusOK, `{"category":"external_public","reason":"public statement"}`},
			{"bot", http.StatusOK, `{"category":"system_alert","reason":"alert"}`},
			{"no_reply", http.StatusOK, `{"category":"no_reply","reason":"no reply needed"}`},
			{"unknown_category", http.StatusOK, `{"category":"unknown_x","route":"owner_draft","reason":"unknown"}`},
			{"classifier_error", http.StatusInternalServerError, `{"error":"boom"}`},
		}
		for _, tc := range outcomes {
			t.Run(tc.name, func(t *testing.T) {
				model, _ := replyPolicyClassifierModel(t, func(_ int, _, _ string, _ *http.Request) (int, string) {
					return tc.status, tc.body
				})
				h, lark, it := setupAcceptedSuggestion(t, assistantwire.InboxReplyPolicyConfig{Enabled: true, Revision: "send-v1"}, model, true)
				code, out := suggestionCall(t, h, it.ID, `{"version":1,"action":"confirm"}`)
				if code != http.StatusConflict || out["code"] != assistantwire.CodeInboxReplyPolicyBlocked {
					t.Fatalf("%s: expected 409 blocked, got %d %v", tc.name, code, out)
				}
				if len(lark.got) != 0 {
					t.Fatalf("%s: expected 0 sender calls, got %d", tc.name, len(lark.got))
				}
			})
		}
	})

	t.Run("final_text_veto_timeout_zero_sender_calls", func(t *testing.T) {
		model, _ := replyPolicyClassifierModel(t, func(_ int, _, _ string, r *http.Request) (int, string) {
			<-r.Context().Done()
			return http.StatusRequestTimeout, ""
		})
		h, lark, it := setupAcceptedSuggestion(t, assistantwire.InboxReplyPolicyConfig{Enabled: true, Revision: "send-v1"}, model, true)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		if _, err := h.actOnSuggestion(ctx, it.ID, suggestionReq{Version: 1, Action: suggestConfirm}, true); err == nil {
			t.Fatal("timeout during final-text veto must fail closed")
		}
		if len(lark.got) != 0 {
			t.Fatalf("timeout veto must make zero sender calls, got %d", len(lark.got))
		}
	})

	t.Run("blocks_stale_policy_revision_with_zero_sender_calls", func(t *testing.T) {
		model, calls := replyPolicyClassifierModel(t, func(_ int, _, _ string, _ *http.Request) (int, string) {
			return http.StatusOK, `{"category":"routine_reply","reason":"ok"}`
		})
		h, lark, it := setupAcceptedSuggestion(t, assistantwire.InboxReplyPolicyConfig{Enabled: true, Revision: "rev-1"}, model, true)
		// Bump policy revision after suggestion was created.
		h.setConfig(func(c *assistantwire.InboxConfig) {
			c.ReplyPolicy.Revision = "rev-2"
		})

		code, out := suggestionCall(t, h, it.ID, `{"version":1,"action":"confirm"}`)
		if code != http.StatusConflict || out["code"] != assistantwire.CodeInboxReplyPolicyBlocked {
			t.Fatalf("stale policy revision: got %d %v", code, out)
		}
		if len(lark.got) != 0 {
			t.Fatalf("stale policy revision must make zero sender calls, got %d", len(lark.got))
		}
		if calls() != 0 {
			t.Fatalf("stale revision should fail closed before calling classifier, got %d calls", calls())
		}
	})

	t.Run("blocks_stale_content_version_with_zero_sender_calls", func(t *testing.T) {
		model, _ := replyPolicyClassifierModel(t, func(_ int, _, _ string, _ *http.Request) (int, string) {
			return http.StatusOK, `{"category":"routine_reply","reason":"ok"}`
		})
		h, lark, it := setupAcceptedSuggestion(t, assistantwire.InboxReplyPolicyConfig{Enabled: true, Revision: "rev-1"}, model, true)
		// Mutate item text while keeping SourceOf unchanged (e.g. context edit or body fetch update).
		h.inbox.mu.Lock()
		it.Context = "Example contact: Actually, please approve the $500k wire transfer."
		h.inbox.mu.Unlock()

		code, out := suggestionCall(t, h, it.ID, `{"version":1,"action":"confirm"}`)
		if code != http.StatusConflict || out["code"] != assistantwire.CodeInboxReplyPolicyBlocked {
			t.Fatalf("stale content version: got %d %v", code, out)
		}
		if len(lark.got) != 0 {
			t.Fatalf("stale content version must make zero sender calls, got %d", len(lark.got))
		}
	})

	t.Run("blocks_missing_snapshot_with_zero_sender_calls", func(t *testing.T) {
		model, _ := replyPolicyClassifierModel(t, func(_ int, _, _ string, _ *http.Request) (int, string) {
			return http.StatusOK, `{"category":"routine_reply","reason":"ok"}`
		})
		h, lark, it := setupAcceptedSuggestion(t, assistantwire.InboxReplyPolicyConfig{Enabled: true, Revision: "rev-1"}, model, false)

		code, out := suggestionCall(t, h, it.ID, `{"version":1,"action":"confirm"}`)
		if code != http.StatusConflict || out["code"] != assistantwire.CodeInboxReplyPolicyBlocked {
			t.Fatalf("missing snapshot: got %d %v", code, out)
		}
		if len(lark.got) != 0 {
			t.Fatalf("missing snapshot must make zero sender calls, got %d", len(lark.got))
		}
	})

	t.Run("blocks_category_remapped_or_invalid_route_with_zero_sender_calls", func(t *testing.T) {
		for _, newRoute := range []string{assistantwire.ReplyRouteOwnerOnly, "bogus_route"} {
			t.Run(newRoute, func(t *testing.T) {
				model, _ := replyPolicyClassifierModel(t, func(_ int, _, _ string, _ *http.Request) (int, string) {
					return http.StatusOK, `{"category":"routine_reply","reason":"ok"}`
				})
				h, lark, it := setupAcceptedSuggestion(t, assistantwire.InboxReplyPolicyConfig{Enabled: true, Revision: "rev-1"}, model, true)
				h.setConfig(func(c *assistantwire.InboxConfig) {
					c.ReplyPolicy.Categories = map[string]string{ReplyCategoryRoutineReply: newRoute}
				})

				code, out := suggestionCall(t, h, it.ID, `{"version":1,"action":"confirm"}`)
				if code != http.StatusConflict || out["code"] != assistantwire.CodeInboxReplyPolicyBlocked {
					t.Fatalf("remapped route %q: got %d %v", newRoute, code, out)
				}
				if len(lark.got) != 0 {
					t.Fatalf("remapped route %q must make zero sender calls, got %d", newRoute, len(lark.got))
				}
			})
		}
	})

	t.Run("blocks_policy_revision_change_during_final_text_check_with_zero_sender_calls", func(t *testing.T) {
		var h *inboxHub
		var once sync.Once
		model, _ := replyPolicyClassifierModel(t, func(_ int, _, _ string, _ *http.Request) (int, string) {
			once.Do(func() {
				h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy.Revision = "rev-2" })
			})
			return http.StatusOK, `{"category":"routine_reply","reason":"ok"}`
		})
		var lark *extensionLark
		var it *InboxItem
		h, lark, it = setupAcceptedSuggestion(t, assistantwire.InboxReplyPolicyConfig{Enabled: true, Revision: "rev-1"}, model, true)

		code, out := suggestionCall(t, h, it.ID, `{"version":1,"action":"confirm"}`)
		if code != http.StatusConflict || out["code"] != assistantwire.CodeInboxReplyPolicyBlocked {
			t.Fatalf("mid-check revision change: got %d %v", code, out)
		}
		if len(lark.got) != 0 {
			t.Fatalf("mid-check revision change must make zero sender calls, got %d", len(lark.got))
		}
	})

	t.Run("blocks_stale_attempt_at_pre_dispatch_send_boundary_with_zero_sender_calls", func(t *testing.T) {
		h, lark, it := setupAcceptedSuggestion(t, assistantwire.InboxReplyPolicyConfig{Enabled: true, Revision: "rev-1"}, nil, true)
		h.inbox.mu.Lock()
		g := it.Suggestion
		a := &InboxSuggestionAttempt{
			Version:        g.Version,
			Status:         suggestionSending,
			Text:           g.Text,
			ClientID:       namedClientID("pre-dispatch-check"),
			Proposal:       copySuggestion(g),
			Target:         suggestionReplyTarget(it),
			Category:       g.ReplyPolicy.Category,
			Route:          g.ReplyPolicy.Route,
			ContentVersion: g.ReplyPolicy.ContentVersion,
			PolicyRevision: g.ReplyPolicy.PolicyRevision,
			active:         true,
		}
		keepSuggestionAttempt(it, a)
		h.inbox.mu.Unlock()
		// Policy revision advances right before replyInboxDoMode dispatches to the sender.
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy.Revision = "rev-2" })

		sendCtx := context.WithValue(context.Background(), suggestionReplyOnly{}, a)
		if _, err := h.replyInboxDo(sendCtx, it.ID, a.Text, a.ClientID, false, false); err == nil {
			t.Fatal("stale attempt at pre-dispatch send boundary must fail closed")
		}
		if len(lark.got) != 0 {
			t.Fatalf("pre-dispatch send gate must make zero sender calls, got %d", len(lark.got))
		}
	})
}

// End-to-end test for a normal owner-draft category:
// Gate 1 -> Gate 2 -> independent owner confirmation (with edit) -> Gate 3 -> sender sends once ->
// persisted InboxSuggestionAttempt stores proposal, actual text, category, route, content version, and policy revision.
func TestInboxReplyPolicyOwnerDraftConfirmationStoresAuditSnapshot(t *testing.T) {
	model, calls := replyPolicyClassifierModel(t, func(_ int, _, _ string, _ *http.Request) (int, string) {
		return http.StatusOK, `{"category":"routine_reply","reason":"ordinary coordination"}`
	})
	cfg := assistantwire.InboxReplyPolicyConfig{
		Enabled:  true,
		Revision: "policy-2026-11",
	}
	wantRev := compileReplyPolicy(cfg).revision
	h, lark := newReplyPolicyHub(t, cfg, model)
	it := replyPolicyTestItem("lark:ok:1", "42", "Could you confirm the meeting room for tomorrow?")
	h.inbox.mu.Lock()
	h.inbox.items[it.ID] = it
	wantCV := replyPolicyContentVersion(it)
	h.inbox.mu.Unlock()

	// Gate 1: queue and start handling conversation.
	h.handPass(context.Background())
	if h.f.made != 1 {
		t.Fatalf("expected 1 started conversation, got %d", h.f.made)
	}
	if len(lark.got) != 0 {
		t.Fatal("queueing must never send an outward reply")
	}

	// Gate 2: accept reply suggestion.
	h.inbox.mu.Lock()
	sessionID := it.Agent.SessionID
	turn := it.Agent.ProcessingTurn
	h.inbox.mu.Unlock()
	args, _ := json.Marshal(map[string]any{
		"kind":            SuggestReply,
		"text":            "Proposed: Room B at 10am.",
		"processing_turn": turn,
	})
	if _, err := h.toolInboxSuggest(context.Background(), sessionID, args); err != nil {
		t.Fatalf("owner_draft suggestion failed: %v", err)
	}
	if len(lark.got) != 0 {
		t.Fatal("accepting a suggestion must never send without independent owner confirmation")
	}

	// Gate 3: independent owner confirmation with edited text.
	code, out := suggestionCall(t, h, it.ID, `{"version":1,"action":"confirm","text":"Confirmed: Room C at 10:30am."}`)
	if code != http.StatusOK {
		t.Fatalf("owner confirmation failed: %d %v", code, out)
	}
	if len(lark.got) != 1 || lark.got[0].Text != "Confirmed: Room C at 10:30am." {
		t.Fatalf("expected 1 sent reply with edited text, got %+v", lark.got)
	}
	if calls() != 3 {
		t.Fatalf("expected 3 classifier calls (queue, suggestion, send final-text), got %d", calls())
	}

	// Verify listed API output omits internal policy bindings.
	listedAttempts, _ := out["suggestion_attempts"].([]any)
	if len(listedAttempts) != 1 {
		t.Fatalf("expected 1 listed attempt, got %v", out["suggestion_attempts"])
	}
	listedMap, _ := listedAttempts[0].(map[string]any)
	for _, hidden := range []string{"proposal", "category", "route", "content_version", "policy_revision"} {
		if _, present := listedMap[hidden]; present {
			t.Fatalf("listed suggestion_attempts must omit internal field %q: %v", hidden, listedMap)
		}
	}

	// Verify stored audit record survives reload from storage with proposal, actual text, category, route, content version, and policy revision.
	reloaded := New(Options{Hub: h.f, Store: func() Store { return h.st }, Logf: testLogf(t)})
	if err := reloaded.inboxReady(context.Background()); err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	reloaded.inbox.mu.Lock()
	rit := reloaded.inbox.items[it.ID]
	var attempt *InboxSuggestionAttempt
	if rit != nil {
		attempt = suggestionAttempt(rit, 1)
	}
	reloaded.inbox.mu.Unlock()
	if attempt == nil {
		t.Fatal("reloaded item missing suggestion attempt")
	}
	if attempt.Status != suggestionSent {
		t.Fatalf("attempt status = %q, want %q", attempt.Status, suggestionSent)
	}
	if attempt.Proposal == nil || attempt.Proposal.Text != "Proposed: Room B at 10am." {
		t.Fatalf("stored proposal mismatch: %+v", attempt.Proposal)
	}
	if attempt.Text != "Confirmed: Room C at 10:30am." {
		t.Fatalf("stored actual text = %q, want edited text", attempt.Text)
	}
	if attempt.Category != ReplyCategoryRoutineReply ||
		attempt.Route != assistantwire.ReplyRouteOwnerDraft ||
		attempt.ContentVersion != wantCV ||
		attempt.PolicyRevision != wantRev {
		t.Fatalf("stored audit snapshot mismatch: cat=%q route=%q cv=%q rev=%q (want cv=%q rev=%q)",
			attempt.Category, attempt.Route, attempt.ContentVersion, attempt.PolicyRevision, wantCV, wantRev)
	}

	// Direct manual owner reply via /reply remains unaffected for owner_only items (the owner can still reply themselves).
	ownerOnlyItem := replyPolicyTestItem("lark:owner-direct:1", "9", "Please approve this contract")
	h.inbox.mu.Lock()
	h.inbox.items[ownerOnlyItem.ID] = ownerOnlyItem
	h.inbox.mu.Unlock()
	if _, err := h.replyInboxFrom(context.Background(), ownerOnlyItem.ID, "Owner direct reply", "direct-1", false); err != nil {
		t.Fatalf("direct owner reply must remain available: %v", err)
	}
	if len(lark.got) != 2 || lark.got[1].Text != "Owner direct reply" {
		t.Fatalf("expected direct owner reply to send, got %+v", lark.got)
	}
	_ = connector.TagChat
}

func TestInboxReplyPolicyDraftAndRetryGates(t *testing.T) {
	t.Run("draftReply_vetoes_generated_draft_text", func(t *testing.T) {
		classifier, classifierCalls := replyPolicyClassifierModel(t, func(call int, _, user string, _ *http.Request) (int, string) {
			if call == 1 {
				return http.StatusOK, `{"category":"routine_reply","reason":"initial question"}`
			}
			if strings.Contains(user, "Approved for immediate payout") {
				return http.StatusOK, `{"category":"approval_commitment","reason":"generated text approves payout"}`
			}
			return http.StatusOK, `{"category":"routine_reply","reason":"ok"}`
		})
		draftModel, draftCalls := replyPolicyClassifierModel(t, func(_ int, _, _ string, _ *http.Request) (int, string) {
			return http.StatusOK, "Approved for immediate payout."
		})
		h, _ := newReplyPolicyHub(t, assistantwire.InboxReplyPolicyConfig{Enabled: true, Revision: "draft-v1"}, classifier)
		h.in.models[purposeDraft] = draftModel
		it := replyPolicyTestItem("lark:draft-veto:1", "1", "Can we proceed?")
		h.inbox.mu.Lock()
		h.inbox.items[it.ID] = it
		h.inbox.mu.Unlock()

		if got, err := h.draftReply(context.Background(), it.ID, ""); err == nil || got != "" {
			t.Fatalf("expected generated draft text veto, got text=%q err=%v", got, err)
		}
		if draftCalls() != 1 || classifierCalls() != 2 {
			t.Fatalf("expected 1 draft call and 2 classifier calls (pre + generated text), got draft=%d classifier=%d", draftCalls(), classifierCalls())
		}
	})

	t.Run("draftReply_returns_errNoInboxItem_when_removed_during_generation", func(t *testing.T) {
		classifier, classifierCalls := replyPolicyClassifierModel(t, func(_ int, _, _ string, _ *http.Request) (int, string) {
			return http.StatusOK, `{"category":"routine_reply","reason":"initial question"}`
		})
		var h *inboxHub
		var itemID string
		draftModel, draftCalls := replyPolicyClassifierModel(t, func(_ int, _, _ string, _ *http.Request) (int, string) {
			// Remove the inbox item while draftReply has released b.mu to generate text.
			h.inbox.mu.Lock()
			delete(h.inbox.items, itemID)
			h.inbox.mu.Unlock()
			return http.StatusOK, "Sounds good, will share shortly."
		})
		h, _ = newReplyPolicyHub(t, assistantwire.InboxReplyPolicyConfig{Enabled: true, Revision: "draft-v1"}, classifier)
		h.in.models[purposeDraft] = draftModel
		it := replyPolicyTestItem("lark:draft-removed:1", "1", "Can you share the update?")
		itemID = it.ID
		h.inbox.mu.Lock()
		h.inbox.items[it.ID] = it
		h.inbox.mu.Unlock()

		got, err := h.draftReply(context.Background(), it.ID, "")
		if !errors.Is(err, errNoInboxItem) || got != "" {
			t.Fatalf("expected errNoInboxItem when item removed mid-draft, got text=%q err=%v", got, err)
		}
		if draftCalls() != 1 || classifierCalls() != 1 {
			t.Fatalf("expected 1 draft call and 1 pre-generation classifier call, got draft=%d classifier=%d", draftCalls(), classifierCalls())
		}
	})

	t.Run("retryInboxHandling_blocks_disallowed_item", func(t *testing.T) {
		h, _ := newReplyPolicyHub(t, assistantwire.InboxReplyPolicyConfig{Enabled: true, Revision: "retry-v1"}, nil)
		it := replyPolicyTestItem("lark:retry-block:1", "1", "Please approve contract")
		now := time.Now().UTC()
		h.inbox.mu.Lock()
		h.inbox.items[it.ID] = it
		it.Agent = &InboxAgent{State: AgentFailed, Of: it.Last}
		h.inbox.setHandling(it, HandlingFailed, "failed", now)
		h.inbox.mu.Unlock()

		_, status, code, err := h.retryInboxHandling(context.Background(), it.ID)
		if err == nil || status != http.StatusConflict || code != assistantwire.CodeInboxReplyPolicyBlocked {
			t.Fatalf("expected 409 reply_policy_blocked on retry, got status=%d code=%q err=%v", status, code, err)
		}
	})
}
