package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/i18n"
)

// model_probe is deliberately a narrow MCP facade over the manager's stored-provider probe transport. In particular,
// its input is not probeInput: callers cannot supply a draft URL, credentials, headers, proxy, or via-machine route.
const (
	modelProbeMaxSamples       = 10
	modelProbeMaxPromptBytes   = 16 << 10
	modelProbeMaxLabelRunes    = 128
	modelProbeMaxTokens        = 1024
	modelProbeMaxTimeout       = 30
	modelProbeMaxResponseBytes = 1 << 20
	modelProbeMaxTextBytes     = 8 << 10
	modelProbeMaxInputBytes    = 192 << 10
)

type modelProbeInput struct {
	Provider       string             `json:"provider"`
	API            string             `json:"api"`
	Model          string             `json:"model"`
	Effort         *string            `json:"effort"`
	Samples        []modelProbeSample `json:"samples"`
	Concurrency    *int               `json:"concurrency"`
	MaxTokens      *int               `json:"max_tokens"`
	TimeoutSeconds *int               `json:"timeout_seconds"`
}

type modelProbeSample struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	System string `json:"system"`
	User   string `json:"user"`
}

type modelProbeResult struct {
	Index               int            `json:"index"`
	ID                  string         `json:"id,omitempty"`
	Label               string         `json:"label,omitempty"`
	OK                  bool           `json:"ok"`
	MS                  int64          `json:"ms"`
	HTTPStatus          int            `json:"http_status,omitempty"`
	Error               string         `json:"error,omitempty"`
	RetryAfter          string         `json:"retry_after,omitempty"`
	BlockTypes          []string       `json:"block_types"`
	Text                string         `json:"text"`
	TextTruncated       bool           `json:"text_truncated,omitempty"`
	GenerationTruncated bool           `json:"generation_truncated,omitempty"`
	Usage               map[string]int `json:"usage,omitempty"`
	Attempts            int            `json:"attempts"`
	// Effort is what the final compatibility attempt actually sent, not merely what the caller requested. An empty
	// value after a rejected-field retry says the answer did not verify the requested effort.
	Effort string `json:"effort"`
}

type modelProbeOutput struct {
	Provider  string             `json:"provider"`
	API       string             `json:"api"`
	Model     string             `json:"model"`
	Effort    string             `json:"effort"`
	Results   []modelProbeResult `json:"results"`
	Total     int                `json:"total"`
	Succeeded int                `json:"succeeded"`
	Failed    int                `json:"failed"`
	TotalMS   int64              `json:"total_ms"`
}

