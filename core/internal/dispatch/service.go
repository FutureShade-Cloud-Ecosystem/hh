package dispatch

// service.go, the branch fence and the lifecycle guards.
//
// THE FENCE, STATED ONCE. Every read and every write in this file resolves the trusted, server-
// stamped caller (never a header, never a body field), then answers exactly one question: can this
// person reach the BRANCH this row belongs to? Three consequences, and they are deliberate:
//
//   - A SCOPED caller who names no branch gets 403, not "all branches". Enumeration is the failure
//     mode a branch fence exists to prevent, and an empty filter is the easiest way to fall through
//     one. A GLOBAL caller may still list everything. (This is the landedcost.List posture, and it is
//     canonical here.)
//   - A row in a branch the caller cannot reach is 403 when they addressed it by INTERNAL ID, and 404
//     when they addressed it by a printed NUMBER. Those look inconsistent and are not: an id is
//     opaque and unguessable, so 403 leaks nothing, whereas answering 403 to "#RUN-KGN-12" would
//     confirm that number exists at a branch the caller cannot see, which is precisely the
//     enumeration a number lookup invites.
//   - The branch on a new stop is COPIED FROM THE ORDER through the OrderReader seam, never taken
//     from the caller or the request body. A caller cannot file a delivery against a branch by
//     asserting it.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

// Service is the dispatch domain seam. Sibling modules are reached ONLY through the narrow interfaces
// in module.go, injected by the composition root; this package imports neither order, nor pos, nor
// printing.
type Service struct {
	repo   *Repository
	orders OrderReader
	fulfil OrderFulfiller
	sig    SignatureCapturer
	prints PrintEnqueuer
	// gate is the Wave 3 OPTIONAL AR/COD dispatch predicate (model_w3.go). Nil is allow, the field
	// is nil unless the composition root injects Swarm B's seam, and every call goes through
	// s.gateFor, which is nil-safe by construction.
	gate ARDispatchGate
	// The load-board seams (0184). All three are OPTIONAL and all three are nil-safe: a board with no
	// value seam stamps no value, and a board with no stock seam reports that the gap check did not
	// run rather than an all-clear.
	values OrderValueReader
	stock  StockAvailabilityReader
	lines  OrderLineReader
	// picks is the rack-tag seam (module.go). Nil is ALLOW, un-wired, MintWillCall behaves exactly as
	// 0092 shipped it, and every call goes through s.pickStateFor, which is nil-safe by construction.
	picks PickCompletionReader
	// pickHold is the pay before release hold (item 9.3): an order that owes
	// money under it gets no rack tag and no self confirmation. Nil is ALLOW
	// (the additive lane, asserted at boot in the composition root); a wired
	// seam that errors REFUSES (money, the closed direction).
	pickHold PickHoldGate
	// costRoles is the door money fence's rule (cost_gate.go): whether the
	// caller is on the cost and margin list. Nil is REFUSE, the closed
	// direction money takes everywhere in this module.
	costRoles CostRoles
	// stopSource is the narrow read seam behind guardStop, guardRun and the
	// payment hold reads' stops list (cost_gate_w3_test.go names it): one
	// stop, one run, one run's stops. It is the repository; the door money
	// fence's untagged legs override it with a double so CI's databaseless
	// job carries the two hold reads the integration legs cover today.
	stopSource stopRunSource
	// trail is the audit READ seam (session A item 3d). Nil is an empty history, never an error.
	trail PolicyTrailReader
	names ActorNameResolver
	// The second-truck gate's two seams (0195), and NEITHER of them is nil-is-allow, the posture the
	// rest of this list takes. `approvals` unwired means a raise above the quoted freight cannot be
	// filed, so it is REFUSED; `freightWriter` unwired means the order's own freight figure cannot be
	// moved, so a split that would move it is refused too. A gate whose absence lets the gated thing
	// through is not a gate, and both of these guard a figure on a customer's invoice.
	approvals     FreightApprovalFiler
	freightWriter OrderFreightWriter
	audit         audit.Recorder
	log           *slog.Logger
	now           func() time.Time
	// callerFrom resolves the guard-stamped acting staff member every branch-scoped read and write
	// gates against. Defaults to identity's stamped caller; tests override via withCaller. Mirrors
	// order.Service.callerFrom and landedcost.Service.callerFrom.
	callerFrom func(context.Context) (identity.StaffContext, bool)
}

// NewService wires the repository.
func NewService(repo *Repository) *Service {
	return &Service{
		repo:       repo,
		stopSource: repo,
		log:        slog.Default(),
		now:        time.Now,
		callerFrom: identity.StaffContextFromContext,
	}
}

// stopRunSource is the repository's stop and run read seam: everything the
// payment hold reads and their guards resolve through (one stop, one run, one
// run's stops). *Repository satisfies it, and NewService wires exactly that;
// tests substitute an in-memory double (withStopSource) so the reads are
// driven in the untagged suite, the one CI's ci job runs with no database.
type stopRunSource interface {
	GetStop(ctx context.Context, id string) (Stop, error)
	GetRun(ctx context.Context, id string) (Run, error)
	ListStopsForRun(ctx context.Context, runID string) ([]Stop, error)
}

// withStopSource overrides the stop and run read seam for a test double. The
// repository stays the production source; nothing outside a test calls this.
func (s *Service) withStopSource(src stopRunSource) *Service { s.stopSource = src; return s }

// WithOrders injects the order authority seam (branch/status validation at scheduling).
func (s *Service) WithOrders(r OrderReader) *Service { s.orders = r; return s }

// WithFulfiller injects the invoice-handoff seam.
func (s *Service) WithFulfiller(f OrderFulfiller) *Service { s.fulfil = f; return s }

// WithPickState injects the rack-tag seam: "has this order actually been pulled" (module.go).
func (s *Service) WithPickState(p PickCompletionReader) *Service { s.picks = p; return s }

// WithSignatures injects the DIB-386 typed-name attestation seam.
func (s *Service) WithSignatures(c SignatureCapturer) *Service { s.sig = c; return s }

// WithPrinting injects the W1a print_queue seam.
func (s *Service) WithPrinting(p PrintEnqueuer) *Service { s.prints = p; return s }

// WithFreightApprovals injects the approvals-queue seam the second-truck gate files on (0195).
func (s *Service) WithFreightApprovals(f FreightApprovalFiler) *Service { s.approvals = f; return s }

// WithOrderFreight injects the seam that moves an order's own freight figure when a raise is applied.
func (s *Service) WithOrderFreight(w OrderFreightWriter) *Service { s.freightWriter = w; return s }

// WithAudit injects the A-21 Recorder.
func (s *Service) WithAudit(r audit.Recorder) *Service { s.audit = r; return s }

// WithLogger injects the server logger.
func (s *Service) WithLogger(l *slog.Logger) *Service {
	if l != nil {
		s.log = l
	}
	return s
}

// WithClock injects a deterministic clock (tests).
func (s *Service) WithClock(now func() time.Time) *Service {
	if now != nil {
		s.now = now
	}
	return s
}

// withCaller is the test seam for the guard-stamped StaffContext.
func (s *Service) withCaller(f func(context.Context) (identity.StaffContext, bool)) *Service {
	s.callerFrom = f
	return s
}

// caller resolves the trusted acting staff member. An inactive account is refused here rather than at
// each call site: a suspended driver must not be able to sign for a delivery.
func (s *Service) caller(ctx context.Context) (identity.StaffContext, error) {
	if s.callerFrom == nil {
		return identity.StaffContext{}, ErrUnauthorized
	}
	c, ok := s.callerFrom(ctx)
	if !ok || c.UserID == "" {
		return identity.StaffContext{}, ErrUnauthorized
	}
	if c.Status != "" && c.Status != identity.StatusActive {
		return identity.StaffContext{}, ErrForbidden
	}
	return c, nil
}

// requireBranch is the fence for a NAMED branch: the caller must reach it, and it must be named.
//
// An empty branch from a scoped caller is 403, not a silent "everything", see the file header.
func (s *Service) requireBranch(caller identity.StaffContext, branchID string) error {
	if branchID == "" {
		if caller.Scope != identity.ScopeGlobal {
			return ErrForbidden
		}
		return nil
	}
	if !caller.CanAccessBranch(branchID) {
		return ErrForbidden
	}
	return nil
}

// - fleet registry -

// Trucks lists a branch's trucks.
func (s *Service) Trucks(ctx context.Context, branchID string, includeInactive bool) ([]Truck, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.requireBranch(caller, branchID); err != nil {
		return nil, err
	}
	if branchID == "" {
		return nil, httpx.BadRequestf("dispatch: branch_id is required")
	}
	return s.repo.ListTrucks(ctx, branchID, includeInactive)
}

