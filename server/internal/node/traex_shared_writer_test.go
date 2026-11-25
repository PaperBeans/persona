package node

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/herdr"
	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
)

func withProcessArgvStub(t *testing.T, fn func(pid int) ([]string, error)) {
	t.Helper()
	prev := processArgv
	processArgv = fn
	t.Cleanup(func() { processArgv = prev })
}

func writeTraexOwnerWithName(t *testing.T, home, thread string, pid int, processName string, mtime time.Time) {
	t.Helper()
	dir := filepath.Join(home, ".trae", "cli", "thread-writer-locks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(map[string]any{"pid": pid, "process_name": processName})
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, thread+".owner.json")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if !mtime.IsZero() {
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
}

func traexSharedProjectFixture(t *testing.T, n *Node, cwd string, rows []struct {
	id, title, model string
	updatedAt        time.Time
}, turns []historyTurn) {
	t.Helper()
	var sql strings.Builder
	sql.WriteString(codexAppSchema)
	for _, r := range rows {
		sql.WriteString(codexAppRow(r.id, "", cwd, r.title, r.updatedAt, 0))
	}
	sql.WriteString(`ALTER TABLE threads ADD COLUMN history_mode TEXT DEFAULT 'paginated';`)
	sql.WriteString(`ALTER TABLE threads ADD COLUMN model TEXT DEFAULT '';`)
	sql.WriteString(`UPDATE threads SET source = 'cli';`)
	for _, r := range rows {
		if r.model != "" {
			fmt.Fprintf(&sql, "UPDATE threads SET model = '%s' WHERE id = '%s';", r.model, r.id)
		}
	}
	writeCodexApp(t, n.traexHome(), cwd, sql.String())
	writeCodexHistory(t, n.traexHome(), turns)
	invalidateCodexPaginated(n.traexHome())
}

func hcTraexTerminal(addr, app, state string, fgPid int, argv []string, cwd, space, tab, where string) map[string]any {
	t := hcTerminal(addr, app, state, fgPid, argv[0], cwd, space, tab, where)
	t["title"] = addr
	fg := t["foreground"].(map[string]any)
	fg["name"] = filepath.Base(argv[0])
	fg["argv"] = argv
	return t
}

func TestTraexResumedIDFromArgsAndAppServerDetection(t *testing.T) {
	const id = threadMain
	managed := func(name string) bool { return filepath.Base(name) == "my-trae" }

	resumeCases := []struct {
		name            string
		argv            []string
		allowAnyProgram bool
		want            string
	}{
		{"traex resume", []string{"traex", "resume", id}, false, id},
		{"traecli resume", []string{"/usr/local/bin/traecli", "resume", id}, false, id},
		{"trae-cli flags before resume", []string{"trae-cli", "--profile", "dev", "-C", "/work/app", "resume", id}, false, id},
		{"flags after resume", []string{"traecli", "resume", "--full-auto", "--model", "gpt-5", id}, false, id},
		{"double dash before id", []string{"traecli", "resume", "--", id}, false, id},
		{"resume flag value", []string{"traecli", "--resume", id}, false, id},
		{"resume flag equals", []string{"traecli", "--resume=" + id}, false, id},
		{"resume subcommand session-id", []string{"traecli", "resume", "--session-id", id}, false, id},
		{"resume subcommand thread-id equals", []string{"traecli", "resume", "--thread-id=" + id}, false, id},
		{"node script wrapper", []string{"node", "--no-warnings", "/opt/traecli/bin/traecli.js", "resume", id}, false, id},
		{"env wrapper", []string{"env", "TRAE_HOME=/tmp/x", "traecli", "resume", id}, false, id},
		{"managed launcher", []string{"/opt/bin/my-trae", "resume", id}, false, id},
		{"custom launcher allowed on host traex pane", []string{"tx", "resume", id}, true, id},
		{"custom launcher rejected on unknown pane", []string{"tx", "resume", id}, false, ""},
		{"claude rejected on unknown pane", []string{"claude", "--resume", id}, false, ""},
		{"codex rejected on unknown pane", []string{"codex", "resume", id}, false, ""},
		{"shell always rejected", []string{"-zsh", "resume", id}, true, ""},
		{"uuid in prompt ignored", []string{"traecli", "inspect " + id}, false, ""},
		{"exec subcommand ignored", []string{"traecli", "exec", "resume", id}, false, ""},
		{"resume last ignored", []string{"traecli", "resume", "--last"}, false, ""},
		{"app-server ignored", []string{"traecli", "app-server", "--listen", "unix:///tmp/trae.sock"}, false, ""},
		{"invalid id ignored", []string{"traecli", "resume", "not-a-valid-uuid"}, false, ""},
	}
	for _, tc := range resumeCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := traexResumedIDFromArgs(tc.argv, tc.allowAnyProgram, managed); got != tc.want {
				t.Fatalf("traexResumedIDFromArgs(%q, %v) = %q, want %q", tc.argv, tc.allowAnyProgram, got, tc.want)
			}
		})
	}

	serverCases := []struct {
		name string
		argv []string
		want bool
	}{
		{"traecli app-server", []string{"traecli", "app-server", "--listen", "unix:///tmp/trae.sock"}, true},
		{"traex with config flag", []string{"/opt/bin/traex", "-c", "model=x", "app-server"}, true},
		{"node app-server", []string{"node", "/opt/traecli/cli.js", "app-server", "--stdio"}, true},
		{"dedicated app-server binary", []string{"/opt/bin/trae-app-server", "--stdio"}, true},
		{"resume client", []string{"traecli", "resume", id}, false},
		{"prompt mentioning app-server", []string{"traecli", "fix app-server startup"}, false},
		{"positional after double dash", []string{"traecli", "--", "app-server"}, false},
	}
	for _, tc := range serverCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := traexAppServerArgs(tc.argv); got != tc.want {
				t.Fatalf("traexAppServerArgs(%q) = %v, want %v", tc.argv, got, tc.want)
			}
		})
	}
}

