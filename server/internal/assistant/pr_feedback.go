package assistant

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	prFeedbackCursorsKey = "assistant.pr_feedback_cursors"
	prFeedbackReviews    = "reviews"
)

// A cursor remembers every handled ID at its newest timestamp. Another review submitted in that
// second can appear on a later poll, regardless of its ID's order.
type prFeedbackCursor struct {
	At    time.Time `json:"at"`
	IDsAt []string  `json:"ids_at,omitempty"`
}

type prFeedbackCursors map[string]map[string]prFeedbackCursor // (todo, normalized PR URL) -> stream -> cursor

type prFeedbackBook struct {
	locks   cardLocks // one routing pass per task and PR, without holding a lock across other PRs' sends
	writeMu sync.Mutex
}

func prFeedbackKey(todoID, url string) string {
	raw, _ := json.Marshal([2]string{todoID, triageNormalizePRURL(url)})
	return string(raw)
}

func (c prFeedbackCursor) handled(at time.Time, id string) bool {
	return at.Before(c.At) || at.Equal(c.At) && slices.Contains(c.IDsAt, id)
}

func (c *prFeedbackCursor) advance(at time.Time, id string) {
	if at.After(c.At) {
		c.At, c.IDsAt = at.UTC(), nil
	}
	c.IDsAt = append(c.IDsAt, id)
}

func (s *Service) savePRFeedbackCursor(ctx context.Context, st Store, key, stream string, cursor prFeedbackCursor) error {
	s.prFeedback.writeMu.Lock()
	defer s.prFeedback.writeMu.Unlock()
	var cursors prFeedbackCursors
	if _, err := st.Setting(ctx, prFeedbackCursorsKey, &cursors); err != nil {
		return fmt.Errorf("read feedback cursors: %w", err)
	}
	if cursors == nil {
		cursors = make(prFeedbackCursors)
	}
	if cursors[key] == nil {
		cursors[key] = make(map[string]prFeedbackCursor)
	}
	cursors[key][stream] = cursor
	if err := st.PutSetting(ctx, prFeedbackCursorsKey, cursors); err != nil {
		return fmt.Errorf("save feedback cursor: %w", err)
	}
	return nil
}

func prReviewEventID(id int64) string { return "review:" + strconv.FormatInt(id, 10) }

