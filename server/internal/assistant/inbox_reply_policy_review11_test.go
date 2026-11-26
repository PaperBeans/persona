package assistant

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/prompts"
)

func TestInboxReplyPolicyIndependentReviewReceivesClassificationInstructions(t *testing.T) {
	h := agentHub(t)
	cfg := replyPolicyTestConfig()
	cfg.Categories = map[string]string{
		" Custom_Owner_Action ": ReplyRouteOwnerDraft,
		"custom_denied":         ReplyRouteOwnerOnly,
		"unknown":               ReplyRouteOwnerOnly,
	}
	cfg.Rules = "Synthetic custom rule: custom owner actions still require an independent classification"
	cfg.RequireIndependentReview = true
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
	classifierSystem := replyPolicyTestClassifierSystem(h, cfg)
	var mu sync.Mutex
	var requests []capturedCall
	model, _ := replyPolicyClassifierModel(t, func(call int, system, user string, _ *http.Request) (int, string) {
		mu.Lock()
		requests = append(requests, capturedCall{System: system, User: user})
		mu.Unlock()
		if call == 1 {
			return http.StatusOK, `{"category":"custom_owner_action","reason":"primary-response-sentinel"}`
		}
		complete := system == classifierSystem
		for _, marker := range []string{"custom_owner_action", "custom_denied", cfg.Rules} {
			complete = complete && strings.Contains(system, marker)
		}
		if !complete {
			return http.StatusOK, `{"category":"custom_denied","reason":"reviewer did not receive the classifier instructions"}`
		}
		return http.StatusOK, `{"category":"custom_owner_action","reason":"independent agreement"}`
	})
	h.in.model = model
	it := actionItem("lark:independent-review-instructions", time.Now())
	it.Source = "lark"
	it.Agent = &InboxAgent{State: AgentQueued}
	h.inbox.items[it.ID] = it
	h.classifyInboxReplyPolicies(t.Context(), cfg)

	mu.Lock()
	got := append([]capturedCall(nil), requests...)
	mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("independent review made %d model calls, want primary plus reviewer", len(got))
	}
	snap := it.ReplyPolicy
	if snap == nil || snap.Category != "custom_owner_action" || snap.Route != ReplyRouteOwnerDraft || !snap.Reviewed || snap.ContentVersion == "" || snap.PolicyRevision != compileReplyPolicy(cfg).revision || snap.Error != "" || !replyPolicyClassificationCurrent(it, cfg) {
		t.Fatalf("fully instructed agreeing review did not produce a current reviewed snapshot: %+v", snap)
	}
	if got[0].System != classifierSystem || got[1].System != classifierSystem {
		t.Fatalf("reviewer did not receive the complete classifier instructions: primary=%q reviewer=%q want=%q", got[0].System, got[1].System, classifierSystem)
	}
	for _, marker := range []string{"custom_owner_action", "custom_denied", "unknown", cfg.Rules} {
		if !strings.Contains(got[1].System, marker) {
			t.Errorf("reviewer instructions omit %q: %q", marker, got[1].System)
		}
	}
	if got[1].User != got[0].User || strings.Contains(got[1].System+got[1].User, "primary-response-sentinel") {
		t.Fatalf("independent reviewer received the primary answer instead of only the original input: primary=%+v reviewer=%+v", got[0], got[1])
	}
}

