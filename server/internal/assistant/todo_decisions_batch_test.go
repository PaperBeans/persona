package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func batchDecisionsRequest(s *Service, role, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/todos/decisions/batch", strings.NewReader(body))
	if role != "" {
		r.Header.Set(RoleHeader, role)
	}
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

// newBatchDecisionEnv creates a service with n supervised tasks, each linked to its own active agent.
func newBatchDecisionEnv(t *testing.T, n int) (*Service, *fakeHub, []Todo) {
	t.Helper()
	st := todoTestDB(t)
	s, f := newQuietServiceOn(t, st)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	f.judge = `{"verdict":"done","reason":"The example change is reviewed."}`

	tasks := make([]map[string]any, 0, n)
	for i := range n {
		tasks = append(tasks, map[string]any{
			"title":          fmt.Sprintf("Task %d", i+1),
			"prompt":         fmt.Sprintf("Implement task %d and add tests.", i+1),
			"machine":        "box",
			"cwd":            fmt.Sprintf("/code/app-%d", i+1),
			"readonly":       true,
			"why":            "The example project is on this computer.",
			"done_when":      []string{"Tests pass"},
			"expect_minutes": 30,
		})
	}
	args, err := json.Marshal(map[string]any{"tasks": tasks})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.toolTaskPlan(context.Background(), "conv1", args); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "all task agent links", func() bool {
		s.todos.mu.RLock()
		defer s.todos.mu.RUnlock()
		linked := 0
		for _, td := range s.todos.todos {
			if len(td.Agents) == 1 {
				linked++
			}
		}
		return linked == n
	})
	var todos []Todo
	s.todos.mu.RLock()
	for _, td := range s.todos.todos {
		if len(td.Agents) == 1 {
			todos = append(todos, *td)
		}
	}
	s.todos.mu.RUnlock()
	if len(todos) != n {
		t.Fatalf("got %d linked todos, want %d", len(todos), n)
	}
	return s, f, todos
}

func setAllBatchSessions(f *fakeHub, todos []Todo, activeSession, status, text string) {
	sessions := make([]proto.Session, 0, len(todos))
	for _, td := range todos {
		if len(td.Agents) == 0 {
			continue
		}
		ag := td.Agents[0]
		sStatus, sState, lastText := proto.StatusIdle, proto.StateUnread, ""
		if ag.SessionID == activeSession {
			sStatus, lastText = status, text
			if status == proto.StatusWorking {
				sState = proto.StateWorking
			}
		}
		sessions = append(sessions, proto.Session{
			SessionID: ag.SessionID,
			MachineID: ag.MachineID,
			Cwd:       ag.Cwd,
			Agent:     "claude",
			Status:    sStatus,
			State:     sState,
			LastText:  lastText,
			UpdatedAt: time.Now(),
		})
	}
	f.setSessions("box", sessions...)
}

func raiseDecision(t *testing.T, s *Service, f *fakeHub, todo Todo, allTodos []Todo, decision map[string]any) {
	t.Helper()
	ctx := context.Background()
	sid := todo.Agents[0].SessionID
	f.judge = `{"verdict":"done","reason":"Ready for a decision."}`
	setAllBatchSessions(f, allTodos, sid, proto.StatusWorking, "")
	s.taskWorking(ctx, "box", sid, startRow{})
	setAllBatchSessions(f, allTodos, sid, proto.StatusDone, "Ready for a decision.")
	s.taskTurnEnded(ctx, "box", sid)
	waitFor(t, "the review", func() bool { return todoNow(s, todo.ID).Supervision.State == reviewPending })
	if _, err := reviewErr(s, todo.ID, decision); err != nil {
		t.Fatalf("reviewErr(%s): %v", todo.ID, err)
	}
	a, err := s.askRound(todo.ID, "")
	if err != nil || a == nil {
		t.Fatalf("expected pending decision on %s, got ask=%v err=%v", todo.ID, a, err)
	}
}

func releaseDecision() map[string]any {
	return map[string]any{
		"verdict":   "needs_user",
		"why_owner": "priority",
		"reason":    "When should we ship?",
		"recommend": "Ship Friday",
		"options": []map[string]string{
			{"label": "Ship Friday", "leads_to": "Goes out before the weekend"},
			{"label": "Wait for Monday", "leads_to": "Safer monitoring window"},
		},
		"cost_of_waiting": "Release train slips by three days",
	}
}

