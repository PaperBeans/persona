package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// handoffSuggestHub sets up a handled Inbox item (processing conversation s1 on server) with:
// - main conversation m1
// - active leader conversation lead-1 (direction "Release Ops")
// - archived leader conversation lead-arch
// - active linked task todo_1 whose conversation is task-chat-1 (a side assistant conversation)
// - unrelated side assistant conversation side-1
// - non-assistant coding agent session agent-1 on server
func handoffSuggestHub(t *testing.T) (*inboxHub, *extensionLark, *InboxItem) {
	t.Helper()
	h, lark, it := handledHub(t)
	setSettings(t, h.Service, func(v *Settings) {
		v.Main = "m1"
		v.Leaders = []string{"lead-1", "lead-arch"}
	})
	h.directions.mu.Lock()
	h.directions.loaded = true
	h.directions.m = map[string]*direction{
		"lead-1":    {MachineID: "server", Name: "Release Ops", By: directionByUser, At: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)},
		"lead-arch": {MachineID: "server", Name: "Old Direction", By: directionByUser, At: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)},
	}
	h.directions.mu.Unlock()
	h.f.setSessions("server",
		proto.Session{SessionID: "s1", Assistant: true, Origin: OriginInbox, Status: proto.StatusWorking},
		proto.Session{SessionID: "m1", Assistant: true, Title: "Main", Status: proto.StatusIdle},
		proto.Session{SessionID: "lead-1", Assistant: true, Title: "Release Ops", Status: proto.StatusIdle},
		proto.Session{SessionID: "lead-arch", Assistant: true, Title: "Old Direction", Status: proto.StatusIdle},
		proto.Session{SessionID: "task-chat-1", Assistant: true, Title: "Login fix chat", Status: proto.StatusIdle},
		proto.Session{SessionID: "side-1", Assistant: true, Title: "Random side chat", Status: proto.StatusIdle},
		proto.Session{SessionID: "agent-1", Assistant: false, Title: "Coding agent", Status: proto.StatusIdle},
	)
	if _, err := h.setArchived(context.Background(), "server", "lead-arch", true, titleByUser); err != nil {
		t.Fatalf("archive lead-arch: %v", err)
	}
	putTodo(h.Service, Todo{
		ID:     "todo_1",
		Title:  "Fix login crash",
		Origin: "task-chat-1",
	})
	h.inbox.mu.Lock()
	it.Agent.TodoID = "todo_1"
	h.inbox.mu.Unlock()
	return h, lark, it
}

func TestInboxSuggestHandoffEligibleTargetsWaitForConfirmation(t *testing.T) {
	cases := []struct {
		name      string
		session   string
		wantTitle string
		wantChain string
	}{
		{"main", "m1", "Main", "助手建议交给 Persona 主对话"},
		{"leader", "lead-1", "Release Ops", "助手建议交给「Release Ops」"},
		{"linked_task", "task-chat-1", "Login fix chat", "助手建议交给「Login fix chat」"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _, it := handoffSuggestHub(t)
			out, err := suggestTool(t, h, `{"kind":"handoff","machine":"server","session":"`+tc.session+`","text":"Follow up with the customer","note":"Belongs to existing work","recap":"Customer asked for status"}`)
			if err != nil {
				t.Fatalf("suggest handoff to %s: %v", tc.session, err)
			}
			if !strings.Contains(out, "nothing goes out until they confirm") {
				t.Fatalf("unexpected tool output: %q", out)
			}
			// Nothing is sent before confirmation.
			if got := len(sendOnces(h.f)); got != 0 {
				t.Fatalf("sent %d messages before confirmation, want 0", got)
			}
			h.inbox.mu.Lock()
			l := it.listed()
			handoffs := len(it.Handoffs)
			last := it.Chain[len(it.Chain)-1]
			h.inbox.mu.Unlock()
			if handoffs != 0 {
				t.Fatalf("recorded %d handoffs before confirmation, want 0", handoffs)
			}
			if l.Suggestion == nil || l.Suggestion.Kind != SuggestHandoff || l.Suggestion.MachineID != "server" ||
				l.Suggestion.SessionID != tc.session || l.Suggestion.TargetTitle != tc.wantTitle ||
				l.Suggestion.Text != "Follow up with the customer" || l.Suggestion.Conversation != nil {
				t.Fatalf("listed suggestion = %+v", l.Suggestion)
			}
			if l.Status != StatusAction || l.Now != NowConfirmHandoff {
				t.Fatalf("status=%q now=%q, want action / confirm_handoff", l.Status, l.Now)
			}
			if last.Kind != ChainHandling || last.Text != tc.wantChain || last.Link == nil || last.Link.SessionID != "s1" {
				t.Fatalf("chain = %+v, want %q linked to s1", last, tc.wantChain)
			}
		})
	}
}