func TestInboxReplyPolicyOutOfScopeSuggestionConfirmationSkipsClassifier(t *testing.T) {
	h, lark, it := handledHub(t)
	cfg := replyPolicyTestConfig()
	cfg.Sources = []string{"mail"}
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
	model, calls := replyPolicyClassifierModel(t, func(_ int, _, _ string, _ *http.Request) (int, string) {
		return http.StatusOK, `{"category":"approval_permission","reason":"would deny an in-scope send"}`
	})
	h.in.setModel(model)
	const text = "Synthetic Lark reply outside the mail-only policy"
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"`+text+`"}`); err != nil {
		t.Fatalf("publishing the out-of-scope suggestion failed: %v", err)
	}
	g := currentSuggestion(it)
	if g == nil || g.ReplyPolicy != nil || it.ReplyPolicy != nil {
		t.Fatalf("test setup did not publish a Lark suggestion without a policy snapshot: suggestion=%+v item_policy=%+v", g, it.ReplyPolicy)
	}
	if got := calls(); got != 0 {
		t.Fatalf("publishing the out-of-scope suggestion made %d policy classifier calls", got)
	}
	code, out := suggestionCall(t, h, it.ID, `{"version":1,"action":"confirm"}`)
	if code != http.StatusOK || len(lark.got) != 1 || lark.got[0].Text != text || calls() != 0 {
		t.Fatalf("out-of-scope confirmation did not retain legacy send behavior: code=%d out=%+v sends=%+v classifier_calls=%d", code, out, lark.got, calls())
	}
}

func TestInboxReplyPolicyCompletedReceiptPrecedesReplacementMirror(t *testing.T) {
	c := &recoveryReplier{}
	h, _, it := handledHub(t)
	h.in.mu.Lock()
	h.in.conns[0] = c
	h.in.mu.Unlock()
	replyPolicyReview8OrdinaryItem(h, it)
	const completed = "Synthetic reply A whose successful response was lost"
	if _, err := suggestOrdinaryReplyForFixture(t, h, `{"kind":"reply","text":"`+completed+`"}`); err != nil {
		t.Fatal(err)
	}
	h.in.setModel(fakeModel(t, `{"category":"ordinary_coordination"}`))
	g := currentSuggestion(it)
	if g == nil {
		t.Fatal("test setup did not publish reply A")
	}
	body := fmt.Sprintf(`{"text":%q}`, completed)
	code, out := inboxCall(t, h.routes(), http.MethodPost, "/api/v1/inbox/"+it.ID+"/reply", body)
	if code != http.StatusOK || len(c.calls()) != 1 {
		t.Fatalf("first reply A confirmation failed: code=%d out=%+v sends=%+v", code, out, c.calls())
	}
	const replacement = "Synthetic replacement draft B published before the old client retried A"
	if g := finalReviewInstallMirror(h, it, replacement); g == nil {
		t.Fatal("test setup did not publish replacement draft B")
	}

	// The old client still has only A's exact text. B's presence must not turn this retry into an edit and second send.
	// Keep the edited text ordinarily classifiable so the old implementation reaches the physical-send assertion.
	h.in.setModel(fakeModel(t, `{"category":"ordinary_coordination"}`))
	code, out = inboxCall(t, h.routes(), http.MethodPost, "/api/v1/inbox/"+it.ID+"/reply", body)
	if code != http.StatusOK || len(c.calls()) != 1 {
		t.Fatalf("completed reply A was sent again after replacement draft B appeared: code=%d out=%+v sends=%+v", code, out, c.calls())
	}
}

func TestInboxReplyPolicyInstructedOnDemandDraftKeepsWithdrawnProvenance(t *testing.T) {
	const generated = "Synthetic on-demand draft withdrawn by an owner rewrite instruction"
	newWithdrawnDraft := func(t *testing.T) (*inboxHub, *extensionLark, *InboxItem) {
		t.Helper()
		h, lark, it := handledHub(t)
		replyPolicyReview8OrdinaryItem(h, it)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.Agent.Enabled = false })
		replyPolicyOrdinaryDraftModel(t, h, generated)
		code, out := inboxCall(t, h.routes(), http.MethodPost, "/api/v1/inbox/"+it.ID+"/draft", `{}`)
		if code != http.StatusOK || out["text"] != generated {
			t.Fatalf("on-demand draft failed: code=%d out=%+v", code, out)
		}
		instruction := "Synthetic owner request for a different draft"
		code, out = suggestionCall(t, h, it.ID, fmt.Sprintf(`{"version":1,"action":"instruct","text":%q}`, instruction))
		if code != http.StatusOK || currentSuggestion(it) != nil {
			t.Fatalf("rewrite instruction did not withdraw the old mirror: code=%d out=%+v suggestion=%+v", code, out, currentSuggestion(it))
		}
		h.inbox.mu.Lock()
		{
			replyPolicyTestSetCategory(it, "approval_permission")
			replyPolicyTestSetRoute(it, ReplyRouteOwnerOnly)
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

	t.Run("withdrawn generated text", func(t *testing.T) {
		h, lark, it := newWithdrawnDraft(t)
		code, out := inboxCall(t, h.routes(), http.MethodPost, "/api/v1/inbox/"+it.ID+"/reply", fmt.Sprintf(`{"text":%q}`, generated))
		if code == http.StatusOK || len(lark.got) != 0 {
			t.Fatalf("withdrawn on-demand text became a direct owner reply before its replacement arrived: code=%d out=%+v sends=%+v", code, out, lark.got)
		}
	})

	t.Run("different direct owner text", func(t *testing.T) {
		h, lark, it := newWithdrawnDraft(t)
		const ownerText = "Different text written directly by the owner after requesting a rewrite"
		code, out := inboxCall(t, h.routes(), http.MethodPost, "/api/v1/inbox/"+it.ID+"/reply", fmt.Sprintf(`{"text":%q}`, ownerText))
		if code != http.StatusOK || len(lark.got) != 1 || lark.got[0].Text != ownerText {
			t.Fatalf("genuine direct owner text was gated with withdrawn-draft provenance: code=%d out=%+v sends=%+v", code, out, lark.got)
		}
	})
}

func review11RefreshedMailSurvivor(t *testing.T, readBody bool) *InboxItem {
	t.Helper()
	h := agentHub(t)
	cfg := replyPolicyTestConfig()
	cfg.Sources = []string{"mail"}
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
	now := time.Now().UTC()
	base := now.Add(-time.Hour)
	account := assistantwire.InboxMailAccount{ID: "review11-mail", Email: "me@example.com"}
	from := MailAddress{Name: "Sender", Email: "sender@example.com"}
	to := []MailAddress{{Email: account.Email}}
	subject := "Synthetic ordinary survivor refresh"
	const memberMarker = "Synthetic restricted merged-member approval request that must survive mail refresh"
	survivor := &InboxItem{ID: "mail:review11:survivor", Source: "mail", Kind: KindMail, Account: account.ID,
		Last: "8", BodyOf: "8", TriagedAt: "8", Context: "Original ordinary survivor body\n\n" + memberMarker,
		Count: 2, StartedAt: base, UpdatedAt: base, Tier: TierAction,
		Mail:                &InboxMail{AccountID: account.ID, Mailbox: "INBOX", UIDs: []uint32{8}, Thread: mailThread(subject, from.Email)},
		MergedFrom:          []InboxMergeRef{{ID: "mail:review11:member", Source: "mail"}},
		ReplyContentSources: []string{"mail"}}
	member := &InboxItem{ID: "mail:review11:member", Source: "mail", Kind: KindMail, Account: account.ID,
		Last: "7", BodyOf: "7", TriagedAt: "7", Context: memberMarker, Count: 1, StartedAt: base, UpdatedAt: base, Tier: TierAction,
		Mail: &InboxMail{AccountID: account.ID, Mailbox: "INBOX", UIDs: []uint32{7}, Thread: "different-member-thread"}, MergedInto: survivor.ID}
	h.inbox.items[survivor.ID], h.inbox.items[member.ID] = survivor, member
	message := assistantwire.InboxMailMessage{From: from, To: to, Subject: subject, Date: base.Add(time.Minute).Format(time.RFC3339),
		Text: "A short ordinary replacement body"}
	h.in.mail = &fakeMail{bodies: map[uint32]assistantwire.InboxMailMessage{9: message}}
	summary := assistantwire.InboxMailSummary{Mailbox: "INBOX", UID: 9, Subject: subject, From: from, To: to,
		Date: message.Date, Snippet: "Ordinary survivor-only snippet"}
	changed := h.inbox.applyMail(account, []assistantwire.InboxMailSummary{summary}, now)
	h.inbox.mu.Lock()
	h.inbox.followMerged(changed, now)
	h.inbox.mu.Unlock()
	if survivor.Last != "9" {
		t.Fatalf("mail fixture did not refresh the survivor: last=%q", survivor.Last)
	}
	if readBody && !h.readMailBody(t.Context(), account, survivor) {
		t.Fatal("mail fixture could not read the refreshed survivor body")
	}
	return survivor
}

func TestInboxReplyPolicyMailSurvivorSummaryRefreshRetainsMemberEvidence(t *testing.T) {
	it := review11RefreshedMailSurvivor(t, false)
	if !strings.Contains(it.Context, "Synthetic restricted merged-member approval request") {
		t.Fatalf("mail summary refresh erased merged-member authorization evidence: %q", it.Context)
	}
}

func TestInboxReplyPolicyMailSurvivorBodyRefreshRetainsMemberEvidence(t *testing.T) {
	it := review11RefreshedMailSurvivor(t, true)
	if it.BodyOf != it.Last || !strings.Contains(it.Context, "Synthetic restricted merged-member approval request") {
		t.Fatalf("mail body refresh erased merged-member authorization evidence: body_of=%q last=%q context=%q", it.BodyOf, it.Last, it.Context)
	}
}

func review11AgentStartPolicyChangeDuringSettings(t *testing.T, handed bool) {
	t.Helper()
	h := agentHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) {
		c.Agent.Enabled = true
		c.ReplyPolicy = replyPolicyTestConfig()
	})
	id := "review11-agent-settings"
	if handed {
		id = "review11-handed-agent-settings"
	}
	it := disabledTwinItem(h, id, HandlingDone, AgentSkipped, "parent-session")
	it.Source = "lark"
	{
		replyPolicyTestSetCategory(it, "ordinary_coordination")
		replyPolicyTestSetRoute(it, ReplyRouteOwnerDraft)
	}
	{
		replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
		replyPolicyTestSetPolicyRevision(it, "test-v1")
	}
	g := &InboxSuggestion{Version: 1, Kind: SuggestAgent, Text: "Inspect the synthetic request", At: time.Now(), MachineID: "server"}
	snapshotReplyPolicy(g, it, replyPolicyTestConfig())
	it.Suggestion = g
	blocking := &inboxGroupStateWakeBlockingHelloHub{fakeHub: h.f, entered: make(chan struct{}), release: make(chan struct{})}
	blocking.armed.Store(true)
	h.Service.hub = blocking
	before := h.f.startCount()
	done := make(chan error, 1)
	go func() {
		if handed {
			_, err := h.confirmHandedAgent(t.Context(), it, g, g.Text, false, SessionRef{MachineID: "server", SessionID: "handoff-session"}, "Synthetic")
			done <- err
			return
		}
		_, err := h.confirmAgent(t.Context(), it, g, g.Text, false)
		done <- err
	}()
	select {
	case <-blocking.entered:
	case <-time.After(5 * time.Second):
		close(blocking.release)
		t.Fatal("Agent start did not reach its blocking settings resolution")
	}
	h.setConfig(func(c *assistantwire.InboxConfig) {
		cfg := replyPolicyTestConfig()
		cfg.Categories["ordinary_coordination"] = ReplyRouteOwnerOnly
		cfg.Revision = "test-v2"
		c.ReplyPolicy = cfg
	})
	close(blocking.release)
	var err error
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Agent start did not finish after settings resolution")
	}
	if starts := h.f.startCount() - before; !replyPolicyDenied(err) || starts != 0 {
		t.Fatalf("Agent start used authorization captured before blocking settings: handed=%t err=%v starts=%d", handed, err, starts)
	}
}

func TestInboxReplyPolicyAgentStartResolvesSettingsBeforeFinalAuthorization(t *testing.T) {
	t.Run("item conversation", func(t *testing.T) { review11AgentStartPolicyChangeDuringSettings(t, false) })
	t.Run("handed-off conversation", func(t *testing.T) { review11AgentStartPolicyChangeDuringSettings(t, true) })
}

func TestInboxReplyPolicyMetadataOnlyMemberUpdateDoesNotReopenSurvivor(t *testing.T) {
	h := agentHub(t)
	base := time.Now().Add(-time.Hour)
	doneAt := base.Add(30 * time.Minute)
	survivor := &InboxItem{ID: "synthetic:review11-survivor", Source: "synthetic", AccountName: "Old connector name", Kind: KindDM,
		Last: "survivor-1", Context: "Original survivor context", Count: 2, UpdatedAt: base.Add(time.Minute), StartedAt: base,
		Tier: TierAction, TriagedAt: "survivor-1", DoneAt: &doneAt, DoneBy: "user", Connector: &InboxConnector{Thread: "survivor"},
		MergedFrom: []InboxMergeRef{{ID: "synthetic:review11-member", Source: "synthetic"}},
		Chain:      []InboxChainEntry{{ID: "c1", Kind: ChainReceived, At: base}}}
	member := &InboxItem{ID: "synthetic:review11-member", Source: "synthetic", AccountName: "Old connector name", Kind: KindDM,
		Last: "member-1", Context: "Existing member context", Count: 1, UpdatedAt: base, StartedAt: base,
		Tier: TierAction, TriagedAt: "member-1", MergedInto: survivor.ID, Connector: &InboxConnector{Thread: "member"}}
	h.inbox.mu.Lock()
	h.inbox.items[survivor.ID], h.inbox.items[member.ID] = survivor, member
	changed := h.inbox.applyConnector("synthetic", "Renamed connector", nil, time.Now())
	if !containsItem(changed, survivor) || !containsItem(changed, member) || member.receivedInCollection {
		h.inbox.mu.Unlock()
		t.Fatalf("test setup did not create a metadata-only update for both rows: changed=%+v received=%t", changed, member.receivedInCollection)
	}
	h.inbox.followMerged(changed, time.Now())
	h.inbox.mu.Unlock()
	if survivor.Count != 2 || survivor.DoneAt == nil || survivor.Context != "Original survivor context" || len(survivor.Chain) != 1 {
		t.Fatalf("metadata-only member update fabricated a received event and reopened its survivor: count=%d done_at=%v context=%q chain=%+v",
			survivor.Count, survivor.DoneAt, survivor.Context, survivor.Chain)
	}
}

func TestInboxReplyPolicyCompleteMailBodyWithHeadersIsNotTruncated(t *testing.T) {
	h := agentHub(t)
	now := time.Now().UTC()
	account := assistantwire.InboxMailAccount{ID: "review11-complete-mail", Email: "me@example.com"}
	from := MailAddress{Name: "Sender", Email: "sender@example.com"}
	to := []MailAddress{{Email: account.Email}}
	subject := "Synthetic complete body below the clipping limit"
	summary := assistantwire.InboxMailSummary{Mailbox: "INBOX", UID: 9, Subject: subject, From: from, To: to,
		Date: now.Add(-time.Hour).Format(time.RFC3339), Snippet: "Ordinary snippet"}
	const bodyRunes = 2950
	body := strings.Repeat("x", bodyRunes)
	h.in.mail = &fakeMail{bodies: map[uint32]assistantwire.InboxMailMessage{9: {From: from, To: to, Subject: subject, Date: summary.Date, Text: body}}}
	items := h.inbox.applyMail(account, []assistantwire.InboxMailSummary{summary}, now)
	if len(items) != 1 {
		t.Fatalf("mail fixture produced %d items, want one", len(items))
	}
	it := items[0]
	if !h.readMailBody(t.Context(), account, it) || it.BodyOf != it.Last || utf8.RuneCountInString(it.Context) <= 3000 {
		t.Fatalf("test setup did not create a complete body whose formatted context exceeds the limit: body_of=%q last=%q context_runes=%d",
			it.BodyOf, it.Last, utf8.RuneCountInString(it.Context))
	}
	raw, err := json.Marshal(it)
	if err != nil {
		t.Fatal(err)
	}
	var stored InboxItem
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if replyPolicyContextTruncated(&stored) {
		t.Fatalf("complete %d-rune mail body was treated as truncated because formatted headers crossed the limit", bodyRunes)
	}
}

func TestInboxReplyPolicyClassifierRejectsPaddedCategory(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{name: "exact", raw: `{"category":"acknowledgement"}`},
		{name: "trailing newline", raw: `{"category":"acknowledgement\n"}`},
		{name: "leading space", raw: `{"category":" acknowledgement"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := agentHub(t)
			h.in.setModel(fakeModel(t, tc.raw))
			cfg := replyPolicyTestConfig()
			category, err := h.classifyReplyPolicyInput(t.Context(), cfg, prompts.InboxReplyPolicyInput{Context: "Synthetic classification input"})
			if tc.name == "exact" {
				if err != nil || category != "acknowledgement" {
					t.Fatalf("exact enum category was rejected: category=%q err=%v", category, err)
				}
				return
			}
			if err == nil || replyRoute(category, cfg) == ReplyRouteOwnerDraft {
				t.Fatalf("classifier accepted out-of-enum padded category from %s: category=%q err=%v", tc.raw, category, err)
			}
		})
	}
}