// Two concurrent TRAE CLI conversations in the same project share one app-server writer whose environment
// still carries Terminal 1's pane identity. Both Herdr terminals must retain their own distinct conversation
// identities, native Working/Done lifecycle states, and matched completion metadata, while a third daemon-only
// conversation never appears as an independent live session source.
func TestTraexSharedAppServerTwoConversationsInOneProject(t *testing.T) {
	const (
		threadA = threadMain
		threadB = threadNew
		threadC = threadOld
	)
	const cwd = "/work/shared-project"
	tsA := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	tsB := time.Date(2026, 11, 20, 10, 5, 0, 0, time.UTC)
	tsC := time.Date(2026, 11, 20, 10, 10, 0, 0, time.UTC)

	t.Run("herdr_core_shared_app_server", func(t *testing.T) {
		n, _, hc, home := hcNode(t)
		traexSharedProjectFixture(t, n, cwd, []struct {
			id, title, model string
			updatedAt        time.Time
		}{
			{threadA, "Conversation A", "trae-model-a", tsA},
			{threadB, "Conversation B", "trae-model-b", tsB},
			{threadC, "External daemon thread", "trae-model-c", tsC},
		}, []historyTurn{
			{thread: threadA, id: "turn-a", status: "inProgress", ordinal: 0,
				items: [][2]string{userItem("ua", "Start A")}},
			{thread: threadB, id: "turn-b", status: "completed", ordinal: 0,
				items: [][2]string{userItem("ub", "Run B"), answerItem("ab", "Final reply from B.")}},
			{thread: threadC, id: "turn-c", status: "completed", ordinal: 0,
				items: [][2]string{userItem("uc", "External C"), answerItem("ac", "External reply C.")}},
		})

		// Single shared app-server PID inherited t1's HC_TERMINAL_ID and writes locks for A (older), B, and C (newest).
		sharedPID := os.Getpid()
		withProcessArgvStub(t, func(pid int) ([]string, error) {
			if pid == sharedPID {
				return []string{"traecli", "app-server", "--listen", "unix:///tmp/trae.sock"}, nil
			}
			return nil, os.ErrNotExist
		})
		seedTraexPane(sharedPID, "herdr-core:t1")
		now := time.Now()
		writeTraexOwnerWithName(t, home, threadA, sharedPID, "TRAE CLI", now.Add(-2*time.Minute))
		writeTraexOwnerWithName(t, home, threadB, sharedPID, "TRAE CLI", now.Add(-time.Minute))
		writeTraexOwnerWithName(t, home, threadC, sharedPID, "TRAE CLI", now)

		t1 := hcTraexTerminal("t1", "traex", "working", 4101, []string{"traecli", "resume", threadA}, cwd, "s1", "tab1", "proj / tab1")
		// TRAE CLI can also emit Codex-compatible OSC 7501 status while foreground binary is traecli.
		t2 := hcTraexTerminal("t2", "codex", "done", 4102, []string{"traecli", "resume", threadB}, cwd, "s1", "tab2", "proj / tab2")
		hc.setTerminals(t1, t2)

		m := byID(listSessions(t, n))
		sA, okA := m[threadA]
		sB, okB := m[threadB]
		sC, okC := m[threadC]
		if !okA || !okB || !okC {
			t.Fatalf("expected all 3 threads listed (2 live + 1 offline history), got %v", ids(listSessions(t, n)))
		}
		if sA.PaneID != "herdr-core:t1" || sA.Agent != traexAgent || sA.Status != proto.StatusWorking {
			t.Fatalf("terminal 1 misbound or lost native Working status: %+v", sA)
		}
		if sA.UpdatedAt.IsZero() {
			t.Fatalf("terminal 1 lost nonzero timestamp: %+v", sA)
		}
		if sB.PaneID != "herdr-core:t2" || sB.Agent != traexAgent || sB.Status != proto.StatusDone {
			t.Fatalf("terminal 2 misbound or lost native Done status: %+v", sB)
		}
		if sB.LastText != "Final reply from B." || sB.UpdatedAt.IsZero() {
			t.Fatalf("terminal 2 lost final reply text or completion timestamp: last=%q updated=%v", sB.LastText, sB.UpdatedAt)
		}
		// Thread C has no Herdr terminal: must remain offline history, never a live daemon source or attached to t1/t2.
		if sC.PaneID != "" || sC.Status != proto.StatusOffline {
			t.Fatalf("external shared-server thread became a live session or bound to a terminal: %+v", sC)
		}
		if fileExists(n.codexAppHistoryFile(codexSource{home: n.traexHome(), thread: threadC})) {
			t.Fatal("cold offline shared-server thread C was eagerly projected")
		}
		// Native herdr-core status required no screen reads.
		for _, c := range hc.trace() {
			if strings.HasPrefix(c, "terminal.read ") {
				t.Fatalf("native herdr-core lifecycle unexpectedly scraped screen: %v", hc.trace())
			}
		}
	})

	for _, mode := range []string{"argv_app_server", "owner_process_name", "cross_pane_generic_daemon"} {
		t.Run("classic_herdr_"+mode, func(t *testing.T) {
			n, f, home := herdrNode(t)
			traexSharedProjectFixture(t, n, cwd, []struct {
				id, title, model string
				updatedAt        time.Time
			}{
				{threadA, "Conversation A", "trae-model-a", tsA},
				{threadB, "Conversation B", "trae-model-b", tsB},
			}, []historyTurn{
				{thread: threadA, id: "turn-a", status: "completed", ordinal: 0,
					items: [][2]string{userItem("ua", "Run A"), answerItem("aa", "Final reply from A.")}},
				{thread: threadB, id: "turn-b", status: "completed", ordinal: 0,
					items: [][2]string{userItem("ub", "Run B"), answerItem("ab", "Final reply from B.")}},
			})

			sharedPID := os.Getpid()
			const fgPid1, fgPid2 = 51001, 51002
			ownerProcName := "TRAE CLI"
			sharedArgv := []string{"traecli", "app-server", "--listen", "unix:///tmp/trae.sock"}
			switch mode {
			case "owner_process_name":
				ownerProcName = "traecli app-server"
				sharedArgv = []string{"traecli"}
			case "cross_pane_generic_daemon":
				ownerProcName = "TRAE CLI"
				sharedArgv = []string{"traex-daemon"}
			}
			withProcessArgvStub(t, func(pid int) ([]string, error) {
				switch pid {
				case sharedPID:
					return sharedArgv, nil
				case fgPid1:
					return []string{"traecli", "resume", threadA}, nil
				case fgPid2:
					return []string{"traecli", "resume", threadB}, nil
				default:
					return nil, os.ErrNotExist
				}
			})
			seedTraexPane(sharedPID, "w1:p1")
			now := time.Now()
			writeTraexOwnerWithName(t, home, threadA, sharedPID, ownerProcName, now.Add(-time.Minute))
			writeTraexOwnerWithName(t, home, threadB, sharedPID, ownerProcName, now)

			f.handle("pane.process_info", func(p map[string]any) any {
				pid := fgPid1
				if str(p["pane_id"]) == "w1:p2" {
					pid = fgPid2
				}
				return map[string]any{"process_info": map[string]any{"foreground_processes": []any{
					map[string]any{"pid": pid, "name": "traecli", "argv0": "traecli"},
				}}}
			})
			// Classic herdr natively reports Working on w1:p1 and Done on w1:p2 (even with non-traex spelling "traecli").
			f.reply("session.snapshot", snapshot(
				[]any{map[string]any{"workspace_id": "w1", "label": "proj", "number": 1}},
				[]any{
					map[string]any{"tab_id": "w1:t1", "workspace_id": "w1", "label": "A", "number": 1},
					map[string]any{"tab_id": "w1:t2", "workspace_id": "w1", "label": "B", "number": 2},
				},
				[]any{
					agentOf("traecli", "", proto.StatusWorking, "w1:p1", "w1:t1", "w1", cwd, "A"),
					agentOf("trae-cli", "", proto.StatusDone, "w1:p2", "w1:t2", "w1", cwd, "B"),
				},
				[]any{
					map[string]any{"pane_id": "w1:p1", "tab_id": "w1:t1", "workspace_id": "w1", "cwd": cwd, "terminal_title": "A"},
					map[string]any{"pane_id": "w1:p2", "tab_id": "w1:t2", "workspace_id": "w1", "cwd": cwd, "terminal_title": "B"},
				},
			))

			m := byID(listSessions(t, n))
			sA, sB := m[threadA], m[threadB]
			if sA.PaneID != "w1:p1" || sA.Status != proto.StatusWorking || sA.Agent != traexAgent {
				t.Fatalf("pane 1 mismatch: %+v", sA)
			}
			if sB.PaneID != "w1:p2" || sB.Status != proto.StatusDone || sB.Agent != traexAgent || sB.LastText != "Final reply from B." || sB.UpdatedAt.IsZero() {
				t.Fatalf("pane 2 mismatch: %+v", sB)
			}
			// Neither pane needed pane.read because native Herdr Working/Done was preserved.
			for _, c := range f.trace() {
				if strings.HasPrefix(c, "pane.read ") {
					t.Fatalf("native Herdr Working/Done fell back to pane.read: %v", f.trace())
				}
			}
		})
	}

	t.Run("mixed_host_session_and_foreground_resume", func(t *testing.T) {
		n, f, home := herdrNode(t)
		traexSharedProjectFixture(t, n, cwd, []struct {
			id, title, model string
			updatedAt        time.Time
		}{
			{threadA, "Conversation A", "trae-model-a", tsA},
			{threadB, "Conversation B", "trae-model-b", tsB},
		}, []historyTurn{
			{thread: threadA, id: "turn-a", status: "completed", ordinal: 0,
				items: [][2]string{userItem("ua", "Run A"), answerItem("aa", "Reply A.")}},
			{thread: threadB, id: "turn-b", status: "completed", ordinal: 0,
				items: [][2]string{userItem("ub", "Run B"), answerItem("ab", "Reply B.")}},
		})

		sharedPID := os.Getpid()
		const fgPid1, fgPid2 = 51101, 51102
		withProcessArgvStub(t, func(pid int) ([]string, error) {
			switch pid {
			case sharedPID:
				return []string{"traecli", "app-server", "--listen", "unix:///tmp/trae.sock"}, nil
			case fgPid1:
				return []string{"traecli"}, nil // started fresh, no resume arg
			case fgPid2:
				return []string{"traecli", "resume", threadB}, nil
			default:
				return nil, os.ErrNotExist
			}
		})
		seedTraexPane(sharedPID, "w1:p1")
		now := time.Now()
		writeTraexOwnerWithName(t, home, threadA, sharedPID, "TRAE CLI", now.Add(-time.Minute))
		writeTraexOwnerWithName(t, home, threadB, sharedPID, "TRAE CLI", now)

		f.handle("pane.process_info", func(p map[string]any) any {
			pid := fgPid1
			if str(p["pane_id"]) == "w1:p2" {
				pid = fgPid2
			}
			return map[string]any{"process_info": map[string]any{"foreground_processes": []any{
				map[string]any{"pid": pid, "name": "traecli", "argv0": "traecli"},
			}}}
		})
		// Pane 1 keeps its native host-reported threadA (fresh start); pane 2 resolves threadB from foreground resume.
		f.reply("session.snapshot", snapshot(
			nil, nil,
			[]any{
				agentOf(traexAgent, threadA, proto.StatusDone, "w1:p1", "w1:t1", "w1", cwd, "A"),
				agentOf(traexAgent, "", proto.StatusWorking, "w1:p2", "w1:t2", "w1", cwd, "B"),
			},
			[]any{
				map[string]any{"pane_id": "w1:p1", "tab_id": "w1:t1", "workspace_id": "w1", "cwd": cwd, "terminal_title": "A"},
				map[string]any{"pane_id": "w1:p2", "tab_id": "w1:t2", "workspace_id": "w1", "cwd": cwd, "terminal_title": "B"},
			},
		))

		m := byID(listSessions(t, n))
		if m[threadA].PaneID != "w1:p1" || m[threadA].Status != proto.StatusDone || m[threadA].LastText != "Reply A." {
			t.Fatalf("pane 1 host session lost or cross-bound: %+v", m[threadA])
		}
		if m[threadB].PaneID != "w1:p2" || m[threadB].Status != proto.StatusWorking {
			t.Fatalf("pane 2 resumed session lost or misbound: %+v", m[threadB])
		}
	})
}

