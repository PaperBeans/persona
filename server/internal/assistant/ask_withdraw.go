package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// errWithdrawnByOrigin is what ask returns when the originating conversation explicitly withdrew its own pending
// ask_user card through ask_user_withdraw.
var errWithdrawnByOrigin = errors.New("the question was withdrawn by its originating conversation")

const terminalQuestionsMax = 256

type endedAskQuestion struct {
	machine string
	session string
	status  string
	reason  string
}

func (qs *questions) recordTerminalLocked(ix proto.Interaction, status, reason string) {
	if ix.ID == "" || ix.ToolName != "ask_user" || status == "" || status == "pending" {
		return
	}
	if qs.terminal == nil {
		qs.terminal = map[string]endedAskQuestion{}
	}
	if _, exists := qs.terminal[ix.ID]; !exists {
		qs.order = append(qs.order, ix.ID)
		for len(qs.order) > terminalQuestionsMax {
			evict := qs.order[0]
			qs.order = qs.order[1:]
			delete(qs.terminal, evict)
		}
	}
	qs.terminal[ix.ID] = endedAskQuestion{
		machine: ix.MachineID,
		session: ix.SessionID,
		status:  status,
		reason:  reason,
	}
}

func askUserWithdrawnResult(id, reason string) string {
	if reason != "" {
		return fmt.Sprintf("Question %s was withdrawn by this conversation (reason: %s). No answer was supplied; do not act on any option from it.", id, reason)
	}
	return fmt.Sprintf("Question %s was withdrawn by this conversation. No answer was supplied; do not act on any option from it.", id)
}

func alreadyTerminalWithdrawResult(id, status, reason string) string {
	switch status {
	case "cancelled":
		if reason != "" {
			return fmt.Sprintf("Question %s was already cancelled (reason: %s); nothing changed.", id, reason)
		}
		return fmt.Sprintf("Question %s was already cancelled; nothing changed.", id)
	case "resolved":
		return fmt.Sprintf("Question %s was already answered; nothing changed.", id)
	case "dismissed":
		return fmt.Sprintf("Question %s was already dismissed; nothing changed.", id)
	case "expired":
		return fmt.Sprintf("Question %s already expired; nothing changed.", id)
	default:
		return fmt.Sprintf("Question %s is already finished (%s); nothing changed.", id, status)
	}
}

func withdrawnConfirmResult(ix proto.Interaction, reason string) string {
	summary := safeInteractionSummary(ix)
	var head string
	if summary != "" {
		head = fmt.Sprintf("Withdrew question %s (%s): the card is cancelled and no longer waiting.", ix.ID, summary)
	} else {
		head = fmt.Sprintf("Withdrew question %s: the card is cancelled and no longer waiting.", ix.ID)
	}
	if reason != "" {
		return head + " Reason: " + reason
	}
	return head
}

func validateAskUserOrigin(ix proto.Interaction, machine, self string) error {
	if ix.Kind != proto.IxQuestion || ix.ToolName != "ask_user" {
		if ix.Kind != proto.IxQuestion {
			return fmt.Errorf("interaction %s is a %s, not an ask_user question; only the user can decide it", ix.ID, ix.Kind)
		}
		return fmt.Errorf("interaction %s is not an ask_user question (%s); only an originating conversation's ask_user card can be withdrawn here", ix.ID, orDash(ix.ToolName))
	}
	if ix.SessionID != self || ix.MachineID != machine {
		return fmt.Errorf("interaction %s belongs to another conversation; only the originating conversation can withdraw it", ix.ID)
	}
	return nil
}

