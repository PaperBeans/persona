package assistant

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/rpc"
)

func TestInboxReplyPolicyCheckedMirrorHonorsExpectedMailTargetLegacySchema(t *testing.T) {
	for _, mode := range []string{"uid", "version", "matching"} {
		t.Run(mode, func(t *testing.T) {
			h, _, it := handledHub(t)
			mail := &fakeMail{}
			h.in.mail = mail
			h.inbox.mu.Lock()
			it.Source, it.Account, it.Lark = "mail", "synthetic-account", nil
			it.Mail = &InboxMail{AccountID: "synthetic-account", Mailbox: "INBOX", UIDs: []uint32{1}, Thread: "synthetic-thread"}
			h.inbox.mu.Unlock()
			h.setConfig(func(c *assistantwire.InboxConfig) {
				c.ReplyPolicy = legacyReplyPolicyTestConfig()
				c.ReplyPolicy.Sources = []string{"mail"}
			})
			it.ReplyCategory, it.ReplyMode = "ordinary_coordination", "owner_draft"
			it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
			const text = "Same synthetic reply on a replacement card"
			if _, err := suggestTool(t, h, `{"kind":"reply","text":"`+text+`"}`); err != nil {
				t.Fatal(err)
			}
			oldTarget := *mailTarget(*it)
			h.inbox.mu.Lock()
			it.Mail.UIDs = []uint32{2}
			it.ReplyClassifiedOf = replyPolicyContentVersion(it)
			h.inbox.mu.Unlock()
			if _, err := suggestTool(t, h, `{"kind":"reply","text":"`+text+`"}`); err != nil {
				t.Fatal(err)
			}
			g := mirroredLegacyReply(it)
			if g == nil || g.Version != 2 || g.MailTarget == nil || g.MailTarget.UID != 2 {
				t.Fatalf("test did not create a same-text replacement for the new mail target: %+v", g)
			}
			var uid *uint32
			var version *string
			switch mode {
			case "uid":
				uid = &oldTarget.UID
			case "version":
				version = &oldTarget.Version
			case "matching":
				uid, version = &g.MailTarget.UID, &g.MailTarget.Version
			}
			_, err := h.replyInboxChecked(t.Context(), it.ID, text, "synthetic-checked-"+mode, false, uid, version)
			if mode == "matching" {
				if err != nil || len(mail.sent) != 1 || mail.sent[0]["uid"] != float64(2) {
					t.Fatalf("matching checked mirror reply failed: err=%v sends=%+v", err, mail.sent)
				}
				return
			}
			if !errors.Is(err, errMailReplyTargetChanged) {
				t.Errorf("old card's expected %s was ignored by the mirror reroute: err=%v", mode, err)
			}
			if len(mail.sent) != 0 {
				t.Fatalf("old card's expected %s authorized a reply to replacement UID 2: %+v", mode, mail.sent)
			}
		})
	}
}

func TestInboxReplyPolicyLegacyDirectCannotRepeatUncertainAttemptLegacySchema(t *testing.T) {
	for _, mode := range []string{"policy disabled", "mirror removed"} {
		t.Run(mode, func(t *testing.T) {
			c := &recoveryReplier{err: rpc.ErrDropped}
			h, _, it := handledHub(t)
			h.in.mu.Lock()
			h.in.conns[0] = c
			h.in.mu.Unlock()
			h.setConfig(func(cfg *assistantwire.InboxConfig) { cfg.ReplyPolicy = legacyReplyPolicyTestConfig() })
			it.ReplyCategory, it.ReplyMode = "ordinary_coordination", "owner_draft"
			it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
			if _, err := suggestTool(t, h, `{"kind":"reply","text":"Uncertain synthetic legacy reply"}`); err != nil {
				t.Fatal(err)
			}
			text := currentSuggestion(it).Text
			if _, err := h.replyInboxChecked(t.Context(), it.ID, text, recoveryFirstID, false, nil, nil); !replyInDoubt(err) {
				t.Fatalf("first mirrored send error = %v, want an uncertain outcome", err)
			}
			if got := len(c.calls()); got != 1 {
				t.Fatalf("test setup made %d physical sends, want 1", got)
			}
			c.answer(nil)
			switch mode {
			case "policy disabled":
				h.setConfig(func(cfg *assistantwire.InboxConfig) { cfg.ReplyPolicy = nil })
			case "mirror removed":
				h.inbox.mu.Lock()
				it.Suggestion = nil
				it.Agent.State, it.Agent.Draft, it.Agent.Note = AgentSkipped, "", ""
				h.inbox.mu.Unlock()
			}
			_, secondErr := h.replyInboxChecked(t.Context(), it.ID, text, recoveryFreshID, false, nil, nil)
			if !replyInDoubt(secondErr) {
				t.Errorf("%s allowed a fresh-ID legacy retry to escape the unresolved attempt: %v", mode, secondErr)
			}
			if got := len(c.calls()); got != 1 {
				t.Fatalf("%s repeated an uncertain legacy physical reply: calls=%d second error=%v", mode, got, secondErr)
			}
		})
	}
}

