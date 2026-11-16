package ai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// directAPIError sends one synthetic request through the requested wire protocol and returns its error and request
// count. The response is deliberately identical on every request so an unexpected compatibility retry is observable.
func directAPIError(t *testing.T, api string, status int, body string, header ...string) (error, int) {
	t.Helper()
	u := newUpstream(t, answer(status, body, header...))
	client := New(Config{API: api, BaseURL: u.srv.URL, APIKey: "example-key", Model: "example-model"})
	_, err := client.Chat(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "synthetic input"}}, MaxTokens: 32})
	return err, int(u.sent.Load())
}

func requireDirectPause(t *testing.T, err error, reason string) *PausedError {
	t.Helper()
	var paused *PausedError
	if !errors.As(err, &paused) || paused.Reason != reason {
		t.Fatalf("error = %v, want a %s pause", err, reason)
	}
	if Transient(err) {
		t.Fatalf("pause is transient: %v", err)
	}
	return paused
}

func TestDirectHTTPFailureKindsAcrossProtocols(t *testing.T) {
	apis := []struct{ name, api string }{{"messages", ""}, {"responses", "responses"}, {"chat", "chat"}}
	cases := []struct {
		name      string
		status    int
		body      string
		want      FailureKind
		transient bool
	}{
		{"nested OpenAI model code", 404, `{"error":{"code":"model_not_found","message":"request rejected"}}`, FailureModelUnavailable, false},
		{"nested Anthropic context code", 400, `{"type":"error","error":{"type":"invalid_request_error","code":"context_length_exceeded","message":"request rejected"}}`, FailureContextTooLong, false},
		{"deeper nested context code", 400, `{"error":{"error":{"code":"context_length_exceeded","message":"request rejected"}}}`, FailureContextTooLong, false},
		{"top-level model code", 404, `{"type":"error","code":"model_not_found","message":"request rejected"}`, FailureModelUnavailable, false},
		{"unsupported endpoint beats model wording", 404, `{"error":{"code":"unsupported_endpoint","message":"model example-model does not exist on this endpoint"}}`, FailureOther, false},
		{"unknown code keeps model wording", 404, `{"error":{"code":"future_code","message":"The model example-model does not exist"}}`, FailureModelUnavailable, false},
		{"unknown code keeps context wording", 400, `{"error":{"code":"future_code","message":"prompt is too long: synthetic input"}}`, FailureContextTooLong, false},
		{"unknown code generic message", 400, `{"error":{"code":"future_code","message":"request rejected"}}`, FailureOther, false},
		{"string error wording", 404, `{"error":"unknown model example-model"}`, FailureModelUnavailable, false},
		{"explicit model code beyond ordinary statuses", 500, `{"error":{"code":"model_not_found","message":"request rejected"}}`, FailureModelUnavailable, false},
	}
	for _, protocol := range apis {
		for _, tc := range cases {
			t.Run(protocol.name+"/"+tc.name, func(t *testing.T) {
				err, requests := directAPIError(t, protocol.api, tc.status, tc.body)
				if requests != 1 {
					t.Fatalf("requests = %d, want 1", requests)
				}
				if want := fmt.Sprintf("ai: HTTP %d: %s", tc.status, tc.body); err == nil || err.Error() != want {
					t.Fatalf("error = %v, want %q", err, want)
				}
				if got := FailureKindOf(err); got != tc.want || IsModelUnavailable(err) != (tc.want == FailureModelUnavailable) || IsContextTooLong(err) != (tc.want == FailureContextTooLong) {
					t.Fatalf("failure kind = %v, want %v (%v)", got, tc.want, err)
				}
				if got := Transient(err); got != tc.transient {
					t.Fatalf("Transient(%v) = %v, want %v", err, got, tc.transient)
				}
			})
		}
	}
}

