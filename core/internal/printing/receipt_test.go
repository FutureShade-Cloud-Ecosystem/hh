package printing

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// fakeReceiptStore serves one committed sale, DB-free. It reproduces the repository's id-vs-order_no
// fork faithfully, including the branch-scoped RESOLUTION on the "#SO-" path, because that
// difference is the point of TestReceipt_CrossBranchOrderNoIsNotFound.
type fakeReceiptStore struct {
	sale  receiptSale
	found bool
}

func (f *fakeReceiptStore) ReadReceiptSale(_ context.Context, id string, global bool, branchIDs []string) (receiptSale, bool, error) {
	if !f.found {
		return receiptSale{}, false, nil
	}
	if id == f.sale.OrderID {
		return f.sale, true, nil // uuid: resolved unscoped, reach enforced by the caller
	}
	if id == f.sale.OrderNo || "#"+id == f.sale.OrderNo {
		if global {
			return f.sale, true, nil
		}
		for _, b := range branchIDs {
			if b == f.sale.BranchID {
				return f.sale, true, nil
			}
		}
	}
	return receiptSale{}, false, nil
}

func (f *fakeReceiptStore) ReadReceiptLines(_ context.Context, _ string) ([]ReceiptLine, error) {
	return []ReceiptLine{{
		LineNo: 1, SKU: "DBS-UL-BRUSSELS", Description: "Brussels Block",
		Qty: 10, UOM: "SF", UnitPriceCents: 1234, PriceUOM: "SF", LineTotalCents: 12340,
	}}, nil
}

func (f *fakeReceiptStore) ReadReceiptTenders(_ context.Context, _, _ string) ([]ReceiptTender, error) {
	return []ReceiptTender{{Method: "CASH", Label: "CASH", AmountCents: 13944, Ref: "PMT-000001"}}, nil
}

func (f *fakeReceiptStore) ReadBranchHeader(_ context.Context, _ string) (branchHeader, error) {
	return branchHeader{OrgName: "Dibbits Landscape Supply", Name: "Trenton", Code: "TRT",
		Address: "132 County Road 40, Trenton, ON", HSTRateBps: 1300}, nil
}

const (
	trentonID   = "5d1b3f6e-0000-4000-9000-000000000001"
	kingstonID  = "5d1b3f6e-0000-4000-9000-000000000002"
	saleOrderID = "44444444-4444-4444-8444-444444444444"
)

func receiptHarness(t *testing.T) (*ReceiptService, *fakeQueueStore) {
	t.Helper()
	qs := &fakeQueueStore{markFind: true, markUpd: true}
	store := &fakeReceiptStore{found: true, sale: receiptSale{
		OrderID: saleOrderID, OrderNo: "#SO-42", BranchID: trentonID, InvoiceNo: "#INV-9",
		Tender: "CASH", SaleType: "COLLECT_NOW", Status: "COMMITTED",
		SubtotalCents: 12340, TaxCents: 1604, TotalCents: 13944,
		SoldAt: time.Date(2026, 8, 4, 10, 30, 0, 0, time.UTC),
	}}
	return NewReceiptService(store, NewQueueService(qs)), qs
}

func staffFor(branch string) context.Context {
	return identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: "u-1", RoleCode: "INSIDE_SALES", Tier: identity.TierFrontline,
		Scope: identity.ScopeBranch, Status: identity.StatusActive, Branches: map[string]bool{branch: true},
	})
}

// DIB-410, THE GUARD. A receipt names the customer, every line they bought, and how they paid.
// A cashier scoped to Kingston pulling a Trenton sale's receipt is exactly the cross-branch read the
// branch scope exists to stop (order/edit.go authorizeEdit, verbatim shape). Removing the
// CanAccessBranch check makes this test fail.
func TestReceipt_CrossBranchIsForbidden(t *testing.T) {
	svc, _ := receiptHarness(t)
	if _, err := svc.Receipt(staffFor(kingstonID), saleOrderID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a Kingston-scoped caller must be refused a Trenton receipt, got %v", err)
	}
	// The same caller, granted the sale's branch, gets the document.
	if _, err := svc.Receipt(staffFor(trentonID), saleOrderID); err != nil {
		t.Fatalf("the sale's own branch must be readable: %v", err)
	}
}

