package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/osproc"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/update"
)

func TestStampEvent(t *testing.T) {
	for in, want := range map[string]string{
		`{"type":"todos"}`: `{"event_seq":7,"type":"todos"}`,
		`{}`:               `{"event_seq":7}`,
		`[1]`:              `[1]`,
	} {
		if got := string(stampEvent([]byte(in), 7)); got != want {
			t.Errorf("stampEvent(%s) = %s, want %s", in, got, want)
		}
		var v any
		if err := json.Unmarshal(stampEvent([]byte(in), 7), &v); err != nil {
			t.Errorf("stampEvent(%s): not JSON: %v", in, err)
		}
	}
}

func TestEventLogSince(t *testing.T) {
	l := newEventLog()
	now := time.Now()
	for i := range 5 {
		l.add([]byte(fmt.Sprintf(`{"type":"t","i":%d}`, i)), false, now)
	}
	if missed, ok := l.since(l.stream, 5); !ok || len(missed) != 0 {
		t.Fatalf("up to date: %v %v", missed, ok)
	}
	missed, ok := l.since(l.stream, 2)
	if !ok || len(missed) != 3 || missed[0].Seq != 3 || !strings.HasPrefix(string(missed[0].Body), `{"event_seq":3,`) {
		t.Fatalf("missed 3..5: %v %v", missed, ok)
	}
	for name, c := range map[string]struct {
		stream string
		after  int64
	}{"other stream": {"x", 2}, "ahead": {l.stream, 9}, "no stream": {"", 2}} {
		if _, ok := l.since(c.stream, c.after); ok {
			t.Errorf("%s: resumed", name)
		}
	}
	// Fallen out of the log: no resume, the client refreshes.
	l.events = l.events[2:]
	if _, ok := l.since(l.stream, 1); ok {
		t.Fatal("resumed over a hole")
	}
	if _, ok := l.since(l.stream, 2); !ok {
		t.Fatal("the oldest kept event follows: resumable")
	}
	// Too much to catch up on: refresh.
	for range eventReplayMax + 1 {
		l.add([]byte(`{"type":"t"}`), false, now)
	}
	if _, ok := l.since(l.stream, 5); ok {
		t.Fatal("replayed more than a client's queue holds")
	}
	// Old events go.
	l.add([]byte(`{"type":"t"}`), false, now.Add(eventLogAge+time.Minute))
	if len(l.events) != 1 {
		t.Fatalf("aged events kept: %d", len(l.events))
	}
}

// A full log drops its oldest event on every add. It must keep the latest eventLogMax, in order and resumable, without
// copying them all each time: that was 2000 events' worth of copying per broadcast, under Hub.mu.
func TestEventLogFullDropsOldestCheaply(t *testing.T) {
	l := newEventLog()
	now := time.Now()
	body := []byte(`{"type":"sessions","title":"` + strings.Repeat("t", 200) + `"}`)
	for range eventLogMax {
		l.add(body, false, now)
	}
	const adds = 1000
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range adds {
		l.add(body, false, now)
	}
	runtime.ReadMemStats(&after)
	if per := (after.TotalAlloc - before.TotalAlloc) / adds; per > 8<<10 {
		t.Errorf("an add to a full log allocates %d bytes", per)
	}
	if len(l.events) != eventLogMax || cap(l.events) > 2*eventLogMax {
		t.Fatalf("log holds %d events in an array of %d", len(l.events), cap(l.events))
	}
	size := 0
	for i, e := range l.events {
		if e.Seq != l.seq-int64(eventLogMax-1-i) {
			t.Fatalf("events[%d].Seq = %d, latest %d", i, e.Seq, l.seq)
		}
		size += len(e.Body)
	}
	if size != l.bytes {
		t.Fatalf("bytes = %d, events hold %d", l.bytes, size)
	}
	missed, ok := l.since(l.stream, l.seq-10)
	if !ok || len(missed) != 10 || missed[0].Seq != l.seq-9 ||
		!strings.HasPrefix(string(missed[0].Body), `{"event_seq":`+strconv.FormatInt(l.seq-9, 10)+`,`) {
		t.Fatalf("resume over the last 10: %d events, ok %v", len(missed), ok)
	}
}

