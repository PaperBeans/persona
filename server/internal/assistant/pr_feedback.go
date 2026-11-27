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
	prFeedbackCursorsKey    = "assistant.pr_feedback_cursors"
	prFeedbackReviews       = "reviews"
	prFeedbackIssueComments = "issue_comments"
)

// A cursor remembers every handled ID at its newest timestamp. Feedback from that same second
// can appear on a later poll, regardless of its ID's order.
type prFeedbackCursor struct {
	At    time.Time `json:"at"`
	IDsAt []string  `json:"ids_at,omitempty"`
}

type prFeedbackCursors map[string]map[string]prFeedbackCursor // (todo, normalized PR URL) -> stream -> cursor

type prFeedbackItem struct {
	githubPRFeedbackPost
	at       time.Time
	commitID string // reviews refer to a particular head
}

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

func prFeedbackEventID(stream string, id int64) string {
	if stream == prFeedbackIssueComments {
		return "issue_comment:" + strconv.FormatInt(id, 10)
	}
	return prReviewEventID(id)
}

func pendingPRFeedback(stream string, items []prFeedbackItem, cursor prFeedbackCursor) []prFeedbackItem {
	var pending []prFeedbackItem
	seen := make(map[int64]bool)
	for _, item := range items {
		if item.ID > 0 && !item.at.IsZero() && !seen[item.ID] && !cursor.handled(item.at, prFeedbackEventID(stream, item.ID)) {
			seen[item.ID] = true
			pending = append(pending, item)
		}
	}
	slices.SortFunc(pending, func(a, b prFeedbackItem) int {
		if order := a.at.Compare(b.at); order != 0 {
			return order
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return pending
}

func (s *Service) prFeedbackTarget(todoID, url, head string, checkedAt time.Time) (Todo, bool) {
	t, ok := s.todoNow(todoID)
	if !ok {
		return t, false
	}
	p := s.prFeedbackBinding(t, url)
	return t, p != nil && p.HeadOID == head && p.CheckedAt.Equal(checkedAt)
}

func (s *Service) prFeedbackBinding(t Todo, url string) *TodoPRStatus {
	if !taskOpen(t) || t.PRURL != url || s.stagePR(t.ID) != url {
		return nil
	}
	p := t.PRStatus
	if p == nil || p.URL != url || p.State != "open" || p.HeadOID == "" {
		return nil
	}
	return p
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
	route := func(stream string, items []prFeedbackItem) error {
		cursor, initialized := saved[key][stream]
		pending := pendingPRFeedback(stream, items, cursor)
		if initialized && len(pending) == 0 {
			return nil
		}
		var events []prRoutedEvent
		if initialized {
			var err error
			events, err = feedback.events(ctx, todoID, status, g, stream, pending)
			if err != nil {
				return err // no sends or cursor writes from a partial stream
			}
		}
		if len(pending) > 0 {
			head := status.HeadOID
			if stream == prFeedbackIssueComments {
				head = "" // discussion follows the PR across head changes
			}
			if err := g.checkPRFeedbackSource(ctx, feedback.pullPath, head); err != nil {
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
		// Baseline the first complete list, keeping only its newest timestamp and IDs. An empty saved
		// cursor distinguishes an observed empty stream from one this hub has never read.
		for _, item := range pending {
			cursor.advance(item.at, prFeedbackEventID(stream, item.ID))
		}
		return s.savePRFeedbackCursor(ctx, st, key, stream, cursor)
	}
	reviewErr := route(prFeedbackReviews, feedback.reviews)
	if _, ok := s.prFeedbackTarget(todoID, status.URL, status.HeadOID, status.CheckedAt); !ok {
		return reviewErr
	}
	rows, err := readPRPages[githubPRIssueComment](ctx, g, feedback.commentsPath)
	if err != nil {
		return errors.Join(reviewErr, err)
	}
	comments := make([]prFeedbackItem, 0, len(rows))
	for _, row := range rows {
		// Editing a handled comment does not create a new discussion event.
		comments = append(comments, prFeedbackItem{githubPRFeedbackPost: row.githubPRFeedbackPost, at: row.CreatedAt})
	}
	return errors.Join(reviewErr, route(prFeedbackIssueComments, comments))
}

func (f *prFeedback) events(ctx context.Context, todoID string, status TodoPRStatus, g githubForge, stream string, items []prFeedbackItem) ([]prRoutedEvent, error) {
	var events []prRoutedEvent
	reviews := stream == prFeedbackReviews
	for _, item := range items {
		author := strings.TrimSpace(item.User.Login)
		if reviews && item.commitID != status.HeadOID || author == "" || strings.EqualFold(author, f.author) ||
			isBotReviewAuthor(author, item.User.Type) || !reviewAuthorHasWriteAssociation(item.AuthorAssociation) {
			continue
		}
		if !f.viewerChecked {
			f.viewer = g.prViewer(ctx)
			f.viewerChecked = true
		}
		if strings.EqualFold(author, f.viewer) {
			continue
		}
		body := strings.TrimSpace(item.Body)
		kind, reviewState, anchor := prEventKindComment, "", "#issuecomment-"
		if reviews {
			inline, err := g.prReviewComments(ctx, f.pullPath, item.ID)
			if err != nil {
				return nil, err
			}
			var substantive bool
			body, substantive = combinedPRReview(body, inline)
			if !substantive {
				continue
			}
			kind, reviewState, anchor = prEventKindReview, "COMMENTED", "#pullrequestreview-"
		} else if commentedReviewLooksInformational(body) {
			continue
		}
		events = append(events, prRoutedEvent{
			TodoID: todoID, PRURL: status.URL, HeadSHA: status.HeadOID, Kind: kind,
			EventID: prFeedbackEventID(stream, item.ID), ReviewState: reviewState, Author: author, Body: body,
			EventURL:  cmp.Or(strings.TrimSpace(item.HTMLURL), triageNormalizePRURL(status.URL)+anchor+strconv.FormatInt(item.ID, 10)),
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
