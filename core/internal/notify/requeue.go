package notify

// requeue.go, SCAFFOLD-owned DEAD-row replay seam on *notify.PgOutboxStore. Satisfies gl.DeadRequeuer. At
// boot the GL module replays outbox rows that dead-lettered before a handler existed (pre-W5 receipts that
// were never posted): RequeueDead resets DEAD rows for the named topics back to PENDING so the dispatcher
// re-delivers them to the now-registered posting consumer. Idempotent, a row already PENDING/PUBLISHED is
// untouched.

import (
	"context"
	"fmt"
	"time"
)

// RequeueDead resets DEAD events_outbox rows for the given topics to PENDING (attempts 0, error cleared) and
// returns how many rows were requeued. A nil pool (scaffold/offline mode) is a no-op.
func (s *PgOutboxStore) RequeueDead(ctx context.Context, topics []string) (int, error) {
	if s.pool == nil || len(topics) == 0 {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE events_outbox
		 SET status = 'PENDING', attempts = 0, last_error = ''
		 WHERE status = 'DEAD' AND topic = ANY($1)`, topics)
	if err != nil {
		return 0, fmt.Errorf("notify: requeue dead: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// UnpostedForWindow answers how many outbox rows for the named topics stand
// PENDING or DEAD with occurred_at in [from, to): the GL close pre flight's
// "nothing waiting or dead lettered for that month" check (FW1-09b). A nil
// pool answers zero (scaffold/offline mode).
func (s *PgOutboxStore) UnpostedForWindow(ctx context.Context, topics []string, from, to time.Time) (int, error) {
	if s.pool == nil || len(topics) == 0 {
		return 0, nil
	}
	var n int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM events_outbox
		  WHERE topic = ANY($1) AND status IN ('PENDING','DEAD') AND occurred_at >= $2 AND occurred_at < $3`,
		topics, from, to).Scan(&n); err != nil {
		return 0, fmt.Errorf("notify: unposted for window: %w", err)
	}
	return n, nil
}
