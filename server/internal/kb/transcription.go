package kb

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/recordingwire"
)

// minutesHubWait is how long a hub-mode meeting's minutes wait for its final transcript after the complete upload.
const minutesHubWait = 45 * time.Minute

// Re-exported from recordingwire.
var (
	ErrHubTranscribes     = recordingwire.ErrHubTranscribes
	ErrNotHub             = recordingwire.ErrNotHub
	ErrTranscriptionFinal = recordingwire.ErrTranscriptionFinal
	ErrAudioFull          = recordingwire.ErrAudioFull
	ErrBadAudio           = recordingwire.ErrBadAudio
)

// ---- the timeline ----

// TimelineRole is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type TimelineRole = recordingwire.TimelineRole

// Timeline is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type Timeline = recordingwire.Timeline

// ParseTimeline reads a timeline as the upload and the socket's start carry it (recordingwire.ParseTimeline).
func ParseTimeline(raw json.RawMessage) (t Timeline, ok bool) {
	return recordingwire.ParseTimeline(raw)
}

// transcriptionTimeline is t's timeline (empty without one).
func transcriptionTimeline(t *MeetingTranscription) Timeline {
	if t == nil || t.Timeline == nil {
		return Timeline{}
	}
	return *t.Timeline
}

// settleTranscription keeps t's own rules after a change: a hub-mode transcription has a state; once final or failed the final
// pass has started and ended; no empty gaps or timeline.
func settleTranscription(t *MeetingTranscription, now time.Time) {
	if t.Mode != TranscriptionHub {
		*t = MeetingTranscription{Mode: t.Mode}
		return
	}
	if t.State == "" {
		t.State = TranscriptionLive
	}
	if t.Done() {
		if t.FinalAt.IsZero() {
			t.FinalAt = now.UTC()
		}
		if t.FinalFrom.IsZero() {
			t.FinalFrom = t.FinalAt
		}
		t.FinalDone = 0
	}
	if len(t.Gaps) == 0 {
		t.Gaps = nil
	}
	if t.Timeline != nil && len(t.Timeline.Roles) == 0 && len(t.Timeline.Speaking) == 0 {
		t.Timeline = nil
	}
}

// TranscriptionView is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type TranscriptionView = recordingwire.TranscriptionView

// hubTranscription is r's hub-mode transcription, nil when r is not transcribed on the hub.
func hubTranscription(r *Recording) *MeetingTranscription {
	if !r.HubTranscribed() {
		return nil
	}
	return r.Conv.Transcription
}

// transcriptionHolds: r's minutes wait for its final transcript: hub mode, the state not final nor failed, at most
// minutesHubWait after the complete upload.
func transcriptionHolds(r *Recording, now time.Time) bool {
	t := hubTranscription(r)
	return t != nil && !t.Done() && !t.CompleteAt.IsZero() && now.Before(t.CompleteAt.Add(minutesHubWait))
}

// transcriptionDone: r is transcribed on the hub and its final pass has run or failed.
func transcriptionDone(r *Recording) bool {
	t := hubTranscription(r)
	return t != nil && t.Done()
}

// ---- the upload ----

// putTranscriptionFields applies what upload u (cleaned) says of the transcription to m, recording id's meeting.json
// under meetMu: the mode of the first meeting upload that names one (hub only while TranscriptionReady says so, else
// device with TranscriptionUnavailable), and on every hub-mode upload the timeline's union, CompleteAt and End (from
// the complete upload's duration, or the end of the audio held when that is later, while the socket said no end). A
// note or day upload's are ignored.
func (e *Engine) putTranscriptionFields(id string, m *Meeting, u *RecordingUpload, now time.Time) {
	asked := transcriptionMode(u.Transcription)
	if u.Kind != RecordingMeeting {
		return
	}
	t := m.Transcription
	if t == nil && asked != "" {
		mode := asked
		if mode == TranscriptionHub && (e.TranscriptionReady == nil || !e.TranscriptionReady()) {
			mode, u.TranscriptionProblem = TranscriptionDevice, TranscriptionUnavailable
		}
		t = &MeetingTranscription{Mode: mode}
		m.Transcription = t
	}
	if !t.Hub() {
		return
	}
	if u.Timeline != nil {
		tl := transcriptionTimeline(t)
		tl.Merge(*u.Timeline)
		t.Timeline = &tl
	}
	if u.Complete {
		if t.CompleteAt.IsZero() {
			t.CompleteAt = now.UTC()
		}
		if t.End == 0 && finite(u.Duration) && u.Duration > 0 {
			// The longer of the upload's and the audio held (#9587): a phone killed mid-segment uploads less.
			t.End = max(u.Duration, e.audioHeldEnd(id))
		}
	}
	settleTranscription(t, now)
	if !t.AudioClosed() {
		t.Gaps = e.audioGaps(id, t.End)
	}
}

