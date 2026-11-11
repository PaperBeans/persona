package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"

	"github.com/nwverse/persona/server/internal/prompts"
)

func TestGoldenStandingAuthorizationDispatch(t *testing.T) {
	golden(t, "standing_authorizations.task", prompts.RenderStandingAuthorizations(prompts.StandingAuthorizations{
		Text: "Fix the login crash and run the affected tests.",
		Grants: []prompts.StandingAuthorization{{
			ID: "auth_example", MachineID: "box", Directory: "/code/app", Actions: []string{"merge", "deploy-test"},
			Conditions: []string{"CI is green", "No merge conflicts"},
			Quote:      "Merge this repository once its checks pass; no further review confirmation is needed.",
			GrantedAt:  "2026-11-11T07:36:00Z", Source: "Main conversation", Reversible: true,
		}},
	}))
}

func dispatchAuthorization() StandingAuthorization {
	return StandingAuthorization{ID: "auth_dispatch", MachineID: "box", Directory: "/code/app",
		Actions: []string{"merge", "deploy-test"}, Conditions: []string{"CI is green", "No merge conflicts"},
		Quote: "Merge this repository after its checks pass without asking again.", Source: "Main conversation",
		GrantedAt: time.Date(2026, time.November, 11, 7, 36, 0, 0, time.UTC), Reversible: true}
}

func saveDispatchAuthorizations(t *testing.T, database *db.DB, grants ...StandingAuthorization) {
	t.Helper()
	if err := database.PutSetting(t.Context(), standingAuthorizationsKey,
		StandingAuthorizations{Revision: 1, Authorizations: grants}); err != nil {
		t.Fatal(err)
	}
}

func assertDispatchAuthorization(t *testing.T, text string, grant StandingAuthorization, want bool) {
	t.Helper()
	if strings.Contains(text, grant.ID) != want {
		t.Fatalf("authorization present = %v, want %v:\n%s", strings.Contains(text, grant.ID), want, text)
	}
	if want {
		for _, field := range []string{grant.Directory, grant.MachineID, grant.Quote, grant.Source, "merge, deploy-test", "CI is green; No merge conflicts", "Reversible: true", "never grant tool permissions"} {
			if !strings.Contains(text, field) {
				t.Errorf("dispatch omitted %q:\n%s", field, text)
			}
		}
	}
}

func TestStandingAuthorizationTaskPlanDispatch(t *testing.T) {
	for _, test := range []struct {
		name, directory, machine string
		want                     bool
	}{
		{"matching_repository", "/code/app", "box", true},
		{"matching_subdirectory", "/code/app/server", "box", true},
		{"other_repository", "/code/other", "box", false},
		{"same_prefix_other_repository", "/code/application", "box", false},
		{"other_machine", "/code/app", "another-box", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			database := taskRecoveryDatabase(t)
			grant := dispatchAuthorization()
			grant.MachineID = test.machine
			saveDispatchAuthorizations(t, database, grant)
			s, f := newQuietServiceOn(t, database)
			t.Cleanup(func() { settleService(t, s) })
			f.answerNode("git.status", func(_ string, args json.RawMessage) (any, error) {
				var params struct {
					Cwd string `json:"cwd"`
				}
				if err := json.Unmarshal(args, &params); err != nil {
					return nil, err
				}
				root := params.Cwd
				if root == "/code/app/server" {
					root = "/code/app"
				}
				return proto.RepositoryIdentity{Version: 1, IsRepo: true, Cwd: params.Cwd, Root: root, CommonDir: root + "/.git"}, nil
			})
			task := PlanTask{Title: "Fix the login crash", Prompt: "Fix the login crash and test the change.", Machine: "box",
				Cwd: test.directory, Permission: "bypassPermissions", UserRequested: true}
			if _, err := s.toolTaskPlan(t.Context(), "conv1", readonlyTaskArgs(t, task)); err != nil {
				t.Fatal(err)
			}
			waitFor(t, "task dispatch", func() bool { return len(f.started()) == 1 })
			text, _ := f.started()[0]["prompt"].(string)
			assertDispatchAuthorization(t, text, grant, test.want)
			if test.want {
				t.Logf("Generated task instructions:\n%s", text)
			}
		})
	}
}

