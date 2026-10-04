// edit_write.go, the five staff quote write paths (DIB-56).
//
// Every one of them follows the same skeleton, which is DIB-175's verbatim:
//
//	authorizeQuoteEdit  → the cheap fail-fast gates (caller, branch, status, expiry), NO lock taken
//	price the line      → through the shared pricing seam, never local money math
//	<store>Guarded      → BEGIN; lock the quote row; RE-check status + expiry under the lock;
//	                      mutate; recompute totals; record the trail on the SAME tx; COMMIT
//
// The re-check under the lock is not redundant with authorizeQuoteEdit. The first check is advisory -
// it exists so an obviously-illegal edit is refused without paying for a lock. The authoritative one is
// inside the transaction, because between the two a concurrent acceptance can move the quote to
// ACCEPTED, and an edit that slipped through would silently change numbers the customer had agreed to.
package quote

import (
	"context"
	"fmt"
	"strings"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/jackc/pgx/v5"
)

// QuoteEditResult is what a successful edit returns: the affected line (nil for a removal or a date
// edit), the recomputed money, and the quote's status as read under the lock.
type QuoteEditResult struct {
	Line   *QuoteLineSnapshot `json:"line,omitempty"`
	Totals QuoteTotals        `json:"totals"`
	Status string             `json:"status"`
}

// CreateQuote opens a new DRAFT quote for a customer at a branch the caller can reach.
//
// The quote number is minted by the DB from `quote_no_seq` as '#Q-<n>'. That sequence has existed since
// 0008 and has never been used by any Go code, there was no creation path to use it. The '#'-prefixed
// form matches the house convention every other document already follows ('#SO-' for orders,
// '#PRICE-' for pricing orders), so a quote number reads the same way on a screen, a printout and a
// phone call.
func (s *StaffService) CreateQuote(ctx context.Context, in QuoteCreate) (StaffQuoteView, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return StaffQuoteView{}, err
	}
	if caller.Status != identity.StatusActive {
		return StaffQuoteView{}, ErrForbidden
	}
	in.CustomerID = strings.TrimSpace(in.CustomerID)
	in.BranchID = strings.TrimSpace(in.BranchID)
	if in.CustomerID == "" || in.BranchID == "" {
		return StaffQuoteView{}, httpx.BadRequestf("customer_id and branch_id are required")
	}
	// DIB-432: a branch id that is not a UUID used to sail past the global caller's
	// CanAccessBranch (GLOBAL reaches every branch, so the check is not an existence test) and
	// die inside the INSERT as a raw 500. A typed branch NAME is a client mistake, not an
	// internal error, say so, with the fix.
	if !looksLikeUUID(in.BranchID) {
		return StaffQuoteView{}, httpx.BadRequestf("quote: branch_id must be a branch id, pick a branch from the list, not a name")
	}
	// A staff member may only open a quote in a branch they can reach, the same horizontal boundary
	// every other write in this module enforces, applied at creation so a quote can never be born
	// outside its author's reach.
	if !caller.CanAccessBranch(in.BranchID) {
		return StaffQuoteView{}, ErrForbidden
	}
	vu, err := parseValidUntil(in.ValidUntil)
	if err != nil {
		return StaffQuoteView{}, err
	}
	in.ValidUntil = vu
	in.CreatedBy = caller.UserID

	id, _, err := s.store.CreateQuote(ctx, in)
	if err != nil {
		return StaffQuoteView{}, err
	}
	return s.StaffQuoteRead(ctx, id)
}

// AddLine appends a priced line to a quote.
func (s *StaffService) AddLine(ctx context.Context, quoteID, productID, description, qtyEntered, uom string) (QuoteEditResult, error) {
	caller, rule, quoteID, err := s.authorizeQuoteEdit(ctx, quoteID, FieldQuoteLineAdd)
	if err != nil {
		return QuoteEditResult{}, err
	}
	productID = strings.TrimSpace(productID)
	uom = strings.TrimSpace(uom)
	if productID == "" || uom == "" {
		return QuoteEditResult{}, httpx.BadRequestf("product_id and uom_entered are required")
	}
	description = strings.TrimSpace(description)
	if len(description) > maxQuoteDescription {
		return QuoteEditResult{}, httpx.BadRequestf(
			"description exceeds the %d character maximum", maxQuoteDescription)
	}
	qty, err := parseQuoteQty(qtyEntered)
	if err != nil {
		return QuoteEditResult{}, err
	}
	customerID, branchID, err := s.store.QuotePricingContext(ctx, quoteID)
	if err != nil {
		return QuoteEditResult{}, err
	}
	lp, err := s.priceLine(ctx, customerID, branchID, productID, uom, qty)
	if err != nil {
		return QuoteEditResult{}, err
	}
	// CP0-3 answer A: an accepted line at a below floor rule price is
	// logged for the pricing editors (the mark rides the read). The row
	// rides the write's own transaction, named by the line the insert
	// derived under the lock.
	pricingRows := rulePriceLogRows(caller, quoteID, productID, lp.Breakdown)
	after, totals, status, err := s.store.InsertLineGuarded(ctx, quoteEditWrite{
		QuoteID: quoteID, AllowedFrom: rule.fromList(),
		ProductID: productID, Description: description,
		QtyEntered: strings.TrimSpace(qtyEntered), UOMEntered: uom, QtyBase: formatQty(lp.QtyBase),
		UnitPriceCents: lp.UnitPriceCents, PriceUOM: lp.PriceUOM, LineTotalCents: lp.LineTotalCents,
		ActorID: caller.UserID,
	}, quoteEditHooks{
		record:  s.lineAuditHook(caller.UserID, quoteID, FieldQuoteLineAdd),
		holds:   s.holdSyncHook(quoteID, caller.UserID),
		pricing: s.pricingLogHook(pricingRows),
	})
	if err != nil {
		return QuoteEditResult{}, nameQuoteField(err, FieldQuoteLineAdd)
	}
	return QuoteEditResult{Line: &after, Totals: totals, Status: status}, nil
}

