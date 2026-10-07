// convert_staff.go, DIB-483 / M3-G7-NO-STAFF-CONVERT: the COUNTER door onto the conversion.
//
// THE GAP. A customer rings up, accepts a quote on the phone, then drives in. The counter user has the
// quote on screen, the customer in front of them, and no way to turn it into an order: the only
// convert route in the system was POST /api/portal/v1/quotes/{id}/convert, which requires a contractor
// portal session. The staff answer was to ask the customer to go home and click a button, which is
// not an answer, and which the M3 gate correctly refuses to pass.
//
// THE SHAPE. This is a DOOR, not a conversion. Everything that makes a conversion correct stays where
// it is (Repository.Convert, one transaction: credit gate → order mint → line copy → CONVERTED →
// soft-holds-become-hard). What this file adds is the part a counter door must do that a portal door
// must not: prove the caller may reach this quote's BRANCH, prove the caller holds a SELLING role, and
// then read the quote's OWN customer so the tenant-scoped write below is as narrow as it always was.
//
// Read that middle guard carefully, because it is the one that is easy to get wrong. Quote EDITING is
// open to any active in-branch staff member (edit_handler.go says so explicitly: a quote moves no
// money until Convert). Convert IS the money. So convert does NOT inherit the edit surface's
// permissiveness, it is gated on the selling roles (identity.CanSellOnCredit), which is why the yard
// team can price a pallet of flagstone all day and still not commit the customer's credit.
package quote

import (
	"context"
	"fmt"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// NotSellingRoleError is the refusal a non-selling staff member gets at the counter door. It carries
// the caller's role so the message names WHY rather than saying "forbidden", a yard-team member who
// is told only "403" will assume the system is broken and ask a manager to try it, which is exactly
// the wasted trip a plain refusal creates.
//
// It is a 403, not a 409: the quote is fine, the person is not.
type NotSellingRoleError struct {
	RoleCode string
	// Action names what the caller tried when it is not the conversion, so the staff accept and reject
	// doors (staff_decision.go) refuse in their own words while sharing this type and the one
	// predicate. Empty means the conversion, and the message is unchanged.
	Action string
}

func (e *NotSellingRoleError) Error() string {
	if e.Action != "" {
		if e.RoleCode == "" {
			return fmt.Sprintf("quote: %s is limited to sales roles", e.Action)
		}
		return fmt.Sprintf("quote: %s is limited to sales roles, %s cannot do it; ask inside sales or a manager",
			e.Action, e.RoleCode)
	}
	if e.RoleCode == "" {
		return "quote: converting a quote to an order is limited to sales roles"
	}
	return fmt.Sprintf(
		"quote: converting a quote to an order commits the customer's credit and is limited to sales roles, %s cannot do it; ask inside sales or a manager",
		e.RoleCode)
}

// convertPricingContext is the two facts the counter door needs off the quote before it can call the
// shared conversion: WHOSE quote it is (so the tenant-scoped write stays scoped) and WHICH BRANCH owns
// it (so the caller's reach can be checked). QuotePricingContext already answers exactly that pair for
// the pricing path, so this door reuses it rather than adding a third reader of the same two columns.
type convertPricingContext interface {
	QuotePricingContext(ctx context.Context, quoteID string) (customerID, branchID string, err error)
}

// ConvertQuote converts an ACCEPTED quote into an order from the COUNTER.
//
// idOrNo is a quote id or the visible quote number ("#Q-4" / "Q-4"), resolved branch-scoped by the
// same ResolveQuoteID every other staff quote read uses, so a counter user can convert the quote off
// the paper in their hand.
//
// The guard order is deliberate and mirrors StaffQuoteRead:
//
//	authenticated caller  → ErrUnauthorized       (401)
//	ACTIVE caller         → ErrForbidden          (403)
//	selling role          → NotSellingRoleError   (403, role named)
//	quote resolves        → ErrNotFound           (404, no existence oracle)
//	caller reaches branch → ErrForbidden          (403)
//	…then THE conversion  → runConvert (convert.go), identical to the contractor door
//
// Status is NOT checked here. Repository.Convert re-reads it under the row's FOR UPDATE lock and
// answers ErrInvalidTransition for anything that is not ACCEPTED; checking it again out here would be
// a check→write window and a second opinion about the same fact.
//
// A credit refusal arrives as ErrCreditBlocked and is surfaced with its policy + detail intact by
// StaffHandler.writeErr, the same words, the same 409, as the contractor door.
func (s *StaffService) ConvertQuote(ctx context.Context, idOrNo string, opt ConvertOptions) (ConvertResult, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return ConvertResult{}, err
	}
	if caller.Status != identity.StatusActive {
		return ConvertResult{}, ErrForbidden
	}
	if !identity.CanSellOnCredit(caller) {
		return ConvertResult{}, &NotSellingRoleError{RoleCode: caller.RoleCode}
	}
	rGlobal, rBranches := callerBranchScope(caller)
	quoteID, err := s.store.ResolveQuoteID(ctx, idOrNo, rGlobal, rBranches)
	if err != nil {
		return ConvertResult{}, err
	}
	pc, ok := s.store.(convertPricingContext)
	if !ok {
		// The store predates the seam. Refuse rather than guess at a customer id, a conversion
		// written against the wrong tenant is unrecoverable.
		return ConvertResult{}, ErrEditNotConfigured
	}
	customerID, branchID, err := pc.QuotePricingContext(ctx, quoteID)
	if err != nil {
		return ConvertResult{}, err
	}
	if !caller.CanAccessBranch(branchID) {
		return ConvertResult{}, ErrForbidden
	}
	// THE conversion, the same function, with the same arguments, as the contractor door. The only
	// difference is the actor stamped on the history row.
	return runConvert(ctx, s.convertStore(), s.audit, customerID, quoteID, caller.UserID, opt)
}

// convertStore narrows this service's store to the conversion seam. staffStore embeds convertStore, so
// this is a widening-free projection, it exists to make the call site above read as "the one seam",
// and to keep runConvert's parameter honest about how little it needs.
func (s *StaffService) convertStore() convertStore { return s.store }

// compile-time guard: the staff door's audit recorder is the same seam the contractor door uses, so a
// drift in audit.Recorder is a build failure here rather than a missing history row at the counter.
var _ = func(s *StaffService) audit.Recorder { return s.audit }
