package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

type visibilityPreflightExtensions struct {
	*policyExtensions
	tools map[string]bool
}

func (e *visibilityPreflightExtensions) InboxVisibilityPreflight(name string) bool {
	return e.tools[name]
}

func TestInboxVisibilityMixedThreadAndAtMeVetoBeforeConnectorCall(t *testing.T) {
	h, _, it := handledHub(t)
	ext := &policyExtensions{read: map[string]bool{"lark_messages": true, "lark_chats": true}}
	h.SetExtensions(ext)
	h.setConfig(func(c *assistantwire.InboxConfig) {
		c.Agent.ReadProfile = "source_only"
		c.Agent.ConnectorTools = []string{"lark_messages", "lark_chats"}
		c.Visibility = visibilityTestConfig()
	})
	h.originSet(t.Context(), "server", "s1", OriginInbox, it.ID, "")
	for _, call := range []struct {
		name string
		args string
	}{
		{name: "lark_messages", args: `{"chat_id":"synthetic-allowed-group","thread":"synthetic-private-thread"}`},
		{name: "lark_chats", args: `{"filter":"at_me"}`},
		{name: "lark_chats", args: `{"filter":"recent"}`},
	} {
		res := h.mcpCallWithOrigin(t.Context(), "server", "s1", call.name, json.RawMessage(call.args), true, OriginInbox)
		if !res.IsError {
			t.Errorf("visibility allowed %s %s: %+v", call.name, call.args, res)
		}
	}
	if len(ext.calls) != 0 {
		t.Fatalf("mixed-thread or at_me veto reached the connector: %v", ext.calls)
	}

	for _, call := range []struct {
		name string
		args string
	}{
		{name: "lark_messages", args: `{"chat_id":"synthetic-allowed-group"}`},
	} {
		res := h.mcpCallWithOrigin(t.Context(), "server", "s1", call.name, json.RawMessage(call.args), true, OriginInbox)
		if res.IsError {
			t.Errorf("ordinary metadata read %s was refused: %+v", call.name, res)
		}
	}
	if len(ext.calls) != 1 {
		t.Fatalf("allowed read did not reach the connector exactly once: %v", ext.calls)
	}
}

func TestInboxVisibilityDirectToolsRequireProviderPreflight(t *testing.T) {
	for _, capable := range []bool{false, true} {
		t.Run(map[bool]string{false: "older provider fails closed", true: "declared preflight allows provider check"}[capable], func(t *testing.T) {
			h, _, it := handledHub(t)
			base := &policyExtensions{read: map[string]bool{"lark_messages": true, "lark_chats": true}}
			ext := &visibilityPreflightExtensions{policyExtensions: base, tools: map[string]bool{
				"lark_messages": capable,
				"lark_chats":    capable,
			}}
			h.SetExtensions(ext)
			h.setConfig(func(c *assistantwire.InboxConfig) {
				c.Agent.ReadProfile = "source_only"
				c.Agent.ConnectorTools = []string{"lark_messages", "lark_chats"}
				c.Visibility = &assistantwire.InboxVisibilityConfig{Sources: []string{"lark"}, DirectChats: true}
			})
			h.originSet(t.Context(), "server", "s1", OriginInbox, it.ID, "")
			for _, call := range []struct {
				name string
				args string
			}{
				{name: "lark_messages", args: `{"chat_id":"synthetic-unlisted-chat"}`},
				{name: "lark_chats", args: `{"filter":"recent"}`},
			} {
				res := h.mcpCallWithOrigin(t.Context(), "server", "s1", call.name, json.RawMessage(call.args), true, OriginInbox)
				if res.IsError == capable {
					t.Errorf("capable=%v: %s returned %+v", capable, call.name, res)
				}
			}
			wantCalls := 0
			if capable {
				wantCalls = 2
			}
			if len(base.calls) != wantCalls {
				t.Fatalf("capable=%v: connector calls = %v, want %d", capable, base.calls, wantCalls)
			}

			res := h.mcpCallWithOrigin(t.Context(), "server", "s1", "lark_chats", json.RawMessage(`{"filter":"at_me"}`), true, OriginInbox)
			if !res.IsError || len(base.calls) != wantCalls {
				t.Fatalf("capable=%v: at_me bypassed the body-search veto: %+v, calls=%v", capable, res, base.calls)
			}
			h.setConfig(func(c *assistantwire.InboxConfig) { c.Visibility = nil })
			res = h.mcpCallWithOrigin(t.Context(), "server", "s1", "lark_chats", json.RawMessage(`{"filter":"at_me"}`), true, OriginInbox)
			if res.IsError || len(base.calls) != wantCalls+1 {
				t.Fatalf("capable=%v: empty visibility changed the legacy at_me path: %+v, calls=%v", capable, res, base.calls)
			}
		})
	}
}

