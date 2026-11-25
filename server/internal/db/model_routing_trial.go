package db

import (
	"context"
	"time"
)

// ModelRoutingTrialSummary holds the last-N-days dry-run counts for one inactive model profile (#14702).
// Dispatches is the number of distinct dispatch decisions since the cutoff that have a trial row for ProfileID;
// WouldSwitch is how many of those decisions picked a launcher or model different from the active row of the same
// decision.
type ModelRoutingTrialSummary struct {
	ProfileID   string
	Dispatches  int
	WouldSwitch int
}

// ModelRoutingTrialSummaries counts distinct trial dispatch decisions and would-switch decisions per profile since
// since, using the time index on model_routing (at).
func (d *DB) ModelRoutingTrialSummaries(ctx context.Context, since time.Time) ([]ModelRoutingTrialSummary, error) {
	rows, err := d.pool.Query(ctx, `
		WITH trial_decisions AS (
			SELECT DISTINCT ON (
				t.profile_id,
				CASE WHEN t.session_id <> '' THEN 's:' || t.session_id
				     ELSE 'r:' || t.todo_id || ':' || t.plan_id || ':' || t.task_index::text || ':' || t.at::text END
			)
				t.profile_id,
				t.at,
				t.todo_id,
				t.plan_id,
				t.task_index,
				t.session_id,
				t.launcher,
				t.model
			FROM model_routing t
			WHERE t.at >= $1 AND NOT t.active
			ORDER BY
				t.profile_id,
				CASE WHEN t.session_id <> '' THEN 's:' || t.session_id
				     ELSE 'r:' || t.todo_id || ':' || t.plan_id || ':' || t.task_index::text || ':' || t.at::text END,
				t.at DESC,
				t.id DESC
		)
		SELECT
			td.profile_id,
			COUNT(*)::int AS dispatches,
			COUNT(*) FILTER (
				WHERE a.id IS NOT NULL AND (td.launcher <> a.launcher OR td.model <> a.model)
			)::int AS would_switch
		FROM trial_decisions td
		LEFT JOIN LATERAL (
			SELECT mr.id, mr.launcher, mr.model
			FROM model_routing mr
			WHERE mr.active
			  AND mr.at >= $1 - interval '1 minute'
			  AND (
			      (td.session_id <> '' AND mr.session_id = td.session_id)
			      OR (td.session_id = '' AND mr.session_id = '' AND mr.todo_id = td.todo_id
			          AND mr.plan_id = td.plan_id AND mr.task_index = td.task_index AND mr.at = td.at)
			  )
			ORDER BY mr.at DESC, mr.id DESC
			LIMIT 1
		) a ON true
		GROUP BY td.profile_id
		ORDER BY td.profile_id`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModelRoutingTrialSummary
	for rows.Next() {
		var s ModelRoutingTrialSummary
		if err := rows.Scan(&s.ProfileID, &s.Dispatches, &s.WouldSwitch); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
