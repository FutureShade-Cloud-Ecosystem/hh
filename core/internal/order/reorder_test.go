package order

// reorder_test.go, CP-04 service tests (DB-free, mirrors service_test.go): US-RO-01
// repriced-at-today's-rates (never the historical price_each), US-RO-02 partial
// selection, US-RO-03 flagged + rejected unavailable lines, US-RO-04 IDOR-safe 404.

import (
	"context"
	"errors"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
)

// fakeReorderStore implements orderStore + the reorderStore assertion, enforcing tenant
// scope exactly like the pgx Repository (a foreign order reads as ErrNotFound).
type fakeReorderStore struct {
	fakeStore
	sources map[string]ReorderSource // orderID -> source (owner via owners map)
	owners  map[string]string        // orderID -> customerID
	created *ReorderDraft            // recorded write
	result  ReorderResult
}

func newFakeReorderStore() *fakeReorderStore {
	return &fakeReorderStore{
		fakeStore: fakeStore{details: map[string]Detail{}},
		sources:   map[string]ReorderSource{},
		owners:    map[string]string{},
		result:    ReorderResult{OrderID: "new-order-id", OrderNo: "#SO-99", Status: "DRAFT"},
	}
}

func (f *fakeReorderStore) ReorderSource(_ context.Context, customerID, orderID string) (ReorderSource, error) {
	src, ok := f.sources[orderID]
	if !ok || f.owners[orderID] != customerID {
		return ReorderSource{}, ErrNotFound
	}
	return src, nil
}

func (f *fakeReorderStore) CreateReorderDraft(_ context.Context, d ReorderDraft) (ReorderResult, error) {
	f.created = &d
	return f.result, nil
}

// fakePricer returns a fixed today's price per product (cents per uom), recording the
// inputs so a test can prove the resolver was consulted with the right facts.
type fakePricer struct {
	prices  map[string]int64 // productID -> unit price cents
	blocked map[string]bool
	failing map[string]bool
	calls   []pricing.ResolveInput
}

func (p *fakePricer) ResolvePrice(_ context.Context, in pricing.ResolveInput) (pricing.Breakdown, error) {
	p.calls = append(p.calls, in)
	if p.failing[in.ProductID] {
		return pricing.Breakdown{}, errors.New("no anchor")
	}
	unit := p.prices[in.ProductID]
	return pricing.Breakdown{
		FinalCents:     unit,
		PriceUOM:       in.UOMEntered,
		QtyBase:        in.QtyEntered,
		LineTotalCents: int64(in.QtyEntered * float64(unit)),
		Blocked:        p.blocked[in.ProductID],
	}, nil
}

// fakeInventory grades availability from canned verdicts (default: available).
type fakeInventory struct {
	unavailable map[string]string // productID -> reason
	cost        map[string]int64
}

func (i *fakeInventory) CheckAvailability(_ context.Context, productID, _ string, _ float64) (bool, string, error) {
	if reason, bad := i.unavailable[productID]; bad {
		return false, reason, nil
	}
	return true, "", nil
}

func (i *fakeInventory) CurrentCostCents(_ context.Context, productID, _ string) (int64, bool, error) {
	c, ok := i.cost[productID]
	return c, ok, nil
}

// twoLineSource is a past order with two priced lines (historical prices 555 / 1200
// cents, the prices a correct reorder must NOT copy).
func twoLineSource() ReorderSource {
	return ReorderSource{
		OrderID: "ord-1", OrderNo: "#SO-1", BranchID: "branch-1", Fulfillment: "PICKUP",
		Lines: []ReorderSourceLine{
			{LineNo: 1, ProductID: "prod-a", Description: "Paver A", QtyEntered: "10", UOMEntered: "EA", QtyBase: "10"},
			{LineNo: 2, ProductID: "prod-b", Description: "Gravel B", QtyEntered: "2.5", UOMEntered: "TON", QtyBase: "2.5"},
		},
	}
}

