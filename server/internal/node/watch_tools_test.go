package node

import (
	"context"
	"path/filepath"
	"testing"
)

func TestStoredToolsShowOnlyCurrentUnfinishedCalls(t *testing.T) {
	home := t.TempDir()
	item := [2]string{"commandExecution", `{"id":"cmd1","status":"inProgress","command":"sleep 4"}`}
	turn := historyTurn{thread: appChat, id: "t1", status: "inProgress", ordinal: 1, items: [][2]string{item, {"reasoning", `{"id":"r1","status":"inProgress"}`}}}
	writeCodexHistory(t, home, []historyTurn{{thread: appChat, id: "older", status: "inProgress", items: [][2]string{item}}, turn})
	query := func() (int, bool, bool) {
		s, err := storedLiveTools(context.Background(), filepath.Join(home, "thread_history_1.sqlite"), codexAppThread{ID: appChat})
		if err != nil {
			t.Fatal(err)
		}
		if len(s.Tools) > 0 && (s.Tools[0].ID != "cmd1" || s.Tools[0].Name != "Bash") {
			t.Fatal(s.Tools)
		}
		return len(s.Tools), s.Active, s.Thinking
	}
	if n, a, r := query(); n != 1 || !a || !r {
		t.Fatalf("before return: %d %v %v", n, a, r)
	}
	turn.status = "completed"
	writeCodexHistory(t, home, []historyTurn{turn})
	if n, a, r := query(); n != 0 || a || r {
		t.Fatalf("after return: %d %v %v", n, a, r)
	}
}
