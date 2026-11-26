package agentview_test

import (
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/assistant/agentview"
	"github.com/nwverse/persona/server/internal/domainkit/defect"
	"github.com/nwverse/persona/server/internal/domainkit/domaintest"
)

// in is a session's input to the view, every part a real value unless a test changes it.
type in struct {
	host       agentview.Host
	background agentview.Background
	failure    agentview.Failure
	asking     agentview.Asking
	recency    agentview.Recency
	row        agentview.Row
	muting     agentview.Muting
	resumable  agentview.Resumability
}

func base(host agentview.Host) in {
	return in{host: host, background: agentview.BackgroundNone(), failure: agentview.FailureNone(),
		asking: agentview.AskingNothing(), recency: agentview.RecencyEarlier(), row: agentview.RowNone(),
		muting: agentview.MutingHeard(), resumable: agentview.ResumabilityResumable()}
}

func (i in) decide(t *testing.T) agentview.View {
	t.Helper()
	s := domaintest.Ok(t, agentview.NewSessionBuilder().
		Turn(i.host, i.background).
		Ending(i.failure, i.asking, i.recency).
		Place(i.row, i.muting, i.resumable).
		Build())
	return agentview.Decide(s)
}

func (i in) view(t *testing.T) string {
	t.Helper()
	var w words
	i.decide(t).Accept(&w)
	return w.String()
}

// rankOf is a view's rank.
type rankOf struct{ rank agentview.Rank }

func (r *rankOf) VisitView(_ agentview.Phase, _ agentview.Reason, _ agentview.Counting, rank agentview.Rank) {
	r.rank = rank
}

// words writes a view as "phase reason [counted] rank".
type words struct{ parts []string }

func (w *words) String() string  { return strings.Join(w.parts, " ") }
func (w *words) add(s ...string) { w.parts = append(w.parts, s...) }

func (w *words) VisitView(phase agentview.Phase, reason agentview.Reason, counting agentview.Counting, rank agentview.Rank) {
	phase.Accept(w)
	reason.Accept(w)
	counting.Accept(w)
}

func (w *words) VisitNeedsYou() { w.add("needs_you") }
func (w *words) VisitWorking()  { w.add("working") }
func (w *words) VisitDone()     { w.add("done") }
func (w *words) VisitError()    { w.add("error") }
func (w *words) VisitIdle()     { w.add("idle") }

func (w *words) VisitCards(_ agentview.CardCount, holding agentview.Holding) {
	w.add("cards")
	if holding == agentview.HoldingHeld() {
		w.add("held")
	}
}
func (w *words) VisitTerminalDialog() { w.add("terminal_dialog") }
func (w *words) VisitTurn()           { w.add("turn") }
func (w *words) VisitBackground(b agentview.Background) {
	w.add("background")
	if b == agentview.BackgroundWaiting() {
		w.add("waiting")
	}
}
func (w *words) VisitUnseen()       { w.add("unseen") }
func (w *words) VisitJustFinished() { w.add("just_finished") }
func (w *words) VisitAsks(s agentview.Seeing) {
	w.add("asks")
	if s == agentview.SeeingUnseen() {
		w.add("unseen")
	}
}
func (w *words) VisitUsageLimit(f agentview.Failure) {
	w.add("usage_limit")
	if f == agentview.FailureUsageLimitResuming() {
		w.add("resumes")
	}
}
func (w *words) VisitModel()        { w.add("model") }
func (w *words) VisitStartRefused() { w.add("start_refused") }
func (w *words) VisitRestartInterrupted(r agentview.Resumability) {
	w.add("restart_interrupted")
	if r == agentview.ResumabilityResumable() {
		w.add("resumable")
	}
}
func (w *words) VisitSeen()        { w.add("seen") }
func (w *words) VisitNotRunning()  { w.add("not_running") }
func (w *words) VisitHostUnknown() { w.add("host_unknown") }
func (w *words) VisitCounted()     { w.add("counted") }
func (w *words) VisitUncounted()   {}

