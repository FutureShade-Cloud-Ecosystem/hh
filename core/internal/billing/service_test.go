package billing

import (
	"context"
	"sort"
	"testing"
	"time"
)

// fakeStore is an in-memory invoiceStore with real tenant scoping, status filtering,
// paging math, and open-balance summation, so the service tests exercise the seam
// without pgx (mirrors internal/product test style).
type fakeStore struct {
	items map[string]Invoice // keyed by invoice ID
}

func newFakeStore(invs ...Invoice) *fakeStore {
	m := make(map[string]Invoice, len(invs))
	for _, inv := range invs {
		m[inv.ID] = inv
	}
	return &fakeStore{items: m}
}

func (f *fakeStore) ListInvoices(_ context.Context, customerID string, flt Filter) ([]Invoice, error) {
	var matched []Invoice
	for _, inv := range f.items {
		if inv.CustomerID != customerID {
			continue
		}
		if flt.Status != "" && inv.Status != flt.Status {
			continue
		}
		matched = append(matched, inv)
	}
	// ORDER BY issue_date DESC, invoice_no, deterministic for cursor assertions
	// (the cursor carry, HH-3.2 fold: the window follows the stable key).
	sort.Slice(matched, func(i, j int) bool {
		if !matched[i].IssueDate.Equal(matched[j].IssueDate) {
			return matched[i].IssueDate.After(matched[j].IssueDate)
		}
		return matched[i].InvoiceNo < matched[j].InvoiceNo
	})
	if flt.AfterCursor {
		for i, inv := range matched {
			if inv.IssueDate.Format("2006-01-02") == flt.CursorIssueDate && inv.InvoiceNo == flt.CursorInvoiceNo {
				matched = matched[i+1:]
				break
			}
		}
	}
	hi := len(matched)
	if flt.Limit > 0 && flt.Limit < hi {
		hi = flt.Limit
	}
	return matched[:hi], nil
}

func (f *fakeStore) GetInvoice(_ context.Context, customerID, invoiceID string) (Invoice, error) {
	inv, ok := f.items[invoiceID]
	if !ok || inv.CustomerID != customerID { // cross-tenant reads as not-found, never forbidden
		return Invoice{}, ErrNotFound
	}
	return inv, nil
}

func (f *fakeStore) OpenBalanceCents(_ context.Context, customerID string) (int64, error) {
	var sum int64
	for _, inv := range f.items {
		if inv.CustomerID != customerID {
			continue
		}
		if inv.Status == "FINALIZED" || inv.Status == "PARTIALLY_PAID" {
			sum += inv.BalanceCents
		}
	}
	return sum, nil
}

func mkInvoice(id, no, customerID, status string, day int, balance int64) Invoice {
	return Invoice{
		ID:           id,
		InvoiceNo:    no,
		CustomerID:   customerID,
		Status:       status,
		IssueDate:    time.Date(2026, time.June, day, 0, 0, 0, 0, time.UTC),
		BalanceCents: balance,
	}
}

func seed() *fakeStore {
	// Tenant A: mix of statuses. Tenant B: isolation guard.
	return newFakeStore(
		mkInvoice("a1", "INV-1001", "cust-A", "FINALIZED", 1, 10000),
		mkInvoice("a2", "INV-1002", "cust-A", "PARTIALLY_PAID", 2, 5000),
		mkInvoice("a3", "INV-1003", "cust-A", "PAID", 3, 0),
		mkInvoice("a4", "INV-1004", "cust-A", "DRAFT", 4, 9999),
		mkInvoice("a5", "INV-1005", "cust-A", "FINALIZED", 5, 2500),
		mkInvoice("b1", "INV-2001", "cust-B", "FINALIZED", 1, 77777),
	)
}

func TestListInvoices_TenantScopedAndOrdered(t *testing.T) {
	svc := NewService(seed())
	ctx := context.Background()

	got, err := svc.ListInvoices(ctx, "cust-A", Filter{Limit: 100})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("got %d rows, want 5 (tenant A only, no B leak)", len(got))
	}
	// issue_date DESC -> a5,a4,a3,a2,a1
	wantOrder := []string{"a5", "a4", "a3", "a2", "a1"}
	for i, id := range wantOrder {
		if got[i].ID != id {
			t.Fatalf("order[%d] = %s, want %s (issue_date DESC)", i, got[i].ID, id)
		}
	}
}

func TestListInvoices_StatusFilter(t *testing.T) {
	svc := NewService(seed())
	ctx := context.Background()

	got, err := svc.ListInvoices(ctx, "cust-A", Filter{Status: "FINALIZED", Limit: 100})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("FINALIZED count = %d, want 2", len(got))
	}
	for _, inv := range got {
		if inv.Status != "FINALIZED" {
			t.Fatalf("status filter leaked %s", inv.Status)
		}
	}
}

