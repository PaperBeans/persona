package node

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// The hub's answer when the conversation's last reply drew a view that failed its check (assistant/context_view_errors.go).
var viewErrorsAnswer = map[string]any{
	"text":                "- 现在：测试时间",
	"view_errors":         "<persona-view-errors>\nexample problem\n</persona-view-errors>",
	"view_error_receipts": []map[string]any{{"item_id": "item-1", "digest": "d1"}},
}

func (h *fakeHub) ackCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.acks)
}

// failingStdin plays a claude whose stdin is gone: the message does not go out.
type failingStdin struct{}

func (failingStdin) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }
func (failingStdin) Close() error              { return nil }

func viewErrorsClaude(t *testing.T, n *Node, stdin interface {
	Write([]byte) (int, error)
	Close() error
}) *assistantProc {
	p := &assistantProc{id: testThread, agent: "claude", launcher: "claude", cwd: t.TempDir(), stdin: stdin, cmd: &exec.Cmd{}, exited: make(chan struct{}), born: time.Now(),
		lastActive: time.Now(), canBypass: true, mode: proto.ModeBypass, fingerprint: n.executionFingerprint("claude", modelExecution{})}
	n.mu.Lock()
	n.procs[testThread] = p
	n.mu.Unlock()
	n.state.put(testThread, sessionState{Launcher: "claude", Cwd: p.cwd, StartedAt: time.Now().UTC()})
	return p
}

// Claude: the view errors go into the message itself after the snapshot, and are acked only once it was written.
func TestViewErrorsGoWithAClaudeMessageAndAreAckedAfterIt(t *testing.T) {
	n := codexTestNode(t)
	fh := withFakeHub(t, n)
	fh.mu.Lock()
	fh.context = viewErrorsAnswer
	fh.mu.Unlock()

	// A message that does not go out acks nothing: the errors wait for the next one.
	p := viewErrorsClaude(t, n, failingStdin{})
	if _, err := n.assistantSend(context.Background(), testThread, p.cwd, "claude", "hello", nil, "", ""); err == nil {
		t.Fatal("a failed write answered ok")
	}
	if fh.ackCount() != 0 {
		t.Fatal("view errors acked for a message that was never delivered")
	}

	in := &stdinBuf{}
	p = viewErrorsClaude(t, n, in)
	if _, err := n.assistantSend(context.Background(), testThread, p.cwd, "claude", "hello", nil, "", ""); err != nil {
		t.Fatal(err)
	}
	var msg struct {
		Message struct {
			Content []struct{ Text string } `json:"content"`
		} `json:"message"`
	}
	for line := range strings.SplitSeq(strings.TrimSpace(in.String()), "\n") {
		if strings.Contains(line, `"type":"user"`) {
			_ = json.Unmarshal([]byte(line), &msg)
		}
	}
	if c := msg.Message.Content; len(c) != 2 || !strings.HasPrefix(c[0].Text, "<persona-context>") ||
		!strings.HasSuffix(c[0].Text, "</persona-view-errors>") || c[1].Text != "hello" {
		t.Fatalf("message content %+v", msg.Message.Content)
	}
	waitFor(t, "the ack", func() bool { return fh.ackCount() == 1 })
	fh.mu.Lock()
	ack := string(fh.acks[0])
	fh.mu.Unlock()
	if ack != `[{"digest":"d1","item_id":"item-1"}]` {
		t.Fatalf("ack %s", ack)
	}
}

// Codex: the same, through its turn/start.
func TestViewErrorsGoWithACodexTurnAndAreAckedAfterIt(t *testing.T) {
	n := codexTestNode(t)
	fh := withFakeHub(t, n)
	fh.mu.Lock()
	fh.context = viewErrorsAnswer
	fh.mu.Unlock()
	const launcher = "cx_viewerrors"
	probeCache.Lock()
	probeCache.at[launcher], probeCache.got[launcher] = time.Now(), proto.LauncherDetail{Agent: "codex"}
	probeCache.Unlock()
	t.Cleanup(func() {
		probeCache.Lock()
		delete(probeCache.at, launcher)
		probeCache.Unlock()
	})
	cwd := t.TempDir()
	p, c, f := newFakeCodex(t, n, cwd)
	p.launcher = launcher
	p.fingerprint = n.executionFingerprint(launcher, modelExecution{})
	f.results["thread/start"] = map[string]any{"thread": map[string]any{"id": testThread}}
	if err := c.start(context.Background(), "", cwd, false); err != nil {
		t.Fatal(err)
	}
	n.mu.Lock()
	n.procs[testThread] = p
	n.mu.Unlock()
	n.state.put(testThread, sessionState{Launcher: launcher, Cwd: cwd, StartedAt: time.Now().UTC()})

	f.mu.Lock()
	f.errors = map[string]string{"turn/start": "overloaded"}
	f.mu.Unlock()
	if _, err := n.assistantSend(context.Background(), testThread, cwd, launcher, "hello", nil, "", ""); err == nil {
		t.Fatal("a refused turn answered ok")
	}
	if fh.ackCount() != 0 {
		t.Fatal("view errors acked for a turn Codex refused")
	}

	f.mu.Lock()
	f.errors = nil
	f.results["turn/start"] = map[string]any{"turn": map[string]any{"id": "turn-1", "status": "inProgress"}}
	f.mu.Unlock()
	if _, err := n.assistantSend(context.Background(), testThread, cwd, launcher, "hello", nil, "", ""); err != nil {
		t.Fatal(err)
	}
	ts := f.sent("turn/start")
	var turn struct {
		Input []struct{ Text string } `json:"input"`
	}
	_ = json.Unmarshal(ts[len(ts)-1].Params, &turn)
	if len(turn.Input) != 2 || !strings.Contains(turn.Input[0].Text, "<persona-view-errors>") || turn.Input[1].Text != "hello" {
		t.Fatalf("turn input %+v", turn.Input)
	}
	waitFor(t, "the ack", func() bool { return fh.ackCount() == 1 })
}