// Each input lands in its phase with its reason and rank: the row first (needs you, as fleetcount counted it), then
// an interrupted session and an error, work, a finished turn and idle; D-A, D-B and D-C as decided.
func TestDecide(t *testing.T) {
	muted := func(i in) in { i.muting = agentview.MutingMuted(); return i }
	with := func(i in, f func(*in)) in { f(&i); return i }
	cases := []struct {
		name string
		in   in
		want string
	}{
		{"cards counted", with(base(agentview.HostWaiting()), func(i *in) {
			i.row = agentview.RowByCards(agentview.ParseCardCount(2), agentview.HoldingOwners(), agentview.CountingCounted())
		}), "needs_you cards counted"},
		{"held cards", with(base(agentview.HostWaiting()), func(i *in) {
			i.row = agentview.RowByCards(agentview.ParseCardCount(1), agentview.HoldingHeld(), agentview.CountingUncounted())
		}), "needs_you cards held"},
		{"a dialog", with(base(agentview.HostWaiting()), func(i *in) { i.row = agentview.RowByDialog(agentview.CountingCounted()) }),
			"needs_you terminal_dialog counted"},
		{"a muted dialog stays first", muted(with(base(agentview.HostWaiting()), func(i *in) {
			i.row = agentview.RowByDialog(agentview.CountingUncounted())
		})), "needs_you terminal_dialog"},
		{"cards under another row", with(base(agentview.HostWaiting()), func(i *in) {
			i.row = agentview.RowElsewhere(agentview.ParseCardCount(1), agentview.HoldingOwners())
		}), "needs_you cards"},
		{"the supervisor's cards", with(base(agentview.HostWaiting()), func(i *in) { i.row = agentview.RowSupervised() }),
			"working turn"},
		{"waiting with no row", base(agentview.HostWaiting()), "needs_you terminal_dialog"},
		{"interrupted (D-C)", base(agentview.HostInterrupted()), "error restart_interrupted resumable"},
		{"interrupted, not resumable", with(base(agentview.HostInterrupted()), func(i *in) {
			i.resumable = agentview.ResumabilityNotResumable()
		}), "error restart_interrupted"},
		{"a model error", with(base(agentview.HostUnread()), func(i *in) { i.failure = agentview.FailureModel() }), "error model"},
		{"a usage limit", with(base(agentview.HostSeen()), func(i *in) { i.failure = agentview.FailureUsageLimit() }),
			"error usage_limit"},
		{"a usage limit that resumes", with(base(agentview.HostSeen()), func(i *in) {
			i.failure = agentview.FailureUsageLimitResuming()
		}), "error usage_limit resumes"},
		{"a refused start", with(base(agentview.HostOffline()), func(i *in) { i.failure = agentview.FailureStartRefused() }),
			"error start_refused"},
		{"a muted error", muted(with(base(agentview.HostUnread()), func(i *in) { i.failure = agentview.FailureModel() })),
			"error model"},
		{"working", base(agentview.HostWorking()), "working turn"},
		{"background", with(base(agentview.HostWorking()), func(i *in) { i.background = agentview.BackgroundRunning() }),
			"working background"},
		{"background waiting", with(base(agentview.HostWorking()), func(i *in) { i.background = agentview.BackgroundWaiting() }),
			"working background waiting"},
		{"muted working", muted(base(agentview.HostWorking())), "working turn"},
		{"working ignores an error", with(base(agentview.HostWorking()), func(i *in) { i.failure = agentview.FailureModel() }),
			"working turn"},
		{"unseen", base(agentview.HostUnread()), "done unseen"},
		{"asks, unseen (D-A)", with(base(agentview.HostUnread()), func(i *in) { i.asking = agentview.AskingOwner() }),
			"done asks unseen"},
		{"asks, seen (D-A)", with(base(agentview.HostSeen()), func(i *in) { i.asking = agentview.AskingOwner() }), "done asks"},
		{"just finished", with(base(agentview.HostSeen()), func(i *in) { i.recency = agentview.RecencyJustFinished() }),
			"done just_finished"},
		{"muted unseen", muted(base(agentview.HostUnread())), "done unseen"},
		{"seen", base(agentview.HostSeen()), "idle seen"},
		{"muted seen", muted(base(agentview.HostSeen())), "idle seen"},
		{"unknown (D-B)", base(agentview.HostUnknown()), "idle host_unknown"},
		{"offline", base(agentview.HostOffline()), "idle not_running"},
	}
	for _, c := range cases {
		if got := c.in.view(t); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// Build reports every part a skipped stage or a zero value left out; Decide and the zero View are defects.
func TestZeroValues(t *testing.T) {
	var (
		b          agentview.SessionBuilder
		noSession  agentview.Session
		noView     agentview.View
		noPhase    agentview.Phase
		noHolding  agentview.Holding
		noCounting agentview.Counting
	)
	got := domaintest.Fails(t, b.Build())
	for _, part := range []string{"host", "background", "failure", "asking", "recency", "row", "muting", "resumability"} {
		if !strings.Contains(got, part) {
			t.Errorf("Build of the zero builder: %q names no %s", got, part)
		}
	}
	for name, f := range map[string]func(){
		"Decide":         func() { agentview.Decide(noSession) },
		"View.Accept":    func() { noView.Accept(&words{}) },
		"Phase.Accept":   func() { noPhase.Accept(&words{}) },
		"ParseCardCount": func() { agentview.ParseCardCount(-1) },
		"RowByCards": func() {
			agentview.RowByCards(agentview.ParseCardCount(1), noHolding, agentview.CountingCounted())
		},
		"RowByDialog":  func() { agentview.RowByDialog(noCounting) },
		"RowElsewhere": func() { agentview.RowElsewhere(agentview.ParseCardCount(1), noHolding) },
	} {
		func() {
			defer func() {
				if _, ok := recover().(defect.Defect); !ok {
					t.Errorf("%s on a zero value is no defect", name)
				}
			}()
			f()
		}()
	}
}

// One order for every client: needs you (counted first), then error, working, done, idle; within each, the order of
// today's lists (docs/protocol/agent-states.md "Rank"), a muted session last in its phase.
func TestRankIsOneOrder(t *testing.T) {
	muted := func(i in) in { i.muting = agentview.MutingMuted(); return i }
	with := func(i in, f func(*in)) in { f(&i); return i }
	order := []in{
		with(base(agentview.HostWaiting()), func(i *in) { i.row = agentview.RowByDialog(agentview.CountingCounted()) }),
		with(base(agentview.HostWaiting()), func(i *in) { i.row = agentview.RowByDialog(agentview.CountingUncounted()) }),
		base(agentview.HostInterrupted()),
		with(base(agentview.HostSeen()), func(i *in) { i.failure = agentview.FailureUsageLimitResuming() }),
		muted(with(base(agentview.HostSeen()), func(i *in) { i.failure = agentview.FailureModel() })),
		base(agentview.HostWorking()),
		with(base(agentview.HostWorking()), func(i *in) { i.background = agentview.BackgroundRunning() }),
		with(base(agentview.HostWorking()), func(i *in) { i.background = agentview.BackgroundWaiting() }),
		muted(base(agentview.HostWorking())),
		base(agentview.HostUnread()),
		with(base(agentview.HostSeen()), func(i *in) { i.asking = agentview.AskingOwner() }),
		with(base(agentview.HostSeen()), func(i *in) { i.recency = agentview.RecencyJustFinished() }),
		muted(base(agentview.HostUnread())),
		base(agentview.HostSeen()),
		base(agentview.HostUnknown()),
		base(agentview.HostOffline()),
	}
	for k := 1; k < len(order); k++ {
		var a, b rankOf
		order[k-1].decide(t).Accept(&a)
		order[k].decide(t).Accept(&b)
		if !a.rank.Before(b.rank) {
			t.Errorf("%q does not sort ahead of %q", order[k-1].view(t), order[k].view(t))
		}
	}
	var a, b rankOf
	base(agentview.HostUnread()).decide(t).Accept(&a)
	with(base(agentview.HostUnread()), func(i *in) { i.asking = agentview.AskingOwner() }).decide(t).Accept(&b)
	if a.rank.Before(b.rank) || b.rank.Before(a.rank) {
		t.Error("an unseen reply that asks does not sort with the unseen ones")
	}
}
