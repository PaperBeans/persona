package db

import (
	"context"
	"time"
)

// ConfirmationCount groups cards by their originating Agent, task and kind. A
// card counts when dispatched to the user, rather than when created or shown to a supervisor.
type ConfirmationCount struct {
	MachineID      string `json:"machine_id"`
	SessionID      string `json:"session_id"`
	TodoID         string `json:"todo_id,omitempty"`
	Kind           string `json:"kind"`
	SessionTitle   string `json:"session_title,omitempty"`
	UserNotified   int    `json:"user_notified"`
	AutoAuthorized int    `json:"auto_authorized"`
}

// MarkConfirmationNotified keeps the first dispatch time, including across retries and restarts.
func (d *DB) MarkConfirmationNotified(ctx context.Context, id string, created, at time.Time, todo string) error {
	_, err := d.pool.Exec(ctx, `UPDATE interactions SET user_notified_at = COALESCE(user_notified_at, $3),
		todo_id = CASE WHEN todo_id = '' THEN $4 ELSE todo_id END
		WHERE id = $1 AND created_at = $2`, id, created, at, todo)
	return err
}

// MarkConfirmationAutoAuthorized records successful automatic resolutions only; a
// failed attempt has no automatic outcome to count.
func (d *DB) MarkConfirmationAutoAuthorized(ctx context.Context, id string, created, at time.Time, todo, grant string) error {
	_, err := d.pool.Exec(ctx, `UPDATE interactions SET auto_authorized_at = COALESCE(auto_authorized_at, $3),
		todo_id = CASE WHEN todo_id = '' THEN $4 ELSE todo_id END,
		authorization_id = CASE WHEN authorization_id = '' THEN $5 ELSE authorization_id END
		WHERE id = $1 AND created_at = $2`, id, created, at, todo, grant)
	return err
}

// ConfirmationCounts counts events in [since, until]. Creation time deliberately
// does not filter the rows: yesterday's held card may reach the user today.
func (d *DB) ConfirmationCounts(ctx context.Context, since, until time.Time) ([]ConfirmationCount, error) {
	rows, err := d.pool.Query(ctx, `SELECT machine_id, session_id, todo_id, kind,
		max(COALESCE(payload->>'session_title', '')),
		count(*) FILTER (WHERE user_notified_at >= $1 AND user_notified_at <= $2),
		count(*) FILTER (WHERE auto_authorized_at >= $1 AND auto_authorized_at <= $2)
		FROM interactions
		WHERE (user_notified_at >= $1 AND user_notified_at <= $2)
		OR (auto_authorized_at >= $1 AND auto_authorized_at <= $2)
		GROUP BY machine_id, session_id, todo_id, kind
		ORDER BY machine_id, session_id, todo_id, kind`, since, until)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ConfirmationCount{}
	for rows.Next() {
		var c ConfirmationCount
		if err := rows.Scan(&c.MachineID, &c.SessionID, &c.TodoID, &c.Kind, &c.SessionTitle, &c.UserNotified, &c.AutoAuthorized); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
