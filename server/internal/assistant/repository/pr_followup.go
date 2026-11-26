package repository

import "time"

// Watch statuses persisted on todo_pr_followups (#12371). An empty WatchStatus on a row written before this column
// existed reads as active watching.
const (
	// PRFollowupWatchActive means the subscription is actively polled by a connected code host.
	PRFollowupWatchActive = "active"
	// PRFollowupWatchHostNotConnected means the subscription is not being watched because GitHub is not connected
	// or the PR's host has no code host that can read its review/conflict/status state.
	PRFollowupWatchHostNotConnected = "host_not_connected"
)

// Watch reasons explaining why WatchStatus is PRFollowupWatchHostNotConnected.
const (
	// PRFollowupReasonHostNotConnected means the code host (such as GitHub or a disabled/signed-out provider) is
	// not connected.
	PRFollowupReasonHostNotConnected = "host_not_connected"
	// PRFollowupReasonUnsupportedHost means the PR's host has no code host capable of reading its review state.
	PRFollowupReasonUnsupportedHost = "unsupported_host"
)

// PRFollowup is a lightweight subscription that keeps watching a to-do's own delivery PR after the to-do was
// accepted. It never reopens the to-do: the PR sweep polls it independently of the to-do's DoneAt and notifies the
// original supervisor conversation once per event, then removes the subscription on a terminal PR state. All fields
// persist in Postgres (todo_pr_followups), never in a JSON setting.
type PRFollowup struct {
	// ID is the subscription id (stable, derived from the to-do id and PR url).
	ID string `json:"id"`
	// TodoID is the accepted (closed) to-do the delivery PR belonged to.
	TodoID string `json:"todo_id"`
	// PRURL is the delivery PR the subscription watches.
	PRURL string `json:"pr_url"`
	// Supervisor is the conversation that reviewed the to-do; notifications go there.
	Supervisor string `json:"supervisor"`
	// Origin is the to-do's own dispatch conversation, kept for the notification's lineage.
	Origin string `json:"origin"`
	// Last observed forge facts. State is "" until the first poll.
	HeadOID            string     `json:"head_oid,omitempty"`
	State              string     `json:"state,omitempty"`
	Review             string     `json:"review,omitempty"`
	ChangesRequestedAt *time.Time `json:"changes_requested_at,omitempty"`
	LastObservedAt     *time.Time `json:"last_observed_at,omitempty"`
	// CreatedAt records the to-do's closure time for new subscriptions.
	CreatedAt time.Time `json:"created_at"`
	// Deduplication keys: a conflict or a current-head changes-requested review wakes the supervisor only once per head.
	NotifiedConflictHead string `json:"notified_conflict_head,omitempty"`
	NotifiedReviewHead   string `json:"notified_review_head,omitempty"`
	// WatchStatus persists whether the subscription is actively watched ("active", or "" for rows predating the
	// column) or degraded because the code host is disconnected / unsupported ("host_not_connected"). WatchReason
	// clarifies why it is unwatched ("host_not_connected" or "unsupported_host"), and WatchUpdatedAt records when
	// that watch status was recorded without touching LastObservedAt.
	WatchStatus    string     `json:"watch_status,omitempty"`
	WatchReason    string     `json:"watch_reason,omitempty"`
	WatchUpdatedAt *time.Time `json:"watch_updated_at,omitempty"`
}

// EffectiveWatchStatus returns PRFollowupWatchActive for legacy rows whose WatchStatus is empty.
func (f PRFollowup) EffectiveWatchStatus() string {
	if f.WatchStatus == "" {
		return PRFollowupWatchActive
	}
	return f.WatchStatus
}

// IsUnwatched reports whether the subscription is recorded as not being watched.
func (f PRFollowup) IsUnwatched() bool {
	return f.EffectiveWatchStatus() != PRFollowupWatchActive
}
