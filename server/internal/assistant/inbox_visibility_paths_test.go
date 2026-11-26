package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
)

type visibilitySourceExtensions struct {
	*policyExtensions
	sources   map[string]string
	preflight map[string]bool
}

func (e *visibilitySourceExtensions) InboxVisibilitySource(name string) (string, bool) {
	source, ok := e.sources[name]
	return source, ok
}

func (e *visibilitySourceExtensions) InboxVisibilityPreflight(name string) bool {
	return e.preflight[name]
}

func TestInboxVisibilityOriginStoreFailureFailsClosed(t *testing.T) {
	h, _, it := handledHub(t)
	ext := &policyExtensions{read: map[string]bool{"lark_messages": true}}
	h.SetExtensions(ext)
	h.setConfig(func(c *assistantwire.InboxConfig) {
		c.Agent.ReadProfile = "source_only"
		c.Agent.ConnectorTools = []string{"lark_messages"}
		c.Visibility = visibilityTestConfig()
	})
	h.originSet(t.Context(), "server", "s1", OriginInbox, it.ID, "")
	h.origins.mu.Lock()
	h.origins.loaded, h.origins.m = false, nil
	h.origins.mu.Unlock()
	h.setStore(func() Store { return originErrorStore{h.st} })
	// No origin hint simulates an older node. An unreadable origin book must not make this an ordinary tool call.
	res := h.mcpCallWithOrigin(t.Context(), "server", "s1", "lark_messages", json.RawMessage(`{"chat_id":"synthetic-allowed"}`), true, "")
	if !res.IsError || len(ext.calls) != 0 {
		t.Fatalf("origin-store failure bypassed Inbox policy: %+v, connector calls=%v", res, ext.calls)
	}
	res = h.mcpCallWithOrigin(t.Context(), "server", "s1", "kb_search", json.RawMessage(`{"query":"ordinary"}`), true, "")
	if !res.IsError {
		t.Fatalf("origin-store failure allowed a KB read under source_only: %+v", res)
	}
}

func TestSourceOnlyConfigFailureFailsClosed(t *testing.T) {
	h, _, it := handledHub(t)
	ext := &policyExtensions{read: map[string]bool{"lark_messages": true}}
	h.SetExtensions(ext)
	h.setConfig(func(c *assistantwire.InboxConfig) {
		c.Agent.ReadProfile = "source_only"
		c.Agent.ConnectorTools = []string{"lark_messages"}
	})
	h.originSet(t.Context(), "server", "s1", OriginInbox, it.ID, "")
	h.in.mu.Lock()
	h.in.configErr = errors.New("synthetic configuration outage")
	h.in.mu.Unlock()

	if res := h.mcpCallWithOrigin(t.Context(), "server", "s1", "kb_search", json.RawMessage(`{"query":"ordinary"}`), true, OriginInbox); !res.IsError {
		t.Fatalf("configuration outage allowed a KB read: %+v", res)
	}
	res := h.mcpCallWithOrigin(t.Context(), "server", "s1", "lark_messages", json.RawMessage(`{"chat_id":"synthetic-allowed"}`), true, OriginInbox)
	if !res.IsError || len(ext.calls) != 0 {
		t.Fatalf("configuration outage allowed a connector read: %+v, calls=%v", res, ext.calls)
	}
}

