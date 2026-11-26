package db

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var tapCount atomic.Int64

// pageTap is a tap on page p's action at version, from the phone.
func pageTap(p *AssistantPageRow, version int64, action string, values map[string]string, at time.Time) AssistantPageActionRow {
	n := tapCount.Add(1)
	return AssistantPageActionRow{ID: fmt.Sprintf("pa_%016x", n), PageID: p.ID, Action: action, Values: values, PageVersion: version,
		Text:      fmt.Sprintf(`Done today <persona-action view="-pages/%s" action=%q>{"values":{}}</persona-action>`, p.ID, action),
		MessageID: fmt.Sprintf("msg-%d", n), Device: "phone", At: at}
}

func TestAssistantPageTapDedupeAndRate(t *testing.T) {
	d, ctx := pagesDB(t)
	p := mustKeep(t, d, mustCreate(t, d, draft(1, "c1", "Running", pagesBase)), pagesBase)
	at := pagesBase.Add(time.Hour)
	first, created, err := d.BeginAssistantPageAction(ctx, pageTap(p, 1, "done", map[string]string{"km": "5"}, at))
	if err != nil || !created || first.Status != AssistantPageTapPending || first.Dedupe == "" || first.Values["km"] != "5" ||
		first.DeliveredAt != nil || first.LeaseUntil != nil {
		t.Fatalf("%+v %v %v", first, created, err)
	}
	// The same tap again within 30 seconds is the first one, even tapped at once from two places.
	var made atomic.Int32
	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, created, err := d.BeginAssistantPageAction(ctx, pageTap(p, 1, "done", map[string]string{"km": "5"}, at.Add(29*time.Second)))
			if err != nil || got.ID != first.ID {
				t.Error(got, err)
			}
			if created {
				made.Add(1)
			}
		}()
	}
	wg.Wait()
	if made.Load() != 0 {
		t.Fatalf("a repeated tap stored %d times", made.Load())
	}
	// Another value, version, action or device is another tap; so is the same one 30 seconds later.
	others := []AssistantPageActionRow{
		pageTap(p, 1, "done", map[string]string{"km": "6"}, at.Add(time.Second)),
		pageTap(p, 2, "done", map[string]string{"km": "5"}, at.Add(time.Second)),
		pageTap(p, 1, "skip", map[string]string{"km": "5"}, at.Add(time.Second)),
		func() AssistantPageActionRow {
			a := pageTap(p, 1, "done", map[string]string{"km": "5"}, at.Add(time.Second))
			a.Device = "web"
			return a
		}(),
		pageTap(p, 1, "done", map[string]string{"km": "5"}, at.Add(AssistantPageTapDedupe)),
	}
	for i, a := range others {
		if got, created, err := d.BeginAssistantPageAction(ctx, a); err != nil || !created || got.ID != a.ID {
			t.Fatalf("tap %d: %+v %v %v", i, got, created, err)
		}
	}
	// 10 taps a minute: 6 so far.
	for i := range AssistantPageTapsPerMinute - 6 {
		if _, created, err := d.BeginAssistantPageAction(ctx, pageTap(p, 1, "add", map[string]string{"n": fmt.Sprint(i)}, at.Add(40*time.Second))); err != nil || !created {
			t.Fatal(i, err)
		}
	}
	if _, _, err := d.BeginAssistantPageAction(ctx, pageTap(p, 1, "add", map[string]string{"n": "x"}, at.Add(50*time.Second))); !errors.Is(err, ErrAssistantPageTapRate) {
		t.Fatalf("an 11th tap in a minute: %v", err)
	}
	if got, created, err := d.BeginAssistantPageAction(ctx, pageTap(p, 1, "add", map[string]string{"n": "0"}, at.Add(50*time.Second))); err != nil || created {
		t.Fatalf("a repeated tap is answered even at the limit: %+v %v %v", got, created, err)
	}
	if _, created, err := d.BeginAssistantPageAction(ctx, pageTap(p, 1, "add", map[string]string{"n": "x"}, at.Add(time.Minute+time.Second))); err != nil || !created {
		t.Fatalf("the next minute: %v", err)
	}
	list, err := d.AssistantPageActions(ctx, p.ID, 0)
	if err != nil || len(list) != 11 || list[0].Values["n"] != "x" || list[len(list)-1].ID != first.ID {
		t.Fatalf("%d taps %v", len(list), err)
	}
	// Taps only on kept pages.
	draftPage := mustCreate(t, d, draft(2, "c1", "Draft", pagesBase))
	if _, _, err := d.BeginAssistantPageAction(ctx, pageTap(draftPage, 1, "done", nil, at)); !errors.Is(err, ErrAssistantPageNotKept) {
		t.Fatalf("a tap on a draft: %v", err)
	}
	if _, _, err := d.RemoveAssistantPage(ctx, p.ID, AssistantPageRemovedByOwner, at); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.BeginAssistantPageAction(ctx, pageTap(p, 1, "done", nil, at.Add(time.Hour))); !errors.Is(err, ErrAssistantPageRemoved) {
		t.Fatalf("a tap on a removed page: %v", err)
	}
	missing := *p
	missing.ID = "pg_ffffffffffffffff"
	if _, _, err := d.BeginAssistantPageAction(ctx, pageTap(&missing, 1, "done", nil, at)); !errors.Is(err, ErrAssistantPageNotFound) {
		t.Fatalf("a tap on no page: %v", err)
	}
	bad := pageTap(p, 1, "done", nil, at)
	bad.ID = "tap-1"
	if _, _, err := d.BeginAssistantPageAction(ctx, bad); err == nil {
		t.Fatal("a tap id out of shape")
	}
}

