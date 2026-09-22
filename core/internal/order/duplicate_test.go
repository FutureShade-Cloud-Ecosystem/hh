package order

// duplicate_test.go, DIB-489 staff order duplication + DIB-490 duplicate-order detection, DB-free
// against the package's fakes (the reorder_test.go / edit_test.go approach): the staff authority
// ladder, repriced-at-today's-rates with per-line quantity overrides, the flag-and-reject vocabulary,
// provenance + attribution on the write, and the material-overlap heuristic with its provenance
// exclusions applied by the service.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// - fakes -

// fakeDupInv grades availability from canned verdicts and RECORDS the base quantity each check was
// asked about, the fact the qty-override test needs (the check must run at the ADJUSTED base).
type fakeDupInv struct {
	unavailable map[string]string  // productID -> reason
	cost        map[string]int64   // productID -> cost basis cents
	checkedBase map[string]float64 // productID -> last base qty checked
}

func (i *fakeDupInv) CheckAvailability(_ context.Context, productID, _ string, qtyBase float64) (bool, string, error) {
	if i.checkedBase == nil {
		i.checkedBase = map[string]float64{}
	}
	i.checkedBase[productID] = qtyBase
	if reason, bad := i.unavailable[productID]; bad {
		return false, reason, nil
	}
	return true, "", nil
}

func (i *fakeDupInv) CurrentCostCents(_ context.Context, productID, _ string) (int64, bool, error) {
	c, ok := i.cost[productID]
	return c, ok, nil
}

// fakeDuplicateStore layers the duplicate persistence over fakeEditStore (which supplies the full
// orderStore + editStore behaviour the authority ladder and pricing context need), so every store
// assertion in the code under test succeeds against ONE fake.
type fakeDuplicateStore struct {
	*fakeEditStore
	sources map[string]ReorderSource
	owners  map[string]string // orderID -> customerID (tenant scope, like the pgx ReorderSource)

	created     *ReorderDraft
	createdBy   string
	createCalls int
	result      ReorderResult

	cands         []DuplicateCandidate
	draftProducts int
	lastWindow    time.Duration
	lastLimit     int
	checkCalls    int
}

func newFakeDuplicateStore(status string) *fakeDuplicateStore {
	return &fakeDuplicateStore{
		fakeEditStore: newFakeEditStore(status),
		sources:       map[string]ReorderSource{},
		owners:        map[string]string{},
		result:        ReorderResult{OrderID: "dup-order-id", OrderNo: "#SO-77", Status: "DRAFT"},
	}
}

func (f *fakeDuplicateStore) ReorderSource(_ context.Context, customerID, orderID string) (ReorderSource, error) {
	src, ok := f.sources[orderID]
	if !ok || f.owners[orderID] != customerID {
		return ReorderSource{}, ErrNotFound
	}
	return src, nil
}

func (f *fakeDuplicateStore) CreateReorderDraft(_ context.Context, d ReorderDraft) (ReorderResult, error) {
	return ReorderResult{}, errors.New("the STAFF duplicate must write through CreateDuplicateDraft, never the portal writer")
}

func (f *fakeDuplicateStore) CreateDuplicateDraft(_ context.Context, d ReorderDraft, createdBy string) (ReorderResult, error) {
	f.createCalls++
	f.created = &d
	f.createdBy = createdBy
	return f.result, nil
}

func (f *fakeDuplicateStore) SimilarRecentOrders(_ context.Context, _ string, window time.Duration, limit int) ([]DuplicateCandidate, int, error) {
	f.checkCalls++
	f.lastWindow = window
	f.lastLimit = limit
	return f.cands, f.draftProducts, nil
}

