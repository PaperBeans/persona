package assistant

import (
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestDirectSupervisionSideTranscriptConfirmsWithPushesReleased(t *testing.T) {
	s, f := newQuietServiceOn(t, &memStore{})
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	f.setSessions("server",
		proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "conv2", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
	)
	_, g := directSupervisionTask(t, s, f, "conv2", "Side review ready")
	setSettings(t, s, func(v *Settings) { v.Leaders = nil }) // an ordinary side conversation, not a direction lead
	tries := relayTries(f, "conv2")
	if !s.deliverDigest(t.Context(), s.settings(t.Context()), "conv2", []superviseSignal{g}, "", time.Now(), time.Time{}) {
		t.Fatal("side supervision was not delivered")
	}
	if s.queuedFor("conv2") != 0 || s.supervising("conv2") {
		t.Fatal("confirmed side supervision stayed queued or held pushes")
	}
	f.mu.Lock()
	reads, items := f.itemReads, len(f.sentItems["conv2"])
	send := f.sends[len(f.sends)-1]
	f.mu.Unlock()
	if send[holdPushesParam] != false || reads == 0 || items != 1 {
		t.Fatalf("side transcript confirmation: hold=%v reads=%d items=%d", send[holdPushesParam], reads, items)
	}
	if got := triesOf(f, tries); len(got) != 1 || got[0] == "" {
		t.Fatalf("side sends = %q, want one stable once-ID", got)
	}
	if pending := storedSupervisePending(t, s); len(pending) != 0 {
		t.Fatalf("confirmed side delivery retained pending outbox entries: %+v", pending)
	}
}
