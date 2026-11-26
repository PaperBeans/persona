package assistant

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	w "github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

func (s *Service) admitExternalPlan(ctx context.Context, store externalInboxStore, now time.Time, ev externalInboxReady) error {
	if ev.plan.Owner != nil {
		if err := s.coalesceExternalOwner(ctx, store, &ev); err != nil {
			return errors.Join(err, store.SaveExternalInboxProgress(ctx, ev.work.Key, now.Add(externalInboxRetry), nil, false))
		}
	}
	if err := store.SaveExternalInboxProgress(ctx, ev.work.Key, now, &ev.plan, false); err != nil {
		return err
	}
	return s.deliverExternalPlan(ctx, store, now, ev)
}

// coalesceExternalOwner chooses the existing owner before reserving any new
// execution. A changed binding cannot send to a now unauthorized owner or
// start another task while the existing one still owns the object.
func (s *Service) coalesceExternalOwner(ctx context.Context, store externalInboxStore, ev *externalInboxReady) error {
	owners, err := store.RecentExternalInboxOwners(ctx, ev.work.Ref, ev.work.Key)
	if err != nil {
		return err
	}
	for _, prior := range owners {
		if !s.externalOwnerActive(ctx, prior) {
			continue
		}
		i := slices.IndexFunc(ev.selection.candidates, func(candidate externalCandidate) bool {
			return candidate.externalTarget == prior.Scope && candidate.owner
		})
		if i < 0 {
			ev.plan.Ask = true
			ev.plan.Owner = nil
			return nil
		}
		coalesced := ev.selection.candidates[i].recipient()
		coalesced.Target = prior.Target
		if prior.AutoTodo && !prior.Delivered {
			_, exists := s.todoNow(string(prior.Target.ID))
			coalesced.AutoTodo = !exists
		}
		if prior.Method == "session" && s.externalSessionStillOwns(ctx, prior) {
			coalesced.Method, coalesced.Machine, coalesced.Session = prior.Method, prior.Machine, prior.Session
		}
		ev.plan.FYI = slices.DeleteFunc(ev.plan.FYI, func(fyi externalDelivery) bool {
			return fyi.Target == coalesced.Target || fyi.Target == coalesced.Scope
		})
		ev.plan.Owner = &coalesced
		return nil
	}
	return nil
}

func (s *Service) externalOwnerActive(ctx context.Context, prior externalDelivery) bool {
	if prior.Target.Kind == w.ExternalTargetTodo {
		t, ok := s.todoNow(string(prior.Target.ID))
		if !ok {
			return prior.AutoTodo && !prior.Delivered
		}
		if !taskOpen(t) {
			return false
		}
		if prior.AutoTodo || prior.Method == "relay" || prior.Target != prior.Scope {
			return true
		}
		if prior.Method == "session" {
			// Missing inventory does not establish that a linked Agent stopped.
			return slices.ContainsFunc(t.Agents, func(a TodoAgent) bool {
				return a.active() && a.StartError == "" && a.SessionID == prior.Session && a.MachineID == prior.Machine
			})
		}
		return prior.Method == "" || slices.ContainsFunc(t.Agents, func(a TodoAgent) bool { return a.active() && a.StartError == "" })
	}
	d, ok := s.externalDirection(ctx, prior.Target)
	return ok && (prior.Method == "" || prior.Method == "relay" || d.Outside != nil)
}

func (s *Service) externalSessionStillOwns(ctx context.Context, prior externalDelivery) bool {
	c, ok := s.session(ctx, prior.Machine, prior.Session)
	return ok && c.Status != proto.StatusOffline && c.ReadOnlyReason != "stopped"
}

func (s *Service) externalDirection(ctx context.Context, target externalTarget) (direction, bool) {
	b := &s.directions
	b.mu.Lock()
	defer b.mu.Unlock()
	if s.directionsLocked(ctx) {
		if d := b.m[string(target.ID)]; d != nil && d.EndedAt == nil {
			return *d, true
		}
	}
	return direction{}, false
}

