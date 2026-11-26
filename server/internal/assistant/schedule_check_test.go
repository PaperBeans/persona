package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

type routineCheckHub struct {
	*fakeHub
	result  proto.RoutineCheckResult
	err     error
	calls   int
	machine string
}

func (h *routineCheckHub) DeviceCall(ctx context.Context, p DeviceCall) (json.RawMessage, error) {
	if p.Method != proto.MethodRoutineCheck {
		return h.fakeHub.DeviceCall(ctx, p)
	}
	h.calls++
	h.machine = p.Device
	if h.err != nil {
		return nil, h.err
	}
	return json.Marshal(h.result)
}

func TestRoutineCheckRun(t *testing.T) {
	for _, tc := range []struct {
		name                                   string
		result                                 proto.RoutineCheckResult
		err                                    error
		policy                                 string
		manual, retried, once, skipped, warned bool
	}{
		{name: "pass"},
		{name: "skip", result: proto.RoutineCheckResult{ExitCode: 1, Reason: "holiday"}, skipped: true},
		{name: "continue after timeout", err: context.DeadlineExceeded, warned: true},
		{name: "skip when offline", err: errors.New("offline"), policy: "skip", skipped: true},
		{name: "manual ignores skip", result: proto.RoutineCheckResult{ExitCode: 1}, manual: true},
		{name: "one-off ignores skip", result: proto.RoutineCheckResult{ExitCode: 1}, once: true},
		{name: "retry continues the admitted run", result: proto.RoutineCheckResult{ExitCode: 1}, retried: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := readyHub()
			h := &routineCheckHub{fakeHub: f, result: tc.result, err: tc.err}
			st := &memStore{}
			s := withSettings(t, f, New(Options{Hub: h, Store: func() Store { return st }, Logf: testLogf(t)}))
			task := Task{ID: "test", Title: "Brief", Prompt: "Summarize", Repeat: repeatDaily, Time: "09:00", Facts: tc.skipped,
				RunIf: &routineCheck{RoutineCheckParams: proto.RoutineCheckParams{Command: "check", Timeout: 1}, OnError: tc.policy}}
			now := time.Now()
			if tc.once {
				task.Repeat, task.At = repeatOnce, now
			}
			setTasks(s, task)
			if _, err := s.runTask(task, now, now, tc.manual, tc.retried); err != nil {
				t.Fatal(err)
			}
			r := lastRunOf(t, s, task.ID)
			if r.ConditionSkipped != tc.skipped || (r.RunIfError != "") != tc.warned || (r.Status == runSkipped) != tc.skipped {
				t.Fatalf("unexpected run: %+v", r)
			}
			if (h.calls == 0) != (tc.manual || tc.once || tc.retried) {
				t.Fatalf("unexpected check count: %d", h.calls)
			}
			if !tc.skipped && (len(f.sends) == 0 || r.Retried != tc.retried) {
				t.Fatalf("run was not dispatched as expected: %+v", r)
			}
			if tc.skipped && (len(f.sends) != 0 || len(f.pushed()) != 0 || counted(r)) {
				t.Fatalf("skip started work or counted as failure: %+v", r)
			}
			if tc.skipped {
				failures, total := s.todayFailures(t.Context(), "*", now, time.UTC)
				if len(failures) != 0 || total != 0 {
					t.Fatalf("condition skip counted in failure summary: total=%d, failures=%+v", total, failures)
				}
			}
			if tc.name == "skip" && r.Error != "holiday" {
				t.Fatalf("lost skip reason: %+v", r)
			}
		})
	}
}

func TestRoutineCheckApprovalUnaffectedByReversibleSettingsSwitch(t *testing.T) {
	s, _ := newReversibleSettingService(t, true)
	shown := make(chan proto.Interaction, 1)
	go answerArchiveCard(t, s, false, shown)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	approved, err := s.approveRoutineCheck(ctx, "server", "conv1", &routineCheck{RoutineCheckParams: proto.RoutineCheckParams{Command: "check", Timeout: 1}})
	if err != nil || approved {
		t.Fatalf("run_if approval = %v, %v", approved, err)
	}
	if card := <-shown; card.Kind != proto.IxPermission || card.ToolName != "schedule_routine" {
		t.Fatalf("run_if card: %+v", card)
	}
}

