package externalsourcestore

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
)

// ConfiguredExternalRefs finds explicit owner configuration. A repository
// projection participates in lookup after opt-in, but does not enable routing.
func (s *Store) ConfiguredExternalRefs(ctx context.Context, refs []assistantwire.ExternalRef) ([]assistantwire.ExternalRef, error) {
	var found []assistantwire.ExternalRef
	err := db.ReadSnapshot(ctx, s.database, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT ref FROM jsonb_array_elements($1::jsonb) AS path(ref)
			WHERE EXISTS (SELECT 1 FROM assistant_external_bindings WHERE external_ref = ref AND policy ? 'mode')
			   OR EXISTS (SELECT 1 FROM assistant_external_source_policies WHERE external_ref = ref AND policy ? 'mode')`, refs)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var ref assistantwire.ExternalRef
			if err := rows.Scan(&ref); err != nil {
				return err
			}
			found = append(found, ref)
		}
		return rows.Err()
	})
	return found, err
}

func (s *Store) ExternalRoutingConfiguration(ctx context.Context, path []assistantwire.ExternalRef) (assistantwire.ExternalRoutingConfiguration, error) {
	out := assistantwire.ExternalRoutingConfiguration{Lookup: assistantwire.ExternalBindingLookup{Path: path}}
	if len(path) == 0 {
		return out, errors.New("external routing path is empty")
	}
	err := db.ReadSnapshot(ctx, s.database, func(tx pgx.Tx) error {
		var err error
		out.Lookup.Matches, err = lookupMatches(ctx, tx, path)
		if err != nil {
			return err
		}
		out.Policies, err = scopedPolicies(ctx, tx, path)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT id, external_ref, target_kind, target_id, policy FROM assistant_external_bindings
			WHERE external_ref IN (SELECT value FROM jsonb_array_elements($1::jsonb))`, path)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			row, err := scanBinding(rows)
			if err != nil {
				return err
			}
			out.Bindings = append(out.Bindings, row)
		}
		return rows.Err()
	})
	return out, err
}

// SaveExternalInboxItems durably accepts source events and the corresponding Inbox
// changes together. Receipt identity is independent of Inbox row and binding ids.
func (s *Store) SaveExternalInboxItems(ctx context.Context, rows []db.InboxRow, events []assistantwire.ExternalInboxEvent, now time.Time) error {
	return s.database.Transaction(ctx, func(tx pgx.Tx) error {
		if err := db.WriteInboxItems(ctx, tx, rows); err != nil {
			return err
		}
		var batch pgx.Batch
		for _, event := range events {
			batch.Queue(`
				INSERT INTO assistant_external_inbox_events (id, external_ref, event_id, input, received_at, ready_at)
				VALUES ($1, $2, $3, $4, $5, $5)
				ON CONFLICT (external_ref, event_id) DO NOTHING`, event.Key, event.Ref, event.EventID, event, now)
		}
		return tx.SendBatch(ctx, &batch).Close()
	})
}

var errReceiptTaken = errors.New("another route owns the Inbox event")

// ClaimLegacyExternalInbox uses the same receipt key as scoped routing. A legacy
// receipt is complete from the scoped worker's point of view. Only the original
// Inbox version and recipient can retry it, through the legacy delivery path.
func (s *Store) ClaimLegacyExternalInbox(ctx context.Context, events []assistantwire.ExternalInboxEvent, owner string, now time.Time) (bool, error) {
	ordered := slices.Clone(events)
	slices.SortFunc(ordered, func(a, b assistantwire.ExternalInboxEvent) int { return cmp.Compare(a.Key, b.Key) })
	err := s.database.Transaction(ctx, func(tx pgx.Tx) error {
		for _, event := range ordered {
			tag, err := tx.Exec(ctx, `
				INSERT INTO assistant_external_inbox_events (id, external_ref, event_id, input, received_at, ready_at, completed)
				VALUES ($1, $2, $3, jsonb_build_object('legacy_forward_claim', $4::text), $5, $5, TRUE)
				ON CONFLICT (external_ref, event_id) DO NOTHING`, event.Key, event.Ref, event.EventID, owner, now)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 0 {
				continue
			}
			var owns bool
			err = tx.QueryRow(ctx, `
				SELECT COALESCE(input->>'legacy_forward_claim' = $3, FALSE)
				FROM assistant_external_inbox_events WHERE external_ref = $1 AND event_id = $2`,
				event.Ref, event.EventID, owner).Scan(&owns)
			if err != nil {
				return err
			}
			if !owns {
				return errReceiptTaken
			}
		}
		return nil
	})
	if errors.Is(err, errReceiptTaken) {
		return false, nil
	}
	return err == nil, err
}

