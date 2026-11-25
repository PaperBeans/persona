package recordingwire

import (
	"errors"
	"fmt"
	"time"
)

// SessionRef names one session the way the rest of the API does.
type SessionRef struct {
	MachineID string `json:"machine_id"`
	SessionID string `json:"session_id"`
}

// LiveSegment is one finished sentence of the live log: seconds from the recording's start, as the upload's
// segments. Text "" is a placeholder position (an empty sentence), never fed.
type LiveSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
	// Source is whose sentence it is in hub mode (SourceHub, SourceDevice: filled from the phone's uploads); absent in
	// device mode. Only the hub sets it.
	Source string `json:"source,omitempty"`
}

// AskSource is an earlier meeting (kind recordings) or a project (kind projects) an answer drew on; When, Start and End
// are the meeting's minutes' (#4713: clients format Start / End and show When only without them).
type AskSource struct {
	Kind  string    `json:"kind"`
	ID    string    `json:"id"`
	Name  string    `json:"name"`
	When  string    `json:"when,omitempty"`
	Start time.Time `json:"start,omitzero"`
	End   time.Time `json:"end,omitzero"`
}

// LiveAsk is a question asked during the meeting (POST /api/v1/recordings/{id}/ask) and its answer.
type LiveAsk struct {
	ID       string `json:"id"`
	Question string `json:"question"`
	// QuestionSent is the question as the request had it ("" without one), what a retry with the same id is matched on.
	QuestionSent   string      `json:"question_sent,omitempty"`
	QuestionSource string      `json:"question_source"`
	At             *float64    `json:"at,omitempty"`
	LiveNext       *int        `json:"live_next,omitempty"`
	State          string      `json:"state"` // pending | answered | failed
	Answer         string      `json:"answer"`
	Lang           string      `json:"lang"`
	Speak          bool        `json:"speak"`
	Sources        []AskSource `json:"sources"`
	Error          string      `json:"error,omitempty"`
	AskedAt        time.Time   `json:"asked_at"`
	AnsweredAt     *time.Time  `json:"answered_at,omitempty"`
	// Fed is the hub's: the answer already went to the attending session in a batch.
	Fed bool `json:"fed,omitempty"`
	// Room meetings (docs/assistant/meeting-minutes.md "Asking the room"). Via is the way it is answered (AskViaRoom or
	// AskViaMaterial), fixed when its first attempt goes out ("" before); Spoken the request's spoken (a Talk span);
	// Heard the lines a heard question was made of; Attempts the messages it sent the room, the latest last.
	Via      string        `json:"via,omitempty"`
	Spoken   bool          `json:"spoken,omitempty"`
	Heard    []LiveSegment `json:"heard,omitempty"`
	Attempts []AskAttempt  `json:"attempts,omitempty"`
}

// AskAttempt is one message a room ask sent the room: its attempt number k (1 for the first), its client_msg_id
// (ask-<id>-<k>), the room's last line before it (where its turn is read from) and when it went out.
type AskAttempt struct {
	K      int       `json:"k"`
	MsgID  string    `json:"msg_id"`
	Base   int       `json:"base"`
	SentAt time.Time `json:"sent_at,omitzero"`
}

// Live is live.json: the live log of an attended meeting and what became of it.
type Live struct {
	Segments []LiveSegment `json:"segments"` // index = position
	Bytes    int           `json:"bytes"`    // the live text's bytes in all (maxLiveBytes)
	Fed      int           `json:"fed"`      // positions given to FedTo at least once
	// FedTo is the session last fed (resolved: the main chat by its real ids), FedAt when.
	FedTo *SessionRef `json:"fed_to,omitempty"`
	FedAt time.Time   `json:"fed_at,omitzero"`
	// EndedAt is when the hub first saw an upload carrying ended_at (PutRecording); EndFed that the end batch went out.
	EndedAt time.Time `json:"ended_at,omitzero"`
	EndFed  bool      `json:"end_fed,omitempty"`
	// Problem is the last feeding problem (attendee_unavailable, attendee_not_allowed), the feeder's.
	Problem string `json:"problem,omitempty"`
	// Full: sentences were refused for the limits (ErrLiveFull), so the phone stopped sending them (problem live_full).
	Full bool      `json:"full,omitempty"`
	Asks []LiveAsk `json:"asks,omitempty"`
	// AsksRev is the asks' revision (the meeting_live event's asks): it goes up whenever they change (LiveUpdate).
	AsksRev int64 `json:"asks_rev,omitempty"`
	// Topics are the meeting's topics as its room set them (meeting_topics_set, TopicsSet), TopicsAt their last change
	// and TopicsRev their revision (the meeting_live event's topics), which goes up whenever they change (LiveUpdate).
	// Kept here, they go with the transcript: the trim takes live.json.
	Topics    []LiveTopic `json:"topics,omitempty"`
	TopicsAt  time.Time   `json:"topics_at,omitzero"`
	TopicsRev int64       `json:"topics_rev,omitempty"`
	// Highlights are the meeting's highlights so far as its room set them (meeting_highlights_set, HighlightsSet),
	// the earliest first; HighlightsAt their last change and HighlightsRev their revision (the meeting_live event's
	// highlights), which goes up whenever they change (LiveUpdate). They go with the transcript as the topics do.
	Highlights    []LiveHighlight `json:"highlights,omitempty"`
	HighlightsAt  time.Time       `json:"highlights_at,omitzero"`
	HighlightsRev int64           `json:"highlights_rev,omitempty"`
}

