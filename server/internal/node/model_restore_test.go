package node

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/proto"
)

// Synthetic transcript lines only (#2528): placeholder models, no real conversation.
const (
	rsID       = "33333333-4444-4555-8666-777777777777"
	rsUser     = `{"type":"user","uuid":"u1","sessionId":"` + rsID + `","cwd":"/tmp/synthetic","timestamp":"2026-11-09T01:00:00Z","message":{"role":"user","content":"synthetic question"}}`
	rsReplyOld = `{"type":"assistant","uuid":"a0","timestamp":"2026-11-09T01:00:01Z","message":{"role":"assistant","model":"primary-model","content":[{"type":"text","text":"synthetic answer"}]}}`
	rsRefusal  = `{"type":"system","subtype":"model_refusal_fallback","uuid":"s1","timestamp":"2026-11-09T01:00:02Z","scope":"session","originalModel":"primary-model","fallbackModel":"fallback-model","apiRefusalCategory":"synthetic-policy","apiRefusalExplanation":"Upstream refused the request","content":"Synthetic refusal content"}`
	rsNotFound = `{"type":"assistant","uuid":"a1","timestamp":"2026-11-09T01:00:03Z","isApiErrorMessage":true,"error":"model_not_found","apiErrorStatus":404,"message":{"role":"assistant","model":"<synthetic>","content":[{"type":"text","text":"API Error: 404 model fallback-model not available"}]}}`
)

func rsReset(ts string) string {
	return `{"type":"assistant","uuid":"e` + ts + `","timestamp":"` + ts + `","isApiErrorMessage":true,"error":"server_error","message":{"role":"assistant","model":"<synthetic>","content":[{"type":"text","text":"API Error: read ECONNRESET"}]}}`
}

func rsErrNamingModel(ts, model string) string { // an API error line that names a real model: no reply from it
	return `{"type":"assistant","uuid":"m` + ts + `","timestamp":"` + ts + `","isApiErrorMessage":true,"error":"server_error","message":{"role":"assistant","model":"` + model + `","content":[{"type":"text","text":"API Error: 500"}]}}`
}

func rsReply(ts, model string) string {
	return `{"type":"assistant","uuid":"r` + ts + `","timestamp":"` + ts + `","message":{"role":"assistant","model":"` + model + `","content":[{"type":"text","text":"synthetic reply"}]}}`
}

// stopCounter is a fake process's stdin: Close is the process being stopped, Write a message sent to it.
type stopCounter struct {
	mu             sync.Mutex
	closes, writes int
}

func (c *stopCounter) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writes++
	return len(p), nil
}

func (c *stopCounter) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closes++
	return nil
}

func (c *stopCounter) counts() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closes, c.writes
}

func rsProc(id string) (*assistantProc, *stopCounter) {
	c := &stopCounter{}
	return &assistantProc{id: id, agent: "claude", stdin: c, cmd: &exec.Cmd{}, exited: make(chan struct{})}, c
}

type rsEnv struct {
	n    *Node
	fh   *fakeHub
	path string
}

