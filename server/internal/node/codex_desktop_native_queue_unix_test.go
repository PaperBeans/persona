//go:build !windows

package node

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestCodexDesktopUserChatWithoutLegacySidebarEntry(t *testing.T) {
	home, _ := startFakeCodexBus(t, "")
	for _, tc := range []struct {
		name, origin, source, threadSource string
		want                               bool
	}{
		{"desktop user", "Codex Desktop", "vscode", "user", true},
		{"desktop subagent", "Codex Desktop", "vscode", "subagent", false},
		{"background job", "Codex Desktop", "exec", "user", false},
		{"CLI session", "codex-tui", "cli", "user", false},
		{"unclassified external process", "Codex Desktop", "vscode", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			meta := map[string]any{"type": "session_meta", "payload": map[string]any{
				"originator": tc.origin, "source": tc.source, "thread_source": tc.threadSource,
			}}
			line, _ := json.Marshal(meta)
			path, _ := appRollout(t, string(line))
			if got := codexDesktopChat(codexSource{home: home, thread: newUUID(), path: path}); got != tc.want {
				t.Fatalf("sendable=%t want %t", got, tc.want)
			}
		})
	}
}

func fakeNativeQueueCLI(t *testing.T, home, pending, reply string, dropAfterAdd bool) string {
	t.Helper()
	cli := filepath.Join(home, "codex")
	if reply == "" && !dropAfterAdd {
		reply = `{"id":3,"result":{"queuedSubmission":{"id":"ack","clientUserMessageId":"mobile-send-42"}}}`
	}
	addAction := `printf '%s\n' '` + reply + `'`
	if dropAfterAdd {
		addAction = `exit 0`
	}
	script := `#!/bin/sh
while IFS= read -r line; do
  printf '%s\n' "$line" >> "$CODEX_HOME/requests"
  case "$line" in
    *'"method":"initialize"'*) printf '%s\n' '{"id":1,"result":{}}';;
    *'"method":"thread/queue/list"'*) printf '%s\n' '{"id":2,"result":{"data":[{"clientUserMessageId":"` + pending + `"}]}}';;
    *'"method":"thread/queue/add"'*) ` + addAction + `;;
  esac
done
`
	if err := os.WriteFile(cli, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return cli
}

func TestCodexDesktopSendWithMissingFollowerBus(t *testing.T) {
	const thread = "018f0000-0000-4000-8000-000000000001"
	const clientID = "mobile-send-42"
	for _, tc := range []struct {
		name, pending, reply string
		stale, openFails     bool
		dropAfterAdd         bool
		wantAdd, wantQueued  bool
		wantUnknown          bool
	}{
		{name: "missing", wantAdd: true, wantQueued: true},
		{name: "stale and busy", stale: true, wantAdd: true, wantQueued: true},
		{name: "already persisted", pending: clientID, wantQueued: true},
		{name: "open failed", openFails: true},
		{name: "server refused", reply: `{"id":3,"error":{"code":-32602,"message":"thread archived"}}`, wantAdd: true},
		{name: "missing receipt", reply: `{"id":3,"result":{}}`, wantAdd: true, wantUnknown: true},
		{name: "transport closed after add", dropAfterAdd: true, wantAdd: true, wantUnknown: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, listener := listenFakeCodexBus(t)
			if tc.stale {
				listener.(*net.UnixListener).SetUnlinkOnClose(false)
			}
			listener.Close()
			if codexDesktopAvailable(home) {
				t.Fatal("a dead socket alone advertised desktop delivery")
			}
			cli := fakeNativeQueueCLI(t, home, tc.pending, tc.reply, tc.dropAfterAdd)
			prevCLI, prevOpen := codexDesktopQueueCLI, openCodexQueueThread
			t.Cleanup(func() { codexDesktopQueueCLI, openCodexQueueThread = prevCLI, prevOpen })
			codexDesktopQueueCLI = func(h string) string {
				if h == home {
					return cli
				}
				return ""
			}
			openCodexQueueThread = func(_ context.Context, bin string, dst codexSource) error {
				if bin != cli || dst.thread != thread || dst.home != home {
					t.Fatalf("wrong desktop destination %q %+v", bin, dst)
				}
				if tc.openFails {
					return errors.New("desktop launch failed")
				}
				return nil
			}
			if !codexDesktopAvailable(home) {
				t.Fatal("native queue not advertised")
			}
			if codexDesktopBusAvailable(home) {
				t.Fatal("native queue falsely advertised legacy follower bus")
			}
			ctx, queued := withSendQueued(context.WithValue(t.Context(), sendTraceKey{}, clientID))
			src := codexSource{home: home, thread: thread}
			if tc.stale {
				src.path, _ = appRollout(t, `{"type":"event_msg","payload":{"type":"task_started","turn_id":"busy"}}`)
			}
			err := (&Node{}).sendCodexDesktop(ctx, src, "hello\n世界", nil)
			if *queued != tc.wantQueued || (err == nil) != tc.wantQueued || maybeDelivered(err) != tc.wantUnknown {
				t.Fatalf("queued=%t err=%v", *queued, err)
			}
			wire, err := os.ReadFile(filepath.Join(home, "requests"))
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, line := range strings.Split(strings.TrimSpace(string(wire)), "\n") {
				var msg struct {
					Method string
					Params struct {
						ThreadID, ClientUserMessageID string
						Input                         []struct{ Text string }
					}
				}
				if err := json.Unmarshal([]byte(line), &msg); err != nil {
					t.Fatal(err)
				}
				switch msg.Method {
				case "thread/queue/add":
					count++
					p := msg.Params
					if p.ThreadID != thread || p.ClientUserMessageID != clientID || len(p.Input) != 1 || p.Input[0].Text != "hello\n世界" {
						t.Fatalf("changed queued input: %s", line)
					}
				case "thread/resume", "turn/start":
					t.Fatalf("queue client took ownership: %s", line)
				}
			}
			if (count == 1) != tc.wantAdd || count > 1 {
				t.Fatalf("queue writes=%d, wantAdd=%t", count, tc.wantAdd)
			}
		})
	}
}

