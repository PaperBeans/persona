package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

// countsHub is a hub whose roster is kept in memory, served over plain HTTP on loopback.
func countsHub(t *testing.T) (*Hub, *httptest.Server) {
	t.Helper()
	h := newTestHub(t)
	h.bus.idents = newMemManagerStore()
	srv := httptest.NewServer(h.routes())
	t.Cleanup(srv.Close)
	t.Cleanup(func() { // before the server closes
		for _, c := range h.stopBus() {
			_ = c.ws.CloseNow()
		}
	})
	return h, srv
}

// countsManager connects manager id, its hello naming the counts view's capability when capable.
func countsManager(t *testing.T, h *Hub, srv *httptest.Server, id string, capable bool) *rawBus {
	t.Helper()
	capabilities := []string{assistantwire.CapabilityConfigOwn}
	if capable {
		capabilities = append(capabilities, assistantwire.CapabilityCountsView)
	}
	r := dialRawBus(t, srv, busToken(t, h, id, id))
	if err := r.call(assistantwire.MethodHello, map[string]any{"id": id, "capabilities": capabilities}, nil); err != nil {
		t.Fatal(err)
	}
	return r
}

// tellCounts has manager r tell view, and returns once the hub took it: a connection's events and calls are taken in
// order, so the ping's answer comes after the view was handled.
func tellCounts(t *testing.T, r *rawBus, view json.RawMessage) {
	t.Helper()
	if err := r.peer.Notify(assistantwire.EventCountsView, view); err != nil {
		t.Fatal(err)
	}
	if err := r.call("ping", map[string]any{}, nil); err != nil {
		t.Fatal(err)
	}
}

// countsApp is an app on h as the hub's queue has it: its role and what it named in the WebSocket URL's ?features=.
func countsApp(h *Hub, role string, features ...string) *client {
	c := &client{device: "device-" + role, role: role, out: make(chan []byte, 64), urlFeatures: map[string]bool{}}
	for _, f := range features {
		c.urlFeatures[f] = true
	}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	return c
}

// countsQueued are the counts messages queued for app c since the last look, in order; its other messages are
// dropped. The queue is what the hub hands the app's writer, so a scoped device's writer allowlist plays no part.
func countsQueued(c *client) []string {
	var out []string
	for {
		select {
		case b := <-c.out:
			if bytes.HasPrefix(b, []byte(`{"type":"counts",`)) {
				out = append(out, string(b))
			}
		default:
			return out
		}
	}
}

// rosterEntries is GET /api/v1/managers as the apps read it, by manager id.
func rosterEntries(t *testing.T, h *Hub, srv *httptest.Server) map[string]map[string]any {
	t.Helper()
	var out struct {
		Managers []map[string]any `json:"managers"`
	}
	if code := managersDo(t, h, srv, http.MethodGet, "/api/v1/managers", nil, &out); code != http.StatusOK {
		t.Fatalf("GET /api/v1/managers: %d", code)
	}
	byID := map[string]map[string]any{}
	for _, m := range out.Managers {
		byID[m["id"].(string)] = m
	}
	return byID
}

const workView = `{"epoch":"b7e1c2a9","rev":5,"as_of":1795593605000,"build":"1.0.0",` +
	`"counts":{"agents_needs_you":{"n":2,"keys":[{"id":"agent:studio/s1","cards":["ix-plan"]},{"id":"card:ix-a","cards":["ix-a"]}]}},` +
	`"totals":{"agents_needs_you":2}}`

// hangUp closes manager r's connection and returns the counts messages app c got until the hub had handled the end:
// the hub tells the apps that asked for "managers" the roster changed after it sent the counts message, if any.
func hangUp(t *testing.T, r *rawBus, c *client) []string {
	t.Helper()
	_ = r.ws.Close(websocket.StatusNormalClosure, "restarting")
	<-r.ended
	var got []string
	deadline := time.After(5 * time.Second)
	for {
		select {
		case b := <-c.out:
			if bytes.HasPrefix(b, []byte(`{"type":"counts",`)) {
				got = append(got, string(b))
			}
			if string(b) == `{"type":"managers"}` {
				return got
			}
		case <-deadline:
			t.Fatal("no managers push after the manager hung up")
			return nil
		}
	}
}