func TestDirectHTTPErrorRetainsSemanticsPastClippedText(t *testing.T) {
	body := `{"padding":"` + strings.Repeat("x", 360) + `","error":{"code":"model_not_found","message":"request rejected"}}`
	err, requests := directAPIError(t, "", 404, body)
	if requests != 1 || !IsModelUnavailable(err) {
		t.Fatalf("requests = %d, error = %v; want one typed model failure", requests, err)
	}
	if want := "ai: HTTP 404: " + strings.TrimSpace(body[:300]); err.Error() != want {
		t.Fatalf("clipped error text changed:\n got %q\nwant %q", err, want)
	}
	contextBody := `{"padding":"` + strings.Repeat("x", 360) + `","error":{"code":"future_code","message":"prompt is too long: synthetic input"}}`
	err, requests = directAPIError(t, "responses", 400, contextBody)
	if requests != 1 || !IsContextTooLong(err) {
		t.Fatalf("requests = %d, error = %v; want the full message's context classification", requests, err)
	}
	unknownBody := `{"padding":"` + strings.Repeat("x", 360) + `","error":{"code":"future_code","message":"quota exhausted"}}`
	err, requests = directAPIError(t, "chat", 429, unknownBody)
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
	// Before full envelopes were parsed, wording beyond the clipped 300-byte body could not turn a plain 429 into quota.
	requireDirectPause(t, err, ReasonBusy)
}

func TestDirectExplicitNoRetryAcrossProtocols(t *testing.T) {
	for _, protocol := range []struct{ name, api string }{{"messages", ""}, {"responses", "responses"}, {"chat", "chat"}} {
		for _, status := range []int{http.StatusInternalServerError, http.StatusRequestTimeout} {
			t.Run(fmt.Sprintf("%s/%d", protocol.name, status), func(t *testing.T) {
				err, requests := directAPIError(t, protocol.api, status, `{"error":{"message":"temporary failure"}}`, "X-Should-Retry", " FaLsE ")
				if requests != 1 || err == nil || IsPaused(err) || Transient(err) {
					t.Fatalf("requests = %d, error = %v, transient = %v; want one non-transient failure", requests, err, Transient(err))
				}
			})
		}
	}
	for _, status := range []int{http.StatusInternalServerError, http.StatusServiceUnavailable, http.StatusRequestTimeout} {
		resp := &http.Response{StatusCode: status, Header: http.Header{"X-Should-Retry": []string{"false"}}}
		if err := newHTTPError(resp, []byte(`{"error":{"message":"temporary failure"}}`)); Transient(err) {
			t.Errorf("status %d with explicit no-retry is transient: %v", status, err)
		}
	}
	for _, value := range []string{"", "true", "0", "falsehood"} {
		resp := &http.Response{StatusCode: http.StatusInternalServerError, Header: http.Header{}}
		if value != "" {
			resp.Header.Set("X-Should-Retry", value)
		}
		if err := newHTTPError(resp, []byte(`{"error":{"message":"temporary failure"}}`)); !Transient(err) {
			t.Errorf("X-Should-Retry %q made an ordinary 500 non-transient", value)
		}
	}
}

func TestDirectNoRetryRateLimitUsesRetryAfterAsQuota(t *testing.T) {
	fakeClock(t)
	for _, protocol := range []struct{ name, api string }{{"messages", ""}, {"responses", "responses"}, {"chat", "chat"}} {
		t.Run(protocol.name, func(t *testing.T) {
			body := `{"error":{"code":"rpm_limit","message":"requests per minute"}}`
			err, requests := directAPIError(t, protocol.api, 429, body, "X-Should-Retry", "false", "Retry-After", "1")
			paused := requireDirectPause(t, err, ReasonQuota)
			if requests != 1 || !paused.Until.Equal(now().Add(time.Second)) {
				t.Fatalf("requests = %d, pause = %+v; want one request and a 1-second quota pause", requests, paused)
			}
		})
	}
	t.Run("zero cooldown is still quota", func(t *testing.T) {
		err, requests := directAPIError(t, "chat", 429, `{"error":{"code":"future_code","message":"request rejected"}}`,
			"X-Should-Retry", "false", "Retry-After", "0")
		paused := requireDirectPause(t, err, ReasonQuota)
		if requests != 1 || !paused.Until.Equal(now()) {
			t.Fatalf("requests = %d, pause = %+v; want one request and a pause until now", requests, paused)
		}
	})
	t.Run("large cooldown caps without overflowing", func(t *testing.T) {
		err, requests := directAPIError(t, "chat", 429, `{"error":{"code":"future_code","message":"request rejected"}}`,
			"X-Should-Retry", "false", "Retry-After", "10000000000")
		paused := requireDirectPause(t, err, ReasonQuota)
		if requests != 1 || !paused.Until.Equal(now().Add(afterMost)) {
			t.Fatalf("requests = %d, pause = %+v; want one request and the maximum pause", requests, paused)
		}
	})
}