// dupSource is the source order under test: two catalogue lines with HISTORICAL prices a correct
// duplicate must never copy (the reorder_test.go convention), owned by edCust at edBranch.
func dupSource() ReorderSource {
	return ReorderSource{
		OrderID: edOrder, OrderNo: "#SO-1", BranchID: edBranch, Fulfillment: "DELIVERY",
		Lines: []ReorderSourceLine{
			{LineNo: 1, ProductID: "prod-a", Description: "Paver A", QtyEntered: "10", UOMEntered: "EA", QtyBase: "10"},
			{LineNo: 2, ProductID: "prod-b", Description: "Slab B", QtyEntered: "2", UOMEntered: "PLT", QtyBase: "120"},
		},
	}
}

// dupHarness wires a service whose caller resolves from the context (ctxAs), with a working pricing
// resolver (today's rates: prod-a 999¢, prod-b 2000¢) and permissive availability.
func dupHarness(t *testing.T) (*Service, *fakeDuplicateStore, *fakePricer, *fakeDupInv, *recordingRecorder) {
	t.Helper()
	store := newFakeDuplicateStore("FULFILLED")
	store.sources[edOrder] = dupSource()
	store.owners[edOrder] = edCust
	pricer := &fakePricer{
		prices:  map[string]int64{"prod-a": 999, "prod-b": 2000},
		blocked: map[string]bool{},
		failing: map[string]bool{},
	}
	inv := &fakeDupInv{unavailable: map[string]string{}, cost: map[string]int64{"prod-a": 500, "prod-b": 900}}
	rec := &recordingRecorder{}
	svc := NewService(store).withCaller(testCallerFrom).WithAudit(rec).
		WithReorderPricing(pricer).WithReorderInventory(inv).WithCustomerType(fakeCustType{})
	return svc, store, pricer, inv, rec
}

func dupCtx() context.Context { return ctxAs(staffAt(edUser, edBranch)) }

func dupPick(ns ...int) []DuplicateSelection {
	out := make([]DuplicateSelection, 0, len(ns))
	for _, n := range ns {
		out = append(out, DuplicateSelection{LineNo: n})
	}
	return out
}

// - DIB-489: create -

// The created draft is repriced at TODAY'S rates (the historical price is never copied), records the
// source order as provenance, is attributed to the staff caller, and renumbers from 1.
func TestDuplicate_CreatesDraftRepricedToday(t *testing.T) {
	svc, store, _, _, _ := dupHarness(t)

	res, err := svc.CreateDuplicate(dupCtx(), edOrder, dupPick(1, 2), "dup-key-1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if res.OrderID != "dup-order-id" || res.Status != "DRAFT" {
		t.Fatalf("result = %+v, want the store's DRAFT", res)
	}
	d := store.created
	if d == nil {
		t.Fatal("draft not written")
	}
	if d.Lines[0].UnitPriceCents != 999 || d.Lines[1].UnitPriceCents != 2000 {
		t.Fatalf("draft prices = %d/%d, want today's 999/2000 (historical price_each must never be copied)",
			d.Lines[0].UnitPriceCents, d.Lines[1].UnitPriceCents)
	}
	wantSubtotal := int64(10*999) + int64(2*2000)
	if d.SubtotalCents != wantSubtotal {
		t.Fatalf("draft subtotal = %d, want %d", d.SubtotalCents, wantSubtotal)
	}
	if d.CustomerID != edCust || d.BranchID != edBranch || d.Fulfillment != "DELIVERY" {
		t.Fatalf("draft header = %+v, want the source's customer/branch/fulfillment", d)
	}
	if d.SourceOrderID != edOrder {
		t.Fatalf("provenance = %q, want the source order id %q", d.SourceOrderID, edOrder)
	}
	if d.IdempotencyKey != "dup-key-1" {
		t.Fatalf("idempotency key = %q, want dup-key-1", d.IdempotencyKey)
	}
	if store.createdBy != edUser {
		t.Fatalf("created_by = %q, want the staff caller %q", store.createdBy, edUser)
	}
	if d.Lines[0].LineNo != 1 || d.Lines[1].LineNo != 2 {
		t.Fatalf("line numbers = %d/%d, want renumbered 1/2", d.Lines[0].LineNo, d.Lines[1].LineNo)
	}
}

