package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/ai"
)

type modelProbeTestResult struct {
	Index               int            `json:"index"`
	ID                  string         `json:"id"`
	Label               string         `json:"label"`
	OK                  bool           `json:"ok"`
	MS                  int64          `json:"ms"`
	HTTPStatus          int            `json:"http_status"`
	Error               string         `json:"error"`
	RetryAfter          string         `json:"retry_after"`
	BlockTypes          []string       `json:"block_types"`
	Text                string         `json:"text"`
	TextTruncated       bool           `json:"text_truncated"`
	GenerationTruncated bool           `json:"generation_truncated"`
	Usage               map[string]int `json:"usage"`
	Attempts            int            `json:"attempts"`
	Effort              string         `json:"effort"`
}

type modelProbeTestOutput struct {
	Provider  string                 `json:"provider"`
	API       string                 `json:"api"`
	Model     string                 `json:"model"`
	Effort    string                 `json:"effort"`
	Results   []modelProbeTestResult `json:"results"`
	Total     int                    `json:"total"`
	Succeeded int                    `json:"succeeded"`
	Failed    int                    `json:"failed"`
	TotalMS   int64                  `json:"total_ms"`
}

func modelProbeTestService(t *testing.T, api, baseURL string) (*Service, http.Handler) {
	t.Helper()
	s, mux := launcherTestService(t)
	saved, err := json.Marshal(map[string]any{
		"name": "managed", "kind": "api", "api": api, "base_url": baseURL,
		"model": "saved-model", "effort": "high", "api_key": fakeKey,
		"headers": "X-Test-Secret: hdr-secret-value",
	})
	if err != nil {
		t.Fatal(err)
	}
	if code, raw := launcherCall(t, mux, http.MethodPost, "/api/v1/launchers", string(saved)); code != http.StatusOK {
		t.Fatalf("save managed provider: %d %s", code, raw)
	}
	return s, mux
}

func runModelProbeTest(t *testing.T, s *Service, args string) (modelProbeTestOutput, string) {
	t.Helper()
	raw, err := s.runTool(context.Background(), "server", "main", "model_probe", json.RawMessage(args))
	if err != nil {
		t.Fatalf("model_probe: %v", err)
	}
	if strings.Contains(raw, fakeKey) || strings.Contains(raw, "hdr-secret-value") {
		t.Fatalf("model_probe leaked a credential: %s", raw)
	}
	var out modelProbeTestOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode model_probe result: %v: %s", err, raw)
	}
	return out, raw
}

