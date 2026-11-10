package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// restartMem is the store a manager restarts on: the interactions table as the next manager reads it (restoreMem)
// and session_work_events, read back in time order as Postgres reads them.
type restartMem struct {
	*restoreMem
	wmu    sync.Mutex
	events []db.WorkEvent
}

func (m *restartMem) PutSessionWork(context.Context, db.SessionWork) error { return nil }
func (m *restartMem) SessionWorks(context.Context, time.Time) ([]db.SessionWork, error) {
	return nil, nil
}
func (m *restartMem) PruneSessionWork(context.Context, time.Time) error { return nil }

func (m *restartMem) AddWorkEvent(_ context.Context, e db.WorkEvent) error {
	m.wmu.Lock()
	defer m.wmu.Unlock()
	m.events = append(m.events, e)
	return nil
}

func (m *restartMem) WorkEvents(_ context.Context, since time.Time) ([]db.WorkEvent, error) {
	m.wmu.Lock()
	defer m.wmu.Unlock()
	var out []db.WorkEvent
	for _, e := range m.events {
		if e.At.After(since) {
			out = append(out, e)
		}
	}
	slices.SortStableFunc(out, func(a, b db.WorkEvent) int { return a.At.Compare(b.At) })
	return out, nil
}

// kinds are the kinds of the events about card id, in time order.
func (m *restartMem) kinds(id string) []string {
	events, _ := m.WorkEvents(context.Background(), time.Time{})
	var out []string
	for _, e := range events {
		if e.Detail["id"] == id {
			out = append(out, e.Kind)
		}
	}
	return out
}

// waitKinds waits for the events about card id to be want (they are written in the background).
func (m *restartMem) waitKinds(t *testing.T, id string, want ...string) {
	t.Helper()
	waitDelegate(t, fmt.Sprintf("the events about %s to be %q", id, want), func() bool { return slices.Equal(m.kinds(id), want) })
}

// backdate moves the events about card id back by d, as if they had happened that long ago.
func (m *restartMem) backdate(id string, d time.Duration) {
	m.wmu.Lock()
	defer m.wmu.Unlock()
	for i := range m.events {
		if m.events[i].Detail["id"] == id {
			m.events[i].At = m.events[i].At.Add(-d)
		}
	}
}

// restarted is the manager that starts after the first one went away: s on hub f, on the first one's store.
type restarted struct {
	s  *Service
	f  *fakeHub
	mu sync.Mutex
	// calls counts how often its assistant was asked to decide.
	calls int
}

func (r *restarted) delegateCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// pushesFor are the pushes for card id.
func (r *restarted) pushesFor(id string) []proto.Interaction {
	var out []proto.Interaction
	for _, n := range r.f.pushed() {
		if n.Interaction != nil && n.Interaction.ID == id {
			out = append(out, *n.Interaction)
		}
	}
	return out
}

// fullList is the last full list of cards the hub was told (fleet.interactions).
func (r *restarted) fullList() []proto.Interaction {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	var full assistantwire.FleetInteractions
	for _, tl := range r.f.told {
		if tl.event == assistantwire.EventFleetInteractions {
			_ = json.Unmarshal(tl.params, &full)
		}
	}
	return full.Interactions
}

// up is the next manager's connection to the hub coming up (FleetUp: the cards are restored, then the hub gets them).
func (r *restarted) up() { r.s.FleetUp(context.Background()) }

// hookBack is ix's hook claiming it again through the next manager; the channel has the answer the hook gets.
func (r *restarted) hookBack(ix proto.Interaction) <-chan proto.Resolution {
	out := make(chan proto.Resolution, 1)
	go func() { res, _ := r.s.waitInteraction(context.Background(), ix); out <- res }()
	return out
}

