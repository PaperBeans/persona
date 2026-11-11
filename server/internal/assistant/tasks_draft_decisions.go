package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
)

var (
	errDraftNotFound  = errors.New("task reply card not found")
	errDraftStale     = i18n.ErrEnglish("assistant-tasks.draft_changed")
	errDraftDismissed = i18n.ErrEnglish("assistant-tasks.draft_dismissed")
)

// cardDismissalID belongs to this card, never to every future draft for its inbox ref.
func cardDismissalID(it FeedItem) string {
	var b struct {
		ID string `json:"dismissal_id"`
	}
	_ = json.Unmarshal(it.Body, &b)
	return b.ID
}

func cardChangedAt(it FeedItem) *time.Time {
	var b struct {
		At *time.Time `json:"dismissal_updated_at"`
	}
	_ = json.Unmarshal(it.Body, &b)
	return b.At
}

func (s *Service) draftDecisionRoutes() {
	for _, undo := range []bool{false, true} {
		action := "dismiss"
		if undo {
			action = "undo-dismiss"
		}
		s.handle("POST /api/v1/assistant/feed/{id}/"+action, func(w http.ResponseWriter, r *http.Request) {
			if !fullRole(r) {
				apierr.Write(w, http.StatusForbidden, errors.New("read-only role cannot modify drafts"))
				return
			}
			var in struct {
				Draft       *string `json:"draft"`
				DismissalID string  `json:"dismissal_id"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
				apierr.Write(w, http.StatusBadRequest, err)
				return
			}
			if !undo && in.Draft == nil || undo && in.DismissalID == "" {
				apierr.Write(w, http.StatusBadRequest, errors.New("draft or dismissal_id is required"))
				return
			}
			if undo {
				in.Draft = nil
			} else {
				in.DismissalID = ""
			}
			item, err := s.decideDraft(r.Context(), r.PathValue("id"), in.Draft, in.DismissalID)
			switch {
			case errors.Is(err, errDraftNotFound), errors.Is(err, db.ErrTodoNotFound):
				apierr.Write(w, http.StatusNotFound, err)
			case errors.Is(err, errDraftStale):
				apierr.WriteBody(w, http.StatusConflict, map[string]any{"code": "task_draft_stale", "error": i18n.T(i18n.OfWriter(w), "assistant-tasks.draft_changed")})
			case err != nil:
				apierr.Write(w, http.StatusServiceUnavailable, err)
			default:
				out := map[string]any{"item": item}
				if !undo {
					out["dismissal_id"] = cardDismissalID(item)
				}
				apierr.WriteJSON(w, out)
			}
		})
	}
}

// decideDraft records only the user's decision on this immutable draft version.
// It neither files the inbox item nor sends, stops, closes or completes its task.
func (s *Service) decideDraft(ctx context.Context, id string, draft *string, undoID string) (FeedItem, error) {
	s.inbox.cardSends.acquire(id) // the same lock as Send: a dismissed card cannot send from a stale page
	defer s.inbox.cardSends.release(id)
	if err := s.inboxReady(ctx); err != nil {
		return FeedItem{}, err
	}
	for range 2 { // another manager may have committed the same decision first
		card, err := s.feedItem(ctx, id)
		if err != nil {
			return FeedItem{}, err
		}
		if card == nil || card.Kind != feedMessage {
			return FeedItem{}, errDraftNotFound
		}
		var b MessageCard
		if err := json.Unmarshal(card.Body, &b); err != nil {
			return FeedItem{}, err
		}
		if b.TodoID == "" {
			return FeedItem{}, errDraftNotFound
		}
		if draft != nil && *draft != b.Draft {
			return FeedItem{}, errDraftStale
		}
		s.inbox.mu.Lock()
		it := s.inbox.items[card.Ref]
		filed := it != nil && !it.open()
		messageVersion := ""
		if it != nil {
			messageVersion = it.Last
		}
		s.inbox.mu.Unlock()
		if cardSentAt(*card) != nil || filed {
			return FeedItem{}, errDraftStale
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal(card.Body, &body); err != nil {
			return FeedItem{}, err
		}
		active := cardDismissalID(*card)
		if undoID == "" && active != "" {
			return *card, nil
		}
		if undoID != "" && active != undoID {
			return FeedItem{}, errDraftStale
		}
		at := decisionNow()
		todo, ok, err := s.todo(ctx, b.TodoID)
		if err != nil {
			return FeedItem{}, err
		}
		if !ok {
			return FeedItem{}, db.ErrTodoNotFound
		}
		// The immutable card's generation, not current Agent activity, is what
		// the user is declining. Only legacy cards need a baseline established.
		if undoID == "" && b.ProgressVersion == "" {
			body["progress_version"], _ = json.Marshal(s.draftProgressVersion(ctx, todo))
		}
		if undoID == "" && b.MessageVersion == "" && messageVersion != "" {
			body["message_version"], _ = json.Marshal(messageVersion)
		}
		decision := db.TodoDecisionRow{ID: "dec_" + randomHex(8), TodoID: b.TodoID, Source: db.DecisionCard,
			SourceSession: card.Conversation, SourceMessage: "draft:" + id + ":" + randomHex(8), CreatedAt: at,
			Question: i18n.T(s.lang(ctx), "assistant-tasks.draft_decision_question"), Reason: decisionText(b.Draft)}
		if undoID == "" {
			body["dismissal_id"], _ = json.Marshal(decision.ID)
			body["dismissed_at"], _ = json.Marshal(at)
			decision.Choice = i18n.T(s.lang(ctx), "assistant-tasks.draft_decision_ignore")
		} else {
			delete(body, "dismissal_id")
			delete(body, "dismissed_at")
			decision.SupersedesID = undoID
			decision.Choice = i18n.T(s.lang(ctx), "assistant-tasks.draft_decision_undo")
			// Postgres compares decision timestamps at microsecond precision.
			if changed := cardChangedAt(*card); changed != nil && !at.After(*changed) {
				at = changed.Add(time.Microsecond)
				decision.CreatedAt = at
			}
		}
		body["dismissal_updated_at"], _ = json.Marshal(at)
		raw, _ := json.Marshal(body)
		changed, err := s.applyDraftDecision(ctx, *card, raw, decision, at)
		if err != nil {
			return FeedItem{}, err
		}
		if !changed {
			continue
		}
		card.Body = raw
		s.event(ctx, map[string]any{"type": "assistant_feed", "conversation": card.Conversation, "item": *card})
		s.decisionsChanged(ctx, b.TodoID, at)
		return *card, nil
	}
	return FeedItem{}, errDraftStale
}

// feedDecisionStore is optional for older test stores; a configured store lacking
// atomic audit support fails closed rather than acknowledging an unrecorded choice.
type feedDecisionStore interface {
	ApplyFeedDecision(context.Context, db.FeedRow, json.RawMessage, db.TodoDecisionRow, time.Time) (bool, error)
}

func (s *Service) applyDraftDecision(ctx context.Context, before FeedItem, body json.RawMessage, decision db.TodoDecisionRow, at time.Time) (bool, error) {
	if st := s.store(); st != nil {
		ds, ok := st.(feedDecisionStore)
		if !ok {
			return false, errNoDecisionStore
		}
		return ds.ApplyFeedDecision(ctx, db.FeedRow{ID: before.ID, Conversation: before.Conversation, At: before.At,
			Kind: before.Kind, Ref: before.Ref, Body: before.Body}, body, decision, at)
	}
	// Store-less unit tests use the same compare-and-replace rule. Only Undo
	// requires the latest card; dismissing an older card cannot change a new one.
	s.tasks.mu.Lock()
	defer s.tasks.mu.Unlock()
	if decision.SupersedesID != "" {
		for _, it := range s.tasks.feed[before.Conversation] {
			if it.Kind == feedMessage && it.Ref == before.Ref && (it.At.After(before.At) || it.At.Equal(before.At) && it.ID > before.ID) {
				return false, nil
			}
		}
	}
	for i, it := range s.tasks.feed[before.Conversation] {
		if it.ID == before.ID && string(it.Body) == string(before.Body) {
			it.Body = body
			s.tasks.feed[before.Conversation][i] = it
			return true, nil
		}
	}
	return false, nil
}