// A terminal's foreground explicit `resume <id>` must override both shared app-server writer metadata
// (even when another thread's lock is newer) and a stale host AgentSession, while rejecting known guardians.
func TestTraexForegroundResumedPreferredOverSharedWriterAndStaleHost(t *testing.T) {
	n, _, hc, home := hcNode(t)
	const cwd = "/work/app"
	traexHistoryFixture(t, n, nil)
	writeCodexApp(t, n.traexHome(), cwd, `UPDATE threads SET archived = 0, source = 'cli', name = 'Other chat' WHERE id = '`+threadOld+`';`)
	invalidateCodexPaginated(n.traexHome())

	sharedPID := os.Getpid()
	withProcessArgvStub(t, func(pid int) ([]string, error) {
		if pid == sharedPID {
			return []string{"traecli", "app-server", "--listen", "unix:///tmp/trae.sock"}, nil
		}
		return nil, os.ErrNotExist
	})
	seedTraexPane(sharedPID, "herdr-core:t1")
	now := time.Now()
	writeTraexOwnerWithName(t, home, threadMain, sharedPID, "TRAE CLI", now.Add(-time.Minute))
	writeTraexOwnerWithName(t, home, threadOld, sharedPID, "TRAE CLI", now) // newer lock on shared server

	// Stale host session claims threadOld, while foreground client explicitly resumed threadMain.
	panes := []host.Agent{{
		PaneID:         "herdr-core:t1",
		Cwd:            cwd,
		Agent:          traexAgent,
		AgentStatus:    proto.StatusDone,
		ForegroundArgv: []string{"traecli", "resume", threadMain},
	}}
	agents := []host.Agent{{
		PaneID:       "herdr-core:t1",
		Cwd:          cwd,
		Agent:        traexAgent,
		AgentStatus:  proto.StatusDone,
		AgentSession: &host.AgentSession{Source: "herdr:trae", Agent: traexAgent, Kind: "id", Value: threadOld},
	}}
	if added := n.traexAgents(context.Background(), panes, agents); len(added) != 0 {
		t.Fatalf("unexpected extra agent row: %+v", added)
	}
	if agents[0].SessionID() != threadMain || agents[0].AgentStatus != proto.StatusDone {
		t.Fatalf("foreground resumed id did not override stale host/shared writer: %+v", agents[0])
	}

	// Known guardian/subagent ID in foreground resume must never surface as a terminal conversation.
	hc.setTerminals(hcTraexTerminal("t2", "traex", "done", 4202, []string{"traecli", "resume", threadGuardian}, cwd, "s1", "tab2", "proj / tab2"))
	m := byID(listSessions(t, n))
	if s, ok := m[threadGuardian]; ok {
		t.Fatalf("guardian subagent surfaced as terminal session: %+v", s)
	}
}

