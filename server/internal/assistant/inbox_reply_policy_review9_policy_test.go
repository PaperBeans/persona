package assistant

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"errors"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/prompts"
)

func replyPolicyReview9PolicyModelReply(w http.ResponseWriter, reply string) {
	_ = json.NewEncoder(w).Encode(map[string]any{"content": []any{map[string]any{"type": "text", "text": reply}}})
}

// replyPolicyReview9PolicyMarkerModel makes the resulting authorization depend on evidence in the classifier input.
// A static model reply cannot distinguish retained member evidence from an ordinary survivor-only context.
func replyPolicyReview9PolicyMarkerModel(t *testing.T, h *inboxHub, marker string) func() []capturedCall {
	t.Helper()
	var mu sync.Mutex
	var calls []capturedCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			http.NotFound(w, r)
			return
		}
		var in struct {
			System   string `json:"system"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		call := capturedCall{System: in.System}
		if len(in.Messages) > 0 {
			call.User = in.Messages[0].Content
		}
		mu.Lock()
		calls = append(calls, call)
		mu.Unlock()
		category := "ordinary_coordination"
		if strings.Contains(call.User, marker) {
			category = "approval_permission"
		}
		replyPolicyReview9PolicyModelReply(w, `{"category":"`+category+`"}`)
	}))
	t.Cleanup(srv.Close)
	h.in.model = modelAt(t, srv.URL)
	return func() []capturedCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]capturedCall(nil), calls...)
	}
}

func TestInboxReplyPolicyConcurrentClassificationCannotOverwriteDenialLegacySchema(t *testing.T) {
	h := agentHub(t)
	cfg := legacyReplyPolicyTestConfig()
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
	it := actionItem("lark:review9-concurrent-classification", time.Now())
	it.Source = "lark"
	it.Agent = &InboxAgent{State: AgentQueued}
	h.inbox.items[it.ID] = it

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseFirst) }) }
	defer release()
	var modelCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if modelCalls.Add(1) == 1 {
			close(firstStarted)
			<-releaseFirst
			replyPolicyReview9PolicyModelReply(w, `{"category":"ordinary_coordination"}`)
			return
		}
		replyPolicyReview9PolicyModelReply(w, `{"category":"approval_permission"}`)
	}))
	t.Cleanup(srv.Close)
	h.in.model = modelAt(t, srv.URL)

	firstDone := make(chan error, 1)
	go func() { firstDone <- h.classifyInboxReplyPolicyItem(t.Context(), cfg, it) }()
	select {
	case <-firstStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("first classification did not reach its model call")
	}
	secondDone := make(chan error, 1)
	go func() { secondDone <- h.classifyInboxReplyPolicyItem(t.Context(), cfg, it) }()
	secondEarly := false
	select {
	case err := <-secondDone:
		secondEarly = true
		if err != nil {
			release()
			t.Fatalf("denial classification failed: %v", err)
		}
		if it.ReplyCategory != "approval_permission" || it.ReplyMode != ReplyRouteOwnerOnly {
			release()
			t.Fatalf("test did not commit its denial first: %+v", it)
		}
	case <-time.After(500 * time.Millisecond):
		// A per-item serialization fix legitimately makes the second call wait. Release the first rather than rejecting
		// that valid implementation; the final committed denial assertion below is the authorization invariant.
	}

	release()
	if !secondEarly {
		select {
		case err := <-secondDone:
			if err != nil {
				t.Fatalf("serialized denial classification failed: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("denial classification did not finish after releasing the ordinary result")
		}
	}
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("ordinary classification failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ordinary classification did not finish")
	}
	if it.ReplyCategory != "approval_permission" || it.ReplyMode != ReplyRouteOwnerOnly {
		t.Fatalf("slower ordinary classification overwrote the committed denial: category=%q mode=%q", it.ReplyCategory, it.ReplyMode)
	}

	h.st.imu.Lock()
	raw := append([]byte(nil), h.st.rows[it.ID]...)
	h.st.imu.Unlock()
	var stored InboxItem
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("decode persisted classification: %v", err)
	}
	if stored.ReplyCategory != "approval_permission" || stored.ReplyMode != ReplyRouteOwnerOnly {
		t.Fatalf("slower ordinary classification overwrote the persisted denial: category=%q mode=%q", stored.ReplyCategory, stored.ReplyMode)
	}
}

func TestInboxReplyPolicyFollowMergedIncludesMemberWhenSurvivorAlsoAdvancesLegacySchema(t *testing.T) {
	h := agentHub(t)
	cfg := legacyReplyPolicyTestConfig()
	cfg.Sources = []string{"synthetic"}
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
	const memberMarker = "Synthetic restricted member update hidden by survivor advancement"
	replyPolicyReview9PolicyMarkerModel(t, h, memberMarker)
	base := time.Now().Add(-time.Hour)
	survivor := &InboxItem{ID: "synthetic:review9-survivor-round", Source: "synthetic", Kind: KindDM,
		Last: "survivor-0", Context: "Original ordinary survivor context", Count: 1, UpdatedAt: base, StartedAt: base,
		Tier: TierAction, TriagedAt: "survivor-0", Connector: &InboxConnector{Thread: "survivor-round"},
		MergedFrom: []InboxMergeRef{{ID: "synthetic:review9-member-round", Source: "synthetic"}}}
	member := &InboxItem{ID: "synthetic:review9-member-round", Source: "synthetic", Kind: KindDM,
		Last: "member-0", Context: "Original member context", Count: 1, UpdatedAt: base, StartedAt: base,
		MergedInto: survivor.ID, Connector: &InboxConnector{Thread: "member-round"}}

	h.inbox.mu.Lock()
	h.inbox.items[survivor.ID], h.inbox.items[member.ID] = survivor, member
	changed := h.inbox.applyConnector("synthetic", "Synthetic", []connector.InboxThing{
		{Thread: "survivor-round", Kind: KindDM, Last: "survivor-1", At: base.Add(2 * time.Minute), Context: "Ordinary survivor update at the later timestamp"},
		{Thread: "member-round", Kind: KindDM, Last: "member-1", At: base.Add(time.Minute), Context: memberMarker},
	}, time.Now())
	h.inbox.followMerged(changed, time.Now())
	h.inbox.mu.Unlock()
	h.classifyInboxReplyPolicies(t.Context(), cfg)

	if !strings.Contains(survivor.Context, memberMarker) || survivor.ReplyCategory != "approval_permission" || survivor.ReplyMode != ReplyRouteOwnerOnly {
		t.Fatalf("same-round survivor advancement dropped member authorization evidence: context=%q category=%q mode=%q", survivor.Context, survivor.ReplyCategory, survivor.ReplyMode)
	}
}

func TestInboxReplyPolicyConnectorRefreshRetainsMergedMemberEvidenceLegacySchema(t *testing.T) {
	h := agentHub(t)
	cfg := legacyReplyPolicyTestConfig()
	cfg.Sources = []string{"synthetic"}
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
	const memberMarker = "Synthetic restricted member evidence erased by connector refresh"
	replyPolicyReview9PolicyMarkerModel(t, h, memberMarker)
	base := time.Now().Add(-time.Hour)
	survivor := &InboxItem{ID: "synthetic:review9-survivor-refresh", Source: "synthetic", Kind: KindDM,
		Last: "survivor-0", Context: "Original ordinary survivor context\n\n" + memberMarker, Count: 2, UpdatedAt: base, StartedAt: base,
		Tier: TierAction, TriagedAt: "survivor-0", Connector: &InboxConnector{Thread: "survivor-refresh"},
		MergedFrom: []InboxMergeRef{{ID: "synthetic:review9-member-refresh", Source: "synthetic"}}, ReplyContentSources: []string{"synthetic"}}
	member := &InboxItem{ID: "synthetic:review9-member-refresh", Source: "synthetic", Kind: KindDM,
		Last: "member-0", Context: memberMarker, Count: 1, UpdatedAt: base, StartedAt: base,
		MergedInto: survivor.ID, Connector: &InboxConnector{Thread: "member-refresh"}}

	h.inbox.mu.Lock()
	h.inbox.items[survivor.ID], h.inbox.items[member.ID] = survivor, member
	changed := h.inbox.applyConnector("synthetic", "Synthetic", []connector.InboxThing{
		{Thread: "survivor-refresh", Kind: KindDM, Last: "survivor-1", At: base.Add(time.Minute), Context: "New ordinary survivor-only connector context"},
	}, time.Now())
	h.inbox.followMerged(changed, time.Now())
	h.inbox.mu.Unlock()
	h.classifyInboxReplyPolicies(t.Context(), cfg)

	if !strings.Contains(survivor.Context, memberMarker) || survivor.ReplyCategory != "approval_permission" || survivor.ReplyMode != ReplyRouteOwnerOnly {
		t.Fatalf("survivor refresh erased retained member authorization evidence: context=%q category=%q mode=%q", survivor.Context, survivor.ReplyCategory, survivor.ReplyMode)
	}
}

// replyPolicyReview9PolicyMergedInstructionLegacy creates an outstanding owner instruction on the handled survivor and then
// merges a member whose entries are newer than that instruction. The member row itself is older, so the handled row survives.
func replyPolicyReview9PolicyMergedInstructionLegacy(t *testing.T, memberChain func(time.Time) []InboxChainEntry) (*inboxHub, *InboxItem, string) {
	t.Helper()
	h, _, it := handledHub(t)
	replyPolicyReview8OrdinaryItemLegacy(h, it)
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"Original ordinary draft before merge"}`); err != nil {
		t.Fatal(err)
	}
	instruction := "Synthetic owner rewrite instruction that must survive merge provenance"
	if _, err := h.actOnSuggestion(t.Context(), it.ID, suggestionReq{Version: 1, Action: suggestInstruct, Text: &instruction}, true); err != nil {
		t.Fatal(err)
	}
	toldConv(t, h, instruction)
	h.inbox.mu.Lock()
	instructionAt := it.Chain[len(it.Chain)-1].At
	h.inbox.mu.Unlock()

	member := actionItem("lark:review9-merge-member", it.UpdatedAt.Add(-time.Second))
	member.Source = "lark"
	member.StartedAt = member.UpdatedAt
	member.Count = 1
	member.Context = "Synthetic merged member conversation"
	member.Chain = memberChain(instructionAt.Add(time.Second))
	member.ChainSeq = len(member.Chain)
	h.inbox.mu.Lock()
	h.inbox.items[member.ID] = member
	survivor, _ := h.inbox.merge([]*InboxItem{it, member}, time.Now())
	h.inbox.mu.Unlock()
	if survivor != it {
		t.Fatalf("merge survivor = %p, want handled row %p", survivor, it)
	}
	return h, it, instruction
}

