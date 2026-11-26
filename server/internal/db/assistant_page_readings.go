package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// Watch page readings (Dashboards phase 2, #13020): each reading is one answer to the watch's question, stored in
// assistant_page_readings. Each page keeps its latest AssistantPageReadingsKept (30) readings, trimmed in the
// writing transaction. Adding a reading counts against the page's and all pages' hourly write windows just like
// UpdateAssistantPage, and is refused on paused, removed and draft pages.

const (
	AssistantPageReadingsKept  = 30
	AssistantPageAlertTextMost = 120
	AssistantPageNoteMost      = 120
)

// AssistantPageReadingRow is one stored reading of a watch page.
type AssistantPageReadingRow struct {
	ID        int64
	PageID    string
	Reading   json.RawMessage
	AlertText string
	Note      string
	ByKind    string
	ByMachine string
	BySession string
	At        time.Time
}

// NewAssistantPageReading is a reading written to a kept watch page (page patch).
type NewAssistantPageReading struct {
	PageID    string
	Reading   json.RawMessage
	AlertText string
	Note      string
	// ByKind is AssistantPageByAgent or AssistantPageByRoutine.
	ByKind    string
	ByMachine string
	BySession string
	At        time.Time
}

const assistantPageReadingColumns = `id, page_id, reading, alert_text, note, by_kind, by_machine, by_session, at`

func scanAssistantPageReading(row pgx.Row) (*AssistantPageReadingRow, error) {
	var r AssistantPageReadingRow
	err := row.Scan(&r.ID, &r.PageID, &r.Reading, &r.AlertText, &r.Note, &r.ByKind, &r.ByMachine, &r.BySession, &r.At)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func collectAssistantPageReadings(rows pgx.Rows, err error) ([]AssistantPageReadingRow, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AssistantPageReadingRow{}
	for rows.Next() {
		r, err := scanAssistantPageReading(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func checkAssistantPageReading(raw json.RawMessage) error {
	if len(raw) > assistantPageViewBytes {
		return fmt.Errorf("an assistant page reading has at most %d bytes", assistantPageViewBytes)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return errors.New("an assistant page reading must be a JSON object")
	}
	return nil
}

// AddAssistantPageReading appends a reading to a kept, unpaused watch page, trimming older readings beyond the latest
// AssistantPageReadingsKept in the same transaction. It counts one write in the page's hourly window
// (ErrAssistantPageRate) and in all pages' hourly window (ErrAssistantPagesRate), refuses removed
// (ErrAssistantPageRemoved), draft (ErrAssistantPageNotKept) and paused (ErrAssistantPagePaused) pages, and returns
// the updated page row (with View) and the reading immediately before this one (nil if this is the first reading).
func (d *DB) AddAssistantPageReading(ctx context.Context, r NewAssistantPageReading) (page *AssistantPageRow, prev *AssistantPageReadingRow, err error) {
	if r.PageID == "" || r.At.IsZero() {
		return nil, nil, errors.New("an assistant page reading needs a page id and a time")
	}
	if r.ByKind != AssistantPageByAgent && r.ByKind != AssistantPageByRoutine {
		return nil, nil, fmt.Errorf("unknown assistant page reading writer %q", r.ByKind)
	}
	if err := checkAssistantPageReading(r.Reading); err != nil {
		return nil, nil, err
	}
	if utf8.RuneCountInString(r.AlertText) > AssistantPageAlertTextMost {
		return nil, nil, fmt.Errorf("an alert text has at most %d characters", AssistantPageAlertTextMost)
	}
	if utf8.RuneCountInString(r.Note) > AssistantPageNoteMost {
		return nil, nil, fmt.Errorf("a reading note has at most %d characters", AssistantPageNoteMost)
	}
	r.At = r.At.UTC().Truncate(time.Microsecond)
	err = d.pagesTx(ctx, false, func(tx pgx.Tx) error {
		p, err := lockAssistantPage(ctx, tx, r.PageID)
		if err != nil {
			return err
		}
		if err := p.kept(); err != nil {
			return err
		}
		if p.Paused {
			return ErrAssistantPagePaused
		}
		start, n := p.WritesWindowStart, p.WritesWindowN
		if !r.At.Before(start.Add(assistantPageWriteWindow)) {
			start, n = r.At, 0
		}
		if n >= AssistantPageWritesPerHour {
			return ErrAssistantPageRate
		}
		if err := d.countPagesWrite(ctx, tx, r.At); err != nil {
			return err
		}
		before, err := scanAssistantPageReading(tx.QueryRow(ctx, `SELECT `+assistantPageReadingColumns+`
			FROM assistant_page_readings WHERE page_id = $1 ORDER BY at DESC, id DESC LIMIT 1`, r.PageID))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO assistant_page_readings (page_id, reading, alert_text, note, by_kind,
			by_machine, by_session, at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			r.PageID, []byte(r.Reading), r.AlertText, r.Note, r.ByKind, r.ByMachine, r.BySession, r.At); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM assistant_page_readings WHERE page_id = $1 AND id NOT IN (
			SELECT id FROM assistant_page_readings WHERE page_id = $1 ORDER BY at DESC, id DESC LIMIT $2)`,
			r.PageID, AssistantPageReadingsKept); err != nil {
			return err
		}
		row, err := scanAssistantPage(tx.QueryRow(ctx, `UPDATE assistant_pages SET writes_window_start = $2,
			writes_window_n = $3, updated_at = $4, content_at = $4 WHERE id = $1 RETURNING `+assistantPageColumns,
			r.PageID, start, n+1, r.At), true)
		if err != nil {
			return err
		}
		page, prev = row, before
		return nil
	})
	return page, prev, err
}

// AssistantPageReadings returns a page's latest limit readings, newest first (at most AssistantPageReadingsKept;
// defaults to AssistantPageReadingsKept when limit is outside 1..AssistantPageReadingsKept).
func (d *DB) AssistantPageReadings(ctx context.Context, pageID string, limit int) ([]AssistantPageReadingRow, error) {
	if limit < 1 || limit > AssistantPageReadingsKept {
		limit = AssistantPageReadingsKept
	}
	return collectAssistantPageReadings(d.pool.Query(ctx, `SELECT `+assistantPageReadingColumns+`
		FROM assistant_page_readings WHERE page_id = $1 ORDER BY at DESC, id DESC LIMIT $2`, pageID, limit))
}

// AssistantPagesReadings returns up to limit latest readings (newest first) for each page in pageIDs in a single query.
func (d *DB) AssistantPagesReadings(ctx context.Context, pageIDs []string, limit int) (map[string][]AssistantPageReadingRow, error) {
	out := map[string][]AssistantPageReadingRow{}
	if len(pageIDs) == 0 {
		return out, nil
	}
	if limit < 1 || limit > AssistantPageReadingsKept {
		limit = AssistantPageReadingsKept
	}
	rows, err := d.pool.Query(ctx, `SELECT `+assistantPageReadingColumns+` FROM (
		SELECT `+assistantPageReadingColumns+`, row_number() OVER (PARTITION BY page_id ORDER BY at DESC, id DESC) AS rn
		FROM assistant_page_readings WHERE page_id = ANY($1)
	) ranked WHERE rn <= $2 ORDER BY page_id, at DESC, id DESC`, pageIDs, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		r, err := scanAssistantPageReading(rows)
		if err != nil {
			return nil, err
		}
		out[r.PageID] = append(out[r.PageID], *r)
	}
	return out, rows.Err()
}
