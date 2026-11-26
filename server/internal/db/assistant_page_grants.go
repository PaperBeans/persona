package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Data-only grants on dashboard pages (Dashboards phase 2, #13020): lets a bound routine (or later a to-do agent)
// write readings and patch declared items on a kept page without owning the page. Cascade-deleted with the page.

const (
	AssistantPageGrantRoutine   = "routine"
	AssistantPageGrantTodoAgent = "todo_agent"
	AssistantPageRightData      = "data"
)

// AssistantPageGrantRow is one data-only grant on a dashboard page.
type AssistantPageGrantRow struct {
	PageID      string
	GranteeKind string
	GranteeRef  string
	Rights      string
	GrantedBy   string
	CreatedAt   time.Time
}

const assistantPageGrantColumns = `page_id, grantee_kind, grantee_ref, rights, granted_by, created_at`

func scanAssistantPageGrant(row pgx.Row) (*AssistantPageGrantRow, error) {
	var g AssistantPageGrantRow
	err := row.Scan(&g.PageID, &g.GranteeKind, &g.GranteeRef, &g.Rights, &g.GrantedBy, &g.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &g, nil
}

// GrantAssistantPageData grants data-only rights on a kept page to (GranteeKind, GranteeRef). Re-granting updates
// GrantedBy and CreatedAt idempotently. Refuses draft (ErrAssistantPageNotKept), removed (ErrAssistantPageRemoved) and
// missing (ErrAssistantPageNotFound) pages.
func (d *DB) GrantAssistantPageData(ctx context.Context, g AssistantPageGrantRow) (*AssistantPageGrantRow, error) {
	if g.PageID == "" || g.GranteeRef == "" || g.CreatedAt.IsZero() {
		return nil, errors.New("a page data grant needs a page id, a grantee ref and a time")
	}
	if g.GranteeKind != AssistantPageGrantRoutine && g.GranteeKind != AssistantPageGrantTodoAgent {
		return nil, fmt.Errorf("unknown assistant page grantee kind %q", g.GranteeKind)
	}
	if g.Rights == "" {
		g.Rights = AssistantPageRightData
	}
	if g.Rights != AssistantPageRightData {
		return nil, fmt.Errorf("unknown assistant page grant rights %q", g.Rights)
	}
	g.CreatedAt = g.CreatedAt.UTC().Truncate(time.Microsecond)
	var out *AssistantPageGrantRow
	err := d.pagesTx(ctx, false, func(tx pgx.Tx) error {
		p, err := lockAssistantPage(ctx, tx, g.PageID)
		if err != nil {
			return err
		}
		if err := p.kept(); err != nil {
			return err
		}
		out, err = scanAssistantPageGrant(tx.QueryRow(ctx, `INSERT INTO assistant_page_grants
			(page_id, grantee_kind, grantee_ref, rights, granted_by, created_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (page_id, grantee_kind, grantee_ref) DO UPDATE
				SET granted_by = EXCLUDED.granted_by, created_at = EXCLUDED.created_at
			RETURNING `+assistantPageGrantColumns,
			g.PageID, g.GranteeKind, g.GranteeRef, g.Rights, g.GrantedBy, g.CreatedAt))
		return err
	})
	return out, err
}

// RevokeAssistantPageData removes a data grant from pageID. Returns true if a grant row was deleted.
func (d *DB) RevokeAssistantPageData(ctx context.Context, pageID, granteeKind, granteeRef string) (bool, error) {
	if pageID == "" || granteeKind == "" || granteeRef == "" {
		return false, nil
	}
	tag, err := d.pool.Exec(ctx, `DELETE FROM assistant_page_grants WHERE page_id = $1 AND grantee_kind = $2 AND grantee_ref = $3`,
		pageID, granteeKind, granteeRef)
	return tag.RowsAffected() > 0, err
}

// HasAssistantPageDataGrant reports whether (granteeKind, granteeRef) holds a data grant on pageID.
func (d *DB) HasAssistantPageDataGrant(ctx context.Context, pageID, granteeKind, granteeRef string) (bool, error) {
	if pageID == "" || granteeKind == "" || granteeRef == "" {
		return false, nil
	}
	var exists bool
	err := d.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM assistant_page_grants
		WHERE page_id = $1 AND grantee_kind = $2 AND grantee_ref = $3 AND rights = $4)`,
		pageID, granteeKind, granteeRef, AssistantPageRightData).Scan(&exists)
	return exists, err
}

// AssistantPageGrants returns all grants currently held on pageID, oldest first.
func (d *DB) AssistantPageGrants(ctx context.Context, pageID string) ([]AssistantPageGrantRow, error) {
	rows, err := d.pool.Query(ctx, `SELECT `+assistantPageGrantColumns+` FROM assistant_page_grants
		WHERE page_id = $1 ORDER BY created_at, grantee_kind, grantee_ref`, pageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AssistantPageGrantRow{}
	for rows.Next() {
		g, err := scanAssistantPageGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *g)
	}
	return out, rows.Err()
}
