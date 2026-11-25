package recordingwire

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
)

// TranscriptionView is transcription on GET …/live and in detail.meta.
type TranscriptionView struct {
	Mode     string        `json:"mode"`
	State    string        `json:"state,omitempty"`
	Gaps     *[][2]float64 `json:"gaps,omitempty"` // hub: always present, [] when none
	Problem  string        `json:"problem,omitempty"`
	Fallback bool          `json:"fallback,omitempty"`
	// FinalDone and End: while the final pass runs (finishing, the pass started), how far it is and the recording's
	// end, both in recording seconds (#13461); absent otherwise. FinalDone is 0 as the pass starts.
	FinalDone *float64 `json:"final_done,omitempty"`
	End       *float64 `json:"end,omitempty"`
}

// Modes, states, parts, limits.
const (
	// The transcription modes (meeting.json's transcription.mode, the upload's transcription).
	TranscriptionDevice, TranscriptionHub = "device", "hub"
	// The states of a hub-mode recording's transcription.
	TranscriptionLive, TranscriptionFinishing, TranscriptionFinal, TranscriptionFailed = "live", "finishing", "final", "failed"
	// The upload's transcription_problem: the hub was asked for and is not ready, or the recording is not a meeting.
	TranscriptionUnavailable, TranscriptionNotMeeting = "unavailable", "not_meeting"
	// The meeting parts the apps read again (GET …/live?parts=, the meeting_live event): PartTranscription's revision
	// is kept in meeting.json, PartInterim's is the caption's time in milliseconds (the manager's, never stored).
	PartTranscription, PartInterim = "transcription", "interim"
	// The timeline's limits: role changes and speaking spans.
	MaxTimelineRoles, MaxTimelineSpans = 100, 1000
	// AudioRate is the stored audio's sample rate (PCM16 little-endian, mono).
	AudioRate = 16000
	// MaxAudioSeconds is the most audio the hub keeps for one recording: none from this recording second on.
	MaxAudioSeconds = 12 * 3600
	// The passes that wrote the hub's transcript (HubTranscript.Pass).
	HubPassLive, HubPassFinal = "live", "final"
	// A segment's source in the transcript of record (RecordingSegment.Source) and a live line's (LiveSegment.Source).
	SourceHub, SourceDevice = "hub", "device"
)

var (
	// ErrHubTranscribes: POST …/live on a recording the hub transcribes (its live log is the hub's).
	ErrHubTranscribes = errors.New("the hub transcribes this meeting")
	// ErrNotHub: the recording's mode is not hub (the audio socket, PUT …/audio).
	ErrNotHub = errors.New("this recording is not transcribed on the hub")
	// ErrTranscriptionFinal: the final pass has started; the hub takes no more audio.
	ErrTranscriptionFinal = errors.New("the final pass has started")
	// ErrAudioFull: the recording holds MaxAudioSeconds of audio.
	ErrAudioFull = errors.New("the hub holds the most audio a recording may have")
	// ErrBadAudio: audio that is not whole 16-bit samples, or a position that is not 0 or more seconds (400, or the
	// socket's bad_frame).
	ErrBadAudio = errors.New("audio must be whole 16-bit samples")
)

// TimelineRole is one role change of the timeline: the role (AttendanceOff, AttendanceNotes, AttendanceListener,
// AttendanceParticipant) from recording second At on.
type TimelineRole struct {
	At         float64 `json:"at"`
	Attendance string  `json:"attendance"`
}

// Timeline is a recording's role changes and the spans Persona spoke in, in recording seconds, each with the answer
// said in it as an upload's spoken has them (SpokenSpan), as the phone keeps them ("The timeline"); meeting.json keeps
// the union of every one received. Roles are in time order, one per At; Speaking is a union (sorted, apart: spokenUnion).
type Timeline struct {
	Roles    []TimelineRole `json:"roles,omitempty"`
	Speaking []SpokenSpan   `json:"speaking,omitempty"`
}

// timelineRole is attendance as a timeline has it ("" when it is not one of the four roles).
func timelineRole(attendance string) string {
	switch a := strings.ToLower(strings.TrimSpace(attendance)); a {
	case AttendanceOff, AttendanceNotes, AttendanceListener, AttendanceParticipant:
		return a
	}
	return ""
}