// An agent runtime: the errors go in session.send's context, acked once the runtime took the message.
func TestViewErrorsGoWithARuntimeMessageAndAreAckedAfterIt(t *testing.T) {
	n, hub := runtimeNode(t)
	assistantHub(hub)
	var mu sync.Mutex
	var acks []string
	hub.mu.Lock()
	base := hub.answer
	hub.answer = func(method string, params json.RawMessage) (any, error, bool) {
		if method != "assistant.context" {
			return base(method, params)
		}
		var p struct {
			Ack json.RawMessage `json:"ack"`
		}
		_ = json.Unmarshal(params, &p)
		if len(p.Ack) > 0 {
			mu.Lock()
			acks = append(acks, string(p.Ack))
			mu.Unlock()
			return map[string]any{}, nil, true
		}
		return viewErrorsAnswer, nil, true
	}
	hub.mu.Unlock()
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(acks)
	}
	host := dialRuntime(t, n, assistantRuntimeHello())
	sid := startedAssistant(t, n, host)
	waitFor(t, "the first ack", func() bool { return count() == 1 })
	var p struct {
		Context string `json:"context"`
	}
	_ = json.Unmarshal(host.got("session.send")[0], &p)
	if !strings.Contains(p.Context, "测试时间") || !strings.HasSuffix(p.Context, "</persona-view-errors>") {
		t.Fatalf("context %q", p.Context)
	}

	// The runtime refuses the next one: no ack.
	host.mu.Lock()
	answer := host.answer
	host.answer = func(method string, params json.RawMessage) (any, error) {
		if method == "session.send" {
			return nil, errors.New("busy")
		}
		return answer(method, params)
	}
	host.mu.Unlock()
	if _, err := nodeCall(t, n, "assistant.send", map[string]any{"session_id": sid, "text": "next"}); err == nil {
		t.Fatal("a refused send answered ok")
	}
	time.Sleep(50 * time.Millisecond)
	if count() != 1 {
		t.Fatal("view errors acked for a message the runtime refused")
	}
}

// Without view errors the message is as before and nothing is acked.
func TestNoViewErrorsNoBlockNoAck(t *testing.T) {
	n := codexTestNode(t)
	fh := withFakeHub(t, n)
	block, commit := n.turnContext(testThread, n.assistantContext(testThread))
	if strings.Contains(block, "persona-view-errors") {
		t.Fatalf("block %q", block)
	}
	commit()
	time.Sleep(20 * time.Millisecond)
	if fh.ackCount() != 0 {
		t.Fatal("acked without receipts")
	}
	// A block with only view errors (the snapshot unchanged) still goes, and its commit does not record a snapshot.
	const other = "22222222-3333-4444-5555-666666666666"
	block, commit = n.turnContext(other, contextAnswer{ViewErrors: "<persona-view-errors>\nx\n</persona-view-errors>"})
	if block != "<persona-view-errors>\nx\n</persona-view-errors>" {
		t.Fatalf("errors only %q", block)
	}
	commit()
	n.mu.Lock()
	_, seen := n.ctxSent[other]
	n.mu.Unlock()
	if seen {
		t.Fatal("an errors-only block recorded a snapshot as seen")
	}
}

// A fresh Claude that never starts did not get the message: the view errors it carried are not acked.
func TestViewErrorsNotAckedWhenClaudeDoesNotStart(t *testing.T) {
	fakeClaudeHome(t, "echo 'launch failed' >&2\nexit 1\n")
	n := codexTestNode(t)
	fh := withFakeHub(t, n)
	fh.mu.Lock()
	fh.context = viewErrorsAnswer
	fh.mu.Unlock()
	if _, err := n.assistantSend(context.Background(), "", t.TempDir(), "claude", "hello", nil, "", ""); err == nil {
		t.Fatal("a launcher that did not start answered ok")
	}
	time.Sleep(50 * time.Millisecond)
	if fh.ackCount() != 0 {
		t.Fatal("view errors acked for a process that never started")
	}
}
