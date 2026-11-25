package assistant

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"weak"

	"github.com/nwverse/persona/server/internal/assistant/todo/mergetrain"
)

// mergeTrainOutcome captures the deterministic result of feeding a PR status observation
// into the per-Service merge train coordinator under todos_v2.
type mergeTrainOutcome struct {
	Tracked  bool
	RepoKey  string
	Decision mergetrain.Decision
	Escalate bool
	Detail   string
}

type mergeTrainConflictTrack struct {
	inConflict bool
	lastHead   string
}

// mergeTrainCoordinator wraps the pure mergetrain.Train state machine with concurrency-safe
// conflict-cycle tracking and escalation deduplication for live PR status polling.
type mergeTrainCoordinator struct {
	mu        sync.Mutex
	train     *mergetrain.Train
	conflicts map[string]*mergeTrainConflictTrack
	escalated map[string]struct{}
}

func newMergeTrainCoordinator() *mergeTrainCoordinator {
	return &mergeTrainCoordinator{
		train:     mergetrain.New(),
		conflicts: make(map[string]*mergeTrainConflictTrack),
		escalated: make(map[string]struct{}),
	}
}

func (c *mergeTrainCoordinator) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.train = mergetrain.New()
	clear(c.conflicts)
	clear(c.escalated)
}

func (c *mergeTrainCoordinator) snapshot() mergetrain.Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.train == nil {
		return mergetrain.Snapshot{}
	}
	return c.train.Snapshot()
}