func TestHelloResumes(t *testing.T) {
	h := newTestHub(t)
	h.broadcast(map[string]any{"type": "todos"})
	h.broadcast(map[string]any{"type": "inbox"})
	h.broadcast(map[string]any{"type": "interaction", "interaction": proto.Interaction{ID: "f", Kind: proto.IxElicitation}})
	h.broadcast(map[string]any{"type": "lark"})
	read := func(c *client) []map[string]any {
		var out []map[string]any
		for {
			select {
			case b := <-c.out:
				var m map[string]any
				_ = json.Unmarshal(b, &m)
				out = append(out, m)
			default:
				return out
			}
		}
	}
	stream := h.events.stream
	c := &client{out: make(chan []byte, 512)}
	h.helloLocked(c, wsHello{Type: "hello"}, stream, 1, true)
	got := read(c)
	if len(got) != 3 || got[0]["type"] != "hello" || got[0]["resumed"] != true || got[0]["event_seq"] != float64(4) ||
		got[1]["type"] != "inbox" || got[1]["event_seq"] != float64(2) || got[2]["type"] != "lark" {
		t.Fatalf("resumed hello and replay (the form held back): %v", got)
	}
	c = &client{out: make(chan []byte, 512)}
	h.helloLocked(c, wsHello{Type: "hello"}, "another", 1, true)
	if got := read(c); len(got) != 1 || got[0]["resumed"] != false || got[0]["event_stream"] != stream {
		t.Fatalf("another stream: %v", got)
	}
}

func TestEventLogHandOver(t *testing.T) {
	url := os.Getenv("PERSONA_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PERSONA_TEST_DATABASE_URL not set")
	}
	database, err := db.Open(context.Background(), url, db.HubSet)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	a := newTestHub(t)
	a.store = database
	a.loadEventLog(ctx)
	a.broadcast(map[string]any{"type": "todos"})
	a.broadcast(map[string]any{"type": "inbox"})
	a.saveEventLog(ctx)

	b := newTestHub(t)
	b.store = database
	b.loadEventLog(ctx)
	if b.events.stream != a.events.stream || b.events.seq != 2 {
		t.Fatalf("not continued: %s/%d after %s/%d", b.events.stream, b.events.seq, a.events.stream, a.events.seq)
	}
	b.broadcast(map[string]any{"type": "lark"})
	if missed, ok := b.events.since(a.events.stream, 1); !ok || len(missed) != 2 || missed[1].Seq != 3 {
		t.Fatalf("the next hub replays across the restart: %v %v", missed, ok)
	}
	// Taken once: a hub that crashes later must not continue numbers it may have handed out since.
	c := newTestHub(t)
	c.store = database
	c.loadEventLog(ctx)
	if c.events.stream == a.events.stream {
		t.Fatal("the hand-over was read twice")
	}
}