// When only the macOS native thread queue is available (no legacy follower bus), App chats
// advertise Send without claiming Interrupt or Model/Effort switching, and those bus-only
// controls still fail closed with codex_read_only.
func TestCodexDesktopNativeQueueCapabilitiesAndControls(t *testing.T) {
	home := unavailableCodexBus(t, true)
	cli := fakeNativeQueueCLI(t, home, "", `{"id":3,"result":{"queuedSubmission":{"id":"ack","clientUserMessageId":"mobile-send-42"}}}`, false)
	prevCLI, prevOpen, prevAppCLI, prevSeed := codexDesktopQueueCLI, openCodexQueueThread, codexAppCLI, seedCodexAppChat
	t.Cleanup(func() {
		codexDesktopQueueCLI, openCodexQueueThread, codexAppCLI, seedCodexAppChat = prevCLI, prevOpen, prevAppCLI, prevSeed
	})
	n := remoteTestNode(t)
	n.home = filepath.Dir(home)
	n.configureCodex(proto.CodexConfig{})
	canonHome := n.codexHome()
	realHome := realPath(home)
	codexDesktopQueueCLI = func(h string) string {
		if h == home || h == canonHome || h == realHome {
			return cli
		}
		return ""
	}
	codexAppCLI = func() (string, error) { return cli, nil }
	openCodexQueueThread = func(_ context.Context, _ string, _ codexSource) error { return nil }

	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".codex-global-state.json"), []byte(`{"projectless-thread-ids":["`+appChat+`"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	path := writeAppRollout(t, home, appChat, cwd, time.Now())
	appendLines(t, path, event("task_started"))
	id := n.registerCodex(home, appChat, path)
	src, _ := n.codexSource(id)
	withCodexAppModels(t, src.home, map[string]codexModelEfforts{"gpt-x": {levels: []string{"low", "high"}, def: "low"}})

	row := appChatRow(t, n)
	if row.AgentName != "Codex App" || row.SpaceKind != "codex_app" || row.Status != proto.StatusWorking {
		t.Fatalf("App identity or status lost: %+v", row)
	}
	caps := row.Capabilities
	if caps == nil || !caps.Read || !caps.Send || caps.Interrupt || caps.Model != nil || caps.Takeover != nil || len(row.Efforts) != 0 || row.ReadOnlyReason != "" {
		t.Fatalf("queue-only row advertised wrong capabilities: %+v, efforts=%v, reason=%q", caps, row.Efforts, row.ReadOnlyReason)
	}

	// Bus-only controls still refuse cleanly with delivered: none.
	wantReason := i18n.T(i18n.Default, codexDesktopUnavailableKey)
	for name, run := range map[string]func() error{
		"interrupt": func() error { return n.interruptCodex(context.Background(), id) },
		"settings":  func() error { return modesCall(t, n, id, map[string]any{"effort": "high"}) },
	} {
		err := run()
		if code, data := refusalOf(t, err); code != "codex_read_only" || data["delivered"] != deliveredNone || err.Error() != wantReason {
			t.Fatalf("%s: %v, data %v", name, err, data)
		}
	}

	// The codex-app launcher is offered with locked model selection; custom model/effort refuses before seeding.
	if _, added := n.withCodexAppLauncher(nil); !added {
		t.Fatal("codex-app launcher not offered with native queue")
	}
	if models, err := n.launcherModels(context.Background(), codexAppLauncher, "", true); err != nil || models.Locked != codexAppModelLocked {
		t.Fatalf("launcher models = %+v, %v; want Locked=%q", models, err, codexAppModelLocked)
	}
	seeded := 0
	seedCodexAppChat = func(_ context.Context, _, _, _, _ string) (string, error) {
		seeded++
		return appChat, nil
	}
	if _, err := n.newCodexAppChat(context.Background(), runtimeStartParams{Cwd: cwd, Launcher: codexAppLauncher, Prompt: "hi", Model: "gpt-x"}, ""); refusalCode(err) != "codex_read_only" || seeded != 0 {
		t.Fatalf("custom model start = %v, seeded=%d; want refusal before seeding", err, seeded)
	}
	ctx := context.WithValue(context.Background(), sendTraceKey{}, "mobile-send-42")
	if s, err := n.newCodexAppChat(ctx, runtimeStartParams{Cwd: cwd, Launcher: codexAppLauncher, Prompt: "hi"}, ""); err != nil || s == nil || seeded != 1 {
		t.Fatalf("default-model new chat via native queue = (%+v, %v), seeded=%d", s, err, seeded)
	}
}

func TestStartCodexDesktopTurnFallsBackToNativeQueueWhenBusDisappears(t *testing.T) {
	const thread = "018f0000-0000-4000-8000-000000000002"
	const clientID = "mobile-send-42"
	home := unavailableCodexBus(t, true)
	cli := fakeNativeQueueCLI(t, home, "", `{"id":3,"result":{"queuedSubmission":{"id":"ack","clientUserMessageId":"`+clientID+`"}}}`, false)
	prevCLI, prevOpen := codexDesktopQueueCLI, openCodexQueueThread
	t.Cleanup(func() { codexDesktopQueueCLI, openCodexQueueThread = prevCLI, prevOpen })
	codexDesktopQueueCLI = func(h string) string {
		if h == home {
			return cli
		}
		return ""
	}
	openCodexQueueThread = func(_ context.Context, _ string, _ codexSource) error { return nil }

	ctx, queued := withSendQueued(context.WithValue(context.Background(), sendTraceKey{}, clientID))
	input := codexDesktopInput([]codexDesktopMessage{{text: "held follow-up"}})
	if err := (&Node{}).startCodexDesktopTurn(ctx, codexSource{home: home, thread: thread}, input); err != nil || !*queued {
		t.Fatalf("fallback startCodexDesktopTurn: err=%v queued=%v", err, *queued)
	}
	wire, err := os.ReadFile(filepath.Join(home, "requests"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), `"method":"thread/queue/add"`) || !strings.Contains(string(wire), `"held follow-up"`) {
		t.Fatalf("held turn did not fall back to thread/queue/add:\n%s", wire)
	}
}

func TestCodexDesktopNativeQueueSerializesConcurrentSends(t *testing.T) {
	const thread = "018f0000-0000-4000-8000-000000000003"
	home := unavailableCodexBus(t, true)
	cli := filepath.Join(home, "codex")
	script := `#!/bin/sh
while IFS= read -r line; do
  printf '%s\n' "$line" >> "$CODEX_HOME/requests"
  case "$line" in
    *'"method":"initialize"'*) printf '%s\n' '{"id":1,"result":{}}';;
    *'"method":"thread/queue/list"'*) printf '%s\n' '{"id":2,"result":{"data":[]}}';;
    *'"method":"thread/queue/add"'*)
      cid=$(printf '%s' "$line" | sed -n 's/.*"clientUserMessageId":"\([^"]*\)".*/\1/p')
      printf '{"id":3,"result":{"queuedSubmission":{"id":"ack","clientUserMessageId":"%s"}}}\n' "$cid"
      ;;
  esac
