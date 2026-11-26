package instant_test

import (
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/domainkit/defect"
	"github.com/nwverse/persona/server/internal/domainkit/instant"
)

func raises(t *testing.T, op string, f func()) {
	t.Helper()
	defer func() {
		t.Helper()
		d, ok := recover().(defect.Defect)
		if !ok || !strings.HasPrefix(d.Error(), op+": ") {
			t.Errorf("want a defect from %s, got %v", op, d)
		}
	}()
	f()
}

func TestTheZeroTimeIsTheZeroInstant(t *testing.T) {
	at := instant.At(time.Time{})
	var zero instant.Instant
	if at.Known() || !at.Equal(zero) || at.Wire() != 0 {
		t.Errorf("At(time.Time{}) is known or not the zero Instant: %d", at.Wire())
	}
}

func TestWireKeepsNanoseconds(t *testing.T) {
	when := time.Date(2026, 11, 22, 17, 36, 5, 123456789, time.FixedZone("plus8", 8*3600))
	at := instant.At(when)
	if !at.Known() || at.Wire() != when.UnixNano() || !time.Unix(0, at.Wire()).Equal(when) {
		t.Errorf("Wire = %d, want %d", at.Wire(), when.UnixNano())
	}
	raises(t, "instant.At", func() { instant.At(time.Date(1600, 1, 1, 0, 0, 0, 0, time.UTC)) })
	raises(t, "instant.At", func() { instant.At(time.Date(2300, 1, 1, 0, 0, 0, 0, time.UTC)) })
}

func TestAZeroNowIsADefect(t *testing.T) {
	raises(t, "instant.ReadClock", func() { instant.ReadClock(time.Time{}) })
	var now instant.Now
	raises(t, "instant.Now.Stamp", func() { now.Stamp() })
	raises(t, "instant.Now.Stamp", func() { now.Reached(instant.At(time.Unix(10, 0))) })
	raises(t, "instant.Now.Stamp", func() { instant.Elapsed(instant.At(time.Unix(10, 0)), now) })
}

func TestComparisons(t *testing.T) {
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	a, b := instant.At(base), instant.At(base.Add(time.Minute))
	if !a.Before(b) || b.Before(a) || a.Before(a) || !a.Equal(instant.At(base.In(time.FixedZone("x", 3600)))) {
		t.Error("Before or Equal")
	}
	if !a.After(instant.ParseMinutes(1)).Equal(b) || !a.After(instant.ParseSeconds(60)).Equal(b) {
		t.Error("After a minute")
	}
	now := instant.ReadClock(base.Add(time.Minute))
	if !now.Stamp().Equal(b) || !now.Reached(a) || !now.Reached(b) || now.Reached(b.After(instant.ParseSeconds(1))) {
		t.Error("Stamp or Reached")
	}
	if instant.Elapsed(a, now).Wire() != int64(time.Minute) {
		t.Errorf("Elapsed = %d", instant.Elapsed(a, now).Wire())
	}
	if instant.Elapsed(b.After(instant.ParseMinutes(5)), now).Wire() != 0 {
		t.Error("Elapsed since a later instant is not zero")
	}
	var unknown instant.Instant
	raises(t, "instant.Instant.After", func() { unknown.After(instant.ParseSeconds(1)) })
	raises(t, "instant.Now.Reached", func() { now.Reached(unknown) })
	raises(t, "instant.Elapsed", func() { instant.Elapsed(unknown, now) })
	raises(t, "instant.ParseMinutes", func() { instant.ParseMinutes(-1) })
	raises(t, "instant.ParseSeconds", func() { instant.ParseSeconds(-1) })
	raises(t, "instant.ParseMinutes", func() { instant.ParseMinutes(1 << 62) })
}