func TestInboxReplyPolicyInstructionSurvivesReportedStepLegacySchema(t *testing.T) {
	h, lark, it := handledHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
	it.Context = "Synthetic original conversation before an intervening reported step"
	it.ReplyCategory, it.ReplyMode = "ordinary_coordination", "owner_draft"
	it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"Original ordinary draft"}`); err != nil {
		t.Fatal(err)
	}
	instruction := "Synthetic owner rewrite instruction before a reported step"
	if _, err := h.actOnSuggestion(t.Context(), it.ID, suggestionReq{Version: 1, Action: suggestInstruct, Text: &instruction}, true); err != nil {
		t.Fatal(err)
	}
	toldConv(t, h, instruction)
	if _, err := suggestTool(t, h, `{"reported":{"text":"Synthetic non-reply progress entry"}}`); err != nil {
		t.Fatal(err)
	}
	if got := lastChain(h, it).Kind; got != ChainReported {
		t.Fatalf("test did not place a reported step after the instruction: %q", got)
	}
	generated := "Synthetic approval generated after the reported step"
	calls := replyPolicyDraftSequenceModel(t, h, nil, `{"category":"approval_permission"}`)
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"`+generated+`"}`); !errors.Is(err, errReplyPolicyDenied) {
		t.Errorf("intervening reported step skipped generated-text classification: %v", err)
	}
	got := calls()
	if len(got) != 1 || got[0].System != prompts.RenderJSONOnly(prompts.RenderInboxReplyPolicy()) {
		t.Fatalf("reported step lost the instructed replacement classification: %+v", got)
	}
	for _, marker := range []string{generated, instruction, "Synthetic original conversation"} {
		if !strings.Contains(got[0].User, marker) {
			t.Fatalf("classifier after reported step lacks %q: %+v", marker, got[0])
		}
	}
	if currentSuggestion(it) != nil || len(lark.got) != 0 {
		t.Fatalf("restricted instructed replacement escaped after a reported step: suggestion=%+v sends=%+v", currentSuggestion(it), lark.got)
	}
}

func TestInboxReplyPolicyDisabledDraftIgnoresUnrelatedItemUpdate(t *testing.T) {
	h, _, it := handledHub(t)
	var changed atomic.Bool
	const generated = "Legacy transient draft despite an unrelated triage update"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if changed.CompareAndSwap(false, true) {
			h.inbox.mu.Lock()
			it.Title = "Synthetic unrelated triage title update"
			h.inbox.touched(it.ID)
			h.inbox.mu.Unlock()
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"content": []any{map[string]any{"type": "text", "text": generated}}})
	}))
	t.Cleanup(srv.Close)
	h.in.model = modelAt(t, srv.URL)
	text, err := h.draftReply(t.Context(), it.ID, "")
	if err != nil || text != generated {
		t.Fatalf("policy-disabled transient draft failed on an unrelated item update: text=%q err=%v", text, err)
	}
	if !changed.Load() {
		t.Fatal("test model did not apply the unrelated item update")
	}
	if currentSuggestion(it) != nil || mirroredLegacyReply(it) != nil {
		t.Fatalf("policy-disabled transient draft became a persisted suggestion: %+v", currentSuggestion(it))
	}
}