// toolAskUserWithdraw lets an originating assistant conversation withdraw its own pending ask_user card with an
// optional short reason. Cross-origin cards and non-ask_user interactions (approvals, plans, screens, other agents'
// questions) are strictly rejected. Repeated withdrawals of an already-terminal card belonging to the same origin are
// idempotent without resurrecting or duplicating the card.
func (s *Service) toolAskUserWithdraw(ctx context.Context, machine, self string, args json.RawMessage) (string, error) {
	var a struct {
		InteractionID string `json:"interaction_id"`
		Reason        string `json:"reason"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(self) == "" {
		return "", errors.New("ask_user_withdraw requires an originating conversation")
	}
	id := strings.TrimSpace(a.InteractionID)
	if id == "" {
		return "", errors.New("interaction_id is required")
	}
	reason := clip(oneLine(a.Reason), 120)

	s.loadAskLater(ctx)

	s.questions.mu.Lock()
	q := s.questions.m[id]
	if q != nil {
		if err := validateAskUserOrigin(q.ix, machine, self); err != nil {
			s.questions.mu.Unlock()
			return "", err
		}
		if q.withdrawn {
			prevReason := q.withdrawnReason
			s.questions.mu.Unlock()
			return alreadyTerminalWithdrawResult(id, "cancelled", prevReason), nil
		}
		if q.userSpoke {
			s.questions.mu.Unlock()
			return alreadyTerminalWithdrawResult(id, "dismissed", ""), nil
		}
		if !q.later && len(q.answer) > 0 {
			s.questions.mu.Unlock()
			return alreadyTerminalWithdrawResult(id, "resolved", ""), nil
		}
		q.withdrawn = true
		q.withdrawnReason = reason
		ix := q.ix
		later := q.later
		s.questions.mu.Unlock()

		cancelRes := proto.Resolution{Cancel: true}
		err := s.resolve(id, cancelRes)
		if err != nil {
			if !errors.Is(err, errNotPending) {
				s.questions.mu.Lock()
				q.withdrawn = false
				q.withdrawnReason = ""
				s.questions.mu.Unlock()
				return "", err
			}
			if ended := s.endedStatus(ctx, id, err); ended != "" && ended != "pending" {
				s.questions.mu.Lock()
				q.withdrawn = false
				q.withdrawnReason = ""
				s.questions.recordTerminalLocked(ix, ended, "")
				s.questions.mu.Unlock()
				return alreadyTerminalWithdrawResult(id, ended, ""), nil
			}
			// Not yet on the fleet queue (or running without a fleet): deliver directly to the question.
			if !later {
				delivered := false
				s.questions.mu.Lock()
				select {
				case q.answer <- cancelRes:
					delivered = true
					s.questions.recordTerminalLocked(ix, "cancelled", reason)
				default:
					q.withdrawn = false
					q.withdrawnReason = ""
				}
				s.questions.mu.Unlock()
				if !delivered {
					return alreadyTerminalWithdrawResult(id, "resolved", ""), nil
				}
				return withdrawnConfirmResult(ix, reason), nil
			}
			s.questions.mu.Lock()
			delete(s.questions.m, id)
			s.questions.recordTerminalLocked(ix, "cancelled", reason)
			s.questions.mu.Unlock()
			s.forgetAskLater(ctx, id)
			s.publish(context.WithoutCancel(ctx))
			s.logf("ask_user %s: withdrawn by its conversation %s", id, short(self))
			return withdrawnConfirmResult(ix, reason), nil
		}

		if !later {
			s.questions.mu.Lock()
			select {
			case q.answer <- cancelRes:
			default:
			}
			s.questions.recordTerminalLocked(ix, "cancelled", reason)
			s.questions.mu.Unlock()
		} else {
			s.questions.mu.Lock()
			delete(s.questions.m, id)
			s.questions.recordTerminalLocked(ix, "cancelled", reason)
			s.questions.mu.Unlock()
			s.forgetAskLater(ctx, id)
			s.publish(context.WithoutCancel(ctx))
		}
		s.logf("ask_user %s: withdrawn by its conversation %s", id, short(self))
		return withdrawnConfirmResult(ix, reason), nil
	}
	term, hasTerm := s.questions.terminal[id]
	s.questions.mu.Unlock()

	if ix, ok := s.pendingIx(id); ok {
		if err := validateAskUserOrigin(ix, machine, self); err != nil {
			return "", err
		}
		if err := s.resolve(id, proto.Resolution{Cancel: true}); err != nil {
			if errors.Is(err, errNotPending) {
				ended := cmpOr(s.endedStatus(ctx, id, err), "cancelled")
				s.questions.mu.Lock()
				s.questions.recordTerminalLocked(ix, ended, "")
				s.questions.mu.Unlock()
				return alreadyTerminalWithdrawResult(id, ended, ""), nil
			}
			return "", err
		}
		s.questions.mu.Lock()
		s.questions.recordTerminalLocked(ix, "cancelled", reason)
		s.questions.mu.Unlock()
		return withdrawnConfirmResult(ix, reason), nil
	}

	if st, ok := s.store().(interactionRecorder); ok {
		readCtx, cancel := context.WithTimeout(ctx, historyTimeout)
		rec, err := func() (rec *db.InteractionRecord, err error) {
			defer guard(&err)
			return st.InteractionRecord(readCtx, id)
		}()
		cancel()
		if err == nil && rec != nil {
			if err := validateAskUserOrigin(rec.Interaction, machine, self); err != nil {
				return "", err
			}
			if rec.Status != "" && rec.Status != "pending" {
				storedReason := ""
				if hasTerm && term.machine == machine && term.session == self {
					storedReason = term.reason
				}
				return alreadyTerminalWithdrawResult(id, rec.Status, storedReason), nil
			}
		}
	}

	if hasTerm {
		if term.session != self || term.machine != machine {
			return "", fmt.Errorf("interaction %s belongs to another conversation; only the originating conversation can withdraw it", id)
		}
		return alreadyTerminalWithdrawResult(id, term.status, term.reason), nil
	}

	return "", fmt.Errorf("no pending ask_user question %s", id)
}
