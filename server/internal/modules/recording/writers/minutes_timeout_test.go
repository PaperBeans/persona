package writers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// slowModel needs latency for every answer: a call given less time than that runs out of it, as the client does at
// its deadline; given is the time each call had.
type slowModel struct {
	latency time.Duration
	reply   string
	given   []time.Duration
}

func (m *slowModel) JSONMax(ctx context.Context, _, _ string, _ int, out any) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		return errors.New("a call without a deadline")
	}
	left := time.Until(deadline)
	m.given = append(m.given, left)
	if left < m.latency {
		return context.DeadlineExceeded
	}
	return json.Unmarshal([]byte(m.reply), out)
}

// minutesWaitOver ends recording id's wait for its next minutes try, as if the time had passed.
func minutesWaitOver(t *testing.T, e *testEngine, id string) {
	t.Helper()
	st := e.Store
	ws(st).recMu.Lock()
	defer ws(st).recMu.Unlock()
	r, ok := st.Recording(id)
	if !ok {
		t.Fatalf("recording %s is gone", id)
	}
	r.MinutesRetryAt = time.Now().Add(-time.Second)
	if err := ws(st).putEntity(KindRecording, r.ID, r, &r.Tagging, false); err != nil {
		t.Fatal(err)
	}
}

// A model slower than a call's time (#3170): the first two tries run out of it and are counted apart from unusable
// answers, the minutes stay pending (never failed), each next try waits for a later round and gets more time, and the
// third gets them written.
func TestMinutesSlowModel(t *testing.T) {
	ctx := context.Background()
	model := &slowModel{latency: 15 * time.Minute, reply: `{"title":"t","decisions":[{"text":"定了","line":1}]}`}
	e := goldenEngine(t, model)
	now := time.Now()
	putRecording(t, e, recID, meetingUpload(1, now.Add(-time.Hour), "就这么定了"), now)
	for i := 1; i <= 2; i++ {
		if n := e.WriteMinutes(ctx, 2); n != 0 {
			t.Fatalf("try %d wrote %d", i, n)
		}
		r := recordingNow(t, e, recID)
		if r.MinutesState != MinutesPending || r.MinutesFails != 0 || r.MinutesTimeouts != i {
			t.Fatalf("try %d: %q fails %d timeouts %d", i, r.MinutesState, r.MinutesFails, r.MinutesTimeouts)
		}
		if wait := time.Until(r.MinutesRetryAt); wait > minutesRetry<<(i-1) || wait < minutesRetry<<(i-1)-time.Minute {
			t.Fatalf("try %d: next in %s", i, wait)
		}
		calls := len(model.given)
		e.WriteMinutes(ctx, 2)
		if len(model.given) != calls {
			t.Fatalf("try %d: asked again before its wait was over", i)
		}
		minutesWaitOver(t, e, recID)
	}
	if n := e.WriteMinutes(ctx, 2); n != 1 {
		t.Fatalf("third try wrote %d", n)
	}
	r := recordingNow(t, e, recID)
	if r.MinutesState != MinutesReady || r.MinutesTimeouts != 0 || r.MinutesFailRev != 0 || !r.MinutesRetryAt.IsZero() {
		t.Fatalf("written: %q timeouts %d fail rev %d retry %s", r.MinutesState, r.MinutesTimeouts, r.MinutesFailRev, r.MinutesRetryAt)
	}
	if m, err := e.Store.ReadMinutes(recID); err != nil || len(m.Decisions) != 1 {
		t.Fatalf("minutes %+v %v", m, err)
	}
	if len(model.given) != 3 {
		t.Fatalf("%d calls, want 3", len(model.given))
	}
	for i, got := range model.given {
		if want := minutesCallTimeout * time.Duration(i+1); got > want || got < want-time.Minute {
			t.Errorf("call %d had %s, want %s", i+1, got, want)
		}
	}
}

