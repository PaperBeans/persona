package assistant

import (
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/prompts"
)

func TestInboxReplyPolicyCheckedMailCompletedReceiptHonorsExpectationsLegacySchema(t *testing.T) {
	newSentMail := func(t *testing.T) (*inboxHub, *fakeMail, *InboxItem, inboxMailTarget, string) {
		t.Helper()
		h := agentHub(t)
		mail := &fakeMail{}
		h.in.mail = mail
		cfg := legacyReplyPolicyTestConfig()
		cfg.Sources = []string{"mail"}
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
		now := time.Now()
		it := &InboxItem{ID: "mail:synthetic:completed-checked", Source: "mail", Kind: KindMail, Last: "1", Context: "Synthetic checked retry",
			StartedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute), Tier: TierAction, TriagedAt: "1",
			Mail:          &InboxMail{AccountID: "synthetic-account", Mailbox: "INBOX", UIDs: []uint32{9}, Thread: "Synthetic thread"},
			ReplyCategory: "ordinary_coordination", ReplyMode: "owner_draft", ReplyPolicyRev: "test-v1"}
		it.ReplyClassifiedOf = replyPolicyContentVersion(it)
		h.inbox.items[it.ID] = it
		const text = "Synthetic checked mail completed receipt"
		finalReviewInstallMirror(h, it, text)
		target := mailTarget(*it)
		if target == nil {
			t.Fatal("synthetic mail item has no checked target")
		}
		if _, err := h.replyInboxChecked(t.Context(), it.ID, text, "", false, &target.UID, &target.Version); err != nil {
			t.Fatalf("first checked mail confirmation failed: %v", err)
		}
		if got := len(mail.sent); got != 1 {
			t.Fatalf("first checked mail confirmation made %d physical sends, want 1", got)
		}
		return h, mail, it, *target, text
	}

	t.Run("matching expectations reuse receipt", func(t *testing.T) {
		h, mail, it, target, text := newSentMail(t)
		if _, err := h.replyInboxChecked(t.Context(), it.ID, text, "", false, &target.UID, &target.Version); err != nil {
			t.Fatalf("matching checked retry did not reuse its completed receipt: %v", err)
		}
		if got := len(mail.sent); got != 1 {
			t.Fatalf("matching checked retry made %d physical sends, want the original one: %+v", got, mail.sent)
		}
	})

	t.Run("stale expectation stays denied", func(t *testing.T) {
		h, mail, it, target, text := newSentMail(t)
		staleUID := target.UID + 1
		_, err := h.replyInboxChecked(t.Context(), it.ID, text, "", false, &staleUID, nil)
		if !errors.Is(err, errMailReplyTargetChanged) {
			t.Fatalf("stale checked retry error = %v, want target changed", err)
		}
		if got := len(mail.sent); got != 1 {
			t.Fatalf("stale checked retry made %d physical sends, want the original one", got)
		}
	})
}

func TestInboxReplyPolicyEveryRejectedOnDemandDraftStaysRejectedLegacySchema(t *testing.T) {
	const first = "Synthetic rejected on-demand draft A"
	const second = "Synthetic rejected on-demand draft B"
	newRejectedPair := func(t *testing.T) (*inboxHub, *extensionLark, *InboxItem) {
		t.Helper()
		h, lark, it := handledHub(t)
		replyPolicyReview8OrdinaryItemLegacy(h, it)
		// Rejection normally wakes the processing session, whose asynchronous turn update can legitimately make the next
		// draft stale. Disable that unrelated delivery so this regression exercises two completed draft/reject cycles.
		h.setConfig(func(c *assistantwire.InboxConfig) { c.Agent.Enabled = false })
		for version, generated := range []string{first, second} {
			h.in.model = fakeModel(t, generated)
			saveCalls := 0
			h.inbox.onDemandDraftSaveHook = func() { saveCalls++ }
			code, out := inboxCall(t, h.routes(), http.MethodPost, "/api/v1/inbox/"+it.ID+"/draft", `{}`)
			if code != http.StatusOK || out["text"] != generated {
				t.Fatalf("on-demand draft %d failed: code=%d out=%+v save_calls=%d suggestion=%+v agent=%+v attempts=%+v", version+1, code, out, saveCalls, currentSuggestion(it), it.Agent, it.SuggestionAttempts)
			}
			h.inbox.onDemandDraftSaveHook = nil
			body := fmt.Sprintf(`{"version":%d,"action":"reject"}`, version+1)
			code, out = suggestionCall(t, h, it.ID, body)
			if code != http.StatusOK {
				t.Fatalf("rejecting on-demand draft %d failed: code=%d out=%+v", version+1, code, out)
			}
		}
		h.inbox.mu.Lock()
		it.ReplyCategory, it.ReplyMode = "approval_permission", "owner_only"
		it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
		it.ReplyReviewed = false
		h.inbox.mu.Unlock()
		return h, lark, it
	}

	t.Run("first rejected text", func(t *testing.T) {
		h, lark, it := newRejectedPair(t)
		code, out := inboxCall(t, h.routes(), http.MethodPost, "/api/v1/inbox/"+it.ID+"/reply", fmt.Sprintf(`{"text":%q}`, first))
		if code == http.StatusOK || len(lark.got) != 0 {
			t.Fatalf("first rejected draft became sendable after rejecting its replacement: code=%d out=%+v sends=%+v", code, out, lark.got)
		}
	})

	t.Run("different direct owner text", func(t *testing.T) {
		h, lark, it := newRejectedPair(t)
		const ownerText = "Different direct owner text after two rejected drafts"
		code, out := inboxCall(t, h.routes(), http.MethodPost, "/api/v1/inbox/"+it.ID+"/reply", fmt.Sprintf(`{"text":%q}`, ownerText))
		if code != http.StatusOK || len(lark.got) != 1 || lark.got[0].Text != ownerText {
			t.Fatalf("different owner-authored text was gated with rejected provenance: code=%d out=%+v sends=%+v", code, out, lark.got)
		}
	})
}