// The hub keeps a capable manager's view as it was told and relays it, unchanged and under the manager's id, to the
// full-role apps that named the feature, and to no one else: an app that did not ask, a scoped device and an
// answer-only device that asked get nothing queued. The roster's counts_view says which managers tell one. The same
// view again, and a view from a manager whose hello did not name the capability, relay nothing; when the connection
// ends, the apps hear view null and the roster entry loses the flag, and the end of a manager that told no view tells
// them nothing.
func TestTheHubRelaysCountsViewsToFullAppsThatAsk(t *testing.T) {
	h, srv := countsHub(t)
	full := countsApp(h, roleFull, proto.FeatureCountsView, "managers")
	unasked := countsApp(h, roleFull)
	scoped := countsApp(h, roleScoped, proto.FeatureCountsView)
	answer := countsApp(h, roleAnswer, proto.FeatureCountsView)
	work := countsManager(t, h, srv, "m-work", true)
	old := countsManager(t, h, srv, "m-old", false)

	roster := rosterEntries(t, h, srv)
	if roster["m-work"]["counts_view"] != true {
		t.Fatalf("a connected manager that names the capability: %v", roster["m-work"])
	}
	if _, has := roster["m-old"]["counts_view"]; has {
		t.Fatalf("a connected manager that does not name it: %v", roster["m-old"])
	}

	tellCounts(t, work, json.RawMessage(workView))
	want := `{"type":"counts","manager":"m-work","view":` + workView + `}`
	if got := countsQueued(full); !slices.Equal(got, []string{want}) {
		t.Fatalf("the full app that asked got %q, want %q", got, want)
	}
	for name, c := range map[string]*client{"an app that did not ask": unasked, "a scoped device": scoped, "an answer-only device": answer} {
		if got := countsQueued(c); len(got) != 0 {
			t.Errorf("%s got %q", name, got)
		}
	}

	tellCounts(t, work, json.RawMessage(workView))
	tellCounts(t, old, json.RawMessage(workView))
	if got := countsQueued(full); len(got) != 0 {
		t.Fatalf("the same view again, or one from a manager without the capability, relayed %q", got)
	}
	newer := strings.Replace(workView, `"rev":5`, `"rev":6`, 1)
	tellCounts(t, work, json.RawMessage(newer))
	if got := countsQueued(full); !slices.Equal(got, []string{`{"type":"counts","manager":"m-work","view":` + newer + `}`}) {
		t.Fatalf("a newer view relayed %q", got)
	}

	if got := hangUp(t, work, full); !slices.Equal(got, []string{`{"type":"counts","manager":"m-work","view":null}`}) {
		t.Fatalf("on disconnect the full app got %q", got)
	}
	for name, c := range map[string]*client{"an app that did not ask": unasked, "a scoped device": scoped, "an answer-only device": answer} {
		if got := countsQueued(c); len(got) != 0 {
			t.Errorf("%s got %q", name, got)
		}
	}
	gone := rosterEntries(t, h, srv)["m-work"]
	if _, has := gone["counts_view"]; has || gone["online"] != false {
		t.Fatalf("an offline manager's entry: %v", gone)
	}
	if got := hangUp(t, old, full); len(got) != 0 {
		t.Fatalf("a manager that never told a view went, and the apps got %q", got)
	}
}

