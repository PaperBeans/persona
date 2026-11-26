package extmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/larksyncwire"
)

// larkSyncExtStore combines extensionStore (extension records, secrets, settings) with larkSyncMemStore (cursors,
// dedup index, outbox) so real extension processes can receive events emitted directly by larkSyncRunHeld.
type larkSyncExtStore struct {
	*extensionStore
	sync *larkSyncMemStore
}

var _ larkSyncStore = (*larkSyncExtStore)(nil)

func (s *larkSyncExtStore) LarkSyncCursors(ctx context.Context, binding string) (map[string]string, error) {
	return s.sync.LarkSyncCursors(ctx, binding)
}

func (s *larkSyncExtStore) DeleteLarkSyncCursors(ctx context.Context, binding string) error {
	return s.sync.DeleteLarkSyncCursors(ctx, binding)
}

func (s *larkSyncExtStore) LarkSyncIndex(ctx context.Context, keys []string) (map[string]larksyncwire.IndexRecord, error) {
	return s.sync.LarkSyncIndex(ctx, keys)
}

func (s *larkSyncExtStore) CommitLarkSyncRun(ctx context.Context, binding string, cursors map[string]string, recs []larksyncwire.IndexRecord, outbox []db.LarkSyncOutbox) error {
	return s.sync.CommitLarkSyncRun(ctx, binding, cursors, recs, outbox)
}

func (s *larkSyncExtStore) LarkSyncOutboxAfter(ctx context.Context, kind string, after int64, limit int) ([]db.LarkSyncOutbox, error) {
	return s.sync.LarkSyncOutboxAfter(ctx, kind, after, limit)
}

func (s *larkSyncExtStore) LarkSyncDocsSince(ctx context.Context, since time.Time) ([]db.LarkSyncOutbox, error) {
	return s.sync.LarkSyncDocsSince(ctx, since)
}

func (s *larkSyncExtStore) PruneLarkSyncOutbox(ctx context.Context, before time.Time) (int64, error) {
	return s.sync.PruneLarkSyncOutbox(ctx, before)
}

type staticLarkSyncer struct {
	res larksyncwire.Result
	err error
}

func (s *staticLarkSyncer) Run(context.Context, larksyncwire.Binding, func(context.Context) (string, error), map[string]string, larksyncwire.Index) (larksyncwire.Result, error) {
	return s.res, s.err
}

func (s *staticLarkSyncer) Media(context.Context, larksyncwire.Binding, func(context.Context) (string, error), string, int64) ([]byte, string, error) {
	return nil, "", nil
}