// A per-line quantity override reprices at the NEW quantity and stock-checks the ADJUSTED base
// (derived from the line's base-per-entered ratio: 2 PLT → 120 base, so 3 PLT → 180 base).
func TestDuplicate_QtyOverrideRepricesAndRechecksAtNewQty(t *testing.T) {
	svc, store, pricer, inv, _ := dupHarness(t)

	_, err := svc.CreateDuplicate(dupCtx(), edOrder,
		[]DuplicateSelection{{LineNo: 2, QtyEntered: "3"}}, "dup-key-2")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	d := store.created
	if len(d.Lines) != 1 || d.Lines[0].QtyEntered != "3" {
		t.Fatalf("draft line = %+v, want qty_entered 3", d.Lines)
	}
	if d.Lines[0].LineTotalCents != 3*2000 {
		t.Fatalf("line total = %d, want repriced at the new qty (3 × 2000)", d.Lines[0].LineTotalCents)
	}
	if got := inv.checkedBase["prod-b"]; got != 180 {
		t.Fatalf("availability checked at base %v, want the ADJUSTED base 180 (3 × 120/2)", got)
	}
	var in bool
	for _, c := range pricer.calls {
		if c.ProductID == "prod-b" && c.QtyEntered == 3 {
			in = true
		}
	}
	if !in {
		t.Fatal("the resolver was never consulted at the overridden quantity")
	}
}

// A partial selection lands ONLY the chosen lines, renumbered from 1 (the multi-truck flow: pick the
// product line, drop the delivery-fee line, create four drafts).
func TestDuplicate_PartialSelection(t *testing.T) {
	svc, store, _, _, _ := dupHarness(t)

	if _, err := svc.CreateDuplicate(dupCtx(), edOrder, dupPick(2), "dup-key-3"); err != nil {
		t.Fatalf("create: %v", err)
	}
	d := store.created
	if len(d.Lines) != 1 || d.Lines[0].ProductID != "prod-b" || d.Lines[0].LineNo != 1 {
		t.Fatalf("draft lines = %+v, want prod-b renumbered to line 1", d.Lines)
	}
	if d.SubtotalCents != 2*2000 {
		t.Fatalf("subtotal = %d, want %d", d.SubtotalCents, 2*2000)
	}
}

// A selected line that cannot be duplicated rejects the WHOLE submit with the per-line reasons -
// unavailable stock, a margin-blocked price, and a free-text line each carry their own code, and
// nothing is written.
func TestDuplicate_RejectsFlaggedSelection(t *testing.T) {
	svc, store, pricer, inv, _ := dupHarness(t)
	src := store.sources[edOrder]
	src.Lines = append(src.Lines, ReorderSourceLine{
		LineNo: 3, Description: "Custom delivery fee", QtyEntered: "1", UOMEntered: "EA", QtyBase: "1",
	})
	store.sources[edOrder] = src
	inv.unavailable["prod-a"] = "OUT_OF_STOCK"
	pricer.blocked["prod-b"] = true

	_, err := svc.CreateDuplicate(dupCtx(), edOrder, dupPick(1, 2, 3), "dup-key-4")
	var ua *ReorderUnavailableError
	if !errors.As(err, &ua) {
		t.Fatalf("submit err = %v, want ReorderUnavailableError", err)
	}
	want := map[int]string{1: "OUT_OF_STOCK", 2: ReorderReasonPriceBlocked, 3: ReorderReasonNoCatalogItem}
	if len(ua.Lines) != 3 {
		t.Fatalf("rejections = %+v, want all three flagged", ua.Lines)
	}
	for _, l := range ua.Lines {
		if want[l.LineNo] != l.Reason {
			t.Fatalf("line %d reason = %q, want %q", l.LineNo, l.Reason, want[l.LineNo])
		}
	}
	if store.created != nil {
		t.Fatal("a rejected submit must not write a draft")
	}
	// Deselecting the dead lines succeeds (one extra pick, never a dead end).
	pricer.blocked = map[string]bool{}
	if _, err := svc.CreateDuplicate(dupCtx(), edOrder, dupPick(2), "dup-key-4"); err != nil {
		t.Fatalf("resubmit without the flagged lines: %v", err)
	}
}

