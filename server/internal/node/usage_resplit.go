package node

import (
	"context"
	"slices"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/tokenuse"
)

// Re-splitting the last week by directory (docs/server/machines.md "Token usage by project", #16530): the usage the
// manager stored before this node metered by directory is not split. Once, with a manager that keeps directories, the
// node re-reads its transcripts from a week ago to where its live window starts, by the ten minutes, with the live
// meter's rules (a reply counted once, the same launcher and agent) and the directory each session started in, and
// sends them with what the directories resolve to; the manager splits each stored ten minutes in those shares, its
// sums unchanged (devices/usage_resplit.go). Gentle like the backfill: after a delay, rate-limited, gone with the
// connection, and a cut one goes on from the last ten minutes the manager recorded.

// resplitDelay: after the connection, and after the meter has asked whether the manager keeps directories.
var resplitDelay = 4 * time.Minute

// resplitUsage asks the manager whether this machine's last week wants re-splitting and does it.
func (n *Node) resplitUsage(ctx context.Context, stateLoaded <-chan struct{}) {
	select {
	case <-ctx.Done():
		return
	case <-stateLoaded:
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(resplitDelay):
	}
	if n.peer == nil || n.usageDirs.get() == nil { // the manager keeps no directories (or the meter is not up)
		return
	}
	var st backfillRange
	cctx, cancel := context.WithTimeout(ctx, backfillCall)
	err := n.peer.Call(cctx, "usage.resplit.status", map[string]any{}, &st)
	cancel()
	if err != nil || st.Done || st.Since >= st.Before { // an older manager does not know it
		return
	}
	since, before := time.Unix(st.Since, 0), time.Unix(st.Before, 0)
	started := time.Now()
	lim := &readLimiter{rate: backfillRate, start: started}
	files := slices.DeleteFunc(n.backfillFiles(ctx, since, lim), func(f backfillFile) bool { return !tokenuse.FileMetered(f.ad.agent()) })
	queries := slices.DeleteFunc(n.usageQueries(), func(q usageQuery) bool { return !tokenuse.FileMetered(q.agent()) })
	buckets, err := n.usageScan(ctx, files, queries, since, before, lim, int64(resplitStep/time.Second))
	if err != nil {
		return
	}
	resolved := map[string]*proto.UsageDir{}
	moved := 0
	for _, req := range resplitRequests(buckets, before) {
		if dirs := n.backfillDirs(ctx, req["buckets"].([]proto.UsageBucket), resolved); len(dirs) > 0 {
			req["dirs"] = dirs
		}
		var res struct {
			Moved int `json:"moved"`
		}
		cctx, cancel := context.WithTimeout(ctx, backfillCall)
		err := n.peer.Call(cctx, "usage.resplit", req, &res)
		cancel()
		if err != nil { // the next connection asks again, from the ten minutes after the last recorded
			n.logger.Printf("usage re-split: %v (%d split)", err, moved)
			return
		}
		moved += res.Moved
	}
	n.logger.Printf("usage re-split: %d buckets read in %s, %d split", len(buckets), time.Since(started).Round(time.Second), moved)
}

// resplitStep: the ten minutes the manager keeps the last week in.
const resplitStep = 10 * time.Minute

// resplitRequests are the re-split's usage.resplit requests: its buckets with a directory (in time order) in whole ten
// minutes, each saying the last ten minutes it holds, the last the ten minutes before end, and done.
func resplitRequests(buckets []proto.UsageBucket, end time.Time) []map[string]any {
	keep := slices.DeleteFunc(slices.Clone(buckets), func(b proto.UsageBucket) bool { return b.Dir == "" })
	out := backfillRequestsSized(keep, end, true, backfillChunk)
	step := int64(resplitStep / time.Second)
	for _, req := range out {
		if !req["done"].(bool) {
			continue
		}
		req["through"] = end.Unix() - step
	}
	return out
}
