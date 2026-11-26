package devices

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// Re-splitting the last week by directory (docs/server/machines.md "Token usage by project", #16530): usage stored
// before a node metered by directory is "not split". Once, a node that does re-reads its transcripts from a week ago to
// where its live window starts, by the ten minutes, and the manager splits each stored ten minutes, model, launcher
// and agent in the re-read shares, keeping its sums (db.ResplitUsage): the calendar does not change, the projects fill
// in. What the transcripts no longer hold stays not split. Asked once per connection until done.

// usageResplitStore keeps the re-split (Postgres).
type usageResplitStore interface {
	UsageResplit(ctx context.Context, machine string) (db.UsageResplitRecord, error)
	UsageNotSplit(ctx context.Context, machine string, from, to time.Time) (bool, error)
	ResplitUsage(ctx context.Context, machine string, buckets []proto.UsageBucket, through time.Time, done bool) (int, error)
}

// usageResplitRange is what a re-split may read at now: from a week ago (the ten-minute rows the projects' week is
// summed from) to where a node's whole window starts (usageNodeWindow: that the node sends split already).
func usageResplitRange(now time.Time) (since, before time.Time) {
	step := time.Duration(db.UsageResplitStep) * time.Second
	return now.Add(-7 * 24 * time.Hour).Truncate(step).Add(step), now.Add(-usageNodeWindow).Truncate(step)
}

// usageResplitStatus answers usage.resplit.status: done (nothing to split, or split already), else the range to read,
// from the ten minutes after the last one sent.
func (d *Devices) usageResplitStatus(ctx context.Context, machine string, now time.Time) (map[string]any, error) {
	st, ok := d.opts.Store.(usageResplitStore)
	if !ok {
		return map[string]any{"done": true}, nil
	}
	rec, err := st.UsageResplit(ctx, machine)
	if err != nil || !rec.Done.IsZero() {
		return map[string]any{"done": true}, err
	}
	since, before := usageResplitRange(now)
	if !rec.Through.IsZero() && !rec.Through.Before(since) {
		since = rec.Through.Add(time.Duration(db.UsageResplitStep) * time.Second)
	}
	pending, err := st.UsageNotSplit(ctx, machine, since, before)
	if err != nil {
		return nil, err
	}
	if !pending || !since.Before(before) {
		_, err := st.ResplitUsage(ctx, machine, nil, time.Time{}, true)
		return map[string]any{"done": true}, err
	}
	return map[string]any{"done": false, "since": since.Unix(), "before": before.Unix()}, nil
}

// putUsageResplit takes a usage.resplit request: its directories' resolutions, then its buckets (only those in the
// range a re-split may read now) split into what is stored.
func (d *Devices) putUsageResplit(ctx context.Context, machine string, params json.RawMessage, now time.Time) (map[string]any, error) {
	var p struct {
		Buckets []proto.UsageBucket `json:"buckets"`
		Dirs    []proto.UsageDir    `json:"dirs"`
		Through int64               `json:"through"`
		Done    bool                `json:"done"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	st, ok := d.opts.Store.(usageResplitStore)
	if !ok {
		return map[string]any{"moved": 0}, nil
	}
	if ds, ok := d.opts.Store.(usageDirStore); ok && len(p.Dirs) > 0 {
		if err := ds.PutUsageDirs(ctx, machine, usableDirs(p.Dirs)); err != nil {
			return nil, err
		}
	}
	since, before := usageResplitRange(now)
	keep := p.Buckets[:0]
	for _, b := range p.Buckets {
		if t := time.Unix(b.Minute, 0); !t.Before(since) && t.Before(before) && usableDir(b.Dir) {
			keep = append(keep, b)
		}
	}
	var through time.Time
	if p.Through > 0 {
		through = time.Unix(p.Through, 0)
	}
	moved, err := st.ResplitUsage(ctx, machine, keep, through, p.Done)
	if err != nil {
		return nil, err
	}
	if p.Done {
		d.logf("[%s] usage re-split by directory done", machine)
	}
	return map[string]any{"moved": moved}, nil
}
