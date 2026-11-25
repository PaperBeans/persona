package node

import (
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// An agy conversation keeps the ids it was listed under (#14395): while a pane shows it, the pane's row names the
// companion's id for it; once the pane is gone, the companion's row names the pane's; another pane's row names both.
// A pane that runs another conversation now is that one's row, and a pane listed beside the companion's row (agy quit
// in it) is named by neither.
func TestAgyConversationAliases(t *testing.T) {
	n, herdr, home := herdrNode(t)
	const (
		convRow    = "agy-" + agyTestID
		otherRow   = "agy-" + agyOtherTestID
		secondPane = "w1:p2"
		secondRow  = "agy-w1-p2"
	)
	n.runtimes = newRuntimeRegistry()
	runtime := n.runtimes.add(nil)
	runtime.ready, runtime.hello.Runtime = true, agyAgent
	n.runtimes.setSessions(runtime, []proto.Session{
		{SessionID: convRow, Agent: agyAgent, Cwd: home, Status: proto.StatusIdle, Title: "Release notes"},
		{SessionID: otherRow, Agent: agyAgent, Cwd: home, Status: proto.StatusIdle, Title: "Login page"},
	})
	stopped := func(pane, id string) any {
		a := agyTestAgent(pane, home, id)
		a["agent_status"] = proto.StatusOffline
		return a
	}
	for _, step := range []struct {
		name   string
		agents []any
		want   map[string][]string
	}{
		{"a pane shows it", []any{agyTestAgent(agyTestPane, home, agyTestID)},
			map[string][]string{agyTestRow: {convRow}, otherRow: nil}},
		{"the pane closed", nil,
			map[string][]string{convRow: {agyTestRow}, otherRow: nil}},
		{"another pane shows it", []any{agyTestAgent(secondPane, home, agyTestID)},
			map[string][]string{secondRow: {convRow, agyTestRow}, otherRow: nil}},
		{"the first pane runs another conversation", []any{agyTestAgent(agyTestPane, home, agyOtherTestID), agyTestAgent(secondPane, home, agyTestID)},
			map[string][]string{agyTestRow: {otherRow}, secondRow: {convRow}}},
		{"agy quit in the second pane", []any{stopped(secondPane, agyTestID)},
			map[string][]string{secondRow: nil, convRow: nil, otherRow: {agyTestRow}}},
	} {
		herdr.reply("session.snapshot", snapshot(nil, nil, step.agents, nil))
		got := map[string][]string{}
		for _, row := range listSessions(t, n) {
			if row.Agent == agyAgent {
				got[row.SessionID] = row.Aliases()
			}
		}
		if !maps.EqualFunc(got, step.want, slices.Equal) {
			t.Fatalf("%s: aliases %v, want %v", step.name, got, step.want)
		}
	}
}

// The node remembers the agyShownMost pane rows it listed last, and a row names the most recently listed of those that
// showed its conversation, at most agyAliasesMost, in an order that does not change from one list to the next.
func TestAgyAliasesNameTheRecentPanes(t *testing.T) {
	n := &Node{}
	at := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	pane := func(i int) string { return fmt.Sprintf("agy-w1-p%d", i) }
	for i := range agyShownMost + 2 {
		n.rememberAgyPanes(map[string]string{pane(i): agyTestID}, at.Add(time.Duration(i)*time.Second))
	}
	if len(n.agyShown) != agyShownMost {
		t.Fatalf("%d panes remembered, want %d", len(n.agyShown), agyShownMost)
	}
	for _, i := range []int{0, 1} {
		if _, ok := n.agyShown[pane(i)]; ok {
			t.Fatalf("the oldest pane %s was kept", pane(i))
		}
	}
	row := func() []proto.Session {
		list := []proto.Session{{SessionID: "agy-" + agyTestID, Agent: agyAgent}, {SessionID: pane(agyShownMost), Agent: agyAgent}}
		n.nameAgyAliases(list, nil)
		return list
	}
	list := row()
	var want []string
	for i := agyShownMost + 1; len(want) < agyAliasesMost; i-- {
		if i != agyShownMost { // listed: its row's id, not an alias
			want = append(want, pane(i))
		}
	}
	if got := list[0].Aliases(); !slices.Equal(got, want) {
		t.Fatalf("aliases %v, want %v", got, want)
	}
	if got := list[1].Aliases(); got != nil {
		t.Fatalf("a pane listed under its own id named %v", got)
	}
	if listKey(row()) != listKey(list) {
		t.Fatal("the same panes named again changed the list key")
	}
}

// A change of aliases alone changes the list key (#14395): the manager gets a row's new aliases with no other change.
func TestListKeyNamesAliases(t *testing.T) {
	key := func(aliases ...string) string {
		s := proto.Session{SessionID: agyTestRow, Agent: agyAgent}
		s.SetStartedAt(time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)) // a hierarchy with or without aliases
		s.SetAliases(aliases)
		return listKey([]proto.Session{s})
	}
	conv := "agy-" + agyTestID
	if key(conv) != key(conv) {
		t.Fatal("the same aliases keyed differently")
	}
	for _, other := range [][]string{nil, {"agy-" + agyOtherTestID}, {conv, "agy-w1-p2"}} {
		if key(other...) == key(conv) {
			t.Fatalf("aliases %v keyed as %v", other, []string{conv})
		}
	}
}
