package ai

import (
	"errors"
	"fmt"
	"testing"
)

func TestFailureKindForSyntheticEnvelopes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   FailureKind
	}{
		{"nested OpenAI model code beats wording", 400, `{"error":{"code":"model_not_found","message":"request rejected"}}`, FailureModelUnavailable},
		{"top-level Anthropic context code", 400, `{"type":"error","code":"context_length_exceeded","message":"request rejected"}`, FailureContextTooLong},
		{"nested Anthropic model code", 404, `{"type":"error","error":{"type":"not_found_error","code":"model_not_found","message":"request rejected"}}`, FailureModelUnavailable},
		{"deeper nested error", 400, `{"error":{"error":{"code":"context_length_exceeded","message":"request rejected"}}}`, FailureContextTooLong},
		{"explicit code beyond ordinary statuses", 500, `{"error":{"code":"model_not_found"}}`, FailureModelUnavailable},
		{"unknown code keeps model wording fallback", 404, `{"error":{"code":"future_code","message":"The model example-model does not exist"}}`, FailureModelUnavailable},
		{"unknown code keeps context wording fallback", 400, `{"error":{"code":"future_code","message":"prompt is too long: synthetic input"}}`, FailureContextTooLong},
		{"unknown code generic message", 400, `{"error":{"code":"future_code","message":"request rejected"}}`, FailureOther},
		{"unsupported endpoint code beats model wording", 404, `{"error":{"code":"unsupported_endpoint","message":"model example-model does not exist on this endpoint"}}`, FailureOther},
		{"plain missing endpoint", 404, `404 page not found`, FailureOther},
		{"string error wording", 404, `{"error":"unknown model example-model"}`, FailureModelUnavailable},
		{"model wording", 404, `{"error":{"type":"not_found_error","message":"example-gateway knows no model example-model"}}`, FailureModelUnavailable},
		{"context wording", 400, `{"error":{"message":"input exceeds the model context window"}}`, FailureContextTooLong},
		{"context code alone", 400, `{"error":{"code":"context_length_exceeded"}}`, FailureContextTooLong},
		{"unrelated status keeps wording behavior", 500, `{"error":{"message":"prompt is too long"}}`, FailureOther},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := FailureKindFor(tc.status, []byte(tc.body)); got != tc.want {
				t.Fatalf("FailureKindFor() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFailureKindOfUnwraps(t *testing.T) {
	base := errors.New("synthetic upstream failure")
	err := withFailureKind(fmt.Errorf("outer: %w", base), FailureModelUnavailable)
	if !IsModelUnavailable(fmt.Errorf("call: %w", err)) || IsContextTooLong(err) {
		t.Fatalf("classification lost through wrapping: %v", err)
	}
	if !errors.Is(err, base) || err.Error() != "outer: synthetic upstream failure" {
		t.Fatalf("error identity or text changed: %v", err)
	}
	if FailureKindOf(nil) != FailureOther || FailureKindOf(base) != FailureOther {
		t.Fatal("unclassified error acquired a kind")
	}
}
