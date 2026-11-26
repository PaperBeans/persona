package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/botcard"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/rpc"
)

// finalReviewInstallMirror gives an item the same durable AI mirror that suggestion publication creates.
func finalReviewInstallMirror(h *inboxHub, it *InboxItem, text string) *InboxSuggestion {
	h.inbox.mu.Lock()
	defer h.inbox.mu.Unlock()
	if it.Agent == nil {
		it.Agent = &InboxAgent{}
	}
	it.SuggestSeq++
	g := &InboxSuggestion{Version: it.SuggestSeq, Kind: SuggestReply, Text: text, At: time.Now().UTC(),
		SourceOf: suggestionReplyTarget(it), MailTarget: mailTarget(*it)}
	snapshotReplyPolicy(g, it, h.in.cfg.ReplyPolicy)
	it.Suggestion = g
	it.Agent.State, it.Agent.Draft = AgentDrafted, text
	return g
}

func TestInboxReplyPolicyCheckedMailMirrorDoesNotDeadlockLegacySchema(t *testing.T) {
	h := agentHub(t)
	h.in.mail = &fakeMail{}
	cfg := legacyReplyPolicyTestConfig()
	cfg.Sources = []string{"mail"}
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
	now := time.Now()
	it := &InboxItem{ID: "mail:synthetic:1", Source: "mail", Kind: KindMail, Last: "1", Context: "Synthetic coordination request",
		StartedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute), Tier: TierAction, TriagedAt: "1",
		Mail:          &InboxMail{AccountID: "synthetic-account", Mailbox: "INBOX", UIDs: []uint32{9}, Thread: "Synthetic thread"},
		ReplyCategory: "ordinary_coordination", ReplyMode: "owner_draft", ReplyPolicyRev: "test-v1"}
	it.ReplyClassifiedOf = replyPolicyContentVersion(it)
	h.inbox.items[it.ID] = it
	text := "Synthetic checked-mail mirror"
	finalReviewInstallMirror(h, it, text)
	target := mailTarget(*it)
	if target == nil {
		t.Fatal("synthetic mail item has no checked target")
	}
	result := make(chan error, 1)
	go func() {
		_, err := h.replyInboxChecked(context.Background(), it.ID, text, recoveryFirstID, false, &target.UID, &target.Version)
		result <- err
	}()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("checked mail mirror confirmation failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("checked mail /reply rerouted to suggestion confirmation while still holding inbox.running")
	}
	if got := len(h.in.mail.sent); got != 1 {
		t.Fatalf("checked mail mirror physical sends = %d, want 1", got)
	}
}

func finalReviewSendAction(t *testing.T, raw string) map[string]any {
	t.Helper()
	var decoded any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("decode draft card: %v", err)
	}
	var visit func(any) map[string]any
	visit = func(v any) map[string]any {
		switch x := v.(type) {
		case map[string]any:
			if x["op"] == "send" {
				return x
			}
			for _, child := range x {
				if found := visit(child); found != nil {
					return found
				}
			}
		case []any:
			for _, child := range x {
				if found := visit(child); found != nil {
					return found
				}
			}
		}
		return nil
	}
	if action := visit(decoded); action != nil {
		return action
	}
	t.Fatalf("draft card has no send action: %s", raw)
	return nil
}

