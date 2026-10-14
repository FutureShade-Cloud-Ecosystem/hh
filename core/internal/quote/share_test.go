package quote

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// - the data-leak guard (R4) -

// staffOnlyFields are fields a customer holding a share link must NEVER see. They are enumerated
// explicitly rather than derived, because the whole risk here is a field being ADDED upstream and
// silently flowing through: a derived list would grow with the leak instead of catching it.
//
// The list covers four distinct classes of leak:
//
//	margin/cost  , what we paid and what we make. Commercially damaging on its own.
//	internal ids , product_id, branch_id, customer_id: identifiers that invite probing other records.
//	staff process, created_by/updated_by/editable_fields/metadata: who touched it and what staff may
//	                do next, none of which is the customer's business.
//	other records, anything implying another customer's or another quote's existence.
var staffOnlyFields = []string{
	"cost_cents", "cost", "margin", "margin_bps", "floor_bps",
	"product_id", "branch_id", "customer_id",
	"created_by", "updated_by", "metadata",
	"editable_fields", "qty_base", "price_uom",
	"blocked", "block_policy", "discount_bps", "original_cents",
}

// publicQuoteViewKeys is the RULING on what an anonymous link holder may see, written out as an
// independent literal rather than derived from the struct, a derived expectation would move with the
// code it is supposed to constrain, which is how a test becomes vacuous (the W10-A R6 lesson).
//
// Adding a field to PublicQuoteView must fail here and be added deliberately, with a reason.
var publicQuoteViewKeys = []string{
	"quote_no", "customer_name", "status", "valid_until", "expired", "lines",
	"subtotal_cents", "tax_cents", "total_cents", "as_of", "acceptable",
	// quantity_notice is the dealer's own wording that the quantities are the customer's to confirm:
	// shown to the customer by design, and it carries nothing but the dealer's sentence.
	"quantity_notice",
	// tax_label is the name of the dealer's own tax ("HST", "Sales tax") printed beside the tax figure:
	// a word of the dealer's, nothing of the customer's or the staff's.
	"tax_label",
}

// TestPublicQuoteView_LeaksNoStaffField asserts KEY-SET EQUALITY, in both directions, over the public
// payload, plus the staff-only denylist.
//
// The equality is what catches a leak: the previous denylist-only version could only catch a field
// whose name someone had thought to forbid in advance, and `InternalCostCents` sailed through it
// because "cost_cents" is not a substring match for "internal_cost_cents" as a quoted key (DIB-394).
// The denylist is KEPT alongside it because the two catch different things: equality catches any NEW
// top-level field, the denylist documents WHY specific names are forbidden and catches them nested
// inside a line, where a top-level key check cannot see them.
//
// Marshalling the real struct (rather than inspecting the type) is deliberate: it tests what actually
// crosses the wire, including anything an embedded struct or a json tag would drag along.
//
// OMITEMPTY TRAP: `valid_until` is the only omitempty field on PublicQuoteView, and `as_of` is a
// time.Time that would marshal as a zero date. Both are built NON-ZERO below, because an omitempty
// field left zero vanishes from the marshalled map and would fail the "lost expected field" leg for
// the wrong reason.
func TestPublicQuoteView_LeaksNoStaffField(t *testing.T) {
	view := PublicQuoteView{
		QuoteNo: "#Q-1", CustomerName: "Acme Hardscapes", Status: "SENT",
		ValidUntil: "2026-12-31", Expired: false, Acceptable: true,
		SubtotalCents: 500000, TaxCents: 65000, TotalCents: 565000,
		AsOf: time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC),
		Lines: []PublicQuoteLine{{
			LineNo: 1, Description: "Granite paver", QtyEntered: "10.0000",
			UOMEntered: "EA", UnitPriceCents: 50000, LineTotalCents: 500000,
		}},
	}
	blob, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(blob, &generic); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	want := make(map[string]bool, len(publicQuoteViewKeys))
	for _, k := range publicQuoteViewKeys {
		want[k] = true
	}
	for k := range generic {
		if !want[k] {
			t.Errorf("public quote payload exposes unexpected field %q, a customer must not see it; "+
				"add it to publicQuoteViewKeys with a reason, or remove it from PublicQuoteView", k)
		}
	}
	for k := range want {
		if _, ok := generic[k]; !ok {
			t.Errorf("public quote payload lost expected field %q", k)
		}
	}

	body := string(blob)
	for _, f := range staffOnlyFields {
		if _, present := generic[f]; present {
			t.Errorf("public quote payload exposes staff-only field %q", f)
		}
		// Also catch it nested inside a line, where a top-level key check would miss it.
		if strings.Contains(body, `"`+f+`"`) {
			t.Errorf("public quote payload contains staff-only key %q somewhere in the body: %s", f, body)
		}
	}
}

