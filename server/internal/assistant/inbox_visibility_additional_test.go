package assistant

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/prompts"
)

type visibilityProviderExtensions struct {
	*policyExtensions
	providers map[string]bool
}

func (e *visibilityProviderExtensions) InboxVisibilityProvider(source string) bool {
	return e.providers[source]
}

func linkVisibilityTestMerge(h *inboxHub, survivor, member *InboxItem) {
	h.inbox.mu.Lock()
	defer h.inbox.mu.Unlock()
	member.MergedInto = survivor.ID
	survivor.MergedFrom = []InboxMergeRef{{ID: member.ID, Source: member.Source, Title: member.Title}}
	h.inbox.items[survivor.ID], h.inbox.items[member.ID] = survivor, member
}

func visibilityTestMergeMember(survivor *InboxItem) *InboxItem {
	member := actionItem("lark:synthetic-private:merged-member", survivor.UpdatedAt.Add(-time.Minute))
	member.Source = "lark"
	member.Lark = &InboxLark{FeedID: "synthetic-private"}
	member.Title = "SECRET-MERGED-MEMBER-TITLE"
	member.Context = "SECRET-MERGED-MEMBER-CONTEXT"
	member.VisibilityExcluded = true
	return member
}

func TestInboxVisibilityInitialHandoffFailsClosedSynchronously(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*inboxHub, *InboxItem)
	}{
		{name: "current configuration", setup: func(h *inboxHub, it *InboxItem) {
			it.Lark.FeedID = "synthetic-private"
			h.setConfig(func(c *assistantwire.InboxConfig) { c.Visibility = visibilityTestConfig() })
		}},
		{name: "persistent marker", setup: func(_ *inboxHub, it *InboxItem) {
			it.VisibilityExcluded = true
		}},
		{name: "historical mixed merge", setup: func(h *inboxHub, it *InboxItem) {
			linkVisibilityTestMerge(h, it, visibilityTestMergeMember(it))
		}},
		{name: "configuration failure", setup: func(h *inboxHub, _ *InboxItem) {
			h.in.mu.Lock()
			h.in.configErr = errors.New("synthetic configuration outage")
			h.in.mu.Unlock()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, it := handoffHub(t)
			tc.setup(h, it)
			_, _, err := h.handOff(t.Context(), it.ID, handoffReq{Text: "Inspect this synthetic item", ClientID: handoffClient})
			if err == nil {
				t.Fatal("an excluded item completed its initial Hand to AI send")
			}
			if got := sendOnces(h.f); len(got) != 0 {
				t.Fatalf("the denied handoff wrote %d messages into the main conversation: %+v", len(got), got)
			}
			if got := conversationStarts(h.f); len(got) != 0 {
				t.Fatalf("the denied handoff started %d conversations", len(got))
			}
			h.inbox.mu.Lock()
			handoffs := len(it.Handoffs)
			h.inbox.mu.Unlock()
			if handoffs != 0 {
				t.Fatalf("the denied handoff recorded %d sends", handoffs)
			}
		})
	}
}

func TestInboxVisibilityHistoricalMixedMergeBlocksSuggestionLifecycle(t *testing.T) {
	t.Run("creation", func(t *testing.T) {
		h, lark, it := handledHub(t)
		linkVisibilityTestMerge(h, it, visibilityTestMergeMember(it))
		if _, err := suggestTool(t, h, `{"kind":"reply","text":"Synthetic reply through a mixed merge"}`); err == nil {
			t.Fatal("a survivor containing a quarantined member accepted a new suggestion")
		}
		if currentSuggestion(it) != nil || len(lark.got) != 0 {
			t.Fatalf("denied creation changed suggestion or sender state: %+v, sends=%d", currentSuggestion(it), len(lark.got))
		}
	})

	t.Run("confirmation", func(t *testing.T) {
		h, lark, it := handledHub(t)
		if _, err := suggestTool(t, h, `{"kind":"reply","text":"Synthetic reply before merging private history"}`); err != nil {
			t.Fatal(err)
		}
		linkVisibilityTestMerge(h, it, visibilityTestMergeMember(it))
		if _, err := h.actOnSuggestion(t.Context(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm}, true); err == nil {
			t.Fatal("a survivor containing a quarantined member confirmed its old suggestion")
		}
		if len(lark.got) != 0 {
			t.Fatalf("denied mixed-merge confirmation called the sender: %+v", lark.got)
		}
	})

	t.Run("final send recheck", func(t *testing.T) {
		h, lark, it := handledHub(t)
		if _, err := suggestTool(t, h, `{"kind":"reply","text":"Synthetic reply before the final merge check"}`); err != nil {
			t.Fatal(err)
		}
		member := visibilityTestMergeMember(it)
		h.in.mu.Lock()
		h.in.configQueue = []assistantwire.InboxConfig{{}, {}, {}, {}, {}}
		reads := 0
		h.in.configHook = func() {
			reads++
			if reads == 5 {
				linkVisibilityTestMerge(h, it, member)
			}
		}
		h.in.mu.Unlock()
		if _, err := h.actOnSuggestion(t.Context(), it.ID, suggestionReq{Version: 1, Action: suggestConfirm}, true); err == nil {
			t.Fatal("a private member introduced before the final send check was ignored")
		}
		if len(lark.got) != 0 {
			t.Fatalf("the final mixed-merge check called the sender: %+v", lark.got)
		}
	})
}

