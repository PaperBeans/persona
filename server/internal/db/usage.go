package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nwverse/persona/server/internal/proto"
)

// ---- token usage by machine (docs/server/machines.md "Token usage") ----

// UsageMinuteStep is the step of a row as a node reported it; UsageRolledStep of ten minutes rolled up; UsageHourStep
// of an hour, rolled up from the ten-minute rows past a week and kept for the calendar's year.
const (
	UsageMinuteStep = 60
	UsageRolledStep = 600
	UsageHourStep   = 3600
)

const putUsageSQL = `INSERT INTO machine_usage (machine_id, ts, step, model, launcher, input, cache_write, cache_read,
		output, requests)
	VALUES ($1, to_timestamp($2), 60, $3, $4, $5, $6, $7, $8, $9)
	ON CONFLICT (machine_id, ts, step, model, launcher) DO UPDATE SET input = $5, cache_write = $6, cache_read = $7,
		output = $8, requests = $9`

// UsageWrite is one node's "usage" notification to store (PutUsage's arguments).
type UsageWrite struct {
	Machine     string
	Buckets     []proto.UsageBucket
	ReplaceFrom time.Time
}

func queueUsage(b *pgx.Batch, w UsageWrite) {
	if !w.ReplaceFrom.IsZero() {
		b.Queue(`DELETE FROM machine_usage WHERE machine_id = $1 AND step = 60 AND ts >= to_timestamp($2)`,
			w.Machine, w.ReplaceFrom.Unix())
	}
	for _, x := range w.Buckets {
		b.Queue(putUsageSQL, w.Machine, x.Minute, x.Model, x.Launcher, x.Input, x.CacheWrite, x.CacheRead, x.Output, x.Requests)
	}
}

// PutUsage stores a node's buckets: a bucket is its minute's whole total, so it replaces what was there. With
// replaceFrom set (the node's whole window, "full"), the machine's minute rows from then on that it did not send
// are deleted first: what the node counts now is the record (an older node's rows without a launcher, a reply it no
// longer counts twice).
func (d *DB) PutUsage(ctx context.Context, machine string, buckets []proto.UsageBucket, replaceFrom time.Time) error {
	_, err := d.PutUsages(ctx, []UsageWrite{{Machine: machine, Buckets: buckets, ReplaceFrom: replaceFrom}})
	return err
}

// PutUsages stores several writes as PutUsage does, in order, in one transaction: one commit, so one WAL flush,
// instead of one a write. A write Postgres rejects costs only itself: rejected[i] is the error writes[i] got
// (writeAll).
func (d *DB) PutUsages(ctx context.Context, writes []UsageWrite) (rejected []error, err error) {
	return d.writeAll(ctx, len(writes), func(b *pgx.Batch, i int) { queueUsage(b, writes[i]) })
}

// RollupUsage sums the minute rows before before (rounded down to ten minutes) into one row a machine, ten minutes,
// model and launcher. Moving and summing is one statement, so a minute is rolled up once; a ten-minute row already
// there (a node that sent an old minute again) is added to.
func (d *DB) RollupUsage(ctx context.Context, before time.Time) (int64, error) {
	return d.rollupUsage(ctx, UsageMinuteStep, UsageRolledStep, before)
}

// RollupUsageHours sums the ten-minute rows before before (rounded down to the hour) into one row a machine, hour,
// model and launcher, as RollupUsage does the minutes.
func (d *DB) RollupUsageHours(ctx context.Context, before time.Time) (int64, error) {
	return d.rollupUsage(ctx, UsageRolledStep, UsageHourStep, before)
}