func TestInboxReplyPolicyWorkbenchOldCardCannotConfirmReplacementLegacySchema(t *testing.T) {
	needsLarkLayer(t) // the integrated Workbench renderer and actions are the lark module's
	c := &recoveryReplier{}
	h, _, it := handledHub(t)
	h.in.mu.Lock()
	h.in.conns = []connector.Connector{c}
	h.in.mu.Unlock()
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
	it.ReplyCategory, it.ReplyMode = "ordinary_coordination", "owner_draft"
	it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
	h.f.answerApp(AppFeishu, FeishuStatusGet, func(json.RawMessage) (any, error) {
		return FeishuStatus{Ready: true, Enabled: true, Owner: "synthetic-owner", Features: workbenchOnFeatures}, nil
	})
	updated := make(chan struct{}, 1)
	h.f.answerApp(AppFeishu, FeishuUpdateCard, func(json.RawMessage) (any, error) {
		updated <- struct{}{}
		return map[string]any{}, nil
	})
	oldText := "Text shown on the old Workbench card"
	finalReviewInstallMirror(h, it, oldText)
	actionValue := finalReviewSendAction(t, h.draftCard(context.Background(), *it, oldText, nil, "").JSON())

	// Another device replaces both the draft and the source position after the old card was rendered.
	h.inbox.mu.Lock()
	it.Last = "new-source-position"
	it.Lark.ReplyTo = "synthetic-new-recipient"
	it.ReplyClassifiedOf = replyPolicyContentVersion(it)
	h.inbox.mu.Unlock()
	finalReviewInstallMirror(h, it, "Replacement draft from another device")
	h.in.model = fakeModel(t, `{"category":"ordinary_coordination"}`)
	action := &botcard.CardAction{}
	action.Action.Value = actionValue
	action.Action.FormValue = map[string]any{"text": oldText}
	action.Context.MessageID = "synthetic-old-workbench-card"
	h.workbenchInboxAction(context.Background(), action)
	select {
	case <-updated:
	case <-time.After(3 * time.Second):
		t.Fatal("old Workbench card send did not finish")
	}
	if calls := c.calls(); len(calls) != 0 {
		t.Fatalf("old Workbench card confirmed its text as an edit of a replacement: %+v", calls)
	}
}

func TestInboxReplyPolicyMirrorReplyPreservesRoleAndSelectedChangesLegacySchema(t *testing.T) {
	for _, answerOnly := range []bool{false, true} {
		name := "full role deselects stored moves"
		if answerOnly {
			name = "answer-only cannot execute stored moves"
		}
		t.Run(name, func(t *testing.T) {
			c := &recoveryReplier{}
			h, _, it := handledHub(t)
			h.in.mu.Lock()
			h.in.conns = []connector.Connector{c}
			h.in.mu.Unlock()
			h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
			it.ReplyCategory, it.ReplyMode = "ordinary_coordination", "owner_draft"
			it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
			it.Doc = &InboxDoc{}
			text := "Mirrored reply with a stored status move"
			g := finalReviewInstallMirror(h, it, text)
			g.Changes = []DocChange{{Ref: "synthetic-row", Field: "Status", Value: "Done"}}
			body := fmt.Sprintf(`{"text":%q,"client_id":%q}`, text, recoveryFirstID)
			role := ""
			if answerOnly {
				role = "answer-only"
			}
			status, _ := inboxCall(t, h.routes(), http.MethodPost, "/api/v1/inbox/"+it.ID+"/reply", body, role)
			calls := c.calls()
			if answerOnly {
				if status != http.StatusForbidden || len(calls) != 0 {
					t.Fatalf("answer-only /reply executed stored moves: status=%d calls=%+v", status, calls)
				}
				return
			}
			if status != http.StatusOK || len(calls) != 1 || len(calls[0].Changes) != 0 {
				t.Fatalf("deselected mirror moves reached sender: status=%d calls=%+v", status, calls)
			}
		})
	}
}

