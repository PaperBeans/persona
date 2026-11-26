package assistant

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
)

func review12StartItem(h *inboxHub, id, chat string) *InboxItem {
	it := actionItem(id, time.Now())
	it.Source = "lark"
	it.Lark = &InboxLark{FeedID: chat, IsGroup: chat == "synthetic-group"}
	it.Agent = &InboxAgent{State: AgentRunning, ProcessingTurn: "review12-turn"}
	h.inbox.items[it.ID] = it
	return it
}

func TestInboxVisibilityReview12AgentStartFreshVisibility(t *testing.T) {
	for _, mergedMember := range []bool{false, true} {
		t.Run(map[bool]string{false: "item", true: "merged member"}[mergedMember], func(t *testing.T) {
			h := agentHub(t)
			chat := "synthetic-private"
			it := review12StartItem(h, "lark:review12-start-direct", chat)
			if mergedMember {
				it = review12StartItem(h, "lark:review12-start-survivor", "synthetic-group")
				member := actionItem("lark:review12-start-member", time.Now().Add(-time.Minute))
				member.Source = "lark"
				member.Lark = &InboxLark{FeedID: "synthetic-private"}
				member.MergedInto = it.ID
				it.MergedFrom = []InboxMergeRef{{ID: member.ID, Source: member.Source}}
				h.inbox.items[member.ID] = member
			}
			cfg := InboxAgentConfig{Enabled: true}
			h.in.mu.Lock()
			h.in.configQueue = []assistantwire.InboxConfig{{Agent: cfg, Visibility: visibilityTestConfig()}}
			h.in.mu.Unlock()
			before := len(conversationStarts(h.f))
			_, _, err := h.startInboxAgentAuthorized(context.Background(), cfg, it, "SECRET-FINAL-START", replyPolicyContentVersion(it), "review12-turn")
			if !errors.Is(err, errInboxStartAuthorization) {
				t.Errorf("final Agent-start visibility error = %v, want errInboxStartAuthorization", err)
			}
			starts := conversationStarts(h.f)
			if len(starts) != before {
				t.Fatalf("freshly excluded item reached Start %d time(s)", len(starts)-before)
			}
			for _, start := range starts {
				if text, _ := start["text"].(string); strings.Contains(text, "SECRET-FINAL-START") {
					t.Fatalf("freshly excluded prompt reached Start: %s", text)
				}
			}
		})
	}
}

func TestInboxVisibilityReview12AgentStartFreshReadProfile(t *testing.T) {
	h := agentHub(t)
	it := review12StartItem(h, "lark:review12-start-profile", "synthetic-group")
	cfg := InboxAgentConfig{Enabled: true}
	fresh := assistantwire.InboxConfig{Agent: cfg}
	fresh.Agent.ReadProfile = "source_only"
	h.in.mu.Lock()
	h.in.configQueue = []assistantwire.InboxConfig{fresh}
	h.in.mu.Unlock()
	before := len(conversationStarts(h.f))
	_, _, err := h.startInboxAgentAuthorized(context.Background(), cfg, it, "SECRET-KB-CONTEXT", replyPolicyContentVersion(it), "review12-turn")
	if !errors.Is(err, errInboxStartAuthorization) {
		t.Errorf("final Agent-start read-profile error = %v, want errInboxStartAuthorization", err)
	}
	starts := conversationStarts(h.f)
	if len(starts) != before {
		t.Fatalf("prompt built under the old read profile reached Start %d time(s)", len(starts)-before)
	}
}

func TestInboxVisibilityReview12AgentStartUnchangedControl(t *testing.T) {
	h := agentHub(t)
	it := review12StartItem(h, "lark:review12-start-control", "synthetic-group")
	cfg := InboxAgentConfig{Enabled: true}
	h.in.mu.Lock()
	h.in.configQueue = []assistantwire.InboxConfig{{Agent: cfg}}
	h.in.mu.Unlock()
	before := len(conversationStarts(h.f))
	_, _, err := h.startInboxAgentAuthorized(context.Background(), cfg, it, "SYNTHETIC-START-CONTROL", replyPolicyContentVersion(it), "review12-turn")
	if err != nil {
		t.Fatalf("unchanged final authorization refused Start: %v", err)
	}
	starts := conversationStarts(h.f)
	if len(starts) != before+1 {
		t.Fatalf("unchanged final authorization made %d Starts, want one", len(starts)-before)
	}
	text, _ := starts[len(starts)-1]["text"].(string)
	if !strings.Contains(text, "SYNTHETIC-START-CONTROL") {
		t.Fatalf("positive control did not observe the exact prompt: %q", text)
	}
}

