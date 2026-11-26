package extmgr

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/kbwire"
	"github.com/nwverse/persona/server/internal/larksyncwire"
)

// Mock suite B: two accounts × two bots are four bindings, each with its own reader, refresh lock and circuit. Three
// of them fail in their own way (the refresh token refused, the rate limit, a scope the admin has not approved) and
// the fourth reads in full: each ends with its own health, cursors and outbox rows, and the chat bot is untouched.
func TestLarkSyncBindingsFailOnTheirOwn(t *testing.T) {
	w := newLarkWorld(t)
	ctx := context.Background()
	_, botViewBefore := w.do(t, "GET", "/api/v1/bots/feishu", "")
	feishuBefore := w.store.raw(botSettingsKey)

	w.addSyncBot(t, "bsync", "cli_sync", larksyncwire.TierFull)
	// A's access token ends within the refresh margin and its refresh token was revoked.
	keyA, accA := w.adopt(t, larksyncwire.PrimaryBot, "ou_a", "acc-A", "ref-A", 5*time.Minute)
	keyB, accB := w.adopt(t, larksyncwire.PrimaryBot, "ou_b", "acc-B", "ref-B", 2*time.Hour)
	keyC, accC := w.adopt(t, "bsync", "ou_a", "acc-C", "ref-C", 2*time.Hour)
	keyD, accD := w.adopt(t, "bsync", "ou_b", "acc-D", "ref-D", 2*time.Hour)
	if accA != accC || accB != accD || len(map[string]bool{keyA: true, keyB: true, keyC: true, keyD: true}) != 4 {
		t.Fatalf("2 accounts × 2 bots: %s %s %s %s", keyA, keyB, keyC, keyD)
	}
	var refusedA atomic.Int32
	w.ua.refresh = func(_ larksyncwire.App, rt string) (larksyncwire.Token, error) {
		if rt == "ref-A" {
			refusedA.Add(1)
			return larksyncwire.Token{}, &larksyncwire.Error{Kind: larksyncwire.ErrKindInvalidGrant, Op: "refresh", Code: 20064, Msg: "revoked"}
		}
		return larksyncwire.Token{AccessToken: "acc-" + rt + "+", RefreshToken: rt + "+", ExpiresAt: w.now().Add(2 * time.Hour)}, nil
	}
	w.script(larksyncwire.PrimaryBot, accB, func(ctx context.Context, b larksyncwire.Binding, token func(context.Context) (string, error), _ map[string]string, _ larksyncwire.Index) (larksyncwire.Result, error) {
		if tok, err := token(ctx); err != nil || (tok != "acc-B" && w.firstRun(b.Key)) {
			t.Errorf("B's token: %q %v", tok, err)
		}
		return larksyncwire.Result{
			Docs:    []larksyncwire.Doc{{Key: larksyncwire.KeyDoc + "doxB", Kind: "docs", ObjToken: "doxB", Title: "B's doc", Revision: 3}},
			Cursors: map[string]string{"drive": "100", "im:oc_b": "999"},
			Errors:  []larksyncwire.ResourceError{{Resource: "im:oc_b", Kind: larksyncwire.ErrKindRateLimited, Detail: "429"}},
		}, nil
	})
	w.script("bsync", accC, func(ctx context.Context, _ larksyncwire.Binding, token func(context.Context) (string, error), _ map[string]string, idx larksyncwire.Index) (larksyncwire.Result, error) {
		_ = idx.Put(ctx, []larksyncwire.IndexRecord{{Key: larksyncwire.KeyDoc + "neverC", Kind: "doc", Revision: 1}})
		return larksyncwire.Result{}, &larksyncwire.Error{Kind: larksyncwire.ErrKindScope, Op: "drive", Code: 99991672, Msg: "scope not approved"}
	})
	w.script("bsync", accD, func(ctx context.Context, b larksyncwire.Binding, token func(context.Context) (string, error), cursors map[string]string, idx larksyncwire.Index) (larksyncwire.Result, error) {
		if tok, err := token(ctx); err != nil || (tok != "acc-D" && w.firstRun(b.Key)) {
			t.Errorf("D's token: %q %v", tok, err)
		}
		if b.Tier != larksyncwire.TierFull || b.App.AppID != "cli_sync" || b.App.AppSecret != "cli_sync-secret" {
			t.Errorf("D reads as %+v", b)
		}
		got, err := idx.Lookup(ctx, []string{larksyncwire.KeyDoc + "doxD"})
		if err != nil || (len(got) != 0 && w.firstRun(b.Key)) {
			t.Errorf("lookup: %v %v", got, err)
		}
		at := w.now().Add(-time.Hour)
		src := []larksyncwire.SourceRef{{Channel: larksyncwire.ConnectorID, Binding: b.Key, Account: b.AccountID, Entry: larksyncwire.EntryDrive, SeenAt: w.now()}}
		_ = idx.Put(ctx, []larksyncwire.IndexRecord{{Key: larksyncwire.KeyDoc + "doxD", Kind: "doc", Revision: 7, BodyHash: "h7", Sources: src}})
		return larksyncwire.Result{
			Messages: []larksyncwire.Message{
				{ID: "om_2", ChatID: "oc_d", ChatName: "Pat", ChatType: "p2p", SenderID: "ou_x", SenderName: "Pat", Text: "second", At: at.Add(time.Minute),
					Files: []larksyncwire.Attachment{{Ref: b.Key + "|om_2|img_1|image", Kind: "image"}}},
				{ID: "om_1", ChatID: "oc_d", ChatName: "Pat", ChatType: "p2p", SenderID: "ou_b", Mine: true, Text: "first", At: at},
				{ID: "om_3", ChatID: "oc_g", ChatName: "Team", ChatType: "group", SenderID: "ou_y", Text: "hello", At: at},
			},
			Docs:    []larksyncwire.Doc{{Key: larksyncwire.KeyDoc + "doxD", Kind: "docs", ObjToken: "doxD", ObjType: "docx", Title: "D's doc", Revision: 7, BodyHash: "h7", Body: "# D", Sources: src}},
			Cursors: map[string]string{"im:oc_d": "1700000000000", "drive": "200"},
		}, nil
	})

	w.h.larkSyncTick(ctx)

	if w.readers != 4 {
		t.Fatalf("each binding has a reader of its own: %d readers", w.readers)
	}
	if n := w.ua.refreshes.Load(); n != 1 {
		t.Fatalf("refreshes: %d, want only A's", n)
	}
	now := w.now()
	// A: the refresh token was refused: authorize again; nothing moved.
	if b := w.binding(t, keyA); b.Health != larksyncwire.HealthNeedsReauth || b.LastError == "" || len(w.store.cursorsOf(keyA)) != 0 {
		t.Fatalf("A: %+v cursors %v", b, w.store.cursorsOf(keyA))
	}
	// B: the rate limit stops one chat; the drive's progress and its document are kept, the chat's cursor is not.
	if b := w.binding(t, keyB); b.Health != larksyncwire.HealthRateLimited || b.Failures != 0 || !b.NextRunAt.Equal(now.Add(larkSyncPartialNext)) || b.LastDocs != 1 {
		t.Fatalf("B: %+v", b)
	}
	if c := w.store.cursorsOf(keyB); len(c) != 1 || c["drive"] != "100" {
		t.Fatalf("B's cursors: %v", c)
	}
	// C: a scope is missing: nothing it staged reaches the index, its circuit counts one failure.
	if b := w.binding(t, keyC); b.Health != larksyncwire.HealthScopeMissing || b.Failures != 1 || !b.NextRunAt.Equal(now.Add(larkSyncFailSoon)) {
		t.Fatalf("C: %+v", b)
	}
	if len(w.store.cursorsOf(keyC)) != 0 || w.store.index[larksyncwire.KeyDoc+"neverC"].Key != "" {
		t.Fatalf("C committed: %v %v", w.store.cursorsOf(keyC), w.store.index)
	}
	// D: read in full.
	b := w.binding(t, keyD)
	if b.Health != larksyncwire.HealthOK || b.LastMessages != 3 || b.LastDocs != 1 || b.Failures != 0 || !b.LastRunAt.Equal(now) {
		t.Fatalf("D: %+v", b)
	}
	if want := larkSyncNextSlot(now, larkSyncDefaultHour, keyD); !b.NextRunAt.Equal(want) || b.NextRunAt.Hour() != larkSyncDefaultHour || !b.NextRunAt.After(now) {
		t.Fatalf("D's next run %v, want %v", b.NextRunAt, want)
	}
	if c := w.store.cursorsOf(keyD); len(c) != 2 || c["drive"] != "200" || c["im:oc_d"] != "1700000000000" {
		t.Fatalf("D's cursors: %v", c)
	}
	if r := w.store.index[larksyncwire.KeyDoc+"doxD"]; r.Revision != 7 || len(r.Sources) != 1 || r.Sources[0].Binding != keyD {
		t.Fatalf("D's index: %+v", r)
	}
	if rows := w.store.outboxOf(keyA); len(rows) != 0 {
		t.Fatalf("A handed over %v", rows)
	}
	// The outbox: D's two chats (messages in time order, the attachment's ref kept) and both documents.
	var chats []kbwire.SourceChat
	byChat := map[string][]kbwire.SourceMessage{}
	for _, o := range w.store.outbox {
		if o.Kind != db.LarkSyncChat {
			continue
		}
		var cb struct {
			Chat     kbwire.SourceChat
			Messages []kbwire.SourceMessage
		}
		if err := json.Unmarshal(o.Payload, &cb); err != nil {
			t.Fatal(err)
		}
		chats = append(chats, cb.Chat)
		byChat[cb.Chat.ID] = cb.Messages
	}
	if len(chats) != 2 || chats[0].ID != "oc_d" || chats[0].Kind != "dm" || chats[1].Kind != "group" || chats[1].Name != "Team" {
		t.Fatalf("chats: %+v", chats)
	}
	if m := byChat["oc_d"]; len(m) != 2 || m[0].ID != "om_1" || !m[0].Mine || m[1].ID != "om_2" || len(m[1].Media) != 1 || m[1].Media[0] != keyD+"|om_2|img_1|image" {
		t.Fatalf("oc_d: %+v", m)
	}
	items, err := larkSyncItems(larkSyncConnector{w.h}).Items(ctx)
	if err != nil || len(items) != 2 {
		t.Fatalf("items: %+v %v", items, err)
	}
	for _, it := range items {
		if it.Source != larksyncwire.ConnectorID || it.CanonicalKey == "" || len(it.Sources) == 0 {
			t.Fatalf("item: %+v", it)
		}
		if it.ID == "doxD" && (it.Revision != 7 || it.Content != "# D" || it.BodyHash != "h7" || it.Sources[0].Binding != keyD) {
			t.Fatalf("D's item: %+v", it)
		}
	}

	// The chat bot is untouched: its settings and its view.
	if after := w.store.raw(botSettingsKey); after != feishuBefore {
		t.Fatalf("bots.feishu changed: %s -> %s", feishuBefore, after)
	}
	_, botViewAfter := w.do(t, "GET", "/api/v1/bots/feishu", "")
	if a, b := larkJSON(t, botViewBefore), larkJSON(t, botViewAfter); a != b {
		t.Fatalf("the chat bot's view changed: %s -> %s", a, b)
	}

	// A, needing authorization again, is not run (nor refreshed) again, whatever the time.
	w.advance(48 * time.Hour)
	w.script("bsync", accC, nil)
	w.h.larkSyncTick(ctx)
	if n := refusedA.Load(); n != 1 || countOf(w.ran, keyA) != 1 {
		t.Fatalf("A was refreshed %d times, ran %d times", n, countOf(w.ran, keyA))
	}
	if countOf(w.ran, keyD) != 2 {
		t.Fatalf("the others go on: %v", w.ran)
	}
}