func decodeModelProbeInput(args json.RawMessage) (modelProbeInput, error) {
	var in modelProbeInput
	if len(args) > modelProbeMaxInputBytes {
		return in, fmt.Errorf("model_probe: input exceeds %d bytes", modelProbeMaxInputBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return in, fmt.Errorf("model_probe: invalid input: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return in, errors.New("model_probe: exactly one JSON object is required")
	}
	in.Provider = strings.TrimSpace(in.Provider)
	in.API = strings.TrimSpace(in.API)
	in.Model = strings.TrimSpace(in.Model)
	if in.Provider == "" {
		return in, errors.New("model_probe: provider is required")
	}
	if in.API != "" && in.API != "messages" && in.API != "chat" {
		return in, errors.New("model_probe: api must be messages or chat")
	}
	if len(in.Samples) < 1 || len(in.Samples) > modelProbeMaxSamples {
		return in, fmt.Errorf("model_probe: samples must contain 1..%d entries", modelProbeMaxSamples)
	}
	for i, sample := range in.Samples {
		if strings.TrimSpace(sample.User) == "" {
			return in, fmt.Errorf("model_probe: samples[%d].user is required", i)
		}
		if len(sample.System)+len(sample.User) > modelProbeMaxPromptBytes {
			return in, fmt.Errorf("model_probe: samples[%d] system+user exceeds %d bytes", i, modelProbeMaxPromptBytes)
		}
		if utf8.RuneCountInString(sample.ID) > modelProbeMaxLabelRunes || utf8.RuneCountInString(sample.Label) > modelProbeMaxLabelRunes {
			return in, fmt.Errorf("model_probe: samples[%d] id and label must be at most %d Unicode code points", i, modelProbeMaxLabelRunes)
		}
	}
	return in, nil
}

func modelProbeInt(value *int, fallback, low, high int, name string) (int, error) {
	if value == nil {
		return fallback, nil
	}
	if *value < low || *value > high {
		return 0, fmt.Errorf("model_probe: %s must be in %d..%d", name, low, high)
	}
	return *value, nil
}

func applyModelProbeEffort(body map[string]any, api, model, effort string) bool {
	if !ai.ApplyReasoningEffort(body, api, model, effort) {
		return false
	}
	// This diagnostic must verify the requested off wire format, not the runtime's model-specific low-effort fallback.
	// In particular, an off result always means Messages sent thinking.type=disabled and Chat sent reasoning_effort=none.
	if api == "messages" && ai.ChatNoThinking(effort) {
		body["thinking"] = ai.DisabledThinking()
		delete(body, "output_config")
	}
	return true
}

func (s *Service) toolModelProbe(ctx context.Context, args json.RawMessage) (string, error) {
	started := time.Now()
	in, err := decodeModelProbeInput(args)
	if err != nil {
		return "", err
	}
	concurrency, err := modelProbeInt(in.Concurrency, 1, 1, 4, "concurrency")
	if err != nil {
		return "", err
	}
	maxTokens, err := modelProbeInt(in.MaxTokens, 256, 1, modelProbeMaxTokens, "max_tokens")
	if err != nil {
		return "", err
	}
	timeoutSeconds, err := modelProbeInt(in.TimeoutSeconds, modelProbeMaxTimeout, 1, modelProbeMaxTimeout, "timeout_seconds")
	if err != nil {
		return "", err
	}

	// Only these four fields cross the public/internal boundary. All address, credential, header and route values are
	// resolved from the registered provider by probeTarget.
	t, err := s.probeTarget(ctx, probeInput{Name: in.Provider, API: in.API, Model: in.Model, Effort: in.Effort})
	if err != nil {
		return "", fmt.Errorf("model_probe: %w", err)
	}
	if t.api == "" {
		t.api = "messages"
	}
	if t.api != "messages" && t.api != "chat" {
		return "", fmt.Errorf("model_probe: provider %q uses unsupported api %q", in.Provider, t.api)
	}
	if t.chatAlias != nil && t.api == "chat" {
		t.model, err = resolveAPITierAlias(*t.chatAlias, t.model)
		if err != nil {
			return "", fmt.Errorf("model_probe: %w", err)
		}
	}
	if t.api == "chat" {
		t.effort = chatEffortForModel(t.models, t.model, t.effort)
	}
	if strings.TrimSpace(t.model) == "" {
		return "", i18n.Err("assistant-models_launchers.err_model_required")
	}
	// Validate protocol/effort compatibility before starting any worker. Chat's model-specific values are validated by
	// its gateway, as in providerTest; Messages rejects an unknown product effort locally.
	if !applyModelProbeEffort(map[string]any{}, t.api, t.model, t.effort) {
		return "", errors.New("model_probe: unsupported effort for this protocol")
	}

	out := modelProbeOutput{
		Provider: in.Provider, API: t.api, Model: t.model, Effort: t.effort,
		Results: make([]modelProbeResult, len(in.Samples)), Total: len(in.Samples),
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(concurrency, len(in.Samples)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				// send can settle auto authentication on its target. Each sample therefore owns a copy; the resolved
				// transport itself is read-only and retains the registered via-machine route.
				target := t
				out.Results[i] = target.runModelProbeSample(ctx, in.Samples[i], i, maxTokens, time.Duration(timeoutSeconds)*time.Second)
			}
		}()
	}
	for i := range in.Samples {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	for _, result := range out.Results {
		if result.OK {
			out.Succeeded++
		} else {
			out.Failed++
		}
	}
	out.TotalMS = time.Since(started).Milliseconds()
	encoded, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("model_probe: encode result: %w", err)
	}
	return string(encoded), nil
}

func (t probeTarget) runModelProbeSample(ctx context.Context, sample modelProbeSample, index, maxTokens int, timeout time.Duration) (out modelProbeResult) {
	started := time.Now()
	out = modelProbeResult{Index: index, ID: sample.ID, Label: sample.Label, BlockTypes: []string{}, Effort: t.effort}
	defer func() { out.MS = time.Since(started).Milliseconds() }()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	turns := make([]any, 0, 2)
	if sample.System != "" && t.api == "chat" {
		turns = append(turns, map[string]any{"role": "system", "content": sample.System})
	}
	turns = append(turns, map[string]any{"role": "user", "content": sample.User})
	path := "messages"
	payload := map[string]any{"model": t.model, "max_tokens": maxTokens, "messages": turns}
	if sample.System != "" && t.api == "messages" {
		payload["system"] = sample.System
	}
	if t.api == "chat" {
		path = "chat/completions"
		payload["stream"] = true
		payload["stream_options"] = map[string]any{"include_usage": true}
	}
	if !applyModelProbeEffort(payload, t.api, t.model, t.effort) {
		out.Error = "unsupported effort for this protocol"
		return out
	}

	renamedTokens, droppedEffort, droppedStreamOptions := false, false, false
	var res *http.Response
	var err error
	for {
		body, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			out.Error = modelProbeError(t, ctx, marshalErr, timeout)
			return out
		}
		out.Attempts++
		res, err = t.send(ctx, http.MethodPost, t.endpoint(path), body)
		if err != nil {
			break
		}
		out.HTTPStatus = res.StatusCode
		out.RetryAfter = t.redact(res.Header.Get("Retry-After"))
		if t.api != "chat" || (res.StatusCode != http.StatusBadRequest && res.StatusCode != http.StatusUnprocessableEntity) {
			break
		}
		raw, readErr := io.ReadAll(io.LimitReader(res.Body, 4096))
		res.Body.Close()
		if ai.HasRecognizedErrorCode(raw) {
			res.Body = io.NopCloser(bytes.NewReader(raw))
			break
		}
		fieldRejected := ai.ChatRejectsReasoningEffort(raw)
		offValueRejected := !fieldRejected && ai.ChatNoThinking(t.effort) && ai.ChatRejectsNoEffortValue(raw)
		if readErr == nil && res.StatusCode == http.StatusBadRequest && !droppedEffort && payload["reasoning_effort"] != nil && (fieldRejected || offValueRejected) {
			// Unlike providerTest, a read-only diagnostic must not teach the process-wide compatibility cache.
			droppedEffort = true
			delete(payload, "reasoning_effort")
			t.effort = ""
			out.Effort = ""
			continue
		}
		if readErr == nil && res.StatusCode == http.StatusBadRequest && !renamedTokens && payload["max_tokens"] != nil && ai.ChatRequiresCompletionTokens(raw) {
			renamedTokens = true
			delete(payload, "max_tokens")
			payload["max_completion_tokens"] = maxTokens
			continue
		}
		if readErr == nil && !droppedStreamOptions && payload["stream_options"] != nil && ai.ChatRejectsStreamOptions(raw) {
			droppedStreamOptions = true
			delete(payload, "stream_options")
			continue
		}
		res.Body = io.NopCloser(bytes.NewReader(raw))
		break
	}
	if res != nil {
		defer res.Body.Close()
	}
	if err != nil {
		out.Error = modelProbeError(t, ctx, err, timeout)
		return out
	}
	if res.StatusCode/100 != 2 {
		out.Error, _ = t.failure(ctx, res, nil)
		return out
	}

	var text strings.Builder
	if t.api == "messages" {
		err = readModelProbeMessages(res.Body, &out, &text, t.redact)
	} else {
		err = readModelProbeChat(res.Body, &out, &text, t.redact)
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		out.Error = modelProbeError(t, ctx, err, timeout)
	} else {
		out.OK = true
	}
	out.Text, out.TextTruncated = modelProbeText(t.redact(text.String()))
	return out
}

