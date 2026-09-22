// Package printing owns every document this system puts on paper, the queue substrate that records
// what was printed (DIB-411) and the documents themselves (DIB-410 counter receipt).
//
// It is deliberately a MODULE, not a helper inside pos. The receipt is the first document, but not
// the last: yard labels (raw ZPL at a Zebra) and delivery paperwork are the two printing gaps already
// found in the field, and neither belongs to the counter. Keeping the queue + the renderer here means
// the second doc type adds a template and an enum value, not a second mechanism.
package printing

import (
	"errors"
	"time"
)

// Document types. The enum is the one in migration 0088's CHECK, Go and the database agree by
// construction, and an unknown type is refused at the service boundary BEFORE it reaches the CHECK
// (so the caller gets a 400 naming the value, not a 500 carrying a constraint name).
const (
	DocReceipt     = "RECEIPT"      // a committed counter sale's customer receipt (DIB-410)
	DocLabelZPL    = "LABEL_ZPL"    // a yard/bin label, rendered as raw ZPL for a device agent
	DocDeliveryDoc = "DELIVERY_DOC" // the driver's delivery paperwork
)

// Render targets. BROWSER completes inside the request that enqueued it (the operator's print
// dialog is the device); ZPL waits for an agent to claim it.
const (
	TargetBrowser = "BROWSER"
	TargetZPL     = "ZPL"
)

// Job statuses.
const (
	StatusQueued  = "QUEUED"
	StatusPrinted = "PRINTED"
	StatusFailed  = "FAILED"
)

var (
	docTypes      = map[string]bool{DocReceipt: true, DocLabelZPL: true, DocDeliveryDoc: true}
	renderTargets = map[string]bool{TargetBrowser: true, TargetZPL: true}
)

// Job is one row of print_queue.
type Job struct {
	ID            string     `json:"id"`
	DocType       string     `json:"doc_type"`
	RefID         string     `json:"ref_id"`
	BranchID      string     `json:"branch_id"`
	RenderTarget  string     `json:"render_target"`
	Status        string     `json:"status"`
	RequestedBy   string     `json:"requested_by,omitempty"`
	FailureReason string     `json:"failure_reason,omitempty"`
	RequestedAt   time.Time  `json:"requested_at"`
	PrintedAt     *time.Time `json:"printed_at,omitempty"`
}

// EnqueueInput is one request for paper.
type EnqueueInput struct {
	DocType      string `json:"doc_type"`
	RefID        string `json:"ref_id"`
	BranchID     string `json:"branch_id"`
	RenderTarget string `json:"render_target"`
}

// - errors -

var (
	// ErrNotFound, no such sale/job, or one the caller may not know exists.
	ErrNotFound = errors.New("printing: not found")
	// ErrForbidden, the caller's branch scope does not reach this document's branch.
	ErrForbidden = errors.New("printing: forbidden")
	// ErrUnauthorized, no resolved staff caller on the context.
	ErrUnauthorized = errors.New("printing: unauthenticated")
	// ErrNotQueued, MarkPrinted was called on a job that is not QUEUED. A FAILED or an
	// already-PRINTED job must be re-enqueued, never restamped: the print count on a receipt is
	// only meaningful if a PRINTED row is created by exactly one print.
	ErrNotQueued = errors.New("printing: job is not QUEUED")
)