// An unauthenticated context and a suspended staff row are both refused before any read.
func TestReceipt_RequiresActiveStaff(t *testing.T) {
	svc, _ := receiptHarness(t)
	if _, err := svc.Receipt(context.Background(), saleOrderID); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("no caller must be ErrUnauthorized, got %v", err)
	}
	suspended := identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: "u-1", Scope: identity.ScopeGlobal, Status: "SUSPENDED",
	})
	if _, err := svc.Receipt(suspended, saleOrderID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a non-ACTIVE caller must be ErrForbidden, got %v", err)
	}
}

// An id that resolves to nothing, as a uuid or as an order number, is not-found, never a 500.
func TestReceipt_UnresolvableIDIsNotFound(t *testing.T) {
	svc, _ := receiptHarness(t)
	for _, id := range []string{"not-a-uuid", "", "   ", "#SO-99999"} {
		if _, err := svc.Receipt(staffFor(trentonID), id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("id %q must be ErrNotFound, got %v", id, err)
		}
	}
}

// The visible "#SO-" number is what a cashier holds, so it must open the receipt, with or without
// the leading '#'.
func TestReceipt_ResolvesTheVisibleOrderNumber(t *testing.T) {
	svc, _ := receiptHarness(t)
	for _, id := range []string{"#SO-42", "SO-42"} {
		rec, err := svc.Receipt(staffFor(trentonID), id)
		if err != nil {
			t.Fatalf("receipt by %q: %v", id, err)
		}
		if rec.OrderNo != "#SO-42" {
			t.Fatalf("resolved the wrong order: %q", rec.OrderNo)
		}
	}
}

// DIB-410, the oracle guard. A uuid is unguessable, so a cross-branch uuid is answered 403. An
// "#SO-4" is SEQUENTIAL: answering 403 for it would confirm "branch X has a sale numbered N" to
// anyone who counts. The "#SO-" path therefore resolves branch-scoped and answers not-found.
func TestReceipt_CrossBranchOrderNoIsNotFound(t *testing.T) {
	svc, _ := receiptHarness(t)
	_, err := svc.Receipt(staffFor(kingstonID), "#SO-42")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("a cross-branch #SO number must be not-found (no existence oracle), got %v", err)
	}
	// The same sale by uuid is the ruled 403, the caller proved they know the id already.
	if _, err := svc.Receipt(staffFor(kingstonID), saleOrderID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a cross-branch uuid must be ErrForbidden, got %v", err)
	}
}

// DIB-410/411, every fetch records a print, and the marker is driven by the COUNT of prior prints.
func TestReceipt_EnqueuesAndMarksPrinted(t *testing.T) {
	svc, qs := receiptHarness(t)
	rec, err := svc.Receipt(staffFor(trentonID), saleOrderID)
	if err != nil {
		t.Fatalf("receipt: %v", err)
	}
	if rec.ReprintCount != 0 || rec.IsReprint() {
		t.Fatalf("the first fetch is the original, got ReprintCount=%d", rec.ReprintCount)
	}
	if len(qs.jobs) != 1 || qs.jobs[0].DocType != DocReceipt || qs.jobs[0].RefID != saleOrderID {
		t.Fatalf("the fetch must enqueue exactly one RECEIPT job for the order: %+v", qs.jobs)
	}
	if qs.jobs[0].RenderTarget != TargetBrowser {
		t.Fatalf("a counter receipt renders in the BROWSER, got %q", qs.jobs[0].RenderTarget)
	}

	// A prior PRINTED row is what makes the next fetch a reprint.
	qs.printed = 2
	rec, err = svc.Receipt(staffFor(trentonID), saleOrderID)
	if err != nil {
		t.Fatalf("reprint: %v", err)
	}
	if !rec.IsReprint() || rec.ReprintCount != 2 {
		t.Fatalf("a sale printed twice before must reprint as #2, got %+v", rec.ReprintCount)
	}
	html, err := RenderReceiptHTML(rec)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(html, "REPRINT #2") {
		t.Fatal("the REPRINT marker must be on the page, it is a control, not decoration")
	}
}

