package recordingwire

import (
	"errors"
	"fmt"
)

// MaxAppendSegments is how many segments one append carries at most.
const MaxAppendSegments = 500

// Why the phone has no text for a stretch of the day (CoverageGap.Reason, an open set: an unknown one is kept).
const (
	GapPaused      = "paused"
	GapInterrupted = "interrupted"
	GapStopped     = "stopped"
	GapPending     = "pending"
	GapLost        = "lost"
	GapErased      = "erased"
)

var (
	// ErrKindChanged: a PUT would make a day recording another kind, or another recording a day recording.
	ErrKindChanged = errors.New("a recording becomes a day recording only at its first upload, and stays one")
	// ErrNotDay: an append to a recording that is not a day recording.
	ErrNotDay = errors.New("this recording is not an all-day recording")
	// ErrBaseChanged: an append names a base another PUT replaced; a BaseChangedError says which is current.
	ErrBaseChanged = errors.New("another upload replaced this recording since that base")
	// ErrSegmentsGap: an append's from is above the positions held; a SegmentsGapError says how many there are.
	ErrSegmentsGap = errors.New("segments are missing before from")
	// ErrDayFull: the request would pass maxRecordingSegments positions or maxRecordingText bytes of text; nothing of
	// it was stored. A DayFullError says how many positions there are.
	ErrDayFull = errors.New("this part of the day is full; continue in the next part")
)

// BaseChangedError is ErrBaseChanged with the current base (0: a manager that did not know bases rewrote it) and count.
// errors.As finds it as a BaseChangedError and as a *BaseChangedError, like LiveGapError.
type BaseChangedError struct {
	Base int64
	Next int
}

func (e BaseChangedError) Error() string {
	return fmt.Sprintf("%v: base %d, next %d", ErrBaseChanged, e.Base, e.Next)
}

func (e BaseChangedError) Unwrap() error { return ErrBaseChanged }

func (e BaseChangedError) As(target any) bool {
	if p, ok := target.(**BaseChangedError); ok {
		c := e
		*p = &c
		return true
	}
	return false
}

// SegmentsGapError is ErrSegmentsGap with how many positions the hub holds: the phone sends again from Next.
type SegmentsGapError struct{ Next int }

func (e SegmentsGapError) Error() string {
	return fmt.Sprintf("%v: send from %d", ErrSegmentsGap, e.Next)
}

func (e SegmentsGapError) Unwrap() error { return ErrSegmentsGap }

func (e SegmentsGapError) As(target any) bool {
	if p, ok := target.(**SegmentsGapError); ok {
		c := e
		*p = &c
		return true
	}
	return false
}

// DayFullError is ErrDayFull with how many positions the hub holds.
type DayFullError struct{ Next int }

func (e DayFullError) Error() string { return fmt.Sprintf("%v (next %d)", ErrDayFull, e.Next) }

func (e DayFullError) Unwrap() error { return ErrDayFull }

func (e DayFullError) As(target any) bool {
	if p, ok := target.(**DayFullError); ok {
		c := e
		*p = &c
		return true
	}
	return false
}

// RecordingCoverage is the phone's view of a day recording, sent whole every time: To is the seconds since started_at
// up to which everything it holds is transcribed and appended, Gaps the time with no text and why.
type RecordingCoverage struct {
	To   float64       `json:"to"`
	Gaps []CoverageGap `json:"gaps,omitempty"`
}

// CoverageGap is a stretch of the day with no text: seconds since started_at, and why (Gap*).
type CoverageGap struct {
	From   float64 `json:"from"`
	To     float64 `json:"to"`
	Reason string  `json:"reason"`
}