func (s *Service) deliverExternalPlan(ctx context.Context, store externalInboxStore, now time.Time, ev externalInboxReady) error {
	plan := &ev.plan
	when := time.Time{}
	waitFor := func(at time.Time) {
		if when.IsZero() || at.Before(when) {
			when = at
		}
	}
	save := func() error { return store.SaveExternalInboxProgress(ctx, ev.work.Key, now, plan, false) }
	retry := func(err error) error {
		return errors.Join(err, store.SaveExternalInboxProgress(ctx, ev.work.Key, now.Add(externalInboxRetry), plan, false))
	}
	if owner := plan.Owner; owner != nil && !owner.Delivered {
		at, allowed := ev.selection.deliveryTime(*owner, true)
		switch {
		case !allowed:
			plan.Owner = nil
		case at.After(now):
			waitFor(at)
		default:
			if err := s.deliverExternalOwner(ctx, ev.work, owner, save); err != nil {
				return retry(err)
			}
			owner.Delivered = true
			if err := save(); err != nil {
				return err
			}
		}
	}
	for i := range plan.FYI {
		fyi := &plan.FYI[i]
		if fyi.Delivered {
			continue
		}
		at, allowed := ev.selection.deliveryTime(*fyi, false)
		if !allowed {
			continue
		}
		if at.After(now) {
			waitFor(at)
			continue
		}
		if err := s.recordExternalDelivery(ctx, ev.work, *fyi, false, false); err != nil {
			return retry(err)
		}
		fyi.Delivered = true
	}
	if plan.Ask && !ev.selection.readyAt.IsZero() {
		at := ev.selection.readyAt
		if at.After(now) {
			waitFor(at)
		} else if err := s.offerExternalTodo(ctx, ev.work); err != nil {
			return retry(err)
		} else {
			plan.Ask = false
		}
	}
	return store.SaveExternalInboxProgress(ctx, ev.work.Key, when, plan, when.IsZero())
}

func (selection externalSelection) deliveryTime(d externalDelivery, owner bool) (time.Time, bool) {
	for _, c := range selection.candidates {
		if c.externalTarget != d.Scope || owner && !c.owner {
			continue
		}
		if d.AutoTodo && *c.policy.NewItemPolicy != "auto_todo" {
			break
		}
		return c.at, true
	}
	return time.Time{}, false
}

func (s *Service) deliverExternalOwner(ctx context.Context, work w.ExternalInboxWork, dest *externalDelivery, save func() error) error {
	if dest.AutoTodo {
		if err := s.makeExternalTodo(ctx, work, *dest); err != nil {
			return err
		}
		return s.recordExternalDelivery(ctx, work, *dest, true, false)
	}
	if dest.Method == "" {
		s.resolveExternalRecipient(ctx, dest)
		if err := save(); err != nil {
			return err
		}
	}
	if dest.Method == "feed" {
		return s.recordExternalDelivery(ctx, work, *dest, true, false)
	}
	if dest.Tried {
		// After an ambiguous result or a restart, only reconcile this recipient.
		// The durable reservation must not become a write to a different Agent.
		var transcript struct{ Items []proto.Item }
		err := s.call(ctx, dest.Machine, DeviceSessionItems,
			map[string]any{"session_id": dest.Session, "turns": 10, "lazy_images": true}, &transcript)
		seen := err == nil && slices.ContainsFunc(transcript.Items, func(item proto.Item) bool {
			return item.Role == "user" && (item.ClientMsgID == work.Key ||
				strings.Contains(item.Text, "<persona_inbox_event>"+work.Key+"</persona_inbox_event>"))
		})
		return s.recordExternalDelivery(ctx, work, *dest, true, !seen)
	}
	text := s.renderExternalUpdate(ctx, work.ExternalInboxEvent)
	dest.Tried = true
	if err := save(); err != nil {
		dest.Tried = false
		return err
	}
	if dest.Method == "session" {
		err := s.call(ctx, dest.Machine, DeviceSessionSend, map[string]any{
			"session_id": dest.Session, "text": text, "client_msg_id": work.Key,
		}, nil)
		if err != nil && writeDelivered(err) == "none" {
			// A definite refusal permits a fallback within the same owning task.
			dest.Tried = false
			s.resolveExternalSupervisor(ctx, dest)
			return s.deliverExternalOwner(ctx, work, dest, save)
		}
		return s.recordExternalDelivery(ctx, work, *dest, true, err != nil && writeDelivered(err) != "yes")
	}
	// The existing durable relay can append to a live supervisor or queue
	// the update for it. It does not start a task in another direction.
	if _, err := s.queueRelayDurable(dest.Session, relayMsg{
		From: string(dest.Target.ID), Tool: noticeTool, Text: text,
		At: work.ReceivedAt, OnceID: "external-inbox-" + work.Key,
	}); err != nil {
		// The queue write may have committed before its answer was lost.
		return err
	}
	s.publish(ctx)
	if c, ok := s.session(ctx, dest.Machine, dest.Session); ok {
		s.drainRelays(dest.Machine, []proto.Session{c})
	}
	return s.recordExternalDelivery(ctx, work, *dest, true, false)
}

