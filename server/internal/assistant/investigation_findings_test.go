package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

func findingTask(t *testing.T, st Store) (*Service, Todo) {
	t.Helper()
	s, _, todo := findingTaskWithHub(t, st)
	return s, todo
}

func findingTaskWithHub(t *testing.T, st Store) (*Service, *fakeHub, Todo) {
	t.Helper()
	s, f := newQuietServiceOn(t, st)
	holdDigest(s)
	raw := json.RawMessage(`{"tasks":[{"title":"Investigate request problems","prompt":"Find and classify each error.","goal":"A list of findings","machine":"box","cwd":"/code/app","readonly":true,"multi_issue_report":true}]}`)
	if _, err := s.toolTaskPlan(context.Background(), "conv1", raw); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "investigation Agent linked", func() bool { return len(onlyTodo(s).Agents) == 1 })
	todo := onlyTodo(s)
	if todo.Supervision == nil || todo.Supervision.Investigation == nil || todo.Supervision.Investigation.Version != 0 {
		t.Fatalf("explicit plan did not opt in: %+v", todo.Supervision)
	}
	return s, f, todo
}

func reportFindings(t *testing.T, s *Service, todo Todo, findings []investigationFinding) *investigationFindings {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"kind": "done", "text": "Investigation report is ready.", "findings": findings})
	if err != nil {
		t.Fatal(err)
	}
	worker := todo.Agents[0]
	if _, err := s.reportToSupervisor(context.Background(), worker.MachineID, worker.SessionID, raw, time.Now()); err != nil {
		t.Fatal(err)
	}
	return todoNow(s, todo.ID).Supervision.Investigation
}

func confirmTestFindings(s *Service, id string, version uint64, dispositions ...findingDisposition) (string, error) {
	return reviewErr(s, id, map[string]any{
		"verdict": reviewFindings, "findings_review": findingReview{Version: version, Dispositions: dispositions},
	})
}

func setFindingTestCriterion(t *testing.T, s *Service, id, criterion string) {
	t.Helper()
	if _, _, err := s.updateSupervision(context.Background(), id, func(v *Supervision) bool {
		v.DoneWhen = append(v.DoneWhen, criterion)
		return true
	}); err != nil {
		t.Fatal(err)
	}
}

func completeTestFinding(t *testing.T, s *Service, id string, version uint64, finding investigationFinding, evidence string) {
	t.Helper()
	setFindingTestCriterion(t, s, id, finding.Next)
	if _, err := reviewErr(s, id, map[string]any{
		"verdict":         reviewFindings,
		"findings_review": findingReview{Version: version, Dispositions: []findingDisposition{{ID: finding.ID, Done: evidence}}},
		"checks":          []ReviewCheck{{Criterion: finding.Next, Met: true, Evidence: evidence}},
	}); err != nil {
		t.Fatal(err)
	}
}

var (
	investigateRollout = investigationFinding{ID: "rollout", Summary: "External rollout", Next: "Check rollout result", Source: "report#rollout"}
	investigateLogs    = investigationFinding{ID: "logs", Summary: "Missing detailed log", Next: "Diagnose the missing log", Source: "report#logs"}
)

