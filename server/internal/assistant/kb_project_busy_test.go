package assistant

import (
	"context"
	"testing"
	"time"
)

// A project with an unfinished to-do filed under it is busy, so FinishProjects leaves it (#9427); a done to-do does not
// hold it.
func TestKBProjectBusyFollowsOpenTodos(t *testing.T) {
	s := &Service{}
	done := time.Now()
	s.todos.loaded = true
	s.todos.todos = map[string]*Todo{
		"t1": {ID: "t1", Project: "open-work"},
		"t2": {ID: "t2", Project: "finished-work", DoneAt: &done},
		"t3": {ID: "t3"},
	}
	busy := s.kbProjectBusy(context.Background())
	if !busy("open-work") || busy("finished-work") || busy("other") || busy("") {
		t.Fatalf("busy: open-work %v, finished-work %v, other %v", busy("open-work"), busy("finished-work"), busy("other"))
	}
}
