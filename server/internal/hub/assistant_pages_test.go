package hub

import (
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/extwire"
)

// assistant_pages (Dashboards, #15017) is listed while the default manager (the one the extensions' places and the
// assistant's settings go to) is connected and named the capability in its hello: not for another manager's, not from
// an extension's report, not once it is gone. The apps show the Dashboards switch only then.
func TestAssistantPagesFollowsTheDefaultManager(t *testing.T) {
	h, srv, _ := newBusHub(t)
	a := connectStateManager(t, h, srv, assistantwire.AssistantID)
	b := connectStateManager(t, h, srv, "m-0d0d0d")
	hello := func(m *stateManager, pages bool) {
		t.Helper()
		caps := []string{assistantwire.CapabilityConfigOwn}
		if pages {
			caps = append(caps, assistantwire.CapabilityAssistantPages)
		}
		if err := m.call(t, busHello, map[string]any{"id": m.id, "capabilities": caps}, nil); err != nil {
			t.Fatal(err)
		}
	}
	listed := func() bool { return slices.Contains(h.features(), featureAssistantPages) }
	if listed() {
		t.Fatal("listed before any manager named it")
	}
	hello(b, true)
	if listed() {
		t.Fatal("listed for a manager that is not the default")
	}
	hello(a, true)
	if !listed() {
		t.Fatal("the default manager names it, and it is not listed")
	}

	// An extension's report cannot add it, though it adds its own features.
	hello(a, false)
	a.notify(t, extwire.EventReport, extwire.Report{Features: []string{featureAssistantPages, "fc"}})
	eventually(t, 5*time.Second, "the report", func() bool { return slices.Contains(h.features(), "fc") })
	if listed() {
		t.Fatal("an extension's report added it")
	}

	hello(a, true)
	if err := a.ws.CloseNow(); err != nil {
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, "the default manager offline", func() bool { return h.busConnOf(a.id) == nil })
	if listed() {
		t.Fatal("listed while the default manager is offline")
	}
}