// An unpriceable line (resolver refusal) is a flagged rejection, not a 500, and an infrastructure
// failure from the availability seam DOES fail the call rather than silently flagging.
func TestDuplicate_UnpriceableFlaggedInfraFails(t *testing.T) {
	svc, _, pricer, _, _ := dupHarness(t)
	pricer.failing["prod-a"] = true

	_, err := svc.CreateDuplicate(dupCtx(), edOrder, dupPick(1), "dup-key-5")
	var ua *ReorderUnavailableError
	if !errors.As(err, &ua) || len(ua.Lines) != 1 || ua.Lines[0].Reason != ReorderReasonPriceUnavailable {
		t.Fatalf("err = %v, want a single %s rejection", err, ReorderReasonPriceUnavailable)
	}
}

// Input validation is 400-shaped with a walkthrough-style message: missing/oversized key, empty or
// unknown or twice-selected lines, and a malformed quantity adjustment.
func TestDuplicate_Validation(t *testing.T) {
	svc, _, _, _, _ := dupHarness(t)
	ctx := dupCtx()
	var bre httpx.BadRequestError

	if _, err := svc.CreateDuplicate(ctx, edOrder, dupPick(1), ""); !errors.As(err, &bre) {
		t.Fatalf("missing idempotency_key err = %v, want BadRequestError", err)
	}
	long := make([]byte, 129)
	for i := range long {
		long[i] = 'k'
	}
	if _, err := svc.CreateDuplicate(ctx, edOrder, dupPick(1), string(long)); !errors.As(err, &bre) {
		t.Fatalf("oversized key err = %v, want BadRequestError", err)
	}
	if _, err := svc.CreateDuplicate(ctx, edOrder, nil, "k"); !errors.As(err, &bre) {
		t.Fatalf("empty selection err = %v, want BadRequestError", err)
	}
	if _, err := svc.CreateDuplicate(ctx, edOrder, dupPick(9), "k"); !errors.As(err, &bre) {
		t.Fatalf("unknown line err = %v, want BadRequestError", err)
	}
	if _, err := svc.CreateDuplicate(ctx, edOrder, dupPick(1, 1), "k"); !errors.As(err, &bre) {
		t.Fatalf("twice-selected line err = %v, want BadRequestError", err)
	}
	if _, err := svc.CreateDuplicate(ctx, edOrder,
		[]DuplicateSelection{{LineNo: 1, QtyEntered: "-4"}}, "k"); !errors.As(err, &bre) {
		t.Fatalf("negative override err = %v, want BadRequestError", err)
	}
	if _, err := svc.CreateDuplicate(ctx, edOrder,
		[]DuplicateSelection{{LineNo: 1, QtyEntered: "a lot"}}, "k"); !errors.As(err, &bre) {
		t.Fatalf("garbage override err = %v, want BadRequestError", err)
	}
}

