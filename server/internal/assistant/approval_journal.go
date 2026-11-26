package assistant

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

const (
	approvalJournalKey       = "assistant.approval_clarifications"
	invalidationSummaryRunes = 200
	invalidationLabelRunes   = 80
)

var errApprovalConversationGone = errors.New("the originating conversation no longer exists")

// approvalJournalEntry contains delivery state and a minimal safe summary only.
// In particular it has no executable callback, raw tool arguments, secret text,
// Resolution.Values or approval fields.
type approvalJournalEntry struct {
	ID        string `json:"id"`
	MachineID string `json:"machine_id"`
	SessionID string `json:"session_id"`
	Kind      string `json:"kind,omitempty"`
	Summary   string `json:"summary,omitempty"`
	Status    string `json:"status,omitempty"`
	Result    string `json:"result,omitempty"`
	Private   bool   `json:"private,omitempty"`
	// Continuation marks a detached command result whose retry instruction is
	// valid only while its one-time grant remains unused.
	Continuation bool      `json:"continuation,omitempty"`
	Accepted     bool      `json:"accepted,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// safeInteractionSummary extracts only the minimum safe human-readable context
// needed for a terminal or invalidation notice. It never reads ToolInput,
// EditableText, Screen, full Plan text, or secret prompt/field content.
func safeInteractionSummary(ix proto.Interaction) string {
	if ix.Secret || hasSecretField(ix) {
		return clip(oneLine(cmp.Or(ix.ToolTitle, ix.ToolName, "secret confirmation")), invalidationLabelRunes)
	}
	if ix.Kind == proto.IxQuestion && len(ix.Questions) > 0 {
		if line := strings.TrimSpace(questionLine(ix)); line != "" {
			return clip(oneLine(line), invalidationSummaryRunes)
		}
	}
	if summary := strings.TrimSpace(ix.Summary); summary != "" {
		return clip(oneLine(summary), invalidationSummaryRunes)
	}
	if ix.Kind == proto.IxPlan && strings.TrimSpace(ix.Plan) != "" {
		if title := strings.TrimSpace(planTitle(ix.Plan)); title != "" {
			return clip(oneLine(title), invalidationSummaryRunes)
		}
	}
	if ix.Kind == proto.IxElicitation && ix.Elicitation != nil && strings.TrimSpace(ix.Elicitation.Message) != "" {
		return clip(oneLine(ix.Elicitation.Message), invalidationSummaryRunes)
	}
	return clip(oneLine(cmp.Or(ix.ToolTitle, ix.ToolName)), invalidationLabelRunes)
}

func (s *Service) loadApprovalJournalLocked(ctx context.Context) bool {
	b := &s.approvals
	if b.loaded {
		return true
	}
	var list []approvalJournalEntry
	if st := s.store(); st != nil {
		if _, err := st.Setting(ctx, approvalJournalKey, &list); err != nil {
			s.logf("load approval clarification journal: %v", err)
			return false
		}
	}
	b.list, b.loaded = list, true
	return true
}

func (s *Service) saveApprovalJournalLocked(ctx context.Context) error {
	if st := s.store(); st != nil {
		list := s.approvals.list
		if list == nil {
			list = []approvalJournalEntry{}
		}
		return st.PutSetting(ctx, approvalJournalKey, list)
	}
	return nil
}

func (s *Service) approvalJournalStarted(ctx context.Context, ix proto.Interaction) error {
	b := &s.approvals
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.loadApprovalJournalLocked(ctx) {
		return errors.New("the clarification journal is unavailable")
	}
	summary := safeInteractionSummary(ix)
	if i := slices.IndexFunc(b.list, func(a approvalJournalEntry) bool { return a.ID == ix.ID }); i >= 0 {
		if b.list[i].Summary == "" && summary != "" {
			b.list[i].Summary = summary
			if err := s.saveApprovalJournalLocked(ctx); err != nil {
				b.list[i].Summary = ""
				return fmt.Errorf("keep the clarification result journal: %w", err)
			}
		}
		return nil
	}
	b.list = append(b.list, approvalJournalEntry{ID: ix.ID, MachineID: ix.MachineID, SessionID: ix.SessionID, Kind: ix.Kind, Summary: summary, Private: ix.Private, UpdatedAt: time.Now().UTC()})
	if err := s.saveApprovalJournalLocked(ctx); err != nil {
		b.list = slices.DeleteFunc(b.list, func(a approvalJournalEntry) bool { return a.ID == ix.ID })
		return fmt.Errorf("keep the clarification result journal: %w", err)
	}
	return nil
}

// approvalJournalApplying is persisted immediately before the retained
// operation executes. A restart in this state has an uncertain outcome, never a
// fabricated cancellation or success.
func (s *Service) approvalJournalApplying(ctx context.Context, ix proto.Interaction) error {
	b := &s.approvals
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.loadApprovalJournalLocked(ctx) {
		return errors.New("the clarification journal is unavailable")
	}
	summary := safeInteractionSummary(ix)
	i := slices.IndexFunc(b.list, func(a approvalJournalEntry) bool { return a.ID == ix.ID })
	if i < 0 {
		b.list = append(b.list, approvalJournalEntry{ID: ix.ID, MachineID: ix.MachineID, SessionID: ix.SessionID, Kind: ix.Kind, Summary: summary, Private: ix.Private})
		i = len(b.list) - 1
	} else {
		if ix.Private {
			b.list[i].Private = true
		}
		if b.list[i].Summary == "" && summary != "" {
			b.list[i].Summary = summary
		}
		if b.list[i].Kind == "" && ix.Kind != "" {
			b.list[i].Kind = ix.Kind
		}
	}
	if b.list[i].Result != "" {
		return errors.New("the approval already has a terminal result")
	}
	b.list[i].Status = "applying"
	b.list[i].UpdatedAt = time.Now().UTC()
	if err := s.saveApprovalJournalLocked(ctx); err != nil {
		return fmt.Errorf("save approval execution state: %w", err)
	}
	return nil
}

func (s *Service) approvalJournalAborted(ctx context.Context, id string) {
	b := &s.approvals
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.loadApprovalJournalLocked(ctx) {
		return
	}
	b.list = slices.DeleteFunc(b.list, func(a approvalJournalEntry) bool { return a.ID == id && a.Result == "" && !a.Accepted })
	if err := s.saveApprovalJournalLocked(ctx); err != nil {
		s.logf("abort approval clarification journal: %v", err)
	}
}

// approvalResultText always names the literal interaction ID and says that the
// original card is no longer waiting, including when execution is uncertain.
func approvalResultText(id, status, text string) string {
	return approvalResultTextWithSummary(id, status, "", text)
}

func approvalResultTextWithSummary(id, status, summary, text string) string {
	word := map[string]string{
		"resolved": "applied", "approved": "approved", "covered": "covered by a newer Always allow", "denied": "denied", "expired": "expired",
		"replaced": "replaced", "stale": "stale and cancelled", "cancelled": "cancelled", "dismissed": "dismissed",
		"failed": "left with an uncertain execution outcome",
	}[status]
	if word == "" {
		word = "finished"
	}
	if summary != "" {
		return fmt.Sprintf("Approval %s is %s and is no longer pending. Original request: %s. %s", id, word, summary, text)
	}
	return fmt.Sprintf("Approval %s is %s and is no longer pending. %s", id, word, text)
}

func questionInvalidationResultText(id, summary string) string {
	if summary != "" {
		return fmt.Sprintf("Question %s was cancelled by a restart or upgrade and is no longer pending. Original question: %s. Nothing was applied. Create a fresh ask_user card if you still need the user's answer.", id, summary)
	}
	return fmt.Sprintf("Question %s was cancelled by a restart or upgrade and is no longer pending. Nothing was applied. Create a fresh ask_user card if you still need the user's answer.", id)
}

func (s *Service) approvalJournalFinished(ctx context.Context, ix proto.Interaction, status, text string) {
	b := &s.approvals
	b.mu.Lock()
	if !s.loadApprovalJournalLocked(ctx) {
		b.mu.Unlock()
		return
	}
	summary := safeInteractionSummary(ix)
	i := slices.IndexFunc(b.list, func(a approvalJournalEntry) bool { return a.ID == ix.ID })
	if i < 0 {
		b.list = append(b.list, approvalJournalEntry{ID: ix.ID, MachineID: ix.MachineID, SessionID: ix.SessionID, Kind: ix.Kind, Summary: summary, Private: ix.Private})
		i = len(b.list) - 1
	} else {
		if ix.Private {
			// Upgrade a legacy or recovered entry before its terminal delivery.
			b.list[i].Private = true
		}
		if b.list[i].Summary == "" && summary != "" {
			b.list[i].Summary = summary
		}
		if b.list[i].Kind == "" && ix.Kind != "" {
			b.list[i].Kind = ix.Kind
		}
	}
	if b.list[i].Result == "" {
		b.list[i].Status = status
		b.list[i].Result = approvalResultTextWithSummary(ix.ID, status, b.list[i].Summary, text)
		b.list[i].Continuation = ix.Detachable && status == "approved"
		b.list[i].UpdatedAt = time.Now().UTC()
	}
	if b.handled == nil {
		b.handled = map[string]bool{}
	}
	b.handled[ix.ID] = true
	if err := s.saveApprovalJournalLocked(ctx); err != nil {
		s.logf("save approval result %s: %v", ix.ID, err)
	}
	b.mu.Unlock()
	go s.deliverApprovalResults()
}

// deliverApprovalResults hands each result to the exact conversation or its
// durable relay queue. There is deliberately no fallback to today's main chat.
// A single drain prevents concurrent accepts; a result added during a drain
// triggers one more pass. Transient failures wait for the retry timer; a result
// handed to the conversation or durable queue is removed. A result is
// permanently undeliverable only when the deletion ledger names its original
// conversation; an uncertain execution outcome stays durable even then.
func (s *Service) deliverApprovalResults() {
	b := &s.approvals
	b.mu.Lock()
	if b.delivering {
		b.again = true
		b.mu.Unlock()
		return
	}
	if !s.loadApprovalJournalLocked(context.Background()) {
		b.mu.Unlock()
		return
	}
	b.delivering = true
	for {
		b.again = false
		var due []approvalJournalEntry
		kept := b.list[:0]
		cleaned := false
		for _, a := range b.list {
			if a.Accepted {
				// Versions before result cleanup retained handed-off entries forever.
				cleaned = true
				continue
			}
			kept = append(kept, a)
			if a.Result != "" {
				due = append(due, a)
			}
		}
		b.list = kept
		if cleaned {
			if err := s.saveApprovalJournalLocked(context.Background()); err != nil {
				s.logf("clean handed-off approval results: %v", err)
			}
		}
		b.mu.Unlock()
		failed := false
		for _, a := range due {
			err := s.deliverApprovalResult(a)
			gone := errors.Is(err, errApprovalConversationGone)
			if err != nil && !gone {
				failed = true
				s.logf("deliver approval result %s: %v", a.ID, err)
				continue
			}
			if gone && a.Status == "failed" {
				// A deleted destination cannot receive the warning, but its durable
				// record is the last evidence that execution may have happened.
				s.logf("retain uncertain approval result %s for recovery: %v", a.ID, err)
				continue
			}
			if gone {
				s.logf("discard approval result %s: %v", a.ID, err)
			}
			b.mu.Lock()
			before := len(b.list)
			b.list = slices.DeleteFunc(b.list, func(x approvalJournalEntry) bool { return x.ID == a.ID })
			if len(b.list) != before {
				if err := s.saveApprovalJournalLocked(context.Background()); err != nil {
					s.logf("remove handed-off approval result %s: %v", a.ID, err)
				}
			}
			b.mu.Unlock()
		}
		b.mu.Lock()
		if failed {
			s.scheduleApprovalResultRetryLocked()
		}
		if !b.again {
			b.delivering = false
			b.mu.Unlock()
			return
		}
	}
}

// approvalResultRetryDelay is how long a transient delivery failure waits before the next pass
// (approvalBook.retryDelay shortens it for one manager in tests).
const approvalResultRetryDelay = time.Minute

// scheduleApprovalResultRetryLocked arms one retry pass. It belongs to the run that armed it: once that run has ended
// (the manager stopped, its store and bus closed), the pass does nothing and arms no other; the next manager's Run
// delivers what is left from the durable journal (recoverApprovalJournals, #16045). Before Run, with no run context,
// it always retries.
func (s *Service) scheduleApprovalResultRetryLocked() {
	b := &s.approvals
	if b.retry {
		return
	}
	b.retry = true
	run := s.runCtx()
	time.AfterFunc(cmp.Or(b.retryDelay, approvalResultRetryDelay), func() {
		b.mu.Lock()
		b.retry = false
		b.mu.Unlock()
		if run.Err() != nil {
			s.logf("approval results: retry skipped, the manager's run ended; the next run delivers them from the journal")
			return
		}
		s.deliverApprovalResults()
	})
}

func (s *Service) deliverApprovalResult(a approvalJournalEntry) error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if a.Continuation {
		// Serialize with grant consumption through the actual conversation/queue
		// handoff. If another call consumed first, the late result must not tell
		// the model to execute that same call again (especially under Always).
		s.detached.mu.Lock()
		defer s.detached.mu.Unlock()
		pending, err := s.detachedContinuationPendingLocked(ctx, a.ID)
		if err != nil {
			return fmt.Errorf("check detached continuation before delivery: %w", err)
		}
		if !pending {
			a.Result = fmt.Sprintf("Approval %s is no longer pending. Its one-time continuation was already consumed or is no longer available. Do not retry it automatically; check whether the exact call already ran.", a.ID)
		}
	}
	if _, deleted, err := s.deletedConversation(ctx, a.MachineID, a.SessionID); err != nil {
		return fmt.Errorf("check the originating conversation's deletion record: %w", err)
	} else if deleted {
		return errApprovalConversationGone
	}
	var res struct {
		OK bool `json:"ok"`
	}
	if err := s.call(ctx, a.MachineID, DeviceSessionGet, map[string]any{"session_id": a.SessionID}, &res); err != nil {
		return err
	}
	if !res.OK {
		// session.get reads the manager's current cache. A restart, an offline
		// node, or a list that has not arrived yet says nothing about deletion.
		return errors.New("the originating conversation is not currently listed")
	}
	title := "Approval result"
	if a.Kind == proto.IxQuestion {
		title = "Question result"
	}
	msg := relayMsg{From: approvalResultFrom + a.ID, FromTitle: title, Tool: askAnswerTool,
		Text: a.Result, At: a.UpdatedAt, Private: a.Private}
	if msg.At.IsZero() {
		msg.At = time.Now().UTC()
	}
	working := false
	if c, ok := s.session(ctx, a.MachineID, a.SessionID); ok {
		working = c.Status == proto.StatusWorking || c.Starting
	}
	if !working {
		if err := s.tryDirectRelay(ctx, a.MachineID, a.SessionID, &msg); err == nil {
			return nil
		} else if s.refusedDirect(ctx, a.MachineID, a.SessionID, msg, err) {
			return nil // a runtime conversation that cannot take it: the owner was pushed (relayRefused)
		} else if !relayInDoubt(err) && !refusedForGood(err) {
			// A transient send refusal may still be retried through the durable
			// queue; an actually missing conversation was rejected above.
			s.logf("approval result %s direct send: %v; queued", a.ID, err)
		}
	}
	return s.queueApprovalResult(a.MachineID, a.SessionID, msg)
}

// queueApprovalResult deduplicates by the stable once_id before persisting.
func (s *Service) queueApprovalResult(machine, to string, msg relayMsg) error {
	b := &s.relays
	b.mu.Lock()
	if !s.relayLocked(context.Background()) {
		b.mu.Unlock()
		return errors.New("the relay queue is unavailable")
	}
	id := relayMsgID(to, msg)
	for _, old := range b.st.Queue[to] {
		if relayMsgID(to, old) == id {
			b.mu.Unlock()
			return nil
		}
	}
	msg.QueuedAt, msg.Depth = time.Now().UTC(), len(b.st.Queue[to])+1
	b.st.Queue[to] = append(b.st.Queue[to], msg)
	s.saveRelayLocked()
	b.mu.Unlock()
	if c, ok := s.session(context.Background(), machine, to); ok {
		s.drainRelays(machine, []proto.Session{c})
	}
	return nil
}

// lookupInteractionSummary backfills a safe summary from the interaction record
// for entries persisted by an older binary before Summary was recorded.
func (s *Service) lookupInteractionSummary(ctx context.Context, id string) string {
	st, ok := s.store().(interactionRecorder)
	if !ok {
		return ""
	}
	readCtx, cancel := context.WithTimeout(ctx, historyTimeout)
	rec, err := func() (rec *db.InteractionRecord, err error) {
		defer guard(&err)
		return st.InteractionRecord(readCtx, id)
	}()
	cancel()
	if err != nil || rec == nil {
		return ""
	}
	return safeInteractionSummary(rec.Interaction)
}

// shouldNotifyRestartInvalidated reports whether a non-resumable interaction
// cancelled by startup reconciliation belongs to an assistant conversation that
// needs a safe invalidation notice to issue a fresh card. Resumable cards keep
// their existing restart behavior, and coding-agent / one-shot cards are untouched.
func shouldNotifyRestartInvalidated(ix proto.Interaction) bool {
	if ix.Resumable || ix.MachineID == "" || ix.SessionID == "" {
		return false
	}
	return ix.ToolName == "ask_user" || ix.CanClarify
}

// recordRestartInvalidation queues one contextual invalidation notice on the
// originating conversation's durable route for a non-resumable card cancelled
// during startup reconciliation. It never duplicates an entry already handled by
// approvalJournal and never overwrites an interrupted applying outcome.
func (s *Service) recordRestartInvalidation(ctx context.Context, ix proto.Interaction) {
	if !shouldNotifyRestartInvalidated(ix) {
		return
	}
	summary := safeInteractionSummary(ix)
	b := &s.approvals
	b.mu.Lock()
	if !s.loadApprovalJournalLocked(ctx) {
		b.mu.Unlock()
		return
	}
	if b.handled[ix.ID] {
		b.mu.Unlock()
		return
	}
	if i := slices.IndexFunc(b.list, func(a approvalJournalEntry) bool { return a.ID == ix.ID }); i >= 0 {
		// Already tracked in the approval journal (e.g. clarifying or applying):
		// backfill a missing summary for older pre-upgrade entries, and let
		// recoverApprovalJournals decide the exact terminal state.
		if b.list[i].Summary == "" && summary != "" {
			b.list[i].Summary = summary
			if err := s.saveApprovalJournalLocked(ctx); err != nil {
				s.logf("save recovered approval summary %s: %v", ix.ID, err)
			}
		}
		b.mu.Unlock()
		return
	}
	var result string
	if ix.Kind == proto.IxQuestion {
		result = questionInvalidationResultText(ix.ID, summary)
	} else {
		result = approvalResultTextWithSummary(ix.ID, "cancelled", summary,
			"The assistant restarted, so nothing was applied and the retained operation was cancelled without executing. Ask for a fresh approval card if the change is still wanted.")
	}
	b.list = append(b.list, approvalJournalEntry{
		ID:        ix.ID,
		MachineID: ix.MachineID,
		SessionID: ix.SessionID,
		Kind:      ix.Kind,
		Summary:   summary,
		Status:    "cancelled",
		Result:    result,
		Private:   ix.Private,
		UpdatedAt: time.Now().UTC(),
	})
	if b.handled == nil {
		b.handled = map[string]bool{}
	}
	b.handled[ix.ID] = true
	if err := s.saveApprovalJournalLocked(ctx); err != nil {
		s.logf("save restart invalidation %s: %v", ix.ID, err)
	}
	b.mu.Unlock()
	go s.deliverApprovalResults()
}

// recoverApprovalJournals is fail-closed: no process-local operation survives a
// manager restart. An interrupted apply is explicitly uncertain; any other
// unfinished clarified card is cancelled without executing.
func (s *Service) recoverApprovalJournals(ctx context.Context) {
	b := &s.approvals
	b.mu.Lock()
	if !s.loadApprovalJournalLocked(ctx) {
		b.mu.Unlock()
		return
	}
	var needSummary []string
	for _, a := range b.list {
		if a.Result == "" && a.Summary == "" {
			needSummary = append(needSummary, a.ID)
		}
	}
	b.mu.Unlock()

	backfilled := map[string]string{}
	for _, id := range needSummary {
		if summary := s.lookupInteractionSummary(ctx, id); summary != "" {
			backfilled[id] = summary
		}
	}

	b.mu.Lock()
	var unfinished []approvalJournalEntry
	for i := range b.list {
		if b.list[i].Status != "approved" {
			// A continuation grant is valid only with its completed approved
			// result. Revoke interrupted, cancelled and failed outcomes before
			// delivery; a crashed apply must never leave an execution permit.
			if err := s.revokeDetachedGrant(ctx, b.list[i].ID); err != nil {
				s.logf("revoke interrupted approval %s: %v", b.list[i].ID, err)
			}
		}
		if b.handled == nil {
			b.handled = map[string]bool{}
		}
		b.handled[b.list[i].ID] = true
		if b.list[i].Result != "" {
			continue
		}
		if b.list[i].Summary == "" {
			b.list[i].Summary = backfilled[b.list[i].ID]
		}
		if b.list[i].Status == "applying" {
			b.list[i].Status = "failed"
			b.list[i].Result = approvalResultTextWithSummary(b.list[i].ID, "failed", b.list[i].Summary, "The assistant restarted while the approved operation was executing. Its execution outcome is uncertain: check current state before retrying; do not assume it applied or that nothing changed.")
		} else {
			b.list[i].Status = "cancelled"
			b.list[i].Result = approvalResultTextWithSummary(b.list[i].ID, "cancelled", b.list[i].Summary, "The assistant restarted, so nothing was applied and the retained operation was cancelled without executing. Ask for a fresh approval card if the change is still wanted.")
		}
		b.list[i].UpdatedAt = time.Now().UTC()
		unfinished = append(unfinished, b.list[i])
	}
	if len(unfinished) > 0 {
		if err := s.saveApprovalJournalLocked(ctx); err != nil {
			s.logf("save restarted approval outcomes: %v", err)
		}
	}
	b.mu.Unlock()
	for _, a := range unfinished {
		s.fleet.mu.Lock()
		p := s.fleet.interactions[a.ID]
		s.fleet.mu.Unlock()
		if p != nil && p.prepared != nil {
			s.endPrepared(p, "cancelled", "The assistant restarted; nothing was applied unless the result says its outcome is uncertain.")
		}
	}
	s.deliverApprovalResults()
}
