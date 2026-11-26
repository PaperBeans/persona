package assistant

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// readonlyCompanionService is readonlyTaskService with settings kept, so a tier can resolve to agy.
func readonlyCompanionService(t *testing.T) (*Service, *fakeHub) {
	t.Helper()
	f := newFakeHub()
	f.sessions["box"] = nil
	f.ownModels = true
	s := newServiceOn(t, f, &profileTaskStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}})
	saveTiers(t, s, map[string]ModelTier{
		"simple":   {Launcher: "agy"},
		"agy-auto": {Launcher: "agy", PermissionMode: "auto"},
	})
	return s, f
}

// A read-only agy task the computer's Antigravity companion runs starts there at once, pinned to it (host agy), with
// bypassPermissions and the first-message read-only constraint (matching Claude Code) under both sandbox and prompt
// modes, whether it names the agy launcher or a tier resolving to it. Without the companion, or with a terminal host
// named, it is refused when planned; a companion that went away fails the start instead of opening a pane.
func TestReadonlyAgyTaskStartsOnCompanion(t *testing.T) {
	for _, tc := range []struct {
		name      string
		task      func(*PlanTask)
		launchers []proto.Launcher
		prompt    bool // assistant.readonly_mode prompt
		away      bool // the node answers a start naming agy with agy_companion_offline
		refused   bool // readonly_unsupported when planned
		mode      string
	}{
		{name: "agy launcher sandbox", task: func(p *PlanTask) { p.Launcher = "agy" }, launchers: companionLaunchers(), mode: "bypassPermissions"},
		{name: "simple tier sandbox", task: func(p *PlanTask) { p.Tier = "simple" }, launchers: companionLaunchers(), mode: "bypassPermissions"},
		{name: "prompt mode", task: func(p *PlanTask) { p.Launcher = "agy" }, launchers: companionLaunchers(), prompt: true,
			mode: "bypassPermissions"},
		{name: "no companion", task: func(p *PlanTask) { p.Launcher = "agy" }, launchers: paneLaunchers(), refused: true},
		{name: "herdr named", task: func(p *PlanTask) { p.Launcher, p.Host = "agy", proto.EngineHerdr }, launchers: companionLaunchers(),
			refused: true},
		{name: "companion went away", task: func(p *PlanTask) { p.Launcher = "agy" }, launchers: companionLaunchers(), away: true,
			mode: "bypassPermissions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s, f := readonlyCompanionService(t)
				f.launcherList = tc.launchers
				if tc.prompt {
					setSettings(t, s, func(v *Settings) { v.ReadonlyMode = readonlyPrompt })
				}
				if tc.away {
					f.refuseHost = map[string]error{proto.HostAgy: companionAwayErr}
				}
				task := readonlyTestTask("")
				tc.task(&task)
				_, err := s.toolTaskPlan(context.Background(), "readonly-chat", readonlyTaskArgs(t, task))
				synctest.Wait()
				if tc.refused {
					var re *rpc.Error
					if !errors.As(err, &re) || re.Code != "readonly_unsupported" {
						t.Fatalf("read-only agy plan: %v, want readonly_unsupported", err)
					}
					if hosts := startHosts(f); len(hosts) != 0 {
						t.Fatalf("a refused read-only agy task started: %q", hosts)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				f.mu.Lock()
				starts := append([]map[string]any(nil), f.starts...)
				f.mu.Unlock()
				if len(starts) != 1 || starts[0]["launcher"] != "agy" || starts[0]["host"] != proto.HostAgy ||
					starts[0]["permission_mode"] != tc.mode {
					t.Fatalf("read-only agy start = %v, want one on the companion in %s", starts, tc.mode)
				}
				prompt, _ := starts[0]["prompt"].(string)
				if strings.Count(prompt, prompts.RenderReadonlyTaskConstraint("")) != 1 {
					t.Fatalf("read-only agy prompt missing single read-only constraint: %q", prompt)
				}
				// A companion that went away fails the task (no pane in its place); otherwise it runs.
				if todo := onlyTodo(s); tc.away != (todo.StartError != "" && todo.DoneAt != nil) {
					t.Fatalf("to-do = %+v (companion away: %v)", todo, tc.away)
				}
			})
		})
	}
}

