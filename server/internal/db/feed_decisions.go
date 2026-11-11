package db

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// lockFeedRef orders message insertion with version-sensitive body updates across
// overlapping manager processes. Non-message feed entries need no such fence.
func lockFeedRef(ctx context.Context, tx pgx.Tx, kind, ref string) error {
	if kind != "message" {
		return nil
	}
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "feed-message:"+ref)
	return err
}

// ApplyFeedDecision compares and replaces one message body's exact stored version.
// A superseding decision (Undo) additionally requires the latest card for its ref
// in this conversation. Its audit commits with the body; false changes neither.
func (d *DB) ApplyFeedDecision(ctx context.Context, before FeedRow, body json.RawMessage, decision TodoDecisionRow, at time.Time) (bool, error) {
	changed := false
	err := pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		if err := lockFeedRef(ctx, tx, before.Kind, before.Ref); err != nil {
			return err
		}
		var one int
		err := tx.QueryRow(ctx, `SELECT 1 FROM todos WHERE id = $1 FOR UPDATE`, decision.TodoID).Scan(&one)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTodoNotFound
		}
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE assistant_feed f SET body = $3, updated_at = $4
			WHERE f.id = $1 AND f.kind = 'message' AND f.body = $2::jsonb
			AND f.body->>'todo_id' = $5
			AND ($6 = '' OR NOT EXISTS (SELECT 1 FROM assistant_feed n
				WHERE n.conversation = f.conversation AND n.kind = f.kind AND n.ref = f.ref
				AND (n.at, n.id) > (f.at, f.id)))`, before.ID, []byte(before.Body), []byte(body), at, decision.TodoID, decision.SupersedesID)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		if decision.SupersedesID != "" {
			if err := checkSupersede(ctx, tx, decision.TodoID, decision.SupersedesID, decision.ID, decision.CreatedAt); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `INSERT INTO todo_decisions (id, todo_id, question, choice, reason, source, source_machine,
				source_session, source_message, supersedes_id, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NULLIF($10, ''), $11)`,
			decision.ID, decision.TodoID, decision.Question, decision.Choice, decision.Reason, decision.Source,
			decision.SourceMachine, decision.SourceSession, decision.SourceMessage, decision.SupersedesID, decision.CreatedAt)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE todos SET updated_at = $2 WHERE id = $1`, decision.TodoID, at)
		changed = err == nil
		return err
	})
	return changed && err == nil, err
}
