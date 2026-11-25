package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/prompts"
)

type replyPolicyDraftModelCall struct {
	System string
	User   string
}

// replyPolicyDraftSequenceModel answers generation and classification calls in order and records what each call saw.
func replyPolicyDraftSequenceModel(t *testing.T, h *inboxHub, onCall func(int), replies ...string) func() []replyPolicyDraftModelCall {
	t.Helper()
	if len(replies) == 0 {
		t.Fatal("reply-policy sequence model needs at least one response")
	}
	var mu sync.Mutex
	var calls []replyPolicyDraftModelCall
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
		call := replyPolicyDraftModelCall{System: in.System}
		if len(in.Messages) > 0 {
			call.User = in.Messages[0].Content
		}
		mu.Lock()
		n := len(calls)
		calls = append(calls, call)
		mu.Unlock()
		if n >= len(replies) {
			t.Errorf("reply-policy sequence model received unexpected call %d", n+1)
			http.Error(w, "unexpected model call", http.StatusInternalServerError)
			return
		}
		if onCall != nil {
			onCall(n)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"content": []any{map[string]any{"type": "text", "text": replies[n]}}})
	}))
	t.Cleanup(srv.Close)
	h.in.model = modelAt(t, srv.URL)
	return func() []replyPolicyDraftModelCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]replyPolicyDraftModelCall(nil), calls...)
	}
}

func TestInboxReplyPolicyDraftPolicyExitAfterDeviceChangeIsStaleLegacySchema(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*assistantwire.InboxReplyPolicyConfig)
	}{
		{name: "disabled", change: func(cfg *assistantwire.InboxReplyPolicyConfig) { cfg.Enabled = false }},
		{name: "out of scope", change: func(cfg *assistantwire.InboxReplyPolicyConfig) { cfg.Sources = []string{"mail"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, it := handledHub(t)
			h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
			it.Context = "Synthetic coordination request before the policy becomes inapplicable"
			it.ReplyCategory, it.ReplyMode = "ordinary_coordination", "owner_draft"
			it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
			calls := replyPolicyDraftSequenceModel(t, h, func(call int) {
				if call != 0 {
					return
				}
				h.setConfig(func(c *assistantwire.InboxConfig) { tc.change(c.ReplyPolicy) })
				h.inbox.mu.Lock()
				it.Title = "Synthetic newer state from another device"
				h.inbox.mu.Unlock()
			}, "Synthetic draft that must not become a stale transient result")

			code, out := inboxCall(t, h.routes(), "POST", "/api/v1/inbox/"+it.ID+"/draft", `{}`)
			if got := len(calls()); got != 1 {
				t.Errorf("policy-exit draft made %d model calls, want one generation", got)
			}
			if code != http.StatusConflict || out["error"] != errSuggestionStale.Error() {
				t.Errorf("policy-exit draft response = %d %+v, want HTTP 409 for errSuggestionStale", code, out)
			}
			if text, _ := out["text"].(string); text != "" {
				t.Errorf("policy-exit draft returned stale text %q after another device changed the item", text)
			}
		})
	}
}

type replyPolicyDraftAfterWriteStore struct {
	*inboxMemStore
	after func()
}

func (s *replyPolicyDraftAfterWriteStore) PutInboxItem(ctx context.Context, id string, data []byte, updated time.Time, done *time.Time) error {
	err := s.inboxMemStore.PutInboxItem(ctx, id, data, updated, done)
	if err == nil && s.after != nil {
		s.after()
	}
	return err
}

func TestInboxReplyPolicyDraftRejectsStateChangeAfterPersistenceLegacySchema(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*InboxItem)
	}{
		{name: "content", mutate: func(it *InboxItem) { it.Context += "\nSynthetic content arrived after publication" }},
		{name: "reply target", mutate: func(it *InboxItem) { it.Lark.ReplyTo = "synthetic-recipient-after-publication" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, it := handledHub(t)
			h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
			it.Context = "Synthetic coordination request before post-save mutation"
			it.ReplyCategory, it.ReplyMode = "ordinary_coordination", "owner_draft"
			it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
			h.in.model = fakeModel(t, "Synthetic draft whose state changes after its successful write")
			st := &replyPolicyDraftAfterWriteStore{inboxMemStore: h.st}
			afterCalls := 0
			st.after = func() {
				afterCalls++
				h.inbox.mu.Lock()
				defer h.inbox.mu.Unlock()
				mirror := mirroredLegacyReply(it)
				tc.mutate(it)
				if mirror == nil || mirroredLegacyReply(it) != mirror {
					t.Errorf("%s mutation did not preserve the original mirror pointer", tc.name)
				}
			}
			h.store = func() Store { return st }

			text, err := h.draftReply(t.Context(), it.ID, "")
			if afterCalls != 1 {
				t.Errorf("successful publication write hook ran %d times, want once", afterCalls)
			}
			if !errors.Is(err, errSuggestionStale) {
				t.Errorf("%s post-save mutation error = %v, want errSuggestionStale", tc.name, err)
			}
			if text != "" {
				t.Errorf("%s post-save mutation returned stale draft text %q", tc.name, text)
			}
		})
	}
}