// ParseTimeline reads a timeline as the upload and the socket's start carry it: an object with roles
// ([{"at", "attendance"}]) and speaking ([{"start", "end", "text"?}], as an upload's spoken; a text over maxSpokenText
// bytes is cut at a character boundary). ok is false when it is absent, malformed or over the limits: a timeline with
// any entry that is not one (an at that is not a number of 0 or more, a role that is not one of the four, a span that
// is not an object with numbers start and end, 0 <= start <= end, and a string text if any) is ignored whole, never an
// error.
func ParseTimeline(raw json.RawMessage) (t Timeline, ok bool) {
	if jsonNull(raw) {
		return Timeline{}, false
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		return Timeline{}, false
	}
	var roles, speaking []json.RawMessage
	if v := obj["roles"]; !jsonNull(v) && json.Unmarshal(v, &roles) != nil {
		return Timeline{}, false
	}
	if v := obj["speaking"]; !jsonNull(v) && json.Unmarshal(v, &speaking) != nil {
		return Timeline{}, false
	}
	if len(roles) > MaxTimelineRoles || len(speaking) > MaxTimelineSpans {
		return Timeline{}, false
	}
	var in Timeline
	for _, raw := range roles {
		var role struct {
			At         *float64 `json:"at"`
			Attendance *string  `json:"attendance"`
		}
		if json.Unmarshal(raw, &role) != nil || role.At == nil || role.Attendance == nil || *role.At < 0 {
			return Timeline{}, false
		}
		a := timelineRole(*role.Attendance)
		if a == "" {
			return Timeline{}, false
		}
		in.Roles = append(in.Roles, TimelineRole{At: *role.At, Attendance: a})
	}
	for _, raw := range speaking {
		var span struct {
			Start *float64 `json:"start"`
			End   *float64 `json:"end"`
			Text  *string  `json:"text"`
		}
		if json.Unmarshal(raw, &span) != nil || span.Start == nil || span.End == nil || *span.Start < 0 || *span.End < *span.Start {
			return Timeline{}, false
		}
		sp := SpokenSpan{Start: *span.Start, End: *span.End}
		if span.Text != nil {
			sp.Text = SpokenText(*span.Text)
		}
		in.Speaking = append(in.Speaking, sp)
	}
	t.Merge(in)
	return t, true
}

// Merge adds o to t: one role change per at (o's winning: it was received later), in time order; overlapping or
// touching spans made one, their texts joined in time order, a span received again counted once (spokenUnion). Past
// the limits t keeps the first MaxTimelineRoles role changes and MaxTimelineSpans spans. Entries that are not ones are
// left out. changed says whether t moved.
func (t *Timeline) Merge(o Timeline) (changed bool) {
	before, _ := json.Marshal(t)
	byAt := map[float64]string{}
	for _, list := range [][]TimelineRole{t.Roles, o.Roles} {
		for _, r := range list {
			if a := timelineRole(r.Attendance); a != "" && finite(r.At) && r.At >= 0 {
				byAt[r.At] = a
			}
		}
	}
	var roles []TimelineRole
	for at, a := range byAt {
		roles = append(roles, TimelineRole{At: at, Attendance: a})
	}
	slices.SortFunc(roles, func(a, b TimelineRole) int { return cmp.Compare(a.At, b.At) })
	if len(roles) > MaxTimelineRoles {
		roles = roles[:MaxTimelineRoles]
	}
	var spans []SpokenSpan
	for _, s := range spokenUnion(append(slices.Clone(t.Speaking), o.Speaking...)) {
		if s.Start >= 0 {
			spans = append(spans, s)
		}
	}
	if len(spans) > MaxTimelineSpans {
		spans = spans[:MaxTimelineSpans]
	}
	t.Roles, t.Speaking = roles, spans
	after, _ := json.Marshal(t)
	return !bytes.Equal(before, after)
}