func TestListInvoices_Paging(t *testing.T) {
	svc := NewService(seed())
	ctx := context.Background()

	// page 1: limit 2 -> a5,a4
	p1, err := svc.ListInvoices(ctx, "cust-A", Filter{Limit: 2})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(p1) != 2 || p1[0].ID != "a5" || p1[1].ID != "a4" {
		t.Fatalf("page1 = %+v, want [a5 a4]", ids(p1))
	}

	// page 2: after (a4 issue date, INV-1004) -> a3,a2
	p2, err := svc.ListInvoices(ctx, "cust-A", Filter{Limit: 2, AfterCursor: true,
		CursorIssueDate: p1[1].IssueDate.Format("2006-01-02"), CursorInvoiceNo: p1[1].InvoiceNo})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(p2) != 2 || p2[0].ID != "a3" || p2[1].ID != "a2" {
		t.Fatalf("page2 = %v, want [a3 a2]", ids(p2))
	}

	// page 3: after a2 -> a1 (partial last page)
	p3, err := svc.ListInvoices(ctx, "cust-A", Filter{Limit: 2, AfterCursor: true,
		CursorIssueDate: p2[1].IssueDate.Format("2006-01-02"), CursorInvoiceNo: p2[1].InvoiceNo})
	if err != nil {
		t.Fatalf("page3: %v", err)
	}
	if len(p3) != 1 || p3[0].ID != "a1" {
		t.Fatalf("page3 = %v, want [a1]", ids(p3))
	}

	// a cursor past every row -> the empty page
	p4, err := svc.ListInvoices(ctx, "cust-A", Filter{Limit: 2, AfterCursor: true,
		CursorIssueDate: p3[0].IssueDate.Format("2006-01-02"), CursorInvoiceNo: p3[0].InvoiceNo})
	if err != nil {
		t.Fatalf("overshoot: %v", err)
	}
	if len(p4) != 0 {
		t.Fatalf("overshoot page = %v, want empty", ids(p4))
	}
}

func TestListInvoices_Validation(t *testing.T) {
	svc := NewService(seed())
	ctx := context.Background()

	if _, err := svc.ListInvoices(ctx, "", Filter{}); err == nil {
		t.Fatal("expected error for empty customer id")
	}
	if _, err := svc.ListInvoices(ctx, "cust-A", Filter{Status: "BOGUS"}); err == nil {
		t.Fatal("expected error for invalid status")
	}
}

func TestGetInvoice_NotFoundAndCrossTenant(t *testing.T) {
	svc := NewService(seed())
	ctx := context.Background()

	if _, err := svc.GetInvoice(ctx, "cust-A", "nope"); err != ErrNotFound {
		t.Fatalf("missing id err = %v, want ErrNotFound", err)
	}
	// b1 belongs to cust-B; cust-A must see not-found, never forbidden.
	if _, err := svc.GetInvoice(ctx, "cust-A", "b1"); err != ErrNotFound {
		t.Fatalf("cross-tenant err = %v, want ErrNotFound", err)
	}
	got, err := svc.GetInvoice(ctx, "cust-A", "a1")
	if err != nil || got.ID != "a1" {
		t.Fatalf("own invoice: got %+v err %v", got, err)
	}
	if _, err := svc.GetInvoice(ctx, "", "a1"); err == nil {
		t.Fatal("expected error for empty customer id")
	}
}

func TestOpenBalanceCents_SumsOpenOnly(t *testing.T) {
	svc := NewService(seed())
	ctx := context.Background()

	// FINALIZED(10000) + PARTIALLY_PAID(5000) + FINALIZED(2500) = 17500; PAID/DRAFT excluded.
	bal, err := svc.OpenBalanceCents(ctx, "cust-A")
	if err != nil {
		t.Fatalf("open balance: %v", err)
	}
	if bal != 17500 {
		t.Fatalf("open balance = %d, want 17500", bal)
	}
	if _, err := svc.OpenBalanceCents(ctx, ""); err == nil {
		t.Fatal("expected error for empty customer id")
	}
}

func TestAvailableCreditCents_FlooredAtZero(t *testing.T) {
	svc := NewService(seed())
	ctx := context.Background()

	// limit 20000 - open 17500 = 2500 headroom.
	avail, err := svc.AvailableCreditCents(ctx, "cust-A", 20000)
	if err != nil {
		t.Fatalf("avail: %v", err)
	}
	if avail != 2500 {
		t.Fatalf("available = %d, want 2500", avail)
	}

	// over-limit account floors at 0, never negative.
	over, _ := svc.AvailableCreditCents(ctx, "cust-A", 10000)
	if over != 0 {
		t.Fatalf("over-limit available = %d, want 0", over)
	}
	if _, err := svc.AvailableCreditCents(ctx, "", 1); err == nil {
		t.Fatal("expected error for empty customer id")
	}
}

func ids(invs []Invoice) []string {
	out := make([]string, len(invs))
	for i, inv := range invs {
		out[i] = inv.ID
	}
	return out
}
