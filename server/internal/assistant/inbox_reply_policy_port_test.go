package assistant

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
)

// replyPolicyDenied accepts either schema's typed refusal as well as the send path's sentinel error.
// A generic suggestion rejection does not match: the refusal must still be specifically attributable to reply policy.
func replyPolicyDenied(err error) bool {
	if errors.Is(err, errReplyPolicyDenied) || errors.Is(err, errReplyPolicyBlocked) {
		return true
	}
	var refusal errSuggestion
	return errors.As(err, &refusal) && strings.Contains(refusal.msg, "reply policy")
}

const replyPolicyIndependentReviewPrompt = independentReplyPolicyReviewPrompt

func checkReplyPolicyReleased(t *testing.T, it *InboxItem, lang i18n.Lang) {
	t.Helper()
	if it == nil {
		t.Fatal("released item is missing")
	}
	want := i18n.T(lang, "assistant-inbox_agent.reply_policy_blocked")
	if want == "" || want == "assistant-inbox_agent.reply_policy_blocked" {
		t.Fatal("the reply-policy release reason is missing from the catalog")
	}
	if !it.open() || it.handled() || it.status() != it.Tier || it.Handling == nil || it.Handling.State != HandlingDone || it.Handling.Error != "" {
		t.Fatalf("item was not returned to its tier: tier=%s status=%s handling=%+v", it.Tier, it.status(), it.Handling)
	}
	if a := it.Agent; a == nil || a.State != AgentSkipped || a.SessionID != "" || a.Error != "" || len(a.Pending) != 0 || a.Wake || a.Back != "" || a.RetryAfter != nil {
		t.Fatalf("released item kept queued work: %+v", a)
	}
	if len(it.Chain) == 0 || it.Chain[len(it.Chain)-1].Kind != ChainConcluded || it.Chain[len(it.Chain)-1].Text != want {
		t.Fatalf("missing policy explanation: %+v", it.Chain)
	}
}

// suggestOrdinaryReplyForFixture supplies the ordinary classification needed while constructing an initial suggestion.
// The prior model is restored before the operation under test, whose own classifier and generation fixtures are unchanged.
func suggestOrdinaryReplyForFixture(t *testing.T, h *inboxHub, args string) (string, error) {
	t.Helper()
	h.in.mu.Lock()
	previous := h.in.model
	h.in.model = fakeModel(t, `{"category":"ordinary_coordination"}`)
	h.in.mu.Unlock()
	defer func() {
		h.in.mu.Lock()
		h.in.model = previous
		h.in.mu.Unlock()
	}()
	return suggestTool(t, h, args)
}

// replyPolicyOrdinaryDraftModel separates upstream's mandatory classification calls from draft generation.
// The operation under test still performs both; only the fixture responses are purpose-specific.
func replyPolicyOrdinaryDraftModel(t *testing.T, h *inboxHub, text string) {
	t.Helper()
	h.in.model = fakeModel(t, `{"category":"ordinary_coordination"}`)
	h.in.models[purposeDraft] = fakeModel(t, text)
}

func replyPolicyTestSnapshot(v any) *InboxReplyPolicySnapshot {
	switch x := v.(type) {
	case *InboxItem:
		return x.ReplyPolicy
	case InboxItem:
		return x.ReplyPolicy
	case *InboxSuggestion:
		return x.ReplyPolicy
	case InboxSuggestion:
		return x.ReplyPolicy
	default:
		return nil
	}
}

func replyPolicyTestEnsureSnapshot(v any) *InboxReplyPolicySnapshot {
	snap := replyPolicyTestSnapshot(v)
	if snap != nil {
		return snap
	}
	snap = &InboxReplyPolicySnapshot{}
	switch x := v.(type) {
	case *InboxItem:
		x.ReplyPolicy = snap
	case *InboxSuggestion:
		x.ReplyPolicy = snap
	}
	return snap
}

func replyPolicyTestCategory(v any) string {
	if snap := replyPolicyTestSnapshot(v); snap != nil {
		return snap.Category
	}
	return ""
}
func replyPolicyTestRoute(v any) string {
	if snap := replyPolicyTestSnapshot(v); snap != nil {
		return snap.Route
	}
	return ""
}
func replyPolicyTestContentVersion(v any) string {
	if snap := replyPolicyTestSnapshot(v); snap != nil {
		return snap.ContentVersion
	}
	return ""
}
func replyPolicyTestPolicyRevision(v any) string {
	if snap := replyPolicyTestSnapshot(v); snap != nil {
		return strings.Split(snap.PolicyRevision, "@")[0]
	}
	return ""
}
func replyPolicyTestReviewed(v any) bool {
	if snap := replyPolicyTestSnapshot(v); snap != nil {
		return snap.Reviewed
	}
	return false
}

func replyPolicyTestSetCategory(v any, value string) {
	replyPolicyTestEnsureSnapshot(v).Category = value
}
func replyPolicyTestSetRoute(v any, value string) {
	replyPolicyTestEnsureSnapshot(v).Route = value
}
func replyPolicyTestSetContentVersion(v any, value string) {
	replyPolicyTestEnsureSnapshot(v).ContentVersion = value
}
func replyPolicyTestSetPolicyRevision(v any, value string) {
	if value != "" {
		cfg := replyPolicyTestConfig()
		cfg.Revision = strings.Split(value, "@")[0]
		value = compileReplyPolicy(cfg).revision
	}
	replyPolicyTestEnsureSnapshot(v).PolicyRevision = value
}
func replyPolicyTestSetReviewed(v any, value bool) {
	replyPolicyTestEnsureSnapshot(v).Reviewed = value
}

// replyPolicyTestClassifierSystem renders the configured prompt exactly as the production classifier does. Keeping
// language, owner, category names and rules here avoids turning an upstream renderer improvement into a false red.
func replyPolicyTestClassifierSystem(h *inboxHub, cfg any) string {
	policy := compileReplyPolicy(cfg)
	hello := h.hello(context.Background())
	return prompts.RenderJSONOnly(prompts.RenderInboxReplyPolicy(prompts.InboxReplyPolicy{
		Language:   answerLanguage(hello.Locale),
		Owner:      inboxPromptOwner(hello),
		Categories: policy.names,
		Rules:      policy.rules,
	}))
}
