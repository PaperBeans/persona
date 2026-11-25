package health

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
)

func TestStrengthWorkoutReportInputsAndGoldens(t *testing.T) {
	for _, scenario := range []string{"sets", "plan", "none"} {
		t.Run(scenario, func(t *testing.T) {
			s, d, ctx := strengthHarness(t)
			e := assistantwire.StrengthExercise{ID: healthIntakeUUID("golden-exercise"), Name: "Press", WeightRule: "stack"}
			if _, _, err := d.HealthStrengthExerciseSave(ctx, e); err != nil {
				t.Fatal(err)
			}
			start := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
			workout := assistantwire.StrengthWorkout{ID: healthIntakeUUID("golden-workout"), Start: start, End: start.Add(time.Hour), Report: json.RawMessage(`{"activity":"Strength training","duration":3600}`)}
			if scenario != "none" {
				weight := 40.0
				body := strengthDraftBody{Sets: []draftSet{{Exercise: e.ID, Weight: &weight, Reps: 10}}}
				raw, _ := json.Marshal(body)
				day := strengthDay(start, time.UTC)
				plan := assistantwire.StrengthDraft{ID: healthIntakeUUID("golden-plan"), Kind: "plan", Status: "open", Revision: 1, Body: raw, PlanDay: &day, Conversation: "training", MachineID: "computer", CardID: strengthCardPrefix + "golden", CardVersion: 1, CreatedAt: start, UpdatedAt: start}
				card, err := s.strengthCard(ctx, plan, body, "open")
				if err != nil {
					t.Fatal(err)
				}
				if err = d.HealthStrengthDraftCreate(ctx, plan, assistantwire.StrengthCard{ID: plan.CardID, Conversation: plan.Conversation, MachineID: plan.MachineID, FeedID: "golden-plan", Version: 1, View: card, At: start}); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "sets" {
				rows := []assistantwire.StrengthSet{
					{ID: healthIntakeUUID("prior"), ExerciseID: e.ID, Weight: 40, Reps: 10, At: start.AddDate(0, 0, -2)},
					{ID: healthIntakeUUID("prior-warmup"), ExerciseID: e.ID, Weight: 100, Reps: 3, Warmup: true, At: start.AddDate(0, 0, -2).Add(-time.Minute)},
					{ID: healthIntakeUUID("prestart"), ExerciseID: e.ID, Weight: 45, Reps: 10, At: start.Add(-10 * time.Minute)},
					{ID: healthIntakeUUID("current"), ExerciseID: e.ID, Weight: 35, Reps: 12, At: start.Add(10 * time.Minute)},
					{ID: healthIntakeUUID("warmup"), ExerciseID: e.ID, Weight: 60, Reps: 1, Warmup: true, At: start.Add(20 * time.Minute)},
				}
				if _, err := d.HealthStrengthSetAdd(ctx, rows); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.WorkoutReceived(ctx, workout); err != nil {
				t.Fatal(err)
			}
			if scenario == "sets" {
				rows, _ := d.HealthStrengthSets(ctx, assistantwire.StrengthSetFilter{Workout: workout.ID})
				prior, _ := d.HealthStrengthSets(ctx, assistantwire.StrengthSetFilter{Until: start.Add(-30 * time.Minute)})
				input := strengthReportInputs(rows, prior, []assistantwire.StrengthExercise{e}, time.UTC)
				if len(input) != 1 || len(input[0].Sets) != 3 || input[0].Best["weight"] != 40 || input[0].Previous.Day != "2026-11-18" || !input[0].Sets[0].Record || input[0].Sets[1].Record || input[0].Sets[2].Record {
					t.Fatalf("wrong comparison/record inputs: %+v", input)
				}
			}
			for _, language := range []struct{ tag, value string }{{"en", "English"}, {"zh-Hans", ""}, {"ja", "日本語"}} {
				report, ok, err := s.WorkoutReport(ctx, workout.ID, language.value)
				prompt := report.Prompt
				if ok && !report.Start.Equal(workout.Start) {
					t.Fatalf("report start = %v, want %v", report.Start, workout.Start)
				}
				if err != nil || !ok {
					t.Fatal(ok, err)
				}
				if strings.Contains(prompt, "call `phone_workouts_recent` once") {
					t.Fatal("strength report still asks for comparison tool")
				}
				file := filepath.Join("testdata", "workout_report.strength_"+scenario+"."+language.tag+".golden")
				if os.Getenv("UPDATE_GOLDEN") != "" {
					if err = os.MkdirAll(filepath.Dir(file), 0755); err != nil {
						t.Fatal(err)
					}
					if err = os.WriteFile(file, []byte(prompt), 0644); err != nil {
						t.Fatal(err)
					}
				}
				want, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				if prompt != string(want) {
					t.Fatalf("%s changed (UPDATE_GOLDEN=1 writes it)", file)
				}
			}
			if err := d.PutSetting(ctx, healthItemsKey, healthItemsSetting{Items: []string{}, Revision: 2}); err != nil {
				t.Fatal(err)
			}
			if _, ok, err := s.WorkoutReport(ctx, workout.ID, "English"); ok || err != nil {
				t.Fatal("removed item changed report", ok, err)
			}
		})
	}
}

type failingStrengthReportStore struct{ *db.DB }

func (f failingStrengthReportStore) HealthStrengthSets(context.Context, assistantwire.StrengthSetFilter) ([]assistantwire.StrengthSet, error) {
	return nil, errors.New("synthetic report read failure")
}
func TestStrengthWorkoutReportMissingStorageAndOtherActivities(t *testing.T) {
	s, d, ctx := strengthHarness(t)
	id := healthIntakeUUID("absent-workout")
	if _, ok, err := s.WorkoutReport(ctx, id, ""); ok || err != nil {
		t.Fatal("missing window assembled", ok, err)
	}
	w := assistantwire.StrengthWorkout{ID: id, Start: time.Now().Add(-time.Hour), End: time.Now(), Report: json.RawMessage(`{"activity":"Running"}`)}
	if err := s.WorkoutReceived(ctx, w); err != nil {
		t.Fatal(err)
	}
	if got, err := d.HealthStrengthWorkout(ctx, id); got != nil || err != nil {
		t.Fatal("running took a strength window")
	}
	w.Report = json.RawMessage(`{"activity":"Strength training"}`)
	if err := s.WorkoutReceived(ctx, w); err != nil {
		t.Fatal(err)
	}
	failing := newServiceOn(t, newFakeHub(), failingStrengthReportStore{d})
	if _, ok, err := failing.WorkoutReport(ctx, id, ""); ok || err == nil {
		t.Fatal("read failure hidden")
	}
	absent := newServiceOn(t, newFakeHub(), &intakeTestStore{})
	if _, ok, err := absent.WorkoutReport(ctx, id, ""); ok || err != nil {
		t.Fatal("missing storage assembled")
	}
	if err := absent.WorkoutReceived(ctx, w); err == nil {
		t.Fatal("upload acknowledged without strength storage")
	}
}
