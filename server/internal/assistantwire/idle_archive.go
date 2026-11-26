package assistantwire

import (
	"encoding/json"
	"time"

	"github.com/nwverse/persona/server/internal/wireids"
)

// CapabilityAgentSessionIdleArchive advertises the manager's guarded idle-session archive-and-close API and its audit
// read endpoint.
const CapabilityAgentSessionIdleArchive = "agent_session_idle_archive"

const (
	RouteAgentIdleArchive      = "POST /api/v1/sessions/{mid}/{sid}/archive-idle"
	RouteAgentIdleArchiveAudit = "GET /api/v1/sessions/archive-idle/audit"
)

// IdleArchiveCloseMode selects whether a guarded idle archive closes the session's terminal.
// The zero value defaults to closing the terminal (omitted or true in JSON); false keeps the terminal open.
type IdleArchiveCloseMode uint8

const (
	IdleArchiveCloseDefault IdleArchiveCloseMode = iota
	IdleArchiveCloseTerminal
	IdleArchiveCloseKeep
)

// ShouldClose reports whether the request asks to close the session's terminal.
func (m IdleArchiveCloseMode) ShouldClose() bool {
	return m != IdleArchiveCloseKeep
}

// UnmarshalJSON decodes an optional JSON boolean (null or omitted -> default close).
func (m *IdleArchiveCloseMode) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*m = IdleArchiveCloseDefault
		return nil
	}
	var closeTerminal bool
	if err := json.Unmarshal(data, &closeTerminal); err != nil {
		return err
	}
	if closeTerminal {
		*m = IdleArchiveCloseTerminal
	} else {
		*m = IdleArchiveCloseKeep
	}
	return nil
}

// MarshalJSON encodes the mode as its JSON boolean value.
func (m IdleArchiveCloseMode) MarshalJSON() ([]byte, error) {
	if m == IdleArchiveCloseKeep {
		return []byte("false"), nil
	}
	return []byte("true"), nil
}

// IdleArchiveRequest asks the manager to archive an idle coding-agent session (and optionally close its terminal) only
// after strictly verifying it has been idle for more than IdleMinutes and is not protected or busy.
type IdleArchiveRequest struct {
	IdleMinutes   int                  `json:"idle_minutes"`
	CloseTerminal IdleArchiveCloseMode `json:"close_terminal,omitzero"`
	Reason        string               `json:"reason,omitempty"`
}

// IdleArchiveCloseResult describes what happened to the session's running agent/terminal when CloseTerminal was requested.
type IdleArchiveCloseResult struct {
	Closed  bool   `json:"closed"`
	Outcome string `json:"outcome"`
	Reason  string `json:"reason,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// IdleArchiveAuditID identifies one idle-archive audit record (contract-bare-id).
type IdleArchiveAuditID string

// IdleArchiveResponse is returned on HTTP 200 after a guarded idle archive succeeds (or is already archived offline),
// or on HTTP 503 when an irreversible terminal close succeeded against a durably persisted attempt record while final
// audit outcome persistence is pending durable retry.
type IdleArchiveResponse struct {
	OK                       bool                    `json:"ok"`
	Code                     string                  `json:"code,omitempty"`
	Message                  string                  `json:"error,omitempty"`
	Archived                 bool                    `json:"archived"`
	AlreadyArchived          bool                    `json:"already_archived,omitempty"`
	ObservedIdleSeconds      int64                   `json:"observed_idle_seconds"`
	Close                    *IdleArchiveCloseResult `json:"close,omitempty"`
	AuditID                  IdleArchiveAuditID      `json:"audit_id,omitempty"`
	AuditPersisted           bool                    `json:"audit_persisted"`
	AuditFinalizationPending bool                    `json:"audit_finalization_pending,omitempty"`
}

// IdleArchiveRefusalResponse is returned on HTTP 409 / 502 / 503 when a guarded idle archive-and-close request is refused.
type IdleArchiveRefusalResponse struct {
	Message        string             `json:"error"`
	Code           string             `json:"code"`
	AuditPersisted bool               `json:"audit_persisted"`
	AuditID        IdleArchiveAuditID `json:"audit_id,omitempty"`
	CloseOutcome   string             `json:"close_outcome,omitempty"`
}

// IdleArchiveAuditEntry is one persisted audit record of a guarded idle archive-and-close request (succeeded or refused).
// It keeps only routing, timing, actor, and outcome metadata—never prompts, transcripts, or credentials.
type IdleArchiveAuditEntry struct {
	ID                  IdleArchiveAuditID `json:"id"`
	At                  time.Time          `json:"at"`
	Actor               string             `json:"actor"`
	Extension           string             `json:"extension,omitempty"`
	MachineID           wireids.MachineID  `json:"machine_id"`
	SessionID           wireids.SessionID  `json:"session_id"`
	IdleMinutes         int                `json:"idle_minutes"`
	ObservedIdleSeconds *int64             `json:"observed_idle_seconds,omitempty"`
	CloseTerminal       bool               `json:"close_terminal"`
	Outcome             string             `json:"outcome"`
	RefusalCode         string             `json:"refusal_code,omitempty"`
	CloseOutcome        string             `json:"close_outcome,omitempty"`
	Reason              string             `json:"reason,omitempty"`
}

// IdleArchiveAuditResponse is returned by GET /api/v1/sessions/archive-idle/audit.
type IdleArchiveAuditResponse struct {
	Entries []IdleArchiveAuditEntry `json:"entries"`
}

func init() {
	Routes = append(Routes,
		RouteAgentIdleArchive,
		RouteAgentIdleArchiveAudit,
	)
}