func reorderHarness(t *testing.T) (*Service, *fakeReorderStore, *fakePricer, *fakeInventory) {
	t.Helper()
	store := newFakeReorderStore()
	store.sources["ord-1"] = twoLineSource()
	store.owners["ord-1"] = "cust-1"
	pricer := &fakePricer{
		prices:  map[string]int64{"prod-a": 999, "prod-b": 2000},
		blocked: map[string]bool{},
		failing: map[string]bool{},
	}
	inv := &fakeInventory{unavailable: map[string]string{}, cost: map[string]int64{"prod-a": 500, "prod-b": 900}}
	svc := NewService(store).WithReorderPricing(pricer).WithReorderInventory(inv)
	return svc, store, pricer, inv
}

var rc = ReorderContext{CustomerType: "CONTRACTOR"}

// US-RO-01: the preview + the created draft carry TODAY'S resolved prices, the source
// order's historical unit prices never leak onto either.
func TestReorder_RepricesAtTodaysRates(t *testing.T) {
	svc, store, pricer, _ := reorderHarness(t)
	ctx := context.Background()

	prev, err := svc.PreviewReorder(ctx, "cust-1", "ord-1", rc)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(prev.Lines) != 2 {
		t.Fatalf("preview lines = %d, want 2", len(prev.Lines))
	}
	if prev.Lines[0].UnitPriceCents != 999 || prev.Lines[1].UnitPriceCents != 2000 {
		t.Fatalf("preview prices = %d/%d, want today's 999/2000",
			prev.Lines[0].UnitPriceCents, prev.Lines[1].UnitPriceCents)
	}
	// The resolver must be consulted with the caller's pricing facts + cost basis.
	if len(pricer.calls) != 2 {
		t.Fatalf("resolver consulted %d times, want 2", len(pricer.calls))
	}
	if in := pricer.calls[0]; in.CustomerID != "cust-1" || in.CustomerType != "CONTRACTOR" ||
		in.BranchID != "branch-1" || in.CostCents != 500 || in.QtyEntered != 10 || in.UOMEntered != "EA" {
		t.Fatalf("resolver input = %+v, want caller-scoped facts", in)
	}

	if _, err := svc.CreateReorder(ctx, "cust-1", "ord-1", rc, []int{1, 2}, "key-1"); err != nil {
		t.Fatalf("create: %v", err)
	}
	d := store.created
	if d == nil {
		t.Fatal("draft not written")
	}
	if d.Lines[0].UnitPriceCents != 999 || d.Lines[1].UnitPriceCents != 2000 {
		t.Fatalf("draft prices = %d/%d, want today's 999/2000 (historical price_each must never be copied, defect D1)",
			d.Lines[0].UnitPriceCents, d.Lines[1].UnitPriceCents)
	}
	wantSubtotal := int64(10*999) + int64(2.5*2000)
	if d.SubtotalCents != wantSubtotal {
		t.Fatalf("draft subtotal = %d, want %d", d.SubtotalCents, wantSubtotal)
	}
	if d.CustomerID != "cust-1" || d.BranchID != "branch-1" || d.SourceOrderID != "ord-1" || d.IdempotencyKey != "key-1" {
		t.Fatalf("draft header = %+v, want same customer/branch + provenance", d)
	}
}

// US-RO-02: a partial selection lands ONLY the chosen lines, renumbered from 1.
func TestReorder_PartialSelection(t *testing.T) {
	svc, store, _, _ := reorderHarness(t)

	if _, err := svc.CreateReorder(context.Background(), "cust-1", "ord-1", rc, []int{2}, "key-2"); err != nil {
		t.Fatalf("create: %v", err)
	}
	d := store.created
	if len(d.Lines) != 1 {
		t.Fatalf("draft lines = %d, want 1", len(d.Lines))
	}
	if d.Lines[0].ProductID != "prod-b" || d.Lines[0].LineNo != 1 {
		t.Fatalf("draft line = %+v, want prod-b renumbered to line 1", d.Lines[0])
	}
	if d.SubtotalCents != int64(2.5*2000) {
		t.Fatalf("draft subtotal = %d, want %d", d.SubtotalCents, int64(2.5*2000))
	}
}

