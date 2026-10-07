// edit.go, DIB-175 staff order-edit. This is the FIRST surface in the codebase that mutates an
// order's CONTENT after creation: until now `orders`/`order_lines` had only creation writers
// (quote convert, POS ring, portal reorder draft) plus the DIB-191 status choke-point. the assistant's
// counter workflow needs line + date edits with a full who/what/when trail (Q-044, sync_register).
//
//	PATCH  /api/v1/orders/{id}/lines/{lineNo}  → change a line's quantity   (manager, Tier ≤ 2)
//	POST   /api/v1/orders/{id}/lines           → add a line                 (manager, Tier ≤ 2)
//	DELETE /api/v1/orders/{id}/lines/{lineNo}  → remove a line              (manager, Tier ≤ 2)
//	PATCH  /api/v1/orders/{id}                 → change the requested date  (active in-branch staff)
//
// THREE GUARDS, ratified 2026-07-29, every one of them enforced here and re-enforced under the row
// lock in the write (edit_repository.go):
//
//  1. The edit-state matrix (editRules): line edits die at READY, PICKING stays open because that is
//     exactly the "customer rang while we were pulling it" case, but a staged load must not have its
//     content changed underneath the yard. Date edits live to FULFILLED.
//  2. Authority: line edits move money (a qty change moves line_total → order subtotal/total), so they
//     are manager-gated exactly like cancel. A date is not money, so it is open to in-branch staff.
//  3. The money predicate: a money-moving edit reuses the DIB-191 cancel-predicate seams
//     (cancel_seam.go) AND requires orders.tax_cents == 0.
//
// Why guard 3 exists. A POS ring creates its order at CONFIRMED (pos/service.go) with assessed tax and
// a finalized invoice, and CONFIRMED is line-editable. Without this guard a rung sale's lines would be
// editable while its invoice, tax and GL postings stood unchanged: the exact H2 divergence class DIB-191
// closed for cancel. Reusing that predicate makes "an edit never leaves a stale tax figure" PROVABLE
// rather than assumed, because the only writer of non-zero order tax is the ring, which always attaches
// a finalized invoice. Tax re-assessment stays M5's, nothing here ever writes tax_cents.
package order

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/jackc/pgx/v5"
)

// - errors -

// NotEditableError is returned when a field is not editable in the order's CURRENT status. It carries
// the status so the 409 can NAME it ("...while the order is READY"), the acceptance criterion is that
// a blocked state answers 409 with the status named, so a caller never has to guess why.
type NotEditableError struct {
	Field  EditField
	Status string
}

func (e *NotEditableError) Error() string {
	return fmt.Sprintf("order: %s cannot be edited while the order is %s", e.Field.describe(), e.Status)
}

// ErrEditBlocked: a money-moving edit was refused because money is already attached to the order (a
// finalized invoice or a committed pos_sale). Editing would orphan the invoice/payment/GL state, so the
// caller must use the POS returns flow. Mapped to 409. Mirrors ErrCancelBlocked.
var ErrEditBlocked = errors.New("order: edit blocked, order has transacted money state")

// ErrEditTaxAssessed: a money-moving edit was refused because the order carries assessed tax. Order
// totals would recompute while tax_cents stood, leaving a silently wrong total. Tax re-assessment is
// M5's, unbuilt, so this fails closed rather than writing money we cannot make correct. Mapped to 409.
var ErrEditTaxAssessed = errors.New("order: edit blocked, order carries assessed tax that cannot be re-assessed here")

// ErrEditUnverifiable: the money predicate could not be EVALUATED (its seams are unwired). The
// predicate's job is to prove no money is attached; a nil seam proves nothing, so the edit is refused
// rather than silently allowed (fail closed). Mapped to 503 so the operator sees a wiring fault, not a
// business refusal. main.go asserts the wiring at boot, so this is unreachable in a correct deploy.
var ErrEditUnverifiable = errors.New("order: edit predicate unavailable, cancel guards not wired")

// ErrLineNotFound: no such line_no on this order. Mapped to 404.
var ErrLineNotFound = errors.New("order: line not found")

// ErrLineInUse: the line has allocated or fulfilled quantity, so it may not be removed (and a qty edit
// may not take it below what is already fulfilled, the 0008 CHECK would refuse it anyway; naming it
// here turns an opaque constraint violation into a clear refusal). Mapped to 409.
var ErrLineInUse = errors.New("order: line has allocated or fulfilled quantity")

// ErrEditNotConfigured: the store or the pricing/inventory seams needed to price an edited line are not
// wired. Fails closed (503), an unpriceable line must never be persisted at a stale or zero price.
var ErrEditNotConfigured = errors.New("order: edit seams not configured")

// - the ratified edit-state matrix -

// EditField is a staff edit COMMAND (what the caller asked to do). It is deliberately distinct from the
// audit `field` vocabulary (the COLUMN names that actually moved, recorded per changed column below): a
// single qty command can move qty_entered, qty_base, unit_price_cents and line_total_cents at once.
type EditField string

const (
	FieldLineQty       EditField = "line_qty"
	FieldLineAdd       EditField = "line_add"
	FieldLineRemove    EditField = "line_remove"
	FieldRequestedDate EditField = "requested_date"
)

