package order

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// ErrNotFound is returned when an order does not exist for the tenant (a cross-tenant
// id is reported as not-found, never forbidden, IDOR-safe). Exported for errors.Is.
var ErrNotFound = errors.New("order: not found")

// ErrForbidden is returned when the order EXISTS but sits in a branch the caller cannot
// access (the horizontal-access boundary on the staff-gated history read, mirrors
// till.forbidden at till/history.go:211). A missing order returns ErrNotFound instead, so
// the authz error never leaks the existence of an order in another branch. Exported for errors.Is.
var ErrForbidden = errors.New("order: forbidden")

// ErrUnauthorized is returned when the request carries no authenticated staff caller (the
// route guard should already have rejected it; this is defence-in-depth). Exported for errors.Is.
var ErrUnauthorized = errors.New("order: unauthenticated")

// ErrNoTransition is returned by the status choke-point when the target status equals the
// current one, a no-op that must NOT emit an ORDER_STATUS_CHANGED history row. Callers
// treat it as a benign "nothing to do", never as a failure. Exported for errors.Is.
var ErrNoTransition = errors.New("order: status unchanged")

// ErrIllegalTransition is returned by the MANUAL status endpoints (DIB-191) when the requested
// from→to move is not permitted under the forward-only operational matrix, a legal-role caller
// asking for a move the order's current status does not allow (e.g. READY→PICKING), or a target that
// is not manually settable. It is DISTINCT from ErrNoTransition (target == current, a benign no-op)
// and from the vocabulary BadRequest; the handler maps it to 409 Conflict. Exported for errors.Is.
var ErrIllegalTransition = errors.New("order: illegal status transition")

// ErrCancelBlocked is returned when a manual CANCELLED is refused because money is already attached to
// the order, a finalized invoice or a committed pos_sale (B2/R2, audit H2). Cancelling then would
// orphan the invoice/payment/stock/GL state, so the caller must use the POS returns flow instead. The
// handler maps it to 409. Exported for errors.Is.
var ErrCancelBlocked = errors.New("order: cancel blocked, order has transacted money state")

// ErrCancelUnverifiable is returned when the cancel predicate cannot be EVALUATED because its seams are
// not wired. The predicate exists to prove no money is attached; a nil reader proves nothing, so the
// cancel is refused rather than silently allowed (fail closed). Distinct from ErrCancelBlocked so the
// handler can answer 503 (a server wiring fault) rather than 409 (a legitimate business refusal), and
// distinct from a generic 500 so the message can name the unwired seam. main.go asserts the wiring at
// boot, so this should be unreachable in a correct deploy. Exported for errors.Is.
var ErrCancelUnverifiable = errors.New("order: cancel predicate unavailable, cancel guards not wired")

// maxInstructionsLen bounds the free-text delivery note so a caller can't store an
// unbounded blob on the order.
const maxInstructionsLen = 2000

// maxReasonLen bounds the free-text transition reason persisted on the timeline (audit M:
// previously unbounded). Generous for a real cancellation note, far short of a blob.
const maxReasonLen = 2000

// orderStore is the persistence the Service needs. Real impl: Repository (pgx); tests
// use a fake. Every operation is tenant-scoped by customerID.
type orderStore interface {
	ListOrders(ctx context.Context, customerID string, f Filter) ([]Order, error)
	GetOrder(ctx context.Context, customerID, orderID string) (Detail, error)
	SetDeliveryInstructions(ctx context.Context, customerID, orderID, text string) error
	// UpdateStatus is the DIB-159 status-transition write: it moves ONE order to newStatus
	// and returns the prior status atomically (RETURNING), so the from→to pair is captured
	// with the write. tenant "" is a non-tenant-scoped (staff/ERP/fulfilment) transition.
	// Returns ErrNoTransition when newStatus == the current status (a no-op, no history).
	UpdateStatus(ctx context.Context, tenant, orderID, newStatus string) (oldStatus string, err error)
	// UpdateStatusFrom is the guarded MANUAL status write (DIB-191): it moves the order to newStatus
	// only when its current status is in allowedFrom, atomically (one locked UPDATE), RETURNING the
	// prior status. This closes the check-then-write race the forward-only matrix would otherwise have.
	// current ∉ allowedFrom → ErrIllegalTransition; target == current → ErrNoTransition; missing id →
	// ErrNotFound. Not tenant-scoped, the manual path is staff/ERP; branch scope is enforced above it.
	UpdateStatusFrom(ctx context.Context, orderID, newStatus string, allowedFrom []string) (oldStatus string, err error)
	// UpdateStatusFromChecked is UpdateStatusFrom with a Go-side predicate run INSIDE the order-row lock,
	// before the write (B2-2). The DIB-191 cancel routes here so its money predicate cannot be raced by a
	// writer that finalizes an invoice in a check→write gap. Same outcomes as UpdateStatusFrom, plus the
	// precheck's own error (e.g. ErrCancelBlocked). Only the cancel action uses it, AdvanceFromEvent and
	// the other manual transitions keep the single-statement UpdateStatusFrom.
	UpdateStatusFromChecked(ctx context.Context, orderID, newStatus string, allowedFrom []string, precheck func(ctx context.Context) error) (oldStatus string, err error)
	// OrderBranch resolves the owning branch_id of one order (migration 0008) so the staff-gated
	// history read can branch-scope the caller. A missing order maps to ErrNotFound (never a leak).
	OrderBranch(ctx context.Context, orderID string) (branchID string, err error)
	// OrderHeader reads the order's CURRENT status and owning branch in one query, for the staff
	// order read (B2/R6). Status comes from orders.status, never derived from the timeline, which
	// is best-effort and therefore not a source of truth. Missing id → ErrNotFound.
	OrderHeader(ctx context.Context, orderID string) (status, branchID string, err error)
	// ResolveOrderID accepts either a real order id or its visible order_no ("#SO-4"/"SO-4") and returns
	// the real id (DIB-379/F3). A UUID passes through UNRESOLVED (a cross-branch deep-link then answers
	// 403 downstream). An order_no is resolved ONLY within the caller's accessible branches (global=true
	// reaches all; else branchIDs) so a cross-branch #SO answers ErrNotFound (404) rather than leaking
	// existence via the number space (B2-4). Missing/garbage → ErrNotFound.
	ResolveOrderID(ctx context.Context, idOrNo string, global bool, branchIDs []string) (string, error)
}

