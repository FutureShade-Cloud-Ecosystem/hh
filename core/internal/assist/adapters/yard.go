package adapters

// yard.go, DIB-478. "When's more arriving?", answered from BOTH legs.
//
// THE QUESTION HAS TWO ANSWERS AND A SURFACE THAT GIVES ONE IS WRONG. Stock reaches a yard two ways:
// a purchase order from a vendor (purchasing.IncomingForProduct) and a transfer from another of our
// own yards (inventory.InTransitForProduct). A rep told "nothing on order" while a truck from
// Kingston is halfway down the 401 has been given a true sentence and a false impression, and will
// tell a customer the wrong thing. So this adapter asks both and hands back one list.
//
// THE DATE IS DIB-472'S, AND IT IS READ, NEVER DERIVED. A purchase order line carries expected_at;
// a transfer leg now carries a stored promised-arrival (migration 0158, ruling D-083) captured at
// ship time from the branch pair's admin-maintained transit days. Either may be EMPTY, and empty
// travels across this seam as empty. Nothing here fills a date in from a ship date plus a guess:
// people plan around a date, and a date nothing in the database supports is worse than no date.

import (
	"context"
	"sort"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/purchasing"
)

// OpenPOReader is what this adapter needs from internal/purchasing.
//
// It takes no reach: purchasing derives its OWN branch fence from the request guard's caller
// (reachFor) and refuses a branch outside it, so passing a second reach here would be a second
// answer to a question that already has one. branchID "" means "every yard I may see", which is what
// the product card asks and what a conversation without a named branch means.
type OpenPOReader interface {
	IncomingForProduct(ctx context.Context, productID, branchID string) ([]purchasing.IncomingPOLine, error)
}

// InTransitReader is what this adapter needs from internal/inventory. It DOES take a reach, because
// that module's reads take the caller's authority as a required argument.
type InTransitReader interface {
	InTransitForProduct(ctx context.Context, reach inventory.BranchReach, productID string) ([]inventory.InTransitLine, error)
	LookupStock(ctx context.Context, reach inventory.BranchReach, ref string) (inventory.ProductLookup, []inventory.BranchStock, bool, error)
}

// The shipped services satisfy them, PARK risk discharged by the compiler, as adapters.go does for
// the original five.
var (
	_ OpenPOReader    = (*purchasing.Service)(nil)
	_ InTransitReader = (*inventory.Service)(nil)
)

type incomingAdapter struct {
	pos   OpenPOReader
	stock InTransitReader
	names BranchNamer
}

var _ assist.IncomingReader = incomingAdapter{}

// NewIncomingReader builds the seam.
//
// THE INVENTORY HALF IS REQUIRED AND THE PURCHASING HALF IS NOT. Inventory is what RESOLVES the
// product reference, without it there is no product and no answer at all. Purchasing only adds a
// leg: a deployment without it answers with transfers alone, which is incomplete but not wrong, and
// the alternative (refusing the whole question) would be worse. A missing PO leg is invisible to the
// reader, which is the one thing about this decision worth being uncomfortable about; it is
// acceptable only because a deployment with inventory and no purchasing does not exist outside a
// mid-rollout hour.
func NewIncomingReader(pos OpenPOReader, stock InTransitReader, names BranchNamer) assist.IncomingReader {
	if stock == nil {
		return nil
	}
	return incomingAdapter{pos: pos, stock: stock, names: names}
}

// Incoming resolves the product and gathers both legs, soonest first.
func (a incomingAdapter) Incoming(ctx context.Context, c assist.Caller, productRef string) (assist.ProductRef, []assist.IncomingLine, bool, error) {
	// THE PRODUCT IS RESOLVED THROUGH THE SAME LOOKUP the stock command uses, so "TB-BLU60" means the
	// same product to both answers and a conversation that moves from stock to incoming does not
	// silently change subject.
	p, _, found, err := a.stock.LookupStock(ctx, stockReach(c), productRef)
	if err != nil {
		return assist.ProductRef{}, nil, false, err
	}
	if !found {
		return assist.ProductRef{}, nil, false, nil
	}
	ref := assist.ProductRef{ProductID: p.ProductID, SKU: p.SKU, Name: p.Name}

	// The DESTINATION YARD, by the name a person says. Both legs carry a branch id and neither
	// carries a name, and a uuid on a chat line is a database key pretending to be an answer, it is
	// also unusable as a conversational referent, which is the half that matters here: "what about
	// Kingston?" can only narrow this answer if the answer says Kingston.
	names := map[string]string{}
	if a.names != nil {
		if m, nerr := a.names.BranchNames(ctx, c); nerr == nil {
			names = m
		}
	}

	var out []assist.IncomingLine
	if a.pos != nil {
		poLines, err := a.pos.IncomingForProduct(ctx, p.ProductID, "")
		if err != nil {
			return ref, nil, true, err
		}
		for _, l := range poLines {
			out = append(out, assist.IncomingLine{
				Source:    "PO",
				Reference: l.PONo,
				From:      vendorOr(l.VendorName),
				ToBranch:  branchNameOr(names, l.BranchID),
				QtyBase:   l.QtyOutstanding,
				UOM:       l.UOM,
				// Read, not derived. '' when the buyer raised the order without a promised date.
				ExpectedAt: l.ExpectedAt,
				Status:     l.Status,
			})
		}
	}

	transfers, err := a.stock.InTransitForProduct(ctx, stockReach(c), p.ProductID)
	if err != nil {
		return ref, nil, true, err
	}
	for _, t := range transfers {
		out = append(out, assist.IncomingLine{
			Source:    "TRANSFER",
			Reference: t.TransferNo,
			From:      transferFrom(t),
			ToBranch:  branchNameOrCode(names, t.ToBranchID, t.ToCode),
			QtyBase:   t.QtyBase,
			UOM:       t.UOM,
			// DIB-472: a STORED promise, or '', never a computation off ShippedAt.
			ExpectedAt: t.ETA,
			ShippedAt:  t.ShippedAt,
			Status:     "IN TRANSIT",
		})
	}

	// SOONEST FIRST, AND UNDATED LAST. "When is more arriving" is a question about the FIRST one, so
	// a promised line must never sort behind an unpromised one, an empty string sorts before every
	// date in a naive comparison, which would have put "not promised" at the top of the answer to a
	// question about timing.
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].ExpectedAt, out[j].ExpectedAt
		switch {
		case a == "" && b == "":
			return false
		case a == "":
			return false
		case b == "":
			return true
		default:
			return a < b
		}
	})
	if out == nil {
		// EMPTY, NOT NIL. "Nothing is on the way" is an answer the command says in words, and it must
		// not arrive looking like an absence.
		out = []assist.IncomingLine{}
	}
	return ref, out, true, nil
}

// branchNameOrCode prefers the yard's NAME, falls back to the code the transfer carried, and only
// then to the honest generic. A code is at least something a person recognises off a manifest.
func branchNameOrCode(names map[string]string, id, code string) string {
	if n := names[id]; n != "" {
		return n
	}
	if strings.TrimSpace(code) != "" {
		return code
	}
	return "another yard"
}

func vendorOr(name string) string {
	if strings.TrimSpace(name) == "" {
		return "a supplier"
	}
	return name
}

// transferFrom names the shipping yard the way a person would: its name if the read carried one, its
// code otherwise. Never blank, "from" with nothing after it reads as a rendering fault.
func transferFrom(t inventory.InTransitLine) string {
	switch {
	case strings.TrimSpace(t.FromName) != "":
		return t.FromName
	case strings.TrimSpace(t.FromCode) != "":
		return t.FromCode
	default:
		return "another yard"
	}
}
