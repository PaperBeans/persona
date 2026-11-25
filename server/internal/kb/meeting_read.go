package kb

// The read path of meeting.go: Core, with or without the recording module (docs/plans/plan-kb-recording-port.md,
// section 1; docs/architecture/core-and-modules.md "Recording").

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/nwverse/persona/server/internal/recordingwire"
)

// The meeting conversation (docs/assistant/meeting-minutes.md "Meeting conversation (#3862)"): a meeting recording
// whose upload carries room is a room meeting, attended by a conversation of its own (the room) in the role room says.
// What the manager keeps of it (the room's role, state and ids, the private mark, the follow-up and the creations
// asked of a node that are not settled yet) is meeting.json beside meta.json, never meta.json: an older manager after a
// rollback rewrites meta.json without the fields it does not know and, when it starts, resumes feeding every recording
// whose meta.json has an attendance, so a room meeting's meta.json never carries one (fail closed). The trim keeps
// meeting.json (an older manager's trim removes it, which leaves the meeting unmarked: private); search skips it.

const meetingFile = "meeting.json"

// Re-exported from recordingwire.
const (
	RoomNone        = recordingwire.RoomNone
	RoomStarting    = recordingwire.RoomStarting
	RoomReady       = recordingwire.RoomReady
	RoomUnavailable = recordingwire.RoomUnavailable
	RoomEnded       = recordingwire.RoomEnded
	RoomDeleted     = recordingwire.RoomDeleted
)

// Meeting is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type Meeting = recordingwire.Meeting

// MeetingRoom is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type MeetingRoom = recordingwire.MeetingRoom

// readMeeting is recording id's meeting.json, read from disk (an upload's undo journal never holds it); false when
// there is none.
func (s *Store) readMeeting(id string) (*Meeting, error) {
	raw, err := os.ReadFile(s.path(KindRecording, id, meetingFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m Meeting
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("meeting of %s: %w", id, err)
	}
	return &m, nil
}

// metaWithMeeting is a recording's meta.json as detail.meta shows it: with its meeting.json's private mark, room,
// follow-up and transcription (its mode, and in hub mode its state and gaps); a room meeting's attendance is its
// room's role (absent when off), never an attending_session.
func metaWithMeeting(raw []byte, m *Meeting) []byte {
	if m == nil {
		return raw
	}
	var meta map[string]json.RawMessage
	if json.Unmarshal(raw, &meta) != nil || meta == nil {
		return raw
	}
	set := func(k string, v any) {
		if b, err := json.Marshal(v); err == nil {
			meta[k] = b
		}
	}
	if m.Private != nil {
		set("private", *m.Private)
	}
	if v := m.RoomView(); v != nil {
		set("room", v)
		delete(meta, "attending_session")
		delete(meta, "attendance")
		if m.Room.Attendance != "" {
			if trimmed := meta["trimmed_at"]; jsonNull(trimmed) {
				set("attendance", m.Room.Attendance)
			}
		}
	}
	if v := m.FollowupView(); v != nil {
		set("followup", v)
	}
	if v := m.TranscriptionView(nil); v != nil {
		set("transcription", v)
	}
	if len(m.Handoffs) > 0 {
		set("handoffs", m.Handoffs)
	}
	out, err := json.Marshal(meta)
	if err != nil {
		return raw
	}
	return out
}
