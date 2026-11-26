package assistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"weak"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistant/todo/mergetrain"
	"github.com/nwverse/persona/server/internal/db"
)

// mergeTrainOutcome captures the deterministic result of feeding a PR status observation
// into the per-Service merge train coordinator under todos_v2.
type mergeTrainOutcome struct {
	Tracked  bool
	RepoKey  string
	Decision mergetrain.Decision
	Escalate bool
	Detail   string

	promotedTodoID   string
	promotedPRURL    string
	promotedRepoKey  string
	previousPRURL    string
	previousState    string
	promotedPolicy   mergetrain.MergePolicy
	promotedApproved bool
	turnWakeupKey    string
	emitTurnWakeup   bool
}

type mergeTrainConflictTrack struct {
	inConflict bool
	lastHead   string
}

type mergeTrainBlockerCondition struct {
	RepoKey     string `json:"repo_key,omitempty"`
	PRURL       string `json:"pr_url,omitempty"`
	LastHeadOID string `json:"last_head_oid,omitempty"`
	MaxAttempts int    `json:"max_attempts,omitempty"`
}

type mergeTrainDBAction struct {
	persistConflict bool
	hadActiveRow    bool
	attempts        int
	headOID         string
	resolveBlocker  bool
}

func mergeTrainBlockerID(todoID, normURL string) string {
	sum := sha256.Sum256([]byte(todoID + "\x00" + todoBlockerKindMergeTrain + "\x00" + normURL))
	return "mt_" + hex.EncodeToString(sum[:8])
}

// mergeTrainCoordinator wraps the pure mergetrain.Train state machine with concurrency-safe
// conflict-cycle tracking, escalation deduplication, and turn-wakeup deduplication for live PR status polling.
type mergeTrainCoordinator struct {
	mu               sync.Mutex
	train            *mergetrain.Train
	conflicts        map[string]*mergeTrainConflictTrack
	escalated        map[string]struct{}
	turnWakeups      map[string]struct{}
	hydrated         map[string]bool
	hasActiveBlocker map[string]bool
}

func newMergeTrainCoordinator() *mergeTrainCoordinator {
	return &mergeTrainCoordinator{
		train:            mergetrain.New(),
		conflicts:        make(map[string]*mergeTrainConflictTrack),
		escalated:        make(map[string]struct{}),
		turnWakeups:      make(map[string]struct{}),
		hydrated:         make(map[string]bool),
		hasActiveBlocker: make(map[string]bool),
	}
}

func (c *mergeTrainCoordinator) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.train = mergetrain.New()
	clear(c.conflicts)
	clear(c.escalated)
	clear(c.turnWakeups)
	clear(c.hydrated)
	clear(c.hasActiveBlocker)
}

func (c *mergeTrainCoordinator) isHydrated(trackKey string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hydrated[trackKey]
}

func (c *mergeTrainCoordinator) setHasActiveBlocker(trackKey string, active bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if active {
		c.hasActiveBlocker[trackKey] = true
	} else {
		delete(c.hasActiveBlocker, trackKey)
	}
}

