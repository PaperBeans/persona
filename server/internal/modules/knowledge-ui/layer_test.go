package knowledgeui

import (
	"errors"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/connectorwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/modules/knowledge-ui/kbhistory"
)

// fakeHost is a core that is never asked anything: a test that reaches one of its methods fails on the nil Host.
type fakeHost struct{ Host }

// While the import is stopped the selection page's chats are its plan's, cached, with what each is filed under, and
// the source is not asked (the host is not reached).
func TestChatsFromThePlanWhileStopped(t *testing.T) {
	data := t.TempDir()
	at := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	p := &kbhistory.Plan{Source: "chat", GeneratedAt: at, Chats: []kbhistory.PlanChat{
		{ID: "d1", Name: "Direct One", Kind: "dm", Bot: true, Estimate: 7},
		{ID: "p1", Name: "Posts", Kind: "group", Collapsed: true, FiledAs: "posts"}}}
	if err := kbhistory.SavePlan(data, p); err != nil {
		t.Fatal(err)
	}
	if err := kbhistory.WriteStop(data, kbhistory.Stop{By: "tool"}); err != nil {
		t.Fatal(err)
	}
	chats, listed, cached, err := New(fakeHost{}).Chats(t.Context(), data, "chat")
	want := []connectorwire.HistoryChat{{ID: "d1", Name: "Direct One", Kind: "dm", Bot: true, Estimate: 7},
		{ID: "p1", Name: "Posts", Kind: "group", Collapsed: true, Source: "posts"}}
	if err != nil || !cached || !listed.Equal(at) || len(chats) != 2 || chats[0] != want[0] || chats[1] != want[1] {
		t.Fatalf("chats %+v listed %v cached %v err %v", chats, listed, cached, err)
	}
}

// Why Chats failed, as the selection page says it: no history, stopped (with why) or failed (with the error's code).
func TestChatsProblem(t *testing.T) {
	l := New(fakeHost{})
	if p, text := l.ChatsProblem(kbhistory.ErrUnavailable, i18n.Lang("en")); p != "no_history" || text != "" {
		t.Fatalf("unavailable: %s %q", p, text)
	}
	halt := kbhistory.HaltWhy(true, kbhistory.NewReason("asked"))
	if p, text := l.ChatsProblem(halt, i18n.Lang("en")); p != "stopped" || text != halt.ReasonText(i18n.Lang("en")) || text == "" {
		t.Fatalf("halt: %s %q", p, text)
	}
	other := errors.New("boom")
	if p, text := l.ChatsProblem(other, i18n.Lang("en")); p != "failed" || text != kbhistory.ErrCode(other) {
		t.Fatalf("other: %s %q", p, text)
	}
}
