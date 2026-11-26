package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	// TodoBlockerStateActive indicates a blocker edge is currently blocking its to-do.
	TodoBlockerStateActive = "active"
	// TodoBlockerStateResolved indicates the blocker condition was satisfied by ground-truth state.
	TodoBlockerStateResolved = "resolved"
	// TodoBlockerStateCancelled indicates the blocker edge was explicitly cancelled.
	TodoBlockerStateCancelled = "cancelled"
)

// TodoBlockerRow persists one pull-based blocker edge for a to-do under todos_v2 (#15731).
type TodoBlockerRow struct {
	ID            string
	TodoID        string
	Kind          string
	TargetRef     string
	ConditionJSON string
	State         string
	Attempts      int
	CreatedAt     time.Time
	UpdatedAt     time.Time
	ResolvedAt    *time.Time
}

const todoBlockerCols = `id, todo_id, kind, target_ref, condition_json, state, attempts, created_at, updated_at, resolved_at`

func scanTodoBlocker(row pgx.Row) (*TodoBlockerRow, error) {
	var r TodoBlockerRow
	err := row.Scan(
		&r.ID,
		&r.TodoID,
		&r.Kind,
		&r.TargetRef,
		&r.ConditionJSON,
		&r.State,
		&r.Attempts,
		&r.CreatedAt,
		&r.UpdatedAt,
		&r.ResolvedAt,
	)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func scanTodoBlockerRows(rows pgx.Rows) ([]TodoBlockerRow, error) {
	var out []TodoBlockerRow
	for rows.Next() {
		r, err := scanTodoBlocker(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// UpsertTodoBlocker inserts or updates a persistent blocker edge by its primary key id.
func (d *DB) UpsertTodoBlocker(ctx context.Context, r TodoBlockerRow) (*TodoBlockerRow, error) {
	r.ID = strings.TrimSpace(r.ID)
	r.TodoID = strings.TrimSpace(r.TodoID)
	r.Kind = strings.TrimSpace(r.Kind)
	r.TargetRef = strings.TrimSpace(r.TargetRef)
	if r.ID == "" || r.TodoID == "" || r.Kind == "" || r.TargetRef == "" {
		return nil, fmt.Errorf("todo_blockers: id, todo_id, kind, and target_ref are required")
	}
	r.ConditionJSON = strings.TrimSpace(r.ConditionJSON)
	if r.ConditionJSON == "" {
		r.ConditionJSON = "{}"
	}
	r.State = strings.TrimSpace(r.State)
	if r.State == "" {
		r.State = TodoBlockerStateActive
	}
	if r.Attempts < 0 {
		r.Attempts = 0
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = r.CreatedAt
	}

	return scanTodoBlocker(d.pool.QueryRow(ctx, `INSERT INTO todo_blockers (
		id, todo_id, kind, target_ref, condition_json, state, attempts, created_at, updated_at, resolved_at
	) VALUES (
		$1, $2, $3, $4, $5, $6, $7, $8, $9, $10
	) ON CONFLICT (id) DO UPDATE SET
		todo_id = EXCLUDED.todo_id,
		kind = EXCLUDED.kind,
		target_ref = EXCLUDED.target_ref,
		condition_json = EXCLUDED.condition_json,
		state = EXCLUDED.state,
		attempts = EXCLUDED.attempts,
		updated_at = EXCLUDED.updated_at,
		resolved_at = EXCLUDED.resolved_at
	RETURNING `+todoBlockerCols,
		r.ID, r.TodoID, r.Kind, r.TargetRef, r.ConditionJSON, r.State, r.Attempts, r.CreatedAt, r.UpdatedAt, r.ResolvedAt,
	))
}

// ListActiveTodoBlockers returns all active blocker edges ordered deterministically by creation time and id.
func (d *DB) ListActiveTodoBlockers(ctx context.Context) ([]TodoBlockerRow, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT `+todoBlockerCols+` FROM todo_blockers WHERE state = $1 ORDER BY created_at ASC, id ASC`,
		TodoBlockerStateActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTodoBlockerRows(rows)
}

// ListTodoBlockersByTodo returns all blocker edges (any state) attached to todoID.
func (d *DB) ListTodoBlockersByTodo(ctx context.Context, todoID string) ([]TodoBlockerRow, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT `+todoBlockerCols+` FROM todo_blockers WHERE todo_id = $1 ORDER BY created_at ASC, id ASC`,
		strings.TrimSpace(todoID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTodoBlockerRows(rows)
}

// ListActiveTodoBlockersByTarget returns all active blocker edges waiting on targetRef
// using case-insensitive matching so mixed-case PR URLs hit the event-driven fast path.
func (d *DB) ListActiveTodoBlockersByTarget(ctx context.Context, targetRef string) ([]TodoBlockerRow, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT `+todoBlockerCols+` FROM todo_blockers WHERE LOWER(target_ref) = LOWER($1) AND state = $2 ORDER BY created_at ASC, id ASC`,
		strings.TrimSpace(targetRef), TodoBlockerStateActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTodoBlockerRows(rows)
}

// ResolveTodoBlocker marks blocker id as resolved and sets resolved_at when unset.
// Returns nil, nil when id does not exist.
func (d *DB) ResolveTodoBlocker(ctx context.Context, id string, at time.Time) (*TodoBlockerRow, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	row, err := scanTodoBlocker(d.pool.QueryRow(ctx, `UPDATE todo_blockers
		SET state = $2,
		    updated_at = $3,
		    resolved_at = COALESCE(resolved_at, $3)
		WHERE id = $1
		RETURNING `+todoBlockerCols,
		strings.TrimSpace(id), TodoBlockerStateResolved, at))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return row, err
}

// CancelTodoBlocker marks blocker id as cancelled and sets resolved_at when unset.
// Returns nil, nil when id does not exist.
func (d *DB) CancelTodoBlocker(ctx context.Context, id string, at time.Time) (*TodoBlockerRow, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	row, err := scanTodoBlocker(d.pool.QueryRow(ctx, `UPDATE todo_blockers
		SET state = $2,
		    updated_at = $3,
		    resolved_at = COALESCE(resolved_at, $3)
		WHERE id = $1
		RETURNING `+todoBlockerCols,
		strings.TrimSpace(id), TodoBlockerStateCancelled, at))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return row, err
}

// IncrementTodoBlockerAttempts atomically increments the persistent attempt counter for blocker id.
// Returns nil, nil when id does not exist.
func (d *DB) IncrementTodoBlockerAttempts(ctx context.Context, id string, at time.Time) (*TodoBlockerRow, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	row, err := scanTodoBlocker(d.pool.QueryRow(ctx, `UPDATE todo_blockers
		SET attempts = attempts + 1,
		    updated_at = $2
		WHERE id = $1
		RETURNING `+todoBlockerCols,
		strings.TrimSpace(id), at))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return row, err
}