func replyPolicyReview9PolicyCheckMergedReplacementLegacy(t *testing.T, h *inboxHub, it *InboxItem, instruction, replacement string) {
	t.Helper()
	calls := replyPolicyDraftSequenceModel(t, h, nil, `{"category":"ordinary_coordination"}`, `{"category":"approval_permission"}`)
	cfg := legacyReplyPolicyTestConfig()
	h.classifyInboxReplyPolicies(t.Context(), cfg)
	if it.ReplyCategory != "ordinary_coordination" || it.ReplyMode != ReplyRouteOwnerDraft {
		t.Fatalf("test did not classify the merged item as an ordinary owner draft: %+v", it)
	}
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"`+replacement+`"}`); !errors.Is(err, errReplyPolicyDenied) {
		t.Fatalf("merged history authorized an unreviewed restricted replacement: err=%v calls=%d suggestion=%+v", err, len(calls()), currentSuggestion(it))
	}
	if g := currentSuggestion(it); g != nil {
		t.Fatalf("restricted replacement after merge was published: %+v", g)
	}
	got := calls()
	if len(got) != 2 {
		t.Fatalf("merged replacement made %d model calls, want item plus generated-text classification", len(got))
	}
	for _, marker := range []string{replacement, instruction} {
		if !strings.Contains(got[1].User, marker) {
			t.Fatalf("generated replacement classifier input lacks %q: %+v", marker, got[1])
		}
	}
}

func TestInboxReplyPolicyMergeChainTrimPreservesActiveInstruction(t *testing.T) {
	h, it, instruction := replyPolicyReview9PolicyMergedInstructionLegacy(t, func(base time.Time) []InboxChainEntry {
		entries := make([]InboxChainEntry, 0, chainMax)
		for i := 0; i < chainMax; i++ {
			entries = append(entries, InboxChainEntry{Kind: ChainReported, At: base.Add(time.Duration(i) * time.Millisecond), Detail: "synthetic-member-progress-" + strconv.Itoa(i)})
		}
		return entries
	})
	const replacement = "Restricted replacement after merge chain trimming"
	replyPolicyReview9PolicyCheckMergedReplacementLegacy(t, h, it, instruction, replacement)
}

func TestInboxReplyPolicyMergeMemberDecisionDoesNotConsumeSurvivorInstruction(t *testing.T) {
	for _, decision := range []string{ChainConfirmed, ChainRejected} {
		t.Run(decision, func(t *testing.T) {
			h, it, instruction := replyPolicyReview9PolicyMergedInstructionLegacy(t, func(base time.Time) []InboxChainEntry {
				return []InboxChainEntry{{Kind: decision, At: base, Detail: "Decision about the member row's own suggestion"}}
			})
			const replacement = "Restricted replacement after another row's decision"
			replyPolicyReview9PolicyCheckMergedReplacementLegacy(t, h, it, instruction, replacement)
		})
	}
}

func TestInboxReplyPolicyIncompleteMailFailsClosedLegacySchema(t *testing.T) {
	for _, tc := range []struct {
		name        string
		clippedBody bool
	}{
		{name: "snippet"},
		{name: "clipped body", clippedBody: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := agentHub(t)
			cfg := legacyReplyPolicyTestConfig()
			cfg.Sources = []string{"mail"}
			h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
			model, calls := captureModel(t, `{"category":"ordinary_coordination"}`)
			h.in.model = model
			now := time.Now().UTC()
			account := assistantwire.InboxMailAccount{ID: "review9-mail", Email: "me@example.com"}
			summary := assistantwire.InboxMailSummary{Mailbox: "INBOX", UID: 9, Subject: "Synthetic incomplete authorization input",
				From: MailAddress{Name: "Sender", Email: "sender@example.com"}, To: []MailAddress{{Email: "me@example.com"}},
				Date: now.Add(-time.Hour).Format(time.RFC3339), Snippet: "Ordinary coordination visible in the snippet"}
			const restrictedSuffix = "Synthetic restricted approval request beyond the retained body prefix"
			h.in.mail = &fakeMail{bodies: map[uint32]assistantwire.InboxMailMessage{}}
			if tc.clippedBody {
				h.in.mail.bodies[9] = assistantwire.InboxMailMessage{From: summary.From, To: summary.To, Subject: summary.Subject,
					Text: strings.Repeat("ordinary coordination\n", 200) + restrictedSuffix}
			}
			items := h.inbox.applyMail(account, []assistantwire.InboxMailSummary{summary}, now)
			if len(items) != 1 {
				t.Fatalf("mail fixture produced %d items, want one", len(items))
			}
			it := items[0]
			h.inbox.mu.Lock()
			it.Tier, it.TriagedAt = TierAction, it.Last
			h.inbox.mu.Unlock()
			if tc.clippedBody {
				if !h.readMailBody(t.Context(), account, it) {
					t.Fatal("clipped-body fixture could not read its mail body")
				}
				if it.BodyOf != it.Last || strings.Contains(it.Context, restrictedSuffix) {
					t.Fatalf("test did not create a clipped current body: body_of=%q last=%q contains_suffix=%v", it.BodyOf, it.Last, strings.Contains(it.Context, restrictedSuffix))
				}
			} else if it.BodyOf == it.Last {
				t.Fatalf("snippet fixture unexpectedly has a current full body: body_of=%q last=%q", it.BodyOf, it.Last)
			}

			h.classifyInboxReplyPolicies(t.Context(), cfg)
			if it.ReplyCategory != "unknown" || it.ReplyMode != ReplyRouteOwnerOnly || !replyPolicyClassificationCurrent(it, cfg) || len(calls()) != 0 {
				t.Fatalf("incomplete mail authorized classification from a partial input: body_of=%q last=%q category=%q mode=%q truncated=%v model_calls=%d",
					it.BodyOf, it.Last, it.ReplyCategory, it.ReplyMode, it.ReplyContextTruncated, len(calls()))
			}
		})
	}
}

func TestInboxReplyPolicyDeliverNotesGatesWorkButAllowsClosedNoticeLegacySchema(t *testing.T) {
	t.Run("work", func(t *testing.T) {
		h, _, it := handledHub(t)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
		const restrictedWork = "Synthetic owner-only approval work delivered to an existing session"
		h.inbox.mu.Lock()
		it.Context = restrictedWork
		it.ReplyCategory, it.ReplyMode = "approval_permission", ReplyRouteOwnerOnly
		it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
		it.Agent.Pending = nil
		it.Agent.queueNote(inboxNote{Name: prompts.NoteNewMessages, Context: restrictedWork})
		it.Agent.Wake = true
		oldTurn := it.Agent.ProcessingTurn
		h.inbox.mu.Unlock()
		before := len(h.f.sentToAgents())
		h.deliverNotes(t.Context(), it)
		after := h.f.sentToAgents()
		if len(after) != before || strings.Contains(strings.Join(after[before:], "\n"), restrictedWork) || it.Agent.ProcessingTurn != oldTurn {
			t.Fatalf("owner-only work reached the existing processing session: new_messages=%q processing_turn %q -> %q", strings.Join(after[before:], "\n"), oldTurn, it.Agent.ProcessingTurn)
		}
	})

	t.Run("closed notice", func(t *testing.T) {
		h, _, it := handledHub(t)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
		it.ReplyCategory, it.ReplyMode = "approval_permission", ReplyRouteOwnerOnly
		it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
		h.inbox.mu.Lock()
		queued := h.inbox.closeHandling(it, "user", time.Now())
		h.inbox.file(it, time.Now(), "user")
		h.inbox.mu.Unlock()
		if !queued || it.open() {
			t.Fatalf("terminal-notice fixture did not close an active handling session: queued=%v open=%v", queued, it.open())
		}
		hello := h.hello(t.Context())
		want := renderNoteIn(inboxNote{Name: prompts.NoteClosed, Text: "user"}, inboxPromptOwner(hello), answerLanguage(hello.Locale))
		before := len(h.f.sentToAgents())
		h.deliverNotes(t.Context(), it)
		after := h.f.sentToAgents()
		if len(after) != before+1 || !strings.Contains(after[len(after)-1], want) {
			t.Fatalf("owner-only policy blocked the terminal closed notice: new_messages=%q want=%q", strings.Join(after[before:], "\n"), want)
		}
	})
}

func TestInboxReplyPolicyQueueRechecksAfterBlockingStartPreparationLegacySchema(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*inboxHub, *InboxItem)
	}{
		{name: "content", mutate: func(h *inboxHub, it *InboxItem) {
			h.inbox.mu.Lock()
			defer h.inbox.mu.Unlock()
			it.Last = "restricted-content-after-preparation"
			it.Context = "Synthetic approval request arriving during device preparation"
			it.ReplyCategory, it.ReplyMode = "approval_permission", ReplyRouteOwnerOnly
			it.ReplyClassifiedOf = replyPolicyContentVersion(it)
		}},
		{name: "policy revision", mutate: func(h *inboxHub, _ *InboxItem) {
			h.setConfig(func(c *assistantwire.InboxConfig) {
				cfg := legacyReplyPolicyTestConfig()
				cfg.Revision = "test-v2"
				c.ReplyPolicy = cfg
			})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := agentHub(t)
			h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
			it := actionItem("lark:review9-preparation-race", time.Now())
			it.Source = "lark"
			it.Agent = &InboxAgent{State: AgentQueued}
			it.ReplyCategory, it.ReplyMode = "ordinary_coordination", ReplyRouteOwnerDraft
			it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
			h.inbox.items[it.ID] = it
			var prepared atomic.Bool
			var configReads atomic.Int32
			h.in.configHook = func() {
				if configReads.Add(1) != 2 {
					return
				}
				// The second fresh read is the per-job authorization check. The following devices.list is start preparation.
				h.f.mu.Lock()
				h.f.onDevicesList = func() {
					prepared.Store(true)
					tc.mutate(h, it)
				}
				h.f.mu.Unlock()
			}
			before := len(conversationStarts(h.f))
			h.startInboxAgents(t.Context(), InboxAgentConfig{Enabled: true})
			if !prepared.Load() {
				t.Fatal("queue start did not reach the device-preparation hook")
			}
			if got := len(conversationStarts(h.f)) - before; got != 0 {
				t.Fatalf("queue started %d conversations with authorization revoked during preparation", got)
			}
		})
	}
}

func TestInboxReplyPolicyEditedAgentInstructionRequiresTextReviewLegacySchema(t *testing.T) {
	h := agentHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) {
		c.Agent.Enabled = true
		c.ReplyPolicy = legacyReplyPolicyTestConfig()
	})
	it := disabledTwinItem(h, "lark:review9-edited-agent", HandlingDone, AgentSkipped, "parent-session")
	it.Source = "lark"
	it.Context = "Synthetic original conversation for an edited Agent instruction"
	it.ReplyCategory, it.ReplyMode = "ordinary_coordination", ReplyRouteOwnerDraft
	it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
	g := &InboxSuggestion{Version: 1, Kind: SuggestAgent, Text: "Inspect the original request", At: time.Now(), MachineID: "server", Cwd: "/work"}
	snapshotReplyPolicy(g, it, legacyReplyPolicyTestConfig())
	it.Suggestion = g
	h.originSet(t.Context(), "server", "parent-session", OriginInbox, it.ID, "")
	model, calls := captureModel(t, `{"category":"approval_permission"}`)
	h.in.model = model
	edited := "I approve the synthetic restricted Agent instruction"
	_, err := h.actOnSuggestion(t.Context(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm, Text: &edited}, true)
	h.f.mu.Lock()
	starts := append([]map[string]any(nil), h.f.starts...)
	h.f.mu.Unlock()
	got := calls()
	if !errors.Is(err, errReplyPolicyDenied) || len(starts) != 0 || len(got) != 1 || currentSuggestion(it) != g {
		rawStarts, _ := json.Marshal(starts)
		t.Fatalf("edited Agent instruction bypassed generated-text review: err=%v starts=%s model_calls=%d suggestion=%+v", err, rawStarts, len(got), currentSuggestion(it))
	}
	for _, marker := range []string{edited, it.Context} {
		if !strings.Contains(got[0].User, marker) {
			t.Fatalf("edited Agent classifier input lacks %q: %+v", marker, got[0])
		}
	}
}

func TestInboxReplyPolicyInstructedHandbackRequiresTextReviewLegacySchema(t *testing.T) {
	h, _, it := handledHub(t)
	replyPolicyReview8OrdinaryItemLegacy(h, it)
	original := it.Context
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"Original ordinary draft before instructed handback"}`); err != nil {
		t.Fatal(err)
	}
	instruction := "Synthetic owner rewrite instruction preceding a handback"
	if _, err := h.actOnSuggestion(t.Context(), it.ID, suggestionReq{Version: 1, Action: suggestInstruct, Text: &instruction}, true); err != nil {
		t.Fatal(err)
	}
	toldConv(t, h, instruction)
	model, calls := captureModel(t, `{"category":"approval_permission"}`)
	h.in.model = model
	const restricted = "Restricted generated handback requiring its own review"
	_, err := suggestTool(t, h, `{"kind":"reply","handback":true,"text":"`+restricted+`"}`)
	got := calls()
	if !errors.Is(err, errReplyPolicyDenied) || len(got) != 1 || currentSuggestion(it) != nil {
		t.Fatalf("instructed handback bypassed generated-text review: err=%v model_calls=%d suggestion=%+v", err, len(got), currentSuggestion(it))
	}
	for _, marker := range []string{restricted, instruction, original} {
		if !strings.Contains(got[0].User, marker) {
			t.Fatalf("instructed handback classifier input lacks %q: %+v", marker, got[0])
		}
	}
}