// CreateTruck registers a truck. Manager-gated at the route; the branch fence is re-checked here so
// the authority does not live only in the mux.
func (s *Service) CreateTruck(ctx context.Context, t Truck) (string, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return "", err
	}
	if t.BranchID == "" {
		return "", httpx.BadRequestf("dispatch: branch_id is required")
	}
	if !caller.CanAccessBranch(t.BranchID) {
		return "", ErrForbidden
	}
	// THE BRANCH MUST EXIST, NOT JUST BE NON-EMPTY. CanAccessBranch above returns true for ANY id
	// when the caller is GLOBAL (identity/rbac.go:105-107), so a manager-up global caller could
	// register a truck against an invented uuid, violate `branch_id ... REFERENCES locations(id)`, and
	// receive a 500 out of the driver, which wrote nothing, but distinguished a real yard from an
	// invented one. Wave 0b closed the identical hole on the widget substrate's branch narrowing;
	// this is the closest sibling to it in the tree and it is one read, because the repository
	// already has the read: BranchName answers "" for a row that is not there.
	//
	// IT SITS AFTER THE FENCE, WHICH IS THE SECURITY ARGUMENT. A scoped caller's invented id is
	// refused by CanAccessBranch and never reaches this lookup, so no scoped caller can learn from
	// it whether a yard exists. Only a caller who may reach every branch gets here, and they can
	// already list every branch.
	//
	// AN UNREADABLE LOOKUP IS NOT A REFUSAL. err != nil means "I could not check", the bare service
	// the unit tests build has no pool at all, and BranchName answers ("", errNoDB) there, so the
	// guard fires only on a definite "" with no error, and a database that blinked cannot stop a yard
	// registering a truck.
	if name, err := s.repo.BranchName(ctx, t.BranchID); err == nil && name == "" {
		return "", httpx.BadRequestf("dispatch: no branch with that id")
	}
	t.Code = strings.TrimSpace(t.Code)
	if t.Code == "" {
		return "", httpx.BadRequestf("dispatch: a truck needs a code")
	}
	t.TruckType = strings.ToUpper(strings.TrimSpace(t.TruckType))
	if t.TruckType == "" {
		t.TruckType = "FLATBED"
	}
	if !truckTypes[t.TruckType] {
		return "", httpx.BadRequestf("dispatch: unknown truck_type %q", t.TruckType)
	}
	if t.CapacityKg != nil && *t.CapacityKg < 0 {
		return "", httpx.BadRequestf("dispatch: capacity_kg must not be negative")
	}
	id, err := s.repo.InsertTruck(ctx, t)
	if err != nil {
		return "", asConflict(err, "dispatch: a truck with that code already exists at this branch")
	}
	s.record(ctx, audit.SensitiveAccess{Action: "DISPATCH_TRUCK_CREATED", EntityType: "dispatch_truck", EntityID: id})
	return id, nil
}

// UpdateTruck rewrites a truck's registry facts: the fields the create takes, plus active or not (a
// truck the yard has sold or parked stays on the books, invisible to the picker). Manager-gated at
// the route; the branch fence is re-checked here on the ROW's branch so the authority does not live
// only in the mux, exactly like every id-addressed endpoint this module serves (guardStop's rule:
// 403, not 404, the id is opaque so refusing loudly leaks nothing).
//
// THE BRANCH DOES NOT MOVE. The body carries branch_id because it is the row the caller read, and a
// branch_id that names another yard is refused in words: a truck's runs, its crew assignments and
// its capacity story all hang off the yard it lives at, and a registry that silently re-homes a
// truck strands them. Retire it here and register it there.
//
// IDEMPOTENT BY CONSTRUCTION. The write is an absolute SET the create's validations gate, so the
// same body twice leaves the same row and answers 200 both times; the only conflict is a DIFFERENT
// code colliding with another truck at the branch, the create's own 409.
func (s *Service) UpdateTruck(ctx context.Context, truckID string, in Truck) (Truck, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return Truck{}, err
	}
	row, err := s.repo.GetTruck(ctx, truckID)
	if err != nil {
		return Truck{}, err
	}
	if !caller.CanAccessBranch(row.BranchID) {
		return Truck{}, ErrForbidden
	}
	if in.BranchID == "" {
		return Truck{}, httpx.BadRequestf("dispatch: branch_id is required")
	}
	if in.BranchID != row.BranchID {
		return Truck{}, httpx.BadRequestf("dispatch: a truck's branch does not move; retire it here and register it at the other yard")
	}
	in.Code = strings.TrimSpace(in.Code)
	if in.Code == "" {
		return Truck{}, httpx.BadRequestf("dispatch: a truck needs a code")
	}
	in.TruckType = strings.ToUpper(strings.TrimSpace(in.TruckType))
	if in.TruckType == "" {
		in.TruckType = "FLATBED"
	}
	if !truckTypes[in.TruckType] {
		return Truck{}, httpx.BadRequestf("dispatch: unknown truck_type %q", in.TruckType)
	}
	if in.CapacityKg != nil && *in.CapacityKg < 0 {
		return Truck{}, httpx.BadRequestf("dispatch: capacity_kg must not be negative")
	}
	updated, err := s.repo.UpdateTruck(ctx, truckID, in)
	if err != nil {
		return Truck{}, asConflict(err, "dispatch: a truck with that code already exists at this branch")
	}
	s.record(ctx, audit.SensitiveAccess{Action: "DISPATCH_TRUCK_UPDATED", EntityType: "dispatch_truck", EntityID: truckID})
	return updated, nil
}

// Drivers lists a branch's drivers.
func (s *Service) Drivers(ctx context.Context, branchID string, includeInactive bool) ([]Driver, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.requireBranch(caller, branchID); err != nil {
		return nil, err
	}
	if branchID == "" {
		return nil, httpx.BadRequestf("dispatch: branch_id is required")
	}
	return s.repo.ListDrivers(ctx, branchID, includeInactive)
}

// CreateDriver registers a driver.
func (s *Service) CreateDriver(ctx context.Context, d Driver) (string, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return "", err
	}
	if d.BranchID == "" {
		return "", httpx.BadRequestf("dispatch: branch_id is required")
	}
	if !caller.CanAccessBranch(d.BranchID) {
		return "", ErrForbidden
	}
	// Same guard, same order, same words as CreateTruck directly above, one vocabulary per surface,
	// which is the rule TestBoardGaps_RefusesAMissingBranchByNameLikeItsSiblings pins for the three
	// board reads (dispatch/loadboard_test.go:837-848).
	if name, err := s.repo.BranchName(ctx, d.BranchID); err == nil && name == "" {
		return "", httpx.BadRequestf("dispatch: no branch with that id")
	}
	d.Name = strings.TrimSpace(d.Name)
	if d.Name == "" {
		return "", httpx.BadRequestf("dispatch: a driver needs a name")
	}
	id, err := s.repo.InsertDriver(ctx, d)
	if err != nil {
		return "", err
	}
	s.record(ctx, audit.SensitiveAccess{Action: "DISPATCH_DRIVER_CREATED", EntityType: "dispatch_driver", EntityID: id})
	return id, nil
}

// UpdateDriver rewrites a driver's registry facts: the fields the create takes (including the login
// link, a casual driver who later gets a staff account), plus active or not. The same fence, the
// same branch-does-not-move rule and the same idempotent-by-construction write as UpdateTruck
// directly above; one vocabulary per surface.
func (s *Service) UpdateDriver(ctx context.Context, driverID string, in Driver) (Driver, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return Driver{}, err
	}
	row, err := s.repo.GetDriver(ctx, driverID)
	if err != nil {
		return Driver{}, err
	}
	if !caller.CanAccessBranch(row.BranchID) {
		return Driver{}, ErrForbidden
	}
	if in.BranchID == "" {
		return Driver{}, httpx.BadRequestf("dispatch: branch_id is required")
	}
	if in.BranchID != row.BranchID {
		return Driver{}, httpx.BadRequestf("dispatch: a driver's branch does not move; retire them here and register them at the other yard")
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return Driver{}, httpx.BadRequestf("dispatch: a driver needs a name")
	}
	updated, err := s.repo.UpdateDriver(ctx, driverID, in)
	if err != nil {
		return Driver{}, err
	}
	s.record(ctx, audit.SensitiveAccess{Action: "DISPATCH_DRIVER_UPDATED", EntityType: "dispatch_driver", EntityID: driverID})
	return updated, nil
}

// - the board -

