package assistant

import (
	"context"
	"encoding/json"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestInboxHandlingVisibilitySnapshot(t *testing.T) {
	ctx := context.Background()
	f := newFakeHub().withMachine()
	s := newTestService(t, f)
	setSettings(t, s, func(v *Settings) { v.Main = "main" })
	for id, origin := range map[string]string{
		"main": OriginInbox, "inbox": OriginInbox, "user": OriginUser,
		"channel": OriginChannel, "task": OriginTask, "spawned": OriginSpawned,
		"extension": OriginExtension, "lark-agent": OriginLarkAgent,
	} {
		s.originSet(ctx, "server", id, origin, "ref", "")
	}
	for _, v := range s.snapshot(ctx).Sessions {
		if want := v.SessionID == "inbox"; v.Archived != want {
			t.Errorf("%s: archived=%v, want %v", v.SessionID, v.Archived, want)
		}
	}
	// The default is derived from saved origins, including conversations from before the upgrade.
	s.origins.mu.Lock()
	s.origins.loaded = false
	s.origins.mu.Unlock()
	s.UserSpoke(ctx, SessionRef{MachineID: "server", SessionID: "inbox"})
	for _, v := range s.snapshot(ctx).Sessions {
		if v.SessionID == "inbox" && (!v.Archived || v.OriginRef != "ref") {
			t.Fatalf("continuing a recovered handling chat exposed it: %+v", v)
		}
	}
}

func TestInboxHandlingVisibilityListsAndLookup(t *testing.T) {
	ctx := context.Background()
	f := newFakeHub()
	f.hello.Assistant.MachineID = "box"
	f.hello.Assistant.Machines = []string{"box"}
	f.devices = []Device{{ID: "box", Home: "/home/u"}}
	d := newFleetDevs()
	d.add("box", "Computer")
	st := newFleetMem()
	s := New(Options{Hub: f, Devices: d, Store: func() Store { return st }})
	setSettings(t, s, func(v *Settings) { v.MachineID, v.Main = "box", "main" })
	d.setSessions("box", []proto.Session{
		{SessionID: "main", Cwd: "/home/u/.persona/assistant", Status: proto.StatusDone},
		{SessionID: "inbox", Cwd: "/home/u/.persona/assistant", Status: proto.StatusDone},
		{SessionID: "user", Cwd: "/home/u/.persona/assistant", Status: proto.StatusDone},
	})
	s.originSet(ctx, "box", "inbox", OriginInbox, "example:item", "")
	for _, list := range [][]proto.Session{
		s.view(ctx, false).Conversations,
		func() []proto.Session { list, _ := s.conversations(ctx, false, ""); return list }(),
	} {
		if len(list) != 2 {
			t.Fatalf("default list: %+v", list)
		}
		for _, c := range list {
			if c.SessionID == "inbox" {
				t.Fatal("the default list contains an Inbox handling chat")
			}
		}
	}
	if got := s.view(ctx, true).Conversations; len(got) != 3 {
		t.Fatalf("archived history lost the handling chat: %+v", got)
	}
	c, err := s.assistantConversation(ctx, "inbox")
	if err != nil || !c.Archived || c.OriginRef != "example:item" {
		t.Fatalf("the Inbox link cannot resolve its processing chat: %+v, %v", c, err)
	}
}

func TestInboxHandlingVisibilityKeepsInstructions(t *testing.T) {
	ctx := context.Background()
	h, _, it := handledHub(t)
	h.originSet(ctx, "server", "s1", OriginInbox, it.ID, "")
	if _, err := suggestTool(t, h, `{"kind":"conclusion","text":"Check the schedule","outcome":"fyi"}`); err != nil {
		t.Fatal(err)
	}
	code, _ := suggestionCall(t, h, it.ID, `{"version":1,"action":"instruct","text":"Check the deadline first"}`)
	if code != 200 {
		t.Fatalf("instruction returned %d", code)
	}
	toldConv(t, h, "Check the deadline first")
	h.inbox.mu.Lock()
	listed := it.listed()
	h.inbox.mu.Unlock()
	if listed.Agent.SessionID != "s1" || listed.Handling.State != HandlingLooking {
		t.Fatalf("the instruction did not continue the same chat: %+v", listed)
	}
	linked := false
	for _, e := range listed.Chain {
		if e.Link != nil && e.Link.SessionID == "s1" {
			linked = true
		}
	}
	if !linked {
		t.Fatal("the Inbox chain lost the processing-chat link")
	}
	h.UserSpoke(ctx, SessionRef{MachineID: "server", SessionID: "s1"})
	for _, v := range h.snapshot(ctx).Sessions {
		if v.SessionID == "s1" && !v.Archived {
			t.Fatal("an instruction brought the processing chat into the default list")
		}
	}
	// The item's API still carries its processing conversation and handling chain.
	code, detail := inboxCall(t, h.routes(), "GET", "/api/v1/inbox/"+it.ID, "")
	agent, ok := detail["agent"].(map[string]any)
	if code != 200 || !ok || agent["session_id"] != "s1" {
		t.Fatalf("item detail: %d %v", code, detail)
	}
}

func visibilityTestConfig() *assistantwire.InboxVisibilityConfig {
	return &assistantwire.InboxVisibilityConfig{Sources: []string{"lark"}, Chats: []string{"synthetic-private"}}
}

func TestInboxVisibilityMatch(t *testing.T) {
	newItem := func(chat string, direct bool) *InboxItem {
		it := actionItem("lark:visibility", time.Now())
		it.Source = "lark"
		it.Lark = &InboxLark{FeedID: chat, IsGroup: !direct}
		return it
	}
	cfg := visibilityTestConfig()
	if inboxVisibilityMatch(newItem("synthetic-work-dm", true), cfg) {
		t.Fatal("an explicit private-chat list excluded an unlisted work DM")
	}
	if !inboxVisibilityMatch(newItem("synthetic-private", false), cfg) {
		t.Fatal("an explicitly listed group was not excluded")
	}
	cfg = &assistantwire.InboxVisibilityConfig{Sources: []string{"lark"}, DirectChats: true}
	if !inboxVisibilityMatch(newItem("synthetic-work-dm", true), cfg) || inboxVisibilityMatch(newItem("synthetic-group", false), cfg) {
		t.Fatal("the direct-chat switch did not distinguish direct and group chats")
	}
	if !inboxVisibilityMatch(&InboxItem{Source: "lark", Kind: KindDM}, cfg) {
		t.Fatal("unknown Lark chat type did not fail closed with the direct-chat switch on")
	}
	var zero *assistantwire.InboxVisibilityConfig
	if inboxVisibilityMatch(newItem("synthetic-private", true), zero) {
		t.Fatal("nil visibility config changed legacy behavior")
	}
}

func TestInboxVisibilityQuarantineReleasesQueuedAndHandling(t *testing.T) {
	for _, running := range []bool{false, true} {
		name := "queued"
		if running {
			name = "handling"
		}
		t.Run(name, func(t *testing.T) {
			h := agentHub(t)
			it := actionItem("lark:synthetic-private:1", time.Now())
			it.Source = "lark"
			it.Lark.FeedID = "synthetic-private"
			h.inbox.items[it.ID] = it
			now := time.Now()
			if got := h.inbox.escalate(now.Add(-time.Hour), now); len(got) != 1 {
				t.Fatal("test item did not enter AI handling")
			}
			if running {
				it.Agent.State, it.Agent.SessionID = AgentRunning, "synthetic-session"
				h.inbox.setHandling(it, HandlingLooking, "", now)
			}
			h.quarantineInboxVisibility(context.Background(), visibilityTestConfig())
			if !it.VisibilityExcluded {
				t.Fatal("quarantine marker was not persisted on the item")
			}
			checkPolicyReleased(t, it, h.inbox.userLang())
			if running && (it.Agent.MachineID != "" || it.Agent.SessionID != "" || it.Handling.MachineID != "" || it.Handling.SessionID != "") {
				t.Fatalf("quarantine left a live handling session attached: agent=%+v handling=%+v", it.Agent, it.Handling)
			}
			before := len(conversationStarts(h.f))
			h.startInboxAgents(context.Background(), InboxAgentConfig{Enabled: true})
			if got := len(conversationStarts(h.f)); got != before {
				t.Fatalf("quarantined item started %d conversations", got-before)
			}
		})
	}
}

func TestInboxVisibilityQueuePassQuarantinesWithoutHandPass(t *testing.T) {
	h := agentHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.Visibility = visibilityTestConfig() })
	h.inbox.cfgMu.Lock()
	h.inbox.cfg = assistantwire.InboxConfig{}
	h.inbox.cfgAt = time.Now()
	h.inbox.cfgMu.Unlock()
	it := actionItem("lark:synthetic-private:direct-queue", time.Now())
	it.Source = "lark"
	it.Lark.FeedID = "synthetic-private"
	it.Agent = &InboxAgent{State: AgentQueued}
	h.inbox.items[it.ID] = it
	before := len(conversationStarts(h.f))
	h.startInboxAgents(context.Background(), InboxAgentConfig{Enabled: true})
	if !it.VisibilityExcluded {
		t.Fatal("queue pass did not persist the current visibility exclusion")
	}
	checkPolicyReleased(t, it, h.inbox.userLang())
	if got := len(conversationStarts(h.f)); got != before {
		t.Fatalf("newly excluded stored chat started %d handling conversations", got-before)
	}
}

