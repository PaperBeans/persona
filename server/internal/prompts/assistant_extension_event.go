package prompts

import "encoding/json"

// ExtensionEvent is something an extension observed and reported to the main conversation (assistant.event, docs/extensions/extensions.md
// "Assistant events"). Every field but ID and Severity is the extension's word and untrusted: it goes into the prompt as
// one JSON object, whose escaping keeps any text in it, a closing tag included, from standing as part of the prompt.
type ExtensionEvent struct {
	ID       string // the extension's id, as the manager checked it
	Severity string // info | warning | critical
	Name     string // the extension's name in its manifest
	Key      string
	Title    string
	Body     string
	// ActionHint is what the extension suggests be done: a suggestion, not an order.
	ActionHint string
	State      string
}

// RenderExtensionEvent is the message the main conversation gets for e (assistant/extension_event).
func RenderExtensionEvent(e ExtensionEvent) string {
	data, err := json.Marshal(struct {
		Extension  string `json:"extension"`
		Key        string `json:"key"`
		Title      string `json:"title"`
		Body       string `json:"body,omitempty"`
		ActionHint string `json:"action_hint,omitempty"`
		State      string `json:"state,omitempty"`
	}{e.Name, e.Key, e.Title, e.Body, e.ActionHint, e.State})
	if err != nil {
		panic(err) // only strings
	}
	return render("assistant/extension_event", struct{ ID, Severity, Data string }{e.ID, e.Severity, string(data)})
}