// What the hub keeps of a counts view: a JSON object of at most CountsViewMaxBytes with a non-empty string epoch and a
// whole rev of 0 or more, compacted. Nothing else is read, so counts it cannot make sense of are the apps' business.
func TestTheHubKeepsOnlyAViewWithItsEpochAndRev(t *testing.T) {
	sized := func(n int) string { // a view of exactly n bytes
		head := `{"epoch":"e","rev":0,"pad":"`
		return head + strings.Repeat("x", n-len(head)-2) + `"}`
	}
	for _, c := range []struct {
		in, want string
	}{
		{`{ "epoch": "e1", "rev": 0, "counts": {} }`, `{"epoch":"e1","rev":0,"counts":{}}`},
		{`{"epoch":"e1","rev":12,"counts":"not read"}`, `{"epoch":"e1","rev":12,"counts":"not read"}`},
		{sized(assistantwire.CountsViewMaxBytes), sized(assistantwire.CountsViewMaxBytes)},
	} {
		got, ok := keptCountsView(json.RawMessage(c.in))
		if !ok || string(got) != c.want {
			t.Errorf("keptCountsView(%.60s) = %.60s, %v; want %.60s", c.in, got, ok, c.want)
		}
	}
	for _, in := range []string{
		sized(assistantwire.CountsViewMaxBytes + 1),
		`null`, `[]`, `"view"`, `{}`, `{"epoch":"e1"}`, `{"rev":1}`,
		`{"epoch":"","rev":1}`, `{"epoch":null,"rev":1}`, `{"epoch":7,"rev":1}`, `{"Epoch":"e1","rev":1}`,
		`{"epoch":"e1","rev":null}`, `{"epoch":"e1","rev":-1}`, `{"epoch":"e1","rev":1.5}`, `{"epoch":"e1","rev":"1"}`,
		`{"epoch":"e1","rev":1`,
	} {
		if got, ok := keptCountsView(json.RawMessage(in)); ok {
			t.Errorf("keptCountsView(%.60s) kept %.60s", in, got)
		}
	}
}

// The hub lists counts_view always, whatever the managers are: a hub without a manager connected lists it too.
func TestTheHubListsCountsViewAlways(t *testing.T) {
	h := newTestHub(t)
	if !slices.Contains(h.features(), proto.FeatureCountsView) {
		t.Fatalf("features() without a manager: %v", h.features())
	}
}

// connectFrames dials the hub's WebSocket with the master token and ?features=features, and reads the connect
// sequence: every frame up to and with the desktops list, which ends it.
func connectFrames(t *testing.T, h *Hub, srv *httptest.Server, features string) (*websocket.Conn, []map[string]json.RawMessage) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	conn, _, err := websocket.Dial(ctx, srv.URL+"/api/v1/ws?token="+h.cfg.Token+"&features="+features, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	var frames []map[string]json.RawMessage
	for {
		frame := readFrame(t, ctx, conn)
		frames = append(frames, frame)
		if frameType(frame) == "desktops" {
			return conn, frames
		}
	}
}

func readFrame(t *testing.T, ctx context.Context, conn *websocket.Conn) map[string]json.RawMessage {
	t.Helper()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var frame map[string]json.RawMessage
	if err := json.Unmarshal(data, &frame); err != nil {
		t.Fatalf("%v: %s", err, data)
	}
	return frame
}

func frameType(frame map[string]json.RawMessage) string {
	var typ string
	_ = json.Unmarshal(frame["type"], &typ)
	return typ
}

func frameTypes(frames []map[string]json.RawMessage) []string {
	out := make([]string, 0, len(frames))
	for _, f := range frames {
		out = append(out, frameType(f))
	}
	return out
}

