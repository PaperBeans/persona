package node

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// quietAfter shortens the wait before a busy process is checked against Claude Code's own state.
func quietAfter(t *testing.T, d time.Duration) {
	t.Helper()
	was := assistantBusyQuiet
	assistantBusyQuiet = d
	t.Cleanup(func() { assistantBusyQuiet = was })
}

// claudeSays writes the sessions/<pid>.json Claude Code keeps for the conversation's process, with status.
func claudeSays(t *testing.T, f *cliFixture, id, status string) {
	t.Helper()
	p := f.proc(id)
	if p == nil {
		t.Fatalf("no process for %s", id)
	}
	dir := filepath.Join(f.n.defaultClaudeDir(), "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]any{"pid": p.cmd.Process.Pid, "sessionId": id, "kind": "interactive", "entrypoint": "sdk-cli", "status": status})
	if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(p.cmd.Process.Pid)+".json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// lastPushed is the status of conversation id in the node's latest list to the manager.
func lastPushed(f *cliFixture, id string) string {
	rows := f.fh.rows(id)
	if len(rows) == 0 {
		return ""
	}
	return rows[len(rows)-1].status
}

// A turn ended and its result never reached the node (#14351): the row said working until the conversation's next
// turn ended, hours later. Claude Code's own state says the turn is over, so the next sweep takes it as ended, the
// manager gets the list, and the process stays for the next message.
func TestAssistantTurnEndedUnheardGoesBackToDone(t *testing.T) {
	f := cliTest(t)
	quietAfter(t, 20*time.Millisecond)
	if err := os.WriteFile(filepath.Join(f.dir, "hold"), nil, 0o600); err != nil { // the turn plays, no result comes
		t.Fatal(err)
	}
	id, err := f.n.assistantSend(context.Background(), "", t.TempDir(), "claude", "hello", nil, "", "", cliExec())
	if err != nil {
		t.Fatal(err)
	}
	p := f.proc(id)
	cliWait(t, "the turn's last line", func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.deltaSent == "hello"
	})
	claudeSays(t, f, id, "busy")
	f.n.sweepAssistants()
	if got := f.session(t, id).Status; got != proto.StatusWorking {
		t.Fatalf("a turn Claude Code says is running is listed %q, want working", got)
	}
	claudeSays(t, f, id, "idle")
	cliWait(t, "the unheard end to be taken", func() bool {
		f.n.sweepAssistants()
		return f.session(t, id).Status == proto.StatusDone
	})
	cliWait(t, "the manager to get the row", func() bool { return lastPushed(f, id) == proto.StatusDone })
	if f.proc(id) != p || !p.alive() {
		t.Fatal("the conversation's process was let go with the turn")
	}
	if logs := f.log.String(); !strings.Contains(logs, "listed working with no turn running") {
		t.Fatalf("nothing logged: %s", logs)
	}
	// The conversation takes its next message as an idle one does, and that turn ends as usual.
	if err := os.Remove(filepath.Join(f.dir, "hold")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.n.assistantSend(context.Background(), id, "", "claude", "again", nil, "", "", cliExec()); err != nil {
		t.Fatal(err)
	}
	f.idle(t, id)
}

// A message written to an idle conversation that starts no turn there, as one the manager delivers from its relay
// queue may (#14351): the send marked the process busy and no result follows, so the row stayed working and the
// manager kept every later message from other conversations queued behind it. Claude Code says it is idle: done again.
func TestAssistantMessageThatStartedNoTurnGoesBackToDone(t *testing.T) {
	f := cliTest(t)
	quietAfter(t, 20*time.Millisecond)
	id, err := f.n.assistantSend(context.Background(), "", t.TempDir(), "claude", "hello", nil, "", "", cliExec())
	if err != nil {
		t.Fatal(err)
	}
	f.idle(t, id)
	if err := os.WriteFile(filepath.Join(f.dir, "mute"), nil, 0o600); err != nil { // the next message starts nothing
		t.Fatal(err)
	}
	if _, err := f.n.assistantSend(context.Background(), id, "", "claude", "a message from another conversation", nil, "", "", cliExec()); err != nil {
		t.Fatal(err)
	}
	cliWait(t, "the message to be written", func() bool {
		return strings.Contains(f.file("stdin.jsonl"), "a message from another conversation")
	})
	if got := f.session(t, id).Status; got != proto.StatusWorking {
		t.Fatalf("listed %q right after the send, want working", got)
	}
	claudeSays(t, f, id, "idle")
	cliWait(t, "the row to go back to done", func() bool {
		f.n.sweepAssistants()
		return f.session(t, id).Status == proto.StatusDone
	})
	cliWait(t, "the manager to get the row", func() bool { return lastPushed(f, id) == proto.StatusDone })
}

// busyProc is a conversation's process held busy with nothing heard of it for longer than assistantBusyQuiet.
func busyProc(n *Node, id string) *assistantProc {
	p := &assistantProc{id: id, agent: "claude", busy: true}
	n.procs[id] = p
	return p
}

// claudeStatus makes Claude Code's own word for every conversation what says returns.
func claudeStatus(t *testing.T, says func(id string) string) {
	t.Helper()
	was := assistantClaudeStatus
	assistantClaudeStatus = func(_ *Node, id string) string { return says(id) }
	t.Cleanup(func() { assistantClaudeStatus = was })
}

// Only Claude Code saying the turn is over ends it: one it says is running or waiting on a dialog, and one it says
// nothing of (no state file: an older Claude Code, a process on another machine), keep the flag.
func TestAssistantBusyKeptUnlessClaudeSaysIdle(t *testing.T) {
	for status, wantBusy := range map[string]bool{"idle": false, "shell": false, "busy": true, "waiting": true, "": true} {
		t.Run("status "+status, func(t *testing.T) {
			n := assistantNode(t)
			claudeStatus(t, func(string) string { return status })
			p := busyProc(n, "11111111-2222-3333-4444-555555555555")
			n.reconcileAssistantTurns()
			p.mu.Lock()
			busy := p.busy
			p.mu.Unlock()
			if busy != wantBusy {
				t.Fatalf("busy %v with Claude Code saying %q, want %v", busy, status, wantBusy)
			}
		})
	}
}

// What holds busy on purpose, or shows the turn alive, is not asked about at all: a compaction (it has its own
// timeout), a recovery's hold on a failed turn, a line printed or a message sent within assistantBusyQuiet.
func TestAssistantBusyNotReconciledWhileHeldOrHeard(t *testing.T) {
	const id = "11111111-2222-3333-4444-555555555555"
	for name, hold := range map[string]func(n *Node, p *assistantProc){
		"compacting":      func(_ *Node, p *assistantProc) { p.compacting = true },
		"user compaction": func(_ *Node, p *assistantProc) { p.manual = true },
		"recovery":        func(n *Node, _ *assistantProc) { n.recoveries = map[string]*assistantTurnRecovery{id: {}} },
		"just sent":       func(_ *Node, p *assistantProc) { p.lastActive = time.Now() },
		"just printed":    func(_ *Node, p *assistantProc) { p.born = time.Now(); p.printed() },
		"reading a line":  func(_ *Node, p *assistantProc) { p.reading.Add(1) },
		"codex":           func(_ *Node, p *assistantProc) { p.agent = "codex" },
	} {
		t.Run(name, func(t *testing.T) {
			n := assistantNode(t)
			asked := false
			claudeStatus(t, func(string) string { asked = true; return "idle" })
			p := busyProc(n, id)
			hold(n, p)
			n.reconcileAssistantTurns()
			if asked || !p.busy {
				t.Fatalf("asked Claude Code: %v, busy: %v; want neither touched", asked, p.busy)
			}
		})
	}
}

// A turn that began while Claude Code's state was being read (a message sent, a line printed) is not ended by what
// the file said a moment earlier.
func TestAssistantBusyKeptWhenTurnBeginsDuringTheLook(t *testing.T) {
	for name, begin := range map[string]func(p *assistantProc){
		"a message sent": func(p *assistantProc) { p.mu.Lock(); p.lastActive = time.Now(); p.mu.Unlock() },
		"a line printed": func(p *assistantProc) { p.lastOut.Add(1) },
	} {
		t.Run(name, func(t *testing.T) {
			n := assistantNode(t)
			p := busyProc(n, "11111111-2222-3333-4444-555555555555")
			claudeStatus(t, func(string) string { begin(p); return "idle" })
			n.reconcileAssistantTurns()
			p.mu.Lock()
			defer p.mu.Unlock()
			if !p.busy {
				t.Fatal("the turn that just began was taken as ended")
			}
		})
	}
}

// A result line with a field of another type than the node reads (a gateway's token count as a string) used to be
// dropped whole, and the turn it ended stayed working (#14351). Its type and words are decoded all the same.
func TestAssistantResultWithAnUnreadableFieldStillEndsTheTurn(t *testing.T) {
	n := assistantNode(t)
	p := &assistantProc{id: "11111111-2222-3333-4444-555555555555", busy: true}
	n.readAssistant(p, strings.NewReader(`{"type":"result","subtype":"success","is_error":false,"result":"done","modelUsage":{"m":{"inputTokens":"12","contextWindow":200000.5}}}`+"\n"))
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.busy {
		t.Fatal("the turn is still working after its result")
	}
	if p.turnError != "" {
		t.Fatalf("a successful result left the turn error %q", p.turnError)
	}
}

// Any other line that does not decode is still dropped: a message_start with an unreadable usage starts nothing.
func TestAssistantOtherUnreadableLinesAreStillDropped(t *testing.T) {
	n := assistantNode(t)
	p := &assistantProc{id: "11111111-2222-3333-4444-555555555555"}
	n.readAssistant(p, strings.NewReader(`{"type":"stream_event","event":{"type":"message_start","message":{"usage":{"input_tokens":"5"}}}}`+"\n"+`{"type":"result"`+"\n"))
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.busy {
		t.Fatal("a line that did not decode marked the conversation busy")
	}
}
