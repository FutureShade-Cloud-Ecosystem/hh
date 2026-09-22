package pricing

// Unit gates for the DIB-402 drill-through reference. These are pure, no DB, and they pin the
// two decisions that matter: WHICH record a change points at, and what happens to a reference that
// is not a complete, known pair.
//
// The integration half (does the pair actually reach the row, and come back out of the read query)
// lives in auditref_integration_test.go.

import "testing"

func TestNormaliseAuditRef_DropsIncompleteOrUnknownPairs(t *testing.T) {
	// Why this is a GUARD and not a formality: every AppendAudit call site in service.go discards
	// its error (`_ = s.store.AppendAudit(...)`). If a half-set or unknown pair reached the INSERT
	// it would trip the pricing_audit_ref_pair / pricing_audit_ref_type_known CHECKs from migration
	// 0098, the INSERT would fail, the error would be thrown away, and the AUDIT ROW WOULD VANISH.
	// A pricing change with no audit row is the worst outcome available here, strictly worse than
	// a row with no link, so normaliseAuditRef degrades to "no reference" rather than to "no row".
	cases := []struct {
		name          string
		inType, inID  string
		wantT, wantID string
	}{
		{"complete product ref survives", RefProduct, "11111111-1111-1111-1111-111111111111", RefProduct, "11111111-1111-1111-1111-111111111111"},
		{"complete customer ref survives", RefCustomer, "22222222-2222-2222-2222-222222222222", RefCustomer, "22222222-2222-2222-2222-222222222222"},
		{"complete category ref survives", RefCategory, "33333333-3333-3333-3333-333333333333", RefCategory, "33333333-3333-3333-3333-333333333333"},
		{"type without id is dropped", RefProduct, "", "", ""},
		{"id without type is dropped", "", "44444444-4444-4444-4444-444444444444", "", ""},
		// W4-A DELIBERATE FLIP. 'quote' used to be the example of an unknown kind here, on the
		// grounds (0098) that "pricing.ResolveInput carries no document id, so the resolver
		// physically does not know which quote it is pricing for". ResolveInput.DocRefType/DocRefID
		// is that missing seam, migration 0114 widened the CHECK, and REF_ROUTES gained the route -
		// so 'quote' and 'order' are now COMPLETE, KNOWN pairs and must survive.
		{"quote ref now survives (E3 unpark)", RefQuote, "55555555-5555-5555-5555-555555555555", RefQuote, "55555555-5555-5555-5555-555555555555"},
		{"order ref now survives (E3 unpark)", RefOrder, "77777777-7777-7777-7777-777777777777", RefOrder, "77777777-7777-7777-7777-777777777777"},
		// The guard itself is unchanged, a kind outside the closed set is still dropped rather
		// than written, because the 0114 CHECK would reject the INSERT and the discarded error
		// would take the whole audit row with it.
		{"a kind outside the set is still dropped", "invoice", "55555555-5555-5555-5555-555555555555", "", ""},
		{"a config-row kind is not a drill-through kind", "pricing_rule", "55555555-5555-5555-5555-555555555555", "", ""},
		{"whitespace-only type is dropped", "   ", "66666666-6666-6666-6666-666666666666", "", ""},
		{"whitespace-only id is dropped", RefProduct, "   ", "", ""},
		{"empty pair stays empty", "", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotT, gotID := normaliseAuditRef(c.inType, c.inID)
			if gotT != c.wantT || gotID != c.wantID {
				t.Fatalf("normaliseAuditRef(%q,%q) = (%q,%q), want (%q,%q)",
					c.inType, c.inID, gotT, gotID, c.wantT, c.wantID)
			}
		})
	}
}

func TestRuleRef_PicksTheMostSpecificDimension(t *testing.T) {
	// The order is not arbitrary. A rule that names a product is ABOUT that product even when it
	// also names the customer it was negotiated with; sending the reader to the customer page
	// instead would answer a question they did not ask.
	prod, cust, cat := "p-1", "c-1", "k-1"

	if gotT, gotID := ruleRef(Rule{ProductID: prod, CustomerID: cust, CategoryID: cat}); gotT != RefProduct || gotID != prod {
		t.Fatalf("product must win over customer and category, got (%q,%q)", gotT, gotID)
	}
	if gotT, gotID := ruleRef(Rule{CustomerID: cust, CategoryID: cat}); gotT != RefCustomer || gotID != cust {
		t.Fatalf("customer must win over category, got (%q,%q)", gotT, gotID)
	}
	if gotT, gotID := ruleRef(Rule{CategoryID: cat}); gotT != RefCategory || gotID != cat {
		t.Fatalf("category is the last resort, got (%q,%q)", gotT, gotID)
	}
}