// Board is the day view: one branch, one date, its runs and its stops. Stops with no run come back
// separately as Unassigned, the board's first column, and where a stop is born.
func (s *Service) Board(ctx context.Context, branchID, date string) (Board, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return Board{}, err
	}
	if err := s.requireBranch(caller, branchID); err != nil {
		return Board{}, err
	}
	if branchID == "" {
		return Board{}, httpx.BadRequestf("dispatch: branch_id is required")
	}
	if date == "" {
		date = s.now().UTC().Format("2006-01-02")
	}
	if !validDate(date) {
		return Board{}, httpx.BadRequestf("dispatch: date must be YYYY-MM-DD")
	}
	runs, err := s.repo.ListRuns(ctx, branchID, date)
	if err != nil {
		return Board{}, err
	}
	stops, err := s.repo.ListStops(ctx, branchID, date)
	if err != nil {
		return Board{}, err
	}
	board := Board{BranchID: branchID, Date: date, Runs: runs, Stops: stops, Unassigned: []Stop{}}
	for _, st := range stops {
		if st.RunID == "" {
			board.Unassigned = append(board.Unassigned, st)
		}
	}
	return board, nil
}

// - scheduling -

// Schedule turns a confirmed order into a delivery stop on a date.
//
// THE ORDER IS THE AUTHORITY. Its branch is copied onto the stop (the caller does not get to assert
// one) and its status decides whether it may be scheduled at all. A DRAFT order has not been agreed
// to; a CANCELLED one is not going anywhere; a CLOSED one already went. Scheduling any of those
// produces a stop that will never be deliverable, so it is refused at the door with a message naming
// the status rather than left to fail three screens later.
func (s *Service) Schedule(ctx context.Context, in ScheduleInput) (Stop, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return Stop{}, err
	}
	if s.orders == nil {
		return Stop{}, fmt.Errorf("dispatch: order seam is not wired")
	}
	// DIB-483 / M3-G8: order_id accepts what the counter actually has in hand, the visible #SO
	// number, with or without its '#', as well as a pasted UUID. One resolver (orderref.go), the
	// same one the POS lookup uses, branch-scoped at resolution.
	orderRef, err := s.resolveOrderRef(ctx, caller, in.OrderID)
	if err != nil {
		return Stop{}, err
	}
	date := strings.TrimSpace(in.ScheduledDate)
	if date == "" {
		return Stop{}, httpx.BadRequestf("dispatch: scheduled_date is required (YYYY-MM-DD)")
	}
	if !validDate(date) {
		return Stop{}, httpx.BadRequestf("dispatch: scheduled_date must be YYYY-MM-DD")
	}
	if err := validCoords(in.Latitude, in.Longitude); err != nil {
		return Stop{}, err
	}
	brief, err := s.orders.Brief(ctx, orderRef)
	if err != nil {
		return Stop{}, ErrNotFound // a missing/garbage order id never distinguishes itself
	}
	// The fence: the caller must reach the ORDER's branch. Note the ordering, existence is resolved
	// first, so a cross-branch order and a nonexistent one are told apart only by someone who can
	// already see both.
	if !caller.CanAccessBranch(brief.BranchID) {
		return Stop{}, ErrForbidden
	}
	switch brief.Status {
	case "DRAFT":
		return Stop{}, conflictf("dispatch: confirm the order before scheduling a delivery for it")
	case "CANCELLED", "CLOSED":
		return Stop{}, conflictf("dispatch: order is %s and cannot be scheduled", brief.Status)
	}
	live, err := s.repo.LiveStopsForOrder(ctx, brief.ID)
	if err != nil {
		return Stop{}, err
	}
	if len(live) > 0 {
		return Stop{}, conflictf("dispatch: this order already has a delivery scheduled for %s", live[0].ScheduledDate)
	}
	// 0184/0185, REQUESTED vs COMMITTED. An omitted state normalises to REQUESTED: a caller who did
	// not say they were promising anything has not promised anything, and that is the direction this
	// distinction has to fail in. `date_state` is 0184's name for the same field and is still read.
	asked := in.CommitState
	if asked == "" {
		asked = in.DateState
	}
	commitState, err := ValidateCommitState(asked)
	if err != nil {
		return Stop{}, err
	}
	// ITEM D. A salesperson may put a date on the board and may NEVER stand behind one. A request that
	// asks to be born COMMITTED is refused here, the forged commit, rather than trusted because the
	// UI would not have offered it.
	if commitState == CommitCommitted {
		if err := canCommitLoad(caller); err != nil {
			return Stop{}, err
		}
	}
	policy := s.PolicyFor(ctx, brief.BranchID)
	if !WithinHorizon(date, s.now(), policy.CommitHorizonWeeks) {
		return Stop{}, settings.Errf(http.StatusUnprocessableEntity,
			"dispatch: %s is outside the %d-week planning horizon", date, policy.CommitHorizonWeeks)
	}
	requested := strings.TrimSpace(in.RequestedDate)
	if requested != "" && !validDate(requested) {
		return Stop{}, httpx.BadRequestf("dispatch: requested_date must be YYYY-MM-DD")
	}
	if requested == "" && commitState == CommitRequested {
		requested = date
	}
	// 0184, the load's VALUE is stamped from the ORDER, exactly as its branch is. The request never
	// gets to say what a load is worth. An unwired or silent seam leaves it NULL, which the card
	// prints as a dash and never as zero.
	var valueCents *int64
	if s.values != nil {
		if cents, known, verr := s.values.OrderValueCents(ctx, brief.ID); verr != nil {
			s.log.Warn("dispatch: order value read failed; the load ships unpriced", "order_id", brief.ID, "err", verr)
		} else if known {
			v := cents
			valueCents = &v
		}
	}
	stop, err := s.repo.InsertStop(ctx, Stop{
		BranchID: brief.BranchID, OrderID: brief.ID, ScheduledDate: date,
		TimeWindow: in.TimeWindow, ContactName: in.ContactName, ContactPhone: in.ContactPhone,
		AddressLine: in.AddressLine, City: in.City, PostalCode: in.PostalCode,
		Latitude: in.Latitude, Longitude: in.Longitude, Instructions: in.Instructions,
		CommitState: commitState, RequestedDate: requested,
		WeightKg: in.WeightKg, VolumeM3: in.VolumeM3, PalletPositions: in.PalletPositions,
		ValueCents: valueCents,
	}, caller.UserID, s.now())
	if err != nil {
		return Stop{}, asConflict(err, "dispatch: this order already has a live delivery stop")
	}
	s.record(ctx, audit.SensitiveAccess{
		Action: "DELIVERY_SCHEDULED", EntityType: "delivery_stop", EntityID: stop.ID,
		Extra: map[string]any{"order_id": brief.ID, "date": date, "commit_state": commitState},
	})
	return stop, nil
}

// StopDetail reads one stop plus its POD photo metadata, fenced on the stop's own branch.
func (s *Service) StopDetail(ctx context.Context, stopID string) (StopDetail, error) {
	stop, err := s.guardStop(ctx, stopID)
	if err != nil {
		return StopDetail{}, err
	}
	photos, err := s.repo.ListPhotoMeta(ctx, stop.ID)
	if err != nil {
		return StopDetail{}, err
	}
	// W3-C: the classification travels with the drawer read, so a caller that just SET it reads the
	// value back rather than an empty field, the gap the walkthrough surfaced.
	class, note, weight, err := s.repo.StopMTO(ctx, stop.ID)
	if err != nil {
		return StopDetail{}, err
	}
	return StopDetail{Stop: stop, Photos: photos, MTO: StopMTO{Class: class, Note: note, WeightKg: weight}}, nil
}

// guardStop resolves a stop by internal id and applies the fence. 403 (not 404) on a cross-branch
// reach: the id is opaque, so refusing loudly leaks nothing and tells an operator with the wrong
// branch selected what actually went wrong.
func (s *Service) guardStop(ctx context.Context, stopID string) (Stop, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return Stop{}, err
	}
	stop, err := s.stopSource.GetStop(ctx, stopID)
	if err != nil {
		return Stop{}, err
	}
	if !caller.CanAccessBranch(stop.BranchID) {
		return Stop{}, ErrForbidden
	}
	return stop, nil
}

// guardRun is guardStop for runs.
func (s *Service) guardRun(ctx context.Context, runID string) (Run, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return Run{}, err
	}
	run, err := s.stopSource.GetRun(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	if !caller.CanAccessBranch(run.BranchID) {
		return Run{}, ErrForbidden
	}
	return run, nil
}

// - runs -

// CreateRun opens a run on a branch/date. Truck and driver are optional now and required at dispatch.
func (s *Service) CreateRun(ctx context.Context, in CreateRunInput) (Run, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return Run{}, err
	}
	if in.BranchID == "" {
		return Run{}, httpx.BadRequestf("dispatch: branch_id is required")
	}
	if !caller.CanAccessBranch(in.BranchID) {
		return Run{}, ErrForbidden
	}
	if !validDate(in.ScheduledDate) {
		return Run{}, httpx.BadRequestf("dispatch: scheduled_date must be YYYY-MM-DD")
	}
	// A truck or driver from ANOTHER branch is refused: a run is a branch's crew and a branch's
	// vehicle, and the fence would be decorative if the assignment could cross it.
	if err := s.assertCrewInBranch(ctx, in.BranchID, in.TruckID, in.DriverID); err != nil {
		return Run{}, err
	}
	run, err := s.repo.InsertRun(ctx, Run{
		BranchID: in.BranchID, ScheduledDate: in.ScheduledDate,
		TruckID: in.TruckID, DriverID: in.DriverID, Notes: in.Notes,
	}, caller.UserID)
	if err != nil {
		return Run{}, err
	}
	s.record(ctx, audit.SensitiveAccess{Action: "DELIVERY_RUN_CREATED", EntityType: "delivery_run", EntityID: run.ID})
	return run, nil
}