func TestAssistantPageTapDelivery(t *testing.T) {
	d, ctx := pagesDB(t)
	p := mustKeep(t, d, mustCreate(t, d, draft(1, "c1", "Running", pagesBase)), pagesBase)
	at := time.Now().UTC().Truncate(time.Microsecond)
	a, _, err := d.BeginAssistantPageAction(ctx, pageTap(p, 1, "done", nil, at))
	if err != nil {
		t.Fatal(err)
	}
	pending, err := d.PendingAssistantPageActions(ctx, 0)
	if err != nil || len(pending) != 1 || pending[0].ID != a.ID {
		t.Fatalf("pending %+v %v", pending, err)
	}
	leased, err := d.ClaimAssistantPageDelivery(ctx, a.ID, at.Add(time.Minute))
	if err != nil || leased == nil || leased.LeaseUntil == nil || !leased.LeaseUntil.Equal(at.Add(time.Minute)) {
		t.Fatalf("lease %+v %v", leased, err)
	}
	if again, err := d.ClaimAssistantPageDelivery(ctx, a.ID, at.Add(time.Minute)); err != nil || again != nil {
		t.Fatalf("leased twice: %+v %v", again, err)
	}
	if pending, err := d.PendingAssistantPageActions(ctx, 0); err != nil || len(pending) != 0 {
		t.Fatalf("a leased tap is not pending: %+v %v", pending, err)
	}
	if _, err := d.SettleAssistantPageDelivery(ctx, AssistantPageDelivery{ID: a.ID, Lease: at, Status: AssistantPageTapSent, At: at}); err == nil {
		t.Fatal("settled with another lease")
	}
	if _, err := d.SettleAssistantPageDelivery(ctx, AssistantPageDelivery{ID: a.ID, Lease: *leased.LeaseUntil, Status: AssistantPageTapSent}); err == nil {
		t.Fatal("sent without a time")
	}
	delivered := at.Add(2 * time.Second)
	sent, err := d.SettleAssistantPageDelivery(ctx, AssistantPageDelivery{ID: a.ID, Lease: *leased.LeaseUntil, Status: AssistantPageTapSent,
		TargetMachine: "m1", TargetConversation: "main-1", Redirected: true, At: delivered})
	if err != nil || sent.Status != AssistantPageTapSent || sent.DeliveredAt == nil || !sent.DeliveredAt.Equal(delivered) ||
		sent.TargetConversation != "main-1" || !sent.Redirected || sent.LeaseUntil != nil || sent.Revision != 1 {
		t.Fatalf("sent %+v %v", sent, err)
	}
	if again, err := d.ClaimAssistantPageDelivery(ctx, a.ID, at.Add(time.Minute)); err != nil || again != nil {
		t.Fatalf("a sent tap leased again: %+v %v", again, err)
	}
	// The turn that answers: started once it begins after the delivery, ended when the conversation goes idle.
	if marked, err := d.MarkAssistantPageTurnEnded(ctx, "m1", "main-1", delivered.Add(time.Second)); err != nil || len(marked) != 0 {
		t.Fatalf("ended before it started: %+v %v", marked, err)
	}
	if marked, err := d.MarkAssistantPageTurnStarted(ctx, "m1", "main-1", delivered.Add(-time.Second)); err != nil || len(marked) != 0 {
		t.Fatalf("a turn that started before the delivery: %+v %v", marked, err)
	}
	if marked, err := d.MarkAssistantPageTurnStarted(ctx, "m1", "other", delivered.Add(time.Second)); err != nil || len(marked) != 0 {
		t.Fatalf("another conversation's turn: %+v %v", marked, err)
	}
	started, err := d.MarkAssistantPageTurnStarted(ctx, "m1", "main-1", delivered.Add(3*time.Second))
	if err != nil || len(started) != 1 || started[0].TurnStartedAt == nil || !started[0].TurnStartedAt.Equal(delivered.Add(3*time.Second)) {
		t.Fatalf("started %+v %v", started, err)
	}
	if again, err := d.MarkAssistantPageTurnStarted(ctx, "m1", "main-1", delivered.Add(4*time.Second)); err != nil || len(again) != 0 {
		t.Fatalf("started twice: %+v %v", again, err)
	}
	ended, err := d.MarkAssistantPageTurnEnded(ctx, "m1", "main-1", delivered.Add(9*time.Second))
	if err != nil || len(ended) != 1 || ended[0].TurnEndedAt == nil || !ended[0].TurnEndedAt.Equal(delivered.Add(9*time.Second)) {
		t.Fatalf("ended %+v %v", ended, err)
	}
	if again, err := d.MarkAssistantPageTurnEnded(ctx, "m1", "main-1", delivered.Add(10*time.Second)); err != nil || len(again) != 0 {
		t.Fatalf("ended twice: %+v %v", again, err)
	}
	// A failed delivery waits for the owner's Retry; a lease that ran out can be taken again.
	b, _, err := d.BeginAssistantPageAction(ctx, pageTap(p, 1, "skip", nil, at))
	if err != nil {
		t.Fatal(err)
	}
	leased, err = d.ClaimAssistantPageDelivery(ctx, b.ID, at.Add(time.Minute))
	if err != nil || leased == nil {
		t.Fatal(leased, err)
	}
	failed, err := d.SettleAssistantPageDelivery(ctx, AssistantPageDelivery{ID: b.ID, Lease: *leased.LeaseUntil, Status: AssistantPageTapFailed, Error: "conversation is gone"})
	if err != nil || failed.Status != AssistantPageTapFailed || failed.Error != "conversation is gone" || failed.DeliveredAt != nil {
		t.Fatalf("failed %+v %v", failed, err)
	}
	if again, err := d.ClaimAssistantPageDelivery(ctx, b.ID, at.Add(time.Minute)); err != nil || again != nil {
		t.Fatalf("a failed tap leased: %+v %v", again, err)
	}
	retried, err := d.RetryAssistantPageAction(ctx, b.ID)
	if err != nil || retried.Status != AssistantPageTapPending || retried.Error != "" || retried.Revision != 2 {
		t.Fatalf("retried %+v %v", retried, err)
	}
	if same, err := d.RetryAssistantPageAction(ctx, a.ID); err != nil || same.Status != AssistantPageTapSent {
		t.Fatalf("retrying a sent tap: %+v %v", same, err)
	}
	if none, err := d.RetryAssistantPageAction(ctx, "pa_ffffffffffffffff"); err != nil || none != nil {
		t.Fatalf("%+v %v", none, err)
	}
	expired, err := d.ClaimAssistantPageDelivery(ctx, b.ID, time.Now().Add(-time.Second))
	if err != nil || expired == nil {
		t.Fatal(expired, err)
	}
	if retaken, err := d.ClaimAssistantPageDelivery(ctx, b.ID, time.Now().Add(time.Minute)); err != nil || retaken == nil {
		t.Fatalf("a run-out lease not taken again: %+v %v", retaken, err)
	}
	if _, err := d.SettleAssistantPageDelivery(ctx, AssistantPageDelivery{ID: b.ID, Lease: *expired.LeaseUntil, Status: AssistantPageTapSent, At: at}); err == nil {
		t.Fatal("the old lease settled a tap another process holds")
	}
}