// EditLineQty changes one line's quantity and reprices it at today's rates.
//
// Repricing rather than scaling the stored unit price is deliberate and matches the reorder keystone:
// the stored unit_price_cents is never the input to a new price. A quantity change can cross a
// quantity-break threshold, so the unit price legitimately moves with it, which is exactly why the
// change trail records unit_price_cents as its own column alongside the quantity.
func (s *StaffService) EditLineQty(ctx context.Context, quoteID string, lineNo int, qtyEntered string) (QuoteEditResult, error) {
	caller, rule, quoteID, err := s.authorizeQuoteEdit(ctx, quoteID, FieldQuoteLineQty)
	if err != nil {
		return QuoteEditResult{}, err
	}
	qty, err := parseQuoteQty(qtyEntered)
	if err != nil {
		return QuoteEditResult{}, err
	}
	cur, uom, err := s.store.LineForEdit(ctx, quoteID, lineNo)
	if err != nil {
		return QuoteEditResult{}, err
	}
	customerID, branchID, err := s.store.QuotePricingContext(ctx, quoteID)
	if err != nil {
		return QuoteEditResult{}, err
	}
	lp, err := s.priceLine(ctx, customerID, branchID, cur.ProductID, uom, qty)
	if err != nil {
		return QuoteEditResult{}, err
	}
	// D5 (plan 3.3): a quantity change on a line that carries a discount
	// reprices it below its rule price, so the reprice runs the same floor
	// decision the discount arm runs. A line at its rule price (disc 0) is
	// not asked: that is QD-2's read side. But the discount the read SAW is
	// always recorded, at 0 too, so the guarded write re-checks it under
	// the lock: a discount set between the read and the commit cannot ride
	// a repriced line nobody decided on (P2-6). And a read that FAILS is a
	// refusal, never a skip: the floor question that cannot be answered
	// must not answer itself "no discount".
	plan := floorWritePlan{expectedDisc: -1}
	var out *floorOutcome
	if fs, ok := s.floorStore(); ok {
		lineID, disc, _, _, _, clearing, ferr := fs.LineFloorState(ctx, quoteID, lineNo)
		if ferr != nil {
			return QuoteEditResult{}, ferr
		}
		plan.expectedDisc = disc
		if disc > 0 {
			out, err = s.decideLineFloor(ctx, caller, quoteID, branchID, cur.ProductID, lineNo, disc, lineID, uom,
				lp.UnitPriceCents, lp.LineTotalCents, lp.PriceUOM, clearing)
			if err != nil {
				return QuoteEditResult{}, err
			}
			if out.refusal != nil {
				return QuoteEditResult{}, out.refusal
			}
			plan = out.plan
			plan.expectedDisc = disc
		}
	}
	// The pricing log rows the reprice owes, on the write's own
	// transaction (plan 4.2 and CP0-3 answer A): the floor row for a
	// repriced discounted line, and the rule price row when the reprice
	// struck a below floor rule price, logged like the add that first
	// wrote it. The add's own row is named by the line the write locked.
	rows := append(
		floorLogRows(caller, quoteID, cur.ProductID, plan.expectedDisc, out),
		rulePriceLogRows(caller, quoteID, cur.ProductID, lp.Breakdown)...)
	_, after, totals, status, err := s.store.UpdateLineGuarded(ctx, quoteEditWrite{
		QuoteID: quoteID, LineNo: lineNo, AllowedFrom: rule.fromList(),
		QtyEntered: strings.TrimSpace(qtyEntered), QtyBase: formatQty(lp.QtyBase),
		UnitPriceCents: lp.UnitPriceCents, PriceUOM: lp.PriceUOM, LineTotalCents: lp.LineTotalCents,
		ActorID: caller.UserID,
	}, plan, quoteEditHooks{
		record:  s.lineAuditHook(caller.UserID, quoteID, FieldQuoteLineQty),
		holds:   s.holdSyncHook(quoteID, caller.UserID),
		pricing: s.pricingLogHook(rows),
	})
	if err != nil {
		return QuoteEditResult{}, nameQuoteField(err, FieldQuoteLineQty)
	}
	return QuoteEditResult{Line: &after, Totals: totals, Status: status}, nil
}

