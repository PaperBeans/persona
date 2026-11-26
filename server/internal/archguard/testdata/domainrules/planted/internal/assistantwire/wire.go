// Package assistantwire is the planted contract: plain wire data, which only adapters convert to and from the cores.
//
//persona:layer contract
package assistantwire

import "time"

// MachineID names a computer, SessionID an agent session on it: named, so one cannot stand for the other
// (contract-bare-id).
type (
	MachineID     string
	SessionID     string
	InteractionID string
)

// SessionRef names one session as the wire carries it.
type SessionRef struct {
	MachineID MachineID `json:"machine_id"`
	SessionID SessionID `json:"session_id"`
}

// Session is an agent's session as the apps get it.
type Session struct {
	MachineID  MachineID `json:"machine_id"`
	SessionID  SessionID `json:"session_id"`
	State      string    `json:"state"`
	Assistant  bool      `json:"assistant,omitempty"`
	Subtask    bool      `json:"subtask,omitempty"`
	Muted      bool      `json:"muted,omitempty"`
	StateSince time.Time `json:"state_since,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Interaction is a pending card; a card on no session has no session id.
type Interaction struct {
	ID        InteractionID `json:"id"`
	MachineID MachineID     `json:"machine_id,omitempty"`
	SessionID SessionID     `json:"session_id,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
}

// NeedsYouRow is one row of the Needs you list.
type NeedsYouRow struct {
	Machine string    `json:"machine,omitempty"`
	Session string    `json:"session,omitempty"`
	Card    string    `json:"card,omitempty"`
	Why     string    `json:"why"`
	Since   time.Time `json:"since,omitzero"`
}

// NeedsYou is the Needs you list and its count.
type NeedsYou struct {
	Rows  []NeedsYouRow `json:"rows"`
	Total int           `json:"total"`
}
