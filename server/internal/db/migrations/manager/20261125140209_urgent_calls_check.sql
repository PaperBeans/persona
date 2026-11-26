-- urgent_calls.state holds only the states the urgent-call code writes (#15397, docs/plans/plan-typed-core.md step P9):
-- callRinging, callAnswered, callDeclined, callMissed, callEnded, callFailed and callCancelled in
-- server/internal/assistant/urgent_call.go. callSeen is a phone's report that stops the reminders; it never becomes the
-- state. outcome is left without one: it is the phone's own words about the call (callOutcomeMax), not a closed set.
-- NOT VALID: new and updated rows are checked, existing rows are not scanned, so the deploy cannot fail on old data; a
-- later migration validates it once the hub's rows have been checked. TestStorageChecksMatchTheCode keeps this list and
-- the constants together.
ALTER TABLE urgent_calls ADD CONSTRAINT urgent_calls_state_check
    CHECK (state IN ('ringing', 'answered', 'declined', 'missed', 'ended', 'failed', 'cancelled'))
    NOT VALID;