func TestTodoDecisionsBatchDistinctOptions(t *testing.T) {
	ctx := context.Background()
	s, f, todos := newBatchDecisionEnv(t, 2)
	tdA, tdB := todos[0], todos[1]

	raiseDecision(t, s, f, tdA, todos, ryokanDecision())
	raiseDecision(t, s, f, tdB, todos, releaseDecision())

	sentBefore := len(sentToAgent(f))
	body, _ := json.Marshal(batchDecisionRequest{
		Items: []batchDecisionItem{
			{TodoID: tdA.ID, Option: "Ryokan Hanami"},   // recommended option
			{TodoID: tdB.ID, Option: "Wait for Monday"}, // non-recommended option
		},
	})
	w := batchDecisionsRequest(s, "full", string(body))
	if w.Code != http.StatusOK {
		t.Fatalf("batch distinct options: got %d, want 200: %s", w.Code, w.Body.String())
	}
	var resp batchDecisionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Succeeded != 2 || resp.Failed != 0 || len(resp.Results) != 2 {
		t.Fatalf("unexpected summary: %+v", resp)
	}
	if !resp.Results[0].OK || resp.Results[0].TodoID != tdA.ID || !resp.Results[1].OK || resp.Results[1].TodoID != tdB.ID {
		t.Fatalf("unexpected results: %+v", resp.Results)
	}

	// Both decisions must be recorded and relayed to their respective Agents, and neither ask still waits.
	sent := sentToAgent(f)[sentBefore:]
	if len(sent) != 2 {
		t.Fatalf("expected 2 agent messages, got %d: %q", len(sent), sent)
	}
	st := s.todoDB()
	decsA, err := st.ListTodoDecisions(ctx, tdA.ID, false)
	if err != nil || len(decsA) != 1 || decsA[0].Choice != "Ryokan Hanami" {
		t.Fatalf("tdA decisions: %+v err=%v", decsA, err)
	}
	decsB, err := st.ListTodoDecisions(ctx, tdB.ID, false)
	if err != nil || len(decsB) != 1 || decsB[0].Choice != "Wait for Monday" {
		t.Fatalf("tdB decisions: %+v err=%v", decsB, err)
	}
	for _, id := range []string{tdA.ID, tdB.ID} {
		if a, err := s.askRound(id, ""); err != nil || a != nil {
			t.Fatalf("todo %s still waiting after batch decision: ask=%v err=%v", id, a, err)
		}
	}
}

func TestTodoDecisionsBatchSharedCustomReply(t *testing.T) {
	ctx := context.Background()
	s, f, todos := newBatchDecisionEnv(t, 2)
	tdA, tdB := todos[0], todos[1]

	raiseDecision(t, s, f, tdA, todos, ryokanDecision())
	raiseDecision(t, s, f, tdB, todos, releaseDecision())

	const sharedReply = "Hold off until next week's planning sync."
	sentBefore := len(sentToAgent(f))
	body, _ := json.Marshal(batchDecisionRequest{
		Items: []batchDecisionItem{
			{TodoID: tdA.ID, Reply: sharedReply},
			{TodoID: tdB.ID, Reply: sharedReply},
		},
	})
	w := batchDecisionsRequest(s, "full", string(body))
	if w.Code != http.StatusOK {
		t.Fatalf("batch shared reply: got %d, want 200: %s", w.Code, w.Body.String())
	}
	var resp batchDecisionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Succeeded != 2 || resp.Failed != 0 {
		t.Fatalf("unexpected summary: %+v", resp)
	}

	sent := sentToAgent(f)[sentBefore:]
	if len(sent) != 2 {
		t.Fatalf("expected 2 agent messages, got %d: %q", len(sent), sent)
	}
	st := s.todoDB()
	for _, id := range []string{tdA.ID, tdB.ID} {
		decs, err := st.ListTodoDecisions(ctx, id, false)
		if err != nil || len(decs) != 1 || decs[0].Choice != sharedReply {
			t.Fatalf("todo %s decisions: %+v err=%v", id, decs, err)
		}
		if a, err := s.askRound(id, ""); err != nil || a != nil {
			t.Fatalf("todo %s still waiting: ask=%v err=%v", id, a, err)
		}
	}
}

func TestTodoDecisionsBatchPartialSuccess(t *testing.T) {
	ctx := context.Background()
	s, f, todos := newBatchDecisionEnv(t, 4)
	validTodo := todos[0]
	noPendingTodo := todos[1]
	closedTodo := todos[2]
	badOptionTodo := todos[3]

	raiseDecision(t, s, f, validTodo, todos, ryokanDecision())
	raiseDecision(t, s, f, badOptionTodo, todos, releaseDecision())

	// Mark closedTodo as done.
	if _, err := s.editTodo(ctx, closedTodo.ID, todoDoneEdit(true)); err != nil {
		t.Fatal(err)
	}

	sentBefore := len(sentToAgent(f))
	body, _ := json.Marshal(batchDecisionRequest{
		Items: []batchDecisionItem{
			{TodoID: "todo_missing", Option: "Ryokan Hanami"},
			{TodoID: validTodo.ID, Option: "Kaede"},
			{TodoID: noPendingTodo.ID, Reply: "Proceed anyway"},
			{TodoID: closedTodo.ID, Reply: "Reopen?"},
			{TodoID: badOptionTodo.ID, Option: "Nonexistent Option"},
		},
	})
	w := batchDecisionsRequest(s, "full", string(body))
	if w.Code != http.StatusOK {
		t.Fatalf("partial success: got %d, want 200: %s", w.Code, w.Body.String())
	}
	var resp batchDecisionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Succeeded != 1 || resp.Failed != 4 || len(resp.Results) != 5 {
		t.Fatalf("unexpected summary: %+v", resp)
	}

	// Verify order and per-item status codes.
	want := []struct {
		id     string
		ok     bool
		status int
	}{
		{"todo_missing", false, http.StatusNotFound},
		{validTodo.ID, true, 0},
		{noPendingTodo.ID, false, http.StatusConflict},
		{closedTodo.ID, false, http.StatusConflict},
		{badOptionTodo.ID, false, http.StatusBadRequest},
	}
	for i, exp := range want {
		got := resp.Results[i]
		if got.TodoID != exp.id || got.OK != exp.ok || got.Status != exp.status {
			t.Errorf("results[%d] = %+v, want id=%q ok=%v status=%d", i, got, exp.id, exp.ok, exp.status)
		}
		if !exp.ok && got.Error == "" {
			t.Errorf("results[%d] failed without error message: %+v", i, got)
		}
	}

	// Valid item was delivered and recorded; badOptionTodo's pending decision remains open.
	if gotSent := len(sentToAgent(f)) - sentBefore; gotSent != 1 {
		t.Fatalf("expected 1 agent message for the valid item, got %d", gotSent)
	}
	if a, err := s.askRound(validTodo.ID, ""); err != nil || a != nil {
		t.Fatalf("validTodo still waiting: ask=%v err=%v", a, err)
	}
	if a, err := s.askRound(badOptionTodo.ID, ""); err != nil || a == nil {
		t.Fatalf("badOptionTodo should still be waiting after invalid option: ask=%v err=%v", a, err)
	}
}

