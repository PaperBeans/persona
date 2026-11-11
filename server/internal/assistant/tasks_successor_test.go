package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// A task whose agents have all stopped (read-only, #4754): the user's message on its card starts a new agent for it,
// on the same computer, in the same directory with the same launcher, its first message the user's after who worked
// on it before and that agent's last reply; the new agent is linked to the task and the feed line names it. Persona's
// own follow-ups (a rework, a relayed answer) start nothing and send nothing.
func TestStoppedTaskGoesOnWithANewAgent(t *testing.T) {
	s, f := newQuietService(t)
	secretary(f)
	ctx := context.Background()
	if _, err := runJSON(t, s, "conv1", "agent_send", map[string]any{"machine": "box", "session_id": "sec00001", "text": "把周报数据拉一下",
		"task": map[string]any{"title": "拉周报数据"}}); err != nil {
		t.Fatal(err)
	}
	todo := onlyTodo(s)
	// Its agent stops: the node lists it read-only.
	f.setSessions("box", proto.Session{SessionID: "sec00001", MachineID: "box", Cwd: "/code/desk", Agent: "claude", Title: "秘书",
		Launcher: "work", PermissionMode: "acceptEdits", LastText: "表格在 /code/desk/out.xlsx",
		Status: proto.StatusOffline, State: proto.StateOffline, UpdatedAt: time.Now(),
		Capabilities: &proto.SessionCapabilities{Read: true, Terminal: true}, ReadOnlyReason: "stopped"})
	sent := len(sentToAgent(f))

	if _, err := s.sendToTask(ctx, todo, "（督办：返工）"); !errors.Is(err, errTaskAgentsStopped) {
		t.Fatalf("rework to a stopped task: %v", err)
	}
	if _, err := s.routeToTaskAs(ctx, "conv1", todo.ID, "接着把环比也算上", "acceptance", nil); err == nil {
		t.Fatal("a relayed answer went to a stopped task")
	}
	f.mu.Lock()
	starts := len(f.starts)
	f.mu.Unlock()
	if starts != 0 || len(sentToAgent(f)) != sent {
		t.Fatalf("Persona's follow-ups started %d agents, sent %d", starts, len(sentToAgent(f))-sent)
	}

	item, err := s.routeToTask(ctx, "conv1", todo.ID, "接着把环比也算上")
	if err != nil {
		t.Fatalf("the user's message: %v", err)
	}
	f.mu.Lock()
	if len(f.starts) != 1 {
		f.mu.Unlock()
		t.Fatalf("started %d agents", len(f.starts))
	}
	start := f.starts[0]
	f.mu.Unlock()
	prompt, _ := start["prompt"].(string)
	if start["cwd"] != "/code/desk" || start["launcher"] != "work" || start["permission_mode"] != "acceptEdits" ||
		!strings.Contains(prompt, "秘书") || !strings.Contains(prompt, "sec00001") ||
		!strings.Contains(prompt, "/code/desk/out.xlsx") || !strings.HasSuffix(prompt, "接着把环比也算上") {
		t.Fatalf("start = %v", start)
	}
	var body struct {
		NewAgent string `json:"new_agent"`
	}
	_ = json.Unmarshal(item.Body, &body)
	if body.NewAgent == "" {
		t.Fatalf("the feed line does not name the new agent: %s", item.Body)
	}
	linked := false
	for _, a := range todoNow(s, todo.ID).Agents {
		linked = linked || a.SessionID == body.NewAgent
	}
	if !linked {
		t.Fatalf("the new agent %s is not on the task: %+v", body.NewAgent, todoNow(s, todo.ID).Agents)
	}
	if len(sentToAgent(f)) != sent {
		t.Fatal("the stopped agent was sent the message")
	}
}