func newRestoreEnv(t *testing.T, lines ...string) *rsEnv {
	t.Helper()
	n := codexTestNode(t)
	fh := withFakeHub(t, n)
	fh.mu.Lock()
	fh.model = map[string]any{"launcher": "claude", "model": "configured-model"}
	fh.mu.Unlock()
	path := filepath.Join(t.TempDir(), rsID+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	n.mu.Lock()
	n.paths[rsID] = path
	n.mu.Unlock()
	return &rsEnv{n: n, fh: fh, path: path}
}

func (e *rsEnv) appendLines(t *testing.T, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(e.path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		t.Fatal(err)
	}
}

func (e *rsEnv) setProc(p *assistantProc) {
	e.n.mu.Lock()
	e.n.procs[rsID] = p
	e.n.mu.Unlock()
}

func (e *rsEnv) proc() *assistantProc {
	e.n.mu.Lock()
	defer e.n.mu.Unlock()
	return e.n.procs[rsID]
}

// view is the session as a list shows it, from a fresh read of the transcript.
func (e *rsEnv) view(t *testing.T) proto.Session {
	t.Helper()
	st, err := os.Stat(e.path)
	if err != nil {
		t.Fatal(err)
	}
	e.n.mu.Lock()
	if m, ok := e.n.metas[e.path]; ok {
		m.readAt = time.Time{}
		e.n.metas[e.path] = m
	}
	e.n.mu.Unlock()
	m := e.n.meta(e.path, st)
	s := proto.Session{SessionID: rsID, ModelFallback: m.modelFallback}
	m.turnErr.set(&s)
	e.n.setModelRestore(&s, e.path, m)
	return s
}

func rsRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Restore closes the idle process and sends nothing: no message to it, the transcript (history, todo links, the
// session id, the refusal record) byte for byte the same; the answer names the configured model the hub gives, not
// one inferred from the refusal. The refusal signal and the fallback's error stay as they were.
func TestRestoreModelClosesIdleSendsNothing(t *testing.T) {
	e := newRestoreEnv(t, rsUser, rsReplyOld, rsRefusal, rsNotFound)
	p, c := rsProc(rsID)
	e.setProc(p)
	before := rsRead(t, e.path)
	beforeView := e.view(t)
	res, err := e.n.restoreModel(context.Background(), rsID)
	if err != nil {
		t.Fatal(err)
	}
	if res["restore"] != proto.ModelRestoreNextStart || res["restore_to"] != "configured-model" || res["closed"] != true {
		t.Fatalf("result %v", res)
	}
	if closes, writes := c.counts(); closes != 1 || writes != 0 {
		t.Fatalf("closes %d writes %d, want 1 and 0", closes, writes)
	}
	if e.proc() != nil {
		t.Fatal("process still listed")
	}
	if after := rsRead(t, e.path); !bytes.Equal(before, after) {
		t.Fatal("transcript changed")
	}
	e.fh.mu.Lock()
	deltas, asked, queried := len(e.fh.deltas), len(e.fh.asked), e.fh.queried["assistant.model"]
	e.fh.mu.Unlock()
	if deltas != 0 || asked != 0 {
		t.Fatalf("%d deltas, %d interactions sent", deltas, asked)
	}
	if queried != rsID {
		t.Fatalf("asked the hub about %q", queried)
	}
	s := e.view(t)
	if s.ModelFallback == nil || *s.ModelFallback != *beforeView.ModelFallback {
		t.Fatalf("refusal signal changed: %+v, was %+v", s.ModelFallback, beforeView.ModelFallback)
	}
	if s.TurnErrorCode != beforeView.TurnErrorCode || s.TurnError != beforeView.TurnError {
		t.Fatalf("turn error rewritten: %q/%q, was %q/%q", s.TurnError, s.TurnErrorCode, beforeView.TurnError, beforeView.TurnErrorCode)
	}
	r := s.ModelRestore
	if r == nil || r.State != proto.ModelRestoreNextStart || r.To != "configured-model" || r.At.IsZero() {
		t.Fatalf("restore %+v", r)
	}
}

// No process running (it was let go already): nothing to close, the next start uses the configured model all the same.
func TestRestoreModelWithoutProcess(t *testing.T) {
	e := newRestoreEnv(t, rsUser, rsRefusal, rsNotFound)
	e.n.state.put(rsID, sessionState{Cwd: identity.AssistantDir(e.n.home)})
	res, err := e.n.restoreModel(context.Background(), rsID)
	if err != nil {
		t.Fatal(err)
	}
	if res["closed"] != false || res["restore_to"] != "configured-model" {
		t.Fatalf("result %v", res)
	}
	if r := e.view(t).ModelRestore; r == nil || r.To != "configured-model" {
		t.Fatalf("restore %+v", r)
	}
}

// A process that looks idle but is not (a turn, background tasks, a compaction, a control request waiting for its
// answer) is never closed, and no restore is marked.
func TestRestoreModelKeepsWorkingProcess(t *testing.T) {
	for _, c := range []struct {
		name string
		set  func(p *assistantProc)
	}{
		{"busy", func(p *assistantProc) { p.busy = true }},
		{"background task", func(p *assistantProc) { p.tasks = []string{"synthetic monitor"} }},
		{"idle compaction", func(p *assistantProc) { p.compacting = true }},
		{"manual compaction", func(p *assistantProc) { p.manual = true }},
		{"pending control", func(p *assistantProc) {
			p.controls = map[string]chan assistantControlResponse{"req-1": make(chan assistantControlResponse, 1)}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newRestoreEnv(t, rsUser, rsRefusal, rsNotFound)
			p, counter := rsProc(rsID)
			c.set(p)
			e.setProc(p)
			if _, err := e.n.restoreModel(context.Background(), rsID); codeOf(err) != restoreBusy {
				t.Fatalf("err %v, want %s", err, restoreBusy)
			}
			if closes, writes := counter.counts(); closes != 0 || writes != 0 {
				t.Fatalf("closes %d writes %d", closes, writes)
			}
			if e.proc() != p {
				t.Fatal("process taken out")
			}
			if s := e.view(t); s.ModelFallback == nil || s.ModelRestore != nil {
				t.Fatalf("fallback %+v restore %+v", s.ModelFallback, s.ModelRestore)
			}
		})
	}
}

// A permission card waiting for the user (a PermissionRequest hook in flight) keeps the process; once answered,
// restore goes ahead.
func TestRestoreModelWaitsOutPermissionRequest(t *testing.T) {
	e := newRestoreEnv(t, rsUser, rsRefusal, rsNotFound)
	p, counter := rsProc(rsID)
	e.setProc(p)
	asked, release := make(chan struct{}), make(chan struct{})
	e.fh.mu.Lock()
	e.fh.answer = func(proto.Interaction) proto.Resolution {
		close(asked)
		<-release
		yes := true
		return proto.Resolution{Allow: &yes}
	}
	e.fh.mu.Unlock()
	raw, _ := json.Marshal(hookInput{HookEventName: "PermissionRequest", SessionID: rsID, ToolName: "Bash",
		ToolUseID: "toolu_synthetic", ToolInput: json.RawMessage(`{"command":"true"}`)})
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := e.n.handleHook(ctx, sockRequest{Input: raw, Attempt: "a1"})
		done <- err
	}()
	select {
	case <-asked:
	case <-time.After(10 * time.Second):
		t.Fatal("the permission card never reached the hub")
	}
	if _, err := e.n.restoreModel(context.Background(), rsID); codeOf(err) != restoreBusy {
		t.Fatalf("err %v while a permission card waits, want busy", err)
	}
	if closes, _ := counter.counts(); closes != 0 {
		t.Fatal("closed while a permission card waits")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := e.n.restoreModel(context.Background(), rsID); err != nil {
		t.Fatal(err)
	}
	if closes, _ := counter.counts(); closes != 1 {
		t.Fatalf("closes %d after the card was answered", closes)
	}
}

// A send or compaction holding the conversation (assistantLife) makes restore answer busy at once instead of
// waiting: a request cancelled meanwhile can never close the process after. A cancelled request closes nothing.
func TestRestoreModelLockHeldThenCancelled(t *testing.T) {
	e := newRestoreEnv(t, rsUser, rsRefusal, rsNotFound)
	p, counter := rsProc(rsID)
	e.setProc(p)
	unlock := e.n.assistantLife.lock(rsID)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := e.n.restoreModel(ctx, rsID); done <- err }()
	var err error
	answered := true
	select {
	case err = <-done:
	case <-time.After(5 * time.Second): // a bound: a restore that waits for the lock never answers
		answered = false
	}
	cancel()
	unlock()
	if !answered {
		err = <-done
		if closes, _ := counter.counts(); closes != 0 {
			t.Fatalf("a cancelled request closed the process after the lock came free (%v)", err)
		}
		t.Fatal("restore waited for the lock instead of answering busy")
	}
	if codeOf(err) != restoreBusy {
		t.Fatalf("err %v with the lock held, want busy", err)
	}
	if closes, _ := counter.counts(); closes != 0 || e.proc() != p {
		t.Fatalf("closes %d", closes)
	}
	if _, err := e.n.restoreModel(ctx, rsID); err == nil {
		t.Fatal("a cancelled request restored")
	}
	if closes, _ := counter.counts(); closes != 0 || e.proc() != p {
		t.Fatalf("closes %d after a cancelled request", closes)
	}
}

// The process restore looked at was replaced (a send started a new one) before it stopped it: neither is closed.
func TestStopIdleAssistantReplacedProcess(t *testing.T) {
	e := newRestoreEnv(t, rsUser, rsRefusal)
	old, oc := rsProc(rsID)
	cur, cc := rsProc(rsID)
	e.setProc(cur)
	if e.n.stopIdleAssistant(rsID, old) {
		t.Fatal("stopped a process no longer listed")
	}
	if a, _ := oc.counts(); a != 0 {
		t.Fatal("old process closed")
	}
	if b, _ := cc.counts(); b != 0 || e.proc() != cur {
		t.Fatal("current process touched")
	}
}

// The warm spare is no conversation's until adoptSpare gives it one: restore leaves it alone.
func TestRestoreModelLeavesSpare(t *testing.T) {
	e := newRestoreEnv(t, rsUser, rsRefusal, rsNotFound)
	sp, sc := rsProc("")
	e.n.mu.Lock()
	e.n.spare = sp
	e.n.mu.Unlock()
	p, _ := rsProc(rsID)
	e.setProc(p)
	if _, err := e.n.restoreModel(context.Background(), rsID); err != nil {
		t.Fatal(err)
	}
	e.n.mu.Lock()
	still := e.n.spare == sp
	e.n.mu.Unlock()
	if closes, writes := sc.counts(); !still || closes != 0 || writes != 0 {
		t.Fatalf("spare kept %v closes %d writes %d", still, closes, writes)
	}
}

// A terminal (TUI) session is refused with the manual step; nothing is typed, closed or asked of the hub.
func TestRestoreModelRefusesTerminal(t *testing.T) {
	e := newRestoreEnv(t, rsUser, rsRefusal, rsNotFound)
	e.n.mu.Lock()
	e.n.agents[rsID] = host.Agent{PaneID: "synthetic-pane"}
	e.n.mu.Unlock()
	_, err := e.n.restoreModel(context.Background(), rsID)
	if codeOf(err) != restoreUnsupported || !strings.Contains(err.Error(), "/model primary-model") {
		t.Fatalf("err %v", err)
	}
	e.fh.mu.Lock()
	_, asked := e.fh.queried["assistant.model"]
	e.fh.mu.Unlock()
	if asked {
		t.Fatal("asked the hub for a model for a terminal session")
	}
	if r := e.view(t).ModelRestore; r != nil {
		t.Fatalf("restore %+v", r)
	}
}

// No configured model the hub can name: refused, nothing closed, nothing inferred from the refusal.
func TestRestoreModelNeedsConfiguredModel(t *testing.T) {
	e := newRestoreEnv(t, rsUser, rsRefusal, rsNotFound)
	e.fh.mu.Lock()
	e.fh.model = nil
	e.fh.mu.Unlock()
	p, c := rsProc(rsID)
	e.setProc(p)
	if _, err := e.n.restoreModel(context.Background(), rsID); codeOf(err) != restoreUnknown {
		t.Fatalf("err %v", err)
	}
	if closes, _ := c.counts(); closes != 0 || e.proc() != p {
		t.Fatal("closed without a configured model")
	}
}

// Without a fallback there is nothing to restore; without a record, nothing to look at.
func TestRestoreModelWithoutFallback(t *testing.T) {
	e := newRestoreEnv(t, rsUser, rsReplyOld)
	p, c := rsProc(rsID)
	e.setProc(p)
	if _, err := e.n.restoreModel(context.Background(), rsID); codeOf(err) != restoreNothing {
		t.Fatalf("err %v", err)
	}
	if closes, _ := c.counts(); closes != 0 {
		t.Fatal("closed")
	}
	if _, err := e.n.restoreModel(context.Background(), "44444444-4444-4444-8444-444444444444"); codeOf(err) != restoreGone {
		t.Fatalf("unknown session: %v", err)
	}
}

// The pending restore is the next start's only: API errors after it (a network error, an error line naming a model)
// and reading the same lines again keep it; model evidence (a real reply from any model, a new refusal) ends it.
func TestRestoreMarkLifetime(t *testing.T) {
	for _, c := range []struct {
		name  string
		after []string
		kept  bool
		fb    bool
	}{
		{"network error", []string{rsReset("2026-11-09T01:10:00Z")}, true, true},
		{"error naming the fallback", []string{rsErrNamingModel("2026-11-09T01:10:00Z", "fallback-model")}, true, true},
		// The transcript reader (#2289) takes any line naming a model other than the fallback's as the switch back,
		// API error lines included; with the fallback gone there is no restore to show either.
		{"error naming the configured model", []string{rsErrNamingModel("2026-11-09T01:10:00Z", "configured-model")}, false, false},
		{"nothing new", nil, true, true},
		{"reply from the fallback", []string{rsReply("2026-11-09T01:10:00Z", "fallback-model")}, false, true},
		{"a new refusal", []string{strings.Replace(rsRefusal, "01:00:02", "01:10:00", 1)}, false, true},
		{"reply from the configured model", []string{rsReply("2026-11-09T01:10:00Z", "configured-model")}, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newRestoreEnv(t, rsUser, rsReplyOld, rsRefusal, rsNotFound)
			p, _ := rsProc(rsID)
			e.setProc(p)
			if _, err := e.n.restoreModel(context.Background(), rsID); err != nil {
				t.Fatal(err)
			}
			if len(c.after) > 0 {
				e.appendLines(t, c.after...)
			}
			for i := range 2 { // the second read is the same lines again
				s := e.view(t)
				if (s.ModelFallback != nil) != c.fb {
					t.Fatalf("read %d: fallback %+v", i, s.ModelFallback)
				}
				if kept := s.ModelRestore != nil; kept != c.kept {
					t.Fatalf("read %d: restore pending %v, want %v", i, kept, c.kept)
				}
			}
		})
	}
}

