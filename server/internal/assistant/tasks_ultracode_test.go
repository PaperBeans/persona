package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
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

// A start refused for its ultracode (a Codex launcher, an old Claude Code, a node too old for it) goes again without
// it: the task starts, its card and receipt say why, and the stored plan stops asking for it (#3596).
func TestTaskStartsWithoutUltracodeWhenRefused(t *testing.T) {
	for _, refusal := range []*rpc.Error{
		{Code: "ultracode_unsupported", Message: "只有 Claude Code Agent 支持 ultracode"},
		{Code: proto.CodeNodeOutdated, Message: "这台机器上的 personad 太旧，还不支持 ultracode，需要更新 node"},
	} {
		t.Run(refusal.Code, func(t *testing.T) {
			s, f := newTaskService(t)
			f.refuseUltra = refusal
			ctx := context.Background()
			args, _ := json.Marshal(map[string]any{"tasks": []any{map[string]any{"title": "重构同步层", "prompt": "把同步层拆成三个模块并补测试",
				"machine": "box", "cwd": "/code/app", "readonly": false, "permission": "bypassPermissions", "why": "这个仓库最近在 box 上",
				"ultracode": true}}})
			if _, err := s.toolTaskPlan(ctx, "conv1", args); err != nil {
				t.Fatal(err)
			}
			items, plans, _ := s.feed(ctx, "conv1", time.Time{})
			var planID string
			for _, it := range items {
				if it.Kind == feedPlan {
					planID = it.Ref
				}
			}
			if _, err := s.startPlan(ctx, planID, nil, false); err != nil {
				t.Fatal(err)
			}
			waitFor(t, "the start without ultracode", func() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.agents == 1 })
			f.mu.Lock()
			starts := append([]map[string]any(nil), f.starts...)
			f.mu.Unlock()
			if len(starts) != 2 || starts[0]["ultracode"] != true {
				t.Fatalf("starts %v: want the refused one with ultracode, then one without", starts)
			}
			if _, sent := starts[1]["ultracode"]; sent {
				t.Fatalf("the second start asked for ultracode again: %v", starts[1])
			}
			waitFor(t, "the plan to record the start", func() bool {
				_, plans, _ = s.feed(ctx, "conv1", time.Time{})
				pt := plans[planID].Tasks[0]
				return pt.Ultracode != nil && !*pt.Ultracode && strings.Contains(pt.Why, refusal.Message)
			})
			if pt := plans[planID].Tasks[0]; pt.Error != "" {
				t.Fatalf("the task reads as not started: %q", pt.Error)
			}
		})
	}
}

// agent_start does the same, and its answer says so (#3596).
func TestAgentStartWithoutUltracodeWhenRefused(t *testing.T) {
	s, f := newTaskService(t)
	f.refuseUltra = &rpc.Error{Code: "ultracode_unsupported", Message: "只有 Claude Code Agent 支持 ultracode"}
	args, _ := json.Marshal(map[string]any{"machine": "box", "cwd": "/code/app", "prompt": "把同步层拆成三个模块", "ultracode": true})
	res, err := s.runTool(context.Background(), "server", "conv9", "agent_start", args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "started without ultracode (只有 Claude Code Agent 支持 ultracode)") {
		t.Fatalf("the answer does not say ultracode is off: %q", res)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.starts) != 2 || f.agents != 1 {
		t.Fatalf("starts %v, agents %d: want one refused start and one agent", f.starts, f.agents)
	}
}

// Only a refusal of ultracode, of a start that asked for it, starts again without it.
func TestUltracodeRefusal(t *testing.T) {
	asked := map[string]any{"ultracode": true}
	for _, c := range []struct {
		name   string
		err    error
		params map[string]any
		want   bool
	}{
		{"unsupported", &rpc.Error{Code: "ultracode_unsupported", Message: "x"}, asked, true},
		{"old node", &rpc.Error{Code: proto.CodeNodeOutdated, Message: "x"}, asked, true},
		{"not asked", &rpc.Error{Code: "ultracode_unsupported", Message: "x"}, map[string]any{}, false},
		{"another refusal", &rpc.Error{Code: "codex_needs_prompt", Message: "x"}, asked, false},
		{"not a refusal", errors.New("connection reset"), asked, false},
		{"no error", nil, asked, false},
	} {
		s, _ := newTaskService(t)
		if _, got := s.ultracodeRefusal(context.Background(), c.err, c.params); got != c.want {
			t.Errorf("%s: refused %v, want %v", c.name, got, c.want)
		}
	}
}

// The start without ultracode whose answer is lost still says ultracode is off: the model must not think it is on.
func TestAgentStartWithoutUltracodeSaysSoWhenTheAnswerIsLost(t *testing.T) {
	s, f := newTaskService(t)
	shortAgentStartTimeout(t)
	f.refuseUltra = &rpc.Error{Code: "ultracode_unsupported", Message: "只有 Claude Code Agent 支持 ultracode"}
	f.slowStart = true
	args, _ := json.Marshal(map[string]any{"machine": "box", "cwd": "/code/app", "prompt": "把同步层拆成三个模块", "ultracode": true})
	res, err := s.runTool(context.Background(), "server", "conv9", "agent_start", args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "started without ultracode") {
		t.Fatalf("the unknown-result answer does not say ultracode is off: %q", res)
	}
}

// A start refused for ultracode whose second try fails as well leaves the task asking for it: moved to a computer that
// can, it gets it (#3596).
func TestTaskKeepsUltracodeWhenTheStartWithoutItFails(t *testing.T) {
	s, f := newTaskService(t)
	f.refuseUltra = &rpc.Error{Code: "ultracode_unsupported", Message: "请升级到 Claude Code 2.1.284 或更新版本"}
	f.startFailed = "zsh: command not found: claude" // the start without it went out, and its launcher failed
	on := true
	task := PlanTask{Title: "重构同步层", Prompt: "把同步层拆成三个模块", Machine: "box", Cwd: "/code/app", Ultracode: &on}
	if _, _, err := s.startTaskAgent(context.Background(), &task, false); err == nil {
		t.Fatal("the failed start went through")
	}
	if task.Ultracode == nil || !*task.Ultracode || strings.Contains(task.Why, "ultracode") {
		t.Fatalf("a start that did not happen turned ultracode off (%v, why %q)", task.Ultracode, task.Why)
	}
}
