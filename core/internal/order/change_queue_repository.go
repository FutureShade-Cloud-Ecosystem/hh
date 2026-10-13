package order

// change_queue_repository.go, the cross order read behind the change queue (change_queue.go).
//
// One statement over the order module's own tables: change_orders joined to orders for the visible
// order number. No other module's table is touched; the requester's role label comes through the
// injected RequesterRoleReader seam, not a join.

import (
	"context"
	"fmt"
)

// RequesterRoleReader answers the staff role label of each requesting user (users.id), so a queue
// row names who raised it by role and never by name. The composition root binds identity's reader;
// unbound, the label is blank.
type RequesterRoleReader interface {
	RoleLabelsForUsers(ctx context.Context, userIDs []string) (map[string]string, error)
}

// ChangeQueueItem is one row of the cross order change read: the change, the order it belongs to,
// the stored priced delta and what this caller may do to it.
type ChangeQueueItem struct {
	ID                    string   `json:"id"`
	ChangeNo              string   `json:"change_no"`
	OrderID               string   `json:"order_id"`
	OrderNo               string   `json:"order_no"`
	BranchID              string   `json:"branch_id"`
	Status                string   `json:"status"`
	Reason                string   `json:"reason"`
	DeltaCents            int64    `json:"delta_cents"`
	FinanceThresholdCents int64    `json:"finance_threshold_cents"`
	FinanceRequired       bool     `json:"finance_required"`
	RequestedByRole       string   `json:"requested_by_role"`
	RequestedAt           string   `json:"requested_at"`
	YardAckNote           string   `json:"yard_ack_note,omitempty"`
	NextActions           []string `json:"next_actions"`

	requestedByID string
	cursorAt      string
}

// changeQueueStore is asserted off the injected orderStore (the changeStore pattern), so every
// existing service fake keeps compiling.
type changeQueueStore interface {
	ListChangeQueue(ctx context.Context, q changeQueueQuery) ([]ChangeQueueItem, error)
}

// compile-time proof the real repository satisfies the queue store.
var _ changeQueueStore = (*Repository)(nil)

// changeQueueQuery is one page request: the status filter (empty = every status), the branch scope
// (global, or the caller's branch grants), the resume position and the row count to fetch.
type changeQueueQuery struct {
	Status    string
	Global    bool
	Branches  []string
	HasAfter  bool
	AfterAt   string // created_at, UTC, microsecond precision, as the cursor minted it
	AfterID   string
	FetchRows int
}

// ListChangeQueue returns change requests across orders, oldest first, ties broken by id so the
// order is total and a cursor never skips or repeats a row.
func (r *Repository) ListChangeQueue(ctx context.Context, q changeQueueQuery) ([]ChangeQueueItem, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	branches := q.Branches
	if branches == nil {
		branches = []string{}
	}
	afterAt, afterID := q.AfterAt, q.AfterID
	if !q.HasAfter {
		afterAt, afterID = "1970-01-01T00:00:00.000000Z", "00000000-0000-0000-0000-000000000000"
	}
	rows, err := r.pool.Query(ctx, `
		SELECT co.id::text, co.change_no, co.order_id::text, o.order_no::text,
		       COALESCE(co.branch_id::text,''), co.status, co.reason, co.delta_cents,
		       co.finance_threshold_cents, co.finance_required,
		       COALESCE(co.requested_by::text,''),
		       to_char(co.requested_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		       co.yard_ack_note,
		       to_char(co.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"')
		  FROM change_orders co
		  JOIN orders o ON o.id = co.order_id
		 WHERE ($1 = '' OR co.status = $1)
		   AND ($2::bool OR co.branch_id = ANY($3::uuid[]))
		   AND (NOT $4::bool OR (co.created_at, co.id) > ($5::timestamptz, $6::uuid))
		 ORDER BY co.created_at, co.id
		 LIMIT $7`,
		q.Status, q.Global, branches, q.HasAfter, afterAt, afterID, q.FetchRows)
	if err != nil {
		return nil, fmt.Errorf("list change queue: %w", err)
	}
	defer rows.Close()
	out := []ChangeQueueItem{}
	for rows.Next() {
		var it ChangeQueueItem
		if err := rows.Scan(&it.ID, &it.ChangeNo, &it.OrderID, &it.OrderNo, &it.BranchID, &it.Status,
			&it.Reason, &it.DeltaCents, &it.FinanceThresholdCents, &it.FinanceRequired,
			&it.requestedByID, &it.RequestedAt, &it.YardAckNote, &it.cursorAt); err != nil {
			return nil, fmt.Errorf("scan change queue row: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}
