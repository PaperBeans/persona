package kb

// The read path of day_recap_write.go: Core, with or without the recording module (docs/plans/plan-kb-recording-port.md,
// section 1; docs/architecture/core-and-modules.md "Recording").

import (
	"github.com/nwverse/persona/server/internal/recordingwire"
	"time"
)

// dayStart is the start of day (YYYY-MM-DD) in zone, and of the day after.
func dayStart(day string, zone *time.Location) (time.Time, time.Time, bool) {
	return recordingwire.DayStart(day, zone)
}

// daySpan is when day recording r was recorded, as far as the hub knows: from its start to where its coverage
// reaches, or its end; one still open (no ended_at) goes on until now, within its day (the watch cuts it at midnight
// in its zone).
func (e *Engine) daySpan(r *Recording, now time.Time) (time.Time, time.Time) {
	return recordingwire.DaySpan(r, now, e.Store.Zone())
}

// recordedWithin: day recording r's span as of now (daySpan) overlaps from–to.
func (e *Engine) recordedWithin(r *Recording, now, from, to time.Time) bool {
	return recordingwire.RecordedWithin(r, now, from, to, e.Store.Zone())
}