func TestInboxReplyPolicyDraftPolicyExitWithoutItemChangeIsDeniedLegacySchema(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*assistantwire.InboxReplyPolicyConfig)
	}{
		{name: "disabled", change: func(cfg *assistantwire.InboxReplyPolicyConfig) { cfg.Enabled = false }},
		{name: "out of scope", change: func(cfg *assistantwire.InboxReplyPolicyConfig) { cfg.Sources = []string{"mail"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, it := handledHub(t)
			replyPolicyReview8OrdinaryItemLegacy(h, it)
			const generated = "Synthetic generated draft spanning a pure policy exit"
			calls := replyPolicyDraftSequenceModel(t, h, func(call int) {
				if call == 0 {
					h.setConfig(func(c *assistantwire.InboxConfig) { tc.change(c.ReplyPolicy) })
				}
			}, generated)
			text, err := h.draftReply(t.Context(), it.ID, "")
			if !errors.Is(err, errReplyPolicyDenied) || text != "" {
				t.Fatalf("policy exit without an item change returned a transient draft: text=%q err=%v", text, err)
			}
			if got := len(calls()); got != 1 {
				t.Fatalf("policy-exit draft made %d model calls, want one generation", got)
			}
			if g := currentSuggestion(it); g != nil || mirroredLegacyReply(it) != nil {
				t.Fatalf("policy-exit draft published a suggestion or mirror: %+v", g)
			}
		})
	}
}

