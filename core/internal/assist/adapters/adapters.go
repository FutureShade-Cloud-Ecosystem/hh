// Package adapters wires TanyaAI's read seams to the domain services that answer them.
//
// -----------------------------------------------------------------------------------------------
// WHY THIS IS A PACKAGE AND NOT FOUR TYPES INSIDE internal/assist
// -----------------------------------------------------------------------------------------------
// internal/assist's package doc makes a load-bearing claim: "WHAT THIS PACKAGE IMPORTS: nothing
// from another domain." That is not decoration. It is what keeps the dependency arrow pointing one
// way, what lets every test in that package run against fakes with no database, and what makes
// "payments/order/inventory/customer never learn that an assistant exists" a statement about the
// import graph rather than a promise. Four adapters dropped into that package would import
// inventory, order, customer, billing and payments, and the claim would become false in the same
// commit that documented it.
//
// So the adapters live one directory down, in their own package. internal/assist still imports
// nothing; this package imports assist (for the seam types) and the five domains (for the reads),
// and nothing imports this package except the composition root. There is no cycle, and the
// architecture invariant survives contact with the wiring.
//
// -----------------------------------------------------------------------------------------------
// WHAT AN ADAPTER IS ALLOWED TO DO, AND WHAT IT IS NOT
// -----------------------------------------------------------------------------------------------
// ALLOWED: translate a Caller into the domain's own scope value; translate the domain's record into
// the seam's record; translate a domain vocabulary word into an English one.
//
// NOT ALLOWED, and structurally prevented rather than merely discouraged:
//
//   - WIDENING. Every domain read below takes the branch reach as a REQUIRED argument. There is no
//     overload that omits it, no default, and no "" that means "all". An adapter that forgot to
//     scope a read would not compile.
//   - RE-DERIVING AUTHORITY. Nothing here reads identity, touches a request, or looks in a context
//     for a caller. Authority is decided once by the request guard, converted once by
//     assist.CallerFrom, and travels here as a value that can only be read.
//   - ARITHMETIC. No adapter computes a balance, an availability or a total. Every number below is
//     carried through untouched from the service that owns it. One owner per number.
//   - CACHING. There is no state on any adapter but the service it calls. The single most likely way
//     to break the invariant this feature exists to hold would be a per-command cache that forgot
//     the caller in its key, so there is nothing here that could hold one.
//
// -----------------------------------------------------------------------------------------------
// EMPTY IS AN ANSWER, EVERYWHERE
// -----------------------------------------------------------------------------------------------
// Not one adapter converts "nothing found" into an error. found=false and a zero-length slice are
// carried through as themselves, because the commands above are written to SAY them, "no payment
// matches that reference", "that product has no stock on record yet", and dressing an ordinary
// empty state as a fault is the exact defect DIB-443 was filed for.
package adapters

import (
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
)

// -- the wiring, proved at compile time ---------------------------------------------------------
//
// These five lines are the whole of PARK 3's risk, discharged by the compiler rather than by a
// paragraph in a document. Each asserts that the SHIPPED service, the same value main.go already
// constructs for its own routes, not a copy and not a wrapper, satisfies the interface the
// corresponding adapter consumes.
//
// The reason this matters more than the usual compile-time-proof idiom: main.go is outside this
// lane's fence, so the wiring block is written in NOTES-FIX-431B.md and pasted by someone else. If
// one of these seams did not fit, the failure would surface in THEIR paste rather than in this
// build, at the least convenient moment. Here it is a build error in the package that owns the
// mistake.
var (
	_ StockLookup        = (*inventory.Service)(nil)
	_ OrderSummaryReader = (*order.Service)(nil)
	_ AccountLookup      = (*customer.Service)(nil)
	_ ARReader           = (*billing.CreditService)(nil)
	_ PaymentFinder      = (*payments.Service)(nil)
)

// -- the one scope conversion -------------------------------------------------------------------
//
// Each domain declares its OWN branch-reach type. They are structurally identical and deliberately
// distinct: the codebase's zero-trust convention is that a module names its own inputs, so that a
// change to one module's fence cannot silently redefine another's. The conversions therefore live
// here, in the composition layer, which is the only place that is allowed to know both vocabularies.
//
// THE THREE CASES, AND THE MIDDLE ONE IS THE POINT (the same rule w6aBranchScope encodes for the
// dashboard tiles):
//
//	Global               → no branch filter.
//	granted branches     → those branches.
//	NO grants at all     → NOTHING. Never "everything". A scoped account with no grants reaches no
//	                       branch, and every domain reach type below renders that as a filter that
//	                       matches no row rather than as an absent filter.
//
// A note on why these are four one-line functions and not one generic helper: a generic one would
// take and return `any`, which is precisely the type information that makes it impossible to pass
// the wrong domain's reach to the wrong domain's read.

func stockReach(c assist.Caller) inventory.BranchReach {
	if c.Global {
		return inventory.BranchReach{Global: true}
	}
	return inventory.BranchReach{BranchIDs: c.BranchIDs}
}

func orderReach(c assist.Caller) order.ReadScope {
	return order.ScopeFor(c.Global, c.BranchIDs)
}

func accountReach(c assist.Caller) customer.AccountReach {
	if c.Global {
		return customer.AccountReach{Global: true}
	}
	return customer.AccountReach{BranchIDs: c.BranchIDs}
}

func paymentReach(c assist.Caller) payments.BranchReach {
	if c.Global {
		return payments.BranchReach{Global: true}
	}
	return payments.BranchReach{BranchIDs: c.BranchIDs}
}
