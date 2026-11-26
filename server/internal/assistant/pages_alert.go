package assistant

import (
	"context"
	"strings"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/deeplink"
	"github.com/nwverse/persona/server/internal/extmgr"
	"github.com/nwverse/persona/server/internal/i18n"
)

// pageAlertDeepLink is the in-app destination for a watch page's alert notification: /extension/-pages/<page id>.
func pageAlertDeepLink(pageID string) string {
	return deeplink.Extension(extmgr.PagesExtension, pageID, "")
}

// pageAlertBody formats the alert push body: the watch's question and latest reading in words, followed by alert_text
// when the reading supplied one.
func pageAlertBody(question, readingWords, alertText string, l i18n.Lang) string {
	note := strings.TrimSpace(alertText)
	if note != "" {
		return i18n.T(l, "assistant-page-watch.alert_body_note", i18n.Args{
			"question": question,
			"reading":  readingWords,
			"note":     note,
		})
	}
	return i18n.T(l, "assistant-page-watch.alert_body", i18n.Args{
		"question": question,
		"reading":  readingWords,
	})
}

// pageReadingWritten runs after a reading is stored on watch page p (page patch, #13020):
//   - clears any pending manual refresh ("Checking now…") and records separated refresh timing;
//   - invalidates the page, the Dashboards index, and the page's Overview card (when on Overview);
//   - evaluates the watch's alert rule deterministically (wire.Trips + atomic db.EvaluateAssistantPageAlert edge
//     trigger and 12-hour cooldown) and sends the page_alert push (card ledger kind 1, level P1) only when the
//     owner's alert switch is on and the rule newly trips outside the cooldown window.
func (s *Service) pageReadingWritten(ctx context.Context, p *db.AssistantPageRow) {
	if p == nil || p.Status != db.AssistantPageActive {
		return
	}
	st, ok := s.pageStore()
	if !ok {
		return
	}
	rows, err := st.AssistantPageReadings(ctx, p.ID, 1)
	if err != nil {
		s.logf("assistant pages: alert readings for %s: %v", p.ID, err)
		return
	}
	evalAt := time.Now()
	if len(rows) > 0 && !rows[0].At.IsZero() {
		evalAt = rows[0].At
	}
	s.clearPageRefreshOnReading(ctx, p.ID, evalAt)
	if _, _, err := st.ClearAssistantPageRefresh(ctx, p.ID, time.Now()); err != nil {
		s.logf("assistant pages: clear refresh for %s: %v", p.ID, err)
	}
	s.pageReadingChanged(p)
	if len(p.Watch) == 0 || len(rows) == 0 {
		return
	}
	watch, err := wire.ParsePageWatch(p.Watch)
	if err != nil {
		s.logf("assistant pages: parse watch for %s: %v", p.ID, err)
		return
	}
	reading, err := wire.ParsePageReading(watch, rows[0].Reading)
	if err != nil {
		s.logf("assistant pages: parse reading for %s: %v", p.ID, err)
		return
	}
	if watch.Alert == nil {
		return
	}
	tripped := wire.Trips(watch.Alert, reading)
	_, shouldPush, err := st.EvaluateAssistantPageAlert(ctx, p.ID, tripped, db.AssistantPageAlertCooldown, evalAt)
	if err != nil {
		s.logf("assistant pages: evaluate alert for %s: %v", p.ID, err)
		return
	}
	if !shouldPush {
		return
	}
	l := s.lang(ctx)
	readingWords := watchReadingText(watch, reading, l)
	// Deep link destination is pageAlertDeepLink(p.ID) (`/extension/-pages/<id>`); Notification currently carries
	// no URL/DeepLink field in assistantwire, so clients route by RelatedKey until the push wire carries deep links.
	_ = pageAlertDeepLink(p.ID)
	s.push(withCard(ctx, cardPageAlert), Notification{
		Subtitle:   "Persona",
		Title:      p.Title,
		Body:       pageAlertBody(watch.Question, readingWords, rows[0].AlertText, l),
		Level:      assistantwire.LevelP1,
		RelatedKey: "page-alert:" + p.ID,
	})
}
