package node

import (
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// The assistant's agent_switch (#5973): session.send with switch_now carries the switch it just picked out with the
// message now, or refuses the message with nothing typed and drops the switch (launcher_switch.go switchNowReady).

// tellNow is the assistant's message to sidA with switch_now (apply_switch left out: switch_now implies it).
func tellNow(t *testing.T, n *Node, id, text string) error {
	t.Helper()
	_, err := request(t, n, "session.send", map[string]any{"session_id": id, "text": text, "switch_now": true})
	return err
}

// An idle agent is quit and resumed on the launcher picked, and the message goes to it there, as the user's would.
func TestSwitchNowCarriesTheSwitchOut(t *testing.T) {
	n, f, _, cwd, _ := switchNode(t, proto.StatusIdle, nil)
	if err := pick(t, n, map[string]any{"launcher": "work", "from": "claude", "model": "opus"}); err != nil {
		t.Fatal(err)
	}
	f.reset()
	quitsToShell(f, cwd)
	comesUpWhen(f, "w1:p1", func(string) bool { return true })
	if err := tellNow(t, n, sidA, "go on"); err != nil {
		t.Fatalf("send: %v\n%s", err, strings.Join(f.trace(), "\n"))
	}
	starts := typedStarts(f, "w1:p1")
	if len(starts) != 1 || !strings.Contains(starts[0], " launch work ") || !strings.Contains(starts[0], "--resume "+sidA) {
		t.Fatalf("typed %q", starts)
	}
	if got := f.trace("agent.prompt"); len(got) != 1 || !strings.Contains(got[0], "go on") {
		t.Errorf("prompts = %q", got)
	}
	if st, _ := n.state.get(sidA); st.Launcher != "work" || st.PendingSwitch != nil {
		t.Errorf("record after the switch = %+v", st)
	}
}

// Where the user's message would go on the launcher the agent runs on and leave the switch waiting, the assistant's is
// refused with nothing typed, and the switch is dropped: a busy agent (switch_busy), a draft in its box.
func TestSwitchNowRefusedAndDroppedWhenItCannotGoNow(t *testing.T) {
	t.Run("working", func(t *testing.T) {
		n, f, _, _, _ := switchNode(t, proto.StatusWorking, nil)
		n.state.setPendingSwitch(sidA, &proto.PendingSwitch{Launcher: "work", At: time.Now()})
		f.reset()
		err := tellNow(t, n, sidA, "go on")
		if code, data := refusalOf(t, err); code != proto.CodeSwitchBusy || data["delivered"] != deliveredNone {
			t.Fatalf("refused with %q (%v)", code, err)
		}
		if got := drove(f); len(got) != 0 {
			t.Errorf("drove the terminal: %q", got)
		}
		if ps := n.pendingSwitch(sidA); ps != nil {
			t.Errorf("the switch still waits for the user's next message: %+v", ps)
		}
	})
	t.Run("a draft in the box", func(t *testing.T) {
		n, f, _, _, _ := switchNode(t, proto.StatusIdle, nil)
		n.state.setPendingSwitch(sidA, &proto.PendingSwitch{Launcher: "work", At: time.Now()})
		f.setScreens("w1:p1", screenFixture(t, "claude-2.1.312-typed-162.txt"), screenFixture(t, "claude-2.1.312-typed-162.ansi"))
		f.reset()
		err := tellNow(t, n, sidA, "go on")
		if code, _ := refusalOf(t, err); code != "input_draft" {
			t.Fatalf("refused with %q (%v)", code, err)
		}
		if got := drove(f); len(got) != 0 {
			t.Errorf("drove the terminal: %q", got)
		}
		if ps := n.pendingSwitch(sidA); ps != nil {
			t.Errorf("the switch still waits for the user's next message: %+v", ps)
		}
	})
}

// With no switch waiting (the user picked another meanwhile, or none was made) or no agent running, nothing is typed.
func TestSwitchNowNeedsARunningAgentWithASwitch(t *testing.T) {
	n, f, _, _, _ := switchNode(t, proto.StatusIdle, nil)
	err := tellNow(t, n, sidA, "go on")
	if code, _ := refusalOf(t, err); code != proto.CodeSwitchStale {
		t.Fatalf("no switch: refused with %q (%v)", code, err)
	}
	const other = "0f1e2d3c-4b5a-4968-8776-655443322110"
	n.state.setPendingSwitch(other, &proto.PendingSwitch{Launcher: "work", At: time.Now()})
	err = tellNow(t, n, other, "go on")
	if code, _ := refusalOf(t, err); code != proto.CodeSwitchUnsupported {
		t.Fatalf("not running: refused with %q (%v)", code, err)
	}
	if n.pendingSwitch(other) != nil {
		t.Error("the switch of an agent that is not running still waits")
	}
	if got := drove(f); len(got) != 0 {
		t.Errorf("drove the terminal: %q", got)
	}
}
