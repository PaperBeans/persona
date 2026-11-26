package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ModelRoutingSample is one accepted task round with the candidate it ran on (docs/assistant/model-profiles.md
// "Choosing a candidate"): the tier, launcher and model of the latest dispatch decision of the profile in use for its
// to-do (model_routing, active rows) before acceptance, its wall-clock minutes (task_estimate_rounds.actual_minutes)
// and whether it was accepted with no rework review (todo_supervision.reviews).
type ModelRoutingSample struct {
	Tier          string
	Launcher      string
	Model         string
	ActualMinutes int
	FirstPass     bool
}

// ModelRoutingSamples returns tier's accepted rounds between since and now, newest first, at most limit. Like
// RecentTaskEstimateSamples it keeps only a closure that still stands: the to-do is done by review at the round's
// acceptance and was not reopened after the round was dispatched.
func (d *DB) ModelRoutingSamples(ctx context.Context, tier string, since, now time.Time, limit int) ([]ModelRoutingSample, error) {
	rows, err := d.pool.Query(ctx, `
		SELECT m.tier, m.launcher, m.model, r.actual_minutes,
		       NOT EXISTS (
		           SELECT 1 FROM jsonb_array_elements(
		               CASE WHEN jsonb_typeof(s.reviews) = 'array' THEN s.reviews ELSE '[]'::jsonb END) e
		           WHERE e->>'verdict' = 'rework') AS first_pass
		FROM task_estimate_rounds r
		JOIN todos t ON t.id = r.todo_id
		JOIN LATERAL (
		    SELECT mr.tier, mr.launcher, mr.model FROM model_routing mr
		    WHERE mr.todo_id = r.todo_id AND mr.active AND mr.at <= r.accepted_at
		    ORDER BY mr.at DESC LIMIT 1) m ON true
		LEFT JOIN todo_supervision s ON s.todo_id = r.todo_id
		WHERE m.tier = $1 AND r.accepted_at >= $2 AND r.accepted_at <= $3 AND r.actual_minutes IS NOT NULL
		  AND t.done_at = r.accepted_at AND t.done_by = $5 AND t.start_error = ''
		  AND (t.reopened_at IS NULL OR t.reopened_at <= r.dispatched_at)
		ORDER BY r.accepted_at DESC, r.round_id DESC LIMIT $4`,
		tier, since, now, limit, DoneByReview)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModelRoutingSample
	for rows.Next() {
		var s ModelRoutingSample
		if err := rows.Scan(&s.Tier, &s.Launcher, &s.Model, &s.ActualMinutes, &s.FirstPass); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// LatestModelRouting is todoID's latest dispatch decision of the profile in use (an active row); nil when there is
// none. A continuation or retry keeps the candidate it names (docs/assistant/model-profiles.md "Continuations keep the
// candidate").
func (d *DB) LatestModelRouting(ctx context.Context, todoID string) (*ModelRoutingRow, error) {
	var r ModelRoutingRow
	err := d.pool.QueryRow(ctx, `SELECT id, at, todo_id, plan_id, task_index, session_id, profile_id, active, tier,
		candidate, of, launcher, model, effort, reason, detail FROM model_routing
		WHERE todo_id = $1 AND active ORDER BY at DESC, id DESC LIMIT 1`, todoID).Scan(&r.ID, &r.At, &r.TodoID, &r.PlanID,
		&r.TaskIndex, &r.SessionID, &r.ProfileID, &r.Active, &r.Tier, &r.Candidate, &r.Of, &r.Launcher, &r.Model, &r.Effort,
		&r.Reason, &r.Detail)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}
