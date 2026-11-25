package kb

// The read path of live.go: Core, with or without the recording module (docs/plans/plan-kb-recording-port.md,
// section 1; docs/architecture/core-and-modules.md "Recording").

import (
	"bytes"
	"encoding/json"
)

const liveFile = "live.json"

// jsonNull: a field absent or null.
func jsonNull(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) == 0 || string(raw) == "null"
}
