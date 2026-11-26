package hub

import (
	"bytes"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

// The hub keeps a manager's hello capabilities up to maxManagerCapabilities, so a capability past the cut is dropped
// without a word and the feature it gates silently never starts.
func TestCapabilityListBounds(t *testing.T) {
	var in []string
	for i := range maxManagerCapabilities + 1 {
		in = append(in, fmt.Sprintf("cap.%d", i))
	}
	in = append([]string{"", "  ", strings.Repeat("x", 65), "cap.0"}, in...)
	in = append(in, "cap.0", fmt.Sprintf("cap.%d", maxManagerCapabilities), "  ", strings.Repeat("y", 65), fmt.Sprintf("cap.%d", maxManagerCapabilities))
	out, cut := capabilityList(in)
	if len(out) != maxManagerCapabilities || out[0] != "cap.0" || out[len(out)-1] != fmt.Sprintf("cap.%d", maxManagerCapabilities-1) {
		t.Fatalf("kept %d: first %q, last %q", len(out), out[0], out[len(out)-1])
	}
	// what is cut is only the well-formed names past the limit, each once: the ones the log names
	if want := []string{fmt.Sprintf("cap.%d", maxManagerCapabilities)}; !slices.Equal(cut, want) {
		t.Fatalf("cut %q, want %q", cut, want)
	}
	if _, cut := capabilityList(in[:maxManagerCapabilities]); len(cut) != 0 {
		t.Fatalf("a hello within the limit cut %q", cut)
	}
}

// The real manager's hello must fit with room to spare: at the limit, the next capability a change adds would be
// cut (#13941: a project-review manager's 33rd capability was, once main's own list reached 32).
func TestManagerHelloCapabilitiesFit(t *testing.T) {
	h, server, _ := newBusHub(t)
	dialAssistant(t, h, server.URL, false)
	var n int
	eventually(t, 5*time.Second, "the manager's hello", func() bool {
		h.bus.mu.Lock()
		defer h.bus.mu.Unlock()
		for _, c := range h.bus.current {
			if c != nil && len(c.presence.Capabilities) > 0 {
				n = len(c.presence.Capabilities)
				return true
			}
		}
		return false
	})
	if n+8 > maxManagerCapabilities {
		t.Fatalf("the manager's hello has %d capabilities and the hub keeps %d: raise maxManagerCapabilities", n, maxManagerCapabilities)
	}
}

// A hello past the limit is still accepted with what fits, and the hub says which capabilities it dropped, so the
// feature that never starts can be traced to the cut.
func TestManagerHelloLogsCutCapabilities(t *testing.T) {
	h, srv, _ := newBusHub(t)
	var logMu sync.Mutex
	var logs bytes.Buffer
	h.logger = log.New(lockedWriter{mu: &logMu, w: &logs}, "", 0)
	m := connectStateManager(t, h, srv, "m-cut")
	caps := []string{assistantwire.CapabilityConfigOwn}
	for i := range maxManagerCapabilities + 1 {
		caps = append(caps, fmt.Sprintf("cap.%d", i))
	}
	features := caps[1:]
	if err := m.call(t, busHello, map[string]any{"id": m.id, "capabilities": caps, "features": features}, nil); err != nil {
		t.Fatal(err)
	}
	logMu.Lock()
	got := logs.String()
	logMu.Unlock()
	want := fmt.Sprintf("bus: [m-cut] hello names %d capabilities and the hub keeps %d: dropped cap.%d, cap.%d",
		maxManagerCapabilities+2, maxManagerCapabilities, maxManagerCapabilities-1, maxManagerCapabilities)
	if !strings.Contains(got, want) {
		t.Fatalf("log %q lacks %q", got, want)
	}
	// the hello's module features go through the same bound, and their cut is told the same way
	want = fmt.Sprintf("bus: [m-cut] hello names %d features and the hub keeps %d: dropped cap.%d",
		maxManagerCapabilities+1, maxManagerCapabilities, maxManagerCapabilities)
	if !strings.Contains(got, want) {
		t.Fatalf("log %q lacks %q", got, want)
	}

	logMu.Lock()
	logs.Reset()
	logMu.Unlock()
	if err := m.call(t, busHello, map[string]any{"id": m.id, "capabilities": caps[:3]}, nil); err != nil {
		t.Fatal(err)
	}
	logMu.Lock()
	got = logs.String()
	logMu.Unlock()
	if strings.Contains(got, "dropped") {
		t.Fatalf("a hello within the limit logged a cut: %q", got)
	}
}
