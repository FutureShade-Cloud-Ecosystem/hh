package assist

// model_activity.go, DIB-494. The read seam for "what happened on this order?", declared the way
// model.go and model_domain.go declare every other seam: in the language of the QUESTION, with the
// asking user's Caller as a REQUIRED argument, and with no writer anywhere near it.
//
// THE GATING CLAIM THIS SEAM CARRIES. The adapter behind it resolves the order through the SAME
// caller-scoped resolve every other order question uses (the order module's branch-fenced
// SummaryByNumber), and the trail it returns is the union of the two staff audit surfaces the app
// already shows, the status timeline and the edit history, never a wider projection of the audit
// log. A person who cannot open an order's history screen cannot read that history here, because it
// is the same fence, crossed once.

import (
	"context"
	"time"
)

// Audit event kinds, mirroring the order module's two trails.
const (
	AuditKindStatus = "STATUS"
	AuditKindEdit   = "EDIT"
)

// OrderAuditEvent is one recorded change on an order, already scoped to what the caller may see.
//
// Field carries the audit trail's own column vocabulary ("qty_entered", "requested_date") and
// Before/After its raw values, the COMMAND renders both into human words and formatted money
// (commands_activity.go), because format.go is the one formatter and an adapter computes nothing.
type OrderAuditEvent struct {
	Kind       string // AuditKindStatus | AuditKindEdit
	FromStatus string // "" at creation
	ToStatus   string
	Reason     string
	Field      string
	LineNo     int
	Before     string
	After      string
	ActorID    string
	ActorName  string // "" when the change carries no staff attribution (portal / system)
	At         time.Time
}

// OrderActivityReader answers "what has happened on this order" as the asking user.
//
// found=false covers the same three situations OrderReader's resolve collapses, no such number, a
// number at a branch the caller cannot reach, and no branch grants at all, and it is returned
// rather than an error for the same reason: all three are one honest sentence to the person asking,
// and a distinct "that exists but is not yours" would hand back exactly the fact the branch-scoped
// resolve exists to withhold. A resolved order with an EMPTY slice has simply never been moved or
// edited, which is an ordinary state and must be reported as one.
type OrderActivityReader interface {
	ActivityFor(ctx context.Context, c Caller, orderRef string) (o OrderRecord, events []OrderAuditEvent, found bool, err error)
}