func TestInboxReplyPolicyOnDemandDraftClassifiesReplyableFYILegacySchema(t *testing.T) {
	h, _, it := handledHub(t)
	cfg := legacyReplyPolicyTestConfig()
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
	h.inbox.mu.Lock()
	it.Tier = TierFYI
	it.Agent = nil
	it.Handling = nil
	it.Context = "Synthetic replyable FYI request: arrange the Thursday project sync"
	it.ReplyCategory, it.ReplyMode, it.ReplyClassifiedOf, it.ReplyPolicyRev = "", "", "", ""
	it.ReplyReviewed = false
	h.inbox.mu.Unlock()
	const wantDraft = "Synthetic on-demand reply for an FYI item"
	calls := replyPolicyDraftSequenceModel(t, h, nil, `{"category":"ordinary_coordination"}`, wantDraft)

	text, err := h.draftReply(t.Context(), it.ID, "")
	if err != nil {
		t.Errorf("replyable FYI on-demand draft error = %v, want on-demand classification followed by generation", err)
	}
	if text != wantDraft {
		t.Errorf("replyable FYI on-demand draft text = %q, want %q", text, wantDraft)
	}
	got := calls()
	if len(got) != 2 {
		t.Errorf("replyable FYI draft made %d model calls, want classification followed by generation", len(got))
	} else {
		if got[0].System != prompts.RenderJSONOnly(prompts.RenderInboxReplyPolicy()) || !strings.Contains(got[0].User, it.Context) {
			t.Errorf("first call did not classify the FYI item's current context: %+v", got[0])
		}
		if got[1].System == prompts.RenderJSONOnly(prompts.RenderInboxReplyPolicy()) || got[1].System == prompts.RenderJSONOnly(prompts.RenderInboxReplyPolicyReview()) {
			t.Errorf("second call repeated classification instead of generating the draft: %+v", got[1])
		}
	}
	if it.ReplyCategory != "ordinary_coordination" || it.ReplyMode != "owner_draft" ||
		it.ReplyClassifiedOf != replyPolicyContentVersion(it) || it.ReplyPolicyRev != "test-v1" {
		t.Errorf("FYI on-demand classification was not published as current: %+v", it)
	}
	if !replyPolicySnapshotCurrent(it, cfg) {
		t.Error("FYI on-demand draft has no current reply-policy snapshot")
	}
	mirror := mirroredLegacyReply(it)
	if mirror == nil || mirror.Text != wantDraft || currentSuggestion(it) != mirror {
		t.Errorf("FYI on-demand draft did not publish its current mirror: agent=%+v suggestion=%+v", it.Agent, currentSuggestion(it))
	}
}

func TestInboxReplyPolicyDraftPolicyExitAfterDeviceChangeIsStale(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*assistantwire.InboxReplyPolicyConfig)
	}{
		{name: "disabled", change: func(cfg *assistantwire.InboxReplyPolicyConfig) { cfg.Enabled = false }},
		{name: "out of scope", change: func(cfg *assistantwire.InboxReplyPolicyConfig) { cfg.Sources = []string{"mail"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, it := handledHub(t)
			h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
			it.Context = "Synthetic coordination request before the policy becomes inapplicable"
			{
				replyPolicyTestSetCategory(it, "ordinary_coordination")
				replyPolicyTestSetRoute(it, "owner_draft")
			}
			{
				replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
				replyPolicyTestSetPolicyRevision(it, "test-v1")
			}
			calls := replyPolicyDraftSequenceModel(t, h, func(call int) {
				if call != 1 {
					return
				}
				h.setConfig(func(c *assistantwire.InboxConfig) { tc.change(c.ReplyPolicy) })
				h.inbox.mu.Lock()
				it.Title = "Synthetic newer state from another device"
				h.inbox.mu.Unlock()
			}, `{"category":"ordinary_coordination"}`, "Synthetic draft that must not become a stale transient result")

			code, out := inboxCall(t, h.routes(), "POST", "/api/v1/inbox/"+it.ID+"/draft", `{}`)
			if got := len(calls()); got != 2 {
				t.Errorf("policy-exit draft made %d model calls, want initial classification and one generation", got)
			}
			if code != http.StatusConflict || out["error"] != errSuggestionStale.Error() {
				t.Errorf("policy-exit draft response = %d %+v, want HTTP 409 for errSuggestionStale", code, out)
			}
			if text, _ := out["text"].(string); text != "" {
				t.Errorf("policy-exit draft returned stale text %q after another device changed the item", text)
			}
		})
	}
}

