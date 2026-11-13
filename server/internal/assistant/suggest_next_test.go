package assistant

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// The hub-made next-message suggestion (suggest_next.go, #4644).

// suggestTest is a fleet with the switch on and a node answering session.items with items; done gets every call's end.
type suggestTest struct {
	*fleetTest
	mu    sync.Mutex
	items []proto.Item
	hold  chan struct{} // non-nil: session.items waits for it to close
	done  chan string
}

func newSuggestTest(t *testing.T, on bool) *suggestTest {
	t.Helper()
	ft := newFleetTestOn(t, &memStore{})
	ft.f.fakeModel(t)
	if on {
		setSettings(t, ft.s, func(v *Settings) { v.NextSuggestions = &on })
	}
	st := &suggestTest{fleetTest: ft, done: make(chan string, 8)}
	st.items = []proto.Item{
		{Seq: 1, Role: "user", Kind: proto.KindText, Text: "Find the flaky test"},
		{Seq: 2, Role: "assistant", Kind: proto.KindThinking, Text: "secret thoughts"},
		{Seq: 3, Role: "assistant", Kind: proto.KindToolUse, ToolName: "Bash", Text: "go test ./..."},
		{Seq: 4, Role: "user", Kind: proto.KindToolResult, Text: "tool output"},
		{Seq: 5, Role: "assistant", Kind: proto.KindText, Text: "Found two. Fix 1, 2 or both?"},
	}
	ft.f.mu.Lock()
	ft.f.sessions["mac"] = nil // the node answers session.items with items
	ft.f.items = st.items
	ft.f.mu.Unlock()
	ft.f.hookReads(receiptHooks{before: func(int) {
		st.mu.Lock()
		hold := st.hold
		st.mu.Unlock()
		if hold != nil {
			<-hold
		}
	}})
	ft.s.nextSug.done = func(_, _, text string) { st.done <- text }
	return st
}

// suggestAsked are the model calls the suggestion made.
func (st *suggestTest) suggestAsked() []modelCall {
	st.f.mu.Lock()
	defer st.f.mu.Unlock()
	var out []modelCall
	for _, c := range st.f.asked {
		if strings.Contains(c.System, "suggest the next message") {
			out = append(out, c)
		}
	}
	return out
}

// turn has sess work and then stop at status.
func (st *suggestTest) turn(sess proto.Session, status string) {
	sess.Status = proto.StatusWorking
	st.sessions("mac", sess)
	sess.Status = status
	st.sessions("mac", sess)
}

func (st *suggestTest) waitDone(t *testing.T) string {
	t.Helper()
	select {
	case text := <-st.done:
		return text
	case <-time.After(5 * time.Second):
		t.Fatal("no suggestion call ended")
		return ""
	}
}

// noCall checks that no more than made suggestion calls were made (they start synchronously, so none is on its way).
func (st *suggestTest) noCall(t *testing.T, made ...int) {
	t.Helper()
	select {
	case <-st.done:
		t.Fatal("a suggestion call ran")
	case <-time.After(50 * time.Millisecond):
	}
	want := 0
	if len(made) > 0 {
		want = made[0]
	}
	if n := len(st.suggestAsked()); n != want {
		t.Fatalf("%d suggestion calls", n)
	}
}

func shownSuggestion(t *testing.T, list []proto.Session, sid string) proto.Session {
	t.Helper()
	for _, s := range list {
		if s.SessionID == sid {
			return s
		}
	}
	t.Fatalf("%s not listed", sid)
	return proto.Session{}
}

func codexSession() proto.Session {
	return proto.Session{SessionID: "cx1", Agent: "codex", PaneID: "p1", Cwd: "/home/u/src/app"}
}

