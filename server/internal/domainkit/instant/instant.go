// Package instant is time inside cores: an Instant is a point in time, a Span a stretch of it, and Now the reading
// the application's clock took for one operation. Cores never name time.Time or time.Duration, so "unknown" has one
// meaning and the clock reaches a core only as a parameter. Adapters convert with At and Wire; only domainkit/clock
// and its testkit read the clock (docs/server/architecture.md "Domain model", "The functional core").
//
//persona:layer kit
package instant

import (
	"time"

	"github.com/nwverse/persona/server/internal/domainkit/defect"
)

// Instant is a point in time, in UTC nanoseconds since the Unix epoch. The zero Instant is unknown, and so reads the
// epoch's own instant.
type Instant struct{ instant int64 }

// Span is a stretch of time in nanoseconds, never negative. The zero Span is no time.
type Span struct{ span int64 }

// Now is the application's clock read once for an operation. The zero Now is no reading: every method raises a
// defect on it, since only the application's clock makes one.
type Now struct{ now Instant }

// At is t as an Instant, for adapters and the application; the zero time is the zero Instant, and a time outside
// the years 1678 to 2262 is a defect.
func At(t time.Time) Instant {
	if t.IsZero() {
		return Instant{}
	}
	ns := t.UnixNano()
	if !time.Unix(0, ns).Equal(t) { // UnixNano does not hold it
		defect.Raise("instant.At", "a time outside the years 1678 to 2262")
	}
	return Instant{instant: ns}
}

// ReadClock is the reading t, for domainkit/clock and its testkit only (out-clock-read); the zero time is a defect.
func ReadClock(t time.Time) Now {
	at := At(t)
	if !at.Known() {
		defect.Raise("instant.ReadClock", "the zero time: read it from the application's clock")
	}
	return Now{now: at}
}

// ParseMinutes is n minutes; a negative n, or one too long to count in nanoseconds, is a defect.
func ParseMinutes(n int) Span { return span(n, int64(time.Minute), "instant.ParseMinutes") }

// ParseSeconds is n seconds; a negative n, or one too long to count in nanoseconds, is a defect.
func ParseSeconds(n int) Span { return span(n, int64(time.Second), "instant.ParseSeconds") }

func span(n int, unit int64, op string) Span {
	if n < 0 || int64(n) > (1<<63-1)/unit {
		defect.Raise(op, "a negative or too long span")
	}
	return Span{span: int64(n) * unit}
}

// Known says whether a is a known instant.
func (a Instant) Known() bool { return a.instant != 0 }

// Before says whether a is earlier than b.
func (a Instant) Before(b Instant) bool { return a.instant < b.instant }

// Equal says whether a and b are the same instant.
func (a Instant) Equal(b Instant) bool { return a.instant == b.instant }

// After is the instant s after a; an unknown a, or a sum past the year 2262, is a defect.
func (a Instant) After(s Span) Instant {
	if !a.Known() {
		defect.Raise("instant.Instant.After", "an unknown instant")
	}
	if a.instant > 1<<63-1-s.span {
		defect.Raise("instant.Instant.After", "past the year 2262")
	}
	return Instant{instant: a.instant + s.span}
}

// Elapsed is the time from since to now, or no time when since is later; an unknown since is a defect.
func Elapsed(since Instant, now Now) Span {
	at := now.Stamp()
	if !since.Known() {
		defect.Raise("instant.Elapsed", "an unknown instant")
	}
	if !since.Before(at) {
		return Span{}
	}
	return Span{span: at.instant - since.instant}
}

// Stamp is the instant the clock was read at, for a core to keep (an item's done time).
func (n Now) Stamp() Instant {
	if !n.now.Known() {
		defect.Raise("instant.Now.Stamp", "the zero Now: read it from the application's clock")
	}
	return n.now
}

// Reached says whether the clock reading is at or past deadline; an unknown deadline is a defect.
func (n Now) Reached(deadline Instant) bool {
	at := n.Stamp()
	if !deadline.Known() {
		defect.Raise("instant.Now.Reached", "an unknown deadline")
	}
	return !at.Before(deadline)
}

// Wire is the instant in UTC nanoseconds since the epoch, 0 when unknown. Adapters only; they make the time.Time.
func (a Instant) Wire() int64 { return a.instant }

// Wire is the span in nanoseconds. Adapters only.
func (s Span) Wire() int64 { return s.span }