func (c *mergeTrainCoordinator) clearBlocker(todoID, targetRef, conditionJSON string) {
	todoID = strings.TrimSpace(todoID)
	if todoID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	var cond mergeTrainBlockerCondition
	if strings.TrimSpace(conditionJSON) != "" {
		_ = json.Unmarshal([]byte(conditionJSON), &cond)
	}

	repoKey := mergetrain.NormalizeRepoKey(cond.RepoKey)
	normURL := triageNormalizePRURL(cond.PRURL)
	if rk, _, u, ok := parseMergeTrainPRURL(targetRef); ok {
		if repoKey == "" {
			repoKey = rk
		}
		if normURL == "" {
			normURL = u
		}
	} else if repoKey == "" {
		repoKey = mergetrain.NormalizeRepoKey(targetRef)
	}
	if repoKey == "" && normURL != "" {
		if rk, _, _, ok := parseMergeTrainPRURL(normURL); ok {
			repoKey = rk
		}
	}
	if normURL == "" && c.train != nil && repoKey != "" {
		if entry, ok := c.train.Entry(repoKey, todoID); ok && strings.TrimSpace(entry.PRURL) != "" {
			normURL = triageNormalizePRURL(entry.PRURL)
		}
	}

	if normURL != "" {
		trackKey := todoID + "|" + normURL
		delete(c.hasActiveBlocker, trackKey)
		delete(c.conflicts, trackKey)
		delete(c.hydrated, trackKey)
		escPrefix := todoID + "|" + normURL + "|"
		for k := range c.escalated {
			if strings.HasPrefix(k, escPrefix) {
				delete(c.escalated, k)
			}
		}
	} else {
		todoPrefix := todoID + "|"
		matchesKey := func(k string) bool {
			if !strings.HasPrefix(k, todoPrefix) {
				return false
			}
			if repoKey == "" {
				return true
			}
			rest := strings.TrimPrefix(k, todoPrefix)
			prPart, _, _ := strings.Cut(rest, "|")
			rk, _, _, ok := parseMergeTrainPRURL(prPart)
			return ok && rk == repoKey
		}
		for k := range c.hasActiveBlocker {
			if matchesKey(k) {
				delete(c.hasActiveBlocker, k)
			}
		}
		for k := range c.conflicts {
			if matchesKey(k) {
				delete(c.conflicts, k)
			}
		}
		for k := range c.hydrated {
			if matchesKey(k) {
				delete(c.hydrated, k)
			}
		}
		for k := range c.escalated {
			if matchesKey(k) {
				delete(c.escalated, k)
			}
		}
	}

	if c.train != nil && repoKey != "" {
		_, _ = c.train.Cancel(repoKey, todoID)
		c.train = rebuildMergeTrainWithout(c.train, repoKey, todoID)
	}
}

func rebuildMergeTrainWithout(old *mergetrain.Train, dropRepoKey, dropTodoID string) *mergetrain.Train {
	if old == nil {
		return mergetrain.New()
	}
	snap := old.Snapshot()
	next := mergetrain.New()
	for _, rs := range snap.Repos {
		var entries []mergetrain.Entry
		if rs.Head != nil {
			entries = append(entries, *rs.Head)
		}
		entries = append(entries, rs.Queue...)
		entries = append(entries, rs.Escalated...)
		for _, e := range entries {
			if rs.RepoKey == dropRepoKey && e.TodoID == dropTodoID {
				continue
			}
			_, _ = next.Enqueue(mergetrain.Entry{
				TodoID:            e.TodoID,
				RepoKey:           e.RepoKey,
				PRNumber:          e.PRNumber,
				PRURL:             e.PRURL,
				Priority:          e.Priority,
				Policy:            e.Policy,
				Approved:          e.Approved,
				MaxRebaseFailures: e.MaxRebaseFailures,
				ConflictReasons:   e.ConflictReasons,
			})
			for range e.RebaseFailures {
				_, _ = next.RecordRebaseFailure(e.RepoKey, e.TodoID, e.LastError)
			}
		}
	}
	return next
}

func (c *mergeTrainCoordinator) hydrateIfNeeded(trackKey, todoID, repoKey string, prNumber int, normURL string, status TodoPRStatus, activeRow *db.TodoBlockerRow) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hydrated[trackKey] {
		return
	}
	c.hydrated[trackKey] = true
	if activeRow == nil {
		return
	}
	c.hasActiveBlocker[trackKey] = true

	var cond mergeTrainBlockerCondition
	if strings.TrimSpace(activeRow.ConditionJSON) != "" {
		_ = json.Unmarshal([]byte(activeRow.ConditionJSON), &cond)
	}
	lastHead := strings.TrimSpace(cond.LastHeadOID)

	if !strings.EqualFold(strings.TrimSpace(status.State), "open") || activeRow.Attempts <= 0 {
		return
	}
	if c.train == nil {
		c.train = mergetrain.New()
	}
	policy := mergeTrainPolicyForStatus(status)
	approved := policy == mergetrain.PolicyAutoMerge
	if _, err := c.train.Enqueue(mergetrain.Entry{
		TodoID:   todoID,
		RepoKey:  repoKey,
		PRNumber: prNumber,
		PRURL:    normURL,
		Policy:   policy,
		Approved: approved,
	}); err != nil {
		return
	}
	replay := min(activeRow.Attempts, mergetrain.DefaultMaxRebaseFailures)
	errMsg := "restored prior merge conflict"
	if lastHead != "" {
		errMsg = fmt.Sprintf("merge conflicts at head %s", lastHead)
	}
	for range replay {
		_, _ = c.train.RecordRebaseFailure(repoKey, todoID, errMsg)
	}
	c.conflicts[trackKey] = &mergeTrainConflictTrack{
		inConflict: true,
		lastHead:   lastHead,
	}
	if activeRow.Attempts >= mergetrain.DefaultMaxRebaseFailures && lastHead != "" {
		c.escalated[todoID+"|"+normURL+"|"+lastHead] = struct{}{}
	}
}