// Aligning read-only companion agy tasks with Claude must keep Codex read-only tasks in plan (hard sandbox under
// sandbox mode, default under prompt mode), leave writable agy tasks unchanged, and let retryOn target companion agy.
func TestReadonlyAgyKeepsCodexSandboxAndWritableRegression(t *testing.T) {
	t.Run("codex readonly keeps sandbox", func(t *testing.T) {
		for _, tc := range []struct {
			readonlyMode string
			wantPerm     string
		}{
			{readonlySandbox, "plan"},
			{readonlyPrompt, "default"},
		} {
			t.Run(tc.readonlyMode, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					s, f := readonlyCompanionService(t)
					f.launcherList = append(companionLaunchers(), proto.Launcher{Name: "codex", Agent: "codex"})
					setSettings(t, s, func(v *Settings) { v.ReadonlyMode = tc.readonlyMode })
					task := readonlyTestTask("")
					task.Launcher = "codex"
					if _, err := s.toolTaskPlan(context.Background(), "readonly-chat", readonlyTaskArgs(t, task)); err != nil {
						t.Fatal(err)
					}
					synctest.Wait()
					f.mu.Lock()
					starts := append([]map[string]any(nil), f.starts...)
					f.mu.Unlock()
					if len(starts) != 1 || starts[0]["launcher"] != "codex" || starts[0]["permission_mode"] != tc.wantPerm {
						t.Fatalf("codex readonly (%s) start = %v, want permission_mode=%s", tc.readonlyMode, starts, tc.wantPerm)
					}
					prompt, _ := starts[0]["prompt"].(string)
					if strings.Count(prompt, prompts.RenderReadonlyTaskConstraint("")) != 1 {
						t.Fatalf("codex readonly prompt missing single read-only constraint: %q", prompt)
					}
				})
			})
		}
	})

	t.Run("writable agy keeps configured permission without readonly constraint", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := newFakeHub()
			f.sessions["box"] = nil
			f.ownModels = true
			f.launcherList = companionLaunchers()
			st := &profileTaskStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}}
			s := newServiceOn(t, f, st)
			saveTiers(t, s, map[string]ModelTier{"agy-auto": {Launcher: "agy", PermissionMode: "auto"}})
			task := readonlyTestTask("")
			task.Readonly, task.Tier = false, "agy-auto"
			ctx := context.Background()
			if _, err := s.toolTaskPlan(ctx, "writable-chat", readonlyTaskArgs(t, task)); err != nil {
				t.Fatal(err)
			}
			var planID string
			st.planMu.Lock()
			for id := range st.plans {
				planID = id
			}
			st.planMu.Unlock()
			if planID == "" {
				t.Fatal("writable agy plan was not saved")
			}
			if _, err := s.startPlan(ctx, planID, nil, false); err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
			f.mu.Lock()
			starts := append([]map[string]any(nil), f.starts...)
			f.mu.Unlock()
			if len(starts) != 1 || starts[0]["launcher"] != "agy" || starts[0]["host"] != proto.HostAgy ||
				starts[0]["permission_mode"] != "auto" {
				t.Fatalf("writable agy start = %v, want companion auto", starts)
			}
			prompt, _ := starts[0]["prompt"].(string)
			if strings.Contains(prompt, prompts.RenderReadonlyTaskConstraint("")) {
				t.Fatalf("writable agy task unexpectedly received read-only constraint: %q", prompt)
			}
		})
	})

	t.Run("retryOn readonly task onto companion agy", func(t *testing.T) {
		for _, tc := range []struct {
			name      string
			host      string
			launchers []proto.Launcher
			refused   bool
		}{
			{name: "companion connected", launchers: companionLaunchers()},
			{name: "no companion", launchers: paneLaunchers(), refused: true},
			{name: "explicit herdr", host: proto.EngineHerdr, launchers: companionLaunchers(), refused: true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				s, f := readonlyCompanionService(t)
				f.launcherList = tc.launchers
				m := Device{ID: "box", Name: "Box"}
				task := readonlyTestTask("plan")
				task.Launcher, task.Host = "claude", tc.host
				err := s.retryOn(context.Background(), &task, m, retryWith{launcher: "agy"})
				if tc.refused {
					var re *rpc.Error
					if !errors.As(err, &re) || re.Code != "readonly_unsupported" {
						t.Fatalf("retryOn = %v, want readonly_unsupported", err)
					}
					return
				}
				if err != nil {
					t.Fatalf("retryOn companion agy failed: %v", err)
				}
				if task.Launcher != "agy" || task.Permission != "bypassPermissions" {
					t.Fatalf("retried task = %+v, want launcher=agy permission=bypassPermissions", task)
				}
			})
		}
	})
}
