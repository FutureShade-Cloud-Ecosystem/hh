package gl

// posting_queue.go, FW1-02 sub item 3: the GL posting queue read. Every
// posted topic's outbox event that is still waiting (PENDING) or dead
// lettered (DEAD) is a journal the books do not hold yet, and until now no
// read surfaced them: the only sign was the subledger and the ledger drifting
// apart. PostingQueue lists them for the Financial Controller (topic,
// document, last error, attempts); PostingQueueCounts carries just the two
// counts for the Money Desk's banner.

import (
	"context"
	"time"
)

// PostingQueueRow is one GL event waiting or dead lettered.
type PostingQueueRow struct {
	EventID    string    `json:"event_id"`
	Topic      string    `json:"topic"`
	Document   string    `json:"document"`
	Status     string    `json:"status"`
	Attempts   int       `json:"attempts"`
	LastError  string    `json:"last_error"`
	OccurredAt time.Time `json:"occurred_at"`
}

// PostingQueueCounts is the two figure summary the Money Desk's banner reads.
type PostingQueueCounts struct {
	Waiting int `json:"waiting"`
	Dead    int `json:"dead"`
}

// PostingQueue lists every posted topic event waiting or dead lettered, with
// its topic, its document, its last error and its attempts. Financial
// Controller only (the same narrowing as the period reopen).
// SCAFFOLD STUB: the FW1-02 red test is written against this seam; the body
// lands with the sub item's fix.
func (s *Service) PostingQueue(ctx context.Context) ([]PostingQueueRow, error) {
	return nil, errNotImplemented
}

// PostingQueueCounts reads the waiting and dead lettered counts for the whole
// posting queue. SCAFFOLD STUB: see PostingQueue.
func (s *Service) PostingQueueCounts(ctx context.Context) (PostingQueueCounts, error) {
	return PostingQueueCounts{}, errNotImplemented
}