// Service is the orders read seam the portal BFF composes in-process, plus the
// delivery-instructions write and the DIB-159 status-transition choke-point.
type Service struct {
	store orderStore
	audit audit.Recorder
	// DIB-159 order-change history read seams (history.go): auditTrail is the READ side of the
	// audit spine (the ORDER_STATUS_CHANGED timeline); names resolves actor display names. Both
	// are nil until main.go injects them, OrderHistory degrades to an empty/id-only timeline.
	auditTrail AuditTrailReader
	names      ActorNameResolver
	// CP-04 reorder seams (reorder.go): nil until main.go injects them via
	// WithReorderPricing / WithReorderInventory, reorder answers 501 until then.
	pricer PriceResolver
	inv    InventoryReader
	// callerFrom resolves the trusted, server-stamped acting staff member for the branch-scoped
	// history read (history.go). Defaults to the identity guard-stamped caller; tests override it
	// via withCaller. Mirrors till.Service.callerFrom (till/service.go:86).
	callerFrom func(context.Context) (identity.StaffContext, bool)
	// B2/R2 cancel-predicate seams (cancel_seam.go): the two reads that prove no money is attached to
	// an order before a manual CANCELLED is allowed. nil until main.go injects them via
	// WithCancelGuards, and a nil seam FAILS CLOSED (a cancel attempt is refused, never allowed).
	cancelInvoices FinalizedInvoiceReader
	cancelSales    CommittedSaleReader
	// DIB-175 staff order-edit seam (edit.go): resolves an order's customer pricing tier so an edited
	// line reprices at the rate that account actually gets. nil until main.go injects it via
	// WithCustomerType, and a nil seam FAILS CLOSED (a money-moving edit is refused, never priced
	// at a default tier).
	custType CustomerTypeResolver
	// DIB-175 B2-1: the TRANSACTIONAL audit writer. The edit path records ORDER_CHANGED rows on the
	// SAME tx as the line write so a recorder failure rolls the edit back (unlike s.audit, which is
	// pool-based + best-effort). Set by WithAudit when the injected recorder implements RecordTx
	// (the real *audit.Service does). nil for a fake that only implements Record → the edit path then
	// has no atomic recorder and its record hook is a no-op (unit fakes provide their own RecordTx).
	auditTx txRecorder
	// W5 seam-3 stock reservation port (stock_seam.go / reservations.go): hard-reserve on confirm,
	// consume on fulfil, release on cancel, every one of them inside the status transaction. nil
	// until main.go injects it via WithStockReserver, and a nil seam turns the whole reservation lane
	// OFF (fail-open, loudly: main.go asserts the binding at boot and StockSeamWired reports it).
	stock StockReserver
	// SCOPE:F10 change-order seams (change.go): picks reports whether the tasks substrate has a
	// started pick for an order (that is what makes a change POST-pick); finance narrows who may sign
	// a material delta through the DIB-360 approvals substrate. Both nil until main.go injects them.
	picks   PickStateReader
	finance FinanceApprover
	// DIB-78 multi-ship invoice writer (shipgroup.go). Unbound is the SHIPPED state and a documented
	// PARK: no order→invoice writer exists in the tree outside the POS ring, so readiness is computed
	// and published while the button stays absent rather than fabricating an invoice number.
	invoicer MultiShipInvoicer
	// standing is the billing read behind the yard pick notice facts (yard_pick.go); nil = none.
	standing InvoiceStandingReader
	// backorderNotifier tells the rep when an order edit clears a short line's backorder (yard_pick_edit.go).
	backorderNotifier BackorderNotifier
	// pickHold is the billing read behind the pay before release hold (pick_hold.go,
	// item 9.3): nil leaves the lane off (the composition root asserts the wiring at boot).
	pickHold PickHoldReader
}