func TestStandingAuthorizationTaskPlanReadsAtStart(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		name := "new_grant_after_plan"
		if revoke {
			name = "revoked_after_plan"
		}
		t.Run(name, func(t *testing.T) {
			database := taskRecoveryDatabase(t)
			grant := dispatchAuthorization()
			if revoke {
				saveDispatchAuthorizations(t, database, grant)
			}
			s, f := newQuietServiceOn(t, database)
			t.Cleanup(func() { settleService(t, s) })
			task := PlanTask{Title: "Fix the login crash", Prompt: "Fix the login crash and test the change.", Machine: "box", Cwd: "/code/app", Permission: "bypassPermissions"}
			if _, err := s.toolTaskPlan(t.Context(), "conv1", readonlyTaskArgs(t, task)); err != nil {
				t.Fatal(err)
			}
			plans, err := s.conversationPlans(t.Context(), "conv1")
			if err != nil || len(plans) != 1 || len(f.started()) != 0 {
				t.Fatalf("pending plans = %v, error = %v, starts = %v", plans, err, f.started())
			}
			if revoke {
				saveDispatchAuthorizations(t, database)
			} else {
				saveDispatchAuthorizations(t, database, grant)
			}
			if _, err := s.startPlan(t.Context(), plans[0].ID, nil, false); err != nil {
				t.Fatal(err)
			}
			waitFor(t, "task dispatch", func() bool { return len(f.started()) == 1 })
			text, _ := f.started()[0]["prompt"].(string)
			assertDispatchAuthorization(t, text, grant, !revoke)
		})
	}
}

func TestStandingAuthorizationAgentSendTaskDispatch(t *testing.T) {
	for _, directory := range []string{"/code/desk", "/code/another"} {
		t.Run(directory, func(t *testing.T) {
			database := taskRecoveryDatabase(t)
			grant := dispatchAuthorization()
			grant.Directory = directory
			saveDispatchAuthorizations(t, database, grant)
			s, f := newQuietServiceOn(t, database)
			t.Cleanup(func() { settleService(t, s) })
			secretary(f)
			if _, err := runJSON(t, s, "conv1", "agent_send", map[string]any{"machine": "box", "session_id": "sec00001", "text": "Fix the login crash.", "task": map[string]any{"title": "Fix the login crash"}}); err != nil {
				t.Fatal(err)
			}
			sent := sentToAgent(f)
			if len(sent) != 1 {
				t.Fatalf("sent = %q", sent)
			}
			assertDispatchAuthorization(t, sent[0], grant, directory == "/code/desk")
		})
	}
}

type dispatchAuthorizationReadFailure struct{ *db.DB }

func (st dispatchAuthorizationReadFailure) Setting(ctx context.Context, key string, out any) (bool, error) {
	if key == standingAuthorizationsKey {
		return false, errors.New("authorization registry unavailable")
	}
	return st.DB.Setting(ctx, key, out)
}

func TestStandingAuthorizationReadFailureStopsDispatch(t *testing.T) {
	database := taskRecoveryDatabase(t)
	s, f := newQuietServiceOn(t, dispatchAuthorizationReadFailure{database})
	t.Cleanup(func() { settleService(t, s) })
	task := PlanTask{Title: "Fix the login crash", Prompt: "Fix the login crash.", Machine: "box", Cwd: "/code/app", Permission: "bypassPermissions"}
	if _, _, err := s.startTaskAgent(t.Context(), &task, false); err == nil || !strings.Contains(err.Error(), "authorization registry unavailable") {
		t.Fatalf("task start error = %v", err)
	}
	secretary(f)
	if _, err := runJSON(t, s, "conv1", "agent_send", map[string]any{"machine": "box", "session_id": "sec00001", "text": "Fix the login crash.", "task": map[string]any{"title": "Fix the login crash"}}); err == nil || !strings.Contains(err.Error(), "authorization registry unavailable") {
		t.Fatalf("agent send error = %v", err)
	}
	if len(f.started()) != 0 || len(sentToAgent(f)) != 0 {
		t.Fatalf("work dispatched despite unavailable grants: starts=%v, sent=%v", f.started(), sentToAgent(f))
	}
}