// lark.messages reaches a running extension only when it subscribed to the topic AND was explicitly granted the
// named-only lark.messages.read scope. kb.read, no permissions, and legacy hub_api are not enough. Message bodies,
// chat names, sender names and attachments are never carried in the event payload.
func TestLarkMessagesEventsReachOnlyAuthorizedExtensions(t *testing.T) {
	now := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name        string
		permissions string
		subscribe   []string
		allowed     bool
	}{
		{"lark-reader", `{"hub":["lark.messages.read"]}`, []string{"lark.messages", "agent.finished"}, true},
		{"kb-reader", `{"hub":["kb.read"]}`, []string{"lark.messages", "agent.finished"}, false},
		{"none", `{}`, []string{"lark.messages", "agent.finished"}, false},
		{"legacy-hub-api", `{"hub_api":true}`, []string{"lark.messages", "agent.finished"}, false},
		{"authorized-not-subscribed", `{"hub":["lark.messages.read"]}`, []string{"agent.finished"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestManager(t)
			extStore := newExtensionStore()
			syncStore := newLarkSyncMemStore(func() time.Time { return now })
			h.store = &larkSyncExtStore{extensionStore: extStore, sync: syncStore}
			h.cfg.Bots.Feishu = FeishuBotConfig{AppID: "cli_example", AppSecret: "example-secret", Domain: "feishu"}
			h.bots.sync.now = func() time.Time { return now }
			h.bots.sync.userAuth = &fakeUserAuth{now: func() time.Time { return now }, exchange: map[string]larksyncwire.Token{}}
			startTestExtensions(t, h)

			bindingKey := larksyncwire.PrimaryBot + "~feishu:tenant_example:ou_owner_example"
			accEnc, err := h.larkSyncSeal("access-example")
			if err != nil {
				t.Fatal(err)
			}
			refEnc, err := h.larkSyncSeal("refresh-example")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := h.larkSyncChange(context.Background(), func(st *larkSyncState) error {
				st.accounts = []larkSyncAccount{{AccountID: "feishu:tenant_example:ou_owner_example", Label: "Work", Name: "Owner"}}
				st.bindings = []larkSyncBinding{{
					Key:                  bindingKey,
					BotID:                larksyncwire.PrimaryBot,
					AccountID:            "feishu:tenant_example:ou_owner_example",
					Tier:                 larksyncwire.TierFull,
					Enabled:              true,
					AuthorizedAt:         now.Add(-time.Hour),
					TokenExpiresAt:       now.Add(2 * time.Hour),
					RefreshExpiresAt:     now.Add(30 * 24 * time.Hour),
					AccessTokenEnc:       accEnc,
					RefreshTokenEnc:      refEnc,
					BackfillVersion:      larkSyncBackfillVersion,
					LinksBackfillVersion: larkSyncLinksBackfillVersion,
				}}
				return nil
			}); err != nil {
				t.Fatal(err)
			}

			mux := h.routes()
			token := h.cfg.Token
			dir := uiPackage(t)
			path := filepath.Join(dir, extension.ManifestFile)
			raw, _ := os.ReadFile(path)
			var manifest map[string]any
			_ = json.Unmarshal(raw, &manifest)
			var permissions map[string]any
			_ = json.Unmarshal([]byte(tc.permissions), &permissions)
			permissions["launcher_keys"] = true
			manifest["permissions"] = permissions
			manifest["contributes"].(map[string]any)["events"] = []any{map[string]any{
				"id":        "lark",
				"subscribe": tc.subscribe,
			}}
			raw, _ = json.Marshal(manifest)
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			code, pv := call(t, mux, "POST", "/api/v1/extensions/-/preview", token, `{"source":"dir:`+dir+`"}`)
			if code != 200 {
				t.Fatalf("preview %d %v", code, pv)
			}
			code, out := call(t, mux, "POST", "/api/v1/extensions/-/install", token, fmt.Sprintf(`{"preview_id":%q,"permissions_digest":%q}`, pv["preview_id"], pv["permissions_digest"]))
			if code != 200 {
				t.Fatalf("install %d %v", code, out)
			}
			waitLong(t, "lark subscriber running", func() bool {
				p := h.extensions.get("example-ui")
				return p != nil && p.proc != nil && p.proc.Ready()
			})

			t1 := now.Add(-2 * time.Minute)
			t2 := now.Add(-1 * time.Minute)
			h.bots.sync.newSyncer = func() larksyncwire.Syncer {
				return &staticLarkSyncer{res: larksyncwire.Result{
					Messages: []larksyncwire.Message{
						{
							ID:         "om_example_2",
							ChatID:     "oc_example_chat_2",
							ChatName:   "Private Project Group",
							ChatType:   "group",
							SenderID:   "ou_sender_2",
							SenderName: "Secret Person Two",
							Type:       "interactive",
							Text:       "Secret meeting body two",
							At:         t2,
							Files:      []larksyncwire.Attachment{{Ref: "ref_secret", Name: "notes.pdf", Kind: "file"}},
						},
						{
							ID:         "om_example_1",
							ChatID:     "oc_example_chat_1",
							ChatName:   "Private Direct Chat",
							ChatType:   "p2p",
							SenderID:   "ou_sender_1",
							SenderName: "Secret Person One",
							Type:       "text",
							Text:       "Secret message body one",
							At:         t1,
						},
					},
					Cursors: map[string]string{"im:oc_example_chat_1": "1700000001000"},
				}}
			}

			if err := h.larkSyncRunMode(context.Background(), bindingKey, larksyncwire.ModeIM); err != nil {
				t.Fatalf("larkSyncRunMode: %v", err)
			}
			waitEventQueuesEmpty(t, h)

			view := func() string {
				_, out := call(t, mux, "POST", "/api/v1/extensions/example-ui/-/ui/home", token, `{}`)
				return fmt.Sprint(out["view"])
			}

			// Emit agent.finished as an in-order sentinel on the same extension stdio stream.
			h.extensions.emit("agent.finished", map[string]any{})
			if !tc.allowed {
				waitLong(t, "sentinel event delivered", func() bool { return strings.Contains(view(), "value:1") })
				if v := view(); strings.Contains(v, "lark.messages") || strings.Contains(v, "om_example_1") || strings.Contains(v, "oc_example_chat_1") {
					t.Fatalf("lark.messages reached an unauthorized/unsubscribed extension: %s", v)
				}
				return
			}

			// One single batched event for the 2 messages + 1 sentinel = value:2.
			waitLong(t, "lark.messages and sentinel delivered", func() bool { return strings.Contains(view(), "value:2") })
			v := view()
			for _, want := range []string{
				`<lark.messages `,
				`"binding":"` + bindingKey + `"`,
				`"page":1`,
				`"pages":1`,
				`"total":2`,
				`"chat_id":"oc_example_chat_1"`,
				`"message_id":"om_example_1"`,
				`"sender_open_id":"ou_sender_1"`,
				`"type":"text"`,
				`"at":"` + t1.Format(time.RFC3339) + `"`,
				`"chat_id":"oc_example_chat_2"`,
				`"message_id":"om_example_2"`,
				`"sender_open_id":"ou_sender_2"`,
				`"type":"interactive"`,
				`"at":"` + t2.Format(time.RFC3339) + `"`,
			} {
				if !strings.Contains(v, want) {
					t.Fatalf("lark.messages lacks %s: %s", want, v)
				}
			}
			// Strictly no message body, chat names, sender names, or attachment metadata.
			for _, leak := range []string{
				"Secret message body one",
				"Secret meeting body two",
				"Private Direct Chat",
				"Private Project Group",
				"Secret Person One",
				"Secret Person Two",
				"notes.pdf",
				"ref_secret",
				`"text":`,
				`"chat_name":`,
				`"sender_name":`,
				`"files":`,
			} {
				if strings.Contains(v, leak) {
					t.Fatalf("lark.messages leaked %s: %s", leak, v)
				}
			}
			// Chronological ordering: om_example_1 (t1) precedes om_example_2 (t2).
			if strings.Index(v, `"om_example_1"`) > strings.Index(v, `"om_example_2"`) {
				t.Fatalf("messages not ordered chronologically: %s", v)
			}

			// Disabling the contribution or the extension stops further delivery.
			if err := h.extensions.reg.SetContribution(context.Background(), "example-ui", "events", "lark", false, "test"); err != nil {
				t.Fatal(err)
			}
			if ids := h.extensions.reg.EventSubscribers(larksyncwire.EventLarkMessages, extension.EventScopes[larksyncwire.EventLarkMessages]); len(ids) != 0 {
				t.Fatalf("disabled contribution still subscribed: %v", ids)
			}
			if err := h.extensions.reg.SetContribution(context.Background(), "example-ui", "events", "lark", true, "test"); err != nil {
				t.Fatal(err)
			}
			if err := h.extensions.reg.SetEnabled(context.Background(), "example-ui", false, "test"); err != nil {
				t.Fatal(err)
			}
			if ids := h.extensions.reg.EventSubscribers(larksyncwire.EventLarkMessages, extension.EventScopes[larksyncwire.EventLarkMessages]); len(ids) != 0 {
				t.Fatalf("disabled extension still subscribed: %v", ids)
			}
		})
	}
}