// LiveTopic is one topic of the meeting: its title, its state, the seconds from the start when it began (nil: not
// said) and the room's short note on it.
type LiveTopic struct {
	Title string   `json:"title"`
	State string   `json:"state"`
	At    *float64 `json:"at,omitempty"`
	Note  string   `json:"note,omitempty"`
}

// LiveHighlight is one highlight of the meeting so far: one short sentence and the minute of the live log it was
// made in, At the start of that minute's first line and End the end of its last, in seconds from the recording's
// start.
type LiveHighlight struct {
	Text string  `json:"text"`
	At   float64 `json:"at"`
	End  float64 `json:"end"`
}

// HighlightSaid is one highlight as the room names it (meeting_highlights_set): its text and SaidAt, the clock time
// (H:MM or HH:MM) of a batch line it was made in. Malformed: the room's item was not an object with a string text and
// a string said_at, which CleanHighlights refuses in its place.
type HighlightSaid struct {
	Text, SaidAt string
	Malformed    bool
}

// Attendance roles. Off is stored as "" (omitted in meta.json). Notes is a room meeting's only (#8851): its room keeps
// the live log, the topics and the highlights while Persona answers nothing (Answering), so a meeting Persona was not
// invited to still has its live view; #2981's top-level attendance never takes it.
const (
	AttendanceOff         = "off"
	AttendanceNotes       = "notes"
	AttendanceListener    = "listener"
	AttendanceParticipant = "participant"
)

// What was wrong with an upload's attendance fields (RecordingPut.AttendanceProblem); attending is off then.
const (
	AttendanceUnknownRole = "unknown_role" // attendance is not off, listener or participant
	AttendanceNotMeeting  = "not_meeting"  // a role on a recording whose kind is not meeting
	AttendanceBadSession  = "bad_session"  // attending_session has exactly one id, or one over maxSessionIDRunes
)

// The ways an ask is answered (LiveAsk.Via).
const (
	AskViaRoom     = "room"
	AskViaMaterial = "material"
)

// The topics of a room meeting (docs/assistant/meeting-minutes.md "Phase 2: live view"): each done, current (at most
// one) or open, set whole by the room.
const (
	TopicDone    = "done"
	TopicCurrent = "current"
	TopicOpen    = "open"
	// The limits of meeting_topics_set: a call over one is refused, saying which.
	MaxTopics     = 20
	MaxTopicTitle = 80  // characters
	MaxTopicNote  = 200 // characters
)

// The limits of meeting_highlights_set (docs/assistant/meeting-minutes.md "Phase 2b: highlights"): a call over one is
// refused, saying which.
const (
	MaxHighlights    = 4
	MaxHighlightText = 80 // characters, once on one line
)

var (
	// ErrNotAttending: the stored upload has attendance off.
	ErrNotAttending = errors.New("Persona does not attend this recording")
	// ErrLiveGap: from is above the positions held; a LiveGapError says how many there are.
	ErrLiveGap = errors.New("live sentences are missing before from")
	// ErrLiveFull: the live log reached maxLivePositions or maxLiveBytes; nothing of the request was stored.
	ErrLiveFull = errors.New("the live log of this recording is full")
	// ErrBadLive wraps what is wrong with a live request (a negative from, too many sentences).
	ErrBadLive = errors.New("invalid live sentences")
	// ErrBadTopics wraps the limit a meeting_topics_set call broke (CleanTopics).
	ErrBadTopics = errors.New("invalid topics")
	// ErrBadHighlights wraps the limit a meeting_highlights_set call broke (CleanHighlights, HighlightsSet).
	ErrBadHighlights = errors.New("invalid highlights")
)

// LiveGapError is ErrLiveGap with how many positions the hub holds: the phone sends again from Next. errors.As finds
// it as a LiveGapError and as a *LiveGapError.
type LiveGapError struct{ Next int }

func (e LiveGapError) Error() string { return fmt.Sprintf("%v: send from %d", ErrLiveGap, e.Next) }

func (e LiveGapError) Unwrap() error { return ErrLiveGap }

func (e LiveGapError) As(target any) bool {
	if p, ok := target.(**LiveGapError); ok {
		c := e
		*p = &c
		return true
	}
	return false
}