func TestDirectPaymentRequiredPausesQuotaAcrossProtocols(t *testing.T) {
	fakeClock(t)
	for _, protocol := range []struct{ name, api string }{{"messages", ""}, {"responses", "responses"}, {"chat", "chat"}} {
		t.Run(protocol.name, func(t *testing.T) {
			err, requests := directAPIError(t, protocol.api, http.StatusPaymentRequired, `{"error":{"message":"payment required"}}`)
			paused := requireDirectPause(t, err, ReasonQuota)
			if requests != 1 || !paused.Until.Equal(now().Add(quotaPause)) {
				t.Fatalf("requests = %d, pause = %+v; want one request and the default quota pause", requests, paused)
			}
		})
	}
}

func TestDirectLimitCodesBeatMisleadingWording(t *testing.T) {
	fakeClock(t)
	apis := []string{"", "responses", "chat"}
	quotaCodes := []string{
		"insufficient_quota", "credit_balance_exhausted", "organization_spend_limit_exceeded",
		"project_spend_limit_exceeded", "account_quota_exhausted", "budget_exhausted",
		"weekly_budget_exhausted", "api_key_model_limit_exhausted", "quota_exhausted",
	}
	for i, code := range quotaCodes {
		t.Run("quota/"+code, func(t *testing.T) {
			body := fmt.Sprintf(`{"error":{"code":%q,"message":"concurrency full; request rejected"}}`, code)
			err, requests := directAPIError(t, apis[i%len(apis)], 429, body)
			requireDirectPause(t, err, ReasonQuota)
			if requests != 1 {
				t.Fatalf("requests = %d, want 1", requests)
			}
		})
	}
	for i, code := range []string{"rpm_limit", "tpm_limit", "concurrency_limit"} {
		t.Run("busy/"+code, func(t *testing.T) {
			body := fmt.Sprintf(`{"error":{"code":%q,"message":"quota exhausted; credit billing"}}`, code)
			err, requests := directAPIError(t, apis[i%len(apis)], 429, body)
			requireDirectPause(t, err, ReasonBusy)
			if requests != 1 {
				t.Fatalf("requests = %d, want 1", requests)
			}
		})
	}
	for _, status := range []int{400, 422} {
		t.Run(fmt.Sprintf("quota code on %d", status), func(t *testing.T) {
			err, requests := directAPIError(t, "responses", status, `{"error":{"code":"budget_exhausted","message":"request rejected"}}`)
			requireDirectPause(t, err, ReasonQuota)
			if requests != 1 {
				t.Fatalf("requests = %d, want 1", requests)
			}
		})
	}
	for _, tc := range []struct {
		status    int
		transient bool
	}{{400, false}, {500, true}} {
		t.Run(fmt.Sprintf("busy code on unrelated status %d", tc.status), func(t *testing.T) {
			err, requests := directAPIError(t, "chat", tc.status, `{"error":{"code":"rpm_limit","message":"request rejected"}}`)
			if requests != 1 || IsPaused(err) || Transient(err) != tc.transient {
				t.Fatalf("requests = %d, error = %v, transient = %v; want unchanged status behavior", requests, err, Transient(err))
			}
		})
	}
	t.Run("unknown code keeps quota wording", func(t *testing.T) {
		err, requests := directAPIError(t, "", 429, `{"error":{"code":"future_code","message":"quota exhausted"}}`)
		requireDirectPause(t, err, ReasonQuota)
		if requests != 1 {
			t.Fatalf("requests = %d, want 1", requests)
		}
	})
	t.Run("unknown code does not change other statuses", func(t *testing.T) {
		err, requests := directAPIError(t, "responses", 500, `{"error":{"code":"future_code","message":"quota exhausted"}}`)
		if requests != 1 || IsPaused(err) || !Transient(err) {
			t.Fatalf("requests = %d, error = %v; want one ordinary transient 500", requests, err)
		}
	})
}