func TestInboxSuggestHandoffRefusesIneligibleTargets(t *testing.T) {
	h, _, it := handoffSuggestHub(t)

	// Also add an archived-on-node leader, a deleted leader, an outside direction, and a closed task.
	setSettings(t, h.Service, func(v *Settings) {
		v.Leaders = append(v.Leaders, "lead-node-arch", "lead-deleted", "dir_outside")
	})
	h.directions.mu.Lock()
	h.directions.m["lead-node-arch"] = &direction{MachineID: "server", Name: "Node Archived", By: directionByUser}
	h.directions.m["lead-deleted"] = &direction{MachineID: "server", Name: "Deleted Leader", By: directionByUser}
	h.directions.m["dir_outside"] = &direction{Name: "Outside", By: directionByUser, Outside: &directionOutside{MachineID: "server", SessionID: "agent-1"}}
	h.directions.mu.Unlock()
	deletedRow := proto.Session{SessionID: "lead-deleted", Assistant: true, Title: "Deleted"}
	deletedRow.SetDeleted(true)
	h.f.mu.Lock()
	h.f.sessions["server"] = append(h.f.sessions["server"],
		proto.Session{SessionID: "lead-node-arch", Assistant: true, Archived: true, Title: "Node Archived"},
		deletedRow,
	)
	h.f.mu.Unlock()

	cases := []struct {
		name    string
		args    string
		wantErr string
	}{
		{"missing session", `{"kind":"handoff","machine":"server","text":"Note"}`, "requires both machine and session"},
		{"missing machine", `{"kind":"handoff","session":"m1","text":"Note"}`, "requires both machine and session"},
		{"session on reply", `{"kind":"reply","session":"m1","text":"Hi"}`, "kind handoff only"},
		{"session on recap", `{"recap":"Updated","session":"m1"}`, "kind handoff only"},
		{"archived in settings", `{"kind":"handoff","machine":"server","session":"lead-arch","text":"Note"}`, "archived"},
		{"archived on node", `{"kind":"handoff","machine":"server","session":"lead-node-arch","text":"Note"}`, "archived"},
		{"deleted", `{"kind":"handoff","machine":"server","session":"lead-deleted","text":"Note"}`, "deleted"},
		{"unknown conversation", `{"kind":"handoff","machine":"server","session":"does-not-exist","text":"Note"}`, "was not found"},
		{"other manager machine", `{"kind":"handoff","machine":"other-manager-box","session":"m1","text":"Note"}`, "not on this manager"},
		{"non-assistant session", `{"kind":"handoff","machine":"server","session":"agent-1","text":"Note"}`, "not an eligible assistant conversation"},
		{"unrelated side chat", `{"kind":"handoff","machine":"server","session":"side-1","text":"Note"}`, "not an eligible handoff target"},
		{"self processing chat", `{"kind":"handoff","machine":"server","session":"s1","text":"Note"}`, "own Inbox processing conversation"},
		{"outside direction", `{"kind":"handoff","machine":"server","session":"dir_outside","text":"Note"}`, "was not found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := suggestTool(t, h, tc.args); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want substring %q", err, tc.wantErr)
			}
			h.inbox.mu.Lock()
			g, hs := it.Suggestion, len(it.Handoffs)
			h.inbox.mu.Unlock()
			if g != nil || hs != 0 {
				t.Fatalf("refused call mutated item: suggestion=%+v handoffs=%d", g, hs)
			}
		})
	}

	// Refusal from a handed-off conversation (item set).
	h.inbox.mu.Lock()
	it.Handoffs = []InboxHandoff{{MachineID: "server", SessionID: "m1", At: time.Now().UTC()}}
	h.inbox.mu.Unlock()
	if _, err := mainSuggest(h, "m1", `{"item":"lark:f1:100","kind":"handoff","machine":"server","session":"lead-1","text":"Pass on"}`); err == nil || !strings.Contains(err.Error(), "processes the item") {
		t.Fatalf("handed-off conversation proposing handoff: %v", err)
	}
	h.inbox.mu.Lock()
	it.Handoffs = nil
	h.inbox.mu.Unlock()

	// Closed linked task's side conversation is no longer eligible.
	doneAt := time.Now().UTC()
	putTodo(h.Service, Todo{ID: "todo_1", Title: "Fix login crash", Origin: "task-chat-1", DoneAt: &doneAt})
	if _, err := suggestTool(t, h, `{"kind":"handoff","machine":"server","session":"task-chat-1","text":"Still about login"}`); err == nil || !strings.Contains(err.Error(), "not an eligible handoff target") {
		t.Fatalf("closed task conversation: %v", err)
	}

	// After a refusal, the model can still pick a valid eligible target or fall back to reply.
	if _, err := suggestTool(t, h, `{"kind":"handoff","machine":"server","session":"lead-1","text":"Take this over"}`); err != nil {
		t.Fatalf("recovery to leader failed: %v", err)
	}
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"On it, will follow up shortly."}`); err != nil {
		t.Fatalf("fallback to reply failed: %v", err)
	}
}

func TestInboxSuggestHandoffConfirmDeliversAndAllowsWriteBack(t *testing.T) {
	for _, tc := range []struct {
		name          string
		session       string
		wantChainText string
	}{
		{"main", "m1", "交给了 Persona 主对话"},
		{"leader", "lead-1", "交给了「Release Ops」"},
		{"task", "task-chat-1", "交给了「Login fix chat」"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, lark, it := handoffSuggestHub(t)

			// 1. Processing conversation proposes kind: "handoff".
			if _, err := suggestTool(t, h, `{"kind":"handoff","machine":"server","session":"`+tc.session+`","text":"Original handoff note","recap":"Needs owner team"}`); err != nil {
				t.Fatalf("propose: %v", err)
			}
			// Watch / answer-only device cannot confirm a handoff suggestion.
			if code, out := suggestionCall(t, h, it.ID, `{"version":1,"action":"confirm"}`, roleAnswer); code != 403 {
				t.Fatalf("answer-only confirm: %d %v", code, out)
			}
			if len(sendOnces(h.f)) != 0 {
				t.Fatal("answer-only confirmation sent a message")
			}

			// 2. Owner confirms (with edited note).
			code, out := suggestionCall(t, h, it.ID, `{"version":1,"action":"confirm","text":"Edited handoff note","client_id":"`+handoffClient+`"}`)
			if code != 200 {
				t.Fatalf("confirm: %d %v", code, out)
			}
			if out["suggestion"] != nil {
				t.Fatalf("suggestion not cleared: %v", out["suggestion"])
			}
			if out["done_at"] != nil {
				t.Fatalf("handoff filed the item: %v", out["done_at"])
			}

			// Delivered through Hand to AI (session.send_once) to the target conversation with the context block + edited note.
			sent := sendOnces(h.f)
			if len(sent) != 1 || sent[0]["session_id"] != tc.session || sent[0]["client_msg_id"] != handoffClient {
				t.Fatalf("send_once = %+v", sent)
			}
			text, _ := sent[0]["text"].(string)
			if !strings.Contains(text, `item: "lark:f1:100"`) {
				t.Fatalf("handoff message missing item id:\n%s", text)
			}
			if _, after, _ := strings.Cut(text, "</persona-context>"); after != "\nEdited handoff note" {
				t.Fatalf("handoff message note mismatch:\n%s", text)
			}

			// Recorded in item's handoffs and chain, linked to target session.
			hs, _ := out["handoffs"].([]any)
			if len(hs) != 1 || hs[0].(map[string]any)["machine_id"] != "server" || hs[0].(map[string]any)["session_id"] != tc.session || hs[0].(map[string]any)["op"] != nil {
				t.Fatalf("listed handoffs = %v", hs)
			}
			h.inbox.mu.Lock()
			handlingState := it.Handling.State
			var handedEntry *InboxChainEntry
			var confirmedEntry *InboxChainEntry
			for i := range it.Chain {
				e := &it.Chain[i]
				if e.Kind == ChainConfirmed {
					confirmedEntry = e
				}
				if e.Kind == ChainHandling && e.Link != nil && e.Link.SessionID == tc.session {
					handedEntry = e
				}
			}
			h.inbox.mu.Unlock()
			if handlingState != HandlingDone {
				t.Fatalf("handling state = %q, want done", handlingState)
			}
			if confirmedEntry == nil || !confirmedEntry.Edited || confirmedEntry.Detail != "Edited handoff note" {
				t.Fatalf("confirmed chain entry = %+v", confirmedEntry)
			}
			if handedEntry == nil || handedEntry.Text != tc.wantChainText || handedEntry.Link.MachineID != "server" {
				t.Fatalf("handed chain entry = %+v, want %q", handedEntry, tc.wantChainText)
			}

			// Re-confirming stale version sends nothing more.
			if code, stale := suggestionCall(t, h, it.ID, `{"version":1,"action":"confirm"}`); code != 409 || stale["code"] != "suggestion_stale" {
				t.Fatalf("stale confirm: %d %v", code, stale)
			}
			if len(sendOnces(h.f)) != 1 {
				t.Fatalf("stale confirm resent: %d", len(sendOnces(h.f)))
			}

			// 3. Target conversation can write back with inbox_suggest item, and confirming sends the reply and notifies the target.
			toldCh := make(chan string, 4)
			h.f.mu.Lock()
			h.f.onSend = func(_ *fakeHub, p map[string]any) {
				if p["session_id"] == tc.session {
					if msg, _ := p["text"].(string); msg != "" {
						select {
						case toldCh <- msg:
						default:
						}
					}
				}
			}
			h.f.mu.Unlock()

			wbOut, err := mainSuggest(h, tc.session, `{"item":"lark:f1:100","kind":"reply","text":"Fixed in next release.","note":"Checked with team","recap":"Target handled it"}`)
			if err != nil || !strings.Contains(wbOut, "told here") {
				t.Fatalf("write-back suggest from %s: %q %v", tc.session, wbOut, err)
			}
			cards := handoffCards(t, h, tc.session)
			if len(cards) != 1 || cards[0].Kind != SuggestReply || cards[0].Text != "Fixed in next release." {
				t.Fatalf("write-back cards in %s: %+v", tc.session, cards)
			}
			if code, res := suggestionCall(t, h, it.ID, `{"version":2,"action":"confirm"}`); code != 200 || res["done_by"] != "replied" {
				t.Fatalf("confirm write-back reply: %d %v", code, res)
			}
			if len(lark.got) != 1 || lark.got[0].Text != "Fixed in next release." {
				t.Fatalf("sent lark replies: %+v", lark.got)
			}
			select {
			case note := <-toldCh:
				if !strings.Contains(note, `item: "lark:f1:100"`) {
					t.Fatalf("target notice missing item id:\n%s", note)
				}
			case <-time.After(3 * time.Second):
				t.Fatalf("target %s was not notified of confirmed write-back reply", tc.session)
			}
		})
	}
}

func TestInboxSuggestHandoffConfirmFailsIfTargetArchivedOrSendFails(t *testing.T) {
	t.Run("archived before confirmation", func(t *testing.T) {
		h, _, it := handoffSuggestHub(t)
		if _, err := suggestTool(t, h, `{"kind":"handoff","machine":"server","session":"lead-1","text":"Please take this"}`); err != nil {
			t.Fatal(err)
		}
		if _, err := h.setArchived(context.Background(), "server", "lead-1", true, titleByUser); err != nil {
			t.Fatal(err)
		}
		code, out := suggestionCall(t, h, it.ID, `{"version":1,"action":"confirm"}`)
		if code != 409 || out["code"] != "handoff_target_unavailable" {
			t.Fatalf("confirm archived target: %d %v", code, out)
		}
		if len(sendOnces(h.f)) != 0 {
			t.Fatal("sent message to archived target")
		}
		h.inbox.mu.Lock()
		hs, state := len(it.Handoffs), it.Handling.State
		h.inbox.mu.Unlock()
		if hs != 0 || state != HandlingFailed {
			t.Fatalf("handoffs=%d handling=%q, want 0 / failed", hs, state)
		}
	})

	t.Run("send failure records no handoff", func(t *testing.T) {
		h, _, it := handoffSuggestHub(t)
		if _, err := suggestTool(t, h, `{"kind":"handoff","machine":"server","session":"lead-1","text":"Please take this"}`); err != nil {
			t.Fatal(err)
		}
		h.f.mu.Lock()
		h.f.failSendOnce = map[string]error{"server/lead-1": errors.New("node offline")}
		h.f.mu.Unlock()
		code, out := suggestionCall(t, h, it.ID, `{"version":1,"action":"confirm"}`)
		if code != 502 {
			t.Fatalf("confirm failed send: %d %v", code, out)
		}
		h.inbox.mu.Lock()
		hs, state := len(it.Handoffs), it.Handling.State
		h.inbox.mu.Unlock()
		if hs != 0 || state != HandlingFailed {
			t.Fatalf("handoffs=%d handling=%q, want 0 / failed", hs, state)
		}
	})
}

func TestInboxProcessingConversationCannotDelegateDirectly(t *testing.T) {
	h, _, it := handoffSuggestHub(t)
	ctx := context.Background()
	h.origins.mu.Lock()
	h.origins.loaded = true
	h.origins.m = map[string]*convOrigin{
		"s1": {MachineID: "server", Origin: OriginInbox, Ref: it.ID, At: time.Now().UTC()},
	}
	h.origins.mu.Unlock()

	for _, denied := range []string{"conversation_send", "agent_start", "schedule_followup", "schedule_routine"} {
		res := h.tool(ctx, ToolCall{MachineID: "server", SessionID: "s1", Name: denied, Arguments: json.RawMessage(`{}`)})
		if !res.IsError || !strings.Contains(res.Content[0].Text, "inbox_suggest") {
			t.Fatalf("%s was not refused with inbox_suggest guidance: %+v", denied, res)
		}
	}

	// Meanwhile inbox_suggest kind: "handoff" is allowed through the same tool entry point.
	args := `{"kind":"handoff","machine":"server","session":"lead-1","text":"Hand off via suggestion"}`
	h.inbox.mu.Lock()
	turn := it.Agent.ProcessingTurn
	h.inbox.mu.Unlock()
	if turn != "" {
		args = `{"processing_turn":"` + turn + `","kind":"handoff","machine":"server","session":"lead-1","text":"Hand off via suggestion"}`
	}
	res := h.tool(ctx, ToolCall{MachineID: "server", SessionID: "s1", Name: "inbox_suggest", Arguments: json.RawMessage(args)})
	if res.IsError {
		t.Fatalf("inbox_suggest kind handoff refused: %+v", res)
	}
	if len(sendOnces(h.f)) != 0 {
		t.Fatal("proposing handoff sent immediately before confirmation")
	}
}

func TestInboxProcessingPromptListsEligibleHandoffTargets(t *testing.T) {
	h := agentHub(t)
	h.f.hello.Locale = "en"
	h.f.hello.Owner = "Example owner"
	setSettings(t, h.Service, func(v *Settings) {
		v.Main = "m1"
		v.Leaders = []string{"lead-1", "lead-arch", "dir_outside"}
	})
	h.directions.mu.Lock()
	h.directions.loaded = true
	h.directions.m = map[string]*direction{
		"lead-1":      {MachineID: "server", Name: "Release Ops", By: directionByUser, At: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)},
		"lead-arch":   {MachineID: "server", Name: "Archived Lead", By: directionByUser, At: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)},
		"dir_outside": {Name: "Outside", By: directionByUser, Outside: &directionOutside{MachineID: "server", SessionID: "agent-1"}},
	}
	h.directions.mu.Unlock()
	h.f.setSessions("server",
		proto.Session{SessionID: "m1", Assistant: true, Title: "Main Chat", Status: proto.StatusIdle},
		proto.Session{SessionID: "lead-1", Assistant: true, Title: "Release Ops", Status: proto.StatusIdle},
		proto.Session{SessionID: "lead-arch", Assistant: true, Title: "Archived Lead", Status: proto.StatusIdle},
		proto.Session{SessionID: "task-chat-1", Assistant: true, Title: "Login fix chat", Status: proto.StatusIdle},
		proto.Session{SessionID: "side-1", Assistant: true, Title: "Unrelated side chat", Status: proto.StatusIdle},
		proto.Session{SessionID: "agent-1", Assistant: false, Title: "Coding agent", Status: proto.StatusIdle},
	)
	if _, err := h.setArchived(context.Background(), "server", "lead-arch", true, titleByUser); err != nil {
		t.Fatal(err)
	}
	putTodo(h.Service, Todo{ID: "todo_1", Title: "Fix login crash", Origin: "task-chat-1"})

	it := actionItem("lark:f1:55", time.Now())
	it.Agent = &InboxAgent{State: AgentQueued, Task: "Check release question", TodoID: "todo_1"}
	h.inbox.items[it.ID] = it

	h.startInboxAgents(context.Background(), InboxAgentConfig{Enabled: true})
	promptText, _ := h.f.lastSend(t)["text"].(string)

	for _, want := range []string{
		"Eligible existing conversations for kind handoff",
		"[main] Main Chat (machine: server, session: m1)",
		"[leader] Release Ops (machine: server, session: lead-1)",
		"[task] Login fix chat (machine: server, session: task-chat-1)",
		"kind handoff:",
	} {
		if !strings.Contains(promptText, want) {
			t.Errorf("processing prompt missing %q:\n%s", want, promptText)
		}
	}
	for _, unwanted := range []string{"lead-arch", "dir_outside", "side-1", "agent-1"} {
		if strings.Contains(promptText, unwanted) {
			t.Errorf("processing prompt leaked ineligible target %q:\n%s", unwanted, promptText)
		}
	}
}

func TestInboxHandoffTargetUsesTransferredTaskSupervisor(t *testing.T) {
	h, _, it := handoffSuggestHub(t)
	h.f.mu.Lock()
	h.f.sessions["server"] = append(h.f.sessions["server"],
		proto.Session{SessionID: "sup-chat-2", Assistant: true, Title: "Transferred owner", Status: proto.StatusIdle},
	)
	h.f.mu.Unlock()
	putTodo(h.Service, Todo{
		ID:          "todo_1",
		Title:       "Fix login crash",
		Origin:      "task-chat-1",
		Supervision: &Supervision{Supervisor: "sup-chat-2", SupervisorSource: "transfer"},
	})

	targets := h.inboxPromptHandoffTargets(context.Background(), "todo_1")
	var sessions []string
	for _, tgt := range targets {
		sessions = append(sessions, tgt.Role+":"+tgt.Session)
	}
	got := strings.Join(sessions, ",")
	if !strings.Contains(got, "task:sup-chat-2") || !strings.Contains(got, "task:task-chat-1") {
		t.Fatalf("targets = %q, want both supervisor and origin task conversations", got)
	}
	if _, err := suggestTool(t, h, `{"kind":"handoff","machine":"server","session":"sup-chat-2","text":"Transfer update"}`); err != nil {
		t.Fatalf("suggest handoff to transferred task supervisor: %v", err)
	}
	h.inbox.mu.Lock()
	g := it.Suggestion
	h.inbox.mu.Unlock()
	if g == nil || g.SessionID != "sup-chat-2" || g.TargetTitle != "Transferred owner" {
		t.Fatalf("suggestion = %+v", g)
	}
}

func TestInboxHandoffGoldenTargetsLanguages(t *testing.T) {
	targets := []prompts.InboxHandoffTarget{
		{Machine: "server", Session: "m1", Role: "main", Title: "Main"},
		{Machine: "server", Session: "lead-1", Role: "leader", Title: "Release Ops"},
	}
	for _, locale := range []string{"", "zh-Hans", "en", "ja"} {
		lang := answerLanguage(locale)
		out := prompts.RenderInboxAgent(prompts.InboxAgent{
			Language:       lang,
			Owner:          "Example owner",
			EventID:        "lark:f1:1",
			Title:          "Release question",
			Item:           prompts.InboxItem{Language: lang, Kind: "dm", Context: "Is it ready?"},
			HandoffTargets: targets,
		})
		if !strings.Contains(out, "machine: server, session: m1") || !strings.Contains(out, "machine: server, session: lead-1") {
			t.Fatalf("locale %q missing target coordinates:\n%s", locale, out)
		}
	}
}

func TestInboxSuggestHandoffSurvivesReload(t *testing.T) {
	h, _, it := handoffSuggestHub(t)
	if _, err := suggestTool(t, h, `{"kind":"handoff","machine":"server","session":"lead-1","text":"Keep across restart","recap":"Persisted recap"}`); err != nil {
		t.Fatal(err)
	}
	// Simulate manager reload from persisted store.
	h.inbox.mu.Lock()
	h.inbox.items = map[string]*InboxItem{}
	h.inbox.loaded.Store(false)
	h.inbox.mu.Unlock()
	if err := h.inboxReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.inbox.mu.Lock()
	reloaded := h.inbox.items[it.ID]
	h.inbox.mu.Unlock()
	if reloaded == nil || reloaded.Suggestion == nil || reloaded.Suggestion.Kind != SuggestHandoff ||
		reloaded.Suggestion.MachineID != "server" || reloaded.Suggestion.SessionID != "lead-1" ||
		reloaded.Suggestion.TargetTitle != "Release Ops" || reloaded.Suggestion.Text != "Keep across restart" {
		t.Fatalf("reloaded suggestion = %+v", reloaded)
	}
	if code, out := suggestionCall(t, h, it.ID, `{"version":1,"action":"confirm"}`); code != 200 {
		t.Fatalf("confirm after reload: %d %v", code, out)
	}
	if sent := sendOnces(h.f); len(sent) != 1 || sent[0]["session_id"] != "lead-1" {
		t.Fatalf("sent after reload = %+v", sent)
	}
}