// describe renders an EditField for the 409 message ("line quantity cannot be edited while … READY").
func (f EditField) describe() string {
	switch f {
	case FieldLineQty:
		return "line quantity"
	case FieldLineAdd:
		return "order lines"
	case FieldLineRemove:
		return "order lines"
	case FieldRequestedDate:
		return "the requested date"
	default:
		return string(f)
	}
}

// editRule is the legality of one edit command: the set of statuses it may be applied FROM, whether it
// is manager-gated, and whether it moves money (and therefore needs the money predicate).
type editRule struct {
	from        map[string]bool
	managerOnly bool
	movesMoney  bool
}

// editFieldOrder is the deterministic order edit fields are reported in (editable_fields on the order
// read) and iterated over for matrix coverage in the tests.
var editFieldOrder = []EditField{FieldLineQty, FieldLineAdd, FieldLineRemove, FieldRequestedDate}

// editRules is the RATIFIED edit-state matrix and the single source of truth for it. The write path,
// the server-computed editable_fields on the order read, and the matrix tests all read THIS map, so
// the client can never render a control the server would refuse, and a matrix change needs no client
// redeploy (the same discipline manualRules gives the transition controls).
//
//	                    DRAFT CONFIRMED PICKING READY PART_FUL FULFILLED INVOICED CLOSED CANCELLED
//	line qty/add/remove   ✅      ✅        ✅      ❌      ❌        ❌        ❌       ❌       ❌
//	requested_date        ✅      ✅        ✅      ✅      ✅        ❌        ❌       ❌       ❌
//
// No rule lists FULFILLED, INVOICED, CLOSED or CANCELLED: past FULFILLED the stock has moved and the
// money is settled, so content and dates are history, corrections go through the POS returns flow.
var editRules = map[EditField]editRule{
	FieldLineQty:    {from: statusSet("DRAFT", "CONFIRMED", "PICKING"), managerOnly: true, movesMoney: true},
	FieldLineAdd:    {from: statusSet("DRAFT", "CONFIRMED", "PICKING"), managerOnly: true, movesMoney: true},
	FieldLineRemove: {from: statusSet("DRAFT", "CONFIRMED", "PICKING"), managerOnly: true, movesMoney: true},
	FieldRequestedDate: {
		from:        statusSet("DRAFT", "CONFIRMED", "PICKING", "READY", "PARTIALLY_FULFILLED"),
		managerOnly: false, movesMoney: false,
	},
}

func (r editRule) fromList() []string {
	out := make([]string, 0, len(r.from))
	for s := range r.from {
		out = append(out, s)
	}
	return out
}

// - bounds -

// maxEditQty bounds an edited quantity, applied to BOTH the entered qty and the derived base qty.
//
// This is not an arbitrary business cap: it is what keeps the audit trail truthful. Quantities ride
// into the audit `before`/`after` fields as DECIMAL strings, and the audit boundary redacts any string
// containing 7+ consecutive digits (audit.redactCleartext, a PAN/SIN/registry-number defence). A qty
// of 1,000,000 would therefore land in the trail as "[redacted]", silently destroying the before→after
// reconstruction this feature exists to provide. Bounding below the threshold means a legitimate value
// can never be redacted. The base qty is bounded too because a UOM factor multiplies it (999,999
// pallets × 100 would clear the threshold even though the entered qty does not).
const maxEditQty = 999999

// maxAddDescription bounds a free-text description on an added line, mirroring maxInstructionsLen on
// the delivery note. The description is NOT auditable content (it never enters the change trail, see
// the audit allowlist note), it is only persisted on the line.
const maxAddDescription = 500

// - models -

// LineSnapshot is the pre- or post-image of one order line, everything the change trail needs to
// reconstruct what a line looked like before and after an edit.
type LineSnapshot struct {
	LineNo         int
	ProductID      string
	QtyEntered     string
	QtyBase        string
	UnitPriceCents int64
	LineTotalCents int64
}

// OrderTotals is the recomputed order money after an edit. tax_cents is deliberately absent: an edit
// never writes it (M5 owns tax) and a money-moving edit is only permitted when it is already zero.
type OrderTotals struct {
	SubtotalCents int64
	TotalCents    int64
}

// OrderViewLine is one line as the STAFF order surface renders it: the quantity trio, the money, and
// the allocated/fulfilled progress that decides whether the line may still be cut or removed.
type OrderViewLine struct {
	LineNo         int    `json:"line_no"`
	ProductID      string `json:"product_id"`
	Description    string `json:"description"`
	QtyEntered     string `json:"qty_entered"`
	UOMEntered     string `json:"uom_entered"`
	QtyBase        string `json:"qty_base"`
	AllocatedBase  string `json:"allocated_base"`
	FulfilledBase  string `json:"fulfilled_base"`
	UnitPriceCents int64  `json:"unit_price_cents"`
	LineTotalCents int64  `json:"line_total_cents"`
	// BackorderEntered and BackorderBase are the quantity the yard picked short and the order still
	// owes the customer (migration 0366), in the line's own unit and in base. The line's quantity
	// above is what is to be fulfilled now; ordered = quantity + backorder.
	BackorderEntered string `json:"backorder_entered"`
	BackorderBase    string `json:"backorder_base"`
}