// Taps are kept 90 days and at most 200 a page; one waiting for delivery stays.
func TestAssistantPageTapTrim(t *testing.T) {
	d, ctx := pagesDB(t)
	p := mustKeep(t, d, mustCreate(t, d, draft(1, "c1", "Running", pagesBase)), pagesBase)
	q := mustKeep(t, d, mustCreate(t, d, draft(2, "c1", "Reading", pagesBase)), pagesBase)
	settle := func(a *AssistantPageActionRow) {
		t.Helper()
		if _, err := d.pool.Exec(ctx, `UPDATE assistant_page_actions SET status = 'sent' WHERE id = $1`, a.ID); err != nil {
			t.Fatal(err)
		}
	}
	old, _, err := d.BeginAssistantPageAction(ctx, pageTap(q, 1, "done", nil, pagesBase))
	if err != nil {
		t.Fatal(err)
	}
	settle(old)
	oldPending, _, err := d.BeginAssistantPageAction(ctx, pageTap(q, 1, "skip", nil, pagesBase))
	if err != nil {
		t.Fatal(err)
	}
	start := pagesBase.Add(24 * time.Hour)
	for i := range AssistantPageTapsKept + 5 {
		a, _, err := d.BeginAssistantPageAction(ctx, pageTap(p, 1, "done", map[string]string{"n": fmt.Sprint(i)}, start.Add(time.Duration(i)*7*time.Second)))
		if err != nil {
			t.Fatal(i, err)
		}
		if i != 2 {
			settle(a)
		}
	}
	now := pagesBase.Add(AssistantPageTapLife)
	n, err := d.TrimAssistantPageActions(ctx, now)
	if err != nil || n != 5 {
		t.Fatalf("trimmed %d %v", n, err)
	}
	left, err := d.AssistantPageActions(ctx, p.ID, AssistantPageTapsKept)
	if err != nil || len(left) != AssistantPageTapsKept || left[0].Values["n"] != fmt.Sprint(AssistantPageTapsKept+4) {
		t.Fatalf("%d left %v", len(left), err)
	}
	if got, err := d.AssistantPageAction(ctx, oldPending.ID); err != nil || got == nil {
		t.Fatalf("a pending tap trimmed: %v", err)
	}
	if got, err := d.AssistantPageAction(ctx, old.ID); err != nil || got != nil {
		t.Fatalf("a 90-day-old tap kept: %+v %v", got, err)
	}
	if n := countRows(t, d, `SELECT count(*) FROM assistant_page_actions WHERE page_id = $1 AND values->>'n' = '2'`, p.ID); n != 1 {
		t.Fatalf("the pending tap past 200: %d", n)
	}
}
