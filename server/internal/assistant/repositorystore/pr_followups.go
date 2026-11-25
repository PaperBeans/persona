package repositorystore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nwverse/persona/server/internal/assistant/repository"
)

// followupColumns is the shared column list for reading one subscription row.
const followupColumns = `followup_id, todo_id, pr_url, supervisor, origin, head_oid, state, review,
	changes_requested_at, last_observed_at, created_at, notified_conflict_head, notified_review_head,
	watch_status, watch_reason, watch_updated_at`

// scanPRFollowup scans one subscription from any pgx row source.
func scanPRFollowup(row pgx.Row) (repository.PRFollowup, error) {
	var f repository.PRFollowup
	err := row.Scan(&f.ID, &f.TodoID, &f.PRURL, &f.Supervisor, &f.Origin, &f.HeadOID, &f.State, &f.Review,
		&f.ChangesRequestedAt, &f.LastObservedAt, &f.CreatedAt, &f.NotifiedConflictHead, &f.NotifiedReviewHead,
		&f.WatchStatus, &f.WatchReason, &f.WatchUpdatedAt)
	return f, err
}

// prFollowupID derives a stable subscription id from its to-do and PR so re-acceptance is idempotent.
func prFollowupID(todoID, prURL string) string {
	sum := sha256.Sum256([]byte(todoID + "|" + prURL))
	return "fup_" + hex.EncodeToString(sum[:12])
}

// UpsertPRFollowups records an active follow-up for each still-open delivery PR of a to-do that was just accepted. An
// existing subscription for the pair is left untouched (its observed facts and notifications continue), so this is
// safe to call on every acceptance.
func (d *Store) UpsertPRFollowups(ctx context.Context, todoID, supervisor, origin string, prURLs []string, now time.Time) error {
	if len(prURLs) == 0 {
		return nil
	}
	subs := make([]repository.PRFollowup, 0, len(prURLs))
	for _, prURL := range prURLs {
		subs = append(subs, repository.PRFollowup{
			TodoID:      todoID,
			PRURL:       prURL,
			Supervisor:  supervisor,
			Origin:      origin,
			CreatedAt:   now,
			WatchStatus: repository.PRFollowupWatchActive,
		})
	}
	return d.UpsertPRFollowupRecords(ctx, subs)
}

