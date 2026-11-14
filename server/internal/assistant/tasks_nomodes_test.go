package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/nwverse/persona/server/internal/proto"
)

// #7559: the node starts traex and omp with no permission mode and no effort, and refuses a start that names one
// (node/control.go). The hub must not send plan for a read-only task to them, nor fill in a writable task's default
// mode or a tier's effort; one the caller names is refused before anything starts.

const readonlyConstraint = "本任务只读：不改项目文件、不提交、不对外写入"

// noModesService is readonlyTaskService with a store (model profiles live there), and launcher agent besides claude.
func noModesService(t *testing.T, agent string) (*Service, *fakeHub) {
	t.Helper()
	f := newFakeHub()
	f.sessions["box"] = nil
	f.launcherList = []proto.Launcher{{Name: "claude", Agent: "claude"}, {Name: agent, Agent: agent}}
	f.ownModels = true
	return newServiceOn(t, f, &memStore{}), f
}

// noModesStart fails t when start carries what the node refuses for an agent that takes no modes.
func noModesStart(t *testing.T, start map[string]any) {
	t.Helper()
	for _, k := range []string{"permission_mode", "plan_then", "effort"} {
		if v, ok := start[k]; ok && v != "" {
			t.Fatalf("start sends %s %v to an agent that takes none: %v", k, v, start)
		}
	}
}

func TestReadonlyNonClaudeTaskCarriesTheConstraintNotPlan(t *testing.T) {
	for _, agent := range []string{"traex", "omp"} {
		t.Run(agent, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s, f := readonlyTaskService(t)
				f.launcherList = []proto.Launcher{{Name: "claude", Agent: "claude"}, {Name: agent, Agent: agent}}
				task := readonlyTestTask("")
				task.Launcher = agent
				if _, err := s.toolTaskPlan(context.Background(), "readonly-chat", readonlyTaskArgs(t, task)); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				p := readonlyFeedPlan(t, s, "readonly-chat")
				if p.State != planStarted || !p.Tasks[0].Readonly || p.Tasks[0].Error != "" {
					t.Fatalf("saved read-only plan = %+v", p.Tasks[0])
				}
				f.mu.Lock()
				defer f.mu.Unlock()
				if len(f.starts) != 1 || f.starts[0]["launcher"] != agent {
					t.Fatalf("starts = %v", f.starts)
				}
				noModesStart(t, f.starts[0])
				if prompt, _ := f.starts[0]["prompt"].(string); strings.Count(prompt, readonlyConstraint) != 1 {
					t.Fatalf("read-only prompt = %q", prompt)
				}
				if todo := onlyTodo(s); len(todo.Agents) != 1 {
					t.Fatalf("task did not finish dispatch: %+v", todo)
				}
			})
		})
	}
}

// A writable task to traex gets neither the owner's default permission mode (on its card, or at the start) nor its
// tier's effort; Codex and Claude keep theirs.
func TestWritableNonClaudeTaskGetsNoDefaultModeOrEffort(t *testing.T) {
	for _, tc := range []struct{ agent, wantMode string }{
		{"traex", ""},
		{"omp", ""},
		{"codex", "acceptEdits"},
		{"claude", "bypassPermissions"},
	} {
		t.Run(tc.agent, func(t *testing.T) {
			task := readonlyTestTask("")
			task.Readonly, task.Launcher = false, tc.agent
			task.DoneWhen, task.ExpectMinutes = []string{"the login flow is fixed"}, 30
			ctx := context.Background()

			// The plan: the card's permission is what will run.
			s, f := readonlyTaskService(t)
			f.launcherList = []proto.Launcher{{Name: "claude", Agent: "claude"}, {Name: tc.agent, Agent: tc.agent}}
			setSettings(t, s, func(v *Settings) { v.DefaultPermissionMode = "acceptEdits" })
			if _, err := s.toolTaskPlan(ctx, "writable-chat", readonlyTaskArgs(t, task)); err != nil {
				t.Fatal(err)
			}
			if saved := readonlyFeedPlan(t, s, "writable-chat").Tasks[0]; saved.Permission != tc.wantMode {
				t.Fatalf("the card's permission = %q, want %q: %+v", saved.Permission, tc.wantMode, saved)
			}

			// The start, of a task on a tier with an effort (model profiles need a store).
			s, f = noModesService(t, tc.agent)
			setSettings(t, s, func(v *Settings) { v.DefaultPermissionMode = "acceptEdits" })
			saveTier(t, s, "standard", ModelTier{Launcher: tc.agent, Effort: "high"})
			task.Launcher, task.Tier = "", "standard"
			if _, _, err := s.startTaskAgent(ctx, &task, false); err != nil {
				t.Fatal(err)
			}
			if len(f.starts) != 1 || f.starts[0]["launcher"] != tc.agent {
				t.Fatalf("starts = %v", f.starts)
			}
			start := f.starts[0]
			if tc.wantMode == "" {
				noModesStart(t, start)
				if task.Effort != "" {
					t.Fatalf("the task keeps the tier's effort %q", task.Effort)
				}
				return
			}
			if start["permission_mode"] != tc.wantMode || start["effort"] != "high" {
				t.Fatalf("%s lost its mode or effort: %v", tc.agent, start)
			}
		})
	}
}