func (c *mergeTrainCoordinator) observe(todoID string, status TodoPRStatus, repoKey string, prNumber int, normURL string) mergeTrainOutcome {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.train == nil {
		c.train = mergetrain.New()
	}

	trackKey := todoID + "|" + normURL
	state := strings.ToLower(strings.TrimSpace(status.State))

	switch state {
	case "merged":
		delete(c.conflicts, trackKey)
		dec, err := c.train.RecordMerged(repoKey, todoID)
		if errors.Is(err, mergetrain.ErrEntryNotFound) {
			return mergeTrainOutcome{RepoKey: repoKey, Decision: dec}
		}
		return mergeTrainOutcome{Tracked: true, RepoKey: repoKey, Decision: dec}

	case "closed":
		delete(c.conflicts, trackKey)
		dec, err := c.train.Cancel(repoKey, todoID)
		if errors.Is(err, mergetrain.ErrEntryNotFound) {
			return mergeTrainOutcome{RepoKey: repoKey, Decision: dec}
		}
		return mergeTrainOutcome{Tracked: true, RepoKey: repoKey, Decision: dec}

	case "open":
		policy := mergeTrainPolicyForStatus(status)
		approved := policy == mergetrain.PolicyAutoMerge
		entry, err := c.train.Enqueue(mergetrain.Entry{
			TodoID:   todoID,
			RepoKey:  repoKey,
			PRNumber: prNumber,
			PRURL:    normURL,
			Policy:   policy,
			Approved: approved,
		})
		if err != nil {
			return mergeTrainOutcome{RepoKey: repoKey}
		}

		if strings.EqualFold(strings.TrimSpace(status.Review), "approved") && !status.Draft && !entry.Approved && entry.State != mergetrain.StateEscalated {
			if _, approveErr := c.train.Approve(repoKey, todoID); approveErr == nil {
				if updated, ok := c.train.Entry(repoKey, todoID); ok {
					entry = updated
				}
			}
		}

		headOID := strings.TrimSpace(status.HeadOID)
		if status.Conflicts {
			track := c.conflicts[trackKey]
			if track == nil {
				track = &mergeTrainConflictTrack{}
				c.conflicts[trackKey] = track
			}
			isNewConflict := !track.inConflict || (headOID != "" && track.lastHead != headOID)
			track.inConflict = true
			if headOID != "" {
				track.lastHead = headOID
			}
			if isNewConflict {
				errMsg := "merge conflicts detected"
				if headOID != "" {
					errMsg = fmt.Sprintf("merge conflicts at head %s", headOID)
				}
				dec, failErr := c.train.RecordRebaseFailure(repoKey, todoID, errMsg)
				if failErr == nil && dec.Action == mergetrain.ActionEscalateNeedsUser {
					dedupKey := todoID + "|" + normURL + "|" + headOID
					escalate := false
					if _, seen := c.escalated[dedupKey]; !seen {
						c.escalated[dedupKey] = struct{}{}
						escalate = true
					}
					detail := formatMergeTrainEscalationDetail(status.URL, headOID, dec.Reason)
					return mergeTrainOutcome{
						Tracked:  true,
						RepoKey:  repoKey,
						Decision: dec,
						Escalate: escalate,
						Detail:   detail,
					}
				}
				if failErr == nil {
					return mergeTrainOutcome{
						Tracked:  true,
						RepoKey:  repoKey,
						Decision: dec,
					}
				}
			}
			if latest, ok := c.train.Entry(repoKey, todoID); ok && latest.State == mergetrain.StateEscalated {
				return mergeTrainOutcome{
					Tracked: true,
					RepoKey: repoKey,
					Decision: mergetrain.Decision{
						RepoKey: repoKey,
						TodoID:  todoID,
						Action:  mergetrain.ActionEscalateNeedsUser,
						State:   mergetrain.StateEscalated,
						Reason:  latest.LastError,
					},
				}
			}
			return mergeTrainOutcome{
				Tracked: true,
				RepoKey: repoKey,
				Decision: mergetrain.Decision{
					RepoKey: repoKey,
					TodoID:  todoID,
					Action:  mergetrain.ActionWaitQueue,
					State:   entry.State,
					Reason:  "waiting on merge conflict resolution",
				},
			}
		}

		// Conflicts cleared: reset consecutive failure streak for non-escalated entries.
		if track := c.conflicts[trackKey]; track != nil {
			track.inConflict = false
			track.lastHead = ""
		}
		_ = c.train.RecordRebaseSuccess(repoKey, todoID)

		if latest, ok := c.train.Entry(repoKey, todoID); ok {
			entry = latest
		}
		// Escalated entries must never trigger head evaluation (they may still be queued behind
		// a different active head, whose checks must not be evaluated using this PR's status).
		if entry.State == mergetrain.StateEscalated {
			reason := "rebase failure circuit breaker tripped"
			if entry.LastError != "" {
				reason = fmt.Sprintf("rebase failure circuit breaker tripped (%d/%d): %s",
					entry.RebaseFailures, entry.MaxRebaseFailures, entry.LastError)
			}
			return mergeTrainOutcome{
				Tracked: true,
				RepoKey: repoKey,
				Decision: mergetrain.Decision{
					RepoKey: repoKey,
					TodoID:  todoID,
					Action:  mergetrain.ActionEscalateNeedsUser,
					State:   mergetrain.StateEscalated,
					Reason:  reason,
				},
			}
		}
		// Only evaluate the repository head using this PR's own checks; queued entries wait their turn.
		if entry.State == mergetrain.StateActive || entry.State == mergetrain.StateAwaitingApproval {
			checksGreen := strings.EqualFold(strings.TrimSpace(status.Checks), "success") && !status.Draft
			reviewApproved := strings.EqualFold(strings.TrimSpace(status.Review), "approved")
			// Never auto-merge an already-approved entry if current review status is no longer approved
			// (e.g. changes_requested, required, pending after dismissal/truncation, or empty).
			if !reviewApproved && (entry.Policy == mergetrain.PolicyAutoMerge || entry.Approved) {
				checksGreen = false
			}
			dec := c.train.EvaluateHead(repoKey, checksGreen)
			return mergeTrainOutcome{
				Tracked:  true,
				RepoKey:  repoKey,
				Decision: dec,
			}
		}
		return mergeTrainOutcome{
			Tracked: true,
			RepoKey: repoKey,
			Decision: mergetrain.Decision{
				RepoKey: repoKey,
				TodoID:  todoID,
				Action:  mergetrain.ActionWaitQueue,
				State:   entry.State,
				Reason:  "queued behind active repository head",
			},
		}
	default:
		return mergeTrainOutcome{RepoKey: repoKey}
	}
}

func mergeTrainPolicyForStatus(status TodoPRStatus) mergetrain.MergePolicy {
	if status.Draft || !strings.EqualFold(strings.TrimSpace(status.Review), "approved") {
		return mergetrain.PolicyHumanReviewRequired
	}
	return mergetrain.PolicyAutoMerge
}

func formatMergeTrainEscalationDetail(prURL, headOID, reason string) string {
	headPart := ""
	if headOID != "" {
		headPart = " at head " + headOID
	}
	if strings.TrimSpace(reason) != "" {
		return fmt.Sprintf("Merge train circuit breaker tripped (needs_user) for PR %s%s: %s. Automatic conflict retries stopped; manual resolution required.", prURL, headPart, reason)
	}
	return fmt.Sprintf("Merge train circuit breaker tripped (needs_user) for PR %s%s. Automatic conflict retries stopped; manual resolution required.", prURL, headPart)
}

