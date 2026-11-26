package assistant

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/prompts"
)

func TestInboxReplyPolicyOldTurnCannotPublishAfterNewContentClassificationLegacySchema(t *testing.T) {
	h, _, it := handledHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
	h.in.model = fakeModel(t, `{"category":"ordinary_coordination"}`)
	h.inbox.mu.Lock()
	oldTurn := it.Agent.ProcessingTurn
	if oldTurn == "" {
		oldTurn = "synthetic-old-processing-turn"
		it.Agent.ProcessingTurn = oldTurn
	}
	// This is the collection state at handPass: new content and target are present, the note still awaits delivery,
	// and the processing token has not rotated yet. classifyInboxReplyPolicies runs before passHook.
	it.Last = "new-classified-content"
	it.Context = "Synthetic newer coordination content"
	it.Lark.ReplyTo = "synthetic-new-target"
	it.ReplyCategory, it.ReplyMode, it.ReplyClassifiedOf, it.ReplyPolicyRev = "", "", "", ""
	it.Agent.Pending = []inboxNote{{Name: prompts.NoteNewMessages, Text: it.Context}}
	it.Agent.Wake = true
	h.inbox.mu.Unlock()
	var suggestErr error
	h.inbox.passHook = func() {
		h.inbox.passHook = nil
		if !replyPolicyClassificationCurrent(it, legacyReplyPolicyTestConfig()) {
			t.Errorf("test did not reach the post-classification / pre-delivery window: %+v", it)
			return
		}
		_, _, suggestErr = h.suggest(context.Background(), "s1", suggestIn{Kind: SuggestReply,
			Text: "Old turn text that predates the classified content", ProcessingTurn: oldTurn})
	}
	h.handPass(context.Background())
	if !errors.Is(suggestErr, errStaleInboxTurn) {
		t.Fatalf("old processing turn error = %v, want stale after new content was classified", suggestErr)
	}
	if g := currentSuggestion(it); g != nil {
		t.Fatalf("old processing turn published against the new target/classification: %+v", g)
	}
}

func TestInboxReplyPolicyInstructedReplacementClassifiesGeneratedTextLegacySchema(t *testing.T) {
	h, _, it := handledHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = legacyReplyPolicyTestConfig() })
	it.Context = "Synthetic original conversation before an instructed replacement"
	it.ReplyCategory, it.ReplyMode = "ordinary_coordination", "owner_draft"
	it.ReplyClassifiedOf, it.ReplyPolicyRev = replyPolicyContentVersion(it), "test-v1"
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"Original ordinary draft"}`); err != nil {
		t.Fatal(err)
	}
	instruction := "Synthetic owner instruction to rewrite the reply"
	if _, err := h.actOnSuggestion(context.Background(), it.ID, suggestionReq{Version: 1, Action: suggestInstruct, Text: &instruction}, true); err != nil {
		t.Fatal(err)
	}
	toldConv(t, h, instruction)
	generated := "Synthetic generated approval for the restricted permission request"
	calls := replyPolicyDraftSequenceModel(t, h, nil, `{"category":"approval_permission"}`)
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"`+generated+`"}`); !errors.Is(err, errReplyPolicyDenied) {
		t.Fatalf("restricted instructed replacement error = %v, want generated-text policy denial", err)
	}
	if g := currentSuggestion(it); g != nil {
		t.Fatalf("restricted instructed replacement was published with the old item classification: %+v", g)
	}
	got := calls()
	if len(got) != 1 || got[0].System != prompts.RenderJSONOnly(prompts.RenderInboxReplyPolicy()) {
		t.Fatalf("instructed replacement did not run generated-text classification: %+v", got)
	} else {
		for _, marker := range []string{generated, instruction, "Synthetic original conversation"} {
			if !strings.Contains(got[0].User, marker) {
				t.Fatalf("replacement classifier input lacks %q: %+v", marker, got[0])
			}
		}
	}
}