// A mode or effort the call names for traex is refused by task_plan, saying why, before a plan is saved or anything
// starts. A read-only task's permission is replaced by read-only, so it is not one the call asked for.
func TestNonClaudeTaskRefusesAskedModeOrEffort(t *testing.T) {
	for _, tc := range []struct {
		name, permission, effort, want string
		readonly                       bool
	}{
		{"writable permission", "acceptEdits", "", "takes no permission mode", false},
		{"writable effort", "", "high", "takes no effort", false},
		{"read-only effort", "", "high", "takes no effort", true},
		{"read-only permission", "bypassPermissions", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s, f := readonlyTaskService(t)
				f.launcherList = []proto.Launcher{{Name: "claude", Agent: "claude"}, {Name: "traex", Agent: "traex"}}
				task := readonlyTestTask(tc.permission)
				task.Readonly, task.Launcher, task.Effort = tc.readonly, "traex", tc.effort
				task.DoneWhen, task.ExpectMinutes = []string{"the login flow is fixed"}, 30
				_, err := s.toolTaskPlan(context.Background(), "chat", readonlyTaskArgs(t, task))
				synctest.Wait()
				f.mu.Lock()
				defer f.mu.Unlock()
				if tc.want == "" {
					if err != nil || len(f.starts) != 1 {
						t.Fatalf("read-only task refused: %v, starts = %v", err, f.starts)
					}
					noModesStart(t, f.starts[0])
					return
				}
				if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "traex") {
					t.Fatalf("error = %v, want one saying %q", err, tc.want)
				}
				if len(f.starts) != 0 {
					t.Fatalf("a refused task started: %v", f.starts)
				}
			})
		})
	}
}

// agent_start to traex: an effort the call names is refused before anything starts (and before a health conversation's
// card asks the user); a tier's is dropped; no mode.
func TestAgentStartNonClaudeEffort(t *testing.T) {
	run := func(t *testing.T, input map[string]any) (*fakeHub, error) {
		t.Helper()
		s, f := newQuietServiceOn(t, &memStore{})
		f.launcherList = []proto.Launcher{{Name: "claude", Agent: "claude"}, {Name: "traex", Agent: "traex"}}
		saveTier(t, s, "standard", ModelTier{Launcher: "traex", Effort: "high"})
		input["machine"], input["cwd"], input["prompt"] = "box", "/code/app", "Fix the failure"
		args, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.runTool(context.Background(), "server", "conv1", "agent_start", args)
		return f, err
	}
	t.Run("asked", func(t *testing.T) {
		f, err := run(t, map[string]any{"launcher": "traex", "effort": "high"})
		if err == nil || !strings.Contains(err.Error(), "takes no effort") || !strings.Contains(err.Error(), "no agent was started") {
			t.Fatalf("error = %v", err)
		}
		if len(f.starts) != 0 {
			t.Fatalf("refused start went on: %v", f.starts)
		}
	})
	t.Run("tier", func(t *testing.T) {
		f, err := run(t, map[string]any{"tier": "standard"})
		if err != nil {
			t.Fatal(err)
		}
		if len(f.starts) != 1 || f.starts[0]["launcher"] != "traex" {
			t.Fatalf("starts = %v", f.starts)
		}
		noModesStart(t, f.starts[0])
	})
}
