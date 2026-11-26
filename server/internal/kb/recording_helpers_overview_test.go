package kb

import (
	"encoding/json"
	"testing"
)

// jsonEqual: a and b are the same JSON once decoded into T (and so in T's field order).
func jsonEqual[T any](t *testing.T, a, b any) bool {
	t.Helper()
	norm := func(v any) string {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var x T
		if err := json.Unmarshal(raw, &x); err != nil {
			t.Fatal(err)
		}
		out, _ := json.Marshal(x)
		return string(out)
	}
	return norm(a) == norm(b)
}
