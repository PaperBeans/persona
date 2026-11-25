package kb

// The writes of meeting.go that delete, retention and recovery need: Core, with or without the recording module (docs/plans/plan-kb-recording-port.md,
// section 1; docs/architecture/core-and-modules.md "Recording").

import (
	"bytes"
	"encoding/json"
	"os"

	"github.com/nwverse/persona/server/internal/recordingwire"
)

// Re-exported from recordingwire.
const (
	PartRoom       = recordingwire.PartRoom
	PartAsks       = recordingwire.PartAsks
	PartPrivate    = recordingwire.PartPrivate
	PartFollowup   = recordingwire.PartFollowup
	PartPhotos     = recordingwire.PartPhotos
	PartLines      = recordingwire.PartLines
	PartTopics     = recordingwire.PartTopics
	PartHighlights = recordingwire.PartHighlights
)

// MeetingRoomLean is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type MeetingRoomLean = recordingwire.MeetingRoomLean

// putMeeting writes recording id's meeting.json, not when the recording was deleted meanwhile (false then); a trimmed
// recording keeps it (its private mark and its room's end). The caller holds meetMu.
func (s *Store) putMeeting(id string, m *Meeting) (bool, error) {
	if s.readOnly {
		return false, ErrReadOnly
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return false, err
	}
	s.recFilesMu.Lock()
	defer s.recFilesMu.Unlock()
	if _, gone := s.tombstones()[id]; gone {
		return false, nil
	}
	if _, err := os.Stat(s.path(KindRecording, id)); err != nil {
		return false, nil
	}
	return true, writeRecordingFile(s.path(KindRecording, id, meetingFile), append(raw, '\n'))
}

// meetingParts are the parts of m that differ from before, in a fixed order.
func meetingParts(before, after Meeting) []string {
	var out []string
	same := func(a, b any) bool {
		x, _ := json.Marshal(a)
		y, _ := json.Marshal(b)
		return bytes.Equal(x, y)
	}
	if !same(roomShown(before.Room), roomShown(after.Room)) {
		out = append(out, PartRoom)
	}
	if !same(before.Private, after.Private) {
		out = append(out, PartPrivate)
	}
	if !same(before.Followup, after.Followup) {
		out = append(out, PartFollowup)
	}
	if !same(transcriptionShown(before.Transcription), transcriptionShown(after.Transcription)) {
		out = append(out, PartTranscription)
	}
	return out
}

// roomShown is room without what only the manager reads (its batching and a lean room's bookkeeping, #8253), whose
// changes raise no revision: the apps see nothing new of it.
func roomShown(room *MeetingRoom) *MeetingRoom {
	if room == nil {
		return nil
	}
	r := *room
	r.Batching, r.Lean = 0, MeetingRoomLean{}
	return &r
}

// meetingSame: a and b are the same meeting.json (also where no part the apps read differs: the timeline, the final
// pass's bookkeeping, a lean room's bookkeeping).
func meetingSame(a, b Meeting) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

// bumpRevs raises the revisions of parts in m.
func bumpRevs(m *Meeting, parts []string) {
	if len(parts) == 0 {
		return
	}
	if m.Revs == nil {
		m.Revs = map[string]int64{}
	}
	for _, p := range parts {
		m.Revs[p]++
	}
}

// meetingChanged tells the engine's MeetingChanged of recording id's changed parts.
func (e *Engine) meetingChanged(id string, parts []string) {
	if len(parts) > 0 && e.MeetingChanged != nil {
		e.MeetingChanged(id, parts)
	}
}

func (e *Engine) meetingUpdateLocked(id string, fn func(m *Meeting, r Recording) error) (Meeting, []string, error) {
	st := e.Store
	if _, gone := st.tombstones()[id]; gone {
		return Meeting{}, nil, ErrRecordingDeleted
	}
	r, ok := st.Recording(id)
	if !ok {
		return Meeting{}, nil, ErrNotFound
	}
	if st.ReadOnly() {
		return Meeting{}, nil, ErrReadOnly
	}
	var before Meeting
	if r.Conv != nil {
		before = r.Conv.Clone()
	}
	m := before.Clone()
	if err := fn(&m, *r); err != nil {
		return Meeting{}, nil, err
	}
	if meetingSame(before, m) {
		return m, nil, nil
	}
	parts := meetingParts(before, m)
	bumpRevs(&m, parts)
	if ok, err := st.putMeeting(id, &m); err != nil {
		return Meeting{}, nil, err
	} else if !ok {
		return Meeting{}, nil, ErrNotFound
	}
	return m, parts, nil
}
