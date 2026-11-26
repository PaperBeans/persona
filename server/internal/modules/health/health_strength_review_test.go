package health

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/assistantwire"
)

func TestStrengthPlanSaveUsesLatestWorkoutOnPlanDay(t *testing.T) {
	for _, hasWorkout := range []bool{false, true} {
		t.Run(fmt.Sprint(hasWorkout), func(t *testing.T) {
			s, d, ctx := strengthHarness(t)
			e := createStrengthExercise(t, s, ctx, "stack")
			start := time.Now().Truncate(time.Microsecond).Add(-6 * time.Hour)
			day := strengthDay(start, s.h.Location(ctx))
			target := assistantwire.StrengthWorkout{ID: healthIntakeUUID("review-latest"), Start: start, End: start.Add(time.Hour), Report: json.RawMessage(`{"activity":"Strength training"}`)}
			if hasWorkout {
				for _, w := range []assistantwire.StrengthWorkout{
					{ID: healthIntakeUUID("review-earlier"), Start: day, End: day.Add(30 * time.Minute), Report: target.Report},
					target,
					{ID: healthIntakeUUID("review-next-day"), Start: day.AddDate(0, 0, 1).Add(time.Hour), End: day.AddDate(0, 0, 1).Add(2 * time.Hour), Report: target.Report},
				} {
					if err := s.WorkoutReceived(ctx, w); err != nil {
						t.Fatal(err)
					}
				}
			}
			weight := 40.0
			body := strengthDraftBody{Sets: []draftSet{{Exercise: e.ID, Weight: &weight, Reps: 10}}}
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			draft := assistantwire.StrengthDraft{ID: healthIntakeUUID("review-plan"), Kind: "plan", Status: "open", Revision: 1, Body: raw, PlanDay: &day, Conversation: "training", MachineID: "computer", CardID: strengthCardPrefix + "review-plan", CardVersion: 1}
			view, err := s.strengthCard(ctx, draft, body, "open")
			if err != nil {
				t.Fatal(err)
			}
			if err := d.HealthStrengthDraftCreate(ctx, draft, assistantwire.StrengthCard{ID: draft.CardID, Conversation: draft.Conversation, MachineID: draft.MachineID, FeedID: "review-plan", View: view, Version: 1, At: day}); err != nil {
				t.Fatal(err)
			}
			before := time.Now().UTC()
			result, err := s.CardAction(ctx, assistantwire.HealthCardAction{Conversation: "training", Key: draft.CardID, Action: "save", Version: 1, Declared: true, ClientMsgID: "save-plan"})
			after := time.Now().UTC()
			if err != nil || result.Outcome != "done" {
				t.Fatal(result, err)
			}
			rows, err := d.HealthStrengthSets(ctx, assistantwire.StrengthSetFilter{Exercise: e.ID})
			if err != nil || len(rows) != 1 {
				t.Fatal(rows, err)
			}
			if hasWorkout {
				if !before.After(target.End.Add(5*time.Hour)) || !rows[0].At.Equal(target.Start) || rows[0].WorkoutID == nil || *rows[0].WorkoutID != target.ID || rows[0].At.In(s.h.Location(ctx)).Format("2006-01-02") != day.Format("2006-01-02") {
					t.Fatalf("late plan save lost workout/day: %+v, target=%+v", rows[0], target)
				}
			} else if rows[0].At.Before(before) || rows[0].At.After(after) {
				t.Fatalf("no workout did not use confirmation time: %+v", rows[0])
			}
		})
	}
}