func TestSuggestNextAfterACodexTurn(t *testing.T) {
	st := newSuggestTest(t, true)
	st.f.replies = []string{"Fix both"}
	st.d.watch("mac", "cx1", true)
	st.turn(codexSession(), proto.StatusDone)
	if got := st.waitDone(t); got != "Fix both" {
		t.Fatalf("suggestion %q", got)
	}
	calls := st.suggestAsked()
	if len(calls) != 1 {
		t.Fatalf("%d calls", len(calls))
	}
	in := calls[0].User
	if !strings.Contains(in, "Find the flaky test") || !strings.Contains(in, "Fix 1, 2 or both?") {
		t.Fatalf("the visible messages are missing:\n%s", in)
	}
	for _, hidden := range []string{"secret thoughts", "go test", "tool output"} {
		if strings.Contains(in, hidden) {
			t.Fatalf("the model read %q:\n%s", hidden, in)
		}
	}
	got := shownSuggestion(t, st.lastList("mac"), "cx1")
	if got.Suggestion != "Fix both" || got.SuggestionRisky {
		t.Fatalf("listed %q risky=%v", got.Suggestion, got.SuggestionRisky)
	}
	// Another list of the same stopped turn (a second app opening it, a rebuild) asks nothing more: one per turn.
	sess := codexSession()
	sess.Status = proto.StatusDone
	st.sessions("mac", sess)
	st.noCall(t, 1)
	// A new turn takes it away.
	sess.Status = proto.StatusWorking
	st.sessions("mac", sess)
	if got := shownSuggestion(t, st.lastList("mac"), "cx1"); got.Suggestion != "" {
		t.Fatalf("still suggested while working: %q", got.Suggestion)
	}
}

func TestSuggestNextForAnAssistantConversation(t *testing.T) {
	st := newSuggestTest(t, true)
	st.f.replies = []string{"Go on"}
	sess := proto.Session{SessionID: "as1", Agent: "claude", Cwd: "/home/u/.persona/assistant"} // headless: no pane
	st.d.watch("mac", "as1", true)
	st.turn(sess, proto.StatusIdle)
	if got := st.waitDone(t); got != "Go on" {
		t.Fatalf("suggestion %q", got)
	}
}

func TestSuggestNextOnlyWhereTheRulesSay(t *testing.T) {
	failed := codexSession()
	failed.TurnError = "API Error: 500"
	sub := codexSession()
	sub.IsSubagent = true
	claude := proto.Session{SessionID: "cl1", Agent: "claude", PaneID: "p2", Suggestion: "run the tests"}
	for _, c := range []struct {
		name    string
		on      bool
		watched bool
		sess    proto.Session
		status  string
	}{
		{"switch off", false, true, codexSession(), proto.StatusDone},
		{"nobody has it open", true, false, codexSession(), proto.StatusDone},
		{"failed turn", true, true, failed, proto.StatusDone},
		{"interrupted turn", true, true, codexSession(), proto.StatusInterrupted},
		{"subagent", true, true, sub, proto.StatusDone},
		{"Claude Code in a terminal has its own", true, true, claude, proto.StatusDone},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newSuggestTest(t, c.on)
			st.f.replies = []string{"Fix both"}
			st.d.watch("mac", c.sess.SessionID, c.watched)
			st.turn(c.sess, c.status)
			st.noCall(t)
		})
	}
	// Claude Code's own suggestion still reaches the apps, as it is.
	st := newSuggestTest(t, true)
	st.d.watch("mac", "cl1", true)
	claude.Status = proto.StatusDone
	st.sessions("mac", claude)
	got := shownSuggestion(t, st.lastList("mac"), "cl1")
	if got.Suggestion != "run the tests" {
		t.Fatalf("listed %q", got.Suggestion)
	}
}

func TestSuggestNextCancelledByANewTurn(t *testing.T) {
	st := newSuggestTest(t, true)
	st.f.replies = []string{"Fix both"}
	hold := make(chan struct{})
	st.mu.Lock()
	st.hold = hold
	st.mu.Unlock()
	st.d.watch("mac", "cx1", true)
	st.turn(codexSession(), proto.StatusDone)
	sess := codexSession()
	sess.Status = proto.StatusWorking // the user wrote on the computer: a new turn while the call reads
	st.sessions("mac", sess)
	close(hold)
	if got := st.waitDone(t); got != "" {
		t.Fatalf("a cancelled call left %q", got)
	}
	if got := shownSuggestion(t, st.lastList("mac"), "cx1"); got.Suggestion != "" {
		t.Fatalf("listed %q", got.Suggestion)
	}
}

func TestSuggestNextDroppedWhenAMessageGoesIn(t *testing.T) {
	st := newSuggestTest(t, true)
	st.f.replies = []string{"Fix both"}
	st.d.watch("mac", "cx1", true)
	st.turn(codexSession(), proto.StatusDone)
	st.waitDone(t)
	st.s.UserSpoke(t.Context(), SessionRef{MachineID: "mac", SessionID: "cx1"})
	if got := shownSuggestion(t, st.lastList("mac"), "cx1"); got.Suggestion != "" {
		t.Fatalf("listed %q after a message went in", got.Suggestion)
	}
}

