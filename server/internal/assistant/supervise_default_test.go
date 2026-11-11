package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// #4669: a task an ordinary conversation (neither the main one nor a leader) hands out with task_plan is supervised by
// the main conversation: it has a ledger, its supervision messages go to the main conversation, and the planning
// conversation gets one line with the task's link when it ends.
func TestSideConversationTaskSupervisedByMain(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	f.judge = `{"verdict":"done","reason":"PR 已合并"}`
	f.known["side1"] = true
	f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "side1", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
	raw, _ := json.Marshal(map[string]any{"tasks": []map[string]any{{
		"title": "修登录崩溃", "prompt": "iPad 横屏时登录页崩溃，修好并补测试", "machine": "box", "cwd": "/code/app", "readonly": true,
		"why": "这个仓库最近在 box 上", "done_when": []string{"PR 合并到 main"}, "expect_minutes": 30,
	}}})
	res, err := s.toolTaskPlan(ctx, "side1", raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "The main conversation supervises them") {
		t.Fatalf("the planning conversation is not told who supervises: %s", res)
	}
	waitFor(t, "the link", func() bool { return len(onlyTodo(s).Agents) == 1 })
	todo := onlyTodo(s)
	if v := todo.Supervision; v == nil || v.Supervisor != "conv1" || todo.Origin != "side1" || len(v.DoneWhen) != 1 {
		t.Fatalf("ledger = %+v (origin %q)", todo.Supervision, todo.Origin)
	}

	// Its supervision message goes to the main conversation, not the side one.
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "做完了，PR https://github.com/o/r/pull/7 已合并", 0)
	waitFor(t, "the main conversation's supervision message", func() bool {
		for _, n := range f.noticesTo("conv1") {
			if strings.Contains(n, "📋 督办") {
				return true
			}
		}
		return false
	})
	for _, n := range f.noticesTo("side1") {
		if strings.Contains(n, "📋 督办") {
			t.Fatalf("the side conversation got the supervision message:\n%s", n)
		}
	}

	// The main conversation accepts it; the side conversation hears it ended, once, with the task's id.
	if _, err := runJSON(t, s, "conv1", "task_review", map[string]any{"todo_id": todo.ID, "verdict": "accepted", "note": "PR 已合并",
		"checks": []map[string]any{{"criterion": "PR 合并到 main", "met": true, "evidence": "pull/7"}}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the side conversation's notice", func() bool { return len(endNotices(f, "side1")) == 1 })
	n := endNotices(f, "side1")[0]
	for _, want := range []string{"修登录崩溃", "已验收", todo.ID} {
		if !strings.Contains(n, want) {
			t.Errorf("the end notice lacks %q:\n%s", want, n)
		}
	}
	for _, n := range f.noticesTo("conv1") {
		if strings.Contains(n, "这段对话派出去的任务") {
			t.Fatalf("the main conversation got the planning conversation's end notice:\n%s", n)
		}
	}
}

// The main conversation's own tasks, and a leader's, are as before: no end notice anywhere, the supervisor is the
// conversation that planned them. A hub with no main conversation leaves a side conversation's task unsupervised.
func TestPlanSupervisorDefaults(t *testing.T) {
	s, _ := newQuietService(t)
	ctx := context.Background()
	setSettings(t, s, func(v *Settings) { v.Leaders = []string{"lead1"} })
	for _, c := range []struct {
		conv, parent, want string
		ok                 bool
	}{
		{"side1", "", "conv1", true},
		{"conv1", "", "", false}, // the main conversation supervises its own (supervises)
		{"side1", "box/s1", "", false},
		{"", "", "", false},
	} {
		if got, ok := s.defaultPlanSupervisor(ctx, c.conv, c.parent); got != c.want && ok || ok != c.ok {
			t.Errorf("defaultPlanSupervisor(%q, %q) = %q %v, want %q %v", c.conv, c.parent, got, ok, c.want, c.ok)
		}
	}
	setSettings(t, s, func(v *Settings) { v.Main = "" })
	if _, ok := s.defaultPlanSupervisor(ctx, "side1", ""); ok {
		t.Fatal("no main conversation, yet a supervisor")
	}
}

// endNotices are the task-end notices sent into conversation id.
func endNotices(f *fakeHub, id string) []string {
	var out []string
	for _, n := range f.noticesTo(id) {
		if strings.Contains(n, "这段对话派出去的任务") {
			out = append(out, n)
		}
	}
	return out
}
