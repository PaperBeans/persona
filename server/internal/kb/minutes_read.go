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

// calendarTitle is the phone calendar's title of r, else the matched calendar item's ("" without either).
func calendarTitle(r *Recording) string { return r.CalendarTitle() }

func clockAt(start time.Time, sec float64, zone *time.Location) string {
	return recordingwire.ClockAt(start, sec, zone)
}

// clipRunes is s on one line, at most n runes ("…" marking a cut).
func clipRunes(s string, n int) string { return clip(strings.Join(strings.Fields(s), " "), n) }

// minutesSpan is the recording's start and end in the user's zone, to the second: the span minutes are about.
func minutesSpan(r *Recording, zone *time.Location) (time.Time, time.Time) {
	return recordingwire.MinutesSpan(r, zone)
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

// MinutesItem is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type MinutesItem = recordingwire.MinutesItem

// MinutesPoint is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type MinutesPoint = recordingwire.MinutesPoint

// MinutesAction is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type MinutesAction = recordingwire.MinutesAction

// PurposeMeetingMinutes is the model purpose every call of a minutes job asks for (#7980): the whole transcript, or each
// part's notes and their merge, with the summary card in the same answers. It follows Information processing until it
// is set apart in Model settings (docs/assistant/model-config.md "Meeting minutes and the in-meeting conversation
// (#7980)"); the recordings' summaries and the day recap stay on kb.compress.
const PurposeMeetingMinutes = recordingwire.PurposeMeetingMinutes

// PurposeMeetingAnswer is the model purpose a question asked during a meeting is answered on, and PurposeMeetingRoom the
// one an attended meeting's room conversation starts on (#7980); the meeting recording module asks for them.
const (
	PurposeMeetingAnswer = recordingwire.PurposeMeetingAnswer
	PurposeMeetingRoom   = recordingwire.PurposeMeetingRoom
)

// MinutesOverview is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type MinutesOverview = recordingwire.MinutesOverview

// MinutesConclusion is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type MinutesConclusion = recordingwire.MinutesConclusion

// MinutesGroup is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type MinutesGroup = recordingwire.MinutesGroup

// MinutesTopic is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type MinutesTopic = recordingwire.MinutesTopic

// MinutesBullet is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type MinutesBullet = recordingwire.MinutesBullet

// MinutesTaskKey is what makes an action the same task in minutes made again: who and what.
func MinutesTaskKey(a MinutesAction) string { return recordingwire.MinutesTaskKey(a) }