// TestPublicQuoteView_OrderNoRidesOnlyAConvertedQuote pins the one field the quote accent adds
// (decision log row 1063): the number of the order a converted quote became, so the customer's page
// can read "Sales order <number>, converted from <quote number>". The key is absent for every other
// quote (omitempty, so a SENT quote's payload is byte for byte what it was), and present exactly when
// the repository filled it. It is a number the customer was already shown on their own documents,
// never an id.
func TestPublicQuoteView_OrderNoRidesOnlyAConvertedQuote(t *testing.T) {
	base := PublicQuoteView{
		QuoteNo: "#Q-7", CustomerName: "Acme Hardscapes", Status: "SENT",
		AsOf: time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC), Lines: []PublicQuoteLine{},
	}
	keys := func(v PublicQuoteView) map[string]any {
		blob, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(blob, &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return m
	}
	if _, present := keys(base)["order_no"]; present {
		t.Error("a quote that is not converted must not carry order_no at all")
	}
	converted := base
	converted.Status, converted.OrderNo = "CONVERTED", "SO-TRT-17"
	if got := keys(converted)["order_no"]; got != "SO-TRT-17" {
		t.Errorf("a converted quote's payload carries order_no %v, want SO-TRT-17", got)
	}
	// The key set of a converted view is the ruling plus exactly this one key.
	want := map[string]bool{"order_no": true}
	for _, k := range publicQuoteViewKeys {
		want[k] = true
	}
	for k := range keys(converted) {
		if !want[k] && k != "valid_until" {
			t.Errorf("a converted view exposes unexpected field %q", k)
		}
	}
}

// TestPublicQuoteLine_ExposesOnlyWhatTheCustomerBuys pins the line shape independently, since a line is
// where an internal identifier is most likely to be added by reflex.
func TestPublicQuoteLine_ExposesOnlyWhatTheCustomerBuys(t *testing.T) {
	blob, err := json.Marshal(PublicQuoteLine{LineNo: 1, Description: "x", QtyEntered: "1",
		UOMEntered: "EA", UnitPriceCents: 1, LineTotalCents: 1})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(blob, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := map[string]bool{
		"line_no": true, "description": true, "qty_entered": true,
		"uom_entered": true, "unit_price_cents": true, "line_total_cents": true,
	}
	for k := range m {
		if !want[k] {
			t.Errorf("public line exposes unexpected field %q, add it to the ruling or remove it", k)
		}
	}
	for k := range want {
		if _, ok := m[k]; !ok {
			t.Errorf("public line is missing expected field %q", k)
		}
	}
}

// TestShareLink_NeverCarriesAToken pins that the staff-facing link model cannot serialise a token. The
// raw value is returned exactly once, from the mint endpoint, via MintedShareLink.
func TestShareLink_NeverCarriesAToken(t *testing.T) {
	blob, err := json.Marshal(ShareLink{ID: "l1", QuoteID: "q1"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(blob)
	for _, bad := range []string{"token", "token_hash", "hash"} {
		if strings.Contains(body, `"`+bad+`"`) {
			t.Errorf("ShareLink must not serialise %q; got %s", bad, body)
		}
	}
	// …and the mint response MUST carry it, exactly once.
	minted, err := json.Marshal(MintedShareLink{ShareLink: ShareLink{ID: "l1"}, Token: "raw"})
	if err != nil {
		t.Fatalf("marshal minted: %v", err)
	}
	if !strings.Contains(string(minted), `"token"`) {
		t.Error("the mint response must return the raw token once")
	}
}

// - the token primitive -

func TestShareToken_IsHighEntropyAndHashStable(t *testing.T) {
	raw1, hash1, err := newShareToken()
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	raw2, hash2, err := newShareToken()
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if raw1 == raw2 || hash1 == hash2 {
		t.Fatal("two mints produced the same token, the source of randomness is broken")
	}
	// 32 random bytes → 43 base64url chars. A shorter token would mean less entropy than intended.
	if len(raw1) < 43 {
		t.Errorf("raw token is %d chars, want >= 43 (32 bytes of entropy)", len(raw1))
	}
	// sha256 hex is 64 chars, and hashing must be deterministic or no lookup could ever match.
	if len(hash1) != 64 {
		t.Errorf("hash is %d chars, want 64 (sha256 hex)", len(hash1))
	}
	if hashShareToken(raw1) != hash1 {
		t.Error("hashing is not deterministic, a minted token would never resolve")
	}
	// The raw token must not be recoverable from, or equal to, what is stored.
	if strings.Contains(hash1, raw1) || raw1 == hash1 {
		t.Error("the stored hash must not contain or equal the raw token")
	}
}

func TestShareToken_UrlSafe(t *testing.T) {
	// The token rides in a URL path segment. Any '+' or '/' from standard base64 would need escaping
	// and would eventually be mangled by something in the chain.
	for i := 0; i < 50; i++ {
		raw, _, err := newShareToken()
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		if strings.ContainsAny(raw, "+/=") {
			t.Fatalf("token %q is not URL-safe", raw)
		}
	}
}

// TestDefaultShareTTL pins R4's default without freezing it as policy: the value is stored per link, so
// this constant only decides what a link gets when the caller does not say.
func TestDefaultShareTTL(t *testing.T) {
	if defaultShareTTL.Hours() != 30*24 {
		t.Errorf("defaultShareTTL = %v, want 30 days (R4)", defaultShareTTL)
	}
}
