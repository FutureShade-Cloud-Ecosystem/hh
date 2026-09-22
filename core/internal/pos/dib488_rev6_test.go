package pos

// DIB-488 rev 6, item D (PO / Job # + salesperson, 0168) and item F (cash tendered / change due).
// DB-free service tests on the service_test.go harness; exactly additive.

import (
	"context"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
)

// -- item D: PO / Job # + salesperson ride the order and the audit trail -------------------------

func TestSale_POJobRefAndSalesperson_RideTheOrder(t *testing.T) {
	h := newHarness()
	h.store.staff["11111111-2222-4333-8444-555555555555"] = "Dana Counter"
	in := baseInput()
	in.POJobRef = "  PO 4471 / SMITH DECK  "
	in.SalespersonID = "11111111-2222-4333-8444-555555555555"
	out, err := h.svc.Sale(context.Background(), in)
	if err != nil {
		t.Fatalf("sale: %v", err)
	}
	if len(h.store.orders) != 1 {
		t.Fatalf("want 1 order, got %d", len(h.store.orders))
	}
	o := h.store.orders[0]
	if o.POJobRef != "PO 4471 / SMITH DECK" {
		t.Fatalf("po_job_ref not trimmed onto the order: %q", o.POJobRef)
	}
	if o.SalespersonID != in.SalespersonID {
		t.Fatalf("salesperson_id not on the order: %q", o.SalespersonID)
	}
	if out.POJobRef != "PO 4471 / SMITH DECK" || out.SalespersonName != "Dana Counter" {
		t.Fatalf("result must echo the facts: %+v", out)
	}
	// The completed-sale audit record carries both in Extra (the activity-trail idiom).
	var found bool
	for _, row := range h.rec.rows {
		if row.Action != audit.ActSaleCompleted {
			continue
		}
		found = true
		if row.Extra["po_job_ref"] != "PO 4471 / SMITH DECK" || row.Extra["salesperson_id"] != in.SalespersonID {
			t.Fatalf("audit Extra must carry the item-D facts: %+v", row.Extra)
		}
	}
	if !found {
		t.Fatalf("no ActSaleCompleted record")
	}
}

func TestSale_POJobRefTooLong_422(t *testing.T) {
	h := newHarness()
	in := baseInput()
	in.POJobRef = strings.Repeat("x", 65)
	_, err := h.svc.Sale(context.Background(), in)
	assertStatus(t, err, 422)
	if len(h.store.orders) != 0 {
		t.Fatalf("a refused sale must write nothing")
	}
}

func TestSale_SalespersonUnknown_422(t *testing.T) {
	h := newHarness()
	in := baseInput()
	// A well-formed uuid that is NOT an active staff member.
	in.SalespersonID = "99999999-9999-4999-8999-999999999999"
	_, err := h.svc.Sale(context.Background(), in)
	assertStatus(t, err, 422)
	// A malformed id is the same refusal, before any read.
	in.SalespersonID = "not-a-uuid"
	_, err = h.svc.Sale(context.Background(), in)
	assertStatus(t, err, 422)
}

// -- item F: cash tendered / change due ----------------------------------------------------------

func TestSale_CashTendered_ChangeDueComputed(t *testing.T) {
	h := newHarness()
	in := baseInput() // CASH, one 10000¢ line → total 11300¢ (flat 13%)
	in.CashTenderedCents = 20000
	out, err := h.svc.Sale(context.Background(), in)
	if err != nil {
		t.Fatalf("sale: %v", err)
	}
	if out.CashTenderedCents != 20000 {
		t.Fatalf("tendered must echo: %d", out.CashTenderedCents)
	}
	if want := int64(20000) - out.TotalCents; out.ChangeDueCents != want {
		t.Fatalf("change due: want %d, got %d", want, out.ChangeDueCents)
	}
	// The money itself is unchanged: the till captured exactly the total.
	if h.tender.last.AmountCents != out.TotalCents {
		t.Fatalf("the till must capture the TOTAL, not the tendered amount: %d", h.tender.last.AmountCents)
	}
	// The fact is parked on the order metadata so a replay can answer verbatim.
	if got, ok := h.store.orders[0].Metadata["cash_tendered_cents"].(int64); !ok || got != 20000 {
		t.Fatalf("cash_tendered_cents must ride the order metadata: %+v", h.store.orders[0].Metadata)
	}
}

func TestSale_CashTenderedShort_422(t *testing.T) {
	h := newHarness()
	in := baseInput()
	in.CashTenderedCents = 100 // far under the 11300¢ total
	_, err := h.svc.Sale(context.Background(), in)
	assertStatus(t, err, 422)
	if len(h.store.orders) != 0 {
		t.Fatalf("a refused sale must write nothing")
	}
}

func TestSale_CashTenderedOnNonCash_422(t *testing.T) {
	h := newHarness()
	in := baseInput()
	in.Tender = TenderAccount // unpaid at ring, no cash changes hands now
	in.CashTenderedCents = 20000
	_, err := h.svc.Sale(context.Background(), in)
	assertStatus(t, err, 422)
}

// assertStatus asserts err is the pos clientError with the wanted HTTP status.
func assertStatus(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatalf("want a %d refusal, got nil", want)
	}
	ce, ok := err.(clientError)
	if !ok {
		t.Fatalf("want clientError, got %T: %v", err, err)
	}
	if ce.status != want {
		t.Fatalf("want %d, got %d (%s)", want, ce.status, ce.msg)
	}
}