func TestRoutineCheckUsesTaskExecutionComputer(t *testing.T) {
	for _, previous := range []string{"", "previous"} {
		t.Run("previous="+previous, func(t *testing.T) {
			f := readyHub()
			f.devices = append(f.devices, Device{ID: "other", Name: "Other computer", Online: true})
			h := &routineCheckHub{fakeHub: f, result: proto.RoutineCheckResult{ExitCode: 1, Reason: "nothing new"}}
			st := &memStore{}
			s := withSettings(t, f, New(Options{Hub: h, Store: func() Store { return st }, Logf: testLogf(t)}))
			task := Task{ID: "scoped", Title: "Check files", Prompt: "Summarize", Repeat: repeatDaily, Time: "09:00",
				ExecutionMachineID: "other", RunMachineID: previous,
				RunIf: &routineCheck{RoutineCheckParams: proto.RoutineCheckParams{Command: "check", Timeout: 1}}}
			setTasks(s, task)
			now := time.Now()
			if _, err := s.runTask(task, now, now, false, false); err != nil {
				t.Fatal(err)
			}
			if h.calls != 1 || h.machine != "other" {
				t.Fatalf("pre-run command used %q instead of the selected computer; calls=%d", h.machine, h.calls)
			}
			if r := lastRunOf(t, s, task.ID); !r.ConditionSkipped || f.sent() != 0 {
				t.Fatalf("pre-run skip started a model turn: %+v sends=%d", r, f.sent())
			}
		})
	}
}

func TestRoutineCheckUpdatePreservesAndClears(t *testing.T) {
	s, _, st := scheduleStorageFixture(t)
	const body = `{"title":"Daily","prompt":"Example","repeat":"daily","time":"09:00","enabled":true`
	for _, tc := range []struct {
		field string
		want  bool
	}{
		{field: `,"run_if":{"command":"check"}`, want: true},
		{want: true},
		{field: `,"run_if":null`},
	} {
		w := scheduleRequest(s, "PUT", "tasks/daily", body+tc.field+"}")
		task, _ := taskByID(s, "daily")
		if w.Code != http.StatusOK || (task.RunIf != nil) != tc.want {
			t.Fatalf("PUT %s: %d %s, check=%+v", tc.field, w.Code, w.Body, task.RunIf)
		}
		if tc.want && task.RunIf.Timeout != proto.RoutineCheckDefaultTimeout {
			t.Fatalf("wrong default: %+v", task.RunIf)
		}
		if tc.field != "" && tc.want {
			if _, err := s.toolScheduleRoutine(t.Context(), "", "", json.RawMessage(`{"id":"daily","enabled":false}`)); err != nil {
				t.Fatal(err)
			}
			task, _ := taskByID(s, "daily")
			if task.RunIf == nil {
				t.Fatal("tool update cleared the check")
			}
		}
		assertScheduleStored(t, s, st)
	}
	w := scheduleRequest(s, "PUT", "tasks/daily", body+`,"run_if":{"command":"check","timeout_s":0}}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "timeout_s") {
		t.Fatalf("invalid check: %d %s", w.Code, w.Body)
	}
}

func saveRoutineCheckForUpdateTest(t *testing.T, raw string) *Service {
	t.Helper()
	s, _, _ := scheduleStorageFixture(t)
	if raw == "" {
		return s
	}
	body := `{"title":"Daily","prompt":"Example","repeat":"daily","time":"09:00","enabled":true,"run_if":` + raw + `}`
	if w := scheduleRequest(s, "PUT", "tasks/daily", body); w.Code != http.StatusOK {
		t.Fatalf("save run_if: %d %s", w.Code, w.Body)
	}
	return s
}

func routineUpdateArgs(raw string) json.RawMessage {
	return json.RawMessage(`{"id":"daily","run_if":` + raw + `}`)
}

func TestRoutineCheckUpdateUnchangedDoesNotAskAgain(t *testing.T) {
	for _, tc := range []struct {
		name, saved, update string
	}{
		{name: "identical", saved: `{"command":"check","timeout_s":30,"on_error":"skip"}`, update: `{"command":"check","timeout_s":30,"on_error":"skip"}`},
		{name: "default timeout made explicit", saved: `{"command":"check"}`, update: `{"command":"check","timeout_s":30}`},
		{name: "empty on_error made explicit", saved: `{"command":"check"}`, update: `{"command":"check","on_error":""}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := saveRoutineCheckForUpdateTest(t, tc.saved)
			before, _ := taskByID(s, "daily")
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			out, err := s.toolScheduleRoutine(ctx, "server", "conv1", routineUpdateArgs(tc.update))
			if err != nil || !strings.Contains(out, "Saved") {
				t.Fatalf("unchanged run_if asked again or failed: out=%q err=%v pending=%+v", out, err, s.pendingQuestions())
			}
			after, _ := taskByID(s, "daily")
			if !reflect.DeepEqual(after.RunIf, before.RunIf) || len(s.pendingQuestions()) != 0 {
				t.Fatalf("unchanged run_if changed or left a card: before=%+v after=%+v pending=%+v", before.RunIf, after.RunIf, s.pendingQuestions())
			}
		})
	}
}

