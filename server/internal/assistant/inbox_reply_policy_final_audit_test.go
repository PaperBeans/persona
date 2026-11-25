package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/botcard"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/rpc"
)

// These forwarding adapters keep the integrated assistant regressions on the real Lark-module Workbench after #13404
// moved its rendering and dispatch out of the assistant package.
func TestInboxReplyPolicyReplacementReclassifiesChangedTextLegacySchema(t *testing.T) {
	h, lark, it := handledHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
	it.Context = "Synthetic original conversation behind successive replacements"
	it.ReplyCategory, it.ReplyMode = "ordinary_coordination", "owner_draft"
	it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"Original draft before owner instruction"}`); err != nil {
		t.Fatal(err)
	}
	instruction := "Synthetic owner instruction shared by successive replacements"
	if _, err := h.actOnSuggestion(t.Context(), it.ID, suggestionReq{Version: 1, Action: suggestInstruct, Text: &instruction}, true); err != nil {
		t.Fatal(err)
	}
	toldConv(t, h, instruction)
	calls := replyPolicyDraftSequenceModel(t, h, nil, `{"category":"ordinary_coordination"}`, `{"category":"approval_permission"}`)
	const ordinary = "First ordinary replacement classified under the instruction"
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"`+ordinary+`"}`); err != nil {
		t.Fatalf("first instructed replacement was not published: %v", err)
	}
	const restricted = "Changed replacement that requests approval permission"
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"`+restricted+`"}`); !errors.Is(err, errReplyPolicyDenied) {
		t.Errorf("changed replacement reused the previous text's generated-text authorization: %v", err)
	}
	got := calls()
	if len(got) != 2 || got[1].System != prompts.RenderJSONOnly(prompts.RenderInboxReplyPolicy()) {
		t.Fatalf("changed replacement did not receive its own generated-text classification: %+v", got)
	}
	if !strings.Contains(got[1].User, restricted) || !strings.Contains(got[1].User, instruction) {
		t.Fatalf("replacement classifier did not receive the changed text and instruction: %+v", got[1])
	}
	if g := currentSuggestion(it); g == nil || g.Text != ordinary {
		t.Fatalf("restricted replacement displaced the previously authorized draft: %+v", g)
	}
	if len(lark.got) != 0 {
		t.Fatalf("restricted replacement reached the sender: %+v", lark.got)
	}
}

func TestInboxReplyPolicyUnknownAttemptOutranksPolicyFailureLegacySchema(t *testing.T) {
	c := &recoveryReplier{err: rpc.ErrDropped}
	h, _, it := handledHub(t)
	h.in.mu.Lock()
	h.in.conns[0] = c
	h.in.mu.Unlock()
	h.setConfig(func(cfg *assistantwire.InboxConfig) { cfg.ReplyPolicy = legacyReplyPolicyTestConfig() })
	it.ReplyCategory, it.ReplyMode = "ordinary_coordination", "owner_draft"
	it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"Synthetic send whose outcome remains unknown"}`); err != nil {
		t.Fatal(err)
	}
	code, out := suggestionCall(t, h, it.ID, `{"version":1,"action":"confirm","client_id":"`+recoveryFirstID+`"}`)
	if code != http.StatusBadGateway || out["delivered"] != "unknown" || len(c.calls()) != 1 {
		t.Fatalf("test setup did not create one uncertain send: code=%d out=%+v calls=%+v", code, out, c.calls())
	}

	// Reconstruct from the actual store before making fresh configuration reads fail. An in-memory-only retry would
	// not prove that the durable uncertain outcome survives a restart and still outranks a later policy outage.
	restarted := New(Options{Hub: h.f, Store: func() Store { return h.st }, Logf: testLogf(t)})
	if err := restarted.inboxReady(t.Context()); err != nil {
		t.Fatal(err)
	}
	restarted.inbox.mu.Lock()
	loaded := restarted.inbox.items[it.ID]
	var persisted *InboxSuggestionAttempt
	if loaded != nil {
		persisted = suggestionAttempt(loaded, 1)
	}
	restarted.inbox.mu.Unlock()
	if persisted == nil || persisted.Status != suggestionUnknown {
		t.Fatalf("uncertain outcome was not persisted through reload: %+v", persisted)
	}
	c.answer(nil)
	h.in.mu.Lock()
	h.in.configErr = errors.New("controlled policy configuration outage")
	h.in.mu.Unlock()
	code, out = inboxCall(t, restarted, "POST", "/api/v1/inbox/"+it.ID+"/suggestion",
		`{"version":1,"action":"confirm","client_id":"`+recoveryFreshID+`"}`)
	if code != http.StatusBadGateway || out["delivered"] != "unknown" || out["code"] != assistantwire.CodeAssistantInDoubt {
		t.Errorf("policy failure hid the persisted uncertain outcome: code=%d out=%+v", code, out)
	}
	if got := len(c.calls()); got != 1 {
		t.Fatalf("policy-failure retry repeated the uncertain physical send after reload: calls=%d", got)
	}
}

