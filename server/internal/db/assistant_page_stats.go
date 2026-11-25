package db

import (
	"context"
	"time"
)

// What the owner waits for after a tap on a page (Dashboards, #15017; docs/design/PRINCIPLES.md principle 7: receipt,
// delivery and execution timed apart). The tap's row times it: at (the tap), delivered_at (in the conversation: the
// queue time) and turn_started_at (the conversation took it up: the turn wait).

// AssistantPageTapDay is one day's taps: how many there were, how many were delivered and redirected to the main
// conversation, how many failed, and the queue time (tap to delivered) and turn wait (delivered to the turn that took
// them up) of those that have one: the median, the 95th percentile and the longest.
type AssistantPageTapDay struct {
	// Day is the day's first moment in the zone asked for.
	Day                          time.Time
	Taps, Delivered, Redirected  int
	Failed, Started              int
	QueueP50, QueueP95, QueueMax time.Duration
	WaitP50, WaitP95, WaitMax    time.Duration
}

// AssistantPageTapDays are the days from since until now, in zone (an IANA name; UTC when empty), with their taps,
// the earliest first. A day without a tap is left out.
func (d *DB) AssistantPageTapDays(ctx context.Context, since time.Time, zone string) ([]AssistantPageTapDay, error) {
	if zone == "" {
		zone = "UTC"
	}
	rows, err := d.pool.Query(ctx, `SELECT day, taps, delivered, redirected, failed, started,
			COALESCE(queue_p50, 0), COALESCE(queue_p95, 0), COALESCE(queue_max, 0),
			COALESCE(wait_p50, 0), COALESCE(wait_p95, 0), COALESCE(wait_max, 0)
		FROM (SELECT date_trunc('day', at AT TIME ZONE $2) AT TIME ZONE $2 AS day, count(*) AS taps,
				count(delivered_at) AS delivered, count(*) FILTER (WHERE redirected AND delivered_at IS NOT NULL) AS redirected,
				count(*) FILTER (WHERE status = 'failed') AS failed, count(turn_started_at) AS started,
				percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM delivered_at - at)) AS queue_p50,
				percentile_cont(0.95) WITHIN GROUP (ORDER BY extract(epoch FROM delivered_at - at)) AS queue_p95,
				max(extract(epoch FROM delivered_at - at)) AS queue_max,
				percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM turn_started_at - delivered_at)) AS wait_p50,
				percentile_cont(0.95) WITHIN GROUP (ORDER BY extract(epoch FROM turn_started_at - delivered_at)) AS wait_p95,
				max(extract(epoch FROM turn_started_at - delivered_at)) AS wait_max
			FROM assistant_page_actions WHERE at >= $1 GROUP BY 1) days
		ORDER BY day`, since.UTC(), zone)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seconds := func(s float64) time.Duration { return time.Duration(s * float64(time.Second)).Round(time.Millisecond) }
	out := []AssistantPageTapDay{}
	for rows.Next() {
		var day AssistantPageTapDay
		var qp50, qp95, qmax, wp50, wp95, wmax float64
		if err := rows.Scan(&day.Day, &day.Taps, &day.Delivered, &day.Redirected, &day.Failed, &day.Started,
			&qp50, &qp95, &qmax, &wp50, &wp95, &wmax); err != nil {
			return nil, err
		}
		day.QueueP50, day.QueueP95, day.QueueMax = seconds(qp50), seconds(qp95), seconds(qmax)
		day.WaitP50, day.WaitP95, day.WaitMax = seconds(wp50), seconds(wp95), seconds(wmax)
		out = append(out, day)
	}
	return out, rows.Err()
}

// AssistantPageTurnTarget is a conversation taps were delivered to whose turn has not ended yet.
type AssistantPageTurnTarget struct {
	Machine      string
	Conversation string
}

// AssistantPageTurnTargets are the conversations taps were delivered to whose turn has not ended: the manager follows
// their turns (MarkAssistantPageTurnStarted, MarkAssistantPageTurnEnded) and no other conversation's.
func (d *DB) AssistantPageTurnTargets(ctx context.Context) ([]AssistantPageTurnTarget, error) {
	rows, err := d.pool.Query(ctx, `SELECT DISTINCT target_machine, target_conversation FROM assistant_page_actions
		WHERE status = 'sent' AND turn_ended_at IS NULL AND target_conversation <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AssistantPageTurnTarget{}
	for rows.Next() {
		var t AssistantPageTurnTarget
		if err := rows.Scan(&t.Machine, &t.Conversation); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