func (c *mergeTrainCoordinator) snapshot() mergetrain.Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.train == nil {
		return mergetrain.Snapshot{}
	}
	return c.train.Snapshot()
}

func (c *mergeTrainCoordinator) headTodoIDLocked(repoKey string) string {
	if c.train == nil {
		return ""
	}
	for _, rs := range c.train.Snapshot().Repos {
		if rs.RepoKey == repoKey {
			if rs.Head != nil {
				return rs.Head.TodoID
			}
			return ""
		}
	}
	return ""
}

func (c *mergeTrainCoordinator) recordPromotedTurnLocked(repoKey, prevTodoID, prevPRURL, prevState, prevHeadID string, dec mergetrain.Decision, out *mergeTrainOutcome) {
	if out == nil || c.train == nil {
		return
	}
	nextTodoID := strings.TrimSpace(dec.TodoID)
	if nextTodoID == "" || nextTodoID == prevTodoID || nextTodoID == prevHeadID {
		return
	}
	nextEntry, ok := c.train.Entry(repoKey, nextTodoID)
	if !ok || nextEntry.State == mergetrain.StateEscalated {
		return
	}
	wakeKey := "mergetrain_turn:" + repoKey + ":" + nextTodoID + ":" + prevPRURL
	emit := false
	if _, seen := c.turnWakeups[wakeKey]; !seen {
		c.turnWakeups[wakeKey] = struct{}{}
		emit = true
	}
	out.promotedTodoID = nextTodoID
	out.promotedPRURL = nextEntry.PRURL
	out.promotedRepoKey = repoKey
	out.previousPRURL = prevPRURL
	out.previousState = prevState
	out.promotedPolicy = nextEntry.Policy
	out.promotedApproved = nextEntry.Approved
	out.turnWakeupKey = wakeKey
	out.emitTurnWakeup = emit
}

func (c *mergeTrainCoordinator) observe(todoID string, status TodoPRStatus, repoKey string, prNumber int, normURL string) (mergeTrainOutcome, mergeTrainDBAction) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.train == nil {
		c.train = mergetrain.New()
	}

	trackKey := todoID + "|" + normURL
	state := strings.ToLower(strings.TrimSpace(status.State))
	var dbAct mergeTrainDBAction

	switch state {
	case "merged":
		delete(c.conflicts, trackKey)
		if c.hasActiveBlocker[trackKey] {
			dbAct.resolveBlocker = true
		}
		prevHeadID := c.headTodoIDLocked(repoKey)
		dec, err := c.train.RecordMerged(repoKey, todoID)
		if errors.Is(err, mergetrain.ErrEntryNotFound) {
			return mergeTrainOutcome{RepoKey: repoKey, Decision: dec}, dbAct
		}
		out := mergeTrainOutcome{Tracked: true, RepoKey: repoKey, Decision: dec}
		if err == nil {
			c.recordPromotedTurnLocked(repoKey, todoID, normURL, state, prevHeadID, dec, &out)
		}
		return out, dbAct

	case "closed":
		delete(c.conflicts, trackKey)
		if c.hasActiveBlocker[trackKey] {
			dbAct.resolveBlocker = true
		}
		prevHeadID := c.headTodoIDLocked(repoKey)
		dec, err := c.train.Cancel(repoKey, todoID)
		if errors.Is(err, mergetrain.ErrEntryNotFound) {
			return mergeTrainOutcome{RepoKey: repoKey, Decision: dec}, dbAct
		}
		out := mergeTrainOutcome{Tracked: true, RepoKey: repoKey, Decision: dec}
		if err == nil {
			c.recordPromotedTurnLocked(repoKey, todoID, normURL, state, prevHeadID, dec, &out)
		}
		return out, dbAct

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
			return mergeTrainOutcome{RepoKey: repoKey}, dbAct
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
				if failErr == nil {
					attempts := 1
					if latest, ok := c.train.Entry(repoKey, todoID); ok && latest.RebaseFailures > 0 {
						attempts = latest.RebaseFailures
					}
					dbAct.persistConflict = true
					dbAct.hadActiveRow = c.hasActiveBlocker[trackKey]
					dbAct.attempts = attempts
					dbAct.headOID = headOID
				}
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
					}, dbAct
				}
				if failErr == nil {
					return mergeTrainOutcome{
						Tracked:  true,
						RepoKey:  repoKey,
						Decision: dec,
					}, dbAct
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
				}, dbAct
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
			}, dbAct
		}

		// Conflicts cleared: reset consecutive failure streak for non-escalated entries.
		if track := c.conflicts[trackKey]; track != nil {
			track.inConflict = false
			track.lastHead = ""
		}
		if c.hasActiveBlocker[trackKey] {
			dbAct.resolveBlocker = true
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
			}, dbAct
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
			}, dbAct
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
		}, dbAct
	default:
		return mergeTrainOutcome{RepoKey: repoKey}, dbAct
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