// EditResult is what a successful edit returns to the caller: the new line state (absent for a
// removal), the recomputed order totals, and the order's status at the moment the write landed.
type EditResult struct {
	Line   *LineSnapshot `json:"line,omitempty"`
	Totals OrderTotals   `json:"-"`
	Status string        `json:"status"`
}

// lineEditWrite is the write shape passed into the guarded repository mutation.
type lineEditWrite struct {
	OrderID     string
	LineNo      int
	AllowedFrom []string
	// Priced values (absent for a removal).
	ProductID      string
	Description    string
	QtyEntered     string
	UOMEntered     string
	QtyBase        string
	UnitPriceCents int64
	PriceUOM       string
	LineTotalCents int64
}

// editStore is the extra persistence the edit surface needs. It is asserted OFF the injected orderStore
// (the reorderStore pattern, reorder.go) rather than widened into orderStore itself, so every existing
// service fake keeps compiling; *Repository satisfies it in production.
type editStore interface {
	// OrderEditHeader reads the status, owning branch and assessed tax of one order, the three facts
	// every edit guard needs, in a single query. Missing id → ErrNotFound (never an existence leak).
	OrderEditHeader(ctx context.Context, orderID string) (status, branchID string, taxCents int64, err error)
	// LineForEdit reads one line's current state plus its allocated/fulfilled progress, so the service
	// can reprice it and enforce the fulfilment invariants. Missing line → ErrLineNotFound.
	LineForEdit(ctx context.Context, orderID string, lineNo int) (snap LineSnapshot, uomEntered string, allocatedBase, fulfilledBase float64, err error)
	// UpdateLineGuarded / InsertLineGuarded / DeleteLineGuarded apply one line mutation AND recompute
	// the order totals in ONE transaction under the order row lock, with the status guard re-evaluated
	// inside that lock. The hooks fold the money predicate (precheck, B2-2) and the audit trail (record,
	// B2-1) INTO that same transaction. See edit_repository.go for why none of it is redundant.
	UpdateLineGuarded(ctx context.Context, w lineEditWrite, hooks lineEditHooks) (before, after LineSnapshot, totals OrderTotals, status string, err error)
	InsertLineGuarded(ctx context.Context, w lineEditWrite, hooks lineEditHooks) (after LineSnapshot, totals OrderTotals, status string, err error)
	DeleteLineGuarded(ctx context.Context, orderID string, lineNo int, allowedFrom []string, hooks lineEditHooks) (before LineSnapshot, totals OrderTotals, status string, err error)
	// UpdateRequestedDateGuarded moves orders.requested_date under the same lock + status guard and
	// records the change on the same tx (a date is not money → no precheck).
	UpdateRequestedDateGuarded(ctx context.Context, orderID, newDate string, allowedFrom []string, hooks dateEditHooks) (before, after, status string, err error)
	// OrderPricingContext resolves the customer + branch an edited line must be priced for.
	OrderPricingContext(ctx context.Context, orderID string) (customerID, branchID string, err error)
	// OrderLines reads an order's lines for the STAFF order read. The existing GetOrder detail read is
	// tenant-scoped (a portal caller proving ownership by customer_id); a counter user proves reach by
	// BRANCH instead, so it cannot be reused here. Branch scope is enforced above, in OrderRead.
	OrderLines(ctx context.Context, orderID string) ([]OrderViewLine, OrderTotals, int64, error)
}

// lineEditHooks are the two closures the service threads into a guarded LINE write so the money
// predicate and the audit trail run INSIDE the same transaction + order-row lock as the mutation.
type lineEditHooks struct {
	// precheck runs AFTER the order row is locked and BEFORE the mutation, with tax_cents read under
	// that lock. A non-nil error aborts the edit (rollback). This is where the money predicate (B2-2)
	// lives so no lock-respecting writer can attach money in a check→write gap. Its GUARANTEE and its
	// BOUNDARY are documented at the invocation site (runPrecheck, edit_repository.go). nil = no
	// predicate (a date edit).
	precheck func(ctx context.Context, taxCents int64) error
	// record runs AFTER the mutation + totals recompute and BEFORE commit, on the SAME tx. A non-nil
	// error ROLLS BACK the whole edit (B2-1: the trail is atomic with the write). before is zero for an
	// add; after is zero for a remove.
	record func(ctx context.Context, tx pgx.Tx, before, after LineSnapshot, totals OrderTotals) error
	// cleared runs inside the same tx when the edit cleared a short line's backorder (see
	// yard_pick_edit.go). nil = no one is listening; the clearing still happens.
	cleared func(ctx context.Context, tx pgx.Tx, c ClearedBackorder) error
}

// dateEditHooks is the record-only hook for the requested-date write (not money-moving → no precheck).
type dateEditHooks struct {
	record func(ctx context.Context, tx pgx.Tx, before, after string) error
}

func (s *Service) editStore() (editStore, error) {
	es, ok := s.store.(editStore)
	if !ok {
		return nil, ErrEditNotConfigured
	}
	return es, nil
}

