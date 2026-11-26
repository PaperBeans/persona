package writers

import (
	"slices"
	"strings"
)

// ---- the manager's changes ----

// TranscriptionUpdate is MeetingUpdate on the transcription part: fn runs under the meeting's lock on recording
// rawID's hub-mode transcription (ErrNotHub for a recording not transcribed on the hub), and MeetingChanged is told of
// PartTranscription after it when what the apps read changed. Its mode never changes. Once the state is final or
// failed FinalAt (and FinalFrom) are set when fn did not; once FinalFrom is set the recording takes no more audio
// (ErrTranscriptionFinal). Until then the gaps follow the audio held and End, unless fn set them: as the final pass
// starts they are frozen, those it has no audio for. The transcription after it.
func (e *engine) TranscriptionUpdate(rawID string, fn func(t *MeetingTranscription, r Recording) error) (MeetingTranscription, error) {
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
func (e *engine) HubRecordings() ([]string, error) {
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
