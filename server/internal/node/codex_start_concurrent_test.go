package node

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/screen"
)

func TestCodexStartKeepsRolloutHome(t *testing.T) {
	const rawID = "aaaaaaaa-0000-4000-8000-000000000001"
	const prompt = "check the project"
	for _, tc := range []struct {
		name      string
		alternate bool
		paneKnown bool
		lateHome  bool
	}{
		{name: "default"},
		{name: "alternate, pane home unknown", alternate: true},
		{name: "alternate, pane home known", alternate: true, paneKnown: true},
		{name: "alternate discovered during start", alternate: true, lateHome: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, f := stalledCodexNode(t)
			codexStartWait = time.Second
			n.codices = newCodexRegistry()
			n.configureCodex(proto.CodexConfig{})
			cwd := filepath.Join(n.home, "proj")
			root := n.home
			if tc.alternate {
				root = t.TempDir()
				// An independent Codex may have imported the same native thread ID.
				otherPrompt := "another conversation"
				if tc.paneKnown {
					otherPrompt = prompt
				}
				writeThread(t, n.home, rawID, cwd, otherPrompt)
			}
			home := canonicalCodexHome(filepath.Join(root, ".codex"), n.home)
			writeThread(t, root, rawID, cwd, prompt)
			discover := func() {
				n.configureCodex(proto.CodexConfig{Homes: []string{home}})
				if tc.paneKnown {
					n.codices.mu.Lock()
					n.codices.byPane = map[string]string{"w1:p2": home}
					n.codices.mu.Unlock()
				}
			}
			if tc.lateHome {
				var once sync.Once
				f.after(func(c herdrCall) {
					if c.Method == "pane.read" && c.Params["lines"] == float64(screen.DialogLines) {
						once.Do(discover) // after the first unsuccessful rollout lookup
					}
				})
			} else {
				discover()
			}

			s, err := n.startCodex(t.Context(), n.defaultHost().(host.Launcher), cwd, "codex", []string{"codex"}, modelExecution{}, prompt, "w1", "", "")
			if err != nil {
				t.Fatal(err)
			}
			want := codexSourceID(home, rawID, n.codexInventory().LegacyHome)
			if s.SessionID != want || n.panes["w1:p2"] != want {
				t.Fatalf("start returned %q, bound %q, want %q", s.SessionID, n.panes["w1:p2"], want)
			}
			gotHome, thread, ok := n.codexThread(s.SessionID)
			if !ok || gotHome != home || thread != rawID {
				t.Fatalf("start routes to (%q, %q), want (%q, %q)", gotHome, thread, home, rawID)
			}
		})
	}
}

// Both requests must reach their own thread wait before either is released. A lock spanning startCodex would
// prevent the second request from reaching this barrier until the first timed out or was cancelled.
func TestCodexStartsInSameDirectoryRunConcurrently(t *testing.T) {
	for _, cleanupFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("cleanupFails=%v", cleanupFails), func(t *testing.T) {
			n, f := stalledCodexNode(t)
			codexStartWait = time.Minute
			next := 0
			f.handle("tab.create", func(map[string]any) any {
				next++
				return map[string]any{"tab": map[string]any{"tab_id": fmt.Sprintf("w1:t%d", next)}, "root_pane": map[string]any{"pane_id": fmt.Sprintf("w1:p%d", next)}}
			})
			if cleanupFails {
				f.reply("tab.close", herdrFail{Code: "unavailable", Message: "terminal unavailable"})
			}
			waiting := make(chan string, 2)
			seen := map[string]bool{}
			f.after(func(c herdrCall) {
				if c.Method == "pane.read" && c.Params["source"] == "visible" && c.Params["lines"] == float64(screen.DialogLines) {
					pane := str(c.Params["pane_id"])
					if !seen[pane] {
						seen[pane] = true
						waiting <- pane
					}
				}
			})
			ctx, cancel := context.WithCancel(context.Background())
			results := make(chan error, 2)
			var wg sync.WaitGroup
			defer func() { cancel(); wg.Wait() }()
			cwd := filepath.Join(filepath.Dir(n.dir), "proj")
			launch := n.defaultHost().(host.Launcher)
			panes := map[string]bool{}
			for i := range 2 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, err := n.startCodex(ctx, launch, cwd, "codex", []string{"codex"}, modelExecution{}, fmt.Sprintf("subtask %d", i), "w1", "", "")
					results <- err
				}()
				select {
				case pane := <-waiting:
					panes[pane] = true
				case err := <-results:
					t.Fatalf("start returned before both thread waits: %v", err)
				case <-time.After(5 * time.Second):
					t.Fatal("one start blocked another from waiting for its own thread")
				}
			}
			if len(panes) != 2 {
				t.Fatalf("starts did not get distinct panes: %v", panes)
			}
			select {
			case err := <-results:
				t.Fatalf("start ended before the concurrency barrier: %v", err)
			default:
			}
			cancel()
			for range 2 {
				err := <-results
				if cleanupFails {
					var coded *rpc.Error
					if !errors.As(err, &coded) || coded.Code != proto.CodeAgentStartStalled {
						t.Fatalf("cleanup failure lost code: %v", err)
					}
				} else if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled wait: %v", err)
				}
			}
			key := cwd + "\x00codex"
			n.mu.Lock()
			pending := len(n.codexAbandoned[key])
			n.mu.Unlock()
			want := 0
			if cleanupFails {
				want = 2
			}
			if pending != want {
				t.Fatalf("pending cleanups = %d, want %d", pending, want)
			}
			if cleanupFails {
				f.reply("tab.close", map[string]any{})
				if err := n.closeAbandonedCodex(context.Background(), key); err != nil {
					t.Fatal(err)
				}
				if len(n.codexAbandoned) != 0 {
					t.Fatal("retry did not clean both abandoned panes")
				}
			}
		})
	}
}