func TestDirectChatNoRetrySkipsCompatibilityRetries(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"max_tokens", 400, `{"error":{"message":"Unsupported parameter: 'max_tokens' is not supported with this model. Use 'max_completion_tokens' instead."}}`},
		{"stream_options", 400, `{"error":{"message":"Unrecognized request argument supplied: stream_options"}}`},
		{"include_usage", 422, `{"detail":[{"loc":["body","include_usage"],"msg":"Extra inputs are not permitted"}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err, requests := directAPIError(t, "chat", tc.status, tc.body, "X-Should-Retry", " FALSE ")
			if requests != 1 {
				t.Fatalf("requests = %d, want the explicit no-retry response to stop at 1", requests)
			}
			if want := fmt.Sprintf("ai: HTTP %d: %s", tc.status, tc.body); err == nil || err.Error() != want || Transient(err) {
				t.Fatalf("error = %v, want %q, non-transient", err, want)
			}
		})
	}
}

func TestDirectChatNoRetrySkipsReasoningEffortRetry(t *testing.T) {
	body := `{"error":{"message":"Unsupported parameter: 'reasoning_effort' is not supported with this model."}}`
	u := newUpstream(t, answer(http.StatusBadRequest, body, "X-Should-Retry", "false"))
	client := New(Config{API: "chat", BaseURL: u.srv.URL, APIKey: "example-key", Model: "example-model", ReasoningEffort: "high"})
	_, err := client.Chat(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "synthetic input"}}, MaxTokens: 32})
	if got := u.sent.Load(); got != 1 {
		t.Fatalf("requests = %d, want the explicit no-retry response to stop at 1", got)
	}
	if want := "ai: HTTP 400: " + body; err == nil || err.Error() != want || Transient(err) {
		t.Fatalf("error = %v, want %q, non-transient", err, want)
	}
}

func TestDirectStreamFailureKinds(t *testing.T) {
	cases := []struct {
		name      string
		api       string
		body      string
		want      FailureKind
		text      string
		transient bool
	}{
		{"messages nested model code", "", `data: {"type":"error","error":{"type":"not_found_error","code":"model_not_found","message":"request rejected"}}` + "\n\n", FailureModelUnavailable, "ai: error: not_found_error: request rejected", false},
		{"messages top-level context code", "", `data: {"type":"error","code":"context_length_exceeded","message":"request rejected"}` + "\n\n", FailureContextTooLong, "ai: error: context_length_exceeded: request rejected", false},
		{"messages whole JSON model code", "", `{"type":"error","error":{"type":"not_found_error","code":"model_not_found","message":"request rejected"}}`, FailureModelUnavailable, "ai: error: not_found_error: request rejected", false},
		{"messages string error model wording", "", `{"error":"unknown model example-model"}`, FailureModelUnavailable, "ai: error: error: unknown model example-model", false},
		{"messages unknown code context wording", "", `data: {"type":"error","error":{"type":"invalid_request_error","code":"future_code","message":"prompt is too long"}}` + "\n\n", FailureContextTooLong, "ai: error: invalid_request_error: prompt is too long", false},
		{"messages unknown code server status unchanged", "", `data: {"type":"error","error":{"type":"api_error","code":"future_code","message":"prompt is too long"}}` + "\n\n", FailureOther, "ai: error: api_error: prompt is too long", true},
		{"messages unsupported endpoint", "", `data: {"type":"error","error":{"type":"not_found_error","code":"unsupported_endpoint","message":"model example-model does not exist"}}` + "\n\n", FailureOther, "ai: error: not_found_error: model example-model does not exist", false},
		{"responses nested model code", "responses", `data: {"type":"response.failed","response":{"error":{"code":"model_not_found","message":"request rejected"}}}` + "\n\n", FailureModelUnavailable, "ai: response.failed: request rejected", false},
		{"responses top-level context code", "responses", `data: {"type":"error","code":"context_length_exceeded","message":"request rejected"}` + "\n\n", FailureContextTooLong, "ai: error: request rejected", false},
		{"responses incomplete context code", "responses", `data: {"type":"response.incomplete","response":{"error":{"code":"context_length_exceeded","message":"request rejected"}}}` + "\n\n", FailureContextTooLong, "ai: response.incomplete: request rejected", false},
		{"responses unknown code context wording", "responses", `data: {"type":"response.failed","response":{"error":{"code":"future_code","message":"prompt is too long"}}}` + "\n\n", FailureContextTooLong, "ai: response.failed: prompt is too long", false},
		{"responses unsupported endpoint", "responses", `data: {"type":"response.failed","response":{"error":{"code":"unsupported_endpoint","message":"model example-model does not exist"}}}` + "\n\n", FailureOther, "ai: response.failed: model example-model does not exist", false},
		{"chat nested model code", "chat", `data: {"error":{"code":"model_not_found","message":"request rejected"}}` + "\n\n", FailureModelUnavailable, "ai: stream error: request rejected", false},
		{"chat nested context code", "chat", `data: {"error":{"code":"context_length_exceeded","message":"request rejected"}}` + "\n\n", FailureContextTooLong, "ai: stream error: request rejected", false},
		{"chat top-level context code", "chat", `data: {"type":"error","code":"context_length_exceeded","message":"request rejected"}` + "\n\n", FailureContextTooLong, "ai: stream error: request rejected", false},
		{"chat unknown code model wording", "chat", `data: {"error":{"code":"future_code","message":"unknown model example-model"}}` + "\n\n", FailureModelUnavailable, "ai: stream error: unknown model example-model", false},
		{"chat unsupported endpoint", "chat", `data: {"error":{"code":"unsupported_endpoint","message":"model example-model does not exist"}}` + "\n\n", FailureOther, "ai: stream error: model example-model does not exist", false},
		{"chat unknown code generic", "chat", `data: {"error":{"code":"future_code","message":"request rejected"}}` + "\n\n", FailureOther, "ai: stream error: request rejected", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err, requests := directAPIError(t, tc.api, 200, tc.body)
			if requests != 1 || err == nil || err.Error() != tc.text {
				t.Fatalf("requests = %d, error = %v, want one request and %q", requests, err, tc.text)
			}
			if got := FailureKindOf(err); got != tc.want {
				t.Fatalf("failure kind = %v, want %v (%v)", got, tc.want, err)
			}
			if got := Transient(err); got != tc.transient {
				t.Fatalf("Transient(%v) = %v, want %v", err, got, tc.transient)
			}
		})
	}
}

func TestDirectStreamLimitCodes(t *testing.T) {
	fakeClock(t)
	cases := []struct {
		name, api, body, reason string
	}{
		{"messages budget code", "", `data: {"type":"error","error":{"type":"rate_limit_error","code":"weekly_budget_exhausted","message":"request rejected"}}` + "\n\n", ReasonQuota},
		{"messages busy code beats type and wording", "", `data: {"type":"error","error":{"type":"rate_limit_error","code":"tpm_limit","message":"quota exhausted"}}` + "\n\n", ReasonBusy},
		{"messages overloaded type preserved", "", `data: {"type":"error","error":{"type":"overloaded_error","message":"request rejected"}}` + "\n\n", ReasonBusy},
		{"responses budget code", "responses", `data: {"type":"response.failed","response":{"error":{"code":"budget_exhausted","message":"request rejected"}}}` + "\n\n", ReasonQuota},
		{"responses busy code beats wording", "responses", `data: {"type":"error","code":"concurrency_limit","message":"quota exhausted"}` + "\n\n", ReasonBusy},
		{"chat budget code", "chat", `data: {"error":{"code":"api_key_model_limit_exhausted","message":"request rejected"}}` + "\n\n", ReasonQuota},
		{"chat busy code beats wording", "chat", `data: {"error":{"code":"rpm_limit","message":"quota exhausted"}}` + "\n\n", ReasonBusy},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err, requests := directAPIError(t, tc.api, 200, tc.body)
			requireDirectPause(t, err, tc.reason)
			if requests != 1 {
				t.Fatalf("requests = %d, want 1", requests)
			}
		})
	}
	t.Run("chat limit keeps its original error text", func(t *testing.T) {
		_, err := readCompletions(strings.NewReader(`data: {"error":{"code":"budget_exhausted","message":"request rejected"}}` + "\n\n"))
		var limit *streamLimit
		if !errors.As(err, &limit) || err.Error() != "ai: stream error: request rejected" || limit.reason != ReasonQuota {
			t.Fatalf("error = %v, limit = %+v; want unchanged text and a quota limit", err, limit)
		}
	})
}
