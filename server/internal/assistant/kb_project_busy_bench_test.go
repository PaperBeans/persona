package assistant

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/kb"
)

// The archive lane builds FinishProjects' busy every round, and live messages start a round every few seconds. A
// knowledge base with about 80 projects and a few hundred open to-dos filed under them is a real size.
const (
	busyBenchProjects = 80
	busyBenchTodos    = 300
)

func BenchmarkKBProjectBusy(b *testing.B) {
	st, err := kb.NewStore(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	e := &kb.Engine{Store: st}
	for i := range busyBenchProjects {
		p := &kb.Project{Slug: fmt.Sprintf("project-%d", i), Name: fmt.Sprintf("Project %d", i), Status: "active",
			Aliases: []string{fmt.Sprintf("alias %d a", i), fmt.Sprintf("alias %d b", i)}, OneLine: "work on a thing"}
		if i%10 == 9 {
			p.Status, p.MergedInto = "merged", fmt.Sprintf("project-%d", i-1)
		}
		if err := st.PutProject(p); err != nil {
			b.Fatal(err)
		}
	}
	s := &Service{}
	s.todos.loaded = true
	s.todos.todos = map[string]*Todo{}
	done := time.Now()
	for i := range busyBenchTodos {
		t := &Todo{ID: fmt.Sprintf("t%d", i), Project: fmt.Sprintf("project-%d", i%(busyBenchProjects+20))}
		if i%3 == 0 {
			t.DoneAt = &done
		}
		s.todos.todos[t.ID] = t
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if busy := s.kbProjectBusy(ctx, e); !busy("project-1") {
			b.Fatal("project-1 has open to-dos")
		}
	}
}