// rollupUsage moves the rows of step from before before (rounded down to step to) into rows of step to.
func (d *DB) rollupUsage(ctx context.Context, from, to int, before time.Time) (int64, error) {
	tag, err := d.pool.Exec(ctx, `WITH moved AS (
			DELETE FROM machine_usage WHERE step = $2 AND ts < to_timestamp($1) RETURNING *
		)
		INSERT INTO machine_usage AS u (machine_id, ts, step, model, launcher, input, cache_write, cache_read, output, requests)
		SELECT machine_id, date_bin(make_interval(secs => $3), ts, timestamptz 'epoch'), $3, model, launcher, sum(input),
			sum(cache_write), sum(cache_read), sum(output), sum(requests)
		FROM moved GROUP BY 1, 2, 4, 5
		ON CONFLICT (machine_id, ts, step, model, launcher) DO UPDATE SET input = u.input + excluded.input,
			cache_write = u.cache_write + excluded.cache_write, cache_read = u.cache_read + excluded.cache_read,
			output = u.output + excluded.output, requests = u.requests + excluded.requests`,
		before.Truncate(time.Duration(to)*time.Second).Unix(), from, to)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// PruneUsage deletes every row older than before.
func (d *DB) PruneUsage(ctx context.Context, before time.Time) (int64, error) {
	tag, err := d.pool.Exec(ctx, `DELETE FROM machine_usage WHERE ts < to_timestamp($1)`, before.Unix())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// UsageRow is one model and launcher's usage over a step (Minute: the step's start).
type UsageRow = proto.UsageBucket

// UsageSeries sums a machine's rows from from (inclusive) to to over steps of step seconds (aligned to the Unix
// epoch; a multiple of ten minutes, or of a minute under 27 hours), one row a step, model and launcher, in time order.
func (d *DB) UsageSeries(ctx context.Context, machine string, from, to time.Time, step int) ([]UsageRow, error) {
	rows, err := d.pool.Query(ctx, `SELECT extract(epoch FROM date_bin(make_interval(secs => $4), ts, timestamptz 'epoch'))::bigint,
			model, launcher, sum(input)::bigint, sum(cache_write)::bigint, sum(cache_read)::bigint, sum(output)::bigint, sum(requests)::bigint
		FROM machine_usage WHERE machine_id = $1 AND ts >= to_timestamp($2) AND ts < to_timestamp($3)
		GROUP BY 1, 2, 3 ORDER BY 1, 2, 3`, machine, from.Unix(), to.Unix(), step)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UsageRow
	for rows.Next() {
		var r UsageRow
		if err := rows.Scan(&r.Minute, &r.Model, &r.Launcher, &r.Input, &r.CacheWrite, &r.CacheRead, &r.Output, &r.Requests); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UsageSpan is the tokens of one span of UsageSpans' bounds: every machine's, or one machine's.
type UsageSpan struct {
	Span       int    // bounds[Span] to bounds[Span+1]
	Machine    string // "" when summed over the machines
	Input      int64  // not from the cache
	CacheWrite int64
	CacheRead  int64
	Output     int64
	Requests   int
}

// UsageSpans sums every machine's rows into the spans between bounds (in time order: a span is [bounds[i],
// bounds[i+1])), and with byMachine each machine's apart; only the spans with rows, in order. A row counts in the
// span its start is in: the days and hours of a zone the clock is not a whole hour from UTC in split the hour rows
// at the hour (docs/server/machines.md "Token usage").
func (d *DB) UsageSpans(ctx context.Context, bounds []time.Time, byMachine bool) ([]UsageSpan, error) {
	if len(bounds) < 2 {
		return nil, nil
	}
	rows, err := d.pool.Query(ctx, `SELECT width_bucket(ts, $1::timestamptz[]) - 1, CASE WHEN $2::boolean THEN machine_id ELSE '' END,
			sum(input)::bigint, sum(cache_write)::bigint, sum(cache_read)::bigint, sum(output)::bigint, sum(requests)::bigint
		FROM machine_usage WHERE ts >= $3 AND ts < $4
		GROUP BY 1, 2 ORDER BY 1, 2`, bounds, byMachine, bounds[0], bounds[len(bounds)-1])
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UsageSpan
	for rows.Next() {
		var s UsageSpan
		var req int64
		if err := rows.Scan(&s.Span, &s.Machine, &s.Input, &s.CacheWrite, &s.CacheRead, &s.Output, &req); err != nil {
			return nil, err
		}
		s.Requests = int(req)
		out = append(out, s)
	}
	return out, rows.Err()
}

// UsageBackfill is how far a machine's backfill got (usage_backfill, docs/server/machines.md "Backfill").
type UsageBackfill struct {
	Through time.Time // the start of the last hour sent; zero: none yet
	Done    time.Time // when the scan finished; zero: not yet
	Hours   int       // hours stored
}

// UsageBackfill is machine's backfill so far (zero: never started).
func (d *DB) UsageBackfill(ctx context.Context, machine string) (UsageBackfill, error) {
	var b UsageBackfill
	var through, done *time.Time
	err := d.pool.QueryRow(ctx, `SELECT through, done_at, hours FROM usage_backfill WHERE machine_id = $1`, machine).
		Scan(&through, &done, &b.Hours)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, nil
	}
	if through != nil {
		b.Through = *through
	}
	if done != nil {
		b.Done = *done
	}
	return b, err
}

// PutUsageBackfill stores one request of machine's backfill: buckets (Minute an hour's start, any order) go in as
// hour rows, but only for the hours that had no row of the machine (of any step) before it, so the live meter's
// count stays and a backfill sent again adds nothing; through (the last hour sent) and done move its record on. One
// transaction; stored is the hours added.
func (d *DB) PutUsageBackfill(ctx context.Context, machine string, buckets []proto.UsageBucket, through time.Time, done bool) (stored int, err error) {
	n := len(buckets)
	hours, models, launchers := make([]int64, n), make([]string, n), make([]string, n)
	in, cw, cr, out, req := make([]int64, n), make([]int64, n), make([]int64, n), make([]int64, n), make([]int64, n)
	for i, b := range buckets {
		hours[i], models[i], launchers[i] = b.Minute, b.Model, b.Launcher
		in[i], cw[i], cr[i], out[i], req[i] = b.Input, b.CacheWrite, b.CacheRead, b.Output, int64(b.Requests)
	}
	err = pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		if n > 0 {
			// had: the machine's hours with a row, as the statement starts (its own rows are not in it).
			if err := tx.QueryRow(ctx, `WITH b AS (
					SELECT date_bin('1 hour', to_timestamp(hour), timestamptz 'epoch') AS ts, model, launcher, input,
						cache_write, cache_read, output, requests
					FROM unnest($2::bigint[], $3::text[], $4::text[], $5::bigint[], $6::bigint[], $7::bigint[], $8::bigint[], $9::bigint[])
						AS u(hour, model, launcher, input, cache_write, cache_read, output, requests)
				), had AS (
					SELECT DISTINCT date_bin('1 hour', ts, timestamptz 'epoch') AS ts FROM machine_usage
					WHERE machine_id = $1 AND ts >= (SELECT min(ts) FROM b) AND ts < (SELECT max(ts) FROM b) + interval '1 hour'
				), added AS (
					INSERT INTO machine_usage (machine_id, ts, step, model, launcher, input, cache_write, cache_read, output, requests)
					SELECT $1, ts, 3600, model, launcher, sum(input), sum(cache_write), sum(cache_read), sum(output), sum(requests)
					FROM b WHERE ts NOT IN (SELECT ts FROM had) GROUP BY ts, model, launcher
					ON CONFLICT (machine_id, ts, step, model, launcher) DO NOTHING
					RETURNING ts
				)
				SELECT count(DISTINCT ts) FROM added`,
				machine, hours, models, launchers, in, cw, cr, out, req).Scan(&stored); err != nil {
				return err
			}
		}
		var th *time.Time
		if !through.IsZero() {
			th = &through
		}
		_, err := tx.Exec(ctx, `INSERT INTO usage_backfill (machine_id, through, done_at, hours)
			VALUES ($1, $2, CASE WHEN $3::boolean THEN now() END, $4)
			ON CONFLICT (machine_id) DO UPDATE SET through = GREATEST(usage_backfill.through, excluded.through),
				done_at = COALESCE(usage_backfill.done_at, excluded.done_at), hours = usage_backfill.hours + excluded.hours,
				updated_at = now()`, machine, th, done, stored)
		return err
	})
	return stored, err
}
