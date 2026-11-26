package fleetcountadapt

import (
	"github.com/nwverse/persona/server/internal/agentviewwire"
	"github.com/nwverse/persona/server/internal/assistant/agentview"
	"github.com/nwverse/persona/server/internal/assistant/fleetcount"
	"github.com/nwverse/persona/server/internal/domainkit/instant"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
	"github.com/nwverse/persona/server/internal/domainkit/slot"
	"github.com/nwverse/persona/server/internal/proto"
)

// Views puts each session of a list its five-phase view, keeping its arrays from one list to the next; a caller keeps
// one and uses it from one goroutine at a time. The zero Views is ready.
type Views struct {
	mutes, built []bool
	agents       []fleetcount.Agent
	cards        []fleetcount.Card
	rows         viewRows
	out          viewWired
	shared       map[viewKey]*agentviewwire.AgentView
	// held and next: the hierarchy each row's hierarchy became with a shared view, the last list's and this one's. A
	// row's hierarchy is never written in place (it is shared by the row's copies), so the one made for a hierarchy and
	// a view serves every row that has them, this list and the next: a list told again allocates none.
	held, next map[heldKey]*proto.SessionHierarchy
}

// heldKey is a row's hierarchy and the shared view it gets.
type heldKey struct {
	was  *proto.SessionHierarchy
	view *agentviewwire.AgentView
}

// viewsSharedMax bounds how many views without text Views keeps to share between rows.
const viewsSharedMax = 256

// AttachViews is Views.Attach with arrays of its own.
func AttachViews(list []proto.Session, muted func(*proto.Session) bool, cards []proto.Interaction) {
	var v Views
	v.Attach(list, muted, cards)
}

// Attach puts each session's view on it (Session.SetView): its phase, why, whether its row counts in the Agents
// list's Needs you and its rank (docs/protocol/agent-states.md "Five phases"). Whether a row counts is fleetcount's
// Agents list (fleetcount.AgentsNeedsYou on SurfaceBadge) over list and cards, the pending cards as the apps see them;
// muted says whether the owner muted a session. A session fleetcount refuses (no machine or session id) gets no view.
// Only the view is written; the rows' other fields are read. Rows whose views carry no text share one value (a view
// is never changed in place).
func (v *Views) Attach(list []proto.Session, muted func(*proto.Session) bool, cards []proto.Interaction) {
	v.mutes, v.built = resize(v.mutes, len(list)), resize(v.built, len(list))
	agents := v.agents[:0]
	for i := range list {
		v.mutes[i] = muted(&list[i])
		a, failed := outcome.Unpack(Agent(&list[i], v.mutes[i], i))
		if v.built[i] = failed == nil; v.built[i] {
			agents = append(agents, a)
		}
	}
	fc := v.cards[:0]
	for j := range cards {
		if c, failed := outcome.Unpack(Card(&cards[j], false, j)); failed == nil {
			fc = append(fc, c)
		}
	}
	r := &v.rows
	r.cards, r.of, r.last = cards, resize(r.of, len(list)), -1
	fleetcount.AgentsNeedsYou(fleetcount.SurfaceBadge(), fleetcount.NewAgents(agents), fleetcount.NewCards(fc)).Accept(r)
	if v.next == nil {
		v.held, v.next = map[heldKey]*proto.SessionHierarchy{}, map[heldKey]*proto.SessionHierarchy{}
	}
	for i := range list {
		var view *agentviewwire.AgentView
		shared := false
		if v.built[i] {
			if s, failed := outcome.Unpack(ViewSession(&list[i], r.row(i, &list[i]), v.mutes[i])); failed == nil {
				view, shared = v.wire(agentview.Decide(s), &list[i])
			}
		}
		if !shared {
			list[i].SetView(view)
			continue
		}
		key := heldKey{was: list[i].Hierarchy, view: view}
		h, ok := v.next[key]
		if !ok {
			if h, ok = v.held[key]; !ok {
				list[i].SetView(view)
				h = list[i].Hierarchy
			}
			v.next[key] = h
		}
		list[i].Hierarchy = h
	}
	v.held, v.next = v.next, v.held
	clear(v.next)
	clear(agents)
	clear(fc)
	v.agents, v.cards, r.cards = agents[:0], fc[:0], nil // nothing of this list's sessions and cards is kept
}

