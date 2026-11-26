package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/bus"
	"github.com/nwverse/persona/server/internal/rpc"
)

// goroutinesIn counts the goroutines whose stack (or whose starter) is in the function named.
func goroutinesIn(fn string) int {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	count := 0
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, fn) {
			count++
		}
	}
	return count
}

const queueEventFn = "(*BusHub).queueEvent"

// eventsWaiting is how many of the hub's events wait in b's line; takeWaiting takes the oldest out.
func eventsWaiting(b *BusHub) int { return b.events.len() }

func takeWaiting(t *testing.T, b *BusHub) busEvent {
	t.Helper()
	ev, ok := b.events.take()
	if !ok {
		t.Fatal("no event waits")
	}
	b.pending.Add(-1)
	return ev
}

// numbered is params for an event about conversation server/c1 that also says it is the nth queued (the handlers do
// not read n).
func numbered(n int) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"machine_id": "server", "session_id": "c1", "n": n, "at": time.Unix(int64(n), 0).UTC()})
	return raw
}

func numberOf(t *testing.T, ev busEvent) int {
	t.Helper()
	var p struct {
		N *int `json:"n"`
	}
	if err := json.Unmarshal(ev.params, &p); err != nil || p.N == nil {
		t.Fatalf("%s %s: no number (%v)", ev.name, ev.params, err)
	}
	return *p.N
}

// heldBus is a bus whose events are taken by takeEvents as in Serve, with no connection: the first event taken is
// held (held is closed then) until release, and every event taken is noted in the order it was.
type heldBus struct {
	b       *BusHub
	held    chan struct{}
	release func()
	cancel  context.CancelFunc
	ended   chan struct{} // closed once takeEvents returned

	mu    sync.Mutex
	taken []busEvent
	seen  chan struct{} // one per event taken after the first
}

