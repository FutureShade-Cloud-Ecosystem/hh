package pricing

// rebate_test.go, DIB-69 CORE. The accrual arithmetic, the at-most-once guard, and the refusals.
//
// accrualFor is a pure function, so the arithmetic is table-tested directly rather than through the
// service. That is deliberate: it is the mutation target named in REPORT-W4-A.md, and a mutation to
// the rounding or to the rate application has to fail on the ARITHMETIC assertion, not on some
// downstream total that happens to notice.

import (
	"context"
	"strings"
	"testing"
	"time"
)

// - DB-free rebate fake -

type fakeRebateStore struct {
	programs []RebateProgram
	inserted []RebateAccrual
	// seen emulates the 0112 unique index: (program, source_type, source_id, product).
	seen map[string]bool
	nID  int
}

func (f *fakeRebateStore) key(a RebateAccrual) string {
	return a.ProgramID + "|" + a.SourceType + "|" + a.SourceID + "|" + a.ProductID
}

func (f *fakeRebateStore) ListRebatePrograms(_ context.Context, vendorID string, _ bool) ([]RebateProgram, error) {
	var out []RebateProgram
	for _, p := range f.programs {
		if vendorID == "" || p.VendorID == vendorID {
			out = append(out, p)
		}
	}
	return out, nil
}
func (f *fakeRebateStore) GetRebateProgram(_ context.Context, id string) (RebateProgram, error) {
	for _, p := range f.programs {
		if p.ID == id {
			return p, nil
		}
	}
	return RebateProgram{}, errNotFound
}
func (f *fakeRebateStore) CreateRebateProgram(_ context.Context, p RebateProgram) (RebateProgram, error) {
	if p.ID == "" {
		p.ID = "rp-" + p.Code
	}
	p.Version = 1
	f.programs = append(f.programs, p)
	return p, nil
}
func (f *fakeRebateStore) SaveRebateProgram(_ context.Context, p RebateProgram, expectedVersion int64) (RebateProgram, error) {
	for i, cur := range f.programs {
		if cur.ID != p.ID {
			continue
		}
		if cur.Version != expectedVersion {
			return RebateProgram{}, errConflict
		}
		p.Version = cur.Version + 1
		f.programs[i] = p
		return p, nil
	}
	return RebateProgram{}, errNotFound
}
func (f *fakeRebateStore) DeleteRebateProgram(_ context.Context, id string, _ int64) error {
	for i, cur := range f.programs {
		if cur.ID == id {
			f.programs[i].IsActive = false
			return nil
		}
	}
	return errNotFound
}
func (f *fakeRebateStore) ProgramsForLine(_ context.Context, productID, categoryID, vendorID string) ([]RebateProgram, error) {
	var out []RebateProgram
	for _, p := range f.programs {
		if !p.IsActive {
			continue
		}
		if p.ProductID != "" && p.ProductID != productID {
			continue
		}
		if p.CategoryID != "" && p.CategoryID != categoryID {
			continue
		}
		if vendorID != "" && p.VendorID != vendorID {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}
func (f *fakeRebateStore) InsertAccrual(_ context.Context, a RebateAccrual, _ string) (RebateAccrual, error) {
	if f.seen == nil {
		f.seen = map[string]bool{}
	}
	if a.SourceID != "" && f.seen[f.key(a)] {
		return RebateAccrual{}, errAccrualExists
	}
	f.seen[f.key(a)] = true
	f.nID++
	a.ID = "acc-" + itoa(f.nID)
	f.inserted = append(f.inserted, a)
	return a, nil
}
func (f *fakeRebateStore) VendorLedger(_ context.Context, vendorID string, from, to *time.Time) (VendorRebateLedger, error) {
	out := VendorRebateLedger{VendorID: vendorID, VendorName: "Fake Vendor", From: from, To: to}
	for _, a := range f.inserted {
		if a.VendorID != vendorID {
			continue
		}
		if from != nil && a.AccruedAt.Before(*from) {
			continue
		}
		if to != nil && !a.AccruedAt.Before(*to) {
			continue
		}
		out.Rows = append(out.Rows, a)
		out.TotalCents += a.AccrualCents
		out.AccrualCount++
	}
	return out, nil
}

func rebateSvc(rs *fakeRebateStore) *Service {
	return newSvc(&fakeStore{}, "").WithRebateStore(rs).withClock(func() time.Time { return fixedNow })
}

// - THE ARITHMETIC (mutation target) -

func TestAccrualFor_TheThreeBases(t *testing.T) {
	line := RebateSaleLine{QtyBase: 12.5, SaleCents: 199900, ExtCostCents: 120000}

	t.Run("PCT_OF_SALE applies the rate to the extended SALE", func(t *testing.T) {
		p := RebateProgram{Basis: RebatePctOfSale, RateBps: p64(250)} // 2.50%
		cents, basis, note := accrualFor(p, line)
		// 199900 × 250 / 10000 = 4997.5 → half-up → 4998.
		if cents != 4998 {
			t.Fatalf("2.50%% of $1999.00 = $49.98 (half-up from 4997.5), got %d", cents)
		}
		if basis != 199900 {
			t.Fatalf("the recorded basis must be the sale, got %d", basis)
		}
		if !strings.Contains(note, "of sale") {
			t.Fatalf("the frozen rate note must say what the rate was OF, got %q", note)
		}
	})

	t.Run("PCT_OF_COST applies the rate to the extended COST", func(t *testing.T) {
		p := RebateProgram{Basis: RebatePctOfCost, RateBps: p64(250)}
		cents, basis, _ := accrualFor(p, line)
		if cents != 3000 { // 120000 × 250 / 10000
			t.Fatalf("2.50%% of $1200.00 = $30.00, got %d", cents)
		}
		if basis != 120000 {
			t.Fatalf("the recorded basis must be the cost, got %d", basis)
		}
	})

	t.Run("AMT_PER_UNIT multiplies the FRACTIONAL base qty", func(t *testing.T) {
		// The fraction is the point. Base qty is tonnes and square feet, not counts, an int64
		// multiply here would drop the .5 and under-claim on every partial-tonne line.
		p := RebateProgram{Basis: RebateAmtPerUnit, RateCentsPerUnit: p64(15)}
		cents, _, note := accrualFor(p, line)
		if cents != 188 { // 12.5 × 15 = 187.5 → half-up → 188
			t.Fatalf("12.5 units × $0.15 = $1.88 (half-up from 187.5), got %d", cents)
		}
		if !strings.Contains(note, "per base unit") {
			t.Fatalf("note must name the per-unit basis, got %q", note)
		}
	})

	t.Run("AMT_PER_UNIT accrues the exact half cent the float arm drops", func(t *testing.T) {
		// WHY 4.1 AND NOT ANOTHER 12.5. 12.5 is exact in binary, so the case above passes on the
		// float multiply too and cannot tell the exact arm (rebate.go, the row 820 arm) from the
		// float one it replaced. 4.1 is not binary-exact: the float product lands at
		// 61.499999999999993 and half-up answers 61, a cent under-claimed against the
		// manufacturer, while the exact rational 4.1 × 15 = 61.5 rounds away from zero to 62.
		// This subtest is the bite for that arm: reverting the accrual to the float multiply
		// turns it red at 61.
		p := RebateProgram{Basis: RebateAmtPerUnit, RateCentsPerUnit: p64(15)}
		cents, _, _ := accrualFor(p, RebateSaleLine{QtyBase: 4.1, SaleCents: 9100, ExtCostCents: 6200})
		if cents != 62 { // 4.1 × 15 = 61.5 exactly → half away from zero → 62
			t.Fatalf("4.1 units × 15 cents = 61.5 exactly (half away from zero 62, the float's 61.499999999999993 answers 61), got %d", cents)
		}
	})
}

func TestAccrualFor_RoundingIsHalfUpNotTruncation(t *testing.T) {
	// WHY THIS IS ITS OWN TEST. Truncation is the tempting implementation (integer division does
	// it for free) and it is wrong in a specific, cumulative way: it biases EVERY claim downward,
	// so across a year of lines it is a systematic under-claim against the manufacturer rather
	// than noise that cancels out. These three cases each land on a .5 boundary.
	cases := []struct {
		sale int64
		bps  int64
		want int64
	}{
		{sale: 100, bps: 250, want: 3},   // 2.5 → 3, NOT 2
		{sale: 300, bps: 250, want: 8},   // 7.5 → 8, NOT 7
		{sale: 1999, bps: 250, want: 50}, // 49.975 → 50
	}
	for _, c := range cases {
		p := RebateProgram{Basis: RebatePctOfSale, RateBps: p64(c.bps)}
		got, _, _ := accrualFor(p, RebateSaleLine{SaleCents: c.sale})
		if got != c.want {
			t.Fatalf("%d cents × %d bps = %d, got %d", c.sale, c.bps, c.want, got)
		}
	}
}

// - the AT-MOST-ONCE guard -

func TestAccrueSale_IsIdempotentAcrossARetry(t *testing.T) {
	rs := &fakeRebateStore{programs: []RebateProgram{{
		ID: "rp-1", Code: "RB", VendorID: "v-1", CategoryID: "k-pavers",
		Basis: RebatePctOfSale, RateBps: p64(250), StartsAt: juneStart, IsActive: true,
	}}}
	svc := rebateSvc(rs)
	ev := RebateSaleEvent{
		SourceType: RebateSourcePOS, SourceID: "sale-1", OccurredAt: fixedNow,
		Lines: []RebateSaleLine{{ProductID: "p-1", CategoryID: "k-pavers", QtyBase: 10, SaleCents: 100000, ExtCostCents: 60000}},
	}

	first, err := svc.AccrueSale(context.Background(), ev)
	if err != nil {
		t.Fatalf("first accrual: %v", err)
	}
	if len(first.Accrued) != 1 || first.TotalCents != 2500 {
		t.Fatalf("first run must claim 2.50%% of $1000 = $25.00, got %+v", first)
	}

	// The retry. Every transport in this system is at-least-once, so this is the NORMAL case, not
	// an edge case, and it must look like SUCCESS, because a retry that errors retries forever.
	second, err := svc.AccrueSale(context.Background(), ev)
	if err != nil {
		t.Fatalf("a retried sale must not error, got %v", err)
	}
	if len(second.Accrued) != 0 || second.SkippedCount != 1 || second.TotalCents != 0 {
		t.Fatalf("the retry must accrue NOTHING and report the skip, got %+v", second)
	}
	if len(rs.inserted) != 1 {
		t.Fatalf("the ledger must hold exactly one row after a double delivery, got %d", len(rs.inserted))
	}
}

func TestAccrueSale_ADifferentSaleOfTheSameProductAccruesAgain(t *testing.T) {
	// The control for the idempotency test: the guard must key on the SOURCE, not on the product,
	// or the second customer to buy the same SKU silently earns nothing.
	rs := &fakeRebateStore{programs: []RebateProgram{{
		ID: "rp-1", Code: "RB", VendorID: "v-1", Basis: RebatePctOfSale, RateBps: p64(250),
		StartsAt: juneStart, IsActive: true,
	}}}
	svc := rebateSvc(rs)
	line := RebateSaleLine{ProductID: "p-1", QtyBase: 10, SaleCents: 100000}
	for _, id := range []string{"sale-1", "sale-2"} {
		if _, err := svc.AccrueSale(context.Background(), RebateSaleEvent{
			SourceType: RebateSourcePOS, SourceID: id, OccurredAt: fixedNow,
			Lines: []RebateSaleLine{line},
		}); err != nil {
			t.Fatalf("accrue %s: %v", id, err)
		}
	}
	if len(rs.inserted) != 2 {
		t.Fatalf("two distinct sales must both accrue, got %d rows", len(rs.inserted))
	}
}

// - coverage + window -

func TestAccrueSale_OnlyCoveringProgramsInTheirWindow(t *testing.T) {
	ended := fixedNow.Add(-24 * time.Hour)
	rs := &fakeRebateStore{programs: []RebateProgram{
		{ID: "rp-cat", Code: "A-CAT", VendorID: "v-1", CategoryID: "k-pavers", Basis: RebatePctOfSale, RateBps: p64(100), StartsAt: juneStart, IsActive: true},
		{ID: "rp-other", Code: "B-OTHER", VendorID: "v-2", CategoryID: "k-walls", Basis: RebatePctOfSale, RateBps: p64(500), StartsAt: juneStart, IsActive: true},
		{ID: "rp-expired", Code: "C-EXP", VendorID: "v-1", CategoryID: "k-pavers", Basis: RebatePctOfSale, RateBps: p64(900), StartsAt: juneStart, EndsAt: &ended, IsActive: true},
		{ID: "rp-future", Code: "D-FUT", VendorID: "v-1", CategoryID: "k-pavers", Basis: RebatePctOfSale, RateBps: p64(900), StartsAt: fixedNow.Add(24 * time.Hour), IsActive: true},
		{ID: "rp-retired", Code: "E-RET", VendorID: "v-1", CategoryID: "k-pavers", Basis: RebatePctOfSale, RateBps: p64(900), StartsAt: juneStart, IsActive: false},
	}}
	svc := rebateSvc(rs)
	res, err := svc.AccrueSale(context.Background(), RebateSaleEvent{
		SourceType: RebateSourceOrder, SourceID: "ord-1", OccurredAt: fixedNow,
		Lines: []RebateSaleLine{{ProductID: "p-1", CategoryID: "k-pavers", QtyBase: 1, SaleCents: 100000}},
	})
	if err != nil {
		t.Fatalf("AccrueSale: %v", err)
	}
	if len(res.Accrued) != 1 {
		t.Fatalf("exactly one program covers this line in-window, got %d: %+v", len(res.Accrued), res.Accrued)
	}
	if res.Accrued[0].ProgramID != "rp-cat" || res.TotalCents != 1000 {
		t.Fatalf("want rp-cat at 1%% of $1000 = $10.00, got %+v", res.Accrued[0])
	}
}

func TestAccrueSale_ZeroClaimsAreNotWritten(t *testing.T) {
	// A zero-cent claim is not a claim. Writing it would fill the ledger with rows that make
	// "which products actually earn rebate" unanswerable at a glance.
	rs := &fakeRebateStore{programs: []RebateProgram{{
		ID: "rp-1", Code: "RB", VendorID: "v-1", Basis: RebatePctOfSale, RateBps: p64(0),
		StartsAt: juneStart, IsActive: true,
	}}}
	res, err := rebateSvc(rs).AccrueSale(context.Background(), RebateSaleEvent{
		SourceType: RebateSourcePOS, SourceID: "s-1", OccurredAt: fixedNow,
		Lines: []RebateSaleLine{{ProductID: "p-1", QtyBase: 1, SaleCents: 100000}},
	})
	if err != nil {
		t.Fatalf("AccrueSale: %v", err)
	}
	if len(res.Accrued) != 0 || len(rs.inserted) != 0 {
		t.Fatalf("a 0-cent claim must not be written, got %+v", res.Accrued)
	}
}

// - refusals -

func TestAccrueSale_Refusals(t *testing.T) {
	rs := &fakeRebateStore{programs: []RebateProgram{{
		ID: "rp-1", Code: "RB", VendorID: "v-1", Basis: RebatePctOfSale, RateBps: p64(250),
		StartsAt: juneStart, IsActive: true,
	}}}
	svc := rebateSvc(rs)
	good := RebateSaleLine{ProductID: "p-1", QtyBase: 1, SaleCents: 100000}

	t.Run("a document accrual with no source id is refused", func(t *testing.T) {
		// Without a source id the ledger cannot be reconciled against the sale AND the unique index
		// cannot deduplicate, so the accrual would be both unauditable and double-claimable.
		_, err := svc.AccrueSale(context.Background(), RebateSaleEvent{
			SourceType: RebateSourcePOS, Lines: []RebateSaleLine{good},
		})
		if err == nil || !strings.Contains(err.Error(), "source_id is required") {
			t.Fatalf("want a source_id refusal, got %v", err)
		}
	})

	t.Run("an unknown source type is refused", func(t *testing.T) {
		_, err := svc.AccrueSale(context.Background(), RebateSaleEvent{
			SourceType: "GUESS", SourceID: "x", Lines: []RebateSaleLine{good},
		})
		if err == nil || !strings.Contains(err.Error(), "source_type must be") {
			t.Fatalf("want a source_type refusal, got %v", err)
		}
	})

	t.Run("a negative line is refused, and told where to go instead", func(t *testing.T) {
		// A return claws back a claim, and that is an ADJUSTMENT a human signs for, not a negative
		// number smuggled in on a sale event, which the 0112 CHECK would reject anyway.
		neg := good
		neg.SaleCents = -100000
		_, err := svc.AccrueSale(context.Background(), RebateSaleEvent{
			SourceType: RebateSourcePOS, SourceID: "s-9", Lines: []RebateSaleLine{neg},
		})
		if err == nil || !strings.Contains(err.Error(), "ADJUSTMENT") {
			t.Fatalf("the refusal must point at the ADJUSTMENT path, got %v", err)
		}
	})

	t.Run("no lines is refused", func(t *testing.T) {
		_, err := svc.AccrueSale(context.Background(), RebateSaleEvent{SourceType: RebateSourcePOS, SourceID: "s-9"})
		if err == nil {
			t.Fatalf("an empty event must be refused")
		}
	})
}

func TestAccrueSale_WithoutTheSeamIsUnavailableNotSilent(t *testing.T) {
	// Flag-off must be LOUD at the door and SILENT in the waterfall. An unwired rebate store makes
	// AccrueSale return errNoDB (→503) rather than quietly reporting a successful zero-row accrual,
	// which would look like "this product earns no rebate".
	_, err := newSvc(&fakeStore{}, "").AccrueSale(context.Background(), RebateSaleEvent{
		SourceType: RebateSourcePOS, SourceID: "s-1",
		Lines: []RebateSaleLine{{ProductID: "p-1", SaleCents: 1000}},
	})
	if err == nil {
		t.Fatalf("an unwired rebate seam must refuse, not silently no-op")
	}
}

// - the per-vendor ledger -

func TestVendorRebateLedger_TotalsWhatItLists(t *testing.T) {
	rs := &fakeRebateStore{programs: []RebateProgram{
		{ID: "rp-1", Code: "A", VendorID: "v-1", Basis: RebatePctOfSale, RateBps: p64(250), StartsAt: juneStart, IsActive: true},
		{ID: "rp-2", Code: "B", VendorID: "v-2", Basis: RebatePctOfSale, RateBps: p64(500), StartsAt: juneStart, IsActive: true},
	}}
	svc := rebateSvc(rs)
	for i, id := range []string{"s-1", "s-2"} {
		_ = i
		if _, err := svc.AccrueSale(context.Background(), RebateSaleEvent{
			SourceType: RebateSourcePOS, SourceID: id, OccurredAt: fixedNow,
			Lines: []RebateSaleLine{{ProductID: "p-1", QtyBase: 1, SaleCents: 100000}},
		}); err != nil {
			t.Fatalf("accrue: %v", err)
		}
	}
	led, err := svc.VendorRebateLedger(context.Background(), "v-1", nil, nil)
	if err != nil {
		t.Fatalf("VendorRebateLedger: %v", err)
	}
	// Two sales × 2.50% of $1000 for v-1 only. v-2's 5% rows must not be in v-1's total, this is
	// the assertion that catches a ledger query that forgot its vendor predicate.
	if led.AccrualCount != 2 || led.TotalCents != 5000 {
		t.Fatalf("want 2 rows totalling $50.00 for v-1, got %d rows / %d cents", led.AccrualCount, led.TotalCents)
	}
	var sum int64
	for _, r := range led.Rows {
		if r.VendorID != "v-1" {
			t.Fatalf("another vendor's row leaked into the ledger: %+v", r)
		}
		sum += r.AccrualCents
	}
	if sum != led.TotalCents {
		t.Fatalf("the header total must equal the rows it lists: %d vs %d", led.TotalCents, sum)
	}
}

// - program admin -

func TestUpdateRebateProgram_VendorIsImmutable(t *testing.T) {
	// rebate_accruals.vendor_id is denormalised from the program. Letting the program's vendor move
	// would re-attribute every claim it has already written, one vendor's ledger becoming
	// another's without a single accrual row being touched.
	rs := &fakeRebateStore{programs: []RebateProgram{{
		ID: "rp-1", Code: "RB", Name: "Rebate", VendorID: "v-1", Basis: RebatePctOfSale,
		RateBps: p64(250), StartsAt: juneStart, IsActive: true, Version: 1,
	}}}
	svc := rebateSvc(rs)
	out, err := svc.UpdateRebateProgram(context.Background(), "rp-1", RebateProgram{
		Code: "RB", Name: "Renamed", VendorID: "v-999", Basis: RebatePctOfSale,
		RateBps: p64(300), StartsAt: juneStart, IsActive: true,
	}, 1, "u-1")
	if err != nil {
		t.Fatalf("UpdateRebateProgram: %v", err)
	}
	if out.VendorID != "v-1" {
		t.Fatalf("the vendor must be pinned to its original value, got %q", out.VendorID)
	}
	if out.Name != "Renamed" {
		t.Fatalf("the rest of the edit must still land, got %q", out.Name)
	}
}

func TestValidateRebateProgram_Refusals(t *testing.T) {
	ok := RebateProgram{Code: "RB", Name: "R", VendorID: "v-1", Basis: RebatePctOfSale, RateBps: p64(250), StartsAt: juneStart}
	if err := validateRebateProgram(ok); err != nil {
		t.Fatalf("a well-formed program must validate, got %v", err)
	}
	cases := []struct {
		name string
		mut  func(p *RebateProgram)
		want string
	}{
		{"no vendor", func(p *RebateProgram) { p.VendorID = "" }, "claim against someone"},
		{"unknown basis", func(p *RebateProgram) { p.Basis = "PCT_OF_HOPE" }, "basis must be"},
		{"pct with no rate", func(p *RebateProgram) { p.RateBps = nil }, "requires rate_bps"},
		{"rate over 100%", func(p *RebateProgram) { p.RateBps = p64(10001) }, "rate_bps"},
		{"no start", func(p *RebateProgram) { p.StartsAt = time.Time{} }, "starts_at is required"},
		{"product AND category", func(p *RebateProgram) { p.ProductID, p.CategoryID = "p", "k" }, "not both"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := ok
			c.mut(&p)
			err := validateRebateProgram(p)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want a rejection mentioning %q, got %v", c.want, err)
			}
		})
	}
}
