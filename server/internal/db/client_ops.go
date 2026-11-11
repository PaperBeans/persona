package db

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type ClientOp struct {
	ID        string          `json:"id"`
	DeviceID  string          `json:"device_id"`
	Kind      string          `json:"kind"`
	Target    json.RawMessage `json:"target"`
	Digest    string          `json:"digest"`
	State     string          `json:"state"`
	Result    json.RawMessage `json:"result"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// BeginClientOp atomically claims an ID. Only the successful inserter may execute it.
func (d *DB) BeginClientOp(ctx context.Context, op ClientOp) (*ClientOp, bool, error) {
	tag, err := d.pool.Exec(ctx, `INSERT INTO client_ops (id, device_id, kind, target, digest)
		VALUES ($1, $2, $3, $4, $5) ON CONFLICT (id) DO NOTHING`, op.ID, op.DeviceID, op.Kind, op.Target, op.Digest)
	if err != nil {
		return nil, false, err
	}
	stored, err := d.ClientOp(ctx, op.ID)
	return stored, tag.RowsAffected() == 1, err
}

func (d *DB) ClientOp(ctx context.Context, id string) (*ClientOp, error) {
	var op ClientOp
	err := d.pool.QueryRow(ctx, `SELECT id::text, device_id, kind, target, digest, state, result, created_at, updated_at
		FROM client_ops WHERE id = $1`, id).Scan(&op.ID, &op.DeviceID, &op.Kind, &op.Target, &op.Digest,
		&op.State, &op.Result, &op.CreatedAt, &op.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &op, nil
}

func (d *DB) FinishClientOp(ctx context.Context, id, state string, result json.RawMessage) error {
	_, err := d.pool.Exec(ctx, `UPDATE client_ops SET state = $2, result = $3, updated_at = now()
		WHERE id = $1 AND state = 'pending'`, id, state, result)
	return err
}

// ForgetClientOp drops an operation claimed and not finished, which did nothing: its retry runs it again.
func (d *DB) ForgetClientOp(ctx context.Context, id string) error {
	_, err := d.pool.Exec(ctx, `DELETE FROM client_ops WHERE id = $1 AND state = 'pending'`, id)
	return err
}

// The startup statement of RecoverClientOps. It must stay answerable from the client_ops_pending partial index
// (migration 0417); TestPendingClientOpsIndexed checks the plan of this exact constant.
const recoverClientOpsSQL = `UPDATE client_ops SET state = 'unknown', updated_at = now() WHERE state = 'pending'`

// RecoverClientOps never retries operations whose executor disappeared during a restart.
func (d *DB) RecoverClientOps(ctx context.Context) error {
	_, err := d.pool.Exec(ctx, recoverClientOpsSQL)
	return err
}

func (d *DB) CleanupClientOps(ctx context.Context, before time.Time) error {
	_, err := d.pool.Exec(ctx, `DELETE FROM client_ops WHERE created_at < $1`, before)
	return err
}

// ReclaimClientOp takes back an operation a restart left unknown, for a kind whose call may run again (a meeting's
// follow-up: its creation record makes nothing twice); false when it is not unknown (finished, or taken already).
func (d *DB) ReclaimClientOp(ctx context.Context, id string) (bool, error) {
	tag, err := d.pool.Exec(ctx, `UPDATE client_ops SET state = 'pending', updated_at = now() WHERE id = $1 AND state = 'unknown'`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}