// Each sync round emits one batched event for small batches, paginates large batches at larkMessagesPageSize, emits
// nothing when there are no new messages or when the run/commit fails, does not emit for sender-name backfill
// repairs, and keeps its own event queue isolated from KB events.
func TestLarkSyncRunEmitsBatchedAndPaginatedLarkMessages(t *testing.T) {
	w := newLarkWorld(t)
	ctx := context.Background()
	w.h.extensions = &extensions{h: w.h}
	holdEvents(&w.h.extensions.events)
	holdEvents(&w.h.extensions.larkEvents)

	drainLarkEvents := func() []larksyncwire.MessagesEvent {
		q := &w.h.extensions.larkEvents
		q.mu.Lock()
		pending := append([]extensionNotice(nil), q.pending...)
		q.pending = nil
		q.mu.Unlock()
		out := make([]larksyncwire.MessagesEvent, 0, len(pending))
		for _, n := range pending {
			if n.topic != larksyncwire.EventLarkMessages {
				t.Fatalf("unexpected topic in larkEvents: %q", n.topic)
			}
			raw, err := json.Marshal(n.payload)
			if err != nil {
				t.Fatal(err)
			}
			var ev larksyncwire.MessagesEvent
			if err := json.Unmarshal(raw, &ev); err != nil {
				t.Fatal(err)
			}
			out = append(out, ev)
		}
		return out
	}

	w.addSyncBot(t, "bsync", "cli_sync", larksyncwire.TierFull)
	key, acc := w.adopt(t, "bsync", "ou_user", "acc-1", "ref-1", 24*time.Hour)
	if _, err := w.h.larkSyncChange(ctx, func(st *larkSyncState) error {
		b := st.binding(key)
		b.Tier = larksyncwire.TierFull
		b.BackfillVersion = larkSyncBackfillVersion
		b.LinksBackfillVersion = larkSyncLinksBackfillVersion
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 1. Small batch (3 messages across 2 chats) -> 1 single event, NOT 3 events.
	base := w.now()
	w.script("bsync", acc, func(context.Context, larksyncwire.Binding, func(context.Context) (string, error), map[string]string, larksyncwire.Index) (larksyncwire.Result, error) {
		return larksyncwire.Result{
			Messages: []larksyncwire.Message{
				{ID: "om_3", ChatID: "oc_b", SenderID: "ou_3", Type: "post", Text: "body 3", At: base.Add(3 * time.Second)},
				{ID: "om_1", ChatID: "oc_a", SenderID: "ou_1", Type: "text", Text: "body 1", At: base.Add(1 * time.Second)},
				{ID: "om_2", ChatID: "oc_a", SenderID: "ou_2", Type: "image", Text: "body 2", At: base.Add(2 * time.Second)},
			},
			Cursors: map[string]string{"im:oc_a": "1000"},
		}, nil
	})
	if err := w.h.larkSyncRunMode(ctx, key, larksyncwire.ModeIM); err != nil {
		t.Fatalf("small batch run: %v", err)
	}
	evs := drainLarkEvents()
	if len(evs) != 1 {
		t.Fatalf("small batch emitted %d events, want 1", len(evs))
	}
	if evs[0].Binding != key || evs[0].Page != 1 || evs[0].Pages != 1 || evs[0].Total != 3 || len(evs[0].Messages) != 3 {
		t.Fatalf("unexpected small batch event: %+v", evs[0])
	}
	if evs[0].Messages[0].MessageID != "om_1" || evs[0].Messages[1].MessageID != "om_2" || evs[0].Messages[2].MessageID != "om_3" {
		t.Fatalf("messages not sorted chronologically: %+v", evs[0].Messages)
	}
	if evs[0].Messages[0].Type != larksyncwire.MessageTypeText || evs[0].Messages[1].Type != larksyncwire.MessageTypeImage || evs[0].Messages[2].Type != larksyncwire.MessageTypePost {
		t.Fatalf("unexpected message types: %+v", evs[0].Messages)
	}

	// 2. Large batch (250 messages > larkMessagesPageSize=100) -> 3 paginated events (100, 100, 50).
	const largeCount = 250
	w.script("bsync", acc, func(context.Context, larksyncwire.Binding, func(context.Context) (string, error), map[string]string, larksyncwire.Index) (larksyncwire.Result, error) {
		msgs := make([]larksyncwire.Message, 0, largeCount)
		for i := range largeCount {
			msgs = append(msgs, larksyncwire.Message{
				ID:       fmt.Sprintf("om_big_%03d", i),
				ChatID:   fmt.Sprintf("oc_chat_%d", i%5),
				SenderID: fmt.Sprintf("ou_sender_%d", i%7),
				Type:     "text",
				Text:     "body must not be emitted",
				At:       base.Add(time.Duration(i+1) * time.Second),
			})
		}
		return larksyncwire.Result{Messages: msgs, Cursors: map[string]string{"im:oc_chat_0": "2000"}}, nil
	})
	if err := w.h.larkSyncRunMode(ctx, key, larksyncwire.ModeFull); err != nil {
		t.Fatalf("large batch run: %v", err)
	}
	evs = drainLarkEvents()
	if len(evs) != 3 {
		t.Fatalf("large batch of %d emitted %d pages, want 3", largeCount, len(evs))
	}
	wantLens := []int{100, 100, 50}
	seen := 0
	for i, ev := range evs {
		if ev.Binding != key || ev.Page != i+1 || ev.Pages != 3 || ev.Total != largeCount || len(ev.Messages) != wantLens[i] {
			t.Fatalf("page %d header/size mismatch: %+v", i+1, ev)
		}
		for _, item := range ev.Messages {
			wantID := larksyncwire.MessageID(fmt.Sprintf("om_big_%03d", seen))
			if item.MessageID != wantID {
				t.Fatalf("item %d got %q, want %q", seen, item.MessageID, wantID)
			}
			seen++
		}
	}

	// 3. Zero new messages -> 0 events.
	w.script("bsync", acc, func(context.Context, larksyncwire.Binding, func(context.Context) (string, error), map[string]string, larksyncwire.Index) (larksyncwire.Result, error) {
		return larksyncwire.Result{Cursors: map[string]string{"im:oc_a": "3000"}}, nil
	})
	if err := w.h.larkSyncRunMode(ctx, key, larksyncwire.ModeIM); err != nil {
		t.Fatalf("empty run: %v", err)
	}
	if evs := drainLarkEvents(); len(evs) != 0 {
		t.Fatalf("empty run emitted %d events, want 0", len(evs))
	}

	// 4. Commit failure -> 0 events (nothing committed to outbox).
	w.store.commitErr = errors.New("db unavailable")
	w.script("bsync", acc, func(context.Context, larksyncwire.Binding, func(context.Context) (string, error), map[string]string, larksyncwire.Index) (larksyncwire.Result, error) {
		return larksyncwire.Result{
			Messages: []larksyncwire.Message{{ID: "om_fail_commit", ChatID: "oc_a", SenderID: "ou_1", Type: "text", At: base}},
		}, nil
	})
	if err := w.h.larkSyncRunMode(ctx, key, larksyncwire.ModeIM); err == nil {
		t.Fatal("expected commit error")
	}
	w.store.commitErr = nil
	if evs := drainLarkEvents(); len(evs) != 0 {
		t.Fatalf("failed commit emitted %d events, want 0", len(evs))
	}

	// 5. Run failure -> 0 events.
	w.script("bsync", acc, func(context.Context, larksyncwire.Binding, func(context.Context) (string, error), map[string]string, larksyncwire.Index) (larksyncwire.Result, error) {
		return larksyncwire.Result{
			Messages: []larksyncwire.Message{{ID: "om_fail_run", ChatID: "oc_a", SenderID: "ou_1", Type: "text", At: base}},
		}, &larksyncwire.Error{Kind: larksyncwire.ErrKindTransient, Op: "im", Msg: "boom"}
	})
	if err := w.h.larkSyncRunMode(ctx, key, larksyncwire.ModeIM); err == nil {
		t.Fatal("expected run error")
	}
	if evs := drainLarkEvents(); len(evs) != 0 {
		t.Fatalf("failed run emitted %d events, want 0", len(evs))
	}

	// 6. Sender-name backfill repair alone does not emit lark.messages.
	if _, err := w.h.larkSyncChange(ctx, func(st *larkSyncState) error {
		st.binding(key).BackfillVersion = 0
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	w.resolveNamesWithFallback = func(context.Context, larksyncwire.Binding, func(context.Context) (string, error), larksyncwire.NameResolveRequest) (larksyncwire.NameResolveResult, error) {
		return larksyncwire.NameResolveResult{UserNames: map[string]string{"ou_1": "Resolved Name"}}, nil
	}
	w.script("bsync", acc, func(context.Context, larksyncwire.Binding, func(context.Context) (string, error), map[string]string, larksyncwire.Index) (larksyncwire.Result, error) {
		return larksyncwire.Result{}, nil
	})
	if err := w.h.larkSyncRunMode(ctx, key, larksyncwire.ModeFull); err != nil {
		t.Fatalf("backfill run: %v", err)
	}
	if evs := drainLarkEvents(); len(evs) != 0 {
		t.Fatalf("sender-name backfill repair emitted %d events, want 0", len(evs))
	}

	// 7. Queue isolation: a flood of lark.messages pages leaves KB events untouched.
	w.h.AssistantSaid(assistantwire.EventKBChanged, json.RawMessage(`{"kind":"chats","id":"example-chat","what":"messages"}`))
	for i := range extensionEventLimit + 10 {
		w.h.emitLarkMessages(key, []larksyncwire.Message{{
			ID:       fmt.Sprintf("om_flood_%d", i),
			ChatID:   "oc_flood",
			SenderID: "ou_flood",
			Type:     "text",
			At:       base.Add(time.Duration(i) * time.Second),
		}})
	}
	w.h.extensions.events.mu.Lock()
	kbPending := len(w.h.extensions.events.pending)
	w.h.extensions.events.mu.Unlock()
	if kbPending != 1 {
		t.Fatalf("KB queue had %d pending after Lark event flood, want 1", kbPending)
	}
}

func TestLarkMessageEventPagesTypeNormalization(t *testing.T) {
	base := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	evs := larkMessageEventPages("primary~feishu:tenant_example:ou_owner", []larksyncwire.Message{
		{ID: "om_trimmed", ChatID: "oc_1", SenderID: "ou_1", Type: "  post  ", At: base},
		{ID: "om_custom", ChatID: "oc_1", SenderID: "ou_1", Type: "sticker", At: base.Add(time.Second)},
		{ID: "om_blank", ChatID: "oc_1", SenderID: "ou_1", Type: "   ", At: base.Add(2 * time.Second)},
	}, 10)
	if len(evs) != 1 || len(evs[0].Messages) != 3 {
		t.Fatalf("unexpected pages: %+v", evs)
	}
	if got := evs[0].Messages[0].Type; got != larksyncwire.MessageTypePost {
		t.Fatalf("trimmed type = %q, want %q", got, larksyncwire.MessageTypePost)
	}
	if got := evs[0].Messages[1].Type; got != larksyncwire.MessageType("sticker") {
		t.Fatalf("custom type = %q, want %q", got, "sticker")
	}
	if got := evs[0].Messages[2].Type; got != "" {
		t.Fatalf("blank type = %q, want empty", got)
	}

	fields := fieldsOf(evs[0])
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Messages) != 3 {
		t.Fatalf("decoded messages = %d, want 3", len(decoded.Messages))
	}
	if got := decoded.Messages[0]["type"]; got != "post" {
		t.Fatalf("decoded[0].type = %v, want %q", got, "post")
	}
	if got := decoded.Messages[1]["type"]; got != "sticker" {
		t.Fatalf("decoded[1].type = %v, want %q", got, "sticker")
	}
	if _, hasType := decoded.Messages[2]["type"]; hasType {
		t.Fatalf("blank message type must be omitted from JSON: %v", decoded.Messages[2])
	}
}