func TestInboxVisibilityLegacyMirroredFinalSend(t *testing.T) {
	h, lark, it := handledHub(t)
	it.Source = "lark"
	it.Lark.FeedID = "synthetic-private"
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"Synthetic old-client mirrored reply"}`); err != nil {
		t.Fatal(err)
	}
	g := mirroredLegacyReply(it)
	if g == nil {
		t.Fatal("test setup did not expose an old-client mirrored draft")
	}
	// The route, pre-send policy snapshot and edit veto each take a fresh read first. Exclusion arrives only at the
	// final fresh read, so none of those earlier gates can mask the legacy mirror's own final-send authorization.
	h.in.mu.Lock()
	h.in.configQueue = []assistantwire.InboxConfig{{}, {}, {}, {Visibility: visibilityTestConfig()}}
	h.in.mu.Unlock()
	if _, err := h.replyInboxChecked(t.Context(), it.ID, g.Text, "", false, nil, nil); err == nil {
		t.Fatal("an old-client mirrored reply bypassed the final fresh visibility gate")
	}
	if len(lark.got) != 0 {
		t.Fatalf("denied old-client mirrored reply called the sender: %+v", lark.got)
	}
}

func TestInboxVisibilityGroupingFiltersHistoricalMixedMerge(t *testing.T) {
	h := newInboxHub(t)
	survivor := actionItem("lark:F1:1051", time.Now())
	survivor.Source = "lark"
	survivor.Lark.FeedID = "F1"
	survivor.Title = "SECRET-MIXED-MERGE-GROUP-TITLE"
	survivor.Group = &InboxGroup{Connector: "lark", Thread: "chat:F1"}
	linkVisibilityTestMerge(h, survivor, visibilityTestMergeMember(survivor))
	events := h.groupEventsWithVisibility("lark", "chat:F1", "", nil)
	for _, event := range events {
		if event.ID == survivor.ID || strings.Contains(event.Title, "SECRET-MIXED-MERGE") {
			t.Fatalf("a quarantined merged member kept its survivor in grouping: %+v", event)
		}
	}
}

func TestInboxVisibilityTaskNotificationsFailClosed(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		t.Run(map[bool]string{false: "current configuration", true: "persistent marker"}[persistent], func(t *testing.T) {
			s, f := newQuietService(t)
			in := newFakeInbox()
			f.inbox, in.svc = in, s
			h := &inboxHub{Service: s, f: f, in: in}
			if err := h.inboxReady(t.Context()); err != nil {
				t.Fatal(err)
			}
			turnInboxSignalsOn(t, s)
			todo := supervisedTodo(t, s, "Synthetic supervised task")
			it := linkedItem(s, "lark:synthetic-private:task-notification", todo.ID, "12", "SECRET-TASK-NOTIFICATION", 1)
			it.Lark = &InboxLark{FeedID: "synthetic-private"}
			it.VisibilityExcluded = persistent
			if !persistent {
				h.setConfig(func(c *assistantwire.InboxConfig) { c.Visibility = visibilityTestConfig() })
			}
			h.inbox.mu.Lock()
			h.inbox.items[it.ID] = it
			h.inbox.mu.Unlock()

			s.superviseInbox(t.Context(), []*InboxItem{it}, map[*InboxItem]bool{it: true})
			if g, ok := pendingInbox(s, todo.ID); ok {
				t.Fatalf("excluded item raised a task notification: %+v", g.Inbox)
			}
			// Exercise the direct Agent delivery entry too. A denied delivery must not fall back to a supervisor signal.
			s.inboxToAgent(t.Context(), superviseSignal{Todo: todo.ID, Kind: sigInbox, Key: "synthetic-private-notification",
				Inbox: &prompts.SuperviseInbox{Item: it.ID, Preview: "SECRET-TASK-NOTIFICATION"}})
			if g, ok := pendingInbox(s, todo.ID); ok {
				t.Fatalf("denied Agent delivery fell back to a task notification: %+v", g.Inbox)
			}
			if got := f.notices(); len(got) != 0 {
				t.Fatalf("excluded task content produced notices: %q", got)
			}
		})
	}
}

func TestInboxVisibilityPendingDeliveryAfterQuarantineRollback(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("quarantine rollback/pending-delivery authorization panicked: %v", recovered)
		}
	}()
	h, _, it := handledHub(t)
	it.Source = "lark"
	it.Lark.FeedID = "synthetic-private"
	h.originSet(t.Context(), "server", "s1", OriginInbox, it.ID, "")
	h.inbox.mu.Lock()
	h.inbox.tell(it, inboxNote{Name: prompts.NoteNewMessages, Context: "SECRET-ROLLED-BACK-PENDING-NOTE"}, true, time.Now())
	h.inbox.mu.Unlock()
	h.setConfig(func(c *assistantwire.InboxConfig) { c.Visibility = visibilityTestConfig() })
	h.st.imu.Lock()
	h.st.down = errors.New("synthetic inbox write outage")
	h.st.imu.Unlock()

	// The failed quarantine write restores the old session and pending notes. Keep the store failing so the delivery
	// pass reaches its own authorization check rather than being saved by a later successful quarantine.
	h.quarantineInboxVisibility(t.Context(), visibilityTestConfig())
	if it.VisibilityExcluded || it.Agent.SessionID == "" || len(it.Agent.Pending) == 0 {
		t.Fatalf("test setup did not exercise rollback: excluded=%v agent=%+v", it.VisibilityExcluded, it.Agent)
	}
	h.deliverNotes(t.Context(), it)
	if got := notesOf(h.f, "s1"); len(got) != 0 {
		t.Fatalf("rolled-back private pending notes were delivered: %q", got)
	}
}

func TestInboxVisibilityOrdinaryTaskJudgeFailsClosed(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		t.Run(map[bool]string{false: "current configuration", true: "persistent marker"}[persistent], func(t *testing.T) {
			h := agentHub(t)
			model, calls := captureModel(t, `{"task_id":"synthetic-task","evidence":"Synthetic task evidence"}`)
			h.in.setModel(model)
			it := actionItem("lark:synthetic-private:ordinary-judge", time.Now())
			it.Source = "lark"
			it.Lark.FeedID = "synthetic-private"
			it.Context = "SECRET-ORDINARY-TASK-JUDGE-CONTEXT"
			it.Agent = &InboxAgent{State: AgentQueued}
			it.VisibilityExcluded = persistent
			h.inbox.items[it.ID] = it
			if !persistent {
				h.setConfig(func(c *assistantwire.InboxConfig) { c.Visibility = visibilityTestConfig() })
			}
			id, err := h.judgeInboxTask(t.Context(), it, []prompts.InboxTask{{ID: "synthetic-task", Title: "Synthetic task evidence", State: "active"}})
			if err != nil || id != "" {
				t.Fatalf("excluded ordinary judge returned id=%q err=%v", id, err)
			}
			if got := len(calls()); got != 0 {
				t.Fatalf("excluded ordinary item reached the task judge %d times", got)
			}
		})
	}
}

func TestInboxVisibilityTriagePreviewFailsBeforeModel(t *testing.T) {
	for _, tc := range []struct {
		name     string
		source   string
		thing    connector.InboxThing
		provider bool
	}{
		{name: "explicit private chat", source: "lark", thing: connector.InboxThing{Thread: "chat:synthetic-private", Kind: KindDM,
			Context: "SECRET-DRY-RUN-PRIVATE", Last: "1", At: time.Now()}},
		{name: "unsupported provider", source: "acme", thing: connector.InboxThing{Thread: "opaque", Kind: KindGroup,
			Context: "SECRET-DRY-RUN-UNSUPPORTED", Last: "1", At: time.Now()}},
		{name: "declared provider remains usable", source: "acme", provider: true, thing: connector.InboxThing{Thread: "opaque", Kind: KindGroup,
			Context: "Ordinary dry-run content", Last: "1", At: time.Now(), Tags: []connector.Tag{{Key: connector.TagChat, Value: "synthetic-allowed"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newInboxHub(t)
			model, calls := captureModel(t, `{"tier":"noise","summary":"Synthetic summary","reason":"Synthetic reason"}`)
			h.in.setModel(model)
			if tc.provider {
				h.SetExtensions(&visibilityProviderExtensions{policyExtensions: &policyExtensions{}, providers: map[string]bool{"acme": true}})
			}
			h.setConfig(func(c *assistantwire.InboxConfig) {
				c.Visibility = &assistantwire.InboxVisibilityConfig{Sources: []string{tc.source}, Chats: []string{"synthetic-private"}}
			})
			verdicts := h.InboxTriagePreview(t.Context(), assistantwire.InboxTriagePreview{Source: tc.source, Things: []connector.InboxThing{tc.thing}})
			if len(verdicts) != 1 {
				t.Fatalf("preview verdicts: %+v", verdicts)
			}
			wantCalls := 0
			if tc.provider {
				wantCalls = 1
				if verdicts[0].State == "error" {
					t.Fatalf("declared provider could not preview an allowed chat: %+v", verdicts[0])
				}
			} else if verdicts[0].State != "error" {
				t.Fatalf("denied dry-run verdict: %+v", verdicts[0])
			}
			if got := len(calls()); got != wantCalls {
				t.Fatalf("dry-run model calls=%d, want %d", got, wantCalls)
			}
		})
	}
}

func TestInboxVisibilityMailTranslationReadsNoBody(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		t.Run(map[bool]string{false: "current configuration", true: "persistent marker"}[persistent], func(t *testing.T) {
			h, en, _, translated := translateHub(t)
			en.VisibilityExcluded = persistent
			if !persistent {
				h.setConfig(func(c *assistantwire.InboxConfig) {
					c.Visibility = &assistantwire.InboxVisibilityConfig{Sources: []string{"mail"}, Chats: []string{"synthetic-private"}}
				})
			}
			h.translateInbox(t.Context())
			h.passOver()
			h.in.mu.Lock()
			reads := h.in.mail.reads
			h.in.mu.Unlock()
			if reads != 0 || translated() != 0 {
				t.Fatalf("excluded mail translation read %d bodies and made %d model calls", reads, translated())
			}
			if en.Translations != nil {
				t.Fatalf("excluded mail acquired a translation: %+v", en.Translations)
			}
		})
	}
}

func TestInboxVisibilityFolderPathsAndCountsExcludeMergedMembers(t *testing.T) {
	h := newInboxHub(t)
	model, calls := captureModel(t, `{"folders":{}}`)
	h.in.setModel(model)
	h.setConfig(func(c *assistantwire.InboxConfig) { c.Visibility = visibilityTestConfig() })
	now := time.Now()
	allowed := actionItem("lark:synthetic-allowed:folder-count", now)
	allowed.Source = "lark"
	allowed.Lark.FeedID = "synthetic-allowed"
	allowed.Folder = ""
	allowed.FolderOf = ""
	private := actionItem("lark:synthetic-private:folder-count", now)
	private.Source = "lark"
	private.Lark.FeedID = "synthetic-private"
	private.Folder = "SECRET-PRIVATE-FOLDER"
	memberSurvivor := actionItem("lark:synthetic-allowed:merged-folder-count", now)
	memberSurvivor.Source = "lark"
	memberSurvivor.Lark.FeedID = "synthetic-allowed"
	memberSurvivor.Folder = "ordinary/folder"
	ordinary := actionItem("lark:synthetic-allowed:ordinary-folder-count", now)
	ordinary.Source = "lark"
	ordinary.Lark.FeedID = "synthetic-allowed"
	ordinary.Folder = "ordinary/folder"
	h.inbox.items[allowed.ID], h.inbox.items[private.ID], h.inbox.items[ordinary.ID] = allowed, private, ordinary
	linkVisibilityTestMerge(h, memberSurvivor, visibilityTestMergeMember(memberSurvivor))

	out, err := h.toolInboxFolders(t.Context(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "SECRET-PRIVATE-FOLDER") || !strings.Contains(out, "ordinary/folder (1, 1 open)") {
		t.Fatalf("folder paths or counts include excluded items: %s", out)
	}
	if err := h.fileBatch(t.Context(), []*InboxItem{allowed}); err != nil {
		t.Fatal(err)
	}
	got := calls()
	if len(got) != 1 {
		t.Fatalf("allowed folder classification made %d model calls", len(got))
	}
	if strings.Contains(got[0].User, "SECRET-PRIVATE-FOLDER") || strings.Contains(got[0].User, "SECRET-MERGED-MEMBER") {
		t.Fatalf("excluded folder metadata reached the model: %s", got[0].User)
	}
}

func TestInboxVisibilityQuarantineRetainsAuditAcrossReload(t *testing.T) {
	h, lark, sent := handledHub(t)
	if _, err := suggestTool(t, h, `{"kind":"reply","text":"Synthetic sent reply retained for audit"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.actOnSuggestion(t.Context(), sent.ID, suggestionReq{Version: 1, Action: suggestConfirm}, true); err != nil {
		t.Fatal(err)
	}
	if len(lark.got) != 1 {
		t.Fatalf("test setup sent %d replies", len(lark.got))
	}
	sent.Source = "lark"
	sent.Lark.FeedID = "synthetic-private"
	sent.Context = "SECRET-SENT-BODY-RETAINED"
	unsent := actionItem("lark:synthetic-private:unsent-retained", time.Now())
	unsent.Source = "lark"
	unsent.Lark.FeedID = "synthetic-private"
	unsent.Context = "SECRET-UNSENT-BODY-RETAINED"
	unsent.Suggestion = &InboxSuggestion{Version: 1, Kind: SuggestReply, Text: "Synthetic unsent suggestion retained"}
	h.inbox.items[unsent.ID] = unsent
	if failed := h.saveInbox(t.Context(), []*InboxItem{sent, unsent}); len(failed) != 0 {
		t.Fatalf("test setup could not save retained rows: %+v", failed)
	}
	h.quarantineInboxVisibility(t.Context(), visibilityTestConfig())

	next := newInboxHub(t)
	st := h.st
	next.setStore(func() Store { return st })
	next.setConfig(func(c *assistantwire.InboxConfig) { c.Visibility = visibilityTestConfig() })
	if err := next.inboxReady(t.Context()); err != nil {
		t.Fatal(err)
	}
	next.inbox.mu.Lock()
	loadedSent, loadedUnsent := next.inbox.items[sent.ID], next.inbox.items[unsent.ID]
	next.inbox.mu.Unlock()
	if loadedSent == nil || loadedUnsent == nil {
		t.Fatalf("quarantine deleted rows before reload: sent=%v unsent=%v", loadedSent != nil, loadedUnsent != nil)
	}
	if !loadedSent.VisibilityExcluded || !loadedUnsent.VisibilityExcluded || loadedSent.Context != sent.Context || loadedUnsent.Context != unsent.Context {
		t.Fatalf("reload lost quarantine or source bodies: sent=%+v unsent=%+v", loadedSent, loadedUnsent)
	}
	if currentSuggestion(loadedUnsent) == nil || currentSuggestion(loadedUnsent).Text != unsent.Suggestion.Text {
		t.Fatalf("reload lost the unsent suggestion: %+v", loadedUnsent.Suggestion)
	}
	attempt := suggestionAttempt(loadedSent, 1)
	if attempt == nil || attempt.Status != suggestionSent || attempt.Text != "Synthetic sent reply retained for audit" {
		t.Fatalf("reload lost the sent-attempt audit: %+v", loadedSent.SuggestionAttempts)
	}
	if len(lark.got) != 1 {
		t.Fatalf("reload re-sent retained history: %+v", lark.got)
	}
}
