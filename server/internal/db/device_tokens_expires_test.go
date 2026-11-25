package db

import (
	"context"
	"testing"
	"time"
)

func TestDeviceTokenExpiresAt(t *testing.T) {
	d, ctx := open(t), context.Background()
	if _, err := d.pool.Exec(ctx, `TRUNCATE device_tokens`); err != nil {
		t.Fatal(err)
	}
	future := time.Now().UTC().Add(10 * time.Minute).Truncate(time.Microsecond)
	past := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)

	if err := d.CreateDeviceToken(ctx, DeviceToken{ID: "dt_forever", Name: "Phone", Kind: "phone", Role: "full"}, "hash_forever"); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateDeviceToken(ctx, DeviceToken{ID: "dt_live_ro", Name: "Verify", Kind: "readonly", Role: "readonly", Scopes: []string{"usage"}, ExpiresAt: &future}, "hash_live_ro"); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateDeviceToken(ctx, DeviceToken{ID: "dt_expired_ro", Name: "Expired", Kind: "readonly", Role: "readonly", ExpiresAt: &past}, "hash_expired_ro"); err != nil {
		t.Fatal(err)
	}

	got, err := d.DeviceTokenByHash(ctx, "hash_forever")
	if err != nil || got == nil || got.ID != "dt_forever" || got.ExpiresAt != nil {
		t.Fatalf("forever token: %+v %v", got, err)
	}
	got, err = d.DeviceTokenByHash(ctx, "hash_live_ro")
	if err != nil || got == nil || got.ID != "dt_live_ro" || got.Role != "readonly" || got.Kind != "readonly" || got.ExpiresAt == nil || !got.ExpiresAt.Equal(future) {
		t.Fatalf("live readonly token: %+v %v", got, err)
	}
	if got, err := d.DeviceTokenByHash(ctx, "hash_expired_ro"); err != nil || got != nil {
		t.Fatalf("expired token should be filtered by SQL: %+v %v", got, err)
	}
	list, err := d.DeviceTokens(ctx)
	if err != nil || len(list) != 3 {
		t.Fatalf("list: %+v %v", list, err)
	}
	var seenLive, seenExpired bool
	for _, row := range list {
		switch row.ID {
		case "dt_live_ro":
			seenLive = row.ExpiresAt != nil && row.ExpiresAt.Equal(future)
		case "dt_expired_ro":
			seenExpired = row.ExpiresAt != nil && row.ExpiresAt.Equal(past)
		}
	}
	if !seenLive || !seenExpired {
		t.Fatalf("DeviceTokens did not round-trip expires_at: %+v", list)
	}
}