func TestInboxVisibilityGenericConnectorSourceFailsClosed(t *testing.T) {
	for _, capable := range []bool{false, true} {
		t.Run(map[bool]string{false: "older provider", true: "declared provider preflight"}[capable], func(t *testing.T) {
			h, _, it := handledHub(t)
			base := &policyExtensions{read: map[string]bool{"acme_messages": true, "acme_search": true, "other_messages": true}}
			ext := &visibilitySourceExtensions{policyExtensions: base,
				sources:   map[string]string{"acme_messages": "acme", "acme_search": "acme", "other_messages": "other"},
				preflight: map[string]bool{"acme_messages": capable, "acme_search": capable}}
			h.SetExtensions(ext)
			h.setConfig(func(c *assistantwire.InboxConfig) {
				c.Agent.ReadProfile = "source_only"
				c.Agent.ConnectorTools = []string{"acme_messages", "acme_search", "other_messages"}
				c.Visibility = &assistantwire.InboxVisibilityConfig{Sources: []string{"acme"}, Chats: []string{"synthetic-private"}}
			})
			h.originSet(t.Context(), "server", "s1", OriginInbox, it.ID, "")

			res := h.mcpCallWithOrigin(t.Context(), "server", "s1", "acme_messages", json.RawMessage(`{"chat_id":"synthetic-other"}`), true, OriginInbox)
			if res.IsError == capable {
				t.Fatalf("capable=%v: generic source read returned %+v", capable, res)
			}
			wantCalls := 0
			if capable {
				wantCalls = 1
			}
			if len(base.calls) != wantCalls {
				t.Fatalf("capable=%v: generic provider calls=%v, want %d", capable, base.calls, wantCalls)
			}
			res = h.mcpCallWithOrigin(t.Context(), "server", "s1", "acme_search", json.RawMessage(`{"query":"ordinary"}`), true, OriginInbox)
			if !res.IsError || len(base.calls) != wantCalls {
				t.Fatalf("capable=%v: global search bypassed the veto: %+v, calls=%v", capable, res, base.calls)
			}
			res = h.mcpCallWithOrigin(t.Context(), "server", "s1", "other_messages", json.RawMessage(`{"chat_id":"synthetic-other"}`), true, OriginInbox)
			if res.IsError || len(base.calls) != wantCalls+1 {
				t.Fatalf("an unselected source was changed by the acme boundary: %+v, calls=%v", res, base.calls)
			}
		})
	}
}

func TestInboxVisibilityGroupingVetoBeforeAnyModelOrConnectorCall(t *testing.T) {
	g := &groupLark{placed: map[string]bool{}}
	model, client := newGroupModel(t, groupAnswerOK)
	h, _ := groupHub(t, g, client)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.Visibility = visibilityTestConfig() })
	batch := groupBatch()
	batch.Thread = "chat:synthetic-private"
	batch.Kind = KindDM
	batch.Context = "SECRET-PRE-ADMISSION-PRIVATE-BATCH"

	h.relateBatch(t.Context(), sourceOf(g), *batch)
	if got := len(model.asked()); got != 0 {
		t.Fatalf("excluded pre-admission batch reached the grouping model %d times", got)
	}
	appends, tools, fallbacks := g.counts()
	if appends != 0 || tools != 0 || fallbacks != 0 {
		t.Fatalf("excluded grouping reached downstream: appends=%d tools=%d fallbacks=%d", appends, tools, fallbacks)
	}
}

func TestInboxVisibilitySettlementVetoBeforeHistoryAndModel(t *testing.T) {
	c := &settleLark{groupLark: groupLark{placed: map[string]bool{}}}
	c.set(`{"messages":[{"id":"m1051","pos":"1051","text":"private question"},{"id":"m1052","pos":"1052","text":"private follow-up"}]}`)
	n, mu, model := settleModel(t, `{"state":"over","reason":"synthetic"}`)
	h, it := settleHub(t, c, model, false)
	it.Context = "SECRET-SETTLEMENT-PRIVATE-CONTEXT"
	h.setConfig(func(c *assistantwire.InboxConfig) {
		c.Visibility = &assistantwire.InboxVisibilityConfig{Sources: []string{"lark"}, Chats: []string{"F1"}}
	})

	h.settleAndWait()
	if len(c.asks) != 0 {
		t.Fatalf("excluded settlement fetched private history: %+v", c.asks)
	}
	mu.Lock()
	calls := *n
	mu.Unlock()
	if calls != 0 {
		t.Fatalf("excluded settlement reached the model %d times", calls)
	}
	if !it.open() || !it.VisibilityExcluded {
		t.Fatalf("excluded settlement was not left open and quarantined: %+v", it)
	}
}

