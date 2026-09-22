// orderref.go, DIB-483 / M3-G8 + M3-G12-WILLCALL-UUID: how this module turns what a person TYPED
// into an order it can act on.
//
// THE GAP. The dispatch Schedule form and the Will-call form both asked for "uuid of the order". The
// counter's own POS lookup has accepted "#SO-TRT-1041" since DIB-379, and #SO- is what is printed on
// every piece of paper a customer or a yard hand ever sees. So the two surfaces that put an order on a
// truck or on a rack were the two surfaces that demanded the one identifier nobody has.
//
// WHAT THIS FILE IS AND IS NOT.
//
//	IS:     a GRADER. It decides whether a string is worth sending to the resolver at all, and if not,
//	        refuses it as a 400 in plain words (the productIDParam rule: a caller's typo must not
//	        arrive at the database and come back as "internal error").
//	IS NOT: a parser. It does not strip or add a '#', does not know the shape of an order number, does
//	        not query orders, and does not decide which branch a number may come from. All of that is
//	        order.Repository.ResolveOrderID, reached through the OrderReader seam, one resolver, and
//	        dib483_orderref_test.go fails if this package ever grows a second one.
package dispatch

import (
	"context"
	"strings"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// maxOrderRefLen bounds what will be graded. An order number is short; anything longer is a paste
// accident or an attack, and either way it should be refused before it becomes a query parameter.
const maxOrderRefLen = 64

// callerBranchScope projects a caller onto the (global, branchIDs) filter the resolver needs. It is
// order.callerBranchScope's rule, applied here because dispatch cannot import order, the same split
// CanAccessBranch enforces after the fact, applied AT RESOLUTION TIME so a visible number never
// resolves into a branch the caller cannot reach.
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

// gradeOrderRef decides whether ref is worth resolving.
//
// The three answers, and why each is the answer it is:
//
//	blank            → 400. The caller submitted an empty box; there is nothing to look up and
//	                   "not found" would wrongly imply we looked.
//	a UUID           → pass. Paste still works, unchanged; this is the flow that shipped.
//	an order-number- → pass. Whether it EXISTS is the resolver's business, branch-scoped.
//	shaped token
//	anything else    → 400 naming both accepted forms. A customer name or a phone number typed into
//	                   this box is a mistake the person can fix in two seconds if they are told what
//	                   the box wants, and a 404 does not tell them that.
//
// "Order-number-shaped" is deliberately loose: one leading '#' is optional, the body is letters,
// digits, '-' and '_', and it must contain at least one digit. That admits every form the mint has
// ever produced, the legacy global "#SO-4" and the branch-coded "#SO-TRT-1041" (0087), WITHOUT
// encoding the mint's format here, which is the mistake that would make this a parser. A future
// numbering scheme changes the resolver's SQL and nothing in this file.
func gradeOrderRef(ref string) (string, error) {
	s := strings.TrimSpace(ref)
	if s == "" {
		return "", httpx.BadRequestf("enter an order number (like #SO-TRT-1041) or an order id")
	}
	if len(s) > maxOrderRefLen {
		return "", httpx.BadRequestf("that is too long to be an order number or an order id")
	}
	if looksLikeUUID(s) {
		return s, nil
	}
	if !orderNumberShaped(s) {
		return "", httpx.BadRequestf(
			"%q is not an order number, enter a number like #SO-TRT-1041, or paste the order id", s)
	}
	return s, nil
}

// orderNumberShaped reports whether s could be an order number. See gradeOrderRef for why this is a
// shape test and not a format test.
func orderNumberShaped(s string) bool {
	s = strings.TrimPrefix(s, "#")
	if s == "" {
		return false
	}
	digit := false
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digit = true
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '-', r == '_':
		default:
			return false
		}
	}
	return digit
}

// resolveOrderRef is the ONE way this module turns a typed reference into an order id. Every surface
// that takes an order from a person goes through it, so they cannot drift into accepting different
// things.
//
// A reference that grades badly is a 400 with words. A reference that grades fine but resolves to
// nothing, including a number in a branch this caller cannot reach, is ErrNotFound, indistinguishable
// from a number that never existed. That asymmetry is the point: the caller's TYPING is graded openly,
// the book's CONTENTS are not.
func (s *Service) resolveOrderRef(ctx context.Context, caller identity.StaffContext, ref string) (string, error) {
	if s.orders == nil {
		return "", httpx.BadRequestf("dispatch: order seam is not wired")
	}
	graded, err := gradeOrderRef(ref)
	if err != nil {
		return "", err
	}
	global, branches := callerBranchScope(caller)
	id, err := s.orders.ResolveOrderID(ctx, graded, global, branches)
	if err != nil {
		return "", ErrNotFound
	}
	return id, nil
}
