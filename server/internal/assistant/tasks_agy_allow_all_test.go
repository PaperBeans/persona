package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// agy workers run in Allow all, as Claude Code's do: in a terminal pane nobody answers agy's own approval prompt, and
// the node starts it with --dangerously-skip-permissions (node/workagent.go); on the companion it passes explicit
// bypassPermissions so runtime.js never falls back to settings.json ("default"). A mode the plan names stays.
func TestPlannedAgyTaskAllowsAll(t *testing.T) {
	for _, tc := range []struct{ name, permission, want string }{
		{"unset", "", "bypassPermissions"},
		{"named acceptEdits", "acceptEdits", "acceptEdits"},
		{"named default", "default", "default"},
		{"named plan", "plan", "plan"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s, f := readonlyTaskService(t)
				f.launcherList = []proto.Launcher{{Name: "claude", Agent: "claude"}, {Name: "agy", Agent: "agy", Source: "agy"}}
				setSettings(t, s, func(v *Settings) { v.DefaultPermissionMode = "auto" })
				task := PlanTask{Title: "Fix login", Prompt: "Fix the login flow and add tests.", Machine: "box", Cwd: "/code/app", Launcher: "agy", Permission: tc.permission}
				if _, err := s.toolTaskPlan(context.Background(), "agy-chat", readonlyTaskArgs(t, task)); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				p := readonlyFeedPlan(t, s, "agy-chat")
				if p.Tasks[0].Permission != tc.want {
					t.Fatalf("card = %+v, want permission %s", p.Tasks[0], tc.want)
				}
				if _, err := s.startPlan(context.Background(), p.ID, nil, false); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				f.mu.Lock()
				defer f.mu.Unlock()
				if len(f.starts) != 1 || f.starts[0]["permission_mode"] != tc.want {
					t.Fatalf("start = %v, want permission_mode %s", f.starts, tc.want)
				}
			})
		})
	}
}

