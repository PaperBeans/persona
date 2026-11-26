package health

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
)

func strengthHarness(t *testing.T) (*Service, *db.DB, context.Context) {
	t.Helper()
	ctx := i18n.With(context.Background(), i18n.En)
	d, err := db.Open(ctx, newDatabase(t), db.ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	if err := d.PutSetting(ctx, healthItemsKey, healthItemsSetting{Items: []string{"strength"}, Revision: 1}); err != nil {
		t.Fatal(err)
	}
	return newServiceOn(t, newFakeHub(), d), d, ctx
}
func strengthCall(s *Service, ctx context.Context, args string) (string, error) {
	return s.Tool(ctx, assistantwire.HealthToolCall{Name: assistantwire.CapabilityHealthStrength, Machine: "computer", Session: "training", Args: json.RawMessage(args)})
}
func createStrengthExercise(t *testing.T, s *Service, ctx context.Context, rule string) assistantwire.StrengthExercise {
	t.Helper()
	args := `{"action":"create","name":"Press","aliases":["Seat press"],"equipment":"machine"}`
	if rule != "" {
		args = `{"action":"create","name":"Press","aliases":["Seat press"],"equipment":"machine","weight_rule":"` + rule + `"}`
	}
	out, err := strengthCall(s, ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	var e assistantwire.StrengthExercise
	if err := json.Unmarshal([]byte(out), &e); err != nil {
		t.Fatal(err)
	}
	return e
}
func TestStrengthToolActionsAndValidation(t *testing.T) {
	s, d, ctx := strengthHarness(t)
	e := createStrengthExercise(t, s, ctx, "")
	if _, err := strengthCall(s, ctx, `{"action":"create","name":"Press"}`); err == nil || !strings.Contains(err.Error(), e.ID) {
		t.Fatal("duplicate name lost matching id", err)
	}
	setArgs := `[{"exercise":"` + e.ID + `","weight":40,"reps":10}]`
	if _, err := strengthCall(s, ctx, `{"action":"log","sets":`+setArgs+`}`); err == nil || !strings.Contains(err.Error(), "weight_rule") {
		t.Fatal("missing rule accepted", err)
	}
	if _, err := strengthCall(s, ctx, `{"action":"update","id":"`+e.ID+`","name":"Machine press","weight_rule":"stack","aliases":["Pec press"],"primary":["chest"],"secondary":["triceps"],"setting":"Seat 3"}`); err != nil {
		t.Fatal(err)
	}
	out, err := strengthCall(s, ctx, `{"action":"find","q":"  PEC   PRESS "}`)
	if err != nil || !strings.Contains(out, "Machine press") || !strings.Contains(out, "Seat press") {
		t.Fatal(out, err)
	}
	out, err = strengthCall(s, ctx, `{"action":"log","sets":`+setArgs+`}`)
	if err != nil {
		t.Fatal(err)
	}
	var rows []assistantwire.StrengthSet
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 1 {
		t.Fatal(out, err)
	}
	out, err = strengthCall(s, ctx, `{"action":"history","id":"`+strings.ToUpper(e.ID)+`"}`)
	if err != nil || !strings.Contains(out, rows[0].ID) {
		t.Fatal(out, err)
	}
	out, err = strengthCall(s, ctx, `{"action":"find"}`)
	if err != nil || !strings.Contains(out, `"weight":40`) || !strings.Contains(out, `"e1rm":`) {
		t.Fatal(out, err)
	}
	for range 2 {
		if _, err := strengthCall(s, ctx, `{"action":"undo","id":"`+rows[0].ID+`"}`); err != nil {
			t.Fatal(err)
		}
	}
	saved, err := d.HealthStrengthSets(ctx, assistantwire.StrengthSetFilter{})
	if err != nil || len(saved) != 0 {
		t.Fatal(saved, err)
	}
	for _, bad := range []string{`{"action":"update","id":"` + e.ID + `","weight_rule":""}`, `{"action":"unknown"}`, `{"action":"find","days":1}`, `{"action":"create","name":""}`, `{"action":"log","sets":[]}`, `{"action":"history","id":"bad"}`, `{"action":"history","id":"` + e.ID + `","days":0}`, `{"action":"find","q":null}`, `{"action":"log","sets":[{"exercise":"` + e.ID + `","weight":1.234,"reps":1}]}`, `{"action":"log","sets":[{"exercise":"` + e.ID + `","weight":1,"reps":0}]}`, `{"action":"log","sets":[{"exercise":"` + e.ID + `","weight":1,"reps":1,"extra":1}]}`, `{"action":"find"} {}`} {
		if _, err := strengthCall(s, ctx, bad); err == nil {
			t.Errorf("accepted invalid tool arguments %s", bad)
		}
	}
}
func TestStrengthToolItemGateAndDraftIsolation(t *testing.T) {
	s, d, ctx := strengthHarness(t)
	e := createStrengthExercise(t, s, ctx, "stack")
	if err := d.PutSetting(ctx, healthItemsKey, healthItemsSetting{Items: []string{}, Revision: 2}); err != nil {
		t.Fatal(err)
	}
	out, err := strengthCall(s, ctx, `{"action":"create","name":"Hidden"}`)
	if err != nil || !strings.Contains(out, "ask_user") {
		t.Fatal(out, err)
	}
	for _, args := range []string{`{"action":"find"}`, `{"action":"history","id":"` + e.ID + `"}`} {
		if _, err := strengthCall(s, ctx, args); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.intakeTool(ctx, json.RawMessage(`{"action":"add_item","item":"strength"}`)); err != nil {
		t.Fatal(err)
	}
	out, err = strengthCall(s, ctx, `{"action":"draft","plan":true,"sets":[{"exercise":"`+e.ID+`","weight":40,"reps":10},{"exercise":"`+e.ID+`","weight":35,"reps":12,"warmup":true}]}`)
	if err != nil {
		t.Fatal(err)
	}
	var result struct{ Draft, Card string }
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	draft, err := d.HealthStrengthDraft(ctx, result.Draft)
	if err != nil || draft == nil || draft.Kind != "plan" || draft.PlanDay == nil {
		t.Fatal(draft, err)
	}
	sets, err := d.HealthStrengthSets(ctx, assistantwire.StrengthSetFilter{})
	if err != nil || len(sets) != 0 {
		t.Fatal("draft leaked sets", sets, err)
	}
	if out, err := strengthCall(s, ctx, `{"action":"show","id":"`+draft.ID+`"}`); err != nil || !strings.Contains(out, result.Card) {
		t.Fatal(out, err)
	}
	if err := d.PutSetting(ctx, healthItemsKey, healthItemsSetting{Items: []string{}, Revision: 3}); err != nil {
		t.Fatal(err)
	}
	res, err := s.CardAction(ctx, assistantwire.HealthCardAction{Conversation: draft.Conversation, Key: draft.CardID, Version: 1, Action: "save", ClientMsgID: "click", Declared: true})
	if err != nil || res.Outcome != "done" {
		t.Fatal(res, err)
	}
	sets, err = d.HealthStrengthSets(ctx, assistantwire.StrengthSetFilter{})
	if err != nil || len(sets) != 2 {
		t.Fatal(sets, err)
	}
}
func TestStrengthDraftEditMoveDiscardAndReceipts(t *testing.T) {
	s, d, ctx := strengthHarness(t)
	e := createStrengthExercise(t, s, ctx, "stack")
	raw := `{"action":"draft","sets":[{"exercise":"` + e.ID + `","weight":40,"reps":10}]}`
	out, err := strengthCall(s, ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	var res struct{ Draft, Card string }
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	weight := 35.0
	now := time.Now().UTC()
	sets := []draftSet{{Exercise: e.ID, Weight: &weight, Reps: 12, At: &now}}
	edited, err := s.editStrengthDraft(ctx, res.Draft, 1, sets)
	if err != nil || edited.Revision != 2 || edited.CardVersion != 2 {
		t.Fatal(edited, err)
	}
	if _, err := s.editStrengthDraft(ctx, res.Draft, 1, sets); !errors.Is(err, assistantwire.ErrHealthStrengthDraftStale) {
		t.Fatal("stale edit", err)
	}
	out, err = s.Tool(ctx, assistantwire.HealthToolCall{Name: assistantwire.CapabilityHealthStrength, Machine: "computer", Session: "new-training", Args: json.RawMessage(`{"action":"show","id":"` + res.Draft + `"}`)})
	if err != nil {
		t.Fatal(err)
	}
	old, err := d.AssistantView(ctx, "training", res.Card)
	if err != nil {
		t.Fatal(err)
	}
	v, err := wire.ParseAssistantView(old.View)
	if err != nil || len(wire.Actions(v)) != 0 {
		t.Fatal("old card still actionable", err)
	}
	draft, err := d.HealthStrengthDraft(ctx, res.Draft)
	if err != nil || draft.Conversation != "new-training" || draft.CardVersion != 1 || draft.Revision != 3 {
		t.Fatal(draft, err)
	}
	a := assistantwire.HealthCardAction{Conversation: draft.Conversation, Key: draft.CardID, Version: 1, Action: "discard", ClientMsgID: "click", Declared: true}
	answer, err := s.CardAction(ctx, a)
	if err != nil || answer.Outcome != "done" {
		t.Fatal(answer, err)
	}
	retry, err := s.CardAction(ctx, a)
	if err != nil || retry.Outcome != "done" || !retry.At.Equal(answer.At) {
		t.Fatal(retry, err)
	}
	a.ClientMsgID = "second"
	other, err := s.CardAction(ctx, a)
	if err != nil || other.Outcome != "answered" {
		t.Fatal(other, err)
	}
	changes, err := d.HealthStrengthSetChanges(ctx, 0, 100)
	if err != nil || len(changes) != 0 {
		t.Fatal("discard leaked sync rows", changes, err)
	}
	if _, err := s.editStrengthDraft(ctx, res.Draft, 3, sets); !errors.Is(err, assistantwire.ErrHealthStrengthDraftSettled) {
		t.Fatal(err)
	}
	_ = out
}
func TestStrengthCardLanguagesAndReservedPageLink(t *testing.T) {
	s, d, ctx := strengthHarness(t)
	e := createStrengthExercise(t, s, ctx, "stack")
	s.h.(*fakeCore).pageBase = "https://example.com"
	for _, lang := range []i18n.Lang{i18n.En, i18n.ZhHans, i18n.Ja} {
		ctx := i18n.With(ctx, lang)
		out, err := strengthCall(s, ctx, `{"action":"draft","sets":[{"exercise":"`+e.ID+`","weight":40,"reps":10}]}`)
		if err != nil {
			t.Fatal(err)
		}
		var res struct{ Draft, Card string }
		if err := json.Unmarshal([]byte(out), &res); err != nil {
			t.Fatal(err)
		}
		row, err := d.AssistantView(ctx, "training", res.Card)
		if err != nil {
			t.Fatal(err)
		}
		v, err := wire.ParseAssistantView(row.View)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, a := range wire.Actions(v) {
			if a.ID == "adjust" {
				found = strings.Contains(a.Open, "/extension/-health-intake/pages?") && strings.Contains(a.Open, "strength.edit")
			}
		}
		if !found {
			t.Fatal("missing reserved edit link", string(row.View))
		}
	}
}
