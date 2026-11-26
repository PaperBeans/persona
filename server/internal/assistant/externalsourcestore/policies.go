package externalsourcestore

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
)

// PutExternalSourcePolicy replaces a node's overrides. An empty policy restores inheritance.
func (s *Store) PutExternalSourcePolicy(ctx context.Context, p assistantwire.ExternalScopedPolicy) error {
	return s.database.Transaction(ctx, func(tx pgx.Tx) error {
		if p.Policy == (assistantwire.ExternalPolicy{}) {
			_, err := tx.Exec(ctx, `DELETE FROM assistant_external_source_policies WHERE external_ref = $1`, p.ExternalRef)
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO assistant_external_source_policies (external_ref, policy) VALUES ($1, $2)
			ON CONFLICT (external_ref) DO UPDATE SET policy = EXCLUDED.policy`, p.ExternalRef, p.Policy)
		return err
	})
}

// ChangeExternalSourcePolicy replaces the saved overrides only if they still match the approved before-image.
// An empty before policy denotes inheritance, and an empty after policy restores it.
func (s *Store) ChangeExternalSourcePolicy(ctx context.Context, p assistantwire.ExternalScopedPolicy, before assistantwire.ExternalPolicy) error {
	return s.database.Transaction(ctx, func(tx pgx.Tx) error {
		var tag pgconn.CommandTag
		var err error
		switch {
		case before == (assistantwire.ExternalPolicy{}):
			if p.Policy == before {
				var exists bool
				err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM assistant_external_source_policies WHERE external_ref = $1)`, p.ExternalRef).Scan(&exists)
				if err == nil && exists {
					return ErrChangeStale
				}
				return err
			}
			tag, err = tx.Exec(ctx, `
				INSERT INTO assistant_external_source_policies (external_ref, policy) VALUES ($1, $2)
				ON CONFLICT (external_ref) DO NOTHING`, p.ExternalRef, p.Policy)
		case p.Policy == (assistantwire.ExternalPolicy{}):
			tag, err = tx.Exec(ctx, `DELETE FROM assistant_external_source_policies WHERE external_ref = $1 AND policy = $2`, p.ExternalRef, before)
		default:
			tag, err = tx.Exec(ctx, `UPDATE assistant_external_source_policies SET policy = $3 WHERE external_ref = $1 AND policy = $2`, p.ExternalRef, before, p.Policy)
		}
		if err == nil && tag.RowsAffected() == 0 {
			return ErrChangeStale
		}
		return err
	})
}

// ExternalSourcePolicies reads the sparse overrides for the requested path and, optionally, a selected binding.
func (s *Store) ExternalSourcePolicies(ctx context.Context, path []assistantwire.ExternalRef, id assistantwire.ExternalBindingID) ([]assistantwire.ExternalScopedPolicy, *assistantwire.ExternalBinding, error) {
	list := []assistantwire.ExternalScopedPolicy{}
	var binding *assistantwire.ExternalBinding
	err := db.ReadSnapshot(ctx, s.database, func(tx pgx.Tx) error {
		if id != "" {
			row := tx.QueryRow(ctx, `SELECT id, external_ref, target_kind, target_id, policy FROM assistant_external_bindings WHERE id = $1`, id)
			b, err := scanBinding(row)
			if err != nil {
				return bindingError(err)
			}
			binding = &b
		}
		var err error
		list, err = scopedPolicies(ctx, tx, path)
		return err
	})
	return list, binding, err
}

func scopedPolicies(ctx context.Context, tx pgx.Tx, path []assistantwire.ExternalRef) ([]assistantwire.ExternalScopedPolicy, error) {
	rows, err := tx.Query(ctx, `
		SELECT external_ref, policy FROM assistant_external_source_policies
		WHERE external_ref IN (SELECT value FROM jsonb_array_elements($1::jsonb))`, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []assistantwire.ExternalScopedPolicy{}
	for rows.Next() {
		var p assistantwire.ExternalScopedPolicy
		if err := rows.Scan(&p.ExternalRef, &p.Policy); err != nil {
			return nil, err
		}
		list = append(list, p)
	}
	return list, rows.Err()
}
