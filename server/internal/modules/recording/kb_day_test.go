package recording

import (
	"github.com/nwverse/persona/server/internal/recordingwire"
	"testing"
)

// The push counts only meetings the hub knows of (#5051): a day with one recorded meeting, and a one-to-one, a hallway
// chat and lunch the model still called meetings, reads "1 meeting"; the same meeting in a second entry counts once.
func TestDayRecapCountsOnlyKnownMeetings(t *testing.T) {
	known := &recordingwire.MeetingRef{Kind: recordingwire.KindRecording, ID: "rec-standup", Name: "Standup"}
	d := recordingwire.DayRecap{Timeline: []recordingwire.DayRecapEntry{
		{Kind: recordingwire.RecapMeeting, Title: "Standup", Meeting: known},
		{Kind: recordingwire.RecapMeeting, Title: "One-to-one about the release"},
		{Kind: recordingwire.RecapMeeting, Title: "Hallway chat"},
		{Kind: recordingwire.RecapMeeting, Title: "Lunch", Meeting: &recordingwire.MeetingRef{}},
		{Kind: recordingwire.RecapOutside, Title: "Writing the report"},
		{Kind: recordingwire.RecapMeeting, Title: "Standup, after the break", Meeting: known},
	}, Actions: []recordingwire.MinutesAction{{What: "send the notes"}}}
	if meetings, actions := dayRecapCounts(nil, "2026-11-11", "r1", d); meetings != 1 || actions != 1 {
		t.Fatalf("meetings %d actions %d, want 1 and 1", meetings, actions)
	}
}