// RemoveLine deletes a line and recomputes the quote money.
//
// Unlike an order line there is no allocated/fulfilled interlock to respect, nothing has been picked
// or shipped against a quote, so the only guards are the matrix and the expiry window.
func (s *StaffService) RemoveLine(ctx context.Context, quoteID string, lineNo int) (QuoteEditResult, error) {
	caller, rule, quoteID, err := s.authorizeQuoteEdit(ctx, quoteID, FieldQuoteLineRemove)
	if err != nil {
		return QuoteEditResult{}, err
	}
	_, totals, status, err := s.store.DeleteLineGuarded(ctx, quoteID, lineNo, rule.fromList(), caller.UserID,
		quoteEditHooks{
			record: s.lineAuditHook(caller.UserID, quoteID, FieldQuoteLineRemove),
			holds:  s.holdSyncHook(quoteID, caller.UserID),
		})
	if err != nil {
		return QuoteEditResult{}, nameQuoteField(err, FieldQuoteLineRemove)
	}
	return QuoteEditResult{Totals: totals, Status: status}, nil
}

// SetValidUntil moves the quote's expiry date. An empty string CLEARS it, which means the quote no
// longer expires (D2a), that is a legitimate operation, not a way to resurrect a lapsed quote: the
// authority ladder already refused the edit if the quote was expired when it was attempted.
func (s *StaffService) SetValidUntil(ctx context.Context, quoteID, newDate string) (QuoteEditResult, error) {
	caller, rule, quoteID, err := s.authorizeQuoteEdit(ctx, quoteID, FieldQuoteValidUntil)
	if err != nil {
		return QuoteEditResult{}, err
	}
	vu, err := parseValidUntil(newDate)
	if err != nil {
		return QuoteEditResult{}, err
	}
	_, _, status, err := s.store.UpdateValidUntilGuarded(ctx, quoteID, vu, rule.fromList(), caller.UserID,
		quoteDateHooks{
			record: s.dateAuditHook(caller.UserID, quoteID),
		})
	if err != nil {
		return QuoteEditResult{}, nameQuoteField(err, FieldQuoteValidUntil)
	}
	return QuoteEditResult{Status: status}, nil
}

// - audit hooks -

// lineAuditHook builds the record hook the guarded LINE writes run on their transaction (R3): it emits
// the per-column QUOTE_CHANGED rows for `cmd` and RETURNS any error so the edit rolls back.
func (s *StaffService) lineAuditHook(actor, quoteID string, cmd QuoteEditField) func(context.Context, pgx.Tx, QuoteLineSnapshot, QuoteLineSnapshot, QuoteTotals) error {
	return func(ctx context.Context, tx pgx.Tx, before, after QuoteLineSnapshot, totals QuoteTotals) error {
		return s.recordLineChange(ctx, tx, actor, quoteID, cmd, before, after, totals)
	}
}

// dateAuditHook builds the record hook for the valid_until write. A cleared date records an empty
// after-image, which the trail renders as "cleared" rather than as a missing row.
func (s *StaffService) dateAuditHook(actor, quoteID string) func(context.Context, pgx.Tx, string, string) error {
	return func(ctx context.Context, tx pgx.Tx, before, after string) error {
		if err := s.recordFieldChange(ctx, tx, actor, quoteID, 0, "valid_until", before, after); err != nil {
			return err
		}
		return nil
	}
}

// discountAuditHook builds the record hook the guarded discount write runs
// on its transaction (plan 4.2): the discount before and after and the
// line total, plus the quote level pair every line write records, so the
// quote's own history shows who discounted what.
func (s *StaffService) discountAuditHook(actor, quoteID string) func(context.Context, pgx.Tx, int, int, int, int64, int64, QuoteTotals) error {
	return func(ctx context.Context, tx pgx.Tx, lineNo, beforeDisc, afterDisc int, beforeTotal, afterTotal int64, totals QuoteTotals) error {
		bd, ad := int64(beforeDisc), int64(afterDisc)
		if err := s.recordIntChange(ctx, tx, actor, quoteID, lineNo, "disc_pct", &bd, &ad); err != nil {
			return fmt.Errorf("quote-discount audit: %w", err)
		}
		bt, at := beforeTotal, afterTotal
		if err := s.recordIntChange(ctx, tx, actor, quoteID, lineNo, "line_total_cents", &bt, &at); err != nil {
			return fmt.Errorf("quote-discount audit: %w", err)
		}
		subtotal, total := totals.SubtotalCents, totals.TotalCents
		if err := s.recordIntChange(ctx, tx, actor, quoteID, 0, "subtotal_cents", nil, &subtotal); err != nil {
			return fmt.Errorf("quote-discount audit: %w", err)
		}
		return s.recordIntChange(ctx, tx, actor, quoteID, 0, "total_cents", nil, &total)
	}
}
