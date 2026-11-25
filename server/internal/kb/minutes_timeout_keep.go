package kb

// The writes of minutes_timeout.go that delete, retention and recovery need: Core, with or without the recording module (docs/plans/plan-kb-recording-port.md,
// section 1; docs/architecture/core-and-modules.md "Recording").

import (
	"time"
)

// A slow knowledge-base model (#3170). A minutes answer is several times longer than a summary, and a model that needs
// more than a call's time for it is slow, not wrong. So a try that runs out of its time (one call, or the whole job) is
// not an unusable answer: it is counted apart, the recording waits before its next try (the other recordings go first
// meanwhile), and each try gets one minutesCallTimeout more than the one before. minutesTimeoutTries of them for one
// revision make the minutes failed, so a recording no model ever finishes still can't hold the queue for ever.

const (
	minutesTimeoutTries = 4               // tries of one revision that ran out of time before the minutes are failed
	minutesRetry        = 2 * time.Minute // the wait after the first of them, doubled after each further one
)