func TestInboxVisibilityReview12MergeUndoRetainsContributorVisibility(t *testing.T) {
	h := muteHub(t)
	now := time.Now()
	survivor := actionItem("lark:review12-undo-survivor", now)
	survivor.Source = "lark"
	survivor.Lark = &InboxLark{FeedID: "synthetic-group", IsGroup: true}
	survivor.Context = "SYNTHETIC-GROUP-CONTEXT"
	member := actionItem("lark:review12-undo-member", now.Add(-time.Minute))
	member.Source = "lark"
	member.Lark = &InboxLark{FeedID: "synthetic-private"}
	member.Context = "SECRET-UNDO-PRIVATE-CONTEXT"
	h.inbox.items[survivor.ID] = survivor
	h.inbox.items[member.ID] = member

	h.inbox.mu.Lock()
	merged, _ := h.inbox.merge([]*InboxItem{member, survivor}, now)
	h.inbox.unmerge(merged, now.Add(time.Second))
	excluded := inboxVisibilityWithMembersExcludedLocked(&h.inbox, merged, visibilityTestConfig())
	h.inbox.mu.Unlock()
	if !strings.Contains(merged.Context, member.Context) {
		t.Fatal("fixture did not retain the member's text after Undo")
	}
	if !excluded {
		t.Error("Undo lost the retained private member text's visibility provenance")
	}

	h.inbox.mu.Lock()
	ordinaryAllowed := !inboxVisibilityWithMembersExcludedLocked(&h.inbox, merged,
		&assistantwire.InboxVisibilityConfig{Sources: []string{"lark"}, Chats: []string{"another-synthetic-chat"}})
	h.inbox.mu.Unlock()
	if !ordinaryAllowed {
		t.Error("an unrelated exclusion blocked the ordinary group survivor after Undo")
	}
}

func review12DraftAuthorization(it *InboxItem, text string, policy *assistantwire.InboxReplyPolicyConfig, applied bool) onDemandDraftAuthorization {
	it.Agent = &InboxAgent{State: AgentDrafted, Draft: text}
	if applied {
		it.ReplyCategory = "ordinary_coordination"
		it.ReplyMode = ReplyRouteOwnerDraft
		it.ReplyClassifiedOf = replyPolicyContentVersion(it)
		it.ReplyPolicyRev = policy.Revision
	}
	return onDemandDraftAuthorization{before: itemState(it), policy: policy, applied: applied}
}

func TestInboxVisibilityReview12DraftPublicationLegacyFreshVisibility(t *testing.T) {
	for _, mergedMember := range []bool{false, true} {
		t.Run(map[bool]string{false: "item", true: "merged member"}[mergedMember], func(t *testing.T) {
			h, _, it := handledHub(t)
			it.Source = "lark"
			it.Lark = &InboxLark{FeedID: "synthetic-group", IsGroup: true}
			if mergedMember {
				member := actionItem("lark:review12-draft-member", time.Now().Add(-time.Minute))
				member.Source = "lark"
				member.Lark = &InboxLark{FeedID: "synthetic-private"}
				member.MergedInto = it.ID
				it.MergedFrom = []InboxMergeRef{{ID: member.ID, Source: member.Source}}
				h.inbox.items[member.ID] = member
			} else {
				it.Lark.FeedID = "synthetic-private"
				it.Lark.IsGroup = false
			}
			const text = "SYNTHETIC-LEGACY-DRAFT"
			authorization := review12DraftAuthorization(it, text, nil, false)
			h.in.mu.Lock()
			h.in.configQueue = []assistantwire.InboxConfig{{Visibility: visibilityTestConfig()}}
			h.in.mu.Unlock()
			got, err := h.finishOnDemandDraft(context.Background(), it.ID, text, authorization, nil)
			if !errors.Is(err, errCannotReply) {
				t.Errorf("legacy draft publication visibility error = %v, want errCannotReply (text %q)", err, got)
			}
			if currentSuggestion(it) != nil {
				t.Error("newly excluded legacy draft published a suggestion")
			}
		})
	}
}

func TestInboxVisibilityReview12DraftPublicationPolicyFreshVisibility(t *testing.T) {
	for _, excluded := range []bool{false, true} {
		t.Run(map[bool]string{false: "unchanged visibility control", true: "newly excluded"}[excluded], func(t *testing.T) {
			h, _, it := handledHub(t)
			it.Source = "lark"
			it.Lark = &InboxLark{FeedID: "synthetic-group", IsGroup: true}
			visibility := &assistantwire.InboxVisibilityConfig{Sources: []string{"lark"}, Chats: []string{"another-synthetic-chat"}}
			if excluded {
				it.Lark.FeedID = "synthetic-private"
				it.Lark.IsGroup = false
				visibility = visibilityTestConfig()
			}
			policy := legacyReplyPolicyTestConfig()
			const text = "SYNTHETIC-POLICY-DRAFT"
			authorization := review12DraftAuthorization(it, text, policy, true)
			h.in.mu.Lock()
			h.in.configQueue = []assistantwire.InboxConfig{{ReplyPolicy: policy, Visibility: visibility}}
			h.in.mu.Unlock()
			got, err := h.finishOnDemandDraft(context.Background(), it.ID, text, authorization, nil)
			if excluded {
				if !errors.Is(err, errCannotReply) {
					t.Errorf("policy draft publication visibility error = %v, want errCannotReply (text %q)", err, got)
				}
				if currentSuggestion(it) != nil {
					t.Error("policy draft with an unchanged item snapshot published after its chat was excluded")
				}
			} else if err != nil || got != text || currentSuggestion(it) == nil {
				t.Fatalf("unchanged policy draft positive control: text %q, err %v, suggestion %+v", got, err, currentSuggestion(it))
			}
		})
	}
}

