package notify

// queue.go, FW1-02 sub item 3: the outbox's own read of the rows the books
// are waiting on. The outbox table is this package's; every other module
// reads it through here (the tablegate census), so the GL posting queue's
// waiting and dead lettered rows come back through the same seam the
// dispatcher claims through.

import (
	"context"
	"fmt"
	"time"
)

// QueueRow is one outbox row still undelivered: waiting (PENDING) or dead
// lettered (DEAD). Document is the payload's own naming of the money document
// it carries (invoice_id, payment_id or order_id), so a reader can say which
// invoice or payment a stuck journal belongs to without parsing the payload.
type QueueRow struct {
	EventID    string
	Topic      string
	Document   string
	Status     string
	Attempts   int
	LastError  string
	OccurredAt time.Time
}

// WaitingOrDead lists up to limit undelivered rows for the named topics,
// oldest first. A nil pool (scaffold/offline mode) answers an empty list.
func (s *PgOutboxStore) WaitingOrDead(ctx context.Context, topics []string, limit int) ([]QueueRow, error) {
	if s.pool == nil || len(topics) == 0 || limit <= 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
SELECT event_id::text, topic,
       COALESCE(payload->>'invoice_id', payload->>'payment_id', payload->>'order_id', ''),
       status, attempts, last_error, occurred_at
  FROM events_outbox
 WHERE status IN ('PENDING','DEAD') AND topic = ANY($1::text[])
 ORDER BY occurred_at
 LIMIT $2`, topics, limit)
	if err != nil {
		return nil, fmt.Errorf("notify: queue rows: %w", err)
	}
	defer rows.Close()
	out := make([]QueueRow, 0, limit)
	for rows.Next() {
		var r QueueRow
		if err := rows.Scan(&r.EventID, &r.Topic, &r.Document, &r.Status, &r.Attempts, &r.LastError, &r.OccurredAt); err != nil {
			return nil, fmt.Errorf("notify: queue row: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// WaitingOrDeadCounts returns how many rows for the named topics are waiting
// and how many are dead lettered. A nil pool answers zeroes.
func (s *PgOutboxStore) WaitingOrDeadCounts(ctx context.Context, topics []string) (waiting, dead int, err error) {
	if s.pool == nil || len(topics) == 0 {
		return 0, 0, nil
	}
	if err := s.pool.QueryRow(ctx, `
SELECT COUNT(*) FILTER (WHERE status = 'PENDING'), COUNT(*) FILTER (WHERE status = 'DEAD')
  FROM events_outbox WHERE topic = ANY($1::text[])`, topics).Scan(&waiting, &dead); err != nil {
		return 0, 0, fmt.Errorf("notify: queue counts: %w", err)
	}
	return waiting, dead, nil
}