func TestInboxReplyPolicyDraftRejectsStateChangeAfterPersistence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*InboxItem)
	}{
		{name: "content", mutate: func(it *InboxItem) { it.Context += "\nSynthetic content arrived after publication" }},
		{name: "reply target", mutate: func(it *InboxItem) { it.Lark.ReplyTo = "synthetic-recipient-after-publication" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, it := handledHub(t)
			h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
			it.Context = "Synthetic coordination request before post-save mutation"
			{
				replyPolicyTestSetCategory(it, "ordinary_coordination")
				replyPolicyTestSetRoute(it, "owner_draft")
			}
			{
				replyPolicyTestSetContentVersion(it, replyPolicyContentVersion(it))
				replyPolicyTestSetPolicyRevision(it, "test-v1")
			}
			replyPolicyOrdinaryDraftModel(t, h, "Synthetic draft whose state changes after its successful write")
			st := &replyPolicyDraftAfterWriteStore{inboxMemStore: h.st}
			afterCalls := 0
			st.after = func() {
				afterCalls++
				h.inbox.mu.Lock()
				defer h.inbox.mu.Unlock()
				mirror := mirroredLegacyReply(it)
				tc.mutate(it)
				if mirror == nil || mirroredLegacyReply(it) != mirror {
					t.Errorf("%s mutation did not preserve the original mirror pointer", tc.name)
				}
			}
			h.store = func() Store { return st }

			text, err := h.draftReply(t.Context(), it.ID, "")
			if afterCalls != 1 {
				t.Errorf("successful publication write hook ran %d times, want once", afterCalls)
			}
			if !errors.Is(err, errSuggestionStale) {
				t.Errorf("%s post-save mutation error = %v, want errSuggestionStale", tc.name, err)
			}
			if text != "" {
				t.Errorf("%s post-save mutation returned stale draft text %q", tc.name, text)
			}
		})
	}
}

func TestInboxReplyPolicyOnDemandDraftClassifiesReplyableFYI(t *testing.T) {
	h, _, it := handledHub(t)
	cfg := replyPolicyTestConfig()
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = cfg })
	h.inbox.mu.Lock()
	it.Tier = TierFYI
	it.Agent = nil
	it.Handling = nil
	it.Context = "Synthetic replyable FYI request: arrange the Thursday project sync"
	{
		replyPolicyTestSetCategory(it, "")
		replyPolicyTestSetRoute(it, "")
		replyPolicyTestSetContentVersion(it, "")
		replyPolicyTestSetPolicyRevision(it, "")
	}
	{
		replyPolicyTestSetReviewed(it, false)
	}
	h.inbox.mu.Unlock()
	const wantDraft = "Synthetic on-demand reply for an FYI item"
	calls := replyPolicyDraftSequenceModel(t, h, nil, `{"category":"ordinary_coordination"}`, wantDraft, `{"category":"ordinary_coordination"}`)

	text, err := h.draftReply(t.Context(), it.ID, "")
	if err != nil {
		t.Errorf("replyable FYI on-demand draft error = %v, want on-demand classification followed by generation", err)
	}
	if text != wantDraft {
		t.Errorf("replyable FYI on-demand draft text = %q, want %q", text, wantDraft)
	}
	classifierSystem := replyPolicyTestClassifierSystem(h, cfg)
	got := calls()
	if len(got) != 3 {
		t.Errorf("replyable FYI draft made %d model calls, want classification, generation, and generated-text classification", len(got))
	} else {
		if got[0].System != classifierSystem || !strings.Contains(got[0].User, it.Context) {
			t.Errorf("first call did not classify the FYI item's current context: %+v", got[0])
		}
		if got[1].System == classifierSystem || got[1].System == prompts.RenderJSONOnly(replyPolicyIndependentReviewPrompt) {
			t.Errorf("second call repeated classification instead of generating the draft: %+v", got[1])
		}
		if got[2].System != classifierSystem || !strings.Contains(got[2].User, wantDraft) {
			t.Errorf("third call did not classify the generated FYI draft: %+v", got[2])
		}
	}
	if replyPolicyTestCategory(it) != "ordinary_coordination" || replyPolicyTestRoute(it) != "owner_draft" || replyPolicyTestContentVersion(it) != replyPolicyContentVersion(it) || replyPolicyTestPolicyRevision(it) != "test-v1" {
		t.Errorf("FYI on-demand classification was not published as current: %+v", it)
	}
	if !replyPolicySnapshotCurrent(it, cfg) {
		t.Error("FYI on-demand draft has no current reply-policy snapshot")
	}
	mirror := mirroredLegacyReply(it)
	if mirror == nil || mirror.Text != wantDraft || currentSuggestion(it) != mirror {
		t.Errorf("FYI on-demand draft did not publish its current mirror: agent=%+v suggestion=%+v", it.Agent, currentSuggestion(it))
	}
}
