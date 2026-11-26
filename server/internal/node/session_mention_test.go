package node

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

const (
	quotedSession = "0d000000-0000-4000-8000-00000000000b"
	// exampleProgram is a node's program in the goldens: a placeholder path.
	exampleProgram = "/opt/persona/bin/personad"
)

// quoteMessage is a message as the clients write one with a session picked in the @ menu (web lib/mentions.ts
// sessionQuote, iOS PromptItems.quote, Android sessionQuote): the quote in front, then the user's words.
func quoteMessage(tag, url string) string {
	return `<` + tag + ` kind="context" title="Login fix" url="` + url + `">` +
		"\nAnother session: Login fix (Codex)\nFolder: /code/app\nIt last said: The test now waits for the server.\n</" + tag + ">\n\nWhat did it change?"
}

// The note an Agent gets inside the quote (#13831), in each language the manager gives (prompt_language: empty for
// Chinese, as the peer wrapper takes it), with the node's program and, for a node without an absolute path, without.
func TestGoldenSessionMention(t *testing.T) {
	text := quoteMessage("persona-context", "persona://session/box/"+quotedSession)
	for _, tc := range []struct{ language, suffix string }{{"", "zh-Hans"}, {"English", "en"}, {"日本語", "ja"}} {
		goldenNode(t, "session_mention."+tc.suffix, newSessionNotes(tc.language, exampleProgram).apply(text))
		goldenNode(t, "session_mention.no_program."+tc.suffix, newSessionNotes(tc.language, "personad").apply(text))
	}
	if newSessionNotes("简体中文", exampleProgram).apply(text) != newSessionNotes("", exampleProgram).apply(text) {
		t.Fatal("the language's own name and the empty default disagree on Chinese")
	}
}

func TestSessionMentionsNoteOnlyLeadingSessionQuotes(t *testing.T) {
	notes := newSessionNotes("English", exampleProgram)
	url := "persona://session/box/" + quotedSession
	for name, text := range map[string]string{
		"no context":     "Look at persona://session/box/" + quotedSession + " please",
		"a forward":      `<persona-context kind="forward" title="Login fix" url="` + url + `?at=item-1" msg="item-1">` + "\nThe words.\n</persona-context>\n\nLook",
		"a query":        quoteMessage("persona-context", url+"?at=item-1"),
		"more path":      quoteMessage("persona-context", url+"/more"),
		"no session":     quoteMessage("persona-context", "persona://session/box/"),
		"another link":   quoteMessage("persona-context", "https://example.com/session/box/"+quotedSession),
		"another kind":   strings.Replace(quoteMessage("persona-context", url), `kind="context"`, `kind="edit"`, 1),
		"data-kind only": strings.Replace(quoteMessage("persona-context", url), `kind="context"`, `data-kind="context"`, 1),
		"kind twice":     strings.Replace(quoteMessage("persona-context", url), `kind="context"`, `kind="context" kind="edit"`, 1),
		"a space":        quoteMessage("persona-context", "persona://session/box/a b"),
		"a quote":        quoteMessage("persona-context", "persona://session/box/a&quot;b"),
		"after words":    "First this.\n\n" + quoteMessage("persona-context", url),
	} {
		if got := notes.apply(text); got != text {
			t.Errorf("%s: changed into\n%s", name, got)
		}
	}

	skill := `<persona-context kind="skill" title="Review" url="" ref="skill-1">` + "\nReview skill\n</persona-context>\n"
	text := skill + quoteMessage("persona-context", url) + "\n"
	text = strings.Replace(text, "\n\nWhat did it change?\n", "\n"+quoteMessage("jarvis-context", "persona://session/mac/runtime.worker-0001"), 1)
	got := notes.apply(text)
	if !strings.HasPrefix(got, skill) {
		t.Fatalf("the skill selection in front changed:\n%s", got)
	}
	first, second := notes.note("English", "box", quotedSession), notes.note("English", "mac", "runtime.worker-0001")
	if strings.Count(got, first) != 1 || strings.Count(got, second) != 1 {
		t.Fatalf("want each quote noted once, got:\n%s", got)
	}
	if !strings.Contains(got, "It last said: The test now waits for the server.\n\n"+first+"\n</persona-context>") ||
		!strings.HasSuffix(got, second+"\n</jarvis-context>\n\nWhat did it change?") {
		t.Fatalf("each note ends its own quote, the user's words stay last:\n%s", got)
	}
	// Sent again (an edited or compacted resend), in another language: the note is not added twice.
	if again := newSessionNotes("日本語", exampleProgram).apply(got); again != got {
		t.Fatalf("a noted quote changed when sent again:\n%s", again)
	}
}

