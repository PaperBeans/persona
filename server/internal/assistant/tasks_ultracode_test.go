package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// planStart opens a plan that waits for the card's "Start" (ultracode as the assistant gave it; nil leaves it out),
// starts it with the body a client sends, and answers what session.start was asked for.
func planStart(t *testing.T, ultracode any, body string) map[string]any {
	t.Helper()
	s, f := newTaskService(t)
	ctx := context.Background()
	task := map[string]any{"title": "重构同步层", "prompt": "把同步层拆成三个模块并补测试", "machine": "box", "cwd": "/code/app",
		"readonly": false, "permission": "bypassPermissions", "why": "这个仓库最近在 box 上"}
	if ultracode != nil {
		task["ultracode"] = ultracode
	}
	args, _ := json.Marshal(map[string]any{"tasks": []any{task}})
	if res, err := s.toolTaskPlan(ctx, "conv1", args); err != nil || !strings.Contains(res, "nothing has started") {
		t.Fatalf("plan: %q %v", res, err)
	}
	var in struct {
		Tasks []PlanTask `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(body), &in); err != nil {
		t.Fatal(err)
	}
	items, plans, _ := s.feed(ctx, "conv1", time.Time{})
	for _, it := range items {
		if it.Kind != feedPlan {
			continue
		}
		shown := plans[it.Ref].Tasks[0].Ultracode
		if got, want := shown != nil && *shown, ultracode == true; got != want {
			t.Fatalf("the card shows ultracode %v, the assistant gave %v", got, ultracode)
		}
		if _, err := s.startPlan(ctx, it.Ref, in.Tasks, false); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "the agent to start", func() bool { f.mu.Lock(); defer f.mu.Unlock(); return len(f.starts) == 1 })
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts[0]
}

// A task's ultracode is the assistant's choice unless the user changed it on the card; a client that does not know the
// field keeps the assistant's.
func TestTaskPlanUltracode(t *testing.T) {
	for _, c := range []struct {
		name      string
		assistant any
		body      string
		want      bool
	}{
		{"the assistant's choice", true, `{}`, true},
		{"a card that does not know it keeps it", true, `{"tasks":[{"machine":"box"}]}`, true},
		{"the user turns it off", true, `{"tasks":[{"ultracode":false}]}`, false},
		{"the user turns it on", nil, `{"tasks":[{"ultracode":true}]}`, true},
		{"left out", nil, `{}`, false},
		{"given as off", false, `{}`, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			start := planStart(t, c.assistant, c.body)
			if v, sent := start["ultracode"]; sent != c.want || sent && v != true {
				t.Fatalf("session.start ultracode = %v (sent %v), want %v", v, sent, c.want)
			}
		})
	}
}

// agent_start asks the node for ultracode only when the assistant does; off sends nothing, as a new process is off.
func TestAgentStartUltracode(t *testing.T) {
	for _, on := range []bool{true, false} {
		s, f := newTaskService(t)
		args, _ := json.Marshal(map[string]any{"machine": "box", "cwd": "/code/app", "prompt": "把同步层拆成三个模块", "ultracode": on})
		if _, err := s.runTool(context.Background(), "server", "conv9", "agent_start", args); err != nil {
			t.Fatal(err)
		}
		f.mu.Lock()
		starts := f.starts
		f.mu.Unlock()
		if len(starts) != 1 {
			t.Fatalf("starts %v", starts)
		}
		if v, sent := starts[0]["ultracode"]; sent != on || sent && v != true {
			t.Fatalf("ultracode %v: session.start had %v (sent %v)", on, v, sent)
		}
	}
}