// CustomerTypeResolver resolves an order's customer pricing tier (CONTRACTOR/RETAIL/…), which the
// resolver needs to price an edited line at the rate that account actually gets. Satisfied in the
// composition root by an adapter over *customer.Service.ResolveAccount, the same fact the portal
// reorder path passes in as ReorderContext.CustomerType (portal/reorder.go), reached here through a
// narrow seam so the order module never imports the customer package.
type CustomerTypeResolver interface {
	CustomerType(ctx context.Context, customerID string) (string, error)
}

// WithCustomerType injects the pricing-tier seam used when repricing an edited line.
func (s *Service) WithCustomerType(r CustomerTypeResolver) *Service { s.custType = r; return s }

// EditSeamsWired reports whether everything a money-moving edit needs is present: the pricing resolver,
// the cost-basis reader, the customer-tier resolver, and the cancel-predicate guards. main.go asserts
// this at boot (fail fast) so a dropped wiring cannot reach runtime.
func (s *Service) EditSeamsWired() bool {
	return s.pricer != nil && s.inv != nil && s.custType != nil && s.CancelGuardsWired()
}

// - authority -

// authorizeEdit runs the authority ladder shared by every edit command, in the SAME order
// TransitionManual uses, so the HTTP mapping is deterministic and a cross-branch order is never
// distinguishable from a nonexistent one:
//
//	authenticated caller            → ErrUnauthorized (401)
//	ACTIVE caller                   → ErrForbidden    (403)
//	known edit field                → BadRequest      (400)
//	order exists                    → ErrNotFound     (404)  ← resolved BEFORE any authz rejection
//	caller reaches the order branch  → ErrForbidden   (403)
//	manager tier for a gated field  → ErrForbidden    (403)
//	field editable in this status   → NotEditableError(409, status named)
//	money predicate (money fields)  → ErrEditBlocked / ErrEditTaxAssessed (409) / ErrEditUnverifiable (503)
//
// It returns the caller, the rule, and the RESOLVED order id. The status it checked is deliberately not
// returned: it is ADVISORY only, because the authoritative re-check happens inside the guarded write
// under the order row lock, a concurrent transition must not be able to slip an edit into a now-READY
// order (the TOCTOU class DIB-191 closed for status).
func (s *Service) authorizeEdit(ctx context.Context, orderID string, field EditField) (identity.StaffContext, editRule, string, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return identity.StaffContext{}, editRule{}, "", err
	}
	if caller.Status != identity.StatusActive {
		return identity.StaffContext{}, editRule{}, "", ErrForbidden
	}
	rule, ok := editRules[field]
	if !ok {
		return identity.StaffContext{}, editRule{}, "", httpx.BadRequestf("unknown editable field %q", field)
	}
	if orderID == "" {
		return identity.StaffContext{}, editRule{}, "", httpx.BadRequestf("order id is required")
	}
	// DIB-379/F3: accept the visible order_no ("#SO-4"/"SO-4") as an alternate key, resolved to the
	// real id ONCE up front, so the branch check, the money predicate, the guarded write and the
	// change trail all key off the same resolved id. Same placement TransitionManual uses. B2-4: the
	// resolution is branch-scoped to the caller so a cross-branch #SO answers 404, not an oracle.
	rGlobal, rBranches := callerBranchScope(caller)
	orderID, err = s.store.ResolveOrderID(ctx, orderID, rGlobal, rBranches)
	if err != nil {
		return identity.StaffContext{}, editRule{}, "", err
	}
	es, err := s.editStore()
	if err != nil {
		return identity.StaffContext{}, editRule{}, "", err
	}
	// tax_cents is intentionally NOT read here for the money check: the write-path money predicate (B2-2)
	// runs INSIDE the guarded write under the order-row lock, against the tax read under that lock, so
	// there is no check→write window. authorizeEdit only does the cheap fail-fast gates (branch, tier,
	// status) so an obviously-illegal edit is refused without taking a lock.
	status, branchID, _, err := es.OrderEditHeader(ctx, orderID)
	if err != nil {
		return identity.StaffContext{}, editRule{}, "", err // ErrNotFound, no existence leak
	}
	if !caller.CanAccessBranch(branchID) {
		return identity.StaffContext{}, editRule{}, "", ErrForbidden
	}
	if rule.managerOnly && !caller.IsManager() {
		return identity.StaffContext{}, editRule{}, "", ErrForbidden
	}
	if !rule.from[status] {
		return identity.StaffContext{}, editRule{}, orderID, &NotEditableError{Field: field, Status: status}
	}
	return caller, rule, orderID, nil
}

// lineMoneyPrecheck builds the money predicate the guarded LINE writes run under the order-row lock
// (B2-2). taxCents is supplied by the lock, so the whole predicate, no finalized invoice, no committed
// sale, tax == 0, is evaluated against the locked snapshot immediately before the mutation.
func (s *Service) lineMoneyPrecheck(orderID string) func(context.Context, int64) error {
	return func(ctx context.Context, taxCents int64) error {
		return s.assertEditableMoney(ctx, orderID, taxCents)
	}
}

// lineAuditHook builds the record hook the guarded LINE writes run on their transaction (B2-1): it
// emits the per-column ORDER_CHANGED rows for `cmd` and RETURNS any error so the edit rolls back.
func (s *Service) lineAuditHook(actor, orderID string, cmd EditField) func(context.Context, pgx.Tx, LineSnapshot, LineSnapshot, OrderTotals) error {
	return func(ctx context.Context, tx pgx.Tx, before, after LineSnapshot, totals OrderTotals) error {
		return s.recordLineChange(ctx, tx, actor, orderID, cmd, before, after, totals)
	}
}

