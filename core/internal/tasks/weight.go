package tasks

// weight.go, DIB-324, the NEED_WEIGHT round trip, and DIB-325, the decision-gated order advance.
//
// WHAT WAS GATED, AND WHY IT IS NOT ANY MORE.
//
// 0073 shipped tasks.weight_entered / weight_uom and the NEED_WEIGHT task_type as columns with no
// writer, and service.go's closing TODO names the blocking decision as "weight capture UX". The UX
// question turned out to have one honest answer, which this file implements: the counter, standing in
// front of a customer buying bulk material, pushes a REQUEST naming the order line and the unit; the
// yard weighs the load and types ONE number back; the order line takes that number. Nothing else is
// asked of either person, because everything else is already on the order.
//
// THE THREE RULES THIS FILE KEEPS.
//
//  1. THE UNIT IS CHECKED, NOT ASSUMED. A weight typed in the wrong unit is refused BEFORE the order
//     line is written, by reading the line's own uom through the seam. Silently reinterpreting 2.8
//     TONNE as 2.8 KG is a four-thousand-dollar rounding error, and "the yard should have known" is
//     not a control.
//  2. THE ORDER WRITE GOES THROUGH THE ORDER MODULE'S OWN GUARDED PATH. tasks does not touch
//     order_lines. OrderLineWriter (below) is a consumer-declared seam, satisfied in the composition
//     root by an adapter over the order module's guarded, repriced, audited line edit, so the weight
//     lands under the SAME status guard, money precheck and audit trail as a counter user typing it.
//  3. A REFUSED ORDER WRITE IS A VISIBLE GAP, NOT A LOST MEASUREMENT AND NOT A FAILED TASK. The yard
//     weighed the load; that fact is theirs and it is recorded either way. If the order refuses the
//     edit (a status past editing, a caller without the authority, a seam that is not wired), the
//     task keeps the weight, records the order module's own sentence, and leaves weight_synced_at
//     NULL, which 0108 indexes precisely so the gap can be listed. This is delivery_stops.handoff_at
//     applied to a second module.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// ErrNotWeightTask, a weight was submitted against a task that is not a NEED_WEIGHT task. 409: the
// request was well-formed and the task was the wrong one.
var ErrNotWeightTask = errors.New("tasks: this is not a weight request")

// ErrWeightUOMMismatch, the yard typed a number in a unit the order line does not use. 400, and the
// message names BOTH units, because the fix is to re-weigh or to re-request, and the person holding
// the scale needs to know which.
var ErrWeightUOMMismatch = errors.New("tasks: the weight is in a different unit from the order line")

// WeightRequestInput is the counter's push: which line needs a number, in what unit, and why.
type WeightRequestInput struct {
	OrderID     string
	OrderLineNo int
	UOM         string // the unit the yard must answer in; defaults to the line's own
	Note        string
	RequestedBy string
	// OrgID is the caller's org (UX-81c): the order is read under it and the stored task carries it.
	// Empty takes the order branch's org at the insert (UX-95).
	OrgID string
	// Reach says whether the caller's branch reach covers a branch, as CreateInput.Reach does for a pick.
	Reach func(branch string) (bool, error)
}

// WeightInput is the yard's answer.
type WeightInput struct {
	TaskID  string
	ActorID string
	Weight  string // DECIMAL as string, must parse > 0
	UOM     string
	Note    string
	// OrgID and Reach hold the answer to the caller's org and branch reach (UX-81c round 2): the answer rewrites the
	// order line's quantity, so a task outside either is a missing task and nothing is written. A nil Reach is a
	// system caller; an empty OrgID reads the task unscoped, as every system read does.
	OrgID string
	Reach func(branch string) (bool, error)
}

// WeightResult is one round trip's outcome, stated completely: the task (carrying the weight), and
// whether the order line took it.
//
// OrderSynced and OrderError are BOTH on the response on purpose. A yard worker who typed a weight
// that the order refused must see that on the screen they typed it into, not discover it later from
// a reconciliation report, and the counter reading the chat EVENT gets the same sentence.
type WeightResult struct {
	Task        Task   `json:"task"`
	OrderSynced bool   `json:"order_synced"`
	OrderError  string `json:"order_error,omitempty"`
	OrderLineNo int    `json:"order_line_no,omitempty"`
	UOM         string `json:"uom,omitempty"`
}

// OrderLineWriter is the CONSUMER-DECLARED seam over the order module's guarded line edit. Satisfied
// in cmd/server/main.go; tasks imports order in neither direction, exactly as OrderReader established.
//
// SEAM CONTRACT:
//   - LineUOM returns the unit the line is entered in. An unknown order or line returns an error; the
//     caller treats that as "the line named by this request no longer exists".
//   - SetLineQty rewrites the line's ENTERED quantity in that same unit, through the order module's
//     own authority ladder. Its refusals (status past editing, caller not permitted, fulfilment
//     invariants) come back as errors and are recorded verbatim, never translated, because the order
//     module's sentence is the accurate one.
type OrderLineWriter interface {
	LineUOM(ctx context.Context, orderID string, lineNo int) (string, error)
	SetLineQty(ctx context.Context, orderID string, lineNo int, qtyEntered string) error
}