// txRecorder is the transactional half of the audit seam: write one row on the caller's transaction so
// it commits or rolls back with the caller's mutation (satisfied by *audit.Service.RecordTx).
type txRecorder interface {
	RecordTx(ctx context.Context, q audit.Execer, a audit.SensitiveAccess) error
}

// callerBranchScope projects a caller onto the branch filter ResolveOrderID needs (B2-4): a GLOBAL
// caller reaches every branch (global=true, no ids); a scoped caller reaches only its grants. It is what
// keeps a visible order_no from resolving to an order in a branch the caller cannot reach, mirrors the
// CanAccessBranch split, applied at resolution time so the number space is not an existence oracle.
func callerBranchScope(c identity.StaffContext) (global bool, ids []string) {
	if c.Scope == identity.ScopeGlobal {
		return true, nil
	}
	ids = make([]string, 0, len(c.Branches))
	for b := range c.Branches {
		ids = append(ids, b)
	}
	return false, ids
}

// NewService wires the store. When the store also resolves actor names (the real *Repository does),
// the DIB-159 history timeline gets staff display names with no extra wiring.
func NewService(store orderStore) *Service {
	s := &Service{store: store, callerFrom: identity.StaffContextFromContext}
	if nr, ok := store.(ActorNameResolver); ok {
		s.names = nr
	}
	return s
}

// withCaller is the test seam for the guard-stamped StaffContext (branch scope on the history read).
// Mirrors till.Service.withCaller (till/service.go:109).
func (s *Service) withCaller(f func(context.Context) (identity.StaffContext, bool)) *Service {
	s.callerFrom = f
	return s
}

// caller resolves the trusted, server-stamped acting staff member (never a spoofable header). It is
// the auth subject the history read branch-scopes against. Mirrors till.Service.caller (till/service.go:115).
func (s *Service) caller(ctx context.Context) (identity.StaffContext, error) {
	if s.callerFrom == nil {
		return identity.StaffContext{}, ErrUnauthorized
	}
	c, ok := s.callerFrom(ctx)
	if !ok || c.UserID == "" {
		return identity.StaffContext{}, ErrUnauthorized
	}
	return c, nil
}

// WithAudit injects the A-21 Recorder (Q-044: every order mutation surface writes who/what/when). When
// the recorder also satisfies txRecorder (the real *audit.Service does), the DIB-175 edit path binds it
// as the TRANSACTIONAL recorder too, so ORDER_CHANGED rows commit or roll back with the line write.
func (s *Service) WithAudit(r audit.Recorder) *Service {
	s.audit = r
	if tx, ok := r.(txRecorder); ok {
		s.auditTx = tx
	}
	return s
}

// ListOrders returns one tenant-scoped page of orders (cursor envelope; the
// total echo is gone with the offset family, HH-3.2 fold).
func (s *Service) ListOrders(ctx context.Context, customerID string, f Filter) ([]Order, error) {
	if customerID == "" {
		return nil, httpx.BadRequestf("customer id is required")
	}
	if f.Status != "" && !validStatus(f.Status) {
		return nil, httpx.BadRequestf("unknown order status %q", f.Status)
	}
	return s.store.ListOrders(ctx, customerID, f)
}

// GetOrder returns one order with its lines + delivery instructions, scoped to the
// tenant. ErrNotFound when missing or owned by another account.
func (s *Service) GetOrder(ctx context.Context, customerID, orderID string) (Detail, error) {
	if customerID == "" || orderID == "" {
		return Detail{}, httpx.BadRequestf("customer id and order id are required")
	}
	return s.store.GetOrder(ctx, customerID, orderID)
}

// SetDeliveryInstructions attaches/updates the contractor's note for a scheduled
// delivery. Tenant-scoped: the store updates only when the order belongs to customerID.
// Only meaningful for a DELIVERY order that is not yet fulfilled (enforced downstream).
func (s *Service) SetDeliveryInstructions(ctx context.Context, customerID, orderID, text string) error {
	if customerID == "" || orderID == "" {
		return httpx.BadRequestf("customer id and order id are required")
	}
	if len(text) > maxInstructionsLen {
		return httpx.BadRequestf("delivery instructions exceed %d characters", maxInstructionsLen)
	}
	if err := s.store.SetDeliveryInstructions(ctx, customerID, orderID, text); err != nil {
		return err
	}
	// Q-044 order-change history: the only order mutation surface today audits who/what/when.
	// (Order STATUS transitions have no writer yet, POS orders are immutable post-commit; the
	// ORDER_STATUS_CHANGED action is reserved for the first status-transition surface.)
	if s.audit != nil {
		_ = s.audit.Record(ctx, audit.SensitiveAccess{
			Action: audit.ActOrderChanged, EntityType: "order", EntityID: orderID,
			ActorID: customerID, Purpose: "PORTAL_ORDER_UPDATE", Reason: "delivery_instructions",
			Extra: map[string]any{"field": "delivery_instructions", "new_len": len(text)},
		})
	}
	return nil
}

