package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// HealthTell is a sentence received for Health, independently of quick captures.
type HealthTell struct {
	ID, ClientID                              string
	CreatedAt                                 time.Time
	Source, Text, Status                      string
	Attempts                                  int
	Error, NewSessionID, MachineID, SessionID string
	UpdatedAt                                 time.Time
}

const healthTellColumns = `id, client_id, created_at, source, text, status, attempts, error, new_session_id, machine_id, session_id, updated_at`

func scanHealthTell(row pgx.Row) (HealthTell, error) {
	var r HealthTell
	err := row.Scan(&r.ID, &r.ClientID, &r.CreatedAt, &r.Source, &r.Text, &r.Status, &r.Attempts, &r.Error, &r.NewSessionID, &r.MachineID, &r.SessionID, &r.UpdatedAt)
	return r, err
}

// InsertHealthTell keeps the first sentence for a client id, even when retry text differs.
func (d *DB) InsertHealthTell(ctx context.Context, r HealthTell) (HealthTell, bool, error) {
	got, err := scanHealthTell(d.pool.QueryRow(ctx, `INSERT INTO health_tells (id, client_id, text)
		VALUES ($1, $2, $3) ON CONFLICT (client_id) DO NOTHING RETURNING `+healthTellColumns, r.ID, r.ClientID, r.Text))
	if err == nil {
		return got, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return HealthTell{}, false, err
	}
	got, err = scanHealthTell(d.pool.QueryRow(ctx, `SELECT `+healthTellColumns+` FROM health_tells WHERE client_id = $1`, r.ClientID))
	return got, false, err
}

// HealthTellsPending returns received sentences in delivery order.
func (d *DB) HealthTellsPending(ctx context.Context, limit int) ([]HealthTell, error) {
	rows, err := d.pool.Query(ctx, `SELECT `+healthTellColumns+` FROM health_tells WHERE status = 'received' ORDER BY created_at, id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HealthTell
	for rows.Next() {
		r, err := scanHealthTell(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReserveHealthTellTarget pins a validated continuation before dispatch, including across manager restarts.
func (d *DB) ReserveHealthTellTarget(ctx context.Context, id, machine, session string) (HealthTell, error) {
	_, err := d.pool.Exec(ctx, `UPDATE health_tells SET machine_id = $2, session_id = $3
		WHERE id = $1 AND machine_id = '' AND session_id = '' AND new_session_id = '' AND status = 'received'`, id, machine, session)
	if err != nil {
		return HealthTell{}, err
	}
	return scanHealthTell(d.pool.QueryRow(ctx, `SELECT `+healthTellColumns+` FROM health_tells WHERE id = $1`, id))
}

// ReserveHealthTellSession retains the first reserved id across restarts. A definite continuation refusal may
// switch to a new start; reserving it atomically clears the old target so a restart cannot return to that path.
func (d *DB) ReserveHealthTellSession(ctx context.Context, id, sid string) (string, error) {
	_, err := d.pool.Exec(ctx, `UPDATE health_tells SET new_session_id = $2, machine_id = '', session_id = '', updated_at = now()
		WHERE id = $1 AND new_session_id = '' AND status = 'received'`, id, sid)
	if err != nil {
		return "", err
	}
	var got string
	err = d.pool.QueryRow(ctx, `SELECT new_session_id FROM health_tells WHERE id = $1`, id).Scan(&got)
	return got, err
}

// SetHealthTellResult changes only pending rows; failure retains the original text and pinned destination for
// recovery. Only confirmed delivery replaces the target with the acknowledged conversation.
func (d *DB) SetHealthTellResult(ctx context.Context, id, status, machine, session, errText string, tried bool) (bool, error) {
	tag, err := d.pool.Exec(ctx, `UPDATE health_tells SET status = $2,
		machine_id = CASE WHEN $2 = 'delivered' THEN $3 ELSE machine_id END,
		session_id = CASE WHEN $2 = 'delivered' THEN $4 ELSE session_id END,
		error = $5, attempts = attempts + CASE WHEN $6 THEN 1 ELSE 0 END,
		text = CASE WHEN $2 = 'delivered' THEN '' ELSE text END, updated_at = now()
		WHERE id = $1 AND status = 'received'`, id, status, machine, session, errText, tried)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}