func TestInboxReplyPolicyWorkbenchVersionedMirrorSendLegacySchema(t *testing.T) {
	for _, tc := range []struct {
		name            string
		expectedVersion int
	}{
		{name: "matching", expectedVersion: 1},
		{name: "stale", expectedVersion: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, lark, it := handledHub(t)
			h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
			it.ReplyCategory, it.ReplyMode = "ordinary_coordination", "owner_draft"
			it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
			if _, err := suggestTool(t, h, `{"kind":"reply","text":"Versioned synthetic Workbench mirror"}`); err != nil {
				t.Fatal(err)
			}
			text := currentSuggestion(it).Text
			_, err := h.replyInboxFromVersion(t.Context(), it.ID, text, "feishu-workbench-synthetic-"+tc.name, true, tc.expectedVersion)
			if tc.expectedVersion == 1 {
				if err != nil || len(lark.got) != 1 {
					t.Fatalf("matching-version Workbench mirror failed: err=%v sends=%+v", err, lark.got)
				}
				return
			}
			if !errors.Is(err, errSuggestionStale) {
				t.Errorf("stale Workbench version error = %v, want suggestion stale", err)
			}
			if len(lark.got) != 0 {
				t.Fatalf("stale Workbench version sent the replacement mirror: %+v", lark.got)
			}
		})
	}
}