// SetStatus is the DIB-159 order-status-transition CHOKE-POINT: the ONE method every
// status move, portal, staff/ERP, and cross-module (pos fulfilment), funnels through.
// It performs the `UPDATE orders SET status` write and emits exactly one
// ActOrderStatusChanged audit record capturing {order_id, from_status, to_status, actor,
// reason?, occurred_at}. Because there is no pre-existing status writer in this codebase
// (orders are immutable post-creation today), routing every future transition here is what
// guarantees no path is missed.
//
// tenant is the customer_id scope for a portal-originated transition; pass "" for a
// staff/ERP/fulfilment transition that is not tenant-scoped. actor is the resolved caller
// (customer id for portal, staff user id for ERP, or a system actor for a fulfilment
// event). reason is an optional free-text/short code recorded on the history row.
//
// The record is emitted right after the write returns the pre-image (old status). The
// UpdateStatus RETURNING read is atomic with the write, so the from→to pair can never be
// lost to a concurrent transition. A no-op (target == current) returns ErrNoTransition and
// emits nothing. Audit failure never fails the transition (best-effort, mirrors the other
// Q-044 records), the orders table stays the source of truth.
//
// ⚠ TRANSITION-UNGUARDED. This validates the status VALUE, not the MOVE, it will happily write a
// backward or terminal-resurrecting transition. That unguarded door is what produced audit H1 (a
// nondeterministic terminal status on every POS cash sale). Event/system callers MUST use
// AdvanceFromEvent, and staff-initiated moves MUST use TransitionManual; both grade the move before
// writing. Do not add a new caller here without a deliberate reason.
func (s *Service) SetStatus(ctx context.Context, tenant, actor, orderID, newStatus, reason string) error {
	if orderID == "" {
		return httpx.BadRequestf("order id is required")
	}
	if !validStatus(newStatus) {
		return httpx.BadRequestf("unknown order status %q", newStatus)
	}
	// Item 9.3: the pay before release hold guards the one status that starts
	// releasing goods, at the choke point itself, whatever caller reached it.
	if newStatus == "PICKING" {
		if err := s.refuseHeldFromPicking(ctx, orderID); err != nil {
			return err
		}
	}
	oldStatus, err := s.store.UpdateStatus(ctx, tenant, orderID, newStatus)
	if err != nil {
		// ErrNoTransition (target == current) is benign, surface it so a caller can ignore
		// it, but emit no history row.
		return err
	}
	s.recordStatusChange(ctx, actor, orderID, oldStatus, newStatus, reason)
	return nil
}

// recordStatusChange emits the single best-effort ORDER_STATUS_CHANGED audit row per real transition -
// the DIB-159 timeline store. It is shared by SetStatus (the portal/event-driven path) and
// setStatusGuarded (the DIB-191 manual path) so EVERY status writer lands exactly one attributed
// timeline row through one code path. The old/new status ride in Extra (allowlisted in audit.Service);
// the reason rides in the top-level Reason (persisted as changes["reason"]); occurred_at is the
// audit_log row's created_at (append-only); the actor is the resolved caller ("" for a system/event
// transition → NULL actor_id → renders as "system" on the timeline). Audit failure never fails the
// transition, the orders table stays the source of truth.
func (s *Service) recordStatusChange(ctx context.Context, actor, orderID, oldStatus, newStatus, reason string) {
	if s.audit == nil {
		return
	}
	_ = s.audit.Record(ctx, audit.SensitiveAccess{
		Action: audit.ActOrderStatusChanged, EntityType: "order", EntityID: orderID,
		ActorID: actor, Purpose: "ORDER_STATUS_TRANSITION", Reason: reason,
		Extra: map[string]any{"old_status": oldStatus, "new_status": newStatus},
	})
}

// - DIB-191 event-path forward-only guard (B2 / R1) -

// statusRank is the forward-only lifecycle ladder the EVENT drivers are graded against. CANCELLED is
// deliberately ABSENT: it is terminal and off the ladder, so nothing ever advances out of it.
//
// Why this exists (audit H1). The event path used to funnel through SetStatus → UpdateStatus, which
// guards only `status IS DISTINCT FROM`, i.e. it validates the status VALUE but not the TRANSITION. That
// made the terminal status of a POS cash sale nondeterministic: pos emits order.fulfilled and
// ar.invoice.finalized in ONE transaction, so both outbox rows carry an identical occurred_at (now() is
// the transaction timestamp), and the dispatcher claims rows `ORDER BY occurred_at` with no tiebreaker.
// Whichever drained second won, so the same sale could settle FULFILLED or INVOICED. Grading every event
// against this ladder makes the outcome order-INDEPENDENT: the later stage always wins and the earlier
// one degrades to a logged no-op. It also closes CANCELLED/CLOSED resurrection and the INVOICED→FULFILLED
// backward edge.
var statusRank = map[string]int{
	"DRAFT": 1, "CONFIRMED": 2, "PICKING": 3, "READY": 4,
	"PARTIALLY_FULFILLED": 5, "FULFILLED": 6, "INVOICED": 7, "CLOSED": 8,
}

