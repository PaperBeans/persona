package assistantwire

import (
	"encoding/json"
	"fmt"
)

// Events an extension tells the assistant's main conversation (docs/extensions/extensions.md "Assistant events"). The
// extension's program calls MethodAssistantEvent on its process connection; the manager checks its assistant.events
// grant and its rate, and forwards it to the assistant as MethodExtensionEvent with the extension's identity, which
// the program cannot set (the call is not one an extension can make itself). The params are wire.AssistantEventParams.
const (
	// MethodAssistantEvent is what an extension's program calls.
	MethodAssistantEvent = "assistant.event"
	// MethodExtensionEvent is manager → assistant: ExtensionEvent → wire.AssistantEventResult.
	MethodExtensionEvent = "assistant.extension_event"
)

// ExtensionEvent is MethodExtensionEvent's params: the event as the extension sent it, and who sent it.
type ExtensionEvent struct {
	Extension string               `json:"extension"` // the extension's id: the manager's own word
	Name      string               `json:"name"`      // its name in its manifest, for the people and the assistant to read
	Params    UncheckedEventParams `json:"params"`    // wire.AssistantEventParams, not yet checked
}

// UncheckedEventParams is an extension's assistant.event params exactly as the program sent them. It stays open JSON
// on purpose (the typed core plan's 2.5, x-opaque): the manager forwards the bytes without decoding them, so the
// assistant applies the schema's limits and its strict decoding (unknown fields refused) once, where the event is
// used, and the manager never acts on decoded untrusted input. The assistant decodes it into AssistantEventParams.
type UncheckedEventParams json.RawMessage

// MarshalJSON writes the params as they came (null when empty).
func (p UncheckedEventParams) MarshalJSON() ([]byte, error) { return json.RawMessage(p).MarshalJSON() }

// UnmarshalJSON keeps a copy of the params as they are.
func (p *UncheckedEventParams) UnmarshalJSON(data []byte) error {
	return (*json.RawMessage)(p).UnmarshalJSON(data)
}

// What assistant.event answers besides the codes every assistant call has (CodeAssistantOffline, CodeMachineOffline).
const (
	// CodeNoMainConversation: the user has no main conversation. Nothing was started, replaced or queued.
	CodeNoMainConversation = "no_main_conversation"
	// CodeRateLimited: too many events from the extension; data {retry_after} is the seconds to wait.
	CodeRateLimited = "rate_limited"
	// CodeQueueFull: the extension already has the most events waiting behind the main conversation's running turn.
	CodeQueueFull = "queue_full"
)

// AssistantEventParams is assistant.event's params (schema/extension/assistant.schema.json, which the conformance test
// holds this to). Lengths are in characters: key 128, title 200, body 4000, action_hint 500, state 64.
type AssistantEventParams struct {
	Key        string              `json:"key"`
	Severity   string              `json:"severity,omitempty"` // info (the default) | warning | critical
	Title      string              `json:"title"`
	Body       string              `json:"body,omitempty"`
	ActionHint string              `json:"action_hint,omitempty"`
	State      AssistantEventState `json:"state,omitempty"`
	// Replace false keeps an earlier event with the same key as a message of its own; absent is true.
	Replace EventReplace `json:"replace,omitzero"`
}

// AssistantEventState is where the thing an event's key names is now, in the extension's own words ("firing",
// "resolved", ...). It is a label, not a closed set: each extension chooses its words, and the assistant only shows
// them. The schema bounds it to 64 characters.
type AssistantEventState string

// EventReplace is assistant.event's replace: whether the event takes the place of one of the extension's with the same
// key still waiting behind a running turn. On the wire it is an optional boolean whose absence means true, so it has
// three values, and absent stays absent when the params are written back.
type EventReplace uint8

const (
	// EventReplaceDefault: replace was left out, which replaces.
	EventReplaceDefault EventReplace = iota
	// EventReplaceYes: replace was true.
	EventReplaceYes
	// EventReplaceKeep: replace was false; the earlier event stays a message of its own.
	EventReplaceKeep
)

// Replaces reports whether the event replaces a queued one with its key: every value but EventReplaceKeep.
func (r EventReplace) Replaces() bool { return r != EventReplaceKeep }

// IsZero reports EventReplaceDefault, which omitzero leaves out of the JSON.
func (r EventReplace) IsZero() bool { return r == EventReplaceDefault }

// MarshalJSON writes true or false; EventReplaceDefault is null (a field tagged omitzero leaves it out instead).
func (r EventReplace) MarshalJSON() ([]byte, error) {
	switch r {
	case EventReplaceDefault:
		return []byte("null"), nil
	case EventReplaceYes:
		return []byte("true"), nil
	case EventReplaceKeep:
		return []byte("false"), nil
	}
	return nil, fmt.Errorf("replace: no such value %d", r)
}

// UnmarshalJSON reads true or false; null is absent, as it was for the *bool this replaced. Anything else fails.
func (r *EventReplace) UnmarshalJSON(data []byte) error {
	switch string(data) {
	case "true":
		*r = EventReplaceYes
	case "false":
		*r = EventReplaceKeep
	case "null":
		*r = EventReplaceDefault
	default:
		return fmt.Errorf("replace must be true or false, not %s", data)
	}
	return nil
}

// AssistantEventResult is assistant.event's result.
type AssistantEventResult struct {
	Delivery string `json:"delivery"` // EventDelivered | EventQueued | EventReplaced
}

// The severities of an event, and what became of it.
const (
	EventSeverityInfo     = "info"
	EventSeverityWarning  = "warning"
	EventSeverityCritical = "critical"

	EventDelivered = "delivered" // went into the idle main conversation now
	EventQueued    = "queued"    // waits behind the main conversation's running turn
	EventReplaced  = "replaced"  // took the place of a queued event with the same extension and key
)