// assertEditableMoney proves no money is attached before a money-moving edit, reusing the DIB-191
// cancel-predicate seams verbatim (assertCancellable, cancel_seam.go) and adding the tax check. The
// cancel vocabulary is remapped to edit-specific errors so the HTTP message names the right refusal.
func (s *Service) assertEditableMoney(ctx context.Context, orderID string, taxCents int64) error {
	if err := s.assertCancellable(ctx, orderID); err != nil {
		switch {
		case errors.Is(err, ErrCancelBlocked):
			return ErrEditBlocked
		case errors.Is(err, ErrCancelUnverifiable):
			return ErrEditUnverifiable
		}
		return err // a real read failure, never an implicit allow
	}
	if taxCents != 0 {
		return ErrEditTaxAssessed
	}
	return nil
}

// - pricing -

// priceLine resolves the pre-tax price trio for (product, qty, uom) at today's rates through the
// pricing seam, the SAME seam and the same cost-basis input the portal reorder path uses
// (reorder.gradeReorderLines). The order module does not own UOM factor math or price derivation, so it
// asks pricing rather than reimplementing either; that also keeps the A-20 margin gate live on an
// edited line, so a staff edit cannot quietly push a line below floor.
//
// A nil seam FAILS CLOSED (ErrEditNotConfigured → 503): persisting a line at a stale or zero price
// would be worse than refusing the edit.
func (s *Service) priceLine(ctx context.Context, customerID, branchID, productID, uom string, qty float64) (pricing.LinePrice, error) {
	if s.pricer == nil || s.inv == nil || s.custType == nil {
		return pricing.LinePrice{}, ErrEditNotConfigured
	}
	custType, err := s.custType.CustomerType(ctx, customerID)
	if err != nil {
		return pricing.LinePrice{}, fmt.Errorf("resolve customer pricing tier: %w", err)
	}
	// Cost basis feeds the resolver's derived retail anchor (cost × markup) for products with no
	// explicit list price, mirrors reorder. found=false simply leaves it 0.
	cost, _, err := s.inv.CurrentCostCents(ctx, productID, branchID)
	if err != nil {
		return pricing.LinePrice{}, fmt.Errorf("cost basis: %w", err)
	}
	// ResolvePrice (not DeriveLinePrice) because PriceResolver, the seam reorder already injects, is
	// deliberately the narrow one-method interface; widening it would break every existing fake. The
	// Breakdown already carries the whole trio, so the projection below IS DeriveLinePrice's body.
	bd, err := s.pricer.ResolvePrice(ctx, pricing.ResolveInput{
		CustomerID: customerID, CustomerType: custType,
		ProductID: productID, BranchID: branchID,
		QtyEntered: qty, UOMEntered: uom, CostCents: cost,
	})
	if err != nil {
		return pricing.LinePrice{}, httpx.BadRequestf("cannot price this line: %v", err)
	}
	if bd.Blocked {
		// Below the margin floor. Refuse rather than commit, the A-20 override/approval queue is the
		// sanctioned way past the gate, exactly as it is on the create paths.
		return pricing.LinePrice{}, &PriceBlockedError{Policy: bd.BlockPolicy}
	}
	if bd.QtyBase > maxEditQty {
		return pricing.LinePrice{}, httpx.BadRequestf(
			"resulting base quantity exceeds the %d maximum for an edited line", maxEditQty)
	}
	return pricing.LinePrice{
		UnitPriceCents: bd.FinalCents,
		PriceUOM:       bd.PriceUOM,
		LineTotalCents: bd.LineTotalCents,
		QtyBase:        bd.QtyBase,
		Breakdown:      bd,
	}, nil
}

// PriceBlockedError is returned when repricing an edited line lands below the margin floor. Carries the
// policy so the 409 can name it. Mapped to 409, a legitimate business refusal, not a fault.
type PriceBlockedError struct{ Policy string }

func (e *PriceBlockedError) Error() string {
	if e.Policy == "" {
		return "order: edited line is below the margin floor"
	}
	return fmt.Sprintf("order: edited line is below the margin floor (%s)", e.Policy)
}

// - write paths -