// restartFleet has a manager with agents.delegate on (decide is its assistant) take a resumable question ix, then
// go away when ready says so, its hook's connection with it; the events about ix are then moved back by age. The next
// manager (its assistant next) is made on the same store: the test brings it up and the hook back, in its order.
func restartFleet(t *testing.T, ix proto.Interaction, decide func(DelegateParams) (DelegateResult, error),
	ready func(*delegateFleet) bool, age time.Duration, next func(DelegateParams) (DelegateResult, error),
) (st *restartMem, r *restarted) {
	t.Helper()
	first := newDelegateFleet(t, decide)
	st = &restartMem{restoreMem: &restoreMem{fleetMem: first.st}}
	first.s.store = func() Store { return st }
	hookCtx, hookGone := context.WithCancel(context.Background())
	gone := make(chan struct{})
	go func() { _, _ = first.s.waitInteraction(hookCtx, ix); close(gone) }()
	waitDelegate(t, "the first manager", func() bool { return ready(first) })
	first.s.work.eventWrites.Wait()
	hookGone() // the manager goes, and the hook's connection with it
	select {
	case <-gone:
	case <-time.After(5 * time.Second):
		t.Fatal("the hook still waits on the manager that went")
	}
	st.backdate(ix.ID, age)

	f := newFakeHub()
	on := true
	f.hello.Agents.Delegate.Enabled = &on
	first.f.mu.Lock()
	sessions := slices.Clone(first.f.sessions["mac"])
	first.f.mu.Unlock()
	f.setSessions("mac", sessions...)
	r = &restarted{f: f}
	r.s = New(Options{Hub: f, Store: func() Store { return st }, Logf: testLogf(t), Devices: first.d})
	r.s.delegateHook = func(_ context.Context, p DelegateParams) (DelegateResult, error) {
		r.mu.Lock()
		r.calls++
		r.mu.Unlock()
		return next(p)
	}
	return st, r
}

func resumableQuestion(id string) proto.Interaction {
	ix := delegateQuestion(id)
	ix.Resumable = true // a Claude Code hook's: its hook claims it again through the next manager
	return ix
}

// A question the assistant was deciding on when the manager restarted (#2587) is not put back as a plain card that
// nobody is told of: within agents.delegate's time the next manager has the assistant decide on it again, once.
func TestDelegatingQuestionSurvivesAManagerRestart(t *testing.T) {
	stuck := make(chan struct{})
	t.Cleanup(func() { close(stuck) })
	never := func(DelegateParams) (DelegateResult, error) { <-stuck; return DelegateResult{}, errors.New("gone") }
	ix := resumableQuestion("ix_restart")
	decided := make(chan struct{})
	st, r := restartFleet(t, ix, never, func(df *delegateFleet) bool { return df.delegateCalls() == 1 }, 0,
		func(DelegateParams) (DelegateResult, error) {
			<-decided
			return DelegateResult{Answer: true, Answers: map[string]string{"用哪个分支？": "feat/x"}, Reason: "任务就在 feat/x 上"}, nil
		})
	r.up()
	back := r.hookBack(ix)
	waitDelegate(t, "the assistant asked again", func() bool { return r.delegateCalls() == 1 })
	if list := r.fullList(); len(list) != 1 || list[0].ID != ix.ID || !list[0].Delegating {
		t.Fatalf("the hub's full list = %+v", list)
	}
	close(decided)
	if res := answerOf(t, back); res.AnsweredBy != proto.AnsweredByAssistant || res.Answers["用哪个分支？"] != "feat/x" {
		t.Fatalf("the hook got %+v", res)
	}
	if got := r.delegateCalls(); got != 1 {
		t.Fatalf("the next manager asked the assistant %d times", got)
	}
	if p := r.pushesFor(ix.ID); len(p) != 0 {
		t.Fatalf("pushed a question the assistant answered: %+v", p)
	}
	st.waitKinds(t, ix.ID, "delegate_start", "delegate_end")
}

// Its time up while the manager was away, the question goes to the user as a timeout would have sent it: pushed once,
// the user's (Persona Link counts it), no longer delegating, and the assistant not asked again.
func TestDelegatingQuestionPastItsTimeAfterARestartGoesToTheUser(t *testing.T) {
	stuck := make(chan struct{})
	t.Cleanup(func() { close(stuck) })
	never := func(DelegateParams) (DelegateResult, error) { <-stuck; return DelegateResult{}, errors.New("gone") }
	ix := resumableQuestion("ix_late")
	st, r := restartFleet(t, ix, never, func(df *delegateFleet) bool { return df.delegateCalls() == 1 }, 10*time.Minute,
		func(DelegateParams) (DelegateResult, error) { return DelegateResult{Answer: true}, nil })
	r.up()
	back := r.hookBack(ix)
	waitDelegate(t, "the push", func() bool { return len(r.pushesFor(ix.ID)) > 0 })
	if p := r.pushesFor(ix.ID); len(p) != 1 || p[0].Delegating {
		t.Fatalf("pushes %+v", p)
	}
	if list, told := r.s.pendingCards(); len(list) != 1 || list[0].Delegating || !told[ix.ID] {
		t.Fatalf("pending %+v, told %v", list, told)
	}
	if got := r.delegateCalls(); got != 0 {
		t.Fatalf("the next manager asked the assistant %d times", got)
	}
	st.waitKinds(t, ix.ID, "delegate_start", "delegate_failed", "delegate_end")
	if err := r.s.resolve(ix.ID, proto.Resolution{Answers: map[string]string{"用哪个分支？": "main"}}); err != nil {
		t.Fatal(err)
	}
	if res := answerOf(t, back); res.AnsweredBy != "" || res.Answers["用哪个分支？"] != "main" {
		t.Fatalf("the hook got %+v", res)
	}
	if p := r.pushesFor(ix.ID); len(p) != 1 {
		t.Fatalf("%d pushes", len(p))
	}
}

