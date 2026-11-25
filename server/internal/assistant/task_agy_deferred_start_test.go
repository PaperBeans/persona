package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistant/repositorystore"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// prepareDeferredAgy makes the fake Companion honor defer_mcp as the real two-phase start does. Returning Starting
// without that flag models an older Companion, which ignored the unknown field and completed the whole start itself.
func prepareDeferredAgy(f *fakeHub) {
	f.launcherList = companionLaunchers()
	f.startReply = func(params map[string]any, session *proto.Session) *proto.StartInstead {
		session.Agent, session.Launcher = "agy", "agy"
		session.Starting = params["defer_mcp"] == true
		return nil
	}
}

// planAgyLeader submits and starts one writable leader task on the Companion.
func planAgyLeader(t *testing.T, s *Service) {
	t.Helper()
	var input struct {
		Tasks []map[string]any `json:"tasks"`
	}
	if err := json.Unmarshal(leaderPlanArgs(roleLeader), &input); err != nil {
		t.Fatal(err)
	}
	input.Tasks[0]["launcher"] = "agy"
	raw, _ := json.Marshal(input)
	res, err := s.toolTaskPlan(context.Background(), "conv1", raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.startPlan(context.Background(), planIDRe.FindString(res), nil, false); err != nil {
		t.Fatalf("%s: %v", res, err)
	}
}

func startAgyLeader(t *testing.T, s *Service, f *fakeHub) Todo {
	t.Helper()
	planAgyLeader(t, s)
	waitFor(t, "the Companion leader's link", func() bool { return len(onlyTodo(s).Agents) == 1 })
	return onlyTodo(s)
}

func agyCallSessions(f *fakeHub, method string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, call := range f.calls {
		if call.Method != method {
			continue
		}
		var params struct {
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(call.Params, &params)
		out = append(out, params.SessionID)
	}
	return out
}

func TestAgyDeferredStartLinksLeaderAndWorkerBeforeTheirFirstPrompt(t *testing.T) {
	s, f := newQuietService(t)
	prepareDeferredAgy(f)
	linkedAtActivation := map[string]bool{}
	f.answerNode(DeviceSessionSend, func(_ string, raw json.RawMessage) (any, error) {
		var params map[string]any
		_ = json.Unmarshal(raw, &params)
		f.agentSent = append(f.agentSent, params)
		sid, _ := params["session_id"].(string)
		linked := s.isLeaderSession("box", sid)
		if task, ok := s.taskOf(context.Background(), "box", sid); ok && task.Supervision != nil {
			linked = linked || task.Supervision.Supervisor == "box/s1"
		}
		if _, seen := linkedAtActivation[sid]; !seen {
			linkedAtActivation[sid] = linked
		}
		return map[string]any{}, nil
	})

	leader := startAgyLeader(t, s, f)
	starts := f.started()
	if len(starts) != 1 || starts[0]["defer_mcp"] != true || starts[0]["host"] != proto.HostAgy {
		t.Fatalf("the leader did not use the deferred Companion start: %+v", starts)
	}
	clientID, _ := starts[0]["client_msg_id"].(string)
	if clientID == "" {
		t.Fatal("a deferred first prompt has no idempotency identity")
	}
	waitFor(t, "the leader's first prompt after linking", func() bool { return len(deliveredTo(f, "s1")) == 1 })
	f.mu.Lock()
	firstSend := append([]map[string]any(nil), f.agentSent...)
	linkedLeader := linkedAtActivation["s1"]
	f.mu.Unlock()
	if !linkedLeader {
		t.Fatal("the first prompt reached the Companion before the leader role was linked")
	}
	if len(firstSend) != 1 || firstSend[0]["text"] != starts[0]["prompt"] || firstSend[0]["client_msg_id"] != clientID {
		t.Fatalf("the deferred first prompt changed: start %+v, send %+v", starts[0], firstSend)
	}
	leaderTools := leaderToolNames(s.sessionMCPTools(context.Background(), "box", "s1", false, false))
	for _, name := range []string{"task_plan", "task_review", "report_to_supervisor"} {
		if !slices.Contains(leaderTools, name) {
			t.Fatalf("the leader's first-turn catalog lacks %s: %v", name, leaderTools)
		}
	}
	f.setSessions("box", proto.Session{SessionID: "s1", MachineID: "box", Cwd: "/code/app", Agent: "agy", Launcher: "agy", Status: proto.StatusIdle})
	if res := s.agentTool(context.Background(), "box", "s1", "report_to_supervisor", json.RawMessage(`{"kind":"progress","text":"Starting the work."}`)); res.IsError {
		t.Fatalf("the leader could not report with its real session identity: %+v", res)
	}

	// task_plan is a real leader tools/call, not only a catalog assertion. Its worker must go through the same barrier.
	worker := planWorker(t, s, f, map[string]any{"launcher": "agy"})
	workerSession := worker.Agents[0].SessionID
	waitFor(t, "the worker's first prompt after linking", func() bool { return len(deliveredTo(f, workerSession)) == 1 })
	f.mu.Lock()
	linkedWorker := linkedAtActivation[workerSession]
	f.mu.Unlock()
	if !linkedWorker {
		t.Fatal("the worker's first prompt preceded its supervision link")
	}
	starts = f.started()
	if len(starts) != 2 || starts[1]["defer_mcp"] != true {
		t.Fatalf("the worker did not use the deferred Companion start: %+v", starts)
	}
	workerTools := leaderToolNames(s.sessionMCPTools(context.Background(), "box", workerSession, false, false))
	if !slices.Contains(workerTools, "report_to_supervisor") || slices.Contains(workerTools, "task_plan") || slices.Contains(workerTools, "task_review") {
		t.Fatalf("worker tool isolation failed: %v", workerTools)
	}
	for _, name := range []string{"task_plan", "task_review"} {
		args := workerArgs(nil)
		if name == "task_review" {
			args, _ = json.Marshal(map[string]any{"todo_id": worker.ID, "verdict": "accepted"})
		}
		if res := s.mcpCall(context.Background(), "box", workerSession, name, args, false); !res.IsError {
			t.Fatalf("a worker called %s: %+v", name, res)
		}
	}
	if res := s.agentTool(context.Background(), "box", workerSession, "report_to_supervisor", json.RawMessage(`{"kind":"progress","text":"Working."}`)); res.IsError {
		t.Fatalf("the worker could not report with its real session identity: %+v", res)
	}
	review, _ := json.Marshal(map[string]any{"todo_id": worker.ID, "verdict": "needs_user", "reason": "Which branch should this use?", "recommend": "Keep the current branch."})
	if res := s.mcpCall(context.Background(), "box", "s1", "task_review", review, false); res.IsError {
		t.Fatalf("the leader could not review its worker: %+v", res)
	}
	if leader.Agents[0].SessionID != "s1" {
		t.Fatalf("unexpected leader identity: %+v", leader.Agents)
	}
}

func TestAgyDeferredStartAdoptedAfterLostAnswerStillActivates(t *testing.T) {
	s, f := newQuietService(t)
	prepareDeferredAgy(f)
	f.failStart = context.DeadlineExceeded
	f.onStart = func(f *fakeHub, _ map[string]any) {
		// The Companion created the deferred session, but its session.start answer was lost. The next list proves that
		// exact starting row to the ordinary start reconciler.
		f.sessions["box"] = []proto.Session{{SessionID: "s1", MachineID: "box", Cwd: "/code/app", Agent: "agy", Launcher: "agy",
			StartedByPersona: true, Starting: true, Status: proto.StatusIdle, UpdatedAt: time.Now().UTC()}}
	}
	leader := startAgyLeader(t, s, f)
	waitFor(t, "the adopted deferred session's post-link activation", func() bool { return len(deliveredTo(f, "s1")) == 1 })
	if got := f.started(); len(got) != 1 || got[0]["defer_mcp"] != true {
		t.Fatalf("reconciliation started a second Agent: %+v", got)
	}
	if got := todoNow(s, leader.ID); got.DoneAt != nil || len(got.Agents) != 1 {
		t.Fatalf("the adopted deferred session was not kept and linked: %+v", got)
	}
}

type deferredStartLinkFailStore struct{ *repositorystore.Store }

func (s *deferredStartLinkFailStore) LinkTodoAgentWithSource(context.Context, string, string, string, string, string, string, string, string, string, time.Time) ([]string, error) {
	return nil, errors.New("injected deferred-start link failure")
}

func TestAgyDeferredStartCleansUpDefiniteFailures(t *testing.T) {
	for _, failure := range []string{"link", "activation"} {
		t.Run(failure, func(t *testing.T) {
			var s *Service
			var f *fakeHub
			if failure == "link" {
				s, f = newQuietServiceOn(t, &deferredStartLinkFailStore{Store: repositorystore.New(todoTestDB(t))})
			} else {
				s, f = newQuietService(t)
				f.failSend = &rpc.Error{Code: "mcp_start_failed", Message: "the MCP server could not start"}
			}
			prepareDeferredAgy(f)
			planAgyLeader(t, s)
			waitFor(t, "the deferred start to fail", func() bool { return onlyTodo(s).DoneAt != nil })
			if got := agyCallSessions(f, deviceSessionStop); !slices.Equal(got, []string{"s1"}) {
				t.Fatalf("the unused ACP Agent was not stopped exactly once: %v", got)
			}
			if got := agyCallSessions(f, "session.close"); len(got) != 0 {
				t.Fatalf("the failed start closed the user's terminal: %v", got)
			}
			if got := agyCallSessions(f, DeviceSessionSend); len(got) != map[string]int{"link": 0, "activation": 1}[failure] {
				t.Fatalf("first prompt sends after %s failure: %v", failure, got)
			}
			if got := f.started(); len(got) != 1 || got[0]["defer_mcp"] != true {
				t.Fatalf("the failed start was retried into a second Agent: %+v", got)
			}
		})
	}
}

func TestAgyDeferredStartUnknownActivationKeepsTheLinkedSession(t *testing.T) {
	s, f := newQuietService(t)
	prepareDeferredAgy(f)
	f.failSend = &rpc.Error{Code: "peer_unconfirmed", Message: "the activation answer was lost", Data: json.RawMessage(`{"delivered":"unknown"}`)}
	leader := startAgyLeader(t, s, f)
	waitFor(t, "the uncertain activation to settle without a retry", func() bool { return s.taskStarting.Load() == 0 })
	if got := todoNow(s, leader.ID); got.DoneAt != nil || len(got.Agents) != 1 {
		t.Fatalf("an uncertain activation discarded or failed the linked Agent: %+v", got)
	}
	if got := agyCallSessions(f, DeviceSessionSend); !slices.Equal(got, []string{"s1"}) {
		t.Fatalf("an uncertain first prompt was sent again: %v", got)
	}
	if got := agyCallSessions(f, "session.close"); len(got) != 0 {
		t.Fatalf("an uncertain first prompt closed a session that may be running: %v", got)
	}
	if got := agyCallSessions(f, deviceSessionStop); len(got) != 0 {
		t.Fatalf("an uncertain first prompt stopped a session that may be running: %v", got)
	}
	if got := f.started(); len(got) != 1 {
		t.Fatalf("an uncertain activation opened another Agent: %+v", got)
	}
}

func TestAgyImmediateCompanionAnswerNeedsNoSecondFirstPrompt(t *testing.T) {
	s, f := newQuietService(t)
	prepareDeferredAgy(f)
	f.startReply = func(params map[string]any, session *proto.Session) *proto.StartInstead {
		session.Agent, session.Launcher, session.Starting = "agy", "agy", false
		return nil
	}
	leader := startAgyLeader(t, s, f)
	waitFor(t, "the old Companion start to settle", func() bool { return s.taskStarting.Load() == 0 })
	if got := f.started(); len(got) != 1 || got[0]["defer_mcp"] != true {
		t.Fatalf("the rolling-upgrade start did not offer defer_mcp: %+v", got)
	}
	if got := agyCallSessions(f, DeviceSessionSend); len(got) != 0 {
		t.Fatalf("an old Companion that already prompted got a duplicate: %v", got)
	}
	if got := todoNow(s, leader.ID); got.DoneAt != nil || len(got.Agents) != 1 {
		t.Fatalf("the old Companion start did not remain linked: %+v", got)
	}
	if prompt := f.started()[0]["prompt"]; !strings.Contains(prompt.(string), "you are the leader") {
		t.Fatalf("the old Companion's first prompt changed: %q", prompt)
	}
}

func planAgyLeaderWith(t *testing.T, s *Service, mutate func(map[string]any)) {
	t.Helper()
	var input struct {
		Tasks []map[string]any `json:"tasks"`
	}
	if err := json.Unmarshal(leaderPlanArgs(roleLeader), &input); err != nil {
		t.Fatal(err)
	}
	input.Tasks[0]["launcher"] = "agy"
	if mutate != nil {
		mutate(input.Tasks[0])
	}
	raw, _ := json.Marshal(input)
	res, err := s.toolTaskPlan(context.Background(), "conv1", raw)
	if err != nil {
		t.Fatal(err)
	}
	if id := planIDRe.FindString(res); id != "" && !strings.Contains(res, "without asking") {
		if _, err := s.startPlan(context.Background(), id, nil, false); err != nil {
			t.Fatalf("%s: %v", res, err)
		}
	}
}

func TestAgyDeferredStartReadonlyAndExplicitHostLeadersKeepRoleAndBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "readonly leader",
			mutate: func(task map[string]any) {
				task["readonly"] = true
				delete(task, "permission")
				delete(task, "done_when")
				task["goal"] = "Audit the repository structure without modifying files."
			},
		},
		{
			name: "explicit host agy leader",
			mutate: func(task map[string]any) {
				task["host"] = proto.HostAgy
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f := newQuietService(t)
			prepareDeferredAgy(f)
			linkedAtActivation := map[string]bool{}
			f.answerNode(DeviceSessionSend, func(_ string, raw json.RawMessage) (any, error) {
				var params map[string]any
				_ = json.Unmarshal(raw, &params)
				f.agentSent = append(f.agentSent, params)
				sid, _ := params["session_id"].(string)
				linked := s.isLeaderSession("box", sid)
				if task, ok := s.taskOf(context.Background(), "box", sid); ok && task.Supervision != nil {
					linked = linked || task.Supervision.Supervisor == "box/s1"
				}
				if _, seen := linkedAtActivation[sid]; !seen {
					linkedAtActivation[sid] = linked
				}
				return map[string]any{}, nil
			})

			planAgyLeaderWith(t, s, tc.mutate)
			waitFor(t, "the Companion leader's link", func() bool { return len(onlyTodo(s).Agents) == 1 })
			waitFor(t, "the Companion leader's first prompt", func() bool { return len(deliveredTo(f, "s1")) == 1 })

			starts := f.started()
			if len(starts) != 1 || starts[0]["host"] != proto.HostAgy || starts[0]["defer_mcp"] != true {
				t.Fatalf("leader did not use deferred Companion start: %+v", starts)
			}
			f.mu.Lock()
			linkedLeader := linkedAtActivation["s1"]
			f.mu.Unlock()
			if !linkedLeader {
				t.Fatal("leader first prompt preceded role link")
			}

			leaderTools := leaderToolNames(s.sessionMCPTools(context.Background(), "box", "s1", false, false))
			for _, name := range []string{"task_plan", "task_review", "report_to_supervisor", "task_spawn"} {
				if !slices.Contains(leaderTools, name) {
					t.Fatalf("leader catalog lacks %s: %v", name, leaderTools)
				}
			}
			if slices.Contains(leaderTools, "machines_list") {
				t.Fatalf("leader catalog unexpectedly exposed assistant-only machines_list: %v", leaderTools)
			}
			if res := s.mcpCall(context.Background(), "box", "s1", "machines_list", json.RawMessage(`{}`), false); !res.IsError {
				t.Fatalf("leader unexpectedly called assistant-only machines_list: %+v", res)
			}

			f.setSessions("box", proto.Session{SessionID: "s1", MachineID: "box", Cwd: "/code/app", Agent: "agy", Launcher: "agy", Status: proto.StatusIdle})
			worker := planWorker(t, s, f, map[string]any{
				"launcher": "agy",
				"readonly": true,
				"goal":     "Inspect the layout without editing files.",
			})
			workerSession := worker.Agents[0].SessionID
			waitFor(t, "the worker's first prompt", func() bool { return len(deliveredTo(f, workerSession)) == 1 })

			starts = f.started()
			if len(starts) != 2 || starts[1]["host"] != proto.HostAgy || starts[1]["defer_mcp"] != true {
				t.Fatalf("read-only agy worker did not use deferred Companion start: %+v", starts)
			}
			workerTools := leaderToolNames(s.sessionMCPTools(context.Background(), "box", workerSession, false, false))
			if !slices.Contains(workerTools, "report_to_supervisor") || !slices.Contains(workerTools, "task_spawn") {
				t.Fatalf("worker catalog missing supervision tools: %v", workerTools)
			}
			for _, forbidden := range []string{"task_plan", "task_review", "machines_list"} {
				if slices.Contains(workerTools, forbidden) {
					t.Fatalf("worker catalog exposed forbidden tool %s: %v", forbidden, workerTools)
				}
			}
			for _, forbidden := range []string{"task_plan", "task_review", "machines_list"} {
				args := json.RawMessage(`{}`)
				if forbidden == "task_plan" {
					args = workerArgs(nil)
				} else if forbidden == "task_review" {
					args, _ = json.Marshal(map[string]any{"todo_id": worker.ID, "verdict": "accepted"})
				}
				if res := s.mcpCall(context.Background(), "box", workerSession, forbidden, args, false); !res.IsError {
					t.Fatalf("worker unexpectedly called %s: %+v", forbidden, res)
				}
			}
		})
	}

	t.Run("explicit host agy fails closed when companion is offline", func(t *testing.T) {
		s, f := newQuietService(t)
		prepareDeferredAgy(f)
		f.refuseHost = map[string]error{proto.HostAgy: companionAwayErr}
		planAgyLeaderWith(t, s, func(task map[string]any) {
			task["host"] = proto.HostAgy
		})
		waitFor(t, "explicit agy leader failure", func() bool { return onlyTodo(s).DoneAt != nil })
		starts := f.started()
		if len(starts) != 1 || starts[0]["host"] != proto.HostAgy || starts[0]["defer_mcp"] != true {
			t.Fatalf("explicit host agy fell back to another host or omitted defer_mcp: %+v", starts)
		}
		if got := onlyTodo(s).StartError; got == "" {
			t.Fatal("expected StartError when explicit Companion host is offline")
		}
	})
}