// EditLineQty changes one line's quantity, reprices it through the pricing seam, recomputes the order
// totals, and records every column that moved on the change trail.
func (s *Service) EditLineQty(ctx context.Context, orderID string, lineNo int, qtyEntered string) (EditResult, error) {
	caller, rule, orderID, err := s.authorizeEdit(ctx, orderID, FieldLineQty)
	if err != nil {
		return EditResult{}, err
	}
	qty, err := parseEditQty(qtyEntered)
	if err != nil {
		return EditResult{}, err
	}
	es, err := s.editStore()
	if err != nil {
		return EditResult{}, err
	}
	cur, uom, _, fulfilled, err := es.LineForEdit(ctx, orderID, lineNo)
	if err != nil {
		return EditResult{}, err
	}
	if cur.ProductID == "" {
		// A free-text line has no product to price against; the resolver would refuse it anyway.
		return EditResult{}, httpx.BadRequestf("line %d has no catalogue product and cannot be repriced", lineNo)
	}
	customerID, branchID, err := s.orderPricingContext(ctx, orderID)
	if err != nil {
		return EditResult{}, err
	}
	lp, err := s.priceLine(ctx, customerID, branchID, cur.ProductID, uom, qty)
	if err != nil {
		return EditResult{}, err
	}
	// The 0008 CHECK (fulfilled_base <= qty_base) would refuse this as an opaque constraint violation;
	// naming it here turns it into a clear 409 that tells the caller what is already fulfilled.
	if lp.QtyBase < fulfilled {
		return EditResult{}, fmt.Errorf("%w: %.4f already fulfilled", ErrLineInUse, fulfilled)
	}
	var cleared []ClearedBackorder
	_, after, totals, status, err := es.UpdateLineGuarded(ctx, lineEditWrite{
		OrderID: orderID, LineNo: lineNo, AllowedFrom: rule.fromList(),
		QtyEntered: qtyEntered, QtyBase: formatQty(lp.QtyBase),
		UnitPriceCents: lp.UnitPriceCents, PriceUOM: lp.PriceUOM, LineTotalCents: lp.LineTotalCents,
	}, lineEditHooks{
		precheck: s.lineMoneyPrecheck(orderID),
		record:   s.lineAuditHook(caller.UserID, orderID, FieldLineQty),
		cleared:  s.clearedHook(caller.UserID, orderID, &cleared),
	})
	if err != nil {
		return EditResult{}, nameEditField(err, FieldLineQty)
	}
	s.announceCleared(ctx, caller.UserID, orderID, cleared)
	return EditResult{Line: &after, Totals: totals, Status: status}, nil
}

// AddLine appends a new line to the order at the next free line_no, priced at today's rates.
func (s *Service) AddLine(ctx context.Context, orderID, productID, qtyEntered, uom, description string) (EditResult, error) {
	caller, rule, orderID, err := s.authorizeEdit(ctx, orderID, FieldLineAdd)
	if err != nil {
		return EditResult{}, err
	}
	if strings.TrimSpace(productID) == "" {
		return EditResult{}, httpx.BadRequestf("product_id is required")
	}
	// products.id is a uuid column; a malformed id would otherwise reach the INSERT and raise
	// SQLSTATE 22P02 → 500. Refuse it at the boundary as the 400 it is.
	if !looksLikeUUID(productID) {
		return EditResult{}, httpx.BadRequestf("product_id must be a uuid")
	}
	if strings.TrimSpace(uom) == "" {
		return EditResult{}, httpx.BadRequestf("uom_entered is required")
	}
	if len(description) > maxAddDescription {
		return EditResult{}, httpx.BadRequestf("description exceeds %d characters", maxAddDescription)
	}
	qty, err := parseEditQty(qtyEntered)
	if err != nil {
		return EditResult{}, err
	}
	es, err := s.editStore()
	if err != nil {
		return EditResult{}, err
	}
	customerID, branchID, err := s.orderPricingContext(ctx, orderID)
	if err != nil {
		return EditResult{}, err
	}
	// C1: an added line goes through the SAME pricing seam a qty edit does, never a caller-supplied
	// price, so a new line cannot enter the order at an unresolved or hand-set rate.
	lp, err := s.priceLine(ctx, customerID, branchID, productID, uom, qty)
	if err != nil {
		return EditResult{}, err
	}
	after, totals, status, err := es.InsertLineGuarded(ctx, lineEditWrite{
		OrderID: orderID, AllowedFrom: rule.fromList(),
		ProductID: productID, Description: description,
		QtyEntered: qtyEntered, UOMEntered: uom, QtyBase: formatQty(lp.QtyBase),
		UnitPriceCents: lp.UnitPriceCents, PriceUOM: lp.PriceUOM, LineTotalCents: lp.LineTotalCents,
	}, lineEditHooks{
		precheck: s.lineMoneyPrecheck(orderID),
		record:   s.lineAuditHook(caller.UserID, orderID, FieldLineAdd),
	})
	if err != nil {
		return EditResult{}, nameEditField(err, FieldLineAdd)
	}
	return EditResult{Line: &after, Totals: totals, Status: status}, nil
}

// RemoveLine deletes a line and recomputes the order totals.
//
// C1: the removal's audit row snapshots the line's product_id and qty_base BEFORE the delete, the row
// is gone afterwards, so without the pre-image the trail could say only "a line was removed" and never
// WHAT was removed. The guarded delete RETURNS that pre-image from inside the same transaction.
func (s *Service) RemoveLine(ctx context.Context, orderID string, lineNo int) (EditResult, error) {
	caller, rule, orderID, err := s.authorizeEdit(ctx, orderID, FieldLineRemove)
	if err != nil {
		return EditResult{}, err
	}
	es, err := s.editStore()
	if err != nil {
		return EditResult{}, err
	}
	// Refuse a line the yard has already committed against. Checked here for a clear message, and
	// re-checked inside the guarded delete under the row lock so a concurrent allocation cannot race it.
	_, _, allocated, fulfilled, err := es.LineForEdit(ctx, orderID, lineNo)
	if err != nil {
		return EditResult{}, err
	}
	if allocated > 0 || fulfilled > 0 {
		return EditResult{}, fmt.Errorf("%w: %.4f allocated, %.4f fulfilled", ErrLineInUse, allocated, fulfilled)
	}
	var cleared []ClearedBackorder
	_, totals, status, err := es.DeleteLineGuarded(ctx, orderID, lineNo, rule.fromList(), lineEditHooks{
		precheck: s.lineMoneyPrecheck(orderID),
		record:   s.lineAuditHook(caller.UserID, orderID, FieldLineRemove),
		cleared:  s.clearedHook(caller.UserID, orderID, &cleared),
	})
	if err != nil {
		return EditResult{}, nameEditField(err, FieldLineRemove)
	}
	s.announceCleared(ctx, caller.UserID, orderID, cleared)
	return EditResult{Totals: totals, Status: status}, nil
}

