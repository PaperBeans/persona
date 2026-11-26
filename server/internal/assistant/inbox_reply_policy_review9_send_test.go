package assistant

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/rpc"
)

func TestInboxReplyPolicyLegacyMirrorRetryAfterLostSuccessUsesCompletedReceiptLegacySchema(t *testing.T) {
	c := &recoveryReplier{}
	h, _, it := handledHub(t)
	h.in.mu.Lock()
	h.in.conns[0] = c
	h.in.mu.Unlock()
	replyPolicyReview8OrdinaryItemLegacy(h, it)
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"Synthetic mirror whose successful response is lost"}`); err != nil {
		t.Fatal(err)
	}
	g := currentSuggestion(it)
	if g == nil {
		t.Fatal("test setup did not publish a mirror")
	}
	body := fmt.Sprintf(`{"text":%q}`, g.Text)
	code, out := inboxCall(t, h.routes(), http.MethodPost, "/api/v1/inbox/"+it.ID+"/reply", body)
	if code != http.StatusOK {
		t.Fatalf("first mirror confirmation failed: code=%d out=%+v", code, out)
	}
	a := recoveryAttempt(t, h, it, g.Version)
	if a.Status != suggestionSent {
		t.Fatalf("first mirror confirmation did not complete: %+v", a)
	}
	h.inbox.mu.Lock()
	open := it.open()
	current := currentSuggestion(it)
	h.inbox.mu.Unlock()
	if !open || current != nil {
		t.Fatalf("test setup did not leave an open item with a completed receipt: open=%t suggestion=%+v", open, current)
	}

	// The caller never received the successful response above, so it repeats the identical legacy request without a client ID.
	inboxCall(t, h.routes(), http.MethodPost, "/api/v1/inbox/"+it.ID+"/reply", body)
	if calls := c.calls(); len(calls) != 1 {
		t.Fatalf("legacy retry after a lost success response made %d physical sends, want the one completed send: %+v", len(calls), calls)
	}
}

func TestInboxReplyPolicyRejectedOnDemandDraftCannotBecomeDirectReplyLegacySchema(t *testing.T) {
	const generated = "Synthetic on-demand draft rejected on another device"
	newRejectedDraft := func(t *testing.T) (*inboxHub, *extensionLark, *InboxItem) {
		t.Helper()
		h, lark, it := handledHub(t)
		replyPolicyReview8OrdinaryItemLegacy(h, it)
		h.in.model = fakeModel(t, generated)
		code, out := inboxCall(t, h.routes(), http.MethodPost, "/api/v1/inbox/"+it.ID+"/draft", `{}`)
		if code != http.StatusOK || out["text"] != generated {
			t.Fatalf("on-demand draft failed: code=%d out=%+v", code, out)
		}
		code, out = suggestionCall(t, h, it.ID, `{"version":1,"action":"reject"}`)
		if code != http.StatusOK {
			t.Fatalf("rejecting the mirror failed: code=%d out=%+v", code, out)
		}
		h.inbox.mu.Lock()
		it.ReplyCategory, it.ReplyMode = "approval_permission", "owner_only"
		it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
		it.ReplyReviewed = false
		h.inbox.mu.Unlock()
		return h, lark, it
	}

	t.Run("rejected generated text", func(t *testing.T) {
		h, lark, it := newRejectedDraft(t)
		body := fmt.Sprintf(`{"text":%q}`, generated)
		inboxCall(t, h.routes(), http.MethodPost, "/api/v1/inbox/"+it.ID+"/reply", body)
		if len(lark.got) != 0 {
			t.Fatalf("rejected on-demand text became a direct owner reply after the item was restricted: %+v", lark.got)
		}
	})

	t.Run("different direct owner text", func(t *testing.T) {
		h, lark, it := newRejectedDraft(t)
		const ownerText = "A different reply written directly by the owner"
		body := fmt.Sprintf(`{"text":%q}`, ownerText)
		code, out := inboxCall(t, h.routes(), http.MethodPost, "/api/v1/inbox/"+it.ID+"/reply", body)
		if code != http.StatusOK || len(lark.got) != 1 || lark.got[0].Text != ownerText {
			t.Fatalf("genuine direct owner-authored reply was gated with the rejected draft: code=%d out=%+v sends=%+v", code, out, lark.got)
		}
	})
}

func TestInboxReplyPolicyLegacyChangesOnlyMirrorConfirmationPreservesEmptyTextLegacySchema(t *testing.T) {
	h, lark, it := handledHub(t)
	h.inbox.mu.Lock()
	it.Lark = nil
	it.Kind = KindDoc
	it.Doc = &InboxDoc{NoticeID: "synthetic-document"}
	h.inbox.mu.Unlock()
	replyPolicyReview8OrdinaryItemLegacy(h, it)
	const generated = "Synthetic AI comment that a changes-only confirmation must not post"
	if g := finalReviewInstallMirror(h, it, generated); g == nil {
		t.Fatal("test setup did not install the document mirror")
	}
	body := `{"text":"","changes":[{"ref":"synthetic-row","field":"status","value":"done"}]}`
	code, out := inboxCall(t, h.routes(), http.MethodPost, "/api/v1/inbox/"+it.ID+"/reply", body)
	if code != http.StatusOK {
		t.Fatalf("changes-only mirror confirmation failed: code=%d out=%+v", code, out)
	}
	if len(lark.got) != 1 || lark.got[0].Text != "" || len(lark.got[0].Changes) != 1 {
		t.Fatalf("changes-only confirmation did not preserve explicit empty text: %+v", lark.got)
	}
	if got := lark.got[0].Changes[0]; got.Ref != "synthetic-row" || got.Field != "status" || got.Value != "done" {
		t.Fatalf("changes-only confirmation changed the selected move: %+v", got)
	}
}

func TestInboxReplyPolicyFailedPublicationUnmarkedRowCannotSendAfterReloadLegacySchema(t *testing.T) {
	const generated = "Synthetic draft persisted before its failure tombstone"
	newFailedPublication := func(t *testing.T) (*inboxHub, *recoveryReplier, *InboxItem) {
		t.Helper()
		c := &recoveryReplier{}
		h, _, it := handledHub(t)
		h.in.mu.Lock()
		h.in.conns[0] = c
		h.in.mu.Unlock()
		replyPolicyReview8OrdinaryItemLegacy(h, it)
		h.in.model = fakeModel(t, generated)
		st := &replyPolicyDraftOneShotFailStore{inboxMemStore: h.st, fail: true}
		st.before = func() {
			h.inbox.mu.Lock()
			h.inbox.touched(it.ID)
			h.inbox.mu.Unlock()
		}
		h.setStore(func() Store { return st })
		return h, c, it
	}

	t.Run("restart before tombstone", func(t *testing.T) {
		h, c, it := newFailedPublication(t)
		var restarted *Service
		h.inbox.onDemandDraftSaveHook = func() {
			h.inbox.onDemandDraftSaveHook = nil
			if _, err := suggestTool(t, h, `{"recap":"Metadata persisted in the publication gap"}`); err != nil {
				t.Errorf("persisting the intermediate row failed: %v", err)
				return
			}
			restarted = New(Options{Hub: h.f, Store: func() Store { return h.st }, Logf: testLogf(t)})
			if err := restarted.inboxReady(t.Context()); err != nil {
				t.Errorf("reloading the intermediate row failed: %v", err)
				restarted = nil
				return
			}
			restarted.inbox.mu.Lock()
			loaded := restarted.inbox.items[it.ID]
			g := currentSuggestion(loaded)
			restarted.inbox.mu.Unlock()
			if g == nil || g.Text != generated {
				t.Errorf("test setup did not reload the intermediate mirror: %+v", g)
				restarted = nil
			}
		}
		if text, err := h.draftReply(t.Context(), it.ID, ""); err == nil || text != "" {
			t.Fatalf("failed publication returned text: text=%q err=%v", text, err)
		}
		if restarted == nil {
			t.Fatal("test setup did not reconstruct from the intermediate persisted row")
		}
		_, sendErr := restarted.replyInboxChecked(t.Context(), it.ID, generated, recoveryFreshID, false, nil, nil)
		if sendErr == nil {
			t.Error("intermediate persisted mirror authorized a legacy reply after reload")
		}
		if calls := c.calls(); len(calls) != 0 {
			t.Fatalf("intermediate persisted mirror caused a physical send after reload: %+v", calls)
		}
	})

	t.Run("tombstone write fails", func(t *testing.T) {
		h, c, it := newFailedPublication(t)
		h.inbox.onDemandDraftSaveHook = func() {
			h.inbox.onDemandDraftSaveHook = nil
			h.setStore(func() Store { return h.st })
			if _, err := suggestTool(t, h, `{"recap":"Metadata persisted before the failing tombstone"}`); err != nil {
				t.Errorf("persisting the intermediate row failed: %v", err)
			}
			h.setStore(func() Store {
				return &replyPolicyDraftFailStore{inboxMemStore: h.st, before: func() {}}
			})
		}
		if text, err := h.draftReply(t.Context(), it.ID, ""); err == nil || text != "" {
			t.Fatalf("failed publication returned text: text=%q err=%v", text, err)
		}
		h.setStore(func() Store { return h.st })
		restarted := New(Options{Hub: h.f, Store: func() Store { return h.st }, Logf: testLogf(t)})
		if err := restarted.inboxReady(t.Context()); err != nil {
			t.Fatal(err)
		}
		restarted.inbox.mu.Lock()
		loaded := restarted.inbox.items[it.ID]
		g := currentSuggestion(loaded)
		restarted.inbox.mu.Unlock()
		if g == nil || g.Text != generated {
			t.Fatalf("test setup did not retain the row from before the failed tombstone: %+v", g)
		}
		_, sendErr := restarted.replyInboxChecked(t.Context(), it.ID, generated, recoveryFreshID, false, nil, nil)
		if sendErr == nil {
			t.Error("mirror whose tombstone write failed authorized a legacy reply after reload")
		}
		if calls := c.calls(); len(calls) != 0 {
			t.Fatalf("failed tombstone write left a physically sendable mirror after reload: %+v", calls)
		}
	})
}

func TestInboxReplyPolicyWorkbenchTransientDraftDoesNotBorrowLiveMirrorVersionLegacySchema(t *testing.T) {
	needsLarkLayer(t) // the integrated Workbench renderer and actions are the lark module's
	c := &recoveryReplier{}
	h, _, it := handledHub(t)
	h.in.mu.Lock()
	h.in.conns[0] = c
	h.in.mu.Unlock()
	h.setConfig(func(cfg *assistantwire.InboxConfig) { cfg.ReplyPolicy = nil })
	updated := replyPolicyEnableWorkbench(h)
	posts := replyPolicyReview8DraftPosts(h)
	const generated = "Transient draft A whose card must remain unversioned"
	const replacement = "Authorized mirror B installed before the transient card renders"
	// The old path sends A as an edit of B. Both the outer edit veto and the attempt-side veto need a classification.
	replyPolicyDraftSequenceModel(t, h, nil, generated,
		`{"category":"ordinary_coordination"}`, `{"category":"ordinary_coordination"}`)
	h.inbox.workbenchDraftHook = func() {
		h.inbox.workbenchDraftHook = nil
		replyPolicyReview8OrdinaryItemLegacy(h, it)
		finalReviewInstallMirror(h, it, replacement)
	}
	replyPolicyReview8WorkbenchAction(h, map[string]any{"a": "ib", "op": "draft", "id": it.ID}, nil, "synthetic-review9-transient-source")
	var raw string
	select {
	case raw = <-posts:
	case <-time.After(3 * time.Second):
		t.Fatal("Workbench did not render the transient draft")
	}
	value := replyPolicyWorkbenchActionValue(t, raw, "send")
	if version := replyPolicyReview8Version(value["version"]); version != 0 {
		t.Errorf("transient draft borrowed live mirror version %d, want explicit zero", version)
	}
	replyPolicyReview8WorkbenchAction(h, value, map[string]any{"text": generated}, "synthetic-review9-transient-card")
	select {
	case <-updated:
	case <-time.After(3 * time.Second):
		t.Fatal("transient Workbench send did not finish")
	}
	if calls := c.calls(); len(calls) != 0 {
		t.Fatalf("transient draft was sent as an edit of the live mirror it borrowed: %+v", calls)
	}
}

func TestInboxReplyPolicyWorkbenchTransientSendRechecksPolicyAfterMutexWaitLegacySchema(t *testing.T) {
	for _, tc := range []struct {
		name     string
		activate bool
	}{
		{name: "policy activated", activate: true},
		{name: "policy stays disabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, lark, it := handledHub(t)
			h.setConfig(func(cfg *assistantwire.InboxConfig) { cfg.ReplyPolicy = nil })
			h.inbox.mu.Lock()
			it.ReplyCategory, it.ReplyMode = "approval_permission", "owner_only"
			it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
			it.ReplyReviewed = false
			h.inbox.mu.Unlock()
			if mirroredLegacyReply(it) != nil {
				t.Fatal("test setup unexpectedly has a mirror")
			}
			entered := make(chan struct{})
			once := &inboxReply{beforeLock: func() { close(entered) }}
			once.mu.Lock()
			unlocked := false
			defer func() {
				if !unlocked {
					once.mu.Unlock()
				}
			}()
			h.inbox.replies.Store(it.ID, once)
			result := make(chan error, 1)
			go func() {
				_, err := h.replyInboxFromVersion(t.Context(), it.ID, "Synthetic transient AI draft", "feishu-workbench-review9-policy-wait", true, 0)
				result <- err
			}()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("transient Workbench send did not reach the per-item send lock")
			}
			if tc.activate {
				h.setConfig(func(cfg *assistantwire.InboxConfig) { cfg.ReplyPolicy = legacyReplyPolicyTestConfig() })
			}
			once.mu.Unlock()
			unlocked = true
			var err error
			select {
			case err = <-result:
			case <-time.After(3 * time.Second):
				t.Fatal("transient Workbench send did not finish")
			}
			if tc.activate {
				if err == nil {
					t.Error("policy activation during the send-mutex wait went unnoticed")
				}
				if len(lark.got) != 0 {
					t.Fatalf("transient AI draft dispatched after policy activation during the wait: %+v", lark.got)
				}
				return
			}
			if err != nil {
				t.Fatalf("policy-disabled transient Workbench send failed: %v", err)
			}
			if len(lark.got) != 1 {
				t.Fatalf("policy-disabled transient Workbench send changed legacy behavior: %+v", lark.got)
			}
		})
	}
}

func TestInboxReplyPolicyUnversionedWorkbenchCardCannotRetryFailedAttemptLegacySchema(t *testing.T) {
	needsLarkLayer(t) // the integrated Workbench renderer and actions are the lark module's
	const text = "Same text on an old card and its replacement"
	newAttempt := func(t *testing.T) (*inboxHub, *recoveryReplier, *InboxItem, map[string]any, map[string]any, chan struct{}) {
		t.Helper()
		c := &recoveryReplier{err: &rpc.Error{Code: assistantwire.CodeInboxSendRateLimited, Data: json.RawMessage(`{"retry_after":30}`)}}
		h, _, it := handledHub(t)
		h.in.mu.Lock()
		h.in.conns[0] = c
		h.in.mu.Unlock()
		replyPolicyReview8OrdinaryItemLegacy(h, it)
		updated := replyPolicyEnableWorkbench(h)
		finalReviewInstallMirror(h, it, text)
		oldValue := replyPolicyWorkbenchActionValue(t, h.draftCard(t.Context(), *it, text, nil, "").JSON(), "send")
		delete(oldValue, "version")
		finalReviewInstallMirror(h, it, text)
		currentValue := replyPolicyWorkbenchActionValue(t, h.draftCard(t.Context(), *it, text, nil, "").JSON(), "send")
		if version := replyPolicyReview8Version(currentValue["version"]); version != 2 {
			t.Fatalf("test setup did not render replacement version 2: version=%d", version)
		}
		return h, c, it, oldValue, currentValue, updated
	}
	failFirstAttempt := func(t *testing.T, h *inboxHub, c *recoveryReplier, it *InboxItem, value map[string]any, updated chan struct{}) InboxSuggestionAttempt {
		t.Helper()
		replyPolicyReview8WorkbenchAction(h, value, map[string]any{"text": text}, "synthetic-review9-versioned-first-attempt")
		select {
		case <-updated:
		case <-time.After(3 * time.Second):
			t.Fatal("versioned Workbench attempt did not finish")
		}
		a := recoveryAttempt(t, h, it, 2)
		if a.Status != suggestionFailed || !a.Retryable || a.Code != assistantwire.CodeInboxSendRateLimited {
			t.Fatalf("test setup did not create a known-none retryable attempt: %+v", a)
		}
		if calls := c.calls(); len(calls) != 1 {
			t.Fatalf("test setup made %d physical sends, want one: %+v", len(calls), calls)
		}
		c.answer(nil)
		return a
	}

	t.Run("old unversioned card", func(t *testing.T) {
		h, c, it, oldValue, currentValue, updated := newAttempt(t)
		failFirstAttempt(t, h, c, it, currentValue, updated)
		replyPolicyReview8WorkbenchAction(h, oldValue, map[string]any{"text": text}, "synthetic-review9-old-unversioned-card")
		select {
		case <-updated:
		case <-time.After(3 * time.Second):
			t.Fatal("old unversioned Workbench submission did not finish")
		}
		if calls := c.calls(); len(calls) != 1 {
			t.Fatalf("old unversioned card retried replacement version 2: %+v", calls)
		}
		if a := recoveryAttempt(t, h, it, 2); a.Status != suggestionFailed || !a.Retryable {
			t.Errorf("unversioned receipt lookup changed the durable attempt: %+v", a)
		}
	})

	t.Run("matching version may retry", func(t *testing.T) {
		h, c, it, _, currentValue, updated := newAttempt(t)
		first := failFirstAttempt(t, h, c, it, currentValue, updated)
		replyPolicyReview8WorkbenchAction(h, currentValue, map[string]any{"text": text}, "synthetic-review9-matching-version-retry")
		select {
		case <-updated:
		case <-time.After(3 * time.Second):
			t.Fatal("matching-version Workbench retry did not finish")
		}
		calls := c.calls()
		if len(calls) != 2 || calls[0].ClientID != first.ClientID || calls[1].ClientID != first.ClientID {
			t.Fatalf("matching-version retry did not reuse its frozen physical identity: %+v", calls)
		}
		if a := recoveryAttempt(t, h, it, 2); a.Status != suggestionSent {
			t.Errorf("matching-version retry did not complete: %+v", a)
		}
	})
}

func TestInboxReplyPolicyLegacyMirrorRetryAfterLostSuccessUsesCompletedReceipt(t *testing.T) {
	c := &recoveryReplier{}
	h, _, it := handledHub(t)
	h.in.mu.Lock()
	h.in.conns[0] = c
	h.in.mu.Unlock()
	replyPolicyReview8OrdinaryItem(h, it)
	if _, err := suggestOrdinaryReplyForFixture(t, h, `{"kind":"reply","text":"Synthetic mirror whose successful response is lost"}`); err != nil {
		t.Fatal(err)
	}
	h.in.setModel(fakeModel(t, `{"category":"ordinary_coordination"}`))
	g := currentSuggestion(it)
	if g == nil {
		t.Fatal("test setup did not publish a mirror")
	}
	body := fmt.Sprintf(`{"text":%q}`, g.Text)
	code, out := inboxCall(t, h.routes(), http.MethodPost, "/api/v1/inbox/"+it.ID+"/reply", body)
	if code != http.StatusOK {
		t.Fatalf("first mirror confirmation failed: code=%d out=%+v", code, out)
	}
	a := recoveryAttempt(t, h, it, g.Version)
	if a.Status != suggestionSent {
		t.Fatalf("first mirror confirmation did not complete: %+v", a)
	}
	h.inbox.mu.Lock()
	open := it.open()
	current := currentSuggestion(it)
	h.inbox.mu.Unlock()
	if !open || current != nil {
		t.Fatalf("test setup did not leave an open item with a completed receipt: open=%t suggestion=%+v", open, current)
	}

	// The caller never received the successful response above, so it repeats the identical legacy request without a client ID.
	code, out = inboxCall(t, h.routes(), http.MethodPost, "/api/v1/inbox/"+it.ID+"/reply", body)
	if code != http.StatusOK || out["id"] != it.ID {
		t.Fatalf("completed-receipt retry did not return the successful item response: code=%d out=%+v", code, out)
	}
	if calls := c.calls(); len(calls) != 1 {
		t.Fatalf("legacy retry after a lost success response made %d physical sends, want the one completed send: %+v", len(calls), calls)
	}
}

func TestInboxReplyPolicyRejectedOnDemandDraftCannotBecomeDirectReply(t *testing.T) {
	const generated = "Synthetic on-demand draft rejected on another device"
	newRejectedDraft := func(t *testing.T) (*inboxHub, *extensionLark, *InboxItem) {
		t.Helper()
		h, lark, it := handledHub(t)
		replyPolicyReview8OrdinaryItem(h, it)
		replyPolicyOrdinaryDraftModel(t, h, generated)
		code, out := inboxCall(t, h.routes(), http.MethodPost, "/api/v1/inbox/"+it.ID+"/draft", `{}`)
		if code != http.StatusOK || out["text"] != generated {
			t.Fatalf("on-demand draft failed: code=%d out=%+v", code, out)
		}
		code, out = suggestionCall(t, h, it.ID, `{"version":1,"action":"reject"}`)
		if code != http.StatusOK {
			t.Fatalf("rejecting the mirror failed: code=%d out=%+v", code, out)
		}
		h.inbox.mu.Lock()
		{
			replyPolicyTestSetCategory(it, "approval_permission")
			replyPolicyTestSetRoute(it, "owner_only")
		}
		{
			replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
			replyPolicyTestSetPolicyRevision(it, "test-v1")
		}
		{
			replyPolicyTestSetReviewed(it, false)
		}
		h.inbox.mu.Unlock()
		return h, lark, it
	}

	t.Run("rejected generated text", func(t *testing.T) {
		h, lark, it := newRejectedDraft(t)
		body := fmt.Sprintf(`{"text":%q}`, generated)
		inboxCall(t, h.routes(), http.MethodPost, "/api/v1/inbox/"+it.ID+"/reply", body)
		if len(lark.got) != 0 {
			t.Fatalf("rejected on-demand text became a direct owner reply after the item was restricted: %+v", lark.got)
		}
	})

	t.Run("different direct owner text", func(t *testing.T) {
		h, lark, it := newRejectedDraft(t)
		const ownerText = "A different reply written directly by the owner"
		body := fmt.Sprintf(`{"text":%q}`, ownerText)
		code, out := inboxCall(t, h.routes(), http.MethodPost, "/api/v1/inbox/"+it.ID+"/reply", body)
		if code != http.StatusOK || len(lark.got) != 1 || lark.got[0].Text != ownerText {
			t.Fatalf("genuine direct owner-authored reply was gated with the rejected draft: code=%d out=%+v sends=%+v", code, out, lark.got)
		}
	})
}

func TestInboxReplyPolicyLegacyChangesOnlyMirrorConfirmationPreservesEmptyText(t *testing.T) {
	h, lark, it := handledHub(t)
	h.inbox.mu.Lock()
	it.Lark = nil
	it.Kind = KindDoc
	it.Doc = &InboxDoc{NoticeID: "synthetic-document"}
	h.inbox.mu.Unlock()
	replyPolicyReview8OrdinaryItem(h, it)
	const generated = "Synthetic AI comment that a changes-only confirmation must not post"
	if g := finalReviewInstallMirror(h, it, generated); g == nil {
		t.Fatal("test setup did not install the document mirror")
	}
	h.in.setModel(fakeModel(t, `{"category":"ordinary_coordination"}`))
	body := `{"text":"","changes":[{"ref":"synthetic-row","field":"status","value":"done"}]}`
	code, out := inboxCall(t, h.routes(), http.MethodPost, "/api/v1/inbox/"+it.ID+"/reply", body)
	if code != http.StatusOK {
		t.Fatalf("changes-only mirror confirmation failed: code=%d out=%+v", code, out)
	}
	if len(lark.got) != 1 || lark.got[0].Text != "" || len(lark.got[0].Changes) != 1 {
		t.Fatalf("changes-only confirmation did not preserve explicit empty text: %+v", lark.got)
	}
	if got := lark.got[0].Changes[0]; got.Ref != "synthetic-row" || got.Field != "status" || got.Value != "done" {
		t.Fatalf("changes-only confirmation changed the selected move: %+v", got)
	}
}

func TestInboxReplyPolicyFailedPublicationUnmarkedRowCannotSendAfterReload(t *testing.T) {
	const generated = "Synthetic draft persisted before its failure tombstone"
	newFailedPublication := func(t *testing.T) (*inboxHub, *recoveryReplier, *InboxItem) {
		t.Helper()
		c := &recoveryReplier{}
		h, _, it := handledHub(t)
		h.in.mu.Lock()
		h.in.conns[0] = c
		h.in.mu.Unlock()
		replyPolicyReview8OrdinaryItem(h, it)
		replyPolicyOrdinaryDraftModel(t, h, generated)
		st := &replyPolicyDraftOneShotFailStore{inboxMemStore: h.st, fail: true}
		st.before = func() {
			h.inbox.mu.Lock()
			h.inbox.touched(it.ID)
			h.inbox.mu.Unlock()
		}
		h.setStore(func() Store { return st })
		return h, c, it
	}

	t.Run("restart before tombstone", func(t *testing.T) {
		h, c, it := newFailedPublication(t)
		var restarted *Service
		h.inbox.onDemandDraftSaveHook = func() {
			h.inbox.onDemandDraftSaveHook = nil
			if _, err := suggestTool(t, h, `{"recap":"Metadata persisted in the publication gap"}`); err != nil {
				t.Errorf("persisting the intermediate row failed: %v", err)
				return
			}
			restarted = New(Options{Hub: h.f, Store: func() Store { return h.st }, Logf: testLogf(t)})
			if err := restarted.inboxReady(t.Context()); err != nil {
				t.Errorf("reloading the intermediate row failed: %v", err)
				restarted = nil
				return
			}
			restarted.inbox.mu.Lock()
			loaded := restarted.inbox.items[it.ID]
			g := currentSuggestion(loaded)
			restarted.inbox.mu.Unlock()
			if g == nil || g.Text != generated {
				t.Errorf("test setup did not reload the intermediate mirror: %+v", g)
				restarted = nil
			}
		}
		if text, err := h.draftReply(t.Context(), it.ID, ""); err == nil || text != "" {
			t.Fatalf("failed publication returned text: text=%q err=%v", text, err)
		}
		if restarted == nil {
			t.Fatal("test setup did not reconstruct from the intermediate persisted row")
		}
		_, sendErr := restarted.replyInboxChecked(t.Context(), it.ID, generated, recoveryFreshID, false, nil, nil)
		if sendErr == nil {
			t.Error("intermediate persisted mirror authorized a legacy reply after reload")
		}
		if calls := c.calls(); len(calls) != 0 {
			t.Fatalf("intermediate persisted mirror caused a physical send after reload: %+v", calls)
		}
	})

	t.Run("tombstone write fails", func(t *testing.T) {
		h, c, it := newFailedPublication(t)
		h.inbox.onDemandDraftSaveHook = func() {
			h.inbox.onDemandDraftSaveHook = nil
			h.setStore(func() Store { return h.st })
			if _, err := suggestTool(t, h, `{"recap":"Metadata persisted before the failing tombstone"}`); err != nil {
				t.Errorf("persisting the intermediate row failed: %v", err)
			}
			h.setStore(func() Store {
				return &replyPolicyDraftFailStore{inboxMemStore: h.st, before: func() {}}
			})
		}
		if text, err := h.draftReply(t.Context(), it.ID, ""); err == nil || text != "" {
			t.Fatalf("failed publication returned text: text=%q err=%v", text, err)
		}
		h.setStore(func() Store { return h.st })
		restarted := New(Options{Hub: h.f, Store: func() Store { return h.st }, Logf: testLogf(t)})
		if err := restarted.inboxReady(t.Context()); err != nil {
			t.Fatal(err)
		}
		restarted.inbox.mu.Lock()
		loaded := restarted.inbox.items[it.ID]
		g := currentSuggestion(loaded)
		restarted.inbox.mu.Unlock()
		if g == nil || g.Text != generated {
			t.Fatalf("test setup did not retain the row from before the failed tombstone: %+v", g)
		}
		_, sendErr := restarted.replyInboxChecked(t.Context(), it.ID, generated, recoveryFreshID, false, nil, nil)
		if sendErr == nil {
			t.Error("mirror whose tombstone write failed authorized a legacy reply after reload")
		}
		if calls := c.calls(); len(calls) != 0 {
			t.Fatalf("failed tombstone write left a physically sendable mirror after reload: %+v", calls)
		}
	})
}

func TestInboxReplyPolicyWorkbenchTransientDraftDoesNotBorrowLiveMirrorVersion(t *testing.T) {
	needsLarkLayer(t) // the integrated Workbench renderer and actions are the lark module's
	c := &recoveryReplier{}
	h, _, it := handledHub(t)
	h.in.mu.Lock()
	h.in.conns[0] = c
	h.in.mu.Unlock()
	h.setConfig(func(cfg *assistantwire.InboxConfig) { cfg.ReplyPolicy = &assistantwire.InboxReplyPolicyConfig{} })
	updated := replyPolicyEnableWorkbench(h)
	posts := replyPolicyReview8DraftPosts(h)
	const generated = "Transient draft A whose card must remain unversioned"
	const replacement = "Authorized mirror B installed before the transient card renders"
	// The old path sends A as an edit of B. Both the outer edit veto and the attempt-side veto need a classification.
	replyPolicyDraftSequenceModel(t, h, nil, generated,
		`{"category":"ordinary_coordination"}`, `{"category":"ordinary_coordination"}`)
	h.inbox.workbenchDraftHook = func() {
		h.inbox.workbenchDraftHook = nil
		replyPolicyReview8OrdinaryItem(h, it)
		finalReviewInstallMirror(h, it, replacement)
	}
	replyPolicyReview8WorkbenchAction(h, map[string]any{"a": "ib", "op": "draft", "id": it.ID}, nil, "synthetic-review9-transient-source")
	var raw string
	select {
	case raw = <-posts:
	case <-time.After(3 * time.Second):
		t.Fatal("Workbench did not render the transient draft")
	}
	value := replyPolicyWorkbenchActionValue(t, raw, "send")
	if version := replyPolicyReview8Version(value["version"]); version != 0 {
		t.Errorf("transient draft borrowed live mirror version %d, want explicit zero", version)
	}
	replyPolicyReview8WorkbenchAction(h, value, map[string]any{"text": generated}, "synthetic-review9-transient-card")
	select {
	case <-updated:
	case <-time.After(3 * time.Second):
		t.Fatal("transient Workbench send did not finish")
	}
	if calls := c.calls(); len(calls) != 0 {
		t.Fatalf("transient draft was sent as an edit of the live mirror it borrowed: %+v", calls)
	}
}

func TestInboxReplyPolicyWorkbenchTransientSendRechecksPolicyAfterMutexWait(t *testing.T) {
	needsLarkLayer(t) // the integrated Workbench renderer and actions are the lark module's
	for _, tc := range []struct {
		name     string
		activate bool
	}{
		{name: "policy activated", activate: true},
		{name: "policy stays disabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, lark, it := handledHub(t)
			h.setConfig(func(cfg *assistantwire.InboxConfig) { cfg.ReplyPolicy = &assistantwire.InboxReplyPolicyConfig{} })
			h.inbox.mu.Lock()
			{
				replyPolicyTestSetCategory(it, "approval_permission")
				replyPolicyTestSetRoute(it, "owner_only")
			}
			{
				replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
				replyPolicyTestSetPolicyRevision(it, "test-v1")
			}
			{
				replyPolicyTestSetReviewed(it, false)
			}
			h.inbox.mu.Unlock()
			if mirroredLegacyReply(it) != nil {
				t.Fatal("test setup unexpectedly has a mirror")
			}
			entered := make(chan struct{})
			once := &inboxReply{beforeLock: func() { close(entered) }}
			once.mu.Lock()
			unlocked := false
			defer func() {
				if !unlocked {
					once.mu.Unlock()
				}
			}()
			h.inbox.replies.Store(it.ID, once)
			result := make(chan error, 1)
			go func() {
				_, err := h.replyInboxFromVersion(t.Context(), it.ID, "Synthetic transient AI draft", "feishu-workbench-review9-policy-wait", true, 0)
				result <- err
			}()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("transient Workbench send did not reach the per-item send lock")
			}
			if tc.activate {
				h.setConfig(func(cfg *assistantwire.InboxConfig) { cfg.ReplyPolicy = replyPolicyTestConfig() })
			}
			once.mu.Unlock()
			unlocked = true
			var err error
			select {
			case err = <-result:
			case <-time.After(3 * time.Second):
				t.Fatal("transient Workbench send did not finish")
			}
			if tc.activate {
				if err == nil {
					t.Error("policy activation during the send-mutex wait went unnoticed")
				}
				if len(lark.got) != 0 {
					t.Fatalf("transient AI draft dispatched after policy activation during the wait: %+v", lark.got)
				}
				return
			}
			if err != nil {
				t.Fatalf("policy-disabled transient Workbench send failed: %v", err)
			}
			if len(lark.got) != 1 {
				t.Fatalf("policy-disabled transient Workbench send changed legacy behavior: %+v", lark.got)
			}
		})
	}
}

func TestInboxReplyPolicyUnversionedWorkbenchCardCannotRetryFailedAttempt(t *testing.T) {
	needsLarkLayer(t) // the integrated Workbench renderer and actions are the lark module's
	const text = "Same text on an old card and its replacement"
	newAttempt := func(t *testing.T) (*inboxHub, *recoveryReplier, *InboxItem, map[string]any, map[string]any, chan struct{}) {
		t.Helper()
		c := &recoveryReplier{err: &rpc.Error{Code: assistantwire.CodeInboxSendRateLimited, Data: json.RawMessage(`{"retry_after":30}`)}}
		h, _, it := handledHub(t)
		h.in.mu.Lock()
		h.in.conns[0] = c
		h.in.mu.Unlock()
		replyPolicyReview8OrdinaryItem(h, it)
		updated := replyPolicyEnableWorkbench(h)
		finalReviewInstallMirror(h, it, text)
		oldValue := replyPolicyWorkbenchActionValue(t, h.draftCard(t.Context(), *it, text, nil, "").JSON(), "send")
		delete(oldValue, "version")
		finalReviewInstallMirror(h, it, text)
		h.in.setModel(fakeModel(t, `{"category":"ordinary_coordination"}`))
		currentValue := replyPolicyWorkbenchActionValue(t, h.draftCard(t.Context(), *it, text, nil, "").JSON(), "send")
		if version := replyPolicyReview8Version(currentValue["version"]); version != 2 {
			t.Fatalf("test setup did not render replacement version 2: version=%d", version)
		}
		return h, c, it, oldValue, currentValue, updated
	}
	failFirstAttempt := func(t *testing.T, h *inboxHub, c *recoveryReplier, it *InboxItem, value map[string]any, updated chan struct{}) InboxSuggestionAttempt {
		t.Helper()
		replyPolicyReview8WorkbenchAction(h, value, map[string]any{"text": text}, "synthetic-review9-versioned-first-attempt")
		select {
		case <-updated:
		case <-time.After(3 * time.Second):
			t.Fatal("versioned Workbench attempt did not finish")
		}
		a := recoveryAttempt(t, h, it, 2)
		if a.Status != suggestionFailed || !a.Retryable || a.Code != assistantwire.CodeInboxSendRateLimited {
			t.Fatalf("test setup did not create a known-none retryable attempt: %+v", a)
		}
		if calls := c.calls(); len(calls) != 1 {
			t.Fatalf("test setup made %d physical sends, want one: %+v", len(calls), calls)
		}
		c.answer(nil)
		return a
	}

	t.Run("old unversioned card", func(t *testing.T) {
		h, c, it, oldValue, currentValue, updated := newAttempt(t)
		failFirstAttempt(t, h, c, it, currentValue, updated)
		replyPolicyReview8WorkbenchAction(h, oldValue, map[string]any{"text": text}, "synthetic-review9-old-unversioned-card")
		select {
		case <-updated:
		case <-time.After(3 * time.Second):
			t.Fatal("old unversioned Workbench submission did not finish")
		}
		if calls := c.calls(); len(calls) != 1 {
			t.Fatalf("old unversioned card retried replacement version 2: %+v", calls)
		}
		if a := recoveryAttempt(t, h, it, 2); a.Status != suggestionFailed || !a.Retryable {
			t.Errorf("unversioned receipt lookup changed the durable attempt: %+v", a)
		}
	})

	t.Run("matching version may retry", func(t *testing.T) {
		h, c, it, _, currentValue, updated := newAttempt(t)
		first := failFirstAttempt(t, h, c, it, currentValue, updated)
		replyPolicyReview8WorkbenchAction(h, currentValue, map[string]any{"text": text}, "synthetic-review9-matching-version-retry")
		select {
		case <-updated:
		case <-time.After(3 * time.Second):
			t.Fatal("matching-version Workbench retry did not finish")
		}
		calls := c.calls()
		if len(calls) != 2 || calls[0].ClientID != first.ClientID || calls[1].ClientID != first.ClientID {
			t.Fatalf("matching-version retry did not reuse its frozen physical identity: %+v", calls)
		}
		if a := recoveryAttempt(t, h, it, 2); a.Status != suggestionSent {
			t.Errorf("matching-version retry did not complete: %+v", a)
		}
	})
}
