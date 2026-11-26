package db

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// TodoContextHandoffRow persists an auditable context handoff artifact created when continuing a to-do
// on another provider or launcher (#12980). (todo_id, operation_id) is unique so retries and racing
// requests cannot create duplicate handoffs or inject context twice.
type TodoContextHandoffRow struct {
	ID             string
	OperationID    string
	TodoID         string
	SourceMachine  string
	SourceSession  string
	SourceLauncher string
	SourceAgent    string
	TargetMachine  string
	TargetSession  string
	TargetLauncher string
	TargetAgent    string
	SourceRanges   json.RawMessage
	BriefRevision  string
	DiffRef        json.RawMessage
	Strategy       string
	Generator      string
	Summary        string
	MissingSources json.RawMessage
	SupersedesID   string
	DeliveryStatus string
	DeliveryError  string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	DeliveredAt    *time.Time
}

const todoContextHandoffCols = `id, operation_id, todo_id, source_machine, source_session, source_launcher, source_agent,
	target_machine, target_session, target_launcher, target_agent,
	source_ranges, brief_revision, diff_ref, strategy, generator, summary, missing_sources,
	supersedes_id, delivery_status, delivery_error, created_at, updated_at, delivered_at`

func scanTodoContextHandoff(row pgx.Row) (*TodoContextHandoffRow, error) {
	var r TodoContextHandoffRow
	var sourceRanges, diffRef, missingSources []byte
	err := row.Scan(
		&r.ID, &r.OperationID, &r.TodoID,
		&r.SourceMachine, &r.SourceSession, &r.SourceLauncher, &r.SourceAgent,
		&r.TargetMachine, &r.TargetSession, &r.TargetLauncher, &r.TargetAgent,
		&sourceRanges, &r.BriefRevision, &diffRef, &r.Strategy, &r.Generator, &r.Summary, &missingSources,
		&r.SupersedesID, &r.DeliveryStatus, &r.DeliveryError, &r.CreatedAt, &r.UpdatedAt, &r.DeliveredAt,
	)
	if err != nil {
		return nil, err
	}
	if len(sourceRanges) > 0 {
		r.SourceRanges = sourceRanges
	}
	if len(diffRef) > 0 {
		r.DiffRef = diffRef
	}
	if len(missingSources) > 0 {
		r.MissingSources = missingSources
	}
	return &r, nil
}

// CreateTodoContextHandoff inserts r if no handoff exists for (todo_id, operation_id).
// On conflict it returns the existing row with created=false so callers stay strictly idempotent.
func (d *DB) CreateTodoContextHandoff(ctx context.Context, r TodoContextHandoffRow) (*TodoContextHandoffRow, bool, error) {
	sourceRanges := r.SourceRanges
	if len(sourceRanges) == 0 {
		sourceRanges = json.RawMessage(`[]`)
	}
	missingSources := r.MissingSources
	if len(missingSources) == 0 {
		missingSources = json.RawMessage(`[]`)
	}
	var diffRef any
	if len(r.DiffRef) > 0 && string(r.DiffRef) != "null" {
		diffRef = []byte(r.DiffRef)
	}
	if r.DeliveryStatus == "" {
		r.DeliveryStatus = "pending"
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = r.CreatedAt
	}
	tag, err := d.pool.Exec(ctx, `INSERT INTO todo_context_handoffs (
		id, operation_id, todo_id, source_machine, source_session, source_launcher, source_agent,
		target_machine, target_session, target_launcher, target_agent,
		source_ranges, brief_revision, diff_ref, strategy, generator, summary, missing_sources,
		supersedes_id, delivery_status, delivery_error, created_at, updated_at, delivered_at
	) VALUES (
		$1, $2, $3, $4, $5, $6, $7,
		$8, $9, $10, $11,
		$12, $13, $14, $15, $16, $17, $18,
		$19, $20, $21, $22, $23, $24
	) ON CONFLICT (todo_id, operation_id) DO NOTHING`,
		r.ID, r.OperationID, r.TodoID, r.SourceMachine, r.SourceSession, r.SourceLauncher, r.SourceAgent,
		r.TargetMachine, r.TargetSession, r.TargetLauncher, r.TargetAgent,
		[]byte(sourceRanges), r.BriefRevision, diffRef, r.Strategy, r.Generator, r.Summary, []byte(missingSources),
		r.SupersedesID, r.DeliveryStatus, r.DeliveryError, r.CreatedAt, r.UpdatedAt, r.DeliveredAt,
	)
	if err != nil {
		return nil, false, err
	}
	if tag.RowsAffected() == 1 {
		saved, err := d.GetTodoContextHandoff(ctx, r.ID)
		return saved, true, err
	}
	existing, err := d.GetTodoContextHandoffByOperation(ctx, r.TodoID, r.OperationID)
	return existing, false, err
}

// UpdateTodoContextHandoffDelivery updates the delivery outcome of handoff id.
func (d *DB) UpdateTodoContextHandoffDelivery(ctx context.Context, id, targetMachine, targetSession, targetAgent, status, deliveryError string, at time.Time) (*TodoContextHandoffRow, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	var deliveredAt *time.Time
	if status == "accepted" {
		deliveredAt = &at
	}
	row, err := scanTodoContextHandoff(d.pool.QueryRow(ctx, `UPDATE todo_context_handoffs
		SET target_machine = CASE WHEN $2 <> '' THEN $2 ELSE target_machine END,
		    target_session = CASE WHEN $3 <> '' THEN $3 ELSE target_session END,
		    target_agent = CASE WHEN $4 <> '' THEN $4 ELSE target_agent END,
		    delivery_status = $5,
		    delivery_error = $6,
		    updated_at = $7,
		    delivered_at = COALESCE($8, delivered_at)
		WHERE id = $1
		RETURNING `+todoContextHandoffCols,
		id, targetMachine, targetSession, targetAgent, status, deliveryError, at, deliveredAt))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return row, err
}

// GetTodoContextHandoff returns handoff id, or nil when absent.
func (d *DB) GetTodoContextHandoff(ctx context.Context, id string) (*TodoContextHandoffRow, error) {
	row, err := scanTodoContextHandoff(d.pool.QueryRow(ctx,
		`SELECT `+todoContextHandoffCols+` FROM todo_context_handoffs WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return row, err
}

// GetTodoContextHandoffByOperation returns the handoff for (todoID, operationID), or nil when absent.
func (d *DB) GetTodoContextHandoffByOperation(ctx context.Context, todoID, operationID string) (*TodoContextHandoffRow, error) {
	row, err := scanTodoContextHandoff(d.pool.QueryRow(ctx,
		`SELECT `+todoContextHandoffCols+` FROM todo_context_handoffs WHERE todo_id = $1 AND operation_id = $2`,
		todoID, operationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return row, err
}

// ListTodoContextHandoffs returns every handoff recorded for todoID, newest first.
func (d *DB) ListTodoContextHandoffs(ctx context.Context, todoID string) ([]TodoContextHandoffRow, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT `+todoContextHandoffCols+` FROM todo_context_handoffs WHERE todo_id = $1 ORDER BY created_at DESC, id DESC`,
		todoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TodoContextHandoffRow
	for rows.Next() {
		r, err := scanTodoContextHandoff(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}