// eventAllowedFrom returns every status strictly BELOW target on the ladder, the allowedFrom set for a
// guarded event write. One derivation covers all three R1 rules with no special-casing:
//
//	target <= current      → current is not below target → not in the set → no-op
//	current is CANCELLED   → unranked → never in the set → no-op (no resurrection)
//	current is CLOSED      → rank 8, above every event target → not in the set → no-op
//
// An unranked or unknown target yields an empty set, which the guarded write rejects outright.
func eventAllowedFrom(target string) []string {
	want, ok := statusRank[target]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(statusRank))
	for s, r := range statusRank {
		if r < want {
			out = append(out, s)
		}
	}
	sort.Strings(out) // deterministic order, the set is a query parameter and rides into test assertions
	return out
}

// AdvanceFromEvent is the ONLY sanctioned status writer for the event-driven drivers (consumer.go). It
// advances orderID to target if and only if the order's current status sits strictly below target on the
// ladder, evaluated atomically inside the guarded write (UpdateStatusFrom's FOR UPDATE + ANY(...) CTE),
// then emits the same single attributed timeline row every other writer emits.
//
// The actor is "", a system/event transition (→ NULL actor_id → renders "system" on the timeline), and
// eventID rides in the reason for traceability back to the outbox row.
//
// Returns ErrIllegalTransition when the move is not forward (the caller treats that as a logged no-op),
// ErrNoTransition when the order is already at target, ErrNotFound when it is absent.
func (s *Service) AdvanceFromEvent(ctx context.Context, orderID, target, eventID string) error {
	if orderID == "" {
		return httpx.BadRequestf("order id is required")
	}
	if !validStatus(target) {
		return httpx.BadRequestf("unknown order status %q", target)
	}
	allowed := eventAllowedFrom(target)
	if len(allowed) == 0 {
		// Nothing may advance INTO this status by event (e.g. DRAFT, or a status off the ladder).
		return ErrIllegalTransition
	}
	err := s.setStatusGuarded(ctx, "", orderID, target, eventID, allowed, nil)
	if target == "FULFILLED" {
		switch {
		case err == nil || errors.Is(err, ErrNoTransition):
			s.completeCounterInvoiced(ctx, orderID, eventID) // QD-5, consumer.go; a redelivery reaches it too
			// The deposit release (deposit_release.go): the material has
			// left, the order's deposit documents release. A redelivery
			// re-reaches it; the ledger's journal key keeps it exactly once.
			s.releaseSupplierDeposits(ctx, orderID, eventID)
		case errors.Is(err, ErrIllegalTransition) && s.orderSitsAtInvoiced(ctx, orderID):
			// The pos ring's invoice beat the yard to INVOICED, the beat the
			// dispatch handoff names; the material still left, so the deposit
			// releases anyway (a CANCELLED or CLOSED decline does not).
			s.releaseSupplierDeposits(ctx, orderID, eventID)
		}
	}
	return err
}

// - DIB-191 manual order-fulfilment transitions (forward-only matrix) -

// ManualAction is a staff-initiated order-lifecycle command exposed by the transition endpoints
// (handler.go). The event-driven transitions (FULFILLED, INVOICED) are NOT manual actions, they
// bypass this matrix and are graded instead against the forward-only ladder in AdvanceFromEvent, which
// the event consumers call (consumer.go).
type ManualAction string

const (
	ActionConfirm ManualAction = "CONFIRMED" // DRAFT     → CONFIRMED (active in-branch staff), ruling D2
	ActionPicking ManualAction = "PICKING"   // CONFIRMED → PICKING (active in-branch staff)
	ActionReady   ManualAction = "READY"     // PICKING   → READY   (active in-branch staff)
	ActionCancel  ManualAction = "CANCELLED" // {DRAFT,CONFIRMED,PICKING,READY} → CANCELLED (manager, Tier ≤ 2)
)

// transitionRule captures the legality of one manual action: the status it moves TO, the forward-only
// set of source statuses it may be applied FROM, whether it is manager-gated (Tier ≤ 2, destructive),
// and whether a reason is mandatory. The `from` sets encode forward-only, no rule lists a status at or
// past its own target, so a backward move (READY→PICKING) or a re-open is never legal, and no rule
// targets FULFILLED/INVOICED (event-only), CLOSED (never hand-set, D1 ruling item 5), or
// PARTIALLY_FULFILLED (deferred, D1 ruling item 4).
type transitionRule struct {
	target         string
	from           map[string]bool
	managerOnly    bool
	reasonRequired bool
}

