package node

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// The backfill reads every transcript from since to before with the live meter's rules and sums the replies by the
// hour: a reply in two files once, <synthetic> skipped, nothing before since or in the live window (from before on).
func TestUsageBackfillScan(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	n, home := testNode(t)
	now := time.Now().UTC().Truncate(time.Hour)
	since, before := now.Add(-10*24*time.Hour), now.Add(-26*time.Hour)
	day := now.Add(-3 * 24 * time.Hour)
	project := filepath.Join(home, ".claude", "projects", "-work-app")
	files := map[string]string{
		filepath.Join(project, "s1.jsonl"): claudeReply(since.Add(-time.Hour), "old", "claude-opus-5-5", 1, 1, 1, 1) + // before since
			claudeReply(day.Add(5*time.Minute), "r1", "claude-opus-5-5", 2, 100, 1000, 50) +
			claudeReply(day.Add(5*time.Minute), "r1", "claude-opus-5-5", 2, 100, 1000, 50) + // its next content block
			claudeReply(day.Add(50*time.Minute), "r2", "claude-opus-5-5", 3, 0, 500, 20) +
			claudeReply(day.Add(70*time.Minute), "r3", "<synthetic>", 0, 0, 0, 0) +
			claudeReply(before.Add(time.Minute), "live", "claude-opus-5-5", 9, 9, 9, 9), // the live meter's
		filepath.Join(project, "s2.jsonl"): claudeReply(day.Add(5*time.Minute), "r1", "claude-opus-5-5", 2, 100, 1000, 50) + // a fork's copy
			claudeReply(day.Add(2*time.Hour), "r4", "claude-sonnet-5-5", 4, 0, 40, 7),
	}
	for p, body := range files {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	lim := &readLimiter{rate: 1 << 30, start: time.Now()}
	buckets, err := n.backfillScan(context.Background(), since, before, lim)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 2 {
		t.Fatalf("buckets = %+v", buckets)
	}
	if b := buckets[0]; b.Minute != day.Unix() || b.Model != "claude-opus-5-5" || b.Requests != 2 || b.Output != 70 || b.CacheRead != 1500 || b.Input != 5 {
		t.Fatalf("the first hour = %+v", b)
	}
	if b := buckets[1]; b.Minute != day.Add(2*time.Hour).Unix() || b.Output != 7 {
		t.Fatalf("two hours on = %+v", b)
	}

	// The connection ended: the scan stops.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := n.backfillScan(ctx, since, before, lim); err == nil {
		t.Fatal("a scan after its connection ended")
	}
}

func TestBackfillChunks(t *testing.T) {
	hour := func(h int64, model string) proto.UsageBucket {
		return proto.UsageBucket{Minute: h * 3600, Model: model}
	}
	list := []proto.UsageBucket{hour(1, "a"), hour(1, "b"), hour(1, "c"), hour(2, "a"), hour(3, "a"), hour(3, "b")}
	chunks := backfillChunks(list, 2)
	// Two buckets a request, but hour 1's three stay together.
	if len(chunks) != 2 || len(chunks[0]) != 3 || len(chunks[1]) != 3 || chunks[1][0].Minute != 2*3600 {
		t.Fatalf("chunks = %v", chunks)
	}
	if c := backfillChunks(list, 100); len(c) != 1 || len(c[0]) != 6 {
		t.Fatalf("one request = %v", c)
	}
	if c := backfillChunks(nil, 100); len(c) != 1 || len(c[0]) != 0 {
		t.Fatalf("none = %v", c)
	}
}

// The limiter keeps the reads to its rate, and a connection that ends stops the wait.
func TestBackfillReadLimiter(t *testing.T) {
	l := &readLimiter{rate: 1 << 20, start: time.Now()}
	started := time.Now()
	if err := l.wait(context.Background(), 200<<10); err != nil { // 0.2 MB at 1 MB a second
		t.Fatal(err)
	}
	if took := time.Since(started); took < 150*time.Millisecond {
		t.Fatalf("200 KB took %s at 1 MB/s", took)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.wait(ctx, 10<<20); err == nil {
		t.Fatal("waited past the connection's end")
	}
}

// A week's requests: whole hours each saying the last hour it holds; the last says the week is read through the hour
// before its end (hours without usage too) and, for the last week, that the backfill is done; a week without usage is
// one request with no buckets.
func TestBackfillRequests(t *testing.T) {
	end := time.Unix(100*3600, 0)
	hour := func(h int64) proto.UsageBucket { return proto.UsageBucket{Minute: h * 3600, Model: "m"} }
	list := []proto.UsageBucket{hour(10), hour(10), hour(20), hour(30)}
	got := backfillRequestsSized(list, end, true, 2)
	if len(got) != 2 || got[0]["through"] != int64(10*3600) || got[0]["done"] != false || got[1]["through"] != int64(99*3600) || got[1]["done"] != true {
		t.Fatalf("requests = %v", got)
	}
	if got := backfillRequests(nil, end, false); len(got) != 1 || got[0]["through"] != int64(99*3600) || got[0]["done"] != false {
		t.Fatalf("an empty week = %v", got)
	}
}

// Week by week: each week counts its own replies, a fork's copy within its week once; a file's read stops once
// backfillPast lines in a row are past the week, so the weeks together read about one pass.
func TestUsageBackfillWeeks(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	n, home := testNode(t)
	now := time.Now().UTC().Truncate(time.Hour)
	w1 := now.Add(-21 * 24 * time.Hour)
	w2 := w1.Add(backfillSlice)
	project := filepath.Join(home, ".claude", "projects", "-work-app")
	var long strings.Builder
	long.WriteString(claudeReply(w1.Add(time.Hour), "a1", "claude-opus-5-5", 0, 0, 100, 1))
	for i := range 200 { // the second week: a long tail the first week's read stops in
		long.WriteString(claudeReply(w2.Add(time.Duration(i)*time.Minute), fmt.Sprintf("b%d", i), "claude-opus-5-5", 0, 0, 10, 1))
	}
	files := map[string]string{
		filepath.Join(project, "s1.jsonl"): long.String(),
		filepath.Join(project, "s2.jsonl"): claudeReply(w1.Add(time.Hour), "a1", "claude-opus-5-5", 0, 0, 100, 1) + // a fork's copy
			claudeReply(w2.Add(2*time.Hour), "b0", "claude-opus-5-5", 0, 0, 10, 1), // and of a second-week reply
	}
	for p, body := range files {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	lim := &readLimiter{rate: 1 << 30, start: time.Now()}
	first, err := n.backfillScan(context.Background(), w1, w2, lim)
	if err != nil || len(first) != 1 || first[0].Requests != 1 || first[0].CacheRead != 100 || first[0].Minute != w1.Add(time.Hour).Unix() {
		t.Fatalf("the first week = %+v, %v", first, err)
	}
	second, err := n.backfillScan(context.Background(), w2, w2.Add(backfillSlice), lim)
	var replies int
	for _, b := range second {
		replies += b.Requests
	}
	if err != nil || replies != 200 {
		t.Fatalf("the second week has %d replies (%v), want 200", replies, err)
	}

	// The first week's read of the long file stopped soon after the week's end.
	p := filepath.Join(project, "s1.jsonl")
	st, _ := os.Stat(p)
	m := newUsageMeter()
	m.step, m.until, m.seen = 3600, w2, map[uint64]struct{}{}
	read, err := m.scanSlice(context.Background(), p, st, w1, w2, lim)
	if err != nil || read >= st.Size()/2 || read == 0 {
		t.Fatalf("read %d of %d bytes for the first week (%v)", read, st.Size(), err)
	}
}