func replyPolicyWorkbenchActionValue(t *testing.T, raw, op string) map[string]any {
	t.Helper()
	var decoded any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("decode Workbench card: %v", err)
	}
	var visit func(any) map[string]any
	visit = func(v any) map[string]any {
		switch x := v.(type) {
		case map[string]any:
			if x["op"] == op {
				return x
			}
			for _, child := range x {
				if found := visit(child); found != nil {
					return found
				}
			}
		case []any:
			for _, child := range x {
				if found := visit(child); found != nil {
					return found
				}
			}
		}
		return nil
	}
	if action := visit(decoded); action != nil {
		return action
	}
	t.Fatalf("Workbench card has no %s action: %s", op, raw)
	return nil
}

func replyPolicyEnableWorkbench(h *inboxHub) chan struct{} {
	h.f.answerApp(AppFeishu, FeishuStatusGet, func(json.RawMessage) (any, error) {
		return FeishuStatus{Ready: true, Enabled: true, Owner: "synthetic-owner", Features: workbenchOnFeatures}, nil
	})
	updated := make(chan struct{}, 1)
	h.f.answerApp(AppFeishu, FeishuUpdateCard, func(json.RawMessage) (any, error) {
		updated <- struct{}{}
		return map[string]any{}, nil
	})
	return updated
}

func TestInboxReplyPolicyWorkbenchStaleRedraftRejectedLegacySchema(t *testing.T) {
	c := &recoveryReplier{}
	h, _, it := handledHub(t)
	h.in.mu.Lock()
	h.in.conns[0] = c
	h.in.mu.Unlock()
	h.setConfig(func(cfg *assistantwire.InboxConfig) { cfg.ReplyPolicy = legacyReplyPolicyTestConfig() })
	it.ReplyCategory, it.ReplyMode = "ordinary_coordination", "owner_draft"
	it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
	updated := replyPolicyEnableWorkbench(h)
	const oldText = "Draft rendered on Workbench card version one"
	finalReviewInstallMirror(h, it, oldText)
	actionValue := replyPolicyWorkbenchActionValue(t, h.draftCard(context.Background(), *it, oldText, nil, "").JSON(), "redraft")
	const replacement = "Replacement published before the stale redraft arrived"
	finalReviewInstallMirror(h, it, replacement)
	const generated = "Draft generated from the matching Workbench card"
	calls := replyPolicyDraftSequenceModel(t, h, nil, generated, `{"category":"ordinary_coordination"}`)
	action := &botcard.CardAction{}
	action.Action.Value = actionValue
	action.Action.FormValue = map[string]any{"text": oldText, "instruction": "Synthetic stale redraft instruction"}
	action.Context.MessageID = "synthetic-stale-redraft-card"
	h.workbenchInboxAction(context.Background(), action)
	select {
	case <-updated:
	case <-time.After(3 * time.Second):
		t.Fatal("stale Workbench redraft did not finish")
	}
	if got := len(calls()); got != 0 {
		t.Errorf("stale Workbench redraft invoked generation or classification %d times", got)
	}
	if g := currentSuggestion(it); g == nil || g.Version != 2 || g.Text != replacement {
		t.Fatalf("stale Workbench redraft replaced the current draft: %+v", g)
	}
	if len(c.calls()) != 0 {
		t.Fatalf("stale Workbench redraft reached the sender: %+v", c.calls())
	}

	// A current rendered card must still redraft. Without this positive control, rejecting every redraft as stale
	// would satisfy the negative assertions above without preserving the Workbench operation at all.
	matchingValue := replyPolicyWorkbenchActionValue(t, h.draftCard(context.Background(), *it, replacement, nil, "").JSON(), "redraft")
	matching := &botcard.CardAction{}
	matching.Action.Value = matchingValue
	matching.Action.FormValue = map[string]any{"text": replacement, "instruction": "Synthetic matching-version redraft instruction"}
	matching.Context.MessageID = "synthetic-matching-redraft-card"
	h.workbenchInboxAction(context.Background(), matching)
	select {
	case <-updated:
	case <-time.After(3 * time.Second):
		t.Fatal("matching Workbench redraft did not finish")
	}
	if got := len(calls()); got != 2 {
		t.Errorf("matching Workbench redraft made %d model calls, want generation and classification", got)
	}
	if g := currentSuggestion(it); g == nil || g.Version != 3 || g.Text != generated {
		t.Fatalf("matching Workbench redraft did not publish its generated replacement: %+v", g)
	}
	if len(c.calls()) != 0 {
		t.Fatalf("matching Workbench redraft unexpectedly reached the sender: %+v", c.calls())
	}
}