// UpsertPRFollowupRecords persists one or more post-closure PR subscriptions along with their initial watch status
// atomically. Existing (todo_id, pr_url) subscriptions are left untouched.
func (d *Store) UpsertPRFollowupRecords(ctx context.Context, subs []repository.PRFollowup) error {
	if len(subs) == 0 {
		return nil
	}
	return d.Transaction(ctx, func(tx pgx.Tx) error {
		for _, sub := range subs {
			id := sub.ID
			if id == "" {
				id = prFollowupID(sub.TodoID, sub.PRURL)
			}
			watchStatus := sub.EffectiveWatchStatus()
			watchReason := sub.WatchReason
			watchUpdatedAt := sub.WatchUpdatedAt
			if watchStatus == repository.PRFollowupWatchActive {
				watchReason = ""
			} else if watchUpdatedAt == nil {
				at := sub.CreatedAt.UTC()
				watchUpdatedAt = &at
			}
			_, err := tx.Exec(ctx, `INSERT INTO todo_pr_followups
				(followup_id, todo_id, pr_url, supervisor, origin, created_at, watch_status, watch_reason, watch_updated_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
				ON CONFLICT (todo_id, pr_url) DO NOTHING`,
				id, sub.TodoID, sub.PRURL, sub.Supervisor, sub.Origin, sub.CreatedAt, watchStatus, watchReason, watchUpdatedAt)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// ListPRFollowups reads every active or unwatched post-closure subscription, oldest creation first so a long-idle or
// unwatched one is visible to the sweep and the daily briefing.
func (d *Store) ListPRFollowups(ctx context.Context) ([]repository.PRFollowup, error) {
	var out []repository.PRFollowup
	err := d.Transaction(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+followupColumns+` FROM todo_pr_followups ORDER BY created_at ASC`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			f, err := scanPRFollowup(rows)
			if err != nil {
				return err
			}
			out = append(out, f)
		}
		return rows.Err()
	})
	return out, err
}

// SavePRFollowupObservation persists one sweep's observed facts, notification dedup heads, and recovered active watch
// status, refusing to overwrite with an older observation. A live observation always clears any previous unwatched
// status back to active. It returns whether a row was updated.
func (d *Store) SavePRFollowupObservation(ctx context.Context, f repository.PRFollowup) (bool, error) {
	if f.LastObservedAt == nil {
		return false, repository.Problem("invalid")
	}
	const (
		watchStatus = repository.PRFollowupWatchActive
		watchReason = ""
	)
	observedAt := f.LastObservedAt.UTC()
	var updated bool
	err := d.Transaction(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE todo_pr_followups SET
			head_oid=$2, state=$3, review=$4, changes_requested_at=$5, last_observed_at=$6,
			notified_conflict_head=$7, notified_review_head=$8,
			watch_status=$9, watch_reason=$10,
			watch_updated_at=CASE
				WHEN watch_status IS DISTINCT FROM $9 OR watch_reason IS DISTINCT FROM $10 OR watch_updated_at IS NULL THEN $6
				ELSE watch_updated_at
			END
			WHERE followup_id=$1
			  AND (last_observed_at IS NULL OR last_observed_at <= $6)
			  AND (watch_updated_at IS NULL OR watch_updated_at <= $6)`,
			f.ID, f.HeadOID, f.State, f.Review, f.ChangesRequestedAt, observedAt,
			f.NotifiedConflictHead, f.NotifiedReviewHead, watchStatus, watchReason)
		if err != nil {
			return err
		}
		updated = tag.RowsAffected() > 0
		return nil
	})
	return updated, err
}

// SavePRFollowupWatchStatus records a subscription's watch status (such as host_not_connected) without modifying
// last_observed_at or previously observed forge facts, refusing to overwrite with an older timestamp.
func (d *Store) SavePRFollowupWatchStatus(ctx context.Context, id, status, reason string, updatedAt time.Time) (bool, error) {
	if id == "" || updatedAt.IsZero() {
		return false, repository.Problem("invalid")
	}
	if status == "" {
		status = repository.PRFollowupWatchActive
	}
	if status == repository.PRFollowupWatchActive {
		reason = ""
	}
	at := updatedAt.UTC()
	var updated bool
	err := d.Transaction(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE todo_pr_followups SET
			watch_status=$2, watch_reason=$3, watch_updated_at=$4
			WHERE followup_id=$1
			  AND (last_observed_at IS NULL OR last_observed_at <= $4)
			  AND (watch_updated_at IS NULL OR watch_updated_at <= $4)`,
			id, status, reason, at)
		if err != nil {
			return err
		}
		updated = tag.RowsAffected() > 0
		return nil
	})
	return updated, err
}

// DeletePRFollowup removes a subscription once its PR reaches a terminal state, or when its to-do is gone.
func (d *Store) DeletePRFollowup(ctx context.Context, id string) error {
	return d.Transaction(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM todo_pr_followups WHERE followup_id=$1`, id)
		return err
	})
}

// StalePRFollowups returns subscriptions for the daily briefing: any subscription recorded as unwatched
// (host_not_connected), plus actively watched subscriptions whose PR has shown no terminal change and whose last
// observation (or creation, when never polled) is older than before, oldest creation first.
func (d *Store) StalePRFollowups(ctx context.Context, before time.Time) ([]repository.PRFollowup, error) {
	var out []repository.PRFollowup
	err := d.Transaction(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+followupColumns+`
			FROM todo_pr_followups
			WHERE (watch_status <> '' AND watch_status <> $2)
			   OR COALESCE(last_observed_at, created_at) < $1
			ORDER BY created_at ASC`, before, repository.PRFollowupWatchActive)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			f, err := scanPRFollowup(rows)
			if err != nil {
				return err
			}
			out = append(out, f)
		}
		return rows.Err()
	})
	return out, err
}
