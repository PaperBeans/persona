package kb

// The read path of minutes.go: Core, with or without the recording module (docs/plans/plan-kb-recording-port.md,
// section 1; docs/architecture/core-and-modules.md "Recording").

import (
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/recordingwire"
)

// Meeting minutes (docs/assistant/meeting-minutes.md): a complete recording that is a meeting (the phone said so, the
// user asked, or it overlaps a calendar item of the knowledge base) gets minutes.json (Minutes, what detail.minutes
// is) and minutes.md (the same as Markdown, what the searches and kb_get read) in its directory, so a delete takes them
// with it; the retention keeps them past the transcript, until minutes_days (recording_trim.go). The transcript goes to
// the model as numbered lines, the meeting's photos (recording_photos.go) as lines of their own among them, by their
// words only; the model cites line or photo numbers, which become seconds here, and an item citing no line or photo it
// read is dropped. Long transcripts are read in chunks whose notes one more call merges.

// Minutes is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type Minutes = recordingwire.Minutes

// ReadMinutes is recording id's minutes, ErrNotFound when it has none (or there is no such recording).
func (s *Store) ReadMinutes(id string) (*Minutes, error) {
	id, ok := RecordingID(id)
	if !ok {
		return nil, ErrNotFound
	}
	raw, err := os.ReadFile(s.path(KindRecording, id, "minutes.json"))
	if err != nil {
		return nil, ErrNotFound
	}
	var m Minutes
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// recordingEnd is when r ended: ended_at, else its start plus what was recorded (at least a minute).
func recordingEnd(r *Recording) time.Time {
	end := r.StartedAt.Add(time.Duration(r.Duration * float64(time.Second)))
	if r.EndedAt != nil && r.EndedAt.After(end) {
		end = *r.EndedAt
	}
	if !end.After(r.StartedAt.Add(time.Minute)) {
		end = r.StartedAt.Add(time.Minute)
	}
	return end
}

// calendarTitle is the phone calendar's title of r, else the matched calendar item's ("" without either).
func calendarTitle(r *Recording) string { return r.CalendarTitle() }

func clockAt(start time.Time, sec float64, zone *time.Location) string {
	return start.Add(time.Duration(sec * float64(time.Second))).In(zone).Format("15:04:05")
}

// clipRunes is s on one line, at most n runes ("…" marking a cut).
func clipRunes(s string, n int) string { return clip(strings.Join(strings.Fields(s), " "), n) }

// minutesSpan is the recording's start and end in the user's zone, to the second: the span minutes are about.
func minutesSpan(r *Recording, zone *time.Location) (time.Time, time.Time) {
	return r.StartedAt.In(zone).Truncate(time.Second), recordingEnd(r).In(zone).Truncate(time.Second)
}

// minutesSpanOf fills Start and End of minutes written before they were (#4713) from their recording.
func minutesSpanOf(m *Minutes, r *Recording, zone *time.Location) {
	if m.Start.IsZero() && r != nil && !r.StartedAt.IsZero() {
		m.Start, m.End = minutesSpan(r, zone)
	}
}

// minutesWithSpan is minutes.json as a recording's detail sends it: minutes written before Start and End were get them
// from the recording (spanOf), every other field as stored.
func minutesWithSpan(raw []byte, r *Recording, zone *time.Location) []byte {
	var stored struct {
		Start time.Time `json:"start"`
	}
	if json.Unmarshal(raw, &stored) != nil || !stored.Start.IsZero() || r == nil || r.StartedAt.IsZero() {
		return raw
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return raw
	}
	start, end := minutesSpan(r, zone)
	fields["start"], _ = json.Marshal(start)
	fields["end"], _ = json.Marshal(end)
	out, err := json.Marshal(fields)
	if err != nil {
		return raw
	}
	return out
}
