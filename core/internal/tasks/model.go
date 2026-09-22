// Package tasks is the WMS-Lite operational task queue (scopeBrief #5 buildableSpikeNow). It turns a
// chat message (or a manual staff POST) into a tracked, claimable PICK task bound to an order, with a
// strict status lifecycle and an append-only transition audit (task_events, migration 0073).
//
// Module boundary: tasks NEVER JOINs orders or messaging. It validates an order + reads its branch
// through the narrow OrderReader interface (satisfied in main.go by an adapter over the order module),
// and it posts chat EVENTs through the narrow ChatNotifier interface (satisfied by an adapter over the
// messaging event-post seam). Both are injected, tasks imports neither module.
//
// v1 scope: the PICK skeleton only. DISPATCH / NEED_WEIGHT round-trips, PUSH-timer auto-assignment, and
// SYSTEM auto-creation from the bus are GATED (see the TODOs in service.go / handler.go); the schema and
// this model leave their seams (task_type/priority/source enums, weight/push columns) but no code drives
// them yet.
package tasks

import (
	"context"
	"time"
)

// Task lifecycle states (0073 CHECK). The v1 state machine is a linear pick flow with a cancel escape:
//
//	QUEUED -▶ PUSHED -▶ ACCEPTED -▶ IN_PROGRESS -▶ DONE
//	   -▶ CANCELLED
//
// (QUEUED may also jump straight to ACCEPTED when a worker claims an un-pushed task.) DONE and CANCELLED
// are terminal. PUSHED is reachable in the machine but no v1 endpoint drives QUEUED→PUSHED (PUSH-timer
// auto-assignment is GATED); it is admitted so the gated feature needs no schema change.
//
// W2 (0095) inserts STAGED between IN_PROGRESS and DONE, see the yard.go doc block. The legacy
// IN_PROGRESS → DONE lane is deliberately retained.
const (
	StatusQueued     = "QUEUED"
	StatusPushed     = "PUSHED"
	StatusAccepted   = "ACCEPTED"
	StatusInProgress = "IN_PROGRESS"
	StatusStaged     = "STAGED"
	StatusDone       = "DONE"
	StatusCancelled  = "CANCELLED"
)

// Task types (0073 CHECK). v1 writes only PICK; the rest are GATED seams.
const (
	TypePick       = "PICK"
	TypeDispatch   = "DISPATCH"
	TypeNeedWeight = "NEED_WEIGHT"
	TypeOther      = "OTHER"
)

// Priorities (0073 CHECK).
const (
	PriorityLow    = "LOW"
	PriorityNormal = "NORMAL"
	PriorityHigh   = "HIGH"
	PriorityUrgent = "URGENT"
)

// Sources (0073 CHECK). CHAT is the v1 manual-POST path; SYSTEM is the (gated) bus auto-creation path.
const (
	SourceChat   = "CHAT"
	SourceSystem = "SYSTEM"
	SourceManual = "MANUAL"
)

// allowedTransitions is the single source of truth for the state machine, used BOTH by the repository
// (enforced atomically on a FOR UPDATE-locked pre-image) and by the in-memory fake in tests, so the
// legality matrix is validated once and never drifts between the two. A status maps to the set of
// statuses it may move TO; a terminal status maps to the empty set.
//
// W2/0095 adds STAGED as an optional stop between IN_PROGRESS and DONE. IN_PROGRESS → DONE stays legal:
// a will-call pick that is handed straight over the counter never gets parked, and every task written
// before 0095 must remain closable. STAGED → CANCELLED is legal too (a staged pick can still be pulled).
var allowedTransitions = map[string]map[string]bool{
	StatusQueued:     {StatusPushed: true, StatusAccepted: true, StatusCancelled: true},
	StatusPushed:     {StatusAccepted: true, StatusCancelled: true},
	StatusAccepted:   {StatusInProgress: true, StatusCancelled: true},
	StatusInProgress: {StatusStaged: true, StatusDone: true, StatusCancelled: true},
	StatusStaged:     {StatusDone: true, StatusCancelled: true},
	StatusDone:       {},
	StatusCancelled:  {},
}

