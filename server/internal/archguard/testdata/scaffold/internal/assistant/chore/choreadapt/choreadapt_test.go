package choreadapt_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistant/chore"
	"github.com/nwverse/persona/server/internal/assistant/chore/choreadapt"
	"github.com/nwverse/persona/server/internal/domainkit/clock/clocktest"
	"github.com/nwverse/persona/server/internal/domainkit/domaintest"
)

// fixtures are stored records as the store writes them; each replays through Restore and Store unchanged. Add the
// records a real store holds, written as json.Marshal writes them.
var fixtures = []string{
	`{"id":"chore 1","title":"title 1","effort":1,"priority":"low"}`,
	`{"id":"chore 1","title":"title 2","effort":2,"snooze":2,"priority":"urgent","closed_at":"2026-11-22T17:00:00+08:00"}`,
}

func TestFixturesReplayUnchanged(t *testing.T) {
	for _, fixture := range fixtures {
		var r choreadapt.Record
		if err := json.Unmarshal([]byte(fixture), &r); err != nil {
			t.Fatal(err)
		}
		x := domaintest.Ok(t, choreadapt.Restore(r))
		got, err := json.Marshal(choreadapt.Store(r, x))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != fixture {
			t.Errorf("replayed\n%s\nas\n%s", fixture, got)
		}
	}
}

func TestRestoreRefusesWhatTheStoreCannotHold(t *testing.T) {
	r := choreadapt.Record{ID: "chore 1", Title: "title 1", Effort: 1, Snooze: 1, Priority: "from a newer peer"}
	got := domaintest.Fails(t, choreadapt.Restore(r))
	if want := "invalid chore.chore.no_priority priority"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestActStoresTheDecision(t *testing.T) {
	var prev choreadapt.Record
	if err := json.Unmarshal([]byte(fixtures[0]), &prev); err != nil {
		t.Fatal(err)
	}
	now := clocktest.Fixed(time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)).Now()
	decided := domaintest.Ok(t, chore.Decide(domaintest.Ok(t, choreadapt.Restore(prev)), chore.CommandClose(), now))
	var saved choreadapt.Record
	if err := choreadapt.Act(prev, decided, func(r choreadapt.Record) error { saved = r; return nil }); err != nil {
		t.Fatal(err)
	}
	if saved.ClosedAt == nil || !saved.ClosedAt.Equal(time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("saved %+v, want it closed at the clock's time", saved)
	}
}