func TestInboxVisibilityDraftVetoBeforeModel(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		t.Run(map[bool]string{false: "current configuration", true: "persistent marker"}[persistent], func(t *testing.T) {
			h := newInboxHub(t)
			model, calls := captureModel(t, "Synthetic draft")
			h.in.setModel(model)
			h.setConfig(func(c *assistantwire.InboxConfig) {
				if !persistent {
					c.Visibility = visibilityTestConfig()
				}
			})
			it := actionItem("lark:synthetic-private:draft", time.Now())
			it.Source = "lark"
			it.Lark.FeedID = "synthetic-private"
			it.Context = "SECRET-PRIVATE-DRAFT"
			it.VisibilityExcluded = persistent
			h.inbox.items[it.ID] = it
			if _, err := h.draftReply(t.Context(), it.ID, ""); err == nil {
				t.Fatal("an excluded item was drafted")
			}
			if got := len(calls()); got != 0 {
				t.Fatalf("excluded draft context reached the model in %d calls", got)
			}
		})
	}

	t.Run("allowed item keeps drafting", func(t *testing.T) {
		h := newInboxHub(t)
		model, calls := captureModel(t, "Synthetic draft")
		h.in.setModel(model)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.Visibility = visibilityTestConfig() })
		it := actionItem("lark:synthetic-allowed:draft", time.Now())
		it.Source = "lark"
		it.Lark.FeedID = "synthetic-allowed"
		it.Context = "ORDINARY-DRAFT-CONTEXT"
		h.inbox.items[it.ID] = it
		text, err := h.draftReply(t.Context(), it.ID, "")
		if err != nil || text != "Synthetic draft" {
			t.Fatalf("allowed draft = %q, %v", text, err)
		}
		got := calls()
		if len(got) != 1 || !strings.Contains(got[0].User, "ORDINARY-DRAFT-CONTEXT") {
			t.Fatalf("allowed draft did not use its ordinary context: %+v", got)
		}
	})
}

func TestInboxVisibilityFolderExamplesVetoBeforeModel(t *testing.T) {
	h := newInboxHub(t)
	model, calls := captureModel(t, `{"folders":{}}`)
	h.in.setModel(model)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.Visibility = visibilityTestConfig() })
	newExample := func(id, chat, title, sender string, persistent bool) *InboxItem {
		it := actionItem(id, time.Now())
		it.Source = "lark"
		it.Lark.FeedID = chat
		it.Title, it.Sender = title, sender
		it.Folder, it.FolderBy = "work", FolderByUser
		it.VisibilityExcluded = persistent
		h.inbox.items[it.ID] = it
		return it
	}
	newExample("lark:synthetic-private:folder-current", "synthetic-private", "SECRET-CURRENT-TITLE", "SECRET-CURRENT-SENDER", false)
	newExample("lark:synthetic-other:folder-persistent", "synthetic-other", "SECRET-PERSISTENT-TITLE", "SECRET-PERSISTENT-SENDER", true)
	newExample("lark:synthetic-allowed:folder-example", "synthetic-allowed", "ORDINARY-EXAMPLE", "Ordinary Sender", false)
	target := actionItem("lark:synthetic-allowed:folder-target", time.Now())
	target.Source = "lark"
	target.Lark.FeedID = "synthetic-allowed"
	target.Title = "ORDINARY-TARGET"
	h.inbox.items[target.ID] = target
	if err := h.fileBatch(t.Context(), []*InboxItem{target}); err != nil {
		t.Fatal(err)
	}
	got := calls()
	if len(got) != 1 {
		t.Fatalf("allowed folder classification made %d model calls, want one", len(got))
	}
	prompt := got[0].System + "\n" + got[0].User
	for _, secret := range []string{"SECRET-CURRENT-TITLE", "SECRET-CURRENT-SENDER", "SECRET-PERSISTENT-TITLE", "SECRET-PERSISTENT-SENDER"} {
		if strings.Contains(prompt, secret) {
			t.Errorf("excluded manual example reached the folder model: %s", secret)
		}
	}
	if !strings.Contains(prompt, "ORDINARY-EXAMPLE") || !strings.Contains(prompt, "ORDINARY-TARGET") {
		t.Fatalf("allowed example or target was removed from folder classification: %s", prompt)
	}
}

