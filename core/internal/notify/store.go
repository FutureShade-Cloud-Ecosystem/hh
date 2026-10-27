package notify

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgOutboxStore drains events_outbox (migration 0013) over pgx. It tolerates a nil pool
// (scaffold/offline mode), in which case ProcessBatch is a no-op so the server still boots.
type PgOutboxStore struct{ pool *pgxpool.Pool }

// NewPgOutboxStore builds the store; pass a nil pool in scaffold mode.
func NewPgOutboxStore(pool *pgxpool.Pool) *PgOutboxStore { return &PgOutboxStore{pool: pool} }

// ProcessBatch claims and settles up to n PENDING rows, one short transaction each. Each row
// is locked FOR UPDATE SKIP LOCKED, so multiple dispatcher instances (or replicas) divide the
// work without ever delivering the same row twice. settle performs the delivery and the
// finalize UPDATE runs inside the same transaction as the claim, so a row is only marked
// PUBLISHED once its handler has succeeded.
//
// The handler runs while the row lock is held. Today's only Notifier is the in-process log
// stub (instant), and a real sender works one row per short transaction, acceptable for the
// portal's volume. A high-throughput sender would move delivery out of the lock via a
// dedicated PROCESSING status (a future migration); noted, not needed now.
func (s *PgOutboxStore) ProcessBatch(ctx context.Context, n int, settle func(context.Context, Event) Settlement) (int, error) {
	if s.pool == nil {
		return 0, nil
	}
	processed := 0
	for i := 0; i < n; i++ {
		ok, err := s.processOne(ctx, settle)
		if err != nil {
			return processed, err
		}
		if !ok {
			break // no PENDING rows left
		}
		processed++
	}
	return processed, nil
}

// processOne claims the next PENDING row and settles it within one transaction. Returns
// false when there is nothing to claim.
func (s *PgOutboxStore) processOne(ctx context.Context, settle func(context.Context, Event) Settlement) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var e Event
	err = tx.QueryRow(ctx, `
		SELECT event_id::text, topic, payload, attempts
		FROM events_outbox
		WHERE status = 'PENDING'
		ORDER BY occurred_at
		LIMIT 1
		FOR UPDATE SKIP LOCKED`).Scan(&e.EventID, &e.Topic, &e.Payload, &e.Attempts)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("claim outbox row: %w", err)
	}

	st := settle(ctx, e)

	switch st.Status {
	case statusPublished:
		payloadSet := ""
		if st.Redact {
			// Secret-bearing topic: drop the one-time token from rest now that it is delivered.
			payloadSet = ", payload = '{}'::jsonb"
		}
		_, err = tx.Exec(ctx, `
			UPDATE events_outbox
			SET status = 'PUBLISHED', published_at = now(), attempts = attempts + 1, last_error = ''`+payloadSet+`
			WHERE event_id = $1`, e.EventID)
	case statusDead:
		deadSet := ""
		if st.Redact {
			// A secret topic's DEAD row drops its payload too (HH-188): terminal is terminal, the
			// one-time token will never be delivered from this row, and the diagnosis in last_error
			// is the only part an operator needs.
			deadSet = ", payload = '{}'::jsonb"
		}
		_, err = tx.Exec(ctx, `
			UPDATE events_outbox
			SET status = 'DEAD', attempts = attempts + 1, last_error = $2`+deadSet+`
			WHERE event_id = $1`, e.EventID, st.LastError)
	default: // statusPending, transient failure, retry on a later tick
		_, err = tx.Exec(ctx, `
			UPDATE events_outbox
			SET attempts = attempts + 1, last_error = $2
			WHERE event_id = $1`, e.EventID, st.LastError)
	}
	if err != nil {
		return false, fmt.Errorf("settle outbox row: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// QueueRow is one outbox row still undelivered: waiting (PENDING) or dead
// lettered (DEAD), the FW1-02 posting queue's read. Document is the payload's
// own naming of the money document it carries (invoice_id, payment_id or
// order_id), so a reader can say which invoice or payment a stuck journal
// belongs to without parsing the payload.
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

// WaitingBannerThreshold is how long a PENDING outbox row with zero failed
// attempts may wait for the worker's next drain before the Money Desk banner
// counts it as stuck: a fresh event on a healthy system raises no alert.
const WaitingBannerThreshold = 5 * time.Minute

// WaitingOrDeadCounts returns how many rows for the named topics are waiting
// (failed at least once, or older than WaitingBannerThreshold) and how many are
// dead lettered. A nil pool answers zeroes.
func (s *PgOutboxStore) WaitingOrDeadCounts(ctx context.Context, topics []string) (waiting, dead int, err error) {
	if s.pool == nil || len(topics) == 0 {
		return 0, 0, nil
	}
	cutoff := time.Now().Add(-WaitingBannerThreshold)
	if err := s.pool.QueryRow(ctx, `
SELECT COUNT(*) FILTER (WHERE status = 'PENDING' AND (attempts > 0 OR occurred_at <= $2)),
       COUNT(*) FILTER (WHERE status = 'DEAD')
  FROM events_outbox WHERE topic = ANY($1::text[])`, topics, cutoff).Scan(&waiting, &dead); err != nil {
		return 0, 0, fmt.Errorf("notify: queue counts: %w", err)
	}
	return waiting, dead, nil
}
