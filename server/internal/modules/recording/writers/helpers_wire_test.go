package writers

// Test helpers the knowledge base's own tests have under the same names (internal/kb/*_test.go).

import (
	"encoding/json"
	"testing"
)

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
