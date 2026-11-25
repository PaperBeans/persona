package kb

// The read path of day_recap_write.go: Core, with or without the recording module (docs/plans/plan-kb-recording-port.md,
// section 1; docs/architecture/core-and-modules.md "Recording").

import (
	"time"
)

// dayStart is the start of day (YYYY-MM-DD) in zone, and of the day after.
func dayStart(day string, zone *time.Location) (time.Time, time.Time, bool) {
	d, err := time.ParseInLocation(time.DateOnly, day, zone)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	return d, time.Date(d.Year(), d.Month(), d.Day()+1, 0, 0, 0, 0, zone), true
}

// daySpan is when day recording r was recorded, as far as the hub knows: from its start to where its coverage
// reaches, or its end; one still open (no ended_at) goes on until now, within its day (the watch cuts it at midnight
// in its zone).
func (e *Engine) daySpan(r *Recording, now time.Time) (time.Time, time.Time) {
	end := r.StartedAt.Add(seconds(coverageTo(r)))
	switch {
	case r.EndedAt != nil:
		end = later(end, *r.EndedAt)
	case !r.Complete:
		if _, midnight, ok := dayStart(r.Day, e.Store.RecordingZone(r)); ok {
			end = later(end, earlier(now, midnight))
		}
	}
	return r.StartedAt, end
}

// recordedWithin: day recording r's span as of now (daySpan) overlaps from–to.
func (e *Engine) recordedWithin(r *Recording, now, from, to time.Time) bool {
	start, end := e.daySpan(r, now)
	return start.Before(to) && (end.After(from) || !start.Before(from))
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func earlier(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}