func (s *Service) assertCrewInBranch(ctx context.Context, branchID, truckID, driverID string) error {
	if truckID != "" {
		t, err := s.repo.GetTruck(ctx, truckID)
		if err != nil {
			return httpx.BadRequestf("dispatch: unknown truck")
		}
		if t.BranchID != branchID {
			return conflictf("dispatch: that truck belongs to another branch")
		}
	}
	if driverID != "" {
		d, err := s.repo.GetDriver(ctx, driverID)
		if err != nil {
			return httpx.BadRequestf("dispatch: unknown driver")
		}
		if d.BranchID != branchID {
			return conflictf("dispatch: that driver belongs to another branch")
		}
	}
	return nil
}

// Runs lists a branch's runs for a date.
func (s *Service) Runs(ctx context.Context, branchID, date string) ([]Run, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.requireBranch(caller, branchID); err != nil {
		return nil, err
	}
	if branchID == "" {
		return nil, httpx.BadRequestf("dispatch: branch_id is required")
	}
	if date != "" && !validDate(date) {
		return nil, httpx.BadRequestf("dispatch: date must be YYYY-MM-DD")
	}
	return s.repo.ListRuns(ctx, branchID, date)
}

// RunDetail reads a run with its stops and, when the branch has a depot coordinate, the straight-line
// distance estimate. The estimate is OMITTED (not zeroed) when no stop is geocoded, see geo.go.
func (s *Service) RunDetail(ctx context.Context, runID string) (RunDetail, error) {
	run, err := s.guardRun(ctx, runID)
	if err != nil {
		return RunDetail{}, err
	}
	return s.runDetail(ctx, run)
}

func (s *Service) runDetail(ctx context.Context, run Run) (RunDetail, error) {
	stops, err := s.repo.ListStopsForRun(ctx, run.ID)
	if err != nil {
		return RunDetail{}, err
	}
	d := RunDetail{Run: run, Stops: stops}
	pts := make([]Point, 0, len(stops))
	for _, st := range stops {
		if st.Latitude != nil && st.Longitude != nil {
			pts = append(pts, Point{Lat: *st.Latitude, Lng: *st.Longitude})
		}
	}
	d.GeocodedStops = len(pts)
	d.UngeocodedStops = len(stops) - len(pts)
	depot, ok, err := s.repo.Depot(ctx, run.BranchID)
	if err != nil {
		return RunDetail{}, err
	}
	if ok && len(pts) > 0 {
		km := RouteKm(depot, pts)
		d.EstimatedKm = &km
	}
	return d, nil
}

// RunByNo resolves a run by its printed number.
//
// 404, NEVER 403, ON A CROSS-BRANCH MATCH. A number is a guessable, human-shaped key: answering 403
// for "#RUN-KGN-12" would confirm to a Trenton-scoped caller that Kingston has a twelfth run. The
// only safe answer to "I found it but you may not see it" for a NUMBER is "no such run".
func (s *Service) RunByNo(ctx context.Context, runNo string) (RunDetail, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return RunDetail{}, err
	}
	runNo = strings.TrimSpace(runNo)
	if runNo == "" {
		return RunDetail{}, ErrNotFound
	}
	if !strings.HasPrefix(runNo, "#") {
		runNo = "#" + runNo // the number is printed with the hash; people type it without
	}
	run, err := s.repo.GetRunByNo(ctx, runNo)
	if err != nil {
		return RunDetail{}, err
	}
	if !caller.CanAccessBranch(run.BranchID) {
		return RunDetail{}, ErrNotFound
	}
	return s.runDetail(ctx, run)
}