// A sale the store cannot serve (uncommitted, void, or simply absent) is not-found, not an empty page.
func TestReceipt_UnknownSaleIsNotFound(t *testing.T) {
	svc, _ := receiptHarness(t)
	svc.store.(*fakeReceiptStore).found = false
	if _, err := svc.Receipt(staffFor(trentonID), saleOrderID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unservable sale must be ErrNotFound, got %v", err)
	}
}

func TestHSTLabel_ReadsTheConfiguredRate(t *testing.T) {
	for _, tc := range []struct {
		bps  int
		want string
	}{
		{1300, "HST (13%)"}, {1500, "HST (15%)"}, {1275, "HST (12.75%)"}, {0, "HST"},
	} {
		if got := hstLabel(tc.bps); got != tc.want {
			t.Errorf("hstLabel(%d) = %q, want %q", tc.bps, got, tc.want)
		}
	}
}

// THE COMMITTED SALE CARRIES THE CASH FACTS (row 666 item 1). The change due is not recomputed
// from the tender rows: the order's own cash-tendered figure is the fact, and the change due is the
// core's one derivation from it (tendered minus total, the ring's own arithmetic, pos
// repository.go). A sale with no cash tendered carries no change due at all.
func TestReceipt_ChangeDueFromTheCommittedSale(t *testing.T) {
	svc, _ := receiptHarness(t)
	store := svc.store.(*fakeReceiptStore)
	store.sale.CashTenderedCents = 15000

	rec, err := svc.Receipt(staffFor(trentonID), saleOrderID)
	if err != nil {
		t.Fatalf("receipt: %v", err)
	}
	if rec.ChangeDueCents != 1056 {
		t.Fatalf("tendered 15000 against total 13944 must read change due 1056 from the committed sale, got %d", rec.ChangeDueCents)
	}

	store.sale.CashTenderedCents = 0
	rec, err = svc.Receipt(staffFor(trentonID), saleOrderID)
	if err != nil {
		t.Fatalf("receipt: %v", err)
	}
	if rec.ChangeDueCents != 0 {
		t.Fatalf("a sale with no cash tendered must carry no change due, got %d", rec.ChangeDueCents)
	}
}

// THE DEPOSIT IS THE RING'S OWN MONEY (row 666 item 1). An unpaid-at-ring sale taken with a
// deposit carries the deposit the ring itself captured, read from the committed sale beside the
// invoice's balance: the paper states the deposit and the balance that remains. A settled sale
// (balance zero) and an unsettled account sale (nothing captured at ring) carry no deposit,
// whatever else their tender rows say.
func TestReceipt_DepositFromTheCommittedSale(t *testing.T) {
	svc, _ := receiptHarness(t)
	store := svc.store.(*fakeReceiptStore)
	store.sale.BalanceCents = 6000
	store.sale.DepositCents = 4000

	rec, err := svc.Receipt(staffFor(trentonID), saleOrderID)
	if err != nil {
		t.Fatalf("receipt: %v", err)
	}
	if rec.DepositCents != 4000 {
		t.Fatalf("a deposit sale must read its deposit from the committed sale, got %d", rec.DepositCents)
	}
	if rec.BalanceCents != 6000 {
		t.Fatalf("the balance rides the invoice unchanged, got %d", rec.BalanceCents)
	}

	store.sale.BalanceCents = 0
	rec, err = svc.Receipt(staffFor(trentonID), saleOrderID)
	if err != nil {
		t.Fatalf("receipt: %v", err)
	}
	if rec.DepositCents != 0 {
		t.Fatalf("a settled sale carries no deposit row, got %d", rec.DepositCents)
	}

	store.sale.BalanceCents = 6000
	store.sale.DepositCents = 0
	rec, err = svc.Receipt(staffFor(trentonID), saleOrderID)
	if err != nil {
		t.Fatalf("receipt: %v", err)
	}
	if rec.DepositCents != 0 {
		t.Fatalf("an unsettled sale with nothing captured at ring carries no deposit, got %d", rec.DepositCents)
	}
}
