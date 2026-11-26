package db

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Settings reads several JSON settings in one statement. A caller merging a base
// configuration with a layer therefore sees one statement-consistent generation,
// rather than two reads that a concurrent writer could tear apart.
func (d *DB) Settings(ctx context.Context, keys ...string) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	if len(keys) == 0 {
		return out, nil
	}
	rows, err := d.pool.Query(ctx, `SELECT key, value FROM `+d.settingsTable()+` WHERE key = ANY($1)`, keys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var value json.RawMessage
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

var errPushGradingOwnerConflict = errors.New("push grading layer owner conflict")

// pushGradingWrite serializes every base/layer mutation, including a first
// insert or a delete where no row exists to lock. Extension layer mutations
// never read or write the owner-controlled legacy switch.
func (d *DB) pushGradingWrite(ctx context.Context, write func(pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "push_grading:hub_settings"); err != nil {
			return err
		}
		return write(tx)
	})
}

// PutPushGradingBase stores the owner's complete base configuration. Only an
// explicit true or false enabled value updates the legacy switch, in the same
// transaction. Omitted or null enabled preserves the existing switch, and layer
// presence never contributes to this owner-controlled mirror.
func (d *DB) PutPushGradingBase(ctx context.Context, baseKey, _, mirrorKey string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var base struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.Unmarshal(raw, &base); err != nil {
		return err
	}
	return d.pushGradingWrite(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO `+d.settingsTable()+` (key, value, updated_at) VALUES ($1, $2::jsonb, now())
			ON CONFLICT (key) DO UPDATE SET value = $2::jsonb, updated_at = now()`, baseKey, raw); err != nil {
			return err
		}
		if base.Enabled == nil {
			return nil
		}
		_, err = tx.Exec(ctx, `INSERT INTO `+d.settingsTable()+` (key, value, updated_at) VALUES ($1, $2::jsonb, now())
			ON CONFLICT (key) DO UPDATE SET value = $2::jsonb, updated_at = now()`, mirrorKey, *base.Enabled)
		return err
	})
}

// SetPushGradingSwitch atomically updates the owner's legacy switch and, when a
// versioned base configuration row already exists, updates its enabled field in
// the same advisory-locked transaction so both settings never diverge.
func (d *DB) SetPushGradingSwitch(ctx context.Context, baseKey, mirrorKey string, enabled bool) error {
	return d.pushGradingWrite(ctx, func(tx pgx.Tx) error {
		var existing []byte
		err := tx.QueryRow(ctx, `SELECT value FROM `+d.settingsTable()+` WHERE key = $1`, baseKey).Scan(&existing)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil {
			if _, err := tx.Exec(ctx, `UPDATE `+d.settingsTable()+`
				SET value = jsonb_set(value, '{enabled}', to_jsonb($2::boolean), true), updated_at = now()
				WHERE key = $1`, baseKey, enabled); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `INSERT INTO `+d.settingsTable()+` (key, value, updated_at) VALUES ($1, $2::jsonb, now())
			ON CONFLICT (key) DO UPDATE SET value = $2::jsonb, updated_at = now()`, mirrorKey, enabled)
		return err
	})
}

// PutPushGradingLayer claims or renews only the extension layer. A different
// owner changes no row. Neither success nor conflict touches the legacy switch.
func (d *DB) PutPushGradingLayer(ctx context.Context, _, layerKey, _, manager, source string, value any, out any) (bool, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return false, err
	}
	err = d.pushGradingWrite(ctx, func(tx pgx.Tx) error {
		var stored []byte
		err := tx.QueryRow(ctx, `INSERT INTO `+d.settingsTable()+` AS s (key, value, updated_at)
			VALUES ($1, $2::jsonb, now())
			ON CONFLICT (key) DO UPDATE SET
				value = jsonb_set($2::jsonb, '{revision}', to_jsonb(COALESCE((s.value->>'revision')::bigint, 0) + 1), false),
				updated_at = now()
			WHERE s.value->>'manager' = $3 AND s.value->>'source' = $4
			RETURNING value`, layerKey, raw, manager, source).Scan(&stored)
		if errors.Is(err, pgx.ErrNoRows) {
			return errPushGradingOwnerConflict
		}
		if err != nil {
			return err
		}
		if out != nil {
			return json.Unmarshal(stored, out)
		}
		return nil
	})
	if errors.Is(err, errPushGradingOwnerConflict) {
		return false, nil
	}
	return err == nil, err
}

// DeletePushGradingLayer deletes only the still-matching owner/revision. It
// never changes the legacy switch, including when no base row exists.
func (d *DB) DeletePushGradingLayer(ctx context.Context, _, layerKey, _, manager, source string, revision int64) (bool, error) {
	deleted := false
	err := d.pushGradingWrite(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM `+d.settingsTable()+`
			WHERE key = $1 AND value->>'manager' = $2 AND value->>'source' = $3
			AND ($4::bigint = 0 OR COALESCE((value->>'revision')::bigint, 0) = $4)`, layerKey, manager, source, revision)
		if err != nil {
			return err
		}
		deleted = tag.RowsAffected() == 1
		return nil
	})
	if err != nil {
		return false, err
	}
	return deleted, nil
}

// ClearPushGradingLayer deletes the singleton extension layer when it matches
// the optional manager and source filters. It runs under the serialized push
// grading write lock and never touches the owner-controlled legacy switch.
func (d *DB) ClearPushGradingLayer(ctx context.Context, layerKey, manager, source string) (bool, error) {
	deleted := false
	err := d.pushGradingWrite(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM `+d.settingsTable()+`
			WHERE key = $1
			AND ($2 = '' OR value->>'manager' = $2)
			AND ($3 = '' OR value->>'source' = $3)`, layerKey, manager, source)
		if err != nil {
			return err
		}
		deleted = tag.RowsAffected() == 1
		return nil
	})
	if err != nil {
		return false, err
	}
	return deleted, nil
}

// PrunePushGradingLayer deletes the singleton extension layer when it belongs
// to manager and its source is no longer in keepSources (after extension
// disable, uninstall, or push.grading scope removal). It never touches another
// manager's layer or the owner-controlled legacy switch.
func (d *DB) PrunePushGradingLayer(ctx context.Context, layerKey, manager string, keepSources []string) (bool, error) {
	if strings.TrimSpace(manager) == "" {
		return false, errors.New("push grading layer prune requires a manager")
	}
	if keepSources == nil {
		keepSources = []string{}
	}
	deleted := false
	err := d.pushGradingWrite(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM `+d.settingsTable()+`
			WHERE key = $1
			AND value->>'manager' = $2
			AND NOT (COALESCE(value->>'source', '') = ANY($3::text[]))`, layerKey, manager, keepSources)
		if err != nil {
			return err
		}
		deleted = tag.RowsAffected() == 1
		return nil
	})
	if err != nil {
		return false, err
	}
	return deleted, nil
}
