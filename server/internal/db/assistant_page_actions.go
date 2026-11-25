package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// The owner's taps on a page (Dashboards, #15017). Each tap is a row, since a page's buttons are tapped again and again
// ("done today"), and the row is the tap's delivery outbox to the owning conversation, as assistant_view_actions is a
// card's. The row also times the owner's wait: at (the tap), delivered_at (queue time), turn_started_at and
// turn_ended_at (the turn that answered).

// Tap delivery states.
const (
	AssistantPageTapPending = "pending"
	AssistantPageTapSent    = "sent"
	AssistantPageTapFailed  = "failed"
)

// AssistantPageActionRow is one tap.
type AssistantPageActionRow struct {
	ID     string
	PageID string
	// Dedupe is the tap's hash (page, version, action, values, device): a second one within AssistantPageTapDedupe is
	// the same tap.
	Dedupe      string
	Action      string
	SubPage     string
	Values      map[string]string
	PageVersion int64
	// Text is the message the owning conversation gets, its <persona-action> tag included.
	Text      string
	MessageID string
	// TargetMachine and TargetConversation are where it went; Redirected when that was the main conversation in place
	// of a conversation that is gone.
	TargetMachine      string
	TargetConversation string
	Redirected         bool
	Status             string
	Revision           int
	LeaseUntil         *time.Time
	Error              string
	Device             string
	At                 time.Time
	UpdatedAt          time.Time
	DeliveredAt        *time.Time
	TurnStartedAt      *time.Time
	TurnEndedAt        *time.Time
}

const pageActionColumns = `id, page_id, dedupe, action, sub_page, values, page_version, text, message_id, target_machine,
	target_conversation, redirected, status, revision, claimed_until, error, device, at, updated_at, delivered_at,
	turn_started_at, turn_ended_at`