func TestModelProbeWireProtocolsAndResults(t *testing.T) {
	for _, tc := range []struct {
		api       string
		response  string
		wantPath  string
		wantTypes []string
		wantText  string
		usageKey  string
		usageWant int
	}{
		{
			api:      "messages",
			response: `{"type":"message","content":[{"type":"thinking","thinking":"not returned"},{"type":"text","text":"answer ` + fakeKey + ` ` + "hdr-secret-value" + `"},{"type":"text","text":"!"}],"usage":{"input_tokens":3,"output_tokens":2,"cache_creation":{"ephemeral_5m_input_tokens":1}}}`,
			wantPath: "/v1/messages", wantTypes: []string{"thinking", "text"}, wantText: "answer *** ***!",
			usageKey: "input_tokens", usageWant: 3,
		},
		{
			api: "chat",
			response: "data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"not returned\"}}]}\n\n" +
				"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"answer " + fakeKey + " " + "hdr-secret-value" + "!\"}}]}\n\n" +
				"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
				"data: {\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":2}}\n\n" +
				"data: [DONE]\n\n",
			wantPath: "/v1/chat/completions", wantTypes: []string{"reasoning_content", "text"}, wantText: "answer *** ***!",
			usageKey: "prompt_tokens", usageWant: 4,
		},
	} {
		t.Run(tc.api, func(t *testing.T) {
			var body map[string]any
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.wantPath {
					t.Errorf("path = %q, want %q", r.URL.Path, tc.wantPath)
				}
				if r.Header.Get("x-api-key") != fakeKey && r.Header.Get("Authorization") != "Bearer "+fakeKey {
					t.Errorf("managed credential was not used: %v", r.Header)
				}
				if r.Header.Get("X-Test-Secret") != "hdr-secret-value" {
					t.Errorf("managed custom header was not used: %v", r.Header)
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				_, _ = w.Write([]byte(tc.response))
			}))
			defer gateway.Close()
			s, mux := modelProbeTestService(t, tc.api, gateway.URL)
			_, before := launcherCall(t, mux, http.MethodGet, "/api/v1/model-config", "")
			out, _ := runModelProbeTest(t, s, `{"provider":"managed","model":"synthetic-model","effort":"off","max_tokens":17,"samples":[{"id":"first","label":"First sample","system":"system words","user":"user words"}]}`)
			if out.Provider != "managed" || out.API != tc.api || out.Model != "synthetic-model" || out.Effort != "off" ||
				out.Total != 1 || out.Succeeded != 1 || out.Failed != 0 || out.TotalMS < 0 || len(out.Results) != 1 {
				t.Fatalf("summary = %+v", out)
			}
			got := out.Results[0]
			if got.Index != 0 || got.ID != "first" || got.Label != "First sample" || !got.OK || got.MS < 0 || got.HTTPStatus != http.StatusOK ||
				got.Error != "" || got.Attempts != 1 || got.Effort != "off" || got.Text != tc.wantText || got.TextTruncated ||
				!slices.Equal(got.BlockTypes, tc.wantTypes) || got.Usage[tc.usageKey] != tc.usageWant {
				t.Fatalf("sample = %+v", got)
			}
			if strings.Contains(got.Text, "not returned") {
				t.Fatalf("reasoning content was returned as text: %q", got.Text)
			}
			if body["model"] != "synthetic-model" || body["max_tokens"] != float64(17) {
				t.Fatalf("request = %v", body)
			}
			if tc.api == "messages" {
				thinking, _ := body["thinking"].(map[string]any)
				if thinking["type"] != "disabled" {
					t.Fatalf("Messages effort=off request body = %v", body)
				}
				if _, streamed := body["stream"]; streamed {
					t.Fatalf("Messages sample unexpectedly streamed: %v", body)
				}
			} else {
				if body["reasoning_effort"] != "none" || body["stream"] != true {
					t.Fatalf("Chat effort=off request body = %v", body)
				}
			}
			encoded, _ := json.Marshal(body["messages"])
			if !strings.Contains(string(encoded), "user words") {
				t.Fatalf("user prompt not sent: %s", encoded)
			}
			if tc.api == "messages" && body["system"] != "system words" {
				t.Fatalf("Messages system prompt not sent: %v", body)
			}
			if tc.api == "chat" && !strings.Contains(string(encoded), "system words") {
				t.Fatalf("Chat system prompt not sent: %s", encoded)
			}
			_, after := launcherCall(t, mux, http.MethodGet, "/api/v1/model-config", "")
			if before != after {
				t.Fatal("model_probe changed model configuration")
			}
		})
	}
}

