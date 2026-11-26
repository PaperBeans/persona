package writers

// Test helpers the knowledge base's own tests have under the same names (internal/kb/*_test.go).

import (
	"github.com/nwverse/persona/server/internal/recordingwire"
	"time"
)

// attendedUpload is the upload an attending phone sends when the meeting starts: a meeting, no text yet.
func attendedUpload(rev int64, start time.Time, role string) RecordingUpload {
	u := recUpload(rev, start, false)
	u.Kind, u.Attendance = recordingwire.RecordingMeeting, role
	return u
}