// The assistant's conversations and guests read quotes with their own tools; an Agent's message gets the note.
func TestOnlyAgentsGetTheSessionMentionNote(t *testing.T) {
	n, _, _, _ := guideNode(t, false)
	ctx := context.Background()
	text := quoteMessage("persona-context", "persona://session/box/"+quotedSession)
	if got := n.withSessionMentions(ctx, sidA, text); got == text || !strings.Contains(got, "a2a_read") {
		t.Fatalf("the Agent's message was not noted:\n%s", got)
	}
	const assistant, guest = "11111111-2222-3333-4444-000000000021", "11111111-2222-3333-4444-000000000022"
	n.mu.Lock()
	n.procs[assistant] = &assistantProc{id: assistant, agent: "claude", exited: make(chan struct{})}
	n.mu.Unlock()
	n.state.put(guest, sessionState{Guest: []string{"meeting_lines"}})
	for _, id := range []string{assistant, guest} {
		if got := n.withSessionMentions(ctx, id, text); got != text {
			t.Fatalf("%s got the note:\n%s", id, got)
		}
	}
}

// The note goes in before the send is recorded for its echo (echo.go): the transcript's line holds it, and is still
// tied to the message's client_msg_id, so the app's bubble is not left pending. It names the node's own program.
func TestSendNotesASessionQuoteBeforeItsEcho(t *testing.T) {
	n, f, home, cwd := guideNode(t, true)
	guidePane(t, f, home, cwd, func(int) string { return "" })
	var mu sync.Mutex
	typed := ""
	f.after(func(c herdrCall) {
		if c.Method != "agent.prompt" {
			return
		}
		text := str(c.Params["text"])
		mu.Lock()
		typed = text
		mu.Unlock()
		line, _ := json.Marshal(map[string]any{"type": "user", "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "cwd": cwd,
			"message": map[string]any{"role": "user", "content": text}})
		appendTranscript(t, home, cwd, sidA, string(line))
	})
	text := quoteMessage("persona-context", "persona://session/box/"+quotedSession)
	if _, err := request(t, n, "session.send", map[string]any{"session_id": sidA, "text": text, "client_msg_id": "msg-quote",
		"prompt_language": "English"}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got := typed
	mu.Unlock()
	program, _ := json.Marshal(n.bin)
	if !strings.Contains(got, "This quote shows only part of another Persona conversation") ||
		!strings.Contains(got, `{"machine_id":"box","session_id":"`+quotedSession+`"}`) ||
		!strings.Contains(got, "Executable (JSON string, decode as a path and quote for your terminal's shell): "+string(program)) ||
		!strings.HasSuffix(got, "</persona-context>\n\nWhat did it change?") {
		t.Fatalf("typed:\n%s", got)
	}
	if check := n.checkEcho(context.Background(), sidA, "msg-quote"); check.state != proto.EchoSeen {
		t.Fatalf("the noted line was not tied to its client_msg_id: %v", check.state)
	}
}

// notedQuote is quoteMessage's session quote as n notes it for an Agent in English: the text every path below must
// deliver.
func notedQuote(t *testing.T, n *Node) (text, noted string) {
	t.Helper()
	text = quoteMessage("persona-context", "persona://session/box/"+quotedSession)
	noted = newSessionNotes("English", cmp.Or(n.bin, n.exe)).apply(text)
	if noted == text || !strings.Contains(noted, "a2a_read") {
		t.Fatalf("no note to expect:\n%s", noted)
	}
	return text, noted
}

// The paths that take session.send before the node's own send deliver the note too (#13831): an agy pane and a
// Cursor pane, typed through herdr's agent.prompt (workagent.go), went without it.
func TestPaneAgentsTypedThroughHerdrGetTheSessionMentionNote(t *testing.T) {
	for _, tc := range []struct{ agent, row string }{{agyAgent, agyTestRow}, {cursorAgent, "cursor-w1-p1"}} {
		t.Run(tc.agent, func(t *testing.T) {
			n, f, home := herdrNode(t)
			t.Setenv("ANTIGRAVITY_CLI_CONFIG_DIR", "")
			pane := agyTestAgent(agyTestPane, home, agyTestID)
			if tc.agent == cursorAgent {
				pane = agentOf(cursorAgent, agyTestID, proto.StatusIdle, agyTestPane, "w1:t1", "w1", home, "cursor")
			}
			seedAgents(t, n, f, pane)
			if tc.agent == agyAgent {
				// A send to an agy pane is confirmed in agy's own conversation record (#14722): agy writes the typed text there.
				stubAgyNativePrompt(t, n, f, agyTestPane, agyTestID)
			}
			text, noted := notedQuote(t, n)
			agyTestCall(t, n, "session.send", tc.row, map[string]any{"text": text, "prompt_language": "English"})
			assertTrace(t, f.trace("agent.prompt"), []string{call("agent.prompt", map[string]any{"target": agyTestPane, "text": noted})})
		})
	}
}

// A runtime's session (Paseo's bridge, the pi family's and OpenCode's extensions: runtimes_calls.go) gets the note in
// the text the node hands the runtime.
func TestRuntimeSessionGetsTheSessionMentionNote(t *testing.T) {
	n, _ := runtimeNode(t)
	host := dialRuntime(t, n, dshHello())
	_ = host.peer.Notify("runtime.sessions", map[string]any{"sessions": []map[string]any{{"session_id": "session-quote", "status": "idle"}}})
	waitFor(t, "listed", func() bool { _, ok := listed(t, n)["session-quote"]; return ok })
	text, noted := notedQuote(t, n)
	if _, err := nodeCall(t, n, "session.send", map[string]any{"session_id": "session-quote", "text": text, "client_msg_id": "m-quote",
		"prompt_language": "English"}); err != nil {
		t.Fatal(err)
	}
	sent := host.got("session.send")
	var p struct {
		Text        string `json:"text"`
		ClientMsgID string `json:"client_msg_id"`
	}
	if len(sent) != 1 || json.Unmarshal(sent[0], &p) != nil || p.Text != noted || p.ClientMsgID != "m-quote" {
		t.Fatalf("the runtime got %s", sent)
	}
}

// A Codex waiting for its first message (codex_waiting.go) gets the note in the paste, and the thread it begins is
// found by the noted text it was sent.
func TestCodexFirstMessageGetsTheSessionMentionNote(t *testing.T) {
	n, f, cwd, _ := codexWaitHerdr(t)
	home := filepath.Dir(cwd)
	s := startWaiting(t, n)
	text, noted := notedQuote(t, n)
	var mu sync.Mutex
	var pasted string
	f.after(func(c herdrCall) {
		if str(c.Params["pane_id"]) != "w1:p2" {
			return
		}
		switch {
		case c.Method == "pane.send_text":
			mu.Lock()
			pasted = str(c.Params["text"])
			mu.Unlock()
			f.setScreen("w1:p2", fmt.Sprintf("› [Pasted Content %d chars]\n\n  gpt-6 xhigh · /tmp/proj\n", len([]rune(noted))))
		case c.Method == "pane.send_keys" && slices.Equal(keysOf(c), []string{"enter"}):
			f.setScreen("w1:p2", "› quote\n\n• Working (0s • esc to interrupt)\n\n› Ask Codex to do anything\n")
			writeWaitRollout(t, home, cwd, startThread, time.Now(), noted)
		}
	})
	res, err := request(t, n, "session.send", map[string]any{"session_id": s.SessionID, "text": text, "client_msg_id": "m-quote",
		"prompt_language": "English"})
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeSessions(t, []any{res["session"]})[0]; got.SessionID != startThread {
		t.Fatalf("answered %+v", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if pasted != pasteStart+noted+pasteEnd {
		t.Fatalf("pasted:\n%q", pasted)
	}
}