func TestInboxReplyPolicyOnDemandDraftInheritsHandoffUncertaintyLegacySchema(t *testing.T) {
	c := &recoveryReplier{err: rpc.ErrDropped}
	h, _, it := handledHub(t)
	h.in.mu.Lock()
	h.in.conns = []connector.Connector{c}
	h.in.mu.Unlock()
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
	it.ReplyCategory, it.ReplyMode = "ordinary_coordination", "owner_draft"
	it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
	setSettings(t, h.Service, func(v *Settings) { v.Main = "m1" })
	handOff(t, h, it)
	text := "Same reply after a handed-off uncertain send"
	if _, err := mainSuggest(h, "m1", fmt.Sprintf(`{"item":%q,"kind":"reply","text":%q}`, it.ID, text)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.actOnSuggestion(context.Background(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm, ClientID: recoveryFirstID}, true); !replyInDoubt(err) {
		t.Fatalf("first handed-off send error = %v, want uncertain", err)
	}
	c.answer(nil)
	h.in.model = fakeModel(t, text)
	returned, err := h.draftReply(context.Background(), it.ID, "")
	if !errors.Is(err, errSuggestionStale) || returned != "" {
		t.Fatalf("on-demand draft created another authorization for an uncertain physical send: text=%q err=%v", returned, err)
	}
	if got := len(c.calls()); got != 1 {
		t.Fatalf("equivalent on-demand draft repeated the uncertain send: %d calls", got)
	}
}

func TestInboxReplyPolicyQueueRechecksLaterJobAuthorizationLegacySchema(t *testing.T) {
	h := agentHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
	now := time.Now()
	first := actionItem("lark:queue:first", now)
	second := actionItem("lark:queue:second", now.Add(-time.Second))
	for _, it := range []*InboxItem{first, second} {
		it.Source = "lark"
		it.Agent = &InboxAgent{State: AgentQueued}
		it.ReplyCategory, it.ReplyMode = "ordinary_coordination", "owner_draft"
		it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
		h.inbox.items[it.ID] = it
	}
	var once sync.Once
	h.f.onAssistantSend = func(_ *fakeHub, _ map[string]any) {
		once.Do(func() {
			h.inbox.mu.Lock()
			second.Context = "Synthetic newly arrived approval request"
			second.Last = "restricted-new-content"
			second.ReplyCategory, second.ReplyMode = "approval_permission", "owner_only"
			second.ReplyClassifiedOf = replyPolicyContentVersion(second)
			h.inbox.mu.Unlock()
		})
	}
	before := len(conversationStarts(h.f))
	h.startInboxAgents(context.Background(), InboxAgentConfig{Enabled: true, Concurrency: 2})
	starts := conversationStarts(h.f)
	if got := len(starts) - before; got != 1 {
		t.Fatalf("queue started %d conversations after the later job became owner-only, want only the first", got)
	}
	for _, start := range starts[before:] {
		if strings.Contains(fmt.Sprint(start), second.ID) {
			t.Fatalf("later job started with its stale prompt and authorization: %+v", start)
		}
	}
	checkPolicyReleased(t, second, h.inbox.userLang())
}

func TestInboxReplyPolicyCheckedMailMirrorDoesNotDeadlock(t *testing.T) {
	h := agentHub(t)
	h.in.mail = &fakeMail{}
	cfg := replyPolicyTestConfig()
	cfg.Sources = []string{"mail"}
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
	now := time.Now()
	it := &InboxItem{ID: "mail:synthetic:1", Source: "mail", Kind: KindMail, Last: "1", BodyOf: "1", Context: "Synthetic coordination request",
		StartedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute), Tier: TierAction, TriagedAt: "1",
		Mail: &InboxMail{AccountID: "synthetic-account", Mailbox: "INBOX", UIDs: []uint32{9}, Thread: "Synthetic thread"}, ReplyPolicy: &InboxReplyPolicySnapshot{Category: "ordinary_coordination", Route: "owner_draft", PolicyRevision: compileReplyPolicy(cfg).revision}}
	{
		replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
	}
	h.inbox.items[it.ID] = it
	text := "Synthetic checked-mail mirror"
	finalReviewInstallMirror(h, it, text)
	h.in.setModel(fakeModel(t, `{"category":"ordinary_coordination"}`))
	target := mailTarget(*it)
	if target == nil {
		t.Fatal("synthetic mail item has no checked target")
	}
	result := make(chan error, 1)
	go func() {
		_, err := h.replyInboxChecked(context.Background(), it.ID, text, recoveryFirstID, false, &target.UID, &target.Version)
		result <- err
	}()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("checked mail mirror confirmation failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("checked mail /reply rerouted to suggestion confirmation while still holding inbox.running")
	}
	if got := len(h.in.mail.sent); got != 1 {
		t.Fatalf("checked mail mirror physical sends = %d, want 1", got)
	}
}

