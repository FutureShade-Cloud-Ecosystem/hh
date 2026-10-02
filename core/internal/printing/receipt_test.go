package printing

import (
	"context"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
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

	// Prior PRINTED rows are what make the next fetch a reprint: the fetch
	// above wrote the first one, one more planted row makes this sale's
	// history two prints, as the real store would hold them.
	qs.jobs = append(qs.jobs,
		Job{ID: "00000000-0000-4000-8000-0000000000a1", DocType: DocReceipt, RefID: saleOrderID, Status: StatusPrinted},
	)
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
	// The one shape the word Deposit names: a plain ACCOUNT ring taken with a deposit.
	store.sale.Tender = pos.TenderAccount
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

	store.sale.SaleType = pos.SaleCOD
	store.sale.BalanceCents = 6000
	store.sale.DepositCents = 4000
	rec, err = svc.Receipt(staffFor(trentonID), saleOrderID)
	if err != nil {
		t.Fatalf("receipt: %v", err)
	}
	if rec.DepositCents != 4000 {
		t.Fatalf("a COD deposit sale reads its deposit the same way, got %d", rec.DepositCents)
	}

	store.sale.SaleType = pos.SaleCollectNow
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

// THE FORMAT PARAMETER (row 666 item 2): the route's one optional query value. Empty and roll name
// the 80 mm document, page names the full-page document, and anything else is a client error naming
// the value (the doc_types refusal's shape), never a silent fallthrough to a document the caller
// did not ask for.
func TestParseReceiptFormat(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want string
	}{
		{"", ReceiptFormatRoll},
		{"roll", ReceiptFormatRoll},
		{"page", ReceiptFormatPage},
	} {
		got, err := parseReceiptFormat(tc.raw)
		if err != nil {
			t.Fatalf("parseReceiptFormat(%q): %v", tc.raw, err)
		}
		if got != tc.want {
			t.Fatalf("parseReceiptFormat(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
	_, err := parseReceiptFormat("poster")
	if !errors.As(err, new(httpx.BadRequestError)) {
		t.Fatalf("an unknown format must be a bad request naming the value, got %v", err)
	}
	if !strings.Contains(err.Error(), "poster") {
		t.Fatalf("the refusal must name the value, got %v", err)
	}
}

// THE REPRINT FROM THE ORDER (row 666 item 3): the past-sale reprint is fetched by the ORDER (its
// id or its visible number: the More menu's box takes either), and it is MARKED: the count of prior
// prints is keyed on the order, so the same sale fetched first by id and then by number reads the
// same count and renders the ordinal. An unmarked duplicate is how a return gets run twice.
func TestReceipt_ReprintByTheOrderCarriesTheOrdinal(t *testing.T) {
	svc, _ := receiptHarness(t)

	// The customer's first copy, fetched by the order id.
	first, err := svc.Receipt(staffFor(trentonID), saleOrderID)
	if err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	if first.IsReprint() {
		t.Fatal("the first fetch is the original")
	}

	// The past-sale reprint, fetched by the VISIBLE number: marked, with its ordinal.
	reprint, err := svc.Receipt(staffFor(trentonID), "#SO-42")
	if err != nil {
		t.Fatalf("reprint by the order number: %v", err)
	}
	if !reprint.IsReprint() || reprint.ReprintCount != 1 {
		t.Fatalf("the reprint must be marked #1, got count %d", reprint.ReprintCount)
	}
	html, err := RenderReceiptHTML(reprint)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(html, "REPRINT #1") {
		t.Fatal("the marker must be on the paper")
	}
}

// THE DEPOSIT IS THE PLAIN UNPAID-AT-RING RING'S OWN (the review's P2-1). A split settles the
// sale itself: pos refuses a deposit on one, so a split with an account leg that leaves captured
// money in the ring's transaction and a balance behind it prints its legs as tenders and its
// remainder as the balance, and NO Deposit row: the paper must not name a deposit the ring never
// took. The same holds for a split settled across card legs only (nothing captured would be a
// lie twice over).
func TestReceipt_NoDepositOnASplit(t *testing.T) {
	svc, _ := receiptHarness(t)
	store := svc.store.(*fakeReceiptStore)

	// The reviewer's shape: a split of cash 3000 and account for the rest, a balance left.
	store.sale.SplitTender = true
	store.sale.BalanceCents = 21950
	store.sale.DepositCents = 3000

	rec, err := svc.Receipt(staffFor(trentonID), saleOrderID)
	if err != nil {
		t.Fatalf("receipt: %v", err)
	}
	if rec.DepositCents != 0 {
		t.Fatalf("a split's captured legs must never print as a Deposit, got %d", rec.DepositCents)
	}
	if rec.BalanceCents != 21950 {
		t.Fatalf("the split's remainder is still the invoice's balance, got %d", rec.BalanceCents)
	}
	html, err := RenderReceiptHTML(rec)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(html, "Deposit") {
		t.Fatal("the paper must carry no Deposit row for a split")
	}
	if !strings.Contains(html, "Balance due") {
		t.Fatal("the split's balance due row stays on the paper")
	}

	// The plain deposit sale beside it: the gate stays open for the one shape the word names.
	store.sale.SplitTender = false
	store.sale.Tender = pos.TenderAccount
	store.sale.SaleType = pos.SaleCollectNow
	rec, err = svc.Receipt(staffFor(trentonID), saleOrderID)
	if err != nil {
		t.Fatalf("receipt: %v", err)
	}
	if rec.DepositCents != 3000 {
		t.Fatalf("the plain deposit sale must still read its deposit, got %d", rec.DepositCents)
	}
}

// THE REFUSAL PRECEDES THE RECORD (the review's P2-2): the handler parses the format BEFORE it
// reads the sale, so a refused format burns no reprint ordinal and writes no print row and no
// audit row for a paper nobody received. The marker is a control; an ordinal spent on a 400 is a
// control lying. The pin drives the handler itself over the fakes: the refused request answers
// 400 with zero jobs recorded, and the next fetch is the original.
func TestReceiptHandler_RefusedFormatRecordsNoPrint(t *testing.T) {
	svc, qs := receiptHarness(t)
	h := NewHandler(svc, NewQueueService(qs), nil)

	serve := func(rawQuery string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/api/v1/pos/sales/"+saleOrderID+"/receipt"+rawQuery, nil)
		req.SetPathValue("id", saleOrderID)
		req = req.WithContext(identity.ContextWithStaff(req.Context(), identity.StaffContext{
			UserID: "u-1", RoleCode: "INSIDE_SALES", Tier: identity.TierFrontline,
			Scope: identity.ScopeBranch, Status: identity.StatusActive, Branches: map[string]bool{trentonID: true},
		}))
		rr := httptest.NewRecorder()
		h.receipt(rr, req, guards.StaffCaller{})
		return rr
	}

	refused := serve("?format=poster")
	if refused.Code != http.StatusBadRequest {
		t.Fatalf("an unknown format must answer 400, got %d", refused.Code)
	}
	if len(qs.jobs) != 0 {
		t.Fatalf("a refused format must record no print, got %d job(s)", len(qs.jobs))
	}

	served := serve("?format=page")
	if served.Code != http.StatusOK {
		t.Fatalf("the page format must serve, got %d", served.Code)
	}
	if len(qs.jobs) != 1 {
		t.Fatalf("the served paper records its one print, got %d", len(qs.jobs))
	}

	again := serve("")
	if again.Code != http.StatusOK {
		t.Fatalf("the default roll must serve, got %d", again.Code)
	}
	html := again.Body.String()
	if !strings.Contains(html, "REPRINT #1") {
		t.Fatal("the next fetch is the reprint the one served print earned, no ordinal burned on the refusal")
	}
}

// THE WALK-IN ACCOUNT'S SCRUB PLACEHOLDER NEVER REACHES THE PAPER (the visual read's G3): the
// house's canonical walk-in account (0029's seed, the row pos's ResolveWalkIn resolves by) carries
// a data-protection placeholder for a name, and a customer holding the paper must read the
// counter's word, not a privacy artifact. The receipt yields the walk-in account's name to the
// template's own walk-in word.
func TestReceipt_TheWalkInAccountPrintsTheWalkInWord(t *testing.T) {
	svc, _ := receiptHarness(t)
	store := svc.store.(*fakeReceiptStore)
	store.sale.CustomerAcct = "WALK-IN"
	store.sale.CustomerName = "Scrubbed Party 0009"

	rec, err := svc.Receipt(staffFor(trentonID), saleOrderID)
	if err != nil {
		t.Fatalf("receipt: %v", err)
	}
	if rec.CustomerName != "" {
		t.Fatalf("the walk-in account's placeholder name must not ride the paper, got %q", rec.CustomerName)
	}
	html, err := RenderReceiptHTML(rec)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(html, "Customer: Walk-in (WALK-IN)") {
		t.Fatal("the walk-in sale must print the counter's walk-in word beside the walk-in account")
	}
	if strings.Contains(html, "Scrubbed Party") {
		t.Fatal("the scrub placeholder must never reach the paper")
	}
}
