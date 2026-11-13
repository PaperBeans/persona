// Package dialogwire is a terminal dialog as the node protocol and the client API carry it (docs/server/architecture.md
// "Contract packages"): internal/screen reads it off a screen, the node checks it before pressing keys, the apps show
// it as a card. Types, constants and trivial pure helpers only.
package dialogwire

import "time"

// Dialog is a choice a terminal agent waits on (AskUserQuestion without the hook, a permission prompt, a picker),
// read off its screen: header chip, question, options, and Rev, a hash of what it asks. The node checks Rev again
// right before pressing keys, so an answer never lands in a dialog that changed after the card was made.
type Dialog struct {
	Header   string `json:"header,omitempty"`
	Question string `json:"question"`
	Detail   string `json:"detail,omitempty"` // lines above the question that belong to it (the command asked about)
	// Warning is what the agent adds in a box above the question about why it asks (Claude Code's "Dangerous rm
	// operation on possibly-empty variable…"); Deadline its "⚠ … will automatically block this request in 1:59",
	// whose countdown moves every second. Neither is in Rev: a card stays answerable while the clock runs.
	Warning  string `json:"warning,omitempty"`
	Deadline string `json:"deadline,omitempty"`
	// A permission prompt for a shell command ("Bash command", a description, the command between dashed rules):
	// Command is the command, its wrapped rows joined back as well as the screen tells (line breaks it can tell from
	// wrapping stay); Description the agent's own words for it. Both are in Detail too, as before, and the command is
	// in Rev.
	Command     string `json:"command,omitempty"`
	Description string `json:"description,omitempty"`
	// Risk is what the Warning is about, for the apps to say in plain words: RiskDelete, or "" for a warning they show
	// as it is.
	Risk string `json:"risk,omitempty"`
	// DeadlineAction is what the countdown does when it runs out: DeadlineDeny or DeadlineAllow; DeadlineIn how long
	// it had left when the screen was read. The hub dates it: DeadlineAt, by its clock.
	DeadlineAction string    `json:"deadline_action,omitempty"`
	DeadlineIn     int       `json:"-"` // seconds
	DeadlineAt     time.Time `json:"deadline_at,omitzero"`
	Options        []Option  `json:"options"`
	// Multi: a multi-select question (AskUserQuestion's multiSelect, Claude Code 2.1.316): its options are check
	// boxes ("[ ]" / "[✔]") that enter or their number toggles, and the row under them sends what is checked —
	// Submit, which opens the review tab, or Next when another question follows.
	Multi  bool   `json:"multi,omitempty"`
	Submit string `json:"submit,omitempty"` // that row's label: "Submit" or "Next"
	// Steps are the questions of a multi-question AskUserQuestion, from its tab bar ("←  ☒ Env  ☐ Platform  ✔ Submit  →",
	// the Submit tab left out). A single question's one tab is its Header.
	Steps []Step `json:"steps,omitempty"`
	// Review is the Submit tab ("Review your answers", "1. Submit answers / 2. Cancel"); Incomplete when it warns
	// that a question has no answer yet.
	Review     bool   `json:"review,omitempty"`
	Incomplete bool   `json:"incomplete,omitempty"`
	Rev        string `json:"rev"`
	// LegacyRev is Rev as hubs and nodes from before #5538 compute it (white space and all): the card an older hub
	// makes carries it, and a hub asks an older node to check it.
	LegacyRev string `json:"-"`
	// Cursor is where the dialog's ❯ is: an option's key, CursorSubmit on the Submit / Next row, "" when unseen.
	Cursor string `json:"-"`
}

// Step is a question of a multi-question dialog: its header, and whether it has an answer.
type Step struct {
	Label string `json:"label"`
	Done  bool   `json:"done"`
}

// CursorSubmit is Dialog.Cursor on a multi-select question's Submit / Next row.
const CursorSubmit = "submit"

// Dialog.Risk: the warning says Claude cannot rule out that the command deletes files ("Dangerous rm operation …").
const RiskDelete = "delete"

// Dialog.DeadlineAction: the request is refused when the countdown runs out ("will automatically block", "deny"), or
// let through ("allow", "approve").
const (
	DeadlineDeny  = "deny"
	DeadlineAllow = "allow"
)

// Option kinds besides a plain choice.
const (
	KindTypeSomething = "type_something" // picks a free-text field: answer with text
	KindChat          = "chat"           // "Chat about this": back to the conversation
)

type Option struct {
	Key    string `json:"key"` // its number; for unnumbered dialogs (Claude's trust prompt) its position, 1-based
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
	Kind   string `json:"kind,omitempty"`
	// Keys pick it: its number, or arrow moves from the cursor plus enter for an unnumbered dialog. A type_something
	// option takes them before its text, and enter after. A multi-select option's number only toggles it.
	Keys []string `json:"keys"`
	// Checked: a multi-select option's box is ticked, or the ✔ after the option an answered question had picked.
	Checked bool `json:"checked,omitempty"`
	// Text is what is typed into a type_something option: the terminal shows it in place of the label.
	Text string `json:"text,omitempty"`
}

// Option by key, or nil.
func (d *Dialog) Option(key string) *Option {
	for i := range d.Options {
		if d.Options[i].Key == key {
			return &d.Options[i]
		}
	}
	return nil
}