// An app that names the feature gets the kept views as it connects: after the hello and the machines, before the
// session lists they count, one message per manager by id. Without the feature it gets none.
func TestAnAppGetsTheKeptViewsAsItConnects(t *testing.T) {
	h, srv := countsHub(t)
	m := newMachine(h, MachineConfig{ID: "box", Name: "Box", Kind: "local"})
	h.machines, h.byID["box"] = []*Machine{m}, m
	tellCounts(t, countsManager(t, h, srv, "m-work", true), json.RawMessage(workView))
	homeView := strings.Replace(workView, "b7e1c2a9", "4f0d9e21", 1)
	tellCounts(t, countsManager(t, h, srv, "m-home", true), json.RawMessage(homeView))

	_, frames := connectFrames(t, h, srv, proto.FeatureCountsView)
	if got, want := frameTypes(frames), []string{"hello", "machines", "counts", "counts", "sessions", "desktops"}; !slices.Equal(got, want) {
		t.Fatalf("connect sequence %v, want %v", got, want)
	}
	for i, want := range []struct{ manager, view string }{{"m-home", homeView}, {"m-work", workView}} {
		f := frames[2+i]
		if string(f["manager"]) != `"`+want.manager+`"` || string(f["view"]) != want.view {
			t.Errorf("counts message %d: %s %s, want %s's view", i, f["manager"], f["view"], want.manager)
		}
	}
	_, frames = connectFrames(t, h, srv, "")
	if got, want := frameTypes(frames), []string{"hello", "machines", "sessions", "desktops"}; !slices.Equal(got, want) {
		t.Fatalf("connect sequence without the feature %v, want %v", got, want)
	}
}

// An app that names the feature in a client_hello gets the views kept so far then, once.
func TestAClientHelloThatNamesTheFeatureGetsTheKeptViews(t *testing.T) {
	h, srv := countsHub(t)
	app := countsApp(h, roleFull)
	tellCounts(t, countsManager(t, h, srv, "m-work", true), json.RawMessage(workView))
	if got := countsQueued(app); len(got) != 0 {
		t.Fatalf("before it asked: %q", got)
	}
	hello := []byte(`{"type":"client_hello","features":["` + proto.FeatureCountsView + `"]}`)
	h.onClientMessage(context.Background(), app, hello)
	want := `{"type":"counts","manager":"m-work","view":` + workView + `}`
	if got := countsQueued(app); !slices.Equal(got, []string{want}) {
		t.Fatalf("after its client_hello: %q, want %q", got, want)
	}
	h.onClientMessage(context.Background(), app, hello)
	if got := countsQueued(app); len(got) != 0 {
		t.Fatalf("a second client_hello with the feature: %q", got)
	}
}

// viewsFixture is spec/fixtures/counts/views.json as the hub's tests read it, each view as its JSON.
type viewsFixture struct {
	Version int         `json:"version"`
	Cases   []viewsCase `json:"cases"`
}

type viewsCase struct {
	Name  string `json:"name"`
	Given struct {
		Kept []viewsKept `json:"kept"`
	} `json:"given"`
	When struct {
		Event   string          `json:"event"`
		Hub     string          `json:"hub"`
		Manager string          `json:"manager"`
		View    json.RawMessage `json:"view"`
	} `json:"when"`
	Then struct {
		Kept   []viewsKept    `json:"kept"`
		Totals map[string]int `json:"totals"`
	} `json:"then"`
}

type viewsKept struct {
	Hub     string          `json:"hub"`
	Manager string          `json:"manager"`
	View    json.RawMessage `json:"view"`
}

func readViewsFixture(t *testing.T) viewsFixture {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "spec", "fixtures", "counts", "views.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f viewsFixture
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	if f.Version != 1 || len(f.Cases) == 0 {
		t.Fatalf("views.json: version %d, %d cases", f.Version, len(f.Cases))
	}
	return f
}