func TestInboxReplyPolicyRemergeUndoPreUpgradeSurvivorKeepsMemberScopeLegacySchema(t *testing.T) {
	h, lark, old := handledHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) {
		c.ReplyPolicy = legacyReplyPolicyTestConfig()
		c.ReplyPolicy.Sources = []string{"mail"}
	})
	member := actionItem("mail:pre-upgrade-nested-member", old.UpdatedAt.Add(-2*time.Second))
	member.Source = "mail"
	member.Context = "Synthetic restricted member text retained by the pre-upgrade survivor"
	member.MergedInto = old.ID
	old.MergedFrom = []InboxMergeRef{{ID: member.ID, Source: member.Source}}
	old.ReplyContentSources = nil
	old.Context += "\n\n" + member.Context
	newer := actionItem("lark:newer-remerge-survivor", old.UpdatedAt.Add(time.Second))
	newer.Source = "lark"
	newer.Context = "Synthetic newer survivor"
	h.inbox.mu.Lock()
	h.inbox.items[member.ID] = member
	h.inbox.items[newer.ID] = newer
	survivor, _ := h.inbox.merge([]*InboxItem{old, newer}, time.Now())
	if survivor != newer {
		h.inbox.mu.Unlock()
		t.Fatalf("remerge survivor = %+v, want newer row", survivor)
	}
	h.inbox.unmerge(newer, time.Now())
	h.inbox.mu.Unlock()
	if !strings.Contains(old.Context, member.Context) || old.MergedInto != "" {
		t.Fatalf("test did not restore the pre-upgrade survivor with retained member text: %+v", old)
	}
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"Post-remerge-undo draft"}`); !errors.Is(err, errReplyPolicyDenied) {
		t.Fatalf("remerge/undo dropped the pre-upgrade survivor's member source: %v", err)
	}
	if len(lark.got) != 0 {
		t.Fatalf("remerge/undo source loss reached sender: %+v", lark.got)
	}
}

func TestInboxReplyPolicyCollectionFollowMergedAppliesBothMembersNewestFirst(t *testing.T) {
	h := agentHub(t)
	base := time.Now().Add(-time.Hour)
	survivor := &InboxItem{ID: "synthetic:survivor", Source: "synthetic", Last: "base", Context: "Original survivor context",
		Count: 1, UpdatedAt: base, StartedAt: base, Tier: TierAction,
		MergedFrom: []InboxMergeRef{{ID: "synthetic:new", Source: "synthetic"}, {ID: "synthetic:old", Source: "synthetic"}}}
	newer := &InboxItem{ID: "synthetic:new", Source: "synthetic", Last: "new-0", Count: 1, UpdatedAt: base, StartedAt: base,
		MergedInto: survivor.ID, Connector: &InboxConnector{Thread: "new-member"}}
	older := &InboxItem{ID: "synthetic:old", Source: "synthetic", Last: "old-0", Count: 1, UpdatedAt: base, StartedAt: base,
		MergedInto: survivor.ID, Connector: &InboxConnector{Thread: "old-member"}}
	h.inbox.mu.Lock()
	h.inbox.items[survivor.ID], h.inbox.items[newer.ID], h.inbox.items[older.ID] = survivor, newer, older
	changed := h.inbox.applyConnector("synthetic", "Synthetic", []connector.InboxThing{
		{Thread: "new-member", Last: "new-1", At: base.Add(2 * time.Minute), Context: "Restricted newest member update"},
		{Thread: "old-member", Last: "old-1", At: base.Add(time.Minute), Context: "Restricted older member update"},
	}, time.Now())
	h.inbox.followMerged(changed, time.Now())
	h.inbox.mu.Unlock()
	if survivor.Count != 3 || !survivor.UpdatedAt.Equal(base.Add(2*time.Minute)) {
		t.Fatalf("newest-first member updates reached survivor count %d, want both updates (3) and the newest update last (UpdatedAt=%s)", survivor.Count, survivor.UpdatedAt)
	}
	for _, want := range []string{"Restricted newest member update", "Restricted older member update"} {
		if !strings.Contains(survivor.Context, want) {
			t.Fatalf("survivor context lacks %q after one collection round: %q", want, survivor.Context)
		}
	}
	if !strings.HasPrefix(survivor.Context, "Restricted newest member update") {
		t.Fatalf("older member was applied last, leaving the wrong survivor context order: %q", survivor.Context)
	}
}

func TestInboxReplyPolicyOldTurnCannotPublishAfterNewContentClassification(t *testing.T) {
	h, _, it := handledHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
	h.in.model = fakeModel(t, `{"category":"ordinary_coordination"}`)
	h.inbox.mu.Lock()
	oldTurn := it.Agent.ProcessingTurn
	if oldTurn == "" {
		oldTurn = "synthetic-old-processing-turn"
		it.Agent.ProcessingTurn = oldTurn
	}
	// This is the collection state at handPass: new content and target are present, the note still awaits delivery,
	// and the processing token has not rotated yet. passHook runs before the queue gate upstream, so this fixture
	// explicitly performs that classification before entering the post-classification / pre-delivery window.
	it.Last = "new-classified-content"
	it.Context = "Synthetic newer coordination content"
	it.Lark.ReplyTo = "synthetic-new-target"
	{
		replyPolicyTestSetCategory(it, "")
		replyPolicyTestSetRoute(it, "")
		replyPolicyTestSetContentVersion(it, "")
		replyPolicyTestSetPolicyRevision(it, "")
	}
	it.Agent.Pending = []inboxNote{{Name: prompts.NoteNewMessages, Text: it.Context}}
	it.Agent.Wake = true
	h.inbox.mu.Unlock()
	var suggestErr error
	h.inbox.passHook = func() {
		h.inbox.passHook = nil
		h.classifyInboxReplyPolicies(context.Background(), replyPolicyTestConfig())
		if !replyPolicyClassificationCurrent(it, replyPolicyTestConfig()) {
			t.Errorf("test did not reach the post-classification / pre-delivery window: %+v", it)
			return
		}
		_, _, suggestErr = h.suggest(context.Background(), "s1", suggestIn{Kind: SuggestReply,
			Text: "Old turn text that predates the classified content", ProcessingTurn: oldTurn})
	}
	h.handPass(context.Background())
	if !errors.Is(suggestErr, errStaleInboxTurn) {
		t.Fatalf("old processing turn error = %v, want stale after new content was classified", suggestErr)
	}
	if g := currentSuggestion(it); g != nil {
		t.Fatalf("old processing turn published against the new target/classification: %+v", g)
	}
}

func TestInboxReplyPolicyInstructedReplacementClassifiesGeneratedText(t *testing.T) {
	h, _, it := handledHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.ReplyPolicy = replyPolicyTestConfig() })
	it.Context = "Synthetic original conversation before an instructed replacement"
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
	instruction := "Synthetic owner instruction to rewrite the reply"
	if _, err := h.actOnSuggestion(context.Background(), it.ID, suggestionReq{Version: 1, Action: suggestInstruct, Text: &instruction}, true); err != nil {
		t.Fatal(err)
	}
	toldConv(t, h, instruction)
	generated := "Synthetic generated approval for the restricted permission request"
	calls := replyPolicyDraftSequenceModel(t, h, nil, `{"category":"approval_permission"}`)
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"`+generated+`"}`); !replyPolicyDenied(err) {
		t.Fatalf("restricted instructed replacement error = %v, want generated-text policy denial", err)
	}
	if g := currentSuggestion(it); g != nil {
		t.Fatalf("restricted instructed replacement was published with the old item classification: %+v", g)
	}
	got := calls()
	if len(got) != 1 || got[0].System != replyPolicyTestClassifierSystem(h, replyPolicyTestConfig()) {
		t.Fatalf("instructed replacement did not run generated-text classification: %+v", got)
	} else {
		for _, marker := range []string{generated, instruction, "Synthetic original conversation"} {
			if !strings.Contains(got[0].User, marker) {
				t.Fatalf("replacement classifier input lacks %q: %+v", marker, got[0])
			}
		}
	}
}