// manualRules is the DIB-191 forward-only transition matrix. It is the single source of truth for
// which manual moves are legal and who may make them; the handler and the unit tests both read it.
// manualActionOrder is the deterministic order manual actions are reported in (legal_actions) and
// iterated over for matrix coverage: lifecycle order, with the destructive action last.
var manualActionOrder = []ManualAction{ActionConfirm, ActionPicking, ActionReady, ActionCancel}

var manualRules = map[ManualAction]transitionRule{
	// Ruling D2 (2026-07-28): DRAFT-born orders (quote-convert, portal reorder) had no path into the
	// fulfilment flow, POS sales auto-advance to INVOICED at ring, so the ruled manual path served
	// almost no production orders (audit H3). Confirm is the on-ramp: it gives delivery/will-call
	// orders a real CONFIRMED→PICKING→READY runway.
	ActionConfirm: {target: "CONFIRMED", from: statusSet("DRAFT"), managerOnly: false},
	ActionPicking: {target: "PICKING", from: statusSet("CONFIRMED"), managerOnly: false},
	ActionReady:   {target: "READY", from: statusSet("PICKING"), managerOnly: false},
	ActionCancel:  {target: "CANCELLED", from: statusSet("DRAFT", "CONFIRMED", "PICKING", "READY"), managerOnly: true, reasonRequired: true},
}

func statusSet(ss ...string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}

func (r transitionRule) fromList() []string {
	out := make([]string, 0, len(r.from))
	for s := range r.from {
		out = append(out, s)
	}
	return out
}

// TransitionManual applies a staff-initiated, forward-only status move (PICKING/READY/CANCELLED)
// through the SetStatus choke-point, enforcing the DIB-191 legality matrix ATOMICALLY with the write.
//
// AUTHORITY (checked in this order so the HTTP mapping is deterministic):
//   - an authenticated ACTIVE staff caller           → ErrUnauthorized (401) / ErrForbidden (403 inactive)
//   - a known manual action                           → BadRequest (400)
//   - the order exists in a branch the caller reaches → ErrNotFound (404) / ErrForbidden (403 cross-branch)
//   - a manager-gated action needs Tier ≤ 2           → ErrForbidden (403)
//   - a reason-required action needs a non-empty reason → BadRequest (400)
//
// The forward-only from→to legality is enforced INSIDE the guarded write (UpdateStatusFrom): the move
// lands only when the order's current status is in the rule's `from` set, evaluated under the same row
// lock as the UPDATE, so a concurrent transition can never slip an illegal move through (no TOCTOU).
// A current status outside the set returns ErrIllegalTransition (409); target == current returns
// ErrNoTransition (409, already there). On success exactly one timeline row is emitted, actor = caller.
func (s *Service) TransitionManual(ctx context.Context, orderID string, action ManualAction, reason string) (newStatus string, err error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return "", err
	}
	// Defence-in-depth: the shared route guard already rejects a non-ACTIVE user at authentication
	// (401) before the handler runs, but the choke-point must never advance an order for an inactive
	// caller that reached it by any other path. Mapped to 403 (ErrForbidden).
	if caller.Status != identity.StatusActive {
		return "", ErrForbidden
	}
	rule, ok := manualRules[action]
	if !ok {
		return "", httpx.BadRequestf("unknown order action %q", action)
	}
	if orderID == "" {
		return "", httpx.BadRequestf("order id is required")
	}
	// F3: accept the visible order_no ("#SO-4"/"SO-4") as an alternate key, resolved to the real id
	// ONCE, up front, so every downstream call (branch check, cancel predicate, the guarded write,
	// the timeline write) uses the real id consistently. B2-4: resolution is branch-scoped to the caller,
	// so a cross-branch #SO answers 404 rather than confirming the order exists via the number space.
	rGlobal, rBranches := callerBranchScope(caller)
	orderID, err = s.store.ResolveOrderID(ctx, orderID, rGlobal, rBranches)
	if err != nil {
		return "", err
	}
	// Resolve the owning branch BEFORE any authority-specific rejection: a missing order is reported
	// as not-found (never forbidden), so the authz path never leaks the existence of an order in a
	// branch the caller cannot reach (mirrors OrderHistory and the tenant-safe write choke-point).
	branchID, err := s.store.OrderBranch(ctx, orderID)
	if err != nil {
		return "", err // ErrNotFound for a missing order, no existence leak
	}
	if !caller.CanAccessBranch(branchID) {
		return "", ErrForbidden
	}
	if rule.managerOnly && !caller.IsManager() {
		return "", ErrForbidden
	}
	reason = strings.TrimSpace(reason)
	if rule.reasonRequired && reason == "" {
		return "", httpx.BadRequestf("a cancellation reason is required")
	}
	// Bound the free-text reason so a caller cannot store an unbounded blob on the timeline
	// (audit M: the cancel reason was unbounded). Mirrors maxInstructionsLen on the delivery note.
	if len(reason) > maxReasonLen {
		return "", httpx.BadRequestf("reason exceeds %d characters", maxReasonLen)
	}
	// B2/R2 + B2-2: a cancel must PROVE no money is attached (audit H2), and that proof must run INSIDE
	// the order-row lock so no writer can finalize an invoice in a check→write gap. So rather than run
	// the predicate here (unlocked), the cancel hands it to the guarded write as a precheck, which runs
	// it under the same lock and transaction as the status flip. Non-cancel actions pass nil.
	var precheck func(context.Context) error
	if action == ActionCancel {
		precheck = func(ctx context.Context) error { return s.assertCancellable(ctx, orderID) }
	}
	if err := s.setStatusGuarded(ctx, caller.UserID, orderID, rule.target, reason, rule.fromList(), precheck); err != nil {
		return "", err
	}
	return rule.target, nil
}

