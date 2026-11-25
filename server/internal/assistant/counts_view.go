package assistant

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"time"

	"github.com/nwverse/persona/server/internal/assistant/fleetcount"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/link"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// The counts view (docs/protocol/counts.md, step 7a of docs/plans/plan-derived-counts.md): the counts people see, as
// this manager works them out, told the hub (counts.view) for the clients to draw instead of counting themselves.
// liveLoop makes it in each round from the agents and cards the Live Activity's state and Persona Link's summary are
// made from, after the state is told, so the three agree and a badge is never ahead of the lists the round published.
// Which rows count is fleetcount's (the Agents list's surface); the view's shape, its row keys and the words for why a
// row is held are fleetcountadapt's; what this file adds is the view's place in this run: its epoch, rev, as_of and
// build, and when it is told.

// linkAndCounts is linkAttention and the counts view of the same agents and cards, decided on one fill of the fleet:
// liveLoop's round. The view is fleetcount's Agents list tally of them, written by its adapter, without the epoch,
// rev, as_of and build (tellCounts).
func (r *fleetRound) linkAndCounts(l i18n.Lang, agents []fleetInput, ixs []proto.Interaction, told map[string]bool) (link.Attention, assistantwire.CountsView) {
	r.fillLink(agents, ixs, told)
	defer r.letLinkGo()
	needsYou := fleetcount.AgentsNeedsYou(fleetcount.SurfaceBadge(), r.link.Agents(), r.link.Cards())
	return r.link.Link(string(l), sessionTitle), r.link.View(needsYou)
}

// countsTeller is what liveLoop keeps of the counts view between rounds: this run's epoch, the rev of the last view
// made, its counts and totals as JSON (to tell a new view from a repeat) and the view as told. A view goes up a rev
// only when its counts or totals change; one the hub was not given (it was away, or it connected again since) is told
// again as it was.
type countsTeller struct {
	epoch assistantwire.CountsEpoch
	rev   int
	last  []byte       // the last view's counts and totals
	next  bytes.Buffer // this round's
	enc   *json.Encoder
	part  countsPart
	view  []byte
}

// countsPart is the part of a view that says what it counts.
type countsPart struct {
	Counts assistantwire.CountsViewCounts  `json:"counts"`
	Totals *assistantwire.CountsViewTotals `json:"totals,omitempty"`
}

// tellCounts tells the hub this round's counts view: a new rev when its counts changed, as_of now, and the view the
// hub was last told again when it was not given it yet (liveChanged(true) after a handshake). A round whose counts did
// not change tells nothing.
func (s *Service) tellCounts(v assistantwire.CountsView) {
	l := &s.fleet.live
	t := &l.counts
	if t.enc == nil {
		t.epoch, t.enc = assistantwire.CountsEpoch(rand.Text()), json.NewEncoder(&t.next)
	}
	t.next.Reset()
	t.part = countsPart{Counts: v.Counts, Totals: v.Totals}
	err := t.enc.Encode(&t.part)
	t.part = countsPart{}
	if err != nil {
		s.logf("counts: the view does not encode: %v", err)
		return
	}
	if t.view == nil || !bytes.Equal(t.next.Bytes(), t.last) {
		t.rev++
		v.Epoch, v.Rev, v.AsOf, v.Build = t.epoch, t.rev, time.Now().UnixMilli(), s.build
		b, err := json.Marshal(v)
		if err != nil {
			s.logf("counts: the view does not encode: %v", err)
			return
		}
		t.view, t.last = b, append(t.last[:0], t.next.Bytes()...)
		l.mu.Lock()
		l.countsTold = false
		l.mu.Unlock()
	}
	l.mu.Lock()
	told := l.countsTold
	l.mu.Unlock()
	if told {
		return
	}
	if s.hub.Tell(assistantwire.EventCountsView, rpc.Marshaled(t.view)) == nil { // json.Marshal's bytes, as they are
		l.mu.Lock()
		l.countsTold = true
		l.mu.Unlock()
	}
}