// A half-written line after the restore is looked at again once whole: a reply that ends there still counts.
func TestRestoreMarkPartialLine(t *testing.T) {
	e := newRestoreEnv(t, rsUser, rsRefusal, rsNotFound)
	e.n.state.put(rsID, sessionState{Cwd: identity.AssistantDir(e.n.home)})
	if _, err := e.n.restoreModel(context.Background(), rsID); err != nil {
		t.Fatal(err)
	}
	reply := rsReply("2026-11-09T01:10:00Z", "fallback-model")
	f, err := os.OpenFile(e.path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(reply[:len(reply)/2]); err != nil {
		t.Fatal(err)
	}
	if s := e.view(t); s.ModelRestore == nil {
		t.Fatal("half a line ended the restore")
	}
	if _, err := f.WriteString(reply[len(reply)/2:] + "\n"); err != nil {
		t.Fatal(err)
	}
	if s := e.view(t); s.ModelRestore != nil {
		t.Fatalf("the finished reply did not end the restore: %+v", s.ModelRestore)
	}
}

// The configured model is asked for under the lock, after the fresh read; a request given up while the hub answers
// closes nothing; and the answer names the setting as it is then.
func TestRestoreModelCancelledDuringReads(t *testing.T) {
	e := newRestoreEnv(t, rsUser, rsReplyOld, rsRefusal, rsNotFound)
	p, c := rsProc(rsID)
	e.setProc(p)
	ctx, cancel := context.WithCancel(context.Background())
	var heldDuring bool
	e.fh.mu.Lock()
	e.fh.onModel = func() {
		if unlock, ok := e.n.assistantLife.tryLock(rsID); ok {
			unlock()
		} else {
			heldDuring = true
		}
		cancel()
	}
	e.fh.mu.Unlock()
	if _, err := e.n.restoreModel(ctx, rsID); err == nil {
		t.Fatal("a request given up while the hub answered went on")
	}
	if !heldDuring {
		t.Fatal("the configured model was asked for outside the lock")
	}
	if closes, writes := c.counts(); closes != 0 || writes != 0 || e.proc() != p {
		t.Fatalf("a given-up request closed %d, sent %d", closes, writes)
	}
	if s := e.view(t); s.ModelRestore != nil {
		t.Fatalf("a given-up request left a mark: %+v", s.ModelRestore)
	}
	e.fh.mu.Lock()
	e.fh.onModel = nil
	e.fh.model = map[string]any{"launcher": "claude", "model": "configured-model-2"}
	e.fh.mu.Unlock()
	res, err := e.n.restoreModel(context.Background(), rsID)
	if err != nil || res["restore_to"] != "configured-model-2" {
		t.Fatalf("res %v err %v", res, err)
	}
	if s := e.view(t); s.ModelRestore == nil || s.ModelRestore.To != "configured-model-2" {
		t.Fatalf("mark %+v", s.ModelRestore)
	}
}

// compactIdle leaves a process a send, a restore or the user's compaction holds, and one no longer listed.
func TestCompactIdleTakesTurns(t *testing.T) {
	e := newRestoreEnv(t, rsUser)
	p, c := rsProc(rsID)
	e.setProc(p)
	unlock := e.n.assistantLife.lock(rsID)
	e.n.compactIdle(p)
	unlock()
	if _, writes := c.counts(); writes != 0 {
		t.Fatal("compacted while the conversation was held")
	}
	other, oc := rsProc(rsID)
	e.n.compactIdle(other) // not the listed process
	if _, writes := oc.counts(); writes != 0 {
		t.Fatal("compacted a process no longer listed")
	}
	e.n.compactIdle(p)
	if _, writes := c.counts(); writes != 1 {
		t.Fatalf("writes %d, want the one /compact", writes)
	}
}

// The session list carries the pending restore on the wire, and a list built again says the same.
func TestModelRestoreOnTheWire(t *testing.T) {
	e := newRestoreEnv(t, rsUser, rsRefusal, rsNotFound)
	e.n.state.put(rsID, sessionState{Cwd: identity.AssistantDir(e.n.home)})
	if _, err := e.n.restoreModel(context.Background(), rsID); err != nil {
		t.Fatal(err)
	}
	s := e.view(t)
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"model_restore":{"state":"next_start","to":"configured-model","at":`)) || !bytes.Contains(b, []byte(`"model_fallback":{"subtype":"model_refusal_fallback"`)) {
		t.Fatalf("wire %s", b)
	}
	if a, b := listKey([]proto.Session{s}), listKey([]proto.Session{{SessionID: rsID, ModelFallback: s.ModelFallback, TurnError: s.TurnError, TurnErrorKind: s.TurnErrorKind, TurnErrorCode: s.TurnErrorCode}}); a == b {
		t.Fatal("the list key does not change with a pending restore: it would not be pushed")
	}
}

// The node answers session.restore_model by its own name (the hub relays only the session id).
func TestRestoreModelDispatch(t *testing.T) {
	e := newRestoreEnv(t, rsUser, rsRefusal, rsNotFound)
	p, c := rsProc(rsID)
	e.setProc(p)
	out, err := e.n.handle(context.Background(), "session.restore_model", json.RawMessage(`{"session_id":"`+rsID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res, _ := out.(map[string]any); res["restore"] != proto.ModelRestoreNextStart || res["closed"] != true {
		t.Fatalf("answer %v", out)
	}
	if closes, writes := c.counts(); closes != 1 || writes != 0 {
		t.Fatalf("closes %d writes %d", closes, writes)
	}
}

// A long transcript whose fallback is beyond the quick tail and still being read in the background is not taken for
// "no fallback": the restore is refused as unknown and nothing is closed.
func TestRestoreModelWhileScanPending(t *testing.T) {
	blob := `{"type":"api-request-blob","content":"` + strings.Repeat("x", quickTail+1) + `"}`
	e := newRestoreEnv(t, rsUser, rsRefusal, blob, rsNotFound)
	e.n.mu.Lock()
	e.n.bgScans[e.path] = &bgScan{pending: true} // the first read of a big transcript is running
	e.n.mu.Unlock()
	p, c := rsProc(rsID)
	e.setProc(p)
	if _, err := e.n.restoreModel(context.Background(), rsID); codeOf(err) != restoreUnknown {
		t.Fatalf("err %v, want %s", err, restoreUnknown)
	}
	if closes, _ := c.counts(); closes != 0 || e.proc() != p {
		t.Fatal("closed while the transcript was unread")
	}
}

// The list's read may be seconds old (metaMinInterval): a reply written since that ended the fallback is read again
// under the lock, and nothing is closed for a fallback that is over.
func TestRestoreModelReadsAgain(t *testing.T) {
	e := newRestoreEnv(t, rsUser, rsRefusal, rsNotFound)
	p, c := rsProc(rsID)
	e.setProc(p)
	if s := e.view(t); s.ModelFallback == nil {
		t.Fatal("no fallback to begin with")
	}
	e.appendLines(t, rsReply("2026-11-09T01:10:00Z", "primary-model")) // within the interval of the read above
	if _, err := e.n.restoreModel(context.Background(), rsID); codeOf(err) != restoreNothing {
		t.Fatalf("err %v, want %s", err, restoreNothing)
	}
	if closes, _ := c.counts(); closes != 0 || e.proc() != p {
		t.Fatal("closed for a fallback that is over")
	}
}