func TestInboxReplyPolicyAmbiguousClassifierJSONFailsClosedLegacySchema(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
	}{
		{name: "multiple objects", payload: "{\"category\":\"ordinary_coordination\"}\n{\"category\":\"approval_permission\"}"},
		{name: "duplicate category", payload: `{"category":"approval_permission","category":"ordinary_coordination"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := agentHub(t)
			cfg := legacyReplyPolicyTestConfig()
			h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
			it := actionItem("lark:review9-ambiguous-json", time.Now())
			it.Source = "lark"
			it.Agent = &InboxAgent{State: AgentQueued}
			h.inbox.items[it.ID] = it
			h.in.model = fakeModel(t, tc.payload)
			h.classifyInboxReplyPolicies(t.Context(), cfg)
			before := len(conversationStarts(h.f))
			h.startInboxAgents(t.Context(), InboxAgentConfig{Enabled: true})
			starts := len(conversationStarts(h.f)) - before
			if it.ReplyCategory != "unknown" || it.ReplyMode != ReplyRouteOwnerOnly || !replyPolicyClassificationCurrent(it, cfg) || starts != 0 {
				t.Fatalf("ambiguous classifier JSON authorized handling: category=%q mode=%q classified_of=%q revision=%q starts=%d",
					it.ReplyCategory, it.ReplyMode, it.ReplyClassifiedOf, it.ReplyPolicyRev, starts)
			}
		})
	}
}

func TestInboxReplyPolicyConcurrentClassificationCannotOverwriteDenial(t *testing.T) {
	h := agentHub(t)
	cfg := replyPolicyTestConfig()
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
	it := actionItem("lark:review9-concurrent-classification", time.Now())
	it.Source = "lark"
	it.Agent = &InboxAgent{State: AgentQueued}
	h.inbox.items[it.ID] = it

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseFirst) }) }
	defer release()
	var modelCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if modelCalls.Add(1) == 1 {
			close(firstStarted)
			<-releaseFirst
			replyPolicyReview9PolicyModelReply(w, `{"category":"ordinary_coordination"}`)
			return
		}
		replyPolicyReview9PolicyModelReply(w, `{"category":"approval_permission"}`)
	}))
	t.Cleanup(srv.Close)
	h.in.model = modelAt(t, srv.URL)

	firstDone := make(chan error, 1)
	go func() { firstDone <- h.classifyInboxReplyPolicyItem(t.Context(), cfg, it) }()
	select {
	case <-firstStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("first classification did not reach its model call")
	}
	secondDone := make(chan error, 1)
	go func() { secondDone <- h.classifyInboxReplyPolicyItem(t.Context(), cfg, it) }()
	secondEarly := false
	select {
	case err := <-secondDone:
		secondEarly = true
		if err != nil {
			release()
			t.Fatalf("denial classification failed: %v", err)
		}
		if replyPolicyTestCategory(it) != "approval_permission" || replyPolicyTestRoute(it) != ReplyRouteOwnerOnly {
			release()
			t.Fatalf("test did not commit its denial first: %+v", it)
		}
	case <-time.After(500 * time.Millisecond):
		// A per-item serialization fix legitimately makes the second call wait. Release the first rather than rejecting
		// that valid implementation; the final committed denial assertion below is the authorization invariant.
	}

	release()
	if !secondEarly {
		select {
		case err := <-secondDone:
			if err != nil {
				t.Fatalf("serialized denial classification failed: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("denial classification did not finish after releasing the ordinary result")
		}
	}
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("ordinary classification failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ordinary classification did not finish")
	}
	if replyPolicyTestCategory(it) != "approval_permission" || replyPolicyTestRoute(it) != ReplyRouteOwnerOnly {
		t.Fatalf("slower ordinary classification overwrote the committed denial: category=%q mode=%q", replyPolicyTestCategory(it), replyPolicyTestRoute(it))
	}

	h.st.imu.Lock()
	raw := append([]byte(nil), h.st.rows[it.ID]...)
	h.st.imu.Unlock()
	var stored InboxItem
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("decode persisted classification: %v", err)
	}
	if replyPolicyTestCategory(stored) != "approval_permission" || replyPolicyTestRoute(stored) != ReplyRouteOwnerOnly {
		t.Fatalf("slower ordinary classification overwrote the persisted denial: category=%q mode=%q", replyPolicyTestCategory(stored), replyPolicyTestRoute(stored))
	}
}

func TestInboxReplyPolicyFollowMergedIncludesMemberWhenSurvivorAlsoAdvances(t *testing.T) {
	h := agentHub(t)
	cfg := replyPolicyTestConfig()
	cfg.Sources = []string{"synthetic"}
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
	const memberMarker = "Synthetic restricted member update hidden by survivor advancement"
	replyPolicyReview9PolicyMarkerModel(t, h, memberMarker)
	base := time.Now().Add(-time.Hour)
	survivor := &InboxItem{ID: "synthetic:review9-survivor-round", Source: "synthetic", Kind: KindDM,
		Last: "survivor-0", Context: "Original ordinary survivor context", Count: 1, UpdatedAt: base, StartedAt: base,
		Tier: TierAction, TriagedAt: "survivor-0", Connector: &InboxConnector{Thread: "survivor-round"},
		MergedFrom: []InboxMergeRef{{ID: "synthetic:review9-member-round", Source: "synthetic"}}}
	member := &InboxItem{ID: "synthetic:review9-member-round", Source: "synthetic", Kind: KindDM,
		Last: "member-0", Context: "Original member context", Count: 1, UpdatedAt: base, StartedAt: base,
		MergedInto: survivor.ID, Connector: &InboxConnector{Thread: "member-round"}}

	h.inbox.mu.Lock()
	h.inbox.items[survivor.ID], h.inbox.items[member.ID] = survivor, member
	changed := h.inbox.applyConnector("synthetic", "Synthetic", []connector.InboxThing{
		{Thread: "survivor-round", Kind: KindDM, Last: "survivor-1", At: base.Add(2 * time.Minute), Context: "Ordinary survivor update at the later timestamp"},
		{Thread: "member-round", Kind: KindDM, Last: "member-1", At: base.Add(time.Minute), Context: memberMarker},
	}, time.Now())
	h.inbox.followMerged(changed, time.Now())
	h.inbox.mu.Unlock()
	h.classifyInboxReplyPolicies(t.Context(), cfg)

	if !strings.Contains(survivor.Context, memberMarker) || replyPolicyTestCategory(survivor) != "approval_permission" || replyPolicyTestRoute(survivor) != ReplyRouteOwnerOnly {
		t.Fatalf("same-round survivor advancement dropped member authorization evidence: context=%q category=%q mode=%q", survivor.Context, replyPolicyTestCategory(survivor), replyPolicyTestRoute(survivor))
	}
}

func TestInboxReplyPolicyConnectorRefreshRetainsMergedMemberEvidence(t *testing.T) {
	h := agentHub(t)
	cfg := replyPolicyTestConfig()
	cfg.Sources = []string{"synthetic"}
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
	const memberMarker = "Synthetic restricted member evidence erased by connector refresh"
	replyPolicyReview9PolicyMarkerModel(t, h, memberMarker)
	base := time.Now().Add(-time.Hour)
	survivor := &InboxItem{ID: "synthetic:review9-survivor-refresh", Source: "synthetic", Kind: KindDM,
		Last: "survivor-0", Context: "Original ordinary survivor context\n\n" + memberMarker, Count: 2, UpdatedAt: base, StartedAt: base,
		Tier: TierAction, TriagedAt: "survivor-0", Connector: &InboxConnector{Thread: "survivor-refresh"},
		MergedFrom: []InboxMergeRef{{ID: "synthetic:review9-member-refresh", Source: "synthetic"}}, ReplyContentSources: []string{"synthetic"}}
	member := &InboxItem{ID: "synthetic:review9-member-refresh", Source: "synthetic", Kind: KindDM,
		Last: "member-0", Context: memberMarker, Count: 1, UpdatedAt: base, StartedAt: base,
		MergedInto: survivor.ID, Connector: &InboxConnector{Thread: "member-refresh"}}

	h.inbox.mu.Lock()
	h.inbox.items[survivor.ID], h.inbox.items[member.ID] = survivor, member
	changed := h.inbox.applyConnector("synthetic", "Synthetic", []connector.InboxThing{
		{Thread: "survivor-refresh", Kind: KindDM, Last: "survivor-1", At: base.Add(time.Minute), Context: "New ordinary survivor-only connector context"},
	}, time.Now())
	h.inbox.followMerged(changed, time.Now())
	h.inbox.mu.Unlock()
	h.classifyInboxReplyPolicies(t.Context(), cfg)

	if !strings.Contains(survivor.Context, memberMarker) || replyPolicyTestCategory(survivor) != "approval_permission" || replyPolicyTestRoute(survivor) != ReplyRouteOwnerOnly {
		t.Fatalf("survivor refresh erased retained member authorization evidence: context=%q category=%q mode=%q", survivor.Context, replyPolicyTestCategory(survivor), replyPolicyTestRoute(survivor))
	}
}

// replyPolicyReview9PolicyMergedInstruction creates an outstanding owner instruction on the handled survivor and then
// merges a member whose entries are newer than that instruction. The member row itself is older, so the handled row survives.

func replyPolicyReview9PolicyMergedInstruction(t *testing.T, memberChain func(time.Time) []InboxChainEntry) (*inboxHub, *InboxItem, string) {
	t.Helper()
	h, _, it := handledHub(t)
	replyPolicyReview8OrdinaryItem(h, it)
	if _, err := suggestOrdinaryReplyForFixture(t, h, `{"kind":"reply","text":"Original ordinary draft before merge"}`); err != nil {
		t.Fatal(err)
	}
	instruction := "Synthetic owner rewrite instruction that must survive merge provenance"
	if _, err := h.actOnSuggestion(t.Context(), it.ID, suggestionReq{Version: 1, Action: suggestInstruct, Text: &instruction}, true); err != nil {
		t.Fatal(err)
	}
	toldConv(t, h, instruction)
	h.inbox.mu.Lock()
	instructionAt := it.Chain[len(it.Chain)-1].At
	h.inbox.mu.Unlock()

	member := actionItem("lark:review9-merge-member", it.UpdatedAt.Add(-time.Second))
	member.Source = "lark"
	member.StartedAt = member.UpdatedAt
	member.Count = 1
	member.Context = "Synthetic merged member conversation"
	member.Chain = memberChain(instructionAt.Add(time.Second))
	member.ChainSeq = len(member.Chain)
	h.inbox.mu.Lock()
	h.inbox.items[member.ID] = member
	survivor, _ := h.inbox.merge([]*InboxItem{it, member}, time.Now())
	h.inbox.mu.Unlock()
	if survivor != it {
		t.Fatalf("merge survivor = %p, want handled row %p", survivor, it)
	}
	return h, it, instruction
}

func replyPolicyReview9PolicyCheckMergedReplacement(t *testing.T, h *inboxHub, it *InboxItem, instruction, replacement string) {
	t.Helper()
	calls := replyPolicyDraftSequenceModel(t, h, nil, `{"category":"ordinary_coordination"}`, `{"category":"approval_permission"}`)
	cfg := replyPolicyTestConfig()
	h.classifyInboxReplyPolicies(t.Context(), cfg)
	if replyPolicyTestCategory(it) != "ordinary_coordination" || replyPolicyTestRoute(it) != ReplyRouteOwnerDraft {
		t.Fatalf("test did not classify the merged item as an ordinary owner draft: %+v", it)
	}
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"`+replacement+`"}`); !replyPolicyDenied(err) {
		t.Fatalf("merged history authorized an unreviewed restricted replacement: err=%v calls=%d suggestion=%+v", err, len(calls()), currentSuggestion(it))
	}
	if g := currentSuggestion(it); g != nil {
		t.Fatalf("restricted replacement after merge was published: %+v", g)
	}
	got := calls()
	if len(got) != 2 {
		t.Fatalf("merged replacement made %d model calls, want item plus generated-text classification", len(got))
	}
	for _, marker := range []string{replacement, instruction} {
		if !strings.Contains(got[1].User, marker) {
			t.Fatalf("generated replacement classifier input lacks %q: %+v", marker, got[1])
		}
	}
}

func TestInboxReplyPolicyIncompleteMailFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name        string
		clippedBody bool
	}{
		{name: "snippet"},
		{name: "clipped body", clippedBody: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := agentHub(t)
			cfg := replyPolicyTestConfig()
			cfg.Sources = []string{"mail"}
			h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
			model, calls := captureModel(t, `{"category":"ordinary_coordination"}`)
			h.in.model = model
			now := time.Now().UTC()
			account := assistantwire.InboxMailAccount{ID: "review9-mail", Email: "me@example.com"}
			summary := assistantwire.InboxMailSummary{Mailbox: "INBOX", UID: 9, Subject: "Synthetic incomplete authorization input",
				From: MailAddress{Name: "Sender", Email: "sender@example.com"}, To: []MailAddress{{Email: "me@example.com"}},
				Date: now.Add(-time.Hour).Format(time.RFC3339), Snippet: "Ordinary coordination visible in the snippet"}
			const restrictedSuffix = "Synthetic restricted approval request beyond the retained body prefix"
			h.in.mail = &fakeMail{bodies: map[uint32]assistantwire.InboxMailMessage{}}
			if tc.clippedBody {
				h.in.mail.bodies[9] = assistantwire.InboxMailMessage{From: summary.From, To: summary.To, Subject: summary.Subject,
					Text: strings.Repeat("ordinary coordination\n", 200) + restrictedSuffix}
			}
			items := h.inbox.applyMail(account, []assistantwire.InboxMailSummary{summary}, now)
			if len(items) != 1 {
				t.Fatalf("mail fixture produced %d items, want one", len(items))
			}
			it := items[0]
			h.inbox.mu.Lock()
			it.Tier, it.TriagedAt = TierAction, it.Last
			h.inbox.mu.Unlock()
			if tc.clippedBody {
				if !h.readMailBody(t.Context(), account, it) {
					t.Fatal("clipped-body fixture could not read its mail body")
				}
				if it.BodyOf != it.Last || strings.Contains(it.Context, restrictedSuffix) {
					t.Fatalf("test did not create a clipped current body: body_of=%q last=%q contains_suffix=%v", it.BodyOf, it.Last, strings.Contains(it.Context, restrictedSuffix))
				}
			} else if it.BodyOf == it.Last {
				t.Fatalf("snippet fixture unexpectedly has a current full body: body_of=%q last=%q", it.BodyOf, it.Last)
			}

			h.classifyInboxReplyPolicies(t.Context(), cfg)
			if replyPolicyTestCategory(it) != "unknown" || replyPolicyTestRoute(it) != ReplyRouteOwnerOnly || !replyPolicyClassificationCurrent(it, cfg) || len(calls()) != 0 {
				t.Fatalf("incomplete mail authorized classification from a partial input: body_of=%q last=%q category=%q mode=%q truncated=%v model_calls=%d",
					it.BodyOf, it.Last, replyPolicyTestCategory(it), replyPolicyTestRoute(it), it.ReplyContextTruncated, len(calls()))
			}
		})
	}
}

