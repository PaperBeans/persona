package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/rpc"
)

func replyPolicyTestConfig() *assistantwire.InboxReplyPolicyConfig {
	return &assistantwire.InboxReplyPolicyConfig{
		Enabled: true, Revision: "test-v1", Sources: []string{"lark"},
		Categories: map[string]string{
			"personal_transaction": "owner_only", "security_compliance": "owner_only",
			"approval_permission": "owner_only", "decision_delegation": "owner_only",
			"technical_qa": "bot", "research": "bot", "alert_triage": "bot", "ppe_deploy": "bot", "mr_precheck": "bot",
			"no_reply": "no_reply", "ordinary_coordination": "owner_draft", "logistical_followup": "owner_draft",
			"acknowledgement": "owner_draft", "other_owner_reply": "owner_draft", "unknown": "owner_only",
		},
	}
}

func TestInboxReplyPolicyClassifiesTwinFirstFYIBeforeQueue(t *testing.T) {
	h := agentHub(t)
	h.in.model = fakeModel(t, `{"category":"no_reply"}`)
	h.setConfig(func(c *assistantwire.InboxConfig) {
		c.Agent.Enabled = true
		c.ReplyPolicy = replyPolicyTestConfig()
	})
	it := actionItem("lark:queued:twin-first-fyi", time.Now())
	it.Source, it.Tier, it.TriagedAt = "lark", TierFYI, it.Last
	it.Lark = &InboxLark{FeedID: "synthetic-fyi-chat", IsGroup: true}
	h.inbox.items[it.ID] = it
	h.inbox.handing.Lock()
	t.Cleanup(h.inbox.handing.Unlock)
	setPolicyFor(t, h, InboxPolicy{Source: "lark", Tag: "chat", Value: "synthetic-fyi-chat", Twin: TwinFirst})
	h.handPass(context.Background())
	if replyPolicyTestCategory(it) != "no_reply" || replyPolicyTestRoute(it) != "no_reply" || !replyPolicyClassificationCurrent(it, replyPolicyTestConfig()) {
		t.Fatalf("TwinFirst FYI remained unclassifiable: %+v", it)
	}
	h.inbox.escalate(time.Now().Add(-time.Hour), time.Now())
	before := len(conversationStarts(h.f))
	h.startInboxAgents(context.Background(), InboxAgentConfig{Enabled: true})
	checkReplyPolicyReleased(t, it, h.inbox.userLang())
	if got := len(conversationStarts(h.f)); got != before {
		t.Fatalf("classified TwinFirst FYI started %d conversations", got-before)
	}
}