func TestCodexSamePromptStartsClaimDistinctThreads(t *testing.T) {
	n, f := stalledCodexNode(t)
	codexStartWait = time.Minute
	next := 0
	f.handle("tab.create", func(map[string]any) any {
		next++
		return map[string]any{"tab": map[string]any{"tab_id": fmt.Sprintf("w1:t%d", next)}, "root_pane": map[string]any{"pane_id": fmt.Sprintf("w1:p%d", next)}}
	})
	waiting := make(chan string, 2)
	seen := map[string]bool{}
	f.after(func(c herdrCall) {
		if c.Method == "pane.read" && c.Params["source"] == "visible" && c.Params["lines"] == float64(screen.DialogLines) {
			pane := str(c.Params["pane_id"])
			if !seen[pane] {
				seen[pane] = true
				waiting <- pane
			}
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	results := make(chan error, 2)
	ids := make(chan string, 2)
	var wg sync.WaitGroup
	defer func() { cancel(); wg.Wait() }()
	cwd := filepath.Join(filepath.Dir(n.dir), "proj")
	launch := n.defaultHost().(host.Launcher)
	const prompt = "the same first message"
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := n.startCodex(ctx, launch, cwd, "codex", []string{"codex"}, modelExecution{}, prompt, "w1", "", "")
			if err != nil {
				results <- err
				return
			}
			ids <- s.SessionID
		}()
		select {
		case <-waiting:
		case err := <-results:
			t.Fatalf("start returned before both thread waits: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("one start blocked another from waiting for its own thread")
		}
	}

	firstID := "aaaaaaaa-0000-4000-8000-000000000001"
	writeThread(t, n.home, firstID, cwd, prompt)
	expireRollouts(n.codexHome(), false) // as for the second: the cache would hide it for up to its 5 s, the wait's whole bound
	var first string
	select {
	case first = <-ids:
	case err := <-results:
		t.Fatalf("the first start failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("no start claimed the first rollout")
	}
	select {
	case id := <-ids:
		t.Fatalf("both starts adopted %q before a second rollout existed", id)
	case err := <-results:
		t.Fatalf("the other start ended instead of waiting for a distinct rollout: %v", err)
	default:
	}
	secondID := "bbbbbbbb-0000-4000-8000-000000000002"
	writeThread(t, n.home, secondID, cwd, prompt)
	expireRollouts(n.codexHome(), false) // make the just-published second rollout visible to the next poll
	var second string
	select {
	case second = <-ids:
	case err := <-results:
		t.Fatalf("the second start failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("the second start did not adopt the next rollout")
	}
	if first == second {
		t.Fatalf("both starts returned the same rollout %q", first)
	}
	n.mu.Lock()
	bound := map[string]bool{}
	for _, id := range n.panes {
		bound[id] = true
	}
	n.mu.Unlock()
	if !bound[first] || !bound[second] {
		t.Fatalf("starts did not bind their distinct rollouts: %q %q", first, second)
	}
}
