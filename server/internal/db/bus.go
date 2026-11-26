package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// BusIdentity is a service that may connect to the hub's bus (migrations 0311 and 0419, hub/bustokens.go). For a
// manager it is its entry in the roster (hub/managers.go): where its last handshake said it runs, and when it was
// last connected.
type BusIdentity struct {
	ID         string
	Role       string // manager (the only role so far)
	Name       string
	CreatedAt  time.Time // when its current token was made
	Host       string
	MachineUID string
	OS         string
	Arch       string
	Deploy     string // child (the hub started it), service, docker, external
	Version    string
	LastSeen   *time.Time // when it was last connected (its handshake, or the moment it dropped); nil: never
}

const busIdentityColumns = `id, role, name, created_at, host, machine_uid, os, arch, deploy, version, last_seen`

func scanBusIdentity(r pgx.Row) (BusIdentity, error) {
	var b BusIdentity
	err := r.Scan(&b.ID, &b.Role, &b.Name, &b.CreatedAt, &b.Host, &b.MachineUID, &b.OS, &b.Arch, &b.Deploy, &b.Version, &b.LastSeen)
	return b, err
}

// SaveBusIdentity adds an identity, or gives an existing one a new role, name and token (the old token stops working).
// Where it runs and when it was last seen stay.
func (d *DB) SaveBusIdentity(ctx context.Context, b BusIdentity, tokenHash string) error {
	_, err := d.pool.Exec(ctx, `INSERT INTO bus_identities (id, role, name, token_hash) VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO UPDATE SET role = $2, name = $3, token_hash = $4, created_at = now()`, b.ID, b.Role, b.Name, tokenHash)
	return err
}

// BusIdentityByHash is the identity whose token hashes to hash; nil when there is none.
func (d *DB) BusIdentityByHash(ctx context.Context, hash string) (*BusIdentity, error) {
	return d.oneBusIdentity(ctx, `SELECT `+busIdentityColumns+` FROM bus_identities WHERE token_hash = $1`, hash)
}

// BusIdentityByID is identity id; nil when there is none.
func (d *DB) BusIdentityByID(ctx context.Context, id string) (*BusIdentity, error) {
	return d.oneBusIdentity(ctx, `SELECT `+busIdentityColumns+` FROM bus_identities WHERE id = $1`, id)
}