type replyPolicyDraftOneShotFailStore struct {
	*inboxMemStore
	before func()
	fail   bool
}

func (s *replyPolicyDraftOneShotFailStore) PutInboxItem(ctx context.Context, id string, data []byte, updated time.Time, done *time.Time) error {
	if s.fail {
		s.fail = false
		s.before()
		return errors.New("controlled one-shot on-demand draft write failure")
	}
	return s.inboxMemStore.PutInboxItem(ctx, id, data, updated, done)
}

func TestInboxReplyPolicyFailedDraftCannotSendAfterStoreRecoveryLegacySchema(t *testing.T) {
	newFailedDraft := func(t *testing.T) (*inboxHub, *extensionLark, *InboxItem) {
		t.Helper()
		h, lark, it := handledHub(t)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
		it.ReplyCategory, it.ReplyMode = "ordinary_coordination", "owner_draft"
		it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
		const generated = "Draft whose only publication write fails once"
		h.in.model = fakeModel(t, generated)
		st := &replyPolicyDraftOneShotFailStore{inboxMemStore: h.st, fail: true}
		st.before = func() {
			h.inbox.mu.Lock()
			h.inbox.touched(it.ID)
			h.inbox.mu.Unlock()
		}
		h.store = func() Store { return st }
		text, err := h.draftReply(t.Context(), it.ID, "")
		if err == nil || text != "" {
			t.Fatalf("failed publication returned text: text=%q err=%v", text, err)
		}
		return h, lark, it
	}
	// Each route gets a fresh failed publication. A broken send in one subtest therefore cannot consume the mirror or
	// create an attempt that hides an independent bypass in another route or after policy exit.
	for _, tc := range []struct {
		name       string
		suggestion bool
		policy     bool
		clientID   string
	}{
		{name: "reply_with_policy", clientID: recoveryFreshID, policy: true},
		{name: "suggestion_with_policy", suggestion: true, clientID: "10000000-0000-4000-8000-000000000003", policy: true},
		{name: "reply_after_policy_exit", clientID: "10000000-0000-4000-8000-000000000004"},
		{name: "suggestion_after_policy_exit", suggestion: true, clientID: "10000000-0000-4000-8000-000000000005"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, lark, it := newFailedDraft(t)
			g := mirroredLegacyReply(it)
			if g == nil {
				t.Fatal("test setup did not retain the unpublished mirror")
			}
			if !tc.policy {
				h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = nil })
			}
			what, when := "old-client mirror", "after the store recovered"
			if tc.suggestion {
				what = "suggestion"
			}
			if !tc.policy {
				what, when = "mirror", "after policy exit"
				if tc.suggestion {
					what = "suggestion"
				}
			}
			if tc.suggestion {
				code, _ := suggestionCall(t, h, it.ID, `{"version":1,"action":"confirm","client_id":"`+tc.clientID+`"}`)
				if code < 400 {
					t.Errorf("unpublished %s confirmed %s", what, when)
				}
			} else {
				_, err := h.replyInboxChecked(t.Context(), it.ID, g.Text, tc.clientID, false, nil, nil)
				if err == nil {
					t.Errorf("unpublished %s confirmed %s", what, when)
				}
			}
			if len(lark.got) != 0 {
				t.Fatalf("unpublished draft reached the sender through %s %s: %+v", tc.name, when, lark.got)
			}
		})
	}
}

