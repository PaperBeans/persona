package assistant

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// turnDurationItem is the item the transcript reader makes of Claude Code's system/turn_duration line: the turn it closes ended.
func turnDurationItem() proto.Item {
	return proto.Item{Role: "system", Kind: "turn_duration",
		TurnDuration: &proto.TurnDuration{DurationMS: 4100, Source: "claude_turn_duration", ParentUUID: "p1"}}
}

// An asked patrol that ends with tool calls and no text (#3934): once the transcript has its turn's end, that end is
// the patrol's own. A turn with no output at all, or whose end is not written yet, is still pushed.
func TestQuietTurnItemsToolOnlyPatrol(t *testing.T) {
	asked := taskMessageSource(Task{SessionID: "lead1", Title: "Patrol", Repeat: repeatInterval, Push: pushAsked})
	patrol := proto.Item{Role: "user", Kind: proto.KindText, Text: prompts.MessageSourceText("Check the pending work", asked)}
	check := []proto.Item{{Role: "assistant", Kind: proto.KindToolUse, ToolName: "Bash", ToolUseID: "tu1"},
		{Role: "user", Kind: proto.KindToolResult, ToolUseID: "tu1", Text: "0 open MRs"}}
	notify := []proto.Item{{Role: "assistant", Kind: proto.KindToolUse, ToolName: "mcp__persona__notify_user", ToolUseID: "tu2"},
		{Role: "user", Kind: proto.KindToolResult, ToolUseID: "tu2", Text: "Notification sent."}}
	cat := func(parts ...[]proto.Item) []proto.Item {
		var out []proto.Item
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	one := func(it ...proto.Item) []proto.Item { return it }
	for _, c := range []struct {
		name  string
		items []proto.Item
		want  bool
	}{
		{"tool calls only, turn ended", cat(one(patrol), check, one(turnDurationItem())), true},
		{"only notify_user, turn ended", cat(one(patrol), notify, one(turnDurationItem())), true},
		{"tool calls only, turn end not written yet", cat(one(patrol), check), false},
		{"no output at all, turn ended", one(patrol, turnDurationItem()), false},
		{"the user's turn ended, then a patrol input with no work yet", one(proto.Item{Role: "user", Kind: proto.KindText, Text: "帮我看下 MR"},
			proto.Item{Role: "assistant", Kind: proto.KindText, Text: "MR 已看完。"}, turnDurationItem(), patrol), false},
		{"tool calls only, turn ended, user text absorbed", cat(one(patrol), check,
			one(proto.Item{Role: "user", Kind: proto.KindText, Text: "MR 12 合了吗？", Queued: true}, turnDurationItem())), false},
	} {
		if got := quietTurnItems(c.items); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// End to end: a patrol that checks with a tool and ends without text gets no completion push and is not mirrored;
// one that only calls notify_user delivers that notification once and nothing else.
func TestQuietPatrolEndingWithToolCalls(t *testing.T) {
	e := newPatrolEnv(t)
	e.run()
	e.working()
	e.done([]proto.Item{e.patrolInput(),
		{Role: "assistant", Kind: proto.KindToolUse, ToolName: "Bash", ToolUseID: "tu1"},
		{Role: "user", Kind: proto.KindToolResult, ToolUseID: "tu1", Text: "0 open MRs"}, turnDurationItem()}, "")
	if n := e.pushes(1); n != 0 {
		t.Errorf("a tool-only patrol: %d completion notifications, want 0", n)
	}
	if e.mirrored() {
		t.Error("a tool-only patrol would be mirrored to channels")
	}

	e = newPatrolEnv(t)
	e.run()
	e.working()
	if _, err := e.ft.s.runTool(context.Background(), "mac", "lead1", "notify_user",
		json.RawMessage(`{"title":"MR 12 needs you","body":"Review requested"}`)); err != nil {
		t.Fatal(err)
	}
	e.done([]proto.Item{e.patrolInput(),
		{Role: "assistant", Kind: proto.KindToolUse, ToolName: "mcp__persona__notify_user", ToolUseID: "tu2"},
		{Role: "user", Kind: proto.KindToolResult, ToolUseID: "tu2", Text: "Notification sent."}, turnDurationItem()}, "")
	if n := e.pushes(1); n != 0 {
		t.Errorf("a patrol that only called notify_user: %d completion notifications, want 0", n)
	}
	if got := e.ft.pushesOf(NotifyPush, 1, 300*time.Millisecond); len(got) != 1 || got[0].Title != "MR 12 needs you" {
		t.Errorf("the explicit notification: %d delivered, want 1", len(got))
	}
	if e.mirrored() {
		t.Error("a patrol that only called notify_user would be mirrored to channels a second time")
	}
}
