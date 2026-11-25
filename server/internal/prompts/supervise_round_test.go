package prompts

import (
	"strings"
	"testing"
)

// What only a Fresh message carries. i18n-ignore: exact generated prompt text.
const (
	freshHeader = "这一轮通常从干净的上下文开始"
	lastReview  = "上次结论（"
)

func background(fresh bool, entries ...SuperviseEntry) Supervise {
	return Supervise{Leader: true, Background: true, Fresh: fresh, Entries: entries}
}

func supervisionCriteriaEntry() SuperviseEntry {
	return SuperviseEntry{
		ID:    "todo_example",
		Title: "缩短督办消息",
		Kind:  "ask",
		Link:  "https://hub.example/todos/todo_example",
		DoneWhen: []string{
			"督办消息不再逐字重复任务里的验收标准，只保留固定短指引；主管需要核对原文时，可以通过待办详情或 todo_get 按需读取，并继续逐条填写验收结果和证据。",
			"任务标题、任务链接、signal、Agent 当前状态以及需要处理的事项必须完整保留，不能因为缩短正文而丢掉路由信息、等待时长或下一步操作。",
			"最初的要求继续沿用既有的两百字截断逻辑，长原文只展示开头并说明全文位置；本次修改不得改变任务数据、范围调整记录或 task_review 的逐项验收行为。",
			"ask 和 needs_user 卡片上的问题、选项、推荐与等待代价保持原样；补充新格式测试，保留任务链接和待办事项断言，并运行相关包测试与构建且真实 exit 0。",
		},
		Asked:   "把督办消息里的验收标准改成短指引，其他内容保持。",
		AskedAt: "2026-11-25",
		Asks: []SuperviseAsk{{
			ID: "ix1", Kind: "question", Machine: "builder", Session: "s1",
			Text: "请选择下一步：\n1. 继续修改\n2. 保持现状",
		}},
	}
}

// A supervision message points to the criteria instead of repeating them. Everything the supervisor needs to act on
// the signal, including an ask card's exact question and options, stays in the message.
func TestSuperviseCriteriaUseTodoPointer(t *testing.T) {
	entry := supervisionCriteriaEntry()
	text := RenderSupervise(Supervise{Entries: []SuperviseEntry{entry}})
	for _, want := range []string{
		"任务「缩短督办消息」（todo_example，signal=ask，链接 https://hub.example/todos/todo_example）",
		"它的 Agent 有卡片在等，先交给你处理，用户还没收到",
		"验收标准用 todo_get 取原文",
		"请选择下一步：\n     1. 继续修改\n     2. 保持现状",
		"最初的要求（2026-11-25 用户原话）：把督办消息里的验收标准改成短指引，其他内容保持。",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("message lacks %q:\n%s", want, text)
		}
	}
	for _, criterion := range entry.DoneWhen {
		if strings.Contains(text, criterion) {
			t.Errorf("message repeats criterion %q:\n%s", criterion, text)
		}
	}
	if strings.Count(text, "验收标准用 todo_get 取原文") != 1 {
		t.Errorf("criteria pointer should appear exactly once:\n%s", text)
	}

	withoutCriteria := entry
	withoutCriteria.DoneWhen = nil
	emptyText := RenderSupervise(Supervise{Entries: []SuperviseEntry{withoutCriteria}})
	if !strings.Contains(emptyText, "验收标准：未记录") || strings.Contains(emptyText, "验收标准用 todo_get 取原文") {
		t.Errorf("an entry without criteria should keep its short empty state:\n%s", emptyText)
	}
}