func (d *DB) oneBusIdentity(ctx context.Context, query string, arg string) (*BusIdentity, error) {
	b, err := scanBusIdentity(d.pool.QueryRow(ctx, query, arg))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// BusIdentities lists every identity, by id.
func (d *DB) BusIdentities(ctx context.Context) ([]BusIdentity, error) {
	rows, err := d.pool.Query(ctx, `SELECT `+busIdentityColumns+` FROM bus_identities ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (BusIdentity, error) { return scanBusIdentity(r) })
}

// SeeBusIdentity records that identity id is connected now. With where set (its handshake) it also keeps where it
// runs; without, only the time (it dropped).
func (d *DB) SeeBusIdentity(ctx context.Context, id string, where *BusIdentity) error {
	if where == nil {
		_, err := d.pool.Exec(ctx, `UPDATE bus_identities SET last_seen = now() WHERE id = $1`, id)
		return err
	}
	_, err := d.pool.Exec(ctx, `UPDATE bus_identities SET host = $2, machine_uid = $3, os = $4, arch = $5, deploy = $6, version = $7,
		last_seen = now() WHERE id = $1`, id, where.Host, where.MachineUID, where.OS, where.Arch, where.Deploy, where.Version)
	return err
}

// RenameBusIdentity gives identity id a new name; false when there is none.
func (d *DB) RenameBusIdentity(ctx context.Context, id, name string) (bool, error) {
	tag, err := d.pool.Exec(ctx, `UPDATE bus_identities SET name = $2 WHERE id = $1`, id, name)
	return err == nil && tag.RowsAffected() > 0, err
}

// DeleteBusIdentity removes identity id, and its install codes; it reports whether there was one. One transaction, so
// the codes never go without the identity, in two statements, codes first: a join in flight holds its code's row, so
// the first waits for it to commit, and the second, with a snapshot taken after that, sees the identity the join
// saved and removes it too. (In one statement the identity would be looked for in the snapshot from before the join
// committed, and the manager it installed would stay.)
func (d *DB) DeleteBusIdentity(ctx context.Context, id string) (bool, error) {
	var ok bool
	err := pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM manager_install_codes WHERE manager_id = $1`, id); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM bus_identities WHERE id = $1`, id)
		ok = err == nil && tag.RowsAffected() > 0
		return err
	})
	return err == nil && ok, err
}

// ManagerInstallCode is a one-time code a new manager trades for its bus token (hub/managers.go).
type ManagerInstallCode struct {
	ManagerID string
	Name      string
	ExpiresAt time.Time
}

// ManagerInstallKept is how long a manager that was made and never installed stays in the roster after its last code
// expired (as installing, its code expired: the app offers a new one); after that it is dropped (docs/assistant/managers.md,
// "Install codes").
const ManagerInstallKept = 7 * 24 * time.Hour

// AddManagerInstallCode keeps the sha256 of a new install code for manager c.ManagerID. What is no longer needed goes
// at the same time: codes used more than a day ago, codes of an installed manager that expired more than a day ago,
// and the codes of a never-installed manager whose last one expired more than ManagerInstallKept ago (it leaves the
// roster with them).
func (d *DB) AddManagerInstallCode(ctx context.Context, hash string, c ManagerInstallCode) error {
	if _, err := d.pool.Exec(ctx, `DELETE FROM manager_install_codes c
		WHERE c.used_at < now() - interval '1 day'
		   OR (c.expires_at < now() - interval '1 day' AND EXISTS (SELECT 1 FROM bus_identities b WHERE b.id = c.manager_id))
		   OR c.expires_at < now() - make_interval(secs => $1)`, ManagerInstallKept.Seconds()); err != nil {
		return err
	}
	// A new code voids the manager's codes not used yet: only the latest one handed out installs it.
	return pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM manager_install_codes WHERE manager_id = $1 AND used_at IS NULL`, c.ManagerID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO manager_install_codes (code_hash, manager_id, name, expires_at)
			VALUES ($1, $2, $3, $4)`, hash, c.ManagerID, c.Name, c.ExpiresAt)
		return err
	})
}

// RedeemManagerInstallCode trades the code that hashes to codeHash for a manager token, once: in one transaction it
// marks the code used and saves its manager's identity with tokenHash (a new one, or a new token for one there is),
// so a code is never used up without a token, and a failed save leaves it good for a retry. The name is the
// identity's when it has one (renamed while the code waited), else the code's. Nil when the code is unknown, expired
// or used already.
func (d *DB) RedeemManagerInstallCode(ctx context.Context, codeHash, tokenHash string) (*ManagerInstallCode, string, error) {
	var c *ManagerInstallCode
	var name string
	err := pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		var used ManagerInstallCode
		err := tx.QueryRow(ctx, `UPDATE manager_install_codes SET used_at = now()
			WHERE code_hash = $1 AND used_at IS NULL AND expires_at > now()
			RETURNING manager_id, name, expires_at`, codeHash).Scan(&used.ManagerID, &used.Name, &used.ExpiresAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		// Its other codes not used yet are used up with it: none of them may take the token from the one installed.
		if _, err := tx.Exec(ctx, `UPDATE manager_install_codes SET used_at = now() WHERE manager_id = $1 AND used_at IS NULL`,
			used.ManagerID); err != nil {
			return err
		}
		name = used.Name
		var had string
		err = tx.QueryRow(ctx, `SELECT name FROM bus_identities WHERE id = $1 FOR UPDATE`, used.ManagerID).Scan(&had)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if had != "" {
			name = had
		}
		if _, err := tx.Exec(ctx, `INSERT INTO bus_identities (id, role, name, token_hash) VALUES ($1, 'manager', $2, $3)
			ON CONFLICT (id) DO UPDATE SET role = 'manager', name = $2, token_hash = $3, created_at = now()`,
			used.ManagerID, name, tokenHash); err != nil {
			return err
		}
		c = &used
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return c, name, nil
}

// ManagerInstallCodeByHash is the code that hashes to hash while it is still good (not used, not expired), without
// using it; nil when there is none.
func (d *DB) ManagerInstallCodeByHash(ctx context.Context, hash string) (*ManagerInstallCode, error) {
	var c ManagerInstallCode
	err := d.pool.QueryRow(ctx, `SELECT manager_id, name, expires_at FROM manager_install_codes
		WHERE code_hash = $1 AND used_at IS NULL AND expires_at > now()`, hash).Scan(&c.ManagerID, &c.Name, &c.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ExpireManagerInstallCode voids the code that hashes to hash without trading it, when it is still good: it expires
// now (the roster shows its manager's code as expired, and the app offers a new one). For a code that crossed the
// internet in the clear (hub/managers.go): whoever saw it on the way can't trade it. It reports whether there was one.
func (d *DB) ExpireManagerInstallCode(ctx context.Context, hash string) (bool, error) {
	tag, err := d.pool.Exec(ctx, `UPDATE manager_install_codes SET expires_at = `+endedNow+`
		WHERE code_hash = $1 AND used_at IS NULL AND expires_at > now()`, hash, time.Now())
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// PendingManagerInstalls are the managers made and not installed yet (a code not used, no identity), by id, each with
// its latest code's expiry: still good, or expired less than ManagerInstallKept ago (it waits for a new code).
func (d *DB) PendingManagerInstalls(ctx context.Context) ([]ManagerInstallCode, error) {
	rows, err := d.pool.Query(ctx, `SELECT DISTINCT ON (c.manager_id) c.manager_id, c.name, c.expires_at
		FROM manager_install_codes c LEFT JOIN bus_identities b ON b.id = c.manager_id
		WHERE b.id IS NULL AND c.used_at IS NULL AND c.expires_at > now() - make_interval(secs => $1)
		ORDER BY c.manager_id, c.expires_at DESC`, ManagerInstallKept.Seconds())
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (ManagerInstallCode, error) {
		var c ManagerInstallCode
		err := r.Scan(&c.ManagerID, &c.Name, &c.ExpiresAt)
		return c, err
	})
}

// DropManagerInstallCodes removes manager id's codes that were not used (a pending install called off).
func (d *DB) DropManagerInstallCodes(ctx context.Context, id string) (int64, error) {
	tag, err := d.pool.Exec(ctx, `DELETE FROM manager_install_codes WHERE manager_id = $1 AND used_at IS NULL`, id)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
