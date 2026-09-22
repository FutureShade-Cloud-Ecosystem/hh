package portal

// payments_test.go, M2: the portal payment history is a TENANT surface; the Q-044 staff attribution
// (payment_allocations.allocated_by + display name) must never serialize through it. The staff-facing
// attribution reads stay on the /api/v1/ar surface (internal/till/history.go).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
)

// fakePaymentStore satisfies the payments store seam with one attributed payment, so the REAL
// payments.Service + the REAL handler mapping run end to end (no DB).
type fakePaymentStore struct {
	list []payments.Payment
}

func (f *fakePaymentStore) ListMethods(context.Context, string) ([]payments.PaymentMethod, error) {
	return nil, nil
}
func (f *fakePaymentStore) GetMethod(context.Context, string, string) (payments.PaymentMethod, error) {
	return payments.PaymentMethod{}, payments.ErrNotFound
}
func (f *fakePaymentStore) AddMethod(_ context.Context, m payments.PaymentMethod, _ bool) (payments.PaymentMethod, error) {
	return m, nil
}
func (f *fakePaymentStore) RemoveMethod(context.Context, string, string) error     { return nil }
func (f *fakePaymentStore) SetDefaultMethod(context.Context, string, string) error { return nil }
func (f *fakePaymentStore) ListPayments(context.Context, string) ([]payments.Payment, error) {
	return f.list, nil
}
func (f *fakePaymentStore) FindByGatewayRef(context.Context, string, string) (payments.Payment, bool, error) {
	return payments.Payment{}, false, nil
}
func (f *fakePaymentStore) PayableInvoices(context.Context, string, []string) ([]payments.Allocation, error) {
	return nil, nil
}
func (f *fakePaymentStore) Pay(context.Context, string, string, string, []payments.Allocation) (payments.Payment, error) {
	return payments.Payment{}, nil
}

func TestPayments_PortalList_OmitsStaffAttribution(t *testing.T) {
	const staffID = "99999999-8888-4777-8666-555555555555"
	store := &fakePaymentStore{list: []payments.Payment{{
		ID: "pay-1", PaymentNo: "PMT-000042", CustomerID: tenantA, Method: "CASH",
		AmountCents: 4500, Status: "RECEIVED", ReceivedAt: time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC),
		Allocations: []payments.Allocation{{
			InvoiceID: "inv-1", AmountCents: 4500,
			AllocatedBy: staffID, AllocatedByName: "Sarah Staff",
			CreatedAt: time.Date(2026, 7, 13, 12, 0, 1, 0, time.UTC),
		}},
	}}}
	h := NewHandler(nil, nil).WithPayments(payments.NewService(store, payments.NewStubGateway()))

	w := httptest.NewRecorder()
	h.listPayments(w, reqWithCaller(http.MethodGet, "/api/portal/v1/payments", "", tenantA))
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d: %s", w.Code, w.Body)
	}
	body := w.Body.String()
	for _, leak := range []string{"allocated_by", "allocated_by_name", staffID, "Sarah Staff"} {
		if strings.Contains(body, leak) {
			t.Fatalf("portal payments must not carry staff attribution (%q): %s", leak, body)
		}
	}
	// The tenant-relevant allocation facts survive the projection.
	var out []portalPayment
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, body)
	}
	if len(out) != 1 || len(out[0].Allocations) != 1 {
		t.Fatalf("projection lost the payment/allocation: %s", body)
	}
	al := out[0].Allocations[0]
	if al.InvoiceID != "inv-1" || al.AmountCents != 4500 || al.CreatedAt.IsZero() {
		t.Fatalf("tenant allocation facts lost: %+v", al)
	}

	// An empty history still serializes [] (never null).
	store.list = nil
	w = httptest.NewRecorder()
	h.listPayments(w, reqWithCaller(http.MethodGet, "/api/portal/v1/payments", "", tenantA))
	if got := strings.TrimSpace(w.Body.String()); got != "[]" {
		t.Fatalf("empty history must serialize []: %q", got)
	}
}