// parseMergeTrainPRURL extracts a canonical RepoKey (e.g. "github.com/owner/repo"),
// PR number, and normalized URL from a pull/merge request URL.
func parseMergeTrainPRURL(raw string) (repoKey string, prNumber int, normURL string, ok bool) {
	normURL = triageNormalizePRURL(raw)
	if normURL == "" {
		return "", 0, "", false
	}
	if m := ciPRURL.FindStringSubmatch(normURL); m != nil {
		n, err := strconv.Atoi(m[2])
		if err != nil || n <= 0 {
			return "", 0, "", false
		}
		return mergetrain.NormalizeRepoKey("github.com/" + strings.ToLower(m[1])), n, normURL, true
	}
	u, err := url.Parse(normURL)
	if err != nil || u.Host == "" {
		return "", 0, "", false
	}
	host := strings.ToLower(strings.TrimSpace(u.Host))
	p := strings.Trim(u.Path, "/")
	for _, sep := range []string{"/-/merge_requests/", "/merge_requests/", "/pull/"} {
		if repoPath, tail, found := strings.Cut(p, sep); found {
			numStr, _, _ := strings.Cut(tail, "/")
			n, convErr := strconv.Atoi(numStr)
			if convErr != nil || n <= 0 || strings.TrimSpace(repoPath) == "" {
				return "", 0, "", false
			}
			return mergetrain.NormalizeRepoKey(host + "/" + strings.ToLower(strings.Trim(repoPath, "/"))), n, normURL, true
		}
	}
	return "", 0, "", false
}

// mergeTrainRegistry isolates per-Service merge train coordinators without modifying Service fields
// and cleans up entries automatically when a Service is garbage-collected.
var mergeTrainRegistry = struct {
	mu    sync.Mutex
	bySvc map[weak.Pointer[Service]]*mergeTrainCoordinator
}{
	bySvc: make(map[weak.Pointer[Service]]*mergeTrainCoordinator),
}

func (s *Service) mergeTrainCoordinator() *mergeTrainCoordinator {
	if s == nil {
		return nil
	}
	wp := weak.Make(s)
	mergeTrainRegistry.mu.Lock()
	defer mergeTrainRegistry.mu.Unlock()
	if coord, ok := mergeTrainRegistry.bySvc[wp]; ok {
		return coord
	}
	coord := newMergeTrainCoordinator()
	mergeTrainRegistry.bySvc[wp] = coord
	runtime.AddCleanup(s, func(key weak.Pointer[Service]) {
		mergeTrainRegistry.mu.Lock()
		delete(mergeTrainRegistry.bySvc, key)
		mergeTrainRegistry.mu.Unlock()
	}, wp)
	return coord
}

func (s *Service) peekMergeTrainCoordinator() *mergeTrainCoordinator {
	if s == nil {
		return nil
	}
	wp := weak.Make(s)
	mergeTrainRegistry.mu.Lock()
	defer mergeTrainRegistry.mu.Unlock()
	return mergeTrainRegistry.bySvc[wp]
}

func (s *Service) resetMergeTrain() {
	if coord := s.peekMergeTrainCoordinator(); coord != nil {
		coord.reset()
	}
}

// mergeTrainSnapshot returns a read-only snapshot of this Service's merge train queues.
// When todos_v2 is disabled, it clears any retained in-memory queue and returns an empty snapshot.
func (s *Service) mergeTrainSnapshot(ctx context.Context) mergetrain.Snapshot {
	if s == nil || !s.settings(ctx).todosV2On() {
		s.resetMergeTrain()
		return mergetrain.Snapshot{}
	}
	coord := s.peekMergeTrainCoordinator()
	if coord == nil {
		return mergetrain.Snapshot{}
	}
	return coord.snapshot()
}

// onPRStatusMergeTrain updates the in-memory per-repository merge train from a persisted PR status
// observation when todos_v2 is enabled, and bypasses/resets immediately when todos_v2 is off.
func (s *Service) onPRStatusMergeTrain(ctx context.Context, t Todo, status TodoPRStatus) mergeTrainOutcome {
	if s == nil || !s.settings(ctx).todosV2On() {
		s.resetMergeTrain()
		return mergeTrainOutcome{}
	}
	if strings.TrimSpace(t.ID) == "" {
		return mergeTrainOutcome{}
	}
	repoKey, prNumber, normURL, ok := parseMergeTrainPRURL(status.URL)
	if !ok {
		return mergeTrainOutcome{}
	}
	coord := s.mergeTrainCoordinator()
	if coord == nil {
		return mergeTrainOutcome{}
	}
	outcome := coord.observe(t.ID, status, repoKey, prNumber, normURL)
	if outcome.Escalate && status.State == "open" && status.Conflicts && status.URL == t.PRURL &&
		status.HeadOID != "" && t.Supervision != nil && !parkWaitsOnPR(t.Supervision.parking(), status.URL) {
		s.signal(superviseSignal{
			Todo:   t.ID,
			Kind:   sigPRConflict,
			Key:    status.URL + "@" + status.HeadOID,
			Detail: outcome.Detail,
		})
	}
	return outcome
}