func TestRuleRef_TierDefaultHasNoNavigableRecord(t *testing.T) {
	// A TIER_DEFAULT rule means "every CONTRACTOR, every category". That is a POLICY, not a record,
	// and there is no page in this app it could open. The honest answer is the empty pair, and the
	// viewer renders those rows as plain text.
	//
	// This test exists to stop a future "helpful" fallback, e.g. returning the customer TYPE as if
	// it were a customer id, from shipping. That link would 404 on click, and a drill-through that
	// sometimes lies is worse than one that is sometimes absent.
	gotT, gotID := ruleRef(Rule{Scope: ScopeTierDefault, CustomerType: "CONTRACTOR"})
	if gotT != "" || gotID != "" {
		t.Fatalf("a TIER_DEFAULT rule must yield NO reference, got (%q,%q)", gotT, gotID)
	}
}

func TestBreakRef_ProductThenCategory(t *testing.T) {
	if gotT, gotID := breakRef(QuantityBreakHeader{ProductID: "p-9", CategoryID: "k-9"}); gotT != RefProduct || gotID != "p-9" {
		t.Fatalf("a product-scoped break must reference the product, got (%q,%q)", gotT, gotID)
	}
	if gotT, gotID := breakRef(QuantityBreakHeader{CategoryID: "k-9"}); gotT != RefCategory || gotID != "k-9" {
		t.Fatalf("a category-scoped break must reference the category, got (%q,%q)", gotT, gotID)
	}
	if gotT, gotID := breakRef(QuantityBreakHeader{}); gotT != "" || gotID != "" {
		t.Fatalf("an unscoped header must yield no reference, got (%q,%q)", gotT, gotID)
	}
}

func TestResolveBypassRef_PointsAtWhatWasPricedNotWhatWasDisabled(t *testing.T) {
	// The BYPASS row's entity_id records which POLICY switched the gate off, often a CATEGORY,
	// even when the line being priced was a specific product. A reader who clicks a bypassed line
	// wants the product. This is the whole reason the reference is a separate column rather than a
	// reinterpretation of entity_id.
	if gotT, gotID := resolveBypassRef(ResolveInput{ProductID: "p-2", CategoryID: "k-2"}); gotT != RefProduct || gotID != "p-2" {
		t.Fatalf("a bypass on a product resolve must reference the product, got (%q,%q)", gotT, gotID)
	}
	if gotT, gotID := resolveBypassRef(ResolveInput{CategoryID: "k-2"}); gotT != RefCategory || gotID != "k-2" {
		t.Fatalf("a category-only resolve falls back to the category, got (%q,%q)", gotT, gotID)
	}
	if gotT, gotID := resolveBypassRef(ResolveInput{}); gotT != "" || gotID != "" {
		t.Fatalf("a resolve naming nothing must yield no reference, got (%q,%q)", gotT, gotID)
	}
}

// - E3 unpark: the DOCUMENT ref -

func TestResolveBypassRef_DocumentBeatsProduct(t *testing.T) {
	// The precedence flip this ticket exists for. Nobody browses the pricing audit log; they open
	// it because a specific quote came out at a number somebody is questioning. A BYPASS row says
	// the margin floor did not run on that line, landing the reader on the QUOTE puts the line,
	// the customer, the qty and the price in front of them, where landing them on the product page
	// makes them find the quote themselves from a list.
	in := ResolveInput{ProductID: "p-2", CategoryID: "k-2", DocRefType: RefQuote, DocRefID: "q-7"}
	if gotT, gotID := resolveBypassRef(in); gotT != RefQuote || gotID != "q-7" {
		t.Fatalf("a resolve carrying a quote ref must point at the quote, got (%q,%q)", gotT, gotID)
	}
	in.DocRefType, in.DocRefID = RefOrder, "o-7"
	if gotT, gotID := resolveBypassRef(in); gotT != RefOrder || gotID != "o-7" {
		t.Fatalf("an order ref must point at the order, got (%q,%q)", gotT, gotID)
	}
}

func TestResolveBypassRef_StrictWideningOnly(t *testing.T) {
	// The claim made in auditref.go: "no input that produced a product ref before produces a
	// different one now unless it ALSO carries a document id". These are the three ways a document
	// ref can be present-but-unusable, and every one of them must fall back to the OLD answer
	// rather than to no answer.
	cases := []struct{ name, refType, refID string }{
		{"no document ref at all", "", ""},
		{"type with no id", RefQuote, ""},
		{"id with no type", "", "q-7"},
		{"a NON-document kind cannot be smuggled in through the doc fields", RefCustomer, "c-9"},
		{"an unknown kind is not a document", "invoice", "i-9"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := ResolveInput{ProductID: "p-2", CategoryID: "k-2", DocRefType: c.refType, DocRefID: c.refID}
			gotT, gotID := resolveBypassRef(in)
			if gotT != RefProduct || gotID != "p-2" {
				t.Fatalf("must fall back to the product ref, got (%q,%q)", gotT, gotID)
			}
		})
	}
}
