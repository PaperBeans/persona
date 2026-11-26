package kb

import (
	"github.com/nwverse/persona/server/internal/recordingwire"
	"slices"
	"testing"
)

// Clone is deep: a change to a clone's transcription (its timeline, its gaps) never reaches the original, so
// meetingParts sees it.
func TestMeetingCloneTranscription(t *testing.T) {
	m := Meeting{Transcription: &MeetingTranscription{Mode: TranscriptionHub, State: TranscriptionLive, Gaps: [][2]float64{{1, 2}},
		Timeline: &Timeline{Roles: []TimelineRole{{At: 0, Attendance: recordingwire.AttendanceListener}}, Speaking: []recordingwire.SpokenSpan{{Start: 3, End: 4}}}}}
	c := m.Clone()
	c.Transcription.Gaps[0][1] = 9
	c.Transcription.Timeline.Roles[0].Attendance = AttendanceOff
	c.Transcription.Timeline.Speaking[0].Start = 0
	c.Transcription.State = TranscriptionFinal
	if m.Transcription.Gaps[0][1] != 2 || m.Transcription.Timeline.Roles[0].Attendance != recordingwire.AttendanceListener ||
		m.Transcription.Timeline.Speaking[0].Start != 3 || m.Transcription.State != TranscriptionLive {
		t.Fatalf("the clone shares memory: %+v %+v", m.Transcription, m.Transcription.Timeline)
	}
	if p := meetingParts(m, c); !slices.Equal(p, []string{PartTranscription}) {
		t.Fatalf("parts %v", p)
	}
}
