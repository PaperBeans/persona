package lark

import (
	"testing"
	"time"
)

// The presses of the bot's one-shot buttons (card_actions.go): one press, one thing. The presses themselves, through the
// cards and the agents they start, are the assistant's feishu_cards_test.go.

// pressSlotHeld reports whether message mid's button kind slot is still taken.
func pressSlotHeld(c core, mid, kind string) bool {
	b := c.fbot()
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.pressAt[mid+kind]
	return ok
}

// A start that fails only after the window must not free a slot a later manual press has taken: the late failure
// callback carries the timestamp its own press stamped, and the compare-and-swap leaves the newer slot in place, so
// a third press is still swallowed instead of starting a third agent.
func TestStartCardLateFailureKeepsNewerSlot(t *testing.T) {
	s := newTestService(t, newFakeHub())
	c := s.chat()
	mid := "om_card1"

	s.setLarkTiming("start_card_window", 0) // both presses are past the window, so the second press re-stamps the slot
	now := time.Unix(1000, 0)
	s.ch().now = func() time.Time { return now } // the chat's clock: the second press comes a moment later
	_, first := c.startCardPressed(mid)
	now = now.Add(time.Millisecond)
	if blocked, second := c.startCardPressed(mid); blocked || !second.After(first) {
		t.Fatalf("second press after the window: blocked=%v second=%v first=%v, want it to take a fresh slot", blocked, second, first)
	}
	// The first start's failure arrives late; it owns the old timestamp and must not delete the newer slot.
	c.forgetStartCard(mid, first)
	if !pressSlotHeld(c, mid, "start") {
		t.Fatal("the late failure released the second press's slot")
	}
	// Inside the restored window a third press is therefore still swallowed.
	s.setLarkTiming("start_card_window", 60*time.Second)
	if blocked, _ := c.startCardPressed(mid); !blocked {
		t.Fatal("third press was not deduped: the late failure had dropped the newer slot")
	}
}
