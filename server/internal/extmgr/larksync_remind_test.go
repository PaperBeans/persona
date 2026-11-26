package extmgr

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/larksyncwire"
)

// An authorization lasts a year (or less when the platform says so): the owner is reminded 14, 7 and 1 days before it
// ends, once each, and then the binding needs authorizing again.
func TestLarkSyncRemindsBeforeTheAuthorizationEnds(t *testing.T) {
	w := newLarkWorld(t)
	ctx := context.Background()
	key, _ := w.adopt(t, larksyncwire.PrimaryBot, "ou_a", "acc", "ref", time.Hour)
	start := w.now()
	remindsOf := func() []map[string]any {
		var out []map[string]any
		for _, p := range w.pushes() {
			if p["collapse_id"] == "lark_sync.reauth."+key {
				out = append(out, p)
			}
		}
		return out
	}
	at := func(daysLeft int) {
		w.advance(start.Add(larkSyncAuthLasts - time.Duration(daysLeft)*24*time.Hour).Sub(w.now()))
		w.h.larkSyncRemind(ctx)
		w.h.larkSyncRemind(ctx) // asked twice: still once
	}
	at(20)
	if n := len(remindsOf()); n != 0 {
		t.Fatalf("20 days left: %d reminders", n)
	}
	want := 0
	for _, days := range []int{14, 7, 1} {
		at(days)
		want++
		got := remindsOf()
		if len(got) != want {
			t.Fatalf("%d days left: %d reminders", days, len(got))
		}
		payloads, _ := got[len(got)-1]["payloads"].(map[string]any)
		en, _ := payloads["en"].(map[string]any)
		meta, _ := en["lark_sync"].(map[string]any)
		if meta["binding"] != key || meta["reauth_due_days"] != float64(days) {
			t.Fatalf("%d days left: %v", days, en)
		}
		_, view := w.do(t, "GET", "/api/v1/bots/feishu/sync", "")
		if d := view["bindings"].([]any)[0].(map[string]any)["reauth_due_days"]; d != float64(days) {
			t.Fatalf("view: %v days, want %d", d, days)
		}
	}
	if b := w.binding(t, key); !slices.Equal(b.RemindedDays, []int{14, 7, 1}) || b.Health != larksyncwire.HealthOK {
		t.Fatalf("reminded: %+v", b)
	}
	at(0)
	if b := w.binding(t, key); b.Health != larksyncwire.HealthNeedsReauth || len(remindsOf()) != 3 {
		t.Fatalf("ended: %+v, %d reminders", b, len(remindsOf()))
	}
}

// The platform's own end of the refresh token, when earlier than a year, is the one reminded of; a jump past two
// thresholds at once reminds once.
func TestLarkSyncRemindsOfThePlatformsEnd(t *testing.T) {
	w := newLarkWorld(t)
	ctx := context.Background()
	key, _ := w.adopt(t, larksyncwire.PrimaryBot, "ou_a", "acc", "ref", time.Hour)
	if _, err := w.h.larkSyncChange(ctx, func(st *larkSyncState) error {
		st.binding(key).RefreshExpiresAt = w.now().Add(5 * 24 * time.Hour)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	w.h.larkSyncRemind(ctx)
	if b := w.binding(t, key); !slices.Equal(b.RemindedDays, []int{14, 7}) || len(w.pushes()) != 1 {
		t.Fatalf("reminded %v, %d pushes", b.RemindedDays, len(w.pushes()))
	}
}
