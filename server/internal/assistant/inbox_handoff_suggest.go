package assistant

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// Suggested hand-offs (inbox_suggest kind "handoff", #13562): the Inbox processing conversation may propose
// handing an item to an existing eligible conversation on this manager (main, an active leader, or the linked
// task's conversation) instead of starting a new Agent. Nothing is sent until the user confirms; confirmation
// delivers through the existing Hand to AI path so the item records the hand-off and that conversation can
// write back with inbox_suggest item.

const (
	handoffRoleMain   = "main"
	handoffRoleLeader = "leader"
	handoffRoleTask   = "task"
)

// inboxHandoffTarget is one existing eligible conversation on this manager that kind "handoff" may target.
type inboxHandoffTarget struct {
	MachineID string
	SessionID string
	Role      string // main | leader | task
	Title     string
	Main      bool
}

type handoffCandidate struct {
	machine string
	session string
	role    string
	title   string
	main    bool
}

// handoffCandidates returns the raw (unfiltered) eligible conversation candidates for an item linked to todoID,
// in deterministic priority order: main, active non-outside leaders, then the linked open task's conversation(s).
func (s *Service) handoffCandidates(ctx context.Context, cfg Settings, todoID string) []handoffCandidate {
	var out []handoffCandidate
	add := func(c handoffCandidate) {
		c.machine, c.session = strings.TrimSpace(c.machine), strings.TrimSpace(c.session)
		if c.machine == "" || c.session == "" {
			return
		}
		if c.session == cfg.BackgroundSupervisor {
			return
		}
		out = append(out, c)
	}

	if cfg.Main != "" {
		add(handoffCandidate{
			machine: cfg.mainRouteMachine(),
			session: cfg.Main,
			role:    handoffRoleMain,
			main:    true,
		})
	}

	leaderIDs, leaderNames := s.currentLeaders(ctx, cfg)
	for _, id := range leaderIDs {
		if id == "" || id == cfg.Main || id == cfg.BackgroundSupervisor {
			continue
		}
		d := s.directionOf(id)
		if d != nil && d.Outside != nil {
			continue // outside directions have no Persona conversation to receive write-back handoffs
		}
		machine := cfg.MachineID
		if d != nil && d.MachineID != "" {
			machine = d.MachineID
		}
		add(handoffCandidate{
			machine: machine,
			session: id,
			role:    handoffRoleLeader,
			title:   leaderNames[id],
		})
	}

	if todoID != "" {
		if t, ok := s.todoNow(todoID); ok && t.DoneAt == nil && !t.Cancelled() {
			var taskSessions []string
			if t.Supervision != nil && t.Supervision.Supervisor != "" {
				if _, _, isAgentLeader := parseLeaderRef(t.Supervision.Supervisor); !isAgentLeader {
					taskSessions = append(taskSessions, t.Supervision.Supervisor)
				}
			}
			if t.Origin != "" && !slices.Contains(taskSessions, t.Origin) {
				taskSessions = append(taskSessions, t.Origin)
			}
			for _, sid := range taskSessions {
				machine := cfg.MachineID
				isMain := false
				if sid == cfg.Main {
					machine = cfg.mainRouteMachine()
					isMain = true
				} else if d := s.directionOf(sid); d != nil && d.MachineID != "" {
					machine = d.MachineID
				}
				add(handoffCandidate{
					machine: machine,
					session: sid,
					role:    handoffRoleTask,
					title:   t.Title,
					main:    isMain,
				})
			}
		}
	}
	return out
}

func (s *Service) deviceByMachine(devices []Device, id string) (Device, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Device{}, false
	}
	for _, m := range devices {
		if m.ID == id || s.sameMachine(m.ID, id) {
			return m, true
		}
	}
	for _, m := range devices {
		if strings.EqualFold(m.Name, id) || strings.EqualFold(m.SSHHost, id) {
			return m, true
		}
	}
	return Device{}, false
}

