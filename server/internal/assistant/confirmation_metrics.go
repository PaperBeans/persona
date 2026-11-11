package assistant

import (
	"context"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

type confirmationStore interface {
	MarkConfirmationNotified(context.Context, string, time.Time, time.Time, string) error
	MarkConfirmationAutoAuthorized(context.Context, string, time.Time, time.Time, string, string) error
	ConfirmationCounts(context.Context, time.Time, time.Time) ([]db.ConfirmationCount, error)
}

func (s *Service) recordConfirmationNotification(ix proto.Interaction) {
	st, ok := s.store().(confirmationStore)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), historyTimeout)
	defer cancel()
	todo := ""
	if t, found := s.todoOfSession(ctx, ix.MachineID, ix.SessionID); found {
		todo = t.ID
	}
	if err := st.MarkConfirmationNotified(ctx, ix.ID, ix.CreatedAt, time.Now(), todo); err != nil {
		s.logf("record confirmation notification: %v", err)
	}
}

func (s *Service) recordConfirmationAutoApproval(ix proto.Interaction, todoID, grantID string) {
	st, ok := s.store().(confirmationStore)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), historyTimeout)
	defer cancel()
	if err := st.MarkConfirmationAutoAuthorized(ctx, ix.ID, ix.CreatedAt, time.Now(), todoID, grantID); err != nil {
		s.logf("record automatic confirmation: %v", err)
	}
}

type briefConfirmations struct {
	Unknown        bool                   `json:"unknown,omitempty"`
	Since          time.Time              `json:"since"`
	UserNotified   int                    `json:"user_notified"`
	AutoAuthorized int                    `json:"auto_authorized"`
	Sources        []db.ConfirmationCount `json:"sources"`
}

func (s *Service) briefConfirmationsAt(ctx context.Context, now time.Time, loc *time.Location) briefConfirmations {
	local := now.In(loc)
	c := briefConfirmations{Since: dayStart(local), Sources: []db.ConfirmationCount{}}
	st, ok := s.store().(confirmationStore)
	if !ok {
		c.Unknown = true
		return c
	}
	var err error
	c.Sources, err = st.ConfirmationCounts(ctx, c.Since, now)
	if err != nil {
		s.logf("brief confirmation counts: %v", err)
		c.Unknown = true
		return c
	}
	for _, row := range c.Sources {
		c.UserNotified += row.UserNotified
		c.AutoAuthorized += row.AutoAuthorized
	}
	return c
}

func (c briefConfirmations) summary(l i18n.Lang) string {
	if c.Unknown {
		return i18n.T(l, "assistant-confirmations.unknown")
	}
	return i18n.T(l, "assistant-confirmations.summary", i18n.Args{"user": c.UserNotified, "auto": c.AutoAuthorized})
}

func (s *Service) confirmationLines(l i18n.Lang, c briefConfirmations) []string {
	var out []string
	for _, row := range c.Sources {
		source := row.SessionTitle
		if t, ok := s.todoNow(row.TodoID); ok && t.Title != "" {
			source = t.Title
		}
		if source == "" {
			source = i18n.T(l, "assistant-confirmations.unnamed_agent")
		}
		out = append(out, i18n.T(l, "assistant-confirmations.source", i18n.Args{"source": clip(oneLine(source), 60), "kind": confirmationKind(l, row.Kind), "user": row.UserNotified, "auto": row.AutoAuthorized}))
	}
	return out
}

func confirmationKind(l i18n.Lang, kind string) string {
	key := "assistant-confirmations.kind_" + kind
	if !i18n.Has(key) {
		key = "assistant-confirmations.kind_unknown"
	}
	return i18n.T(l, key)
}
