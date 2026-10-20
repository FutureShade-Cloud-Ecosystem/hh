package tasks

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// Domain errors (exported for errors.Is at the handler boundary and cross-package adapters).
var (
	// ErrNotFound, the task does not exist (or a malformed id names none). → 404.
	ErrNotFound = errors.New("tasks: not found")
	// ErrUnknownOrder, create referenced an order that does not exist. The injected OrderReader
	// returns this (the main.go adapter maps order.ErrNotFound onto it) so the service never imports
	// the order package. → 422.
	ErrUnknownOrder = errors.New("tasks: unknown order")
	// ErrIllegalTransition, the requested status move is not legal from the task's current status. → 409.
	ErrIllegalTransition = errors.New("tasks: illegal transition")
	// ErrNoTransition, the task is already in the requested status (a benign duplicate move). → 409.
	ErrNoTransition = errors.New("tasks: status unchanged")
	// ErrHeldFromPicking, the task's order owes money under a pay before release shape (item 9.3's
	// hold): the goods wait for the money, so the yard cannot claim or stage its task. → 409.
	ErrHeldFromPicking = errors.New("tasks: this order cannot reach picking until the balance is paid")
	// ErrHoldUnreadable, a wired pick hold check could not be read: the move is refused rather than
	// allowed (fail closed). Unreachable in a correct deploy. → 503.
	ErrHoldUnreadable = errors.New("tasks: the pay before release hold could not be read")
	// errNotConfigured, the OrderReader seam is not wired (a boot/wiring bug; dev always wires it and
	// tests always inject a fake). Defensive only. → 500.
	errNotConfigured = errors.New("tasks: order reader not configured")
)

// taskStore is the persistence the Service needs. Real impl: *Repository (pgx); tests use an in-memory
// fake. The status-write-plus-audit atomicity lives inside the store (Transition/CreateTask each own
// their tx), so the service stays a thin validation + orchestration layer.
type taskStore interface {
	CreateTask(ctx context.Context, in NewTaskInput) (Task, error)
	GetTask(ctx context.Context, id string) (Task, error)
	ListTasks(ctx context.Context, f TaskFilter) ([]Task, error)
	Transition(ctx context.Context, in TransitionInput) (Task, error)

	// - W2 yard flow (0095) -
	// Stage applies the IN_PROGRESS → STAGED move together with its stage stamp (location/ref) and
	// its task_events row, in ONE transaction, the same atomicity guarantee Transition gives.
	Stage(ctx context.Context, in StageInput) (Task, error)
	// InsertShortage appends one task_shortages row and returns it with the task it belongs to (the
	// caller needs the task's branch to route the counter EVENT). One transaction.
	InsertShortage(ctx context.Context, in ShortageInput) (Shortage, Task, error)
	// ListShortages reads a task's shortages oldest-first; openOnly filters to unresolved.
	ListShortages(ctx context.Context, taskID string, openOnly bool) ([]Shortage, error)
	// ResolveShortage stamps the resolution on an OPEN shortage (ErrShortageResolved if already
	// closed, ErrShortageNotFound if it is not this task's), returning it with its task.
	ResolveShortage(ctx context.Context, in ResolveShortageInput) (Shortage, Task, error)
	// StalePickTasks lists PICK tasks still QUEUED since before cutoff, oldest first (the sweep feed).
	StalePickTasks(ctx context.Context, cutoff time.Time, limit int) ([]Task, error)

	// - 0187 · what was actually pulled -
	// UpsertPickedLines records the whole pick in ONE transaction with the task locked. Upsert
	// rather than insert because a picker corrects themselves and a line has exactly one live
	// number; the correction history is task_events', as it is for every other change here.
	UpsertPickedLines(ctx context.Context, taskID, actorID string, lines []PickedLineInput) ([]PickedLine, error)
	// RecordPickLocked is the record the service runs: the task row locked across apply (the order
	// call, handing back the lines to store), a CANCELLED or DONE task refused, the lines and the
	// task's fresh seq written in the same transaction.
	RecordPickLocked(ctx context.Context, taskID, actorID string, apply func(context.Context, Task) ([]PickedLineInput, error)) ([]PickedLine, error)
	// ListPickedLines reads a task's recorded pick in ticket order.
	ListPickedLines(ctx context.Context, taskID string) ([]PickedLine, error)

	// - W3 (Swarm C) -
	// RecordWeight stamps a NEED_WEIGHT task's measurement and walks it to DONE through the SAME
	// legality matrix every other move uses, writing one task_events row per hop, all in ONE
	// transaction. See weight.go's SubmitWeight comment on why one tap is still three legal moves.
	RecordWeight(ctx context.Context, in WeightWrite) (Task, error)
	// MarkWeightSync stamps whether the recorded weight reached the order line. A nil `at` with a
	// message is the queryable gap 0108 indexes; a non-nil `at` clears any previous message.
	MarkWeightSync(ctx context.Context, taskID string, at *time.Time, errText string) error
	// AdvanceRule reads one task type's order-advance ruling (0108). An unknown type returns a
	// DISABLED rule and no error, a type with no row cannot advance anything.
	AdvanceRule(ctx context.Context, taskType string) (AdvanceRule, error)
}

