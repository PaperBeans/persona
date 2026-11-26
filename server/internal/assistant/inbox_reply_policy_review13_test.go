package assistant

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/rpc"
)

func replyPolicyReview13SetSnapshot(it *InboxItem, cfg *assistantwire.InboxReplyPolicyConfig, reviewed bool) {
	it.ReplyPolicy = &InboxReplyPolicySnapshot{
		Category:       "ordinary_coordination",
		Route:          ReplyRouteOwnerDraft,
		ContentVersion: replyPolicyContentVersion(it),
		PolicyRevision: compileReplyPolicy(cfg).revision,
		Reviewed:       reviewed,
	}
}

func TestInboxReplyPolicyClassifierRejectsOtherwiseValidRouteField(t *testing.T) {
	category, reason, err := strictReplyPolicyClassifierOutput(`{"category":"ordinary_coordination","reason":"otherwise valid","route":"owner_draft"}`)
	if err == nil {
		t.Fatalf("otherwise valid classification accepted a model-supplied route: category=%q reason=%q", category, reason)
	}
	if category != "ordinary_coordination" || reason != "otherwise valid" {
		t.Fatalf("route rejection did not retain diagnostic category/reason: category=%q reason=%q err=%v", category, reason, err)
	}
}

func TestInboxReplyPolicyIndependentReviewSurvivesDurableSendAttempt(t *testing.T) {
	t.Run("successful send and reload", func(t *testing.T) {
		h, lark, it := handledHub(t)
		cfg := replyPolicyTestConfig()
		cfg.RequireIndependentReview = true
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
		replyPolicyReview13SetSnapshot(it, cfg, true)
		h.in.setModel(fakeModel(t, `{"category":"ordinary_coordination"}`))
		const text = "Synthetic reviewed reply whose durable attempt must retain review provenance"
		if _, err := suggestTool(t, h, `{"kind":"reply","text":"`+text+`"}`); err != nil {
			t.Fatalf("reviewed suggestion publication failed: %v", err)
		}
		g := currentSuggestion(it)
		if g == nil || !replyPolicyTestReviewed(g) || !replyPolicyTestReviewed(it) {
			t.Fatalf("test setup did not publish an independently reviewed suggestion: suggestion=%+v item=%+v", g, it.ReplyPolicy)
		}
		code, out := suggestionCall(t, h, it.ID, `{"version":1,"action":"confirm","client_id":"`+recoveryFirstID+`"}`)
		if code != http.StatusOK || len(lark.got) != 1 || lark.got[0].Text != text {
			t.Fatalf("independently reviewed send lost its authorization: code=%d out=%+v sends=%+v", code, out, lark.got)
		}
		a := suggestionAttempt(it, g.Version)
		if a == nil || a.Status != suggestionSent || !a.Reviewed || a.Proposal == nil || !replyPolicyTestReviewed(a.Proposal) {
			t.Fatalf("successful durable attempt lost review provenance: %+v", a)
		}

		restarted := New(Options{Hub: h.f, Store: func() Store { return h.st }, Logf: testLogf(t)})
		if err := restarted.inboxReady(t.Context()); err != nil {
			t.Fatal(err)
		}
		restarted.inbox.mu.Lock()
		loaded := restarted.inbox.items[it.ID]
		var persisted *InboxSuggestionAttempt
		if loaded != nil {
			persisted = suggestionAttempt(loaded, g.Version)
		}
		restarted.inbox.mu.Unlock()
		if persisted == nil || persisted.Status != suggestionSent || !persisted.Reviewed || persisted.Proposal == nil || !replyPolicyTestReviewed(persisted.Proposal) {
			t.Fatalf("store reload lost reviewed send authorization: %+v", persisted)
		}
	})

	t.Run("known-none rejection retains review provenance", func(t *testing.T) {
		h, lark, it := handledHub(t)
		cfg := replyPolicyTestConfig()
		cfg.RequireIndependentReview = true
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
		replyPolicyReview13SetSnapshot(it, cfg, true)
		g := finalReviewInstallMirror(h, it, "Synthetic reviewed draft rejected before dispatch")
		snap := cloneReplyPolicySnapshot(g.ReplyPolicy)
		if snap == nil || !snap.Reviewed {
			t.Fatalf("test setup did not create a reviewed proposal: %+v", snap)
		}
		h.inbox.mu.Lock()
		_, err := h.rejectReplyPolicySendLocked(t.Context(), it, g, nil, recoveryFirstID, g.Text, snap, snap.ContentVersion, snap.PolicyRevision)
		if !replyPolicyDenied(err) {
			t.Fatalf("known-none policy rejection error = %v", err)
		}
		a := suggestionAttempt(it, g.Version)
		if a == nil || a.Status != suggestionFailed || a.Retryable || a.Code != assistantwire.CodeInboxReplyPolicyBlocked || !a.Reviewed {
			t.Fatalf("rejected durable attempt lost review provenance: %+v", a)
		}
		if len(lark.got) != 0 {
			t.Fatalf("policy rejection reached the physical sender: %+v", lark.got)
		}
	})
}