// The authority ladder mirrors OrderRead: unauthenticated → 401-shaped, inactive → 403, an order in
// a branch the caller cannot reach → 403, a missing order → 404 (no existence leak). Applies to all
// three duplicate surfaces through duplicateAuthorize.
func TestDuplicate_AuthorityLadder(t *testing.T) {
	svc, _, _, _, _ := dupHarness(t)

	if _, err := svc.CreateDuplicate(context.Background(), edOrder, dupPick(1), "k"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("no caller err = %v, want ErrUnauthorized", err)
	}
	inactive := staffAt(edUser, edBranch)
	inactive.Status = "DISABLED"
	if _, err := svc.CreateDuplicate(ctxAs(inactive), edOrder, dupPick(1), "k"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("inactive caller err = %v, want ErrForbidden", err)
	}
	elsewhere := staffAt(edUser, histBranchB)
	if _, err := svc.CreateDuplicate(ctxAs(elsewhere), edOrder, dupPick(1), "k"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-branch caller err = %v, want ErrForbidden", err)
	}
	if _, err := svc.PreviewDuplicate(dupCtx(), "44444444-4444-4444-8444-444444444444"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing order err = %v, want ErrNotFound", err)
	}
	if _, err := svc.CheckDuplicates(ctxAs(elsewhere), edOrder); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-branch check err = %v, want ErrForbidden", err)
	}
}

// The create emits the reorder writer's audit pair, attributed to the STAFF caller: one
// ORDER_CHANGED naming the surface + source, and the creation ""→DRAFT transition without which the
// new draft's status timeline would be empty.
func TestDuplicate_EmitsProvenanceAuditAndCreationTransition(t *testing.T) {
	svc, _, _, _, rec := dupHarness(t)

	res, err := svc.CreateDuplicate(dupCtx(), edOrder, dupPick(1), "dup-key-6")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := rec.countAction(audit.ActOrderStatusChanged); got != 1 {
		t.Fatalf("emitted %d ORDER_STATUS_CHANGED rows, want exactly 1 (creation →DRAFT)", got)
	}
	var changed, status audit.SensitiveAccess
	for _, a := range rec.records {
		switch a.Action {
		case audit.ActOrderChanged:
			changed = a
		case audit.ActOrderStatusChanged:
			status = a
		}
	}
	if changed.Purpose != "STAFF_DUPLICATE" || changed.EntityID != res.OrderID || changed.ActorID != edUser {
		t.Fatalf("ORDER_CHANGED row = %+v, want STAFF_DUPLICATE on the new draft by the caller", changed)
	}
	if changed.Extra["source_order_id"] != edOrder {
		t.Fatalf("audit provenance = %v, want the source order id", changed.Extra["source_order_id"])
	}
	if status.Extra["old_status"] != "" || status.Extra["new_status"] != "DRAFT" || status.ActorID != edUser {
		t.Fatalf("creation transition = %+v, want \"\"→DRAFT by the caller", status)
	}
}

// - DIB-489: preview -

// The preview grades every source line at today's price with availability flags, resolving the
// customer's pricing tier server-side (the resolver must see the SOURCE customer's facts, not a
// caller-supplied tier).
func TestDuplicate_PreviewGradesLinesAtTodaysRates(t *testing.T) {
	svc, _, pricer, inv, _ := dupHarness(t)
	inv.unavailable["prod-b"] = "DISCONTINUED"

	prev, err := svc.PreviewDuplicate(dupCtx(), edOrder)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if prev.SourceOrderNo != "#SO-1" || len(prev.Lines) != 2 {
		t.Fatalf("preview = %+v, want the 2-line source", prev)
	}
	if !prev.Lines[0].Available || prev.Lines[0].UnitPriceCents != 999 {
		t.Fatalf("line 1 = %+v, want available at today's 999", prev.Lines[0])
	}
	if prev.Lines[1].Available || prev.Lines[1].Reason != "DISCONTINUED" {
		t.Fatalf("line 2 = %+v, want flagged DISCONTINUED", prev.Lines[1])
	}
	if len(pricer.calls) == 0 || pricer.calls[0].CustomerType != "CONTRACTOR" || pricer.calls[0].CustomerID != edCust {
		t.Fatalf("resolver inputs = %+v, want the source customer's server-resolved tier", pricer.calls)
	}
}