// A selection referencing a line not on the order is a 400-shaped error, not a 409.
func TestReorder_UnknownLineIs400(t *testing.T) {
	svc, _, _, _ := reorderHarness(t)
	_, err := svc.CreateReorder(context.Background(), "cust-1", "ord-1", rc, []int{7}, "key-3")
	var bre httpx.BadRequestError
	if !errors.As(err, &bre) {
		t.Fatalf("unknown line err = %v, want BadRequestError", err)
	}
}

// US-RO-03: discontinued / out-of-stock lines are FLAGGED in the preview…
func TestReorder_PreviewFlagsUnavailable(t *testing.T) {
	svc, _, pricer, inv := reorderHarness(t)
	inv.unavailable["prod-a"] = "DISCONTINUED"

	prev, err := svc.PreviewReorder(context.Background(), "cust-1", "ord-1", rc)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	a, b := prev.Lines[0], prev.Lines[1]
	if a.Available || a.Reason != "DISCONTINUED" {
		t.Fatalf("flagged line = %+v, want unavailable DISCONTINUED", a)
	}
	if a.UnitPriceCents != 0 {
		t.Fatalf("unavailable line carries a price (%d), must be zero", a.UnitPriceCents)
	}
	if !b.Available || b.Reason != "" {
		t.Fatalf("healthy line = %+v, want available", b)
	}
	// The flagged line is never priced (no resolver call for prod-a).
	for _, c := range pricer.calls {
		if c.ProductID == "prod-a" {
			t.Fatal("resolver consulted for an unavailable line")
		}
	}
}

// …and REJECTED on submit, with the per-line reasons.
func TestReorder_SubmitUnavailableIsRejected(t *testing.T) {
	svc, store, _, inv := reorderHarness(t)
	inv.unavailable["prod-b"] = "OUT_OF_STOCK"

	_, err := svc.CreateReorder(context.Background(), "cust-1", "ord-1", rc, []int{1, 2}, "key-4")
	var ua *ReorderUnavailableError
	if !errors.As(err, &ua) {
		t.Fatalf("submit err = %v, want ReorderUnavailableError", err)
	}
	if len(ua.Lines) != 1 || ua.Lines[0].LineNo != 2 || ua.Lines[0].Reason != "OUT_OF_STOCK" {
		t.Fatalf("rejections = %+v, want line 2 OUT_OF_STOCK", ua.Lines)
	}
	if store.created != nil {
		t.Fatal("a rejected submit must not write a draft")
	}
	// Deselecting the dead line succeeds.
	if _, err := svc.CreateReorder(context.Background(), "cust-1", "ord-1", rc, []int{1}, "key-4"); err != nil {
		t.Fatalf("resubmit without the dead line: %v", err)
	}
}

// A margin-blocked price and an unpriceable line are also flagged, never committed.
func TestReorder_BlockedAndUnpriceableAreFlagged(t *testing.T) {
	svc, _, pricer, _ := reorderHarness(t)
	pricer.blocked["prod-a"] = true
	pricer.failing["prod-b"] = true

	prev, err := svc.PreviewReorder(context.Background(), "cust-1", "ord-1", rc)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if prev.Lines[0].Available || prev.Lines[0].Reason != ReorderReasonPriceBlocked {
		t.Fatalf("blocked line = %+v, want %s", prev.Lines[0], ReorderReasonPriceBlocked)
	}
	if prev.Lines[1].Available || prev.Lines[1].Reason != ReorderReasonPriceUnavailable {
		t.Fatalf("unpriceable line = %+v, want %s", prev.Lines[1], ReorderReasonPriceUnavailable)
	}
}

// A free-text (no product) line is flagged NOT_A_CATALOG_ITEM.
func TestReorder_FreeTextLineFlagged(t *testing.T) {
	svc, store, _, _ := reorderHarness(t)
	src := store.sources["ord-1"]
	src.Lines = append(src.Lines, ReorderSourceLine{
		LineNo: 3, Description: "Custom delivery fee", QtyEntered: "1", UOMEntered: "EA", QtyBase: "1",
	})
	store.sources["ord-1"] = src

	prev, err := svc.PreviewReorder(context.Background(), "cust-1", "ord-1", rc)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if prev.Lines[2].Available || prev.Lines[2].Reason != ReorderReasonNoCatalogItem {
		t.Fatalf("free-text line = %+v, want %s", prev.Lines[2], ReorderReasonNoCatalogItem)
	}
}

