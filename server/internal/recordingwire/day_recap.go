package recordingwire

import "time"

// DayRecapEntry is one thing of the day's timeline: a meeting (one the hub knows of when Meeting is set) or what was
// said outside them, with its points.
type DayRecapEntry struct {
	From    float64        `json:"from"`
	To      float64        `json:"to"`
	Kind    string         `json:"kind"`
	Title   string         `json:"title"`
	Meeting *MeetingRef    `json:"meeting,omitempty"`
	Points  []MinutesPoint `json:"points"`
}

// RecapTodo is a to-do made of a recap's action, remembered whatever the version: the action's who and what
// (recapTaskKey) and when it was said.
type RecapTodo struct {
	Task string  `json:"task"`
	At   float64 `json:"at"`
	Todo string  `json:"todo"`
}

// Why a recap became due (RecapTarget.Reason).
const (
	RecapDueEvening  = "evening"  // the recap time of its day
	RecapDueComplete = "complete" // the recording became complete: the final recap
	RecapDueCutoff   = "cutoff"   // 6 hours after its day ended, not complete: the final recap of what arrived
	RecapDueAsked    = "asked"    // POST …/minutes
	RecapDueRepair   = "repair"   // a PUT set a new base while a recap existed
)

// Why a final recap is final (DayRecap.FinalReason).
const (
	FinalComplete = "complete"
	FinalCutoff   = "cutoff"
)

// Kinds of a recap's timeline entry.
const (
	RecapMeeting = "meeting"
	RecapOutside = "outside"
)

// RecapTarget is a due recap as it became due: its number (Recording.RecapTargets then), why, the recording's base,
// revision and coverage then, and when. Its tries are counted here, as the minutes' are for a revision: Fails unusable
// answers, Timeouts tries that ran out of time, the next not before RetryAt (minutes_timeout.go); new text neither
// moves nor resets it, and a PUT moves it to the new base (Base). It reads what the hub holds when it runs.
type RecapTarget struct {
	N        int       `json:"n"`
	Reason   string    `json:"reason"`
	Base     int64     `json:"base"`
	Revision int64     `json:"revision"`
	To       float64   `json:"to"`
	At       time.Time `json:"at"`
	Fails    int       `json:"fails,omitempty"`
	Timeouts int       `json:"timeouts,omitempty"`
	RetryAt  time.Time `json:"retry_at,omitzero"`
}

// DayRecap is recap.json, and detail.recap of GET /api/v1/kb/recordings/{id}: times are seconds since the recording's
// started_at, the clock of its segments.
type DayRecap struct {
	Version     int             `json:"version"`  // counts the recaps written for this recording
	Revision    int64           `json:"revision"` // the recording revision it was made from
	GeneratedAt time.Time       `json:"generated_at"`
	Day         string          `json:"day"`
	Covers      RecapSpan       `json:"covers"` // up to the end of the last chunk it read
	Final       bool            `json:"final"`
	FinalReason string          `json:"final_reason,omitempty"` // FinalComplete or FinalCutoff, with Final
	Summary     string          `json:"summary"`
	Timeline    []DayRecapEntry `json:"timeline,omitempty"`
	Actions     []MinutesAction `json:"actions,omitempty"`
	Gaps        []CoverageGap   `json:"gaps,omitempty"`    // the coverage's gaps inside Covers
	Partial     bool            `json:"partial,omitempty"` // the day was too long: some of it was not read
	// Language is what it is written in, its Markdown's headings too (language.go): an i18n code (zh-Hans, en, ja);
	// a recap written before it was kept has none, and is Chinese.
	Language string `json:"language,omitempty"`
	Markdown string `json:"markdown"`
}

// RecapSpan is a stretch of the day, seconds since started_at.
type RecapSpan struct {
	From float64 `json:"from"`
	To   float64 `json:"to"`
}
