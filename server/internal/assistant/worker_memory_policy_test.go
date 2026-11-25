package assistant

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/proto"
)

func fakeWorkerOutcomeTimers(s *Service) *outcomeTimers {
	c := &outcomeTimers{}
	h := &s.workerOutcomes
	h.mu.Lock()
	h.after = func(_ time.Duration, f func()) { c.mu.Lock(); c.due = append(c.due, f); c.mu.Unlock() }
	h.mu.Unlock()
	return c
}

type workerMemoryFeedStore struct {
	*trackRegistryStore
	feed map[string][]string
}

func (st *workerMemoryFeedStore) FeedConversations(_ context.Context, refs, _ []string) (map[string][]string, error) {
	out := map[string][]string{}
	for _, ref := range refs {
		if convs := st.feed[ref]; len(convs) > 0 {
			out[ref] = append([]string(nil), convs...)
		}
	}
	return out, nil
}

// Worker memory (memory.jsonl) is shared project knowledge outside any sensitive track (Refs #12216, item 9):
// outcomes from a health-space or sensitive to-do (its origin, supervisor, any run link, feed history, worker
// directory, or a track that denies kb_write) must not call the outcome model or append to memory.jsonl.
// An ordinary main-space to-do still writes as before.
func TestWorkerMemoryRespectsHealthAndKBWritePolicy(t *testing.T) {
	healthDir := identity.HealthDir("/home/owner")
	kbDenyOverride := json.RawMessage(`{"usage":[{"scene":"kb_write","category":"*","rule":"deny"}]}`)

	newPolicyEnv := func(t *testing.T, feed map[string][]string) (*Service, *fakeHub, *kb.Engine) {
		t.Helper()
		f := newFakeHub().withMachine()
		st := &workerMemoryFeedStore{
			trackRegistryStore: &trackRegistryStore{
				memStore: &memStore{},
				rows: []db.Track{
					{Slug: "qa-finance", Kind: trackKindSpace, Template: trackTemplateSpace, DefaultCategories: []string{categoryFinance}},
					{Slug: "qa-no-kb", Kind: trackKindSpace, Template: trackTemplateSpace, ConfigOverride: kbDenyOverride},
				},
			},
			feed: feed,
		}
		s := newKBServiceOn(t, f, st)
		s.work.switches = &TriageSwitches{Enabled: true}
		f.outcome = `{"outcome":"` + workerMemoryConclusion + `","links":["` + workerMemoryPR + `"]}`
		e := kbEng(t, s)
		if _, err := e.Apply(kb.KindProject, "explicit", kb.Update{Dirs: []string{"box:/code/worker"}}); err != nil {
			t.Fatal(err)
		}
		f.setSessions("server",
			proto.Session{MachineID: "server", SessionID: "conv1", Title: "Persona", Assistant: true},
			proto.Session{MachineID: "server", SessionID: "h0", Title: "Health", Cwd: healthDir, Space: SpaceHealth, Assistant: true},
			proto.Session{MachineID: "server", SessionID: "fin0", Title: "Finance", Space: "qa-finance", Assistant: true},
			proto.Session{MachineID: "server", SessionID: "nokb0", Title: "NoKB", Space: "qa-no-kb", Assistant: true},
		)
		f.setSessions("box",
			proto.Session{MachineID: "box", SessionID: "s1", Title: "Worker", Cwd: "/code/worker", Status: proto.StatusDone, UpdatedAt: turnEnd, LastText: workerMemoryConclusion},
			proto.Session{MachineID: "box", SessionID: "h1", Title: "Sleep log", Cwd: healthDir, Space: SpaceHealth, Status: proto.StatusDone, UpdatedAt: turnEnd},
		)
		f.setItems(
			proto.Item{ID: "prompt", Seq: 1, Kind: proto.KindText, Role: "user", Text: "Finish the task.", TS: turnEnd.Add(-time.Minute)},
			proto.Item{ID: "report", Seq: 2, Kind: proto.KindToolUse, Role: "assistant", ToolUseID: "report-call",
				ToolName: "report_to_supervisor", ToolInput: workerMemoryReportInput(), TS: turnEnd.Add(-time.Second)},
		)
		return s, f, e
	}

	releasedAt := turnEnd.Add(-10 * time.Minute)
	cases := []struct {
		name       string
		todo       Todo
		cwd        string
		feed       map[string][]string
		wantRecord bool
		directOnly bool
	}{
		{
			name: "health origin",
			todo: Todo{ID: "todo_health_origin", Title: "Check lab trend", Origin: "h0", Project: "explicit",
				Supervision: &Supervision{Supervisor: "h0"},
				Agents:      []TodoAgent{{MachineID: "box", SessionID: "s1", Cwd: "/code/worker"}}},
			cwd: "/code/worker",
		},
		{
			name: "health run link released",
			todo: Todo{ID: "todo_health_run", Title: "Compare sleep", Origin: "conv1", Project: "explicit",
				Supervision: &Supervision{Supervisor: "conv1"},
				Agents: []TodoAgent{
					{MachineID: "box", SessionID: "h1", Cwd: healthDir, ReleasedAt: &releasedAt},
					{MachineID: "box", SessionID: "s1", Cwd: "/code/worker"},
				}},
			cwd: "/code/worker",
		},
		{
			name: "health worker cwd",
			todo: Todo{ID: "todo_health_cwd", Title: "Check report", Origin: "conv1", Project: "explicit",
				Supervision: &Supervision{Supervisor: "conv1"},
				Agents:      []TodoAgent{{MachineID: "box", SessionID: "s1", Cwd: healthDir + "/reports"}}},
			cwd: healthDir + "/reports",
		},
		{
			name: "direct health cwd without link cwd",
			todo: Todo{ID: "todo_direct_health_cwd", Title: "Check report", Origin: "conv1", Project: "explicit",
				Supervision: &Supervision{Supervisor: "conv1"},
				Agents:      []TodoAgent{{MachineID: "box", SessionID: "s1", Cwd: "/code/worker"}}},
			cwd:        healthDir + "/reports",
			directOnly: true,
		},
		{
			name: "health feed conversation",
			todo: Todo{ID: "todo_health_feed", Title: "Follow up", Origin: "conv1", Project: "explicit",
				Supervision: &Supervision{Supervisor: "conv1"},
				Agents:      []TodoAgent{{MachineID: "box", SessionID: "s1", Cwd: "/code/worker"}}},
			cwd:  "/code/worker",
			feed: map[string][]string{"todo_health_feed": {"h0"}},
		},
		{
			name: "sensitive finance track origin",
			todo: Todo{ID: "todo_finance", Title: "Budget check", Origin: "fin0", Project: "explicit",
				Supervision: &Supervision{Supervisor: "fin0"},
				Agents:      []TodoAgent{{MachineID: "box", SessionID: "s1", Cwd: "/code/worker"}}},
			cwd: "/code/worker",
		},
		{
			name: "kb_write-only track origin",
			todo: Todo{ID: "todo_nokb", Title: "Private notes", Origin: "nokb0", Project: "explicit",
				Supervision: &Supervision{Supervisor: "nokb0"},
				Agents:      []TodoAgent{{MachineID: "box", SessionID: "s1", Cwd: "/code/worker"}}},
			cwd: "/code/worker",
		},
		{
			name: "ordinary main-space todo",
			todo: Todo{ID: "todo_plain", Title: "Fix retry loop", Origin: "conv1", Project: "explicit",
				Supervision: &Supervision{Supervisor: "conv1"},
				Agents:      []TodoAgent{{MachineID: "box", SessionID: "s1", Cwd: "/code/worker"}}},
			cwd:        "/code/worker",
			wantRecord: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, f, e := newPolicyEnv(t, tc.feed)
			putTodo(s, tc.todo)

			// Test direct writer, live report_to_supervisor(done), and triage WorkDone paths.
			s.recordWorkerOutcome(context.Background(), tc.todo, "box", "s1", tc.cwd, 7, 2, "report-call", true,
				workerMemoryConclusion, []string{workerMemoryPR, workerMemoryIssue})
			if !tc.wantRecord && !tc.directOnly {
				s.goRecordWorkerReport(context.Background(), tc.todo, "box", "s1", workerMemoryConclusion,
					[]string{workerMemoryPR, workerMemoryIssue}, turnEnd)
				work := db.SessionWork{MachineID: "box", SessionID: "s1", TurnAt: turnEnd,
					Work: proto.WorkState{Phase: proto.WorkDone, Line: workerMemoryConclusion}}
				items := []proto.Item{
					{ID: "prompt", Seq: 1, Kind: proto.KindText, Role: "user", Text: "Finish the task."},
					{ID: "answer", Seq: 2, Kind: proto.KindText, Role: "assistant", Text: workerMemoryConclusion},
				}
				s.goRecordWorkerDoneForTodo(context.Background(), tc.todo, tc.cwd, work, items)
				s.tasks.outcomeRuns.Wait()
			}

			records := workerMemoryRecords(t, e, "explicit")
			f.mu.Lock()
			asked := len(f.outcomeAsked)
			f.mu.Unlock()
			if tc.wantRecord {
				if len(records) != 1 || records[0].TodoID != tc.todo.ID || records[0].Conclusion != workerMemoryConclusion {
					t.Fatalf("ordinary todo did not write worker memory: %+v", records)
				}
				if asked != 1 {
					t.Fatalf("ordinary todo called outcome model %d time(s), want 1", asked)
				}
				return
			}
			if len(records) != 0 {
				t.Fatalf("restricted todo wrote project memory.jsonl: %+v", records)
			}
			if asked != 0 {
				t.Fatalf("restricted todo called outcome model %d time(s), want zero", asked)
			}
		})
	}
}

