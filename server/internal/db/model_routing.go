package db

import (
	"context"
	"encoding/json"
	"time"
)

// ModelRoutingRow is one decision of which candidate of a model profile's tier a dispatch runs on (model_routing).
type ModelRoutingRow struct {
	ID        int64
	At        time.Time
	TodoID    string
	PlanID    string
	TaskIndex int
	SessionID string
	ProfileID string
	Active    bool // the profile in use: a dispatch that ran; false: a trial of a profile not in use
	Tier      string
	Candidate int
	Of        int
	Launcher  string
	Model     string
	Effort    string
	Reason    string
	Detail    json.RawMessage
}

// PutModelRouting records one routing decision.
func (d *DB) PutModelRouting(ctx context.Context, r ModelRoutingRow) error {
	detail := r.Detail
	if len(detail) == 0 {
		detail = json.RawMessage(`{}`)
	}
	_, err := d.pool.Exec(ctx, `INSERT INTO model_routing (at, todo_id, plan_id, task_index, session_id, profile_id, active,
		tier, candidate, of, launcher, model, effort, reason, detail) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		r.At, r.TodoID, r.PlanID, r.TaskIndex, r.SessionID, r.ProfileID, r.Active, r.Tier, r.Candidate, r.Of, r.Launcher,
		r.Model, r.Effort, r.Reason, detail)
	return err
}

// ModelRoutings are the decisions recorded since since for profile ("" every profile), oldest first.
func (d *DB) ModelRoutings(ctx context.Context, profile string, since time.Time) ([]ModelRoutingRow, error) {
	rows, err := d.pool.Query(ctx, `SELECT id, at, todo_id, plan_id, task_index, session_id, profile_id, active, tier,
		candidate, of, launcher, model, effort, reason, detail FROM model_routing
		WHERE at >= $1 AND ($2 = '' OR profile_id = $2) ORDER BY at, id`, since, profile)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModelRoutingRow
	for rows.Next() {
		var r ModelRoutingRow
		if err := rows.Scan(&r.ID, &r.At, &r.TodoID, &r.PlanID, &r.TaskIndex, &r.SessionID, &r.ProfileID, &r.Active, &r.Tier,
			&r.Candidate, &r.Of, &r.Launcher, &r.Model, &r.Effort, &r.Reason, &r.Detail); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
