package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// PushSent is one alert push the hub delivered (hub/lnmode.go): the record behind the daily push counts. Rows are only
// appended and swept (#8257); the JSON names are those of the hub.push_sent setting it replaces, which
// AdoptPushSentSetting reads.
type PushSent struct {
	// ID names the logical push, chosen once before its first write and kept across retries: the table's UNIQUE
	// push_id, so writing the same push again adds nothing. Not in the old setting, whose entries get one derived from
	// their content (settingPushSentIDs).
	ID         string    `json:"-"`
	At         time.Time `json:"at"`
	Level      string    `json:"level"`
	RelatedKey string    `json:"related_key,omitempty"`
	Manager    string    `json:"manager"`
}

// AddPushSent appends entries in their order, in one statement however many there are. An entry whose ID is already
// in the table is skipped, so a batch retried after a write that landed but reported an error (a timeout, a dropped
// connection) is not counted twice. Every entry needs an ID.
func (d *DB) AddPushSent(ctx context.Context, entries []PushSent) error {
	_, err := addPushSent(ctx, d.pool, entries)
	return err
}

// addPushSent returns how many rows it added: the entries whose ID was not there yet.
func addPushSent(ctx context.Context, q execer, entries []PushSent) (int64, error) {
	if len(entries) == 0 {
		return 0, nil
	}
	id := make([]string, len(entries))
	at := make([]time.Time, len(entries))
	level := make([]string, len(entries))
	key := make([]string, len(entries))
	manager := make([]string, len(entries))
	for i, e := range entries {
		if e.ID == "" {
			return 0, fmt.Errorf("push sent entry %d has no id", i)
		}
		id[i], at[i], level[i], key[i], manager[i] = e.ID, e.At, e.Level, e.RelatedKey, e.Manager
	}
	tag, err := q.Exec(ctx, `INSERT INTO hub_push_sent (push_id, at, level, related_key, manager)
		SELECT push_id, at, level, related_key, manager
		FROM unnest($1::text[], $2::timestamptz[], $3::text[], $4::text[], $5::text[])
			WITH ORDINALITY AS e(push_id, at, level, related_key, manager, n)
		ORDER BY n
		ON CONFLICT (push_id) DO NOTHING`, id, at, level, key, manager)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// settingPushSentIDs gives the old setting's entries, which have no ID, one derived from what they hold: a hash of the
// fields plus which occurrence of those fields it is in the list, so an entry imported twice is the same row and two
// equal entries in one list stay two.
func settingPushSentIDs(entries []PushSent) {
	seen := map[string]int{}
	for i := range entries {
		e := &entries[i]
		sum := sha256.Sum256([]byte(e.At.UTC().Format(time.RFC3339Nano) + "\x00" + e.Level + "\x00" + e.RelatedKey + "\x00" + e.Manager))
		h := hex.EncodeToString(sum[:16])
		e.ID = fmt.Sprintf("setting:%s:%d", h, seen[h])
		seen[h]++
	}
}

// PushSentAfter returns the newest limit rows later than after, oldest first (the order they were added in).
func (d *DB) PushSentAfter(ctx context.Context, after time.Time, limit int) ([]PushSent, error) {
	rows, err := d.pool.Query(ctx, `SELECT push_id, at, level, related_key, manager FROM (
			SELECT id, push_id, at, level, related_key, manager FROM hub_push_sent WHERE at > $1 ORDER BY id DESC LIMIT $2
		) newest ORDER BY id`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PushSent{}
	for rows.Next() {
		var e PushSent
		if err := rows.Scan(&e.ID, &e.At, &e.Level, &e.RelatedKey, &e.Manager); err != nil {
			return nil, err
		}
		e.At = e.At.UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}

// PrunePushSent deletes the rows at or before before, and all but the newest keep; it returns how many went.
func (d *DB) PrunePushSent(ctx context.Context, before time.Time, keep int) (int64, error) {
	tag, err := d.pool.Exec(ctx, `DELETE FROM hub_push_sent WHERE at <= $1
		OR id <= (SELECT id FROM hub_push_sent ORDER BY id DESC OFFSET $2 LIMIT 1)`, before, keep)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// AdoptPushSentSetting moves the entries of the settings row key (the whole-list record before hub_push_sent) into
// the table, after its rows, and deletes the row, in one transaction; it returns how many rows it added. No row is
// none. It runs at every start of the hub: once after the upgrade, and again only if a build from before was switched
// back to and wrote the row meanwhile, whose entries then follow the ones already here. Each entry's ID comes from its
// content (settingPushSentIDs), so an entry imported before is not added again.
func (d *DB) AdoptPushSentSetting(ctx context.Context, key string) (int, error) {
	moved := 0
	err := pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		var raw []byte
		err := tx.QueryRow(ctx, `DELETE FROM `+d.settingsTable()+` WHERE key = $1 RETURNING value`, key).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var entries []PushSent
		if err := json.Unmarshal(raw, &entries); err != nil {
			return fmt.Errorf("adopt %s: %w", key, err)
		}
		settingPushSentIDs(entries)
		n, err := addPushSent(ctx, tx, entries)
		moved = int(n)
		return err
	})
	if err != nil {
		return 0, err
	}
	return moved, nil
}
