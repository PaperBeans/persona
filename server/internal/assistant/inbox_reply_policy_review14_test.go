package assistant

import (
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

func TestInboxReplyPolicyOrdinaryAgentCreateConfirm(t *testing.T) {
	h, _, it := handledHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
	model, calls := captureModel(t, `{"category":"ordinary_coordination"}`)
	h.in.model = model
	const instruction = "Synthetic ordinary Agent instruction requiring owner-draft authorization"
	if _, err := suggestTool(t, h, `{"kind":"agent","text":"`+instruction+`"}`); err != nil {
		t.Fatalf("ordinary Agent publication failed: %v", err)
	}
	g := currentSuggestion(it)
	if g == nil || g.Kind != SuggestAgent || g.Handback || g.Text != instruction {
		t.Fatalf("ordinary Agent publication fixture is invalid: %+v", g)
	}
	publishedSnap := cloneReplyPolicySnapshot(g.ReplyPolicy)
	publishedCalls := len(calls())

	h.f.mu.Lock()
	before := len(h.f.starts)
	h.f.mu.Unlock()
	code, out := suggestionCall(t, h, it.ID, `{"version":1,"action":"confirm"}`)
	h.f.mu.Lock()
	starts := append([]map[string]any(nil), h.f.starts...)
	h.f.mu.Unlock()
	if code != 200 || len(starts) != before+1 {
		t.Fatalf("ordinary Agent create-confirm was denied before agent_start: code=%d out=%v snapshot=%+v classifier_calls=%d starts=%v", code, out, publishedSnap, publishedCalls, starts)
	}
	if prompt, _ := starts[before]["prompt"].(string); !strings.HasPrefix(prompt, instruction) {
		t.Fatalf("ordinary Agent confirmation started the wrong instruction: prompt=%q want_prefix=%q", prompt, instruction)
	}
	if publishedCalls != 1 || publishedSnap == nil || publishedSnap.Route != ReplyRouteOwnerDraft || publishedSnap.ContentVersion != replyPolicyContentVersion(it) {
		t.Fatalf("ordinary Agent publication lacked exact owner-draft authorization: snapshot=%+v classifier_calls=%d", publishedSnap, publishedCalls)
	}
}

func TestInboxReplyPolicyOrdinaryHandbackCreateConfirm(t *testing.T) {
	h, lark, it := handledHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
	model, calls := captureModel(t, `{"category":"ordinary_coordination"}`)
	h.in.model = model
	const instruction = "Synthetic ordinary handback requiring owner-draft authorization"
	if _, err := suggestTool(t, h, `{"kind":"reply","handback":true,"text":"`+instruction+`"}`); err != nil {
		t.Fatalf("ordinary handback publication failed: %v", err)
	}
	g := currentSuggestion(it)
	if g == nil || g.Kind != SuggestReply || !g.Handback || g.Text != instruction {
		t.Fatalf("ordinary handback publication fixture is invalid: %+v", g)
	}
	publishedSnap := cloneReplyPolicySnapshot(g.ReplyPolicy)
	publishedCalls := len(calls())
	contentVersion := replyPolicyContentVersion(it)

	code, out := suggestionCall(t, h, it.ID, `{"version":1,"action":"confirm"}`)
	if code != 200 {
		t.Fatalf("ordinary handback create-confirm was denied: code=%d out=%v snapshot=%+v classifier_calls=%d", code, out, publishedSnap, publishedCalls)
	}
	toldConv(t, h, instruction)
	if len(lark.got) != 0 {
		t.Fatalf("ordinary handback incorrectly reached the physical sender: %+v", lark.got)
	}
	if publishedCalls != 1 || publishedSnap == nil || publishedSnap.Route != ReplyRouteOwnerDraft || publishedSnap.ContentVersion != contentVersion {
		t.Fatalf("ordinary handback publication lacked exact owner-draft authorization: snapshot=%+v classifier_calls=%d", publishedSnap, publishedCalls)
	}
}
