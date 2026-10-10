package purchasing

// service.go, the purchasing orchestration: the PO lifecycle, line-level receiving through the stock seam,
// the landed-cost tie-in, and the AP bill three-way match + posting.
//
// EVERY BRANCH-SCOPED SURFACE GATES ON THE TRUSTED CALLER. A staff member scoped to Trenton may not read or
// receive a Kingston purchase order, and an unscoped list is refused rather than silently widened, the same
// horizontal-access boundary landedcost and order already enforce.

import (
	"context"
	"fmt"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5"
)

// - DIB-421: EVERYTHING PAST THE BOUNDARY IS CAD -
//
// Wave 4 shipped this module CAD-only and wrote down why (REPORT-W4-C, PARK-P2): a foreign PO needed the
// columns, the DX-19 allowlist and the conversion, and doing any one alone breaks something. Migration 0134
// adds currency + fx_rate to purchasing_pos and purchasing_bills, the allowlist names both, and toCAD below
// is the conversion. THE THREE ONLY WORK TOGETHER; do not remove one and leave the others.
//
// WHERE THE MONEY IS FOREIGN, AND WHERE IT STOPS BEING FOREIGN:
//
//	purchasing_po_lines.unit_cost_cents  the VENDOR'S price, in the PO's currency. Stored as agreed so a
//	                                     buyer can hold the vendor's order confirmation against the screen.
//	- toCAD, at exactly three boundaries -
//	the receiving document                converted, so inventory books CAD moves and a CAD weighted average
//	the receipt line + receipt value      converted, so purchasing's own record agrees with the stock ledger
//	the three-way match inputs            converted, so a CAD bill is never compared against a USD PO price
//	everything downstream                 CAD: GRNI relief, price variance, AP, every journal leg
//
// THE CONVERSION IS ARITHMETICALLY IDENTICAL TO INVENTORY'S, deliberately and by test
// (TestToCAD_IsArithmeticallyIdenticalToInventoryCadUnit). inventory.ReceivePOTx computes
// `cadUnit = round(unitCost × fxRate)` PER UNIT COST and then values the receipt at qty × cadUnit. If
// purchasing rounded the LINE TOTAL instead, the two modules would disagree by a cent on most lines and the
// subledger would drift from GL 1200 in a way no reconciliation could name. Round the unit, then multiply -
// in both places, forever.
//
// The receiving document is still written CAD / fx 1.0. That is now a STATEMENT rather than a limitation:
// purchasing has already converted, so inventory must not convert again. A document handed over as USD would
// be double-converted, and the second conversion would be invisible.
//
// The landed-cost document is unchanged: landed_costs has always been allowlisted and internal/landedcost
// does its own conversion, so purchasing passes currency + rate through and converts nothing there.

// - vendors -

// ListVendors proxies the vendor directory seam.
func (s *Service) ListVendors(ctx context.Context) ([]Vendor, error) {
	if _, err := s.caller(ctx); err != nil {
		return nil, err
	}
	if s.vendor == nil {
		return nil, ErrVendorSeamUnwired
	}
	return s.vendor.ListVendors(ctx)
}

// vendorNames resolves ids → names for a list view through the seam (one call, then a map). Purchasing never
// JOINs the vendor table; a directory that cannot answer degrades to blank names rather than failing the read.
func (s *Service) vendorNames(ctx context.Context) map[string]string {
	if s.vendor == nil {
		return nil
	}
	vs, err := s.vendor.ListVendors(ctx)
	if err != nil {
		s.log.Warn("purchasing: vendor directory unavailable for name enrichment", "err", err)
		return nil
	}
	out := make(map[string]string, len(vs))
	for _, v := range vs {
		out[v.ID] = v.Name
	}
	return out
}

// - purchase orders -

// branchFence resolves the caller and validates the requested branch.
//
// An empty branch means "every branch I may see". A GLOBAL caller gets exactly that (no filter). A SCOPED
// caller with exactly ONE granted branch gets that branch implicitly, the common case, and refusing it would
// force every frontline screen to send a branch id it has no other reason to know. A scoped caller with
// SEVERAL grants must say which one: silently picking a branch for them would make the answer depend on map
// iteration order. This is the same not-found-vs-forbidden discipline landedcost.List uses.
func (s *Service) branchFence(ctx context.Context, branchID string) (identity.StaffContext, string, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return identity.StaffContext{}, "", err
	}
	if branchID == "" {
		if caller.Scope == identity.ScopeGlobal {
			return caller, "", nil
		}
		var only string
		for b, granted := range caller.Branches {
			if !granted {
				continue
			}
			if only != "" {
				return identity.StaffContext{}, "", clientErr(400,
					"purchasing: branch_id is required, your access spans more than one branch")
			}
			only = b
		}
		if only == "" {
			return identity.StaffContext{}, "", errForbidden
		}
		return caller, only, nil
	}
	if !caller.CanAccessBranch(branchID) {
		return identity.StaffContext{}, "", errForbidden
	}
	return caller, branchID, nil
}

// callerBranch is the branch an audit entry is attributed to: the caller's sole grant when they have one,
// else empty (a GLOBAL caller has no home branch).
func callerBranch(c identity.StaffContext) string {
	if c.Scope == identity.ScopeGlobal {
		return ""
	}
	var only string
	for b, granted := range c.Branches {
		if granted && only == "" {
			only = b
		} else if granted {
			return ""
		}
	}
	return only
}

