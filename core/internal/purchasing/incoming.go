package purchasing

// incoming.go, DIB-460: what is ON ORDER for ONE product, with the date it is expected.
//
// THE CLIENT'S SENTENCE, 2026-08-11 (Ryan B): "you have no idea how much trouble that would save …
// having to go ask procurement when it's showing up is a massive pain." The answer to "when is it
// showing up" already exists in this module, purchasing_pos.expected_at, migration 0120, and has
// never been readable from anywhere a person looks a product up. This file is that read, and NOTHING
// else: no new table, no new column, no change to any shipped path.
//
// WHY THE READ LIVES HERE AND NOT IN INVENTORY
// --------------------------------------------
// purchasing owns purchasing_pos / purchasing_po_lines. The house rule is zero cross-module table
// access, so the module that owns the rows is the module that answers for them. The other half of
// "what is coming", stock already on a truck between our own yards, is inventory's
// stock_transfers, and it is answered by inventory's own read (inventory/incoming.go). The two
// halves are composed in the SURFACE, which is the one place allowed to know about both modules.
// Composing them in a Go module would need a cross-module seam and therefore a composition-root
// binding, which is outside this ticket's fence (see NOTES-FIX-INV460.md, PARK P1).
//
// WHAT COUNTS AS "ON ORDER"
// -------------------------
// A line on a purchase order that is not finished and not abandoned, with units still outstanding:
//
//	status  DRAFT / ORDERED / PARTIAL     , RECEIVED, CLOSED and CANCELLED are done with
//	qty     qty_ordered_base > qty_received_base
//
// DRAFT is INCLUDED, deliberately, and it is the one arguable call. A draft is not yet a promise to
// the vendor, so counting it as supply would be optimistic, but the person asking "is more coming?"
// is asking whether anyone has ALREADY started dealing with it, and "there is a draft PO for 120 of
// these" is the answer that stops them raising a second one. The status rides on every line so the
// surface can say `draft` rather than pretending it is ordered.
//
// DIB-470 UPDATE. The last clause of that paragraph used to read "and the DIB-72 replenishment
// arithmetic is untouched by this read". It is no longer true, and the change was deliberate: the
// worklist now grades against the SAME rows through the aggregate in on_order.go, because it was
// grading against migration-0011 receiving documents and therefore against a constant zero. The
// DRAFT argument above carries over and gets stronger there, see on_order.go's header.
//
// EXPECTED_AT IS NULLABLE AND STAYS NULLABLE. A PO raised without a promised date reports an empty
// string, and the surface says "expected date TBC" rather than inventing one. Nothing in this file
// or its callers defaults a missing date to today, to the created date, or to a lead time, a made-up
// date is worse than no date, because a person plans around it.