func TestInboxReplyPolicyDeliverNotesGatesWorkButAllowsClosedNotice(t *testing.T) {
	t.Run("work", func(t *testing.T) {
		h, _, it := handledHub(t)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
		const restrictedWork = "Synthetic owner-only approval work delivered to an existing session"
		h.inbox.mu.Lock()
		it.Context = restrictedWork
		{
			replyPolicyTestSetCategory(it, "approval_permission")
			replyPolicyTestSetRoute(it, ReplyRouteOwnerOnly)
		}
		{
			replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
			replyPolicyTestSetPolicyRevision(it, "test-v1")
		}
		it.Agent.Pending = nil
		it.Agent.queueNote(inboxNote{Name: prompts.NoteNewMessages, Context: restrictedWork})
		it.Agent.Wake = true
		oldTurn := it.Agent.ProcessingTurn
		h.inbox.mu.Unlock()
		before := len(h.f.sentToAgents())
		h.deliverNotes(t.Context(), it)
		after := h.f.sentToAgents()
		if len(after) != before || strings.Contains(strings.Join(after[before:], "\n"), restrictedWork) || it.Agent.ProcessingTurn != oldTurn {
			t.Fatalf("owner-only work reached the existing processing session: new_messages=%q processing_turn %q -> %q", strings.Join(after[before:], "\n"), oldTurn, it.Agent.ProcessingTurn)
		}
	})

	t.Run("closed notice", func(t *testing.T) {
		h, _, it := handledHub(t)
		h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
		{
			replyPolicyTestSetCategory(it, "approval_permission")
			replyPolicyTestSetRoute(it, ReplyRouteOwnerOnly)
		}
		{
			replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
			replyPolicyTestSetPolicyRevision(it, "test-v1")
		}
		h.inbox.mu.Lock()
		queued := h.inbox.closeHandling(it, "user", time.Now())
		h.inbox.file(it, time.Now(), "user")
		h.inbox.mu.Unlock()
		if !queued || it.open() {
			t.Fatalf("terminal-notice fixture did not close an active handling session: queued=%v open=%v", queued, it.open())
		}
		hello := h.hello(t.Context())
		want := renderNoteIn(inboxNote{Name: prompts.NoteClosed, Text: "user"}, inboxPromptOwner(hello), answerLanguage(hello.Locale))
		before := len(h.f.sentToAgents())
		h.deliverNotes(t.Context(), it)
		after := h.f.sentToAgents()
		if len(after) != before+1 || !strings.Contains(after[len(after)-1], want) {
			t.Fatalf("owner-only policy blocked the terminal closed notice: new_messages=%q want=%q", strings.Join(after[before:], "\n"), want)
		}
	})
}

