package aiwire

import "errors"

// What a model's reply can fail at when a JSON answer was asked for. The client (internal/ai) returns these values
// under the same names; they are declared here so that a feature module can tell them apart without importing the
// client.
var (
	// ErrNoObject: the reply holds no JSON object at all.
	ErrNoObject = errors.New("ai: no JSON object in reply")
	// ErrUnterminated: the reply's JSON object never closes (and the reply was not cut off at max_tokens).
	ErrUnterminated = errors.New("ai: unterminated JSON object in reply")
	// ErrTruncated: the reply stopped at max_tokens before the answer was complete, even with the room doubled.
	ErrTruncated = errors.New("ai: reply cut off at max_tokens")
	// ErrRefused: the model declined the request (the Messages API's stop_reason "refusal").
	ErrRefused = errors.New("ai: the model declined to answer")
)
