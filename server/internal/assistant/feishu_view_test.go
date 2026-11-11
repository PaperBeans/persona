package assistant

import (
	"context"
	"testing"
	"time"
)

// The settings page lists the Agents the owner has talked to in Lark (feishu.view's agents): one row per Agent, not
// one per message. The chat binds every card it posts about a conversation to it, so an Agent talked to for a while
// has dozens of threads; listed as they are, one Agent fills the section with its own copies and the others drop out
// of sight, keyed alike on both clients (web and iOS key those rows by session id).
func TestFeishuViewListsEachAgentOnce(t *testing.T) {
	s := newTestService(t, newFakeHub().withMachine())
	now := time.Unix(10_000_000, 0)
	s.fbot.now = func() time.Time { return now }
	ctx := context.Background()
	first := botTarget{Kind: targetAgent, MachineID: "server", SessionID: "agent01"}
	second := botTarget{Kind: targetAgent, MachineID: "server", SessionID: "agent02"}
	third := botTarget{Kind: targetAgent, MachineID: "server", SessionID: "agent03"}
	s.rememberBotThread("om_agents", first, "old name · server") // the list card "Chat here" was pressed on
	now = now.Add(time.Hour)
	s.rememberBotThread("om_progress1", first, "fix the login · server") // each card posted about the conversation
	now = now.Add(time.Hour)
	s.rememberBotThread("om_answer1", first, "fix the login · server")
	now = now.Add(time.Hour)
	s.rememberBotThread("om_second", second, "run the tests · server")
	now = now.Add(time.Hour)
	s.rememberBotThread("om_third", third, "write the report · server")
	now = now.Add(time.Hour)
	s.rememberBotThread("om_persona", botTarget{Kind: targetPersona, MachineID: "server", SessionID: "m1"}, "Main chat")

	// Newest first, named by the agent's newest thread, and the same on every load — the map the threads live in has
	// no order, so a list built from it as it comes out changes on every reload.
	want := []FeishuAgent{
		{Title: "write the report · server", MachineID: "server", SessionID: "agent03"},
		{Title: "run the tests · server", MachineID: "server", SessionID: "agent02"},
		{Title: "fix the login · server", MachineID: "server", SessionID: "agent01"},
	}
	for i := 0; i < 10; i++ {
		v := s.FeishuView(ctx)
		if len(v.Agents) != len(want) {
			t.Fatalf("load %d: agents = %+v, want one row for each of the three Agents", i, v.Agents)
		}
		for j, a := range v.Agents {
			if a != want[j] {
				t.Fatalf("load %d: agents = %+v, want %+v", i, v.Agents, want)
			}
		}
	}
}

// State written by hand or by an older version can bind threads with no time at all (pruneBotThreads documents that);
// those rows still keep one order on every load.
func TestFeishuViewOrdersRowsWithoutATime(t *testing.T) {
	s := newTestService(t, newFakeHub().withMachine())
	s.fbot.now = func() time.Time { return time.Time{} } // the bindings carry no time
	ctx := context.Background()
	for _, id := range []string{"agent02", "agent01", "agent03"} {
		s.rememberBotThread("om_"+id, botTarget{Kind: targetAgent, MachineID: "server", SessionID: id}, id+" · server")
	}

	for i := 0; i < 10; i++ {
		v := s.FeishuView(ctx)
		if len(v.Agents) != 3 || v.Agents[0].SessionID != "agent01" || v.Agents[1].SessionID != "agent02" || v.Agents[2].SessionID != "agent03" {
			t.Fatalf("load %d: agents = %+v, want agent01, agent02, agent03 every time", i, v.Agents)
		}
	}
}
