package db

import (
	"context"
	"testing"
)

func TestClientPasswordChangesAndIssuance(t *testing.T) {
	d, ctx := open(t), context.Background()
	if _, err := d.pool.Exec(ctx, `TRUNCATE client_password, device_tokens`); err != nil {
		t.Fatal(err)
	}
	p := ClientPassword{Enabled: true, Revision: "first", ServerKeys: []byte("server-keys"), CredentialID: []byte("identifier"), Record: []byte("opaque-record")}
	if saved, _, err := d.SaveClientPassword(ctx, p, "", false); err != nil || !saved {
		t.Fatalf("first save: %v %v", saved, err)
	}
	if saved, _, err := d.SaveClientPassword(ctx, p, "", false); err != nil || saved {
		t.Fatalf("stale save: %v %v", saved, err)
	}
	stored, err := d.ClientPassword(ctx)
	if err != nil || stored == nil || string(stored.Record) != "opaque-record" || stored.Revision != "first" {
		t.Fatalf("read: %+v %v", stored, err)
	}
	device := DeviceToken{ID: "password-device", Name: "browser", Kind: "other", Role: "full"}
	if issued, err := d.IssuePasswordDevice(ctx, "first", device, "device-hash"); err != nil || !issued {
		t.Fatalf("issue: %v %v", issued, err)
	}
	p.Enabled, p.Revision = false, "disabled"
	if saved, ids, err := d.SaveClientPassword(ctx, p, "first", false); err != nil || !saved || len(ids) != 0 {
		t.Fatalf("disable: %v %v %v", saved, ids, err)
	}
	if current, _ := d.DeviceTokenByHash(ctx, "device-hash"); current == nil {
		t.Fatal("disable revoked existing token")
	}
	for _, revision := range []string{"first", "disabled"} {
		if issued, err := d.IssuePasswordDevice(ctx, revision, DeviceToken{ID: revision, Name: "stale", Kind: "phone", Role: "full"}, revision); err != nil || issued {
			t.Fatalf("disabled issued: %v %v", issued, err)
		}
	}
	p.Enabled, p.Revision = true, "new-password"
	if saved, ids, err := d.SaveClientPassword(ctx, p, "disabled", true); err != nil || !saved || len(ids) != 1 || ids[0] != device.ID {
		t.Fatalf("revoke: %v %v %v", saved, ids, err)
	}
	if current, _ := d.DeviceTokenByHash(ctx, "device-hash"); current != nil {
		t.Fatal("revoked token still valid")
	}
	if issued, err := d.IssuePasswordDevice(ctx, "first", device, "old-password-token"); err != nil || issued {
		t.Fatalf("old password issued: %v %v", issued, err)
	}
}