func TestSuggestNextNothingSensibleShowsNothing(t *testing.T) {
	for _, reply := range []string{"NONE", "", "line one\nline two", strings.Repeat("long ", 60)} {
		st := newSuggestTest(t, true)
		st.f.replies = []string{reply}
		st.d.watch("mac", "cx1", true)
		st.turn(codexSession(), proto.StatusDone)
		if got := st.waitDone(t); got != "" {
			t.Fatalf("reply %q suggested %q", reply, got)
		}
		if n := len(st.suggestAsked()); n != 1 { // no retry
			t.Fatalf("reply %q: %d calls", reply, n)
		}
	}
}

func TestSuggestionTurnsCap(t *testing.T) {
	var items []proto.Item
	for i := range 12 {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		items = append(items, proto.Item{Seq: i + 1, Role: role, Kind: proto.KindText, Text: strings.Repeat("x", 2000)})
	}
	items = append(items, proto.Item{Role: "user", Kind: proto.KindText, Text: "injected", Injected: &proto.Injected{Kind: proto.InjectedFollowup}},
		proto.Item{Role: "user", Kind: proto.KindText, Text: "a picture", Image: &proto.Image{}})
	got := suggestionTurns(items)
	if len(got) > suggestMaxMessages {
		t.Fatalf("%d messages", len(got))
	}
	total := 0
	for _, m := range got {
		total += len([]rune(m.Text))
		if m.Text == "injected" || m.Text == "a picture" {
			t.Fatalf("read %q", m.Text)
		}
	}
	if total > suggestMaxChars {
		t.Fatalf("%d characters", total)
	}
	if got[len(got)-1].Role != "assistant" { // oldest first: the newest visible message last
		t.Fatalf("order: %+v", got[len(got)-1].Role)
	}
}

func TestSuggestionRisky(t *testing.T) {
	for _, c := range []struct {
		text  string
		risky bool
	}{
		{"Fix both", false},
		{"Do 1 and 3", false},
		{"继续", false},
		{"Yes, go ahead", false},
		{"Make it shorter", false},
		{"Delete the old branch", true},
		{"push it", true},
		{"Deploy to staging", true},
		{"发布吧", true},
		{"Send the reply", true},
		{"run `make test`", true},
		{"git reset --hard", true},
		{"rm -rf build", true},
		{"Docker compose up", true},
		{"运行npm test", true},
		{"Open ~/Downloads/report.pdf", true},
		{"Look at src/app/main.go", true},
		{"Check /etc/hosts/", true},
		{"See https://example.com/x", true},
		{"Try example.com", true},
		{"Use sk-abcdef123456789", true},
		{"The token is a8f3k29dk3l2m4n5b6v7c8x9z0q1", true},
	} {
		if got := suggestionRisky(c.text); got != c.risky {
			t.Errorf("%q: risky=%v, want %v", c.text, got, c.risky)
		}
	}
}

func TestCleanSuggestion(t *testing.T) {
	for in, want := range map[string]string{
		"  Fix both  ": "Fix both",
		`"Fix both"`:   "Fix both",
		"「继续」":         "继续",
		"NONE":         "",
		"none.":        "",
		"a\nb":         "",
		"":             "",
		"`ls`":         "ls",
		"Do 1 and 3":   "Do 1 and 3",
	} {
		if got := cleanSuggestion(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// A risky suggestion of Claude Code's own is marked too.
func TestDecorateSuggestionMarksRisky(t *testing.T) {
	sess := proto.Session{SessionID: "cl1", Agent: "claude", PaneID: "p1", Suggestion: "push to main"}
	decorateSuggestion(&sess, nil)
	if !sess.SuggestionRisky {
		t.Fatal("not risky")
	}
	cx := codexSession()
	cx.Status = proto.StatusWorking
	decorateSuggestion(&cx, map[string]string{"cx1": "Fix both"})
	if cx.Suggestion != "" {
		t.Fatal("suggested while working")
	}
	cx.Status = proto.StatusDone
	decorateSuggestion(&cx, map[string]string{"cx1": "Fix both"})
	if cx.Suggestion != "Fix both" || cx.SuggestionRisky {
		t.Fatalf("%q %v", cx.Suggestion, cx.SuggestionRisky)
	}
}

func TestGoldenNextMessage(t *testing.T) {
	st := newSuggestTest(t, true)
	st.f.replies = []string{"Fix both"}
	st.d.watch("mac", "cx1", true)
	st.turn(codexSession(), proto.StatusDone)
	st.waitDone(t)
	c := st.suggestAsked()[0]
	golden(t, "next_message.system", c.System)
	golden(t, "next_message.user", c.User)
}