func TestInboxVisibilityQuarantineBlocksExistingSuggestion(t *testing.T) {
	h, lark, it := handledHub(t)
	it.Source = "lark"
	it.Lark.FeedID = "synthetic-private"
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"Synthetic reply before quarantine"}`); err != nil {
		t.Fatal(err)
	}
	h.setConfig(func(c *assistantwire.InboxConfig) { c.Visibility = visibilityTestConfig() })
	h.quarantineInboxVisibility(context.Background(), visibilityTestConfig())
	if currentSuggestion(it) == nil {
		t.Fatal("quarantine deleted the suggestion instead of retaining its audit")
	}
	if _, err := h.actOnSuggestion(context.Background(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm}, true); err == nil {
		t.Fatal("quarantined suggestion was confirmed")
	}
	if len(lark.got) != 0 {
		t.Fatalf("quarantined suggestion called the sender: %+v", lark.got)
	}
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"Synthetic replacement"}`); err == nil {
		t.Fatal("quarantined item accepted a new suggestion")
	}
}

func TestInboxVisibilityFreshSuggestAndConfirm(t *testing.T) {
	h, lark, it := handledHub(t)
	it.Source = "lark"
	it.Lark.FeedID = "synthetic-private"
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"Synthetic reply before visibility changes"}`); err != nil {
		t.Fatal(err)
	}
	h.setConfig(func(c *assistantwire.InboxConfig) { c.Visibility = visibilityTestConfig() })
	h.inbox.cfgMu.Lock()
	h.inbox.cfg = assistantwire.InboxConfig{}
	h.inbox.cfgAt = time.Now()
	h.inbox.cfgMu.Unlock()
	g := currentSuggestion(it)
	if _, err := h.actOnSuggestion(context.Background(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm}, true); err == nil {
		t.Fatal("stale disabled cache confirmed a newly excluded reply")
	}
	if currentSuggestion(it) != g {
		t.Fatal("fresh visibility denial consumed the suggestion")
	}
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"Synthetic replacement through stale cache"}`); err == nil {
		t.Fatal("stale disabled cache published a reply for a newly excluded chat")
	}
	if len(lark.got) != 0 {
		t.Fatalf("fresh visibility denial called the sender: %+v", lark.got)
	}
}