func TestInboxReplyPolicyRemergeUndoPreUpgradeSurvivorKeepsMemberScope(t *testing.T) {
	h, lark, old := handledHub(t)
	h.setConfig(func(c *assistantwire.InboxConfig) {
		c.ReplyPolicy = replyPolicyTestConfig()
		c.ReplyPolicy.Sources = []string{"mail"}
	})
	member := actionItem("mail:pre-upgrade-nested-member", old.UpdatedAt.Add(-2*time.Second))
	member.Source = "mail"
	member.Context = "Synthetic restricted member text retained by the pre-upgrade survivor"
	member.MergedInto = old.ID
	old.MergedFrom = []InboxMergeRef{{ID: member.ID, Source: member.Source}}
	old.ReplyContentSources = nil
	old.Context += "\n\n" + member.Context
	newer := actionItem("lark:newer-remerge-survivor", old.UpdatedAt.Add(time.Second))
	newer.Source = "lark"
	newer.Context = "Synthetic newer survivor"
	h.inbox.mu.Lock()
	h.inbox.items[member.ID] = member
	h.inbox.items[newer.ID] = newer
	survivor, _ := h.inbox.merge([]*InboxItem{old, newer}, time.Now())
	if survivor != newer {
		h.inbox.mu.Unlock()
		t.Fatalf("remerge survivor = %+v, want newer row", survivor)
	}
	h.inbox.unmerge(newer, time.Now())
	h.inbox.mu.Unlock()
	if !strings.Contains(old.Context, member.Context) || old.MergedInto != "" {
		t.Fatalf("test did not restore the pre-upgrade survivor with retained member text: %+v", old)
	}
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"Post-remerge-undo draft"}`); !replyPolicyDenied(err) {
		t.Fatalf("remerge/undo dropped the pre-upgrade survivor's member source: %v", err)
	}
	if len(lark.got) != 0 {
		t.Fatalf("remerge/undo source loss reached sender: %+v", lark.got)
	}
}
