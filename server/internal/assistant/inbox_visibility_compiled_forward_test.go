package assistant

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistant/repository"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

func compiledReplyPolicyTestConfig() *assistantwire.InboxReplyPolicyConfig {
	return &assistantwire.InboxReplyPolicyConfig{
		Enabled:  true,
		Revision: "1",
		Categories: map[string]string{
			ReplyCategoryRoutineReply: assistantwire.ReplyRouteOwnerDraft,
		},
	}
}

func TestInboxVisibilityBlocksCompiledReplyPolicyClassification(t *testing.T) {
	t.Run("handPass gateReplyPolicyQueue skips excluded DM and classifies allowed group", func(t *testing.T) {
		h := agentHub(t)
		model, calls := captureModel(t, `{"category":"routine_reply","reason":"synthetic routine"}`)
		h.in.setModel(model)
		h.setConfig(func(c *assistantwire.InboxConfig) {
			c.Agent.Enabled = true
			c.ReplyPolicy = compiledReplyPolicyTestConfig()
			c.Visibility = &assistantwire.InboxVisibilityConfig{
				Sources:     []string{"lark"},
				DirectChats: true,
			}
		})

		now := time.Now()
		dm := actionItem("lark:synthetic-dm:compiled-queue", now)
		dm.Source = "lark"
		dm.Lark = &InboxLark{FeedID: "synthetic-private-dm", IsGroup: false}
		dm.Title = "SECRET-DM-TITLE"
		dm.Context = "SECRET-PRIVATE-DM-QUEUE-BODY"

		group := actionItem("lark:synthetic-group:compiled-queue", now)
		group.Source = "lark"
		group.Lark = &InboxLark{FeedID: "synthetic-allowed-group", IsGroup: true}
		group.Title = "ORDINARY-GROUP-TITLE"
		group.Context = "ORDINARY-GROUP-QUEUE-BODY"

		h.inbox.mu.Lock()
		h.inbox.items[dm.ID] = dm
		h.inbox.items[group.ID] = group
		h.inbox.mu.Unlock()

		h.handPass(t.Context())

		for _, call := range calls() {
			if strings.Contains(call.System+"\n"+call.User, "SECRET-PRIVATE-DM-QUEUE-BODY") ||
				strings.Contains(call.System+"\n"+call.User, "SECRET-DM-TITLE") {
				t.Fatalf("excluded private DM body leaked to compiled reply_policy classifier: %+v", call)
			}
		}
		h.inbox.mu.Lock()
		dmExcluded, dmSnap := dm.VisibilityExcluded, dm.ReplyPolicy
		groupSnap := group.ReplyPolicy
		h.inbox.mu.Unlock()
		if !dmExcluded {
			t.Fatal("excluded private DM was not quarantined")
		}
		if dmSnap != nil {
			t.Fatalf("excluded private DM received reply_policy snapshot: %+v", dmSnap)
		}
		if groupSnap == nil || groupSnap.Category != ReplyCategoryRoutineReply {
			t.Fatalf("allowed group did not receive compiled reply_policy snapshot: %+v", groupSnap)
		}
	})

	entrypoints := []struct {
		name string
		run  func(*testing.T, *inboxHub, *assistantwire.InboxReplyPolicyConfig, *InboxItem)
	}{
		{
			name: "classifyInboxReplyPolicies",
			run: func(t *testing.T, h *inboxHub, cfg *assistantwire.InboxReplyPolicyConfig, _ *InboxItem) {
				h.classifyInboxReplyPolicies(t.Context(), cfg)
			},
		},
		{
			name: "classifyInboxReplyPolicyItem",
			run: func(t *testing.T, h *inboxHub, cfg *assistantwire.InboxReplyPolicyConfig, it *InboxItem) {
				if err := h.classifyInboxReplyPolicyItem(t.Context(), cfg, it); err != nil {
					t.Fatalf("classifyInboxReplyPolicyItem returned unexpected error: %v", err)
				}
			},
		},
		{
			name: "gateReplyPolicyQueue",
			run: func(t *testing.T, h *inboxHub, cfg *assistantwire.InboxReplyPolicyConfig, _ *InboxItem) {
				h.gateReplyPolicyQueue(t.Context(), compileReplyPolicy(cfg), time.Now().Add(-time.Hour))
			},
		},
	}

	cases := []struct {
		name    string
		setup   func(*inboxHub, *InboxItem, *assistantwire.InboxReplyPolicyConfig)
		wantErr bool
	}{
		{
			name: "persistent marker",
			setup: func(_ *inboxHub, it *InboxItem, _ *assistantwire.InboxReplyPolicyConfig) {
				it.VisibilityExcluded = true
			},
		},
		{
			name: "fresh config exclusion before quarantine",
			setup: func(h *inboxHub, it *InboxItem, cfg *assistantwire.InboxReplyPolicyConfig) {
				it.Lark = &InboxLark{FeedID: "synthetic-private", IsGroup: false}
				h.setConfig(func(c *assistantwire.InboxConfig) {
					c.ReplyPolicy = cfg
					c.Visibility = visibilityTestConfig()
				})
			},
		},
		{
			name: "historical mixed merge",
			setup: func(h *inboxHub, it *InboxItem, _ *assistantwire.InboxReplyPolicyConfig) {
				it.Lark = &InboxLark{FeedID: "synthetic-allowed", IsGroup: true}
				linkVisibilityTestMerge(h, it, visibilityTestMergeMember(it))
			},
		},
		{
			name: "fresh config read error fails closed without model call",
			setup: func(h *inboxHub, _ *InboxItem, cfg *assistantwire.InboxReplyPolicyConfig) {
				// Even if unknown is explicitly mapped to owner_draft, a visibility outage must fail closed to owner_only.
				cfg.Categories["unknown"] = assistantwire.ReplyRouteOwnerDraft
				h.in.mu.Lock()
				h.in.configErr = errors.New("synthetic config outage")
				h.in.mu.Unlock()
			},
			wantErr: true,
		},
	}

	for _, ep := range entrypoints {
		for _, tc := range cases {
			t.Run(ep.name+"/"+tc.name, func(t *testing.T) {
				h := agentHub(t)
				model, calls := captureModel(t, `{"category":"routine_reply","reason":"synthetic"}`)
				h.in.setModel(model)
				policyCfg := compiledReplyPolicyTestConfig()
				h.setConfig(func(c *assistantwire.InboxConfig) {
					c.ReplyPolicy = policyCfg
				})

				it := actionItem("lark:synthetic-compiled-direct", time.Now())
				it.Source = "lark"
				it.Lark = &InboxLark{FeedID: "synthetic-allowed", IsGroup: true}
				it.Context = "SECRET-COMPILED-DIRECT-BODY"
				h.inbox.mu.Lock()
				h.inbox.items[it.ID] = it
				h.inbox.mu.Unlock()

				tc.setup(h, it, policyCfg)
				ep.run(t, h, policyCfg, it)

				if got := len(calls()); got != 0 {
					t.Fatalf("excluded/unverifiable item reached compiled reply_policy model %d times: %+v", got, calls())
				}
				h.inbox.mu.Lock()
				snap := it.ReplyPolicy
				h.inbox.mu.Unlock()
				if tc.wantErr {
					if snap == nil || snap.Error == "" || snap.Route != assistantwire.ReplyRouteOwnerOnly {
						t.Fatalf("config error did not persist fail-closed owner_only snapshot: %+v", snap)
					}
				} else if snap != nil {
					t.Fatalf("excluded item persisted compiled reply_policy snapshot: %+v", snap)
				}
			})
		}
	}
}