// WithExternalInboxSweep serializes planning and delivery across overlapping manager
// processes. The lock's transaction holds no rows while the worker calls a model or node.
func (s *Store) WithExternalInboxSweep(ctx context.Context, run func() error) error {
	return s.database.Transaction(ctx, func(tx pgx.Tx) error {
		var locked bool
		if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(15517, 0)`).Scan(&locked); err != nil || !locked {
			return err
		}
		return run()
	})
}

func (s *Store) ReadyExternalInboxEvents(ctx context.Context, now time.Time, limit int) ([]assistantwire.ExternalInboxWork, error) {
	var work []assistantwire.ExternalInboxWork
	err := db.ReadSnapshot(ctx, s.database, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT input, received_at, plan FROM assistant_external_inbox_events
			WHERE NOT completed AND ready_at <= $1 ORDER BY ready_at, id LIMIT $2`, now, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var row assistantwire.ExternalInboxWork
			if err := rows.Scan(&row.ExternalInboxEvent, &row.ReceivedAt, &row.Plan); err != nil {
				return err
			}
			work = append(work, row)
		}
		return rows.Err()
	})
	return work, err
}

// RecentExternalInboxOwners includes reserved decisions as well as completed ones.
// Completed receipts keep only the owner, and discard the source content.
func (s *Store) RecentExternalInboxOwners(ctx context.Context, ref assistantwire.ExternalRef, except string) ([]assistantwire.ExternalInboxDelivery, error) {
	var owners []assistantwire.ExternalInboxDelivery
	err := db.ReadSnapshot(ctx, s.database, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT plan->'owner' FROM assistant_external_inbox_events
			WHERE external_ref = $1 AND plan ? 'owner' AND id <> $2
			ORDER BY received_at DESC LIMIT 32`, ref, except)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var owner assistantwire.ExternalInboxDelivery
			if err := rows.Scan(&owner); err != nil {
				return err
			}
			owners = append(owners, owner)
		}
		return rows.Err()
	})
	return owners, err
}

// SaveExternalInboxProgress persists decisions, retries, and terminal receipts.
func (s *Store) SaveExternalInboxProgress(ctx context.Context, key string, when time.Time, plan *assistantwire.ExternalInboxPlan, complete bool) error {
	return s.database.Transaction(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE assistant_external_inbox_events
			SET ready_at = $2, completed = $4,
			    plan = CASE WHEN $4 THEN jsonb_strip_nulls(jsonb_build_object('owner', $3::jsonb->'owner')) ELSE $3::jsonb END,
			    input = CASE WHEN $4 THEN '{}'::jsonb ELSE input END
			WHERE id = $1 AND NOT completed`, key, when, plan, complete)
		return err
	})
}

// LoadExternalInboxItem reads the Inbox row of a receipt discovered by a manager
// whose Inbox was already loaded when another manager committed the round.
func (s *Store) LoadExternalInboxItem(ctx context.Context, id assistantwire.ExternalInboxItemID) (json.RawMessage, error) {
	var raw json.RawMessage
	err := s.database.QueryRow(ctx, `SELECT data FROM inbox_items WHERE id = $1`, id).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return raw, err
}
