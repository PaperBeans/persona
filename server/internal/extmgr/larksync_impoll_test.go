package extmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/kbwire"
	"github.com/nwverse/persona/server/internal/larksyncwire"
)

// Chat-enabled (full_with_im) bindings run a lightweight IM incremental sync every 15 minutes between daily full runs,
// reusing the same per-binding reader (and rate limiter), never overlapping a full run, respecting rate-limit backoff,
// and stopping when im_poll is turned off in Settings while the daily full run continues unchanged.
func TestLarkSyncIMPollScheduleToggleAndMutualExclusion(t *testing.T) {
	w := newLarkWorld(t)
	ctx := context.Background()

	w.addSyncBot(t, "bsync", "cli_sync", larksyncwire.TierFull)
	fullKey, fullAcc := w.adopt(t, "bsync", "ou_full", "acc-full", "ref-full", 24*time.Hour)
	docsKey, _ := w.adopt(t, larksyncwire.PrimaryBot, "ou_docs", "acc-docs", "ref-docs", 24*time.Hour)
	if _, err := w.h.larkSyncChange(ctx, func(st *larkSyncState) error {
		st.binding(fullKey).Tier = larksyncwire.TierFull
		st.binding(docsKey).Tier = larksyncwire.TierDocs
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var modes []string
	var nextRes larksyncwire.Result
	w.script("bsync", fullAcc, func(_ context.Context, b larksyncwire.Binding, _ func(context.Context) (string, error), _ map[string]string, _ larksyncwire.Index) (larksyncwire.Result, error) {
		mu.Lock()
		modes = append(modes, b.Mode)
		res := nextRes
		nextRes = larksyncwire.Result{}
		mu.Unlock()
		return res, nil
	})

	// Initial GET shows im_poll enabled by default.
	code, view := w.do(t, "GET", "/api/v1/bots/feishu/sync", "")
	if code != 200 || view["im_poll"] != true {
		t.Fatalf("initial view = %d %v, want im_poll=true", code, view)
	}

	// First tick runs the initial full sync for both bindings.
	w.h.larkSyncTick(ctx)
	fullNextDaily := w.binding(t, fullKey).NextRunAt
	if w.readers != 2 || !slices.Equal(modes, []string{larksyncwire.ModeFull}) {
		t.Fatalf("after first tick: readers=%d modes=%v", w.readers, modes)
	}

	// 10 minutes later: not yet due.
	w.advance(10 * time.Minute)
	w.h.larkSyncTick(ctx)
	if len(modes) != 1 {
		t.Fatalf("ran early at 10m: modes=%v", modes)
	}

	// 15 minutes after the full sync: only full_with_im runs ModeIM; docs_standard does not; same reader reused;
	// daily NextRunAt is untouched; unreadable Doc.Note reaches the outbox.
	w.advance(5 * time.Minute)
	mu.Lock()
	nextRes = larksyncwire.Result{
		Messages: []larksyncwire.Message{{ID: "om_inc", ChatID: "oc_1", ChatType: "p2p", Text: "hi", At: w.now()}},
		Docs: []larksyncwire.Doc{{
			Key:      larksyncwire.KeyDoc + "doxNoPerm",
			Kind:     "docs",
			ObjToken: "doxNoPerm",
			ObjType:  "docx",
			Title:    "https://my.feishu.cn/docx/doxNoPerm",
			URL:      "https://my.feishu.cn/docx/doxNoPerm",
			Note:     "unreadable: docx document (forbidden, code 1770032)",
			Revision: 0,
		}},
		Cursors: map[string]string{"im:oc_1": "1700000001000"},
	}
	mu.Unlock()
	docsBefore := countOf(w.ran, docsKey)
	w.h.larkSyncTick(ctx)
	if !slices.Equal(modes, []string{larksyncwire.ModeFull, larksyncwire.ModeIM}) {
		t.Fatalf("at 15m: modes=%v, want [full, im]", modes)
	}
	if countOf(w.ran, docsKey) != docsBefore {
		t.Fatalf("docs_standard binding ran during IM poll")
	}
	if w.readers != 2 {
		t.Fatalf("IM poll created a second reader (%d), must reuse the binding's reader/limiter", w.readers)
	}
	fb := w.binding(t, fullKey)
	if !fb.NextRunAt.Equal(fullNextDaily) {
		t.Fatalf("IM poll moved daily NextRunAt: got %v, want %v", fb.NextRunAt, fullNextDaily)
	}
	if !fb.NextIMRunAt.Equal(w.now().Add(larkSyncIMEvery)) {
		t.Fatalf("NextIMRunAt = %v, want %v", fb.NextIMRunAt, w.now().Add(larkSyncIMEvery))
	}
	items, err := larkSyncItems(larkSyncConnector{w.h}).Items(ctx)
	if err != nil || len(items) != 1 || items[0].Note != "unreadable: docx document (forbidden, code 1770032)" {
		t.Fatalf("outbox items = %+v, err = %v", items, err)
	}

	// Rate-limited IM poll backs off for larkSyncPartialNext (1 hour), not 15 minutes.
	w.advance(larkSyncIMEvery)
	mu.Lock()
	nextRes = larksyncwire.Result{
		Errors: []larksyncwire.ResourceError{{Resource: "im:oc_1", Kind: larksyncwire.ErrKindRateLimited, Detail: "429"}},
	}
	mu.Unlock()
	w.h.larkSyncTick(ctx)
	if fb := w.binding(t, fullKey); fb.Health != larksyncwire.HealthRateLimited || !fb.NextIMRunAt.Equal(w.now().Add(larkSyncPartialNext)) {
		t.Fatalf("rate-limited IM poll: health=%s nextIM=%v", fb.Health, fb.NextIMRunAt)
	}

	// Turning im_poll off in Settings stops 15-minute polls while leaving the daily full run intact.
	code, view = w.do(t, "PUT", "/api/v1/bots/feishu/sync", `{"im_poll":false}`)
	if code != 200 || view["im_poll"] != false {
		t.Fatalf("disable im_poll = %d %v", code, view)
	}
	w.advance(2 * time.Hour)
	beforeOff := len(modes)
	w.h.larkSyncTick(ctx)
	if len(modes) != beforeOff {
		t.Fatalf("IM poll ran while im_poll=false: modes=%v", modes)
	}

	// Reaching the daily slot still runs ModeFull even when im_poll is off.
	w.advance(w.binding(t, fullKey).NextRunAt.Sub(w.now()))
	w.h.larkSyncTick(ctx)
	if len(modes) != beforeOff+1 || modes[len(modes)-1] != larksyncwire.ModeFull {
		t.Fatalf("daily full run with im_poll=false: modes=%v", modes)
	}

	// Turning im_poll back on resumes 15-minute polls.
	code, view = w.do(t, "PUT", "/api/v1/bots/feishu/sync", `{"im_poll":true}`)
	if code != 200 || view["im_poll"] != true {
		t.Fatalf("re-enable im_poll = %d %v", code, view)
	}
	w.advance(larkSyncIMEvery)
	w.h.larkSyncTick(ctx)
	if modes[len(modes)-1] != larksyncwire.ModeIM {
		t.Fatalf("IM poll did not resume after re-enabling: modes=%v", modes)
	}

	// Mutual exclusion: while a full run holds rt.running, a concurrent IM poll is refused with errLarkSyncRunning.
	started := make(chan struct{})
	release := make(chan struct{})
	w.script("bsync", fullAcc, func(_ context.Context, _ larksyncwire.Binding, _ func(context.Context) (string, error), _ map[string]string, _ larksyncwire.Index) (larksyncwire.Result, error) {
		close(started)
		<-release
		return larksyncwire.Result{}, nil
	})
	done := make(chan error, 1)
	go func() { done <- w.h.larkSyncRunMode(ctx, fullKey, larksyncwire.ModeFull) }()
	<-started
	if err := w.h.larkSyncRunMode(ctx, fullKey, larksyncwire.ModeIM); !errors.Is(err, errLarkSyncRunning) {
		close(release)
		t.Fatalf("concurrent ModeIM during ModeFull returned %v, want errLarkSyncRunning", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("held ModeFull failed: %v", err)
	}
}

// In ModeIM, a partial run that still made forward progress (delivered messages or moved a cursor) keeps the
// 15-minute cadence (larkSyncIMEvery), while a partial run with zero progress backs off to 1 hour (larkSyncPartialNext).
func TestLarkSyncIMPollPartialWithProgressKeeps15MinuteCadence(t *testing.T) {
	w := newLarkWorld(t)
	ctx := context.Background()

	w.addSyncBot(t, "bsync", "cli_sync", larksyncwire.TierFull)
	fullKey, fullAcc := w.adopt(t, "bsync", "ou_full", "acc-full", "ref-full", 24*time.Hour)
	if _, err := w.h.larkSyncChange(ctx, func(st *larkSyncState) error {
		st.binding(fullKey).Tier = larksyncwire.TierFull
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var nextRes larksyncwire.Result
	w.script("bsync", fullAcc, func(_ context.Context, _ larksyncwire.Binding, _ func(context.Context) (string, error), _ map[string]string, _ larksyncwire.Index) (larksyncwire.Result, error) {
		mu.Lock()
		res := nextRes
		nextRes = larksyncwire.Result{}
		mu.Unlock()
		return res, nil
	})

	// Initial full run seeds cursor.
	mu.Lock()
	nextRes = larksyncwire.Result{Cursors: map[string]string{"im:oc_big": "1000"}}
	mu.Unlock()
	w.h.larkSyncTick(ctx)

	// 15m later: ModeIM partial run that advances cursor im:oc_big -> 2000 keeps 15m schedule.
	w.advance(larkSyncIMEvery)
	mu.Lock()
	nextRes = larksyncwire.Result{
		Partial: true,
		Cursors: map[string]string{"im:oc_big": "2000"},
	}
	mu.Unlock()
	w.h.larkSyncTick(ctx)
	if fb := w.binding(t, fullKey); !fb.NextIMRunAt.Equal(w.now().Add(larkSyncIMEvery)) {
		t.Fatalf("partial ModeIM with cursor progress: NextIMRunAt=%v, want +15m (%v)", fb.NextIMRunAt, w.now().Add(larkSyncIMEvery))
	}

	// 15m later: ModeIM partial run with NO new messages/docs and NO cursor movement (im:oc_big stays 2000) backs off 1h.
	w.advance(larkSyncIMEvery)
	mu.Lock()
	nextRes = larksyncwire.Result{
		Partial: true,
		Cursors: map[string]string{"im:oc_big": "2000"},
	}
	mu.Unlock()
	w.h.larkSyncTick(ctx)
	if fb := w.binding(t, fullKey); !fb.NextIMRunAt.Equal(w.now().Add(larkSyncPartialNext)) {
		t.Fatalf("partial ModeIM with zero progress: NextIMRunAt=%v, want +1h (%v)", fb.NextIMRunAt, w.now().Add(larkSyncPartialNext))
	}
}

// One-time sender-name backfill scans pre-#15486 outbox chat batches, skips bots and other bindings' messages, resolves
// unique user open_ids once, emits repair chat batches (including DM chat names), sets BackfillVersion, and is
// idempotent on subsequent runs (even if BackfillVersion is reset).
func TestLarkSyncBackfillSenderNamesIdempotentAndScoped(t *testing.T) {
	w := newLarkWorld(t)
	ctx := context.Background()

	w.addSyncBot(t, "bsyncA", "cli_a", larksyncwire.TierFull)
	w.addSyncBot(t, "bsyncB", "cli_b", larksyncwire.TierFull)
	keyA, accA := w.adopt(t, "bsyncA", "ou_owner_a", "acc-a", "ref-a", 24*time.Hour)
	keyB, _ := w.adopt(t, "bsyncB", "ou_owner_b", "acc-b", "ref-b", 24*time.Hour)
	if _, err := w.h.larkSyncChange(ctx, func(st *larkSyncState) error {
		st.binding(keyA).Tier = larksyncwire.TierFull
		st.binding(keyB).Tier = larksyncwire.TierFull
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Seed pre-#15486 outbox rows:
	// - oc_grp: one unnamed human (ou_alice), one bot (cli_bot), one already-named human (ou_named), one belonging to binding B (ou_other_tenant).
	// - oc_dm: an unnamed DM with one unnamed peer message (ou_bob).
	seedBatch := func(cb connector.ChatBatch) db.LarkSyncOutbox {
		raw, err := json.Marshal(cb)
		if err != nil {
			t.Fatal(err)
		}
		return db.LarkSyncOutbox{Kind: db.LarkSyncChat, CanonicalKey: "chat:" + cb.Chat.ID, Payload: raw}
	}
	at := w.now().Add(-time.Hour)
	if err := w.store.CommitLarkSyncRun(ctx, keyA, nil, []larksyncwire.IndexRecord{
		{Key: larksyncwire.KeyMessage + "om_a1", Kind: "message", Sources: []larksyncwire.SourceRef{{Binding: keyA, Entry: larksyncwire.EntryChat}}},
		{Key: larksyncwire.KeyMessage + "om_bot", Kind: "message", Sources: []larksyncwire.SourceRef{{Binding: keyA, Entry: larksyncwire.EntryChat}}},
		{Key: larksyncwire.KeyMessage + "om_named", Kind: "message", Sources: []larksyncwire.SourceRef{{Binding: keyA, Entry: larksyncwire.EntryChat}}},
		{Key: larksyncwire.KeyMessage + "om_b1", Kind: "message", Sources: []larksyncwire.SourceRef{{Binding: keyB, Entry: larksyncwire.EntryChat}}},
		{Key: larksyncwire.KeyMessage + "om_dm1", Kind: "message", Sources: []larksyncwire.SourceRef{{Binding: keyA, Entry: larksyncwire.EntryChat}}},
	}, []db.LarkSyncOutbox{
		seedBatch(connector.ChatBatch{
			Chat: kbwire.SourceChat{ID: "oc_grp", Name: "Example Group", Kind: "group"},
			Messages: []kbwire.SourceMessage{
				{ID: "om_a1", SenderID: "ou_alice", SenderName: "", Text: "hello group", At: at},
				{ID: "om_bot", SenderID: "cli_bot", SenderName: "", Text: "bot notice", At: at.Add(time.Second)},
				{ID: "om_named", SenderID: "ou_named", SenderName: "Already Named", Text: "named", At: at.Add(2 * time.Second)},
				{ID: "om_b1", SenderID: "ou_other_tenant", SenderName: "", Text: "other binding", At: at.Add(3 * time.Second)},
			},
		}),
		seedBatch(connector.ChatBatch{
			Chat: kbwire.SourceChat{ID: "oc_dm", Name: "", Kind: "dm"},
			Messages: []kbwire.SourceMessage{
				{ID: "om_dm1", SenderID: "ou_bob", SenderName: "", Text: "hi dm", At: at.Add(4 * time.Second)},
			},
		}),
	}); err != nil {
		t.Fatal(err)
	}

	var resolveCalls atomic.Int32
	var lastRequested []string
	w.mu.Lock()
	w.resolveNames = func(_ context.Context, _ larksyncwire.Binding, _ func(context.Context) (string, error), openIDs []string) (map[string]string, error) {
		resolveCalls.Add(1)
		lastRequested = slices.Clone(openIDs)
		return map[string]string{
			"ou_alice": "Example Alice",
			"ou_bob":   "Example Bob",
		}, nil
	}
	w.mu.Unlock()
	w.script("bsyncA", accA, func(_ context.Context, _ larksyncwire.Binding, _ func(context.Context) (string, error), _ map[string]string, _ larksyncwire.Index) (larksyncwire.Result, error) {
		return larksyncwire.Result{}, nil
	})

	// Run 1: triggers backfill for keyA only.
	if err := w.h.larkSyncRunMode(ctx, keyA, larksyncwire.ModeIM); err != nil {
		t.Fatal(err)
	}
	if resolveCalls.Load() != 1 {
		t.Fatalf("resolveCalls = %d, want 1", resolveCalls.Load())
	}
	if !slices.Equal(lastRequested, []string{"ou_alice", "ou_bob"}) {
		t.Fatalf("resolved openIDs = %v, want [ou_alice ou_bob] (skipping cli_bot, om_named, and binding B's ou_other_tenant)", lastRequested)
	}
	if got := w.binding(t, keyA).BackfillVersion; got != larkSyncBackfillVersion {
		t.Fatalf("BackfillVersion = %d, want %d", got, larkSyncBackfillVersion)
	}
	// Verify repair rows were appended to outbox with resolved names and DM chat name.
	rows, err := w.store.LarkSyncOutboxAfter(ctx, db.LarkSyncChat, 2, 10)
	if err != nil || len(rows) != 2 {
		t.Fatalf("repair outbox rows = %d, err = %v", len(rows), err)
	}
	repaired := map[string]connector.ChatBatch{}
	for _, r := range rows {
		var cb connector.ChatBatch
		if err := json.Unmarshal(r.Payload, &cb); err != nil {
			t.Fatal(err)
		}
		repaired[cb.Chat.ID] = cb
	}
	if grp := repaired["oc_grp"]; len(grp.Messages) != 1 || grp.Messages[0].ID != "om_a1" || grp.Messages[0].SenderName != "Example Alice" {
		t.Fatalf("repaired oc_grp = %+v", grp)
	}
	if dm := repaired["oc_dm"]; dm.Chat.Name != "Example Bob" || len(dm.Messages) != 1 || dm.Messages[0].SenderName != "Example Bob" {
		t.Fatalf("repaired oc_dm = %+v", dm)
	}

	// Run 2: BackfillVersion is set -> does not call ResolveUserNames or emit any rows.
	if err := w.h.larkSyncRunMode(ctx, keyA, larksyncwire.ModeIM); err != nil {
		t.Fatal(err)
	}
	if resolveCalls.Load() != 1 {
		t.Fatalf("run 2 called ResolveUserNames again (%d)", resolveCalls.Load())
	}

	// Run 3: even if BackfillVersion is manually reset to 0, outbox scan sees the newer named rows and is a no-op.
	if _, err := w.h.larkSyncChange(ctx, func(st *larkSyncState) error {
		st.binding(keyA).BackfillVersion = 0
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.h.larkSyncRunMode(ctx, keyA, larksyncwire.ModeIM); err != nil {
		t.Fatal(err)
	}
	if resolveCalls.Load() != 1 {
		t.Fatalf("reset BackfillVersion re-resolved already-repaired messages (%d calls)", resolveCalls.Load())
	}
	if rowsAfter, _ := w.store.LarkSyncOutboxAfter(ctx, db.LarkSyncChat, 4, 10); len(rowsAfter) != 0 {
		t.Fatalf("unexpected extra outbox rows after idempotent rerun: %d", len(rowsAfter))
	}
}

// Missing contact access may occur only in legacy sender repair, after the main sync has already committed.
// It must reach settings health without losing progress; a renewed grant lets the scheduler retry the pending
// repair without a manual run or cursor reset, including when the first attempt resolved only some names.
func TestLarkSyncSenderRepairRetriesAfterReauthorization(t *testing.T) {
	for _, tc := range []struct{ partialNames, completed bool }{{false, false}, {true, false}, {false, true}} {
		t.Run(fmt.Sprintf("partial_names=%v/completed=%v", tc.partialNames, tc.completed), func(t *testing.T) {
			w := newLarkWorld(t)
			ctx := context.Background()
			w.addSyncBot(t, "bsync", "cli_sync", larksyncwire.TierFull)
			key, acc := w.adopt(t, "bsync", "ou_owner", "access-old", "refresh-old", 24*time.Hour)
			if _, err := w.h.larkSyncChange(ctx, func(st *larkSyncState) error {
				st.binding(key).Tier = larksyncwire.TierFull
				if tc.completed {
					st.binding(key).BackfillVersion = larkSyncBackfillVersion
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			batch := connector.ChatBatch{
				Chat: kbwire.SourceChat{ID: "oc_example", Name: "Example group", Kind: "group"},
				Messages: []kbwire.SourceMessage{
					{ID: "om_one", SenderID: "ou_one", At: w.now().Add(-time.Hour), Text: "example one"},
					{ID: "om_two", SenderID: "ou_two", At: w.now().Add(-time.Minute), Text: "example two"},
				},
			}
			raw, err := json.Marshal(batch)
			if err != nil {
				t.Fatal(err)
			}
			if err := w.store.CommitLarkSyncRun(ctx, key, map[string]string{"im:oc_example": "100"}, nil,
				[]db.LarkSyncOutbox{{Kind: db.LarkSyncChat, CanonicalKey: "chat:oc_example", Payload: raw}}); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			var granted atomic.Bool
			w.mu.Lock()
			w.resolveNames = func(_ context.Context, _ larksyncwire.Binding, _ func(context.Context) (string, error), ids []string) (map[string]string, error) {
				calls.Add(1)
				if !granted.Load() {
					names := map[string]string{}
					if tc.partialNames {
						names["ou_one"] = "Example One"
					}
					return names, &larksyncwire.Error{Kind: larksyncwire.ErrKindScope, Msg: "contact:contact.base:readonly required"}
				}
				out := map[string]string{}
				for _, id := range ids {
					out[id] = "Example " + id
				}
				return out, nil
			}
			w.mu.Unlock()
			w.script("bsync", acc, func(_ context.Context, _ larksyncwire.Binding, _ func(context.Context) (string, error), _ map[string]string, _ larksyncwire.Index) (larksyncwire.Result, error) {
				res := larksyncwire.Result{Cursors: map[string]string{"im:oc_example": "200"}}
				if tc.completed && !granted.Load() {
					res.Errors = []larksyncwire.ResourceError{{Resource: "sender_names", Kind: larksyncwire.ErrKindScope, Detail: "contact:contact.base:readonly required"}}
				}
				return res, nil
			})
			if err := w.h.larkSyncRunMode(ctx, key, larksyncwire.ModeIM); err != nil {
				t.Fatal(err)
			}
			b := w.binding(t, key)
			if b.Health != larksyncwire.HealthScopeMissing || b.BackfillVersion != 0 || !strings.Contains(b.LastError, "contact:contact.base:readonly") {
				t.Fatalf("repair refusal not exposed or incorrectly completed: health=%s version=%d error=%s", b.Health, b.BackfillVersion, b.LastError)
			}
			if got := w.store.cursorsOf(key)["im:oc_example"]; got != "200" {
				t.Fatalf("readable sync progress lost: cursor=%s", got)
			}
			// Simulate the authorization callback with a newly expanded grant. No real account is contacted.
			w.advance(time.Minute)
			st, err := w.h.larkSyncLoad(ctx)
			if err != nil {
				t.Fatal(err)
			}
			app, err := w.h.larkSyncApp(ctx, &st, "bsync")
			if err != nil {
				t.Fatal(err)
			}
			granted.Store(true)
			_, err = w.h.larkSyncAdopt(ctx, "bsync", larkSyncPendingKey("bsync"), larksyncwire.TierFull, app, larksyncwire.Token{
				AccessToken: "access-new", RefreshToken: "refresh-new", OpenID: "ou_owner", TenantKey: "t1",
				ExpiresAt: w.now().Add(24 * time.Hour), Scopes: larksyncwire.ScopesFor(larksyncwire.TierFull),
			})
			if err != nil {
				t.Fatal(err)
			}
			if b = w.binding(t, key); b.BackfillVersion != 0 || !b.NextRunAt.Equal(w.now()) || !b.NextIMRunAt.Equal(w.now()) {
				t.Fatal("authorization did not preserve pending repair and make both schedules due")
			}
			w.h.larkSyncTick(ctx)
			wantCalls := int32(2)
			if tc.completed {
				wantCalls = 1
			}
			if b = w.binding(t, key); b.Health != larksyncwire.HealthOK || b.BackfillVersion != larkSyncBackfillVersion || calls.Load() != wantCalls {
				t.Fatalf("scheduler did not recover repair: health=%s version=%d calls=%d", b.Health, b.BackfillVersion, calls.Load())
			}
			rows, err := w.store.LarkSyncOutboxAfter(ctx, db.LarkSyncChat, 1, 10)
			if err != nil {
				t.Fatal(err)
			}
			repaired := map[string]bool{}
			for _, row := range rows {
				var cb connector.ChatBatch
				if err := json.Unmarshal(row.Payload, &cb); err != nil {
					t.Fatal(err)
				}
				for _, m := range cb.Messages {
					if m.SenderName != "" {
						repaired[m.ID] = true
					}
				}
			}
			if len(repaired) != 2 {
				t.Fatalf("repair did not deliver both sender names: %v", repaired)
			}
			w.advance(larkSyncIMEvery)
			w.h.larkSyncTick(ctx)
			if calls.Load() != wantCalls {
				t.Fatal("completed repair was not idempotent")
			}
		})
	}
}

func TestLarkSyncOldRunCannotPostponeRenewedGrant(t *testing.T) {
	w := newLarkWorld(t)
	ctx := context.Background()
	w.addSyncBot(t, "bsync", "cli_sync", larksyncwire.TierFull)
	key, acc := w.adopt(t, "bsync", "ou_owner", "example-old", "example-refresh", 24*time.Hour)
	started, resume := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	w.script("bsync", acc, func(_ context.Context, _ larksyncwire.Binding, _ func(context.Context) (string, error), _ map[string]string, _ larksyncwire.Index) (larksyncwire.Result, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-resume
			return larksyncwire.Result{Errors: []larksyncwire.ResourceError{{Resource: "sender_names", Kind: larksyncwire.ErrKindScope}}}, nil
		}
		return larksyncwire.Result{}, nil
	})
	done := make(chan error, 1)
	go func() { done <- w.h.larkSyncRun(ctx, key) }()
	t.Cleanup(func() {
		select {
		case <-resume:
		default:
			close(resume)
		}
	})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("old run did not start")
	}
	w.advance(time.Minute)
	w.adopt(t, "bsync", "ou_owner", "example-new", "example-new-refresh", 24*time.Hour)
	close(resume)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("old run did not finish")
	}
	b := w.binding(t, key)
	if b.Health != larksyncwire.HealthOK || !b.NextRunAt.Equal(w.now()) || b.BackfillVersion != 0 {
		t.Fatalf("old run overwrote renewed grant: health=%s due=%v repair=%d", b.Health, b.NextRunAt, b.BackfillVersion)
	}
	w.h.larkSyncTick(ctx)
	if calls.Load() != 2 {
		t.Fatal("renewed grant did not automatically run on the next tick")
	}
}

// One-time dropped-links remediation scans already-synced chat outbox messages owned by a binding, skips links whose
// document already has body content in lark_sync_index (or a synced wiki alias with body content in the doc outbox),
// skips other bindings' messages, merges missing links into PendingLinksCursor without duplicating already-queued
// tokens, passes AuthorizedAt/GrantedScopes to the Syncer, sets LinksBackfillVersion, and is idempotent on reruns.
func TestLarkSyncBackfillDroppedLinksIdempotentAndScoped(t *testing.T) {
	w := newLarkWorld(t)
	ctx := context.Background()

	w.addSyncBot(t, "bsyncA", "cli_a", larksyncwire.TierFull)
	w.addSyncBot(t, "bsyncB", "cli_b", larksyncwire.TierFull)
	keyA, accA := w.adopt(t, "bsyncA", "ou_owner_a", "acc-a", "ref-a", 24*time.Hour)
	keyB, _ := w.adopt(t, "bsyncB", "ou_owner_b", "acc-b", "ref-b", 24*time.Hour)
	if _, err := w.h.larkSyncChange(ctx, func(st *larkSyncState) error {
		st.binding(keyA).Tier = larksyncwire.TierFull
		st.binding(keyA).BackfillVersion = larkSyncBackfillVersion // isolate dropped-links remediation
		st.binding(keyB).Tier = larksyncwire.TierFull
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	seedChat := func(cb connector.ChatBatch) db.LarkSyncOutbox {
		raw, err := json.Marshal(cb)
		if err != nil {
			t.Fatal(err)
		}
		return db.LarkSyncOutbox{Kind: db.LarkSyncChat, CanonicalKey: "chat:" + cb.Chat.ID, Payload: raw}
	}
	seedDoc := func(it kbwire.Item) db.LarkSyncOutbox {
		raw, err := json.Marshal(it)
		if err != nil {
			t.Fatal(err)
		}
		return db.LarkSyncOutbox{Kind: db.LarkSyncDoc, CanonicalKey: it.CanonicalKey, Payload: raw}
	}
	at := w.now().Add(-2 * time.Hour)
	if err := w.store.CommitLarkSyncRun(ctx, keyA, map[string]string{
		larksyncwire.PendingLinksCursor: `["doxAlreadyQueued"]`,
	}, []larksyncwire.IndexRecord{
		{Key: larksyncwire.KeyMessage + "om_a1", Kind: "message", Sources: []larksyncwire.SourceRef{{Binding: keyA, Entry: larksyncwire.EntryChat}}},
		{Key: larksyncwire.KeyMessage + "om_b1", Kind: "message", Sources: []larksyncwire.SourceRef{{Binding: keyB, Entry: larksyncwire.EntryChat}}},
		{Key: larksyncwire.KeyDoc + "doxFetched", Kind: "doc", Revision: 3, BodyHash: "body-hash-1"},
		{Key: larksyncwire.KeyDoc + "doxStub", Kind: "doc", Revision: 0, BodyHash: ""},
	}, []db.LarkSyncOutbox{
		seedChat(connector.ChatBatch{
			Chat: kbwire.SourceChat{ID: "oc_1", Name: "Example Group", Kind: "group"},
			Messages: []kbwire.SourceMessage{
				{
					ID: "om_a1", SenderID: "ou_peer", SenderName: "Peer", At: at,
					Text: "https://my.feishu.cn/docx/doxFetched " +
						"https://my.feishu.cn/docx/doxStub " +
						"https://my.feishu.cn/docx/doxDropped " +
						"https://my.feishu.cn/wiki/wikFetched " +
						"https://my.feishu.cn/wiki/wikDropped " +
						"https://my.feishu.cn/docx/doxAlreadyQueued",
				},
				{
					ID: "om_b1", SenderID: "ou_other", SenderName: "Other", At: at.Add(time.Minute),
					Text: "https://my.feishu.cn/docx/doxOtherTenant",
				},
			},
		}),
		seedDoc(kbwire.Item{
			Kind:         "docs",
			CanonicalKey: larksyncwire.KeyDoc + "doxFromWikFetched",
			Aliases:      []string{"wikFetched"},
			Revision:     2,
			BodyHash:     "wiki-body-hash",
			Content:      "already synced wiki doc",
		}),
	}); err != nil {
		t.Fatal(err)
	}

	var extractCalls, mergeCalls atomic.Int32
	w.mu.Lock()
	w.extractDocLinks = func(text string) []larksyncwire.PendingLinkCandidate {
		extractCalls.Add(1)
		var out []larksyncwire.PendingLinkCandidate
		for _, field := range splitSpaces(text) {
			switch {
			case hasPrefix(field, "https://my.feishu.cn/docx/"):
				tok := field[len("https://my.feishu.cn/docx/"):]
				out = append(out, larksyncwire.PendingLinkCandidate{Token: tok, ObjType: "docx", URL: field})
			case hasPrefix(field, "https://my.feishu.cn/wiki/"):
				tok := field[len("https://my.feishu.cn/wiki/"):]
				out = append(out, larksyncwire.PendingLinkCandidate{Token: tok, ObjType: "wiki", URL: field})
			}
		}
		return out
	}
	w.mergePendingLinks = func(existingCursor string, add []larksyncwire.PendingLinkCandidate) (string, int) {
		mergeCalls.Add(1)
		var list []string
		_ = json.Unmarshal([]byte(existingCursor), &list)
		have := map[string]bool{}
		for _, tok := range list {
			have[tok] = true
		}
		added := 0
		for _, c := range add {
			if have[c.Token] {
				continue
			}
			have[c.Token] = true
			list = append(list, c.Token)
			added++
		}
		if added == 0 {
			return existingCursor, 0
		}
		slices.Sort(list)
		raw, _ := json.Marshal(list)
		return string(raw), added
	}
	w.mu.Unlock()

	var seenCursor string
	var seenAuthAt time.Time
	var seenScopes []string
	w.script("bsyncA", accA, func(_ context.Context, b larksyncwire.Binding, _ func(context.Context) (string, error), cursors map[string]string, _ larksyncwire.Index) (larksyncwire.Result, error) {
		seenCursor = cursors[larksyncwire.PendingLinksCursor]
		seenAuthAt = b.AuthorizedAt
		seenScopes = slices.Clone(b.GrantedScopes)
		return larksyncwire.Result{}, nil
	})

	// Run 1: remediates missing links (doxDropped, doxStub, wikDropped), skipping doxFetched, wikFetched,
	// doxAlreadyQueued (no duplicate), and binding B's doxOtherTenant.
	if err := w.h.larkSyncRunMode(ctx, keyA, larksyncwire.ModeIM); err != nil {
		t.Fatal(err)
	}
	if seenAuthAt.IsZero() || len(seenScopes) == 0 {
		t.Fatalf("binding auth metadata not passed to Syncer: AuthorizedAt=%v Scopes=%v", seenAuthAt, seenScopes)
	}
	wantQueue := `["doxAlreadyQueued","doxDropped","doxStub","wikDropped"]`
	if seenCursor != wantQueue {
		t.Fatalf("run 1 received links_queue = %s, want %s", seenCursor, wantQueue)
	}
	if got := w.store.cursorsOf(keyA)[larksyncwire.PendingLinksCursor]; got != wantQueue {
		t.Fatalf("persisted links_queue = %s, want %s", got, wantQueue)
	}
	if got := w.binding(t, keyA).LinksBackfillVersion; got != larkSyncLinksBackfillVersion {
		t.Fatalf("LinksBackfillVersion = %d, want %d", got, larkSyncLinksBackfillVersion)
	}
	extractAfterRun1 := extractCalls.Load()
	mergeAfterRun1 := mergeCalls.Load()

	// Run 2: LinksBackfillVersion already set -> skips remediation completely.
	if err := w.h.larkSyncRunMode(ctx, keyA, larksyncwire.ModeIM); err != nil {
		t.Fatal(err)
	}
	if extractCalls.Load() != extractAfterRun1 || mergeCalls.Load() != mergeAfterRun1 {
		t.Fatalf("run 2 re-ran remediation despite LinksBackfillVersion=%d", larkSyncLinksBackfillVersion)
	}

	// Run 3: even if LinksBackfillVersion is manually reset to 0 after doxDropped is indexed with body content,
	// remediation is completely idempotent (zero duplicates added, queue unchanged).
	if err := w.store.CommitLarkSyncRun(ctx, keyA, nil, []larksyncwire.IndexRecord{
		{Key: larksyncwire.KeyDoc + "doxDropped", Kind: "doc", Revision: 1, BodyHash: "now-has-body"},
	}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := w.h.larkSyncChange(ctx, func(st *larkSyncState) error {
		st.binding(keyA).LinksBackfillVersion = 0
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.h.larkSyncRunMode(ctx, keyA, larksyncwire.ModeIM); err != nil {
		t.Fatal(err)
	}
	if got := w.store.cursorsOf(keyA)[larksyncwire.PendingLinksCursor]; got != wantQueue {
		t.Fatalf("after idempotent reset rerun: links_queue = %s, want %s", got, wantQueue)
	}
}

// Upgrading a binding from BackfillVersion=1 (where contact/v3 could not resolve external personal-tenant users)
// triggers v2 backfill once using ChatNameResolver, repairs external group senders, human DMs, bot DMs, and
// owner-only unnamed DMs, updates im:p2p_chats, advances BackfillVersion to 2, and is idempotent on reruns.
func TestLarkSyncBackfillV2ResolvesExternalSendersAndUnnamedDMsIdempotently(t *testing.T) {
	w := newLarkWorld(t)
	ctx := context.Background()

	w.addSyncBot(t, "bsync", "cli_sync", larksyncwire.TierFull)
	key, acc := w.adopt(t, "bsync", "ou_owner", "acc-1", "ref-1", 24*time.Hour)
	if _, err := w.h.larkSyncChange(ctx, func(st *larkSyncState) error {
		b := st.binding(key)
		b.Tier = larksyncwire.TierFull
		b.BackfillVersion = 2 // already ran v2; v3 (on-demand member lists, the name book, mention scans) runs again
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	seedBatch := func(cb connector.ChatBatch) db.LarkSyncOutbox {
		raw, err := json.Marshal(cb)
		if err != nil {
			t.Fatal(err)
		}
		return db.LarkSyncOutbox{Kind: db.LarkSyncChat, CanonicalKey: "chat:" + cb.Chat.ID, Payload: raw}
	}
	at := w.now().Add(-time.Hour)
	if err := w.store.CommitLarkSyncRun(ctx, key, map[string]string{
		"im:p2p_chats":                 `{"oc_dm_human":"","oc_dm_owner_only":"","oc_dm_bot":""}`,
		larksyncwire.SenderNamesCursor: `{"ou_left":{"n":"Former Member","t":1}}`,
	}, []larksyncwire.IndexRecord{
		{Key: larksyncwire.KeyMessage + "om_ext", Kind: "message", Sources: []larksyncwire.SourceRef{{Binding: key, Entry: larksyncwire.EntryChat}}},
		{Key: larksyncwire.KeyMessage + "om_dm_h", Kind: "message", Sources: []larksyncwire.SourceRef{{Binding: key, Entry: larksyncwire.EntryChat}}},
		{Key: larksyncwire.KeyMessage + "om_dm_o", Kind: "message", Sources: []larksyncwire.SourceRef{{Binding: key, Entry: larksyncwire.EntryChat}}},
		{Key: larksyncwire.KeyMessage + "om_dm_b", Kind: "message", Sources: []larksyncwire.SourceRef{{Binding: key, Entry: larksyncwire.EntryChat}}},
	}, []db.LarkSyncOutbox{
		seedBatch(connector.ChatBatch{
			Chat:     kbwire.SourceChat{ID: "oc_ext_grp", Name: "External Group", Kind: "group"},
			Messages: []kbwire.SourceMessage{{ID: "om_ext", SenderID: "ou_ext", SenderName: "", Text: "hello", At: at}},
		}),
		seedBatch(connector.ChatBatch{
			Chat:     kbwire.SourceChat{ID: "oc_dm_human", Name: "", Kind: "dm"},
			Messages: []kbwire.SourceMessage{{ID: "om_dm_h", SenderID: "ou_friend", SenderName: "", Text: "hi", At: at.Add(time.Second)}},
		}),
		seedBatch(connector.ChatBatch{
			Chat:     kbwire.SourceChat{ID: "oc_dm_owner_only", Name: "", Kind: "dm"},
			Messages: []kbwire.SourceMessage{{ID: "om_dm_o", SenderID: "ou_owner", Mine: true, Text: "ping", At: at.Add(2 * time.Second)}},
		}),
		seedBatch(connector.ChatBatch{
			Chat:     kbwire.SourceChat{ID: "oc_dm_bot", Name: "", Kind: "dm"},
			Messages: []kbwire.SourceMessage{{ID: "om_dm_b", SenderID: "cli_helper", SenderName: "", Text: "alert", At: at.Add(3 * time.Second)}},
		}),
	}); err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32
	var gotReq larksyncwire.NameResolveRequest
	w.mu.Lock()
	w.resolveNamesWithFallback = func(_ context.Context, _ larksyncwire.Binding, _ func(context.Context) (string, error), req larksyncwire.NameResolveRequest) (larksyncwire.NameResolveResult, error) {
		calls.Add(1)
		gotReq = req
		return larksyncwire.NameResolveResult{
			UserNames: map[string]string{
				"ou_ext":     "External Member",
				"ou_friend":  "External Friend",
				"cli_helper": "Helper Bot",
			},
			P2PChatNames: map[string]string{
				"oc_dm_human":      "External Friend",
				"oc_dm_owner_only": "Quiet Peer",
				"oc_dm_bot":        "Helper Bot",
			},
		}, nil
	}
	w.mu.Unlock()
	w.script("bsync", acc, func(_ context.Context, _ larksyncwire.Binding, _ func(context.Context) (string, error), _ map[string]string, _ larksyncwire.Index) (larksyncwire.Result, error) {
		return larksyncwire.Result{}, nil
	})

	if err := w.h.larkSyncRunMode(ctx, key, larksyncwire.ModeIM); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
	if !slices.Equal(gotReq.P2PChatIDs, []larksyncwire.ChatID{"oc_dm_bot", "oc_dm_human", "oc_dm_owner_only"}) {
		t.Fatalf("P2PChatIDs = %v", gotReq.P2PChatIDs)
	}
	if !slices.Equal(gotReq.ChatSenders["oc_ext_grp"], []string{"ou_ext"}) || !slices.Equal(gotReq.ChatSenders["oc_dm_bot"], []string{"cli_helper"}) {
		t.Fatalf("ChatSenders = %v", gotReq.ChatSenders)
	}
	if gotReq.NameBook != `{"ou_left":{"n":"Former Member","t":1}}` {
		t.Fatalf("NameBook = %q, want the binding's stored book", gotReq.NameBook)
	}
	if !gotReq.SampleTimes["ou_ext"].Equal(at) || gotReq.SampleChats["ou_ext"] != "oc_ext_grp" || gotReq.SampleMessages["ou_ext"] != "om_ext" {
		t.Fatalf("sample of ou_ext = %v %v %v", gotReq.SampleMessages["ou_ext"], gotReq.SampleTimes["ou_ext"], gotReq.SampleChats["ou_ext"])
	}
	if larkSyncBackfillVersion != 3 {
		t.Fatalf("larkSyncBackfillVersion = %d, want 3", larkSyncBackfillVersion)
	}
	if got := w.binding(t, key).BackfillVersion; got != larkSyncBackfillVersion {
		t.Fatalf("BackfillVersion = %d, want %d", got, larkSyncBackfillVersion)
	}

	rows, err := w.store.LarkSyncOutboxAfter(ctx, db.LarkSyncChat, 4, 20)
	if err != nil || len(rows) != 4 {
		t.Fatalf("repair rows = %d, err = %v", len(rows), err)
	}
	repaired := map[string]connector.ChatBatch{}
	for _, r := range rows {
		var cb connector.ChatBatch
		if err := json.Unmarshal(r.Payload, &cb); err != nil {
			t.Fatal(err)
		}
		repaired[cb.Chat.ID] = cb
	}
	if g := repaired["oc_ext_grp"]; len(g.Messages) != 1 || g.Messages[0].SenderName != "External Member" {
		t.Fatalf("repaired oc_ext_grp = %+v", g)
	}
	if d := repaired["oc_dm_human"]; d.Chat.Name != "External Friend" || len(d.Messages) != 1 || d.Messages[0].SenderName != "External Friend" {
		t.Fatalf("repaired oc_dm_human = %+v", d)
	}
	if d := repaired["oc_dm_owner_only"]; d.Chat.Name != "Quiet Peer" || len(d.Messages) != 0 {
		t.Fatalf("repaired oc_dm_owner_only = %+v", d)
	}
	if d := repaired["oc_dm_bot"]; d.Chat.Name != "Helper Bot" || len(d.Messages) != 1 || d.Messages[0].SenderName != "Helper Bot" {
		t.Fatalf("repaired oc_dm_bot = %+v", d)
	}
	var savedP2P map[string]string
	if err := json.Unmarshal([]byte(w.store.cursorsOf(key)["im:p2p_chats"]), &savedP2P); err != nil {
		t.Fatalf("im:p2p_chats cursor: %v", err)
	}
	if savedP2P["oc_dm_human"] != "External Friend" || savedP2P["oc_dm_owner_only"] != "Quiet Peer" || savedP2P["oc_dm_bot"] != "Helper Bot" {
		t.Fatalf("updated im:p2p_chats = %v", savedP2P)
	}

	// Idempotent even if BackfillVersion is manually reset to 1: newer named outbox rows leave 0 candidates.
	if _, err := w.h.larkSyncChange(ctx, func(st *larkSyncState) error {
		st.binding(key).BackfillVersion = 1
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.h.larkSyncRunMode(ctx, key, larksyncwire.ModeIM); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("reset BackfillVersion re-resolved already-repaired data (%d calls)", calls.Load())
	}
}

func splitSpaces(s string) []string {
	var out []string
	cur := ""
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\n' || s[i] == '\t' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		cur += string(s[i])
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func hasPrefix(s, p string) bool {
	return len(s) >= len(p) && s[:len(p)] == p
}