// AssignCrew sets a PLANNED run's truck and driver.
func (s *Service) AssignCrew(ctx context.Context, runID, truckID, driverID string) (Run, error) {
	run, err := s.guardRun(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	if run.Status != RunPlanned {
		return Run{}, conflictf("dispatch: a %s run cannot be re-crewed", run.Status)
	}
	// 0185, swapping the truck under a COMMITTED load changes what the load IS (a different capacity,
	// a different gauge, a different answer to "can this go"), so it is a load change and the
	// commitment holds it.
	if err := s.assertRunEditable(ctx, run); err != nil {
		return Run{}, err
	}
	if err := s.assertCrewInBranch(ctx, run.BranchID, truckID, driverID); err != nil {
		return Run{}, err
	}
	ok, err := s.repo.AssignRunCrew(ctx, run.ID, truckID, driverID)
	if err != nil {
		return Run{}, err
	}
	if !ok {
		return Run{}, ErrIllegalTransition // someone dispatched it between the read and the write
	}
	return s.repo.GetRun(ctx, run.ID)
}

// AssignStop puts a stop on a run at a position (StopNo 0 appends), or takes it off with an empty
// RunID. Both the stop and the run are fenced, and they must share a branch.
func (s *Service) AssignStop(ctx context.Context, stopID string, in AssignInput) (Stop, error) {
	stop, err := s.guardStop(ctx, stopID)
	if err != nil {
		return Stop{}, err
	}
	if stop.Status != StopReady {
		return Stop{}, conflictf("dispatch: a %s stop cannot be re-assigned", stop.Status)
	}
	// THE CAPABILITY FIRST, THE STATE SECOND, and the order is chosen for the SENTENCE. Every
	// assignment either commits a load or changes a commitment, so both are dispatch's business -
	// and a salesperson who is refused should be told they may request rather than told to go and
	// reopen something they could not reopen either. Found in the walkthrough: a salesperson's
	// hand-made assign against an already-committed load answered "reopen it first", which is advice
	// they cannot take.
	if caller, cerr := s.caller(ctx); cerr == nil {
		if err := canCommitLoad(caller); err != nil {
			return Stop{}, err
		}
	}
	// 0185, THE COMMITMENT, ENFORCED ON THE WRITE (see assertLoadChangeable's header and NOTES).
	//
	// A load somebody has COMMITTED is one the yard is picking against and the customer has a window
	// for. Taking it off its truck, or moving it to another, is a change to that and is gated.
	//
	// A REQUESTED load coming off the staging lane is not a change to anything, it is the day being
	// built, and putting it on a truck IS the commit. That is the 06:45 work this whole surface exists
	// for, and 0184's clock-shaped reading of the rule stopped it: a 48-hour window measured back from
	// a delivery day covers today AND tomorrow, so a dispatcher could not put tomorrow's flagstone on
	// a truck this morning without hunting for a manager. The integration suite caught it. Grading the
	// COMMITMENT instead of the clock fixes it at the root rather than by carve-out.
	if err := s.assertLoadChangeable(ctx, stop); err != nil {
		return Stop{}, err
	}
	stopNo := in.StopNo
	if in.RunID != "" {
		run, err := s.guardRun(ctx, in.RunID)
		if err != nil {
			return Stop{}, err
		}
		if run.BranchID != stop.BranchID {
			return Stop{}, conflictf("dispatch: that run belongs to another branch")
		}
		// THE RECEIVING TRUCK IS NOT GRADED, and that changed with 0185. Under the clock reading, a
		// truck inside the window refused new loads, which is exactly the 06:45 refusal the previous
		// lane's integration run caught. Adding a load to a truck does not change the loads already on
		// it, so their commitments have nothing to say about it. The load BEING MOVED is graded above,
		// and that is the whole of the rule.
		//
		// The capability that makes this a commit is checked at the top of this function, before the
		// load's own state, so the refusal a salesperson reads is about them and not about the load.
		if run.Status != RunPlanned {
			return Stop{}, conflictf("dispatch: stops cannot be added to a %s run", run.Status)
		}
		if run.ScheduledDate != stop.ScheduledDate {
			return Stop{}, conflictf("dispatch: the stop is scheduled for %s and the run is %s", stop.ScheduledDate, run.ScheduledDate)
		}
		if stopNo <= 0 {
			if stopNo, err = s.repo.NextStopNo(ctx, run.ID); err != nil {
				return Stop{}, err
			}
		}
	} else {
		stopNo = 0
	}
	ok, err := s.repo.AssignStop(ctx, stop.ID, in.RunID, stopNo)
	if err != nil {
		return Stop{}, err
	}
	if !ok {
		return Stop{}, ErrIllegalTransition
	}
	// PUTTING A LOAD ON A TRUCK IS THE COMMITMENT. A salesperson asked for a date; the moment a
	// dispatcher puts that load on a truck for it, somebody has stood behind it, so the state moves
	// with the act rather than waiting for a second button nobody would press. That is the whole
	// unification (0185): there is no separate freeze to switch on afterwards.
	if in.RunID != "" && stop.CommitState == CommitRequested {
		caller, cerr := s.caller(ctx)
		if cerr == nil {
			if err := s.repo.CommitStopDate(ctx, stop.ID, "", caller.UserID, s.now()); err != nil {
				s.log.Warn("dispatch: load assigned but the date did not commit", "stop_id", stop.ID, "err", err)
			} else {
				s.record(ctx, audit.SensitiveAccess{
					Action: "DELIVERY_DATE_COMMITTED", EntityType: "delivery_stop", EntityID: stop.ID,
					Extra: map[string]any{"via": "assignment", "run_id": in.RunID},
				})
			}
		}
	}
	return s.repo.GetStop(ctx, stop.ID)
}

// DispatchRun sends the truck: PLANNED → DISPATCHED, and every READY stop on it → LOADED.
//
// THE CREW GUARD. A run with no truck or no driver cannot be dispatched, because "who took it" is the
// first question anyone asks about a delivery and a run that cannot answer it is not a record of
// anything. 0090's CHECK enforces the same rule at the storage layer; this is where the operator gets
// a sentence instead of a constraint name.
func (s *Service) DispatchRun(ctx context.Context, runID string) (RunDetail, error) {
	run, err := s.guardRun(ctx, runID)
	if err != nil {
		return RunDetail{}, err
	}
	if !runCanTransition(run.Status, RunDispatched) {
		return RunDetail{}, ErrIllegalTransition
	}
	if run.TruckID == "" || run.DriverID == "" {
		return RunDetail{}, ErrCrewRequired
	}
	stops, err := s.repo.ListStopsForRun(ctx, run.ID)
	if err != nil {
		return RunDetail{}, err
	}
	if len(stops) == 0 {
		return RunDetail{}, conflictf("dispatch: an empty run has nothing to dispatch")
	}
	// W3-C: the optional AR/COD gate. Nil gate = allow, so this is a no-op on every deployment that
	// has not wired Swarm B's predicate (service_w3.go states the reasoning for that default).
	if err := s.assertGateAllowsRun(ctx, stops); err != nil {
		return RunDetail{}, err
	}
	at := s.now().UTC()
	ok, err := s.repo.UpdateRunStatus(ctx, run.ID, run.Status, RunDispatched, at)
	if err != nil {
		return RunDetail{}, err
	}
	if !ok {
		return RunDetail{}, ErrIllegalTransition
	}
	// Loading is what dispatching MEANS: the stops went on the truck. Each stop is still moved through
	// the guarded per-stop write, so a stop someone had already cancelled is left alone rather than
	// resurrected by the bulk move.
	for _, st := range stops {
		if st.Status != StopReady {
			continue
		}
		if _, err := s.repo.UpdateStopStatus(ctx, st.ID, StopReady, StopLoaded, "", at); err != nil {
			return RunDetail{}, err
		}
	}
	s.record(ctx, audit.SensitiveAccess{Action: "DELIVERY_RUN_DISPATCHED", EntityType: "delivery_run", EntityID: run.ID})
	return s.RunDetail(ctx, run.ID)
}

// - closing a run, and who is allowed to -

// stopIsResolved reports whether a stop has an ANSWER RECORDED ON IT, DELIVERED or FAILED, the
// driver's two outcomes and nothing else. This is the LITERAL SET the ruling names, and it is the
// precondition a DRIVER closing their own run must satisfy.
//
// - THE THREE-STATUS READING THIS REPLACES, AND WHY IT WAS WRONG -
//
// The first draft of this function counted CANCELLED as resolved and argued for it at length. The
// argument was that "a cancelled stop is DISPATCH withdrawing the stop from the day ... it is not a
// thing a driver can move (the matrix gives CANCELLED no successors)". Every clause of that is about
// a premise, only dispatch cancels, and the premise is FALSE IN THIS TREE. Two places disprove it:
//
// THE MATRIX, transitions.go:19-20. READY and LOADED both list StopCancelled as a legal successor.
// CANCELLED has no successors, which says a cancelled stop cannot be moved ONWARD; it says nothing
// whatever about who may move a stop INTO it.
//
// THE RUN SHEET HH DRIVE RENDERS, dispatch-driver-run.ts:806-827. It draws the DRIVER a confirming
// "Cancel the stop" control, and the header above that confirm records why it needed one, "One
// gloved mis-tap in a cab cancelled a customer's delivery."
//
// So the loophole the wide reading opened, measured on a live run: a driver cancels stop 1, stop 2
// and stop 3, taps Complete, and closes the day COMPLETED with nothing delivered, nothing failed and
// NO REASON RECORDED ANYWHERE. FAILED cannot do that, the server refuses a blank reason, which is
// exactly why the run sheet collects one in a sheet, and a failure triggers the handback that frees
// the order back to the yard. CANCELLED has no reason column and no handback. It is the one outcome
// on this surface that records nothing, and it was the one outcome the close-out counted as an answer.
//
// - THE CONSEQUENCE, STATED RATHER THAN HIDDEN -
//
// A RUN CARRYING A CANCELLED STOP CANNOT BE CLOSED BY THE DRIVER, SO DISPATCH CLOSES IT. That is not
// a side effect to apologise for, it is the point: a cancel is the one outcome nobody wrote a reason
// for, and it deserves a dispatcher's eye before the day is filed. The driver is refused in a
// sentence that names the stop and says who to ask (everyStopHasAnAnswer, below) rather than being
// left tapping a button that will never take.
//
// It also means this wave narrows a driver's reach ONE MORE TIME than its own header claimed, the
// ruling already removed "any run at a branch they reach" in exchange for "their own", and this
// removes "their own, closed over stops they cancelled themselves". Both narrowings are deliberate,
// and neither costs any other role a byte: see stopIsSettled.
func stopIsResolved(status string) bool {
	return status == StopDelivered || status == StopFailed
}

// stopIsSettled reports whether a stop is nobody's outstanding work, resolved, OR withdrawn from the
// day by a cancel. This is EVERYBODY'S precondition and it is unchanged by the paragraphs above: it
// is exactly the set OpenStopCount's SQL has always spelled (`status NOT IN
// ('DELIVERED','FAILED','CANCELLED')`), so a dispatcher, a yard manager or a controller who could
// close a run yesterday closes it today, including the run a driver just got refused on.
//
// TWO PREDICATES IS THE HONEST SHAPE HERE, not a duplication. The old code had one because it
// believed one sentence covered both readers; it does not. "Is there work still outstanding" and "did
// somebody record an answer" are different questions, they have different answers for exactly one
// status, and that status is the whole defect. They are written as one function calling the other so
// the relationship, settled is resolved PLUS the withdrawal, cannot drift into two lists.
func stopIsSettled(status string) bool {
	return stopIsResolved(status) || status == StopCancelled
}

// unsettledStop returns the first stop still IN FLIGHT, in stop order, the one everybody's gate
// refuses on. FIRST rather than a count, because the refusal names it and "stop 3 is on the truck" is
// a thing a driver can act on where "2 stops are not finished yet" is a thing they have to go and
// look up.
func unsettledStop(stops []Stop) (Stop, bool) {
	for _, st := range stops {
		if !stopIsSettled(st.Status) {
			return st, true
		}
	}
	return Stop{}, false
}

// unresolvedStop returns the first stop in stop order with NO ANSWER RECORDED on it, the driver's
// narrower question. It is deliberately a second walk rather than a flag on the first: the two gates
// fire at different moments, refuse in different sentences, and a caller that had to pass a predicate
// in would be a caller that could pass the wrong one.
//
// Reached only after the settled gate has already passed, so in this tree the stop it finds is always
// a CANCELLED one. It is written against the predicate rather than against the literal status anyway,
// so that a fourth terminal status added to 0090 and to OpenStopCount's SQL lands on the driver's
// side of this fence by default, narrow, refusing, and visible, rather than silently counting as an
// answer nobody recorded.
func unresolvedStop(stops []Stop) (Stop, bool) {
	for _, st := range stops {
		if !stopIsResolved(st.Status) {
			return st, true
		}
	}
	return Stop{}, false
}

// unresolvedWord names why a stop is not finished, in the words the person reading it would use. It
// is deliberately NOT stopStateWord (service_loadboard.go): that map answers "why can this not be
// committed", so its phrasings are all "already …", and "stop 3 is already on the road" is a sentence
// about the wrong thing. Same degrade-honestly default arm, so a status this build does not know
// reads as strange rather than as a confident wrong word.
func unresolvedWord(status string) string {
	switch status {
	case StopReady:
		return "not loaded yet"
	case StopLoaded:
		return "on the truck"
	case StopEnRoute:
		return "still en route"
	default:
		return strings.ToLower(strings.ReplaceAll(status, "_", " "))
	}
}

// completionAuthority answers "may this caller close this run" AS A QUESTION OVER VALUES, so the rule
// can be pinned by a table test rather than only by a fixture with a truck in it. driverRecordID is
// the dispatch_drivers row linked to the caller's login, or "" when they have none.
//
// - THE RULING THIS ENCODES (commander, 2 Sep 2026) -
//
// A DRIVER MAY COMPLETE THEIR OWN RUN. The run sheet drew no Complete button until now, and its
// reason was right at the time: POST /runs/{id}/complete was requireStaff and nothing more, so
// "drawing the button because the route happens to accept it is exactly the reasoning
// TestCanUnlockLoad_IsACapabilityCheckAndNotAButton exists to refuse". The answer is not to keep the
// button off a surface whose whole job is finishing a day, it is to give the route an authority
// worth drawing a button against. That authority is here.
//
// - WHY THIS TIGHTENS DRIVERS AND LOOSENS NOBODY -
//
// Read the old code and a driver could ALREADY close any run at a branch they reach: requireStaff at
// the mux, then guardRun's branch fence, and a DRIVERS role is BRANCH-scoped with a grant on the yard
// they work out of. Nothing asked whether the run was theirs. So the new rule REMOVES authority a
// driver has today (someone else's run) in exchange for the one they should have had all along (their
// own), and every other role's reach is byte-for-byte what it was: a dispatcher, a yard manager or a
// controller who could close a run yesterday closes it today, unchanged.
//
// - WHY IT LIVES IN THE SERVICE -
//
// SavePolicy's header states the rule and it holds here: "an authority that lives only in a route
// registration is an authority one refactor away from gone." The mux cannot express "the driver whose
// run this is", it has no run, no driver record and no ownership, so the mux does not pretend to,
// and the route stays requireStaff exactly as it was.
func completionAuthority(caller identity.StaffContext, run Run, driverRecordID string) error {
	if caller.RoleCode != identity.RoleDrivers {
		return nil
	}
	// A staff login carrying the driver ROLE but no driver RECORD is not a driver as far as this
	// module is concerned, MyRuns says the same thing to the same person ("you are not set up as a
	// driver") and there is no run they could own.
	if driverRecordID == "" {
		return ErrForbidden
	}
	if run.DriverID == "" || run.DriverID != driverRecordID {
		return ErrForbidden
	}
	return nil
}

// CompleteRun closes a run once every stop on it has been answered for.
//
// THREE GATES, IN THIS ORDER, AND THE ORDER IS THE POINT. Authority first: a driver who does not own
// this run is refused before the run's stops are read, so a 403 can never be told apart from a 409 by
// timing or by which message comes back. Then the precondition that applies to EVERYBODY, a
// dispatcher cannot close a run with a stop still on the road either, and never could. Then, for a
// driver and only for a driver, the narrower one: every stop DELIVERED or FAILED, because those are
// the two outcomes a driver has and they are the two that record something. stopIsResolved's header
// carries the argument and the consequence in full; the consequence in one line is that a run
// carrying a cancelled stop is closed by dispatch rather than from the truck.
//
// THE THIRD GATE IS THE ONLY ONE THAT DISCRIMINATES BY ROLE, and it discriminates in the direction
// that takes authority away rather than granting it. No caller can close anything today that could
// not be closed yesterday. A driver can close strictly less.
//
// DISPATCH RETAINS THE REOPEN. Nothing here is a one-way door for the person who pressed it: closing
// is audited with the role that did it, and the run's loads remain reopenable by a dispatch manager
// on the ordinary /runs/{id}/reopen path under the dealer's reopen_policy. A driver closing their own
// day therefore costs dispatch no authority at all, it costs them a phone call.
func (s *Service) CompleteRun(ctx context.Context, runID string) (Run, error) {
	run, err := s.guardRun(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	caller, err := s.caller(ctx)
	if err != nil {
		return Run{}, err
	}
	// The driver record is resolved ONLY for a driver-role caller. DriverForUser is a query, and
	// running it for the twelve roles the ownership rule does not apply to would be a round trip per
	// completion to answer a question nobody asked.
	driverRecordID := ""
	if caller.RoleCode == identity.RoleDrivers {
		driver, err := s.repo.DriverForUser(ctx, caller.UserID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return Run{}, err
		}
		driverRecordID = driver.ID
	}
	if err := completionAuthority(caller, run, driverRecordID); err != nil {
		return Run{}, err
	}
	if !runCanTransition(run.Status, RunCompleted) {
		return Run{}, ErrIllegalTransition
	}
	// EVERYBODY'S PRECONDITION, SERVER-ENFORCED, AND IT NAMES THE STOP WHEN IT REFUSES. The cheap
	// COUNT(*) still decides it, so a run that is ready to close costs exactly what it always did; the
	// full stop list is read only on the refusal path, where a driver needs to be told WHICH stop
	// rather than how many. OpenStopCount's SQL (`status NOT IN ('DELIVERED','FAILED','CANCELLED')`)
	// and stopIsSettled are the same set said twice, which is why the count and the name can never
	// disagree about whether there is something to refuse.
	open, err := s.repo.OpenStopCount(ctx, run.ID)
	if err != nil {
		return Run{}, err
	}
	if open > 0 {
		return Run{}, s.unsettledStopRefusal(ctx, run.ID, open)
	}
	// AND THEN THE DRIVER'S, WHICH IS ONE STATUS NARROWER. Nothing is outstanding by now; the question
	// left is whether anybody RECORDED anything, and it is asked of a driver only. Its cost is one
	// extra read of the stop list on a driver's close, once per run per day, on the path that ends
	// the day, and it buys back the loophole stopIsResolved's header describes: three cancels and a
	// tap, and the day is filed with no delivery, no failure and no reason.
	if caller.RoleCode == identity.RoleDrivers {
		if err := s.everyStopHasAnAnswer(ctx, run.ID); err != nil {
			return Run{}, err
		}
	}
	ok, err := s.repo.UpdateRunStatus(ctx, run.ID, run.Status, RunCompleted, s.now().UTC())
	if err != nil {
		return Run{}, err
	}
	if !ok {
		return Run{}, ErrIllegalTransition
	}
	// AUDITED, in the shape DELIVERY_RUN_DISPATCHED already uses one screen up, plus the ROLE that did
	// it, which is the only new fact worth recording and is the fact this ruling turns on. "A driver
	// closed their own run" and "a dispatcher closed it for them" are different mornings, and after
	// today both are possible; actor_role is already allowlisted and already lifted into a first-class
	// column, so recording it costs no new key on a deny-by-default list.
	s.record(ctx, audit.SensitiveAccess{
		Action: "DELIVERY_RUN_COMPLETED", EntityType: "delivery_run", EntityID: run.ID,
		BranchID: run.BranchID,
		Extra:    map[string]any{"actor_role": caller.RoleCode, "status": RunCompleted},
	})
	return s.repo.GetRun(ctx, run.ID)
}

// unsettledStopRefusal builds the 409 that NAMES the stop standing between this run and being
// closed. "Stop 3 (#SO-1042) is on the truck" is a thing a driver can act on; "2 stop(s) on this run
// are not finished yet" is a thing they have to go and reconcile against a list, on a phone, in a
// truck cab, which is where this message is read.
//
// A FAILED SECOND READ DEGRADES TO THE COUNT rather than to a 500. The count is already known and
// already true, it is the old message verbatim, so a database that answered the first query and
// not the second costs the driver a worse sentence and never a worse outcome. The refusal itself is
// not in doubt at this point; only its wording is.
func (s *Service) unsettledStopRefusal(ctx context.Context, runID string, open int) error {
	if stops, err := s.repo.ListStopsForRun(ctx, runID); err == nil {
		if st, found := unsettledStop(stops); found {
			return conflictf(
				"dispatch: stop %d%s is %s, every stop must be delivered or marked not delivered before this run can be closed",
				st.StopNo, orderTag(st.OrderNo), unresolvedWord(st.Status))
		}
	}
	return conflictf("dispatch: %d stop(s) on this run are not finished yet", open)
}

// everyStopHasAnAnswer is the DRIVER'S half of the precondition: nil when every stop on the run was
// delivered or marked not delivered, and a 409 naming the first one that was neither.
//
// IT READS THE LIST BECAUSE NO COUNT IN THIS MODULE ASKS ITS QUESTION. OpenStopCount's SQL spells the
// SETTLED set and is right to, it is what "outstanding work" means for the load board, for the
// day-ahead tiles and for every other reader in this package, and narrowing that SQL to serve one
// caller would change what an open stop means everywhere it is counted, to fix a rule that applies in
// one place. So the narrow question is asked here, in the service, of the one caller it binds, and
// the shared count keeps meaning what every other reader already believes it means.
//
// NO DEGRADE ARM, unlike the refusal above, and the asymmetry is deliberate. There the refusal was
// already certain and only its wording was in doubt; here the read IS the decision, and a database
// that will not answer it must not be read as "then nobody cancelled anything". An unreadable gate
// fails the close rather than passing it, the direction that costs a driver a retry instead of
// costing the record a day filed with nothing in it.
func (s *Service) everyStopHasAnAnswer(ctx context.Context, runID string) error {
	stops, err := s.repo.ListStopsForRun(ctx, runID)
	if err != nil {
		return err
	}
	st, found := unresolvedStop(stops)
	if !found {
		return nil
	}
	// THE SENTENCE DOES NOT ASK FOR THE IMPOSSIBLE. The settled refusal's clause, "every stop must be
	// delivered or marked not delivered", is an instruction, and it is a true one for a stop still on
	// the truck. Said about a cancelled stop it would be a lie: CANCELLED is terminal in the matrix, so
	// there is no tap on this phone that would satisfy it. What is true is that the day now needs a
	// dispatcher, and that is what a driver standing beside a closed truck needs to be told.
	return conflictf(
		"dispatch: stop %d%s is %s, and that is not an outcome a driver records, ask dispatch to close this run",
		st.StopNo, orderTag(st.OrderNo), unresolvedWord(st.Status))
}

// orderTag renders " (#SO-1042)" or "", the parenthetical that lets a driver find the stop the
// refusal is about without counting cards. It is a NUMBER, not money and not a customer: the run
// sheet already prints it on every card, so naming it here tells the reader nothing their own screen
// did not already say.
func orderTag(orderNo string) string {
	if strings.TrimSpace(orderNo) == "" {
		return ""
	}
	return " (" + orderNo + ")"
}

// CancelRun withdraws a PLANNED run. Its stops are NOT cancelled, they fall back to unassigned and
// can go out on another truck. Cancelling the stops too would delete a day of scheduling because one
// truck broke down.
func (s *Service) CancelRun(ctx context.Context, runID string) (Run, error) {
	run, err := s.guardRun(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	if !runCanTransition(run.Status, RunCancelled) {
		return Run{}, ErrIllegalTransition
	}
	stops, err := s.repo.ListStopsForRun(ctx, run.ID)
	if err != nil {
		return Run{}, err
	}
	for _, st := range stops {
		if st.Status == StopReady {
			if _, err := s.repo.AssignStop(ctx, st.ID, "", 0); err != nil {
				return Run{}, err
			}
		}
	}
	ok, err := s.repo.UpdateRunStatus(ctx, run.ID, run.Status, RunCancelled, s.now().UTC())
	if err != nil {
		return Run{}, err
	}
	if !ok {
		return Run{}, ErrIllegalTransition
	}
	return s.repo.GetRun(ctx, run.ID)
}

// - stop lifecycle -

// AdvanceStop moves a stop along the matrix. DELIVERED is deliberately NOT reachable here, it is
// reachable only through CapturePOD, because the evidence and the status are one write (repository
// SetPOD explains why). Asking for DELIVERED on this path is refused with the reason, so a client
// that tries learns where the real door is.
func (s *Service) AdvanceStop(ctx context.Context, stopID, to, reason string) (Stop, error) {
	stop, err := s.guardStop(ctx, stopID)
	if err != nil {
		return Stop{}, err
	}
	to = strings.ToUpper(strings.TrimSpace(to))
	if to == StopDelivered {
		return Stop{}, ErrPODRequired
	}
	if !stopCanTransition(stop.Status, to) {
		return Stop{}, ErrIllegalTransition
	}
	switch to {
	case StopLoaded:
		if stop.RunID == "" {
			return Stop{}, ErrStopUnassigned
		}
	case StopEnRoute:
		// A stop cannot be en route from a run that never left. This is the guard that keeps the board
		// and the world in the same story.
		run, err := s.repo.GetRun(ctx, stop.RunID)
		if err != nil {
			return Stop{}, ErrStopUnassigned
		}
		if run.Status != RunDispatched {
			return Stop{}, ErrRunNotDispatched
		}
	case StopFailed:
		if strings.TrimSpace(reason) == "" {
			return Stop{}, httpx.BadRequestf("dispatch: a failed stop needs a reason")
		}
	}
	ok, err := s.repo.UpdateStopStatus(ctx, stop.ID, stop.Status, to, strings.TrimSpace(reason), s.now().UTC())
	if err != nil {
		return Stop{}, err
	}
	if !ok {
		return Stop{}, ErrIllegalTransition // lost the race; the row moved under us
	}
	s.record(ctx, audit.SensitiveAccess{
		Action: "DELIVERY_STOP_" + to, EntityType: "delivery_stop", EntityID: stop.ID,
		Extra: map[string]any{"from": stop.Status},
	})
	return s.repo.GetStop(ctx, stop.ID)
}

// CapturePOD is the one door to DELIVERED.
//
// It captures a typed-name attestation through the DIB-386 seam, a full name plus an EXPLICIT
// affirmative, both refused when absent, neither inferred from the fact that a form was submitted -
// then writes the ref and the status together. The order of operations matters: the attestation is
// sealed FIRST, so a failure to capture leaves the stop en route rather than delivered-without-proof.
//
// Then the handoff. When this was the order's last live stop, the order is advanced toward invoicing
// through the order seam. The advance is best-effort BY DESIGN: a delivery that physically happened
// must not be un-recorded because a downstream status write failed. handoff_at stays NULL in that
// case, which is exactly how the gap becomes visible instead of silent.
func (s *Service) CapturePOD(ctx context.Context, stopID string, in PODInput) (StopDetail, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return StopDetail{}, err
	}
	stop, err := s.guardStop(ctx, stopID)
	if err != nil {
		return StopDetail{}, err
	}
	if !stopCanTransition(stop.Status, StopDelivered) {
		return StopDetail{}, ErrIllegalTransition
	}
	name := strings.TrimSpace(in.ReceivedBy)
	if name == "" {
		return StopDetail{}, httpx.BadRequestf("dispatch: type the full name of the person receiving this delivery")
	}
	if !in.Affirmed {
		return StopDetail{}, httpx.BadRequestf("dispatch: the recipient must confirm the delivery before it can be signed")
	}
	if s.sig == nil {
		return StopDetail{}, ErrNoSignatureProvider
	}
	ref, err := s.sig.CaptureTyped(ctx, TypedAttestation{
		Purpose: SignaturePurposePOD, RefType: RefTypeDeliveryStop, RefID: stop.ID,
		TypedName: name, Affirmed: true,
	})
	if err != nil {
		return StopDetail{}, err
	}
	at := s.now().UTC()
	ok, err := s.repo.SetPOD(ctx, stop.ID, stop.Status, ref, name, caller.UserID, at)
	if err != nil {
		return StopDetail{}, err
	}
	if !ok {
		return StopDetail{}, ErrIllegalTransition
	}
	s.record(ctx, audit.SensitiveAccess{
		Action: "DELIVERY_POD_CAPTURED", EntityType: "delivery_stop", EntityID: stop.ID,
		MaskedRef: ref, Purpose: SignaturePurposePOD,
	})
	// W3-C / DIB-63 + DIB-75: fold the stop's photos into the sealed POD record and tie the trail's
	// DELIVERED checkpoints to this attestation. Best-effort, after the write, never inside it -
	// afterPOD's comment says why each failure is a visible gap rather than a rollback.
	s.afterPOD(ctx, stop, ref, at)
	s.handoff(ctx, stop, at)
	return s.StopDetail(ctx, stop.ID)
}

// handoff advances the order toward invoicing once every live stop for it is delivered.
//
// It is called after the POD write, never inside it, and every failure is LOGGED AND SWALLOWED. The
// physical fact (this was delivered, here is who signed) is already recorded and must not be rolled
// back because the billing side was briefly unavailable. handoff_at is the reconciliation handle: a
// delivered stop with no handoff timestamp is a queryable gap, which is the point.
func (s *Service) handoff(ctx context.Context, stop Stop, at time.Time) {
	if s.fulfil == nil {
		return
	}
	live, err := s.repo.LiveStopsForOrder(ctx, stop.OrderID)
	if err != nil {
		s.log.Warn("dispatch: handoff read failed", "stop", stop.ID, "err", err)
		return
	}
	for _, st := range live {
		if st.Status != StopDelivered {
			return // something for this order is still out; the order is not fulfilled yet
		}
	}
	if err := s.fulfil.MarkFulfilled(ctx, stop.OrderID, "dispatch:"+stop.ID); err != nil {
		// An order already past FULFILLED (a POS ring beat us to it) is a NO-OP, not a failure, the
		// order seam grades the move against its own forward-only ladder and says so.
		s.log.Info("dispatch: order advance declined", "order", stop.OrderID, "err", err)
	}
	if err := s.repo.MarkHandoff(ctx, stop.ID, at); err != nil {
		s.log.Warn("dispatch: handoff stamp failed", "stop", stop.ID, "err", err)
	}
}

// - POD photos -

// AddPhoto stores one POD photo against a stop.
//
// Photos are accepted on an EN_ROUTE or already-DELIVERED stop, and on nothing else. A photo before
// the truck left is not proof of delivery; a photo after the fact is normal (the driver's phone
// finally got signal, which is the whole reason the driver surface tolerates a retry).
func (s *Service) AddPhoto(ctx context.Context, stopID, contentType, caption string, data []byte) (PhotoMeta, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return PhotoMeta{}, err
	}
	stop, err := s.guardStop(ctx, stopID)
	if err != nil {
		return PhotoMeta{}, err
	}
	if stop.Status != StopEnRoute && stop.Status != StopDelivered && stop.Status != StopFailed {
		return PhotoMeta{}, conflictf("dispatch: a %s stop has nothing to photograph yet", stop.Status)
	}
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	if !podContentTypes[contentType] {
		return PhotoMeta{}, httpx.BadRequestf("dispatch: a proof photo must be a jpeg, png or webp")
	}
	if len(data) == 0 {
		return PhotoMeta{}, httpx.BadRequestf("dispatch: the photo is empty")
	}
	if len(data) > MaxPhotoBytes {
		return PhotoMeta{}, httpx.BadRequestf("dispatch: the photo is too large (max %d MB)", MaxPhotoBytes>>20)
	}
	id, err := s.repo.InsertPhoto(ctx, stop.ID, stop.BranchID, contentType, strings.TrimSpace(caption), caller.UserID, data)
	if err != nil {
		return PhotoMeta{}, err
	}
	return PhotoMeta{ID: id, StopID: stop.ID, ContentType: contentType, ByteSize: len(data),
		Caption: caption, CapturedAt: s.now().UTC()}, nil
}

// Photo reads one POD photo's bytes, fenced on the PHOTO's own branch column (0091) rather than on
// the stop it is assumed to hang off, the fence checks the row it is actually serving.
func (s *Service) Photo(ctx context.Context, photoID string) (Photo, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return Photo{}, err
	}
	p, branchID, err := s.repo.GetPhoto(ctx, photoID)
	if err != nil {
		return Photo{}, err
	}
	if !caller.CanAccessBranch(branchID) {
		return Photo{}, ErrForbidden
	}
	return p, nil
}