func TestStrengthDeletedExerciseCardSaveIsDefinite(t *testing.T) {
	s, d, ctx := strengthHarness(t)
	e := createStrengthExercise(t, s, ctx, "stack")
	out, err := strengthCall(s, ctx, fmt.Sprintf(`{"action":"draft","sets":[{"exercise":%q,"weight":40,"reps":10}]}`, e.ID))
	if err != nil {
		t.Fatal(err)
	}
	var ids struct{ Draft, Card string }
	if err := json.Unmarshal([]byte(out), &ids); err != nil {
		t.Fatal(err)
	}
	if _, err := d.HealthStrengthExerciseDelete(ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	action := assistantwire.HealthCardAction{Conversation: "training", Key: ids.Card, Action: "save", Version: 1, Declared: true, ClientMsgID: "save"}
	for range 2 {
		result, err := s.CardAction(ctx, action)
		if err != nil || result.Outcome != "invalid_draft" {
			t.Fatal(result, err)
		}
	}
	draft, err := d.HealthStrengthDraft(ctx, ids.Draft)
	if err != nil || draft.Status != "open" || draft.Revision != 1 {
		t.Fatal(draft, err)
	}
	action.Action = "discard"
	result, err := s.CardAction(ctx, action)
	if err != nil || result.Outcome != "done" {
		t.Fatal(result, err)
	}
}

func TestStrengthHistoryAllExercisesIsBoundedAndGrouped(t *testing.T) {
	s, d, ctx := strengthHarness(t)
	e := createStrengthExercise(t, s, ctx, "stack")
	other := e
	other.ID = healthIntakeUUID("history-other")
	other.Name = "Row"
	if _, _, err := d.HealthStrengthExerciseSave(ctx, other); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rows := []assistantwire.StrengthSet{}
	for i := range 501 {
		rows = append(rows, assistantwire.StrengthSet{ID: healthIntakeUUID(fmt.Sprintf("history-%d", i)), ExerciseID: e.ID, Weight: 40, Reps: 10, At: now.Add(-time.Duration(i+1) * time.Second)})
	}
	rows = append(rows, assistantwire.StrengthSet{ID: healthIntakeUUID("history-second"), ExerciseID: other.ID, Weight: 20, Reps: 10, At: now.Add(-time.Millisecond)})
	rows = append(rows, assistantwire.StrengthSet{ID: healthIntakeUUID("history-old"), ExerciseID: e.ID, Weight: 40, Reps: 10, At: now.AddDate(0, 0, -4)})
	rows = append(rows, assistantwire.StrengthSet{ID: healthIntakeUUID("history-future"), ExerciseID: e.ID, Weight: 40, Reps: 10, At: now.Add(time.Hour)})
	if _, err := d.HealthStrengthSetAdd(ctx, rows); err != nil {
		t.Fatal(err)
	}
	for _, withID := range []bool{false, true} {
		args := `{"action":"history","days":2}`
		if withID {
			args = fmt.Sprintf(`{"action":"history","days":2,"id":%q}`, strings.ToUpper(e.ID))
		}
		out, err := strengthCall(s, ctx, args)
		if err != nil {
			t.Fatal(err)
		}
		var grouped map[string][]assistantwire.StrengthSet
		if err := json.Unmarshal([]byte(out), &grouped); err != nil {
			t.Fatal(err)
		}
		count := 0
		foundOther := false
		for day, sets := range grouped {
			for _, v := range sets {
				count++
				foundOther = foundOther || v.ExerciseID == other.ID
				if day != v.At.In(s.h.Location(ctx)).Format("2006-01-02") || v.ID == rows[len(rows)-1].ID || v.ID == rows[len(rows)-2].ID || withID && v.ExerciseID != e.ID {
					t.Fatalf("wrong day/range/exercise: %s %+v", day, v)
				}
			}
		}
		if !withID && (count != 500 || !foundOther) || withID && (count != 501 || foundOther) {
			t.Fatalf("history withID=%v count=%d other=%v", withID, count, foundOther)
		}
	}
	for _, args := range []string{`{"action":"history","id":""}`, `{"action":"history","id":"bad"}`, `{"action":"history","days":0}`, `{"action":"history","days":367}`} {
		if _, err := strengthCall(s, ctx, args); err == nil {
			t.Fatalf("accepted %s", args)
		}
	}
}

func TestStrengthSettledEditorActionsReturnReadOnly(t *testing.T) {
	for _, settle := range []string{"save", "discard"} {
		t.Run(settle, func(t *testing.T) {
			s, _, ctx := strengthHarness(t)
			e := createStrengthExercise(t, s, ctx, "stack")
			out, err := strengthCall(s, ctx, fmt.Sprintf(`{"action":"draft","sets":[{"exercise":%q,"weight":40,"reps":10}]}`, e.ID))
			if err != nil {
				t.Fatal(err)
			}
			var ids struct{ Draft, Card string }
			if err := json.Unmarshal([]byte(out), &ids); err != nil {
				t.Fatal(err)
			}
			editor := strengthState(t, strengthPageHTTP(t, s, strengthPageState{Page: "strength.edit", Draft: ids.Draft}, "", nil, 11))
			result, err := s.CardAction(ctx, assistantwire.HealthCardAction{Conversation: "training", Key: ids.Card, Version: 1, Action: settle, Declared: true, ClientMsgID: "settle"})
			if err != nil || result.Outcome != "done" {
				t.Fatal(result, err)
			}
			for _, action := range []string{"save", "set:0", "add", "remove:0"} {
				res := strengthPageHTTP(t, s, editor, action, map[string]string{"weight_0": "bad"}, 11)
				raw, _ := json.Marshal(res.View)
				if res.Toast == "" || res.Pop || strings.Contains(string(raw), `"control"`) || len(wire.Actions(res.View)) != 0 {
					t.Fatalf("settled %s is not read-only: %+v", action, res)
				}
			}
			raw, _ := json.Marshal(editor)
			if w := intakePageHTTP(s, "", "save", nil, string(raw), 5, "full"); w.Code != 400 || strings.Contains(w.Body.String(), "Water or Protein") || strings.Contains(w.Body.String(), "喝水或蛋白质") {
				t.Fatalf("legacy invalid action: %d %s", w.Code, w.Body)
			}
		})
	}
}

func TestStrengthRenameCollisionPreservesInput(t *testing.T) {
	s, d, ctx := strengthHarness(t)
	e := createStrengthExercise(t, s, ctx, "stack")
	other := e
	other.ID = healthIntakeUUID("rename-other")
	other.Name = "Row"
	if _, _, err := d.HealthStrengthExerciseSave(ctx, other); err != nil {
		t.Fatal(err)
	}
	page := strengthPageState{Page: "strength.overview", Manage: true, Exercise: other.ID, Field: "rename"}
	res := strengthPageHTTP(t, s, page, "update", map[string]string{"value": "pReSs"}, 11)
	state := strengthState(t, res)
	if res.Toast == "" || state.Field != "rename" || state.EditValue == nil || *state.EditValue != "pReSs" {
		t.Fatalf("lost duplicate input: %+v %+v", state, res)
	}
	found := false
	for _, sec := range res.View.Sections {
		for _, item := range sec.Items {
			if item.Type == "field" && item.Name == "value" {
				found = item.Value == "pReSs"
			}
		}
	}
	if !found {
		t.Fatal("duplicate input not drawn")
	}
	exercises, err := d.HealthStrengthExercises(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range exercises {
		if v.ID == other.ID && v.Name != "Row" {
			t.Fatalf("duplicate persisted: %+v", v)
		}
	}
	res = strengthPageHTTP(t, s, state, "update", map[string]string{"value": "Pulldown"}, 11)
	state = strengthState(t, res)
	if state.Field != "" || state.EditValue != nil {
		t.Fatalf("successful rename kept error input: %+v", state)
	}
}

func TestStrengthRecordsRequireWorkingSetBaseline(t *testing.T) {
	now := time.Now()
	e := assistantwire.StrengthExercise{ID: healthIntakeUUID("record-exercise"), Name: "Press"}
	current := assistantwire.StrengthSet{ExerciseID: e.ID, Weight: 40, Reps: 10, At: now.Add(-time.Minute)}
	for _, prior := range [][]assistantwire.StrengthSet{nil, {{ExerciseID: e.ID, Weight: 20, Reps: 10, Warmup: true, At: now.Add(-time.Hour)}}} {
		input := strengthReportInputs([]assistantwire.StrengthSet{current}, prior, []assistantwire.StrengthExercise{e}, time.UTC)
		if len(input) != 1 || input[0].Sets[0].Record {
			t.Fatalf("first working set marked as record: %+v", input)
		}
		rows := append([]assistantwire.StrengthSet{current}, prior...)
		if got := strengthRecentRecords(rows, []assistantwire.StrengthExercise{e}, now, time.UTC, "en"); len(got) != 0 {
			t.Fatalf("first working set in recent records: %+v", got)
		}
	}
	prior := []assistantwire.StrengthSet{{ExerciseID: e.ID, Weight: 20, Reps: 10, At: now.AddDate(0, 0, -40)}}
	if !strengthReportInputs([]assistantwire.StrengthSet{current}, prior, []assistantwire.StrengthExercise{e}, time.UTC)[0].Sets[0].Record {
		t.Fatal("improved working set lost record")
	}
	if got := strengthRecentRecords(append([]assistantwire.StrengthSet{current}, prior...), []assistantwire.StrengthExercise{e}, now, time.UTC, "en"); len(got) != 1 {
		t.Fatal("improved set missing from home", got)
	}
}

func TestStrengthReportPlanNamesIncludeDeletedExercises(t *testing.T) {
	at := time.Now()
	weight := 40.0
	e := assistantwire.StrengthExercise{ID: healthIntakeUUID("deleted-plan-exercise"), Name: "Seated press", DeletedAt: &at}
	plan, err := strengthReportPlan([]draftSet{{Exercise: e.ID, Weight: &weight, Reps: 10, At: &at}}, []assistantwire.StrengthExercise{e})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), e.ID) || strings.Contains(string(raw), `"at"`) || !strings.Contains(string(raw), "Seated press") {
		t.Fatal(string(raw))
	}
}
