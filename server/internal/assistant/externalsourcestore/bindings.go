package externalsourcestore

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
)

var (
	ErrBindingExists   = errors.New("this target is already bound to this external source")
	ErrBindingNotFound = errors.New("external source binding not found")
	ErrChangeStale     = errors.New("external source settings changed before the update")
)

// Store uses the manager's pool. References and write inputs have already been normalized by the domain adapter.
type Store struct{ database *db.DB }

func New(database *db.DB) *Store { return &Store{database: database} }

func (s *Store) CreateExternalBinding(ctx context.Context, in assistantwire.ExternalBindingInput) (assistantwire.ExternalBinding, error) {
	out := assistantwire.ExternalBinding{ExternalBindingInput: in}
	err := s.database.QueryRow(ctx, `
		INSERT INTO assistant_external_bindings (external_ref, target_kind, target_id, policy)
		VALUES ($1, $2, $3, $4) RETURNING id`, in.ExternalRef, in.TargetKind, in.TargetID, in.Policy).Scan(&out.ID)
	return out, bindingError(err)
}

func (s *Store) UpdateExternalBinding(ctx context.Context, id assistantwire.ExternalBindingID, in assistantwire.ExternalBindingInput) (assistantwire.ExternalBinding, error) {
	return updateExternalBinding(ctx, s.database, id, in)
}

type bindingWriter interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func updateExternalBinding(ctx context.Context, w bindingWriter, id assistantwire.ExternalBindingID, in assistantwire.ExternalBindingInput) (assistantwire.ExternalBinding, error) {
	out := assistantwire.ExternalBinding{ExternalBindingInput: in}
	err := w.QueryRow(ctx, `
		UPDATE assistant_external_bindings SET external_ref = $2, target_kind = $3, target_id = $4, policy = $5
		WHERE id = $1 RETURNING id`, id, in.ExternalRef, in.TargetKind, in.TargetID, in.Policy).Scan(&out.ID)
	return out, bindingError(err)
}

func (s *Store) DeleteExternalBinding(ctx context.Context, id assistantwire.ExternalBindingID) error {
	return deleteExternalBinding(ctx, s.database, id)
}

func deleteExternalBinding(ctx context.Context, w bindingWriter, id assistantwire.ExternalBindingID) error {
	return bindingError(w.QueryRow(ctx, `DELETE FROM assistant_external_bindings WHERE id = $1 RETURNING id`, id).Scan(&id))
}

// ChangeExternalBinding edits or removes exactly the binding shown for approval.
func (s *Store) ChangeExternalBinding(ctx context.Context, before assistantwire.ExternalBinding, after *assistantwire.ExternalBindingInput) error {
	return s.database.Transaction(ctx, func(tx pgx.Tx) error {
		var id assistantwire.ExternalBindingID
		err := tx.QueryRow(ctx, `
			SELECT id FROM assistant_external_bindings
			WHERE id = $1 AND external_ref = $2 AND target_kind = $3 AND target_id = $4 AND policy = $5
			FOR UPDATE`, before.ID, before.ExternalRef, before.TargetKind, before.TargetID, before.Policy).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrChangeStale
		}
		if err != nil {
			return err
		}
		if after == nil {
			return deleteExternalBinding(ctx, tx, id)
		}
		_, err = updateExternalBinding(ctx, tx, id, *after)
		return err
	})
}

func bindingError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrBindingNotFound
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.ConstraintName {
		case "assistant_external_binding_unique":
			return ErrBindingExists
		case "assistant_external_binding_todo":
			return db.ErrTodoNotFound
		}
	}
	return err
}

func (s *Store) ListExternalBindings(ctx context.Context, kind assistantwire.ExternalTargetKind, target assistantwire.ExternalTargetID, provider assistantwire.ExternalProvider, ref *assistantwire.ExternalRef) ([]assistantwire.ExternalBinding, error) {
	list := []assistantwire.ExternalBinding{}
	err := s.database.Transaction(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, external_ref, target_kind, target_id, policy FROM assistant_external_bindings
			WHERE ($1 = '' OR target_kind = $1) AND ($2 = '' OR target_id = $2)
			  AND ($3 = '' OR external_ref->>'provider' = $3) AND ($4::jsonb IS NULL OR external_ref = $4)
			ORDER BY external_ref, target_kind, target_id`, kind, target, provider, ref)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			b, err := scanBinding(rows)
			if err != nil {
				return err
			}
			list = append(list, b)
		}
		return rows.Err()
	})
	return list, err
}

func scanBinding(row pgx.Row) (assistantwire.ExternalBinding, error) {
	var b assistantwire.ExternalBinding
	err := row.Scan(&b.ID, &b.ExternalRef, &b.TargetKind, &b.TargetID, &b.Policy)
	return b, err
}