// wire is a view as the wire carries it, and whether it is shared: one shared with every other row it is the same
// for when it carries no text.
func (v *Views) wire(view agentview.View, s *proto.Session) (*agentviewwire.AgentView, bool) {
	v.out = viewWired{s: s}
	view.Accept(&v.out)
	out, reason := v.out.out, v.out.reason
	v.out.s = nil
	if reason.Question != "" || reason.Code != "" || reason.Args != nil || !reason.ResetsAt.IsZero() {
		return pair(out, reason), false
	}
	key := viewKey{phase: out.Phase, kind: reason.Kind, n: reason.N, held: reason.Held, waiting: reason.Waiting,
		unseen: reason.Unseen, resumes: reason.Resumes, resumable: reason.Resumable, counted: out.Counted, rank: out.Rank}
	if shared, ok := v.shared[key]; ok {
		return shared, true
	}
	made := pair(out, reason)
	if len(v.shared) >= viewsSharedMax {
		return made, false
	}
	if v.shared == nil {
		v.shared = map[viewKey]*agentviewwire.AgentView{}
	}
	v.shared[key] = made
	return made, true
}

// viewKey is a view without text, as Views shares it.
type viewKey struct {
	phase                                              agentviewwire.AgentPhase
	kind                                               agentviewwire.AgentReasonKind
	n, rank                                            int
	held, waiting, unseen, resumes, resumable, counted bool
}

// pair is a view and its reason in one allocation.
func pair(out agentviewwire.AgentView, reason agentviewwire.AgentReason) *agentviewwire.AgentView {
	p := &struct {
		view   agentviewwire.AgentView
		reason agentviewwire.AgentReason
	}{view: out, reason: reason}
	p.view.Reason = &p.reason
	return &p.view
}

// resize is a's array holding n zero values, grown when it is shorter.
func resize[T any](a []T, n int) []T {
	if cap(a) < n {
		return make([]T, n)
	}
	a = a[:n]
	clear(a)
	return a
}

// viewRowOf is what fleetcount's Agents list says of one session: no row of its own, a row for its terminal's dialog, or
// one its cards stand for (how many, whether one is the owner's), and whether it counts.
type viewRowOf struct {
	kind    uint8 // 0 none, 1 dialog, 2 cards
	cards   int
	owners  bool
	counted bool
}

// viewRows gathers fleetcount's Agents list rows by the session each belongs to.
type viewRows struct {
	cards []proto.Interaction
	of    []viewRowOf
	last  int // the session of the row told last, for the cards under it; -1 for a card of no session
}

func (r *viewRows) VisitAgent(agent slot.Slot, why fleetcount.Why, _ instant.Instant) {
	i := int(agent.Wire())
	r.of[i], r.last = viewRowOf{kind: 1, counted: why.Counted()}, i
}

func (r *viewRows) VisitAgentByCard(agent, card slot.Slot, why fleetcount.Why) {
	i := int(agent.Wire())
	r.of[i], r.last = viewRowOf{kind: 2, cards: 1, owners: r.owners(card), counted: why.Counted()}, i
}

func (r *viewRows) VisitCard(slot.Slot, fleetcount.Why) { r.last = -1 } // a card of no session: no session's row

func (r *viewRows) VisitCardUnder(card slot.Slot) {
	if r.last >= 0 {
		r.of[r.last].cards++
		r.of[r.last].owners = r.of[r.last].owners || r.owners(card)
	}
}

func (r *viewRows) VisitTotal(fleetcount.RowCount) {}

// owners says whether a card is the owner's to answer.
func (r *viewRows) owners(card slot.Slot) bool {
	return holding(&r.cards[card.Wire()]) == fleetcount.HoldingForTheOwner()
}