func formatMergeTrainTurnWakeupDetail(o mergeTrainOutcome) string {
	verb := "merged"
	if o.previousState == "closed" {
		verb = "closed and left the merge queue"
	}
	prPart := ""
	if strings.TrimSpace(o.promotedPRURL) != "" {
		prPart = " (" + strings.TrimSpace(o.promotedPRURL) + ")"
	}
	policyNote := "Ready to rebase, verify checks, and merge according to its review policy."
	if o.promotedPolicy == mergetrain.PolicyHumanReviewRequired && !o.promotedApproved {
		policyNote = "Human review approval is still required before merging; ready to rebase and verify checks without auto-merging."
	}
	return fmt.Sprintf("Preceding PR %s in %s has %s. Your PR%s is now at the head of the merge queue (position 1). %s",
		o.previousPRURL, o.promotedRepoKey, verb, prPart, policyNote)
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

func (s *Service) clearMergeTrainBlocker(todoID, targetRef, conditionJSON string) {
	if coord := s.peekMergeTrainCoordinator(); coord != nil {
		coord.clearBlocker(todoID, targetRef, conditionJSON)
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
// observation when todos_v2 is enabled, persists consecutive conflict counts in todo_blockers
// (kind="merge_train", zero new migrations) so counts survive Hub restarts, and bypasses/resets
// immediately when todos_v2 is off.
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

	trackKey := t.ID + "|" + normURL
	blockerID := mergeTrainBlockerID(t.ID, normURL)
	st := s.todoBlockerDB()
	if st != nil && !coord.isHydrated(trackKey) {
		if rows, err := st.ListTodoBlockersByTodo(ctx, t.ID); err == nil {
			var activeRow *db.TodoBlockerRow
			for i := range rows {
				if rows[i].ID == blockerID && rows[i].Kind == todoBlockerKindMergeTrain && rows[i].State == db.TodoBlockerStateActive {
					cp := rows[i]
					activeRow = &cp
					break
				}
			}
			coord.hydrateIfNeeded(trackKey, t.ID, repoKey, prNumber, normURL, status, activeRow)
		}
	}

	outcome, dbAct := coord.observe(t.ID, status, repoKey, prNumber, normURL)
	if st != nil {
		if dbAct.persistConflict {
			if err := s.persistMergeTrainConflictBlocker(ctx, st, blockerID, t.ID, repoKey, normURL, dbAct.headOID, dbAct.attempts, dbAct.hadActiveRow); err != nil {
				s.logf("todos_v2 mergetrain persist conflict blocker %s: %v", t.ID, err)
			} else {
				coord.setHasActiveBlocker(trackKey, true)
			}
		} else if dbAct.resolveBlocker {
			if _, err := st.ResolveTodoBlocker(ctx, blockerID, time.Now().UTC()); err != nil {
				s.logf("todos_v2 mergetrain resolve conflict blocker %s: %v", t.ID, err)
			} else {
				coord.setHasActiveBlocker(trackKey, false)
			}
		}
	}
	if outcome.Escalate && status.State == "open" && status.Conflicts && status.URL == t.PRURL &&
		status.HeadOID != "" && t.Supervision != nil && !parkWaitsOnPR(t.Supervision.parking(), status.URL) {
		s.signal(superviseSignal{
			Todo:   t.ID,
			Kind:   sigPRConflict,
			Key:    status.URL + "@" + status.HeadOID,
			Detail: outcome.Detail,
		})
	}
	if outcome.emitTurnWakeup && outcome.promotedTodoID != "" {
		if nextTodo, ok := s.todoNow(outcome.promotedTodoID); ok && nextTodo.DoneAt == nil && !nextTodo.Cancelled() {
			s.signal(superviseSignal{
				Todo:   outcome.promotedTodoID,
				Kind:   sigTodoReady,
				Key:    outcome.turnWakeupKey,
				To:     s.todoReadySupervisor(ctx, s.settings(ctx), nextTodo),
				Detail: formatMergeTrainTurnWakeupDetail(outcome),
			})
		}
	}
	return outcome
}

func (s *Service) persistMergeTrainConflictBlocker(
	ctx context.Context,
	st todoBlockerStore,
	blockerID, todoID, repoKey, normURL, headOID string,
	attempts int,
	hadActiveRow bool,
) error {
	now := time.Now().UTC()
	condBytes, err := json.Marshal(mergeTrainBlockerCondition{
		RepoKey:     repoKey,
		PRURL:       normURL,
		LastHeadOID: headOID,
		MaxAttempts: mergetrain.DefaultMaxRebaseFailures,
	})
	if err != nil {
		return err
	}
	condJSON := string(condBytes)
	if hadActiveRow {
		if incRow, incErr := st.IncrementTodoBlockerAttempts(ctx, blockerID, now); incErr == nil && incRow != nil {
			incRow.ConditionJSON = condJSON
			incRow.State = db.TodoBlockerStateActive
			incRow.TargetRef = repoKey
			incRow.UpdatedAt = now
			incRow.ResolvedAt = nil
			_, err = st.UpsertTodoBlocker(ctx, *incRow)
			return err
		}
	}
	if attempts < 1 {
		attempts = 1
	}
	_, err = st.UpsertTodoBlocker(ctx, db.TodoBlockerRow{
		ID:            blockerID,
		TodoID:        todoID,
		Kind:          todoBlockerKindMergeTrain,
		TargetRef:     repoKey,
		ConditionJSON: condJSON,
		State:         db.TodoBlockerStateActive,
		Attempts:      attempts,
		CreatedAt:     now,
		UpdatedAt:     now,
		ResolvedAt:    nil,
	})
	return err
}

type mergeTrainQueueItemView struct {
	TodoID         string `json:"todo_id"`
	PRNumber       int    `json:"pr_number,omitempty"`
	PRURL          string `json:"pr_url,omitempty"`
	State          string `json:"state"`
	Policy         string `json:"policy"`
	Approved       bool   `json:"approved"`
	Position       int    `json:"position"`
	RebaseFailures int    `json:"rebase_failures,omitempty"`
	LastError      string `json:"last_error,omitempty"`
}

type mergeTrainRepoQueueView struct {
	RepoKey   string                    `json:"repo_key"`
	Head      *mergeTrainQueueItemView  `json:"head,omitempty"`
	Queue     []mergeTrainQueueItemView `json:"queue"`
	Escalated []mergeTrainQueueItemView `json:"escalated,omitempty"`
}

type mergeTrainTodoBadgeView struct {
	RepoKey    string `json:"repo_key"`
	Position   int    `json:"position"`
	AheadPRURL string `json:"ahead_pr_url,omitempty"`
	State      string `json:"state"`
}

type mergeTrainSnapshotResponse struct {
	Repos  []mergeTrainRepoQueueView          `json:"repos"`
	ByTodo map[string]mergeTrainTodoBadgeView `json:"by_todo"`
}

func mergeTrainItemView(e mergetrain.Entry, pos int) mergeTrainQueueItemView {
	return mergeTrainQueueItemView{
		TodoID:         e.TodoID,
		PRNumber:       e.PRNumber,
		PRURL:          e.PRURL,
		State:          string(e.State),
		Policy:         string(e.Policy),
		Approved:       e.Approved,
		Position:       pos,
		RebaseFailures: e.RebaseFailures,
		LastError:      e.LastError,
	}
}

func buildMergeTrainSnapshotResponse(snap mergetrain.Snapshot, hidden map[string]bool) mergeTrainSnapshotResponse {
	resp := mergeTrainSnapshotResponse{
		Repos:  make([]mergeTrainRepoQueueView, 0, len(snap.Repos)),
		ByTodo: make(map[string]mergeTrainTodoBadgeView),
	}
	for _, rs := range snap.Repos {
		rv := mergeTrainRepoQueueView{
			RepoKey: rs.RepoKey,
			Queue:   make([]mergeTrainQueueItemView, 0, len(rs.Queue)),
		}
		if rs.Head != nil && !hidden[rs.Head.TodoID] {
			item := mergeTrainItemView(*rs.Head, 1)
			rv.Head = &item
			resp.ByTodo[rs.Head.TodoID] = mergeTrainTodoBadgeView{
				RepoKey:  rs.RepoKey,
				Position: 1,
				State:    string(rs.Head.State),
			}
		}
		basePos := 1
		if rs.Head != nil {
			basePos = 2
		}
		for i, e := range rs.Queue {
			if hidden[e.TodoID] {
				continue
			}
			pos := basePos + i
			aheadURL := ""
			if i == 0 {
				if rs.Head != nil && !hidden[rs.Head.TodoID] {
					aheadURL = rs.Head.PRURL
				}
			} else if !hidden[rs.Queue[i-1].TodoID] {
				aheadURL = rs.Queue[i-1].PRURL
			}
			rv.Queue = append(rv.Queue, mergeTrainItemView(e, pos))
			resp.ByTodo[e.TodoID] = mergeTrainTodoBadgeView{
				RepoKey:    rs.RepoKey,
				Position:   pos,
				AheadPRURL: aheadURL,
				State:      string(e.State),
			}
		}
		for _, e := range rs.Escalated {
			if hidden[e.TodoID] {
				continue
			}
			rv.Escalated = append(rv.Escalated, mergeTrainItemView(e, 0))
			resp.ByTodo[e.TodoID] = mergeTrainTodoBadgeView{
				RepoKey:  rs.RepoKey,
				Position: 0,
				State:    string(e.State),
			}
		}
		if rv.Head != nil || len(rv.Queue) > 0 || len(rv.Escalated) > 0 {
			resp.Repos = append(resp.Repos, rv)
		}
	}
	return resp
}

// todoMergeTrainRoutes registers read-only merge-train queue visibility endpoints strictly gated under todos_v2=true (#16432).
func (s *Service) todoMergeTrainRoutes() {
	s.handle("GET /api/v1/todos/mergetrain", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if !s.settings(ctx).todosV2On() {
			apierr.Write(w, http.StatusConflict, errors.New("merge train queue requires todos_v2 to be enabled"))
			return
		}
		snap := s.mergeTrainSnapshot(ctx)
		var hidden map[string]bool
		if r.Header.Get(RoleHeader) == roleExtension {
			if err := s.ensureTodosLoaded(ctx); err != nil {
				apierr.Write(w, http.StatusServiceUnavailable, errTodosUnavailable)
				return
			}
			var ids []string
			for _, rs := range snap.Repos {
				if rs.Head != nil {
					ids = append(ids, rs.Head.TodoID)
				}
				for _, e := range rs.Queue {
					ids = append(ids, e.TodoID)
				}
				for _, e := range rs.Escalated {
					ids = append(ids, e.TodoID)
				}
			}
			if len(ids) > 0 {
				hidden = s.todosHidden(ctx, ids, nil)
			}
		}
		apierr.WriteJSON(w, buildMergeTrainSnapshotResponse(snap, hidden))
	})
}