// A terminal whose environment was inherited by a shared app-server (when that terminal originally launched
// the daemon) must NEVER be assigned an unrelated shared-server conversation — whether the terminal exited
// back to a shell or is running a fresh TRAE client with no bound conversation yet.
func TestTraexSharedAppServerNeverAssignsUnrelatedConversationToTerminal(t *testing.T) {
	const cwd = "/work/app"
	for _, tc := range []struct {
		name string
		term func() map[string]any
	}{
		{
			name: "launcher_terminal_exited_to_shell",
			term: func() map[string]any {
				return hcTerminal("t1", "traex", "done", 4301, "-zsh", cwd, "s1", "tab1", "proj / tab1")
			},
		},
		{
			name: "fresh_trae_client_without_conversation",
			term: func() map[string]any {
				return hcTraexTerminal("t1", "traex", "idle", 4302, []string{"traecli"}, cwd, "s1", "tab1", "proj / tab1")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, _, hc, home := hcNode(t)
			traexSharedProjectFixture(t, n, cwd, []struct {
				id, title, model string
				updatedAt        time.Time
			}{
				{threadNew, "Unrelated external chat", "trae-model", time.Date(2026, 11, 20, 11, 0, 0, 0, time.UTC)},
			}, []historyTurn{
				{thread: threadNew, id: "turn-ext", status: "completed", ordinal: 0,
					items: [][2]string{userItem("ue", "External"), answerItem("ae", "Unrelated answer.")}},
			})

			sharedPID := os.Getpid()
			withProcessArgvStub(t, func(pid int) ([]string, error) {
				if pid == sharedPID {
					return []string{"traecli", "app-server", "--listen", "unix:///tmp/trae.sock"}, nil
				}
				return nil, os.ErrNotExist
			})
			seedTraexPane(sharedPID, "herdr-core:t1")
			writeTraexOwnerWithName(t, home, threadNew, sharedPID, "TRAE CLI", time.Now())

			hc.setTerminals(tc.term())
			m := byID(listSessions(t, n))
			s, ok := m[threadNew]
			if !ok {
				t.Fatalf("expected offline history row for %s", threadNew)
			}
			if s.PaneID != "" || s.Status != proto.StatusOffline {
				t.Fatalf("unrelated shared-server conversation was attached to terminal: %+v", s)
			}
		})
	}
}

