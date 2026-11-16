package ai

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/nwverse/persona/server/internal/lazyre"
)

// FailureKind is a direct-call failure whose remedy differs from an ordinary rejected request.
type FailureKind uint8

const (
	FailureOther FailureKind = iota
	// FailureModelUnavailable means the selected model does not exist or is not available to this key.
	FailureModelUnavailable
	// FailureContextTooLong means the input exceeds the model's context, not its output-token budget.
	FailureContextTooLong
)

// FailureKindOf reports a typed upstream failure. Pauses, refusals and unclassified errors are FailureOther.
func FailureKindOf(err error) FailureKind {
	var classified interface{ failureKind() FailureKind }
	if errors.As(err, &classified) {
		return classified.failureKind()
	}
	return FailureOther
}

// IsModelUnavailable tells a missing or key-inaccessible model from a missing endpoint or an invalid key.
func IsModelUnavailable(err error) bool { return FailureKindOf(err) == FailureModelUnavailable }

// IsContextTooLong tells an over-context input from an ordinary invalid request or a truncated reply.
func IsContextTooLong(err error) bool { return FailureKindOf(err) == FailureContextTooLong }

// FailureKindFor classifies a gateway's HTTP error body for callers such as connection probes. An explicit,
// recognized error.code wins over wording. An unknown code retains the wording-based behavior.
func FailureKindFor(status int, raw []byte) FailureKind {
	code, message := gatewayErrorDetails(raw)
	return failureKindFor(status, code, message)
}

// kindedError adds a classification without changing an existing error's text or unwrap chain.
type kindedError struct {
	error
	kind FailureKind
}

func (e *kindedError) Unwrap() error            { return e.error }
func (e *kindedError) failureKind() FailureKind { return e.kind }

func withFailureKind(err error, kind FailureKind) error {
	if err == nil || kind == FailureOther {
		return err
	}
	return &kindedError{error: err, kind: kind}
}

// streamFailureError keeps a stream error's text and limit behavior while adding typed model/context semantics.
// The stream itself has no error status, so wording fallback uses bad-request semantics.
func streamFailureError(event, code, msg string) error {
	if limit := streamLimitFor(event, code, msg); limit != nil {
		return limit
	}
	return withFailureKind(fmt.Errorf("ai: %s: %s", event, msg), failureKindFor(http.StatusBadRequest, code, msg))
}

var (
	modelFailureWords   = lazyre.New(`(?i)model[^\n]{0,80}(not.{0,4}(found|exist)|does not exist|invalid|unknown|unsupported|unavailable)|model_not_found|invalid model|unknown model|knows no model`)
	contextFailureWords = lazyre.New(`(?i)prompt is too long|maximum (?:context|prompt) length|context.{0,20}(length|window).{0,40}(exceed|too)|(input|request|message).{0,20}exceeds?.{0,40}context.{0,20}(length|window)|context_length_exceeded`)
)

// failureKindForCode reads recognized non-limit codes. recognized is false for an unknown code, whose established
// type or wording behavior must continue. An unsupported endpoint is recognized but deliberately has FailureOther.
func failureKindForCode(code string) (kind FailureKind, recognized bool) {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "model_not_found":
		return FailureModelUnavailable, true
	case "context_length_exceeded":
		return FailureContextTooLong, true
	case "unsupported_endpoint":
		return FailureOther, true
	default:
		return FailureOther, false
	}
}

func failureKindFor(status int, code, message string) FailureKind {
	// Codes are machine semantics; a type such as not_found_error is deliberately not accepted here.
	if kind, recognized := failureKindForCode(code); recognized {
		return kind
	}
	if status != http.StatusBadRequest && status != http.StatusNotFound {
		return FailureOther
	}
	if modelFailureWords.MatchString(message) {
		return FailureModelUnavailable
	}
	if contextFailureWords.MatchString(message) {
		return FailureContextTooLong
	}
	return FailureOther
}

type errorEnvelope struct {
	Type    string          `json:"type"`
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Error   json.RawMessage `json:"error"`
}

// gatewayErrorDetails reads OpenAI- and Anthropic-shaped envelopes. error.code is more specific than a top-level
// code; either may carry the message. Non-JSON bodies remain available to the established wording fallback.
func gatewayErrorDetails(raw []byte) (code, message string) {
	code, message, _, _ = gatewayErrorDetailsWithType(raw)
	return code, message
}

// gatewayErrorDetailsWithType also retains the most specific envelope type and whether raw was a JSON envelope.
// The latter keeps plain-text log fallback distinct from an envelope that happens to carry only a message.
func gatewayErrorDetailsWithType(raw []byte) (code, message, typ string, envelope bool) {
	var body errorEnvelope
	if json.Unmarshal(raw, &body) != nil {
		return "", string(raw), "", false
	}
	code, message, typ, envelope = strings.TrimSpace(body.Code), body.Message, body.Type, true
	if typ == "error" { // the outer Messages envelope marker, not the nested error's type
		typ = ""
	}
	var errorText string
	if json.Unmarshal(body.Error, &errorText) == nil && errorText != "" {
		message = errorText
	}
	// Some envelopes put the actual error one level deeper, under error.error. Keep the same bounded three levels.
	current := body.Error
	for depth := 0; depth < 2 && len(current) > 0; depth++ {
		var nested errorEnvelope
		if json.Unmarshal(current, &nested) != nil {
			break
		}
		if nested.Type != "" {
			typ = nested.Type
		}
		if c := strings.TrimSpace(nested.Code); c != "" {
			code = c
		}
		if nested.Message != "" {
			message = nested.Message
		}
		current = nested.Error
	}
	return code, message, typ, envelope
}