func TestInboxReplyPolicyPublicationCommitCannotReturnReplacedDraftLegacySchema(t *testing.T) {
	h, _, it := handledHub(t)
	replyPolicyReview8OrdinaryItemLegacy(h, it)
	const generated = "Synthetic on-demand draft replaced during its commit"
	h.in.model = fakeModel(t, generated)
	rejected := make(chan error, 1)
	h.inbox.onDemandDraftSaveHook = func() {
		h.inbox.onDemandDraftSaveHook = nil
		// Hold the save queue after the pending row is durable. The publication clears its marker and then waits here;
		// another device can reject the live mirror before the commit snapshots the row.
		h.inbox.saving.Lock()
		go func() {
			deadline := time.Now().Add(3 * time.Second)
			for {
				h.inbox.mu.Lock()
				g := currentSuggestion(it)
				pending := g != nil && g.ReplyPublicationPending
				h.inbox.mu.Unlock()
				if !pending {
					break
				}
				if time.Now().After(deadline) {
					h.inbox.saving.Unlock()
					rejected <- errors.New("publication never cleared its pending marker")
					return
				}
				runtime.Gosched()
			}
			go func() {
				_, err := h.actOnSuggestion(t.Context(), it.ID, suggestionReq{Version: 1, Action: suggestReject}, true)
				rejected <- err
			}()
			for {
				h.inbox.mu.Lock()
				replaced := currentSuggestion(it) == nil
				h.inbox.mu.Unlock()
				if replaced {
					h.inbox.saving.Unlock()
					return
				}
				if time.Now().After(deadline) {
					h.inbox.saving.Unlock()
					return
				}
				runtime.Gosched()
			}
		}()
	}
	text, err := h.draftReply(t.Context(), it.ID, "")
	if !errors.Is(err, errSuggestionStale) || text != "" {
		t.Fatalf("publication commit returned a draft replaced before its second write: text=%q err=%v", text, err)
	}
	select {
	case err := <-rejected:
		if err != nil {
			t.Fatalf("concurrent rejection failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("concurrent rejection did not finish")
	}
}

func TestInboxReplyPolicyUnexpectedClassifierKeyFailsClosedLegacySchema(t *testing.T) {
	h := agentHub(t)
	cfg := legacyReplyPolicyTestConfig()
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
	it := actionItem("lark:review10-unexpected-classifier-key", time.Now())
	it.Source = "lark"
	it.Agent = &InboxAgent{State: AgentQueued}
	h.inbox.items[it.ID] = it
	h.in.model = fakeModel(t, `{"category":"ordinary_coordination","review":{"category":"approval_permission"}}`)
	h.classifyInboxReplyPolicies(t.Context(), cfg)
	before := len(conversationStarts(h.f))
	h.startInboxAgents(t.Context(), InboxAgentConfig{Enabled: true})
	starts := len(conversationStarts(h.f)) - before
	if it.ReplyCategory != "unknown" || it.ReplyMode != ReplyRouteOwnerOnly || !replyPolicyClassificationCurrent(it, cfg) || starts != 0 {
		t.Fatalf("classifier response with an unexpected key authorized handling: category=%q mode=%q classified_of=%q revision=%q starts=%d",
			it.ReplyCategory, it.ReplyMode, it.ReplyClassifiedOf, it.ReplyPolicyRev, starts)
	}
}

func TestInboxReplyPolicyFollowMergedLaterRoundIncludesOlderMemberEvidenceLegacySchema(t *testing.T) {
	h := agentHub(t)
	cfg := legacyReplyPolicyTestConfig()
	cfg.Sources = []string{"synthetic"}
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
	const memberMarker = "Synthetic late member update older than the survivor display event"
	replyPolicyReview9PolicyMarkerModel(t, h, memberMarker)
	base := time.Now().Add(-time.Hour)
	survivor := &InboxItem{ID: "synthetic:review10-survivor-later", Source: "synthetic", AccountName: "Synthetic", Kind: KindDM,
		Last: "survivor-1", Context: "Survivor already advanced in the preceding round", Count: 1,
		UpdatedAt: base.Add(2 * time.Minute), StartedAt: base, Tier: TierAction, TriagedAt: "survivor-1",
		Connector: &InboxConnector{Thread: "survivor-later"}, MergedFrom: []InboxMergeRef{{ID: "synthetic:review10-member-later", Source: "synthetic"}}}
	member := &InboxItem{ID: "synthetic:review10-member-later", Source: "synthetic", AccountName: "Synthetic", Kind: KindDM,
		Last: "member-0", Context: "Original member context", Count: 1, UpdatedAt: base, StartedAt: base,
		MergedInto: survivor.ID, Connector: &InboxConnector{Thread: "member-later"}}

	h.inbox.mu.Lock()
	h.inbox.items[survivor.ID], h.inbox.items[member.ID] = survivor, member
	changed := h.inbox.applyConnector("synthetic", "Synthetic", []connector.InboxThing{
		{Thread: "member-later", Kind: KindDM, Last: "member-1", At: base.Add(time.Minute), Context: memberMarker},
	}, time.Now())
	memberChanged, survivorChanged := false, false
	for _, it := range changed {
		memberChanged = memberChanged || it == member
		survivorChanged = survivorChanged || it == survivor
	}
	if !memberChanged || survivorChanged {
		h.inbox.mu.Unlock()
		t.Fatalf("test setup did not advance only the member: member=%t survivor=%t", memberChanged, survivorChanged)
	}
	h.inbox.followMerged(changed, time.Now())
	h.inbox.mu.Unlock()
	h.classifyInboxReplyPolicies(t.Context(), cfg)
	if !strings.Contains(survivor.Context, memberMarker) || survivor.ReplyCategory != "approval_permission" || survivor.ReplyMode != ReplyRouteOwnerOnly {
		t.Fatalf("later-round member advancement was skipped because its event was older than the survivor: context=%q category=%q mode=%q",
			survivor.Context, survivor.ReplyCategory, survivor.ReplyMode)
	}
}

func TestInboxReplyPolicyFollowMergedOlderMemberPreservesSurvivorDisplay(t *testing.T) {
	h := agentHub(t)
	base := time.Now().Add(-time.Hour)
	const memberMarker = "Synthetic older member evidence retained without replacing display fields"
	survivor := &InboxItem{ID: "synthetic:review10-survivor-display", Source: "synthetic", AccountName: "Synthetic", Kind: KindDM,
		Last: "survivor-0", Context: "Original ordinary survivor context", Count: 1, UpdatedAt: base, StartedAt: base,
		Tier: TierAction, TriagedAt: "survivor-0", Connector: &InboxConnector{Thread: "survivor-display"},
		MergedFrom: []InboxMergeRef{{ID: "synthetic:review10-member-display", Source: "synthetic"}}}
	member := &InboxItem{ID: "synthetic:review10-member-display", Source: "synthetic", AccountName: "Synthetic", Kind: KindDM,
		Last: "member-0", Context: "Original member context", Count: 1, UpdatedAt: base, StartedAt: base,
		MergedInto: survivor.ID, Connector: &InboxConnector{Thread: "member-display"}}
	newestDisplay := base.Add(2 * time.Minute)

	h.inbox.mu.Lock()
	h.inbox.items[survivor.ID], h.inbox.items[member.ID] = survivor, member
	changed := h.inbox.applyConnector("synthetic", "Synthetic", []connector.InboxThing{
		{Thread: "survivor-display", Kind: KindDM, Last: "survivor-1", At: newestDisplay, Context: "Newest survivor display event", Preview: "newest survivor preview", Sender: "survivor sender"},
		{Thread: "member-display", Kind: KindDM, Last: "member-1", At: base.Add(time.Minute), Context: memberMarker, Preview: "older member preview", Sender: "older member sender"},
	}, time.Now())
	h.inbox.followMerged(changed, time.Now())
	h.inbox.mu.Unlock()
	if !strings.Contains(survivor.Context, memberMarker) {
		t.Fatalf("test setup lost the older member evidence: %q", survivor.Context)
	}
	if !survivor.UpdatedAt.Equal(newestDisplay) || survivor.Preview != "newest survivor preview" || survivor.Sender != "survivor sender" {
		t.Fatalf("older member evidence replaced the survivor display event: updated_at=%s preview=%q sender=%q",
			survivor.UpdatedAt, survivor.Preview, survivor.Sender)
	}
}

func review10BlockingKBStartLegacy(t *testing.T, mutate func(*inboxHub, *InboxItem)) {
	t.Helper()
	h, _ := newInboxHubKB(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
	it := actionItem("lark:review10-kb-authorization", time.Now())
	it.Source = "lark"
	it.Agent = &InboxAgent{State: AgentQueued, ProcessingTurn: "synthetic-old-processing-turn"}
	it.ReplyCategory, it.ReplyMode = "ordinary_coordination", ReplyRouteOwnerDraft
	it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
	h.inbox.items[it.ID] = it
	blocking := &inboxGroupStateWakeBlockingHelloHub{fakeHub: h.f, entered: make(chan struct{}), release: make(chan struct{})}
	h.Service.hub = blocking
	var configReads atomic.Int32
	h.in.configHook = func() {
		if configReads.Add(1) == 2 {
			// The next blocking preparation is the KB lookup after the prompt and its authorization tuple were built.
			blocking.armed.Store(true)
		}
	}
	before := len(conversationStarts(h.f))
	done := make(chan struct{})
	go func() {
		h.startInboxAgents(t.Context(), InboxAgentConfig{Enabled: true})
		close(done)
	}()
	select {
	case <-blocking.entered:
	case <-time.After(5 * time.Second):
		close(blocking.release)
		t.Fatal("queue start did not block in KB preparation")
	}
	mutate(h, it)
	close(blocking.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("queue start did not finish after KB preparation")
	}
	if got := len(conversationStarts(h.f)) - before; got != 0 {
		t.Fatalf("queue started %d conversations with a prompt and authorization captured at different item states", got)
	}
	h.inbox.mu.Lock()
	session := it.Agent.SessionID
	h.inbox.mu.Unlock()
	if session != "" {
		t.Fatalf("stale prompt authorization assigned session %q", session)
	}
}

func TestInboxReplyPolicyQueueContentAuthorizationCapturedBeforeKBContextLegacySchema(t *testing.T) {
	review10BlockingKBStartLegacy(t, func(h *inboxHub, it *InboxItem) {
		h.inbox.mu.Lock()
		defer h.inbox.mu.Unlock()
		it.Last = "synthetic-new-content-during-kb"
		it.Context = "Synthetic replacement content arriving while the old prompt waits for KB context"
		// Keep the replacement authorized so only the prompt/version pairing can reject this start.
		it.ReplyClassifiedOf = replyPolicyContentVersion(it)
	})
}

func TestInboxReplyPolicyQueueTurnAuthorizationCapturedBeforeKBContext(t *testing.T) {
	review10BlockingKBStartLegacy(t, func(h *inboxHub, it *InboxItem) {
		h.inbox.mu.Lock()
		it.Agent.ProcessingTurn = "synthetic-replacement-processing-turn"
		h.inbox.mu.Unlock()
	})
}

func TestInboxReplyPolicyDeliverNotesRechecksPolicyAfterHelloLegacySchema(t *testing.T) {
	h, _, it := handledHub(t)
	replyPolicyReview8OrdinaryItemLegacy(h, it)
	const pendingWork = "Synthetic ordinary work waiting across policy activation"
	h.inbox.mu.Lock()
	it.Context = pendingWork
	it.ReplyClassifiedOf = replyPolicyContentVersion(it)
	it.Agent.Pending = nil
	it.Agent.queueNote(inboxNote{Name: prompts.NoteNewMessages, Context: pendingWork})
	it.Agent.Wake = true
	oldTurn := it.Agent.ProcessingTurn
	h.inbox.mu.Unlock()
	blocking := &inboxGroupStateWakeBlockingHelloHub{fakeHub: h.f, entered: make(chan struct{}), release: make(chan struct{})}
	blocking.armed.Store(true)
	h.Service.hub = blocking
	before := len(h.f.sentToAgents())
	done := make(chan struct{})
	go func() {
		h.deliverNotes(t.Context(), it)
		close(done)
	}()
	select {
	case <-blocking.entered:
	case <-time.After(5 * time.Second):
		close(blocking.release)
		t.Fatal("deliverNotes did not reach its blocking Hello")
	}
	h.setConfig(func(c *assistantwire.InboxConfig) {
		cfg := legacyReplyPolicyTestConfig()
		cfg.Routes["ordinary_coordination"] = ReplyRouteOwnerOnly
		cfg.Revision = "test-v2"
		c.ReplyPolicy = cfg
	})
	close(blocking.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("deliverNotes did not finish after Hello")
	}
	after := h.f.sentToAgents()
	h.inbox.mu.Lock()
	pending, wake, turn := len(it.Agent.Pending), it.Agent.Wake, it.Agent.ProcessingTurn
	h.inbox.mu.Unlock()
	if len(after) != before || strings.Contains(strings.Join(after[before:], "\n"), pendingWork) || pending != 1 || !wake || turn != oldTurn {
		t.Fatalf("work was delivered with policy captured before Hello: new_messages=%q pending=%d wake=%t processing_turn %q -> %q",
			strings.Join(after[before:], "\n"), pending, wake, oldTurn, turn)
	}
}

func TestInboxReplyPolicyInstructedAgentReplacementRequiresTextReviewLegacySchema(t *testing.T) {
	h, _, it := handledHub(t)
	replyPolicyReview8OrdinaryItemLegacy(h, it)
	original := it.Context
	if _, err := suggestTool(t, h, `{"kind":"agent","text":"Original ordinary Agent instruction"}`); err != nil {
		t.Fatal(err)
	}
	instruction := "Synthetic owner rewrite instruction for an Agent replacement"
	if _, err := h.actOnSuggestion(t.Context(), it.ID, suggestionReq{Version: 1, Action: suggestInstruct, Text: &instruction}, true); err != nil {
		t.Fatal(err)
	}
	toldConv(t, h, instruction)
	model, calls := captureModel(t, `{"category":"approval_permission"}`)
	h.in.model = model
	const restricted = "Restricted generated Agent replacement requiring text review"
	_, err := suggestTool(t, h, `{"kind":"agent","text":"`+restricted+`"}`)
	got := calls()
	if !errors.Is(err, errReplyPolicyDenied) || len(got) != 1 || currentSuggestion(it) != nil {
		t.Fatalf("instructed Agent replacement bypassed generated-text review: err=%v model_calls=%d suggestion=%+v", err, len(got), currentSuggestion(it))
	}
	for _, marker := range []string{restricted, instruction, original} {
		if !strings.Contains(got[0].User, marker) {
			t.Fatalf("instructed Agent replacement classifier input lacks %q: %+v", marker, got[0])
		}
	}
}

func TestInboxReplyPolicyCheckedMailCompletedReceiptHonorsExpectations(t *testing.T) {
	newSentMail := func(t *testing.T) (*inboxHub, *fakeMail, *InboxItem, inboxMailTarget, string) {
		t.Helper()
		h := agentHub(t)
		mail := &fakeMail{}
		h.in.mail = mail
		cfg := replyPolicyTestConfig()
		cfg.Sources = []string{"mail"}
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
		now := time.Now()
		it := &InboxItem{ID: "mail:synthetic:completed-checked", Source: "mail", Kind: KindMail, Last: "1", BodyOf: "1", Context: "Synthetic checked retry",
			StartedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute), Tier: TierAction, TriagedAt: "1",
			Mail: &InboxMail{AccountID: "synthetic-account", Mailbox: "INBOX", UIDs: []uint32{9}, Thread: "Synthetic thread"}, ReplyPolicy: &InboxReplyPolicySnapshot{Category: "ordinary_coordination", Route: "owner_draft", PolicyRevision: compileReplyPolicy(cfg).revision}}
		{
			replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
		}
		h.inbox.items[it.ID] = it
		const text = "Synthetic checked mail completed receipt"
		finalReviewInstallMirror(h, it, text)
		h.in.setModel(fakeModel(t, `{"category":"ordinary_coordination"}`))
		target := mailTarget(*it)
		if target == nil {
			t.Fatal("synthetic mail item has no checked target")
		}
		if _, err := h.replyInboxChecked(t.Context(), it.ID, text, "", false, &target.UID, &target.Version); err != nil {
			t.Fatalf("first checked mail confirmation failed: %v", err)
		}
		if got := len(mail.sent); got != 1 {
			t.Fatalf("first checked mail confirmation made %d physical sends, want 1", got)
		}
		return h, mail, it, *target, text
	}

	t.Run("matching expectations reuse receipt", func(t *testing.T) {
		h, mail, it, target, text := newSentMail(t)
		if _, err := h.replyInboxChecked(t.Context(), it.ID, text, "", false, &target.UID, &target.Version); err != nil {
			t.Fatalf("matching checked retry did not reuse its completed receipt: %v", err)
		}
		if got := len(mail.sent); got != 1 {
			t.Fatalf("matching checked retry made %d physical sends, want the original one: %+v", got, mail.sent)
		}
	})

	t.Run("stale expectation stays denied", func(t *testing.T) {
		h, mail, it, target, text := newSentMail(t)
		staleUID := target.UID + 1
		_, err := h.replyInboxChecked(t.Context(), it.ID, text, "", false, &staleUID, nil)
		if !errors.Is(err, errMailReplyTargetChanged) {
			t.Fatalf("stale checked retry error = %v, want target changed", err)
		}
		if got := len(mail.sent); got != 1 {
			t.Fatalf("stale checked retry made %d physical sends, want the original one", got)
		}
	})
}

func TestInboxReplyPolicyEveryRejectedOnDemandDraftStaysRejected(t *testing.T) {
	const first = "Synthetic rejected on-demand draft A"
	const second = "Synthetic rejected on-demand draft B"
	newRejectedPair := func(t *testing.T) (*inboxHub, *extensionLark, *InboxItem) {
		t.Helper()
		h, lark, it := handledHub(t)
		replyPolicyReview8OrdinaryItem(h, it)
		// Rejection normally wakes the processing session, whose asynchronous turn update can legitimately make the next
		// draft stale. Disable that unrelated delivery so this regression exercises two completed draft/reject cycles.
		h.setConfig(func(c *assistantwire.InboxConfig) { c.Agent.Enabled = false })
		for version, generated := range []string{first, second} {
			replyPolicyOrdinaryDraftModel(t, h, generated)
			saveCalls := 0
			h.inbox.onDemandDraftSaveHook = func() { saveCalls++ }
			code, out := inboxCall(t, h.routes(), http.MethodPost, "/api/v1/inbox/"+it.ID+"/draft", `{}`)
			if code != http.StatusOK || out["text"] != generated {
				t.Fatalf("on-demand draft %d failed: code=%d out=%+v save_calls=%d suggestion=%+v agent=%+v attempts=%+v", version+1, code, out, saveCalls, currentSuggestion(it), it.Agent, it.SuggestionAttempts)
			}
			h.inbox.onDemandDraftSaveHook = nil
			body := fmt.Sprintf(`{"version":%d,"action":"reject"}`, version+1)
			code, out = suggestionCall(t, h, it.ID, body)
			if code != http.StatusOK {
				t.Fatalf("rejecting on-demand draft %d failed: code=%d out=%+v", version+1, code, out)
			}
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

	t.Run("first rejected text", func(t *testing.T) {
		h, lark, it := newRejectedPair(t)
		code, out := inboxCall(t, h.routes(), http.MethodPost, "/api/v1/inbox/"+it.ID+"/reply", fmt.Sprintf(`{"text":%q}`, first))
		if code == http.StatusOK || len(lark.got) != 0 {
			t.Fatalf("first rejected draft became sendable after rejecting its replacement: code=%d out=%+v sends=%+v", code, out, lark.got)
		}
	})

	t.Run("second rejected text", func(t *testing.T) {
		h, lark, it := newRejectedPair(t)
		code, out := inboxCall(t, h.routes(), http.MethodPost, "/api/v1/inbox/"+it.ID+"/reply", fmt.Sprintf(`{"text":%q}`, second))
		if code == http.StatusOK || len(lark.got) != 0 {
			t.Fatalf("second rejected draft became sendable after its rejection: code=%d out=%+v sends=%+v", code, out, lark.got)
		}
	})

	t.Run("different direct owner text", func(t *testing.T) {
		h, lark, it := newRejectedPair(t)
		const ownerText = "Different direct owner text after two rejected drafts"
		code, out := inboxCall(t, h.routes(), http.MethodPost, "/api/v1/inbox/"+it.ID+"/reply", fmt.Sprintf(`{"text":%q}`, ownerText))
		if code != http.StatusOK || len(lark.got) != 1 || lark.got[0].Text != ownerText {
			t.Fatalf("different owner-authored text was gated with rejected provenance: code=%d out=%+v sends=%+v", code, out, lark.got)
		}
	})
}

func TestInboxReplyPolicyPublicationCommitCannotReturnReplacedDraft(t *testing.T) {
	h, lark, it := handledHub(t)
	replyPolicyReview8OrdinaryItem(h, it)
	const generated = "Synthetic on-demand draft replaced during its commit"
	replyPolicyOrdinaryDraftModel(t, h, generated)
	rejected := make(chan error, 1)
	h.inbox.onDemandDraftSaveHook = func() {
		h.inbox.onDemandDraftSaveHook = nil
		// Hold the save queue after the pending row is durable. The publication clears its marker and then waits here;
		// another device can reject the live mirror before the commit snapshots the row.
		h.inbox.saving.Lock()
		go func() {
			deadline := time.Now().Add(3 * time.Second)
			for {
				h.inbox.mu.Lock()
				g := currentSuggestion(it)
				pending := g != nil && g.ReplyPublicationPending
				h.inbox.mu.Unlock()
				if !pending {
					break
				}
				if time.Now().After(deadline) {
					h.inbox.saving.Unlock()
					rejected <- errors.New("publication never cleared its pending marker")
					return
				}
				runtime.Gosched()
			}
			go func() {
				_, err := h.actOnSuggestion(t.Context(), it.ID, suggestionReq{Version: 1, Action: suggestReject}, true)
				rejected <- err
			}()
			for {
				h.inbox.mu.Lock()
				replaced := currentSuggestion(it) == nil
				h.inbox.mu.Unlock()
				if replaced {
					h.inbox.saving.Unlock()
					return
				}
				if time.Now().After(deadline) {
					h.inbox.saving.Unlock()
					return
				}
				runtime.Gosched()
			}
		}()
	}
	text, err := h.draftReply(t.Context(), it.ID, "")
	if !errors.Is(err, errSuggestionStale) || text != "" {
		t.Fatalf("publication commit returned a draft replaced before its second write: text=%q err=%v", text, err)
	}
	select {
	case err := <-rejected:
		if err != nil {
			t.Fatalf("concurrent rejection failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("concurrent rejection did not finish")
	}

	restarted := New(Options{Hub: h.f, Store: func() Store { return h.st }, Logf: testLogf(t)})
	if err := restarted.inboxReady(t.Context()); err != nil {
		t.Fatalf("reloading the concurrently rejected publication failed: %v", err)
	}
	restarted.inbox.mu.Lock()
	loaded := restarted.inbox.items[it.ID]
	var current, legacy *InboxSuggestion
	agentDraft := ""
	pending := false
	if loaded != nil {
		current = currentSuggestion(loaded)
		legacy = mirroredLegacyReply(loaded)
		pending = loaded.Suggestion != nil && loaded.Suggestion.ReplyPublicationPending
		if loaded.Agent != nil {
			agentDraft = loaded.Agent.Draft
		}
	}
	restarted.inbox.mu.Unlock()
	if loaded == nil || current != nil || legacy != nil || agentDraft != "" || pending {
		t.Fatalf("concurrent rejection left a durable generated reply or publication marker: loaded=%+v current=%+v legacy=%+v agent_draft=%q pending=%t", loaded, current, legacy, agentDraft, pending)
	}
	if _, err := restarted.replyInboxChecked(t.Context(), it.ID, generated, recoveryFreshID, false, nil, nil); err == nil {
		t.Fatal("replaced generated text became sendable after reloading its rejection")
	}
	if len(lark.got) != 0 {
		t.Fatalf("replaced generated text reached the physical sender after reload: %+v", lark.got)
	}
}

func TestInboxReplyPolicyUnexpectedClassifierKeyFailsClosed(t *testing.T) {
	h := agentHub(t)
	cfg := replyPolicyTestConfig()
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
	it := actionItem("lark:review10-unexpected-classifier-key", time.Now())
	it.Source = "lark"
	it.Agent = &InboxAgent{State: AgentQueued}
	h.inbox.items[it.ID] = it
	h.in.model = fakeModel(t, `{"category":"ordinary_coordination","review":{"category":"approval_permission"}}`)
	h.classifyInboxReplyPolicies(t.Context(), cfg)
	before := len(conversationStarts(h.f))
	h.startInboxAgents(t.Context(), InboxAgentConfig{Enabled: true})
	starts := len(conversationStarts(h.f)) - before
	if replyPolicyTestCategory(it) != "unknown" || replyPolicyTestRoute(it) != ReplyRouteOwnerOnly || !replyPolicyClassificationCurrent(it, cfg) || starts != 0 {
		t.Fatalf("classifier response with an unexpected key authorized handling: category=%q mode=%q classified_of=%q revision=%q starts=%d", replyPolicyTestCategory(it), replyPolicyTestRoute(it), replyPolicyTestContentVersion(it), replyPolicyTestPolicyRevision(it), starts)
	}
}

func TestInboxReplyPolicyFollowMergedLaterRoundIncludesOlderMemberEvidence(t *testing.T) {
	h := agentHub(t)
	cfg := replyPolicyTestConfig()
	cfg.Sources = []string{"synthetic"}
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
	const memberMarker = "Synthetic late member update older than the survivor display event"
	replyPolicyReview9PolicyMarkerModel(t, h, memberMarker)
	base := time.Now().Add(-time.Hour)
	survivor := &InboxItem{ID: "synthetic:review10-survivor-later", Source: "synthetic", AccountName: "Synthetic", Kind: KindDM,
		Last: "survivor-1", Context: "Survivor already advanced in the preceding round", Count: 1,
		UpdatedAt: base.Add(2 * time.Minute), StartedAt: base, Tier: TierAction, TriagedAt: "survivor-1",
		Connector: &InboxConnector{Thread: "survivor-later"}, MergedFrom: []InboxMergeRef{{ID: "synthetic:review10-member-later", Source: "synthetic"}}}
	member := &InboxItem{ID: "synthetic:review10-member-later", Source: "synthetic", AccountName: "Synthetic", Kind: KindDM,
		Last: "member-0", Context: "Original member context", Count: 1, UpdatedAt: base, StartedAt: base,
		MergedInto: survivor.ID, Connector: &InboxConnector{Thread: "member-later"}}

	h.inbox.mu.Lock()
	h.inbox.items[survivor.ID], h.inbox.items[member.ID] = survivor, member
	changed := h.inbox.applyConnector("synthetic", "Synthetic", []connector.InboxThing{
		{Thread: "member-later", Kind: KindDM, Last: "member-1", At: base.Add(time.Minute), Context: memberMarker},
	}, time.Now())
	memberChanged, survivorChanged := false, false
	for _, it := range changed {
		memberChanged = memberChanged || it == member
		survivorChanged = survivorChanged || it == survivor
	}
	if !memberChanged || survivorChanged {
		h.inbox.mu.Unlock()
		t.Fatalf("test setup did not advance only the member: member=%t survivor=%t", memberChanged, survivorChanged)
	}
	h.inbox.followMerged(changed, time.Now())
	h.inbox.mu.Unlock()
	h.classifyInboxReplyPolicies(t.Context(), cfg)
	if !strings.Contains(survivor.Context, memberMarker) || replyPolicyTestCategory(survivor) != "approval_permission" || replyPolicyTestRoute(survivor) != ReplyRouteOwnerOnly {
		t.Fatalf("later-round member advancement was skipped because its event was older than the survivor: context=%q category=%q mode=%q",
			survivor.Context, replyPolicyTestCategory(survivor), replyPolicyTestRoute(survivor))
	}
}

func review10BlockingKBStart(t *testing.T, mutate func(*inboxHub, *InboxItem)) {
	t.Helper()
	h, _ := newInboxHubKB(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
	it := actionItem("lark:review10-kb-authorization", time.Now())
	it.Source = "lark"
	it.Agent = &InboxAgent{State: AgentQueued, ProcessingTurn: "synthetic-old-processing-turn"}
	{
		replyPolicyTestSetCategory(it, "ordinary_coordination")
		replyPolicyTestSetRoute(it, ReplyRouteOwnerDraft)
	}
	{
		replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
		replyPolicyTestSetPolicyRevision(it, "test-v1")
	}
	h.inbox.items[it.ID] = it
	blocking := &inboxGroupStateWakeBlockingHelloHub{fakeHub: h.f, entered: make(chan struct{}), release: make(chan struct{})}
	h.Service.hub = blocking
	var configReads atomic.Int32
	h.in.configHook = func() {
		if configReads.Add(1) == 2 {
			// The next blocking preparation is the KB lookup after the prompt and its authorization tuple were built.
			blocking.armed.Store(true)
		}
	}
	before := len(conversationStarts(h.f))
	done := make(chan struct{})
	go func() {
		h.startInboxAgents(t.Context(), InboxAgentConfig{Enabled: true})
		close(done)
	}()
	select {
	case <-blocking.entered:
	case <-time.After(5 * time.Second):
		close(blocking.release)
		t.Fatal("queue start did not block in KB preparation")
	}
	mutate(h, it)
	close(blocking.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("queue start did not finish after KB preparation")
	}
	if got := len(conversationStarts(h.f)) - before; got != 0 {
		t.Fatalf("queue started %d conversations with a prompt and authorization captured at different item states", got)
	}
	h.inbox.mu.Lock()
	session := it.Agent.SessionID
	h.inbox.mu.Unlock()
	if session != "" {
		t.Fatalf("stale prompt authorization assigned session %q", session)
	}
}

func TestInboxReplyPolicyQueueContentAuthorizationCapturedBeforeKBContext(t *testing.T) {
	review10BlockingKBStart(t, func(h *inboxHub, it *InboxItem) {
		h.inbox.mu.Lock()
		defer h.inbox.mu.Unlock()
		it.Last = "synthetic-new-content-during-kb"
		it.Context = "Synthetic replacement content arriving while the old prompt waits for KB context"
		{
			replyPolicyTestSetContentVersion(
				// Keep the replacement authorized so only the prompt/version pairing can reject this start.
				it, replyPolicyContentVersion(it))
		}
	})
}

func TestInboxReplyPolicyDeliverNotesRechecksPolicyAfterHello(t *testing.T) {
	h, _, it := handledHub(t)
	replyPolicyReview8OrdinaryItem(h, it)
	const pendingWork = "Synthetic ordinary work waiting across policy activation"
	h.inbox.mu.Lock()
	it.Context = pendingWork
	{
		replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
	}
	it.Agent.Pending = nil
	it.Agent.queueNote(inboxNote{Name: prompts.NoteNewMessages, Context: pendingWork})
	it.Agent.Wake = true
	oldTurn := it.Agent.ProcessingTurn
	h.inbox.mu.Unlock()
	blocking := &inboxGroupStateWakeBlockingHelloHub{fakeHub: h.f, entered: make(chan struct{}), release: make(chan struct{})}
	blocking.armed.Store(true)
	h.Service.hub = blocking
	before := len(h.f.sentToAgents())
	done := make(chan struct{})
	go func() {
		h.deliverNotes(t.Context(), it)
		close(done)
	}()
	select {
	case <-blocking.entered:
	case <-time.After(5 * time.Second):
		close(blocking.release)
		t.Fatal("deliverNotes did not reach its blocking Hello")
	}
	h.setConfig(func(c *assistantwire.InboxConfig) {
		cfg := replyPolicyTestConfig()
		cfg.Categories["ordinary_coordination"] = ReplyRouteOwnerOnly
		cfg.Revision = "test-v2"
		c.ReplyPolicy = cfg
	})
	close(blocking.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("deliverNotes did not finish after Hello")
	}
	after := h.f.sentToAgents()
	h.inbox.mu.Lock()
	pending, wake, turn := len(it.Agent.Pending), it.Agent.Wake, it.Agent.ProcessingTurn
	h.inbox.mu.Unlock()
	if len(after) != before || strings.Contains(strings.Join(after[before:], "\n"), pendingWork) || pending != 1 || !wake || turn != oldTurn {
		t.Fatalf("work was delivered with policy captured before Hello: new_messages=%q pending=%d wake=%t processing_turn %q -> %q",
			strings.Join(after[before:], "\n"), pending, wake, oldTurn, turn)
	}
}

func TestInboxReplyPolicyInstructedAgentReplacementRequiresTextReview(t *testing.T) {
	h, _, it := handledHub(t)
	replyPolicyReview8OrdinaryItem(h, it)
	original := it.Context
	if _, err := suggestOrdinaryReplyForFixture(t, h, `{"kind":"agent","text":"Original ordinary Agent instruction"}`); err != nil {
		t.Fatal(err)
	}
	instruction := "Synthetic owner rewrite instruction for an Agent replacement"
	if _, err := h.actOnSuggestion(t.Context(), it.ID, suggestionReq{Version: 1, Action: suggestInstruct, Text: &instruction}, true); err != nil {
		t.Fatal(err)
	}
	toldConv(t, h, instruction)
	model, calls := captureModel(t, `{"category":"approval_permission"}`)
	h.in.model = model
	const restricted = "Restricted generated Agent replacement requiring text review"
	_, err := suggestTool(t, h, `{"kind":"agent","text":"`+restricted+`"}`)
	got := calls()
	if !replyPolicyDenied(err) || len(got) != 1 || currentSuggestion(it) != nil {
		t.Fatalf("instructed Agent replacement bypassed generated-text review: err=%v model_calls=%d suggestion=%+v", err, len(got), currentSuggestion(it))
	}
	for _, marker := range []string{restricted, instruction, original} {
		if !strings.Contains(got[0].User, marker) {
			t.Fatalf("instructed Agent replacement classifier input lacks %q: %+v", marker, got[0])
		}
	}
}