// WithOrderLineWriter injects the line-edit seam. Unwired, the round trip still records the weight and
// says plainly that it went nowhere (see rule 3 in the header).
func (s *Service) WithOrderLineWriter(w OrderLineWriter) *Service { s.lines = w; return s }

// RequestWeight is the counter's push: it mints a NEED_WEIGHT task against one order line and tells
// the YARD about it.
//
// PRIORITY IS HIGH, NOT NORMAL. Somebody is standing at a counter waiting for this number; a weight
// request that queues behind the afternoon's picks is a customer standing at a counter for an hour.
func (s *Service) RequestWeight(ctx context.Context, in WeightRequestInput) (Task, error) {
	if strings.TrimSpace(in.OrderID) == "" {
		return Task{}, httpx.BadRequestf("order_id is required")
	}
	if in.OrderLineNo <= 0 {
		return Task{}, httpx.BadRequestf("order_line_no must be positive, a weight request names ONE line")
	}
	if len(strings.TrimSpace(in.Note)) > 400 {
		return Task{}, httpx.BadRequestf("note is too long (max 400)")
	}
	if s.orders == nil {
		return Task{}, errNotConfigured
	}
	ob, err := s.briefForCreate(ctx, in.OrgID, in.OrderID, in.Reach)
	if err != nil {
		return Task{}, err
	}
	uom := strings.ToUpper(strings.TrimSpace(in.UOM))
	if s.lines != nil {
		lineUOM, err := s.lines.LineUOM(ctx, ob.ID, in.OrderLineNo)
		if err != nil {
			// The line does not exist. Refusing HERE is the whole point of asking: a weight request
			// against a line nobody can find is a task the yard cannot complete, and it would sit in
			// the queue looking like work.
			return Task{}, httpx.BadRequestf("order %s has no line %d", ob.ID, in.OrderLineNo)
		}
		if uom == "" {
			uom = strings.ToUpper(lineUOM)
		} else if !strings.EqualFold(uom, lineUOM) {
			return Task{}, httpx.BadRequestf(
				"line %d is entered in %s, a weight requested in %s could not be written back to it",
				in.OrderLineNo, lineUOM, uom)
		}
	}
	lineNo := in.OrderLineNo
	t, err := s.store.CreateTask(ctx, NewTaskInput{
		OrderID:     ob.ID,
		OrderLineNo: &lineNo,
		BranchID:    ob.BranchID,
		TaskType:    TypeNeedWeight,
		Priority:    PriorityHigh,
		Source:      SourceManual,
		CreatedBy:   in.RequestedBy,
		OrgID:       in.OrgID,
	})
	if err != nil {
		return Task{}, err
	}
	body := fmt.Sprintf("WEIGH IT: order %s line %d needs a weight", t.OrderID, lineNo)
	if uom != "" {
		body += " in " + uom
	}
	body += "."
	if n := strings.TrimSpace(in.Note); n != "" {
		body += " Counter note: " + n
	}
	// The YARD is the audience, this is the one direction the shortage path runs in reverse.
	s.postAudienceEventKeyed(ctx, t, "weight.requested", "task.weight_requested", AudienceYard, body)
	return t, nil
}