import (
	"context"
	"sort"
	"strings"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// The PO statuses that still have something coming. Written once, here, so the definition of "on
// order" cannot drift between the read and its tests.
var openPOStatuses = []string{StatusDraft, StatusOrdered, StatusPartial}

// BranchReach is a caller's branch authority projected onto a read.
//
// purchasing's own copy, by the codebase's zero-trust convention (inventory.BranchReach and
// payments.BranchReach are the same shape and are deliberately different types, a module must not
// be able to accept another module's authority object by accident).
//
// Global true means every branch and BranchIDs is ignored. Global false with an EMPTY BranchIDs
// means NO branches, a real state (a scoped account with no grants) that must resolve to no rows
// rather than to every row. That inversion is the whole reason this is a type and not a string.
type BranchReach struct {
	Global    bool
	BranchIDs []string
}

// branchFilter renders the reach for the `($n::uuid[] IS NULL OR branch_id = ANY($n::uuid[]))`
// predicate. nil → SQL NULL → the predicate short-circuits true (a global caller).
//
// The empty-array arm is the load-bearing one: ANY('{}') is false for every row, so a caller with no
// grants measures nothing. Returning nil there would hand them every yard in the company.
func (r BranchReach) branchFilter() any {
	if r.Global {
		return nil
	}
	if r.BranchIDs == nil {
		return []string{}
	}
	return r.BranchIDs
}

// reachesNothing reports the short-circuit case: a scoped caller with no grants, answered without a
// round trip.
func (r BranchReach) reachesNothing() bool { return !r.Global && len(r.BranchIDs) == 0 }

// IncomingPOLine is one outstanding purchase-order line for one product, the wire shape behind
// "120 PC · expected Aug 19 · #PO-TRT-121".
//
// It carries the ORDERED and RECEIVED quantities as well as the outstanding one because a partially
// received line is a different fact from a whole one ("40 of 120 landed") and a surface that only
// had the difference could not say so.
type IncomingPOLine struct {
	POID       string `json:"po_id"`
	PONo       string `json:"po_no"`
	POLineID   string `json:"po_line_id"`
	Status     string `json:"status"`
	VendorID   string `json:"vendor_id"`
	VendorName string `json:"vendor_name"`
	BranchID   string `json:"branch_id"`
	// ExpectedAt is YYYY-MM-DD, or EMPTY when the buyer raised the order without a promised date.
	// Empty is a real answer and is never filled in with a guess.
	ExpectedAt      string  `json:"expected_at"`
	QtyOrderedBase  float64 `json:"qty_ordered_base"`
	QtyReceivedBase float64 `json:"qty_received_base"`
	QtyOutstanding  float64 `json:"qty_outstanding_base"`
	UOM             string  `json:"uom"`
	// `json:"-"` + the embedded gate: this read is RequireStaff so the yard can answer "when is it
	// showing up", and the unit cost on it is the one thing a yard hand has no business reading.
	// See cost_gate.go.
	UnitCostCents int64 `json:"-"`
	POCosts
}

// incomingStore is the optional persistence seam for this read, discovered by type assertion off
// *Repository, the same shape inventory.stockLookupStore uses, and for the same reason: purchasing
// has no `store` interface to widen, and a test that had to reach a database to prove a BRANCH FENCE
// would be proving it in the one place the fence is hardest to see.
type incomingStore interface {
	OpenPOLinesForProduct(ctx context.Context, productID string, reach BranchReach) ([]IncomingPOLine, error)
	UpdateExpectedAt(ctx context.Context, poID, expectedAt string) error
	// UpdateDeliveryMethod is DIB-572's correction path (ported from the ERP). It sits on this seam
	// rather than on a new one for the reason the seam exists at all: what it must be possible to
	// state without a database is the FENCE and the STATUS GUARD, not the UPDATE.
	UpdateDeliveryMethod(ctx context.Context, poID, method string) error
}

// incoming resolves the seam: the injected fake in unit tests, else the real repository.
func (s *Service) incoming() (incomingStore, error) {
	if s.incomingSeam != nil {
		return s.incomingSeam, nil
	}
	if s.repo == nil {
		return nil, errNoDB
	}
	return s.repo, nil
}

// reachFor projects the caller's authority onto this read.
//
// It is deliberately NOT branchFence. branchFence answers "which single branch is this WRITE aimed
// at" and 403s a scoped caller with no grants, which is right for a write that must name a target.
// A READ has a truthful answer for that caller, nothing, and returning it as an empty list rather
// than as a refusal is what keeps the fence honest: the surface renders "Nothing on order" instead
// of an error nobody can act on, and the caller still sees no yard they were not granted.
//
// An explicit branch_id NARROWS within the reach and is refused when it is outside it: asking about
// a branch you cannot see is a forbidden question, not an empty answer.
func (s *Service) reachFor(ctx context.Context, branchID string) (BranchReach, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return BranchReach{}, err
	}
	branchID = strings.TrimSpace(branchID)
	if branchID != "" {
		if !caller.CanAccessBranch(branchID) {
			return BranchReach{}, errForbidden
		}
		return BranchReach{BranchIDs: []string{branchID}}, nil
	}
	if caller.Scope == identity.ScopeGlobal {
		return BranchReach{Global: true}, nil
	}
	ids := make([]string, 0, len(caller.Branches))
	for b, granted := range caller.Branches {
		if granted {
			ids = append(ids, b)
		}
	}
	// Sorted so the SQL parameter, and therefore the query plan and every test assertion on it -
	// does not depend on Go's randomised map iteration order.
	sort.Strings(ids)
	return BranchReach{BranchIDs: ids}, nil
}

