package devices

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// Token usage backfill (docs/server/machines.md "Backfill", #9484): once a machine, its node reads the transcripts
// from before the history began and sends the hours (node/usage_backfill.go). The manager says where to read
// (usage.backfill.status), stores the hours that have no usage yet and records how far it got (usage.backfill).

// usageBackfillStore keeps the backfill (db.UsageBackfill, db.PutUsageBackfill).
type usageBackfillStore interface {
	UsageBackfill(ctx context.Context, machine string) (db.UsageBackfill, error)
	PutUsageBackfill(ctx context.Context, machine string, buckets []proto.UsageBucket, through time.Time, done bool) (int, error)
}

// usageBackfillStatus answers a node's usage.backfill.status: done, or the hours to read, from where the history
// begins (or the hour after the last one stored) to the start of the node's live window.
func (d *Devices) usageBackfillStatus(ctx context.Context, machine string, now time.Time) (map[string]any, error) {
	st, ok := d.opts.Store.(usageBackfillStore)
	if !ok { // nowhere to keep the history
		return map[string]any{"done": true}, nil
	}
	b, err := st.UsageBackfill(ctx, machine)
	if err != nil {
		return nil, err
	}
	since, before := usageBackfillRange(b, now)
	if !b.Done.IsZero() || !since.Before(before) {
		return map[string]any{"done": true}, nil
	}
	return map[string]any{"done": false, "since": since.Unix(), "before": before.Unix()}, nil
}

// usageBackfillRange is what a backfill reads: from the history's first hour (or the one after the last stored) to
// the hour the node's live window starts in.
func usageBackfillRange(b db.UsageBackfill, now time.Time) (since, before time.Time) {
	since = now.Add(-usageHistoryKeep).Truncate(time.Hour).Add(time.Hour)
	if next := b.Through.Add(time.Hour); !b.Through.IsZero() && next.After(since) {
		since = next
	}
	return since, now.Add(-usageNodeWindow).Truncate(time.Hour)
}

// putUsageBackfill stores a node's usage.backfill request: its hours within the range (others are dropped), how far
// it got, and whether it is done.
func (d *Devices) putUsageBackfill(ctx context.Context, machine string, params json.RawMessage, now time.Time) (map[string]any, error) {
	var p struct {
		Buckets []proto.UsageBucket `json:"buckets"`
		Through int64               `json:"through"`
		Done    bool                `json:"done"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	st, ok := d.opts.Store.(usageBackfillStore)
	if !ok {
		return map[string]any{"stored": 0}, nil
	}
	since, before := usageBackfillRange(db.UsageBackfill{}, now)
	keep := p.Buckets[:0]
	for _, b := range p.Buckets {
		if t := time.Unix(b.Minute, 0); !t.Before(since) && t.Before(before) {
			keep = append(keep, b)
		}
	}
	var through time.Time
	if p.Through > 0 {
		through = time.Unix(p.Through, 0)
	}
	stored, err := st.PutUsageBackfill(ctx, machine, keep, through, p.Done)
	if err != nil {
		return nil, err
	}
	if p.Done {
		d.logf("[%s] usage backfill done", machine)
	}
	return map[string]any{"stored": stored}, nil
}