func TestInboxReplyPolicyFailedDraftReplacementIsFreshAuthorizationLegacySchema(t *testing.T) {
	h, lark, it := handledHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
	it.ReplyCategory, it.ReplyMode = "ordinary_coordination", "owner_draft"
	it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
	const generated = "Republished draft whose first publication failed"
	h.in.model = fakeModel(t, generated)
	st := &replyPolicyDraftOneShotFailStore{inboxMemStore: h.st, fail: true}
	st.before = func() {
		h.inbox.mu.Lock()
		h.inbox.touched(it.ID)
		h.inbox.mu.Unlock()
	}
	h.store = func() Store { return st }
	if text, err := h.draftReply(t.Context(), it.ID, ""); err == nil || text != "" {
		t.Fatalf("failed publication returned text: text=%q err=%v", text, err)
	}
	if g := mirroredLegacyReply(it); g == nil || !g.ReplyPublicationFailed {
		t.Fatalf("test setup did not retain the failed-publication tombstone: %+v", g)
	}
	if len(lark.got) != 0 {
		t.Fatalf("failed publication reached the sender: %+v", lark.got)
	}
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"`+generated+`"}`); err != nil {
		t.Fatalf("fresh replacement was rejected: %v", err)
	}
	g := currentSuggestion(it)
	if g == nil || g.Version < 2 || g.Text != generated || g.ReplyPublicationFailed {
		t.Fatalf("fresh replacement coalesced with the failed publication: %+v", g)
	}
	if _, err := h.actOnSuggestion(t.Context(), it.ID, suggestionReq{Version: g.Version, Action: suggestConfirm, ClientID: recoveryFreshID}, true); err != nil {
		t.Fatalf("freshly authorized replacement could not be confirmed: %v", err)
	}
	if len(lark.got) != 1 || lark.got[0].Text != generated {
		t.Fatalf("freshly authorized replacement did not send exactly once: %+v", lark.got)
	}
}

func TestInboxReplyPolicyWorkbenchRenderedCardSendLegacySchema(t *testing.T) {
	c := &recoveryReplier{}
	h, _, it := handledHub(t)
	h.in.mu.Lock()
	h.in.conns[0] = c
	h.in.mu.Unlock()
	h.setConfig(func(cfg *assistantwire.InboxConfig) { cfg.ReplyPolicy = legacyReplyPolicyTestConfig() })
	it.ReplyCategory, it.ReplyMode = "ordinary_coordination", "owner_draft"
	it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
	updated := replyPolicyEnableWorkbench(h)
	const text = "Synthetic rendered Workbench draft accepted by its real callback"
	finalReviewInstallMirror(h, it, text)
	actionValue := replyPolicyWorkbenchActionValue(t, h.draftCard(context.Background(), *it, text, nil, "").JSON(), "send")
	action := &botcard.CardAction{}
	action.Action.Value = actionValue
	action.Action.FormValue = map[string]any{"text": text}
	action.Context.MessageID = "synthetic-matching-workbench-card"
	h.workbenchInboxAction(context.Background(), action)
	select {
	case <-updated:
	case <-time.After(3 * time.Second):
		t.Fatal("matching Workbench send did not finish")
	}
	calls := c.calls()
	if len(calls) != 1 || calls[0].Text != text {
		t.Fatalf("rendered Workbench card did not send through its real callback: %+v", calls)
	}
}

func TestInboxReplyPolicyOmittedSourcesCoverAllStagesLegacySchema(t *testing.T) {
	policy := func() *assistantwire.InboxReplyPolicyConfig {
		cfg := legacyReplyPolicyTestConfig()
		cfg.Sources = nil
		return cfg
	}
	t.Run("queue", func(t *testing.T) {
		h := agentHub(t)
		cfg := policy()
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
		it := actionItem("lark:omitted-sources-queue", time.Now())
		it.Source = "lark"
		it.ReplyCategory, it.ReplyMode = "approval_permission", "owner_only"
		it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
		it.Agent = &InboxAgent{State: AgentQueued}
		h.inbox.items[it.ID] = it
		before := len(conversationStarts(h.f))
		h.startInboxAgents(context.Background(), InboxAgentConfig{Enabled: true})
		if got := len(conversationStarts(h.f)); got != before {
			t.Errorf("omitted sources disabled queue authorization: %d conversations started", got-before)
		}
		checkPolicyReleased(t, it, h.inbox.userLang())
	})
	t.Run("suggestion", func(t *testing.T) {
		h, _, it := handledHub(t)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = policy() })
		it.ReplyCategory, it.ReplyMode = "approval_permission", "owner_only"
		it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
		if _, err := suggestTool(t, h, `{"kind":"reply","text":"Restricted draft with omitted policy sources"}`); !errors.Is(err, errReplyPolicyDenied) {
			t.Errorf("omitted sources disabled suggestion authorization: %v", err)
		}
		if currentSuggestion(it) != nil {
			t.Fatalf("restricted suggestion was published with omitted sources: %+v", currentSuggestion(it))
		}
	})
	t.Run("send", func(t *testing.T) {
		h, lark, it := handledHub(t)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = policy() })
		it.ReplyCategory, it.ReplyMode = "approval_permission", "owner_only"
		it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
		g := finalReviewInstallMirror(h, it, "Restricted mirror with omitted policy sources")
		if _, err := h.replyInboxChecked(t.Context(), it.ID, g.Text, recoveryFreshID, false, nil, nil); !errors.Is(err, errReplyPolicyDenied) {
			t.Errorf("omitted sources disabled final-send authorization: %v", err)
		}
		if len(lark.got) != 0 {
			t.Fatalf("omitted sources allowed a restricted physical send: %+v", lark.got)
		}
	})
}

func TestInboxReplyPolicyReplacementReclassifiesChangedText(t *testing.T) {
	h, lark, it := handledHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
	it.Context = "Synthetic original conversation behind successive replacements"
	{
		replyPolicyTestSetCategory(it, "ordinary_coordination")
		replyPolicyTestSetRoute(it, "owner_draft")
	}
	{
		replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
		replyPolicyTestSetPolicyRevision(it, "test-v1")
	}
	if _, err := suggestOrdinaryReplyForFixture(t, h, `{"kind":"reply","text":"Original draft before owner instruction"}`); err != nil {
		t.Fatal(err)
	}
	instruction := "Synthetic owner instruction shared by successive replacements"
	if _, err := h.actOnSuggestion(t.Context(), it.ID, suggestionReq{Version: 1, Action: suggestInstruct, Text: &instruction}, true); err != nil {
		t.Fatal(err)
	}
	toldConv(t, h, instruction)
	calls := replyPolicyDraftSequenceModel(t, h, nil, `{"category":"ordinary_coordination"}`, `{"category":"approval_permission"}`)
	const ordinary = "First ordinary replacement classified under the instruction"
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"`+ordinary+`"}`); err != nil {
		t.Fatalf("first instructed replacement was not published: %v", err)
	}
	const restricted = "Changed replacement that requests approval permission"
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"`+restricted+`"}`); !replyPolicyDenied(err) {
		t.Errorf("changed replacement reused the previous text's generated-text authorization: %v", err)
	}
	got := calls()
	if len(got) != 2 || got[1].System != replyPolicyTestClassifierSystem(h, replyPolicyTestConfig()) {
		t.Fatalf("changed replacement did not receive its own generated-text classification: %+v", got)
	}
	if !strings.Contains(got[1].User, restricted) || !strings.Contains(got[1].User, instruction) {
		t.Fatalf("replacement classifier did not receive the changed text and instruction: %+v", got[1])
	}
	if g := currentSuggestion(it); g == nil || g.Text != ordinary {
		t.Fatalf("restricted replacement displaced the previously authorized draft: %+v", g)
	}
	if len(lark.got) != 0 {
		t.Fatalf("restricted replacement reached the sender: %+v", lark.got)
	}
}

func TestInboxReplyPolicyUnknownAttemptOutranksPolicyFailure(t *testing.T) {
	c := &recoveryReplier{err: rpc.ErrDropped}
	h, _, it := handledHub(t)
	h.in.mu.Lock()
	h.in.conns[0] = c
	h.in.mu.Unlock()
	h.setConfig(func(cfg *assistantwire.InboxConfig) { cfg.ReplyPolicy = replyPolicyTestConfig() })
	{
		replyPolicyTestSetCategory(it, "ordinary_coordination")
		replyPolicyTestSetRoute(it, "owner_draft")
	}
	{
		replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
		replyPolicyTestSetPolicyRevision(it, "test-v1")
	}
	if _, err := suggestOrdinaryReplyForFixture(t, h, `{"kind":"reply","text":"Synthetic send whose outcome remains unknown"}`); err != nil {
		t.Fatal(err)
	}
	h.in.setModel(fakeModel(t, `{"category":"ordinary_coordination"}`))
	code, out := suggestionCall(t, h, it.ID, `{"version":1,"action":"confirm","client_id":"`+recoveryFirstID+`"}`)
	if code != http.StatusBadGateway || out["delivered"] != "unknown" || len(c.calls()) != 1 {
		t.Fatalf("test setup did not create one uncertain send: code=%d out=%+v calls=%+v", code, out, c.calls())
	}

	// Reconstruct from the actual store before making fresh configuration reads fail. An in-memory-only retry would
	// not prove that the durable uncertain outcome survives a restart and still outranks a later policy outage.
	restarted := New(Options{Hub: h.f, Store: func() Store { return h.st }, Logf: testLogf(t)})
	if err := restarted.inboxReady(t.Context()); err != nil {
		t.Fatal(err)
	}
	restarted.inbox.mu.Lock()
	loaded := restarted.inbox.items[it.ID]
	var persisted *InboxSuggestionAttempt
	if loaded != nil {
		persisted = suggestionAttempt(loaded, 1)
	}
	restarted.inbox.mu.Unlock()
	if persisted == nil || persisted.Status != suggestionUnknown {
		t.Fatalf("uncertain outcome was not persisted through reload: %+v", persisted)
	}
	c.answer(nil)
	h.in.mu.Lock()
	h.in.configErr = errors.New("controlled policy configuration outage")
	h.in.mu.Unlock()
	code, out = inboxCall(t, restarted, "POST", "/api/v1/inbox/"+it.ID+"/suggestion",
		`{"version":1,"action":"confirm","client_id":"`+recoveryFreshID+`"}`)
	if code != http.StatusBadGateway || out["delivered"] != "unknown" || out["code"] != assistantwire.CodeAssistantInDoubt {
		t.Errorf("policy failure hid the persisted uncertain outcome: code=%d out=%+v", code, out)
	}
	if got := len(c.calls()); got != 1 {
		t.Fatalf("policy-failure retry repeated the uncertain physical send after reload: calls=%d", got)
	}
}

func TestInboxReplyPolicyWorkbenchStaleRedraftRejected(t *testing.T) {
	needsLarkLayer(t) // the integrated Workbench renderer and actions are the lark module's
	c := &recoveryReplier{}
	h, _, it := handledHub(t)
	h.in.mu.Lock()
	h.in.conns[0] = c
	h.in.mu.Unlock()
	h.setConfig(func(cfg *assistantwire.InboxConfig) { cfg.ReplyPolicy = replyPolicyTestConfig() })
	{
		replyPolicyTestSetCategory(it, "ordinary_coordination")
		replyPolicyTestSetRoute(it, "owner_draft")
	}
	{
		replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
		replyPolicyTestSetPolicyRevision(it, "test-v1")
	}
	updated := replyPolicyEnableWorkbench(h)
	const oldText = "Draft rendered on Workbench card version one"
	finalReviewInstallMirror(h, it, oldText)
	actionValue := replyPolicyWorkbenchActionValue(t, h.draftCard(context.Background(), *it, oldText, nil, "").JSON(), "redraft")
	const replacement = "Replacement published before the stale redraft arrived"
	finalReviewInstallMirror(h, it, replacement)
	const generated = "Draft generated from the matching Workbench card"
	calls := replyPolicyDraftSequenceModel(t, h, nil, `{"category":"ordinary_coordination"}`, generated, `{"category":"ordinary_coordination"}`)
	action := &botcard.CardAction{}
	action.Action.Value = actionValue
	action.Action.FormValue = map[string]any{"text": oldText, "instruction": "Synthetic stale redraft instruction"}
	action.Context.MessageID = "synthetic-stale-redraft-card"
	h.workbenchInboxAction(context.Background(), action)
	select {
	case <-updated:
	case <-time.After(3 * time.Second):
		t.Fatal("stale Workbench redraft did not finish")
	}
	if got := len(calls()); got != 0 {
		t.Errorf("stale Workbench redraft invoked generation or classification %d times", got)
	}
	if g := currentSuggestion(it); g == nil || g.Version != 2 || g.Text != replacement {
		t.Fatalf("stale Workbench redraft replaced the current draft: %+v", g)
	}
	if len(c.calls()) != 0 {
		t.Fatalf("stale Workbench redraft reached the sender: %+v", c.calls())
	}

	// A current rendered card must still redraft. Without this positive control, rejecting every redraft as stale
	// would satisfy the negative assertions above without preserving the Workbench operation at all.
	matchingValue := replyPolicyWorkbenchActionValue(t, h.draftCard(context.Background(), *it, replacement, nil, "").JSON(), "redraft")
	matching := &botcard.CardAction{}
	matching.Action.Value = matchingValue
	matching.Action.FormValue = map[string]any{"text": replacement, "instruction": "Synthetic matching-version redraft instruction"}
	matching.Context.MessageID = "synthetic-matching-redraft-card"
	h.workbenchInboxAction(context.Background(), matching)
	select {
	case <-updated:
	case <-time.After(3 * time.Second):
		t.Fatal("matching Workbench redraft did not finish")
	}
	if got := len(calls()); got != 3 {
		t.Errorf("matching Workbench redraft made %d model calls, want initial classification, generation, and generated-text classification", got)
	}
	if g := currentSuggestion(it); g == nil || g.Version != 3 || g.Text != generated {
		t.Fatalf("matching Workbench redraft did not publish its generated replacement: %+v", g)
	}
	if len(c.calls()) != 0 {
		t.Fatalf("matching Workbench redraft unexpectedly reached the sender: %+v", c.calls())
	}
}

func TestInboxReplyPolicyFailedDraftCannotSendAfterStoreRecovery(t *testing.T) {
	newFailedDraft := func(t *testing.T) (*inboxHub, *extensionLark, *InboxItem) {
		t.Helper()
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
		const generated = "Draft whose only publication write fails once"
		replyPolicyOrdinaryDraftModel(t, h, generated)
		st := &replyPolicyDraftOneShotFailStore{inboxMemStore: h.st, fail: true}
		st.before = func() {
			h.inbox.mu.Lock()
			h.inbox.touched(it.ID)
			h.inbox.mu.Unlock()
		}
		h.store = func() Store { return st }
		text, err := h.draftReply(t.Context(), it.ID, "")
		if err == nil || text != "" {
			t.Fatalf("failed publication returned text: text=%q err=%v", text, err)
		}
		return h, lark, it
	}
	// Each route gets a fresh failed publication. A broken send in one subtest therefore cannot consume the mirror or
	// create an attempt that hides an independent bypass in another route or after policy exit.
	for _, tc := range []struct {
		name       string
		suggestion bool
		policy     bool
		clientID   string
	}{
		{name: "reply_with_policy", clientID: recoveryFreshID, policy: true},
		{name: "suggestion_with_policy", suggestion: true, clientID: "10000000-0000-4000-8000-000000000003", policy: true},
		{name: "reply_after_policy_exit", clientID: "10000000-0000-4000-8000-000000000004"},
		{name: "suggestion_after_policy_exit", suggestion: true, clientID: "10000000-0000-4000-8000-000000000005"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, lark, it := newFailedDraft(t)
			g := mirroredLegacyReply(it)
			if g == nil {
				t.Fatal("test setup did not retain the unpublished mirror")
			}
			if !tc.policy {
				h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = &assistantwire.InboxReplyPolicyConfig{} })
			}
			what, when := "old-client mirror", "after the store recovered"
			if tc.suggestion {
				what = "suggestion"
			}
			if !tc.policy {
				what, when = "mirror", "after policy exit"
				if tc.suggestion {
					what = "suggestion"
				}
			}
			if tc.suggestion {
				code, _ := suggestionCall(t, h, it.ID, `{"version":1,"action":"confirm","client_id":"`+tc.clientID+`"}`)
				if code < 400 {
					t.Errorf("unpublished %s confirmed %s", what, when)
				}
			} else {
				_, err := h.replyInboxChecked(t.Context(), it.ID, g.Text, tc.clientID, false, nil, nil)
				if err == nil {
					t.Errorf("unpublished %s confirmed %s", what, when)
				}
			}
			if len(lark.got) != 0 {
				t.Fatalf("unpublished draft reached the sender through %s %s: %+v", tc.name, when, lark.got)
			}
		})
	}
}

func TestInboxReplyPolicyFailedDraftReplacementIsFreshAuthorization(t *testing.T) {
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
	const generated = "Republished draft whose first publication failed"
	replyPolicyOrdinaryDraftModel(t, h, generated)
	st := &replyPolicyDraftOneShotFailStore{inboxMemStore: h.st, fail: true}
	st.before = func() {
		h.inbox.mu.Lock()
		h.inbox.touched(it.ID)
		h.inbox.mu.Unlock()
	}
	h.store = func() Store { return st }
	if text, err := h.draftReply(t.Context(), it.ID, ""); err == nil || text != "" {
		t.Fatalf("failed publication returned text: text=%q err=%v", text, err)
	}
	if g := mirroredLegacyReply(it); g == nil || !g.ReplyPublicationFailed {
		t.Fatalf("test setup did not retain the failed-publication tombstone: %+v", g)
	}
	if len(lark.got) != 0 {
		t.Fatalf("failed publication reached the sender: %+v", lark.got)
	}
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"`+generated+`"}`); err != nil {
		t.Fatalf("fresh replacement was rejected: %v", err)
	}
	g := currentSuggestion(it)
	if g == nil || g.Version < 2 || g.Text != generated || g.ReplyPublicationFailed {
		t.Fatalf("fresh replacement coalesced with the failed publication: %+v", g)
	}
	if _, err := h.actOnSuggestion(t.Context(), it.ID, suggestionReq{Version: g.Version, Action: suggestConfirm, ClientID: recoveryFreshID}, true); err != nil {
		t.Fatalf("freshly authorized replacement could not be confirmed: %v", err)
	}
	if len(lark.got) != 1 || lark.got[0].Text != generated {
		t.Fatalf("freshly authorized replacement did not send exactly once: %+v", lark.got)
	}
}