// CreatePO drafts a purchase order and mints its branch-scoped number. Lines may be supplied inline (the
// usual case, a buyer builds the whole order and saves it once).
func (s *Service) CreatePO(ctx context.Context, in CreatePORequest) (PODetail, error) {
	caller, branchID, err := s.branchFence(ctx, in.BranchID)
	if err != nil {
		return PODetail{}, err
	}
	if branchID == "" {
		return PODetail{}, badReq("purchasing: branch_id is required")
	}
	if in.VendorID == "" {
		return PODetail{}, badReq("purchasing: vendor_id is required")
	}
	if s.vendor == nil {
		return PODetail{}, ErrVendorSeamUnwired
	}
	v, err := s.vendor.Vendor(ctx, in.VendorID)
	if err != nil {
		return PODetail{}, err
	}
	if !v.IsActive {
		// An inactive vendor is one the business has stopped buying from. Letting a PO be raised against one
		// is how a terminated supply relationship quietly resumes.
		return PODetail{}, conflict("purchasing: vendor %s is not active", v.VendorNo)
	}
	for i := range in.Lines {
		if err := validateLine(in.Lines[i], i+1); err != nil {
			return PODetail{}, err
		}
	}
	// DIB-421. Refused BEFORE the transaction opens, so a USD order with no rate burns no PO number.
	currency, fx, err := normalizeCurrency(in.Currency, in.FXRate)
	if err != nil {
		return PODetail{}, err
	}

	tx, err := s.repo.Begin(ctx)
	if err != nil {
		return PODetail{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	poNo, err := s.repo.MintPONo(ctx, tx, branchID)
	if err != nil {
		return PODetail{}, err
	}
	poID, err := s.repo.InsertPO(ctx, tx, insertPOParams{
		PONo: poNo, VendorID: in.VendorID, BranchID: branchID,
		ExpectedAt: strings.TrimSpace(in.ExpectedAt), Notes: in.Notes, CreatedBy: caller.UserID,
		Currency: currency, FXRate: fx,
	})
	if err != nil {
		return PODetail{}, err
	}
	for i, ln := range in.Lines {
		if _, err := s.repo.InsertPOLine(ctx, tx, poID, i+1, ln); err != nil {
			return PODetail{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return PODetail{}, fmt.Errorf("purchasing: commit create po: %w", err)
	}
	s.log.Info("purchasing: PO drafted", "po_no", poNo, "vendor", v.VendorNo, "branch_id", branchID,
		"currency", currency, "fx_rate", fx)
	s.record(ctx, "purchasing.po.created", poID, map[string]any{
		"po_no": poNo, "vendor_id": in.VendorID, "currency": currency, "fx_rate": fx})
	return s.GetPO(ctx, poID)
}

// toCAD converts one amount from a document's currency to CAD at that document's frozen rate.
//
// IT MUST STAY BYTE-FOR-BYTE EQUIVALENT TO inventory/service.go's `cadUnit = round(unitCost × fxRate)`.
// purchasing's round is math.Round; inventory's is `int64(f+0.5)` for f ≥ 0 and `int64(f-0.5)` below -
// both are half-away-from-zero, which is why the two agree, and the named test checks it over a table
// rather than trusting this comment.
//
// A CAD document always carries fx 1.0 (normalizeCurrency guarantees it, the 0134 CHECK enforces it), so
// this is the identity for every pre-DIB-421 row and every CAD document written after it.
func toCAD(cents int64, fx float64) int64 {
	if fx == 1.0 {
		// Not an optimization: float64 multiplication by 1.0 is exact, but going through round() would
		// still be a place a future edit could introduce drift on the overwhelmingly common path.
		return cents
	}
	return round(float64(cents) * fx)
}

// DeliveryPickup / DeliveryDelivered are the two ANSWERS to "how do these goods reach the yard".
// DeliveryUnknown is the absence of an answer, and it is a real third state rather than a hole.
// (Ported from the ERP with DIB-572; the values are the ones the 0217/0219 CHECK constraint
// accepts.)
const (
	DeliveryPickup    = "PICKUP"
	DeliveryDelivered = "DELIVERY"
	// DeliveryUnknown means NOBODY RECORDED IT, not "we are still deciding" and not "neither".
	// It is what every order raised before the correction path existed carries, and what a buyer
	// who leaves the question alone carries.
	DeliveryUnknown = "UNKNOWN"
)

// normalizeDeliveryMethod folds the request's value to one of the three the column accepts.
//
// EMPTY MEANS UNKNOWN, at this layer: a caller that predates DIB-572 omits the field, and folding
// those to DELIVERY would print an arrival on the receiving worklist that no person ever stated.
// In the ERP that caller is the CREATE path; in core the create half is not yet ported (a recorded
// residual), so the fold's only caller today is the correction route, which refuses an explicit
// empty body BEFORE calling here (SetDeliveryMethod): on a correction a request that says nothing
// is malformed, never an unrecord. The branch stays because it is the ported contract of this
// function and the create half lands against it. An unrecognised value is a different thing and
// IS refused: "PICK UP" silently becoming DELIVERY would put a wrong word on the worklist, which
// is worse than a 400 that names the values.
func normalizeDeliveryMethod(m string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(m)) {
	case "":
		return DeliveryUnknown, nil
	case DeliveryPickup:
		return DeliveryPickup, nil
	case DeliveryDelivered:
		return DeliveryDelivered, nil
	case DeliveryUnknown:
		return DeliveryUnknown, nil
	}
	return "", badReq("purchasing: delivery_method must be %s, %s or %s",
		DeliveryPickup, DeliveryDelivered, DeliveryUnknown)
}

func normalizeCurrency(currency string, fx float64) (string, float64, error) {
	c := strings.ToUpper(strings.TrimSpace(currency))
	if c == "" {
		c = "CAD"
	}
	if c != "CAD" && c != "USD" {
		return "", 0, badReq("purchasing: currency must be CAD or USD")
	}
	if c == "CAD" {
		return c, 1.0, nil // a CAD document never carries a manual fx
	}
	if fx <= 0 {
		return "", 0, badReq("purchasing: a USD purchase order needs a positive fx_rate")
	}
	return c, fx, nil
}

func validateLine(l AddLineParams, lineNo int) error {
	if l.ProductID == "" {
		return badReq("purchasing: line %d needs a product_id", lineNo)
	}
	if l.QtyOrderedBase <= 0 {
		return badReq("purchasing: line %d quantity must be greater than zero", lineNo)
	}
	if l.QtyOrderedBase > maxLineQty {
		return unprocessable("purchasing: line %d quantity is beyond what an order line can hold (%.0f at most)", lineNo, maxLineQty)
	}
	// AN EMPTY UOM MEANS "the product's BASE unit", resolved in SQL by InsertPOLine, it is not a
	// missing field. DIB-461 found the shipped consequence of requiring one here: inventory's
	// POCreator seam (the DIB-72 create-PO handoff) speaks BASE QUANTITIES and carries no unit,
	// because the unit a base quantity is in is a fact about the PRODUCT and not about the request.
	// The composition-root adapter therefore sends none, and every "create a PO from the shortfall
	// worklist" has 500'd on this line since the union, a dead button whose only symptom was an
	// "internal error" toast. Defaulting is the correct reading of the seam rather than a patch
	// around it: a base quantity denominated in the base unit is the SAME quantity.
	//
	// It is still a REFUSAL to write a line with no unit at all, the default is resolved from the
	// product row, and a product with no base_uom cannot exist (the column is NOT NULL).
	if l.UnitCostCents < 0 {
		return badReq("purchasing: line %d unit cost cannot be negative", lineNo)
	}
	return nil
}

// GetPO returns a PO with its lines and receipts, branch-fenced.
func (s *Service) GetPO(ctx context.Context, poID string) (PODetail, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return PODetail{}, err
	}
	po, err := s.repo.GetPO(ctx, poID)
	if err != nil {
		return PODetail{}, err
	}
	if !caller.CanAccessBranch(po.BranchID) {
		return PODetail{}, errNotFound
	}
	lines, err := s.repo.POLines(ctx, nil, poID, false)
	if err != nil {
		return PODetail{}, err
	}
	receipts, err := s.repo.Receipts(ctx, poID)
	if err != nil {
		return PODetail{}, err
	}
	po.LineCount = len(lines)
	for _, l := range lines {
		po.SubtotalCents += l.LineTotalCents
	}
	po.SubtotalCadCents = toCAD(po.SubtotalCents, po.FXRate)
	if names := s.vendorNames(ctx); names != nil {
		po.VendorName = names[po.VendorID]
	}
	// THE COST GATE, and the last thing that happens to this read. Everything above is the order;
	// this decides whether the money on it reaches the caller at all (cost_gate.go).
	lens := s.costLensFor(ctx, po.BranchID)
	lens.stampPO(&po)
	lens.stampPOLines(lines)
	lens.stampReceipts(receipts)
	// `[]`, NEVER null. "Nothing has landed yet" is the ordinary state of a placed order, and a null
	// arrives at a client looking like a field that failed to populate, the DIB-452 rule the
	// incoming read already states, applied to the shape it was missing from.
	if lines == nil {
		lines = []POLine{}
	}
	if receipts == nil {
		receipts = []Receipt{}
	}
	return PODetail{PO: po, Lines: lines, Receipts: receipts}, nil
}

// ListPOs lists branch-fenced purchase orders.
func (s *Service) ListPOs(ctx context.Context, branchID, status string) ([]PO, error) {
	_, branch, err := s.branchFence(ctx, branchID)
	if err != nil {
		return nil, err
	}
	if status != "" && !KnownStatus(status) {
		return nil, badReq("purchasing: %q is not a purchase-order status", status)
	}
	pos, err := s.repo.ListPOs(ctx, branch, status)
	if err != nil {
		return nil, err
	}
	if names := s.vendorNames(ctx); names != nil {
		for i := range pos {
			pos[i].VendorName = names[pos[i].VendorID]
		}
	}
	// THE COST GATE. The list carries a subtotal per row, which is the same secret as a line cost
	// with the arithmetic already done, so it is gated with it and not beside it.
	for i := range pos {
		s.costLensFor(ctx, pos[i].BranchID).stampPO(&pos[i])
	}
	return pos, nil
}

// AddLine appends a line to a DRAFT purchase order.
func (s *Service) AddLine(ctx context.Context, poID string, in AddLineParams) (PODetail, error) {
	if err := validateLine(in, 0); err != nil {
		return PODetail{}, err
	}
	if err := s.mutatePO(ctx, poID, func(tx pgx.Tx, po PO) error {
		if !CanEditLines(po.Status) {
			return conflict("purchasing: cannot edit the lines of a %s purchase order", po.Status)
		}
		n, err := s.repo.NextLineNo(ctx, tx, poID)
		if err != nil {
			return err
		}
		_, err = s.repo.InsertPOLine(ctx, tx, poID, n, in)
		return err
	}); err != nil {
		return PODetail{}, err
	}
	return s.GetPO(ctx, poID)
}

// maxLineQty is the ceiling on one line's quantity: the column is numeric(19,4), which overflows
// at 1e15, and a quantity near that is a typo in any yard. Past it the door answers in words
// rather than letting the driver's overflow become a 500.
const maxLineQty = 1e12

// checkLinePath refuses a path whose ids are not uuids before any statement runs: an order id that
// is not one names no order (404), a line id that is not one is a request that cannot be right
// (422, in words), and neither may reach the database's uuid cast as a 500.
func checkLinePath(poID, lineID string) error {
	if !isUUIDString(poID) {
		return errNotFound
	}
	if !isUUIDString(lineID) {
		return unprocessable("purchasing: line id must be a uuid")
	}
	return nil
}

// RemoveLine deletes a line from a DRAFT purchase order.
func (s *Service) RemoveLine(ctx context.Context, poID, lineID string) (PODetail, error) {
	if err := checkLinePath(poID, lineID); err != nil {
		return PODetail{}, err
	}
	if err := s.mutatePO(ctx, poID, func(tx pgx.Tx, po PO) error {
		if !CanEditLines(po.Status) {
			return conflict("purchasing: cannot edit the lines of a %s purchase order", po.Status)
		}
		return s.repo.DeletePOLine(ctx, tx, poID, lineID)
	}); err != nil {
		return PODetail{}, err
	}
	return s.GetPO(ctx, poID)
}

// UpdateLine corrects the quantity or the cost of one line on a DRAFT purchase order, the leg of
// the line editor add and remove never had: a buyer who mistypes a quantity could remove the line
// and retype it, losing the description and the line number, or place a wrong order.
//
// THE EDIT IS DELIBERATELY NARROW: the quantity and the cost, the two figures a vendor's
// confirmation corrects, and nothing else. The PRODUCT is not editable (that is a different line;
// remove this one and add it), and once the order is placed the freeze is the one CanEditLines
// already states, because a purchase order is a contract and editing one after placement is how a
// three-way match is quietly defeated (the add route's own words).
func (s *Service) UpdateLine(ctx context.Context, poID, lineID string, in UpdateLineParams) (PODetail, error) {
	if in.QtyOrderedBase == nil && in.UnitCostCents == nil {
		return PODetail{}, badReq("purchasing: the body names nothing to change")
	}
	if err := checkLinePath(poID, lineID); err != nil {
		return PODetail{}, err
	}
	if in.QtyOrderedBase != nil && *in.QtyOrderedBase <= 0 {
		return PODetail{}, badReq("purchasing: line quantity must be positive")
	}
	if in.QtyOrderedBase != nil && *in.QtyOrderedBase > maxLineQty {
		return PODetail{}, unprocessable("purchasing: line quantity is beyond what an order line can hold (%.0f at most)", maxLineQty)
	}
	if in.UnitCostCents != nil && *in.UnitCostCents < 0 {
		return PODetail{}, badReq("purchasing: line unit cost cannot be negative")
	}
	if err := s.mutatePO(ctx, poID, func(tx pgx.Tx, po PO) error {
		if !CanEditLines(po.Status) {
			return conflict("purchasing: cannot edit the lines of a %s purchase order", po.Status)
		}
		return s.repo.UpdatePOLine(ctx, tx, poID, lineID, in)
	}); err != nil {
		return PODetail{}, err
	}
	return s.GetPO(ctx, poID)
}

// Transition performs an operator-driven lifecycle move, checked against the matrix in lifecycle.go.
func (s *Service) Transition(ctx context.Context, poID, to string) (PODetail, error) {
	to = strings.ToUpper(strings.TrimSpace(to))
	if err := s.mutatePO(ctx, poID, func(tx pgx.Tx, po PO) error {
		lines, err := s.repo.POLines(ctx, tx, poID, false)
		if err != nil {
			return err
		}
		var anyReceived, allReceived = false, len(lines) > 0
		for _, l := range lines {
			if l.QtyReceivedBase > qtyEpsilon {
				anyReceived = true
			}
			if l.QtyReceivedBase < l.QtyOrderedBase-qtyEpsilon {
				allReceived = false
			}
		}
		if err := CheckTransition(Transition{
			From: po.Status, To: to, AnyReceived: anyReceived, HasLines: len(lines) > 0, AllReceived: allReceived,
		}); err != nil {
			return err
		}
		return s.repo.SetPOStatus(ctx, tx, poID, to, to == StatusOrdered, to == StatusClosed)
	}); err != nil {
		return PODetail{}, err
	}
	s.record(ctx, "purchasing.po."+strings.ToLower(to), poID, map[string]any{"status": to})
	return s.GetPO(ctx, poID)
}

// mutatePO is the shared lock-fence-mutate-commit shape: open a tx, take the PO's row lock, check the branch
// fence, run the mutation, commit.
func (s *Service) mutatePO(ctx context.Context, poID string, fn func(tx pgx.Tx, po PO) error) error {
	caller, err := s.caller(ctx)
	if err != nil {
		return err
	}
	tx, err := s.repo.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	po, err := s.repo.POForUpdate(ctx, tx, poID)
	if err != nil {
		return err
	}
	if !caller.CanAccessBranch(po.BranchID) {
		return errNotFound
	}
	if err := fn(tx, po); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("purchasing: commit: %w", err)
	}
	return nil
}

// - receiving -

// Receive records a line-level receipt: it writes the receiving document, hands it to the stock seam, records
// the receipt, advances the line rollups, and recomputes the PO status.
//
// THE ORDER OF THE FIRST TWO CHECKS IS LOAD-BEARING. The stock seam is checked BEFORE the transaction opens,
// so a deployment with the seam unwired writes nothing at all, no receiving document, no receipt row, no
// number burned. Everything after it runs inside ONE transaction, so a seam that ERRORS mid-flight rolls the
// document back with it: there is no state in which purchasing believes goods arrived and the stock ledger
// does not. Both halves are named tests.
func (s *Service) Receive(ctx context.Context, poID string, in ReceiveRequest) (Receipt, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return Receipt{}, err
	}
	if s.stock == nil {
		return Receipt{}, ErrStockSeamUnwired
	}
	if len(in.Lines) == 0 {
		return Receipt{}, badReq("purchasing: nothing to receive, supply at least one line")
	}

	tx, err := s.repo.Begin(ctx)
	if err != nil {
		return Receipt{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	po, err := s.repo.POForUpdate(ctx, tx, poID)
	if err != nil {
		return Receipt{}, err
	}
	if !caller.CanAccessBranch(po.BranchID) {
		return Receipt{}, errNotFound
	}
	if err := CheckReceivable(po.Status); err != nil {
		return Receipt{}, err
	}
	lines, err := s.repo.POLines(ctx, tx, poID, true)
	if err != nil {
		return Receipt{}, err
	}
	byID := make(map[string]POLine, len(lines))
	for _, l := range lines {
		byID[l.ID] = l
	}

	// Fold duplicate requests for the same line before validating, so two entries of 5 against a line with 8
	// outstanding are refused as 10-against-8 rather than passing twice.
	requested := map[string]float64{}
	var order []string
	for _, rl := range in.Lines {
		l, ok := byID[rl.POLineID]
		if !ok {
			return Receipt{}, badReq("purchasing: line %s does not belong to this purchase order", rl.POLineID)
		}
		if _, seen := requested[rl.POLineID]; !seen {
			order = append(order, rl.POLineID)
		}
		requested[rl.POLineID] += rl.QtyBase
		_ = l
	}
	for _, id := range order {
		l := byID[id]
		if err := CheckReceiptQty(l.LineNo, requested[id], l.QtyOrderedBase, l.QtyReceivedBase); err != nil {
			return Receipt{}, err
		}
	}

	seq, err := s.repo.NextReceiptSeq(ctx, tx, poID)
	if err != nil {
		return Receipt{}, err
	}
	grnNo := fmt.Sprintf("%s/R%d", po.PONo, seq)

	docID, err := s.repo.InsertReceivingDoc(ctx, tx, receivingDocParams{
		GRNNo: grnNo, VendorID: po.VendorID, BranchID: po.BranchID, CreatedBy: caller.UserID, SourcePO: poID,
	})
	if err != nil {
		return Receipt{}, err
	}
	// THE BOUNDARY (DIB-421). The receiving document is written in CAD, at the PO's frozen rate,
	// converted PER UNIT COST, the same operation, in the same order, that inventory would perform on
	// a USD document. Purchasing converts and hands over a CAD document precisely so that inventory
	// does NOT convert again: a document passed over as USD would be converted twice and the second
	// conversion would be invisible in every ledger downstream.
	docLineIDs := make(map[string]string, len(order))
	for i, id := range order {
		l := byID[id]
		docLineID, err := s.repo.InsertReceivingDocLine(ctx, tx, docID, i+1, l.ProductID, l.UOM, requested[id],
			toCAD(l.UnitCostCents, po.FXRate))
		if err != nil {
			return Receipt{}, err
		}
		docLineIDs[id] = docLineID
	}

	// THE SEAM. Everything stock, the RECEIPT moves, the weighted-average maintenance, the balance
	// projection, the po.received event that posts GRNI, happens inside this call, in inventory, in this
	// transaction. Purchasing does none of it and never will.
	if err := s.stock.ReceiveDocumentTx(ctx, tx, docID); err != nil {
		return Receipt{}, fmt.Errorf("purchasing: stock receive: %w", err)
	}

	var totalCad int64
	receiptID, err := s.repo.InsertReceipt(ctx, tx, insertReceiptParams{
		POID: poID, Seq: seq, GRNNo: grnNo, StockDocID: docID, ReceivedBy: caller.UserID, Notes: in.Notes,
	})
	if err != nil {
		return Receipt{}, err
	}
	for _, id := range order {
		l := byID[id]
		qty := requested[id]
		// Round the UNIT, then multiply, never round the line total. This is the one place purchasing's
		// arithmetic has to agree with inventory's to the cent, because these two numbers describe the
		// same event in two ledgers, and a cent of disagreement is a subledger that will not tie to
		// GL 1200. (totalCad has been called CAD since Wave 4; DIB-421 makes the name true of USD too.)
		unitCad := toCAD(l.UnitCostCents, po.FXRate)
		value := round(qty * float64(unitCad))
		totalCad += value
		if err := s.repo.InsertReceiptLine(ctx, tx, receiptID, id, docLineIDs[id], qty, unitCad, value); err != nil {
			return Receipt{}, err
		}
		if err := s.repo.BumpReceived(ctx, tx, id, qty); err != nil {
			return Receipt{}, err
		}
		l.QtyReceivedBase += qty
		byID[id] = l
	}

	progress := make([]LineProgress, 0, len(lines))
	for _, l := range lines {
		cur := byID[l.ID]
		progress = append(progress, LineProgress{QtyOrderedBase: cur.QtyOrderedBase, QtyReceivedBase: cur.QtyReceivedBase})
	}
	newStatus := StatusAfterReceipt(progress)
	if err := s.repo.SetPOStatus(ctx, tx, poID, newStatus, false, false); err != nil {
		return Receipt{}, err
	}
	if err := s.repo.SetReceiptValue(ctx, tx, receiptID, totalCad); err != nil {
		return Receipt{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Receipt{}, fmt.Errorf("purchasing: commit receipt: %w", err)
	}
	s.log.Info("purchasing: PO received", "po_no", po.PONo, "grn_no", grnNo,
		"lines", len(order), "value_cad_cents", totalCad, "status", newStatus)
	s.record(ctx, "purchasing.receipt.recorded", receiptID,
		map[string]any{"po_no": po.PONo, "grn_no": grnNo, "value_cad_cents": totalCad, "po_status": newStatus})
	return s.GetReceipt(ctx, receiptID)
}

// Worklist is the receiving queue for a branch.
func (s *Service) Worklist(ctx context.Context, branchID string) ([]WorklistItem, error) {
	_, branch, err := s.branchFence(ctx, branchID)
	if err != nil {
		return nil, err
	}
	items, err := s.repo.Worklist(ctx, branch)
	if err != nil {
		return nil, err
	}
	if names := s.vendorNames(ctx); names != nil {
		for i := range items {
			items[i].VendorName = names[items[i].VendorID]
		}
	}
	// THE COST GATE. "What is still open, in dollars" is a cost figure with a filter on it.
	for i := range items {
		s.costLensFor(ctx, items[i].BranchID).stampWorklist(items[i : i+1])
	}
	return items, nil
}

// GetReceipt returns one receipt with its lines, branch-fenced through its PO.
func (s *Service) GetReceipt(ctx context.Context, receiptID string) (Receipt, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return Receipt{}, err
	}
	rc, err := s.repo.GetReceipt(ctx, receiptID)
	if err != nil {
		return Receipt{}, err
	}
	po, err := s.repo.GetPO(ctx, rc.POID)
	if err != nil {
		return Receipt{}, err
	}
	if !caller.CanAccessBranch(po.BranchID) {
		return Receipt{}, errNotFound
	}
	// THE COST GATE. A receipt's value is what the stock that just landed cost us.
	s.costLensFor(ctx, po.BranchID).stampReceipt(&rc)
	return rc, nil
}

// - landed cost tie-in -

// AttachLandedCost captures a freight/duty/handling document against ONE receipt and applies it, through
// internal/landedcost's existing capitalization path. Purchasing computes no allocation and touches no
// weighted average: it decides WHICH received lines the cost belongs to (by handing over that receipt's
// receiving-document id) and landedcost does the rest.
//
// One landed cost per receipt. A second freight invoice for the same truckload is a retro-adjustment, which
// landedcost already models (RetroAdjust) and which needs the still-on-hand basis this module does not carry.
func (s *Service) AttachLandedCost(ctx context.Context, receiptID string, in LandedCostRequest) (Receipt, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return Receipt{}, err
	}
	if s.landed == nil {
		return Receipt{}, ErrLandedSeamUnwired
	}
	rc, err := s.repo.GetReceipt(ctx, receiptID)
	if err != nil {
		return Receipt{}, err
	}
	po, err := s.repo.GetPO(ctx, rc.POID)
	if err != nil {
		return Receipt{}, err
	}
	if !caller.CanAccessBranch(po.BranchID) {
		return Receipt{}, errNotFound
	}
	if rc.LandedCostID != "" {
		return Receipt{}, conflict("purchasing: receipt %s already carries a landed cost", rc.GRNNo)
	}
	if rc.StockDocID == "" {
		return Receipt{}, conflict("purchasing: receipt %s has no receiving document to allocate over", rc.GRNNo)
	}
	if in.FreightCents < 0 || in.DutyCents < 0 || in.HandlingCents < 0 {
		return Receipt{}, badReq("purchasing: freight/duty/handling must be non-negative")
	}
	if in.FreightCents+in.DutyCents+in.HandlingCents == 0 {
		return Receipt{}, badReq("purchasing: a landed cost with no freight, duty or handling costs nothing")
	}
	currency, fx, err := normalizeCurrency(in.Currency, in.FXRate)
	if err != nil {
		return Receipt{}, err
	}

	lcID, err := s.landed.CreateAndApply(ctx, LandedCostDraft{
		POID: rc.StockDocID, PORef: rc.GRNNo, BranchID: po.BranchID, Currency: currency, FXRate: fx,
		FreightCents: in.FreightCents, DutyCents: in.DutyCents, HandlingCents: in.HandlingCents,
		Basis: strings.ToUpper(strings.TrimSpace(in.Basis)),
	})
	if err != nil {
		return Receipt{}, err
	}
	if err := s.repo.SetReceiptLandedCost(ctx, receiptID, lcID); err != nil {
		return Receipt{}, err
	}
	s.log.Info("purchasing: landed cost applied to receipt", "grn_no", rc.GRNNo, "landed_cost_id", lcID)
	s.record(ctx, "purchasing.receipt.landed_cost", receiptID, map[string]any{"landed_cost_id": lcID})
	return s.GetReceipt(ctx, receiptID)
}

// - tolerance -

// GetTolerance resolves the effective tolerance for a branch.
func (s *Service) GetTolerance(ctx context.Context, branchID string) (Tolerance, error) {
	if _, err := s.caller(ctx); err != nil {
		return Tolerance{}, err
	}
	return s.repo.Tolerance(ctx, branchID)
}

// SetTolerance writes the org default (empty branch) or a branch override.
func (s *Service) SetTolerance(ctx context.Context, in SetToleranceRequest) (Tolerance, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return Tolerance{}, err
	}
	if in.QtyPct < 0 || in.PricePct < 0 || in.PriceAbsCents < 0 {
		return Tolerance{}, badReq("purchasing: tolerances cannot be negative")
	}
	// An upper bound, because a tolerance is a control and a control with no ceiling is not one. 100% would
	// accept a bill for twice the agreed price without anybody looking at it.
	if in.QtyPct > 25 || in.PricePct > 25 {
		return Tolerance{}, badReq("purchasing: a match tolerance above 25%% is not a tolerance, it is an off switch")
	}
	if in.BranchID != "" && !caller.CanAccessBranch(in.BranchID) {
		return Tolerance{}, errForbidden
	}
	if err := s.repo.UpsertTolerance(ctx, in, caller.UserID); err != nil {
		return Tolerance{}, err
	}
	s.record(ctx, "purchasing.tolerance.set", in.BranchID, map[string]any{
		"qty_pct": in.QtyPct, "price_pct": in.PricePct, "price_abs_cents": in.PriceAbsCents,
	})
	return s.repo.Tolerance(ctx, in.BranchID)
}

