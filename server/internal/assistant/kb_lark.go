package assistant

import (
	"context"
	"time"

	"github.com/nwverse/persona/server/internal/assistant/kblark"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/larkcli"
)

const (
	// kbLarkMinutesEvery is the least time between two imports when kb.lark_minutes.interval_minutes is not set.
	kbLarkMinutesEvery = 60 * time.Minute
	// kbLarkMinutesTimeout bounds one lark-cli call.
	kbLarkMinutesTimeout = time.Minute
)

// kbLarkMinutes imports the owner's Lark minutes and fills the titles of their Lark documents through lark-cli, when
// kb.lark_minutes is on and the run is due (the archive lane's kbRounds.lark). A failure is logged and the round goes
// on; nothing else in the knowledge base waits for it.
func (s *Service) kbLarkMinutes(ctx context.Context, e *kb.Engine, r *kbRounds) {
	cfg := s.larkMinutesConfig(ctx)
	if !cfg.On() || e.Store.ReadOnly() {
		return
	}
	every := time.Duration(cfg.IntervalMinutes) * time.Minute
	if every <= 0 {
		every = kbLarkMinutesEvery
	}
	if time.Since(r.lark) < every {
		return
	}
	r.lark = time.Now()
	client := kblark.NewClient(larkcli.New(larkcli.Config{Bin: cfg.Binary, Profile: cfg.Profile, Timeout: kbLarkMinutesTimeout}), e.Store.Zone())
	res, err := kblark.Run(ctx, e, client, kblark.Config{SinceDays: cfg.SinceDays, DocSearch: cfg.DocSearch}, time.Now(), s.logf)
	if err != nil {
		if ctx.Err() == nil {
			s.logf("kb: lark minutes: %v", err)
		}
		return
	}
	if res.Changed > 0 || res.Filled > 0 || res.Docs > 0 {
		s.logf("kb: lark minutes: %d minute(s) read, %d changed; %d document title(s) filled; %d document(s) found",
			res.Minutes, res.Changed, res.Filled, res.Docs)
	}
}

// larkMinutesConfig is kb.lark_minutes, read afresh every round; nil when the knowledge base is off or read-only.
func (s *Service) larkMinutesConfig(ctx context.Context) *assistantwire.KBLarkMinutes {
	if set := s.kbSettings(ctx); set != nil && !set.Off && !set.ReadOnly {
		return set.LarkMinutes
	}
	return nil
}