func TestInboxVisibilityFreshNonReplySuggestion(t *testing.T) {
	t.Run("before association", func(t *testing.T) {
		h, _, it := handledHub(t)
		it.Source = "lark"
		it.Lark.FeedID = "synthetic-private"
		h.setConfig(func(c *assistantwire.InboxConfig) { c.Visibility = visibilityTestConfig() })
		h.inbox.cfgMu.Lock()
		h.inbox.cfg = assistantwire.InboxConfig{}
		h.inbox.cfgAt = time.Now()
		h.inbox.cfgMu.Unlock()
		if _, err := suggestTool(t, h, `{"kind":"conclusion","text":"Synthetic private conclusion","outcome":"fyi"}`); err == nil {
			t.Fatal("stale disabled cache published a conclusion for a newly excluded chat")
		}
		if currentSuggestion(it) != nil {
			t.Fatalf("newly excluded conclusion was stored: %+v", currentSuggestion(it))
		}
	})
	t.Run("after association wait", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := agentHub(t)
			oldWait := inboxDraftWait
			inboxDraftWait = time.Second
			t.Cleanup(func() { inboxDraftWait = oldWait })
			h.in.mu.Lock()
			h.in.configQueue = []assistantwire.InboxConfig{{}, {Visibility: visibilityTestConfig()}}
			h.in.mu.Unlock()
			it := actionItem("lark:visibility-association-wait", time.Now())
			it.Source = "lark"
			it.Lark.FeedID = "synthetic-private"
			it.Agent = &InboxAgent{State: AgentRunning, Task: "synthetic-task"}
			h.inbox.items[it.ID] = it
			h.inbox.waitHook = func() {
				h.inbox.mu.Lock()
				it.Agent.SessionID = "session-visibility-association"
				h.inbox.mu.Unlock()
			}
			_, _, err := h.suggest(context.Background(), "session-visibility-association", suggestIn{Kind: SuggestConclusion, Text: "Delayed private conclusion", Outcome: "fyi"})
			if err == nil {
				t.Fatal("conclusion used the pre-wait visibility configuration")
			}
			if currentSuggestion(it) != nil {
				t.Fatalf("newly excluded delayed conclusion was stored: %+v", currentSuggestion(it))
			}
		})
	})
}