// - AP bills -

// CreateBill enters a vendor bill against a purchase order and runs the three-way match on every line.
// The bill is DRAFT: nothing has been posted and nothing is owed until PostBill.
func (s *Service) CreateBill(ctx context.Context, in CreateBillRequest) (BillDetail, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return BillDetail{}, err
	}
	if strings.TrimSpace(in.BillNo) == "" {
		return BillDetail{}, badReq("purchasing: bill_no (the vendor's invoice number) is required")
	}
	if in.POID == "" {
		return BillDetail{}, badReq("purchasing: po_id is required, a bill with no purchase order cannot be matched")
	}
	if len(in.Lines) == 0 {
		return BillDetail{}, badReq("purchasing: a bill needs at least one line")
	}
	if in.TaxCents < 0 {
		return BillDetail{}, badReq("purchasing: tax cannot be negative")
	}
	po, err := s.repo.GetPO(ctx, in.POID)
	if err != nil {
		return BillDetail{}, err
	}
	if !caller.CanAccessBranch(po.BranchID) {
		return BillDetail{}, errNotFound
	}
	if po.Status == StatusDraft || po.Status == StatusCancelled {
		return BillDetail{}, conflict("purchasing: cannot bill a %s purchase order", po.Status)
	}
	tol, err := s.repo.Tolerance(ctx, po.BranchID)
	if err != nil {
		return BillDetail{}, err
	}
	// DIB-421. The BILL'S OWN currency, not the PO's. A vendor may invoice a USD purchase order in CAD,
	// and the rate on the invoice is the rate they used; inheriting the PO's would silently restate the
	// vendor's own numbers. Both sides are converted below and only then compared.
	billCur, billFX, err := normalizeCurrency(in.Currency, in.FXRate)
	if err != nil {
		return BillDetail{}, err
	}

	tx, err := s.repo.Begin(ctx)
	if err != nil {
		return BillDetail{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	briefs, err := s.repo.POLineBriefs(ctx, tx, in.POID, false)
	if err != nil {
		return BillDetail{}, err
	}

	type evaluated struct {
		params insertBillLineParams
		state  string
	}
	var rows []evaluated
	var subtotal int64
	for i, bl := range in.Lines {
		if bl.QtyBilledBase <= 0 {
			return BillDetail{}, badReq("purchasing: bill line %d quantity must be greater than zero", i+1)
		}
		if bl.UnitCostCents < 0 {
			return BillDetail{}, badReq("purchasing: bill line %d unit cost cannot be negative", i+1)
		}
		// Unit first, then multiply, the same rule as the receipt, for the same reason.
		billUnitCad := toCAD(bl.UnitCostCents, billFX)
		lineTotal := round(bl.QtyBilledBase * float64(billUnitCad))
		subtotal += lineTotal

		mi := MatchInput{QtyBilledBase: bl.QtyBilledBase, BillUnitCostCents: billUnitCad}
		if bl.POLineID != "" {
			b, ok := briefs[bl.POLineID]
			if !ok {
				return BillDetail{}, badReq("purchasing: bill line %d references a line that is not on this purchase order", i+1)
			}
			prior, err := s.repo.PriorBilledQty(ctx, tx, bl.POLineID, "")
			if err != nil {
				return BillDetail{}, err
			}
			mi.HasPOLine = true
			mi.QtyOrderedBase = b.QtyOrderedBase
			mi.QtyReceivedBase = b.QtyReceivedBase
			mi.QtyBilledPriorBase = prior
			// The PO side of the comparison, converted at the PO's OWN rate. The match's contract
			// ("all money is CAD cents; the conversion happens before anything reaches here") is only
			// true if BOTH sides are converted, each at its own document's rate.
			mi.POUnitCostCents = toCAD(b.UnitCostCents, po.FXRate)
		}
		res := EvaluateLine(mi, tol)
		rows = append(rows, evaluated{
			params: insertBillLineParams{
				LineNo: i + 1, POLineID: bl.POLineID, Desc: bl.Description, Qty: bl.QtyBilledBase,
				UnitCost: billUnitCad, LineTotal: lineTotal, MatchState: res.State,
				QtyVar: res.QtyVarianceBase, PriceVar: res.PriceVarianceCents,
			},
			state: res.State,
		})
	}

	states := make([]string, len(rows))
	for i, r := range rows {
		states[i] = r.state
	}
	headerState := RollUp(states)
	// Tax is on the vendor's invoice in the vendor's currency, so it converts with everything else.
	taxCad := toCAD(in.TaxCents, billFX)
	total := subtotal + taxCad

	if err := checkInstalments(in.Instalments, total); err != nil {
		return BillDetail{}, err
	}

	billID, err := s.repo.InsertBill(ctx, tx, insertBillParams{
		BillNo: strings.TrimSpace(in.BillNo), VendorID: po.VendorID, BranchID: po.BranchID, POID: in.POID,
		BillDate: strings.TrimSpace(in.BillDate), DueDate: strings.TrimSpace(in.DueDate),
		Subtotal: subtotal, Tax: taxCad, Total: total,
		Match: headerState, CreatedBy: caller.UserID,
		Currency: billCur, FXRate: billFX,
	})
	if err != nil {
		return BillDetail{}, err
	}
	for _, r := range rows {
		p := r.params
		p.BillID = billID
		if err := s.repo.InsertBillLine(ctx, tx, p); err != nil {
			return BillDetail{}, err
		}
	}
	for i, inst := range in.Instalments {
		if err := s.repo.InsertInstalment(ctx, tx, billID, i+1, inst.DueDate, inst.AmountCents, inst.Note); err != nil {
			return BillDetail{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return BillDetail{}, fmt.Errorf("purchasing: commit bill: %w", err)
	}
	s.log.Info("purchasing: bill entered", "bill_no", in.BillNo, "po_no", po.PONo,
		"match_state", headerState, "total_cents", total)
	s.record(ctx, "purchasing.bill.created", billID,
		map[string]any{"bill_no": in.BillNo, "match_state": headerState, "total_cents": total})
	return s.GetBill(ctx, billID)
}

// checkInstalments enforces the one rule an instalment schedule has: it must add up to the bill. A schedule
// that does not is either a typo or a plan to underpay, and both are better caught here than by a clerk
// wondering why the last payment did not clear the balance. An EMPTY schedule is legal, the bill is simply
// due in full on its due date.
func checkInstalments(ins []InstalmentParams, total int64) error {
	if len(ins) == 0 {
		return nil
	}
	var sum int64
	for i, inst := range ins {
		if inst.AmountCents <= 0 {
			return badReq("purchasing: instalment %d must be a positive amount", i+1)
		}
		if strings.TrimSpace(inst.DueDate) == "" {
			return badReq("purchasing: instalment %d needs a due date", i+1)
		}
		sum += inst.AmountCents
	}
	if sum != total {
		return badReq("purchasing: instalments total %d cents but the bill is %d cents (out by %d)",
			sum, total, sum-total)
	}
	return nil
}

// GetBill returns a bill with lines + instalments, branch-fenced.
func (s *Service) GetBill(ctx context.Context, billID string) (BillDetail, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return BillDetail{}, err
	}
	b, err := s.repo.GetBill(ctx, billID)
	if err != nil {
		return BillDetail{}, err
	}
	if !caller.CanAccessBranch(b.BranchID) {
		return BillDetail{}, errNotFound
	}
	if names := s.vendorNames(ctx); names != nil {
		b.VendorName = names[b.VendorID]
	}
	return b, nil
}

// ListBills lists branch-fenced bills.
func (s *Service) ListBills(ctx context.Context, branchID, status, matchState string) ([]Bill, error) {
	_, branch, err := s.branchFence(ctx, branchID)
	if err != nil {
		return nil, err
	}
	bills, err := s.repo.ListBills(ctx, branch, status, matchState)
	if err != nil {
		return nil, err
	}
	if names := s.vendorNames(ctx); names != nil {
		for i := range bills {
			bills[i].VendorName = names[bills[i].VendorID]
		}
	}
	return bills, nil
}

// PostBill re-runs the match against the CURRENT received quantities, posts the AP journal, and advances the
// PO line billed rollups.
//
// THE RE-RUN IS NOT REDUNDANT. A bill entered on Tuesday against a partly-received PO may be posted on
// Thursday after the rest arrived; the match state stored at entry describes Tuesday. Money moves at POST, so
// the verdict that gates it must describe the moment it moves.
//
// A VARIANCE bill needs an explicit, recorded acceptance. Without it this refuses with the variance spelled
// out in cents, the point of the control is that somebody with a name decides to pay a disputed invoice.
func (s *Service) PostBill(ctx context.Context, billID string, in PostBillRequest) (BillDetail, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return BillDetail{}, err
	}
	if s.gl == nil {
		return BillDetail{}, ErrGLSeamUnwired
	}
	if s.rules == nil {
		return BillDetail{}, ErrGLSeamUnwired
	}

	tx, err := s.repo.Begin(ctx)
	if err != nil {
		return BillDetail{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	bill, err := s.repo.BillForUpdate(ctx, tx, billID)
	if err != nil {
		return BillDetail{}, err
	}
	if !caller.CanAccessBranch(bill.BranchID) {
		return BillDetail{}, errNotFound
	}
	if bill.Status != BillDraft {
		return BillDetail{}, conflict("purchasing: bill is already %s", bill.Status)
	}
	lines, err := s.repo.BillLines(ctx, tx, billID)
	if err != nil {
		return BillDetail{}, err
	}
	po, err := s.repo.GetPOTx(ctx, tx, bill.POID)
	if err != nil {
		return BillDetail{}, err
	}
	briefs, err := s.repo.POLineBriefs(ctx, tx, bill.POID, true)
	if err != nil {
		return BillDetail{}, err
	}
	tol, err := s.repo.ToleranceTx(ctx, tx, bill.BranchID)
	if err != nil {
		return BillDetail{}, err
	}

	states := make([]string, 0, len(lines))
	posting := make([]PostingLine, 0, len(lines))
	for _, l := range lines {
		mi := MatchInput{QtyBilledBase: l.QtyBilledBase, BillUnitCostCents: l.UnitCostCents}
		pl := PostingLine{QtyBilledBase: l.QtyBilledBase, LineTotalCents: l.LineTotalCents}
		if l.POLineID != "" {
			b, ok := briefs[l.POLineID]
			if !ok {
				return BillDetail{}, conflict("purchasing: bill line %d no longer matches a line on %s", l.LineNo, po.PONo)
			}
			prior, err := s.repo.PriorBilledQty(ctx, tx, l.POLineID, billID)
			if err != nil {
				return BillDetail{}, err
			}
			// Converted at the PO's frozen rate at POST time too, the match is re-run here (a bill
			// entered Tuesday may post Thursday), so both sides must be re-converted, not remembered.
			poUnitCad := toCAD(b.UnitCostCents, po.FXRate)
			mi.HasPOLine = true
			mi.QtyOrderedBase = b.QtyOrderedBase
			mi.QtyReceivedBase = b.QtyReceivedBase
			mi.QtyBilledPriorBase = prior
			mi.POUnitCostCents = poUnitCad

			pl.HasPOLine = true
			pl.QtyReceivedBase = b.QtyReceivedBase
			pl.QtyBilledPriorBase = prior
			pl.POUnitCostCents = poUnitCad
		}
		res := EvaluateLine(mi, tol)
		states = append(states, res.State)
		posting = append(posting, pl)
		if err := s.repo.UpdateBillLineMatch(ctx, tx, l.ID, res.State, res.QtyVarianceBase, res.PriceVarianceCents); err != nil {
			return BillDetail{}, err
		}
	}
	headerState := RollUp(states)
	if err := s.repo.SetBillMatchState(ctx, tx, billID, headerState); err != nil {
		return BillDetail{}, err
	}

	legs := BuildJournalLegs(posting, bill.TaxCents)
	if headerState == MatchVariance && !in.AcceptVariance {
		// Commit the refreshed match verdict before refusing, so the operator's next screen shows the
		// variance that blocked them rather than the stale entry-time state.
		if cerr := tx.Commit(ctx); cerr != nil {
			return BillDetail{}, fmt.Errorf("purchasing: commit match refresh: %w", cerr)
		}
		return BillDetail{}, conflict(
			"purchasing: bill %s is in VARIANCE (%d cents against the purchase order), post it with accept_variance to record who accepted it",
			bill.BillNo, legs.VarianceCents)
	}

	journalID, err := s.postBillJournal(ctx, tx, bill, legs)
	if err != nil {
		return BillDetail{}, err
	}
	for i, l := range lines {
		if l.POLineID == "" {
			continue
		}
		if err := s.repo.BumpBilled(ctx, tx, l.POLineID, l.QtyBilledBase, posting[i].LineTotalCents); err != nil {
			return BillDetail{}, err
		}
	}
	acceptedBy := ""
	if headerState == MatchVariance {
		acceptedBy = caller.UserID
	}
	if err := s.repo.SetBillPosted(ctx, tx, billID, journalID, acceptedBy, strings.TrimSpace(in.Note)); err != nil {
		return BillDetail{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return BillDetail{}, fmt.Errorf("purchasing: commit post bill: %w", err)
	}
	s.log.Info("purchasing: bill posted", "bill_no", bill.BillNo, "match_state", headerState,
		"grni_relief_cents", legs.GRNIReliefCents, "variance_cents", legs.VarianceCents, "ap_cents", legs.APCents)
	s.record(ctx, "purchasing.bill.posted", billID, map[string]any{
		"bill_no": bill.BillNo, "match_state": headerState, "gl_journal_id": journalID,
		"variance_cents": legs.VarianceCents, "accepted_variance": in.AcceptVariance,
	})
	return s.GetBill(ctx, billID)
}

// postBillJournal resolves the four legs through the posting rules and posts them. Idempotent on the bill id,
// so a retry after a mid-flight failure re-posts the same (deduped) journal rather than a second payable.
//
// The journal is posted THROUGH the bill's own transaction (the rules are read and the journal inserted on
// tx), so it commits or rolls back with the bill: a failure after the post (the bill's own writes, the commit)
// leaves no journal behind, and a retry never re-uses a journal whose legs came from an earlier read of the
// bill. The idempotency key stays as belt and braces. Going through tx also means PostBill holds one pool
// connection for the whole post, where posting on the pool made every PostBill pin one connection and wait
// for another (connection starvation by second acquisition).
func (s *Service) postBillJournal(ctx context.Context, tx pgx.Tx, bill Bill, legs JournalLegs) (string, error) {
	role := func(r string) (string, error) {
		return s.rules.ResolveRuleTx(ctx, tx, eventBillPosted, r, bill.BranchID)
	}
	apCode, err := role(roleAP)
	if err != nil {
		return "", err
	}
	var glLines []GLLine
	if legs.GRNIReliefCents != 0 {
		code, err := role(roleAPAccrual)
		if err != nil {
			return "", err
		}
		glLines = append(glLines, GLLine{AccountCode: code, DebitCents: legs.GRNIReliefCents, Memo: "GRNI relieved"})
	}
	if legs.TaxCents != 0 {
		code, err := role(roleTax)
		if err != nil {
			return "", err
		}
		glLines = append(glLines, GLLine{AccountCode: code, DebitCents: legs.TaxCents, Memo: "input tax"})
	}
	if legs.VarianceCents != 0 {
		code, err := role(rolePriceVar)
		if err != nil {
			return "", err
		}
		if legs.VarianceCents > 0 {
			glLines = append(glLines, GLLine{AccountCode: code, DebitCents: legs.VarianceCents, Memo: "purchase price variance"})
		} else {
			glLines = append(glLines, GLLine{AccountCode: code, CreditCents: -legs.VarianceCents, Memo: "purchase price variance (credit)"})
		}
	}
	glLines = append(glLines, GLLine{AccountCode: apCode, CreditCents: legs.APCents, Memo: "vendor bill " + bill.BillNo})

	return s.gl.PostJournalTx(ctx, tx, GLJournal{
		Source:         glSourcePOReceipt,
		SourceRefID:    bill.ID,
		IdempotencyKey: "purchasing.bill:" + bill.ID,
		Memo:           "Vendor bill " + bill.BillNo,
		BranchID:       bill.BranchID,
		Lines:          glLines,
	})
}

// record writes an A-21 audit entry when a recorder is wired. A missing recorder is never fatal, an audit
// gap is a lesser fault than refusing the operation the audit describes. (The HTTP middleware already records
// the coarse mutation; these are the money-bearing specifics: which PO, which variance, who accepted it.)
func (s *Service) record(ctx context.Context, action, entityID string, extra map[string]any) {
	if s.audit == nil {
		return
	}
	caller, _ := s.callerFrom(ctx)
	if err := s.audit.Record(ctx, audit.SensitiveAccess{
		Action:     action,
		EntityType: "purchasing",
		EntityID:   entityID,
		ActorID:    caller.UserID,
		BranchID:   callerBranch(caller),
		Purpose:    "PURCHASING",
		Extra:      extra,
	}); err != nil {
		s.log.Warn("purchasing: audit record failed", "action", action, "err", err)
	}
}