func TestTodoDecisionsBatchValidationAndPermissions(t *testing.T) {
	s, f, todos := newBatchDecisionEnv(t, 1)
	td := todos[0]
	raiseDecision(t, s, f, td, todos, ryokanDecision())

	// Non-full roles must be rejected with 403.
	for _, role := range []string{"read", "watch", "answer"} {
		w := batchDecisionsRequest(s, role, fmt.Sprintf(`{"items":[{"todo_id":%q,"option":"Kaede"}]}`, td.ID))
		if w.Code != http.StatusForbidden {
			t.Fatalf("role %q: got %d, want 403: %s", role, w.Code, w.Body.String())
		}
	}

	// Build a >50 items payload.
	tooMany := make([]batchDecisionItem, maxBatchDecisionItems+1)
	for i := range tooMany {
		tooMany[i] = batchDecisionItem{TodoID: fmt.Sprintf("todo_%03d", i), Option: "Yes"}
	}
	rawTooMany, _ := json.Marshal(batchDecisionRequest{Items: tooMany})
	longReply, _ := json.Marshal(batchDecisionRequest{
		Items: []batchDecisionItem{{TodoID: td.ID, Reply: strings.Repeat("a", decisionTextMax+1)}},
	})

	badCases := []struct {
		name string
		body string
	}{
		{"invalid json", `{not-json`},
		{"empty items", `{"items":[]}`},
		{"too many items", string(rawTooMany)},
		{"empty todo_id", `{"items":[{"todo_id":"   ","option":"Kaede"}]}`},
		{"duplicate todo_id", fmt.Sprintf(`{"items":[{"todo_id":%q,"option":"Kaede"},{"todo_id":%q,"option":"Tsuki"}]}`, td.ID, td.ID)},
		{"blank option and reply (must never auto-pick)", fmt.Sprintf(`{"items":[{"todo_id":%q,"option":"  ","reply":""}]}`, td.ID)},
		{"both option and reply set", fmt.Sprintf(`{"items":[{"todo_id":%q,"option":"Kaede","reply":"custom"}]}`, td.ID)},
		{"reply exceeds max length", string(longReply)},
	}
	for _, tc := range badCases {
		t.Run(tc.name, func(t *testing.T) {
			w := batchDecisionsRequest(s, "full", tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("%s: got %d, want 400: %s", tc.name, w.Code, w.Body.String())
			}
		})
	}

	// Pending decision must remain untouched after all rejected requests.
	if a, err := s.askRound(td.ID, ""); err != nil || a == nil {
		t.Fatalf("decision should still be pending after 400/403 rejections: ask=%v err=%v", a, err)
	}
}

func TestTodoDecisionsBatchSingleEndpointZeroRegression(t *testing.T) {
	ctx := context.Background()
	s, f, todos := newBatchDecisionEnv(t, 1)
	td := todos[0]
	raiseDecision(t, s, f, td, todos, ryokanDecision())

	round := viewRound(t, s, td.ID, askDecisionPrefix)
	sentBefore := len(sentToAgent(f))
	rec := answerTodoAsk(s, td.ID, askDecisionPrefix+round, `{"option":"Tsuki"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("single ask answer: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if len(sentToAgent(f)) != sentBefore+1 {
		t.Fatalf("expected 1 agent message from single ask answer, got %d", len(sentToAgent(f))-sentBefore)
	}
	decs, err := s.todoDB().ListTodoDecisions(ctx, td.ID, false)
	if err != nil || len(decs) != 1 || decs[0].Choice != "Tsuki" {
		t.Fatalf("single decision record mismatch: %+v err=%v", decs, err)
	}
}