// A recording no model finishes: while it waits the others go first; it fails only after minutesTimeoutTries tries
// that ran out of time (later than minutesTries unusable answers would), and is asked no more until a new revision or
// a regenerate. A round that is stopped counts nothing.
func TestMinutesTimeoutsFailAtLast(t *testing.T) {
	ctx := context.Background()
	stop := func() {}
	model := &funcModel{f: func(_ int, _, user string) (string, error) {
		if strings.Contains(user, "卡住") {
			stop()
			return "", context.DeadlineExceeded
		}
		return `{"title":"t","points":[{"text":"x","line":1}]}`, nil
	}}
	e := goldenEngine(t, model)
	now := time.Now()
	const other = "6f8fad5b-d9cb-469f-a165-70867728950e"
	putRecording(t, e, recID, meetingUpload(1, now.Add(-time.Minute), "卡住的一句"), now)
	putRecording(t, e, other, meetingUpload(1, now.Add(-time.Hour), "一句"), now)

	// One a round: the stuck one is newest, so it goes first; then it waits and the other one gets its turn.
	e.WriteMinutes(ctx, 1)
	if r := recordingNow(t, e, other); r.MinutesState != MinutesPending {
		t.Fatalf("other tried in the same round: %q", r.MinutesState)
	}
	if n := e.WriteMinutes(ctx, 1); n != 1 {
		t.Fatalf("other not written while the stuck one waits: %d", n)
	}
	if r := recordingNow(t, e, other); r.MinutesState != MinutesReady {
		t.Fatalf("other: %q", r.MinutesState)
	}

	// A stopped round (the hub shutting down) is not the model's fault.
	minutesWaitOver(t, e, recID)
	rctx, cancel := context.WithCancel(ctx)
	stop = cancel
	e.WriteMinutes(rctx, 2)
	stop = func() {}
	if r := recordingNow(t, e, recID); r.MinutesTimeouts != 1 || r.MinutesState != MinutesPending {
		t.Fatalf("stopped round counted: timeouts %d %q", r.MinutesTimeouts, r.MinutesState)
	}

	for i := 2; i <= minutesTimeoutTries; i++ {
		minutesWaitOver(t, e, recID)
		e.WriteMinutes(ctx, 2)
		want := MinutesPending
		if i == minutesTimeoutTries {
			want = MinutesFailed
		}
		if r := recordingNow(t, e, recID); r.MinutesState != want || r.MinutesTimeouts != i || r.MinutesFails != 0 {
			t.Fatalf("timeout %d: %q timeouts %d fails %d", i, r.MinutesState, r.MinutesTimeouts, r.MinutesFails)
		}
	}
	minutesWaitOver(t, e, recID)
	calls := len(model.calls)
	e.WriteMinutes(ctx, 2)
	if len(model.calls) != calls {
		t.Fatal("a failed revision is asked again")
	}

	// A new revision starts over: tried at once, with the first try's time.
	putRecording(t, e, recID, meetingUpload(2, now.Add(-time.Minute), "卡住的一句", "又一句"), now)
	if r := recordingNow(t, e, recID); r.MinutesState != MinutesPending || minutesTimeouts(r) != 0 || minutesWaiting(r, time.Now()) {
		t.Fatalf("new revision: %q timeouts %d", r.MinutesState, minutesTimeouts(r))
	}
	e.WriteMinutes(ctx, 2)
	if r := recordingNow(t, e, recID); r.MinutesTimeouts != 1 || r.MinutesFailRev != 2 {
		t.Fatalf("new revision's try: timeouts %d of rev %d", r.MinutesTimeouts, r.MinutesFailRev)
	}
	// Asking again forgets the timeouts and the wait.
	if err := e.AskMinutes(recID); err != nil {
		t.Fatal(err)
	}
	if r := recordingNow(t, e, recID); r.MinutesState != MinutesPending || r.MinutesTimeouts != 0 || !r.MinutesRetryAt.IsZero() {
		t.Fatalf("asked again: %q timeouts %d retry %s", r.MinutesState, r.MinutesTimeouts, r.MinutesRetryAt)
	}
	if minutesCallTime(0) != minutesCallTimeout || minutesCallTime(3) != 4*minutesCallTimeout || minutesCallTime(3) >= minutesJobTimeout {
		t.Error("minutesCallTime")
	}
}
