package db

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestUpdateInteractionDeadlineChangesOnlyTheSamePendingCard(t *testing.T) {
	d, ctx := open(t), context.Background()
	created := time.Now().UTC().Round(0)
	original := time.Now().UTC().Add(-time.Minute).Round(0)
	ix := proto.Interaction{ID: "deadline_same_pending", MachineID: "server", SessionID: "s", Kind: proto.IxPermission,
		ToolName: "Bash", ToolInput: []byte(`{"command":"printf test"}`), CreatedAt: created, ExpiresAt: original}
	if err := d.InsertInteraction(ctx, ix); err != nil {
		t.Fatal(err)
	}
	extended := original.Add(24 * time.Hour)
	if err := d.UpdateInteractionDeadline(ctx, ix.ID, created, extended); err != nil {
		t.Fatal(err)
	}
	rec, err := d.InteractionRecord(ctx, ix.ID)
	if err != nil || rec == nil {
		t.Fatalf("read updated interaction: %+v, %v", rec, err)
	}
	want := ix
	want.ExpiresAt = extended
	gotJSON, err := json.Marshal(rec.Interaction)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotJSON, wantJSON) || rec.Status != "pending" {
		t.Fatalf("updated interaction = %+v, status %q; want only expires_at changed to %v", rec.Interaction, rec.Status, extended)
	}

	if err := d.UpdateInteractionDeadline(ctx, ix.ID, created.Add(time.Second), extended.Add(time.Hour)); err == nil {
		t.Fatal("update with another creation timestamp succeeded")
	}
	if err := d.UpdateInteractionDeadline(ctx, "deadline_missing", created, extended.Add(time.Hour)); err == nil {
		t.Fatal("update of a missing interaction succeeded")
	}
	if err := d.FinishInteraction(ctx, ix.ID, "expired", nil); err != nil {
		t.Fatal(err)
	}
	if err := d.UpdateInteractionDeadline(ctx, ix.ID, created, extended.Add(2*time.Hour)); err == nil {
		t.Fatal("update of an already-resolved interaction succeeded")
	}
	rec, err = d.InteractionRecord(ctx, ix.ID)
	if err != nil || rec == nil {
		t.Fatalf("read resolved interaction: %+v, %v", rec, err)
	}
	if rec.Status != "expired" || !rec.Interaction.ExpiresAt.Equal(extended) {
		t.Fatalf("rejected writes changed the card: status %q, expires_at %v; want expired at %v", rec.Status, rec.Interaction.ExpiresAt, extended)
	}
}