func newVisibilityForwardHarness(t *testing.T, vis *assistantwire.InboxVisibilityConfig) (*Service, *fakeHub, *fakeInbox, Todo) {
	t.Helper()
	ctx := context.Background()
	repoStore := &fakeTodoRepoStore{
		planMemStore: &planMemStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}},
		repos:        map[string][]repository.TaskRepository{},
	}
	f := newFakeHub().withMachine()
	s := newServiceOn(t, f, repoStore)
	if _, err := s.updateSettings(ctx, func(v *Settings) error { v.Main = "conv1"; return nil }); err != nil {
		t.Fatal(err)
	}
	s.superviseTimes.gather = time.Millisecond
	in := newFakeInbox()
	in.cfg.Visibility = vis
	f.inbox, in.svc = in, s
	if err := s.inboxReady(ctx); err != nil {
		t.Fatal(err)
	}
	turnInboxTodoForwardOn(t, s)

	todoPR := agentTask(t, s, f)
	f.setSessions("box",
		proto.Session{SessionID: "sec00001", MachineID: "box", Cwd: "/code/desk", Agent: "claude", Title: "PR Agent", Status: proto.StatusIdle, State: proto.StateIdle, UpdatedAt: time.Now()},
	)
	s.todos.mu.Lock()
	s.todos.todos[todoPR.ID].PRURL = "https://github.com/example/repo/pull/777"
	s.todos.mu.Unlock()
	return s, f, in, todoPR
}

