package writers

// Test helpers the knowledge base's own tests have under the same names (internal/kb/*_test.go).

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

// echoUpload is a complete meeting upload of body's fields (segments, spoken) over the defaults, decoded as the route
// decodes it.
func echoUpload(t *testing.T, start time.Time, fields string) RecordingUpload {
	t.Helper()
	ended := start.Add(time.Minute)
	body := fmt.Sprintf(`{"revision":1,"started_at":%q,"ended_at":%q,"duration":60,"language":"en-US","device":"phone",`+
		`"kind":"meeting","complete":true,%s}`, start.Format(time.RFC3339), ended.Format(time.RFC3339), fields)
	var u RecordingUpload
	if err := json.Unmarshal([]byte(body), &u); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return u
}

// storedSegments is segments.json as written, each segment as a JSON object.
func storedSegments(t *testing.T, e *testEngine, id string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(e.Store.Path(KindRecording, id, "segments.json"))
	if err != nil {
		t.Fatal(err)
	}
	var segs []map[string]any
	if err := json.Unmarshal(raw, &segs); err != nil {
		t.Fatal(err)
	}
	return segs
}
