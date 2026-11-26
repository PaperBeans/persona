package db

import (
	"context"
	"testing"
)

func TestHealthTellStore(t *testing.T) {
	d, ctx := open(t), context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	first, created, err := d.InsertHealthTell(ctx, HealthTell{ID: "tell_b", ClientID: "client-first", Text: "first", Source: "untrusted"})
	must(err)
	if !created || first.Status != "received" || first.Source != "shortcut" || first.CreatedAt.IsZero() {
		t.Fatalf("first = %+v, created %v", first, created)
	}
	again, created, err := d.InsertHealthTell(ctx, HealthTell{ID: "tell_retry", ClientID: first.ClientID, Text: "different"})
	must(err)
	if created || again.ID != first.ID || again.Text != "first" {
		t.Fatalf("retry = %+v, created %v", again, created)
	}
	_, _, err = d.InsertHealthTell(ctx, HealthTell{ID: "tell_a", ClientID: "client-second", Text: "second"})
	must(err)
	_, err = d.pool.Exec(ctx, `UPDATE health_tells SET created_at = $1`, first.CreatedAt)
	must(err)
	rows, err := d.HealthTellsPending(ctx, 1)
	must(err)
	if len(rows) != 1 || rows[0].ID != "tell_a" {
		t.Fatalf("tie order / limit = %+v", rows)
	}
	_, err = d.pool.Exec(ctx, `UPDATE health_tells SET created_at = created_at - interval '1 minute' WHERE id = 'tell_b'`)
	must(err)
	rows, err = d.HealthTellsPending(ctx, 10)
	must(err)
	if len(rows) != 2 || rows[0].ID != "tell_b" {
		t.Fatalf("time order = %+v", rows)
	}
	for _, proposed := range []string{"reserved", "replacement"} {
		got, err := d.ReserveHealthTellSession(ctx, first.ID, proposed)
		must(err)
		if got != "reserved" {
			t.Fatalf("reservation = %q", got)
		}
	}
	changed, err := d.SetHealthTellResult(ctx, first.ID, "received", "", "", "refused", true)
	must(err)
	rows, err = d.HealthTellsPending(ctx, 10)
	must(err)
	if !changed || rows[0].Attempts != 1 || rows[0].Text != "first" || rows[0].Error != "refused" {
		t.Fatalf("attempt = %+v", rows)
	}
	changed, err = d.SetHealthTellResult(ctx, first.ID, "delivered", "computer", "reserved", "", false)
	must(err)
	if !changed {
		t.Fatal("delivery not recorded")
	}
	got, _, err := d.InsertHealthTell(ctx, HealthTell{ID: "retry-delivered", ClientID: first.ClientID})
	must(err)
	if got.Text != "" || got.Status != "delivered" || got.SessionID != "reserved" || got.MachineID != "computer" {
		t.Fatalf("delivered = %+v", got)
	}
	changed, err = d.SetHealthTellResult(ctx, first.ID, "failed", "", "", "late", true)
	must(err)
	if changed {
		t.Fatal("terminal row changed")
	}
	_, err = d.SetHealthTellResult(ctx, "tell_a", "failed", "", "", "expired", false)
	must(err)
	_, err = d.ReserveHealthTellSession(ctx, "tell_a", "too-late")
	must(err)
	got, _, err = d.InsertHealthTell(ctx, HealthTell{ID: "retry-failed", ClientID: "client-second"})
	must(err)
	if got.Text != "second" || got.NewSessionID != "" || got.Status != "failed" {
		t.Fatalf("failed = %+v", got)
	}
	rows, err = d.HealthTellsPending(ctx, 10)
	must(err)
	if len(rows) != 0 {
		t.Fatalf("terminal rows pending: %+v", rows)
	}
}

func TestHealthTellContinuationReservation(t *testing.T) {
	d, ctx := open(t), context.Background()
	first, _, err := d.InsertHealthTell(ctx, HealthTell{ID: "tell_target", ClientID: "target-client", Text: "private sentence"})
	if err != nil {
		t.Fatal(err)
	}
	for _, destination := range []struct{ machine, session string }{{"computer", "original"}, {"other", "replacement"}} {
		got, err := d.ReserveHealthTellTarget(ctx, first.ID, destination.machine, destination.session)
		if err != nil || got.MachineID != "computer" || got.SessionID != "original" || !got.UpdatedAt.Equal(first.UpdatedAt) {
			t.Fatalf("target reservation: %+v %v", got, err)
		}
	}
	if changed, err := d.SetHealthTellResult(ctx, first.ID, "received", "", "", "reply lost", true); err != nil || !changed {
		t.Fatal(changed, err)
	}
	got, err := d.ReserveHealthTellTarget(ctx, first.ID, "other", "replacement")
	if err != nil || got.MachineID != "computer" || got.SessionID != "original" || got.Attempts != 1 || got.Text != "private sentence" {
		t.Fatalf("failed attempt lost target: %+v %v", got, err)
	}
	// A definite refusal switches to a reserved start in one write; it can never reselect the old target.
	sid, err := d.ReserveHealthTellSession(ctx, first.ID, "new-chat")
	if err != nil || sid != "new-chat" {
		t.Fatal(sid, err)
	}
	got, err = d.ReserveHealthTellTarget(ctx, first.ID, "other", "replacement")
	if err != nil || got.MachineID != "" || got.SessionID != "" || got.NewSessionID != "new-chat" {
		t.Fatalf("new start retained continuation: %+v %v", got, err)
	}
	if changed, err := d.SetHealthTellResult(ctx, first.ID, "received", "computer", "original", "start refused", true); err != nil || !changed {
		t.Fatal(changed, err)
	}
	got, err = d.ReserveHealthTellTarget(ctx, first.ID, "other", "replacement")
	if err != nil || got.SessionID != "" || got.MachineID != "" || got.NewSessionID != "new-chat" || got.Attempts != 2 {
		t.Fatalf("stale result restored target: %+v %v", got, err)
	}
	if changed, err := d.SetHealthTellResult(ctx, first.ID, "delivered", "computer", "new-chat", "", false); err != nil || !changed {
		t.Fatal(changed, err)
	}
	got, err = d.ReserveHealthTellTarget(ctx, first.ID, "other", "replacement")
	if err != nil || got.Status != "delivered" || got.SessionID != "new-chat" || got.MachineID != "computer" || got.Text != "" {
		t.Fatalf("terminal reservation changed: %+v %v", got, err)
	}
	second, _, err := d.InsertHealthTell(ctx, HealthTell{ID: "tell_failed_target", ClientID: "failed-target-client", Text: "retained"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.ReserveHealthTellTarget(ctx, second.ID, "computer", "uncertain"); err != nil {
		t.Fatal(err)
	}
	if _, err = d.SetHealthTellResult(ctx, second.ID, "failed", "", "", "exhausted", true); err != nil {
		t.Fatal(err)
	}
	got, err = d.ReserveHealthTellTarget(ctx, second.ID, "other", "replacement")
	if err != nil || got.Status != "failed" || got.SessionID != "uncertain" || got.MachineID != "computer" || got.Text != "retained" {
		t.Fatalf("terminal failure lost evidence: %+v %v", got, err)
	}
}