func TestInboxReplyPolicyIdenticalNonOrdinaryReplacementNeedsNewReview(t *testing.T) {
	for _, tc := range []struct {
		name    string
		initial string
		replace string
	}{
		{name: "Agent", initial: `{"kind":"agent","text":"Original Agent instruction"}`, replace: `{"kind":"agent","text":"Identical instructed Agent replacement"}`},
		{name: "handback", initial: `{"kind":"reply","handback":true,"text":"Original handback"}`, replace: `{"kind":"reply","handback":true,"text":"Identical instructed handback replacement"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, lark, it := handledHub(t)
			replyPolicyReview8OrdinaryItem(h, it)
			if _, err := suggestOrdinaryReplyForFixture(t, h, tc.initial); err != nil {
				t.Fatal(err)
			}
			instruction := "Synthetic owner rewrite instruction for an identical non-ordinary replacement"
			if _, err := h.actOnSuggestion(t.Context(), it.ID, suggestionReq{Version: 1, Action: suggestInstruct, Text: &instruction}, true); err != nil {
				t.Fatal(err)
			}
			toldConv(t, h, instruction)
			model, calls := replyPolicyClassifierModel(t, func(_ int, _, _ string, _ *http.Request) (int, string) {
				return http.StatusOK, `{"category":"ordinary_coordination"}`
			})
			h.in.setModel(model)
			if _, err := suggestTool(t, h, tc.replace); err != nil {
				t.Fatalf("first instructed replacement was not published: %v", err)
			}
			before := currentSuggestion(it)
			if before == nil || replyPolicyTestReviewed(before) || calls() != 1 {
				t.Fatalf("test setup did not publish one ordinarily classified replacement: suggestion=%+v calls=%d", before, calls())
			}
			h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy.RequireIndependentReview = true })
			if _, err := suggestTool(t, h, tc.replace); err != nil {
				t.Fatalf("identical replacement failed its newly required independent review: %v", err)
			}
			after := currentSuggestion(it)
			if after == nil || !replyPolicyTestReviewed(after) || !replyPolicyTestReviewed(it) || after.Version <= before.Version {
				t.Fatalf("identical replacement did not publish reviewed authorization: before=%+v after=%+v item=%+v", before, after, it.ReplyPolicy)
			}
			if got := calls(); got != 3 {
				t.Fatalf("identical non-ordinary replacement skipped newly required review: model calls=%d want=3", got)
			}
			if len(lark.got) != 0 {
				t.Fatalf("replacement publication reached the physical sender: %+v", lark.got)
			}
		})
	}
}

func TestInboxReplyPolicyRetryCannotChangeFrozenDocChanges(t *testing.T) {
	c := &recoveryReplier{err: &rpc.Error{Code: assistantwire.CodeInboxSendFailed, Message: "synthetic retryable send failure"}}
	h, _, it := handledHub(t)
	h.in.mu.Lock()
	h.in.conns = []connector.Connector{c}
	h.in.mu.Unlock()
	it.Doc = &InboxDoc{}
	g := finalReviewInstallMirror(h, it, "Synthetic reply with a frozen document-move selection")
	first := []DocChange{{Ref: "row-a", Field: "Status", Value: "Done"}}
	_, err := h.actOnSuggestion(t.Context(), it.ID, suggestionReq{Version: g.Version, Action: suggestConfirm, Changes: &first, ClientID: recoveryFirstID}, true)
	var rpcErr *rpc.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != assistantwire.CodeInboxSendFailed || len(c.calls()) != 1 {
		t.Fatalf("test setup did not create one retryable known-none send: err=%v calls=%+v", err, c.calls())
	}
	a := suggestionAttempt(it, g.Version)
	if a == nil || a.Status != suggestionFailed || !a.Retryable || a.Proposal == nil || !slices.Equal(a.Proposal.Changes, first) {
		t.Fatalf("test setup did not freeze the first document selection: %+v", a)
	}
	second := []DocChange{{Ref: "row-b", Field: "Status", Value: "Approved"}}
	_, err = h.actOnSuggestion(t.Context(), it.ID, suggestionReq{Version: g.Version, Action: suggestConfirm, Changes: &second, ClientID: recoveryFirstID}, true)
	if !errors.Is(err, errSuggestionStale) {
		t.Fatalf("retry changed the durable attempt's DocChange selection: err=%v", err)
	}
	if got := len(c.calls()); got != 1 {
		t.Fatalf("changed-selection retry repeated the physical send: calls=%d", got)
	}
	if a = suggestionAttempt(it, g.Version); a == nil || a.Proposal == nil || !slices.Equal(a.Proposal.Changes, first) {
		t.Fatalf("changed-selection retry mutated the frozen proposal: %+v", a)
	}
}

func TestInboxReplyPolicyReviewedBitIsAnIndependentAuthorizationBinding(t *testing.T) {
	cfg := replyPolicyTestConfig()
	cfg.RequireIndependentReview = true
	policy := compileReplyPolicy(cfg)
	unreviewed := &InboxReplyPolicySnapshot{Category: "ordinary_coordination", Route: ReplyRouteOwnerDraft, ContentVersion: "synthetic-content", PolicyRevision: policy.revision}
	reviewed := cloneReplyPolicySnapshot(unreviewed)
	reviewed.Reviewed = true
	if err := policy.allowsOwnerDraft(reviewed, reviewed.ContentVersion); err != nil {
		t.Fatalf("test setup rejected a fully reviewed snapshot: %v", err)
	}
	if err := policy.allowsOwnerDraft(unreviewed, unreviewed.ContentVersion); err == nil {
		t.Fatal("review-required policy accepted a snapshot differing only by Reviewed=false")
	}
	if sameReplyPolicyBinding(unreviewed, reviewed) {
		t.Fatal("reply-policy binding ignored a Reviewed-only difference")
	}
	if !sameReplyPolicyBinding(reviewed, cloneReplyPolicySnapshot(reviewed)) {
		t.Fatal("test setup did not retain identical reviewed bindings")
	}
	withoutReview := cfg
	withoutReview.RequireIndependentReview = false
	if compileReplyPolicy(withoutReview).revision == policy.revision {
		t.Fatal("independent_review did not participate in the effective policy revision")
	}
}

func TestInboxReplyPolicyInitiallyOutOfScopeQueueStaysLegacy(t *testing.T) {
	h := agentHub(t)
	cfg := replyPolicyTestConfig()
	cfg.Sources = []string{"mail"}
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
	model, calls := replyPolicyClassifierModel(t, func(_ int, _, _ string, _ *http.Request) (int, string) {
		return http.StatusOK, `{"category":"approval_permission"}`
	})
	h.in.setModel(model)
	it := replyPolicyTestItem("lark:review13-out-of-scope-queue", "100", "Synthetic Lark request outside a mail-only policy")
	it.Agent = &InboxAgent{State: AgentQueued}
	h.inbox.items[it.ID] = it
	h.classifyInboxReplyPolicies(t.Context(), cfg)
	policy := compileReplyPolicy(cfg)
	h.inbox.mu.Lock()
	needs := h.inbox.needsReplyPolicyQueueCheckLocked(it, policy, time.Now().Add(-time.Hour))
	allowed := replyPolicyAllowsQueueLocked(it, policy)
	h.inbox.mu.Unlock()
	if needs || !allowed || it.ReplyPolicy != nil || calls() != 0 {
		t.Fatalf("initially out-of-scope queue lost legacy behavior: needs=%t allowed=%t snapshot=%+v classifier_calls=%d", needs, allowed, it.ReplyPolicy, calls())
	}
}

func TestInboxReplyPolicyPostVetoConfigOutageIsRetryableKnownNone(t *testing.T) {
	h, lark, it := handledHub(t)
	replyPolicyReview8OrdinaryItem(h, it)
	if _, err := suggestOrdinaryReplyForFixture(t, h, `{"kind":"reply","text":"Synthetic reply spanning a post-veto configuration outage"}`); err != nil {
		t.Fatal(err)
	}
	h.in.setModel(fakeModel(t, `{"category":"ordinary_coordination"}`))
	h.in.mu.Lock()
	reads := 0
	h.in.configHook = func() {
		h.in.mu.Lock()
		reads++
		if reads == 2 {
			h.in.configErr = errors.New("synthetic outage at the post-final-veto fresh read")
		}
		h.in.mu.Unlock()
	}
	h.in.mu.Unlock()
	_, err := h.actOnSuggestion(t.Context(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm, ClientID: recoveryFirstID}, true)
	var rpcErr *rpc.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != assistantwire.CodeInboxSendFailed {
		t.Fatalf("post-final-veto configuration outage error = %v, want retryable send_failed", err)
	}
	a := suggestionAttempt(it, 1)
	if a == nil || a.Status != suggestionFailed || !a.Retryable || a.Code != assistantwire.CodeInboxSendFailed {
		t.Fatalf("post-final-veto outage was not a retryable known-none attempt: %+v", a)
	}
	h.in.mu.Lock()
	gotReads := reads
	h.in.mu.Unlock()
	if gotReads != 3 {
		t.Fatalf("test outage did not isolate the post-final-veto read: fresh reads=%d want=3", gotReads)
	}
	if len(lark.got) != 0 {
		t.Fatalf("post-final-veto outage reached the physical sender: %+v", lark.got)
	}

	lang := h.lang(t.Context())
	wantReason := i18n.T(lang, "assistant-inbox_chain.failed_send")
	blockedReason := i18n.T(lang, "assistant-inbox.reply_policy_blocked")
	if wantReason == "" || wantReason == "assistant-inbox_chain.failed_send" || wantReason == blockedReason {
		t.Fatalf("failed-send reason fixture is not distinct: failed_send=%q reply_policy_blocked=%q", wantReason, blockedReason)
	}
	assertReason := func(label string, got *InboxItem) {
		t.Helper()
		var handling *InboxHandling
		if got != nil {
			handling = got.Handling
		}
		if got == nil || handling == nil || handling.State != HandlingFailed || handling.Error != wantReason || handling.Error == blockedReason {
			t.Fatalf("post-final-veto configuration outage used the policy-blocked display reason: %s handling=%+v want_failed_send=%q", label, handling, wantReason)
		}
		if len(got.Chain) == 0 {
			t.Fatalf("post-final-veto configuration outage used the policy-blocked display reason: %s chain is empty", label)
		}
		last := got.Chain[len(got.Chain)-1]
		if last.Kind != ChainFailed || last.Detail != wantReason || last.Detail == blockedReason {
			t.Fatalf("post-final-veto configuration outage used the policy-blocked display reason: %s chain=%+v want_failed_send=%q", label, last, wantReason)
		}
	}
	assertReason("live", it)

	restarted := New(Options{Hub: h.f, Store: func() Store { return h.st }, Logf: testLogf(t)})
	if err := restarted.inboxReady(t.Context()); err != nil {
		t.Fatal(err)
	}
	restarted.inbox.mu.Lock()
	loaded := restarted.inbox.items[it.ID]
	restarted.inbox.mu.Unlock()
	assertReason("persisted", loaded)
}

func TestInboxReplyPolicyContentVersionIncludesSourcesAndTruncation(t *testing.T) {
	base := replyPolicyTestItem("lark:review13-version", "100", "Synthetic content-version input")
	cfg := replyPolicyTestConfig()
	cfg.Sources = []string{"lark", "mail"}
	policy := compileReplyPolicy(cfg)
	baseVersion := replyPolicyContentVersion(base)
	if baseVersion == "" {
		t.Fatal("test setup produced an empty content version")
	}
	sourceChanged := *base
	sourceChanged.ReplyContentSources = []string{"mail"}
	if !policy.appliesToItem(base) || !policy.appliesToItem(&sourceChanged) || replyPolicyContextTruncated(base) != replyPolicyContextTruncated(&sourceChanged) {
		t.Fatal("source-only version fixture changed an applicability or truncation fence")
	}
	if got := replyPolicyContentVersion(&sourceChanged); got == baseVersion {
		t.Fatal("replyPolicyContentVersion ignored a Sources-only change")
	}
	truncated := *base
	truncated.ReplyContextTruncated = true
	if !slices.Equal(replyPolicySourceSet(base), replyPolicySourceSet(&truncated)) || policy.appliesToItem(base) != policy.appliesToItem(&truncated) {
		t.Fatal("truncation-only version fixture changed source applicability")
	}
	if got := replyPolicyContentVersion(&truncated); got == baseVersion {
		t.Fatal("replyPolicyContentVersion ignored a Truncated-only change")
	}
}

func TestInboxReplyPolicyHandledTruncatedRowStillNeedsQueueClassification(t *testing.T) {
	h := agentHub(t)
	it := replyPolicyTestItem("lark:review13-handled-truncated", "100", "Synthetic already-handled truncated row")
	it.Agent = &InboxAgent{State: AgentSkipped, SessionID: "s1", Of: it.Last}
	h.inbox.mu.Lock()
	h.inbox.items[it.ID] = it
	h.inbox.setHandling(it, HandlingDone, "", time.Now().UTC())
	policy := compileReplyPolicy(replyPolicyTestConfig())
	if h.inbox.needsReplyPolicyQueueCheckLocked(it, policy, time.Now().Add(-time.Hour)) {
		t.Fatal("test setup did not make the complete handled row ineligible for queue classification")
	}
	it.ReplyContextTruncated = true
	needs := h.inbox.needsReplyPolicyQueueCheckLocked(it, policy, time.Now().Add(-time.Hour))
	h.inbox.mu.Unlock()
	if !needs {
		t.Fatal("already-handled truncated row skipped the queue classification pass")
	}
}

func TestInboxReplyPolicyOnDemandDraftInitialConfigOutagePrecedesModel(t *testing.T) {
	h, _, it := handledHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
	model, calls := captureModel(t, "Synthetic draft that must not be generated during the initial outage")
	h.in.setModel(model)
	h.in.mu.Lock()
	h.in.configErr = errors.New("synthetic initial on-demand draft configuration outage")
	h.in.mu.Unlock()
	text, err := h.draftReply(context.Background(), it.ID, "Synthetic owner instruction")
	if !replyPolicyDenied(err) || text != "" {
		t.Fatalf("initial configuration outage did not fail closed: text=%q err=%v", text, err)
	}
	if got := len(calls()); got != 0 {
		t.Fatalf("initial configuration outage reached the draft model: calls=%d", got)
	}
	if g := currentSuggestion(it); g != nil || mirroredLegacyReply(it) != nil {
		t.Fatalf("initial configuration outage published a draft: suggestion=%+v mirror=%+v", g, mirroredLegacyReply(it))
	}
}