func scanPageAction(row pgx.Row) (*AssistantPageActionRow, error) {
	var a AssistantPageActionRow
	err := row.Scan(&a.ID, &a.PageID, &a.Dedupe, &a.Action, &a.SubPage, &a.Values, &a.PageVersion, &a.Text, &a.MessageID,
		&a.TargetMachine, &a.TargetConversation, &a.Redirected, &a.Status, &a.Revision, &a.LeaseUntil, &a.Error, &a.Device,
		&a.At, &a.UpdatedAt, &a.DeliveredAt, &a.TurnStartedAt, &a.TurnEndedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func collectPageActions(rows pgx.Rows, err error) ([]AssistantPageActionRow, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AssistantPageActionRow{}
	for rows.Next() {
		a, err := scanPageAction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// assistantPageTapKey is the hash a repeated tap shares: the page, its version, the action, the values and the device.
func assistantPageTapKey(a AssistantPageActionRow) string {
	raw, _ := json.Marshal([]any{a.PageID, a.PageVersion, a.Action, a.Values, a.Device}) // map keys are sorted
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// BeginAssistantPageAction records a tap on a kept page, pending delivery, under the page's row lock. A tap with the
// same page, version, action, values and device within AssistantPageTapDedupe of a.At is the one given back, with
// created false; else more than AssistantPageTapsPerMinute taps on the page in the minute before a.At is
// ErrAssistantPageTapRate.
func (d *DB) BeginAssistantPageAction(ctx context.Context, a AssistantPageActionRow) (*AssistantPageActionRow, bool, error) {
	if !assistantPageActionID.MatchString(a.ID) || a.PageID == "" || a.Action == "" || a.PageVersion < 1 || a.Text == "" ||
		a.MessageID == "" || a.At.IsZero() {
		return nil, false, errors.New("a page tap needs its id, page, version, action, message and time")
	}
	if a.Values == nil {
		a.Values = map[string]string{}
	}
	a.At = a.At.UTC().Truncate(time.Microsecond)
	a.Dedupe = assistantPageTapKey(a)
	var out *AssistantPageActionRow
	created := false
	err := pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		p, err := lockAssistantPage(ctx, tx, a.PageID)
		if err != nil {
			return err
		}
		if err := p.kept(); err != nil {
			return err
		}
		old, err := scanPageAction(tx.QueryRow(ctx, `SELECT `+pageActionColumns+` FROM assistant_page_actions
			WHERE page_id = $1 AND dedupe = $2 AND at > $3 ORDER BY at DESC LIMIT 1`, a.PageID, a.Dedupe, a.At.Add(-AssistantPageTapDedupe)))
		if err != nil || old != nil {
			out = old
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM assistant_page_actions WHERE page_id = $1 AND at > $2`,
			a.PageID, a.At.Add(-time.Minute)).Scan(&n); err != nil {
			return err
		}
		if n >= AssistantPageTapsPerMinute {
			return ErrAssistantPageTapRate
		}
		values, err := json.Marshal(a.Values)
		if err != nil {
			return err
		}
		out, err = scanPageAction(tx.QueryRow(ctx, `INSERT INTO assistant_page_actions (id, page_id, dedupe, action, sub_page,
			values, page_version, text, message_id, target_machine, target_conversation, redirected, status, device, at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, 'pending', $13, $14, $14) RETURNING `+pageActionColumns,
			a.ID, a.PageID, a.Dedupe, a.Action, a.SubPage, values, a.PageVersion, a.Text, a.MessageID, a.TargetMachine,
			a.TargetConversation, a.Redirected, a.Device, a.At))
		created = err == nil
		return err
	})
	return out, created, err
}

// AssistantPageAction is one tap, nil when there is none.
func (d *DB) AssistantPageAction(ctx context.Context, id string) (*AssistantPageActionRow, error) {
	return scanPageAction(d.pool.QueryRow(ctx, `SELECT `+pageActionColumns+` FROM assistant_page_actions WHERE id = $1`, id))
}

// AssistantPageActions are a page's latest taps, the latest first (at most limit, 20 when out of 1 to 200).
func (d *DB) AssistantPageActions(ctx context.Context, page string, limit int) ([]AssistantPageActionRow, error) {
	if limit < 1 || limit > AssistantPageTapsKept {
		limit = 20
	}
	return collectPageActions(d.pool.Query(ctx, `SELECT `+pageActionColumns+` FROM assistant_page_actions WHERE page_id = $1
		ORDER BY at DESC, id DESC LIMIT $2`, page, limit))
}

// PendingAssistantPageActions are taps waiting to be delivered and not leased, the longest waiting first.
func (d *DB) PendingAssistantPageActions(ctx context.Context, limit int) ([]AssistantPageActionRow, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	return collectPageActions(d.pool.Query(ctx, `SELECT `+pageActionColumns+` FROM assistant_page_actions WHERE status = 'pending'
		AND (claimed_until IS NULL OR claimed_until < now()) ORDER BY updated_at, id LIMIT $1`, limit))
}

// ClaimAssistantPageDelivery leases a pending tap until until: no other process delivers it meanwhile. It gives nil when
// the tap is not pending or another lease holds it.
func (d *DB) ClaimAssistantPageDelivery(ctx context.Context, id string, until time.Time) (*AssistantPageActionRow, error) {
	until = until.UTC().Truncate(time.Microsecond)
	return scanPageAction(d.pool.QueryRow(ctx, `UPDATE assistant_page_actions SET claimed_until = $2, updated_at = now()
		WHERE id = $1 AND status = 'pending' AND (claimed_until IS NULL OR claimed_until < now()) RETURNING `+pageActionColumns, id, until))
}

// AssistantPageDelivery settles a leased tap.
type AssistantPageDelivery struct {
	ID string
	// Lease is the LeaseUntil its claim gave.
	Lease time.Time
	// Status is sent (delivered at At), failed (Error says why) or pending (try again later).
	Status             string
	Error              string
	TargetMachine      string
	TargetConversation string
	Redirected         bool
	At                 time.Time
}

// SettleAssistantPageDelivery ends a lease with the delivery's outcome and where it went. A lease that changed meanwhile
// (another process took the tap over) is an error and changes nothing.
func (d *DB) SettleAssistantPageDelivery(ctx context.Context, s AssistantPageDelivery) (*AssistantPageActionRow, error) {
	if s.Lease.IsZero() || s.Status != AssistantPageTapSent && s.Status != AssistantPageTapFailed && s.Status != AssistantPageTapPending ||
		s.Status == AssistantPageTapSent && s.At.IsZero() {
		return nil, errors.New("invalid page tap delivery status, lease or time")
	}
	var delivered *time.Time
	if s.Status == AssistantPageTapSent {
		at := s.At.UTC().Truncate(time.Microsecond)
		delivered = &at
	}
	a, err := scanPageAction(d.pool.QueryRow(ctx, `UPDATE assistant_page_actions SET status = $3, error = $4,
		target_machine = $5, target_conversation = $6, redirected = $7, delivered_at = $8, revision = revision + 1,
		claimed_until = NULL, updated_at = now()
		WHERE id = $1 AND status = 'pending' AND claimed_until = $2 RETURNING `+pageActionColumns,
		s.ID, s.Lease.UTC().Truncate(time.Microsecond), s.Status, s.Error, s.TargetMachine, s.TargetConversation, s.Redirected, delivered))
	if err == nil && a == nil {
		return nil, errors.New("page tap delivery lease changed")
	}
	return a, err
}

// RetryAssistantPageAction puts a failed tap back in the outbox (the owner's Retry). A tap that is not failed is given
// back as it is.
func (d *DB) RetryAssistantPageAction(ctx context.Context, id string) (*AssistantPageActionRow, error) {
	a, err := scanPageAction(d.pool.QueryRow(ctx, `UPDATE assistant_page_actions SET status = 'pending', error = '',
		revision = revision + 1, claimed_until = NULL, updated_at = now() WHERE id = $1 AND status = 'failed'
		RETURNING `+pageActionColumns, id))
	if err != nil || a != nil {
		return a, err
	}
	return d.AssistantPageAction(ctx, id)
}

// MarkAssistantPageTurnStarted records that the conversation started a turn at at: the taps delivered to it by then
// whose turn had not started are being handled. It gives back the taps it marked.
func (d *DB) MarkAssistantPageTurnStarted(ctx context.Context, machine, conversation string, at time.Time) ([]AssistantPageActionRow, error) {
	return collectPageActions(d.pool.Query(ctx, `UPDATE assistant_page_actions SET turn_started_at = $3, updated_at = now()
		WHERE target_machine = $1 AND target_conversation = $2 AND status = 'sent' AND turn_ended_at IS NULL
			AND turn_started_at IS NULL AND delivered_at <= $3
		RETURNING `+pageActionColumns, machine, conversation, at.UTC().Truncate(time.Microsecond)))
}

// MarkAssistantPageTurnEnded records that the conversation's turn ended at at (it went idle): the taps whose turn had
// started are answered. It gives back the taps it marked.
func (d *DB) MarkAssistantPageTurnEnded(ctx context.Context, machine, conversation string, at time.Time) ([]AssistantPageActionRow, error) {
	return collectPageActions(d.pool.Query(ctx, `UPDATE assistant_page_actions SET turn_ended_at = $3, updated_at = now()
		WHERE target_machine = $1 AND target_conversation = $2 AND status = 'sent' AND turn_ended_at IS NULL
			AND turn_started_at IS NOT NULL
		RETURNING `+pageActionColumns, machine, conversation, at.UTC().Truncate(time.Microsecond)))
}

// TrimAssistantPageActions deletes the taps older than AssistantPageTapLife at now and a page's taps past its latest
// AssistantPageTapsKept. A tap still waiting to be delivered is kept.
func (d *DB) TrimAssistantPageActions(ctx context.Context, now time.Time) (int64, error) {
	tag, err := d.pool.Exec(ctx, `DELETE FROM assistant_page_actions WHERE status <> 'pending' AND (at <= $1 OR id IN (
		SELECT id FROM (SELECT id, row_number() OVER (PARTITION BY page_id ORDER BY at DESC, id DESC) AS n
			FROM assistant_page_actions) ranked WHERE n > $2))`, now.Add(-AssistantPageTapLife), AssistantPageTapsKept)
	return tag.RowsAffected(), err
}