done
`
	if err := os.WriteFile(cli, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	prevCLI, prevOpen := codexDesktopQueueCLI, openCodexQueueThread
	t.Cleanup(func() { codexDesktopQueueCLI, openCodexQueueThread = prevCLI, prevOpen })
	codexDesktopQueueCLI = func(h string) string {
		if h == home {
			return cli
		}
		return ""
	}
	var inFlight, maxInFlight atomic.Int32
	hold := make(chan struct{})
	openCodexQueueThread = func(_ context.Context, _ string, _ codexSource) error {
		cur := inFlight.Add(1)
		for {
			prev := maxInFlight.Load()
			if cur <= prev || maxInFlight.CompareAndSwap(prev, cur) {
				break
			}
		}
		<-hold
		inFlight.Add(-1)
		return nil
	}

	n := &Node{dir: t.TempDir()}
	src := codexSource{home: home, thread: thread}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, id := range []string{"concurrent-1", "concurrent-2"} {
		wg.Add(1)
		go func(idx int, cid string) {
			defer wg.Done()
			ctx, queued := withSendQueued(context.WithValue(context.Background(), sendTraceKey{}, cid))
			err := n.sendCodexDesktop(ctx, src, cid, nil)
			if err == nil && !*queued {
				err = errors.New("expected queued receipt")
			}
			errs[idx] = err
		}(i, id)
	}
	close(hold)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	if got := maxInFlight.Load(); got != 1 {
		t.Fatalf("max concurrent native-queue sends for same thread = %d, want 1", got)
	}
}

func TestCodexDesktopHeldMessageFallbackPreservesClientIDAndOrder(t *testing.T) {
	fastCodexDesktopHold(t)
	const thread = "018f0000-0000-4000-8000-000000000004"
	const firstID = "held-trace-id-1"
	home, listener := listenFakeCodexBus(t)
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	cli := fakeNativeQueueCLI(t, home, "", `{"id":3,"result":{"queuedSubmission":{"id":"ack","clientUserMessageId":"`+firstID+`"}}}`, false)
	prevCLI, prevOpen := codexDesktopQueueCLI, openCodexQueueThread
	t.Cleanup(func() { codexDesktopQueueCLI, openCodexQueueThread = prevCLI, prevOpen })
	codexDesktopQueueCLI = func(h string) string {
		if h == home {
			return cli
		}
		return ""
	}
	openCodexQueueThread = func(_ context.Context, _ string, _ codexSource) error { return nil }

	path, write := appRollout(t, turnStarted)
	n := &Node{dir: t.TempDir()}
	src := codexSource{home: home, thread: thread, path: path}

	// First send while bus is listening and turn is running: held in order with firstID.
	ctx1, q1 := withSendQueued(context.WithValue(context.Background(), sendTraceKey{}, firstID))
	if err := n.sendCodexDesktop(ctx1, src, "first held", nil); err != nil || !*q1 {
		t.Fatalf("first send: err=%v queued=%v", err, *q1)
	}
	// Legacy bus disappears before the turn completes.
	listener.Close()
	<-acceptDone

	// Second send while first is still held must stay behind it instead of overtaking into native queue.
	ctx2, q2 := withSendQueued(context.WithValue(context.Background(), sendTraceKey{}, "held-trace-id-2"))
	if err := n.sendCodexDesktop(ctx2, src, "second held", nil); err != nil || !*q2 {
		t.Fatalf("second send: err=%v queued=%v", err, *q2)
	}
	if _, err := os.Stat(filepath.Join(home, "requests")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("second send overtook held message before running turn ended: stat err=%v", err)
	}

	// Turn completes: held delivery falls back to native queue preserving order and firstID.
	write(turnComplete)
	var wire []byte
	waitFor(t, "held native queue delivery", func() bool {
		b, readErr := os.ReadFile(filepath.Join(home, "requests"))
		if readErr == nil && strings.Contains(string(b), `"method":"thread/queue/add"`) {
			wire = b
			return true
		}
		return false
	})
	if len(wire) == 0 {
		t.Fatal("held messages were not delivered via native queue fallback after turn completed")
	}
	for _, line := range strings.Split(strings.TrimSpace(string(wire)), "\n") {
		var msg struct {
			Method string
			Params struct {
				ThreadID, ClientUserMessageID string
				Input                         []struct{ Text string }
			}
		}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatal(err)
		}
		if msg.Method == "thread/queue/add" {
			if msg.Params.ThreadID != thread || msg.Params.ClientUserMessageID != firstID {
				t.Fatalf("fallback thread/clientID mismatch: %s", line)
			}
			if len(msg.Params.Input) != 1 || msg.Params.Input[0].Text != "first held\n\nsecond held" {
				t.Fatalf("fallback input order mismatch: %s", line)
			}
			return
		}
	}
	t.Fatalf("no thread/queue/add found in requests:\n%s", wire)
}
