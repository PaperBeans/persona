package assistantwire

import (
	"time"

	"github.com/nwverse/persona/server/internal/wireids"
)

const (
	// EventPlacementPending is a one-way wake event sent from the assistant to the extension layer when a task enters
	// the pending_placement queue. Extensions receive event.placement.pending only with the tasks.schedule scope.
	EventPlacementPending = "placement.pending"
)

func init() {
	Routes = append(Routes,
		"GET /api/v1/assistant/machine-resources",
		"GET /api/v1/assistant/placement/pending",
		"POST /api/v1/assistant/placement/{todo_id}/assign",
		"POST /api/v1/assistant/placement/{todo_id}/defer",
	)
}

// Placement-specific scheduling identifiers. Shared IDs use wireids directly.
type (
	PlanID  string
	BatchID string
)

// PlacementTaskRole is the closed role enum exposed on PlacementPendingTask.
type PlacementTaskRole string

const (
	PlacementTaskRoleWorker PlacementTaskRole = "worker"
	PlacementTaskRoleLeader PlacementTaskRole = "leader"
)

func (r PlacementTaskRole) Valid() bool {
	return r == PlacementTaskRoleWorker || r == PlacementTaskRoleLeader
}

// MachineResourceState is the closed reachability state enum exposed on MachineResourceSummary.
type MachineResourceState string

const (
	MachineResourceStateOnline       MachineResourceState = "online"
	MachineResourceStatePending      MachineResourceState = "pending"
	MachineResourceStateOffline      MachineResourceState = "offline"
	MachineResourceStateUnresponsive MachineResourceState = "unresponsive"
)

func (s MachineResourceState) Valid() bool {
	return s == MachineResourceStateOnline || s == MachineResourceStatePending || s == MachineResourceStateOffline || s == MachineResourceStateUnresponsive
}

// PlacementPendingEvent carries only opaque scheduling identifiers, never task prompts or private space data.
type PlacementPendingEvent struct {
	PlanID    PlanID         `json:"plan_id"`
	TodoID    wireids.TodoID `json:"todo_id"`
	BatchSize int            `json:"batch_size"`
}

// PlacementConstraints describes optional automatic machine selection constraints on a planned task.
type PlacementConstraints struct {
	OS                string   `json:"os,omitempty"`
	Capabilities      []string `json:"capabilities,omitempty"`
	RequireHub        bool     `json:"require_hub,omitempty"`
	Workload          string   `json:"workload,omitempty"`
	CandidateMachines []string `json:"candidate_machines,omitempty"`
}

// PlacementPendingTask is the sanitized scheduling view of one task awaiting machine assignment.
// It deliberately omits prompts, titles, context, attachments, and private-space metadata.
type PlacementPendingTask struct {
	PlanID               PlanID                `json:"plan_id"`
	TodoID               wireids.TodoID        `json:"todo_id"`
	Version              int                   `json:"version"`
	Role                 PlacementTaskRole     `json:"role"`
	Readonly             bool                  `json:"readonly"`
	Launcher             string                `json:"launcher,omitempty"`
	Tier                 string                `json:"tier,omitempty"`
	Host                 string                `json:"host,omitempty"`
	CwdHint              string                `json:"cwd_hint,omitempty"`
	MachinePolicy        string                `json:"machine_policy,omitempty"`
	PlacementConstraints *PlacementConstraints `json:"placement_constraints,omitempty"`
	BatchID              BatchID               `json:"batch_id"`
	BatchSize            int                   `json:"batch_size"`
	BatchIndex           int                   `json:"batch_index"`
	Attempt              int                   `json:"attempt"`
	QueuedAt             time.Time             `json:"queued_at"`
	FallbackAt           time.Time             `json:"fallback_at"`
	LastReason           string                `json:"last_reason,omitempty"`
}

// MachineResourceSummary is one machine's read-only resource snapshot served by GET /api/v1/assistant/machine-resources.
// Nullable fields remain explicit null when unknown, stale, offline, pending, future, or ambiguous.
type MachineResourceSummary struct {
	ID                   wireids.MachineID    `json:"id"`
	Name                 string               `json:"name"`
	State                MachineResourceState `json:"state"`
	Online               bool                 `json:"online"`
	OS                   *string              `json:"os"`
	Arch                 *string              `json:"arch"`
	Cores                *int                 `json:"cores"`
	CPUBusyPercent       *float64             `json:"cpu_busy_percent"`
	Load1M               *float64             `json:"load_1m"`
	MemoryAvailableBytes *int64               `json:"memory_available_bytes"`
	MemoryTotalBytes     *int64               `json:"memory_total_bytes"`
	DiskFreeBytes        *int64               `json:"disk_free_bytes"`
	DiskTotalBytes       *int64               `json:"disk_total_bytes"`
	SampleAgeSeconds     *float64             `json:"sample_age_seconds"`
	WorkingAgents        *int                 `json:"working_agents"`
	FailingLaunchers     []string             `json:"failing_launchers"`
	IsHub                bool                 `json:"is_hub"`
}