func TestInboxReplyPolicyWorkbenchRenderedCardSend(t *testing.T) {
	needsLarkLayer(t) // the integrated Workbench renderer and actions are the lark module's
	c := &recoveryReplier{}
	h, _, it := handledHub(t)
	h.in.mu.Lock()
	h.in.conns[0] = c
	h.in.mu.Unlock()
	h.setConfig(func(cfg *assistantwire.InboxConfig) { cfg.ReplyPolicy = replyPolicyTestConfig() })
	{
		replyPolicyTestSetCategory(it, "ordinary_coordination")
		replyPolicyTestSetRoute(it, "owner_draft")
	}
	{
		replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
		replyPolicyTestSetPolicyRevision(it, "test-v1")
	}
	updated := replyPolicyEnableWorkbench(h)
	const text = "Synthetic rendered Workbench draft accepted by its real callback"
	finalReviewInstallMirror(h, it, text)
	actionValue := replyPolicyWorkbenchActionValue(t, h.draftCard(context.Background(), *it, text, nil, "").JSON(), "send")
	action := &botcard.CardAction{}
	action.Action.Value = actionValue
	action.Action.FormValue = map[string]any{"text": text}
	action.Context.MessageID = "synthetic-matching-workbench-card"
	h.in.setModel(fakeModel(t, `{"category":"ordinary_coordination"}`))
	h.workbenchInboxAction(context.Background(), action)
	select {
	case <-updated:
	case <-time.After(3 * time.Second):
		t.Fatal("matching Workbench send did not finish")
	}
	calls := c.calls()
	if len(calls) != 1 || calls[0].Text != text {
		t.Fatalf("rendered Workbench card did not send through its real callback: %+v", calls)
	}
}

