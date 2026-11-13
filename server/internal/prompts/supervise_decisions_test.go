package prompts

import (
	"strings"
	"testing"
)

func TestSuperviseDecisionInstructionsAppearOnce(t *testing.T) {
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
			for _, decisions := range []bool{false, true} {
				data := role.data
				data.Decisions = decisions
				data.Entries = []SuperviseEntry{{ID: "todo_example", Title: "Example task", Kind: "recheck"}}
				text := RenderSupervise(data)
				want := 0
				if decisions {
					want = 1
				}
				// i18n-ignore: exact instructions in the runtime-bound supervision prompt.
				for _, instruction := range []string{
					"- 决定交回你核对的：", "- 用户那边没有能回答的东西的：",
					"- 用户回「你来定」的：", "- 用户回「这不是我要的」的：", "- 用户回「先讲清背景」的：",
				} {
					if got := strings.Count(text, instruction); got != want {
						t.Errorf("Decisions=%v: %q occurs %d times, want %d", decisions, instruction, got, want)
					}
				}
				if got := strings.Count(text, "todo_decision_add"); got != want {
					t.Errorf("Decisions=%v: decision recording instruction occurs %d times, want %d", decisions, got, want)
				}
				// i18n-ignore: the surviving decision policy must still require the owner's approval.
				approval := "标着「不会默认执行」的（不可撤回，或别人看得到）你不能替用户走这一步"
				if got := strings.Count(text, approval); got != want {
					t.Errorf("Decisions=%v: approval restriction occurs %d times, want %d", decisions, got, want)
				}
			}
		})
	}
}