func TestInboxReplyPolicyWorkbenchOldCardCannotConfirmReplacement(t *testing.T) {
	needsLarkLayer(t) // the integrated Workbench renderer and actions are the lark module's
	c := &recoveryReplier{}
	h, _, it := handledHub(t)
	h.in.mu.Lock()
	h.in.conns = []connector.Connector{c}
	h.in.mu.Unlock()
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
	{
		replyPolicyTestSetCategory(it, "ordinary_coordination")
		replyPolicyTestSetRoute(it, "owner_draft")
	}
	{
		replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
		replyPolicyTestSetPolicyRevision(it, "test-v1")
	}
	h.f.answerApp(AppFeishu, FeishuStatusGet, func(json.RawMessage) (any, error) {
		return FeishuStatus{Ready: true, Enabled: true, Owner: "synthetic-owner", Features: workbenchOnFeatures}, nil
	})
	updated := make(chan struct{}, 1)
	h.f.answerApp(AppFeishu, FeishuUpdateCard, func(json.RawMessage) (any, error) {
		updated <- struct{}{}
		return map[string]any{}, nil
	})
	oldText := "Text shown on the old Workbench card"
	finalReviewInstallMirror(h, it, oldText)
	actionValue := finalReviewSendAction(t, h.draftCard(context.Background(), *it, oldText, nil, "").JSON())

	// Another device replaces both the draft and the source position after the old card was rendered.
	h.inbox.mu.Lock()
	it.Last = "new-source-position"
	it.Lark.ReplyTo = "synthetic-new-recipient"
	{
		replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
	}
	h.inbox.mu.Unlock()
	finalReviewInstallMirror(h, it, "Replacement draft from another device")
	h.in.model = fakeModel(t, `{"category":"ordinary_coordination"}`)
	action := &botcard.CardAction{}
	action.Action.Value = actionValue
	action.Action.FormValue = map[string]any{"text": oldText}
	action.Context.MessageID = "synthetic-old-workbench-card"
	h.workbenchInboxAction(context.Background(), action)
	select {
	case <-updated:
	case <-time.After(3 * time.Second):
		t.Fatal("old Workbench card send did not finish")
	}
	if calls := c.calls(); len(calls) != 0 {
		t.Fatalf("old Workbench card confirmed its text as an edit of a replacement: %+v", calls)
	}
}

func TestInboxReplyPolicyMirrorReplyPreservesRoleAndSelectedChanges(t *testing.T) {
	for _, answerOnly := range []bool{false, true} {
		name := "full role deselects stored moves"
		if answerOnly {
			name = "answer-only cannot execute stored moves"
		}
		t.Run(name, func(t *testing.T) {
			c := &recoveryReplier{}
			h, _, it := handledHub(t)
			h.in.mu.Lock()
			h.in.conns = []connector.Connector{c}
			h.in.mu.Unlock()
			h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
			{
				replyPolicyTestSetCategory(it, "ordinary_coordination")
				replyPolicyTestSetRoute(it, "owner_draft")
			}
			{
				replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
				replyPolicyTestSetPolicyRevision(it, "test-v1")
			}
			it.Doc = &InboxDoc{}
			text := "Mirrored reply with a stored status move"
			g := finalReviewInstallMirror(h, it, text)
			g.Changes = []DocChange{{Ref: "synthetic-row", Field: "Status", Value: "Done"}}
			h.in.setModel(fakeModel(t, `{"category":"ordinary_coordination"}`))
			body := fmt.Sprintf(`{"text":%q,"client_id":%q}`, text, recoveryFirstID)
			role := ""
			if answerOnly {
				role = "answer-only"
			}
			status, _ := inboxCall(t, h.routes(), http.MethodPost, "/api/v1/inbox/"+it.ID+"/reply", body, role)
			calls := c.calls()
			if answerOnly {
				if status != http.StatusForbidden || len(calls) != 0 {
					t.Fatalf("answer-only /reply executed stored moves: status=%d calls=%+v", status, calls)
				}
				return
			}
			if status != http.StatusOK || len(calls) != 1 || len(calls[0].Changes) != 0 {
				t.Fatalf("deselected mirror moves reached sender: status=%d calls=%+v", status, calls)
			}
		})
	}
}

