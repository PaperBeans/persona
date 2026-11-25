package connectorwire

import (
	"encoding/json"
	"time"
)

// The ledger of what was written as the user (docs/extensions/connectors.md "Sent as the user"), as the manager's
// store keeps it (internal/db, migrations/0420_sent_as_user.sql).

// SentAsUser is one write that went out through a connector's gate.
type SentAsUser struct {
	ID        int64     `json:"id"`
	At        time.Time `json:"at"`
	Connector string    `json:"connector"`
	Action    string    `json:"action"`
	// Target is where it went, by connector; Origin the conversation that asked; LetThrough how the gate let it
	// through (all JSON objects).
	Target     json.RawMessage `json:"target"`
	Text       string          `json:"text"`
	Summary    string          `json:"summary"`
	Origin     json.RawMessage `json:"origin"`
	LetThrough json.RawMessage `json:"let_through"`
	MessageRef string          `json:"message_ref,omitempty"`
	RecalledAt *time.Time      `json:"recalled_at,omitempty"`
}