// canTransition reports whether from → to is a legal move in the v1 state machine. A no-op (from == to)
// is NOT a legal transition, the caller distinguishes it (ErrNoTransition) from an illegal jump.
func canTransition(from, to string) bool {
	return allowedTransitions[from][to]
}

func validStatus(s string) bool { _, ok := allowedTransitions[s]; return ok }
func validSource(s string) bool { return s == SourceChat || s == SourceSystem || s == SourceManual }
func validPriority(s string) bool {
	return s == PriorityLow || s == PriorityNormal || s == PriorityHigh || s == PriorityUrgent
}

// Task is one operational task (a tasks row, 0073). Nullable columns surface as "" / nil with
// omitempty so an unset optional never bloats the payload. Money/qty (weight) is a NUMERIC(19,4)
// emitted as a string, matching the order module's DECIMAL-as-string convention.
type Task struct {
	ID               string     `json:"id"`
	TaskType         string     `json:"task_type"`
	OrderID          string     `json:"order_id"`
	OrderLineNo      *int       `json:"order_line_no,omitempty"`
	BranchID         string     `json:"branch_id,omitempty"`
	Priority         string     `json:"priority"`
	Status           string     `json:"status"`
	CreatedBy        string     `json:"created_by,omitempty"` // "" = system
	AssignedTo       string     `json:"assigned_to,omitempty"`
	ClaimedBy        string     `json:"claimed_by,omitempty"`
	ClaimedAt        *time.Time `json:"claimed_at,omitempty"`
	PushedAt         *time.Time `json:"pushed_at,omitempty"`
	AcceptDeadlineAt *time.Time `json:"accept_deadline_at,omitempty"`
	DoneAt           *time.Time `json:"done_at,omitempty"`
	PhotoURL         string     `json:"photo_url,omitempty"`
	WeightEntered    string     `json:"weight_entered,omitempty"` // NUMERIC(19,4) as string
	WeightUOM        string     `json:"weight_uom,omitempty"`
	Source           string     `json:"source"`
	SourceMessageID  string     `json:"source_message_id,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`

	// - W2 yard stage + shortage (0095) -
	StagedAt       *time.Time `json:"staged_at,omitempty"`
	StageLocation  string     `json:"stage_location,omitempty"` // physical spot, e.g. "Bay 3"
	StageRef       string     `json:"stage_ref,omitempty"`      // short handle the counter reads out
	OpenShortages  int        `json:"open_shortages"`           // unresolved task_shortages rows
	TotalShortages int        `json:"total_shortages"`          // reported, resolved or not

	// - W3 NEED_WEIGHT sync (0108, DIB-324) -
	// WeightSyncedAt is when WeightEntered reached the order line. A NEED_WEIGHT task carrying a
	// weight with this NULL is the QUERYABLE GAP 0108 indexes, the yard measured and the order did
	// not take it. WeightSyncError carries the order module's own refusal, verbatim, so the counter
	// reads WHY rather than "sync failed".
	WeightSyncedAt  *time.Time `json:"weight_synced_at,omitempty"`
	WeightSyncError string     `json:"weight_sync_error,omitempty"`
}

// NewTaskInput is the create write the repository persists (the tasks row + its birth task_events row,
// from_status NULL → to_status QUEUED, in one tx). The service fills TaskType/Priority/Source defaults
// and copies BranchID from the validated order before calling.
type NewTaskInput struct {
	OrderID         string
	OrderLineNo     *int
	BranchID        string
	TaskType        string
	Priority        string
	Source          string
	CreatedBy       string // "" = system
	SourceMessageID string
}

