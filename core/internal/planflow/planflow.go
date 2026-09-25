// Package planflow owns the plan-flow tables (plan and plan_line, the
// 0214 entities D30 gave client-generatable UUID keys and the sync
// primitives: seq, idempotency_key, deleted_at). The tables needed an
// owner for the sync surface (HH-3.5 #169) and no earlier module reads
// them; this module is that owner, the same discipline the attachments
// module took for the D10 tables in #166.
//
// WHY THE orders SCOPE IS THIS TABLE. The scope name in the manifest is
// the contractor's vocabulary: HH Pro's board card is called an Order
// ("Board cards are Orders, not Projects", hh_pro_dibbits/CLAUDE.md) and
// its stage vocabulary (plan, quote, order, invoice) is exactly the plan
// table's CHECK set (0214: "C19 grain per D7's contractor vocabulary").
// The one canonical dealer order aggregate stays the orders table and
// the contract's OrderCard projection (D7: no second entity, no second
// writer); plan is the contractor's own editable card, the draft the
// contractor composes offline and the desk prices. The confirmed policy
// table matches this grain: the card moves server_wins (the supplier
// leads the card once artifacts exist), while its lines merge
// last_write_wins_by_field (domain-map section 6: two people editing
// different lines is the common case, not a conflict). D30's ruling
// names these exact tables as the ones a create made with no signal
// needs no server round trip for, which is what an offline outbox
// replay is.
//
// The fence rules hold as everywhere: the sync core drives protocol
// mechanics and owns no table, this module touches only plan and
// plan_line, and the price legs of a line (price_source, price_snapshot,
// unit_price_cents, price_uom, line_total_cents, price_valid_until,
// qty_base after conversion) are the pricing flow's to write; a pushed
// payload that declares any of them refuses whole, so no client field
// merge can resurrect or counterfeit a price.
package planflow

import (
	"fmt"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// Stage is the plan table's CHECK vocabulary (0214), the contractor's
// board columns.
const (
	StagePlan    = "plan"
	StageQuote   = "quote"
	StageOrder   = "order"
	StageInvoice = "invoice"
)

// validStage reports whether s is one of the board columns.
func validStage(s string) bool {
	switch s {
	case StagePlan, StageQuote, StageOrder, StageInvoice:
		return true
	}
	return false
}

// tenantStageMove reports whether the tenant caller may drive one stage
// move itself. The contractor composes in plan and submits to the
// supplier's quote desk (plan to quote) and may withdraw a submission
// back to plan; the order and invoice columns are the supplier's moves
// (the artifacts exist by then, and D7's projection is what the desk
// reads). A pushed move into or out of the supplier's columns is the
// module's refusal and travels as that operation's rejected result, the
// refusal sentence the catalog's pro-procurement job names.
func tenantStageMove(from, to string) bool {
	switch {
	case from == StagePlan && to == StageQuote:
		return true
	case from == StageQuote && to == StagePlan:
		return true
	}
	return false
}

// Fulfillment is the plan table's CHECK set (C4's three values).
const (
	FulfillmentPickup   = "PICKUP"
	FulfillmentDelivery = "DELIVERY"
	FulfillmentWillCall = "WILL_CALL"
)

// validFulfillment reports whether f is one of the three fulfillment
// values.
func validFulfillment(f string) bool {
	switch f {
	case FulfillmentPickup, FulfillmentDelivery, FulfillmentWillCall:
		return true
	}
	return false
}

// maxNameLen bounds a card name the way the carried surfaces bound
// theirs; a longer name is a client defect, not a sync input.
const maxNameLen = 200

// maxInstructionsLen bounds the site instructions leg.
const maxInstructionsLen = 2000

// maxPORefLen bounds the purchase-order reference leg.
const maxPORefLen = 120

// maxNotesLen bounds a line's notes leg.
const maxNotesLen = 2000

// errRefused wraps one of the module's invariant refusals in the Store
// seam's classified marker, so the engine reports it as that operation's
// rejected result with the module's own words.
func errRefused(format string, a ...any) error {
	return fmt.Errorf("planflow: %s: %w", fmt.Sprintf(format, a...), sync.ErrOpRefused)
}

// looksLikeUUID reports whether s is a canonical 36-char 8-4-4-4-12 hex
// uuid, the same boundary guard the tasks module uses so a malformed id
// is rejected before it reaches a Postgres uuid column.
func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
	}
	return true
}