func TestInvestigationFindingsParkAndAcceptRequireEveryDisposition(t *testing.T) {
	ctx := context.Background()
	s, todo := findingTask(t, todoTestDB(t))
	park := waitingPark()
	accept := map[string]any{"verdict": reviewAccepted, "note": "The report is delivered."}
	mustBlock := func(args map[string]any, reason string) {
		t.Helper()
		if _, err := reviewErr(s, todo.ID, args); err == nil || !strings.Contains(err.Error(), reason) {
			t.Fatalf("review should fail with %q, got %v", reason, err)
		}
		got := todoNow(s, todo.ID)
		if got.DoneAt != nil || got.Supervision.State != "" || len(got.Supervision.Reviews) != 0 {
			t.Fatalf("refused review wrote state: %+v", got.Supervision)
		}
	}
	mustBlock(park, "Review the current investigation")
	r := reportFindings(t, s, todo, []investigationFinding{investigateRollout, investigateLogs})
	if again := reportFindings(t, s, todo, []investigationFinding{investigateRollout}); again.Version != r.Version || len(again.Items) != 2 {
		t.Fatalf("replayed subset changed the list: %+v", again)
	}
	mustBlock(park, "Review the current investigation")
	if _, err := confirmTestFindings(s, todo.ID, r.Version+1); err == nil || !strings.Contains(err.Error(), "current version") {
		t.Fatalf("stale review result: %v", err)
	}
	if _, err := confirmTestFindings(s, todo.ID, r.Version, findingDisposition{ID: "rollout", WaitTarget: "design team"}); err != nil {
		t.Fatal(err)
	}
	mustBlock(park, "logs (Missing detailed log)")
	follow, err := s.newTodo(ctx, "Investigate logs", "", "conv1", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := confirmTestFindings(s, todo.ID, r.Version, findingDisposition{ID: "logs", FollowUp: follow.ID}); err == nil {
		t.Fatal("unrelated follow-up was accepted")
	}
	if err := s.superviseTodo(ctx, follow.ID, "conv1", []string{investigateLogs.Next}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := confirmTestFindings(s, todo.ID, r.Version, findingDisposition{ID: "logs", FollowUp: follow.ID}); err != nil {
		t.Fatal(err)
	}
	mustBlock(accept, "rollout (External rollout)")
	if _, err := reviewErr(s, todo.ID, park); err != nil {
		t.Fatal(err)
	}
	if got := todoNow(s, todo.ID); got.Supervision.state(got) != reviewParked || got.DoneAt != nil {
		t.Fatalf("valid park did not stick: %+v", got.Supervision)
	}
	if _, err := confirmTestFindings(s, todo.ID, r.Version, findingDisposition{ID: "rollout", Done: "Done."}); err == nil {
		t.Fatal("free-form text was accepted as completion evidence")
	}
	completeTestFinding(t, s, todo.ID, r.Version, investigateRollout, "Observed the new rollout result.")
	accept["checks"] = completeReviewChecks(todoNow(s, todo.ID))
	if _, err := reviewErr(s, todo.ID, accept); err != nil {
		t.Fatal(err)
	}
	if got := todoNow(s, todo.ID); got.DoneAt == nil {
		t.Fatal("investigation did not close after disposition")
	}
	rows, err := s.supervisionDB().ListTodoSupervision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows {
		if row.TodoID == todo.ID {
			found = true
			got := supervisionFromRow(row).Investigation
			if got == nil || len(got.Items) != 2 || got.Items[1].Disposition.FollowUp != follow.ID {
				t.Fatalf("durable handoff was lost: %+v", got)
			}
		}
	}
	if !found {
		t.Fatal("investigation ledger did not survive a database read")
	}
	if _, err := reviewErr(s, follow.ID, map[string]any{"verdict": reviewCancelled, "note": "Cancel"}); err == nil {
		t.Fatal("cancelling an unresolved receiver concealed the handoff")
	}
	if _, err := reviewErr(s, follow.ID, map[string]any{"verdict": reviewAccepted, "checks": completeReviewChecks(todoNow(s, follow.ID))}); err != nil {
		t.Fatal(err)
	}
	if got := todoNow(s, follow.ID); got.DoneAt == nil {
		t.Fatal("a completed handoff could not close")
	}
}

func TestInvestigationFindingsOwnerPassRemainsAnswerable(t *testing.T) {
	ctx := context.Background()
	s, _, todo := acceptedForUserCheck(t)
	r := reportFindings(t, s, todo, []investigationFinding{investigateLogs})
	ask := askOf(s, todo.ID)
	if ask == nil {
		t.Fatal("no verification request")
	}
	if result, err := s.acceptTry(ctx, todo.ID, ask.MessageID); err == nil || result.taken {
		t.Fatalf("unconfirmed findings were passed: %+v, %v", result, err)
	}
	if current := askOf(s, todo.ID); current == nil || !current.AcceptedAt.IsZero() {
		t.Fatal("blocked Pass consumed the verification request")
	}
	completeTestFinding(t, s, todo.ID, r.Version, investigateLogs, "Verified request log exists.")
	if result, err := s.acceptTry(ctx, todo.ID, ask.MessageID); err != nil || !result.taken {
		t.Fatalf("owner could not Pass after disposition: %+v, %v", result, err)
	}
}

func TestInvestigationFindingsAutomaticParkAndClosure(t *testing.T) {
	ctx := context.Background()
	s, _, todo, c, at := automaticFixture(t, automaticCloseout("deployment rollout-42", "build https://example.org/deployments/rollout-42", nil))
	_, _, err := s.updateSupervision(ctx, todo.ID, func(v *Supervision) bool {
		v.Supervisor = "conv1"
		v.Investigation = &investigationFindings{Version: 1, Confirmed: 1, ReportedAt: time.Now().UTC(),
			Items: []recordedFinding{{investigationFinding: investigateLogs}}}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	todo = todoNow(s, todo.ID)
	if s.parkCloseout(ctx, todo, c, at) {
		t.Fatal("automatic park hid an unresolved finding")
	}
	if closed, err := s.autoDoneTodo(ctx, todo.ID, "complete", at); err == nil || closed {
		t.Fatalf("automatic completion ignored finding: %t %v", closed, err)
	}
	yes := true
	if _, err := s.editTodo(ctx, todo.ID, db.TodoEdit{Done: &yes}); err == nil {
		t.Fatal("manual completion ignored finding")
	}
	setFindingTestCriterion(t, s, todo.ID, investigateLogs.Next)
	evidence := "Found and verified the trace."
	review := &findingReview{Version: 1, Dispositions: []findingDisposition{{ID: "logs", Done: evidence}}}
	checks := []ReviewCheck{{Criterion: investigateLogs.Next, Met: true, Evidence: evidence}}
	if _, err := s.reviewInvestigationFindings(ctx, todo, review, checks, "conv1", func(Todo) bool { return true }); err != nil {
		t.Fatal(err)
	}
	todo = todoNow(s, todo.ID)
	if !s.parkCloseout(ctx, todo, c, at) {
		t.Fatal("resolved investigation could not park on documented external wait")
	}
}

func TestInvestigationFindingsFreshReportAndOwnerDrop(t *testing.T) {
	ctx := context.Background()
	d := todoTestDB(t)
	s, todo := findingTask(t, d)
	first := reportFindings(t, s, todo, []investigationFinding{investigateLogs})
	if _, err := confirmTestFindings(s, todo.ID, first.Version); err != nil {
		t.Fatal(err)
	}
	s.supervisedWorking(ctx, todoNow(s, todo.ID))
	if _, err := confirmTestFindings(s, todo.ID, first.Version); err == nil {
		t.Fatal("closed an earlier report version after new work began")
	}
	next := reportFindings(t, s, todo, []investigationFinding{})
	if next.Version <= first.Version || len(next.Items) != 1 {
		t.Fatalf("new report lost its earlier unresolved finding: %+v", next)
	}
	writeDrop := func(id, source string) {
		t.Helper()
		if _, err := s.insertDecision(ctx, d, db.TodoDecisionRow{
			ID: id, TodoID: todo.ID, Question: "Drop investigation finding logs?", Choice: "Drop",
			Reason: "This sample is no longer used.", Source: source, SourceMachine: "box", SourceSession: "conv1", CreatedAt: decisionNow(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	writeDrop("dec_finding_by_agent", db.DecisionAgent)
	if _, err := confirmTestFindings(s, todo.ID, next.Version, findingDisposition{ID: "logs", Drop: "dec_finding_by_agent"}); err == nil {
		t.Fatal("an agent's decision dropped the finding")
	}
	writeDrop("dec_relayed_as_manual", db.DecisionManual)
	if _, err := confirmTestFindings(s, todo.ID, next.Version, findingDisposition{ID: "logs", Drop: "dec_relayed_as_manual"}); err == nil {
		t.Fatal("a model-attributed manual record dropped the finding")
	}
	request := httptest.NewRequest("POST", "/api/v1/todos/"+todo.ID+"/decisions",
		strings.NewReader(`{"question":"Drop investigation finding logs?","choice":"Drop","reason":"This sample is no longer used."}`))
	response := httptest.NewRecorder()
	s.ServeHTTP(response, request)
	var owner struct {
		Decision db.TodoDecisionRow `json:"decision"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &owner) != nil || owner.Decision.ID == "" {
		t.Fatalf("owner manual decision was not recorded: %d %s", response.Code, response.Body)
	}
	if _, err := confirmTestFindings(s, todo.ID, next.Version, findingDisposition{ID: "logs", Drop: owner.Decision.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := reviewErr(s, todo.ID, map[string]any{"verdict": reviewAccepted, "note": "Report accepted."}); err != nil {
		t.Fatal(err)
	}
}

func TestInvestigationFindingsExplicitEmptyAndLegacy(t *testing.T) {
	s, todo := findingTask(t, nil)
	r := reportFindings(t, s, todo, []investigationFinding{})
	if _, err := confirmTestFindings(s, todo.ID, r.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := reviewErr(s, todo.ID, waitingPark()); err != nil {
		t.Fatal(err)
	}
	legacy, _, old := newSupervisedService(t)
	if old.Supervision.Investigation != nil {
		t.Fatal("ordinary task unexpectedly requires an investigation report")
	}
	if _, err := reviewErr(legacy, old.ID, waitingPark()); err != nil {
		t.Fatal(err)
	}
}

func TestInvestigationFindingsStaleDeadWorkerRecoveryAndLiveSafety(t *testing.T) {
	ctx := context.Background()

	t.Run("orphaned_stale_findings_allow_review_and_brief_accept_422_then_200", func(t *testing.T) {
		s, _, todo := acceptedForUserCheck(t)
		r := reportFindings(t, s, todo, []investigationFinding{investigateLogs})
		s.supervisedWorking(ctx, todoNow(s, todo.ID))
		if got := todoNow(s, todo.ID).Supervision.Investigation; !got.Stale {
			t.Fatal("expected findings to be marked stale")
		}
		ask := askOf(s, todo.ID)
		if ask == nil {
			t.Fatal("missing user_check acceptance ask")
		}
		// Before orphan watch, worker is not known gone, so HTTP Pass must still fail with 422 (never 502).
		reqBody := `{"asked_at":"` + ask.AskedAt.UTC().Format(timeKeyLayout) + `"}`
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/todos/"+todo.ID+"/accept", strings.NewReader(reqBody)))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("HTTP /accept with pending findings = %d %s, want 422", rec.Code, rec.Body.String())
		}
		var errBody map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
			t.Fatalf("decode 422 body: %v", err)
		}
		if errBody["code"] != "investigation_findings_pending" || errBody["delivered"] != "none" {
			t.Fatalf("unexpected 422 body: %+v", errBody)
		}

		// Once orphanWatch marks the unlisted agent as orphanClosed, the supervisor can review and dispose of stale findings.
		s.orphanWatch(ctx, time.Now().Add(orphanReconnectWait+time.Minute))
		if o, ok := s.orphanOf(todo.ID); !ok || o.Reason != orphanClosed {
			t.Fatalf("expected orphanClosed, got %+v ok=%v", o, ok)
		}
		completeTestFinding(t, s, todo.ID, r.Version, investigateLogs, "Verified follow-up coverage.")
		got := todoNow(s, todo.ID).Supervision.Investigation
		if got.Stale || got.Confirmed != r.Version {
			t.Fatalf("stale review did not restore current confirmed findings: %+v", got)
		}

		rec2 := httptest.NewRecorder()
		s.ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, "/api/v1/todos/"+todo.ID+"/accept", strings.NewReader(reqBody)))
		if rec2.Code != http.StatusOK {
			t.Fatalf("HTTP /accept after stale findings disposition = %d %s, want 200", rec2.Code, rec2.Body.String())
		}
		if todoNow(s, todo.ID).DoneAt == nil {
			t.Fatal("to-do did not close after owner Pass")
		}
	})

	t.Run("stopped_worker_allows_stale_cancel_while_handoff_receiver_stays_protected", func(t *testing.T) {
		s, f, todo := findingTaskWithHub(t, todoTestDB(t))
		r := reportFindings(t, s, todo, []investigationFinding{investigateLogs})
		follow, err := s.newTodo(ctx, "Follow up logs", "", "conv1", "", false)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.superviseTodo(ctx, follow.ID, "conv1", []string{investigateLogs.Next}, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := confirmTestFindings(s, todo.ID, r.Version, findingDisposition{ID: "logs", FollowUp: follow.ID}); err != nil {
			t.Fatal(err)
		}
		s.supervisedWorking(ctx, todoNow(s, todo.ID))
		// Mark the worker archived so todoAgentStopped confirms no live worker remains.
		worker := todoNow(s, todo.ID).Agents[0]
		f.mu.Lock()
		f.sessions[worker.MachineID] = []proto.Session{{SessionID: worker.SessionID, Archived: true}}
		f.mu.Unlock()

		if _, err := reviewErr(s, todo.ID, map[string]any{"verdict": reviewCancelled, "note": "Findings already tracked in follow-up"}); err != nil {
			t.Fatalf("cancelling stale investigation with dead worker failed: %v", err)
		}
		closed := todoNow(s, todo.ID)
		if closed.DoneAt == nil || closed.DoneBy != db.DoneByCancelled {
			t.Fatalf("expected cancelled closed todo, got done_at=%v done_by=%q", closed.DoneAt, closed.DoneBy)
		}
		// Handoff receiver still cannot be cancelled without completion evidence.
		if _, err := reviewErr(s, follow.ID, map[string]any{"verdict": reviewCancelled, "note": "Drop receiver"}); err == nil {
			t.Fatal("cancelling unresolved handoff receiver unexpectedly succeeded")
		}
	})

	t.Run("live_worker_keeps_stale_findings_and_cancel_blocked", func(t *testing.T) {
		s, f, todo := findingTaskWithHub(t, todoTestDB(t))
		r := reportFindings(t, s, todo, []investigationFinding{investigateLogs})
		s.supervisedWorking(ctx, todoNow(s, todo.ID))
		worker := todoNow(s, todo.ID).Agents[0]
		f.mu.Lock()
		f.sessions[worker.MachineID] = []proto.Session{{SessionID: worker.SessionID, Status: proto.StatusWorking}}
		f.mu.Unlock()

		if _, err := confirmTestFindings(s, todo.ID, r.Version); err == nil {
			t.Fatal("live worker must block confirming stale findings")
		}
		if _, err := reviewErr(s, todo.ID, map[string]any{"verdict": reviewCancelled, "note": "Cancel while live"}); err == nil {
			t.Fatal("live worker must block cancelling stale investigation")
		}
		if _, err := reviewErr(s, todo.ID, map[string]any{"verdict": reviewAccepted, "note": "Accept while live"}); err == nil {
			t.Fatal("live worker must block accepting stale investigation")
		}
		yes := true
		if _, err := s.editTodo(ctx, todo.ID, db.TodoEdit{Done: &yes}); err == nil {
			t.Fatal("live worker must block manual completion on stale investigation")
		}
	})
}
