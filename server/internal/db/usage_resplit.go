package db

import (
	"cmp"
	"context"
	"errors"
	"math/big"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/tokenuse"
)

// ---- re-splitting the last week's usage by directory (docs/server/machines.md "Token usage by project") ----

// UsageResplitStep is the re-split's step: the ten minutes the last week's rows are kept in.
const UsageResplitStep = UsageRolledStep

// UsageResplitRecord is how far a machine's re-split got (usage_resplit).
type UsageResplitRecord struct {
	Through time.Time // the start of the last ten minutes sent; zero: none yet
	Done    time.Time // when it finished; zero: not yet
	Moved   int       // keys split so far
}

// UsageResplit is machine's re-split so far (zero: never started).
func (d *DB) UsageResplit(ctx context.Context, machine string) (UsageResplitRecord, error) {
	var r UsageResplitRecord
	var through, done *time.Time
	err := d.pool.QueryRow(ctx, `SELECT through, done_at, moved FROM usage_resplit WHERE machine_id = $1`, machine).
		Scan(&through, &done, &r.Moved)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, nil
	}
	if through != nil {
		r.Through = *through
	}
	if done != nil {
		r.Done = *done
	}
	return r, err
}

// UsageNotSplit says whether machine has usage of the file-metered agents from from to to that is not split by
// directory (rows of a minute or ten minutes with no dir): what a re-split could split.
func (d *DB) UsageNotSplit(ctx context.Context, machine string, from, to time.Time) (bool, error) {
	var any bool
	err := d.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM machine_usage WHERE machine_id = $1 AND dir = ''
		AND agent = ANY($2) AND step IN (60, 600) AND ts >= $3 AND ts < $4)`, machine, tokenuse.FileAgents(), from, to).Scan(&any)
	return any, err
}

// resplitKey is a stored row's key without its directory: ten minutes, model, launcher, agent.
type resplitKey struct {
	slot                   int64
	model, launcher, agent string
}

// ResplitUsage splits machine's usage stored without a directory by what the node re-read of the same ten minutes
// (buckets: Minute a ten minutes' start, each with its Dir). For each ten minutes, model, launcher and file-metered
// agent that has both a stored row without a directory and re-read buckets, the stored rows (of a minute or ten
// minutes) are replaced by one row a directory, each of the stored kinds and requests split in the re-read shares
// (largest remainder), so every key's sums, and the calendar, stay exactly as they were. A key with no re-read
// usage stays not split, and re-read usage with nothing stored is left out. through and done move the machine's
// record on; one transaction. moved: the keys split.
func (d *DB) ResplitUsage(ctx context.Context, machine string, buckets []proto.UsageBucket, through time.Time, done bool) (moved int, err error) {
	read := map[resplitKey][]proto.UsageBucket{}
	for _, b := range buckets {
		if b.Dir == "" || !tokenuse.FileMetered(b.Agent) {
			continue
		}
		k := resplitKey{b.Minute / UsageResplitStep * UsageResplitStep, b.Model, b.Launcher, b.Agent}
		read[k] = append(read[k], b)
	}
	err = pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		if len(read) > 0 {
			var from, to int64
			for k := range read {
				if from == 0 || k.slot < from {
					from = k.slot
				}
				to = max(to, k.slot+UsageResplitStep)
			}
			rows, err := tx.Query(ctx, `SELECT extract(epoch FROM date_bin('10 minutes', ts, timestamptz 'epoch'))::bigint,
					model, launcher, agent, sum(input)::bigint, sum(cache_write)::bigint, sum(cache_read)::bigint,
					sum(output)::bigint, sum(requests)::bigint
				FROM machine_usage WHERE machine_id = $1 AND dir = '' AND agent = ANY($2) AND step IN (60, 600)
					AND ts >= to_timestamp($3) AND ts < to_timestamp($4)
				GROUP BY 1, 2, 3, 4`, machine, tokenuse.FileAgents(), from, to)
			if err != nil {
				return err
			}
			stored := map[resplitKey]proto.UsageBucket{}
			for rows.Next() {
				var k resplitKey
				var s proto.UsageBucket
				var req int64
				if err := rows.Scan(&k.slot, &k.model, &k.launcher, &k.agent, &s.Input, &s.CacheWrite, &s.CacheRead, &s.Output, &req); err != nil {
					rows.Close()
					return err
				}
				s.Requests = int(req)
				stored[k] = s
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
			keys := make([]resplitKey, 0, len(stored))
			for k := range stored {
				keys = append(keys, k)
			}
			slices.SortFunc(keys, func(a, b resplitKey) int {
				return cmp.Or(cmp.Compare(a.slot, b.slot), cmp.Compare(a.model, b.model), cmp.Compare(a.launcher, b.launcher), cmp.Compare(a.agent, b.agent))
			})
			batch := &pgx.Batch{}
			for _, k := range keys {
				parts := splitUsage(stored[k], read[k])
				if parts == nil {
					continue
				}
				moved++
				batch.Queue(`DELETE FROM machine_usage WHERE machine_id = $1 AND dir = '' AND step IN (60, 600)
					AND ts >= to_timestamp($2) AND ts < to_timestamp($3) AND model = $4 AND launcher = $5 AND agent = $6`,
					machine, k.slot, k.slot+UsageResplitStep, k.model, k.launcher, k.agent)
				for _, p := range parts {
					batch.Queue(`INSERT INTO machine_usage AS u (machine_id, ts, step, model, launcher, agent, dir, input,
							cache_write, cache_read, output, requests)
						VALUES ($1, to_timestamp($2), $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
						ON CONFLICT (machine_id, ts, step, model, launcher, agent, dir) DO UPDATE SET
							input = u.input + excluded.input, cache_write = u.cache_write + excluded.cache_write,
							cache_read = u.cache_read + excluded.cache_read, output = u.output + excluded.output,
							requests = u.requests + excluded.requests`,
						machine, k.slot, UsageResplitStep, k.model, k.launcher, k.agent, p.Dir, p.Input, p.CacheWrite,
						p.CacheRead, p.Output, p.Requests)
				}
			}
			if batch.Len() > 0 {
				if err := tx.SendBatch(ctx, batch).Close(); err != nil {
					return err
				}
			}
		}
		var th *time.Time
		if !through.IsZero() {
			th = &through
		}
		_, err := tx.Exec(ctx, `INSERT INTO usage_resplit (machine_id, through, done_at, moved)
			VALUES ($1, $2, CASE WHEN $3::boolean THEN now() END, $4)
			ON CONFLICT (machine_id) DO UPDATE SET through = GREATEST(usage_resplit.through, excluded.through),
				done_at = COALESCE(usage_resplit.done_at, excluded.done_at), moved = usage_resplit.moved + excluded.moved,
				updated_at = now()`, machine, th, done, moved)
		return err
	})
	return moved, err
}

// splitUsage is stored split by directory in the shares of the re-read buckets: per kind by that kind's re-read
// shares, or by the re-read tokens when the re-read has none of it; the parts of a kind add up to stored's exactly
// (largest remainder, ties to the earlier directory). nil when the re-read has no tokens at all.
func splitUsage(stored proto.UsageBucket, read []proto.UsageBucket) []proto.UsageBucket {
	byDir := map[string]*proto.UsageBucket{}
	var dirs []string
	for _, b := range read {
		p := byDir[b.Dir]
		if p == nil {
			p = &proto.UsageBucket{Dir: b.Dir}
			byDir[b.Dir] = p
			dirs = append(dirs, b.Dir)
		}
		p.Input += b.Input
		p.CacheWrite += b.CacheWrite
		p.CacheRead += b.CacheRead
		p.Output += b.Output
		p.Requests += b.Requests
	}
	slices.Sort(dirs)
	total := make([]int64, len(dirs))
	var all int64
	for i, dir := range dirs {
		p := byDir[dir]
		total[i] = p.Input + p.CacheWrite + p.CacheRead + p.Output
		all += total[i]
	}
	if all <= 0 {
		return nil
	}
	out := make([]proto.UsageBucket, len(dirs))
	for i, dir := range dirs {
		out[i].Dir = dir
	}
	kinds := []struct {
		of  func(*proto.UsageBucket) int64
		set func(*proto.UsageBucket, int64)
	}{
		{func(b *proto.UsageBucket) int64 { return b.Input }, func(b *proto.UsageBucket, v int64) { b.Input = v }},
		{func(b *proto.UsageBucket) int64 { return b.CacheWrite }, func(b *proto.UsageBucket, v int64) { b.CacheWrite = v }},
		{func(b *proto.UsageBucket) int64 { return b.CacheRead }, func(b *proto.UsageBucket, v int64) { b.CacheRead = v }},
		{func(b *proto.UsageBucket) int64 { return b.Output }, func(b *proto.UsageBucket, v int64) { b.Output = v }},
		{func(b *proto.UsageBucket) int64 { return int64(b.Requests) }, func(b *proto.UsageBucket, v int64) { b.Requests = int(v) }},
	}
	for _, k := range kinds {
		weights := make([]int64, len(dirs))
		var sum int64
		for i, dir := range dirs {
			weights[i] = k.of(byDir[dir])
			sum += weights[i]
		}
		if sum <= 0 {
			copy(weights, total)
		}
		for i, v := range largestRemainder(k.of(&stored), weights) {
			k.set(&out[i], v)
		}
	}
	return out
}

// largestRemainder splits whole in proportion to weights (not all 0) into whole numbers that add up to it: each its
// floor, then one more to the largest remainders, the earlier first on a tie.
func largestRemainder(whole int64, weights []int64) []int64 {
	out := make([]int64, len(weights))
	var sum int64
	for _, w := range weights {
		sum += w
	}
	if whole <= 0 || sum <= 0 {
		return out
	}
	W, S := big.NewInt(whole), big.NewInt(sum)
	rems := make([]*big.Int, len(weights))
	var given int64
	for i, w := range weights {
		q, r := new(big.Int).QuoRem(new(big.Int).Mul(W, big.NewInt(w)), S, new(big.Int))
		out[i], rems[i] = q.Int64(), r
		given += out[i]
	}
	order := make([]int, len(weights))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return rems[b].Cmp(rems[a]) })
	for i := 0; given < whole; i++ {
		out[order[i%len(order)]]++
		given++
	}
	return out
}