// Native Herdr lifecycle reports (Working/Done/Blocked/Idle) agree with the displayed conversation and are
// not clobbered by title/screen scraping, while synthetic custom:persona idle fallback retains screen detection.
func TestTraexPreservesNativeHerdrLifecycle(t *testing.T) {
	if got := fromHerdrAgent("", herdr.Agent{Agent: "traecli", PaneID: "w1:p1"}).Agent; got != traexAgent {
		t.Fatalf("fromHerdrAgent(traecli) = %q, want %q", got, traexAgent)
	}
	if got := fromHerdrAgent("", herdr.Agent{Agent: "trae", PaneID: "w1:p1"}).Agent; got != "trae" {
		t.Fatalf("fromHerdrAgent(trae) = %q, want trae (IDE must stay distinct)", got)
	}

	n, f, home := herdrNode(t)
	const cwd = "/work/app"
	traexSharedProjectFixture(t, n, cwd, []struct {
		id, title, model string
		updatedAt        time.Time
	}{
		{threadMain, "Native working", "trae-model", time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)},
		{threadNew, "Native done", "trae-model", time.Date(2026, 11, 20, 12, 1, 0, 0, time.UTC)},
	}, nil)

	// Seed a stale cached status for w1:p1 and w1:p2 opposite to native Herdr reports to prove native wins.
	traexCache.Lock()
	prevStatus := map[string]traexState{}
	for _, pane := range []string{"w1:p1", "w1:p2"} {
		if st, ok := traexCache.status[pane]; ok {
			prevStatus[pane] = st
		}
		traexCache.status[pane] = traexState{status: proto.StatusBlocked, at: time.Now()}
	}
	traexCache.Unlock()
	t.Cleanup(func() {
		traexCache.Lock()
		defer traexCache.Unlock()
		for _, pane := range []string{"w1:p1", "w1:p2"} {
			if st, ok := prevStatus[pane]; ok {
				traexCache.status[pane] = st
			} else {
				delete(traexCache.status, pane)
			}
		}
	})

	sharedPID := os.Getpid()
	const fg1, fg2 = 52001, 52002
	withProcessArgvStub(t, func(pid int) ([]string, error) {
		switch pid {
		case sharedPID:
			return []string{"traecli", "app-server"}, nil
		case fg1:
			return []string{"traecli", "resume", threadMain}, nil
		case fg2:
			return []string{"traecli", "resume", threadNew}, nil
		default:
			return nil, os.ErrNotExist
		}
	})
	seedTraexPane(sharedPID, "w1:p1")
	writeTraexOwnerWithName(t, home, threadMain, sharedPID, "TRAE CLI", time.Now())
	writeTraexOwnerWithName(t, home, threadNew, sharedPID, "TRAE CLI", time.Now())

	f.handle("pane.process_info", func(p map[string]any) any {
		pid := fg1
		if str(p["pane_id"]) == "w1:p2" {
			pid = fg2
		}
		return map[string]any{"process_info": map[string]any{"foreground_processes": []any{
			map[string]any{"pid": pid, "name": "traecli", "argv0": "traecli"},
		}}}
	})
	f.reply("session.snapshot", snapshot(
		nil, nil,
		[]any{
			// Native Working with NO spinner in title; native Done despite stale cached Blocked.
			agentOf(traexAgent, "", proto.StatusWorking, "w1:p1", "w1:t1", "w1", cwd, "plain title"),
			agentOf(traexAgent, "", proto.StatusDone, "w1:p2", "w1:t2", "w1", cwd, "⠸ stale spinner"),
		},
		[]any{
			map[string]any{"pane_id": "w1:p1", "tab_id": "w1:t1", "workspace_id": "w1", "cwd": cwd, "terminal_title": "plain title"},
			map[string]any{"pane_id": "w1:p2", "tab_id": "w1:t2", "workspace_id": "w1", "cwd": cwd, "terminal_title": "⠸ stale spinner"},
		},
	))

	m := byID(listSessions(t, n))
	if m[threadMain].Status != proto.StatusWorking {
		t.Fatalf("native Working was clobbered: %+v", m[threadMain])
	}
	if m[threadNew].Status != proto.StatusDone {
		t.Fatalf("native Done was clobbered: %+v", m[threadNew])
	}
	// Synthetic custom:persona idle report still falls back to title/screen status.
	if got := traexNativeStatus(host.Agent{
		PaneID:       "w1:p3",
		AgentStatus:  proto.StatusIdle,
		AgentSession: &host.AgentSession{Source: resumeSource, Agent: traexAgent, Kind: "id", Value: threadMain},
	}); got != "" {
		t.Fatalf("synthetic custom:persona idle should fall back, got %q", got)
	}
	// Native herdr-core idle is preserved.
	if got := traexNativeStatus(host.Agent{PaneID: "herdr-core:t3", AgentStatus: proto.StatusIdle}); got != proto.StatusIdle {
		t.Fatalf("native herdr-core idle = %q, want %q", got, proto.StatusIdle)
	}
}

