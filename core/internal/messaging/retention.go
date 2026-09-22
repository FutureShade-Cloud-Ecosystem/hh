package messaging

import (
	"context"
	"log/slog"
	"time"
)

// - Feature B: retention worker (0070) -
//
// The retention worker is a periodic ticker that archives (summarizes, NEVER deletes) messages that
// have aged past their channel's hot-retention window. It mirrors notify.Dispatcher's poll/batch/settle
// shape: Run drains once immediately then every interval until ctx is cancelled; ArchiveOnce is the
// exposed single pass (for tests + a one-shot drain). It reuses the same in-process ticker pattern as
// the dispatcher, no new broker, no NATS.
//
// Idempotency lives in the store (ArchiveChannelOlderThan only covers the delta since the last
// archived_through and conflicts on the window unique index), so a missed or repeated tick never
// double-archives or loses content.

// defaultRetentionInterval is deliberately long: retention is a housekeeping sweep, not a hot path, so
// a chatty interval would waste work. Overridable via WithInterval (tests use a tiny value).
const defaultRetentionInterval = 6 * time.Hour

// retentionStore is the persistence seam the worker depends on (satisfied by *Repository). Expressing
// it as an interface lets the per-channel selection loop be unit-tested with a fake, DB-free.
type retentionStore interface {
	ChannelRetentions(ctx context.Context) ([]ChannelRetention, error)
	ArchiveChannelOlderThan(ctx context.Context, channelID string, cutoff time.Time) (int, error)
}

// RetentionWorker archives aged messages on a ticker. It owns no state beyond the ticker, so Run is
// safe to call in a goroutine (go worker.Run(ctx)), exactly like notify.Dispatcher.Run.
type RetentionWorker struct {
	repo     retentionStore
	log      *slog.Logger
	interval time.Duration
}

// NewRetentionWorker builds the worker over a store (the *Repository in production; a fake in tests).
func NewRetentionWorker(repo retentionStore, log *slog.Logger) *RetentionWorker {
	if log == nil {
		log = slog.Default()
	}
	return &RetentionWorker{repo: repo, log: log, interval: defaultRetentionInterval}
}

// WithInterval overrides the sweep interval (tests use a tiny value; <=0 is ignored), mirroring
// notify.Dispatcher.WithInterval.
func (w *RetentionWorker) WithInterval(v time.Duration) *RetentionWorker {
	if v > 0 {
		w.interval = v
	}
	return w
}

// Run sweeps immediately, then every interval, until ctx is cancelled. Mirrors notify.Dispatcher.Run.
func (w *RetentionWorker) Run(ctx context.Context) {
	w.log.Info("messaging: retention worker started", "interval", w.interval.String())
	w.ArchiveOnce(ctx, time.Now())
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			w.log.Info("messaging: retention worker stopped")
			return
		case <-t.C:
			w.ArchiveOnce(ctx, time.Now())
		}
	}
}

// ArchiveOnce runs one sweep as of `now`: it resolves each live channel's effective retention window,
// turns it into a cutoff (retentionCutoff), and archives that channel's messages older than the cutoff.
// A per-channel archive failure is logged and skipped (a bad channel never blocks the others), matching
// the dispatcher's log-and-continue posture. Returns the total number of archive rows written. Errors
// from the store are logged, not propagated, the next tick retries (like DrainOnce).
func (w *RetentionWorker) ArchiveOnce(ctx context.Context, now time.Time) int {
	rets, err := w.repo.ChannelRetentions(ctx)
	if err != nil {
		w.log.Error("messaging: retention sweep failed to load policies", "err", err)
		return 0
	}
	total := 0
	for _, r := range rets {
		cutoff := retentionCutoff(now, r.Months)
		n, err := w.repo.ArchiveChannelOlderThan(ctx, r.ChannelID, cutoff)
		if err != nil {
			w.log.Warn("messaging: retention archive failed for channel, skipping",
				"channel", r.ChannelID, "cutoff", cutoff, "err", err)
			continue
		}
		if n > 0 {
			w.log.Debug("messaging: archived aged messages", "channel", r.ChannelID, "cutoff", cutoff, "rows", n)
		}
		total += n
	}
	return total
}

// retentionCutoff is the pure retention-selection primitive: content strictly older than the returned
// instant (now - months) is eligible to archive. A non-positive months falls back to
// defaultRetentionMonths so a misconfigured policy can never archive live content. Kept pure (no ctx/DB)
// so the month arithmetic + boundary is unit-tested in isolation, and it pins the same contract the
// ArchiveChannelOlderThan SQL relies on.
func retentionCutoff(now time.Time, months int) time.Time {
	if months <= 0 {
		months = defaultRetentionMonths
	}
	return now.AddDate(0, -months, 0)
}
