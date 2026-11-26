package lark

import (
	"context"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/botcard"
)

// What the core asks of the module about the standing board, the pinned workbench and the companion bots
// (internal/larkhost.Board).

// BoardChanged says something the board shows changed: it is edited a moment later, once for all that came meanwhile.
func (l *Layer) BoardChanged() { l.chat().boardChanged() }

// WorkbenchOn notes the workbench switch as just read: the stamp on the bot's cards follows it.
func (l *Layer) WorkbenchOn(f assistantwire.FeishuFeatures, ready bool) {
	l.chat().workbenchOn(f, ready)
}

// WorkbenchStamp adds the way back to the workbench to a card the bot posts or redraws, with the switch on.
func (l *Layer) WorkbenchStamp(ctx context.Context, c *botcard.Card) *botcard.Card {
	return l.chat().workbenchStamp(ctx, c)
}

// DivertNotices hands cards to the notifications companion when it takes them; false: they go to the bot's chat.
func (l *Layer) DivertNotices(ctx context.Context, kind assistantwire.NoticeKind, cards []*botcard.Card, fallbacks []string) bool {
	return l.chat().divertNotices(ctx, kind, cards, fallbacks)
}

// The tests of the core reach into the board through these (black-box tests of what the board does with the clock
// and the lock of a write). Not part of the port (larkhost.Board).

// SetBoardTimer replaces the board's clock and timer, and sets what is called when a settle-time edit has been through.
func (l *Layer) SetBoardTimer(now func() time.Time, after func(time.Duration, func()), done func()) {
	b := &l.turns.board
	b.now, b.after, b.done = now, after, done
}

// HoldBoard takes the lock a write of the board holds; the result lets it go.
func (l *Layer) HoldBoard() (release func()) {
	l.turns.board.run.Lock()
	return l.turns.board.run.Unlock
}

// NoticesState is how many notices wait for the notifications companion and whether a poll waits for the next.
func (l *Layer) NoticesState() (n int, waiting bool) {
	c := &l.turns.comp
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.list), c.wait != nil
}

// NoticesPolled notes that the notifications companion polled now.
func (l *Layer) NoticesPolled() {
	c := &l.turns.comp
	c.mu.Lock()
	c.polled = time.Now()
	c.mu.Unlock()
}

// RefreshBoard runs the settle-time refresh now.
func (l *Layer) RefreshBoard() { l.chat().boardRefresh() }