func newHeldBus(t *testing.T) *heldBus {
	t.Helper()
	s := newTestService(t, newFakeHub())
	b := NewBusHub(BusOptions{Logf: testLogf(t)})
	b.setHello(Hello{}) // the hub's answer is in: events are not held back for it (waitHello)
	gate := make(chan struct{})
	h := &heldBus{b: b, held: make(chan struct{}), ended: make(chan struct{}), seen: make(chan struct{}, 4*eventsMost)}
	var once sync.Once
	h.release = func() { once.Do(func() { close(gate) }) }
	first := true
	b.taking = func(ev busEvent) {
		h.mu.Lock()
		h.taken = append(h.taken, ev)
		h.mu.Unlock()
		if first {
			first = false
			close(h.held)
			<-gate
			return
		}
		h.seen <- struct{}{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	b.mu.Lock()
	b.svc, b.ctx = s, ctx
	b.mu.Unlock()
	go func() {
		defer close(h.ended)
		b.takeEvents(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		h.release()
		<-h.ended
	})
	return h
}

// hold queues one event and waits until the consumer holds it: from then on nothing is taken until release.
func (h *heldBus) hold(t *testing.T) {
	t.Helper()
	h.b.queueEvent("managers", json.RawMessage(`{}`))
	select {
	case <-h.held:
	case <-time.After(10 * time.Second):
		t.Fatal("the first event was not taken")
	}
}

// wait waits until n more events were taken after the held one.
func (h *heldBus) wait(t *testing.T, n int) {
	t.Helper()
	deadline := time.After(30 * time.Second)
	for i := 0; i < n; i++ {
		select {
		case <-h.seen:
		case <-deadline:
			t.Fatalf("%d of %d events taken", i, n)
		}
	}
}

// fill queues n events that nothing reads ("managers": the bus's roster).
func fill(b *BusHub, n int) {
	for i := 0; i < n; i++ {
		b.queueEvent("managers", json.RawMessage(`{}`))
	}
}

// stateEvents are what the hub says of one conversation's state, each of which must be taken itself: its turn
// started, the owner looked at it (the hub's word, and an app's).
var stateEvents = []string{assistantwire.EventTurnStarted, assistantwire.EventSessionSeen, assistantwire.EventSeen}

// A full line starts no goroutine per event: what must each be taken waits in the line itself (#15906; each used to
// start a goroutine that blocked on the channel, with nothing bounding how many).
func TestBusFullLineStartsNoGoroutines(t *testing.T) {
	b := NewBusHub(BusOptions{Logf: testLogf(t)})
	fill(b, eventsRoom)
	const more = 300
	for i := 0; i < more; i++ {
		b.queueEvent(stateEvents[i%len(stateEvents)], numbered(i))
	}
	if n := goroutinesIn(queueEventFn); n != 0 {
		t.Fatalf("%d goroutines started by queueEvent, want none", n)
	}
	if n := eventsWaiting(b); n != eventsRoom+more {
		t.Fatalf("%d events waiting, want %d", n, eventsRoom+more)
	}
	if n := b.pending.Load(); n != eventsRoom+more {
		t.Fatalf("%d events counted as not taken, want %d", n, eventsRoom+more)
	}
}

// What the hub says of one conversation's state is handled in the order it came, however long the line was, and
// none of it is lost (#15906: past 1024 waiting, the events reached the line in the order the scheduler ran their
// goroutines).
func TestBusLongLineKeepsArrivalOrder(t *testing.T) {
	h := newHeldBus(t)
	h.hold(t)
	fill(h.b, eventsRoom) // the line is full: 1024 wait behind the one held
	const more = 300
	for i := 0; i < more; i++ {
		h.b.queueEvent(stateEvents[i%len(stateEvents)], numbered(i))
	}
	h.release()
	h.wait(t, eventsRoom+more)
	h.mu.Lock()
	taken := append([]busEvent(nil), h.taken...)
	h.mu.Unlock()
	var got []int
	for _, ev := range taken {
		if ev.name != "managers" {
			if want := stateEvents[len(got)%len(stateEvents)]; ev.name != want {
				t.Fatalf("event %d taken is %s, want %s", len(got), ev.name, want)
			}
			got = append(got, numberOf(t, ev))
		}
	}
	if len(got) != more {
		t.Fatalf("%d of the conversation's %d events taken", len(got), more)
	}
	for i, n := range got {
		if n != i {
			t.Fatalf("the conversation's events were taken out of order: number %d came %dth (%v…)", n, i, got[:min(len(got), i+8)])
		}
	}
	h.b.Settle()
	if n := h.b.pending.Load(); n != 0 {
		t.Fatalf("%d events counted as not taken after all were", n)
	}
}

// While the line is long, an event that says the whole of something takes the place of the one of its key that
// waits, and is handled where it came, not where the older one waited; under eventsRoom every event waits itself.
func TestBusLongLineKeepsTheNewestWhole(t *testing.T) {
	list := func(machine string, n int) json.RawMessage {
		raw, _ := json.Marshal(map[string]any{"machine_id": machine, "sessions": []any{}, "n": n})
		return raw
	}
	b := NewBusHub(BusOptions{Logf: testLogf(t)})
	b.queueEvent(assistantwire.EventSessionsChanged, list("mac", -2))
	b.queueEvent(assistantwire.EventSessionsChanged, list("mac", -1))
	if n := eventsWaiting(b); n != 2 {
		t.Fatalf("a short line: %d events waiting, want both lists", n)
	}
	fill(b, eventsRoom-2)
	// What comes now, in order: mac's list 50 times with a turn's start in the middle, another machine's list three
	// times, ten requests to report everything, the mutes five times.
	n := 0
	queue := func(event string, params func(int) json.RawMessage, times int) {
		for i := 0; i < times; i++ {
			b.queueEvent(event, params(n))
			n++
		}
	}
	onMac := func(n int) json.RawMessage { return list("mac", n) }
	queue(assistantwire.EventSessionsChanged, onMac, 25)
	queue(assistantwire.EventTurnStarted, numbered, 1) // number 25
	queue(assistantwire.EventSessionsChanged, onMac, 25)
	queue(assistantwire.EventSessionsChanged, func(n int) json.RawMessage { return list("win", n) }, 3)
	queue(assistantwire.EventResync, numbered, 10)
	queue(assistantwire.EventMutes, numbered, 5)
	if n := goroutinesIn(queueEventFn); n != 0 {
		t.Fatalf("%d goroutines started by queueEvent, want none", n)
	}
	for i := 0; i < eventsRoom; i++ {
		takeWaiting(t, b)
	}
	want := []struct {
		name string
		n    int
	}{
		{assistantwire.EventTurnStarted, 25},
		{assistantwire.EventSessionsChanged, 50}, // mac's newest, after the turn's start that came before it
		{assistantwire.EventSessionsChanged, 53}, // the other machine's newest
		{assistantwire.EventResync, 63},
		{assistantwire.EventMutes, 68},
	}
	if n := eventsWaiting(b); n != len(want) {
		t.Fatalf("%d events waiting past the first %d, want %d", n, eventsRoom, len(want))
	}
	for i, w := range want {
		ev := takeWaiting(t, b)
		if ev.name != w.name || numberOf(t, ev) != w.n {
			t.Fatalf("event %d: %s number %d, want %s number %d", i, ev.name, numberOf(t, ev), w.name, w.n)
		}
	}
	if n := b.pending.Load(); n != 0 {
		t.Fatalf("%d events counted as not taken after all were", n)
	}
}

// Once what takes the events has ended (Serve's ctx), nothing is left waiting on the line, and queueing still does
// not block (#15906: the goroutines that waited for room stayed blocked for the life of the process).
func TestBusNothingWaitsAfterTheConsumerEnds(t *testing.T) {
	h := newHeldBus(t)
	h.hold(t)
	fill(h.b, eventsRoom)
	for i := 0; i < 200; i++ {
		h.b.queueEvent(stateEvents[i%len(stateEvents)], numbered(i))
	}
	h.cancel()
	h.release()
	select {
	case <-h.ended:
	case <-time.After(10 * time.Second):
		t.Fatal("takeEvents did not return when its ctx ended")
	}
	for i := 0; i < 2*eventsMost; i++ { // more than the line keeps: none of them waits for room
		h.b.queueEvent(stateEvents[i%len(stateEvents)], numbered(i))
	}
	if n := goroutinesIn(queueEventFn); n != 0 {
		t.Fatalf("%d goroutines still waiting in queueEvent, want none", n)
	}
	if n := eventsWaiting(h.b); n > eventsMost+1 {
		t.Fatalf("%d events waiting, want at most %d", n, eventsMost+1)
	}
}

// The line keeps eventsMost events and no more: the ones that came then are counted in a note that waits where
// they would have, and what comes once there is room again waits behind the note.
func TestBusFullLineNotesWhatItDidNotKeep(t *testing.T) {
	b := NewBusHub(BusOptions{Logf: testLogf(t)})
	for i := 0; i < eventsMost+50; i++ {
		b.queueEvent(stateEvents[i%len(stateEvents)], numbered(i))
	}
	if n := eventsWaiting(b); n != eventsMost+1 {
		t.Fatalf("%d events waiting, want %d and the note", n, eventsMost)
	}
	if n := b.pending.Load(); n != eventsMost+1 {
		t.Fatalf("%d events counted as not taken, want %d", n, eventsMost+1)
	}
	for i := 0; i < 10; i++ {
		if ev := takeWaiting(t, b); numberOf(t, ev) != i {
			t.Fatalf("event %d taken is number %d", i, numberOf(t, ev))
		}
	}
	b.queueEvent(assistantwire.EventTurnStarted, numbered(-1)) // room again
	for i := 10; i < eventsMost; i++ {
		if ev := takeWaiting(t, b); numberOf(t, ev) != i {
			t.Fatalf("event %d taken is number %d", i, numberOf(t, ev))
		}
	}
	if note := takeWaiting(t, b); note.lost != 50 || note.name != "" {
		t.Fatalf("after the %d kept: %+v, want the note of 50 not kept", eventsMost, note)
	}
	if ev := takeWaiting(t, b); numberOf(t, ev) != -1 {
		t.Fatalf("behind the note: number %d", numberOf(t, ev))
	}
	if n := eventsWaiting(b); n != 0 {
		t.Fatalf("%d events still waiting", n)
	}
}

// Events the full line did not keep are made up for the way a connection that was away is: once everything before
// them is taken, the connection to the hub is dropped and the next one says hello again.
func TestBusEventsNotKeptStartANewConnection(t *testing.T) {
	hellos := make(chan struct{}, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		conn := bus.NewConn(r.Context(), ws)
		p := rpc.New("h", conn)
		p.OnRequest = func(_ context.Context, method string, _ json.RawMessage) (any, error) {
			if method == assistantwire.MethodHello {
				hellos <- struct{}{}
			}
			return map[string]any{}, nil
		}
		_ = p.Serve(r.Context(), conn)
	}))
	defer srv.Close()
	hello := func(what string) {
		t.Helper()
		select {
		case <-hellos:
		case <-time.After(10 * time.Second):
			t.Fatalf("no hello: %s", what)
		}
	}

	var logMu sync.Mutex
	var logged []string
	b := NewBusHub(BusOptions{Hub: srv.URL, Token: "jb_x", Backoff: 10 * time.Millisecond, Logf: func(format string, args ...any) {
		logMu.Lock()
		logged = append(logged, fmt.Sprintf(format, args...))
		logMu.Unlock()
	}})
	held, gate := make(chan struct{}), make(chan struct{})
	first := true
	b.taking = func(busEvent) {
		if first {
			first = false
			close(held)
			<-gate
		}
	}
	st := &memStore{}
	svc := New(Options{Hub: b, Store: func() Store { return st }, Logf: testLogf(t)})
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- b.Serve(ctx, svc) }()
	defer func() {
		cancel()
		<-served
	}()
	hello("the first connection")
	if err := b.Wait(ctx); err != nil {
		t.Fatal(err)
	}

	b.TellLocal("managers", json.RawMessage(`{}`))
	select {
	case <-held:
	case <-time.After(10 * time.Second):
		t.Fatal("the first event was not taken")
	}
	fill(b, eventsMost+3) // three more than the line keeps
	select {
	case <-hellos:
		t.Fatal("a new connection before the events in line were taken")
	default:
	}
	close(gate)
	hello("after the events that were not kept")
	if err := b.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	b.Settle()
	logMu.Lock()
	defer logMu.Unlock()
	if !strings.Contains(strings.Join(logged, "\n"), "3 event(s) of the hub's were not kept") {
		t.Fatalf("the log does not say what was not kept:\n%s", strings.Join(logged, "\n"))
	}
}