// While a to-do's verdict cannot be told yet (its origin conversation was never noted and is not listed),
// worker memory holds the outcome without calling the model or writing memory.jsonl. Once the conversation
// is listed in the main space, retry writes it; once listed in the health space, retry drops it without
// calling the model or writing.
func TestWorkerMemoryWaitsForUntoldVerdict(t *testing.T) {
	for _, health := range []bool{false, true} {
		name := "main space"
		if health {
			name = "health space"
		}
		t.Run(name, func(t *testing.T) {
			f := newFakeHub().withMachine()
			s := newKBService(t, f)
			timers := fakeWorkerOutcomeTimers(s)
			f.outcome = `{"outcome":"` + workerMemoryConclusion + `","links":["` + workerMemoryPR + `"]}`
			e := kbEng(t, s)
			if _, err := e.Apply(kb.KindProject, "explicit", kb.Update{Dirs: []string{"box:/code/worker"}}); err != nil {
				t.Fatal(err)
			}
			todo := Todo{ID: "todo_untold", Title: "Pending verdict", Origin: "conv_unlisted", Project: "explicit",
				Supervision: &Supervision{Supervisor: "conv_unlisted"},
				Agents:      []TodoAgent{{MachineID: "box", SessionID: "s1", Cwd: "/code/worker"}}}
			putTodo(s, todo)
			f.setSessions("box", proto.Session{MachineID: "box", SessionID: "s1", Cwd: "/code/worker", Status: proto.StatusDone, LastText: workerMemoryConclusion})

			s.recordWorkerOutcome(context.Background(), todo, "box", "s1", "/code/worker", 7, 2, "report-call", true,
				workerMemoryConclusion, []string{workerMemoryPR})

			if got := workerMemoryRecords(t, e, "explicit"); len(got) != 0 {
				t.Fatalf("untold todo wrote project memory.jsonl: %+v", got)
			}
			f.mu.Lock()
			asked := len(f.outcomeAsked)
			f.mu.Unlock()
			if asked != 0 {
				t.Fatalf("untold todo called outcome model %d time(s), want zero", asked)
			}

			// Still untold on first retry: stays held, no write and no model call.
			if n := timers.fire(); n != 1 {
				t.Fatalf("judgements while still untold = %d, want 1", n)
			}
			s.tasks.outcomeRuns.Wait()
			if got := workerMemoryRecords(t, e, "explicit"); len(got) != 0 {
				t.Fatalf("still-untold todo wrote project memory.jsonl: %+v", got)
			}

			conv := proto.Session{MachineID: "server", SessionID: "conv_unlisted", Title: "Persona", Assistant: true}
			if health {
				conv.Cwd, conv.Space = identity.HealthDir("/home/owner"), SpaceHealth
			}
			f.setSessions("server", conv)

			if n := timers.fire(); n != 1 {
				t.Fatalf("judgements once told = %d, want 1", n)
			}
			s.tasks.outcomeRuns.Wait()
			records := workerMemoryRecords(t, e, "explicit")
			f.mu.Lock()
			asked = len(f.outcomeAsked)
			f.mu.Unlock()
			if health {
				if len(records) != 0 {
					t.Fatalf("health todo wrote project memory.jsonl after retry: %+v", records)
				}
				if asked != 0 {
					t.Fatalf("health todo called outcome model %d time(s) after retry, want zero", asked)
				}
			} else {
				if len(records) != 1 || records[0].TodoID != todo.ID || records[0].Conclusion != workerMemoryConclusion {
					t.Fatalf("main-space todo did not write project memory.jsonl after retry: %+v", records)
				}
				if asked != 1 {
					t.Fatalf("main-space todo called outcome model %d time(s) after retry, want 1", asked)
				}
			}
			if n := timers.fire(); n != 0 {
				t.Fatalf("timer still armed after verdict settled: %d", n)
			}
		})
	}
}

