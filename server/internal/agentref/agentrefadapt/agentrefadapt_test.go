package agentrefadapt_test

import (
	"testing"

	"github.com/nwverse/persona/server/internal/agentref"
	"github.com/nwverse/persona/server/internal/agentref/agentrefadapt"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/domainkit/defect"
	"github.com/nwverse/persona/server/internal/domainkit/domaintest"
	"github.com/nwverse/persona/server/internal/domainkit/opt"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestFromSession(t *testing.T) {
	ref := domaintest.Ok(t, agentrefadapt.FromSession(&proto.Session{MachineID: "mac", SessionID: "s1"}))
	if !ref.IsSame(domaintest.Ok(t, agentref.ParseRef("mac", "s1"))) {
		t.Error("FromSession is another session than the row names")
	}
	got := domaintest.Fails(t, agentrefadapt.FromSession(&proto.Session{SessionID: "s1"}))
	if want := "invalid agentref.machine.empty machine"; got != want {
		t.Errorf("a row without a machine: got %q, want %q", got, want)
	}
}

func TestFromCard(t *testing.T) {
	none := domaintest.Ok(t, agentrefadapt.FromCard(&proto.Interaction{ID: "card 1", MachineID: "mac"}))
	if !none.IsNone() {
		t.Error("a card with no session_id has an asker")
	}
	asker := domaintest.Ok(t, agentrefadapt.FromCard(&proto.Interaction{ID: "card 2", MachineID: "mac", SessionID: "s1"}))
	if asker != opt.Some(domaintest.Ok(t, agentref.ParseRef("mac", "s1"))) {
		t.Error("a card's asker is not the session it names")
	}
	got := domaintest.Fails(t, agentrefadapt.FromCard(&proto.Interaction{ID: "card 3", SessionID: "s1"}))
	if want := "invalid agentref.machine.empty machine"; got != want {
		t.Errorf("a card naming a session without its machine: got %q, want %q", got, want)
	}
}

func TestWireAndFromWireRoundTrip(t *testing.T) {
	wire := assistantwire.SessionRef{MachineID: "mac", SessionID: "s1"}
	ref := domaintest.Ok(t, agentrefadapt.FromWire(wire))
	if got := agentrefadapt.Wire(ref); got != wire {
		t.Errorf("Wire(FromWire(%+v)) = %+v", wire, got)
	}
	got := domaintest.Fails(t, agentrefadapt.FromWire(assistantwire.SessionRef{}))
	if want := "invalid agentref.machine.empty machine; invalid agentref.session.empty session"; got != want {
		t.Errorf("an empty reference: got %q, want %q", got, want)
	}
}

func TestLog(t *testing.T) {
	ref := domaintest.Ok(t, agentref.ParseRef("mac", "s1"))
	if got := agentrefadapt.Log(ref); got != "mac/s1" {
		t.Errorf("Log = %q, want %q", got, "mac/s1")
	}
}

// A zero Ref never reaches the wire or a log line as empty text: the session is absent (opt.Option) or it is a defect.
func TestWireAndLogRaiseOnTheZeroRef(t *testing.T) {
	var zero agentref.Ref
	for _, c := range []struct {
		name string
		use  func()
	}{
		{"Wire", func() { agentrefadapt.Wire(zero) }},
		{"Log", func() { agentrefadapt.Log(zero) }},
	} {
		func() {
			defer func() {
				r := recover()
				if d, ok := r.(defect.Defect); !ok || d.Error() != "agentref.Ref.Accept: the zero Ref" {
					t.Errorf("%s of the zero Ref raised %v, want the zero Ref's defect", c.name, r)
				}
			}()
			c.use()
		}()
	}
}