func TestInboxReplyPolicyQueueRechecksAfterBlockingStartPreparation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*inboxHub, *InboxItem)
	}{
		{name: "content", mutate: func(h *inboxHub, it *InboxItem) {
			h.inbox.mu.Lock()
			defer h.inbox.mu.Unlock()
			it.Last = "restricted-content-after-preparation"
			it.Context = "Synthetic approval request arriving during device preparation"
			{
				replyPolicyTestSetCategory(it, "approval_permission")
				replyPolicyTestSetRoute(it, ReplyRouteOwnerOnly)
			}
			{
				replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
			}
		}},
		{name: "policy revision", mutate: func(h *inboxHub, _ *InboxItem) {
			h.setConfig(func(c *assistantwire.InboxConfig) {
				cfg := replyPolicyTestConfig()
				cfg.Revision = "test-v2"
				c.ReplyPolicy = cfg
			})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := agentHub(t)
			h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
			it := actionItem("lark:review9-preparation-race", time.Now())
			it.Source = "lark"
			it.Agent = &InboxAgent{State: AgentQueued}
			{
				replyPolicyTestSetCategory(it, "ordinary_coordination")
				replyPolicyTestSetRoute(it, ReplyRouteOwnerDraft)
			}
			{
				replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
				replyPolicyTestSetPolicyRevision(it, "test-v1")
			}
			h.inbox.items[it.ID] = it
			var prepared atomic.Bool
			var configReads atomic.Int32
			h.in.configHook = func() {
				if configReads.Add(1) != 2 {
					return
				}
				// The second fresh read is the per-job authorization check. The following devices.list is start preparation.
				h.f.mu.Lock()
				h.f.onDevicesList = func() {
					prepared.Store(true)
					tc.mutate(h, it)
				}
				h.f.mu.Unlock()
			}
			before := len(conversationStarts(h.f))
			h.startInboxAgents(t.Context(), InboxAgentConfig{Enabled: true})
			if !prepared.Load() {
				t.Fatal("queue start did not reach the device-preparation hook")
			}
			if got := len(conversationStarts(h.f)) - before; got != 0 {
				t.Fatalf("queue started %d conversations with authorization revoked during preparation", got)
			}
		})
	}
}