// TestRestartIsPlanned runs a hub, stops it as systemd would and starts the next one: the app hears hub_restarting
// and a 1001 "hub restarting", and resumes its event stream on the next hub.
func TestRestartIsPlanned(t *testing.T) {
	url := os.Getenv("PERSONA_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PERSONA_TEST_DATABASE_URL not set")
	}
	database, err := db.Open(context.Background(), url, db.HubSet)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	fakeLark := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer fakeLark.Close()
	device, token := "restart-"+randomHex(8), "jd_"+randomHex(24)
	if err := database.CreateDeviceToken(context.Background(), db.DeviceToken{ID: device, Name: "test device", Kind: "phone", Role: roleFull}, hashToken(token)); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	dir := filepath.Join(home, ".persona")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PERSONA_HOME", "")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	ln, err := net.Listen("tcp", net.JoinHostPort(netip.AddrFrom4([4]byte{127, 0, 0, 1}).String(), "0"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	socket, err := ln.(*net.TCPListener).File()
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	addr := ln.Addr().String()
	cfg := Config{Listen: addr, PublicURL: "http://" + addr, Token: randomHex(24),
		DatabaseURL: url, Machines: []MachineConfig{}, ShutdownTimeoutSeconds: 4}
	raw, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(dir, "hub.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, stopTest := context.WithTimeout(context.Background(), 30*time.Second)
	defer stopTest()
	start := func() func() error {
		// systemd owns this listener across the restart. Give each real Run its own copy,
		// so nobody else can take the app's endpoint between the two generations.
		listener, err := osproc.FileListener(socket)
		if err != nil {
			t.Fatal(err)
		}
		inheritedListeners()
		inherited.mu.Lock()
		inherited.lns = append(inherited.lns, listener)
		inherited.mu.Unlock()
		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		var runErr error
		go func() {
			runErr = Run(runCtx, update.Build{Version: "restart-test"})
			if runErr != nil {
				t.Errorf("Run: %v", runErr)
				stopTest()
			}
			close(done)
		}()
		stop := func() error { cancel(); <-done; return runErr }
		t.Cleanup(func() {
			_ = stop()
			inherited.mu.Lock()
			inherited.lns = slices.DeleteFunc(inherited.lns, func(item net.Listener) bool { return item == listener })
			inherited.mu.Unlock()
			_ = listener.Close()
		})
		return stop
	}
	dial := func(ctx context.Context, query string) (*websocket.Conn, map[string]any) {
		for {
			conn, _, err := websocket.Dial(ctx, "ws://"+addr+"/api/v1/ws"+query, &websocket.DialOptions{
				HTTPHeader: http.Header{"Authorization": {"Bearer " + token}},
			})
			if err == nil {
				_, b, err := conn.Read(ctx)
				if err != nil {
					t.Fatal(err)
				}
				var hello map[string]any
				_ = json.Unmarshal(b, &hello)
				return conn, hello
			}
			if ctx.Err() != nil {
				t.Fatalf("dial: %v", err)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	stop := start()
	conn, hello := dial(ctx, "")
	stream, _ := hello["event_stream"].(string)
	seq, _ := hello["event_seq"].(float64)
	if stream == "" || hello["resumed"] != false {
		t.Fatalf("first hello: %v", hello)
	}
	heard := make(chan []string, 1)
	go func() {
		var types []string
		for {
			_, b, err := conn.Read(ctx)
			if err != nil {
				types = append(types, fmt.Sprintf("close %d %s", websocket.CloseStatus(err), closeReason(err)))
				heard <- types
				return
			}
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			if t, _ := m["type"].(string); t == "hub_restarting" {
				types = append(types, t)
			}
		}
	}()
	if err := stop(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := strings.Join(<-heard, ", "); got != "hub_restarting, close 1001 hub restarting" {
		t.Fatalf("the app heard: %s", got)
	}

	start()
	conn, hello = dial(ctx, "?event_stream="+stream+"&event_seq="+strconv.Itoa(int(seq)))
	defer conn.CloseNow()
	if hello["event_stream"] != stream || hello["resumed"] != true {
		t.Fatalf("the next hub did not continue the stream: %v (was %s)", hello, stream)
	}
}

func closeReason(err error) string {
	var ce websocket.CloseError
	if errors.As(err, &ce) {
		return ce.Reason
	}
	return ""
}

// TestListenInherited: a socket passed the way systemd does (fd 3, LISTEN_FDS, LISTEN_PID) is used for its port.
func TestListenInherited(t *testing.T) {
	if os.Getenv("PERSONA_TEST_LISTEN_CHILD") == "1" {
		os.Setenv("LISTEN_PID", strconv.Itoa(os.Getpid()))
		ln, activated, err := listenTCP(os.Getenv("PERSONA_TEST_LISTEN_ADDR"))
		if err != nil {
			fmt.Println("error", err)
			os.Exit(1)
		}
		fmt.Println("activated", activated, ln.Addr(), os.Getenv("LISTEN_FDS") == "")
		os.Exit(0)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	f, err := ln.(*net.TCPListener).File()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	addr := ln.Addr().String()
	cmd := exec.Command(os.Args[0], "-test.run=^TestListenInherited$")
	cmd.Env = append(os.Environ(), "PERSONA_TEST_LISTEN_CHILD=1", "PERSONA_TEST_LISTEN_ADDR="+addr, "LISTEN_FDS=1")
	cmd.ExtraFiles = []*os.File{f}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "activated true "+addr+" true") {
		t.Fatalf("child did not take the passed socket: %s", out)
	}
	// No socket passed: it listens itself.
	l2, activated, err := listenTCP("127.0.0.1:0")
	if err != nil || activated {
		t.Fatalf("plain listen: %v %v", activated, err)
	}
	l2.Close()
}
