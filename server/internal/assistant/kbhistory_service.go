package assistant

import (
	"context"
	"errors"
	"time"
)

// kbHistoryPoll is how often the import's loop looks for something to run (the module's kbhistory.Poll).
const kbHistoryPoll = time.Minute

// kbHistoryShared is the import's part of the Service: what the loop is woken by. The import itself is the
// knowledge-ui module's (knowledge_ui.go).
type kbHistoryShared struct {
	wake chan struct{}
}

// ---- the Service's side ----

func (s *Service) kbHistoryInit() {
	s.kb.history.wake = make(chan struct{}, 1)
}

// kbHistoryWake has the loop look again now (a plan was approved, STOP removed).
func (s *Service) kbHistoryWake() {
	select {
	case s.kb.history.wake <- struct{}{}:
	default:
	}
}

// kbHistoryLoop runs the import while ctx lasts, looking every minute (at once when woken). Without the knowledge-ui
// module there is no import and it returns at once.
func (s *Service) kbHistoryLoop(ctx context.Context) {
	if s.kb.dir == "" || s.ui == nil {
		return
	}
	t := time.NewTicker(kbHistoryPoll)
	defer t.Stop()
	for {
		s.kbRun(ctx, func() { s.kbHistoryRound(ctx) })
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.kb.history.wake:
		}
	}
}

// kbHistoryRound runs what is runnable now, if anything: only while connected, not a mesh member's standby, the
// knowledge base writable and kb.history enabled.
func (s *Service) kbHistoryRound(ctx context.Context) {
	if !s.kbConnected() || s.hello(ctx).Assistant.Standby || !s.kbWritable(ctx) {
		return
	}
	if set := s.kbSettings(ctx); set == nil || !set.History.On() {
		return
	}
	e, err := s.kbGet(ctx)
	if err != nil || e.Store.ReadOnly() {
		return
	}
	if err := s.ui.RunOnce(ctx, e.Store.Dir); err != nil && !errors.Is(err, context.Canceled) {
		s.logf("kb history: %v", err)
	}
}
