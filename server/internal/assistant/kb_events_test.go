package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/kb"
)

type recordedKBEvent struct {
	name string
	raw  json.RawMessage
}
type kbEventTestHub struct {
	*fakeHub
	muEvents sync.Mutex
	sent     []recordedKBEvent
	block    chan struct{}
}

func (f *kbEventTestHub) KBEvent(event string, params any) error {
	if f.block != nil {
		<-f.block
	}
	raw, _ := json.Marshal(params)
	f.muEvents.Lock()
	f.sent = append(f.sent, recordedKBEvent{event, raw})
	f.muEvents.Unlock()
	return nil
}
func (f *kbEventTestHub) eventsNow() []recordedKBEvent {
	f.muEvents.Lock()
	defer f.muEvents.Unlock()
	return slices.Clone(f.sent)
}

func settleKBEvents(t *testing.T, s *Service) {
	t.Helper()
	for until := time.Now().Add(5 * time.Second); time.Now().Before(until); time.Sleep(time.Millisecond) {
		s.kb.events.mu.Lock()
		empty := !s.kb.events.running && len(s.kb.events.pending) == 0
		s.kb.events.mu.Unlock()
		if empty {
			return
		}
	}
	t.Fatal("KB events did not settle")
}

func TestKBEventsFollowSuccessfulWritesAndForget(t *testing.T) {
	f := &kbEventTestHub{fakeHub: newFakeHub()}
	f.hello.KB = &KBSettings{}
	s := New(Options{Hub: f, KB: &KBOptions{Dir: t.TempDir()}, Logf: testLogf(t)})
	t.Cleanup(s.kbEnd)
	setSettings(t, s, func(v *Settings) { v.Timezone = "UTC" })
	e := kbEng(t, s)
	if err := e.Store.PutChat(&kb.Chat{ID: "example-chat", Source: "example", Name: "示例会话"}); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.PutSummary(kb.KindChat, "example-chat", "private body"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.kbForget(context.Background(), "example"); err != nil {
		t.Fatal(err)
	}
	settleKBEvents(t, s)
	events := f.eventsNow()
	if len(events) != 3 || events[2].name != assistantwire.EventKBForgotten {
		t.Fatalf("events: %+v", events)
	}
	for i, what := range []string{"metadata", "summary"} {
		var changed assistantwire.KBChanged
		if json.Unmarshal(events[i].raw, &changed) != nil || changed.Kind != kb.KindChat || changed.ID != "example-chat" || changed.What != what {
			t.Fatalf("change: %+v", events[i])
		}
		var fields map[string]any
		_ = json.Unmarshal(events[i].raw, &fields)
		if len(fields) != 3 {
			t.Fatalf("extra event fields: %s", events[i].raw)
		}
	}
	if string(events[2].raw) != `{"source":"example"}` {
		t.Fatalf("forgotten: %s", events[2].raw)
	}
}

type failingKBIndex struct{ *kbStore }

func (*failingKBIndex) DeleteKBEntities(context.Context, string, []string) error {
	return errors.New("index unavailable")
}
func TestKBForgottenWaitsForIndexCleanup(t *testing.T) {
	f := &kbEventTestHub{fakeHub: newFakeHub()}
	f.hello.KB = &KBSettings{}
	st := &failingKBIndex{newKBStore()}
	s := New(Options{Hub: f, Store: func() Store { return st }, KB: &KBOptions{Dir: t.TempDir()}, Logf: testLogf(t)})
	t.Cleanup(s.kbEnd)
	setSettings(t, s, func(v *Settings) { v.Timezone = "UTC" })
	_ = kbEng(t, s)
	if _, err := s.kbForget(context.Background(), "example"); err == nil {
		t.Fatal("failed index deletion reported success")
	}
	settleKBEvents(t, s)
	for _, event := range f.eventsNow() {
		if event.name == assistantwire.EventKBForgotten {
			t.Fatal("forgotten sent before index deletion succeeded")
		}
	}
}

func TestKBEventsDoNotBlockWritesAndQueueIsBounded(t *testing.T) {
	block := make(chan struct{})
	f := &kbEventTestHub{fakeHub: newFakeHub(), block: block}
	s := New(Options{Hub: f})
	defer close(block)
	done := make(chan struct{})
	go func() {
		for i := 0; i < kbEventLimit*2; i++ {
			s.kbChanged(kb.Change{Kind: kb.KindChat, ID: time.Unix(int64(i), 0).String(), What: "metadata"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("a slow publisher blocked the writer")
	}
	s.kb.events.mu.Lock()
	n := len(s.kb.events.pending)
	s.kb.events.mu.Unlock()
	if n > kbEventLimit {
		t.Fatalf("unbounded queue: %d", n)
	}
}
