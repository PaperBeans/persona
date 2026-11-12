package subscriptions

import (
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/connector"
)

// A dry run's items become what the Inbox would be handed (#5278): one thread row each with its context, the
// extension's rules when the subscription has none, the fallback a due date gives, and the source's kinds.
func TestPreviewThings(t *testing.T) {
	e := newEnv(t)
	e.extRules = "Notices from the office need me"
	sub := Subscription{ID: "sub_x", Connector: "example", Extension: "examplechat", Source: "notices", Label: "Notices",
		Recipe: Recipe{Mode: "items", Kind: "sub_notice", GroupBy: "thread"}}
	due := "2026-11-20T00:00:00Z"
	tp := e.svc.PreviewThings(sub, []Preview{
		{UID: "1", Title: "Office closed Friday", Sender: "Office", Time: ptrTo("2026-11-10T08:00:00Z"), Body: "The office is closed on Friday."},
		{UID: "2", Thread: "t9", Title: "Pay the invoice", DueDate: &due, Body: "Please pay", Link: "https://example.com/i/2"},
	})
	if tp.Source != "sub_x" || tp.Name != "Notices" || tp.Kinds["sub_notice"] == "" || len(tp.Things) != 2 {
		t.Fatalf("preview: %+v", tp)
	}
	one, two := tp.Things[0], tp.Things[1]
	if one.Title != "Office closed Friday" || one.Sender != "Office" || one.Rules != "Notices from the office need me" ||
		!strings.Contains(one.Context, "The office is closed on Friday.") || one.Thread != "sub:sub_x:1" {
		t.Errorf("an item as delivered: %+v", one)
	}
	if one.Extra == nil || one.Extra.RuleTier != string(connector.TierFYI) {
		t.Errorf("the default fallback: %+v", one.Extra)
	}
	if two.Thread != "sub:sub_x:t9" || two.Extra == nil || two.Extra.RuleTier != string(connector.TierAction) || two.URL != "https://example.com/i/2" {
		t.Errorf("a due item needs you, its link opens: %+v", two)
	}
}

func ptrTo(s string) *string { return &s }
