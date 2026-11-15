package db

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"
)

func sentKeys(rows []PushSent) []string {
	out := make([]string, len(rows))
	for i, e := range rows {
		out[i] = e.RelatedKey
	}
	return out
}

// Rows are appended in order (a batch in one statement), read back newest limit oldest first after a time, and swept
// by age and by count.
func TestPushSentAppendReadPrune(t *testing.T) {
	d, ctx := open(t), context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	var batch []PushSent
	for i := range 5 {
		batch = append(batch, PushSent{At: now.Add(time.Duration(i-5) * time.Hour), Level: "p1", RelatedKey: fmt.Sprintf("k-%d", i), Manager: "manager-a"})
	}
	if err := d.AddPushSent(ctx, batch); err != nil {
		t.Fatal(err)
	}
	// An older push appended later still reads after the earlier rows: the record keeps the order pushes were added.
	if err := d.AddPushSent(ctx, []PushSent{{At: now.Add(-10 * time.Hour), Level: "p0", Manager: ""}}); err != nil {
		t.Fatal(err)
	}
	if err := d.AddPushSent(ctx, nil); err != nil {
		t.Fatal(err)
	}
	all, err := d.PushSentAfter(ctx, now.Add(-24*time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	if got := sentKeys(all); !slices.Equal(got, []string{"k-0", "k-1", "k-2", "k-3", "k-4", ""}) {
		t.Fatalf("rows = %q, want the order they were added in", got)
	}
	if !all[0].At.Equal(batch[0].At) || all[0].At.Location() != time.UTC || all[0].Level != "p1" || all[0].Manager != "manager-a" || all[5].Level != "p0" {
		t.Fatalf("row fields = %+v / %+v", all[0], all[5])
	}
	newest, err := d.PushSentAfter(ctx, now.Add(-24*time.Hour), 2)
	if err != nil || !slices.Equal(sentKeys(newest), []string{"k-4", ""}) {
		t.Fatalf("newest two = %q (%v), want the last two added, oldest first", sentKeys(newest), err)
	}
	// after is exclusive, as the hub's memory keeps only entries after its cutoff.
	after, err := d.PushSentAfter(ctx, batch[2].At, 100)
	if err != nil || !slices.Equal(sentKeys(after), []string{"k-3", "k-4"}) {
		t.Fatalf("after k-2 = %q (%v)", sentKeys(after), err)
	}

	// By age: at or before the cutoff goes (k-0 at now-5h, the p0 at now-10h).
	if n, err := d.PrunePushSent(ctx, batch[0].At, 100); err != nil || n != 2 {
		t.Fatalf("age sweep removed %d (%v), want 2", n, err)
	}
	// By count: only the newest two stay.
	if n, err := d.PrunePushSent(ctx, now.Add(-24*time.Hour), 2); err != nil || n != 2 {
		t.Fatalf("count sweep removed %d (%v), want 2", n, err)
	}
	left, err := d.PushSentAfter(ctx, time.Time{}, 100)
	if err != nil || !slices.Equal(sentKeys(left), []string{"k-3", "k-4"}) {
		t.Fatalf("after the sweeps = %q (%v), want k-3 k-4", sentKeys(left), err)
	}
	if n, err := d.PrunePushSent(ctx, now.Add(-24*time.Hour), 2); err != nil || n != 0 {
		t.Fatalf("a sweep within the limits removed %d (%v)", n, err)
	}
}

// Upgrade: the old whole-list setting's entries move into the table after its rows and the setting goes, in one
// transaction; a second call moves nothing; a value that is not a list stays where it is. On the hub's own database
// (hub_settings) and on one shared with a manager (settings).
func TestAdoptPushSentSetting(t *testing.T) {
	for _, tc := range []struct {
		name string
		open func(t *testing.T) *DB
	}{
		{"shared", func(t *testing.T) *DB { return open(t) }},
		{"hub", func(t *testing.T) *DB {
			d, err := Open(context.Background(), migratedDatabase(t, HubSet), HubSet)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(d.Close)
			return d
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, ctx := tc.open(t), context.Background()
			const key = "hub.push_sent"
			now := time.Now().UTC().Truncate(time.Microsecond)
			if err := d.AddPushSent(ctx, []PushSent{{At: now.Add(-time.Hour), Level: "p1", RelatedKey: "already", Manager: "manager-a"}}); err != nil {
				t.Fatal(err)
			}
			// The setting as the hub wrote it: its pushSent JSON, related_key left out when empty.
			legacy := []map[string]any{
				{"at": now.Add(-3 * time.Hour), "level": "p0", "related_key": "legacy-1", "manager": "manager-a"},
				{"at": now.Add(-2 * time.Hour), "level": "p2", "manager": ""},
			}
			if err := d.PutSetting(ctx, key, legacy); err != nil {
				t.Fatal(err)
			}
			if n, err := d.AdoptPushSentSetting(ctx, key); err != nil || n != 2 {
				t.Fatalf("moved %d (%v), want 2", n, err)
			}
			var gone []PushSent
			if ok, err := d.Setting(ctx, key, &gone); err != nil || ok {
				t.Fatalf("the setting is still there (%v, %v)", ok, err)
			}
			rows, err := d.PushSentAfter(ctx, time.Time{}, 100)
			if err != nil || !slices.Equal(sentKeys(rows), []string{"already", "legacy-1", ""}) {
				t.Fatalf("rows = %q (%v), want the table's row then the setting's in order", sentKeys(rows), err)
			}
			if !rows[1].At.Equal(now.Add(-3*time.Hour)) || rows[1].Level != "p0" || rows[2].Level != "p2" {
				t.Fatalf("moved rows = %+v", rows[1:])
			}
			if n, err := d.AdoptPushSentSetting(ctx, key); err != nil || n != 0 {
				t.Fatalf("a second move: %d (%v), want nothing", n, err)
			}
			if err := d.PutSetting(ctx, key, map[string]any{"not": "a list"}); err != nil {
				t.Fatal(err)
			}
			if _, err := d.AdoptPushSentSetting(ctx, key); err == nil {
				t.Fatal("a value that is not a list was taken")
			}
			var kept map[string]any
			if ok, err := d.Setting(ctx, key, &kept); err != nil || !ok {
				t.Fatalf("the unreadable setting was dropped (%v, %v)", ok, err)
			}
			if rows, _ := d.PushSentAfter(ctx, time.Time{}, 100); len(rows) != 3 {
				t.Fatalf("a failed move changed the table: %d rows", len(rows))
			}
		})
	}
}
