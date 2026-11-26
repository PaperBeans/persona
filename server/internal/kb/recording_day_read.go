package kb

// The read path of recording_day.go: Core, with or without the recording module (docs/plans/plan-kb-recording-port.md,
// section 1; docs/architecture/core-and-modules.md "Recording").

import (
	"cmp"
	"github.com/nwverse/persona/server/internal/recordingwire"
	"slices"
	"strings"
	"time"
)

// RecordingZone is the zone r's clock times are in: a day recording's own (tz), else (and when it cannot be loaded)
// the user's.
func (s *Store) RecordingZone(r *Recording) *time.Location {
	return recordingwire.RecordingZone(r, s.Zone())
}

// SetClock gives the engine its clock (nil: time.Now): a test of the recordings' writers, here or in the recording
// module, sets the time a round runs at.
func (e *Engine) SetClock(now func() time.Time) { e.clock = now }

// now is the engine's clock (a test sets clock).
func (e *Engine) now() time.Time {
	if e.clock != nil {
		return e.clock()
	}
	return time.Now()
}

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

// recordingName is Store.recordingName, a day recording's in the owner's language (Engine.DayRecordingName) unless
// the user titled it.
func (e *Engine) recordingName(r *Recording) string {
	if r.Kind == RecordingDay && r.Title == "" && e.DayRecordingName != nil {
		if n := oneLine(e.DayRecordingName(r.Day)); n != "" {
			return n
		}
	}
	return e.Store.recordingName(r)
}
