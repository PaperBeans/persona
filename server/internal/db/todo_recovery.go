package db

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// TodoRecoveryState persists the latest active supervised run checkpoint and bounded
// restart recovery attempt counter for a to-do under todos_v2 (#15733).
type TodoRecoveryState struct {
	TodoID              string
	MachineID           string
	SessionID           string
	LastActiveBootEpoch string
	RecoveryAttempts    int
	LastRecoveredAt     *time.Time
	UpdatedAt           time.Time
}

// RecordTodoActiveCheckpoint upserts the active supervised run checkpoint for a to-do.
// Existing recovery_attempts and last_recovered_at are preserved when updating an existing row.
func (d *DB) RecordTodoActiveCheckpoint(ctx context.Context, row TodoRecoveryState) error {
	row.TodoID = strings.TrimSpace(row.TodoID)
	row.MachineID = strings.TrimSpace(row.MachineID)
	row.SessionID = strings.TrimSpace(row.SessionID)
	row.LastActiveBootEpoch = strings.TrimSpace(row.LastActiveBootEpoch)
	if row.TodoID == "" || row.MachineID == "" || row.SessionID == "" || row.LastActiveBootEpoch == "" {
		return errors.New("todo recovery checkpoint requires todo_id, machine_id, session_id and last_active_boot_epoch")
	}
	if row.RecoveryAttempts < 0 {
		row.RecoveryAttempts = 0
	}
	if row.UpdatedAt.IsZero() {
		row.UpdatedAt = time.Now().UTC()
	}
	_, err := d.pool.Exec(ctx, `INSERT INTO todo_recovery_state
			(todo_id, machine_id, session_id, last_active_boot_epoch, recovery_attempts, last_recovered_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (todo_id) DO UPDATE SET
			machine_id = EXCLUDED.machine_id,
			session_id = EXCLUDED.session_id,
			last_active_boot_epoch = EXCLUDED.last_active_boot_epoch,
			updated_at = EXCLUDED.updated_at`,
		row.TodoID, row.MachineID, row.SessionID, row.LastActiveBootEpoch,
		row.RecoveryAttempts, row.LastRecoveredAt, row.UpdatedAt.UTC())
	return err
}

// GetTodoRecoveryState returns the persisted restart recovery state for todoID, if any.
func (d *DB) GetTodoRecoveryState(ctx context.Context, todoID string) (TodoRecoveryState, bool, error) {
	todoID = strings.TrimSpace(todoID)
	if todoID == "" {
		return TodoRecoveryState{}, false, nil
	}
	var row TodoRecoveryState
	err := d.pool.QueryRow(ctx, `SELECT todo_id, machine_id, session_id, last_active_boot_epoch,
			recovery_attempts, last_recovered_at, updated_at
		FROM todo_recovery_state WHERE todo_id = $1`, todoID).Scan(
		&row.TodoID, &row.MachineID, &row.SessionID, &row.LastActiveBootEpoch,
		&row.RecoveryAttempts, &row.LastRecoveredAt, &row.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return TodoRecoveryState{}, false, nil
	}
	if err != nil {
		return TodoRecoveryState{}, false, err
	}
	return row, true, nil
}

// ListTodoRecoveryStates returns all persisted restart recovery checkpoints ordered deterministically.
func (d *DB) ListTodoRecoveryStates(ctx context.Context) ([]TodoRecoveryState, error) {
	rows, err := d.pool.Query(ctx, `SELECT todo_id, machine_id, session_id, last_active_boot_epoch,
			recovery_attempts, last_recovered_at, updated_at
		FROM todo_recovery_state
		ORDER BY updated_at ASC, todo_id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TodoRecoveryState
	for rows.Next() {
		var r TodoRecoveryState
		if err := rows.Scan(
			&r.TodoID, &r.MachineID, &r.SessionID, &r.LastActiveBootEpoch,
			&r.RecoveryAttempts, &r.LastRecoveredAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// IncrementTodoRecoveryAttempts atomically increments recovery_attempts and updates
// last_active_boot_epoch and last_recovered_at for todoID.
func (d *DB) IncrementTodoRecoveryAttempts(ctx context.Context, todoID, newBootEpoch string, recoveredAt time.Time) (TodoRecoveryState, error) {
	todoID = strings.TrimSpace(todoID)
	newBootEpoch = strings.TrimSpace(newBootEpoch)
	if todoID == "" || newBootEpoch == "" {
		return TodoRecoveryState{}, errors.New("increment todo recovery attempts requires todo_id and new_boot_epoch")
	}
	if recoveredAt.IsZero() {
		recoveredAt = time.Now().UTC()
	}
	at := recoveredAt.UTC()
	var row TodoRecoveryState
	err := d.pool.QueryRow(ctx, `UPDATE todo_recovery_state
		SET recovery_attempts = recovery_attempts + 1,
			last_active_boot_epoch = $2,
			last_recovered_at = $3,
			updated_at = $3
		WHERE todo_id = $1
		RETURNING todo_id, machine_id, session_id, last_active_boot_epoch,
			recovery_attempts, last_recovered_at, updated_at`,
		todoID, newBootEpoch, at).Scan(
		&row.TodoID, &row.MachineID, &row.SessionID, &row.LastActiveBootEpoch,
		&row.RecoveryAttempts, &row.LastRecoveredAt, &row.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return TodoRecoveryState{}, ErrTodoNotFound
	}
	if err != nil {
		return TodoRecoveryState{}, err
	}
	return row, nil
}

// ResetTodoRecoveryState resets recovery_attempts to 0 and clears last_recovered_at when a task
// resumes normal progress or completes.
func (d *DB) ResetTodoRecoveryState(ctx context.Context, todoID string, at time.Time) error {
	todoID = strings.TrimSpace(todoID)
	if todoID == "" {
		return nil
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	_, err := d.pool.Exec(ctx, `UPDATE todo_recovery_state
		SET recovery_attempts = 0,
			last_recovered_at = NULL,
			updated_at = $2
		WHERE todo_id = $1`, todoID, at.UTC())
	return err
}

// DeleteTodoRecoveryState removes the persisted restart recovery state for todoID.
func (d *DB) DeleteTodoRecoveryState(ctx context.Context, todoID string) error {
	todoID = strings.TrimSpace(todoID)
	if todoID == "" {
		return nil
	}
	_, err := d.pool.Exec(ctx, `DELETE FROM todo_recovery_state WHERE todo_id = $1`, todoID)
	return err
}
