package notify

// retry.go, FW1-02 sub item 3: the running worker's dead letter retry. A GL
// event whose journal is refused five times is dead lettered, and before this
// the only thing that brought it back was a core restart (the boot time
// RequeueDead in gl.RegisterPostingHandlers). DeadLetterRetry requeues the
// posted topics' DEAD rows on a schedule while the worker runs, with a
// backoff that grows each time the same rows come back dead and resets when a
// pass finds nothing to requeue: a refusal that gets fixed (a period opened,
// an account reactivated) is picked up without a restart, and a refusal that
// cannot be fixed retries forever at the capped pace instead of looping hot.

import (
	"context"
	"log/slog"
	"time"
)

// Dead letter retry tuning. The base wait is the first gap between requeue
// passes; each pass that requeued rows doubles the wait for the next pass up
// to the cap, and a pass that requeued nothing puts the wait back to base.
const (
	deadRetryBase = time.Minute
	deadRetryCap  = 30 * time.Minute
)

// DeadLetterRetry requeues DEAD outbox rows for its topics on a schedule with
// backoff. It is wired beside the dispatcher in the worker and is safe to run
// alongside any number of dispatchers: RequeueDead is an idempotent UPDATE on
// status, and a dispatcher claim races it only by winning a row first.
type DeadLetterRetry struct {
	store  DeadRequeuer
	topics []string
	log    *slog.Logger
}

// DeadRequeuer is the one operation the retry needs (satisfied by
// *PgOutboxStore, the same seam gl's boot replay reads).
type DeadRequeuer interface {
	RequeueDead(ctx context.Context, topics []string) (int, error)
}

// NewDeadLetterRetry builds the retry over the outbox store for the named
// topics.
func NewDeadLetterRetry(store DeadRequeuer, topics []string, log *slog.Logger) *DeadLetterRetry {
	return &DeadLetterRetry{store: store, topics: topics, log: log}
}

// RunOnce performs one requeue pass and returns how many DEAD rows were put
// back to PENDING. SCAFFOLD STUB: the FW1-02 red test is written against this
// seam; the body lands with the sub item's fix (a schedule with backoff, the
// constants above).
func (r *DeadLetterRetry) RunOnce(ctx context.Context) int {
	return 0
}

// Run is the worker loop until ctx is cancelled. SCAFFOLD STUB: see RunOnce.
func (r *DeadLetterRetry) Run(ctx context.Context) {
	<-ctx.Done()
}