func TestInboxReplyPolicyOnDemandDraftInheritsHandoffUncertainty(t *testing.T) {
	c := &recoveryReplier{err: rpc.ErrDropped}
	h, _, it := handledHub(t)
	h.in.mu.Lock()
	h.in.conns = []connector.Connector{c}
	h.in.mu.Unlock()
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
	{
		replyPolicyTestSetCategory(it, "ordinary_coordination")
		replyPolicyTestSetRoute(it, "owner_draft")
	}
	{
		replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
		replyPolicyTestSetPolicyRevision(it, "test-v1")
	}
	setSettings(t, h.Service, func(v *Settings) { v.Main = "m1" })
	h.in.setModel(fakeModel(t, `{"category":"ordinary_coordination"}`))
	handOff(t, h, it)
	text := "Same reply after a handed-off uncertain send"
	if _, err := mainSuggest(h, "m1", fmt.Sprintf(`{"item":%q,"kind":"reply","text":%q}`, it.ID, text)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.actOnSuggestion(context.Background(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm, ClientID: recoveryFirstID}, true); !replyInDoubt(err) {
		t.Fatalf("first handed-off send error = %v, want uncertain", err)
	}
	c.answer(nil)
	replyPolicyOrdinaryDraftModel(t, h, text)
	returned, err := h.draftReply(context.Background(), it.ID, "")
	if !errors.Is(err, errSuggestionStale) || returned != "" {
		t.Fatalf("on-demand draft created another authorization for an uncertain physical send: text=%q err=%v", returned, err)
	}
	if got := len(c.calls()); got != 1 {
		t.Fatalf("equivalent on-demand draft repeated the uncertain send: %d calls", got)
	}
}

func TestInboxReplyPolicyQueueRechecksLaterJobAuthorization(t *testing.T) {
	h := agentHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
	now := time.Now()
	first := actionItem("lark:queue:first", now)
	second := actionItem("lark:queue:second", now.Add(-time.Second))
	for _, it := range []*InboxItem{first, second} {
		it.Source = "lark"
		it.Agent = &InboxAgent{State: AgentQueued}
		{
			replyPolicyTestSetCategory(it, "ordinary_coordination")
			replyPolicyTestSetRoute(it, "owner_draft")
		}
		{
			replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
			replyPolicyTestSetPolicyRevision(it, "test-v1")
		}
		h.inbox.items[it.ID] = it
	}
	var once sync.Once
	h.f.onAssistantSend = func(_ *fakeHub, _ map[string]any) {
		once.Do(func() {
			h.inbox.mu.Lock()
			second.Context = "Synthetic newly arrived approval request"
			second.Last = "restricted-new-content"
			{
				replyPolicyTestSetCategory(second, "approval_permission")
				replyPolicyTestSetRoute(second, "owner_only")
			}
			{
				replyPolicyTestSetContentVersion(second, replyPolicyContentVersion(second))
			}
			h.inbox.mu.Unlock()
		})
	}
	before := len(conversationStarts(h.f))
	h.startInboxAgents(context.Background(), InboxAgentConfig{Enabled: true, Concurrency: 2})
	starts := conversationStarts(h.f)
	if got := len(starts) - before; got != 1 {
		t.Fatalf("queue started %d conversations after the later job became owner-only, want only the first", got)
	}
	for _, start := range starts[before:] {
		if strings.Contains(fmt.Sprint(start), second.ID) {
			t.Fatalf("later job started with its stale prompt and authorization: %+v", start)
		}
	}
	checkReplyPolicyReleased(t, second, h.inbox.userLang())
}
