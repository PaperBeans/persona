package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type statusForge struct {
	fakeForge
	status      TodoPRStatus
	statusErr   error
	statusCalls int
}

func (f *statusForge) PRStatus(context.Context, string, int) (TodoPRStatus, error) {
	f.statusCalls++
	return f.status, f.statusErr
}

func TestPRStatusReadsForgeBlockers(t *testing.T) {
	for _, tc := range []struct {
		name, pr, reviews, statuses, runs string
		want                              TodoPRStatus
	}{
		{
			name:    "ready with review, checks and conflicts",
			pr:      `{"state":"open","draft":false,"mergeable_state":"dirty","head":{"sha":"head"},"requested_reviewers":[{}]}`,
			reviews: `[]`, statuses: `{"state":"success","total_count":1}`,
			runs: `{"total_count":2,"check_runs":[{"status":"completed","conclusion":"failure"},{"status":"in_progress"}]}`,
			want: TodoPRStatus{State: "open", Review: "required", Checks: "failure", Conflicts: true},
		},
		{
			name:     "latest review per reviewer",
			pr:       `{"state":"open","head":{"sha":"head"}}`,
			reviews:  `[{"user":{"login":"reviewer"},"state":"CHANGES_REQUESTED"},{"user":{"login":"reviewer"},"state":"APPROVED"}]`,
			statuses: `{"state":"pending","total_count":0}`,
			runs:     `{"total_count":1,"check_runs":[{"status":"completed","conclusion":"neutral"}]}`,
			want:     TodoPRStatus{State: "open", Review: "approved", Checks: "success"},
		},
		{
			name:     "draft with requested changes",
			pr:       `{"state":"open","draft":true,"head":{"sha":"head"}}`,
			reviews:  `[{"user":{"login":"reviewer"},"state":"CHANGES_REQUESTED"}]`,
			statuses: `{"state":"pending","total_count":1}`, runs: `{"check_runs":[]}`,
			want: TodoPRStatus{State: "open", Draft: true, Review: "changes_requested", Checks: "pending"},
		},
		{
			name: "merged needs no commit endpoints", pr: `{"state":"closed","merged":true}`,
			want: TodoPRStatus{State: "merged", Review: "pending", Checks: "none"},
		},
		{
			name: "closed without merge", pr: `{"state":"closed","merged":false}`,
			want: TodoPRStatus{State: "closed", Review: "pending", Checks: "none"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body string
				switch r.URL.RequestURI() {
				case "/repos/example/app/pulls/12":
					body = tc.pr
				case "/repos/example/app/pulls/12/reviews?per_page=100":
					body = tc.reviews
				case "/repos/example/app/commits/head/status":
					body = tc.statuses
				case "/repos/example/app/commits/head/check-runs?per_page=100&filter=latest":
					body = tc.runs
				default:
					t.Errorf("unexpected request: %s", r.URL)
					http.Error(w, "unexpected", http.StatusNotFound)
					return
				}
				fmt.Fprint(w, body)
			}))
			defer srv.Close()
			got, err := (githubForge{base: srv.URL, client: srv.Client()}).PRStatus(context.Background(), "example/app", 12)
			if err != nil || got != tc.want {
				t.Fatalf("PR status = %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}

func TestPRStatusPollReturnsTerminalPRToSupervision(t *testing.T) {
	for _, terminal := range []string{"merged", "closed"} {
		t.Run(terminal, func(t *testing.T) {
			s, _, task := newSupervisedService(t)
			ctx, now := context.Background(), time.Now()
			url := "https://github.com/example/app/pull/12"
			s.setTaskStage(ctx, task.ID, "review", url)
			if _, err := reviewErr(s, task.ID, mergePark(url)); err != nil {
				t.Fatal(err)
			}
			f := &statusForge{status: TodoPRStatus{State: "open", Checks: "success", Review: "required"}}
			s.deploy.forge = f
			s.sweepPRStatuses(ctx, now)
			got := todoNow(s, task.ID)
			if got.DoneAt != nil || got.Supervision.State != reviewParked || got.PRStatus == nil || got.PRStatus.State != "open" {
				t.Fatalf("ready PR lost its open parked task: %+v", got)
			}
			s.sweepPRStatuses(ctx, now.Add(time.Minute))
			if f.statusCalls != 1 {
				t.Fatalf("polled a fresh observation %d times", f.statusCalls)
			}
			f.status.State = terminal
			s.sweepPRStatuses(ctx, now.Add(deployRecheck))
			got = todoNow(s, task.ID)
			if got.DoneAt != nil || got.PRStatus.State != terminal || got.Supervision.State != reviewPending || got.Supervision.Parked != nil {
				t.Fatalf("terminal PR did not return to review, or auto-closed: %+v, %+v", got, got.Supervision)
			}
			if terminal == "merged" && got.Stage != "merge" {
				t.Fatalf("merged PR stage = %q", got.Stage)
			}
			if terminal == "closed" && got.Stage != "review" {
				t.Fatalf("closed PR advanced as if merged: %q", got.Stage)
			}
			b := &s.supervise
			b.mu.Lock()
			found := false
			for _, signal := range b.pending {
				found = found || signal.Todo == task.ID && signal.Kind == sigReview
			}
			b.mu.Unlock()
			if !found {
				t.Fatal("no supervisor wake was queued")
			}
			raw, _ := json.Marshal(got)
			if !strings.Contains(string(raw), `"pr_status"`) {
				t.Fatal("PR facts not published on Todo")
			}
		})
	}
}

func TestPRStatusFailurePreservesLastObservation(t *testing.T) {
	s, _, task := newSupervisedService(t)
	ctx, now := context.Background(), time.Now()
	s.setTaskStage(ctx, task.ID, "review", "https://github.com/example/app/pull/12")
	f := &statusForge{status: TodoPRStatus{State: "open", Checks: "failure"}}
	s.deploy.forge = f
	s.sweepPRStatuses(ctx, now)
	before := *todoNow(s, task.ID).PRStatus
	f.statusErr = errors.New("forge unavailable")
	s.sweepPRStatuses(ctx, now.Add(deployRecheck))
	got := todoNow(s, task.ID)
	if got.PRStatus == nil || *got.PRStatus != before || got.Supervision.State == reviewPending {
		t.Fatalf("failure replaced facts or triggered acceptance: %+v", got)
	}
	s.sweepPRStatuses(ctx, now.Add(deployRecheck+time.Minute))
	if f.statusCalls != 2 {
		t.Fatalf("an outage bypassed the polling interval: %d calls", f.statusCalls)
	}
}

func TestPRStatusDoesNotWakeAnUnrelatedLaterPark(t *testing.T) {
	s, _, task := newSupervisedService(t)
	ctx, now := context.Background(), time.Now()
	s.setTaskStage(ctx, task.ID, "deploy", "https://github.com/example/app/pull/12")
	if _, err := reviewErr(s, task.ID, waitingPark()); err != nil {
		t.Fatal(err)
	}
	f := &statusForge{status: TodoPRStatus{State: "merged"}}
	s.deploy.forge = f
	s.sweepPRStatuses(ctx, now)
	if got := todoNow(s, task.ID); got.Supervision.State != reviewParked || got.DoneAt != nil {
		t.Fatalf("an already merged task's unrelated park was disturbed: %+v", got)
	}
}

func TestPRStatusPassDoesNotNeedRunningHubCommit(t *testing.T) {
	s, _, task := newSupervisedService(t)
	s.setTaskStage(context.Background(), task.ID, "review", "https://github.com/example/app/pull/12")
	f := &statusForge{status: TodoPRStatus{State: "open", Checks: "success"}}
	s.deploy.forge = f
	s.deploy.hubBuild = func(context.Context) (string, string, error) {
		return "", "", errors.New("hub build unavailable")
	}
	s.sweepDeploys(time.Now())
	if got := todoNow(s, task.ID); got.PRStatus == nil || got.PRStatus.State != "open" || f.statusCalls != 1 {
		t.Fatalf("PR observations depend on the hub's repository/build: calls=%d, facts=%+v", f.statusCalls, got.PRStatus)
	}
}

func TestPRStatusIgnoresAReplacedPR(t *testing.T) {
	s, _, task := newSupervisedService(t)
	ctx := context.Background()
	oldURL, newURL := "https://github.com/example/app/pull/12", "https://github.com/example/app/pull/13"
	s.setTaskStage(ctx, task.ID, "review", oldURL)
	old := todoNow(s, task.ID)
	if _, err := reviewErr(s, task.ID, mergePark(oldURL)); err != nil {
		t.Fatal(err)
	}
	s.setTaskStage(ctx, task.ID, "review", newURL)
	if err := s.applyPRStatus(ctx, old, TodoPRStatus{URL: oldURL, State: "merged", CheckedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if got := todoNow(s, task.ID); got.PRStatus != nil || got.Supervision.State != reviewParked || got.PRURL != newURL {
		t.Fatalf("a stale response changed the replacement PR's task: %+v", got)
	}
}

// mergePark parks a task on the maintainers merging the PR at url, naming it as a supervisor does.
func mergePark(url string) map[string]any {
	return map[string]any{"verdict": "parked", "reason": "Waiting for the maintainers to review and merge " + url,
		"waiting_on": "someone_else", "until": "3d"}
}

func TestParkWaitsOnPR(t *testing.T) {
	url := "https://github.com/example/app/pull/12"
	for reason, want := range map[string]bool{
		"Waiting for the maintainers to merge " + url:                   true,
		"Waiting for review of https://github.com/Example/App/pull/12":  true,
		"PR #12 waits for the maintainer":                               true,
		"The design team has to pick the icon set first":                false,
		"Waiting for https://github.com/example/app/pull/120 to merge":  false,
		"Waiting for https://github.com/example/other/pull/12 to merge": false,
		"Release 2.1 has to ship first (#120)":                          false,
	} {
		if got := parkWaitsOnPR(&TaskReview{Verdict: reviewParked, WaitingOn: parkSomeoneElse, Reason: reason}, url); got != want {
			t.Errorf("%q: %v, want %v", reason, got, want)
		}
	}
	if parkWaitsOnPR(&TaskReview{Verdict: reviewParked, WaitingOn: parkDate, Reason: "PR #12"}, url) {
		t.Error("a date park waits on the PR")
	}
}

// A park for something else, made before the PR was seen open or after, is not the PR's merge wait: the PR merging
// or closing leaves it parked and queues no review (#7614 review).
func TestPRStatusTerminalPRLeavesAnUnrelatedParkAlone(t *testing.T) {
	for _, when := range []string{"before", "after"} {
		t.Run(when, func(t *testing.T) {
			s, _, task := newSupervisedService(t)
			ctx, now := context.Background(), time.Now()
			s.setTaskStage(ctx, task.ID, "review", "https://github.com/example/app/pull/12")
			park := func() {
				if _, err := reviewErr(s, task.ID, waitingPark()); err != nil {
					t.Fatal(err)
				}
			}
			if when == "before" {
				park()
			}
			f := &statusForge{status: TodoPRStatus{State: "open", Checks: "success"}}
			s.deploy.forge = f
			s.sweepPRStatuses(ctx, now)
			if when == "after" {
				park()
			}
			f.status.State = "merged"
			s.sweepPRStatuses(ctx, now.Add(deployRecheck))
			got := todoNow(s, task.ID)
			if got.PRStatus == nil || got.PRStatus.State != "merged" {
				t.Fatalf("merged PR facts not kept: %+v", got.PRStatus)
			}
			if got.Supervision.State != reviewParked || pendingReviewSignal(s, task.ID) {
				t.Fatalf("a merged PR woke a park for something else: %+v", got.Supervision)
			}
		})
	}
}

// A merged observation moves the stage only while the open to-do and its stage still name that PR: one replaced, or a
// to-do closed, between the facts being kept and the stage update stays where it is (#7614 review).
func TestPRStatusMergedStageNeedsTheSamePR(t *testing.T) {
	s, _, task := newSupervisedService(t)
	ctx := context.Background()
	oldURL, newURL := "https://github.com/example/app/pull/12", "https://github.com/example/app/pull/13"
	s.setTaskStage(ctx, task.ID, "review", oldURL)
	s.setTaskStage(ctx, task.ID, "review", newURL) // the replacement lands after the old PR's facts were applied
	s.setTaskStageOn(ctx, task.ID, "merge", "", "", false, oldURL)
	if got := todoNow(s, task.ID); got.Stage != "review" || got.PRURL != newURL {
		t.Fatalf("a replaced PR's merge moved the stage: %q (%s)", got.Stage, got.PRURL)
	}
	s.setTaskStageOn(ctx, task.ID, "merge", "", "", false, newURL)
	if got := todoNow(s, task.ID); got.Stage != "merge" {
		t.Fatalf("the current PR's merge did not move the stage: %q", got.Stage)
	}
}

// pendingReviewSignal says whether a supervisor wake for task id's review is queued.
func pendingReviewSignal(s *Service, id string) bool {
	b := &s.supervise
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, signal := range b.pending {
		if signal.Todo == id && signal.Kind == sigReview {
			return true
		}
	}
	return false
}

// A terminal PR wakes only a park waiting on an outside event or someone else: a task at work (no review yet) keeps
// its state, and no review is queued (#7614).
func TestPRStatusTerminalPRLeavesAnUnparkedTaskAlone(t *testing.T) {
	s, _, task := newSupervisedService(t)
	ctx, now := context.Background(), time.Now()
	s.setTaskStage(ctx, task.ID, "review", "https://github.com/example/app/pull/12")
	f := &statusForge{status: TodoPRStatus{State: "open", Checks: "success"}}
	s.deploy.forge = f
	s.sweepPRStatuses(ctx, now)
	before := todoNow(s, task.ID).Supervision.State
	f.status.State = "merged"
	s.sweepPRStatuses(ctx, now.Add(deployRecheck))
	got := todoNow(s, task.ID)
	if got.PRStatus == nil || got.PRStatus.State != "merged" {
		t.Fatalf("merged PR facts not kept: %+v", got.PRStatus)
	}
	if got.Supervision.State != before || got.Supervision.State == reviewPending || pendingReviewSignal(s, task.ID) {
		t.Fatalf("a merged PR changed an unparked task's supervision %q -> %q", before, got.Supervision.State)
	}
}

// The stage's PR is checked before the supervision: an observation of a PR replaced while it was read (the to-do
// still names the old one, the stage the new) neither wakes the park nor keeps its facts (#7614).
func TestPRStatusReplacedInFlightDoesNotWakeTheSupervisor(t *testing.T) {
	s, _, task := newSupervisedService(t)
	ctx := context.Background()
	oldURL, newURL := "https://github.com/example/app/pull/12", "https://github.com/example/app/pull/13"
	s.setTaskStage(ctx, task.ID, "review", oldURL)
	if _, err := reviewErr(s, task.ID, mergePark(oldURL)); err != nil {
		t.Fatal(err)
	}
	old := todoNow(s, task.ID)
	b := &s.reports
	b.mu.Lock()
	st := b.stages[task.ID]
	st.PR = newURL // the replacement landed in the stages; the to-do is published next
	b.stages[task.ID] = st
	b.mu.Unlock()
	if err := s.applyPRStatus(ctx, old, TodoPRStatus{URL: oldURL, State: "merged", CheckedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if got := todoNow(s, task.ID); got.Supervision.State != reviewParked || pendingReviewSignal(s, task.ID) {
		t.Fatalf("a replaced PR's observation woke the supervisor: %+v", got.Supervision)
	}
}

type hangingStatusForge struct {
	fakeForge
	called func()
}

func (f *hangingStatusForge) PRStatus(ctx context.Context, _ string, _ int) (TodoPRStatus, error) {
	f.called()
	<-ctx.Done()
	return TodoPRStatus{}, ctx.Err()
}

// PR observations come after the deploy checks, on a deadline of their own: a hanging forge read neither runs
// before the hub's commit is read nor holds the pass for the deploy checks' two minutes (#7614).
func TestPRStatusPassRunsAfterTheDeployChecksOnItsOwnDeadline(t *testing.T) {
	s, _, task := newSupervisedService(t)
	s.setTaskStage(context.Background(), task.ID, "review", "https://github.com/example/app/pull/12")
	var mu sync.Mutex
	var order []string
	note := func(what string) { mu.Lock(); order = append(order, what); mu.Unlock() }
	s.deploy.forge = &hangingStatusForge{called: func() { note("pr") }}
	s.deploy.hubBuild = func(context.Context) (string, string, error) {
		note("hub")
		return "", "", errors.New("hub build unavailable")
	}
	old := prStatusPassMost
	prStatusPassMost = 50 * time.Millisecond
	t.Cleanup(func() { prStatusPassMost = old })
	start := time.Now()
	s.sweepDeploys(time.Now())
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("a hanging PR read held the pass for %s", took)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(order, ",") != "hub,pr" {
		t.Fatalf("order %v, want the hub's commit before the PR reads", order)
	}
}