func TestInboxVisibilityRecheckAfterReservationSave(t *testing.T) {
	h, lark, it := handledHub(t)
	it.Source = "lark"
	it.Lark.FeedID = "synthetic-private"
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"Synthetic reply before visibility changes"}`); err != nil {
		t.Fatal(err)
	}
	st := &recoveryBeforeWriteStore{inboxMemStore: h.st, before: func() {
		h.setConfig(func(c *assistantwire.InboxConfig) { c.Visibility = visibilityTestConfig() })
	}}
	h.store = func() Store { return st }
	if _, err := h.actOnSuggestion(context.Background(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm}, true); err == nil {
		t.Fatal("reply was confirmed after its chat became excluded during persistence")
	}
	if len(lark.got) != 0 {
		t.Fatalf("pre-send visibility recheck called the sender: %+v", lark.got)
	}
}

func TestInboxVisibilityFinalSendUsesFreshConfig(t *testing.T) {
	h, lark, it := handledHub(t)
	it.Source = "lark"
	it.Lark.FeedID = "synthetic-private"
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"Synthetic reply before the final visibility gate"}`); err != nil {
		t.Fatal(err)
	}
	h.inbox.cfgMu.Lock()
	h.inbox.cfg = assistantwire.InboxConfig{}
	h.inbox.cfgAt = time.Now()
	h.inbox.cfgMu.Unlock()
	h.in.mu.Lock()
	h.in.configQueue = []assistantwire.InboxConfig{
		{},                                   // confirmation before the edit-veto preflight
		{},                                   // confirmation after the veto
		{},                                   // reservation recheck
		{},                                   // per-item send-mutex recheck before the veto
		{Visibility: visibilityTestConfig()}, // final authorization immediately before the outward send
	}
	h.in.mu.Unlock()
	if _, err := h.actOnSuggestion(context.Background(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm}, true); err == nil {
		t.Fatal("reply bypassed the final fresh visibility gate")
	}
	if len(lark.got) != 0 {
		t.Fatalf("final fresh visibility denial called the sender: %+v", lark.got)
	}
}