func (s *Service) resolveExternalRecipient(ctx context.Context, dest *externalDelivery) {
	if dest.Target.Kind == w.ExternalTargetTodo {
		if t, ok := s.todoNow(string(dest.Target.ID)); ok && taskOpen(t) {
			if r := s.resolveLatestBoundAgent(ctx, t); r.found && r.session.Status != proto.StatusOffline && r.session.ReadOnlyReason != "stopped" {
				dest.Method, dest.Machine, dest.Session = "session", r.session.MachineID, r.session.SessionID
				return
			}
		}
	} else if d, ok := s.externalDirection(ctx, dest.Target); ok && d.Outside != nil && d.Outside.SessionID != "" {
		if c, found := s.session(ctx, d.Outside.MachineID, d.Outside.SessionID); found &&
			c.Status != proto.StatusOffline && c.ReadOnlyReason != "stopped" {
			dest.Method, dest.Machine, dest.Session = "session", d.Outside.MachineID, d.Outside.SessionID
			return
		}
	}
	s.resolveExternalSupervisor(ctx, dest)
}

func (s *Service) resolveExternalSupervisor(ctx context.Context, dest *externalDelivery) {
	dest.Method, dest.Machine, dest.Session = "feed", "", ""
	if dest.Target.Kind == w.ExternalTargetTodo {
		if t, ok := s.todoNow(string(dest.Target.ID)); ok && taskOpen(t) {
			dest.Machine, dest.Session = s.resolveFallbackSupervisorConversation(ctx, t)
		}
	} else if d, ok := s.externalDirection(ctx, dest.Target); ok && d.Outside == nil {
		dest.Machine, dest.Session = d.MachineID, string(dest.Target.ID)
		if dest.Machine == "" {
			dest.Machine = s.settings(ctx).MachineID
		}
	}
	if dest.Machine != "" && dest.Session != "" {
		dest.Method = "relay"
	}
}

func (s *Service) recordExternalDelivery(ctx context.Context, work w.ExternalInboxWork, dest externalDelivery, owner, uncertain bool) error {
	id := string(dest.Target.ID)
	conversation := id
	if dest.Target.Kind == w.ExternalTargetTodo {
		t, _ := s.todoNow(id)
		conversation = t.Origin
		if conversation == "" || s.isLeaderRef(conversation) {
			conversation = s.settings(ctx).Main
		}
	}
	text := externalUpdateText(work.ExternalInboxEvent)
	if work.URL != "" {
		text += "\n" + work.URL
	}
	fields := map[string]string{"text": text, "from": routedFromAssistant, "inbox_item": string(work.InboxID)}
	if owner && uncertain {
		fields["text"] = i18n.T(s.lang(ctx), "assistant-tasks.route_in_doubt") + "\n\n" + text
	} else if dest.Method == "session" {
		fields["machine_id"], fields["session_id"] = dest.Machine, dest.Session
	}
	key := namedClientID("external inbox activity\n" + work.Key + "\n" + string(dest.Target.Kind) + "\n" + id)
	_, err := s.appendFeedOnce(ctx, key, conversation, feedRouted, id, fields)
	return err
}