func TestInboxReplySuggestionSendGate(t *testing.T) {
	newDraft := func(t *testing.T) (*inboxHub, *extensionLark, *InboxItem) {
		h, lark, it := handledHub(t)
		h.in.model = fakeModel(t, `{"category":"ordinary_coordination"}`)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
		{
			replyPolicyTestSetCategory(it, "ordinary_coordination")
			replyPolicyTestSetRoute(it, "owner_draft")
			replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
			replyPolicyTestSetPolicyRevision(it, "test-v1")
		}
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
		if a == nil || a.Status != suggestionSent || a.Text != edited || a.Proposal == nil || a.Proposal.Text != "The original synthetic reply" || replyPolicyTestCategory(a.Proposal) != "ordinary_coordination" || replyPolicyTestRoute(a.Proposal) != "owner_draft" || replyPolicyTestPolicyRevision(a.Proposal) != "test-v1" {
			t.Fatalf("attempt snapshot = %+v", a)
		}
	})
	for _, tc := range []struct {
		name   string
		mutate func(*InboxItem)
	}{
		{"legacy draft has no category", func(it *InboxItem) {
			{
				replyPolicyTestSetCategory(it, "")
				replyPolicyTestSetRoute(it, "")
				replyPolicyTestSetContentVersion(it, "")
				replyPolicyTestSetPolicyRevision(it, "")
			}
			{
				replyPolicyTestSetCategory(it.Suggestion, "")
				replyPolicyTestSetRoute(it.Suggestion, "")
				replyPolicyTestSetContentVersion(it.Suggestion, "")
				replyPolicyTestSetPolicyRevision(it.Suggestion, "")
			}
		}},
		{"policy revision changed", func(it *InboxItem) {
			{
				replyPolicyTestSetPolicyRevision(it, "test-v2")
			}
		}},
		{"content changed", func(it *InboxItem) { it.Last = "newer-content" }},
		{"route is no longer owner draft", func(it *InboxItem) {
			{
				replyPolicyTestSetCategory(it, "technical_qa")
				replyPolicyTestSetRoute(it, "bot")
			}
		}},
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
			{
				replyPolicyTestSetCategory(it, "approval_permission")
				replyPolicyTestSetRoute(it, "owner_only")
			}
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
			cfg := replyPolicyTestConfig()
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
			oldCfg := assistantwire.InboxConfig{ReplyPolicy: replyPolicyTestConfig()}
			newCfg := assistantwire.InboxConfig{ReplyPolicy: replyPolicyTestConfig()}
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
		h.in.cfg.ReplyPolicy = &assistantwire.InboxReplyPolicyConfig{}
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
		{
			replyPolicyTestSetCategory(old, "acknowledgement")
			replyPolicyTestSetRoute(old, "owner_draft")
			replyPolicyTestSetPolicyRevision(old, "test-v0")
			replyPolicyTestSetReviewed(old, true)
		}
		{
			replyPolicyTestSetContentVersion(old, "synthetic-previous-content-version")
		}
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
		if !replyPolicyDenied(err) {
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
		if err == nil || !replyPolicyDenied(err) {
			t.Fatalf("edited approval confirmation error = %v", err)
		}
		if len(lark.got) != 0 {
			t.Fatalf("edited approval called sender: %+v", lark.got)
		}
	})
}