// SetRequestedDate moves the order's requested date. An empty newDate CLEARS it (the column is
// nullable). This is the one edit that is not money-moving, so it is open to any active in-branch staff
// and needs no money predicate.
func (s *Service) SetRequestedDate(ctx context.Context, orderID, newDate string) (EditResult, error) {
	caller, rule, orderID, err := s.authorizeEdit(ctx, orderID, FieldRequestedDate)
	if err != nil {
		return EditResult{}, err
	}
	newDate = strings.TrimSpace(newDate)
	if newDate != "" {
		if _, perr := time.Parse(dateLayout, newDate); perr != nil {
			return EditResult{}, httpx.BadRequestf("requested_date must be YYYY-MM-DD")
		}
	}
	es, err := s.editStore()
	if err != nil {
		return EditResult{}, err
	}
	_, _, status, err := es.UpdateRequestedDateGuarded(ctx, orderID, newDate, rule.fromList(), dateEditHooks{
		record: func(ctx context.Context, tx pgx.Tx, before, after string) error {
			return s.recordFieldChange(ctx, tx, caller.UserID, orderID, 0, "requested_date", before, after)
		},
	})
	if err != nil {
		return EditResult{}, nameEditField(err, FieldRequestedDate)
	}
	return EditResult{Status: status}, nil
}

// dateLayout is the wire + storage format for orders.requested_date (a DATE column).
const dateLayout = "2006-01-02"

// nameEditField completes a *NotEditableError raised by the guarded write. The repository re-evaluates
// the status guard under the row lock (edit_repository.go) but only knows the STATUS it found, which
// command was attempted is the service's knowledge, so the field is filled in here. Any other error
// passes through untouched.
func nameEditField(err error, field EditField) error {
	var ne *NotEditableError
	if errors.As(err, &ne) && ne.Field == "" {
		ne.Field = field
	}
	return err
}

// orderPricingContext resolves the customer + branch an edited line must be priced for.
func (s *Service) orderPricingContext(ctx context.Context, orderID string) (customerID, branchID string, err error) {
	es, err := s.editStore()
	if err != nil {
		return "", "", err
	}
	return es.OrderPricingContext(ctx, orderID)
}

// - quantity helpers -

// parseEditQty validates an entered quantity: a positive decimal, bounded by maxEditQty. Quantities
// stay STRINGS on the wire and in storage (house rule: qty is DECIMAL, never float); the float is only
// what the pricing resolver's input shape takes.
func parseEditQty(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, httpx.BadRequestf("qty_entered is required")
	}
	q, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, httpx.BadRequestf("qty_entered must be a decimal quantity")
	}
	if q <= 0 {
		// A zero/negative quantity is a removal, not an edit, keep the two commands distinct so the
		// change trail says which one the operator actually performed.
		return 0, httpx.BadRequestf("qty_entered must be greater than zero, remove the line instead")
	}
	if q > maxEditQty {
		return 0, httpx.BadRequestf("qty_entered exceeds the %d maximum for an edited line", maxEditQty)
	}
	return q, nil
}

// formatQty renders a resolved quantity at DECIMAL(19,4) precision, so what we persist and what we
// record on the trail are byte-identical to what the column stores.
func formatQty(q float64) string { return strconv.FormatFloat(q, 'f', 4, 64) }

// - the change trail -