// inboxHandoffTargets returns all currently active, non-archived, non-deleted eligible conversations on this manager
// for an item linked to todoID (excluding selfSid). Call without holding s.inbox.mu.
func (s *Service) inboxHandoffTargets(ctx context.Context, todoID, selfSid string) []inboxHandoffTarget {
	cfg := s.settings(ctx)
	candidates := s.handoffCandidates(ctx, cfg, todoID)
	if len(candidates) == 0 {
		return nil
	}
	archived, archOK := s.archivedSet()
	if !archOK {
		return nil // fail closed when archived state cannot be read
	}
	devices := s.devices(ctx)
	titles, _ := s.conversationTitles()
	byMachine := map[string][]proto.Session{}
	failedMachine := map[string]bool{}
	seen := map[[2]string]bool{}
	var out []inboxHandoffTarget
	for _, cand := range candidates {
		if cand.session == selfSid {
			continue
		}
		if _, isArch := archived[cand.session]; isArch {
			continue
		}
		m, ok := s.deviceByMachine(devices, cand.machine)
		if !ok || s.isWorkMachine(m.ID) {
			continue
		}
		key := [2]string{m.ID, cand.session}
		if seen[key] {
			continue
		}
		if s.conversationAvailable(ctx, m.ID, cand.session) != nil {
			continue
		}
		list, listed := byMachine[m.ID]
		if !listed && !failedMachine[m.ID] {
			var err error
			list, err = s.sessions(ctx, m.ID)
			if err != nil {
				failedMachine[m.ID] = true
				continue
			}
			byMachine[m.ID] = list
		}
		if failedMachine[m.ID] {
			continue
		}
		var sess proto.Session
		found := false
		for _, c := range list {
			if c.SessionID == cand.session {
				sess, found = c, true
				break
			}
		}
		if !found || !s.sessionEligibleForHandoff(ctx, m.ID, sess) {
			continue
		}
		seen[key] = true
		title := strings.TrimSpace(cand.title)
		if cand.role == handoffRoleTask {
			title = cmp.Or(ownTitle(sess), titles[cand.session].title, title)
		} else {
			title = cmp.Or(title, ownTitle(sess), titles[cand.session].title)
		}
		out = append(out, inboxHandoffTarget{
			MachineID: m.ID,
			SessionID: cand.session,
			Role:      cand.role,
			Title:     clip(oneLine(title), 80),
			Main:      cand.main,
		})
	}
	return out
}

func (s *Service) sessionEligibleForHandoff(ctx context.Context, machine string, c proto.Session) bool {
	if !c.Assistant || c.Archived || c.IsDeleted() {
		return false
	}
	if c.Origin == OriginInbox || spaceOf(c) != "" {
		return false
	}
	if s.isGuest(ctx, machine, c.SessionID) || s.isBackground(ctx, c.SessionID) {
		return false
	}
	return true
}

// inboxPromptHandoffTargets formats eligible handoff targets for the Inbox processing prompt.
func (s *Service) inboxPromptHandoffTargets(ctx context.Context, todoID string) []prompts.InboxHandoffTarget {
	targets := s.inboxHandoffTargets(ctx, todoID, "")
	if len(targets) == 0 {
		return nil
	}
	out := make([]prompts.InboxHandoffTarget, 0, len(targets))
	for _, t := range targets {
		out = append(out, prompts.InboxHandoffTarget{
			Machine: t.MachineID,
			Session: t.SessionID,
			Role:    t.Role,
			Title:   t.Title,
		})
	}
	return out
}

// validateHandoffTarget checks that (rawMachine, rawSession) names an active, non-archived, non-deleted eligible
// conversation on this manager (main, active leader, or linked open task's conversation). Refusals return an
// errSuggestion explaining why so the model can choose another target or fall back to reply/agent.
// Call without holding s.inbox.mu.
func (s *Service) validateHandoffTarget(ctx context.Context, todoID, selfSid, rawMachine, rawSession string) (inboxHandoffTarget, error) {
	rawMachine, rawSession = strings.TrimSpace(rawMachine), strings.TrimSpace(rawSession)
	if rawMachine == "" || rawSession == "" {
		return inboxHandoffTarget{}, errSuggestion{"handoff requires both machine and session: choose an eligible conversation listed in the prompt, or fall back to reply/agent"}
	}
	devices := s.devices(ctx)
	m, ok := s.deviceByMachine(devices, rawMachine)
	if !ok {
		return inboxHandoffTarget{}, errSuggestion{fmt.Sprintf("machine %q is not on this manager: choose an eligible conversation on this manager or fall back to reply/agent", rawMachine)}
	}
	if s.isWorkMachine(m.ID) {
		return inboxHandoffTarget{}, errSuggestion{fmt.Sprintf("machine %q is a work machine and cannot receive Inbox handoffs: choose another eligible conversation or fall back to reply/agent", m.ID)}
	}
	if err := s.conversationAvailable(ctx, m.ID, rawSession); err != nil {
		return inboxHandoffTarget{}, errSuggestion{fmt.Sprintf("conversation %s on %s is deleted or unavailable: choose another eligible conversation or fall back to reply/agent", rawSession, m.ID)}
	}
	archived, archOK := s.archivedSet()
	if !archOK {
		return inboxHandoffTarget{}, errSuggestion{"archived conversation state could not be read: try again or fall back to reply/agent"}
	}
	if _, isArch := archived[rawSession]; isArch {
		return inboxHandoffTarget{}, errSuggestion{fmt.Sprintf("conversation %s on %s is archived: choose an active eligible conversation or fall back to reply/agent", rawSession, m.ID)}
	}
	sess, found := s.session(ctx, m.ID, rawSession)
	if !found {
		return inboxHandoffTarget{}, errSuggestion{fmt.Sprintf("conversation %s on %s was not found on this manager: choose an eligible conversation listed in the prompt or fall back to reply/agent", rawSession, m.ID)}
	}
	if sess.IsDeleted() {
		return inboxHandoffTarget{}, errSuggestion{fmt.Sprintf("conversation %s on %s is deleted: choose another eligible conversation or fall back to reply/agent", rawSession, m.ID)}
	}
	if sess.Archived {
		return inboxHandoffTarget{}, errSuggestion{fmt.Sprintf("conversation %s on %s is archived: choose an active eligible conversation or fall back to reply/agent", rawSession, m.ID)}
	}
	if selfSid != "" && rawSession == selfSid {
		return inboxHandoffTarget{}, errSuggestion{"cannot hand off an item to its own Inbox processing conversation: choose main, a leader, or the linked task's conversation, or fall back to reply/agent"}
	}
	if !s.sessionEligibleForHandoff(ctx, m.ID, sess) {
		return inboxHandoffTarget{}, errSuggestion{fmt.Sprintf("conversation %s on %s is not an eligible assistant conversation on this manager: choose main, a leader, or the linked task's conversation, or fall back to reply/agent", rawSession, m.ID)}
	}
	cfg := s.settings(ctx)
	titles, _ := s.conversationTitles()
	for _, cand := range s.handoffCandidates(ctx, cfg, todoID) {
		if cand.session != rawSession {
			continue
		}
		if cm, ok := s.deviceByMachine(devices, cand.machine); !ok || !s.sameMachine(cm.ID, m.ID) {
			continue
		}
		title := strings.TrimSpace(cand.title)
		if cand.role == handoffRoleTask {
			title = cmp.Or(ownTitle(sess), titles[rawSession].title, title)
		} else {
			title = cmp.Or(title, ownTitle(sess), titles[rawSession].title)
		}
		return inboxHandoffTarget{
			MachineID: m.ID,
			SessionID: rawSession,
			Role:      cand.role,
			Title:     clip(oneLine(title), 80),
			Main:      cand.main,
		}, nil
	}
	return inboxHandoffTarget{}, errSuggestion{fmt.Sprintf("conversation %s on %s is not an eligible handoff target for this item (must be the main conversation, an active leader conversation, or the linked task's conversation): choose an eligible target or fall back to reply/agent", rawSession, m.ID)}
}

