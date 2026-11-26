package hub

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/bus"
	"github.com/nwverse/persona/server/internal/proto"
)

// The counts view's relay (docs/protocol/counts.md "The hub's relay"; step 7b of docs/plans/plan-derived-counts.md).
// A manager that names assistantwire.CapabilityCountsView tells the hub its CountsView (assistantwire.EventCountsView)
// after each handshake and whenever its counts change. The hub is transport: it keeps the latest view of each
// manager's current connection as the manager wrote it, checks only that it is a JSON object of at most
// assistantwire.CountsViewMaxBytes with an epoch and a rev, never decodes counts, and passes it on:
//
//   - {"type":"counts","manager":<id>,"view":<the view>} when a manager tells a view that differs from its last one,
//     and for every kept view as an app connects (after machines, before the session lists) or names the feature in
//     a client_hello;
//   - {"type":"counts","manager":<id>,"view":null} when the connection of a manager that told a view ends.
//
// Only full-role apps that name proto.FeatureCountsView get them: a view lists every waiting session and card of a
// manager, so it reaches only what may read them all. Scoped and answer-only devices get none, whatever they ask.
// The messages are not in the event log: an app that connects again gets the kept views then. The rules that make the
// numbers are the manager's (server/internal/assistant/fleetcount); what an app keeps across a null view, a manager
// gone offline or a hub restart is the app's (spec/fixtures/counts/views.json, D13 of the plan).

func init() { handleBusEvent(assistantwire.EventCountsView, (*Hub).busCountsView) }

// busCountsView keeps the view manager c told, and passes it on when it differs from the one c told before. A view
// from a connection that is not its manager's current one, or whose hello did not name the capability, is dropped.
func (h *Hub) busCountsView(c *busConn, params json.RawMessage) {
	if c.ident.Role != bus.RoleManager {
		return
	}
	view, ok := keptCountsView(params)
	if !ok {
		return
	}
	// Under h.mu, as sendCountsViews: an app never gets a manager's view after a newer one.
	h.mu.Lock()
	defer h.mu.Unlock()
	h.bus.mu.Lock()
	keep := h.bus.current[c.ident.ID] == c && countsCapable(c) && !bytes.Equal(c.countsView, view)
	if keep {
		c.countsView = view
	}
	h.bus.mu.Unlock()
	if keep {
		h.queueCountsLocked(countsMessage(c.ident.ID, view))
	}
}

// keptCountsView is params, compacted, when the hub keeps it as a counts view: a JSON object of at most
// assistantwire.CountsViewMaxBytes whose epoch is a string other than "" and whose rev is a whole number of 0 or more.
// Nothing else of it is read: what counts and totals hold is the manager's and the apps'.
func keptCountsView(params json.RawMessage) (json.RawMessage, bool) {
	if len(params) > assistantwire.CountsViewMaxBytes {
		return nil, false
	}
	var top map[string]json.RawMessage // exact keys, as the apps read them
	if json.Unmarshal(params, &top) != nil || top == nil {
		return nil, false
	}
	var epoch *string
	var rev *uint64
	if json.Unmarshal(top["epoch"], &epoch) != nil || epoch == nil || *epoch == "" ||
		json.Unmarshal(top["rev"], &rev) != nil || rev == nil {
		return nil, false
	}
	var out bytes.Buffer
	if json.Compact(&out, params) != nil {
		return nil, false
	}
	return out.Bytes(), true
}

// countsViewEnded: manager c's connection ended. The apps that got a view of it hear view null, and keep the view they
// have (D13); the hub keeps nothing of a connection that ended.
func (h *Hub) countsViewEnded(c *busConn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.bus.mu.Lock()
	told := c.countsView != nil
	c.countsView = nil
	h.bus.mu.Unlock()
	if told {
		h.queueCountsLocked(countsMessage(c.ident.ID, nil))
	}
}

// sendCountsViews sends app c the view of every manager whose current connection told one, by manager id: as it
// connects, and when its client_hello names the feature.
func (h *Hub) sendCountsViews(c *client) {
	if !wantsCounts(c) {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, m := range h.countsMessages() {
		c.queue(m)
	}
}

// countsMessages are the counts messages of the views the managers' current connections told, by manager id.
func (h *Hub) countsMessages() [][]byte {
	type kept struct {
		manager string
		view    json.RawMessage
	}
	var views []kept
	h.bus.mu.Lock()
	for id, c := range h.bus.current {
		if c.countsView != nil && countsCapable(c) {
			views = append(views, kept{id, c.countsView})
		}
	}
	h.bus.mu.Unlock()
	slices.SortFunc(views, func(a, b kept) int { return strings.Compare(a.manager, b.manager) })
	out := make([][]byte, 0, len(views))
	for _, v := range views {
		out = append(out, countsMessage(v.manager, v.view))
	}
	return out
}

// queueCountsLocked queues a counts message for every app that takes them; the caller holds h.mu.
func (h *Hub) queueCountsLocked(message []byte) {
	for c := range h.clients {
		if wantsCounts(c) {
			c.queue(message)
		}
	}
}

// wantsCounts: app c takes counts messages, a full-role app that named the feature.
func wantsCounts(c *client) bool {
	return c.role == roleFull && c.wants(proto.FeatureCountsView)
}

// countsCapable: manager connection c's hello named the capability (the roster's counts_view); under h.bus.mu.
func countsCapable(c *busConn) bool {
	return slices.Contains(c.presence.Capabilities, assistantwire.CapabilityCountsView)
}

// countsRelay is the counts message an app gets: whose view it is, and the view as the manager told it (null: its
// connection ended).
type countsRelay struct {
	Type    string          `json:"type"`
	Manager string          `json:"manager"`
	View    json.RawMessage `json:"view"`
}

// countsMessage is the counts message of manager's view; a nil view is null.
func countsMessage(manager string, view json.RawMessage) []byte {
	b, _ := json.Marshal(countsRelay{Type: "counts", Manager: manager, View: view}) // a kept view is valid JSON
	return b
}
