package clock_test

import (
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/domainkit/clock"
	"github.com/nwverse/persona/server/internal/domainkit/clock/clocktest"
	"github.com/nwverse/persona/server/internal/domainkit/instant"
)

func TestSystemReadsTheMachinesClock(t *testing.T) {
	var c clock.Clock = clock.System{}
	before := instant.At(time.Now())
	now := c.Now()
	if !now.Reached(before) || now.Reached(instant.At(time.Now().Add(time.Hour))) {
		t.Error("System's reading is not now")
	}
}

func TestTestClocks(t *testing.T) {
	at := time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)
	if !clocktest.Fixed(at).Now().Stamp().Equal(instant.At(at)) {
		t.Error("Fixed")
	}
	fake := clocktest.NewFake(at)
	fake.Advance(time.Minute)
	if !fake.Now().Stamp().Equal(instant.At(at.Add(time.Minute))) {
		t.Error("Fake.Advance")
	}
}