// IncomingForProduct returns the outstanding purchase-order lines for one product within the
// caller's branch reach, soonest expected first.
//
// branchID "" means "every branch I may see", which is what a product card asks: the person looking
// at a product wants to know whether ANY yard they work in has more coming.
func (s *Service) IncomingForProduct(ctx context.Context, productID, branchID string) ([]IncomingPOLine, error) {
	if strings.TrimSpace(productID) == "" {
		return nil, badReq("purchasing: product_id is required")
	}
	reach, err := s.reachFor(ctx, branchID)
	if err != nil {
		return nil, err
	}
	st, err := s.incoming()
	if err != nil {
		return nil, err
	}
	// The seam is still handed the reach on the short-circuit path, see the seam's own guard. It is
	// NOT short-circuited in Go, deliberately: a fence that is enforced by an early return in the
	// service is a fence that disappears the day another caller reaches the store directly.
	rows, err := st.OpenPOLinesForProduct(ctx, productID, reach)
	if err != nil {
		return nil, err
	}
	if names := s.vendorNames(ctx); names != nil {
		for i := range rows {
			rows[i].VendorName = names[rows[i].VendorID]
		}
	}
	// THE COST GATE (cost_gate.go). This read is RequireStaff so the counter can answer "when is it
	// showing up"; the unit cost that rides along with the answer is the one thing on it that a
	// frontline caller has no business reading.
	for i := range rows {
		s.costLensFor(ctx, rows[i].BranchID).stampIncoming(rows[i : i+1])
	}
	return rows, nil
}

// SetExpectedDate records (or clears) the promised receiving date on a purchase order.
//
// WHY THIS IS PART OF DIB-460 AND NOT A SEPARATE TICKET. The column has existed since 0120 and can
// only ever be written at CREATE time, there is no edit path anywhere in the program. So every PO
// raised from the replenishment worklist, and every PO whose date the vendor later moved, carries no
// date at all, and a read that surfaces expected dates would have surfaced an empty column forever.
// The ticket allocated "a nullable column + an admin edit path"; the column was already there, so
// what shipped is the half that was actually missing.
//
// The date may be CLEARED (an empty string): a vendor who withdraws a promise leaves us with no
// date, and forcing a buyer to leave a stale one on the record is how a screen starts lying.
//
// A finished order is refused. Editing the promised date of a RECEIVED, CLOSED or CANCELLED PO
// changes a historical fact nobody is waiting on, and the receiving worklist sorts on this column.
func (s *Service) SetExpectedDate(ctx context.Context, poID, expectedAt string) (PODetail, error) {
	if strings.TrimSpace(poID) == "" {
		return PODetail{}, badReq("purchasing: po id is required")
	}
	expectedAt = strings.TrimSpace(expectedAt)
	if expectedAt != "" && !isISODate(expectedAt) {
		return PODetail{}, badReq("purchasing: expected_at must be a date (YYYY-MM-DD)")
	}
	po, err := s.GetPO(ctx, poID)
	if err != nil {
		return PODetail{}, err
	}
	if _, _, err := s.branchFence(ctx, po.BranchID); err != nil {
		return PODetail{}, err
	}
	if !CanSetExpectedDate(po.Status) {
		return PODetail{}, conflict("purchasing: cannot change the expected date of a %s purchase order", po.Status)
	}
	st, err := s.incoming()
	if err != nil {
		return PODetail{}, err
	}
	if err := st.UpdateExpectedAt(ctx, poID, expectedAt); err != nil {
		return PODetail{}, err
	}
	s.record(ctx, "purchasing.po.expected_date_set", poID,
		map[string]any{"po_no": po.PONo, "expected_at": expectedAt, "was": po.ExpectedAt})
	return s.GetPO(ctx, poID)
}