// Completion metadata (persisted final reply text + nonzero completion timestamp) must stay attached to the
// matched conversation across Working -> Done transitions AND when the live terminal stops/exits to offline,
// without projecting unrelated cold offline history.
func TestTraexCompletionMetadataAndStoppedThreadHistoryRefresh(t *testing.T) {
	n, _, hc, home := hcNode(t)
	const (
		threadA    = threadMain
		threadB    = threadNew
		threadCold = threadOld
		cwd        = "/work/project"
	)
	tsStart := time.Date(2026, 11, 20, 14, 0, 0, 0, time.UTC)
	tsBDone := time.Date(2026, 11, 20, 14, 1, 0, 0, time.UTC)
	tsCold := time.Date(2026, 11, 20, 13, 0, 0, 0, time.UTC)

	traexSharedProjectFixture(t, n, cwd, []struct {
		id, title, model string
		updatedAt        time.Time
	}{
		{threadA, "Task A", "trae-model-a", tsStart},
		{threadB, "Task B", "trae-model-b", tsBDone},
		{threadCold, "Cold History", "trae-model-c", tsCold},
	}, []historyTurn{
		{thread: threadA, id: "turn-a1", status: "inProgress", ordinal: 0,
			items: [][2]string{userItem("ua1", "Run check A")}},
		{thread: threadB, id: "turn-b1", status: "completed", ordinal: 0,
			items: [][2]string{userItem("ub1", "Run check B"), answerItem("ab1", "Completed check B.")}},
		{thread: threadCold, id: "turn-c1", status: "completed", ordinal: 0,
			items: [][2]string{userItem("uc1", "Old prompt"), answerItem("ac1", "Cold answer.")}},
	})

	sharedPID := os.Getpid()
	withProcessArgvStub(t, func(pid int) ([]string, error) {
		if pid == sharedPID {
			return []string{"traecli", "app-server", "--listen", "unix:///tmp/trae.sock"}, nil
		}
		return nil, os.ErrNotExist
	})
	seedTraexPane(sharedPID, "herdr-core:t1")
	writeTraexOwnerWithName(t, home, threadA, sharedPID, "TRAE CLI", time.Now())
	writeTraexOwnerWithName(t, home, threadB, sharedPID, "TRAE CLI", time.Now())

	// Phase 1: A is Working, B is Done (with no prior n.file call).
	hc.setTerminals(
		hcTraexTerminal("t1", "traex", "working", 4401, []string{"traecli", "resume", threadA}, cwd, "s1", "tab1", "proj / tab1"),
		hcTraexTerminal("t2", "traex", "done", 4402, []string{"traecli", "resume", threadB}, cwd, "s1", "tab2", "proj / tab2"),
	)
	m := byID(listSessions(t, n))
	if m[threadA].Status != proto.StatusWorking || m[threadA].PaneID != "herdr-core:t1" || m[threadA].UpdatedAt.IsZero() {
		t.Fatalf("phase 1 A: %+v", m[threadA])
	}
	if m[threadB].Status != proto.StatusDone || m[threadB].PaneID != "herdr-core:t2" ||
		m[threadB].LastText != "Completed check B." || m[threadB].UpdatedAt.IsZero() {
		t.Fatalf("phase 1 B: %+v", m[threadB])
	}
	coldProjected := n.codexAppHistoryFile(codexSource{home: n.traexHome(), thread: threadCold})
	if fileExists(coldProjected) {
		t.Fatal("phase 1 eagerly projected cold offline history")
	}

	// Phase 2: A finishes Working -> Done. Persist final reply + completion timestamp (including updated_at_ms=0 fallback).
	tsADone := time.Date(2026, 11, 20, 14, 2, 30, 0, time.UTC)
	historyDB := filepath.Join(n.traexHome(), "thread_history_1.sqlite")
	stateDB := filepath.Join(n.traexHome(), "state_5.sqlite")
	ansA := answerItem("aa1", "Completed check A cleanly.")
	traexHistoryRevisionSQL(t, historyDB, fmt.Sprintf(`BEGIN;
		INSERT INTO thread_items VALUES ('%s', 'turn-a1', 'aa1', 1, %d, '%s', '%s');
		UPDATE thread_turns SET status = 'completed' WHERE thread_id = '%s' AND turn_id = 'turn-a1';
		COMMIT;`, threadA, tsADone.UnixMilli(), ansA[1], ansA[0], threadA))
	// Verify NULLIF(updated_at_ms, 0) falls back to updated_at * 1000 when updated_at_ms is 0.
	traexHistoryRevisionSQL(t, stateDB, fmt.Sprintf(
		`UPDATE threads SET updated_at = %d, updated_at_ms = 0 WHERE id = '%s';`, tsADone.Unix(), threadA))

	hc.setTerminals(
		hcTraexTerminal("t1", "traex", "done", 4401, []string{"traecli", "resume", threadA}, cwd, "s1", "tab1", "proj / tab1"),
		hcTraexTerminal("t2", "traex", "done", 4402, []string{"traecli", "resume", threadB}, cwd, "s1", "tab2", "proj / tab2"),
	)
	m = byID(listSessions(t, n))
	if m[threadA].Status != proto.StatusDone || m[threadA].PaneID != "herdr-core:t1" ||
		m[threadA].LastText != "Completed check A cleanly." || !m[threadA].UpdatedAt.Equal(tsADone) {
		t.Fatalf("phase 2 A completion metadata mismatch: status=%q pane=%q last=%q updated=%v want=%v",
			m[threadA].Status, m[threadA].PaneID, m[threadA].LastText, m[threadA].UpdatedAt, tsADone)
	}
	if m[threadB].LastText != "Completed check B." || m[threadB].PaneID != "herdr-core:t2" {
		t.Fatalf("phase 2 B metadata drifted: %+v", m[threadB])
	}

	// Phase 3: A starts another turn, writes final reply, and exits to shell (stopped-thread history refresh).
	tsAExit := time.Date(2026, 11, 20, 14, 5, 0, 0, time.UTC)
	u2 := userItem("ua2", "Final question")
	a2 := answerItem("aa2", "Final reply before terminal exit.")
	traexHistoryRevisionSQL(t, historyDB, fmt.Sprintf(`BEGIN;
		INSERT INTO thread_turns VALUES ('%s', 'turn-a2', 1, 'completed', NULL, 500);
		INSERT INTO thread_items VALUES ('%s', 'turn-a2', 'ua2', 0, %d, '%s', '%s');
		INSERT INTO thread_items VALUES ('%s', 'turn-a2', 'aa2', 1, %d, '%s', '%s');
		COMMIT;`, threadA, threadA, tsAExit.Add(-time.Second).UnixMilli(), u2[1], u2[0], threadA, tsAExit.UnixMilli(), a2[1], a2[0]))
	traexHistoryRevisionSQL(t, stateDB, fmt.Sprintf(
		`UPDATE threads SET updated_at = %d, updated_at_ms = %d WHERE id = '%s';`, tsAExit.Unix(), tsAExit.UnixMilli(), threadA))

	hc.setTerminals(
		hcTerminal("t1", "traex", "done", 4401, "-zsh", cwd, "s1", "tab1", "proj / tab1"),
		hcTraexTerminal("t2", "traex", "done", 4402, []string{"traecli", "resume", threadB}, cwd, "s1", "tab2", "proj / tab2"),
	)
	m = byID(listSessions(t, n))
	if m[threadA].Status != proto.StatusOffline || m[threadA].PaneID != "" ||
		m[threadA].LastText != "Final reply before terminal exit." || !m[threadA].UpdatedAt.Equal(tsAExit) {
		t.Fatalf("phase 3 stopped A history not refreshed: status=%q pane=%q last=%q updated=%v want=%v",
			m[threadA].Status, m[threadA].PaneID, m[threadA].LastText, m[threadA].UpdatedAt, tsAExit)
	}
	if fileExists(coldProjected) {
		t.Fatal("phase 3 eagerly projected unrelated cold offline history")
	}
}

