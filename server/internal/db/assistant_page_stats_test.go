package db

import (
	"slices"
	"testing"
	"time"
)

// Each day's taps: how many, delivered, redirected and failed, the queue time (tap to delivered) and the turn wait
// (delivered to the turn that took them up) as median, 95th percentile and longest; and the conversations whose turns
// are followed.
func TestAssistantPageTapDays(t *testing.T) {
	d, ctx := pagesDB(t)
	p := mustKeep(t, d, mustCreate(t, d, draft(1, "c1", "Running", pagesBase)), pagesBase)
	day := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	deliver := func(at time.Time, queue, wait time.Duration, redirected bool) AssistantPageActionRow {
		t.Helper()
		a, _, err := d.BeginAssistantPageAction(ctx, pageTap(p, 1, "done", map[string]string{"n": at.String()}, at))
		if err != nil {
			t.Fatal(err)
		}
		claimed, err := d.ClaimAssistantPageDelivery(ctx, a.ID, time.Now().Add(time.Minute))
		if err != nil || claimed == nil {
			t.Fatalf("%v %v", claimed, err)
		}
		if _, err := d.SettleAssistantPageDelivery(ctx, AssistantPageDelivery{ID: a.ID, Lease: *claimed.LeaseUntil, Status: AssistantPageTapSent,
			TargetMachine: "m1", TargetConversation: "c1", Redirected: redirected, At: at.Add(queue)}); err != nil {
			t.Fatal(err)
		}
		if wait > 0 {
			if _, err := d.MarkAssistantPageTurnStarted(ctx, "m1", "c1", at.Add(queue+wait)); err != nil {
				t.Fatal(err)
			}
			if _, err := d.MarkAssistantPageTurnEnded(ctx, "m1", "c1", at.Add(queue+wait+time.Second)); err != nil {
				t.Fatal(err)
			}
		}
		return *a
	}
	// day 1: four delivered taps (queue 1, 2, 3, 10 s; turn wait 4, 4, 4, 40 s), one redirected, one failed
	for i, q := range []time.Duration{time.Second, 2 * time.Second, 3 * time.Second, 10 * time.Second} {
		wait := 4 * time.Second
		if i == 3 {
			wait = 40 * time.Second
		}
		deliver(day.Add(8*time.Hour+time.Duration(i)*time.Minute), q, wait, i == 0)
	}
	failed, _, err := d.BeginAssistantPageAction(ctx, pageTap(p, 1, "skip", nil, day.Add(9*time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	claimed, _ := d.ClaimAssistantPageDelivery(ctx, failed.ID, time.Now().Add(time.Minute))
	if _, err := d.SettleAssistantPageDelivery(ctx, AssistantPageDelivery{ID: failed.ID, Lease: *claimed.LeaseUntil, Status: AssistantPageTapFailed, Error: "x"}); err != nil {
		t.Fatal(err)
	}
	// day 2: one delivered, its turn not started
	deliver(day.Add(24*time.Hour+time.Hour), 500*time.Millisecond, 0, false)

	days, err := d.AssistantPageTapDays(ctx, day, "UTC")
	if err != nil || len(days) != 2 {
		t.Fatalf("%+v %v", days, err)
	}
	first := days[0]
	if !first.Day.Equal(day) || first.Taps != 5 || first.Delivered != 4 || first.Redirected != 1 || first.Failed != 1 || first.Started != 4 ||
		first.QueueP50 != 2500*time.Millisecond || first.QueueMax != 10*time.Second || first.QueueP95 <= 3*time.Second ||
		first.WaitP50 != 4*time.Second || first.WaitMax != 40*time.Second || first.WaitP95 <= 4*time.Second {
		t.Fatalf("day 1: %+v", first)
	}
	if second := days[1]; second.Taps != 1 || second.Delivered != 1 || second.Started != 0 || second.QueueMax != 500*time.Millisecond || second.WaitMax != 0 {
		t.Fatalf("day 2: %+v", second)
	}
	// in another zone the days start elsewhere: day 2's tap (01:00 UTC) is the day before's in Los Angeles
	la, err := d.AssistantPageTapDays(ctx, day.Add(-24*time.Hour), "America/Los_Angeles")
	if err != nil || len(la) != 1 || la[0].Taps != 6 {
		t.Fatalf("Los Angeles: %+v %v", la, err)
	}
	targets, err := d.AssistantPageTurnTargets(ctx)
	if err != nil || !slices.Equal(targets, []AssistantPageTurnTarget{{Machine: "m1", Conversation: "c1"}}) {
		t.Fatalf("followed: %+v %v", targets, err)
	}
}
