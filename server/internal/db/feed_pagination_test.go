package db

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestFeedPageUpdatedCardsDoNotStarveProgress(t *testing.T) {
	// DELETE removes the fixture rows but retains index pages that can change a
	// later planner assertion after statistics refresh. Keep this bulk fixture
	// in its own migrated clone, including its table/index/catalog state.
	d, err := Open(context.Background(), migratedDatabase(t, AllSets), AllSets)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	ctx := context.Background()
	conversation := fmt.Sprintf("feed-page-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := d.pool.Exec(cleanup, `DELETE FROM assistant_feed WHERE conversation = $1`, conversation); err != nil {
			t.Error(err)
		}
	})
	oldAt := time.Now().UTC().Truncate(time.Microsecond).Add(-3 * time.Hour)
	since, until := oldAt.Add(time.Hour), oldAt.Add(2*time.Hour)
	updated := since.Add(time.Minute)
	// All 500 already-dismissed cards retain the same old at. Their newly
	// persisted updated_at makes them eligible for the incremental request.
	_, err = d.pool.Exec(ctx, `INSERT INTO assistant_feed (id,conversation,at,kind,ref,body,updated_at)
		SELECT $1 || '-' || lpad(n::text,3,'0'),$1,$2,'message','synthetic-item-' || n,
			jsonb_build_object('draft','Old progress','dismissed_at',$3::timestamptz,'dismissal_id','synthetic-decision'),$3
		FROM generate_series(0,499) n`, conversation, oldAt, updated)
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.pool.Exec(ctx, `INSERT INTO assistant_feed (id,conversation,at,kind,ref,body)
		VALUES ($1,$2,$3,'message','synthetic-new-item','{"draft":"New progress"}')`, conversation+"-new", conversation, since.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	first, err := d.FeedPage(ctx, conversation, since, until, time.Time{}, "", 500)
	if err != nil || len(first) != 500 {
		t.Fatalf("first page: %d %v", len(first), err)
	}
	if first[0].ID != conversation+"-000" || first[499].ID != conversation+"-499" {
		t.Fatalf("tie order: %s .. %s", first[0].ID, first[499].ID)
	}
	// A change newer than the frozen window belongs to the next incremental
	// traversal, not a later page of this one.
	_, err = d.pool.Exec(ctx, `UPDATE assistant_feed SET updated_at=$2 WHERE id=$1`, conversation+"-000", until.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	last := first[len(first)-1]
	second, err := d.FeedPage(ctx, conversation, since, until, last.At, last.ID, 500)
	if err != nil || len(second) != 1 || second[0].ID != conversation+"-new" {
		t.Fatalf("second page: %+v %v", second, err)
	}
	seen := map[string]bool{}
	for _, row := range append(first, second...) {
		if seen[row.ID] {
			t.Fatalf("duplicate row: %s", row.ID)
		}
		seen[row.ID] = true
	}
	if len(seen) != 501 {
		t.Fatalf("returned %d rows, want 501", len(seen))
	}
	next, err := d.FeedPage(ctx, conversation, until, until.Add(2*time.Minute), time.Time{}, "", 500)
	if err != nil || len(next) != 1 || next[0].ID != conversation+"-000" {
		t.Fatalf("next change window: %+v %v", next, err)
	}
	// Legacy pagination is deliberately unchanged, including its old-row
	// selection; only clients opting into FeedPage use the stable boundary.
	legacy, err := d.Feed(ctx, conversation, since, 500)
	if err != nil || len(legacy) != 500 || legacy[0].ID != conversation+"-000" {
		t.Fatalf("legacy feed changed: %+v %v", legacy, err)
	}
}

func TestFeedPageSentAndDismissedCardListedOnce(t *testing.T) {
	d := open(t)
	ctx := context.Background()
	conversation := fmt.Sprintf("feed-page-sent-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := d.pool.Exec(cleanup, `DELETE FROM assistant_feed WHERE conversation=$1`, conversation); err != nil {
			t.Error(err)
		}
	})
	old := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
	since, until := old.Add(time.Minute), old.Add(10*time.Minute)
	_, err := d.pool.Exec(ctx, `INSERT INTO assistant_feed (id,conversation,at,kind,ref,body,sent_at,updated_at)
		VALUES ($1,$1,$2,'message','synthetic-item','{}',$3,$3)`, conversation, old, since.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := d.FeedPage(ctx, conversation, since, until, time.Time{}, "", 500)
	if err != nil || len(rows) != 1 {
		t.Fatalf("card counted twice: %+v %v", rows, err)
	}
}