// Verify traexLiveThread confirms an explicit foreground resume even when the shared app-server writer's
// environment belongs to a different terminal.
func TestTraexLiveThreadSharedServerForegroundResumeConfirmation(t *testing.T) {
	n, f, home := herdrNode(t)
	sharedPID := os.Getpid()
	const fgPID = 53001
	withProcessArgvStub(t, func(pid int) ([]string, error) {
		switch pid {
		case sharedPID:
			return []string{"traecli", "app-server", "--listen", "unix:///tmp/trae.sock"}, nil
		case fgPID:
			return []string{"traecli", "resume", threadNew}, nil
		default:
			return nil, os.ErrNotExist
		}
	})
	seedTraexPane(sharedPID, "w1:p1") // shared server belongs to p1, NOT p2
	writeTraexOwnerWithName(t, home, threadMain, sharedPID, "TRAE CLI", time.Now().Add(-time.Minute))
	writeTraexOwnerWithName(t, home, threadNew, sharedPID, "TRAE CLI", time.Now())

	f.reply("pane.process_info", map[string]any{"process_info": map[string]any{"foreground_processes": []any{
		map[string]any{"pid": fgPID, "name": "traecli", "argv0": "traecli"},
	}}})

	if got := n.traexLiveThread("w1:p2", threadNew); got != threadNew {
		t.Fatalf("traexLiveThread(w1:p2, %s) = %q, want %q", threadNew, got, threadNew)
	}
	// Shared server env on w1:p1 must NOT confirm an unrelated thread for w1:p1 when p1 is just running zsh.
	f.reply("pane.process_info", map[string]any{"process_info": map[string]any{"foreground_processes": []any{
		map[string]any{"pid": 53002, "name": "zsh", "argv0": "-zsh"},
	}}})
	if got := n.traexLiveThread("w1:p1", ""); got != "" {
		t.Fatalf("traexLiveThread(w1:p1, '') unexpectedly adopted shared-server thread %q", got)
	}
}