// transcriptionOf is the transcription mode in effect after an upload that asked for asked (the stored one, device
// when none) and why it is not the one asked for: problem when the upload had one (TranscriptionUnavailable when it
// decided device for want of the hub), else TranscriptionNotMeeting for hub asked of a note or a day with no mode, and
// TranscriptionUnavailable for hub asked of a recording that has another (the mode never changes mid-recording).
func transcriptionOf(m *Meeting, asked, kind, problem string) (mode, why string) {
	mode = TranscriptionDevice
	if m != nil && m.Transcription != nil && m.Transcription.Mode != "" {
		mode = m.Transcription.Mode
	}
	asked = transcriptionMode(asked)
	switch {
	case problem != "":
		return mode, problem
	case asked != TranscriptionHub || mode == TranscriptionHub:
		return mode, ""
	case kind != RecordingMeeting && (m == nil || m.Transcription == nil):
		return mode, TranscriptionNotMeeting
	}
	return mode, TranscriptionUnavailable
}

// ---- the manager's changes ----

// TranscriptionUpdate is MeetingUpdate on the transcription part: fn runs under the meeting's lock on recording
// rawID's hub-mode transcription (ErrNotHub for a recording not transcribed on the hub), and MeetingChanged is told of
// PartTranscription after it when what the apps read changed. Its mode never changes. Once the state is final or
// failed FinalAt (and FinalFrom) are set when fn did not; once FinalFrom is set the recording takes no more audio
// (ErrTranscriptionFinal). Until then the gaps follow the audio held and End, unless fn set them: as the final pass
// starts they are frozen, those it has no audio for. The transcription after it.
func (e *Engine) TranscriptionUpdate(rawID string, fn func(t *MeetingTranscription, r Recording) error) (MeetingTranscription, error) {
	id, ok := RecordingID(rawID)
	if !ok {
		return MeetingTranscription{}, ErrNotFound
	}
	now := e.now()
	m, err := e.MeetingUpdate(id, func(m *Meeting, r Recording) error {
		t := m.Transcription
		if !t.Hub() {
			return ErrNotHub
		}
		open, gaps := !t.AudioClosed(), slices.Clone(t.Gaps)
		if err := fn(t, r); err != nil {
			return err
		}
		t.Mode = TranscriptionHub
		settleTranscription(t, now)
		if open && sameSpans(gaps, t.Gaps) {
			t.Gaps = e.audioGaps(id, t.End)
			settleTranscription(t, now)
		}
		return nil
	})
	if err != nil {
		return MeetingTranscription{}, err
	}
	t := m.Transcription
	if !t.FinalFrom.IsZero() {
		e.Store.audioFinal(id)
	}
	return *t.Clone(), nil
}

// HubRecordings are the hub-mode recordings still needing the manager, oldest first: their state not final nor
// failed, or their audio not gone. Trimmed ones need nothing (their transcript and audio went with the trim).
func (e *Engine) HubRecordings() ([]string, error) {
	var rs []*Recording
	for _, r := range e.Store.Recordings() {
		if t := hubTranscription(r); t != nil && !r.Trimmed() && (!t.Done() || t.AudioGoneAt.IsZero()) {
			rs = append(rs, r)
		}
	}
	slices.SortFunc(rs, func(a, b *Recording) int {
		if c := a.StartedAt.Compare(b.StartedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.ID)
	}
	return out, nil
}

// transcriptionMode is s as a mode ("" when it names none).
func transcriptionMode(s string) string { return recordingwire.TranscriptionMode(s) }
