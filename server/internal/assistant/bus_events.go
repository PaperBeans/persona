package assistant

import (
	"container/list"
	"encoding/json"
	"errors"
	"sync"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

// The hub's events wait in one line, in the order they came, and one goroutine takes them (takeEvents). Up to
// eventsRoom wait as they are. While more wait (what takes them is held up: a slow database, a slow call):
//   - what a later event or transcript read makes up for is dropped (a delta, a followed conversation's items,
//     or a page read that repairs inline view registrations);
//   - an event that says the whole of something (eventKey) takes the place of the one of its key that waits;
//   - every other event still waits in its turn, until eventsMost wait.
//
// Past eventsMost the line keeps no more and notes how many it did not keep (eventsLost).
const (
	eventsRoom = 1024
	eventsMost = 8192
)

// errEventsLost closes the connection to the hub after its events were not all kept (eventsLost).
var errEventsLost = errors.New("assistant: events of the hub's were not kept, reading everything again")

// eventQueue is the line the hub's events wait in. Putting one in never blocks and starts nothing: it is the read
// loop's, which also brings the answers the events' handlers wait for.
type eventQueue struct {
	mu   sync.Mutex
	line list.List // of busEvent, oldest first
	// keyed is, by eventKey, the event that waits past eventsRoom and that the next of its key replaces.
	keyed map[string]*list.Element
	note  *list.Element // the note of the events not kept (busEvent.lost), while it is the last in line
	wake  chan struct{} // holds one once something was put in: takeEvents waits on it when the line is empty
}

func newEventQueue() *eventQueue {
	return &eventQueue{keyed: map[string]*list.Element{}, wake: make(chan struct{}, 1)}
}

// take is the oldest event in line, taken out; false: none waits.
func (q *eventQueue) take() (busEvent, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	e := q.line.Front()
	if e == nil {
		return busEvent{}, false
	}
	ev := q.line.Remove(e).(busEvent)
	if ev.key != "" && q.keyed[ev.key] == e {
		delete(q.keyed, ev.key)
	}
	if q.note == e {
		q.note = nil
	}
	return ev, true
}

// len is how many events wait in line.
func (q *eventQueue) len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.line.Len()
}

// eventKey is the key of an event that says the whole of something, so that only the newest of its key matters: a
// machine's session list, the hub's hello, the mutes, a request to report everything again. "" for every other
// event: each is taken itself (a turn's start or end, a stop, a seen mark, a notice of how a message went).
func eventKey(event string, params json.RawMessage) string {
	switch event {
	case assistantwire.EventSessionsChanged:
		var p struct {
			MachineID string `json:"machine_id"`
		}
		if json.Unmarshal(params, &p) != nil {
			return "" // not a list: taken itself, and its handler says what is wrong with it
		}
		return event + "/" + p.MachineID
	case assistantwire.EventUpdated, assistantwire.EventResync, assistantwire.EventMutes:
		return event
	}
	return ""
}

// queueEvent puts one of the hub's events in line (the read loop's: it must not block). The text a followed
// conversation writes (session.delta) comes many times a second and only its latest matters: one per conversation
// waits at a time, the newest text replacing the older. What a long line does with the others is said above
// (eventsRoom): none of those that must each be taken is lost or passed by a later one until eventsMost wait.
func (b *BusHub) queueEvent(event string, params json.RawMessage) {
	if event == assistantwire.EventSessionDelta {
		var p struct {
			MachineID string `json:"machine_id"`
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(params, &p)
		key := p.MachineID + "/" + p.SessionID
		b.deltaMu.Lock()
		if b.deltas == nil {
			b.deltas = map[string]json.RawMessage{}
		}
		_, waiting := b.deltas[key]
		b.deltas[key] = params
		b.deltaMu.Unlock()
		if waiting {
			return // the one in line takes this text
		}
		params = json.RawMessage(`"` + key + `"`) // the key: the text is taken when it comes up (takeDelta)
	}
	ev := busEvent{name: event, params: params, conn: b.conns.Load()}
	if b.pending.Load() >= eventsRoom { // read only once the line is long: a list is large
		ev.key = eventKey(event, params)
	}
	q := b.events
	q.mu.Lock()
	n := q.line.Len()
	switch {
	case n < eventsRoom:
		q.line.PushBack(ev)
		b.pending.Add(1)
	case event == assistantwire.EventSessionDelta || event == assistantwire.EventSessionItems || event == assistantwire.EventInlineViewPage:
		q.mu.Unlock()
		if event == assistantwire.EventSessionDelta {
			b.takeDelta(params)
		}
		b.opts.Logf("assistant: %s dropped: too many events waiting", event)
		return
	case ev.key != "" && q.keyed[ev.key] != nil:
		// The older one is not handled: what came between them is handled first, then this one, as it came.
		q.line.Remove(q.keyed[ev.key])
		q.keyed[ev.key] = q.line.PushBack(ev)
	case n >= eventsMost:
		if q.note == nil || q.line.Back() != q.note {
			q.note = q.line.PushBack(busEvent{})
			b.pending.Add(1)
		}
		note := q.note.Value.(busEvent)
		note.lost, note.conn = note.lost+1, ev.conn
		q.note.Value = note
		if note.lost == 1 {
			b.opts.Logf("assistant: %s not kept: %d events waiting", event, n)
		}
	default:
		e := q.line.PushBack(ev)
		if ev.key != "" {
			q.keyed[ev.key] = e
		}
		b.pending.Add(1)
	}
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// eventsLost is taken where the line was full (note.lost events came then and were not kept, the last on connection
// note.conn), after everything that came before them. Blocking the read loop instead would hold back the answers
// the handlers wait for, so the line could never empty. What was lost is made up for the way a connection that was
// away is: the connection is dropped, and what the next one starts reads the lists, the interactions, the turns
// that ended and the hub's settings again (onHello), and the hub says again what it says after every handshake. A
// connection made since the loss did that already.
func (b *BusHub) eventsLost(note busEvent) {
	b.opts.Logf("assistant: %d event(s) of the hub's were not kept: more than %d waited", note.lost, eventsMost)
	if note.conn != b.conns.Load() {
		return
	}
	if p := b.client.Peer(); p != nil {
		p.Close(errEventsLost)
	}
}

// takeDelta is the latest text of the conversation a queued session.delta names (its key), taken out.
func (b *BusHub) takeDelta(key json.RawMessage) json.RawMessage {
	var k string
	_ = json.Unmarshal(key, &k)
	b.deltaMu.Lock()
	defer b.deltaMu.Unlock()
	p := b.deltas[k]
	delete(b.deltas, k)
	if p == nil {
		return json.RawMessage(`{}`)
	}
	return p
}
