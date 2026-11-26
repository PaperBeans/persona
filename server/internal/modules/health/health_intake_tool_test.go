package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

func TestHealthIntakeToolLogListDelete(t *testing.T) {
	ctx := context.Background()
	st := &intakeTestStore{}
	if err := st.PutSetting(ctx, healthIntakeKey, defaultHealthIntakeSettings()); err != nil {
		t.Fatal(err)
	}
	s := newServiceOn(t, newFakeHub(), st)
	call := func(args string) string {
		t.Helper()
		out, err := s.intakeTool(ctx, json.RawMessage(args))
		if err != nil {
			t.Fatalf("%s: %v", args, err)
		}
		return out
	}
	if out := call(`{"action":"log","kind":"water","amount":300}`); !strings.Contains(out, "Recorded water 300 ml") || !strings.Contains(out, "water 300/2000 ml") {
		t.Fatal(out)
	}
	reminder, err := s.waterReminderState(ctx)
	if err != nil || reminder.Last.IsZero() {
		t.Fatalf("water write did not reset reminder: %+v %v", reminder, err)
	}
	call(`{"action":"log","kind":"protein","amount":24.5,"note":"Eggs, two"}`)
	// A second log is a new record, even with identical arguments.
	call(`{"action":"log","kind":"protein","amount":24.5,"note":"Eggs, two"}`)
	loc := s.h.Location(ctx)
	now := time.Now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	yesterday := today.AddDate(0, 0, -1).Add(12 * time.Hour)
	old := today.AddDate(0, 0, -7).Add(12 * time.Hour)
	call(fmt.Sprintf(`{"action":"log","kind":"protein","amount":10,"at":%q,"note":"Earlier meal"}`, yesterday.Format(time.RFC3339)))
	call(fmt.Sprintf(`{"action":"log","kind":"water","amount":100,"at":%q}`, old.Format(time.RFC3339)))
	var list struct {
		Entries []assistantwire.HealthIntakeRow `json:"entries"`
		Days    []healthIntakeDay               `json:"days"`
		Today   healthIntakeTotals              `json:"today"`
		Goals   healthIntakeTotals              `json:"goals"`
		Zone    string                          `json:"zone"`
	}
	if err := json.Unmarshal([]byte(call(`{"action":"list"}`)), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Entries) != 4 || len(list.Days) != 7 || list.Today.Water != 300 || list.Today.Protein != 49 ||
		list.Goals.Water != 2000 || list.Goals.Protein != 60 || list.Zone != loc.String() || list.Days[5].Protein != 10 {
		t.Fatalf("list: %+v", list)
	}
	ids := map[string]bool{}
	var earlierID, proteinID string
	for _, entry := range list.Entries {
		if !intakeUUIDPattern.MatchString(entry.ID) || ids[entry.ID] {
			t.Fatalf("missing or repeated server ID: %+v", entry)
		}
		ids[entry.ID] = true
		if entry.Note == "Earlier meal" {
			earlierID = entry.ID
		}
		if entry.Note == "Eggs, two" {
			proteinID = entry.ID
		}
	}
	if earlierID == "" || proteinID == "" {
		t.Fatalf("notes not retained: %+v", list.Entries)
	}
	if out := call(fmt.Sprintf(`{"action":"delete","id":%q}`, earlierID)); !strings.Contains(out, "protein 49/60 g") {
		t.Fatal(out)
	}
	seq := st.seq
	call(fmt.Sprintf(`{"action":"delete","id":%q}`, strings.ToUpper(earlierID)))
	if st.seq != seq {
		t.Fatal("repeated deletion mutated the store")
	}
	if out := call(fmt.Sprintf(`{"action":"delete","id":%q}`, proteinID)); !strings.Contains(out, "protein 24.5/60 g") {
		t.Fatal(out)
	}
	if err := json.Unmarshal([]byte(call(`{"action":"list","days":1}`)), &list); err != nil || len(list.Entries) != 2 || len(list.Days) != 1 {
		t.Fatalf("today's active entries: %+v %v", list, err)
	}
	if err := json.Unmarshal([]byte(call(`{"action":"list","days":8}`)), &list); err != nil || len(list.Entries) != 3 || list.Days[0].Water != 100 {
		t.Fatalf("older entry omitted: %+v %v", list, err)
	}
}

func TestHealthIntakeToolValidation(t *testing.T) {
	st := &intakeTestStore{}
	s := newServiceOn(t, newFakeHub(), st)
	for _, args := range []string{
		`{`, `null`, `[]`, `{}`, `{"action":"unknown"}`, `{"action":"list"} {}`,
		`{"action":"list","days":0}`, `{"action":"list","days":367}`, `{"action":"list","days":1.5}`,
		`{"action":"list","amount":300}`, `{"action":"list","unexpected":true}`,
		`{"action":"log","kind":"water"}`, `{"action":"log","amount":300}`,
		`{"action":"log","kind":"other","amount":1}`, `{"action":"log","kind":"water","amount":0}`,
		`{"action":"log","kind":"water","amount":0.5}`, `{"action":"log","kind":"water","amount":3000.1}`,
		`{"action":"log","kind":"protein","amount":-1}`, `{"action":"log","kind":"protein","amount":200.1}`,
		`{"action":"log","kind":"protein","amount":1.01}`, `{"action":"log","kind":"protein","amount":1e999}`,
		`{"action":"log","kind":"water","amount":"300"}`, `{"action":"log","kind":"water","amount":1,"at":"yesterday"}`,
		`{"action":"log","kind":"water","amount":1,"note":"\u0000"}`,
		`{"action":"log","kind":"water","amount":1,"days":1}`,
		`{"action":"log","kind":"water","amount":1,"id":"00000000-0000-4000-8000-000000000001"}`,
		`{"action":"delete"}`, `{"action":"delete","id":"bad"}`,
		`{"action":"delete","id":"00000000-0000-4000-8000-000000000001","days":1}`,
		`{"action":"log","kind":"protein","amount":1,"note":"` + strings.Repeat("a", 4097) + `"}`,
	} {
		if out, err := s.toolHealthIntake(context.Background(), json.RawMessage(args)); err == nil {
			t.Errorf("accepted %s: %s", args, out)
		}
	}
	if st.seq != 0 || len(st.rows) != 0 {
		t.Fatal("invalid arguments changed storage")
	}
	if _, err := s.toolHealthIntake(context.Background(), json.RawMessage(`{"action":"delete","id":"00000000-0000-4000-8000-000000000001"}`)); err == nil {
		t.Fatal("unknown entry deleted successfully")
	}
	st.fail = true
	if _, err := s.toolHealthIntake(context.Background(), json.RawMessage(`{"action":"list"}`)); err == nil {
		t.Fatal("storage failure became an empty success")
	}
	st.fail = false
	st.failKey(healthIntakeKey, errors.New("synthetic settings failure"))
	if _, err := s.toolHealthIntake(context.Background(), json.RawMessage(`{"action":"list"}`)); err == nil {
		t.Fatal("settings failure was ignored")
	}
}