// Service is the tasks business layer: it validates input, resolves the order through the injected
// OrderReader, funnels every status move through the store's transactional Transition, and posts chat
// EVENTs through the injected ChatNotifier (best-effort). OrderReader is required for create; the
// ChatNotifier is optional (nil = no chat posts).
type Service struct {
	store    taskStore
	orders   OrderReader
	notifier ChatNotifier
	picks    PickSource // W2 yard queue feed (see yard.go); nil = no ticket decoration
	log      *slog.Logger

	// - W3 (Swarm C) -
	// lines is the DIB-324 order-line edit seam (weight.go). Nil = the weight is recorded and the
	// order line is left alone, with the gap stated on the task rather than swallowed.
	lines OrderLineWriter
	// advancer is the DIB-325 order-status advance seam (advance.go). Nil = no advance, ever.
	advancer OrderAdvancer
	// audit records the ONE thing this module does that changes another module's row: a fired
	// order advance. Nil = no audit rows (best-effort, never a precondition).
	audit audit.Recorder
	// yardOrders is the short pick's order seam (yard_short.go). Nil = a short pick is recorded with
	// its reason and does not reach the order.
	yardOrders YardOrderChanges
	// holdCheck is the pick hold seam (yard_hold.go, item 9 Opus P2-1). Nil = the yard doors do not
	// refuse a held order (the additive lane; the composition root asserts the wiring at boot).
	holdCheck PickHoldCheck
	// claimerRoles labels a pick's claimer by role on the pick reads (claimer_role.go). Nil = no label.
	claimerRoles ClaimerRoleReader
}

// NewService wires the store. Inject the order + chat seams with WithOrderReader / WithChatNotifier.
func NewService(store taskStore) *Service {
	return &Service{store: store, log: slog.Default()}
}

// WithOrderReader injects the order-validation seam (create-from-order rejects an unknown order).
func (s *Service) WithOrderReader(r OrderReader) *Service { s.orders = r; return s }

// WithChatNotifier injects the messaging event-post seam (create + done post a system EVENT into chat).
func (s *Service) WithChatNotifier(n ChatNotifier) *Service { s.notifier = n; return s }

// WithLogger overrides the default logger (used only for the best-effort chat-notify warning path).
func (s *Service) WithLogger(l *slog.Logger) *Service {
	if l != nil {
		s.log = l
	}
	return s
}

// CreateFromOrder creates a QUEUED PICK task from an order id. It validates the order exists (via the
// OrderReader, unknown order → ErrUnknownOrder), copies the order's branch onto the task, persists the
// task + its birth audit row (one tx, in the store), then posts a "task created" EVENT into chat
// (best-effort). Source defaults to CHAT; task_type is always PICK and status always QUEUED in v1.
func (s *Service) CreateFromOrder(ctx context.Context, in CreateInput) (Task, error) {
	if in.OrderID == "" {
		return Task{}, httpx.BadRequestf("order_id is required")
	}
	source := in.Source
	if source == "" {
		source = SourceChat
	}
	if !validSource(source) {
		return Task{}, httpx.BadRequestf("unknown source %q", source)
	}
	if s.orders == nil {
		return Task{}, errNotConfigured
	}
	ob, err := s.orders.OrderBrief(ctx, in.OrderID)
	if err != nil {
		return Task{}, err // ErrUnknownOrder (or a transport error), surfaced as-is
	}
	t, err := s.store.CreateTask(ctx, NewTaskInput{
		OrderID:         ob.ID,
		BranchID:        ob.BranchID,
		TaskType:        TypePick,
		Priority:        PriorityNormal,
		Source:          source,
		CreatedBy:       in.CreatedBy,
		SourceMessageID: in.SourceMessageID,
		OrgID:           in.OrgID,
	})
	if err != nil {
		return Task{}, err
	}
	s.postEvent(ctx, t, "created", "New PICK task queued for order "+ob.ID+".")
	return t, nil
}

// Claim moves a QUEUED/PUSHED task to ACCEPTED and stamps claimed_by/at (the store applies the field
// effects on the ACCEPTED transition). Illegal from any other status.
//
// Item 9 Opus P2-1: the claim refuses when the order is held from picking
// under a pay before release shape, BEFORE the picker walks out to the bays
// (the queue row is marked; this is the door under the mark).
func (s *Service) Claim(ctx context.Context, id, actor string) (Task, error) {
	if err := s.refuseHeldTask(ctx, id); err != nil {
		return Task{}, err
	}
	return s.transition(ctx, id, StatusAccepted, actor, "", "")
}

