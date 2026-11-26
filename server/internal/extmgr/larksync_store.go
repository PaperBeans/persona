package extmgr

import (
	"context"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/larksyncwire"
)

// larkSyncStore keeps what the sync's runs read and hand over (db lark_sync_*): the cursors, the dedup index and the
// outbox. A store without it (no database) leaves the sync unavailable.
type larkSyncStore interface {
	LarkSyncCursors(ctx context.Context, binding string) (map[string]string, error)
	DeleteLarkSyncCursors(ctx context.Context, binding string) error
	LarkSyncIndex(ctx context.Context, keys []string) (map[string]larksyncwire.IndexRecord, error)
	CommitLarkSyncRun(ctx context.Context, binding string, cursors map[string]string, recs []larksyncwire.IndexRecord, outbox []db.LarkSyncOutbox) error
	LarkSyncOutboxAfter(ctx context.Context, kind string, after int64, limit int) ([]db.LarkSyncOutbox, error)
	LarkSyncDocsSince(ctx context.Context, since time.Time) ([]db.LarkSyncOutbox, error)
	PruneLarkSyncOutbox(ctx context.Context, before time.Time) (int64, error)
}

var _ larkSyncStore = (*db.DB)(nil)

// larkSyncStaging is the index a run reads through: lookups see the store and what the run staged, and nothing is
// written until the manager commits the run (CommitLarkSyncRun), together with its cursors and its outbox rows. So a
// run whose hand-over fails leaves the index as it was, and the next run reads the same content again.
type larkSyncStaging struct {
	store  larkSyncStore
	mu     sync.Mutex
	staged map[string]larksyncwire.IndexRecord
	order  []string
}

// Lookup is the records of keys: the staged ones first, then the store's.
func (x *larkSyncStaging) Lookup(ctx context.Context, keys []string) (map[string]larksyncwire.IndexRecord, error) {
	out := map[string]larksyncwire.IndexRecord{}
	var ask []string
	x.mu.Lock()
	for _, k := range keys {
		if r, ok := x.staged[k]; ok {
			out[k] = r
		} else {
			ask = append(ask, k)
		}
	}
	x.mu.Unlock()
	if len(ask) == 0 {
		return out, nil
	}
	got, err := x.store.LarkSyncIndex(ctx, ask)
	if err != nil {
		return nil, err
	}
	for k, r := range got {
		out[k] = r
	}
	return out, nil
}

// Put stages recs like Stage: a reader that does not know Stage still writes nothing before the commit.
func (x *larkSyncStaging) Put(_ context.Context, recs []larksyncwire.IndexRecord) error {
	x.Stage(recs)
	return nil
}

// Stage keeps recs for the commit; a key staged twice keeps the later record (its sources merged).
func (x *larkSyncStaging) Stage(recs []larksyncwire.IndexRecord) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.staged == nil {
		x.staged = map[string]larksyncwire.IndexRecord{}
	}
	for _, r := range recs {
		if r.Key == "" {
			continue
		}
		if have, ok := x.staged[r.Key]; ok {
			r.Sources = larksyncwire.MergeSources(have.Sources, r.Sources)
		} else {
			x.order = append(x.order, r.Key)
		}
		x.staged[r.Key] = r
	}
}

// records are the staged records in the order they were first staged.
func (x *larkSyncStaging) records() []larksyncwire.IndexRecord {
	x.mu.Lock()
	defer x.mu.Unlock()
	out := make([]larksyncwire.IndexRecord, 0, len(x.order))
	for _, k := range x.order {
		out = append(out, x.staged[k])
	}
	return out
}