// TransitionInput is one status move the repository applies atomically (locked pre-image → legality
// check → status write + task_events row, in one tx). PhotoURL is applied only on the DONE transition.
type TransitionInput struct {
	TaskID   string
	To       string
	ActorID  string // "" = system transition (audited as NULL actor)
	Note     string
	PhotoURL string // set only when To == DONE
}

// TaskFilter is the picking-queue query projection over tasks (GET /tasks?status=&branch_id=). Empty
// Status / BranchID widen the filter. Limit/Offset are the shared httpx pagination window.
type TaskFilter struct {
	Status   string
	BranchID string
	Limit    int
	// Cursor pagination (HH-3.2 B5): AfterCursor resumes the list strictly
	// after (CursorActive, CursorPriority, CursorCreatedAt, CursorID), the
	// queue's stable-ordering key (open-first flag, priority rank, created,
	// id tie-breaker; the flag is stored inverted so every leg of the
	// row-value comparison ascends like the ORDER BY it mirrors).
	AfterCursor     bool
	CursorActive    int
	CursorPriority  int
	CursorCreatedAt time.Time
	CursorID        string
}

// CreateInput is the service-level create-from-order request (POST /api/v1/tasks body). v1 always
// produces a PICK task at QUEUED; Source defaults to CHAT. CreatedBy is the resolved staff caller.
type CreateInput struct {
	OrderID         string
	Source          string
	CreatedBy       string
	SourceMessageID string
}

// OrderBrief is the narrow order projection the tasks module consumes to validate an order exists and
// copy its branch onto the task, id/branch/fulfilment/status, NEVER a JOIN.
type OrderBrief struct {
	ID          string
	BranchID    string
	Fulfillment string
	Status      string
}

// OrderReader is the injected seam over the order module (satisfied in main.go by an adapter over
// order.Repository.OrderBrief). OrderBrief MUST return ErrUnknownOrder when the order does not exist,
// so create-from-order rejects an unknown order id without importing the order package.
type OrderReader interface {
	OrderBrief(ctx context.Context, orderID string) (OrderBrief, error)
}

// TaskEventRef mirrors the messaging EVENT ref schema (Topic / Entity / DeepLink / Summary) plus the
// idempotency EventKey and routing BranchID the ChatNotifier adapter needs. It lives in the tasks
// package so tasks never imports messaging; the main.go adapter maps it to messaging.EventRef and
// picks the target channel (the order's branch yard channel, else a default).
type TaskEventRef struct {
	EventKey string // idempotency key, e.g. "task:<id>:created"
	BranchID string // routes to the branch yard channel when set
	Topic    string // e.g. "task.created", "task.done"
	Entity   string // "task:<id>"
	DeepLink string // deep link back into the ERP
	Summary  string // human one-liner, becomes the EVENT message body

	// Audience says WHO must read this, AudienceYard (the pickers) or AudienceCounter (the person
	// standing in front of the customer). It exists because a shortage is useless in the yard channel:
	// the yard already knows it is short; the counter is the one who has to tell the customer. An
	// empty Audience means AudienceYard (every pre-W2 caller), so the field is additive.
	Audience Audience
}

// Audience is the routing intent of a task EVENT, which room the message has to land in. The
// mapping from an Audience to a concrete channel slug is the composition root's job (only it knows
// the messaging module); tasks only declares who needs to hear it.
type Audience string

const (
	AudienceYard    Audience = "YARD"    // the branch yard channel (pickers)
	AudienceCounter Audience = "COUNTER" // the branch counter/sales channel (customer-facing staff)
)

// ChatNotifier is the injected seam over the messaging event-post path (satisfied in main.go by an
// adapter over messaging.Service.PostEventToChannelSlug). It is best-effort at the service layer: a
// chat blip never fails a task write (the FR-3 non-owning-observer doctrine).
type ChatNotifier interface {
	PostTaskEvent(ctx context.Context, ref TaskEventRef) error
}
