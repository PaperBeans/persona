package db

import (
	"context"
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
	At         time.Time `json:"at"`
	Level      string    `json:"level"`
	RelatedKey string    `json:"related_key,omitempty"`
	Manager    string    `json:"manager"`
}

// AddPushSent appends entries in their order, in one statement however many there are.
func (d *DB) AddPushSent(ctx context.Context, entries []PushSent) error {
	return addPushSent(ctx, d.pool, entries)
}

func addPushSent(ctx context.Context, q execer, entries []PushSent) error {
	if len(entries) == 0 {
		return nil
	}
	at := make([]time.Time, len(entries))
	level := make([]string, len(entries))
	key := make([]string, len(entries))
	manager := make([]string, len(entries))
	for i, e := range entries {
		at[i], level[i], key[i], manager[i] = e.At, e.Level, e.RelatedKey, e.Manager
	}
	_, err := q.Exec(ctx, `INSERT INTO hub_push_sent (at, level, related_key, manager)
		SELECT at, level, related_key, manager
		FROM unnest($1::timestamptz[], $2::text[], $3::text[], $4::text[]) WITH ORDINALITY AS e(at, level, related_key, manager, n)
		ORDER BY n`, at, level, key, manager)
	return err
}

// PushSentAfter returns the newest limit rows later than after, oldest first (the order they were added in).
func (d *DB) PushSentAfter(ctx context.Context, after time.Time, limit int) ([]PushSent, error) {
	rows, err := d.pool.Query(ctx, `SELECT at, level, related_key, manager FROM (
			SELECT id, at, level, related_key, manager FROM hub_push_sent WHERE at > $1 ORDER BY id DESC LIMIT $2
		) newest ORDER BY id`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PushSent{}
	for rows.Next() {
		var e PushSent
		if err := rows.Scan(&e.At, &e.Level, &e.RelatedKey, &e.Manager); err != nil {
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
// the table, after its rows, and deletes the row, in one transaction; it returns how many entries moved. No row is
// none. It runs at every start of the hub: once after the upgrade, and again only if a build from before was switched
// back to and wrote the row meanwhile, whose entries then follow the ones already here.
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
		moved = len(entries)
		return addPushSent(ctx, tx, entries)
	})
	if err != nil {
		return 0, err
	}
	return moved, nil
}
