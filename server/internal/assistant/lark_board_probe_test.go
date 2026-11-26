package assistant

import (
	"context"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
)

// The standing board, the workbench and the companions are the lark module's; these tests of the core drive them
// through the module's layer, which lets a test set the board's clock and timer, hold its lock and look at the notices
// (modules/lark/layer_board.go). They skip without the module, as needsLarkLayer does.

type (
	botBoardRef  = assistantwire.ChatBoardRef
	CompanionBot = assistantwire.CompanionBot
)

const CompanionNotify = "notify"

// boardProbe is what the module's layer lets a test reach of the board.
type boardProbe interface {
	SetBoardTimer(now func() time.Time, after func(time.Duration, func()), done func())
	HoldBoard() (release func())
	RefreshBoard()
	NoticesState() (n int, waiting bool)
	NoticesPolled()
}

func (s *Service) boardProbe(t testing.TB) boardProbe {
	t.Helper()
	p, ok := s.larkModule().(boardProbe)
	if !ok {
		t.Skip("the lark module is not in this build")
	}
	return p
}

// setBoardTimer makes the board's clock and timer the fake ones.
func (s *Service) setBoardTimer(t testing.TB, now func() time.Time, after func(time.Duration, func())) {
	t.Helper()
	s.boardProbe(t).SetBoardTimer(now, after, nil)
}

// The module's settle and quiet times for the board (modules/lark/board.go); a change there fails the tests below.
const (
	boardSettle = 3 * time.Second
	boardOffFor = time.Minute
)

// workbenchStampMost is the size over which a card is not stamped (modules/lark/workbench_pin.go).
const workbenchStampMost = 22000

// boardRef is the board the chat's state has, nil when it has none.
func (s *Service) boardRef(ctx context.Context) *botBoardRef {
	var ref *botBoardRef
	s.larkModule().Book(ctx, func(b assistantwire.ChatBook) { ref = b.Board() })
	return ref
}

// setBoardRef puts the board in the chat's state.
func (s *Service) setBoardRef(ctx context.Context, ref *botBoardRef) {
	s.larkModule().Book(ctx, func(b assistantwire.ChatBook) { b.SetBoard(ref) })
}

// wbT is a line of the workbench's wording (the catalog the module draws from).
func wbT(l i18n.Lang, key string, args ...i18n.Args) string {
	return i18n.T(l, "assistant-feishu_workbench."+key, args...)
}