func TestModelProbeUnicodeLabelsMatchSchema(t *testing.T) {
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"type":"message","content":[{"type":"text","text":"ok"}]}`))
	}))
	defer gateway.Close()
	s, _ := modelProbeTestService(t, "messages", gateway.URL)
	id, label := strings.Repeat("界", 128), strings.Repeat("例", 128)
	args := fmt.Sprintf(`{"provider":"managed","samples":[{"id":%q,"label":%q,"user":"x"}]}`, id, label)
	out, _ := runModelProbeTest(t, s, args)
	if len(out.Results) != 1 || !out.Results[0].OK || out.Results[0].ID != id || out.Results[0].Label != label {
		t.Fatalf("128-code-point labels rejected: %+v", out)
	}
}

func TestModelProbeTerminalOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name          string
		api           string
		response      string
		ok            bool
		truncated     bool
		errorContains string
	}{
		{name: "Messages HTTP 200 error", api: "messages", response: `{"type":"error","message":"overloaded"}`, errorContains: "overloaded"},
		{name: "Messages null", api: "messages", response: `null`, errorContains: "missing content"},
		{name: "Messages missing content", api: "messages", response: `{}`, errorContains: "missing content"},
		{name: "Messages refusal", api: "messages", response: `{"type":"message","stop_reason":"refusal","content":[]}`, errorContains: "refusal"},
		{name: "Messages token limit", api: "messages", response: `{"type":"message","stop_reason":"max_tokens","content":[{"type":"text","text":"partial"}]}`, ok: true, truncated: true},
		{name: "Chat DONE without a reply", api: "chat", response: "data: [DONE]\n\n", errorContains: "without a reply"},
		{name: "Chat blank reply", api: "chat", response: "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"  \"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", errorContains: "without a reply"},
		{name: "Chat token limit", api: "chat", response: "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"},\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n", ok: true, truncated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tc.response))
			}))
			defer gateway.Close()
			s, _ := modelProbeTestService(t, tc.api, gateway.URL)
			out, _ := runModelProbeTest(t, s, `{"provider":"managed","samples":[{"user":"ping"}]}`)
			if len(out.Results) != 1 {
				t.Fatalf("terminal result = %+v", out)
			}
			got := out.Results[0]
			if got.OK != tc.ok || got.GenerationTruncated != tc.truncated || got.HTTPStatus != http.StatusOK {
				t.Fatalf("terminal sample = %+v", got)
			}
			if tc.ok && (out.Succeeded != 1 || out.Failed != 0 || got.Text != "partial") {
				t.Fatalf("truncated success = %+v", out)
			}
			if !tc.ok && (out.Succeeded != 0 || out.Failed != 1 || !strings.Contains(got.Error, tc.errorContains)) {
				t.Fatalf("terminal failure = %+v", out)
			}
		})
	}
}

func TestModelProbeRedactsProviderControlledMetadata(t *testing.T) {
	for _, tc := range []struct {
		name           string
		api            string
		status         int
		response       string
		retryAfter     string
		wantTypes      []string
		wantUsage      map[string]int
		wantRetryAfter string
	}{
		{
			name: "Messages block type and usage key", api: "messages", status: http.StatusOK,
			response:  `{"type":"message","content":[{"type":"` + fakeKey + `"},{"type":"text","text":"ok"}],"usage":{"input_tokens":3,"` + "hdr-secret-value" + `":4}}`,
			wantTypes: []string{"***", "text"}, wantUsage: map[string]int{"input_tokens": 3},
		},
		{
			name: "Chat usage key", api: "chat", status: http.StatusOK,
			response: "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n" +
				"data: {\"usage\":{\"prompt_tokens\":4,\"" + fakeKey + "\":2}}\n\ndata: [DONE]\n\n",
			wantTypes: []string{"text"}, wantUsage: map[string]int{"prompt_tokens": 4},
		},
		{
			name: "Retry-After", api: "messages", status: http.StatusTooManyRequests,
			response: `{"error":{"message":"slow down"}}`, retryAfter: fakeKey, wantRetryAfter: "***",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.retryAfter != "" {
					w.Header().Set("Retry-After", tc.retryAfter)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.response))
			}))
			defer gateway.Close()
			s, _ := modelProbeTestService(t, tc.api, gateway.URL)
			out, _ := runModelProbeTest(t, s, `{"provider":"managed","samples":[{"user":"ping"}]}`)
			if len(out.Results) != 1 {
				t.Fatalf("metadata result = %+v", out)
			}
			got := out.Results[0]
			if got.OK != (tc.status == http.StatusOK) || !slices.Equal(got.BlockTypes, tc.wantTypes) || got.RetryAfter != tc.wantRetryAfter {
				t.Fatalf("metadata sample = %+v", got)
			}
			if len(got.Usage) != len(tc.wantUsage) {
				t.Fatalf("usage = %v, want %v", got.Usage, tc.wantUsage)
			}
			for key, want := range tc.wantUsage {
				if got.Usage[key] != want {
					t.Fatalf("usage = %v, want %v", got.Usage, tc.wantUsage)
				}
			}
			if _, present := got.Usage["***"]; present {
				t.Fatalf("redacted usage keys collided instead of being omitted: %v", got.Usage)
			}
		})
	}
}

func TestModelProbeMessagesOffAlwaysDisablesThinking(t *testing.T) {
	var body map[string]any
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		_, _ = w.Write([]byte(`{"type":"message","content":[{"type":"text","text":"ok"}]}`))
	}))
	defer gateway.Close()
	s, _ := modelProbeTestService(t, "messages", gateway.URL)
	out, _ := runModelProbeTest(t, s, `{"provider":"managed","model":"sonnet-5-5","effort":"off","samples":[{"user":"ping"}]}`)
	if len(out.Results) != 1 || !out.Results[0].OK {
		t.Fatalf("effort=off result = %+v", out)
	}
	thinking, _ := body["thinking"].(map[string]any)
	if thinking["type"] != "disabled" {
		t.Fatalf("effort=off silently changed the wire request: %v", body)
	}
	if _, present := body["output_config"]; present {
		t.Fatalf("effort=off retained a degraded output effort: %v", body)
	}
}

func TestModelProbeInheritsProviderModelAndEffort(t *testing.T) {
	var body map[string]any
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"type":"message","content":[{"type":"text","text":"ok"}]}`))
	}))
	defer gateway.Close()
	s, _ := modelProbeTestService(t, "messages", gateway.URL)
	out, _ := runModelProbeTest(t, s, `{"provider":"managed","samples":[{"user":"ping"}]}`)
	if out.Model != "saved-model" || out.Effort != "high" || len(out.Results) != 1 || !out.Results[0].OK {
		t.Fatalf("inherited result = %+v", out)
	}
	effort, _ := body["output_config"].(map[string]any)
	if body["model"] != "saved-model" || effort["effort"] != "high" {
		t.Fatalf("inherited request = %v", body)
	}
}