// A question the assistant had already left to the user before the restart was told then: the next manager puts it
// back on the apps without telling it again, and does not hand it to the assistant again.
func TestQuestionHandedOverBeforeARestartIsNotToldAgain(t *testing.T) {
	ix := resumableQuestion("ix_told")
	st, r := restartFleet(t, ix,
		func(DelegateParams) (DelegateResult, error) {
			return DelegateResult{Ask: &proto.Delegate{What: "修登录页崩溃", Recommend: "main"}}, nil
		},
		func(df *delegateFleet) bool { return df.pushedFor(ix.ID) != nil }, 0,
		func(DelegateParams) (DelegateResult, error) { return DelegateResult{Answer: true}, nil })
	r.up()
	back := r.hookBack(ix)
	if list := r.fullList(); len(list) != 1 || list[0].ID != ix.ID || list[0].Delegating {
		t.Fatalf("the hub's full list = %+v", list)
	}
	if p := r.f.pushedBy(time.Now().Add(300 * time.Millisecond)); len(p) != 0 {
		t.Fatalf("told again after the restart: %+v", p)
	}
	if got := r.delegateCalls(); got != 0 {
		t.Fatalf("the next manager asked the assistant %d times", got)
	}
	st.waitKinds(t, ix.ID, "delegate_start", "delegate_end")
	if err := r.s.resolve(ix.ID, proto.Resolution{Answers: map[string]string{"用哪个分支？": "main"}}); err != nil {
		t.Fatal(err)
	}
	answerOf(t, back)
}

// The hook may claim its card through the next manager before the hub's handshake restores the others
// (waitInteraction finds the card only in the history): the assistant goes on deciding on it all the same, once, and the
// handshake that follows does not start it again.
func TestDelegatingQuestionResumesWhenItsHookComesBackFirst(t *testing.T) {
	stuck := make(chan struct{})
	t.Cleanup(func() { close(stuck) })
	never := func(DelegateParams) (DelegateResult, error) { <-stuck; return DelegateResult{}, errors.New("gone") }
	ix := resumableQuestion("ix_hook_first")
	decided := make(chan struct{})
	st, r := restartFleet(t, ix, never, func(df *delegateFleet) bool { return df.delegateCalls() == 1 }, 0,
		func(DelegateParams) (DelegateResult, error) {
			<-decided
			return DelegateResult{Answer: true, Answers: map[string]string{"用哪个分支？": "main"}, Reason: "合到主分支"}, nil
		})
	back := r.hookBack(ix)
	waitDelegate(t, "the assistant asked again", func() bool { return r.delegateCalls() == 1 })
	if list := r.s.pendingInteractions(); len(list) != 1 || !list[0].Delegating {
		t.Fatalf("pending %+v", list)
	}
	r.up()
	if list := r.fullList(); len(list) != 1 || list[0].ID != ix.ID || !list[0].Delegating {
		t.Fatalf("the hub's full list = %+v", list)
	}
	close(decided)
	if res := answerOf(t, back); res.AnsweredBy != proto.AnsweredByAssistant || res.Answers["用哪个分支？"] != "main" {
		t.Fatalf("the hook got %+v", res)
	}
	if got := r.delegateCalls(); got != 1 {
		t.Fatalf("the next manager asked the assistant %d times", got)
	}
	if p := r.pushesFor(ix.ID); len(p) != 0 {
		t.Fatalf("pushed a question the assistant answered: %+v", p)
	}
	st.waitKinds(t, ix.ID, "delegate_start", "delegate_end")
}