func TestAgyDeferredStartPreservesTaskInstructionsAndAttachments(t *testing.T) {
	s, f := newQuietService(t)
	prepareDeferredAgy(f)
	f.mu.Lock()
	f.devices = []Device{{ID: "server", Online: true, Reachable: true}, {ID: "box", Online: true, Reachable: true, TaskInstructions: true}}
	f.mu.Unlock()

	startAgyLeader(t, s, f)
	waitFor(t, "the leader's first prompt with instructions", func() bool { return len(deliveredTo(f, "s1")) == 1 })

	starts := f.started()
	if len(starts) != 1 || starts[0]["defer_mcp"] != true {
		t.Fatalf("modern node leader did not use deferred start: %+v", starts)
	}
	rawPrompt, _ := starts[0]["prompt"].(string)
	instructions, _ := starts[0]["task_instructions"].(string)
	if instructions == "" || !strings.Contains(instructions, "you are the leader") {
		t.Fatalf("expected leader instructions in task_instructions, got %q", instructions)
	}
	if strings.Contains(rawPrompt, "you are the leader") {
		t.Fatalf("expected modern node to separate task_instructions from prompt: %q", rawPrompt)
	}

	sent := deliveredTo(f, "s1")[0]
	if !strings.Contains(sent, rawPrompt) || !strings.Contains(sent, instructions) {
		t.Fatalf("deferred activation dropped task_instructions: sent=%q instructions=%q", sent, instructions)
	}

	combined := deferredTaskPrompt("Task body.\n\n<attached_images>/tmp/1.png</attached_images>", map[string]any{
		"task_instructions": "Leader rule.",
		"task_attachments":  "Attachment note.",
	})
	want := "Task body.\n\nLeader rule.\n\nAttachment note.\n\n<attached_images>/tmp/1.png</attached_images>"
	if combined != want {
		t.Fatalf("deferredTaskPrompt = %q, want %q", combined, want)
	}
}