func TestInboxVisibilityReview12TodoAndConclusionConfirmationFreshConfig(t *testing.T) {
	for _, kind := range []string{SuggestTodo, SuggestConclusion} {
		for _, change := range []string{"unchanged", "new exclusion", "configuration failure"} {
			t.Run(kind+"/"+change, func(t *testing.T) {
				h, _, it := handledHub(t)
				it.Source = "lark"
				it.Lark.FeedID = "synthetic-private"
				g := &InboxSuggestion{Version: 1, Kind: kind, Text: "SYNTHETIC-NON-REPLY-ACTION", Due: "2026-11-16", At: time.Now()}
				it.Suggestion = g
				denied := change != "unchanged"
				switch change {
				case "new exclusion":
					h.setConfig(func(c *assistantwire.InboxConfig) { c.Visibility = visibilityTestConfig() })
					h.inbox.cfgMu.Lock()
					h.inbox.cfg = assistantwire.InboxConfig{}
					h.inbox.cfgAt = time.Now()
					h.inbox.cfgMu.Unlock()
				case "configuration failure":
					// A known, non-excluding visibility boundary still needs its fresh read. Keep that full configuration in
					// the display cache so denial here proves the outage is fatal, not that the cached rule already excluded it.
					h.setConfig(func(c *assistantwire.InboxConfig) {
						c.Visibility = &assistantwire.InboxVisibilityConfig{Sources: []string{"lark"}, Chats: []string{"another-private"}}
					})
					cached := h.inboxConfig(context.Background())
					h.inbox.cfgMu.Lock()
					h.inbox.cfg = cached
					h.inbox.cfgAt = time.Now()
					h.inbox.cfgMu.Unlock()
					h.in.mu.Lock()
					h.in.configErr = errors.New("synthetic visibility configuration outage")
					h.in.mu.Unlock()
				}
				_, err := h.actOnSuggestion(context.Background(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm}, true)
				if denied {
					if !errors.Is(err, errCannotReply) {
						t.Errorf("%s confirmation after %s: error = %v, want errCannotReply", kind, change, err)
					}
					if currentSuggestion(it) != g {
						t.Errorf("%s confirmation after %s consumed its suggestion", kind, change)
					}
					if it.DoneAt != nil {
						t.Errorf("%s confirmation after %s filed the item", kind, change)
					}
				} else if err != nil || currentSuggestion(it) != nil {
					t.Fatalf("%s unchanged confirmation positive control: err %v, suggestion %+v", kind, err, currentSuggestion(it))
				}
			})
		}
	}
}

func TestInboxVisibilityReview12AmbiguousChatIdentifiersFailClosed(t *testing.T) {
	chat := func(id string) connector.Tag { return connector.Tag{Key: connector.TagChat, Value: id} }
	newItem := func(tags ...connector.Tag) *InboxItem {
		return &InboxItem{Source: "example", Kind: "group", Connector: &InboxConnector{Tags: tags}}
	}
	cfg := &assistantwire.InboxVisibilityConfig{Sources: []string{"example"}, Chats: []string{"synthetic-private"}}
	for _, tc := range []struct {
		name string
		item *InboxItem
		cfg  *assistantwire.InboxVisibilityConfig
		want bool
	}{
		{name: "allowed then excluded", item: newItem(chat("synthetic-allowed"), chat("synthetic-private")), cfg: cfg, want: true},
		{name: "allowed then empty", item: newItem(chat("synthetic-allowed"), chat(" \t ")), cfg: cfg, want: true},
		{name: "empty then allowed", item: newItem(chat(" \t "), chat("synthetic-allowed")), cfg: cfg, want: true},
		{name: "excluded then allowed", item: newItem(chat("synthetic-private"), chat("synthetic-allowed")), cfg: cfg, want: true},
		{name: "conflicting unlisted ids", item: newItem(chat("synthetic-one"), chat("synthetic-two")), cfg: cfg, want: true},
		{name: "whitespace only id", item: newItem(chat(" \t ")), cfg: cfg, want: true},
		{name: "whitespace around excluded id", item: newItem(chat(" synthetic-private ")), cfg: cfg, want: true},
		{name: "duplicate normalized allowed id", item: newItem(chat("synthetic-allowed"), chat(" synthetic-allowed ")), cfg: cfg, want: false},
		{name: "conflict with controls off", item: newItem(chat("synthetic-one"), chat("synthetic-two")), cfg: &assistantwire.InboxVisibilityConfig{Sources: []string{"example"}}, want: false},
		{name: "whitespace Lark id", item: &InboxItem{Source: "lark", Lark: &InboxLark{FeedID: " \t "}}, cfg: &assistantwire.InboxVisibilityConfig{Sources: []string{"lark"}, Chats: []string{"synthetic-private"}}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := inboxVisibilityMatch(tc.item, tc.cfg); got != tc.want {
				t.Errorf("inboxVisibilityMatch() = %v, want %v", got, tc.want)
			}
		})
	}
}
