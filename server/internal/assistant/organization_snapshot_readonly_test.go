package assistant

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/nwverse/persona/server/internal/db"
)

func orgDatabaseRows(t *testing.T, database *db.DB) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, table := range []string{"todos", "todo_agents", "todo_supervision", "settings", "task_plans", "task_briefs", "task_brief_todos", "assistant_feed"} {
		var rows string
		query := `SELECT coalesce(jsonb_agg(to_jsonb(entry) ORDER BY to_jsonb(entry)), '[]'::jsonb)::text FROM ` + table + ` AS entry`
		if err := database.QueryRow(context.Background(), query).Scan(&rows); err != nil {
			t.Fatalf("read %s: %v", table, err)
		}
		out[table] = rows
	}
	return out
}

func TestOrganizationSnapshotPostgresColdAndWarmReadsDoNotWrite(t *testing.T) {
	database := todoTestDB(t)
	seed := newTodoEventEnv(t, database)
	seed.s.tpush.after = seed.clock.After
	setSettings(t, seed.s, func(v *Settings) { v.Main, v.Timezone = "conv_main", "UTC" })
	todo, err := seed.s.createTodo(context.Background(), "Postgres worker", "", "conv_main", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.s.linkTodoAgent(context.Background(), todo.ID, "mac", "worker", "Worker", "/home/user/project", "claude"); err != nil {
		t.Fatal(err)
	}
	if err := seed.s.superviseTodo(context.Background(), todo.ID, "conv_main", []string{"Read contract passes"}, 0); err != nil {
		t.Fatal(err)
	}

	// Seed writes queue notifications. Share the database with the read service,
	// and give the reader a fresh hub so those notifications are attributed to the writer.
	readHub := newFakeHub().withMachine()
	readHub.setSessions("mac", orgWorkingSession("mac", "worker"))
	readHub.answerNode(DeviceSessionItems, func(string, json.RawMessage) (any, error) { return historyPage{}, nil })
	cold := newServiceOn(t, readHub, database)
	readClock := todoViewClock(&todoEnv{t: t, s: cold, f: readHub})
	before := orgDatabaseRows(t, database)
	readHub.mu.Lock()
	modelRequests, events, starts, sends := readHub.modelRequests, len(readHub.events), len(readHub.starts), len(readHub.sends)
	readHub.mu.Unlock()
	cold.admission.mu.Lock()
	machineClaims := maps.Clone(cold.admission.machineClaims)
	directionClaims := maps.Clone(cold.admission.directionClaims)
	groupClaims := maps.Clone(cold.admission.groupClaims)
	cold.admission.mu.Unlock()
	seed.clock.advance(todosSettle)
	if todosSent(seed.todoEnv) == 0 {
		t.Fatal("setup did not send its queued todos event")
	}
	for read := 1; read <= 2; read++ {
		recorder := httptest.NewRecorder()
		cold.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, organizationSnapshotRoute, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("read %d: %d %s", read, recorder.Code, recorder.Body)
		}
		var snapshot orgSnapshot
		if err := json.Unmarshal(recorder.Body.Bytes(), &snapshot); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, node := range snapshot.Nodes {
			if node.ID == "agent:mac/worker" && node.CurrentGoal.Value != nil && *node.CurrentGoal.Value == "Read contract passes" {
				found = true
			}
		}
		if !found {
			t.Fatalf("read %d did not project the persisted worker goal", read)
		}
		for table, rows := range orgDatabaseRows(t, database) {
			if before[table] != rows {
				t.Errorf("read %d changed database rows in %s", read, table)
			}
		}
		if pending := readClock.pending(); pending != 0 {
			t.Errorf("read %d queued %d view or notification timers", read, pending)
		}
		readHub.mu.Lock()
		newEvents := slices.Clone(readHub.events[events:])
		modelDelta, startDelta, sendDelta := readHub.modelRequests-modelRequests, len(readHub.starts)-starts, len(readHub.sends)-sends
		readHub.mu.Unlock()
		if modelDelta != 0 || len(newEvents) != 0 || startDelta != 0 || sendDelta != 0 {
			t.Errorf("read %d side effects: model %+d, events %v, starts %+d, sends %+d", read, modelDelta, newEvents, startDelta, sendDelta)
		}
		cold.admission.mu.Lock()
		unchanged := maps.Equal(machineClaims, cold.admission.machineClaims) &&
			maps.Equal(directionClaims, cold.admission.directionClaims) && maps.Equal(groupClaims, cold.admission.groupClaims)
		cold.admission.mu.Unlock()
		if !unchanged {
			t.Errorf("read %d changed admission claims", read)
		}
	}
}