func TestInboxVisibilityQuarantineKeepsSentAuditWithoutResending(t *testing.T) {
	h, lark, it := handledHub(t)
	it.Source = "lark"
	it.Lark.FeedID = "synthetic-private"
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"Synthetic already-sent reply"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.actOnSuggestion(context.Background(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm}, true); err != nil {
		t.Fatal(err)
	}
	if len(lark.got) != 1 {
		t.Fatalf("test setup sent %d replies, want one", len(lark.got))
	}
	a := suggestionAttempt(it, 1)
	if a == nil || a.Status != suggestionSent {
		t.Fatalf("test setup has no sent attempt: %+v", a)
	}
	text, proposal := a.Text, a.Proposal
	h.quarantineInboxVisibility(context.Background(), visibilityTestConfig())
	h.quarantineInboxVisibility(context.Background(), visibilityTestConfig())
	if !it.VisibilityExcluded || len(it.SuggestionAttempts) != 1 || a.Status != suggestionSent || a.Text != text || a.Proposal != proposal {
		t.Fatalf("sent audit changed during quarantine: %+v", it.SuggestionAttempts)
	}
	if len(lark.got) != 1 {
		t.Fatalf("quarantine re-sent the historical attempt: %+v", lark.got)
	}
}

func TestInboxVisibilityHandedSuggestionUsesFreshConfig(t *testing.T) {
	h, _, it := handoffHub(t)
	handOff(t, h, it)
	it.Source = "lark"
	it.Lark.FeedID = "synthetic-private"
	h.setConfig(func(c *assistantwire.InboxConfig) { c.Visibility = visibilityTestConfig() })
	h.inbox.cfgMu.Lock()
	h.inbox.cfg = assistantwire.InboxConfig{}
	h.inbox.cfgAt = time.Now()
	h.inbox.cfgMu.Unlock()
	if _, err := mainSuggest(h, "m1", `{"item":"lark:f1:100","kind":"agent","text":"Inspect the synthetic private request"}`); err == nil {
		t.Fatal("handed-off suggestion used the stale disabled visibility cache")
	}
	if currentSuggestion(it) != nil {
		t.Fatalf("newly excluded handed-off suggestion was published: %+v", currentSuggestion(it))
	}
}

func TestInboxVisibilityHandedAgentUsesFreshConfig(t *testing.T) {
	h := agentHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) {
		c.Agent.Enabled = true
		c.Visibility = visibilityTestConfig()
	})
	h.inbox.cfgMu.Lock()
	h.inbox.cfg = assistantwire.InboxConfig{}
	h.inbox.cfgAt = time.Now()
	h.inbox.cfgMu.Unlock()
	it := disabledTwinItem(h, "visibility-handed-agent", HandlingDone, AgentSkipped, "parent-session")
	it.Source = "lark"
	it.Lark = &InboxLark{FeedID: "synthetic-private", IsGroup: true}
	g := &InboxSuggestion{Version: 1, Kind: SuggestAgent, Text: "Inspect the synthetic private request", At: time.Now(), MachineID: "server"}
	if _, err := h.confirmHandedAgent(context.Background(), it, g, g.Text, false, SessionRef{MachineID: "server", SessionID: "handoff-session"}, "Synthetic"); err == nil {
		t.Fatal("handed-off Agent bypassed fresh visibility")
	}
	h.f.mu.Lock()
	starts := len(h.f.starts)
	h.f.mu.Unlock()
	if starts != 0 {
		t.Fatalf("newly excluded handed-off Agent started %d sessions", starts)
	}
}

