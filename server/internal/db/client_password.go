package db

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

type ClientPassword struct {
	Enabled                          bool
	Revision                         string
	ServerKeys, CredentialID, Record []byte
}

func (d *DB) ClientPassword(ctx context.Context) (*ClientPassword, error) {
	var p ClientPassword
	err := d.pool.QueryRow(ctx, `SELECT enabled, revision, server_keys, credential_id, record FROM client_password WHERE id = 1`).
		Scan(&p.Enabled, &p.Revision, &p.ServerKeys, &p.CredentialID, &p.Record)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &p, err
}

// SaveClientPassword serializes changes, detects stale admin forms and optionally revokes every device atomically.
func (d *DB) SaveClientPassword(ctx context.Context, p ClientPassword, previous string, revoke bool) (bool, []string, error) {
	var saved bool
	var revoked []string
	err := pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('persona-client-password'))`); err != nil {
			return err
		}
		var revision string
		err := tx.QueryRow(ctx, `SELECT revision FROM client_password WHERE id = 1 FOR UPDATE`).Scan(&revision)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if revision != previous {
			return nil
		}
		if _, err := tx.Exec(ctx, `INSERT INTO client_password (id, enabled, revision, server_keys, credential_id, record)
			VALUES (1, $1, $2, $3, $4, $5) ON CONFLICT (id) DO UPDATE SET enabled = $1, revision = $2,
			server_keys = $3, credential_id = $4, record = $5`, p.Enabled, p.Revision, p.ServerKeys, p.CredentialID, p.Record); err != nil {
			return err
		}
		if revoke {
			rows, err := tx.Query(ctx, `UPDATE device_tokens SET revoked_at = now() WHERE revoked_at IS NULL RETURNING id`)
			if err != nil {
				return err
			}
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return err
				}
				revoked = append(revoked, id)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
		}
		saved = true
		return nil
	})
	return saved && err == nil, revoked, err
}

// IssuePasswordDevice locks the registration until commit. A password change / disable cannot race token issuance.
func (d *DB) IssuePasswordDevice(ctx context.Context, revision string, device DeviceToken, hash string) (bool, error) {
	var issued bool
	err := pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		var enabled bool
		var current string
		err := tx.QueryRow(ctx, `SELECT enabled, revision FROM client_password WHERE id = 1 FOR SHARE`).Scan(&enabled, &current)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if !enabled || current != revision {
			return nil
		}
		_, err = tx.Exec(ctx, `INSERT INTO device_tokens (id, name, kind, role, token_hash) VALUES ($1, $2, $3, $4, $5)`,
			device.ID, device.Name, device.Kind, device.Role, hash)
		issued = err == nil
		return err
	})
	return issued && err == nil, err
}