// US-RO-04: a foreign (or missing) order id answers ErrNotFound for BOTH surfaces -
// never a forbidden that would confirm the id exists.
func TestReorder_IDORSafeNotFound(t *testing.T) {
	svc, _, _, _ := reorderHarness(t)
	ctx := context.Background()

	if _, err := svc.PreviewReorder(ctx, "cust-EVIL", "ord-1", rc); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant preview err = %v, want ErrNotFound", err)
	}
	if _, err := svc.CreateReorder(ctx, "cust-EVIL", "ord-1", rc, []int{1}, "key-5"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant create err = %v, want ErrNotFound", err)
	}
	if _, err := svc.PreviewReorder(ctx, "cust-1", "no-such-order", rc); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing order err = %v, want ErrNotFound", err)
	}
}

// Input validation: missing idempotency key / empty selection are 400-shaped.
func TestReorder_Validation(t *testing.T) {
	svc, _, _, _ := reorderHarness(t)
	ctx := context.Background()
	var bre httpx.BadRequestError

	if _, err := svc.CreateReorder(ctx, "cust-1", "ord-1", rc, []int{1}, ""); !errors.As(err, &bre) {
		t.Fatalf("missing idempotency_key err = %v, want BadRequestError", err)
	}
	if _, err := svc.CreateReorder(ctx, "cust-1", "ord-1", rc, nil, "k"); !errors.As(err, &bre) {
		t.Fatalf("empty selection err = %v, want BadRequestError", err)
	}
}

// Unconfigured seams answer ErrReorderNotConfigured (the portal maps it to 501).
func TestReorder_UnconfiguredSeamsAre501Shaped(t *testing.T) {
	svc := NewService(newFakeReorderStore()) // no pricer / inventory injected
	if _, err := svc.PreviewReorder(context.Background(), "c", "o", rc); !errors.Is(err, ErrReorderNotConfigured) {
		t.Fatalf("err = %v, want ErrReorderNotConfigured", err)
	}
}

// DIB-159 REGRESSION (MAJOR-2): a reorder is a creation writer, the order is INSERTed at DRAFT, so its
// per-order status timeline must carry the ""→DRAFT creation transition. Before this fix the reorder
// path emitted only ORDER_CHANGED (a field-change action OrderHistory filters out), so a reorder draft's
// history was silently empty even though the order existed at DRAFT. Assert exactly one
// ORDER_STATUS_CHANGED with ""→DRAFT.
func TestReorder_EmitsCreationStatusTransition(t *testing.T) {
	store := newFakeReorderStore()
	store.sources["ord-1"] = twoLineSource()
	store.owners["ord-1"] = "cust-1"
	pricer := &fakePricer{prices: map[string]int64{"prod-a": 999, "prod-b": 2000}, blocked: map[string]bool{}, failing: map[string]bool{}}
	inv := &fakeInventory{unavailable: map[string]string{}, cost: map[string]int64{"prod-a": 500, "prod-b": 900}}
	rec := &recordingRecorder{}
	svc := NewService(store).WithReorderPricing(pricer).WithReorderInventory(inv).WithAudit(rec)

	res, err := svc.CreateReorder(context.Background(), "cust-1", "ord-1", rc, []int{1, 2}, "key-reorder-hist")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := rec.countAction(audit.ActOrderStatusChanged); got != 1 {
		t.Fatalf("reorder emitted %d ORDER_STATUS_CHANGED rows, want exactly 1 (creation ->DRAFT): %+v", got, rec.records)
	}
	var sc audit.SensitiveAccess
	for _, a := range rec.records {
		if a.Action == audit.ActOrderStatusChanged {
			sc = a
			break
		}
	}
	if sc.EntityType != "order" || sc.EntityID != res.OrderID {
		t.Errorf("status row entity = %s/%s, want order/%s", sc.EntityType, sc.EntityID, res.OrderID)
	}
	if sc.Extra["old_status"] != "" || sc.Extra["new_status"] != "DRAFT" {
		t.Errorf("from→to = %v→%v, want →DRAFT", sc.Extra["old_status"], sc.Extra["new_status"])
	}
}