// Built-in agy must stay recognized even when live launcher discovery fails (slow/busy machine) or when the cached
// machine snapshot only lists shell launchers, so writable and read-only starts keep bypassPermissions instead of
// falling back to "default" or omitting permission_mode.
func TestAgyAllowsAllWhenLauncherDiscoveryUnavailable(t *testing.T) {
	t.Run("task_plan writable agy with empty live list and claude-only snapshot", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s, f := readonlyTaskService(t)
			f.launcherList = nil // live launchersHere fails
			s.models.zshrcMu.Lock()
			s.models.zshrc = map[string][]proto.Launcher{"box": {{Name: "claude", Agent: "claude", Source: "path"}}}
			s.models.zshrcMu.Unlock()
			task := PlanTask{Title: "Fix auth", Prompt: "Fix auth handling.", Machine: "box", Cwd: "/code/app", Launcher: "agy"}
			if _, err := s.toolTaskPlan(context.Background(), "agy-slow-plan", readonlyTaskArgs(t, task)); err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
			p := readonlyFeedPlan(t, s, "agy-slow-plan")
			if p.Tasks[0].Permission != "bypassPermissions" {
				t.Fatalf("card permission = %q, want bypassPermissions", p.Tasks[0].Permission)
			}
			if _, err := s.startPlan(context.Background(), p.ID, nil, false); err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.starts) != 1 || f.starts[0]["permission_mode"] != "bypassPermissions" {
				t.Fatalf("start = %v, want permission_mode=bypassPermissions", f.starts)
			}
		})
	})

	t.Run("agent_start writable agy with empty live list", func(t *testing.T) {
		s, f := newQuietService(t)
		f.launcherList = nil
		args := json.RawMessage(`{"machine":"box","cwd":"/code/app","prompt":"Check git status","launcher":"agy"}`)
		if _, err := s.runTool(context.Background(), "server", "conv1", "agent_start", args); err != nil {
			t.Fatal(err)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.starts) != 1 || f.starts[0]["permission_mode"] != "bypassPermissions" {
			t.Fatalf("agent_start = %v, want permission_mode=bypassPermissions", f.starts)
		}
	})

	t.Run("startTaskSuccessor on stopped agy without reported permission mode", func(t *testing.T) {
		s, f := newQuietService(t)
		f.launcherList = companionLaunchers()
		todo, err := s.newTodo(context.Background(), "Continue task", "Notes", "conv1", "", false)
		if err != nil {
			t.Fatal(err)
		}
		last := proto.Session{MachineID: "box", SessionID: "agy-old", Cwd: "/code/app", Launcher: "agy", Agent: "agy"}
		if _, err := s.startTaskSuccessor(context.Background(), todo, last, "Resume work"); err != nil {
			t.Fatal(err)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.starts) != 1 || f.starts[0]["permission_mode"] != "bypassPermissions" {
			t.Fatalf("successor start = %v, want permission_mode=bypassPermissions", f.starts)
		}
	})

	t.Run("deferred agy leader activation forwards bypassPermissions on session.send", func(t *testing.T) {
		s, f := newQuietService(t)
		prepareDeferredAgy(f)
		f.answerNode(DeviceSessionSend, func(_ string, raw json.RawMessage) (any, error) {
			var params map[string]any
			_ = json.Unmarshal(raw, &params)
			f.agentSent = append(f.agentSent, params)
			return map[string]any{}, nil
		})
		_ = startAgyLeader(t, s, f)
		waitFor(t, "leader activation send", func() bool { return len(deliveredTo(f, "s1")) == 1 })
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.starts) != 1 || f.starts[0]["permission_mode"] != "bypassPermissions" {
			t.Fatalf("leader start = %v, want bypassPermissions", f.starts)
		}
		if len(f.agentSent) != 1 || f.agentSent[0]["permission_mode"] != "bypassPermissions" {
			t.Fatalf("deferred activation send = %v, want permission_mode=bypassPermissions", f.agentSent)
		}
	})

	t.Run("writable default vs readonly agy companion keeps readonly constraint only on readonly", func(t *testing.T) {
		for _, readonly := range []bool{false, true} {
			name := "writable"
			if readonly {
				name = "readonly"
			}
			t.Run(name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					s, f := readonlyCompanionService(t)
					f.launcherList = companionLaunchers()
					task := PlanTask{
						Title: "Inspect or edit", Prompt: "Check status and proceed.",
						Machine: "box", Cwd: "/code/app", Tier: "simple", Readonly: readonly,
					}
					if _, err := s.toolTaskPlan(context.Background(), "agy-mode-chat", readonlyTaskArgs(t, task)); err != nil {
						t.Fatal(err)
					}
					synctest.Wait()
					if !readonly {
						st := s.store().(*profileTaskStore)
						var pid string
						st.planMu.Lock()
						for id := range st.plans {
							pid = id
						}
						st.planMu.Unlock()
						if _, err := s.startPlan(context.Background(), pid, nil, false); err != nil {
							t.Fatal(err)
						}
						synctest.Wait()
					}
					f.mu.Lock()
					defer f.mu.Unlock()
					if len(f.starts) != 1 || f.starts[0]["launcher"] != "agy" || f.starts[0]["host"] != proto.HostAgy ||
						f.starts[0]["permission_mode"] != "bypassPermissions" {
						t.Fatalf("start (%s) = %v, want agy companion bypassPermissions", name, f.starts)
					}
					prompt, _ := f.starts[0]["prompt"].(string)
					hasConstraint := strings.Contains(prompt, prompts.RenderReadonlyTaskConstraint(""))
					if hasConstraint != readonly {
						t.Fatalf("readonly=%v constraint=%v prompt=%q", readonly, hasConstraint, prompt)
					}
				})
			})
		}
	})

	t.Run("pending placement upgrades settled default permission to bypassPermissions on agy when caller left permission unset", func(t *testing.T) {
		e := newPlacementTestEnv(t)
		e.f.launcherList = append(e.f.launcherList, proto.Launcher{Name: "agy", Agent: "agy", Source: "agy", BuiltinCompanion: true})
		ctx := context.Background()
		_ = e.doReq(http.MethodGet, "/api/v1/assistant/placement/pending", roleExtension, "sched-ext", "")
		res, err := e.s.toolTaskPlan(ctx, "conv_pub", json.RawMessage(`{
			"tasks":[{
				"title":"Auto-placed agy task",
				"prompt":"Run tests and fix failures.",
				"cwd":"~/repo",
				"launcher":"agy",
				"role":"worker",
				"goal":"Tests pass",
				"done_when":["All targeted tests pass"],
				"user_requested":true,
				"why":"Needs Linux worker"
			}]
		}`))
		if err != nil {
			t.Fatal(err)
		}
		planID := extractPlanID(t, res)
		p, err := e.s.loadPlan(ctx, planID)
		if err != nil {
			t.Fatal(err)
		}
		todoID := p.Tasks[0].TodoID
		// Simulate a prior settled fallback permission while CallerPermission remains empty.
		if err := e.s.patchPlanTask(ctx, planID, 0, func(pt *PlanTask) { pt.Permission = "default" }); err != nil {
			t.Fatal(err)
		}
		rec := e.doReq(http.MethodPost, "/api/v1/assistant/placement/"+todoID+"/assign", roleExtension, "sched-ext", `{"machine":"linux-idle","reason":"selected idle machine","version":1}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("assign = %d %s, want 200", rec.Code, rec.Body.String())
		}
		waitFor(t, "assigned agy task starts", func() bool {
			return len(taskStartMachines(e.f.fakeHub)) == 1 && e.s.taskStarting.Load() == 0
		})
		updated, err := e.s.loadPlan(ctx, planID)
		if err != nil {
			t.Fatal(err)
		}
		if updated.Tasks[0].Permission != "bypassPermissions" {
			t.Fatalf("placed permission = %q, want bypassPermissions", updated.Tasks[0].Permission)
		}
		e.f.fakeHub.mu.Lock()
		starts := append([]map[string]any(nil), e.f.fakeHub.starts...)
		e.f.fakeHub.mu.Unlock()
		if len(starts) != 1 || starts[0]["permission_mode"] != "bypassPermissions" {
			t.Fatalf("placed start = %v, want permission_mode=bypassPermissions", starts)
		}
	})
}
