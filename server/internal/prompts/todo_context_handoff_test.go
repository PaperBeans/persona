package prompts

import (
	"strings"
	"testing"
)

func TestRenderTodoContextHandoff(t *testing.T) {
	got := RenderTodoContextHandoff(TodoContextHandoff{
		HandoffID:      "handoff_1234",
		OperationID:    "op_abcd",
		TodoID:         "todo_1",
		TodoTitle:      "Ship auth fix",
		Strategy:       "fresh_full_summary",
		Generator:      "deterministic_v1",
		SourceMachine:  "box",
		SourceSession:  "sess_old",
		SourceLauncher: "claude",
		SourceAgent:    "claude",
		TargetMachine:  "box",
		TargetLauncher: "codex",
		TargetAgent:    "codex",
		BriefRevision:  "brief_1@2026-11-23T00:00:00Z",
		KnownFailure:   "API Error: 503 upstream unavailable",
		Goal:           "Fix token refresh race",
		Request:        "Please fix the refresh race and add tests",
		Criteria:       []string{"go test passes"},
		OutOfBounds:    []string{"Do not change public API"},
		Decisions:      []TodoContextDecision{{By: "user", Question: "Storage?", Choice: "Postgres", Reason: "Durable"}},
		SourceRanges:   []TodoContextSourceRange{{MachineID: "box", SessionID: "sess_old", Agent: "claude", FromSeq: 1, ToSeq: 12, ItemCount: 12}},
		Files:          []string{"server/auth.go"},
		Commands:       []string{"go test ./... (exit 1)"},
		Failed:         []string{"TestRefreshRace"},
		RecentTurns:    []TodoContextTurn{{Speaker: "user", Text: "Fix it"}, {Speaker: "assistant", Text: "Found race in refresh"}},
		DiffScope:      "uncommitted",
		DiffBase:       "abc1234 (HEAD)",
		DiffHead:       "abc1234",
		DiffBranch:     "fix/auth",
		DiffFiles:      []string{"modified server/auth.go (+12/-3)"},
		MissingSources: []TodoContextGap{{Source: "transcript", Reason: "truncated to latest 200 items"}},
	})
	for _, want := range []string{
		"[To-do context handoff · internal context]",
		"Handoff ID: handoff_1234",
		"operation op_abcd",
		"strategy fresh_full_summary",
		"generator deterministic_v1",
		"Brief revision: brief_1@2026-11-23T00:00:00Z",
		"API Error: 503 upstream unavailable",
		"Fix token refresh race",
		"go test passes",
		"[user] Storage? -> Postgres (Durable)",
		"seq 1..12 (12 items)",
		"Diff scope: uncommitted · base abc1234 (HEAD)",
		"modified server/auth.go (+12/-3)",
		"transcript: truncated to latest 200 items",
		"== End of to-do context handoff; the task prompt follows ==",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered handoff lacks %q:\n%s", want, got)
		}
	}
}