// A binding that keeps failing backs off (its circuit: 15 minutes twice, then 1h, 6h, 24h) while the others keep
// their daily schedule; one good run closes the circuit.
func TestLarkSyncCircuitBacksOff(t *testing.T) {
	w := newLarkWorld(t)
	ctx := context.Background()
	w.addSyncBot(t, "bsync", "cli_sync", larksyncwire.TierDocs)
	bad, badAcc := w.adopt(t, "bsync", "ou_bad", "acc-bad", "ref-bad", 24*time.Hour)
	good, _ := w.adopt(t, larksyncwire.PrimaryBot, "ou_good", "acc-good", "ref-good", 24*time.Hour)
	fail := func(context.Context, larksyncwire.Binding, func(context.Context) (string, error), map[string]string, larksyncwire.Index) (larksyncwire.Result, error) {
		return larksyncwire.Result{}, &larksyncwire.Error{Kind: larksyncwire.ErrKindTransient, Op: "im", Msg: "502"}
	}
	w.script("bsync", badAcc, fail)
	waits := []time.Duration{15 * time.Minute, 15 * time.Minute, time.Hour, 6 * time.Hour, 24 * time.Hour, 24 * time.Hour}
	for i, want := range waits {
		if i > 0 {
			w.advance(w.binding(t, bad).NextRunAt.Sub(w.now()))
		}
		w.h.larkSyncTick(ctx)
		b := w.binding(t, bad)
		if b.Failures != i+1 || b.Health != larksyncwire.HealthFailing || !b.NextRunAt.Equal(w.now().Add(want)) {
			t.Fatalf("failure %d: %+v (want next in %v)", i+1, b, want)
		}
		if g := w.binding(t, good); g.Failures != 0 || g.Health != larksyncwire.HealthOK {
			t.Fatalf("the good binding caught the failure: %+v", g)
		}
	}
	w.script("bsync", badAcc, nil)
	w.advance(w.binding(t, bad).NextRunAt.Sub(w.now()))
	w.h.larkSyncTick(ctx)
	if b := w.binding(t, bad); b.Failures != 0 || b.Health != larksyncwire.HealthOK || b.NextRunAt.Hour() != larkSyncDefaultHour {
		t.Fatalf("closed: %+v", b)
	}
}

// A member hub runs no binding: the hub it joined does.
func TestLarkSyncMemberHubRunsNothing(t *testing.T) {
	w := newLarkWorld(t)
	w.adopt(t, larksyncwire.PrimaryBot, "ou_a", "acc", "ref", time.Hour)
	w.h.isMemberNow.Store(true)
	w.h.larkSyncTick(context.Background())
	if len(w.ran) != 0 {
		t.Fatalf("a member hub ran %v", w.ran)
	}
}

// firstRun says whether key's reader runs for the first time.
func (w *larkWorld) firstRun(key string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return countOf(w.ran, key) == 1
}

func countOf(xs []string, x string) int {
	n := 0
	for _, y := range xs {
		if y == x {
			n++
		}
	}
	return n
}

func larkJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