func externalTodoTitle(work w.ExternalInboxWork) string {
	if work.Title != "" {
		return clipRunesTo(work.Title, inboxTodoTitleMax)
	}
	return clipRunesTo(work.Summary, inboxTodoTitleMax)
}

func (s *Service) makeExternalTodo(ctx context.Context, work w.ExternalInboxWork, dest externalDelivery) error {
	id := string(dest.Target.ID)
	if st := s.todoDB(); st != nil {
		if reader, ok := st.(interface {
			GetTodo(context.Context, string) (*db.TodoRow, error)
		}); ok {
			row, err := reader.GetTodo(ctx, id)
			switch {
			case err == nil:
				_, _ = s.applyTodoRow(ctx, id, *row, nil)
				return nil
			case !errors.Is(err, db.ErrTodoNotFound):
				return err
			}
		}
	}
	if _, ok := s.todoNow(id); ok {
		return nil
	}
	d, ok := s.externalDirection(ctx, dest.Scope)
	if !ok {
		return errors.New("the direction for an external to-do is unavailable")
	}
	if title := externalTodoTitle(work); title != "" {
		origin := string(dest.Scope.ID)
		if d.Outside != nil {
			origin = ""
		}
		_, err := s.createTaskTodoAs(ctx, id, title, externalUpdateText(work.ExternalInboxEvent), origin,
			"", d.Project, string(work.InboxID), "", string(dest.Scope.ID))
		return err
	}
	return nil
}

func (s *Service) offerExternalTodo(ctx context.Context, work w.ExternalInboxWork) error {
	b := &s.inbox
	b.mu.Lock()
	it := b.items[string(work.InboxID)]
	if it == nil || !it.open() || it.ExternalAsked == work.Key || externalTodoTitle(work) == "" {
		b.mu.Unlock()
		return nil
	}
	if currentSuggestion(it) != nil {
		b.mu.Unlock()
		return errors.New("external to-do confirmation is waiting for the current Inbox suggestion")
	}
	w := beginWrite(it)
	it.SuggestSeq++
	it.ExternalAsked, it.Tier = work.Key, TierAction
	now := time.Now().UTC()
	it.Suggestion = &InboxSuggestion{Version: it.SuggestSeq, Kind: SuggestTodo, Text: externalTodoTitle(work),
		At: now, Note: work.Summary, SourceOf: work.Key}
	b.chainAdd(it, InboxChainEntry{Kind: ChainHandling, At: now,
		Text: i18n.T(b.userLang(), "assistant-inbox_chain.suggested_todo"), Detail: it.Suggestion.Text})
	b.settle(w)
	b.mu.Unlock()
	if s.saveWrites(ctx, []*inboxWrite{w}, "the external to-do confirmation")[it.ID] {
		return errInboxNotPersisted
	}
	return nil
}

func (s *Service) foldExternalChatter(ctx context.Context, work w.ExternalInboxWork) error {
	b := &s.inbox
	b.mu.Lock()
	it := b.items[string(work.InboxID)]
	if it == nil || !it.open() || it.ExternalRouteOf != work.Cursor || it.Last != work.Cursor ||
		it.handled() || currentSuggestion(it) != nil {
		b.mu.Unlock()
		return nil
	}
	w := beginWrite(it)
	it.Tier = TierNoise
	b.noteTriaged(it)
	b.settle(w)
	b.mu.Unlock()
	if s.saveWrites(ctx, []*inboxWrite{w}, "the external Inbox classification")[it.ID] {
		return errInboxNotPersisted
	}
	return nil
}

func (s *Service) renderExternalUpdate(ctx context.Context, work w.ExternalInboxEvent) string {
	return prompts.RenderExternalInboxUpdate(prompts.ExternalInboxUpdate{Language: answerLanguage(s.hello(ctx).Locale),
		Event: work.Key, Summary: externalUpdateText(work), URL: work.URL})
}