// A Fresh round is the same facts as the message that is not Fresh plus its header line, marker and last reviews: the
// handling rules are in neither (they are the static supervision instructions), whatever kinds its entries are.
func TestSuperviseFreshAddsOnlyTheRoundsFacts(t *testing.T) {
	for _, entry := range []SuperviseEntry{
		{ID: "t1", Title: "Example task", Kind: "idle"},
		{ID: "t1", Title: "Example task", Kind: "review"},
		{ID: "t1", Title: "Example task", Kind: "report", Report: "done"},
		{ID: "t1", Title: "Example task", Kind: "idle", Closeout: &SuperviseCloseout{Status: "done"}},
		{ID: "t1", Title: "Example task", Kind: "stalled", Minutes: 30},
		{ID: "t1", Title: "Example task", Kind: "failed"},
	} {
		fresh := RenderSupervise(background(true, entry))
		whole := RenderSupervise(background(false, entry))
		for _, banned := range []string{"怎么处理", "task_review", "agent_send", "interaction_answer", "supervision_finish", "task_scope"} {
			if strings.Contains(fresh, banned) {
				t.Errorf("%s: a Fresh round carries handling policy %q:\n%s", entry.Kind, banned, fresh)
			}
		}
		if !strings.Contains(fresh, "signal="+entry.Kind) {
			t.Errorf("%s: the round lost its signal:\n%s", entry.Kind, fresh)
		}
		lines := strings.Split(fresh, "\n")
		var kept []string
		for _, l := range lines {
			if !strings.HasPrefix(l, freshHeader) && l != SuperviseRoundMarker {
				kept = append(kept, l)
			}
		}
		if strings.Join(kept, "\n") != whole {
			t.Errorf("%s: a Fresh round differs from the whole message by more than its header and marker:\n%s\n---\n%s", entry.Kind, fresh, whole)
		}
		// Every Fresh message is still one ParseInjected reads as supervision, at its level.
		if in := ParseInjected(fresh); in == nil || in.State != "supervise" || in.Level != "triage" {
			t.Fatalf("not read back as a supervision message: %+v", in)
		}
	}
}

// Not Fresh, a message is as before #7315 (the goldens in internal/assistant/testdata hold the exact bytes), with no
// header line, marker or last review.
func TestSuperviseNotFreshIsUnchanged(t *testing.T) {
	entry := SuperviseEntry{ID: "t1", Title: "Example task", Kind: "idle", LastReview: &SuperviseLastReview{Verdict: "rework", At: "11-14 10:20", Note: "add the tests"}}
	for _, role := range []struct {
		name string
		data Supervise
	}{
		{"main", Supervise{}},
		{"side", Supervise{Side: true}},
		{"leader", Supervise{Leader: true}},
		{"background", Supervise{Leader: true, Background: true}},
	} {
		t.Run(role.name, func(t *testing.T) {
			data := role.data
			data.Entries = []SuperviseEntry{entry}
			text := RenderSupervise(data)
			for _, n := range []string{freshHeader, SuperviseRoundMarker, lastReview} {
				if strings.Contains(text, n) {
					t.Errorf("%q is in the message:\n%s", n, text)
				}
			}
			data.Entries[0].LastReview = nil
			if again := RenderSupervise(data); again != text {
				t.Fatal("the last review changed a message that is not Fresh")
			}
			if IsSuperviseRound(text) {
				t.Fatal("a message that is not Fresh reads as a round")
			}
		})
	}
}

// A Fresh message says where earlier rounds' decisions are, carries the marker line after its level tag and each
// entry's last review; IsSuperviseRound is true only for such a supervision message.
func TestSuperviseRoundMarker(t *testing.T) {
	text := RenderSupervise(Supervise{Level: "info", Leader: true, Background: true, Fresh: true, Entries: []SuperviseEntry{
		{ID: "t1", Title: "Example task", Kind: "review", LastReview: &SuperviseLastReview{Verdict: "rework", At: "11-14 10:20", Note: "add the tests"}},
		{ID: "t2", Title: "Other task", Kind: "idle"},
	}})
	lines := strings.Split(text, "\n")
	if !strings.HasPrefix(lines[2], freshHeader) || lines[3] != `<persona-supervision level="info" />` || lines[4] != SuperviseRoundMarker {
		t.Fatalf("header, level tag and marker:\n%s", strings.Join(lines[:6], "\n"))
	}
	if !strings.Contains(text, "\n   "+lastReview+"11-14 10:20）：rework，add the tests\n") || strings.Count(text, lastReview) != 1 {
		t.Fatalf("the last review:\n%s", text)
	}
	if in := ParseInjected(text); in == nil || in.State != "supervise" || in.Level != "info" {
		t.Fatalf("ParseInjected: %+v", in)
	}
	if !IsSuperviseRound(text) || !IsSuperviseRound("<persona-context>\n- 现在：x\n</persona-context>\n"+text) {
		t.Fatal("a round is not read as one")
	}
	for _, other := range []string{
		"hello " + SuperviseRoundMarker,
		"Look:\n" + SuperviseRoundMarker + "\nis a tag",
		RenderSupervise(Supervise{Entries: []SuperviseEntry{{ID: "t1", Title: "Example task", Kind: "idle"}}}),
	} {
		if IsSuperviseRound(other) {
			t.Fatalf("read as a round: %q", other)
		}
	}
}