// Unwired pricing/inventory/tier seams answer ErrDuplicateNotConfigured (503 at the handler) for
// both DIB-489 surfaces, never a draft at a stale or zero price.
func TestDuplicate_UnconfiguredSeams(t *testing.T) {
	store := newFakeDuplicateStore("FULFILLED")
	store.sources[edOrder] = dupSource()
	store.owners[edOrder] = edCust
	svc := NewService(store).withCaller(testCallerFrom) // no pricer / inventory / custType

	if _, err := svc.PreviewDuplicate(dupCtx(), edOrder); !errors.Is(err, ErrDuplicateNotConfigured) {
		t.Fatalf("preview err = %v, want ErrDuplicateNotConfigured", err)
	}
	if _, err := svc.CreateDuplicate(dupCtx(), edOrder, dupPick(1), "k"); !errors.Is(err, ErrDuplicateNotConfigured) {
		t.Fatalf("create err = %v, want ErrDuplicateNotConfigured", err)
	}
}

// - DIB-490: the heuristic -

// materialOverlap is the whole "materially overlapping" rule: at least half the checked order's
// distinct products (rounded up), never zero, and a product-free order never matches.
func TestMaterialOverlap(t *testing.T) {
	cases := []struct {
		shared, mine int
		want         bool
	}{
		{1, 1, true},   // the single-product twin, the classic double-key
		{1, 2, true},   // ceil(2/2) = 1
		{1, 3, false},  // ceil(3/2) = 2, one shared line of three is not material
		{2, 3, true},   //
		{2, 4, true},   // exactly half
		{1, 4, false},  // under half
		{0, 5, false},  // nothing shared
		{3, 0, false},  // a free-text-only order can never overlap
		{5, 5, true},   // identical
		{-1, 2, false}, // defensive: garbage counts never warn
	}
	for _, c := range cases {
		if got := materialOverlap(c.shared, c.mine); got != c.want {
			t.Errorf("materialOverlap(%d, %d) = %v, want %v", c.shared, c.mine, got, c.want)
		}
	}
}

// CheckDuplicates applies the material-overlap rule to the store's provenance-filtered candidates,
// reports the window it used, and answers an EMPTY list, never null, never an error, when nothing
// clears the bar (an empty answer is the ordinary outcome).
func TestDuplicateCheck_FiltersAndReportsWindow(t *testing.T) {
	svc, store, _, _, _ := dupHarness(t)
	store.draftProducts = 3
	store.cands = []DuplicateCandidate{
		{OrderID: "cand-1", OrderNo: "#SO-40", Status: "CONFIRMED", SharedProducts: 3},
		{OrderID: "cand-2", OrderNo: "#SO-41", Status: "DRAFT", SharedProducts: 2},
		{OrderID: "cand-3", OrderNo: "#SO-42", Status: "DRAFT", SharedProducts: 1}, // under ceil(3/2)=2
	}

	chk, err := svc.CheckDuplicates(dupCtx(), edOrder)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if len(chk.Candidates) != 2 || chk.Candidates[0].OrderID != "cand-1" || chk.Candidates[1].OrderID != "cand-2" {
		t.Fatalf("candidates = %+v, want cand-1 and cand-2 only (store order preserved)", chk.Candidates)
	}
	if chk.WindowHours != 24 {
		t.Fatalf("window_hours = %d, want the 24h default", chk.WindowHours)
	}
	if store.lastWindow != DuplicateCheckWindow || store.lastLimit != maxDuplicateCandidates {
		t.Fatalf("store consulted with window=%v limit=%d, want the package tunables", store.lastWindow, store.lastLimit)
	}

	// Nothing material → empty, present, and not nil (the client renders NO warning, not an error).
	store.cands = []DuplicateCandidate{{OrderID: "cand-3", SharedProducts: 1}}
	chk, err = svc.CheckDuplicates(dupCtx(), edOrder)
	if err != nil {
		t.Fatalf("empty check: %v", err)
	}
	if chk.Candidates == nil || len(chk.Candidates) != 0 {
		t.Fatalf("candidates = %#v, want an empty non-nil list", chk.Candidates)
	}
}