// Accept moves an ACCEPTED task to IN_PROGRESS (work has started). Illegal from any other status.
func (s *Service) Accept(ctx context.Context, id, actor string) (Task, error) {
	return s.transition(ctx, id, StatusInProgress, actor, "", "")
}

// Done moves an IN_PROGRESS task to DONE, stamps done_at + the optional photo_url (in the store), and
// posts a completion EVENT into chat (best-effort). Illegal from any other status.
//
// GATED, advancing the ORDER's status on task completion is DIB-191's open SCOPE decision (a product
// ruling, not engineering): tasks stay purely operational and orders keep their current status. When
// that ruling lands, the order-status advance would hook in HERE, through the order module's
// SetStatus choke-point (never a direct write).
func (s *Service) Done(ctx context.Context, id, actor, photoURL string) (Task, error) {
	t, err := s.transition(ctx, id, StatusDone, actor, "", photoURL)
	if err != nil {
		return Task{}, err
	}
	s.postEvent(ctx, t, "done", "PICK task completed for order "+t.OrderID+".")
	// W3-C / DIB-325: the ruling this comment block used to defer. It is still a DECISION, the rule
	// is read per task type from 0108 and every seeded row is OFF, so this is a no-op on every
	// deployment that has not deliberately turned it on. advance.go states the whole reasoning.
	s.maybeAdvanceOrder(ctx, t)
	return t, nil
}

// Cancel moves any non-terminal task to CANCELLED with an optional note. Illegal from a terminal status.
func (s *Service) Cancel(ctx context.Context, id, actor, note string) (Task, error) {
	return s.transition(ctx, id, StatusCancelled, actor, note, "")
}

// ListTasks returns one page of the picking queue, validating the optional
// status/branch filters. Cursor pagination (HH-3.2 B5): the total echo is
// gone with the offset family; the cursor envelope carries no total.
func (s *Service) ListTasks(ctx context.Context, f TaskFilter) ([]Task, error) {
	if f.Status != "" && !validStatus(f.Status) {
		return nil, httpx.BadRequestf("unknown status %q", f.Status)
	}
	if f.BranchID != "" && !looksLikeUUID(f.BranchID) {
		return nil, httpx.BadRequestf("branch_id must be a uuid")
	}
	return s.store.ListTasks(ctx, f)
}

// transition is the shared move helper: validate the id, then funnel through the store's transactional
// Transition (which enforces the state machine on a locked pre-image and writes the task_events row).
func (s *Service) transition(ctx context.Context, id, to, actor, note, photoURL string) (Task, error) {
	if id == "" {
		return Task{}, httpx.BadRequestf("task id is required")
	}
	return s.store.Transition(ctx, TransitionInput{TaskID: id, To: to, ActorID: actor, Note: note, PhotoURL: photoURL})
}

// postEvent posts a system EVENT into chat for a task lifecycle moment. Best-effort: a nil notifier is
// a no-op, and a post error is logged, never returned, a messaging blip must never fail the task write
// (the FR-3 non-owning-observer doctrine). The EventKey is idempotent per (task, moment).
func (s *Service) postEvent(ctx context.Context, t Task, moment, summary string) {
	if s.notifier == nil {
		return
	}
	ref := TaskEventRef{
		EventKey: "task:" + t.ID + ":" + moment,
		BranchID: t.BranchID,
		Topic:    "task." + moment,
		Entity:   "task:" + t.ID,
		DeepLink: "/app/tasks/" + t.ID,
		Summary:  summary,
	}
	if err := s.notifier.PostTaskEvent(ctx, ref); err != nil {
		s.log.Warn("tasks: chat notify failed", "task", t.ID, "moment", moment, "err", err)
	}
}

// TODO(scopeBrief #5, GATED, SYSTEM auto-creation): subscribe to POS/order bus events (platform/events)
// and auto-create SYSTEM-sourced PICK tasks when an order is confirmed/paid. Left as a stubbed seam, the
// manual POST /api/v1/tasks path proves the chat→order→task→chat loop for v1. Blocking decision: which
// order/POS event(s) should mint a task, and the branch/line fan-out, is a product-scope ruling.

// TODO(scopeBrief #5, GATED, DISPATCH / NEED_WEIGHT / PUSH): v1 is the PICK skeleton only. The full
// DISPATCH side (routes/vehicles/drivers/deliveries/POD, capacity), the NEED_WEIGHT weight round-trip
// (the weight_entered/weight_uom columns already exist), and PUSH/timer auto-assignment (the pushed_at/
// accept_deadline_at/assigned_to columns + a QUEUED→PUSHED driver already exist) are deferred. The
// task_type / status enums (0073) already admit their values, so each lands without a schema change;
// blocking decision for each is its own scoped design (dispatch model, weight capture UX, push policy).
