package kb

// The writes of transcription.go that delete, retention and recovery need: Core, with or without the recording module (docs/plans/plan-kb-recording-port.md,
// section 1; docs/architecture/core-and-modules.md "Recording").

import (
	"errors"
	"time"

	"github.com/nwverse/persona/server/internal/recordingwire"
)

// ---- meeting.json's transcription ----

// MeetingTranscription is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type MeetingTranscription = recordingwire.MeetingTranscription

// trimTranscription is the trim's part of recording id's transcription (PruneRecordings, under recMu): a trimmed
// recording keeps its mode and state, with no gaps, problem, fallback or timeline, and its audio is gone. The parts
// that changed, for MeetingChanged once the store's locks are let go.
func (e *Engine) trimTranscription(id string, now time.Time) []string {
	st := e.Store
	st.meetMu.Lock()
	defer st.meetMu.Unlock()
	_, parts, err := e.meetingUpdateLocked(id, func(m *Meeting, _ Recording) error {
		t := m.Transcription
		if t == nil {
			return nil
		}
		t.Gaps, t.Problem, t.Fallback, t.Timeline = nil, "", false, nil
		if t.Hub() && t.AudioGoneAt.IsZero() {
			t.AudioGoneAt = now.UTC()
		}
		return nil
	})
	if err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrRecordingDeleted) {
		e.logf("recording %s: trim transcription: %v", id, err)
	}
	return parts
}