func modelProbeError(t probeTarget, ctx context.Context, err error, timeout time.Duration) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Sprintf("provider request timed out after %d seconds", int(timeout/time.Second))
	}
	var uerr *url.Error
	if errors.As(err, &uerr) {
		err = uerr.Err
	}
	return clip(oneLine(t.redact("provider request failed: "+err.Error())), 300)
}

func readModelProbeResponse(body io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(body, modelProbeMaxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > modelProbeMaxResponseBytes {
		return nil, fmt.Errorf("provider response exceeds %d bytes", modelProbeMaxResponseBytes)
	}
	return raw, nil
}

func modelProbeUsage(raw json.RawMessage, redact func(string) string) map[string]int {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil
	}
	usage := make(map[string]int)
	for name, raw := range fields {
		var value int
		// Field names are provider-controlled too. Omit a sensitive entry rather than risk a collision between redacted keys.
		if redact(name) != name {
			continue
		}
		if json.Unmarshal(raw, &value) == nil {
			usage[name] = value
		}
	}
	if len(usage) == 0 {
		return nil
	}
	return usage
}

func modelProbeBlockType(out *modelProbeResult, typ string) {
	if typ == "" {
		return
	}
	for _, existing := range out.BlockTypes {
		if existing == typ {
			return
		}
	}
	out.BlockTypes = append(out.BlockTypes, typ)
}