// RoleAt is the role at recording second at: the timeline's last role change at or before it, else otherwise (the
// meeting's role, room.attendance).
func (t Timeline) RoleAt(at float64, otherwise string) string {
	role, found := otherwise, -1.0
	for _, r := range t.Roles {
		if r.At <= at && r.At >= found {
			role, found = r.Attendance, r.At
		}
	}
	return role
}

func (t *Timeline) Clone() *Timeline {
	if t == nil {
		return nil
	}
	return &Timeline{Roles: slices.Clone(t.Roles), Speaking: slices.Clone(t.Speaking)}
}

// MeetingTranscription is meeting.json's "transcription" (never meta.json). The manager changes it through
// TranscriptionUpdate; uploads set the mode, the timeline's union, CompleteAt and End; the knowledge base keeps Gaps
// up to date while the audio comes, and AudioGoneAt.
type MeetingTranscription struct {
	Mode     string    `json:"mode"`
	State    string    `json:"state,omitempty"`    // hub only
	Problem  string    `json:"problem,omitempty"`  // the audio socket's standing problem
	Fallback bool      `json:"fallback,omitempty"` // the live log is filled from the phone
	Timeline *Timeline `json:"timeline,omitempty"`
	// End is the recording's end in recording seconds: the socket's end.at (the manager's), else the duration of the
	// complete upload or the end of the audio held, whichever is later.
	End        float64   `json:"end,omitempty"`
	CompleteAt time.Time `json:"complete_at,omitzero"` // the first complete:true upload (the gap and minutes waits)
	FinalFrom  time.Time `json:"final_from,omitzero"`  // the final pass started: transcription_final from here
	// FinalAt is when the state became final or failed (set then when the manager did not).
	FinalAt    time.Time `json:"final_at,omitzero"`
	FinalTries int       `json:"final_tries,omitempty"`
	// FinalUnreachable counts the final pass's tries that never reached the upstream (upstream_unavailable, nothing
	// billed): they are tried again on their own, longer schedule (the manager's finalUnreachableRetries).
	FinalUnreachable int `json:"final_unreachable,omitempty"`
	// FinalDone is how far the final pass has got, in recording seconds: the end of the last window it finished, set
	// by the manager as each window ends and when a try starts (0 after a restart, which starts over). The apps read it
	// as the pass's progress (#13461); none once the state is final or failed.
	FinalDone float64 `json:"final_done,omitempty"`
	// Gaps are the spans of the recording the hub holds no audio for (GapsIn), kept up to date by the audio writes
	// until the final pass starts and frozen from then on: the final pass's, which stay once the audio is gone.
	Gaps         [][2]float64 `json:"gaps,omitempty"`
	AudioGoneAt  time.Time    `json:"audio_gone_at,omitzero"`
	UsageSeconds float64      `json:"usage_seconds,omitempty"` // the upstream's billed seconds, diagnostics only
}

func (t *MeetingTranscription) Clone() *MeetingTranscription {
	if t == nil {
		return nil
	}
	c := *t
	c.Timeline = t.Timeline.Clone()
	c.Gaps = slices.Clone(t.Gaps)
	return &c
}

// hub: t is a hub-mode transcription.
func (t *MeetingTranscription) Hub() bool { return t != nil && t.Mode == TranscriptionHub }

// done: the final pass has run (final), or the hub made nothing usable (failed).
func (t *MeetingTranscription) Done() bool {
	return t.State == TranscriptionFinal || t.State == TranscriptionFailed
}

// audioClosed: the hub takes no more audio of it (the final pass started or ran, or the audio is gone).
func (t *MeetingTranscription) AudioClosed() bool {
	return !t.FinalFrom.IsZero() || t.Done() || !t.AudioGoneAt.IsZero()
}

// finalProgress is how far the final pass is, for the apps (#13461): the recording seconds it has finished and the
// recording's end, while the state is finishing and the pass has started (it may be waiting for its next try), and
// the end is known. done is at most end.
func (t *MeetingTranscription) FinalProgress() (done, end float64, ok bool) {
	if !t.Hub() || t.State != TranscriptionFinishing || t.FinalFrom.IsZero() || t.End <= 0 {
		return 0, 0, false
	}
	return min(max(t.FinalDone, 0), t.End), t.End, true
}
