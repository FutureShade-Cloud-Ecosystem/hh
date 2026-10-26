package gl

// posting_queue.go, FW1-02 sub item 3: the GL posting queue read. Every
// posted topic's outbox event that is still waiting (PENDING) or dead
// lettered (DEAD) is a journal the books do not hold yet, and until now no
// read surfaced them: the only sign was the subledger and the ledger drifting
// apart. PostingQueue lists them for the Financial Controller (topic,
// document, last error, attempts); PostingQueueCounts carries just the two
// counts for the Money Desk's banner. The rows come back through the outbox's
// own seam (notify.PgOutboxStore), never by naming another module's table.

import (
	"context"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

// PostingQueueRow is one GL event waiting or dead lettered.
type PostingQueueRow struct {
	EventID    string `json:"event_id"`
	Topic      string `json:"topic"`
	Document   string `json:"document"`
	Status     string `json:"status"`
	Attempts   int    `json:"attempts"`
	LastError  string `json:"last_error"`
	OccurredAt string `json:"occurred_at"`
}

// PostingQueueCounts is the two figure summary the Money Desk's banner reads.
type PostingQueueCounts struct {
	Waiting int `json:"waiting"`
	Dead    int `json:"dead"`
}

// OutboxQueue is the outbox's own read seam the posting queue reads through
// (satisfied by *notify.PgOutboxStore).
type OutboxQueue interface {
	WaitingOrDead(ctx context.Context, topics []string, limit int) ([]notify.QueueRow, error)
	WaitingOrDeadCounts(ctx context.Context, topics []string) (int, int, error)
}

// WithPostingQueue wires the outbox read the posting queue lists through.
func (s *Service) WithPostingQueue(q OutboxQueue) *Service { s.queue = q; return s }

// PostingQueue lists every posted topic event waiting or dead lettered, with
// its topic, its document, its last error and its attempts. Financial
// Controller only (the same narrowing as the period reopen).
func (s *Service) PostingQueue(ctx context.Context) ([]PostingQueueRow, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	if caller.RoleCode != controllerRole {
		return nil, errNotControllerQueue
	}
	if s.queue == nil {
		return nil, errQueueNotWired
	}
	rows, err := s.queue.WaitingOrDead(ctx, PostedTopics, 200)
	if err != nil {
		return nil, err
	}
	out := make([]PostingQueueRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, PostingQueueRow{
			EventID: r.EventID, Topic: r.Topic, Document: r.Document, Status: r.Status,
			Attempts: r.Attempts, LastError: r.LastError, OccurredAt: r.OccurredAt.UTC().Format(time.RFC3339),
		})
	}
	return out, nil
}

// PostingQueueCounts reads the waiting and dead lettered counts for the whole
// posting queue: the desk's banner reads, with no documents attached.
func (s *Service) PostingQueueCounts(ctx context.Context) (PostingQueueCounts, error) {
	if s.queue == nil {
		return PostingQueueCounts{}, errQueueNotWired
	}
	waiting, dead, err := s.queue.WaitingOrDeadCounts(ctx, PostedTopics)
	if err != nil {
		return PostingQueueCounts{}, err
	}
	return PostingQueueCounts{Waiting: waiting, Dead: dead}, nil
}
