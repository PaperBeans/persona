package db

import (
	"context"
	"time"
)

// FeedPage keeps the initial change window while advancing through original
// conversation positions. Updating an older card does not restart pagination.
func (d *DB) FeedPage(ctx context.Context, conversation string, since, until, afterAt time.Time, afterID string, limit int) ([]FeedRow, error) {
	q := `(SELECT id, conversation, at, kind, ref, body FROM assistant_feed
		WHERE conversation = $1 AND at > $2 AND at <= $3
		AND (at, id) > ($4, $5) ORDER BY at, id LIMIT $6)
		UNION ALL
		(SELECT id, conversation, at, kind, ref, body FROM assistant_feed
		WHERE conversation = $1 AND kind = 'message' AND at <= $2
		AND sent_at > $2 AND sent_at <= $3 AND (at, id) > ($4, $5)
		ORDER BY at, id LIMIT $6)
		UNION ALL
		(SELECT id, conversation, at, kind, ref, body FROM assistant_feed
		WHERE conversation = $1 AND kind = 'message' AND at <= $2
		AND updated_at > $2 AND updated_at <= $3 AND (at, id) > ($4, $5)
		AND (sent_at IS NULL OR sent_at <= $2 OR sent_at > $3)
		ORDER BY at, id LIMIT $6)
		ORDER BY at, id LIMIT $6`
	rows, err := d.pool.Query(ctx, q, conversation, since, until, afterAt, afterID, limit)
	if err != nil {
		return nil, err
	}
	return scanFeed(rows)
}
