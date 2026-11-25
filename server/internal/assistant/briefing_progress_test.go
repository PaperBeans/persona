package assistant

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// The "in progress" section of a briefing: the tasks whose Agents are working, with their stage bar.

// working is an open task with an Agent that is working on it.
func (e *briefRunEnv) working(id, title, stage string, age time.Duration, sessions *[]proto.Session) {
	session := "ag_" + id
	e.s.todos.todos[id] = &Todo{ID: id, Title: title, Origin: "conv1", Stage: stage, CreatedAt: time.Now().Add(-age),
		Agents: []TodoAgent{{MachineID: "server", SessionID: session}}}
	*sessions = append(*sessions, proto.Session{SessionID: session, State: proto.StateWorking, Status: proto.StatusWorking})
}

// Running tasks are listed last, longest running first, each with its stage bar; a task that waits for a card or is done is
// not in the list, and past eight the rest are counted.
func TestBriefListsWhatIsInProgress(t *testing.T) {
	needsLarkLayer(t) // the chat is the lark module's
	e := newBriefRunEnvWith(t, true, func(e *briefRunEnv) {
		var sessions []proto.Session
		for i := 0; i < 9; i++ {
			e.working(fmt.Sprintf("todo_w%d", i), fmt.Sprintf("在做的事%d", i), "develop", time.Duration(9-i)*time.Hour, &sessions)
		}
		e.s.todos.todos["todo_w0"].Stage = "review"
		e.s.todos.todos["todo_idle"] = &Todo{ID: "todo_idle", Title: "没有 Agent 在做", Origin: "conv1"}
		e.f.setSessions("server", sessions...)
	})
	e.mirror()
	card := e.cards()[0]
	at := func(want string) int {
		t.Helper()
		i := strings.Index(card, want)
		if i < 0 {
			t.Fatalf("the card has no %q:\n%s", want, card)
		}
		return i
	}
	if !(at("待验收（1）") < at("进行中（9）") && at("进行中（9）") < at("在做的事0") && at("在做的事0") < at("在做的事1")) {
		t.Errorf("the section is not last, longest running first:\n%s", card)
	}
	at("🔵**审查**") // todo_w0 is at its review stage
	at("还有 1 项")
	if strings.Contains(card, "在做的事8") || strings.Contains(card, "没有 Agent 在做") {
		t.Errorf("a ninth task or an idle one is listed:\n%s", card)
	}
	var b briefBody
	if err := json.Unmarshal(e.briefItems()[0].Body, &b); err != nil {
		t.Fatal(err)
	}
	if b.ProgressTotal != 9 || len(b.Progress) != 8 || b.Progress[0].ID != "todo_w0" || b.Progress[0].Stage != "review" {
		t.Errorf("the brief item's progress: %d of %d, %+v", len(b.Progress), b.ProgressTotal, b.Progress)
	}
	sent, _ := e.f.lastSend(t)["text"].(string)
	if !strings.Contains(sent, "正在做的 9 件") || !strings.Contains(sent, "在做的事0（阶段：review）") {
		t.Errorf("the model is not told what is in progress:\n%s", sent)
	}
}

// With nothing running the section is left out.
func TestBriefLeavesOutAnEmptyProgressSection(t *testing.T) {
	needsLarkLayer(t) // the chat is the lark module's
	e := newBriefRunEnv(t, true)
	e.mirror()
	if card := e.cards()[0]; strings.Contains(card, "进行中") {
		t.Fatalf("an empty section is on the card:\n%s", card)
	}
}
