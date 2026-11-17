package devices

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// usageBackfillMemStore records the backfill's requests and answers its record.
type usageBackfillMemStore struct {
	*memStore
	record db.UsageBackfill
	puts   []usageBackfillPut
}

type usageBackfillPut struct {
	buckets []proto.UsageBucket
	through time.Time
	done    bool
}

func (s *usageBackfillMemStore) UsageBackfill(context.Context, string) (db.UsageBackfill, error) {
	return s.record, nil
}

func (s *usageBackfillMemStore) PutUsageBackfill(_ context.Context, _ string, buckets []proto.UsageBucket, through time.Time, done bool) (int, error) {
	s.puts = append(s.puts, usageBackfillPut{append([]proto.UsageBucket(nil), buckets...), through, done})
	return len(buckets), nil
}

// The manager tells the node what to read: from where the history begins, or the hour after the last stored, to the
// hour the node's live window starts in; nothing once it is done, or without a store to keep it.
func TestUsageBackfillStatus(t *testing.T) {
	now := time.Date(2026, 11, 17, 9, 30, 0, 0, time.UTC)
	st := &usageBackfillMemStore{memStore: newMemStore()}
	d := New(Options{Hub: &fakeHost{}, Store: st})
	status := func() map[string]any {
		t.Helper()
		out, err := d.usageBackfillStatus(context.Background(), "a", now)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	first := now.Add(-usageHistoryKeep).Truncate(time.Hour).Add(time.Hour)
	before := time.Date(2026, 11, 16, 7, 0, 0, 0, time.UTC) // 26 hours back, the start of its hour
	if s := status(); s["done"] != false || s["since"] != first.Unix() || s["before"] != before.Unix() {
		t.Fatalf("a first scan = %v", s)
	}
	st.record.Through = time.Date(2026, 11, 1, 5, 0, 0, 0, time.UTC)
	if s := status(); s["since"] != st.record.Through.Add(time.Hour).Unix() {
		t.Fatalf("after a scan cut off = %v", s)
	}
	st.record.Done = now
	if s := status(); s["done"] != true || s["since"] != nil {
		t.Fatalf("done = %v", s)
	}
	if s, _ := New(Options{Hub: &fakeHost{}, Store: newMemStore()}).usageBackfillStatus(context.Background(), "a", now); s["done"] != true {
		t.Fatalf("no store = %v", s)
	}
}

// A request's hours outside what the manager asked for are not stored (the live window is the live meter's).
func TestPutUsageBackfill(t *testing.T) {
	now := time.Date(2026, 11, 17, 9, 30, 0, 0, time.UTC)
	st := &usageBackfillMemStore{memStore: newMemStore()}
	d := New(Options{Hub: &fakeHost{}, Store: st})
	hour := func(at time.Time) proto.UsageBucket {
		return proto.UsageBucket{Minute: at.Unix(), Model: "opus", CacheRead: 10, Output: 1, Requests: 1}
	}
	params, _ := json.Marshal(map[string]any{"through": now.Add(-2 * time.Hour).Unix(), "done": true, "buckets": []proto.UsageBucket{
		hour(now.Add(-60 * 24 * time.Hour).Truncate(time.Hour)),
		hour(now.Add(-400 * 24 * time.Hour).Truncate(time.Hour)), // before the history
		hour(now.Add(-2 * time.Hour).Truncate(time.Hour)),        // the live window's
	}})
	out, err := d.putUsageBackfill(context.Background(), "a", params, now)
	if err != nil || out["stored"] != 1 {
		t.Fatalf("stored %v, %v", out, err)
	}
	if p := st.puts[0]; len(p.buckets) != 1 || !p.done || p.through.Unix() != now.Add(-2*time.Hour).Unix() {
		t.Fatalf("put = %+v", p)
	}
	if _, err := d.putUsageBackfill(context.Background(), "a", json.RawMessage(`{"buckets":`), now); err == nil {
		t.Fatal("a broken request was taken")
	}
}