func TestInboxReplyPolicyFollowMergedTruncationInvalidatesDraftLegacySchema(t *testing.T) {
	h, lark, it := handledHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
	it.ReplyCategory, it.ReplyMode = "ordinary_coordination", "owner_draft"
	it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"Draft before a truncated merged-member update"}`); err != nil {
		t.Fatal(err)
	}
	member := &InboxItem{ID: "lark:synthetic-truncated-member", Source: "lark", MergedInto: it.ID,
		UpdatedAt: it.UpdatedAt.Add(time.Second), Context: strings.Repeat("R", mergeCtxMax)}
	h.inbox.mu.Lock()
	it.MergedFrom = append(it.MergedFrom, InboxMergeRef{ID: member.ID, Source: member.Source})
	h.inbox.items[member.ID] = member
	h.inbox.followMerged([]*InboxItem{member}, time.Now())
	h.inbox.mu.Unlock()
	if !it.ReplyContextTruncated {
		t.Fatal("followMerged discarded the member update's truncation boundary")
	}
	if it.ReplyCategory != "" || it.ReplyMode != "" || it.ReplyClassifiedOf != "" || it.ReplyPolicyRev != "" {
		t.Fatalf("followMerged retained the pre-update authorization snapshot: %+v", it)
	}
	model, calls := captureModel(t, `{"category":"ordinary_coordination"}`)
	h.in.model = model
	h.classifyInboxReplyPolicies(t.Context(), legacyReplyPolicyTestConfig())
	if got := len(calls()); got != 0 {
		t.Fatalf("truncated followMerged context made %d model calls, want fail-closed classification without one", got)
	}
	if it.ReplyCategory != "unknown" || it.ReplyMode != "owner_only" || !replyPolicyClassificationCurrent(it, legacyReplyPolicyTestConfig()) {
		t.Fatalf("truncated followMerged context was not classified owner-only: %+v", it)
	}
	restarted := New(Options{Hub: h.f, Store: func() Store { return h.st }, Logf: testLogf(t)})
	if err := restarted.inboxReady(t.Context()); err != nil {
		t.Fatal(err)
	}
	restarted.inbox.mu.Lock()
	loaded := restarted.inbox.items[it.ID]
	restarted.inbox.mu.Unlock()
	if loaded == nil || loaded.ReplyCategory != "unknown" || loaded.ReplyMode != "owner_only" || !loaded.ReplyContextTruncated {
		t.Fatalf("fail-closed followMerged classification was not persisted: %+v", loaded)
	}
	if _, err := h.actOnSuggestion(t.Context(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm}, true); err == nil {
		t.Error("the pre-merge owner draft remained sendable after truncated followMerged input")
	}
	if len(lark.got) != 0 {
		t.Fatalf("truncated followMerged input allowed the old draft to send: %+v", lark.got)
	}
}

func TestInboxReplyPolicyCheckedMirrorHonorsExpectedMailTarget(t *testing.T) {
	for _, mode := range []string{"uid", "version", "matching"} {
		t.Run(mode, func(t *testing.T) {
			h, _, it := handledHub(t)
			mail := &fakeMail{}
			h.in.mail = mail
			h.inbox.mu.Lock()
			it.Source, it.Account, it.Lark = "mail", "synthetic-account", nil
			it.Mail = &InboxMail{AccountID: "synthetic-account", Mailbox: "INBOX", UIDs: []uint32{1}, Thread: "synthetic-thread"}
			it.BodyOf = it.Last
			h.inbox.mu.Unlock()
			h.setConfig(func(c *assistantwire.InboxConfig) {
				c.ReplyPolicy = replyPolicyTestConfig()
				c.ReplyPolicy.Sources = []string{"mail"}
			})
			{
				replyPolicyTestSetCategory(it, "ordinary_coordination")
				replyPolicyTestSetRoute(it, "owner_draft")
			}
			{
				replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
				replyPolicyTestSetPolicyRevision(it, "test-v1")
			}
			const text = "Same synthetic reply on a replacement card"
			if _, err := suggestOrdinaryReplyForFixture(t, h, `{"kind":"reply","text":"`+text+`"}`); err != nil {
				t.Fatal(err)
			}
			oldTarget := *mailTarget(*it)
			h.inbox.mu.Lock()
			it.Mail.UIDs = []uint32{2}
			{
				replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
			}
			h.inbox.mu.Unlock()
			if _, err := suggestOrdinaryReplyForFixture(t, h, `{"kind":"reply","text":"`+text+`"}`); err != nil {
				t.Fatal(err)
			}
			g := mirroredLegacyReply(it)
			if g == nil || g.Version != 2 || g.MailTarget == nil || g.MailTarget.UID != 2 {
				t.Fatalf("test did not create a same-text replacement for the new mail target: %+v", g)
			}
			h.in.setModel(fakeModel(t, `{"category":"ordinary_coordination"}`))
			var uid *uint32
			var version *string
			switch mode {
			case "uid":
				uid = &oldTarget.UID
			case "version":
				version = &oldTarget.Version
			case "matching":
				uid, version = &g.MailTarget.UID, &g.MailTarget.Version
			}
			_, err := h.replyInboxChecked(t.Context(), it.ID, text, "synthetic-checked-"+mode, false, uid, version)
			if mode == "matching" {
				if err != nil || len(mail.sent) != 1 || mail.sent[0]["uid"] != float64(2) {
					t.Fatalf("matching checked mirror reply failed: err=%v sends=%+v", err, mail.sent)
				}
				return
			}
			if !errors.Is(err, errMailReplyTargetChanged) {
				t.Errorf("old card's expected %s was ignored by the mirror reroute: err=%v", mode, err)
			}
			if len(mail.sent) != 0 {
				t.Fatalf("old card's expected %s authorized a reply to replacement UID 2: %+v", mode, mail.sent)
			}
		})
	}
}

func TestInboxReplyPolicyLegacyDirectCannotRepeatUncertainAttempt(t *testing.T) {
	for _, mode := range []string{"policy disabled", "mirror removed"} {
		t.Run(mode, func(t *testing.T) {
			c := &recoveryReplier{err: rpc.ErrDropped}
			h, _, it := handledHub(t)
			h.in.mu.Lock()
			h.in.conns[0] = c
			h.in.mu.Unlock()
			h.setConfig(func(cfg *assistantwire.InboxConfig) { cfg.ReplyPolicy = replyPolicyTestConfig() })
			{
				replyPolicyTestSetCategory(it, "ordinary_coordination")
				replyPolicyTestSetRoute(it, "owner_draft")
			}
			{
				replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
				replyPolicyTestSetPolicyRevision(it, "test-v1")
			}
			if _, err := suggestOrdinaryReplyForFixture(t, h, `{"kind":"reply","text":"Uncertain synthetic legacy reply"}`); err != nil {
				t.Fatal(err)
			}
			h.in.setModel(fakeModel(t, `{"category":"ordinary_coordination"}`))
			text := currentSuggestion(it).Text
			if _, err := h.replyInboxChecked(t.Context(), it.ID, text, recoveryFirstID, false, nil, nil); !replyInDoubt(err) {
				t.Fatalf("first mirrored send error = %v, want an uncertain outcome", err)
			}
			if got := len(c.calls()); got != 1 {
				t.Fatalf("test setup made %d physical sends, want 1", got)
			}
			c.answer(nil)
			switch mode {
			case "policy disabled":
				h.setConfig(func(cfg *assistantwire.InboxConfig) { cfg.ReplyPolicy = &assistantwire.InboxReplyPolicyConfig{} })
			case "mirror removed":
				h.inbox.mu.Lock()
				it.Suggestion = nil
				it.Agent.State, it.Agent.Draft, it.Agent.Note = AgentSkipped, "", ""
				h.inbox.mu.Unlock()
			}
			_, secondErr := h.replyInboxChecked(t.Context(), it.ID, text, recoveryFreshID, false, nil, nil)
			if !replyInDoubt(secondErr) {
				t.Errorf("%s allowed a fresh-ID legacy retry to escape the unresolved attempt: %v", mode, secondErr)
			}
			if got := len(c.calls()); got != 1 {
				t.Fatalf("%s repeated an uncertain legacy physical reply: calls=%d second error=%v", mode, got, secondErr)
			}
		})
	}
}

func TestInboxReplyPolicyInstructionSurvivesReportedStep(t *testing.T) {
	h, lark, it := handledHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
	it.Context = "Synthetic original conversation before an intervening reported step"
	{
		replyPolicyTestSetCategory(it, "ordinary_coordination")
		replyPolicyTestSetRoute(it, "owner_draft")
	}
	{
		replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
		replyPolicyTestSetPolicyRevision(it, "test-v1")
	}
	if _, err := suggestOrdinaryReplyForFixture(t, h, `{"kind":"reply","text":"Original ordinary draft"}`); err != nil {
		t.Fatal(err)
	}
	instruction := "Synthetic owner rewrite instruction before a reported step"
	if _, err := h.actOnSuggestion(t.Context(), it.ID, suggestionReq{Version: 1, Action: suggestInstruct, Text: &instruction}, true); err != nil {
		t.Fatal(err)
	}
	toldConv(t, h, instruction)
	if _, err := suggestTool(t, h, `{"reported":{"text":"Synthetic non-reply progress entry"}}`); err != nil {
		t.Fatal(err)
	}
	if got := lastChain(h, it).Kind; got != ChainReported {
		t.Fatalf("test did not place a reported step after the instruction: %q", got)
	}
	generated := "Synthetic approval generated after the reported step"
	calls := replyPolicyDraftSequenceModel(t, h, nil, `{"category":"approval_permission"}`)
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"`+generated+`"}`); !replyPolicyDenied(err) {
		t.Errorf("intervening reported step skipped generated-text classification: %v", err)
	}
	got := calls()
	if len(got) != 1 || got[0].System != replyPolicyTestClassifierSystem(h, replyPolicyTestConfig()) {
		t.Fatalf("reported step lost the instructed replacement classification: %+v", got)
	}
	for _, marker := range []string{generated, instruction, "Synthetic original conversation"} {
		if !strings.Contains(got[0].User, marker) {
			t.Fatalf("classifier after reported step lacks %q: %+v", marker, got[0])
		}
	}
	if currentSuggestion(it) != nil || len(lark.got) != 0 {
		t.Fatalf("restricted instructed replacement escaped after a reported step: suggestion=%+v sends=%+v", currentSuggestion(it), lark.got)
	}
}

