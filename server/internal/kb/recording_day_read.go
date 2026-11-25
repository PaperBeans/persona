package kb

// The read path of recording_day.go: Core, with or without the recording module (docs/plans/plan-kb-recording-port.md,
// section 1; docs/architecture/core-and-modules.md "Recording").

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"time"
)

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

var dayZones sync.Map // IANA name → *time.Location, loaded once

// dayLocation is the IANA zone name ("Local" is not one).
func dayLocation(name string) (*time.Location, error) {
	if l, ok := dayZones.Load(name); ok {
		return l.(*time.Location), nil
	}
	if name == "" || name == "Local" {
		return nil, fmt.Errorf("unknown time zone %q", name)
	}
	l, err := time.LoadLocation(name)
	if err != nil {
		return nil, err
	}
	dayZones.Store(name, l)
	return l, nil
}

// RecordingZone is the zone r's clock times are in: a day recording's own (tz), else (and when it cannot be loaded)
// the user's.
func (s *Store) RecordingZone(r *Recording) *time.Location {
	if r.Kind == RecordingDay && r.TZ != "" {
		if l, err := dayLocation(r.TZ); err == nil {
			return l
		}
	}
	return s.Zone()
}

// now is the engine's clock (a test sets clock).
func (e *Engine) now() time.Time {
	if e.clock != nil {
		return e.clock()
	}
	return time.Now()
}

// coverageTo is how far r's text reaches, in seconds since started_at: its coverage's to, its duration without one
// (the recap reads up to its last segment then, recapLines).
func coverageTo(r *Recording) float64 {
	if r.Coverage != nil {
		return r.Coverage.To
	}
	return r.Duration
}

func seconds(sec float64) time.Duration { return time.Duration(sec * float64(time.Second)) }

// DayRecordings are the day recordings of day (YYYY-MM-DD, in each one's zone), by part, then start.
func (e *Engine) DayRecordings(day string) []Recording {
	return e.dayRecordings(func(r *Recording) bool { return r.Day == day })
}

// TodaysDayRecordings are the day recordings recorded on the owner's day of now in zone owner (the evening routine's:
// a read-only knowledge base's store is not put in it), as the recap time takes them (DaySpan: one still open counts
// until now), by part, then start: what the evening routine reads.
func (e *Engine) TodaysDayRecordings(now time.Time, owner *time.Location) []Recording {
	from, to, _ := dayStart(now.In(owner).Format(time.DateOnly), owner)
	return e.dayRecordings(func(r *Recording) bool { return e.recordedWithin(r, now, from, to) })
}

// DaySpan is when day recording r was recorded as of now (daySpan): from its start to its end, or until now while it
// is open, within its day.
func (e *Engine) DaySpan(r *Recording, now time.Time) (time.Time, time.Time) {
	return e.daySpan(r, now)
}

func (e *Engine) dayRecordings(keep func(r *Recording) bool) []Recording {
	var out []Recording
	for _, r := range e.Store.Recordings() {
		if r.Kind == RecordingDay && keep(r) {
			out = append(out, *r)
		}
	}
	slices.SortFunc(out, func(a, b Recording) int {
		return cmp.Or(cmp.Compare(a.Part, b.Part), a.StartedAt.Compare(b.StartedAt), strings.Compare(a.ID, b.ID))
	})
	return out
}
