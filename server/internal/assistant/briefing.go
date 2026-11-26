package assistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/modapi"
)

// Thin core hooks to the daily briefing module (server/internal/modules/briefing via s.modules.ScheduleFacts().Runner).
// When the briefing module is switched off or left out of the build, every hook degrades cleanly to a no-op / zero value
// and routines run without briefing facts. The Lark card renderer (briefing_card.go) and briefRoutes (briefing_feed.go)
// stay here until the neutral Card model and hub_routes slot land (#13845).

const (
	// briefWindow and briefRowsMax are shared with briefing_card.go until the card moves into the module.
	briefWindow  = 12 * time.Hour
	briefRowsMax = 10
	briefKeep    = 3 * time.Hour
)

type (
	briefFacts    = modapi.BriefSnapshot
	briefRun      = modapi.BriefRun
	briefProgress = modapi.BriefTaskProgress
)

func briefTaskInfo(t Task) modapi.BriefTaskInfo {
	return modapi.BriefTaskInfo{
		ID:           t.ID,
		Label:        t.label(),
		Facts:        t.Facts,
		Report:       t.Report,
		SessionID:    t.SessionID,
		RunSessionID: t.RunSessionID,
		MachineID:    t.MachineID,
	}
}

func briefScheduleRun(r ScheduleRun) modapi.BriefScheduleRun {
	return modapi.BriefScheduleRun{
		ID:        r.ID,
		RefID:     r.RefID,
		SessionID: r.SessionID,
		MachineID: r.MachineID,
		Due:       r.Due,
		Retried:   r.Retried,
		Manual:    r.Manual,
	}
}

func (s *Service) briefFactsAt(ctx context.Context, now time.Time, self string) *briefFacts {
	r, ok := s.briefingRunner()
	if !ok {
		return nil
	}
	return r.FactsAt(ctx, now, self)
}

func (s *Service) briefStart(ctx context.Context, t Task, now time.Time) string {
	r, ok := s.briefingRunner()
	if !ok {
		return ""
	}
	return r.Start(ctx, briefTaskInfo(t), now)
}

func (s *Service) briefReportStart(ctx context.Context, t Task, now time.Time) string {
	r, ok := s.briefingRunner()
	if !ok {
		return ""
	}
	return r.StartReport(ctx, briefTaskInfo(t), now)
}

func (s *Service) briefSent(id, session string, retried bool) {
	if r, ok := s.briefingRunner(); ok {
		r.Sent(id, session, retried)
	}
}

func (s *Service) briefOf(session string) *briefRun {
	r, ok := s.briefingRunner()
	if !ok {
		return nil
	}
	return r.SessionRun(session)
}

func (s *Service) briefClaim(session, asked, turn string, cut bool) (*briefRun, bool) {
	r, ok := s.briefingRunner()
	if !ok {
		return nil, false
	}
	return r.Claim(session, asked, turn, cut)
}

func (s *Service) briefFailure(session, asked string, cut bool) bool {
	r, ok := s.briefingRunner()
	if !ok {
		return false
	}
	return r.IsFailure(session, asked, cut)
}

func (s *Service) briefFallback(ctx context.Context, scheduled ScheduleRun, t Task, m Device) {
	if r, ok := s.briefingRunner(); ok {
		r.Fallback(ctx, briefScheduleRun(scheduled), briefTaskInfo(t), modapi.BriefDevice{ID: m.ID, Name: m.Name})
	}
}

func (s *Service) briefNotStarted(ctx context.Context, t Task, failed ScheduleRun, retry bool) {
	if r, ok := s.briefingRunner(); ok {
		r.NotStarted(ctx, briefTaskInfo(t), briefScheduleRun(failed), retry)
	}
}

func (s *Service) briefPush(session string, l i18n.Lang, report bool) string {
	r, ok := s.briefingRunner()
	if !ok {
		return ""
	}
	return r.Push(session, string(l), report)
}

func (s *Service) briefReached(session string) {
	if r, ok := s.briefingRunner(); ok {
		r.Reached(session)
	}
}

func (s *Service) briefingGrace(ctx context.Context, local time.Time, start int) int {
	r, ok := s.briefingRunner()
	if !ok {
		return 0
	}
	return r.Grace(ctx, local, start)
}

// briefMirror sends the cards of a routine with facts in place of its answer's cards (feishuMirror): the model's answer
// on top, the facts under it. Required owner waits continue onto more cards rather than crossing Feishu's physical
// budget. mirrorSend's per-index key makes every continuation retry independently and idempotently.
func (s *Service) briefMirror(ctx context.Context, machine, session, label string, run *briefRun, answer, turn string) {
	card, fallback, shown := s.briefCard(run.Facts, run.Label, answer)
	card.Buttons(s.openButton(ctx, machine, session))
	cards := splitBriefAnswerCards(card, fallback, run.Label)
	sum := sha256.Sum256([]byte("brief\x00" + machine + "\x00" + session + "\x00" + turn + "\x00" + answer))
	m := &mirrorSend{
		l: i18n.Lang(run.Facts.Lang), cards: cards,
		target: botTarget{Kind: targetPersona, MachineID: machine, SessionID: session}, label: label,
		answer: answer, turn: turn, key: "b" + hex.EncodeToString(sum[:8]),
		sent: func() { s.briefAsked(run.Facts, shown) },
	}
	s.feishuMirrorPost(ctx, m, 0)
}

// briefAsked notes that the briefing card with the Accept buttons of the tasks shown went out: those tasks are not
// reminded that day (remindAcceptance), the card asked for them. Only the round the card was made for counts.
func (s *Service) briefAsked(f *briefFacts, shown []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second) // not the mirror's: this may run on its retry
	defer cancel()
	s.setupTold(fromBriefSetup(f.Setup), f.At) // the card carried the week's block
	asked := map[string]time.Time{}
	for _, r := range f.Accept {
		asked[r.ID] = r.Asked
	}
	b := &s.accept
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.loadAcceptLocked(ctx) {
		return
	}
	for _, id := range shown {
		if a := b.asks[id]; a != nil && a.AskedAt.Equal(asked[id]) {
			a.RemindedAt = time.Now().UTC()
			a.ForBriefing = false // one that waited for a briefing is put to the owner on this card (decision_wait.go)
		}
	}
	s.saveAcceptLocked(ctx)
}

// acceptTryFromBrief is the Accept button of a briefing card: it accepts task id when the round the button was made for
// (askedAt, as the card wrote it) is the round that waits now. The card is not one of the ask's own messages, so
// acceptTry cannot tell an old round's button from a current one by its message id. The errors are acceptTry's: a
// decision whose recommendation did not reach the agent, or errAcceptStore (the asks cannot be read or the acceptance
// saved), which is not "another round".
func (s *Service) acceptTryFromBrief(ctx context.Context, id, askedAt string) (acceptResult, error) {
	at, err := time.Parse(timeKeyLayout, askedAt)
	if err != nil {
		return acceptResult{}, nil
	}
	s.reviewDismissals.actions.acquire(id)
	defer s.reviewDismissals.actions.release(id)
	return s.acceptTryAsDismissalLocked(ctx, id, "", at, false)
}
