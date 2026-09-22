package pricing

// auditref.go, DIB-402. The drill-through reference on a pricing_audit_log row: which NAVIGABLE
// record (product / customer / category) a pricing change lands on.
//
// This is deliberately separate from AuditEntry.EntityType/EntityID, which name the pricing CONFIG
// ROW that was written (a pricing_rules id, a customer_category_overrides id, …). Those ids have no
// screen. The reference below is what the audit viewer turns into a link.
//
// THE ONE RULE HERE: a reference is DERIVED from structured data the caller already holds, or it is
// absent. Nothing in this file parses field_name/old_value/new_value, and nothing falls back to a
// "probably the product" default. An empty pair is a legitimate, expected answer, see the
// TIER_DEFAULT case in ruleRef, and the UI renders those rows as plain text with no link.

import "strings"

// The closed set of drill-through target kinds, mirroring the CHECK in migration 0098. Widening
// this set means widening that CHECK and adding a route in app/src/_audit-drilldown.ts, all three
// move together, which is why the values are named constants rather than inline strings.
const (
	RefProduct  = "product"
	RefCustomer = "customer"
	RefCategory = "category"
	// RefQuote / RefOrder, E3 unpark (W4-A, migration 0114). 0098 shipped without these and said
	// exactly why: "pricing.ResolveInput ... carries NO document id, so the resolver physically does
	// not know which quote or order it is pricing for. Adding that seam is a resolver-contract
	// change." ResolveInput.DocRefType/DocRefID is that seam, so the two kinds are now real.
	//
	// They are DOCUMENT refs, not RECORD refs, and that distinction decides the precedence in
	// resolveBypassRef below: a document is a strictly better place to land a reader than the
	// product, because it carries the line, the customer, the qty and the date the price was struck.
	RefQuote = "quote"
	RefOrder = "order"
)

// validRefTypes is the guard normaliseAuditRef enforces. An unknown type is dropped rather than
// written, because the DB CHECK would otherwise fail the INSERT, and every AppendAudit call site
// discards its error (`_ = s.store.AppendAudit(...)`), so a rejected INSERT is a SILENTLY LOST
// audit row. Dropping the bad reference keeps the row.
//
// This map, the 0114 CHECK and REF_ROUTES in app/src/_audit-drilldown.ts are ONE closed set in
// three places. Widening it is all three edits or none, a kind here but not in the CHECK loses
// rows, a kind in the CHECK but not in REF_ROUTES renders as plain text with no link.
var validRefTypes = map[string]bool{
	RefProduct: true, RefCustomer: true, RefCategory: true, RefQuote: true, RefOrder: true,
}

// docRefTypes is the subset that names a DOCUMENT rather than a master record. Kept separate from
// validRefTypes so resolveBypassRef can prefer a document without hard-coding two string literals
// at the preference site.
var docRefTypes = map[string]bool{RefQuote: true, RefOrder: true}

// normaliseAuditRef returns the (type, id) pair that may be written, or ("", "") when the pair is
// not a complete, known reference. Both-or-neither mirrors the pricing_audit_ref_pair CHECK.
func normaliseAuditRef(refType, refID string) (string, string) {
	t := strings.TrimSpace(refType)
	id := strings.TrimSpace(refID)
	if t == "" || id == "" {
		return "", ""
	}
	if !validRefTypes[t] {
		return "", ""
	}
	return t, id
}

// ruleRef derives the drill-through target of a pricing_rules row, most specific dimension first:
// a rule pinned to a product is ABOUT that product; failing that, one pinned to a customer is about
// that customer; failing that, a category.
//
// A TIER_DEFAULT rule carries none of the three by construction (0023: the scope dimensions are all
// nullable and TIER_DEFAULT sets only customer_type). It returns ("", "") and its audit rows ship
// with no link. That is the honest answer: "every CONTRACTOR, every category" is not a record.
func ruleRef(r Rule) (string, string) {
	switch {
	case r.ProductID != "":
		return RefProduct, r.ProductID
	case r.CustomerID != "":
		return RefCustomer, r.CustomerID
	case r.CategoryID != "":
		return RefCategory, r.CategoryID
	default:
		return "", ""
	}
}

// breakRef derives the target of a quantity-break header. The 0023 CHECK guarantees at least one of
// product_id / category_id is set, so this returns a pair for any header that actually persisted;
// the empty branch exists for an in-memory header that has not been through validation yet.
func breakRef(h QuantityBreakHeader) (string, string) {
	switch {
	case h.ProductID != "":
		return RefProduct, h.ProductID
	case h.CategoryID != "":
		return RefCategory, h.CategoryID
	default:
		return "", ""
	}
}

// resolveBypassRef derives the target of a BYPASS row written during a resolve.
//
// The BYPASS write site records, in entity_id, WHICH policy disabled the margin gate, the product
// policy's product_id or the category policy's category_id. That is the right thing for
// entity_type/entity_id (it identifies the config that caused the bypass) and the wrong thing to
// navigate to on its own, because a reader looking at a bypassed line wants the line, not the
// config row.
//
// PRECEDENCE (E3 unpark): DOCUMENT first, then product, then category.
//
// The document wins when the caller supplied one, and the reasoning is about what the reader is
// doing. Nobody opens the pricing audit log to browse. They open it because a specific quote came
// out at a number somebody is questioning, and a BYPASS row means the margin floor did not run on
// that line, which is the single most likely thing they are questioning. Landing them on the
// product page makes them find the quote themselves, from a list, by date. Landing them on the
// quote puts the line, the customer, the qty and the price in front of them in one click.
//
// The product ref is not lost by this: entity_type/entity_id still identify the policy row, the
// resolve's product is in the same audit row's context, and a BYPASS with no document (a POS ring,
// a preview, procurement's indicative pricing) still falls back to the product exactly as before.
// This is a strict widening, no input that produced a product ref before produces a different one
// now unless it ALSO carries a document id, which no pre-W4 caller does.
func resolveBypassRef(in ResolveInput) (string, string) {
	if t, id := normaliseAuditRef(in.DocRefType, in.DocRefID); t != "" && docRefTypes[t] {
		return t, id
	}
	switch {
	case in.ProductID != "":
		return RefProduct, in.ProductID
	case in.CategoryID != "":
		return RefCategory, in.CategoryID
	default:
		return "", ""
	}
}