func TestRoutineCheckUpdateNewOrChangedAlwaysAsks(t *testing.T) {
	for _, tc := range []struct {
		name, saved, update string
	}{
		{name: "new check", update: `{"command":"check"}`},
		{name: "command", saved: `{"command":"check"}`, update: `{"command":"other"}`},
		{name: "timeout", saved: `{"command":"check"}`, update: `{"command":"check","timeout_s":31}`},
		{name: "on_error from empty to run", saved: `{"command":"check","on_error":""}`, update: `{"command":"check","on_error":"run"}`},
		{name: "on_error from run to skip", saved: `{"command":"check","on_error":"run"}`, update: `{"command":"check","on_error":"skip"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := saveRoutineCheckForUpdateTest(t, tc.saved)
			shown := make(chan proto.Interaction, 1)
			go answerArchiveCard(t, s, true, shown)
			out, err := s.toolScheduleRoutine(t.Context(), "server", "conv1", routineUpdateArgs(tc.update))
			if err != nil || !strings.Contains(out, "Saved") {
				t.Fatalf("changed run_if was not approved and saved: out=%q err=%v", out, err)
			}
			card := <-shown
			if card.ToolName != "schedule_routine" {
				t.Fatalf("approval card = %+v", card)
			}
			want, err := parseRoutineCheck(json.RawMessage(tc.update))
			if err != nil {
				t.Fatal(err)
			}
			after, _ := taskByID(s, "daily")
			if !reflect.DeepEqual(after.RunIf, want) {
				t.Fatalf("saved run_if = %+v, want %+v", after.RunIf, want)
			}
		})
	}
}

func TestRoutineCheckUpdateDeniedDoesNotChangeTask(t *testing.T) {
	s := saveRoutineCheckForUpdateTest(t, `{"command":"saved"}`)
	before, _ := taskByID(s, "daily")
	shown := make(chan proto.Interaction, 1)
	go answerArchiveCard(t, s, false, shown)
	out, err := s.toolScheduleRoutine(t.Context(), "server", "conv1", routineUpdateArgs(`{"command":"changed"}`))
	if err != nil || !strings.Contains(out, "did not approve") {
		t.Fatalf("denied update: out=%q err=%v", out, err)
	}
	<-shown
	after, _ := taskByID(s, "daily")
	if !reflect.DeepEqual(after.RunIf, before.RunIf) {
		t.Fatalf("denied run_if change was saved: before=%+v after=%+v", before.RunIf, after.RunIf)
	}
}

type routineApprovalResult struct {
	allowed bool
	err     error
}

func callRoutineApproval(ctx context.Context, s *Service, machine, session string, check *routineCheck, results chan<- routineApprovalResult) {
	allowed, err := s.approveRoutineCheck(ctx, machine, session, check)
	results <- routineApprovalResult{allowed: allowed, err: err}
}

func answerRoutineQuestion(t *testing.T, s *Service, id string, allow bool) {
	t.Helper()
	raw, _ := json.Marshal(proto.Resolution{Allow: &allow})
	if _, err := s.Action(context.Background(), Action{Op: "answer", InteractionID: id, Answer: raw}); err != nil {
		t.Fatal(err)
	}
}

func receiveRoutineApproval(t *testing.T, results <-chan routineApprovalResult) routineApprovalResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for run_if approval result")
		return routineApprovalResult{}
	}
}

func routineApprovalWaiters(s *Service) int {
	s.routineChecks.mu.Lock()
	defer s.routineChecks.mu.Unlock()
	total := 0
	for _, call := range s.routineChecks.calls {
		total += call.waiters
	}
	return total
}

func TestRoutineCheckConcurrentIdenticalApprovalsShareDecision(t *testing.T) {
	s, _ := newReversibleSettingService(t, true)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	results := make(chan routineApprovalResult, 2)
	check := &routineCheck{RoutineCheckParams: proto.RoutineCheckParams{Command: "check", Timeout: 30}, OnError: "skip"}
	go callRoutineApproval(ctx, s, "server", "conv1", check, results)
	waitFor(t, "the first run_if card", func() bool { return len(s.pendingQuestions()) == 1 })
	go callRoutineApproval(ctx, s, "server", "conv1", check, results)
	waitFor(t, "both callers sharing the in-flight run_if approval", func() bool { return routineApprovalWaiters(s) == 2 })
	cards := s.pendingQuestions()
	if len(cards) != 1 {
		t.Fatalf("identical in-flight run_if produced %d cards, want 1: %+v", len(cards), cards)
	}
	answerRoutineQuestion(t, s, cards[0].ID, false)
	for range 2 {
		if result := receiveRoutineApproval(t, results); result.err != nil || result.allowed {
			t.Fatalf("shared denial = %+v, want unapproved without error", result)
		}
	}
}

func TestRoutineCheckConcurrentCallerCancellationKeepsSharedApproval(t *testing.T) {
	s, _ := newReversibleSettingService(t, true)
	firstCtx, cancelFirst := context.WithCancel(t.Context())
	defer cancelFirst()
	secondCtx, cancelSecond := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancelSecond()
	results := make(chan routineApprovalResult, 2)
	check := &routineCheck{RoutineCheckParams: proto.RoutineCheckParams{Command: "check", Timeout: 30}}
	go callRoutineApproval(firstCtx, s, "server", "conv1", check, results)
	waitFor(t, "the first run_if card", func() bool { return len(s.pendingQuestions()) == 1 })
	go callRoutineApproval(secondCtx, s, "server", "conv1", check, results)
	waitFor(t, "both callers sharing the in-flight run_if approval", func() bool { return routineApprovalWaiters(s) == 2 })
	cards := s.pendingQuestions()
	if len(cards) != 1 {
		t.Fatalf("identical in-flight run_if produced %d cards, want 1: %+v", len(cards), cards)
	}
	cancelFirst()
	if result := receiveRoutineApproval(t, results); !errors.Is(result.err, context.Canceled) || result.allowed {
		t.Fatalf("cancelled caller = %+v, want context cancellation", result)
	}
	select {
	case result := <-results:
		t.Fatalf("second caller ended when the first caller cancelled: %+v", result)
	default:
	}
	answerRoutineQuestion(t, s, cards[0].ID, true)
	if result := receiveRoutineApproval(t, results); result.err != nil || !result.allowed {
		t.Fatalf("remaining caller = %+v, want shared approval", result)
	}
}

func TestRoutineCheckConcurrentDifferentApprovalsDoNotShare(t *testing.T) {
	for _, tc := range []struct {
		name, machine, session, command, onError string
		timeout                                  int
	}{
		{name: "command", machine: "server", session: "conv1", command: "other"},
		{name: "timeout", machine: "server", session: "conv1", command: "check", timeout: 31},
		{name: "on_error", machine: "server", session: "conv1", command: "check", onError: "skip"},
		{name: "machine", machine: "other", session: "conv1", command: "check"},
		{name: "session", machine: "server", session: "conv2", command: "check"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newReversibleSettingService(t, true)
			ctx, cancel := context.WithCancel(t.Context())
			results := make(chan routineApprovalResult, 2)
			go callRoutineApproval(ctx, s, "server", "conv1", &routineCheck{RoutineCheckParams: proto.RoutineCheckParams{Command: "check", Timeout: 30}}, results)
			waitFor(t, "the first run_if card", func() bool { return len(s.pendingQuestions()) == 1 })
			timeout := tc.timeout
			if timeout == 0 {
				timeout = 30
			}
			check := &routineCheck{RoutineCheckParams: proto.RoutineCheckParams{Command: tc.command, Timeout: timeout}, OnError: tc.onError}
			go callRoutineApproval(ctx, s, tc.machine, tc.session, check, results)
			waitFor(t, "a separate run_if card", func() bool { return len(s.pendingQuestions()) == 2 })
			cancel()
			for range 2 {
				if result := receiveRoutineApproval(t, results); result.err == nil || result.allowed {
					t.Fatalf("cancelled distinct approval = %+v", result)
				}
			}
		})
	}
}

func TestRoutineCheckCompletedApprovalIsNotReused(t *testing.T) {
	s, _ := newReversibleSettingService(t, true)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	results := make(chan routineApprovalResult, 1)
	check := &routineCheck{RoutineCheckParams: proto.RoutineCheckParams{Command: "check", Timeout: 30}}
	go callRoutineApproval(ctx, s, "server", "conv1", check, results)
	waitFor(t, "the first run_if card", func() bool { return len(s.pendingQuestions()) == 1 })
	first := s.pendingQuestions()[0]
	answerRoutineQuestion(t, s, first.ID, true)
	if result := receiveRoutineApproval(t, results); result.err != nil || !result.allowed {
		t.Fatalf("first approval = %+v, want allowed", result)
	}

	go callRoutineApproval(ctx, s, "server", "conv1", check, results)
	waitFor(t, "a new card after the completed approval", func() bool { return len(s.pendingQuestions()) == 1 })
	second := s.pendingQuestions()[0]
	if second.ID == first.ID {
		t.Fatalf("completed approval was reused: %+v", second)
	}
	answerRoutineQuestion(t, s, second.ID, false)
	if result := receiveRoutineApproval(t, results); result.err != nil || result.allowed {
		t.Fatalf("second decision = %+v, want a new denial", result)
	}
}

func TestRoutineCheckApprovalBookRetryWhileCancelledCallUnwinds(t *testing.T) {
	var book routineCheckApprovalBook
	key := routineCheckApprovalKey{machine: "server", session: "conv1", check: `{"command":"check"}`}

	firstStarted := make(chan struct{})
	firstSawCancel := make(chan struct{})
	releaseFirst := make(chan struct{})
	var releaseFirstOnce sync.Once
	unlockFirst := func() { releaseFirstOnce.Do(func() { close(releaseFirst) }) }
	t.Cleanup(unlockFirst)
	firstCtx, cancelFirst := context.WithCancel(t.Context())
	defer cancelFirst()

	firstResults := make(chan routineApprovalResult, 1)
	go func() {
		allowed, err := book.wait(firstCtx, key, func(ctx context.Context) (bool, error) {
			close(firstStarted)
			<-ctx.Done()
			close(firstSawCancel)
			<-releaseFirst
			return false, ctx.Err()
		})
		firstResults <- routineApprovalResult{allowed: allowed, err: err}
	}()
	<-firstStarted

	book.mu.Lock()
	firstCall := book.calls[key]
	book.mu.Unlock()
	if firstCall == nil {
		t.Fatal("first call not registered")
	}

	cancelFirst()
	if res := receiveRoutineApproval(t, firstResults); !errors.Is(res.err, context.Canceled) || res.allowed {
		t.Fatalf("first cancelled caller = %+v, want context.Canceled", res)
	}
	<-firstSawCancel

	// Retry while the cancelled call's ask goroutine is still unwinding.
	secondStarted := make(chan struct{})
	secondResults := make(chan routineApprovalResult, 1)
	go func() {
		allowed, err := book.wait(t.Context(), key, func(ctx context.Context) (bool, error) {
			close(secondStarted)
			if err := ctx.Err(); err != nil {
				return false, err
			}
			return true, nil
		})
		secondResults <- routineApprovalResult{allowed: allowed, err: err}
	}()

	// Wait until the retry either started a fresh ask (fixed) or joined the cancelled call (buggy).
	waitFor(t, "retry to start or join an approval call", func() bool {
		select {
		case <-secondStarted:
			return true
		default:
		}
		book.mu.Lock()
		defer book.mu.Unlock()
		return firstCall.waiters > 0
	})
	unlockFirst()
	<-firstCall.done

	if res := receiveRoutineApproval(t, secondResults); res.err != nil || !res.allowed {
		t.Fatalf("retry while cancelled call unwinds = (allowed=%v, err=%v), want allowed without error", res.allowed, res.err)
	}
	select {
	case <-secondStarted:
	default:
		t.Fatal("retry joined the cancelled call instead of starting a fresh approval")
	}
}

func TestRoutineCheckApprovalBookStaleRunDoesNotEvictReplacement(t *testing.T) {
	var book routineCheckApprovalBook
	key := routineCheckApprovalKey{machine: "server", session: "conv1", check: `{"command":"check"}`}

	firstStarted := make(chan struct{})
	firstSawCancel := make(chan struct{})
	releaseFirst := make(chan struct{})
	var releaseFirstOnce sync.Once
	unlockFirst := func() { releaseFirstOnce.Do(func() { close(releaseFirst) }) }
	t.Cleanup(unlockFirst)
	firstCtx, cancelFirst := context.WithCancel(t.Context())
	defer cancelFirst()

	firstResults := make(chan routineApprovalResult, 1)
	go func() {
		allowed, err := book.wait(firstCtx, key, func(ctx context.Context) (bool, error) {
			close(firstStarted)
			<-ctx.Done()
			close(firstSawCancel)
			<-releaseFirst
			return false, ctx.Err()
		})
		firstResults <- routineApprovalResult{allowed: allowed, err: err}
	}()
	<-firstStarted

	book.mu.Lock()
	firstCall := book.calls[key]
	book.mu.Unlock()
	if firstCall == nil {
		t.Fatal("first call not registered")
	}

	cancelFirst()
	if res := receiveRoutineApproval(t, firstResults); !errors.Is(res.err, context.Canceled) || res.allowed {
		t.Fatalf("first cancelled caller = %+v, want context.Canceled", res)
	}
	<-firstSawCancel

	// Start a replacement call while the cancelled first call has not finished unwinding.
	secondStarted := make(chan struct{})
	secondSawCancel := make(chan struct{})
	secondCtx, cancelSecond := context.WithCancel(t.Context())
	defer cancelSecond()

	secondResults := make(chan routineApprovalResult, 1)
	go func() {
		allowed, err := book.wait(secondCtx, key, func(ctx context.Context) (bool, error) {
			close(secondStarted)
			<-ctx.Done()
			close(secondSawCancel)
			return false, ctx.Err()
		})
		secondResults <- routineApprovalResult{allowed: allowed, err: err}
	}()

	// Wait until the replacement call is registered (or fail fast if the cancelled call was not evicted).
	select {
	case <-secondStarted:
	case <-time.After(2 * time.Second):
		unlockFirst()
		<-firstCall.done
		t.Fatal("replacement call did not start while cancelled call was still unwinding")
	}

	book.mu.Lock()
	secondCall := book.calls[key]
	book.mu.Unlock()
	if secondCall == nil || secondCall == firstCall {
		unlockFirst()
		cancelSecond()
		t.Fatalf("replacement call = %p, want distinct from first call %p", secondCall, firstCall)
	}
	t.Cleanup(func() {
		secondCall.cancel()
		<-secondCall.done
	})

	// Let the stale first call finish run(); it must not delete secondCall from book.calls.
	unlockFirst()
	<-firstCall.done

	// A third concurrent caller must share secondCall rather than launching a duplicate ask.
	thirdCtx, cancelThird := context.WithCancel(t.Context())
	defer cancelThird()
	thirdResults := make(chan routineApprovalResult, 1)
	duplicateAsk := make(chan struct{})
	var duplicateAskOnce sync.Once
	go func() {
		allowed, err := book.wait(thirdCtx, key, func(ctx context.Context) (bool, error) {
			duplicateAskOnce.Do(func() { close(duplicateAsk) })
			<-ctx.Done()
			return false, ctx.Err()
		})
		thirdResults <- routineApprovalResult{allowed: allowed, err: err}
	}()

	waitFor(t, "third caller to join second call or trigger duplicate ask", func() bool {
		select {
		case <-duplicateAsk:
			return true
		default:
		}
		book.mu.Lock()
		defer book.mu.Unlock()
		return secondCall.waiters == 2
	})

	select {
	case <-duplicateAsk:
		cancelSecond()
		cancelThird()
		t.Fatal("stale run evicted replacement call, causing a duplicate approval ask")
	default:
	}

	// Cancelling both waiters on secondCall must cancel secondCall's shared context.
	cancelSecond()
	cancelThird()
	if res := receiveRoutineApproval(t, secondResults); !errors.Is(res.err, context.Canceled) {
		t.Fatalf("second caller = %+v, want context.Canceled", res)
	}
	if res := receiveRoutineApproval(t, thirdResults); !errors.Is(res.err, context.Canceled) {
		t.Fatalf("third caller = %+v, want context.Canceled", res)
	}
	select {
	case <-secondSawCancel:
	case <-time.After(2 * time.Second):
		t.Fatal("replacement approval context was not cancelled after all waiters left")
	}
	<-secondCall.done
}