func TestInboxReplyPolicyOnDemandDraftGate(t *testing.T) {
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
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
		{
			replyPolicyTestSetCategory(it, "approval_permission")
			replyPolicyTestSetRoute(it, "owner_only")
		}
		{
			replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
			replyPolicyTestSetPolicyRevision(it, "test-v1")
		}
		model, calls := captureModel(t, "Synthetic owner-voice approval")
		h.in.model = model
		// Upstream's /draft contract reports a policy veto as a coded 422 rather than the legacy bare 403.
		// Keep the pre-model denial assertion and pin the complete coded response instead of accepting either status.
		code, out := inboxCall(t, h.routes(), "POST", "/api/v1/inbox/"+it.ID+"/draft", `{}`)
		if code != http.StatusUnprocessableEntity || out["code"] != assistantwire.CodeInboxReplyPolicyBlocked {
			t.Fatalf("owner-only on-demand draft response = %d %+v, want coded %d", code, out, http.StatusUnprocessableEntity)
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
		{
			replyPolicyTestSetCategory(it, "ordinary_coordination")
			replyPolicyTestSetRoute(it, "owner_draft")
		}
		{
			replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
			replyPolicyTestSetPolicyRevision(it, "test-v1")
		}
		model, calls := captureModel(t, "Synthetic draft from the revoked policy revision")
		h.in.model = fakeModel(t, `{"category":"ordinary_coordination"}`)
		h.in.models[purposeDraft] = model
		next := replyPolicyTestConfig()
		next.Revision = "test-v2"
		h.in.mu.Lock()
		h.in.configQueue = []assistantwire.InboxConfig{{ReplyPolicy: replyPolicyTestConfig()}, {ReplyPolicy: next}}
		h.in.mu.Unlock()
		if _, err := h.draftReply(t.Context(), it.ID, ""); !replyPolicyDenied(err) {
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
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
		{
			replyPolicyTestSetCategory(it, "ordinary_coordination")
			replyPolicyTestSetRoute(it, "owner_draft")
		}
		{
			replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
			replyPolicyTestSetPolicyRevision(it, "test-v1")
		}
		var changed atomic.Bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if changed.CompareAndSwap(false, true) {
				h.inbox.mu.Lock()
				now := time.Now().UTC()
				g := &InboxSuggestion{Version: 1, Kind: SuggestReply, Text: "Newer draft from another device", At: now, SourceOf: suggestionReplyTarget(it)}
				snapshotReplyPolicy(g, it, replyPolicyTestConfig())
				it.SuggestSeq, it.Suggestion = 1, g
				h.inbox.setHandling(it, HandlingDone, "", now)
				h.inbox.mu.Unlock()
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"content": []any{map[string]any{"type": "text", "text": "Stale on-demand draft"}}})
		}))
		t.Cleanup(srv.Close)
		h.in.model = fakeModel(t, `{"category":"ordinary_coordination"}`)
		h.in.models[purposeDraft] = modelAt(t, srv.URL)
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
			h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
			{
				replyPolicyTestSetCategory(it, "ordinary_coordination")
				replyPolicyTestSetRoute(it, "owner_draft")
			}
			{
				replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
				replyPolicyTestSetPolicyRevision(it, "test-v1")
			}
			replyPolicyOrdinaryDraftModel(t, h, "Synthetic on-demand coordination draft")
			text, err := h.draftReply(t.Context(), it.ID, "")
			if err != nil {
				t.Fatalf("ordinary on-demand draft failed: %v", err)
			}
			g := mirroredLegacyReply(it)
			if g == nil || g.Text != text || !replyPolicySnapshotCurrent(it, replyPolicyTestConfig()) {
				t.Fatalf("on-demand draft has no current policy-bound mirror: agent=%+v suggestion=%+v", it.Agent, currentSuggestion(it))
			}
			h.in.model = fakeModel(t, `{"category":"approval_permission"}`)
			edited := "I approve the synthetic on-demand permission request"
			if tc.fromCard {
				_, err = h.replyInboxFromVersion(t.Context(), it.ID, edited, "feishu-workbench-synthetic-send", true, 1)
			} else {
				_, err = h.replyInboxChecked(t.Context(), it.ID, edited, "", false, nil, nil)
			}
			if !replyPolicyDenied(err) {
				t.Fatalf("edited on-demand draft error = %v", err)
			}
			if len(lark.got) != 0 {
				t.Fatalf("edited on-demand draft reached sender through the direct-owner path: %+v", lark.got)
			}
		})
	}
	t.Run("unmodified on-demand draft sends through its mirror", func(t *testing.T) {
		h, lark, it := handledHub(t)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
		{
			replyPolicyTestSetCategory(it, "ordinary_coordination")
			replyPolicyTestSetRoute(it, "owner_draft")
		}
		{
			replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
			replyPolicyTestSetPolicyRevision(it, "test-v1")
		}
		replyPolicyOrdinaryDraftModel(t, h, "Synthetic confirmed on-demand draft")
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

func TestInboxReplyPolicyWorkbenchDraftRejectedBeforeSend(t *testing.T) {
	needsLarkLayer(t) // the integrated Workbench renderer and actions are the lark module's
	h, lark, it := handledHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
	{
		replyPolicyTestSetCategory(it, "ordinary_coordination")
		replyPolicyTestSetRoute(it, "owner_draft")
	}
	{
		replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
		replyPolicyTestSetPolicyRevision(it, "test-v1")
	}
	if _, err := suggestOrdinaryReplyForFixture(t, h, `{"kind":"reply","text":"Workbench draft rejected before its send"}`); err != nil {
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

func TestInboxReplyPolicyMirroredSendUsesDurableAttempt(t *testing.T) {
	for _, fromCard := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "workbench"}[fromCard], func(t *testing.T) {
			c := &recoveryReplier{err: rpc.ErrDropped}
			h, _, it := handledHub(t)
			h.in.mu.Lock()
			h.in.conns[0] = c
			h.in.mu.Unlock()
			h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
			{
				replyPolicyTestSetCategory(it, "ordinary_coordination")
				replyPolicyTestSetRoute(it, "owner_draft")
			}
			{
				replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
				replyPolicyTestSetPolicyRevision(it, "test-v1")
			}
			if _, err := suggestOrdinaryReplyForFixture(t, h, `{"kind":"reply","text":"Mirrored send with a lost acknowledgement"}`); err != nil {
				t.Fatal(err)
			}
			h.in.setModel(fakeModel(t, `{"category":"ordinary_coordination"}`))
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

func TestInboxReplyPolicyMirroredSendChecksRecipientBinding(t *testing.T) {
	for _, fromCard := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "workbench"}[fromCard], func(t *testing.T) {
			h, lark, it := handledHub(t)
			h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
			{
				replyPolicyTestSetCategory(it, "ordinary_coordination")
				replyPolicyTestSetRoute(it, "owner_draft")
			}
			{
				replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
				replyPolicyTestSetPolicyRevision(it, "test-v1")
			}
			if _, err := suggestOrdinaryReplyForFixture(t, h, `{"kind":"reply","text":"Draft bound to the original recipient"}`); err != nil {
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

func TestInboxReplyPolicyInstructedDraftClassifiesGeneratedText(t *testing.T) {
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
			h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
			it.Context = "Synthetic original conversation marker"
			{
				replyPolicyTestSetCategory(it, "ordinary_coordination")
				replyPolicyTestSetRoute(it, "owner_draft")
			}
			{
				replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
				replyPolicyTestSetPolicyRevision(it, "test-v1")
			}
			generated := "Synthetic generated draft marker"
			instruction := "Synthetic owner instruction marker"
			var modelCalls []capturedCall
			classifierCalls := 0
			classifierSystem := replyPolicyTestClassifierSystem(h, replyPolicyTestConfig())
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
				reply := generated
				if in.System == classifierSystem {
					classifierCalls++
					reply = `{"category":"ordinary_coordination"}`
					if classifierCalls > 1 {
						reply = `{"category":"` + tc.category + `"}`
					}
				}
				h.in.mu.Unlock()
				_ = json.NewEncoder(w).Encode(map[string]any{"content": []any{map[string]any{"type": "text", "text": reply}}})
			}))
			t.Cleanup(srv.Close)
			h.in.model = modelAt(t, srv.URL)
			text, err := h.draftReply(t.Context(), it.ID, instruction)
			if tc.denied {
				if !replyPolicyDenied(err) || text != "" {
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
			if len(calls) != 3 {
				t.Fatalf("instructed draft made %d model calls, want generation plus generated-text classification", len(calls))
			}
			if calls[0].System != classifierSystem || calls[2].System != classifierSystem {
				t.Fatalf("draft did not use initial and generated-text reply-policy classification: %+v", calls)
			}
			for _, marker := range []string{generated, instruction, "Synthetic original conversation marker"} {
				if !strings.Contains(calls[2].User, marker) {
					t.Fatalf("classifier input lacks %q: %+v", marker, calls[2])
				}
			}
			if len(lark.got) != 0 {
				t.Fatalf("instructed draft unexpectedly called sender: %+v", lark.got)
			}
		})
	}
}

type replyPolicyDraftFailStore struct {
	*inboxMemStore
	before func()
}

func (s *replyPolicyDraftFailStore) PutInboxItem(ctx context.Context, id string, data []byte, updated time.Time, done *time.Time) error {
	s.before()
	return errors.New("controlled on-demand draft write failure")
}

func TestInboxReplyPolicyDraftFailedWriteWithoutRollbackDoesNotReturnText(t *testing.T) {
	h, _, it := handledHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
	{
		replyPolicyTestSetCategory(it, "ordinary_coordination")
		replyPolicyTestSetRoute(it, "owner_draft")
	}
	{
		replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
		replyPolicyTestSetPolicyRevision(it, "test-v1")
	}
	replyPolicyOrdinaryDraftModel(t, h, "Draft whose only publication write fails")
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

type replyPolicyDraftReplacementStore struct {
	*inboxMemStore
	before func()
}

func (s *replyPolicyDraftReplacementStore) PutInboxItem(ctx context.Context, id string, data []byte, updated time.Time, done *time.Time) error {
	s.before()
	return s.inboxMemStore.PutInboxItem(ctx, id, data, updated, done)
}

func TestInboxReplyPolicyDraftDoesNotReturnReplacedText(t *testing.T) {
	h, _, it := handledHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
	{
		replyPolicyTestSetCategory(it, "ordinary_coordination")
		replyPolicyTestSetRoute(it, "owner_draft")
	}
	{
		replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
		replyPolicyTestSetPolicyRevision(it, "test-v1")
	}
	replyPolicyOrdinaryDraftModel(t, h, "Draft replaced just after its successful write")
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

func TestInboxReplyPolicyMergedContentVersionIsUnambiguous(t *testing.T) {
	// The old merged encoding appended source/truncation fields directly after Context. This ordinary Context is exactly
	// the byte sequence produced for the truncated item below, so the two different authorization states collided.
	suffix := "\x00sources\x001\x001\x00a\x00truncated\x00true"
	ordinary := &InboxItem{Source: "a", Last: "1", Context: "x" + suffix}
	truncated := &InboxItem{Source: "a", Last: "1", Context: "x", ReplyContextTruncated: true}
	if replyPolicyContentVersion(ordinary) == replyPolicyContentVersion(truncated) {
		t.Fatal("ordinary and truncated content share an authorization version through ambiguous concatenation")
	}
}

func TestInboxReplyPolicyMergedContextInvalidatesDraft(t *testing.T) {
	h, lark, it := handledHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
	// A full survivor used to clip the entire appended member away, leaving both the text and its authorization hash unchanged.
	it.Context = strings.Repeat("s", mergeCtxMax)
	{
		replyPolicyTestSetCategory(it, "ordinary_coordination")
		replyPolicyTestSetRoute(it, "owner_draft")
		replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
		replyPolicyTestSetPolicyRevision(it, "test-v1")
	}
	if _, err := suggestOrdinaryReplyForFixture(t, h, `{"kind":"reply","text":"Synthetic scheduling assent"}`); err != nil {
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
	if _, err := h.actOnSuggestion(context.Background(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm}, true); !replyPolicyDenied(err) {
		t.Fatalf("clipped restricted member retained its old authorization: %v", err)
	}
	if len(lark.got) != 0 {
		t.Fatalf("merged restricted context called sender: %+v", lark.got)
	}
	model, calls := captureModel(t, `{"category":"ordinary_coordination"}`)
	h.in.model = model
	cfg := replyPolicyTestConfig()
	for _, revision := range []string{"test-v1", "test-v2"} {
		cfg.Revision = revision
		h.classifyInboxReplyPolicies(t.Context(), cfg)
		if replyPolicyTestCategory(it) != "unknown" || replyPolicyTestRoute(it) != "owner_only" {
			t.Fatalf("clipped merge was reauthorized under %s: category=%q mode=%q", revision, replyPolicyTestCategory(it), replyPolicyTestRoute(it))
		}
	}
	if got := len(calls()); got != 0 {
		t.Fatalf("clipped merge made %d classification calls instead of failing closed", got)
	}
}

func TestInboxReplyPolicyMergePropagatesTruncation(t *testing.T) {
	t.Run("member with identical context", func(t *testing.T) {
		h, _, it := handledHub(t)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
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
		h.classifyInboxReplyPolicies(t.Context(), replyPolicyTestConfig())
		if replyPolicyTestCategory(it) != "unknown" || replyPolicyTestRoute(it) != "owner_only" || len(calls()) != 0 {
			t.Fatalf("incomplete member text regained owner-draft authorization: category=%q mode=%q calls=%d", replyPolicyTestCategory(it), replyPolicyTestRoute(it), len(calls()))
		}
	})
	t.Run("pre-upgrade clipped merged row", func(t *testing.T) {
		h, _, it := handledHub(t)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
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
		h.classifyInboxReplyPolicies(t.Context(), replyPolicyTestConfig())
		if replyPolicyTestCategory(it) != "unknown" || replyPolicyTestRoute(it) != "owner_only" || len(calls()) != 0 {
			t.Fatalf("pre-upgrade clipped row was authorized from its retained prefix: category=%q mode=%q calls=%d", replyPolicyTestCategory(it), replyPolicyTestRoute(it), len(calls()))
		}
		h.inbox.mu.Lock()
		h.inbox.unmerge(it, time.Now())
		h.inbox.mu.Unlock()
		if !it.ReplyContextTruncated {
			t.Fatal("undo discarded the inferred pre-upgrade truncation boundary")
		}
	})
}

func TestInboxReplyPolicyCrossSourceMergeKeepsMemberScope(t *testing.T) {
	newMerged := func(t *testing.T) (*inboxHub, *extensionLark, *InboxItem) {
		h, lark, it := handledHub(t)
		h.setConfig(func(c *assistantwire.InboxConfig) {
			c.ReplyPolicy = replyPolicyTestConfig()
			c.ReplyPolicy.Sources = []string{"mail"}
		})
		// The survivor itself is outside the configured source list, so this suggestion has no policy snapshot.
		if _, err := suggestOrdinaryReplyForFixture(t, h, `{"kind":"reply","text":"Synthetic cross-source owner draft"}`); err != nil {
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
		if _, err := h.actOnSuggestion(t.Context(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm}, true); !replyPolicyDenied(err) {
			t.Fatalf("cross-source merged draft confirmation error = %v", err)
		}
		if len(lark.got) != 0 {
			t.Fatalf("restricted member escaped its source policy through a merge: %+v", lark.got)
		}
	})
	t.Run("new draft cannot be published without classifying the member", func(t *testing.T) {
		h, lark, _ := newMerged(t)
		if _, err := suggestOrdinaryReplyForFixture(t, h, `{"kind":"reply","text":"Replacement cross-source owner draft"}`); !replyPolicyDenied(err) {
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
		if _, err := suggestOrdinaryReplyForFixture(t, h, `{"kind":"reply","text":"Post-undo cross-source owner draft"}`); !replyPolicyDenied(err) {
			t.Fatalf("post-undo retained member text escaped its source policy: %v", err)
		}
		if len(lark.got) != 0 {
			t.Fatalf("post-undo cross-source suggestion called sender: %+v", lark.got)
		}
	})
	t.Run("undo preserves a pre-upgrade member source", func(t *testing.T) {
		h, lark, it := handledHub(t)
		h.setConfig(func(c *assistantwire.InboxConfig) {
			c.ReplyPolicy = replyPolicyTestConfig()
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
		if _, err := suggestOrdinaryReplyForFixture(t, h, `{"kind":"reply","text":"Post-upgrade undo owner draft"}`); !replyPolicyDenied(err) {
			t.Fatalf("pre-upgrade member text lost its policy scope on undo: %v", err)
		}
		if len(lark.got) != 0 {
			t.Fatalf("pre-upgrade undo suggestion called sender: %+v", lark.got)
		}
	})
}

func TestInboxReplyPolicyLegacyDraftRejectedWhileSendWaits(t *testing.T) {
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

func TestInboxReplyPolicyDeniedEditSurvivesConcurrentDisable(t *testing.T) {
	h, lark, it := handledHub(t)
	h.in.model = fakeModel(t, `{"category":"approval_permission"}`)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
	{
		replyPolicyTestSetCategory(it, "ordinary_coordination")
		replyPolicyTestSetRoute(it, "owner_draft")
		replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
		replyPolicyTestSetPolicyRevision(it, "test-v1")
	}
	if _, err := suggestOrdinaryReplyForFixture(t, h, `{"kind":"reply","text":"Original synthetic reply"}`); err != nil {
		t.Fatal(err)
	}
	// Prime the display cache, then make the veto's fresh read enabled and the second fresh read disabled. Calling the
	// complete action proves that the post-veto read cannot discard an established denial and reach the real send path.
	h.inboxConfig(t.Context())
	h.in.mu.Lock()
	h.in.configQueue = []assistantwire.InboxConfig{{ReplyPolicy: replyPolicyTestConfig()}, {}}
	h.in.mu.Unlock()
	edited := "I approve the synthetic permission request"
	before := append([]byte(nil), itemState(it)...)
	_, err := h.actOnSuggestion(t.Context(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm, Text: &edited}, true)
	if !replyPolicyDenied(err) {
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