func TestInboxReplyPolicyEditedAgentInstructionRequiresTextReview(t *testing.T) {
	h := agentHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) {
		c.Agent.Enabled = true
		c.ReplyPolicy = replyPolicyTestConfig()
	})
	it := disabledTwinItem(h, "lark:review9-edited-agent", HandlingDone, AgentSkipped, "parent-session")
	it.Source = "lark"
	it.Context = "Synthetic original conversation for an edited Agent instruction"
	{
		replyPolicyTestSetCategory(it, "ordinary_coordination")
		replyPolicyTestSetRoute(it, ReplyRouteOwnerDraft)
	}
	{
		replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
		replyPolicyTestSetPolicyRevision(it, "test-v1")
	}
	g := &InboxSuggestion{Version: 1, Kind: SuggestAgent, Text: "Inspect the original request", At: time.Now(), MachineID: "server", Cwd: "/work"}
	snapshotReplyPolicy(g, it, replyPolicyTestConfig())
	it.Suggestion = g
	h.originSet(t.Context(), "server", "parent-session", OriginInbox, it.ID, "")
	model, calls := captureModel(t, `{"category":"approval_permission"}`)
	h.in.model = model
	edited := "I approve the synthetic restricted Agent instruction"
	_, err := h.actOnSuggestion(t.Context(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm, Text: &edited}, true)
	h.f.mu.Lock()
	starts := append([]map[string]any(nil), h.f.starts...)
	h.f.mu.Unlock()
	got := calls()
	if !replyPolicyDenied(err) || len(starts) != 0 || len(got) != 1 || currentSuggestion(it) != g {
		rawStarts, _ := json.Marshal(starts)
		t.Fatalf("edited Agent instruction bypassed generated-text review: err=%v starts=%s model_calls=%d suggestion=%+v", err, rawStarts, len(got), currentSuggestion(it))
	}
	for _, marker := range []string{edited, it.Context} {
		if !strings.Contains(got[0].User, marker) {
			t.Fatalf("edited Agent classifier input lacks %q: %+v", marker, got[0])
		}
	}
}

func TestInboxReplyPolicyInstructedHandbackRequiresTextReview(t *testing.T) {
	h, _, it := handledHub(t)
	replyPolicyReview8OrdinaryItem(h, it)
	original := it.Context
	if _, err := suggestOrdinaryReplyForFixture(t, h, `{"kind":"reply","text":"Original ordinary draft before instructed handback"}`); err != nil {
		t.Fatal(err)
	}
	instruction := "Synthetic owner rewrite instruction preceding a handback"
	if _, err := h.actOnSuggestion(t.Context(), it.ID, suggestionReq{Version: 1, Action: suggestInstruct, Text: &instruction}, true); err != nil {
		t.Fatal(err)
	}
	toldConv(t, h, instruction)
	model, calls := captureModel(t, `{"category":"approval_permission"}`)
	h.in.model = model
	const restricted = "Restricted generated handback requiring its own review"
	_, err := suggestTool(t, h, `{"kind":"reply","handback":true,"text":"`+restricted+`"}`)
	got := calls()
	if !replyPolicyDenied(err) || len(got) != 1 || currentSuggestion(it) != nil {
		t.Fatalf("instructed handback bypassed generated-text review: err=%v model_calls=%d suggestion=%+v", err, len(got), currentSuggestion(it))
	}
	for _, marker := range []string{restricted, instruction, original} {
		if !strings.Contains(got[0].User, marker) {
			t.Fatalf("instructed handback classifier input lacks %q: %+v", marker, got[0])
		}
	}
}

func TestInboxReplyPolicyDraftPolicyExitWithoutItemChangeIsDenied(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*assistantwire.InboxReplyPolicyConfig)
	}{
		{name: "disabled", change: func(cfg *assistantwire.InboxReplyPolicyConfig) { cfg.Enabled = false }},
		{name: "out of scope", change: func(cfg *assistantwire.InboxReplyPolicyConfig) { cfg.Sources = []string{"mail"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, it := handledHub(t)
			replyPolicyReview8OrdinaryItem(h, it)
			const generated = "Synthetic generated draft spanning a pure policy exit"
			calls := replyPolicyDraftSequenceModel(t, h, func(call int) {
				if call == 1 {
					h.setConfig(func(c *assistantwire.InboxConfig) { tc.change(c.ReplyPolicy) })
				}
			}, `{"category":"ordinary_coordination"}`, generated, `{"category":"ordinary_coordination"}`)
			text, err := h.draftReply(t.Context(), it.ID, "")
			if !replyPolicyDenied(err) || text != "" {
				t.Fatalf("policy exit without an item change returned a transient draft: text=%q err=%v", text, err)
			}
			if got := len(calls()); got != 2 {
				t.Fatalf("policy-exit draft made %d model calls, want initial classification and one generation", got)
			}
			if g := currentSuggestion(it); g != nil || mirroredLegacyReply(it) != nil {
				t.Fatalf("policy-exit draft published a suggestion or mirror: %+v", g)
			}
		})
	}
}

func TestInboxReplyPolicyAmbiguousClassifierJSONFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
	}{
		{name: "multiple objects", payload: "{\"category\":\"ordinary_coordination\"}\n{\"category\":\"approval_permission\"}"},
		{name: "duplicate category", payload: `{"category":"approval_permission","category":"ordinary_coordination"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := agentHub(t)
			cfg := replyPolicyTestConfig()
			h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
			it := actionItem("lark:review9-ambiguous-json", time.Now())
			it.Source = "lark"
			it.Agent = &InboxAgent{State: AgentQueued}
			h.inbox.items[it.ID] = it
			h.in.model = fakeModel(t, tc.payload)
			h.classifyInboxReplyPolicies(t.Context(), cfg)
			before := len(conversationStarts(h.f))
			h.startInboxAgents(t.Context(), InboxAgentConfig{Enabled: true})
			starts := len(conversationStarts(h.f)) - before
			if replyPolicyTestCategory(it) != "unknown" || replyPolicyTestRoute(it) != ReplyRouteOwnerOnly || !replyPolicyClassificationCurrent(it, cfg) || starts != 0 {
				t.Fatalf("ambiguous classifier JSON authorized handling: category=%q mode=%q classified_of=%q revision=%q starts=%d", replyPolicyTestCategory(it), replyPolicyTestRoute(it), replyPolicyTestContentVersion(it), replyPolicyTestPolicyRevision(it), starts)
			}
		})
	}
}