func TestInboxReplyPolicyOmittedSourcesCoverAllStages(t *testing.T) {
	policy := func() *assistantwire.InboxReplyPolicyConfig {
		cfg := replyPolicyTestConfig()
		cfg.Sources = nil
		return cfg
	}
	t.Run("queue", func(t *testing.T) {
		h := agentHub(t)
		cfg := policy()
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
		it := actionItem("lark:omitted-sources-queue", time.Now())
		it.Source = "lark"
		{
			replyPolicyTestSetCategory(it, "approval_permission")
			replyPolicyTestSetRoute(it, "owner_only")
		}
		{
			replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
			replyPolicyTestSetPolicyRevision(it, "test-v1")
		}
		it.Agent = &InboxAgent{State: AgentQueued}
		h.inbox.items[it.ID] = it
		before := len(conversationStarts(h.f))
		h.startInboxAgents(context.Background(), InboxAgentConfig{Enabled: true})
		if got := len(conversationStarts(h.f)); got != before {
			t.Errorf("omitted sources disabled queue authorization: %d conversations started", got-before)
		}
		checkReplyPolicyReleased(t, it, h.inbox.userLang())
	})
	t.Run("suggestion", func(t *testing.T) {
		h, _, it := handledHub(t)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = policy() })
		{
			replyPolicyTestSetCategory(it, "approval_permission")
			replyPolicyTestSetRoute(it, "owner_only")
		}
		{
			replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
			replyPolicyTestSetPolicyRevision(it, "test-v1")
		}
		if _, err := suggestTool(t, h, `{"kind":"reply","text":"Restricted draft with omitted policy sources"}`); !replyPolicyDenied(err) {
			t.Errorf("omitted sources disabled suggestion authorization: %v", err)
		}
		if currentSuggestion(it) != nil {
			t.Fatalf("restricted suggestion was published with omitted sources: %+v", currentSuggestion(it))
		}
	})
	t.Run("send", func(t *testing.T) {
		h, lark, it := handledHub(t)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = policy() })
		{
			replyPolicyTestSetCategory(it, "approval_permission")
			replyPolicyTestSetRoute(it, "owner_only")
		}
		{
			replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
			replyPolicyTestSetPolicyRevision(it, "test-v1")
		}
		g := finalReviewInstallMirror(h, it, "Restricted mirror with omitted policy sources")
		if _, err := h.replyInboxChecked(t.Context(), it.ID, g.Text, recoveryFreshID, false, nil, nil); !replyPolicyDenied(err) {
			t.Errorf("omitted sources disabled final-send authorization: %v", err)
		}
		if len(lark.got) != 0 {
			t.Fatalf("omitted sources allowed a restricted physical send: %+v", lark.got)
		}
	})
}