// confirmHandoff delivers a confirmed kind "handoff" suggestion through the existing Hand to AI path:
// validates the target again, sends the item + note once per op via session.send_once, records the target in
// it.Handoffs and it.Chain so that conversation can write back with inbox_suggest item, and leaves the item open.
func (s *Service) confirmHandoff(ctx context.Context, it *InboxItem, g *InboxSuggestion, text, clientID string) (InboxItem, error) {
	b := &s.inbox
	op := strings.ToLower(strings.TrimSpace(clientID))
	if !validClientID(op) {
		op = namedClientID(fmt.Sprintf("suggestion handoff\n%s\n%d", it.ID, g.Version))
	}
	v, _ := b.handingOff.LoadOrStore(it.ID, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	b.mu.Lock()
	if cur := b.items[it.ID]; cur != it {
		b.mu.Unlock()
		return InboxItem{}, errNoInboxItem
	}
	if handoffOp(it, op) != nil {
		out := it.listed()
		b.mu.Unlock()
		return out, nil
	}
	title := clip(oneLine(it.Title), 80)
	todoID, selfSid := "", ""
	if it.Agent != nil {
		todoID, selfSid = it.Agent.TodoID, it.Agent.SessionID
	}
	b.mu.Unlock()

	target, err := s.validateHandoffTarget(ctx, todoID, selfSid, g.MachineID, g.SessionID)
	if err != nil {
		now := time.Now()
		b.mu.Lock()
		b.failHandling(it, i18n.T(b.userLang(), "assistant-inbox_chain.failed_handoff"), now)
		out := it.listed()
		b.mu.Unlock()
		s.saveInbox(ctx, []*InboxItem{it})
		return out, err
	}

	rendered := prompts.RenderInboxHandoff(prompts.InboxHandoff{
		Language: answerLanguage(s.hello(ctx).Locale),
		ID:       it.ID,
		Title:    title,
		Text:     text,
	})
	to := SessionRef{MachineID: target.MachineID, SessionID: target.SessionID}
	if err := s.sendHandoffTo(ctx, to, rendered, op); err != nil {
		now := time.Now()
		b.mu.Lock()
		b.failHandling(it, i18n.T(b.userLang(), "assistant-inbox_chain.failed_handoff"), now)
		out := it.listed()
		b.mu.Unlock()
		s.saveInbox(ctx, []*InboxItem{it})
		return out, err
	}

	b.mu.Lock()
	b.noteHandoffTarget(it, target, op, time.Now())
	out := it.listed()
	b.mu.Unlock()
	// No rollback: the message is in the target conversation. The record stays in memory and the next save writes it.
	if failed := s.saveInbox(ctx, []*InboxItem{it}); failed[it.ID] {
		s.logf("inbox: %s was handed to %s but could not be saved; the live state stays until the next save", it.ID, short(to.SessionID))
	}
	return out, nil
}
