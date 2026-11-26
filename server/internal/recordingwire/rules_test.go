package recordingwire

import (
	"regexp"
	"testing"
)

// RecordingID accepts exactly what the pattern the knowledge base used accepts.
func TestRecordingIDIsAUUID(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	for _, raw := range []string{"6f1c2a3b-0000-4000-8000-00000000c0de", " 6F1C2A3B-0000-4000-8000-00000000C0DE\n", "",
		"6f1c2a3b-0000-4000-8000-00000000c0d", "6f1c2a3b-0000-4000-8000-00000000c0dee", "6f1c2a3b00000-4000-8000-00000000c0de",
		"6f1c2a3b-0000-4000-8000-00000000c0dg", "6f1c2a3b_0000-4000-8000-00000000c0de", "../../etc/passwd-0000-4000-8000-0000",
		"6f1c2a3b-0000-4000-8000-00000000c0de\n6f1c2a3b-0000-4000-8000-00000000c0de", "６f1c2a3b-0000-4000-8000-00000000c0de"} {
		id, ok := RecordingID(raw)
		if want := re.MatchString(id); ok != want {
			t.Errorf("RecordingID(%q) = %q, %v; the pattern says %v", raw, id, ok, want)
		}
	}
}