// Custom kb_write-deny-only tracks (NoKBWrite && !HideFromExtensions) must fail closed when a to-do's origin,
// supervisor, or feed conversation is unlisted and never noted, including when the to-do is deleted before retry
// or changes before the async retry writer runs. Ordinary main-space conversations still write once known.
func TestWorkerMemoryCustomKBWriteTrackFailsClosedOnUnlistedAndDelete(t *testing.T) {
	kbDenyOverride := json.RawMessage(`{"usage":[{"scene":"kb_write","category":"*","rule":"deny"}]}`)

	newEnv := func(t *testing.T, feed map[string][]string) (*Service, *fakeHub, *kb.Engine, *workerMemoryFeedStore, *outcomeTimers) {
		t.Helper()
		f := newFakeHub().withMachine()
		st := &workerMemoryFeedStore{
			trackRegistryStore: &trackRegistryStore{
				memStore: &memStore{},
				rows: []db.Track{
					{Slug: "qa-no-kb", Kind: trackKindSpace, Template: trackTemplateSpace, ConfigOverride: kbDenyOverride},
				},
			},
			feed: feed,
		}
		s := newKBServiceOn(t, f, st)
		s.work.switches = &TriageSwitches{Enabled: true}
		pol := s.trackPolicy(context.Background(), "qa-no-kb")
		if !pol.NoKBWrite || pol.HideFromExtensions {
			t.Fatalf("qa-no-kb policy = %+v, want NoKBWrite=true HideFromExtensions=false", pol)
		}
		timers := fakeWorkerOutcomeTimers(s)
		f.outcome = `{"outcome":"` + workerMemoryConclusion + `","links":["` + workerMemoryPR + `"]}`
		e := kbEng(t, s)
		if _, err := e.Apply(kb.KindProject, "explicit", kb.Update{Dirs: []string{"box:/code/worker"}}); err != nil {
			t.Fatal(err)
		}
		f.setSessions("server",
			proto.Session{MachineID: "server", SessionID: "conv1", Title: "Persona", Assistant: true},
			proto.Session{MachineID: "server", SessionID: "nokb_known", Title: "NoKB", Space: "qa-no-kb", Assistant: true},
		)
		f.setSessions("box",
			proto.Session{MachineID: "box", SessionID: "s1", Cwd: "/code/worker", Status: proto.StatusDone, LastText: workerMemoryConclusion},
		)
		return s, f, e, st, timers
	}

	assertNone := func(t *testing.T, e *kb.Engine, f *fakeHub, phase string) {
		t.Helper()
		if got := workerMemoryRecords(t, e, "explicit"); len(got) != 0 {
			t.Fatalf("%s: wrote project memory.jsonl: %+v", phase, got)
		}
		f.mu.Lock()
		asked := len(f.outcomeAsked)
		f.mu.Unlock()
		if asked != 0 {
			t.Fatalf("%s: called outcome model %d time(s), want zero", phase, asked)
		}
	}

	t.Run("unlisted origin in kb_write-deny-only track holds then drops", func(t *testing.T) {
		s, f, e, _, timers := newEnv(t, nil)
		todo := Todo{ID: "todo_nokb_unlisted", Title: "Private task", Origin: "nokb_unlisted", Project: "explicit",
			Supervision: &Supervision{Supervisor: "nokb_unlisted"},
			Agents:      []TodoAgent{{MachineID: "box", SessionID: "s1", Cwd: "/code/worker"}}}
		putTodo(s, todo)

		s.recordWorkerOutcome(context.Background(), todo, "box", "s1", "/code/worker", 7, 2, "report-call", true,
			workerMemoryConclusion, []string{workerMemoryPR})
		assertNone(t, e, f, "while unlisted")

		// Still unlisted on first retry: stays held, no model call and no write.
		if n := timers.fire(); n != 1 {
			t.Fatalf("judgements while still unlisted = %d, want 1", n)
		}
		s.tasks.outcomeRuns.Wait()
		assertNone(t, e, f, "after still-unlisted retry")

		// Once listed in the kb_write-deny-only track (HideFromExtensions=false): dropped, timer disarmed.
		f.setSessions("server",
			proto.Session{MachineID: "server", SessionID: "conv1", Title: "Persona", Assistant: true},
			proto.Session{MachineID: "server", SessionID: "nokb_unlisted", Title: "NoKB", Space: "qa-no-kb", Assistant: true},
		)
		if n := timers.fire(); n != 1 {
			t.Fatalf("judgements once listed in qa-no-kb = %d, want 1", n)
		}
		s.tasks.outcomeRuns.Wait()
		assertNone(t, e, f, "after listed in qa-no-kb")
		if n := timers.fire(); n != 0 {
			t.Fatalf("timer still armed after qa-no-kb drop: %d", n)
		}
	})

	t.Run("unlisted feed conversation in kb_write-deny-only track holds then drops", func(t *testing.T) {
		s, f, e, _, timers := newEnv(t, map[string][]string{"todo_nokb_feed": {"nokb_feed_unlisted"}})
		todo := Todo{ID: "todo_nokb_feed", Title: "Follow up", Origin: "conv1", Project: "explicit",
			Supervision: &Supervision{Supervisor: "conv1"},
			Agents:      []TodoAgent{{MachineID: "box", SessionID: "s1", Cwd: "/code/worker"}}}
		putTodo(s, todo)

		s.recordWorkerOutcome(context.Background(), todo, "box", "s1", "/code/worker", 7, 2, "report-call", true,
			workerMemoryConclusion, []string{workerMemoryPR})
		assertNone(t, e, f, "while feed unlisted")

		if n := timers.fire(); n != 1 {
			t.Fatalf("judgements while feed still unlisted = %d, want 1", n)
		}
		s.tasks.outcomeRuns.Wait()
		assertNone(t, e, f, "after still-unlisted feed retry")

		f.setSessions("server",
			proto.Session{MachineID: "server", SessionID: "conv1", Title: "Persona", Assistant: true},
			proto.Session{MachineID: "server", SessionID: "nokb_feed_unlisted", Title: "NoKB", Space: "qa-no-kb", Assistant: true},
		)
		if n := timers.fire(); n != 1 {
			t.Fatalf("judgements once feed listed in qa-no-kb = %d, want 1", n)
		}
		s.tasks.outcomeRuns.Wait()
		assertNone(t, e, f, "after feed listed in qa-no-kb")
		if n := timers.fire(); n != 0 {
			t.Fatalf("timer still armed after feed qa-no-kb drop: %d", n)
		}
	})

	for _, tc := range []struct {
		name      string
		listAfter string // "", "qa-no-kb", or "main"
	}{
		{name: "delete then retry while still unlisted"},
		{name: "delete then list in kb_write-deny track then retry", listAfter: "qa-no-kb"},
		{name: "delete then list in main space then retry", listAfter: "main"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f, e, _, timers := newEnv(t, nil)
			todo := Todo{ID: "todo_del_retry", Title: "Deleted pending", Origin: "nokb_unlisted", Project: "explicit",
				Supervision: &Supervision{Supervisor: "nokb_unlisted"},
				Agents:      []TodoAgent{{MachineID: "box", SessionID: "s1", Cwd: "/code/worker"}}}
			putTodo(s, todo)

			s.recordWorkerOutcome(context.Background(), todo, "box", "s1", "/code/worker", 7, 2, "report-call", true,
				workerMemoryConclusion, []string{workerMemoryPR})
			assertNone(t, e, f, "before delete")

			if err := s.deleteTodo(context.Background(), todo.ID); err != nil {
				t.Fatalf("deleteTodo: %v", err)
			}
			switch tc.listAfter {
			case "qa-no-kb":
				f.setSessions("server",
					proto.Session{MachineID: "server", SessionID: "conv1", Title: "Persona", Assistant: true},
					proto.Session{MachineID: "server", SessionID: "nokb_unlisted", Title: "NoKB", Space: "qa-no-kb", Assistant: true},
				)
			case "main":
				f.setSessions("server",
					proto.Session{MachineID: "server", SessionID: "conv1", Title: "Persona", Assistant: true},
					proto.Session{MachineID: "server", SessionID: "nokb_unlisted", Title: "Main", Assistant: true},
				)
			}

			if n := timers.fire(); n != 1 {
				t.Fatalf("judgements after delete = %d, want 1", n)
			}
			s.tasks.outcomeRuns.Wait()
			assertNone(t, e, f, "after delete-then-retry")
			if n := timers.fire(); n != 0 {
				t.Fatalf("timer still armed after deleted todo dropped: %d", n)
			}
		})
	}

	for _, tc := range []struct {
		name       string
		wantRehold bool
		mutate     func(t *testing.T, s *Service, f *fakeHub, st *workerMemoryFeedStore, todo Todo)
	}{
		{
			name: "retry writer re-checks when todo deleted before write",
			mutate: func(t *testing.T, s *Service, _ *fakeHub, _ *workerMemoryFeedStore, todo Todo) {
				if err := s.deleteTodo(context.Background(), todo.ID); err != nil {
					t.Fatalf("deleteTodo: %v", err)
				}
			},
		},
		{
			name:       "retry writer re-checks when conversation becomes unlisted before write",
			wantRehold: true,
			mutate: func(_ *testing.T, _ *Service, f *fakeHub, _ *workerMemoryFeedStore, _ Todo) {
				f.setSessions("server",
					proto.Session{MachineID: "server", SessionID: "conv1", Title: "Persona", Assistant: true},
				)
			},
		},
		{
			name: "retry writer re-checks when kb_write-deny feed added before write",
			mutate: func(_ *testing.T, _ *Service, _ *fakeHub, st *workerMemoryFeedStore, todo Todo) {
				st.feed = map[string][]string{todo.ID: {"nokb_known"}}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f, e, st, timers := newEnv(t, nil)
			todo := Todo{ID: "todo_race_recheck", Title: "Pending verdict", Origin: "conv_unlisted", Project: "explicit",
				Supervision: &Supervision{Supervisor: "conv_unlisted"},
				Agents:      []TodoAgent{{MachineID: "box", SessionID: "s1", Cwd: "/code/worker"}}}
			putTodo(s, todo)

			s.recordWorkerOutcome(context.Background(), todo, "box", "s1", "/code/worker", 7, 2, "report-call", true,
				workerMemoryConclusion, []string{workerMemoryPR})
			assertNone(t, e, f, "while unlisted")

			// Resolve to main space so retryWorkerOutcomes dispatches the writer, then mutate right before the
			// writer runs.
			f.setSessions("server",
				proto.Session{MachineID: "server", SessionID: "conv1", Title: "Persona", Assistant: true},
				proto.Session{MachineID: "server", SessionID: "conv_unlisted", Title: "Persona", Assistant: true},
				proto.Session{MachineID: "server", SessionID: "nokb_known", Title: "NoKB", Space: "qa-no-kb", Assistant: true},
			)
			s.runWorkerOutcome = func(run func()) {
				tc.mutate(t, s, f, st, todo)
				run()
			}
			if n := timers.fire(); n != 1 {
				t.Fatalf("judgements on dispatch = %d, want 1", n)
			}
			assertNone(t, e, f, "after writer re-check")
			rearmed := timers.fire()
			if tc.wantRehold && rearmed != 1 {
				t.Fatalf("timer rearmed after untold re-check = %d, want 1", rearmed)
			}
			if !tc.wantRehold && rearmed != 0 {
				t.Fatalf("timer still armed after hidden re-check = %d, want 0", rearmed)
			}
		})
	}

	t.Run("control: main-space conversation still writes when kb_write-deny track exists", func(t *testing.T) {
		s, f, e, _, timers := newEnv(t, nil)
		knownTodo := Todo{ID: "todo_ctrl_known", Title: "Known main", Origin: "conv1", Project: "explicit",
			Supervision: &Supervision{Supervisor: "conv1"},
			Agents:      []TodoAgent{{MachineID: "box", SessionID: "s1", Cwd: "/code/worker"}}}
		putTodo(s, knownTodo)
		s.recordWorkerOutcome(context.Background(), knownTodo, "box", "s1", "/code/worker", 7, 2, "known-call", true,
			workerMemoryConclusion, []string{workerMemoryPR})
		records := workerMemoryRecords(t, e, "explicit")
		if len(records) != 1 || records[0].TodoID != knownTodo.ID {
			t.Fatalf("known main-space todo did not write: %+v", records)
		}

		unlistedMain := Todo{ID: "todo_ctrl_unlisted", Title: "Delayed main", Origin: "conv_late_main", Project: "explicit",
			Supervision: &Supervision{Supervisor: "conv_late_main"},
			Agents:      []TodoAgent{{MachineID: "box", SessionID: "s1", Cwd: "/code/worker"}}}
		putTodo(s, unlistedMain)
		s.recordWorkerOutcome(context.Background(), unlistedMain, "box", "s1", "/code/worker", 8, 2, "late-call", true,
			workerMemoryConclusion, []string{workerMemoryPR})
		if got := workerMemoryRecords(t, e, "explicit"); len(got) != 1 {
			t.Fatalf("unlisted main-space todo wrote early: %+v", got)
		}
		f.setSessions("server",
			proto.Session{MachineID: "server", SessionID: "conv1", Title: "Persona", Assistant: true},
			proto.Session{MachineID: "server", SessionID: "conv_late_main", Title: "Persona", Assistant: true},
		)
		if n := timers.fire(); n != 1 {
			t.Fatalf("judgements once late main listed = %d, want 1", n)
		}
		s.tasks.outcomeRuns.Wait()
		records = workerMemoryRecords(t, e, "explicit")
		if len(records) != 2 {
			t.Fatalf("late main-space todo did not write after retry: %+v", records)
		}
		f.mu.Lock()
		asked := len(f.outcomeAsked)
		f.mu.Unlock()
		if asked != 2 {
			t.Fatalf("control model calls = %d, want 2", asked)
		}
		if n := timers.fire(); n != 0 {
			t.Fatalf("timer still armed after control settled: %d", n)
		}
	})
}
