package assistant

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// nonOwnerCardTarget resolves who handles a non-six-class card instead of pushing it to the owner:
// the task's supervisor when the session belongs to an open supervised to-do, otherwise the main
// conversation (or the background supervisor when the main conversation itself raised the card).
// Returns ("", false) when neither a supervisor nor a main conversation is configured.
func (s *Service) nonOwnerCardTarget(ctx context.Context, machine, sessionID string) (string, bool) {
	cfg := s.settings(ctx)
	if machine != "" && sessionID != "" {
		canon := machine
		if s.devs != nil {
			canon = s.devs.Canonical(machine)
		}
		if t, ok := s.todoOfSession(ctx, canon, sessionID); ok && t.Supervision != nil {
			switch t.Supervision.state(t) {
			case reviewAccepted, reviewCancelled:
			default:
				if sup := s.effectiveSupervisor(ctx, cfg, t); sup != "" && sup != sessionID {
					return sup, true
				}
			}
		}
	}
	if cfg.Main == "" {
		return "", false
	}
	if sessionID == cfg.Main && cfg.BackgroundSupervisor != "" && cfg.BackgroundSupervisor != cfg.Main {
		return cfg.BackgroundSupervisor, true
	}
	return cfg.Main, true
}

// scheduleFailureTarget resolves who handles a failed scheduled run instead of pushing it to the
// owner: the supervisor of the task's originating session (if any), the originating leader/main
// conversation itself, or the main conversation. Returns ("", false) when none is configured.
func (s *Service) scheduleFailureTarget(ctx context.Context, t Task) (string, bool) {
	cfg := s.settings(ctx)
	if t.SessionID != "" {
		machine := cmp.Or(t.MachineID, cfg.MachineID)
		if machine != "" {
			canon := machine
			if s.devs != nil {
				canon = s.devs.Canonical(machine)
			}
			if td, ok := s.todoOfSession(ctx, canon, t.SessionID); ok && td.Supervision != nil {
				switch td.Supervision.state(td) {
				case reviewAccepted, reviewCancelled:
				default:
					if sup := s.effectiveSupervisor(ctx, cfg, td); sup != "" {
						return sup, true
					}
				}
			}
		}
		if t.SessionID == cfg.Main || slices.Contains(cfg.Leaders, t.SessionID) {
			return t.SessionID, true
		}
	}
	if cfg.Main != "" {
		return cfg.Main, true
	}
	return "", false
}

// handNonOwnerCard intercepts non-six-class interaction cards (agent_permission, agent_question,
// extension_approval) that were not routed by ask_route or delegable, and hands them to the task's
// supervisor or the main conversation instead of pushing them to the owner.
//
// Cards that genuinely belong to one of the owner's six classes — secrets/forms/elicitations,
// outward writes (mail_send, comments, etc.), approvals matching userApproval, and unparsed or
// user-only terminal dialogs (trust folder, delete risk, etc.) — return false so they still reach
// the owner as before.
func (s *Service) handNonOwnerCard(ix proto.Interaction) bool {
	src := s.inputCardSource(ix, false)
	if src != cardAgentPermission && src != cardAgentQuestion && src != cardExtensionApproval {
		return false
	}
	if secretOrForm(ix) {
		return false
	}
	if ix.Kind == proto.IxPermission && userApproval(ix) != "" {
		return false
	}
	if ix.Kind == proto.IxScreen && (!supervisedScreen(ix) || userScreen(ix) != "") {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	to, ok := s.nonOwnerCardTarget(ctx, ix.MachineID, ix.SessionID)
	if !ok {
		return false
	}
	cfg := s.settings(ctx)
	home := cfg.MachineID
	if s.devs != nil && home != "" {
		home = s.devs.Canonical(home)
	}

	s.fleet.mu.Lock()
	p := s.fleet.interactions[ix.ID]
	if p == nil || p.claimed || p.told || !p.ix.CreatedAt.Equal(ix.CreatedAt) {
		s.fleet.mu.Unlock()
		return false
	}
	now := time.Now()
	// Parked at askRemindMax with empty todo so askTick never emits a todo-less sigAsk or rings
	// the owner on timeout, while routedTo still lets the target or main conversation answer it.
	p.route = &askRoute{
		machine:   home,
		ladder:    []string{to},
		routedAt:  now,
		since:     now,
		held:      now,
		parked:    true,
		reminders: askRemindMax,
	}
	p.ix.Delegating = true
	s.fleet.mu.Unlock()

	shown := ix
	shown.Delegating = true
	s.show(shown)

	var b strings.Builder
	fmt.Fprintf(&b, "Non-owner card (%s) routed to you instead of pushing to the user.\n", src)
	fmt.Fprintf(&b, "interaction_id=%s kind=%s", ix.ID, ix.Kind)
	if ix.MachineID != "" {
		fmt.Fprintf(&b, " machine=%s", ix.MachineID)
	}
	if ix.SessionID != "" {
		fmt.Fprintf(&b, " session_id=%s", ix.SessionID)
	}
	if ix.ToolName != "" {
		fmt.Fprintf(&b, " tool=%s", ix.ToolName)
	}
	if ask := clip(oneLine(noticeAsk(ix)), 300); ask != "" {
		fmt.Fprintf(&b, "\n%s", ask)
	}
	title := cmp.Or(ix.SessionTitle, ix.ToolName, src)
	if _, kept := s.keepRelayLater(to, relayMsg{
		From:      src,
		FromTitle: title,
		Tool:      src,
		Text:      b.String(),
		At:        now.UTC(),
	}); kept {
		s.publish(ctx)
	}
	s.workEvent(ix.MachineID, ix.SessionID, "ask_routed_nonowner", map[string]any{
		"to":     to,
		"source": src,
		"kind":   ix.Kind,
		"id":     ix.ID,
	})
	return true
}