func (f prFeedback) reviewsAfter(cursor prFeedbackCursor) []githubPRReview {
	var pending []githubPRReview
	seen := make(map[int64]bool)
	for _, r := range f.reviews {
		if !seen[r.ID] && !cursor.handled(r.SubmittedAt, prReviewEventID(r.ID)) {
			seen[r.ID] = true
			pending = append(pending, r)
		}
	}
	slices.SortFunc(pending, func(a, b githubPRReview) int {
		if order := a.SubmittedAt.Compare(b.SubmittedAt); order != 0 {
			return order
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return pending
}

func (s *Service) prFeedbackTarget(todoID, url, head string, checkedAt time.Time) (Todo, bool) {
	t, ok := s.todoNow(todoID)
	return t, ok && taskOpen(t) && !needsUserWakeEligible(t) && t.PRURL == url && s.stagePR(todoID) == url &&
		t.PRStatus != nil && t.PRStatus.URL == url && t.PRStatus.State == "open" && t.PRStatus.HeadOID == head &&
		t.PRStatus.CheckedAt.Equal(checkedAt)
}

func (s *Service) routePRFeedback(ctx context.Context, todoID string, status TodoPRStatus, g githubForge, feedback prFeedback) error {
	key := prFeedbackKey(todoID, status.URL)
	s.prFeedback.locks.acquire(key)
	defer s.prFeedback.locks.release(key)
	if !s.prEventsRouteToAgentOn(ctx) {
		return nil
	}
	if _, ok := s.prFeedbackTarget(todoID, status.URL, status.HeadOID, status.CheckedAt); !ok {
		return nil
	}
	st := s.store()
	if st == nil {
		return errors.New("feedback needs a persistent cursor")
	}
	var saved prFeedbackCursors
	if _, err := st.Setting(ctx, prFeedbackCursorsKey, &saved); err != nil {
		return fmt.Errorf("read feedback cursors: %w", err)
	}
	cursor, initialized := saved[key][prFeedbackReviews]
	pending := feedback.reviewsAfter(cursor)
	if initialized && len(pending) == 0 {
		return nil
	}
	var events []prRoutedEvent
	if initialized {
		var err error
		events, err = feedback.reviewEvents(ctx, todoID, status, g, pending)
		if err != nil {
			return err // no sends or cursor writes from a partial observation
		}
	}
	if len(pending) > 0 {
		if err := g.checkPRFeedbackHead(ctx, feedback.pullPath, status.HeadOID); err != nil {
			return err
		}
	}
	for _, ev := range events {
		t, ok := s.prFeedbackTarget(todoID, status.URL, status.HeadOID, status.CheckedAt)
		if !ok {
			return nil
		}
		if err := s.deliverPRFeedback(ctx, t, ev); err != nil {
			return err
		}
	}
	if _, ok := s.prFeedbackTarget(todoID, status.URL, status.HeadOID, status.CheckedAt); !ok {
		return nil
	}
	// On the first complete observation, baseline the reviews already present without fetching their
	// comments. An empty cursor is still persisted to distinguish an observed empty PR from a new one.
	for _, r := range pending {
		cursor.advance(r.SubmittedAt, prReviewEventID(r.ID))
	}
	return s.savePRFeedbackCursor(ctx, st, key, prFeedbackReviews, cursor)
}

func (f prFeedback) reviewEvents(ctx context.Context, todoID string, status TodoPRStatus, g githubForge, reviews []githubPRReview) ([]prRoutedEvent, error) {
	var events []prRoutedEvent
	viewer, viewerChecked := f.viewer, f.viewerChecked
	for _, r := range reviews {
		author := strings.TrimSpace(r.User.Login)
		if r.CommitID != status.HeadOID || author == "" || strings.EqualFold(author, f.author) ||
			isBotReviewAuthor(author, r.User.Type) || !reviewAuthorHasWriteAssociation(r.AuthorAssociation) {
			continue
		}
		if !viewerChecked {
			viewer = g.prViewer(ctx)
			viewerChecked = true
		}
		if strings.EqualFold(author, viewer) {
			continue
		}
		inline, err := g.prReviewComments(ctx, f.pullPath, r.ID)
		if err != nil {
			return nil, err
		}
		body, substantive := combinedPRReview(r.Body, inline)
		if !substantive {
			continue
		}
		events = append(events, prRoutedEvent{
			TodoID: todoID, PRURL: status.URL, HeadSHA: status.HeadOID, Kind: prEventKindReview,
			EventID: prReviewEventID(r.ID), ReviewState: r.State, Author: author, Body: body,
			EventURL:  cmp.Or(strings.TrimSpace(r.HTMLURL), triageNormalizePRURL(status.URL)+"#pullrequestreview-"+strconv.FormatInt(r.ID, 10)),
			CheckedAt: status.CheckedAt,
		})
	}
	return events, nil
}

func combinedPRReview(body string, inline []githubPRInlineComment) (string, bool) {
	var parts []string
	if body = strings.TrimSpace(body); body != "" {
		parts = append(parts, body)
	}
	substantive := !commentedReviewLooksInformational(body)
	seen := make(map[int64]bool)
	for _, c := range inline {
		body := strings.TrimSpace(c.Body)
		if body == "" || c.ID > 0 && seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		substantive = substantive || !commentedReviewLooksInformational(body)
		location := oneLine(c.Path)
		if line := cmp.Or(c.Line, c.OriginalLine); location != "" && line > 0 {
			location += ":" + strconv.Itoa(line)
		}
		if location != "" {
			body = location + ": " + body
		}
		parts = append(parts, body)
	}
	if body != "" && len(parts) > 1 {
		// Leave room for inline findings in the bounded notification preview.
		parts[0] = clip(parts[0], prEventBodyLimit/2)
	}
	return strings.Join(parts, "\n\n"), substantive
}