// OrderView is the staff order read (B2/R6): the order's current status plus the manual actions THIS
// caller may legally take on it right now. legal_actions is computed server-side from the same
// manualRules the write path enforces, so a client renders controls from the response instead of
// duplicating the matrix, which means a matrix change needs no client redeploy, and the UI cannot
// offer a move the server would refuse.
type OrderView struct {
	ID           string   `json:"id"`
	Status       string   `json:"status"`
	BranchID     string   `json:"branch_id"`
	LegalActions []string `json:"legal_actions"`
	// DIB-175: which fields THIS caller may edit on this order right now, computed server-side from
	// the same editRules the write path enforces (edit.go). Same discipline as legal_actions, the
	// client renders edit controls from this rather than duplicating the matrix, so a control can
	// never be offered for an edit the server would refuse and a matrix change needs no redeploy.
	EditableFields []string `json:"editable_fields"`
	// DIB-175: the order's lines + money, so the staff surface can render what it lets you edit. The
	// portal detail read (GetOrder) cannot serve this, it is tenant-scoped by customer_id, whereas a
	// counter user proves reach by BRANCH. Empty when the edit seam is absent.
	Lines         []OrderViewLine `json:"lines"`
	SubtotalCents int64           `json:"subtotal_cents"`
	TaxCents      int64           `json:"tax_cents"`
	TotalCents    int64           `json:"total_cents"`
	// HasBackorder is true while any line is short and still owed (migration 0369).
	HasBackorder bool `json:"has_backorder"`
	// PayHold is the pay before release block (item 9.3): held or not, the
	// words, order total, paid so far, still owed. Absent when the read's seam
	// is unwired or unreadable, never a block that reads as clear.
	PayHold *OrderPayHold `json:"pay_hold,omitempty"`
}

// OrderRead returns one order's current status + this caller's legal actions. Authority mirrors the
// transition endpoints exactly: unauthenticated → 401, inactive → 403, missing → 404 (resolved BEFORE
// the branch check so a cross-branch order is never distinguishable from a nonexistent one), and
// out-of-branch → 403.
//
// Status is read from orders.status. It is deliberately NOT derived from the status timeline: the audit
// established timeline writes are best-effort, so the timeline cannot be a source of truth.
func (s *Service) OrderRead(ctx context.Context, orderID string) (OrderView, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return OrderView{}, err
	}
	if caller.Status != identity.StatusActive {
		return OrderView{}, ErrForbidden
	}
	if orderID == "" {
		return OrderView{}, httpx.BadRequestf("order id is required")
	}
	// F3: accept the visible order_no ("#SO-4"/"SO-4") as an alternate key.
	rGlobal, rBranches := callerBranchScope(caller)
	orderID, err = s.store.ResolveOrderID(ctx, orderID, rGlobal, rBranches)
	if err != nil {
		return OrderView{}, err
	}
	status, branchID, err := s.store.OrderHeader(ctx, orderID)
	if err != nil {
		return OrderView{}, err // ErrNotFound for a missing order, no existence leak
	}
	if !caller.CanAccessBranch(branchID) {
		return OrderView{}, ErrForbidden
	}
	view := OrderView{
		ID: orderID, Status: status, BranchID: branchID,
		LegalActions:   s.legalActions(ctx, caller, orderID, status),
		EditableFields: s.offeredEditableFields(ctx, caller, orderID, status),
		Lines:          []OrderViewLine{},
		PayHold:        s.payHoldView(ctx, orderID),
	}
	// Lines + money are best-effort: a read that cannot load them still answers status and the
	// available actions, exactly as it did before DIB-175 (an older fake/deploy without the edit seam
	// must not break the lifecycle panel).
	if es, eerr := s.editStore(); eerr == nil {
		if lines, totals, taxCents, lerr := es.OrderLines(ctx, orderID); lerr == nil {
			view.Lines = lines
			for _, l := range lines {
				if r, ok := ratOf(l.BackorderBase); ok && r.Sign() > 0 {
					view.HasBackorder = true
				}
			}
			view.SubtotalCents, view.TaxCents, view.TotalCents = totals.SubtotalCents, taxCents, totals.TotalCents
		}
	}
	return view, nil
}