// CanSetExpectedDate reports whether a PO in this status still has a receiving date worth promising.
func CanSetExpectedDate(status string) bool {
	switch status {
	case StatusDraft, StatusOrdered, StatusPartial:
		return true
	default:
		return false
	}
}

// SetDeliveryMethod records how the goods on this purchase order reach the yard, or unrecords it.
// Ported from the ERP (DIB-572, the route that makes the 0217/0219 column honest).
//
// WHY THIS EXISTS. The ERP's 0214 gave purchasing_pos a delivery_method and nothing anywhere could
// write it after the draft was created: no route, no service method, no UPDATE. A column a person
// can read and cannot correct is worse than no column: a missing feature can be worked around, and
// a wrong value on a worklist cannot. This is deliberately the same shape as DIB-460's
// expected-date handler, a PUT that SETS one field to what the body says.
//
// UNKNOWN IS A VALUE YOU MAY SET, not just one you may leave. A buyer who marked an order PICKUP
// and then learns nobody actually knows must be able to say so; forcing them to choose between two
// answers they do not have is how the column starts lying again.
//
// A finished order is refused, on exactly CanSetExpectedDate's rule and for the same reason. In
// the ERP the receiving worklist is the screen that renders this column and it shows ORDERED and
// PARTIAL orders only, so every row on which a person can see a wrong arrival is a row this route
// can correct. Core's worklist does not render the column yet (the DIB-573 read half is a recorded
// residual); the refusal rule is ported unchanged so the two correction routes do not drift apart
// while the read half is pending. Rewriting a RECEIVED or CLOSED order's arrival changes a
// historical fact nobody is waiting on.
func (s *Service) SetDeliveryMethod(ctx context.Context, poID, method string) (PODetail, error) {
	if strings.TrimSpace(poID) == "" {
		return PODetail{}, badReq("purchasing: po id is required")
	}
	// The EDIT path does not treat an empty body as UNKNOWN by accident. On CREATE an omitted field
	// is an old caller who cannot express the question; on a correction it is a malformed request,
	// and a request that says nothing must not silently erase what a buyer recorded.
	if strings.TrimSpace(method) == "" {
		return PODetail{}, badReq("purchasing: delivery_method is required")
	}
	delivery, err := normalizeDeliveryMethod(method)
	if err != nil {
		return PODetail{}, err
	}
	po, err := s.GetPO(ctx, poID)
	if err != nil {
		return PODetail{}, err
	}
	if _, _, err := s.branchFence(ctx, po.BranchID); err != nil {
		return PODetail{}, err
	}
	if !CanSetDeliveryMethod(po.Status) {
		return PODetail{}, conflict("purchasing: cannot change the arrival of a %s purchase order", po.Status)
	}
	st, err := s.incoming()
	if err != nil {
		return PODetail{}, err
	}
	if err := st.UpdateDeliveryMethod(ctx, poID, delivery); err != nil {
		return PODetail{}, err
	}
	s.record(ctx, "purchasing.po.delivery_method_set", poID,
		map[string]any{"po_no": po.PONo, "delivery_method": delivery, "was": po.DeliveryMethod})
	return s.GetPO(ctx, poID)
}

// CanSetDeliveryMethod reports whether a PO in this status still has an arrival worth correcting.
// The same three statuses as CanSetExpectedDate, and deliberately not a call to it: the two answer
// different questions and a later change to one must not silently move the other.
func CanSetDeliveryMethod(status string) bool {
	switch status {
	case StatusDraft, StatusOrdered, StatusPartial:
		return true
	default:
		return false
	}
}

// isISODate validates YYYY-MM-DD without parsing a calendar: Postgres is the authority on whether
// 2026-02-30 exists, and this guard exists so a nonsense string becomes a 400 here rather than a
// 500 out of the driver.
func isISODate(s string) bool {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return false
	}
	for i, c := range s {
		if i == 4 || i == 7 {
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