// row is session i's Row: fleetcount's row for it, else its own cards (under another row, or counting nowhere), else
// none; a session whose every card its task's supervisor holds is the supervisor's.
func (r *viewRows) row(i int, s *proto.Session) agentview.Row {
	counting := agentview.CountingUncounted()
	if r.of[i].counted {
		counting = agentview.CountingCounted()
	}
	switch r.of[i].kind {
	case 1:
		return agentview.RowByDialog(counting)
	case 2:
		return agentview.RowByCards(agentview.ParseCardCount(r.of[i].cards), viewHolding(r.of[i].owners), counting)
	}
	n, owners, supervised := 0, false, 0
	for j := range r.cards {
		ix := &r.cards[j]
		if ix.SessionID != s.SessionID || ix.MachineID != s.MachineID {
			continue
		}
		n++
		switch holding(ix) {
		case fleetcount.HoldingForTheOwner():
			owners = true
		case fleetcount.HoldingHeldBySupervisor():
			supervised++
		}
	}
	switch {
	case n == 0:
		return agentview.RowNone()
	case supervised == n:
		return agentview.RowSupervised()
	}
	return agentview.RowElsewhere(agentview.ParseCardCount(n), viewHolding(owners))
}

func viewHolding(owners bool) agentview.Holding {
	if owners {
		return agentview.HoldingOwners()
	}
	return agentview.HoldingHeld()
}

// ViewSession is the session s is for its view, in row (fleetcount's row for it) and
// muted when the owner muted it.
func ViewSession(s *proto.Session, row agentview.Row, muted bool) outcome.Of[agentview.Session, agentview.Fail] {
	muting := agentview.MutingHeard()
	if muted {
		muting = agentview.MutingMuted()
	}
	asking, recency := agentview.AskingNothing(), agentview.RecencyEarlier()
	if s.AwaitsReply {
		asking = agentview.AskingOwner()
	}
	if s.Work != nil && s.Work.JustFinished {
		recency = agentview.RecencyJustFinished()
	}
	return agentview.NewSessionBuilder().
		Turn(viewHost(s), viewBackground(s)).
		Ending(viewFailure(s), asking, recency).
		Place(row, muting, viewResumability(s)).
		Build()
}

// viewHost is where the hub says the session's turn stands: its state, and its status for one the hub does not place
// (an interrupted one); a state from a newer peer is unknown.
func viewHost(s *proto.Session) agentview.Host {
	switch s.State {
	case proto.StateNeedsYou:
		return agentview.HostWaiting()
	case proto.StateWorking:
		return agentview.HostWorking()
	case proto.StateUnread:
		return agentview.HostUnread()
	case proto.StateIdle:
		return agentview.HostSeen()
	case proto.StateOffline:
		return agentview.HostOffline()
	}
	if s.Status == proto.StatusInterrupted {
		return agentview.HostInterrupted()
	}
	return agentview.HostUnknown()
}

func viewBackground(s *proto.Session) agentview.Background {
	switch {
	case !s.BackgroundWorking:
		return agentview.BackgroundNone()
	case s.BackgroundWaiting:
		return agentview.BackgroundWaiting()
	}
	return agentview.BackgroundRunning()
}

// viewFailure is the error that ended the session's start or its last turn: a start error first, then a turn error the
// hub keeps (only while the session is stopped); Persona's own Stop is no error.
func viewFailure(s *proto.Session) agentview.Failure {
	switch {
	case s.StartError != "" || s.StartErrorCode != "":
		return agentview.FailureStartRefused()
	case s.TurnError == "" || s.TurnErrorKind == proto.TurnErrorStopped:
		return agentview.FailureNone()
	case s.TurnErrorKind == proto.TurnErrorUsageLimit && s.TurnErrorResumes:
		return agentview.FailureUsageLimitResuming()
	case s.TurnErrorKind == proto.TurnErrorUsageLimit:
		return agentview.FailureUsageLimit()
	}
	return agentview.FailureModel()
}

// viewResumability: an interrupted session resumes with Continue unless its capabilities say it takes neither messages
// nor a terminal.
func viewResumability(s *proto.Session) agentview.Resumability {
	if c := s.Capabilities; c != nil && !c.Send && !c.Terminal {
		return agentview.ResumabilityNotResumable()
	}
	return agentview.ResumabilityResumable()
}
