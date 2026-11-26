package devices

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// usageResplitMemStore answers the re-split's record and records its requests.
type usageResplitMemStore struct {
	*memStore
	rec      db.UsageResplitRecord
	notSplit bool
	from, to time.Time // what UsageNotSplit was asked
	puts     [][]proto.UsageBucket
	through  []time.Time
	done     []bool
	dirs     []proto.UsageDir
}

func (s *usageResplitMemStore) UsageResplit(context.Context, string) (db.UsageResplitRecord, error) {
	return s.rec, nil
}
func (s *usageResplitMemStore) UsageNotSplit(_ context.Context, _ string, from, to time.Time) (bool, error) {
	s.from, s.to = from, to
	return s.notSplit, nil
}
func (s *usageResplitMemStore) ResplitUsage(_ context.Context, _ string, buckets []proto.UsageBucket, through time.Time, done bool) (int, error) {
	s.puts, s.through, s.done = append(s.puts, append([]proto.UsageBucket(nil), buckets...)), append(s.through, through), append(s.done, done)
	return len(buckets), nil
}
func (s *usageResplitMemStore) PutUsageDirs(_ context.Context, _ string, dirs []proto.UsageDir) error {
	s.dirs = append(s.dirs, dirs...)
	return nil
}

// The manager tells a node what to re-split: a week ago to where its live window starts, on from the ten minutes
// after the last recorded; nothing once done, when nothing there is not split (recorded done), or without a store.
func TestUsageResplitStatus(t *testing.T) {
	now := time.Date(2026, 11, 27, 9, 35, 0, 0, time.UTC)
	st := &usageResplitMemStore{memStore: newMemStore(), notSplit: true}
	d := New(Options{Hub: &fakeHost{}, Store: st})
	status := func() map[string]any {
		t.Helper()
		out, err := d.usageResplitStatus(context.Background(), "a", now)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	since, before := time.Date(2026, 11, 20, 9, 40, 0, 0, time.UTC), time.Date(2026, 11, 26, 7, 30, 0, 0, time.UTC)
	if s := status(); s["done"] != false || s["since"] != since.Unix() || s["before"] != before.Unix() || !st.from.Equal(since) || !st.to.Equal(before) {
		t.Fatalf("first = %v (asked %v to %v)", s, st.from, st.to)
	}
	st.rec.Through = time.Date(2026, 11, 24, 3, 0, 0, 0, time.UTC)
	if s := status(); s["since"] != st.rec.Through.Add(10*time.Minute).Unix() {
		t.Fatalf("cut off = %v", s)
	}
	st.notSplit = false
	if s := status(); s["done"] != true || len(st.done) != 1 || !st.done[0] {
		t.Fatalf("nothing to split = %v, recorded %v", s, st.done)
	}
	st.rec.Done = now
	if s := status(); s["done"] != true || len(st.done) != 1 {
		t.Fatalf("done = %v", s)
	}
	if out, _ := New(Options{Hub: &fakeHost{}, Store: newMemStore()}).usageResplitStatus(context.Background(), "a", now); out["done"] != true {
		t.Fatalf("without a store = %v", out)
	}
}

// A request's resolutions are kept, and only its buckets inside the range a re-split may read now, with a directory,
// are split.
func TestUsageResplitPut(t *testing.T) {
	now := time.Date(2026, 11, 27, 9, 35, 0, 0, time.UTC)
	st := &usageResplitMemStore{memStore: newMemStore()}
	d := New(Options{Hub: &fakeHost{}, Store: st})
	in := time.Date(2026, 11, 22, 3, 0, 0, 0, time.UTC).Unix()
	params, _ := json.Marshal(map[string]any{"through": in, "done": true,
		"buckets": []proto.UsageBucket{
			{Minute: in, Model: "opus", Agent: "claude", Dir: "~/code/app", Output: 5},
			{Minute: in, Model: "opus", Agent: "claude", Dir: "", Output: 5},                                            // no directory
			{Minute: time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC).Unix(), Model: "opus", Agent: "claude", Dir: "~/x"}, // in the live window
			{Minute: time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC).Unix(), Model: "opus", Agent: "claude", Dir: "~/x"}, // over a week ago
		},
		"dirs": []proto.UsageDir{{Dir: "~/code/app", Project: "git:github.com/acme/app", Name: "acme/app", Host: "github.com"}}})
	out, err := d.putUsageResplit(context.Background(), "a", params, now)
	if err != nil || out["moved"] != 1 || len(st.puts) != 1 || len(st.puts[0]) != 1 || st.puts[0][0].Dir != "~/code/app" ||
		!st.done[0] || st.through[0].Unix() != in || len(st.dirs) != 1 {
		t.Fatalf("out %v, %v; puts %+v", out, err, st.puts)
	}
}
