package notify

// requeue.go, SCAFFOLD-owned DEAD-row replay seam on *notify.PgOutboxStore. Satisfies gl.DeadRequeuer. At
// boot the GL module replays outbox rows that dead-lettered before a handler existed (pre-W5 receipts that
// were never posted): RequeueDead resets DEAD rows for the named topics back to PENDING so the dispatcher
// re-delivers them to the now-registered posting consumer. Idempotent, a row already PENDING/PUBLISHED is
// untouched.

import (
	"context"
	"fmt"
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
