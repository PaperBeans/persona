-- assistant_captures.kind and status hold only what quick capture writes (#15397, docs/plans/plan-typed-core.md step
-- P9): kind is one of server/internal/assistantwire/captures.go's CaptureKindText, CaptureKindURL and CaptureKindVoice
-- (the manager's captureKinds, checked before a capture is kept; 'text' is the column's default), status one of
-- CaptureNew, CaptureSorted, CaptureFiled, CaptureDismissed and CaptureFailed (captureStatuses; 'new' is the default).
-- NOT VALID: new and updated rows are checked, existing rows are not scanned, so the deploy cannot fail on old data; a
-- later migration validates them once the hub's rows have been checked. TestStorageChecksMatchTheCode keeps these lists
-- and the constants together.
ALTER TABLE assistant_captures
    ADD CONSTRAINT assistant_captures_kind_check CHECK (kind IN ('text', 'url', 'voice')) NOT VALID,
    ADD CONSTRAINT assistant_captures_status_check CHECK (status IN ('new', 'sorted', 'filed', 'dismissed', 'failed')) NOT VALID;