// compactJSON is raw without the fixture's whitespace, as the hub relays it.
func compactJSON(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// keptJSON is a case's kept views, compacted, for comparing two lists.
func keptJSON(t *testing.T, kept []viewsKept) []string {
	t.Helper()
	out := make([]string, 0, len(kept))
	for _, k := range kept {
		out = append(out, k.Hub+" "+k.Manager+" "+compactJSON(t, k.View))
	}
	return out
}

// The kept views' restart cases (spec/fixtures/counts/views.json, D13 of docs/plans/plan-derived-counts.md), replayed
// through the hub. A hub that just restarted has no view: a device that connects to it gets the hello, which still
// lists counts_view, and no counts message, while the roster still lists every manager it kept a view of, offline and
// without the flag. So the restart is the device's only event, and each case's then keeps what its given kept: the
// views an old device has, and none, the fallback, on a new one. When a manager connects again and tells its view,
// the device gets it as the manager told it, which is the reconnect case's when.
func TestTheHubReplaysTheRestartCases(t *testing.T) {
	f := readViewsFixture(t)
	var restarts []viewsCase
	var reconnect *viewsCase
	for i, c := range f.Cases {
		switch {
		case c.When.Event == "restarted":
			restarts = append(restarts, c)
		case c.Name == "the_managers_reconnect_after_a_hub_restart":
			reconnect = &f.Cases[i]
		}
	}
	if !slices.ContainsFunc(restarts, func(c viewsCase) bool { return c.Name == "a_new_device_after_a_hub_restart_shows_the_fallback" }) ||
		reconnect == nil || reconnect.When.Event != "arrived" {
		t.Fatalf("views.json lacks the restart cases this test replays: %d restarted, reconnect %v", len(restarts), reconnect)
	}
	for _, c := range restarts {
		t.Run(c.Name, func(t *testing.T) {
			h, srv := countsHub(t) // just restarted: no manager has connected again
			for _, k := range c.Given.Kept {
				if k.Hub == c.When.Hub {
					busToken(t, h, k.Manager, k.Manager) // the roster outlives the restart
				}
			}
			_, frames := connectFrames(t, h, srv, proto.FeatureCountsView)
			var features []string
			if err := json.Unmarshal(frames[0]["features"], &features); err != nil || !slices.Contains(features, proto.FeatureCountsView) {
				t.Fatalf("the hello after the restart does not list %s: %s", proto.FeatureCountsView, frames[0]["features"])
			}
			if types := frameTypes(frames); slices.Contains(types, "counts") {
				t.Fatalf("the restarted hub sent counts: %v", types)
			}
			roster := rosterEntries(t, h, srv)
			for _, k := range c.Given.Kept {
				if k.Hub != c.When.Hub {
					continue
				}
				e, listed := roster[k.Manager]
				if _, flagged := e["counts_view"]; !listed || e["online"] != false || flagged {
					t.Fatalf("%s after the restart: %v (listed %v)", k.Manager, e, listed)
				}
			}
			if got, want := keptJSON(t, c.Then.Kept), keptJSON(t, c.Given.Kept); !slices.Equal(got, want) {
				t.Fatalf("the case keeps %q after the restart, but the hub sent nothing that changes %q", got, want)
			}
		})
	}
	t.Run(reconnect.Name, func(t *testing.T) {
		h, srv := countsHub(t)
		conn, _ := connectFrames(t, h, srv, proto.FeatureCountsView)
		tellCounts(t, countsManager(t, h, srv, reconnect.When.Manager, true), reconnect.When.View)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		frame := readFrame(t, ctx, conn)
		for frameType(frame) != "counts" {
			frame = readFrame(t, ctx, conn)
		}
		if string(frame["manager"]) != `"`+reconnect.When.Manager+`"` || string(frame["view"]) != compactJSON(t, reconnect.When.View) {
			t.Fatalf("the device got %s %s, want the case's view of %s", frame["manager"], frame["view"], reconnect.When.Manager)
		}
		want := []string{reconnect.When.Hub + " " + reconnect.When.Manager + " " + compactJSON(t, reconnect.When.View)}
		if got := keptJSON(t, reconnect.Then.Kept); !slices.Equal(got, want) {
			t.Fatalf("the case keeps %q, want the view the hub relayed: %q", got, want)
		}
		if e := rosterEntries(t, h, srv)[reconnect.When.Manager]; e["counts_view"] != true || e["online"] != true {
			t.Fatalf("the reconnected manager's entry: %v", e)
		}
	})
}
