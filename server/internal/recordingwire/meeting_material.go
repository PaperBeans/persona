package recordingwire

import "time"

// MeetingMaterial is everything an answer during recording id's meeting may draw on.
type MeetingMaterial struct {
	// Title and Calendar are the upload's own (the user's title, the phone's calendar hint), never the knowledge
	// base's matched calendar item; Language is the recording's.
	Title    string
	Calendar *RecordingCalendar
	Language string
	// Lines are the latest materialLiveRunes of live text, oldest first (an older line cut at its head to fit).
	Lines []LiveSegment
	// Asks are this meeting's questions already answered, oldest first.
	Asks []LiveAsk
	// Photos are the words of this meeting's listed photos that have any, oldest first, the latest materialPhotoRunes
	// of them (other people's material, as the lines are).
	Photos []PhotoText
	// EarlierAvailable distinguishes an allowed but empty lookup from material excluded by the privacy policy.
	EarlierAvailable bool
	// Earlier are at most materialMaxEarlier earlier meetings, the best ranked first.
	Earlier []EarlierMeeting
}

// EarlierMeeting is an earlier meeting as the material has it: its minutes, reduced to what may be said.
type EarlierMeeting struct {
	ID   string
	Name string // the minutes' title (their when without one)
	When string // the minutes' when: a display string in the user's zone
	// Start and End are the minutes' span (#4713), for an answer's sources.
	Start, End time.Time
	// Minutes holds only the title, when, attendees, points (text, who), decisions and open questions (text), and
	// action items (who, what, by, due_date, todo_id).
	Minutes  Minutes
	Projects []EarlierProject
}

// EarlierProject is a project an earlier meeting is linked to: its name and status, nothing else.
type EarlierProject struct {
	Slug   string
	Name   string
	Status string // active | paused | done
}