func TestInboxVisibilityAgentUsesFreshConfig(t *testing.T) {
	h := agentHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) {
		c.Agent.Enabled = true
		c.Visibility = visibilityTestConfig()
	})
	h.inbox.cfgMu.Lock()
	h.inbox.cfg = assistantwire.InboxConfig{}
	h.inbox.cfgAt = time.Now()
	h.inbox.cfgMu.Unlock()
	it := disabledTwinItem(h, "visibility-normal-agent", HandlingDone, AgentSkipped, "parent-session")
	it.Source = "lark"
	it.Lark = &InboxLark{FeedID: "synthetic-private", IsGroup: true}
	g := &InboxSuggestion{Version: 1, Kind: SuggestAgent, Text: "Inspect the synthetic private request", At: time.Now(), MachineID: "server"}
	if _, err := h.confirmAgent(context.Background(), it, g, g.Text, false); err == nil {
		t.Fatal("normal Agent admission ignored the fresh visibility exclusion")
	}
	h.f.mu.Lock()
	starts := len(h.f.starts)
	h.f.mu.Unlock()
	if starts != 0 {
		t.Fatalf("newly excluded normal Agent started %d sessions", starts)
	}
}

func TestInboxVisibilityToolVetoBeforeConnectorCall(t *testing.T) {
	h, _, it := handledHub(t)
	ext := &policyExtensions{read: map[string]bool{"lark_messages": true, "lark_search": true}}
	h.SetExtensions(ext)
	h.setConfig(func(c *assistantwire.InboxConfig) {
		c.Agent.ReadProfile = "source_only"
		c.Agent.ConnectorTools = []string{"lark_messages", "lark_search"}
		c.Visibility = visibilityTestConfig()
	})
	h.originSet(t.Context(), "server", "s1", OriginInbox, it.ID, "")
	for _, call := range []struct {
		name string
		args string
	}{
		{name: "lark_messages", args: `{"chat_id":"synthetic-private"}`},
		{name: "lark_messages", args: `{"thread":"synthetic-unverifiable-thread"}`},
		{name: "lark_search", args: `{"query":"synthetic"}`},
	} {
		res := h.mcpCallWithOrigin(t.Context(), "server", "s1", call.name, json.RawMessage(call.args), true, OriginInbox)
		if !res.IsError {
			t.Errorf("visibility allowed %s %s: %+v", call.name, call.args, res)
		}
	}
	if len(ext.calls) != 0 {
		t.Fatalf("visibility vetoes reached the connector: %v", ext.calls)
	}
	res := h.mcpCallWithOrigin(t.Context(), "server", "s1", "lark_messages", json.RawMessage(`{"chat_id":"synthetic-work-dm"}`), true, OriginInbox)
	if res.IsError || len(ext.calls) != 1 {
		t.Fatalf("unlisted work DM did not retain its read path: %+v, calls=%v", res, ext.calls)
	}
	// A valid display cache saying "disabled" is not authorization to bypass a newly enabled boundary.
	h.inbox.cfgMu.Lock()
	h.inbox.cfg = assistantwire.InboxConfig{}
	h.inbox.cfgAt = time.Now()
	h.inbox.cfgMu.Unlock()
	res = h.mcpCallWithOrigin(t.Context(), "server", "s1", "lark_messages", json.RawMessage(`{"chat_id":"synthetic-private"}`), true, OriginInbox)
	if !res.IsError || len(ext.calls) != 1 {
		t.Fatalf("tool veto used the stale display cache instead of fresh visibility: %+v, calls=%v", res, ext.calls)
	}
}

func TestInboxVisibilityTriageVetoBeforeModel(t *testing.T) {
	for _, stored := range []bool{false, true} {
		name := "new admission"
		if stored {
			name = "stored retriage"
		}
		t.Run(name, func(t *testing.T) {
			h := newInboxHub(t)
			model, calls := captureModel(t, `{"tier":"fyi","summary":"should not be used","reason":"should not be used"}`)
			h.in.setModel(model)
			h.setConfig(func(c *assistantwire.InboxConfig) { c.Visibility = visibilityTestConfig() })
			it := actionItem("lark:synthetic-private:triage", time.Now())
			it.Source = "lark"
			it.Lark.FeedID = "synthetic-private"
			it.Context = "SECRET-PRIVATE-TRIAGE"
			it.TriagedAt = ""
			if stored {
				h.inbox.items[it.ID] = it
				if w := h.triage(t.Context(), it); w != nil {
					t.Fatal("excluded stored item produced a triage write")
				}
			} else {
				batch := newInboxBatch()
				batch.from[it.ID] = "lark"
				batch.read["lark"] = "Synthetic Lark"
				h.admit(t.Context(), batch, []*InboxItem{it})
			}
			if !it.VisibilityExcluded || it.Tier != TierAction {
				t.Fatalf("excluded item was not retained for the owner without AI: %+v", it)
			}
			if got := len(calls()); got != 0 {
				t.Fatalf("excluded content reached triage in %d model calls", got)
			}
		})
	}
}