// offeredEditableFields projects the DIB-175 edit matrix onto this caller + order. The assessed-tax
// fact it needs lives behind the edit store; when that seam is absent (a unit-test fake implementing
// only orderStore, or a deploy without the edit surface) the offer degrades to an EMPTY list rather
// than failing the read, the order view must keep answering status + legal_actions regardless.
func (s *Service) offeredEditableFields(ctx context.Context, caller identity.StaffContext, orderID, status string) []string {
	es, err := s.editStore()
	if err != nil {
		return []string{}
	}
	_, _, taxCents, err := es.OrderEditHeader(ctx, orderID)
	if err != nil {
		return []string{}
	}
	return s.editableFields(ctx, caller, orderID, status, taxCents)
}

// legalActions computes which manual actions this caller may take on an order in this status: the
// forward-only matrix ∧ the caller's authority ∧ (for cancel) the money predicate.
//
// The cancel entry uses canOfferCancel, which is deliberately softer than the write-path
// assertCancellable (supervisor C2): an unwired seam, a blocked order, or a failed predicate read all
// simply OMIT cancel from the offer, none of them may fail the read. The 503 belongs exclusively to an
// actual cancel attempt.
func (s *Service) legalActions(ctx context.Context, caller identity.StaffContext, orderID, status string) []string {
	out := make([]string, 0, len(manualActionOrder))
	for _, action := range manualActionOrder {
		rule := manualRules[action]
		if !rule.from[status] {
			continue // not a legal source status for this move
		}
		if rule.managerOnly && !caller.IsManager() {
			continue // this caller lacks the authority
		}
		if action == ActionCancel && !s.canOfferCancel(ctx, orderID) {
			continue // money attached, or the predicate cannot be evaluated
		}
		out = append(out, string(action))
	}
	return out
}

// setStatusGuarded is the MANUAL-path variant of SetStatus: it moves the order to newStatus ONLY when
// the current status is in allowedFrom (the forward-only guard, enforced atomically in the write), then
// emits the same single attributed timeline row SetStatus emits. Keeping the emission here, not only
// in SetStatus, is what preserves the choke-point invariant that every status writer lands exactly one
// ORDER_STATUS_CHANGED row through one path.
// A non-nil precheck (the cancel money predicate, B2-2) routes the write through the locked-predicate
// variant so the predicate runs under the order-row lock; nil uses the single-statement guarded write.
func (s *Service) setStatusGuarded(ctx context.Context, actor, orderID, newStatus, reason string, allowedFrom []string, precheck func(context.Context) error) error {
	// Item 9.3: the pay before release hold guards the manual and event driven
	// moves into PICKING (this is the path TransitionManual and AdvanceFromEvent
	// take; SetStatus carries its own copy of the guard at the choke point).
	if newStatus == "PICKING" {
		if err := s.refuseHeldFromPicking(ctx, orderID); err != nil {
			return err
		}
	}
	var oldStatus string
	var err error
	// W5 seam 3: when this target has a hold effect AND the reservation lane is available, the
	// transition routes through the tx-scoped writer so the reserve/release commits or rolls back
	// WITH the status. A target with no hold effect, or an unbound seam, takes the pre-existing path
	// unchanged, the reservation lane is strictly additive and cannot alter a transition's legality.
	effect := reservationEffectFor(newStatus)
	if rs, ok := s.reservationLane(); ok && (effect == effectReserve || effect == effectConsume || effect == effectRelease) {
		oldStatus, err = rs.UpdateStatusFromTx(ctx, orderID, newStatus, allowedFrom, precheck,
			s.applyReservationsTx(effect, orderID, actor))
	} else if precheck != nil {
		oldStatus, err = s.store.UpdateStatusFromChecked(ctx, orderID, newStatus, allowedFrom, precheck)
	} else {
		oldStatus, err = s.store.UpdateStatusFrom(ctx, orderID, newStatus, allowedFrom)
	}
	if err != nil {
		return err // ErrNotFound / ErrIllegalTransition / ErrNoTransition / ErrCancelBlocked / ErrCancelUnverifiable
	}
	s.recordStatusChange(ctx, actor, orderID, oldStatus, newStatus, reason)
	return nil
}

// validStatus reports whether status is one of the orders CHECK values (0008).
func validStatus(status string) bool {
	switch status {
	case "DRAFT", "CONFIRMED", "PICKING", "READY", "PARTIALLY_FULFILLED",
		"FULFILLED", "INVOICED", "CLOSED", "CANCELLED":
		return true
	default:
		return false
	}
}