func TestInboxReplyPolicyWorkbenchVersionedMirrorSend(t *testing.T) {
	needsLarkLayer(t) // the integrated Workbench renderer and actions are the lark module's
	for _, tc := range []struct {
		name            string
		expectedVersion int
	}{
		{name: "matching", expectedVersion: 1},
		{name: "stale", expectedVersion: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, lark, it := handledHub(t)
			h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
			{
				replyPolicyTestSetCategory(it, "ordinary_coordination")
				replyPolicyTestSetRoute(it, "owner_draft")
			}
			{
				replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
				replyPolicyTestSetPolicyRevision(it, "test-v1")
			}
			if _, err := suggestOrdinaryReplyForFixture(t, h, `{"kind":"reply","text":"Versioned synthetic Workbench mirror"}`); err != nil {
				t.Fatal(err)
			}
			text := currentSuggestion(it).Text
			h.in.setModel(fakeModel(t, `{"category":"ordinary_coordination"}`))
			_, err := h.replyInboxFromVersion(t.Context(), it.ID, text, "feishu-workbench-synthetic-"+tc.name, true, tc.expectedVersion)
			if tc.expectedVersion == 1 {
				if err != nil || len(lark.got) != 1 {
					t.Fatalf("matching-version Workbench mirror failed: err=%v sends=%+v", err, lark.got)
				}
				return
			}
			if !errors.Is(err, errSuggestionStale) {
				t.Errorf("stale Workbench version error = %v, want suggestion stale", err)
			}
			if len(lark.got) != 0 {
				t.Fatalf("stale Workbench version sent the replacement mirror: %+v", lark.got)
			}
		})
	}
}

