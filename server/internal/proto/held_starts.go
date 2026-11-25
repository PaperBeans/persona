package proto

import "time"

// MethodStartStatus asks a node whether a new-Agent start carrying op_id already started, is still running, or is
// unknown (#10674).
const MethodStartStatus = "start.status"

// StartStatusState is what MethodStartStatus answers for an op_id.
type StartStatusState string

const (
	// StartStatusStarted means the node created a session for this op_id (SessionID is set).
	StartStatusStarted StartStatusState = "started"
	// StartStatusPending means a start for this op_id is still running on the node right now.
	StartStatusPending StartStatusState = "pending"
	// StartStatusUnknown means the node has no record of this op_id.
	StartStatusUnknown StartStatusState = "unknown"
)

// StartStatusParams are MethodStartStatus's params.
type StartStatusParams struct {
	OpID string `json:"op_id"`
}

// StartStatus is MethodStartStatus's answer.
type StartStatus struct {
	State     StartStatusState `json:"state"`
	SessionID string           `json:"session_id,omitempty"`
}

// HeldStartEventType is the WebSocket event type pushed to full clients declaring FeatureAgentStartHold when a held
// Agent start is created, changes state, starts, or is deleted.
const HeldStartEventType = "held_start"

// Wire states for HeldStart.
const (
	HeldStartWaiting  = "waiting"  // waiting for the computer to come back (or queued to start)
	HeldStartStarting = "starting" // being checked or started on the reconnected computer
	HeldStartFailed   = "failed"   // refused, expired, unclear, unconfirmed, or signed_out: owner Retry or Delete
	HeldStartStarted  = "started"  // transition event only: start succeeded or reconciled; SessionID names the Agent
	HeldStartDeleted  = "deleted"  // transition event only: owner cancelled/deleted the held start
)

// HeldStart is one new-Agent start held while its computer is away (#10674). Clients draw a placeholder Agent row and
// held first-message bubble from these fields, and replace it with the real SessionID on the "started" event.
type HeldStart struct {
	OpID        string    `json:"op_id"`
	MachineID   string    `json:"machine_id"`
	Computer    string    `json:"computer,omitempty"`
	Cwd         string    `json:"cwd,omitempty"`
	Launcher    string    `json:"launcher,omitempty"`
	AgentType   string    `json:"agent_type,omitempty"`
	Model       string    `json:"model,omitempty"`
	Host        string    `json:"host,omitempty"`
	TabName     string    `json:"tab_name,omitempty"`
	TodoID      string    `json:"todo_id,omitempty"`
	WorkSession bool      `json:"work_session,omitempty"`
	Prompt      string    `json:"prompt,omitempty"`
	Images      int       `json:"images,omitempty"`
	State       string    `json:"state"`
	Failure     string    `json:"failure,omitempty"`
	Error       string    `json:"error,omitempty"`
	DeleteOnly  bool      `json:"delete_only,omitempty"`
	SessionID   string    `json:"session_id,omitempty"`
	SentAt      time.Time `json:"sent_at"`
	HeldAt      time.Time `json:"held_at"`
	CanRetry    bool      `json:"can_retry,omitempty"`
	CanCancel   bool      `json:"can_cancel,omitempty"`
}

// HeldStartList is GET /api/v1/machines/{mid}/held-starts's answer.
type HeldStartList struct {
	MachineID string      `json:"machine_id"`
	Computer  string      `json:"computer,omitempty"`
	Starts    []HeldStart `json:"starts"`
}