func TestInboxVisibilityFolderToolFiltersPrivateMetadata(t *testing.T) {
	h := newInboxHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.Visibility = visibilityTestConfig() })
	newRow := func(id, chat, title, sender string, persistent bool) *InboxItem {
		it := actionItem(id, time.Now())
		it.Source = "lark"
		it.Lark.FeedID = chat
		it.Title, it.Sender = title, sender
		it.VisibilityExcluded = persistent
		h.inbox.items[it.ID] = it
		return it
	}
	newRow("lark:synthetic-private:folder-tool-current", "synthetic-private", "SECRET-FOLDER-TITLE", "SECRET-FOLDER-SENDER", false)
	newRow("lark:synthetic-other:folder-tool-persistent", "synthetic-other", "SECRET-PERSISTENT-FOLDER-TITLE", "SECRET-PERSISTENT-FOLDER-SENDER", true)
	newRow("lark:synthetic-allowed:folder-tool", "synthetic-allowed", "ORDINARY-FOLDER-ITEM", "Ordinary Sender", false)

	out, err := h.toolInboxFolders(t.Context(), json.RawMessage(`{"unfiled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"SECRET-FOLDER-TITLE", "SECRET-FOLDER-SENDER", "SECRET-PERSISTENT-FOLDER-TITLE", "SECRET-PERSISTENT-FOLDER-SENDER"} {
		if strings.Contains(out, secret) {
			t.Errorf("folder tool exposed %s: %s", secret, out)
		}
	}
	if !strings.Contains(out, "ORDINARY-FOLDER-ITEM") {
		t.Fatalf("folder tool removed an ordinary row too: %s", out)
	}
}

func TestInboxVisibilityTaskRecheckVetoBeforeJudge(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		t.Run(map[bool]string{false: "current configuration", true: "persistent marker"}[persistent], func(t *testing.T) {
			h, todo, it := taskRecheckHub(t)
			it.Source = "lark"
			it.Lark = &InboxLark{FeedID: "synthetic-private"}
			it.Context = "SECRET-TASK-RECHECK-PRIVATE-CONTEXT"
			it.VisibilityExcluded = persistent
			if !persistent {
				h.setConfig(func(c *assistantwire.InboxConfig) { c.Visibility = visibilityTestConfig() })
			}
			model, calls := captureModel(t, `{"task_id":"`+todo.ID+`","evidence":"synthetic"}`)
			h.in.setModel(model)

			out, code, err := h.recheckInboxTask(t.Context(), it.ID)
			if err != nil || code != 200 || out.Status != "excluded" || out.Method != "visibility" {
				t.Fatalf("excluded task recheck: code=%d out=%+v err=%v", code, out, err)
			}
			if got := len(calls()); got != 0 {
				t.Fatalf("excluded task content reached the judge %d times", got)
			}
			if it.Agent.TodoID != "" {
				t.Fatalf("excluded recheck linked task %q", it.Agent.TodoID)
			}
		})
	}
}

func TestInboxVisibilityMergeAndTitleExcludePrivateMembers(t *testing.T) {
	t.Run("private target cannot contaminate a new merge", func(t *testing.T) {
		h := newInboxHub(t)
		model, calls := captureModel(t, `{"title":"Synthetic merged title"}`)
		h.in.setModel(model)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.Visibility = visibilityTestConfig() })
		now := time.Now()
		allowed := actionItem("lark:synthetic-allowed:merge", now)
		allowed.Source = "lark"
		allowed.Lark.FeedID = "synthetic-allowed"
		allowed.Context = "ORDINARY-MERGE-CONTEXT"
		private := actionItem("lark:synthetic-private:merge", now.Add(-time.Minute))
		private.Source = "lark"
		private.Lark.FeedID = "synthetic-private"
		private.Context = "SECRET-MERGE-PRIVATE-CONTEXT"
		h.inbox.items[allowed.ID], h.inbox.items[private.ID] = allowed, private

		out, err := h.mergeInbox(t.Context(), []inboxBulkTarget{{ID: allowed.ID}, {ID: private.ID}}, now)
		if !errors.Is(err, errMergeNone) {
			t.Fatalf("private merge was not refused: out=%+v err=%v", out, err)
		}
		if len(allowed.MergedFrom) != 0 || private.MergedInto != "" || strings.Contains(allowed.Context, "SECRET-MERGE") {
			t.Fatalf("private member contaminated the survivor: allowed=%+v private=%+v", allowed, private)
		}
		if got := len(calls()); got != 0 {
			t.Fatalf("refused merge started %d title calls", got)
		}
	})

	t.Run("historical mixed merge cannot reach the title model", func(t *testing.T) {
		h := newInboxHub(t)
		model, calls := captureModel(t, `{"title":"Synthetic merged title"}`)
		h.in.setModel(model)
		now := time.Now()
		survivor := actionItem("lark:synthetic-allowed:historical-merge", now)
		survivor.Source = "lark"
		survivor.Lark.FeedID = "synthetic-allowed"
		survivor.Context = "ORDINARY-TITLE-CONTEXT"
		member := actionItem("lark:synthetic-other:historical-member", now.Add(-time.Minute))
		member.Source = "lark"
		member.Lark.FeedID = "synthetic-other"
		member.Title = "SECRET-HISTORICAL-MERGE-TITLE"
		member.Context = "SECRET-HISTORICAL-MERGE-CONTEXT"
		member.VisibilityExcluded = true
		member.MergedInto = survivor.ID
		survivor.MergedFrom = []InboxMergeRef{{ID: member.ID, Source: member.Source, Title: member.Title}}
		h.inbox.items[survivor.ID], h.inbox.items[member.ID] = survivor, member

		h.rewriteMergedTitle(t.Context(), survivor.ID)
		if got := len(calls()); got != 0 {
			t.Fatalf("historical private member reached the title model %d times", got)
		}
		if !survivor.VisibilityExcluded {
			t.Fatal("a survivor containing a quarantined member was not quarantined")
		}
	})
}

func TestInboxVisibilityGroupToolRechecksFreshBoundary(t *testing.T) {
	g := &groupLark{placed: map[string]bool{}}
	h, _ := groupHub(t, g, nil)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.Visibility = visibilityTestConfig() })
	result := h.groupTool(t.Context(), "lark", "chat:synthetic-private", connector.GroupMessagesAround, json.RawMessage(`{"message":"m1"}`), map[string]groupEvent{})
	if !strings.Contains(result, "excluded") {
		t.Fatalf("excluded group tool result = %s", result)
	}
	_, tools, _ := g.counts()
	if tools != 0 {
		t.Fatalf("excluded group tool reached the connector %d times", tools)
	}
}

func TestInboxVisibilityBatchWithoutChatIDFailsClosed(t *testing.T) {
	cfg := &assistantwire.InboxVisibilityConfig{Sources: []string{"acme"}, Chats: []string{"synthetic-private"}}
	it := inboxVisibilityThingItem("acme", connector.InboxThing{Thread: "opaque", Kind: KindGroup})
	if !inboxVisibilityExcluded(it, cfg) {
		t.Fatal("a selected batch without a verifiable chat id was assumed to be outside the explicit list")
	}
}

func TestInboxVisibilityConfigFailureSkipsMerge(t *testing.T) {
	h := newInboxHub(t)
	h.in.mu.Lock()
	h.in.configErr = errors.New("synthetic configuration outage")
	h.in.mu.Unlock()
	now := time.Now()
	a, b := actionItem("a", now), actionItem("b", now.Add(-time.Minute))
	h.inbox.items[a.ID], h.inbox.items[b.ID] = a, b
	if _, err := h.mergeInbox(context.Background(), []inboxBulkTarget{{ID: a.ID}, {ID: b.ID}}, now); err == nil {
		t.Fatal("merge proceeded while its visibility configuration was unreadable")
	}
	if len(a.MergedFrom) != 0 || len(b.MergedFrom) != 0 {
		t.Fatal("configuration failure changed merge membership")
	}
}