// - the driver's day -

// MyRuns is the driver surface's read: the runs assigned to the DRIVER RECORD LINKED TO THIS LOGIN,
// for one date.
//
// It resolves the driver from the caller's user id rather than taking a driver_id parameter, on
// purpose: a driver_id parameter is an invitation to read another driver's day, and no fence check
// can make "show me driver X's stops" safe for a frontline role that legitimately reaches the branch.
// A staff user with no driver record gets ErrNotFound, "you are not set up as a driver", rather
// than an empty list that reads like a day off.
func (s *Service) MyRuns(ctx context.Context, date string) ([]RunDetail, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	driver, err := s.repo.DriverForUser(ctx, caller.UserID)
	if err != nil {
		return nil, err
	}
	if !caller.CanAccessBranch(driver.BranchID) {
		return nil, ErrForbidden
	}
	if date == "" {
		date = s.now().UTC().Format("2006-01-02")
	}
	if !validDate(date) {
		return nil, httpx.BadRequestf("dispatch: date must be YYYY-MM-DD")
	}
	runs, err := s.repo.ListRunsForDriver(ctx, driver.ID, date)
	if err != nil {
		return nil, err
	}
	out := make([]RunDetail, 0, len(runs))
	for _, r := range runs {
		d, err := s.runDetail(ctx, r)
		if err != nil {
			return nil, err
		}
		d.StopLines = s.driverStopLines(ctx, d.Stops)
		out = append(out, d)
	}
	return out, nil
}

