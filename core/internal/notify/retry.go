package notify

// retry.go, FW1-02 sub item 3: the running worker's dead letter retry. A GL
// event whose journal is refused five times is dead lettered, and before this
// the only thing that brought it back was a core restart (the boot time
// RequeueDead in gl.RegisterPostingHandlers). DeadLetterRetry requeues the
// posted topics' DEAD rows on a schedule while the worker runs, with a
// backoff that grows each time a pass requeued rows (the same rows probably
// failing again) and resets when a pass finds nothing to requeue: a refusal
// that gets fixed (a period opened, an account reactivated) is picked up
// without a restart, and a refusal that cannot be fixed retries forever at
// the capped pace instead of looping hot.

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
	// deadRetryTick is how often the runner looks at the clock; the wait
	// itself is the schedule above.
	deadRetryTick = 5 * time.Second
)

// DeadLetterRetry requeues DEAD outbox rows for its topics on a schedule with
// backoff. It is wired beside the dispatcher in the worker and is safe to run
// alongside any number of dispatchers: RequeueDead is an idempotent UPDATE on
// status, and a dispatcher claim races it only by winning a row first.
type DeadLetterRetry struct {
	store  DeadRequeuer
	topics []string
	log    *slog.Logger
	wait   time.Duration
	next   time.Time
}

// DeadRequeuer is the one operation the retry needs (satisfied by
// *PgOutboxStore, the same seam gl's boot replay reads).
type DeadRequeuer interface {
	RequeueDead(ctx context.Context, topics []string) (int, error)
}

// NewDeadLetterRetry builds the retry over the outbox store for the named
// topics.
func NewDeadLetterRetry(store DeadRequeuer, topics []string, log *slog.Logger) *DeadLetterRetry {
	return &DeadLetterRetry{store: store, topics: topics, log: log, wait: deadRetryBase}
}

// RunOnce performs one requeue pass if the schedule allows one now, and
// returns how many DEAD rows were put back to PENDING. A pass that requeued
// rows grows the next wait (backoff); a pass that requeued nothing resets it.
func (r *DeadLetterRetry) RunOnce(ctx context.Context) int {
	now := time.Now()
	if now.Before(r.next) {
		return 0
	}
	n, err := r.store.RequeueDead(ctx, r.topics)
	if err != nil {
		if r.log != nil {
			r.log.Error("notify: dead letter requeue failed", "err", err)
		}
		return 0
	}
	if n > 0 {
		r.wait *= 2
		if r.wait > deadRetryCap {
			r.wait = deadRetryCap
		}
		if r.log != nil {
			r.log.Warn("notify: requeued dead lettered events for retry",
				"rows", n, "next_pass_in", r.wait.String())
		}
	} else {
		r.wait = deadRetryBase
	}
	r.next = now.Add(r.wait)
	return n
}

// Run is the worker loop: one immediate pass, then a tick that fires a pass
// whenever the schedule allows one, until ctx is cancelled.
func (r *DeadLetterRetry) Run(ctx context.Context) {
	r.RunOnce(ctx)
	t := time.NewTicker(deadRetryTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.RunOnce(ctx)
		}
	}
}