func readModelProbeMessages(body io.Reader, out *modelProbeResult, text *strings.Builder, redact func(string) string) error {
	raw, err := readModelProbeResponse(body)
	if err != nil {
		return err
	}
	var response struct {
		Type       string `json:"type"`
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage json.RawMessage `json:"usage"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return fmt.Errorf("decode Messages response: %w", err)
	}
	if response.Type == "error" {
		return fmt.Errorf("Messages response error: %s", gatewayMessage(raw))
	}
	if rawError := bytes.TrimSpace(response.Error); len(rawError) > 0 && !bytes.Equal(rawError, []byte("null")) {
		return fmt.Errorf("Messages response error: %s", gatewayMessage(raw))
	}
	if response.StopReason == "refusal" {
		return errors.New("Messages response stopped at refusal")
	}
	if response.Content == nil {
		return errors.New("Messages response missing content")
	}
	out.GenerationTruncated = response.StopReason == "max_tokens"
	for _, block := range response.Content {
		modelProbeBlockType(out, redact(block.Type))
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	out.Usage = modelProbeUsage(response.Usage, redact)
	return nil
}

func readModelProbeChat(body io.Reader, out *modelProbeResult, text *strings.Builder, redact func(string) string) error {
	limited := &io.LimitedReader{R: body, N: modelProbeMaxResponseBytes + 1}
	finished, done := false, false
	var eventErr error
	readErr := ai.SSEEvents(limited, func(data string) bool {
		if strings.TrimSpace(data) == "[DONE]" {
			done = true
			return true
		}
		var event struct {
			Type    json.RawMessage `json:"type"`
			Error   json.RawMessage `json:"error"`
			Usage   json.RawMessage `json:"usage"`
			Choices []struct {
				Index        int    `json:"index"`
				FinishReason string `json:"finish_reason"`
				Delta        struct {
					Content          *string `json:"content"`
					ReasoningContent *string `json:"reasoning_content"`
					Refusal          string  `json:"refusal"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			eventErr = fmt.Errorf("decode Chat stream: %w", err)
			return true
		}
		rawError := bytes.TrimSpace(event.Error)
		if len(rawError) > 0 && !bytes.Equal(rawError, []byte("null")) || string(event.Type) == `"error"` {
			eventErr = fmt.Errorf("Chat stream error: %s", gatewayMessage([]byte(data)))
			return true
		}
		if usage := modelProbeUsage(event.Usage, redact); usage != nil {
			out.Usage = usage
		}
		for _, choice := range event.Choices {
			if choice.Index != 0 {
				continue
			}
			if choice.Delta.ReasoningContent != nil {
				modelProbeBlockType(out, "reasoning_content")
			}
			if choice.Delta.Content != nil {
				modelProbeBlockType(out, "text")
				text.WriteString(*choice.Delta.Content)
			}
			if choice.Delta.Refusal != "" {
				eventErr = fmt.Errorf("Chat stream refusal: %s", choice.Delta.Refusal)
				return true
			}
			if choice.FinishReason == "content_filter" {
				eventErr = errors.New("Chat stream stopped at content_filter")
				return true
			}
			if choice.FinishReason != "" {
				finished = true
				out.GenerationTruncated = choice.FinishReason == "length"
			}
		}
		return false
	})
	if eventErr != nil {
		return eventErr
	}
	if readErr != nil {
		return fmt.Errorf("read Chat stream: %w", readErr)
	}
	if limited.N <= 0 && !done {
		return fmt.Errorf("provider response exceeds %d bytes", modelProbeMaxResponseBytes)
	}
	if !done && !finished {
		return fmt.Errorf("Chat stream ended without a completed reply: %w", io.ErrUnexpectedEOF)
	}
	if strings.TrimSpace(text.String()) == "" && !out.GenerationTruncated {
		return errors.New("Chat stream ended without a reply")
	}
	return nil
}

func modelProbeText(text string) (string, bool) {
	if len(text) <= modelProbeMaxTextBytes || !utf8.ValidString(text) {
		if len(text) <= modelProbeMaxTextBytes {
			return text, false
		}
		// Provider JSON is valid UTF-8 after encoding/json replacement, but retain a safe bound even for a custom reader.
		text = strings.ToValidUTF8(text, "�")
	}
	end := modelProbeMaxTextBytes
	for end > 0 && end < len(text) && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end], true
}