// recordLineChange emits one ORDER_CHANGED audit row per COLUMN that actually moved on a line edit,
// plus the order-level totals, all on the caller's transaction `q` (B2-1), so a recorder failure
// aborts the whole edit rather than committing a mutation with no trail. Every row shares the request
// id (audit.Service stamps it from context), so a multi-column edit correlates back into one operator
// action on the read side without a dedicated table or a correlation column.
//
// Emitting per column, rather than one row per command, is what makes the trail reconstructable: a
// qty edit can also move unit_price_cents (quantity-break pricing) and line_total_cents, and a reader
// must be able to see each of those transitions rather than infer them.
//
// The FIRST emit error is returned (and rolls the edit back); the ordered emit sequence is what gives
// the rows their causal seq order (0078) within the tx, so a partial write can never land.
func (s *Service) recordLineChange(ctx context.Context, q audit.Execer, actor, orderID string, cmd EditField, before, after LineSnapshot, totals OrderTotals) error {
	if s.auditTx == nil {
		return nil // no transactional recorder wired (a fake without RecordTx), degenerate, no trail
	}
	lineNo := after.LineNo
	if cmd == FieldLineRemove {
		lineNo = before.LineNo
	}
	// emits is the ordered column list for this command; each is written in turn on the tx.
	var emits []func() error
	str := func(field, b, a string) func() error {
		return func() error { return s.recordFieldChange(ctx, q, actor, orderID, lineNo, field, b, a) }
	}
	num := func(field string, b, a *int64) func() error {
		return func() error { return s.recordIntChange(ctx, q, actor, orderID, lineNo, field, b, a) }
	}
	switch cmd {
	case FieldLineAdd:
		// A creation has no pre-image; record WHAT was added so the trail names the product and qty.
		emits = append(emits,
			str("line_added", "", after.ProductID),
			str("qty_entered", "", after.QtyEntered),
			str("qty_base", "", after.QtyBase),
			num("unit_price_cents", nil, &after.UnitPriceCents),
			num("line_total_cents", nil, &after.LineTotalCents))
	case FieldLineRemove:
		// C1: the pre-image is the ONLY record of what was removed, the row no longer exists.
		emits = append(emits,
			str("line_removed", before.ProductID, ""),
			str("qty_base", before.QtyBase, ""),
			num("line_total_cents", &before.LineTotalCents, nil))
	default:
		// A qty edit: record only the columns that genuinely moved, so the trail carries no noise rows.
		emits = append(emits,
			str("qty_entered", before.QtyEntered, after.QtyEntered),
			str("qty_base", before.QtyBase, after.QtyBase),
			num("unit_price_cents", &before.UnitPriceCents, &after.UnitPriceCents),
			num("line_total_cents", &before.LineTotalCents, &after.LineTotalCents))
	}
	// The order-level money that every line edit moves. Recorded against line 0 (the header).
	subtotal, total := totals.SubtotalCents, totals.TotalCents
	emits = append(emits,
		func() error { return s.recordIntChange(ctx, q, actor, orderID, 0, "subtotal_cents", nil, &subtotal) },
		func() error { return s.recordIntChange(ctx, q, actor, orderID, 0, "total_cents", nil, &total) })
	for _, emit := range emits {
		if err := emit(); err != nil {
			return fmt.Errorf("order-edit audit: %w", err)
		}
	}
	return nil
}

// recordFieldChange writes one ORDER_CHANGED row for a string-valued column on the caller's tx. A no-op
// change writes nothing (the trail records transitions, not attempts) and is not an error.
func (s *Service) recordFieldChange(ctx context.Context, q audit.Execer, actor, orderID string, lineNo int, field, before, after string) error {
	if s.auditTx == nil || before == after {
		return nil
	}
	extra := map[string]any{"field": field, "line_no": lineNo}
	if before != "" {
		extra["before"] = before
	}
	if after != "" {
		extra["after"] = after
	}
	return s.auditTx.RecordTx(ctx, q, audit.SensitiveAccess{
		Action: audit.ActOrderChanged, EntityType: "order", EntityID: orderID,
		ActorID: actor, Purpose: "STAFF_ORDER_EDIT", Reason: field,
		Extra: extra,
	})
}

// recordIntChange writes one ORDER_CHANGED row for an integer (money) column on the caller's tx. Money
// rides as int64 precisely because a non-string allowlisted value bypasses the audit boundary's
// cleartext redaction, a cents figure can never be mistaken for an identifier and redacted away. nil
// means "no such image" (a creation has no before; a removal has no after).
func (s *Service) recordIntChange(ctx context.Context, q audit.Execer, actor, orderID string, lineNo int, field string, before, after *int64) error {
	if s.auditTx == nil {
		return nil
	}
	if before != nil && after != nil && *before == *after {
		return nil // unchanged, no row
	}
	extra := map[string]any{"field": field, "line_no": lineNo}
	if before != nil {
		extra["before"] = *before
	}
	if after != nil {
		extra["after"] = *after
	}
	return s.auditTx.RecordTx(ctx, q, audit.SensitiveAccess{
		Action: audit.ActOrderChanged, EntityType: "order", EntityID: orderID,
		ActorID: actor, Purpose: "STAFF_ORDER_EDIT", Reason: field,
		Extra: extra,
	})
}

// - read-side matrix projection -

// editableFields computes which fields THIS caller may edit on an order in THIS status: the ratified
// matrix ∧ the caller's authority ∧ (for money-moving fields) the money predicate.
//
// Like canOfferCancel, the money check here is deliberately SOFTER than the write path: an unwired
// seam, a blocked order, or a failed predicate read all simply OMIT the field from the offer, none of
// them may fail the order read. The 503/409 belongs exclusively to an actual edit attempt.
func (s *Service) editableFields(ctx context.Context, caller identity.StaffContext, orderID, status string, taxCents int64) []string {
	out := make([]string, 0, len(editFieldOrder))
	for _, f := range editFieldOrder {
		rule := editRules[f]
		if !rule.from[status] {
			continue
		}
		if rule.managerOnly && !caller.IsManager() {
			continue
		}
		if rule.movesMoney && s.assertEditableMoney(ctx, orderID, taxCents) != nil {
			continue
		}
		out = append(out, string(f))
	}
	return out
}