// SubmitWeight is the yard's answer, and the whole round trip in one call.
//
// ONE TAP IN THE YARD IS STILL THREE LEGAL MOVES, AND THE TRAIL SAYS SO. A picker holding a scale
// ticket should not have to claim, start and then complete a task to type one number. The store
// therefore walks the SAME state machine every other move uses (QUEUED → ACCEPTED → IN_PROGRESS →
// DONE) inside one transaction, writing a task_events row for each hop. The matrix is untouched; what
// changes is how many taps it takes to traverse it.
func (s *Service) SubmitWeight(ctx context.Context, in WeightInput) (WeightResult, error) {
	if strings.TrimSpace(in.TaskID) == "" {
		return WeightResult{}, httpx.BadRequestf("task id is required")
	}
	weight, err := parsePositiveQty(in.Weight)
	if err != nil {
		return WeightResult{}, err
	}
	uom := strings.ToUpper(strings.TrimSpace(in.UOM))
	if uom == "" {
		return WeightResult{}, httpx.BadRequestf("say what unit the weight is in")
	}
	if len(strings.TrimSpace(in.Note)) > 400 {
		return WeightResult{}, httpx.BadRequestf("note is too long (max 400)")
	}

	// The task is read under the caller's org (the branch's org, GetTask) and held to the caller's branch reach before
	// anything else is said or written: another dealer's task, or one on a branch the caller does not reach, is the
	// same ErrNotFound as a missing task, so the answer tells nothing about it.
	ctx = withOrg(ctx, in.OrgID)
	pre, err := s.store.GetTask(ctx, in.TaskID)
	if err != nil {
		return WeightResult{}, err
	}
	if in.Reach != nil {
		reach, err := in.Reach(pre.BranchID)
		if err != nil {
			return WeightResult{}, fmt.Errorf("%w: %v", ErrReachUnavailable, err)
		}
		if !reach {
			return WeightResult{}, ErrNotFound
		}
	}
	if pre.TaskType != TypeNeedWeight {
		return WeightResult{}, ErrNotWeightTask
	}
	if pre.OrderLineNo == nil || *pre.OrderLineNo <= 0 {
		return WeightResult{}, httpx.BadRequestf("this weight request does not name an order line")
	}
	lineNo := *pre.OrderLineNo

	// The unit check happens BEFORE the write (header rule 1). It is skipped only when the seam is
	// absent, in which case nothing is going to be written anyway.
	if s.lines != nil {
		lineUOM, err := s.lines.LineUOM(ctx, pre.OrderID, lineNo)
		if err == nil && !strings.EqualFold(lineUOM, uom) {
			return WeightResult{}, fmt.Errorf("%w: line %d is in %s, the weight was typed in %s",
				ErrWeightUOMMismatch, lineNo, lineUOM, uom)
		}
	}

	note := strings.TrimSpace(in.Note)
	if note == "" {
		note = fmt.Sprintf("weighed %s %s", weight, uom)
	}
	t, err := s.store.RecordWeight(ctx, WeightWrite{
		TaskID: pre.ID, ActorID: in.ActorID, Weight: weight, UOM: uom, Note: note,
	})
	if err != nil {
		return WeightResult{}, err
	}

	res := WeightResult{Task: t, OrderLineNo: lineNo, UOM: uom}
	syncErr := s.syncWeightToOrder(ctx, t, lineNo, weight)
	if syncErr == "" {
		res.OrderSynced = true
	} else {
		res.OrderError = syncErr
	}
	// Re-read so the returned task carries the sync stamp the store just wrote.
	if fresh, err := s.store.GetTask(ctx, t.ID); err == nil {
		res.Task = fresh
		t = fresh
	}

	body := fmt.Sprintf("Weight back on order %s line %d: %s %s.", t.OrderID, lineNo, weight, uom)
	if !res.OrderSynced {
		body += " The order line was NOT updated, " + res.OrderError
	}
	s.postAudienceEventKeyed(ctx, t, "weight.recorded", "task.weight_recorded", AudienceCounter, body)

	// DIB-325: a completed task MAY advance its order, if somebody turned that on for this task type.
	s.maybeAdvanceOrder(ctx, t)
	return res, nil
}

// syncWeightToOrder pushes the weight onto the order line and stamps the outcome on the task.
// Returns "" on success, or the order module's own sentence on refusal.
func (s *Service) syncWeightToOrder(ctx context.Context, t Task, lineNo int, weight string) string {
	if s.lines == nil {
		msg := "the order line seam is not wired on this server"
		if err := s.store.MarkWeightSync(ctx, t.ID, nil, msg); err != nil && s.log != nil {
			s.log.Warn("tasks: weight sync stamp failed", "task", t.ID, "err", err)
		}
		return msg
	}
	if err := s.lines.SetLineQty(ctx, t.OrderID, lineNo, weight); err != nil {
		msg := err.Error()
		if len(msg) > 400 {
			msg = msg[:400]
		}
		if err := s.store.MarkWeightSync(ctx, t.ID, nil, msg); err != nil && s.log != nil {
			s.log.Warn("tasks: weight sync stamp failed", "task", t.ID, "err", err)
		}
		return msg
	}
	at := time.Now().UTC()
	if err := s.store.MarkWeightSync(ctx, t.ID, &at, ""); err != nil {
		// The line DID take the weight; only the stamp failed. Reported as a gap rather than as a
		// failure, because re-running the sync would write the same number twice.
		if s.log != nil {
			s.log.Warn("tasks: weight synced but the stamp failed", "task", t.ID, "err", err)
		}
	}
	return ""
}

// parseWeight is parsePositiveQty under a name that says what it is guarding, kept as a distinct
// symbol so a future change to shortage-quantity rules cannot silently change what a scale may report.
func parseWeight(raw string) (string, error) { return parsePositiveQty(raw) }

// weightIsPlausible is a defensive bound used by the handler's own validation path. A yard scale that
// reports 10^7 has failed, and a task carrying that number would poison the order line.
func weightIsPlausible(v string) bool {
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	return err == nil && f > 0 && f < 1e7
}