func TestInboxVisibilityRejectedHandoffVetoBeforeModel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, _, it := handoffHub(t)
		handOff(t, h, it)
		if _, err := mainSuggest(h, "m1", `{"item":"lark:f1:100","kind":"agent","text":"SECRET-HANDOFF-SUGGESTION"}`); err != nil {
			t.Fatal(err)
		}
		it.Source = "lark"
		it.Lark.FeedID = "synthetic-private"
		it.Title = "SECRET-HANDOFF-TITLE"
		cfg := assistantwire.InboxConfig{Visibility: visibilityTestConfig()}
		h.in.mu.Lock()
		h.in.cfg = cfg
		h.in.mu.Unlock()
		// The action itself may use this valid display cache; tellHandoff must still make its own fresh authorization read.
		h.inbox.cfgMu.Lock()
		h.inbox.cfg = cfg
		h.inbox.cfgAt = time.Now()
		h.inbox.cfgMu.Unlock()
		fresh := make(chan struct{})
		var once sync.Once
		h.in.mu.Lock()
		h.in.configHook = func() { once.Do(func() { close(fresh) }) }
		h.in.mu.Unlock()
		if _, err := h.actOnSuggestion(t.Context(), it.ID, suggestionReq{Version: 1, Action: suggestReject}, true); err != nil {
			t.Fatal(err)
		}

		select {
		case <-fresh:
		case <-time.After(3 * time.Second):
			t.Fatal("the rejected handoff notice never re-read fresh visibility")
		}
		// Let a broken implementation finish an asynchronous send after the configuration hook before the negative assertion.
		synctest.Wait()
		notes := strings.Join(notesOf(h.f, "m1"), "\n")
		if strings.Contains(notes, "SECRET-HANDOFF-SUGGESTION") || strings.Contains(notes, "SECRET-HANDOFF-TITLE") {
			t.Fatalf("fresh visibility did not stop the rejected handoff notice: %s", notes)
		}
	})
}

func TestInboxVisibilityRechecksEachQueuedAgentStart(t *testing.T) {
	for _, excludeSecond := range []bool{false, true} {
		t.Run(map[bool]string{false: "unchanged policy allows both", true: "changed policy vetoes the later job"}[excludeSecond], func(t *testing.T) {
			h := agentHub(t)
			now := time.Now()
			first := actionItem("lark:synthetic-group:first", now)
			first.Source = "lark"
			first.Lark.FeedID = "synthetic-group"
			first.Agent = &InboxAgent{State: AgentQueued, Task: "Ordinary first task"}
			second := actionItem("lark:synthetic-private:second", now.Add(-time.Minute))
			second.Source = "lark"
			second.Lark.FeedID = "synthetic-private"
			second.Context = "SECRET-QUEUED-PRIVATE"
			second.Agent = &InboxAgent{State: AgentQueued, Task: "SECRET-QUEUED-PRIVATE"}
			h.inbox.items[first.ID] = first
			h.inbox.items[second.ID] = second
			changed := false
			h.f.mu.Lock()
			h.f.onAssistantSend = func(_ *fakeHub, _ map[string]any) {
				if excludeSecond && !changed {
					changed = true
					h.in.mu.Lock()
					h.in.cfg.Visibility = visibilityTestConfig()
					h.in.mu.Unlock()
				}
			}
			h.f.mu.Unlock()
			h.startInboxAgents(context.Background(), InboxAgentConfig{Enabled: true, Concurrency: 2})
			h.f.mu.Lock()
			h.f.onAssistantSend = nil
			h.f.mu.Unlock()

			starts := conversationStarts(h.f)
			wantStarts := 2
			if excludeSecond {
				wantStarts = 1
			}
			if len(starts) != wantStarts {
				t.Fatalf("excludeSecond=%v: started %d conversations, want %d", excludeSecond, len(starts), wantStarts)
			}
			for _, start := range starts {
				text, _ := start["text"].(string)
				if excludeSecond && strings.Contains(text, "SECRET-QUEUED-PRIVATE") {
					t.Fatalf("later queued private context reached an Agent start: %s", text)
				}
			}
			if excludeSecond && (!second.VisibilityExcluded || second.Agent.SessionID != "") {
				t.Fatalf("later job was not quarantined without a session: %+v", second.Agent)
			}
			if !excludeSecond && (second.VisibilityExcluded || second.Agent.SessionID == "") {
				t.Fatalf("unchanged policy blocked the ordinary later job: %+v", second.Agent)
			}
		})
	}
}
