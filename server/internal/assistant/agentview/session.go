package agentview

import (
	"github.com/nwverse/persona/server/internal/domainkit/fail"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
)

// Session is one session as the view needs it: where the hub says its turn stands, how
// that turn ended, and where fleetcount put its row. The zero Session is no session; only Build makes one, and Decide
// raises a defect on it.
type Session struct {
	host         Host
	background   Background
	failure      Failure
	asking       Asking
	recency      Recency
	row          Row
	muting       Muting
	resumability Resumability
}

// SessionTurnStep is the builder's first stage: where the session's turn stands. The zero SessionTurnStep is the
// builder's start, as NewSessionBuilder gives it.
type SessionTurnStep struct{ sessionTurnStep Session }

// SessionEndingStep is the builder's second stage: how its last turn ended. The zero SessionEndingStep is a skipped
// stage, and Build refuses what it leaves out.
type SessionEndingStep struct{ sessionEndingStep Session }

// SessionPlaceStep is the builder's third stage: where its row is. The zero SessionPlaceStep is a skipped stage, and
// Build refuses what it leaves out.
type SessionPlaceStep struct{ sessionPlaceStep Session }

// SessionBuilder is the builder's last stage. The zero SessionBuilder is a skipped stage, and Build refuses what it
// leaves out.
type SessionBuilder struct{ sessionBuilder Session }

// NewSessionBuilder starts a session.
func NewSessionBuilder() SessionTurnStep { return SessionTurnStep{} }

// Turn is where the hub says the session's turn stands, and what runs in the background once it ended.
func (s SessionTurnStep) Turn(host Host, background Background) SessionEndingStep {
	v := s.sessionTurnStep
	v.host, v.background = host, background
	return SessionEndingStep{sessionEndingStep: v}
}

// Ending is how its last turn (or its start) ended: an error, a question to the owner, and whether it finished a short
// while ago.
func (s SessionEndingStep) Ending(failure Failure, asking Asking, recency Recency) SessionPlaceStep {
	v := s.sessionEndingStep
	v.failure, v.asking, v.recency = failure, asking, recency
	return SessionPlaceStep{sessionPlaceStep: v}
}

// Place is where fleetcount put its row, whether the owner muted it, and whether an interrupted one can resume.
func (s SessionPlaceStep) Place(row Row, muting Muting, resumability Resumability) SessionBuilder {
	v := s.sessionPlaceStep
	v.row, v.muting, v.resumability = row, muting, resumability
	return SessionBuilder{sessionBuilder: v}
}

// Build is the session, or every part a skipped stage or a zero value left out, in order.
func (b SessionBuilder) Build() (out outcome.Of[Session, Fail]) {
	v := b.sessionBuilder
	var parts []fail.Invalid
	parts = wrong(parts, v.host != (Host{}), "agentview.session.no_host", "host")
	parts = wrong(parts, v.background != (Background{}), "agentview.session.no_background", "background")
	parts = wrong(parts, v.failure != (Failure{}), "agentview.session.no_failure", "failure")
	parts = wrong(parts, v.asking != (Asking{}), "agentview.session.no_asking", "asking")
	parts = wrong(parts, v.recency != (Recency{}), "agentview.session.no_recency", "recency")
	parts = wrong(parts, v.row != nil, "agentview.session.no_row", "row")
	parts = wrong(parts, v.muting != (Muting{}), "agentview.session.no_muting", "muting")
	parts = wrong(parts, v.resumability != (Resumability{}), "agentview.session.no_resumability", "resumability")
	if len(parts) > 0 {
		return out.Err(invalidParts(parts))
	}
	return out.Ok(v)
}
