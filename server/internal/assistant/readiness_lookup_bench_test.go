package assistant

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// largeInventory is a service whose computer "box" holds 8200 sessions, the main conversation "session-00000" among
// them.
func largeInventory() *Service {
	f := newFakeHub()
	f.hello.Assistant.MachineID = "box"
	f.hello.Assistant.Machines = []string{"box"}
	f.devices = []Device{{ID: "box", Home: "/home/u"}}
	d := newFleetDevs()
	d.add("box", "Computer")
	st := newFleetMem()
	s := New(Options{Hub: f, Devices: d, Store: func() Store { return st }})
	list := make([]proto.Session, 8200)
	for i := range list {
		list[i] = proto.Session{SessionID: fmt.Sprintf("session-%05d", i), Cwd: "/code/example", Agent: "claude",
			Status: proto.StatusDone, Title: "Example task", LastText: strings.Repeat("Example result. ", 32)}
		if i < 14 {
			list[i].Cwd = "/home/u/.persona/assistant"
		}
	}
	d.setSessions("box", list)
	return s
}

func BenchmarkReadinessMainLargeInventory(b *testing.B) {
	s := largeInventory()
	ctx := context.Background()
	if _, ok := s.readinessMain(ctx, "box", "session-00000"); !ok {
		b.Fatal("main session not found")
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, ok := s.readinessMain(ctx, "box", "session-00000"); !ok {
			b.Fatal("main session not found")
		}
	}
}

// Readiness reads the one main conversation, not the computer's whole inventory: about 4.7 KB a lookup measured,
// where a copy of the 8200 sessions alone is 11.7 MB.
func TestReadinessMainSkipsInventory(t *testing.T) {
	if raceEnabled {
		t.Skip("race instrumentation allocates")
	}
	s := largeInventory()
	ctx := context.Background()
	if _, ok := s.readinessMain(ctx, "box", "session-00000"); !ok {
		t.Fatal("main session not found")
	}
	const runs = 500
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range runs {
		s.readinessMain(ctx, "box", "session-00000")
	}
	runtime.ReadMemStats(&after)
	if per := (after.TotalAlloc - before.TotalAlloc) / runs; per > 4885 {
		t.Fatalf("one readiness lookup allocated %d bytes, want at most 4885", per)
	}
}