func TestInboxReplyPolicyFollowMergedTruncationInvalidatesDraft(t *testing.T) {
	h, lark, it := handledHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
	{
		replyPolicyTestSetCategory(it, "ordinary_coordination")
		replyPolicyTestSetRoute(it, "owner_draft")
	}
	{
		replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
		replyPolicyTestSetPolicyRevision(it, "test-v1")
	}
	if _, err := suggestOrdinaryReplyForFixture(t, h, `{"kind":"reply","text":"Draft before a truncated merged-member update"}`); err != nil {
		t.Fatal(err)
	}
	member := &InboxItem{ID: "lark:synthetic-truncated-member", Source: "lark", MergedInto: it.ID,
		UpdatedAt: it.UpdatedAt.Add(time.Second), Context: strings.Repeat("R", mergeCtxMax)}
	h.inbox.mu.Lock()
	it.MergedFrom = append(it.MergedFrom, InboxMergeRef{ID: member.ID, Source: member.Source})
	h.inbox.items[member.ID] = member
	h.inbox.followMerged([]*InboxItem{member}, time.Now())
	h.inbox.mu.Unlock()
	if !it.ReplyContextTruncated {
		t.Fatal("followMerged discarded the member update's truncation boundary")
	}
	if replyPolicyTestCategory(it) != "" || replyPolicyTestRoute(it) != "" || replyPolicyTestContentVersion(it) != "" || replyPolicyTestPolicyRevision(it) != "" {
		t.Fatalf("followMerged retained the pre-update authorization snapshot: %+v", it)
	}
	model, calls := captureModel(t, `{"category":"ordinary_coordination"}`)
	h.in.model = model
	h.classifyInboxReplyPolicies(t.Context(), replyPolicyTestConfig())
	if got := len(calls()); got != 0 {
		t.Fatalf("truncated followMerged context made %d model calls, want fail-closed classification without one", got)
	}
	if replyPolicyTestCategory(it) != "unknown" || replyPolicyTestRoute(it) != "owner_only" || !replyPolicyClassificationCurrent(it, replyPolicyTestConfig()) {
		t.Fatalf("truncated followMerged context was not classified owner-only: %+v", it)
	}
	restarted := New(Options{Hub: h.f, Store: func() Store { return h.st }, Logf: testLogf(t)})
	if err := restarted.inboxReady(t.Context()); err != nil {
		t.Fatal(err)
	}
	restarted.inbox.mu.Lock()
	loaded := restarted.inbox.items[it.ID]
	restarted.inbox.mu.Unlock()
	if loaded == nil || replyPolicyTestCategory(loaded) != "unknown" || replyPolicyTestRoute(loaded) != "owner_only" || !loaded.ReplyContextTruncated {
		t.Fatalf("fail-closed followMerged classification was not persisted: %+v", loaded)
	}
	if _, err := h.actOnSuggestion(t.Context(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm}, true); err == nil {
		t.Error("the pre-merge owner draft remained sendable after truncated followMerged input")
	}
	if len(lark.got) != 0 {
		t.Fatalf("truncated followMerged input allowed the old draft to send: %+v", lark.got)
	}
}