func TestModelProbeSerialAndConcurrentOrder(t *testing.T) {
	for _, concurrency := range []int{1, 2} {
		t.Run(fmt.Sprintf("concurrency_%d", concurrency), func(t *testing.T) {
			entered := make(chan struct{}, 4)
			release := make(chan struct{}, 4)
			var active, maximum atomic.Int32
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Messages []struct {
						Content string `json:"content"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if len(body.Messages) == 0 {
					_, _ = w.Write([]byte(`{"type":"message","content":[]}`))
					return
				}
				now := active.Add(1)
				for old := maximum.Load(); now > old && !maximum.CompareAndSwap(old, now); old = maximum.Load() {
				}
				entered <- struct{}{}
				<-release
				active.Add(-1)
				_, _ = fmt.Fprintf(w, `{"type":"message","content":[{"type":"text","text":%q}]}`, body.Messages[len(body.Messages)-1].Content)
			}))
			defer gateway.Close()
			defer close(release) // release blocked handlers before Close waits for them, including after a timeout failure
			s, _ := modelProbeTestService(t, "messages", gateway.URL)
			type answer struct {
				out modelProbeTestOutput
				err error
			}
			done := make(chan answer, 1)
			go func() {
				raw, err := s.runTool(context.Background(), "server", "main", "model_probe", json.RawMessage(
					fmt.Sprintf(`{"provider":"managed","concurrency":%d,"samples":[{"id":"a","user":"one"},{"id":"b","user":"two"},{"id":"c","user":"three"},{"id":"d","user":"four"}]}`, concurrency)))
				if err != nil {
					done <- answer{err: err}
					return
				}
				var out modelProbeTestOutput
				err = json.Unmarshal([]byte(raw), &out)
				done <- answer{out: out, err: err}
			}()
			for remaining := 4; remaining > 0; remaining -= concurrency {
				batch := min(concurrency, remaining)
				for range batch {
					select {
					case <-entered:
					case <-time.After(5 * time.Second):
						t.Fatal("timed out waiting for a sample request")
					}
				}
				for range batch {
					release <- struct{}{}
				}
			}
			var got answer
			select {
			case got = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for model_probe")
			}
			if got.err != nil {
				t.Fatal(got.err)
			}
			if maximum.Load() > int32(concurrency) || got.out.Total != 4 || got.out.Succeeded != 4 {
				t.Fatalf("max in flight = %d, output = %+v", maximum.Load(), got.out)
			}
			for i, want := range []string{"a", "b", "c", "d"} {
				if got.out.Results[i].Index != i || got.out.Results[i].ID != want {
					t.Fatalf("results are not in input order: %+v", got.out.Results)
				}
			}
		})
	}
}

func TestModelProbeRateLimitAndFailures(t *testing.T) {
	for _, tc := range []struct {
		name       string
		api        string
		status     int
		response   string
		retryAfter string
		wantStatus int
	}{
		{name: "rate limit", api: "messages", status: http.StatusTooManyRequests, retryAfter: "17", response: `{"error":{"message":"slow down ` + fakeKey + ` ` + "hdr-secret-value" + `"}}`, wantStatus: http.StatusTooManyRequests},
		{name: "HTTP error", api: "messages", status: http.StatusInternalServerError, response: `{"error":{"message":"broken ` + fakeKey + `"}}`, wantStatus: http.StatusInternalServerError},
		{name: "Messages JSON", api: "messages", status: http.StatusOK, response: `{not-json`, wantStatus: http.StatusOK},
		{name: "Chat malformed stream", api: "chat", status: http.StatusOK, response: "data: {not-json\n\n", wantStatus: http.StatusOK},
		{name: "Chat error event", api: "chat", status: http.StatusOK, response: `data: {"error":{"message":"stream ` + fakeKey + `"}}` + "\n\n", wantStatus: http.StatusOK},
		{name: "Chat unfinished stream", api: "chat", status: http.StatusOK, response: `data: {"choices":[{"index":0,"delta":{"content":"partial"}}]}` + "\n\n", wantStatus: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if tc.retryAfter != "" {
					w.Header().Set("Retry-After", tc.retryAfter)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.response))
			}))
			defer gateway.Close()
			s, _ := modelProbeTestService(t, tc.api, gateway.URL)
			out, _ := runModelProbeTest(t, s, `{"provider":"managed","samples":[{"user":"ping"}]}`)
			if out.Total != 1 || out.Succeeded != 0 || out.Failed != 1 || len(out.Results) != 1 {
				t.Fatalf("failure summary = %+v", out)
			}
			got := out.Results[0]
			if got.OK || got.HTTPStatus != tc.wantStatus || got.Error == "" || got.RetryAfter != tc.retryAfter || got.Attempts != 1 {
				t.Fatalf("failure result = %+v", got)
			}
			if calls.Load() != 1 {
				t.Fatalf("provider calls = %d, want no retry", calls.Load())
			}
		})
	}
}

func TestModelProbeTimeout(t *testing.T) {
	release := make(chan struct{})
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer gateway.Close()
	defer close(release)
	s, _ := modelProbeTestService(t, "messages", gateway.URL)
	out, _ := runModelProbeTest(t, s, `{"provider":"managed","timeout_seconds":1,"samples":[{"user":"ping"}]}`)
	if len(out.Results) != 1 || out.Results[0].OK || !strings.Contains(strings.ToLower(out.Results[0].Error), "timed out") ||
		!strings.Contains(out.Results[0].Error, "1") {
		t.Fatalf("timeout result = %+v", out)
	}
}

func TestModelProbeValidatesEverythingBeforeSending(t *testing.T) {
	var calls atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"type":"message","content":[]}`))
	}))
	defer gateway.Close()
	s, _ := modelProbeTestService(t, "messages", gateway.URL)
	elevenSamples := strings.Repeat(`{"user":"x"},`, 10) + `{"user":"x"}`
	tests := []struct {
		name string
		args string
	}{
		{"unknown API key", `{"provider":"managed","api_key":"attacker","samples":[{"user":"x"}]}`},
		{"unknown headers", `{"provider":"managed","headers":"X-Evil: value","samples":[{"user":"x"}]}`},
		{"unknown base URL", `{"provider":"managed","base_url":"http://example.invalid","samples":[{"user":"x"}]}`},
		{"unknown route", `{"provider":"managed","via_machine":"other","samples":[{"user":"x"}]}`},
		{"trailing JSON", `{"provider":"managed","samples":[{"user":"x"}]} {}`},
		{"missing provider", `{"samples":[{"user":"x"}]}`},
		{"missing samples", `{"provider":"managed"}`},
		{"empty samples", `{"provider":"managed","samples":[]}`},
		{"too many samples", `{"provider":"managed","samples":[` + elevenSamples + `]}`},
		{"missing user", `{"provider":"managed","samples":[{"system":"x"}]}`},
		{"empty user", `{"provider":"managed","samples":[{"user":""}]}`},
		{"unsupported API", `{"provider":"managed","api":"responses","samples":[{"user":"x"}]}`},
		{"prompt too large", `{"provider":"managed","samples":[{"system":"` + strings.Repeat("s", 8192) + `","user":"` + strings.Repeat("u", 8193) + `"}]}`},
		{"id too large", `{"provider":"managed","samples":[{"id":"` + strings.Repeat("i", 129) + `","user":"x"}]}`},
		{"Unicode id too large", `{"provider":"managed","samples":[{"id":"` + strings.Repeat("界", 129) + `","user":"x"}]}`},
		{"label too large", `{"provider":"managed","samples":[{"label":"` + strings.Repeat("l", 129) + `","user":"x"}]}`},
		{"concurrency zero", `{"provider":"managed","concurrency":0,"samples":[{"user":"x"}]}`},
		{"concurrency high", `{"provider":"managed","concurrency":5,"samples":[{"user":"x"}]}`},
		{"tokens zero", `{"provider":"managed","max_tokens":0,"samples":[{"user":"x"}]}`},
		{"tokens high", `{"provider":"managed","max_tokens":1025,"samples":[{"user":"x"}]}`},
		{"timeout zero", `{"provider":"managed","timeout_seconds":0,"samples":[{"user":"x"}]}`},
		{"timeout high", `{"provider":"managed","timeout_seconds":31,"samples":[{"user":"x"}]}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if raw, err := s.runTool(context.Background(), "server", "main", "model_probe", json.RawMessage(tc.args)); err == nil {
				t.Fatalf("invalid input succeeded: %s", raw)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid calls reached provider %d times", calls.Load())
	}
}

func TestModelProbeChatCompatibilityRetriesAreReportedAndNotCached(t *testing.T) {
	var requests []map[string]any
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests = append(requests, body)
		switch len(requests) {
		case 1:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Unsupported parameter: 'reasoning_effort'"}}`))
		case 2:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"max_tokens is not supported; use max_completion_tokens"}}`))
		case 3:
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"error":{"message":"stream_options.include_usage is not supported"}}`))
		default:
			_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
		}
	}))
	defer gateway.Close()
	s, _ := modelProbeTestService(t, "chat", gateway.URL)
	out, _ := runModelProbeTest(t, s, `{"provider":"managed","effort":"off","max_tokens":19,"samples":[{"user":"ping"}]}`)
	if len(requests) != 4 || len(out.Results) != 1 || !out.Results[0].OK || out.Results[0].Attempts != 4 || out.Results[0].Effort != "" {
		t.Fatalf("retry result = %+v, requests = %v", out, requests)
	}
	if requests[0]["reasoning_effort"] != "none" {
		t.Fatalf("first request did not test effort=off: %v", requests[0])
	}
	if _, present := requests[1]["reasoning_effort"]; present {
		t.Fatalf("retry kept rejected effort: %v", requests[1])
	}
	if _, present := requests[2]["max_tokens"]; present || requests[2]["max_completion_tokens"] != float64(19) {
		t.Fatalf("token compatibility retry = %v", requests[2])
	}
	if _, present := requests[2]["stream_options"]; !present {
		t.Fatalf("stream_options was dropped before its rejection: %v", requests[2])
	}
	if _, present := requests[3]["stream_options"]; present {
		t.Fatalf("stream_options compatibility retry = %v", requests[3])
	}
	if ai.OmitChatEffort(gateway.URL, fakeKey, "saved-model", "", "off") {
		t.Fatal("read-only model_probe wrote Chat compatibility state")
	}
}

func TestModelProbeTextIsRedactedAndUTF8Truncated(t *testing.T) {
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"type":"message","content":[{"type":"text","text":%q}]}`, fakeKey+":"+strings.Repeat("界", 4000))
	}))
	defer gateway.Close()
	s, _ := modelProbeTestService(t, "messages", gateway.URL)
	out, _ := runModelProbeTest(t, s, `{"provider":"managed","samples":[{"user":"ping"}]}`)
	got := out.Results[0]
	if !got.OK || !got.TextTruncated || len(got.Text) > 8<<10 || !utf8.ValidString(got.Text) || !strings.HasPrefix(got.Text, "***:") {
		t.Fatalf("bounded text: bytes=%d valid=%v truncated=%v text=%q", len(got.Text), utf8.ValidString(got.Text), got.Text, got.Text)
	}
}