func TestInboxVisibilityReplyClassifierVetoBeforeModel(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		t.Run(map[bool]string{false: "current config", true: "persistent marker"}[persistent], func(t *testing.T) {
			h := newInboxHub(t)
			model, calls := captureModel(t, `{"category":"ordinary_coordination"}`)
			h.in.setModel(model)
			policy := legacyReplyPolicyTestConfig()
			h.setConfig(func(c *assistantwire.InboxConfig) {
				c.ReplyPolicy = policy
				if !persistent {
					c.Visibility = visibilityTestConfig()
				}
			})
			it := actionItem("lark:synthetic-private:classifier", time.Now())
			it.Source = "lark"
			it.Lark.FeedID = "synthetic-private"
			it.Context = "SECRET-PRIVATE-CLASSIFIER"
			it.VisibilityExcluded = persistent
			h.inbox.items[it.ID] = it
			h.classifyInboxReplyPolicies(t.Context(), policy)
			if !it.VisibilityExcluded || it.ReplyCategory != "" {
				t.Fatalf("excluded item was classified for owner drafting: %+v", it)
			}
			if got := len(calls()); got != 0 {
				t.Fatalf("excluded content reached reply classification in %d model calls", got)
			}
		})
	}
}

func TestInboxVisibilityFolderVetoBeforeModel(t *testing.T) {
	h := newInboxHub(t)
	model, calls := captureModel(t, `{"folders":{}}`)
	h.in.setModel(model)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.Visibility = visibilityTestConfig() })
	it := actionItem("lark:synthetic-private:folder", time.Now())
	it.Source = "lark"
	it.Lark.FeedID = "synthetic-private"
	it.Preview = "SECRET-PRIVATE-FOLDER"
	it.FolderOf = ""
	h.inbox.items[it.ID] = it
	if err := h.fileBatch(t.Context(), []*InboxItem{it}); err != nil {
		t.Fatal(err)
	}
	if !it.VisibilityExcluded {
		t.Fatal("folder pass did not quarantine the excluded item")
	}
	if got := len(calls()); got != 0 {
		t.Fatalf("excluded preview reached folder classification in %d model calls", got)
	}
}

func TestInboxVisibilityPendingDeliveryUsesFreshConfig(t *testing.T) {
	h, _, it := handledHub(t)
	it.Source = "lark"
	it.Lark.FeedID = "synthetic-private"
	h.originSet(t.Context(), "server", "s1", OriginInbox, it.ID, "")
	h.inbox.mu.Lock()
	h.inbox.tell(it, inboxNote{Name: prompts.NoteNewMessages, Context: "SECRET-PRIVATE-PENDING"}, true, time.Now())
	h.inbox.mu.Unlock()
	h.setConfig(func(c *assistantwire.InboxConfig) { c.Visibility = visibilityTestConfig() })
	h.inbox.cfgMu.Lock()
	h.inbox.cfg = assistantwire.InboxConfig{}
	h.inbox.cfgAt = time.Now()
	h.inbox.cfgMu.Unlock()
	h.deliverNotes(t.Context(), it)
	h.f.mu.Lock()
	sent := len(h.f.agentSent)
	h.f.mu.Unlock()
	if sent != 0 {
		t.Fatalf("newly excluded pending content was delivered in %d session sends", sent)
	}
	if !it.VisibilityExcluded || it.Agent.SessionID != "" {
		t.Fatalf("fresh delivery veto did not quarantine and detach the handling session: %+v", it.Agent)
	}
}
