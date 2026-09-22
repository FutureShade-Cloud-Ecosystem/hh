package notify

import (
	"context"
	"errors"
	"fmt"

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
		_, err = tx.Exec(ctx, `
			UPDATE events_outbox
			SET status = 'DEAD', attempts = attempts + 1, last_error = $2
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