func TestInboxVisibilityBlocksTodoForwardAndTaskLinks(t *testing.T) {
	ctx := context.Background()
	directVis := &assistantwire.InboxVisibilityConfig{
		Sources:     []string{"lark"},
		DirectChats: true,
	}

	t.Run("forwardInboxToTodos blocks excluded DM and mixed merge while forwarding allowed group", func(t *testing.T) {
		s, f, _, _ := newVisibilityForwardHarness(t, directVis)
		now := time.Now()
		dmDirect := &InboxItem{
			ID:        "lark:synthetic-dm-forward",
			Source:    "lark",
			Kind:      KindDM,
			Title:     "Private DM mentioning PR",
			Sender:    "Private Sender",
			Preview:   "Check https://github.com/example/repo/pull/777 SECRET-PRIVATE-FORWARD-BODY",
			Context:   "SECRET-PRIVATE-FORWARD-BODY https://github.com/example/repo/pull/777",
			Last:      "m-dm-1",
			UpdatedAt: now,
			Lark:      &InboxLark{FeedID: "synthetic-private-dm", IsGroup: false},
		}
		mixedSurvivor := &InboxItem{
			ID:        "lark:synthetic-mixed-forward",
			Source:    "lark",
			Kind:      KindGroup,
			Title:     "Mixed group mentioning PR",
			Sender:    "Teammate",
			Preview:   "Check https://github.com/example/repo/pull/777 SECRET-MIXED-FORWARD-BODY",
			Context:   "SECRET-MIXED-FORWARD-BODY https://github.com/example/repo/pull/777",
			Last:      "m-mix-1",
			UpdatedAt: now,
			Lark:      &InboxLark{FeedID: "synthetic-allowed-group-2", IsGroup: true},
		}
		mixedMember := visibilityTestMergeMember(mixedSurvivor)
		mixedMember.MergedInto = mixedSurvivor.ID
		mixedSurvivor.MergedFrom = []InboxMergeRef{{ID: mixedMember.ID, Source: mixedMember.Source, Title: mixedMember.Title}}

		groupAllowed := &InboxItem{
			ID:        "lark:synthetic-group-forward",
			Source:    "lark",
			Kind:      KindGroup,
			Title:     "Team group mentioning PR",
			Sender:    "Teammate",
			Preview:   "Please review https://github.com/example/repo/pull/777",
			Context:   "ORDINARY-GROUP-FORWARD-BODY https://github.com/example/repo/pull/777",
			Last:      "m-grp-1",
			UpdatedAt: now,
			Lark:      &InboxLark{FeedID: "synthetic-allowed-group", IsGroup: true},
		}

		s.inbox.mu.Lock()
		s.inbox.items[dmDirect.ID] = dmDirect
		s.inbox.items[mixedSurvivor.ID] = mixedSurvivor
		s.inbox.items[mixedMember.ID] = mixedMember
		s.inbox.items[groupAllowed.ID] = groupAllowed
		s.inbox.mu.Unlock()

		before := len(sentToAgent(f))
		s.forwardInboxToTodos(ctx)
		for _, msg := range sentToAgent(f)[before:] {
			if strings.Contains(msg, "SECRET-PRIVATE-FORWARD-BODY") || strings.Contains(msg, "SECRET-MIXED-FORWARD-BODY") {
				t.Fatalf("excluded body leaked to Coding Agent via forwardInboxToTodos: %s", msg)
			}
		}

		s.inbox.mu.Lock()
		dmForward := dmDirect.TodoForward
		mixedForward := mixedSurvivor.TodoForward
		groupForward := groupAllowed.TodoForward
		s.inbox.mu.Unlock()

		if dmForward != nil {
			t.Fatalf("excluded DM recorded TodoForward: %+v", dmForward)
		}
		if mixedForward != nil {
			t.Fatalf("historical mixed-merge survivor recorded TodoForward: %+v", mixedForward)
		}
		if groupForward == nil || groupForward.Status != TodoForwardSent {
			t.Fatalf("allowed group item was not forwarded: %+v", groupForward)
		}
	})

	t.Run("linkInboxTaskURLs blocks unquarantined excluded DM and does not auto-file or signal excluded linked items", func(t *testing.T) {
		s, _, _, todoPR := newVisibilityForwardHarness(t, directVis)
		now := time.Now()
		doneAt := now.Add(-time.Minute)
		doneTodo := Todo{ID: "todo_finished_vis", Title: "Finished task", DoneAt: &doneAt}
		s.todos.mu.Lock()
		s.todos.todos[doneTodo.ID] = &doneTodo
		s.todos.mu.Unlock()

		// Unquarantined excluded DM mentioning an open todo's PR URL.
		dmUnquarantined := &InboxItem{
			ID:        "lark:synthetic-dm-unquarantined-link",
			Source:    "lark",
			Kind:      KindDM,
			Title:     "Private DM mentioning PR",
			Sender:    "Private Sender",
			Preview:   "See https://github.com/example/repo/pull/777",
			Context:   "SECRET-LINK-BODY https://github.com/example/repo/pull/777",
			Last:      "m-link-1",
			UpdatedAt: now,
			Lark:      &InboxLark{FeedID: "synthetic-private-dm", IsGroup: false},
		}
		// Unquarantined excluded DM pre-linked to a finished todo (must not be auto-filed or signaled).
		dmPreLinkedDone := &InboxItem{
			ID:        "lark:synthetic-dm-prelinked-done",
			Source:    "lark",
			Kind:      KindDM,
			Title:     "SECRET-SIGNAL-TITLE",
			Sender:    "Private Sender",
			Preview:   "SECRET-SIGNAL-PREVIEW",
			Last:      "m-done-1",
			Count:     1,
			UpdatedAt: now,
			Lark:      &InboxLark{FeedID: "synthetic-private-dm-2", IsGroup: false},
			Agent:     &InboxAgent{State: AgentSkipped, TodoID: doneTodo.ID},
		}
		// Control allowed group item pre-linked to the finished todo (should be filed normally).
		groupDone := &InboxItem{
			ID:        "lark:synthetic-group-done",
			Source:    "lark",
			Kind:      KindGroup,
			Title:     "Allowed group on finished task",
			Sender:    "Teammate",
			Preview:   "Done",
			Last:      "m-gdone-1",
			UpdatedAt: now,
			Lark:      &InboxLark{FeedID: "synthetic-allowed-group", IsGroup: true},
			Agent:     &InboxAgent{State: AgentSkipped, TodoID: doneTodo.ID},
		}

		s.inbox.mu.Lock()
		s.inbox.items[dmUnquarantined.ID] = dmUnquarantined
		s.inbox.items[dmPreLinkedDone.ID] = dmPreLinkedDone
		s.inbox.items[groupDone.ID] = groupDone
		s.inbox.mu.Unlock()

		// Direct call without prior quarantineInboxVisibility must still fail closed.
		s.linkInboxTaskURLs(ctx)

		// Also verify superviseInbox does not alias/corrupt the caller's slice and fileTaskDone never files excluded items.
		kept := []*InboxItem{dmPreLinkedDone, groupDone}
		s.superviseInbox(ctx, kept, map[*InboxItem]bool{})
		if kept[0] != dmPreLinkedDone || kept[1] != groupDone {
			t.Fatalf("superviseInbox mutated caller slice in place: %v", kept)
		}
		s.fileTaskDone(ctx, kept)

		s.inbox.mu.Lock()
		dmLinkedTodo := ""
		if dmUnquarantined.Agent != nil {
			dmLinkedTodo = dmUnquarantined.Agent.TodoID
		}
		dmDoneOpen := dmPreLinkedDone.open()
		groupDoneOpen := groupDone.open()
		_, sigOK := inboxSignalOf(dmPreLinkedDone, false)
		s.inbox.mu.Unlock()

		if dmLinkedTodo != "" {
			t.Fatalf("unquarantined excluded DM was linked to todo %q by linkInboxTaskURLs (open todo=%s)", dmLinkedTodo, todoPR.ID)
		}
		if !dmDoneOpen {
			t.Fatal("excluded DM pre-linked to a finished todo was auto-filed by fileTaskDone")
		}
		if groupDoneOpen {
			t.Fatal("control allowed group item on a finished todo was not filed")
		}
		if sigOK {
			t.Fatal("excluded pre-linked DM emitted a supervisor inbox signal")
		}
	})

	t.Run("config read outage fails closed for forwardInboxToTodos and linkInboxTaskURLs", func(t *testing.T) {
		s, f, in, _ := newVisibilityForwardHarness(t, nil)
		in.mu.Lock()
		in.configErr = errors.New("synthetic config outage")
		in.mu.Unlock()

		now := time.Now()
		it := &InboxItem{
			ID:        "lark:synthetic-config-outage",
			Source:    "lark",
			Kind:      KindGroup,
			Title:     "PR update during config outage",
			Sender:    "Teammate",
			Preview:   "Check https://github.com/example/repo/pull/777",
			Context:   "CONFIG-OUTAGE-BODY https://github.com/example/repo/pull/777",
			Last:      "m-out-1",
			UpdatedAt: now,
			Lark:      &InboxLark{FeedID: "synthetic-group", IsGroup: true},
		}
		s.inbox.mu.Lock()
		s.inbox.items[it.ID] = it
		s.inbox.mu.Unlock()

		before := len(sentToAgent(f))
		s.forwardInboxToTodos(ctx)
		s.linkInboxTaskURLs(ctx)

		if got := len(sentToAgent(f)) - before; got != 0 {
			t.Fatalf("forwardInboxToTodos sent %d messages during visibility config outage", got)
		}
		s.inbox.mu.Lock()
		todoForward := it.TodoForward
		todoID := ""
		if it.Agent != nil {
			todoID = it.Agent.TodoID
		}
		s.inbox.mu.Unlock()
		if todoForward != nil {
			t.Fatalf("forwardInboxToTodos recorded TodoForward during config outage: %+v", todoForward)
		}
		if todoID != "" {
			t.Fatalf("linkInboxTaskURLs linked todo %q during config outage", todoID)
		}
	})
}