// driverStopLines answers "what comes off the truck here" for every stop on a run.
//
// WHY THIS EXISTS. Until now a driver's day named the customer, the address and the window and said
// nothing whatever about the material. That is fine at the yard, where the picker built the load and
// can see it; it is not fine in a driveway, where the one question the customer asks is "is that all
// of it" and the only answer on the phone was the stop's status. The pick feed has carried this exact
// answer since Wave 3, it is what the yard pulls against, but it lived behind PickFeed, an internal
// unfenced feed with no route, so nobody on the road could see it.
//
// IT REUSES THE SEAM RATHER THAN OPENING A ROUTE. Service.lines (OrderLineReader, injected by the
// composition root and already wired for the load board's gap scan) is the same read the yard uses,
// so a driver and a picker cannot be looking at two different order line sets. Adding
// GET /stops/{id}/lines instead would have been a second read of the same fact behind a second fence,
// and the second fence is the one that gets written slightly differently.
//
// EVERY FAILURE IS SWALLOWED, AND THAT IS THE DELIBERATE CHOICE. A line read that errors, or a
// deployment that has not injected the seam at all, returns no lines for that stop, and the day is
// still delivered: addresses, windows, contacts, POD, the lot. The alternative is a driver parked at
// a gate looking at an error page because a *supplementary* read failed, which trades the whole
// surface for a detail of it. The surface says "not recorded" rather than drawing an empty list, so a
// missing answer never reads as an empty truck (dispatch-driver-run.ts renders that distinction).
//
// NO MONEY CAN ARRIVE HERE. DriverLine (model.go) has no money member of any kind, so the conversion
// below cannot carry a figure even if the seam one day started returning one.
func (s *Service) driverStopLines(ctx context.Context, stops []Stop) map[string][]DriverLine {
	if s.lines == nil || len(stops) == 0 {
		return nil
	}
	// One read per ORDER, not per stop: a split load puts two stops on the same order, and asking the
	// order module the same question twice in one page is a round trip nobody needed.
	byOrder := map[string][]DriverLine{}
	out := map[string][]DriverLine{}
	for _, st := range stops {
		if st.OrderID == "" {
			continue
		}
		lines, seen := byOrder[st.OrderID]
		if !seen {
			raw, err := s.lines.OrderPickLines(ctx, st.OrderID)
			if err != nil {
				s.log.Warn("dispatch: driver line read failed", "order_id", st.OrderID, "err", err)
				raw = nil
			}
			lines = make([]DriverLine, 0, len(raw))
			for _, l := range raw {
				lines = append(lines, DriverLine{
					LineNo: l.LineNo, SKU: l.SKU, Description: l.Description,
					Qty: l.QtyOrdered, UOM: l.UOM,
				})
			}
			byOrder[st.OrderID] = lines
		}
		if len(lines) > 0 {
			out[st.ID] = lines
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// - helpers -

// record writes an audit row best-effort. Audit is an index of who did what, never a precondition:
// a delivery does not fail because the trail could not be written.
func (s *Service) record(ctx context.Context, ev audit.SensitiveAccess) {
	if s.audit == nil {
		return
	}
	if ev.ActorID == "" {
		ev.ActorID = identity.StaffUserIDFromContext(ctx)
	}
	if err := s.audit.Record(ctx, ev); err != nil {
		s.log.Warn("dispatch: audit write failed", "action", ev.Action, "err", err)
	}
}

// validDate accepts exactly YYYY-MM-DD. Parsing rather than pattern-matching so 2026-02-30 is refused
// here instead of by a Postgres error mid-insert.
func validDate(s string) bool {
	if s == "" {
		return false
	}
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// validCoords refuses a half-coordinate and an out-of-range one. A stop with a latitude and no
// longitude is a data-entry slip that would silently drop out of every distance estimate.
func validCoords(lat, lng *float64) error {
	if (lat == nil) != (lng == nil) {
		return httpx.BadRequestf("dispatch: latitude and longitude must be given together")
	}
	if lat == nil {
		return nil
	}
	if *lat < -90 || *lat > 90 {
		return httpx.BadRequestf("dispatch: latitude must be between -90 and 90")
	}
	if *lng < -180 || *lng > 180 {
		return httpx.BadRequestf("dispatch: longitude must be between -180 and 180")
	}
	return nil
}

// conflictError is a 409 carrying a sentence an operator can act on.
type conflictError struct{ msg string }

func (e conflictError) Error() string { return e.msg }

func conflictf(format string, a ...any) error { return conflictError{msg: fmt.Sprintf(format, a...)} }

// asConflict turns a unique-violation into a stated conflict and passes anything else through. The
// alternative, a 500 carrying a constraint name, tells the operator nothing and tells an attacker
// the schema.
func asConflict(err error, msg string) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "SQLSTATE 23505") {
		return conflictError{msg: msg}
	}
	var ce conflictError
	if errors.As(err, &ce) {
		return err
	}
	return err
}
