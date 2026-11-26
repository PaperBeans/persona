package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// promoteSupervision is monotone within a wake-up: a later silent finish cannot hide a user action.
func (s *Service) promoteSupervision(self string) error {
	ctx := context.Background()
	key := s.supervisionHoldKey(ctx, self)
	b := &s.supervise
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := s.loadSupervisionHoldsLocked(ctx); err != nil {
		return err
	}
	return s.promoteSupervisionLocked(ctx, key)
}

func (s *Service) promoteSupervisionLocked(ctx context.Context, key string) error {
	b := &s.supervise
	now := time.Now()
	if hold := b.holds[key]; hold.active(now) {
		if !hold.Visible {
			hold.Visible = true
			b.holds[key] = hold
			b.holdsDirty = true
		}
		if err := s.saveSupervisionHoldsLocked(ctx); err != nil {
			return err
		}
		return s.markStalledHandledLocked(ctx, hold.Stalled, now)
	}
	return s.saveSupervisionHoldsLocked(ctx)
}

// quietInternalAcceptedReview uses only the current plan binding and supervision
// ledger. Unknown provenance fails open as user-visible; titles never participate.
func (s *Service) quietInternalAcceptedReview(ctx context.Context, todoID string, userCheck bool) bool {
	if userCheck {
		return false
	}
	t, ok := s.todoNow(todoID)
	if !ok || t.Supervision == nil {
		return false
	}
	plan, index, err := s.currentPlanAssignment(ctx, t)
	if err != nil || plan == nil || index < 0 || index >= len(plan.Tasks) {
		return false
	}
	task := plan.Tasks[index]
	if task.UserRequested {
		return false
	}
	if task.Parent != "" || t.Supervision.Round > 0 {
		return true
	}
	review := t.Supervision.AutoReview
	return review != nil && review.Round == t.Supervision.Round && (review.State == autoClean || review.State == autoBlocking)
}

// toolSupervisionFinish records the caller's disposition in its persisted MCP result.
// The transcript remains intact; clients fold the entire triage turn, not just its input.
func (s *Service) toolSupervisionFinish(ctx context.Context, self string, args json.RawMessage) (string, error) {
	var a struct {
		Visibility string `json:"visibility"`
		Summary    string `json:"summary"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	a.Summary = strings.TrimSpace(a.Summary)
	if (a.Visibility != "silent" && a.Visibility != "user") || a.Summary == "" || utf8.RuneCountInString(a.Summary) > 1000 {
		return "", errors.New("visibility must be silent or user, with a summary of 1–1000 characters")
	}
	key := s.supervisionHoldKey(ctx, self)
	b := &s.supervise
	b.mu.Lock()
	if err := s.loadSupervisionHoldsLocked(ctx); err != nil {
		b.mu.Unlock()
		return "", err
	}
	hold := b.holds[key]
	if !hold.active(time.Now()) || a.Visibility == "silent" && hold.Visible {
		b.mu.Unlock()
		return "", errors.New("this is not an unpromoted supervision turn; normal user messages cannot be hidden")
	}
	level := "triage"
	if a.Visibility == "user" {
		level = "user"
		if err := s.promoteSupervisionLocked(ctx, key); err != nil {
			b.mu.Unlock()
			return "", err
		}
	} else if err := s.markStalledHandledLocked(ctx, hold.Stalled, time.Now()); err != nil {
		b.mu.Unlock()
		return "", err
	}
	b.mu.Unlock()
	body, err := json.Marshal(map[string]string{"visibility": a.Visibility, "summary": a.Summary, "level": level})
	return string(body), err
}
