package pricing

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// - in-memory batchStore fake (the internal/product DB-free pattern) -
//
// This fake emulates the DB semantics the A-08 ACs depend on: append-only change log,
// idempotent apply (status guard → no duplicate writes), a unified (single-row) retail
// anchor, and the SKU→product match/unmatched split.

type fakeBatchStore struct {
	policy    AnchorPolicy
	profiles  map[string]ImportProfile
	products  map[string]ProductRef // sku → ref
	listPrice map[string]int64      // productID → current retail anchor
	batches   map[string]PriceBatch
	lines     map[string][]PriceChangeLine // batchID → lines
	log       []ChangeLogRow               // append-only
	seq       int
}

func newFakeBatchStore() *fakeBatchStore {
	return &fakeBatchStore{
		policy:    AnchorPolicy{MarkupBps: 18500, FOBLowCents: 40, FOBHighCents: 50, VarianceAlertBps: 1500},
		profiles:  map[string]ImportProfile{},
		products:  map[string]ProductRef{},
		listPrice: map[string]int64{},
		batches:   map[string]PriceBatch{},
		lines:     map[string][]PriceChangeLine{},
	}
}

func (f *fakeBatchStore) id(prefix string) string {
	f.seq++
	return prefix + "-" + itoa(f.seq)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func (f *fakeBatchStore) AnchorPolicy(_ context.Context, _ string) (AnchorPolicy, error) {
	return f.policy, nil
}
func (f *fakeBatchStore) ListImportProfiles(_ context.Context) ([]ImportProfile, error) {
	var out []ImportProfile
	for _, p := range f.profiles {
		out = append(out, p)
	}
	return out, nil
}
func (f *fakeBatchStore) UpdateImportProfileMapping(_ context.Context, id string, mapping map[string]string) (ImportProfile, error) {
	p, ok := f.profiles[id]
	if !ok {
		return ImportProfile{}, errNoProfile
	}
	p.Mapping = mapping
	f.profiles[id] = p
	return p, nil
}
func (f *fakeBatchStore) GetImportProfile(_ context.Context, id string) (ImportProfile, error) {
	if p, ok := f.profiles[id]; ok {
		return p, nil
	}
	return ImportProfile{}, errNoProfile
}
func (f *fakeBatchStore) ProductBySKU(_ context.Context, sku string) (ProductRef, error) {
	if p, ok := f.products[sku]; ok {
		return p, nil
	}
	return ProductRef{}, errNotFound
}
func (f *fakeBatchStore) CurrentListPrice(_ context.Context, productID string) (*int64, error) {
	if v, ok := f.listPrice[productID]; ok {
		c := v
		return &c, nil
	}
	return nil, nil
}
func (f *fakeBatchStore) CreateBatch(_ context.Context, b PriceBatch) (PriceBatch, error) {
	b.ID = f.id("batch")
	b.CreatedAt = time.Now()
	f.batches[b.ID] = b
	return b, nil
}
func (f *fakeBatchStore) GetBatch(_ context.Context, id string) (PriceBatch, error) {
	if b, ok := f.batches[id]; ok {
		return b, nil
	}
	return PriceBatch{}, errNotFound
}
func (f *fakeBatchStore) ListBatches(_ context.Context, fl BatchFilter) ([]PriceBatch, error) {
	var out []PriceBatch
	for _, b := range f.batches {
		if fl.Status != "" && b.Status != fl.Status {
			continue
		}
		out = append(out, b)
	}
	return out, nil
}
func (f *fakeBatchStore) UpdateBatch(_ context.Context, b PriceBatch) (PriceBatch, error) {
	if _, ok := f.batches[b.ID]; !ok {
		return PriceBatch{}, errNotFound
	}
	f.batches[b.ID] = b
	return b, nil
}
func (f *fakeBatchStore) ReplaceBatchLines(_ context.Context, batchID string, lines []PriceChangeLine) error {
	cp := make([]PriceChangeLine, len(lines))
	copy(cp, lines)
	for i := range cp {
		cp[i].ID = f.id("line")
	}
	f.lines[batchID] = cp
	return nil
}
func (f *fakeBatchStore) ListBatchLines(_ context.Context, batchID string) ([]PriceChangeLine, error) {
	return f.lines[batchID], nil
}
func (f *fakeBatchStore) DueBatches(_ context.Context, asOf time.Time) ([]PriceBatch, error) {
	var out []PriceBatch
	for _, b := range f.batches {
		if b.Status == BatchApproved && b.EffectiveDate != nil && !b.EffectiveDate.After(asOf) {
			out = append(out, b)
		}
	}
	return out, nil
}
func (f *fakeBatchStore) ApplyBatch(_ context.Context, batchID, userID string) (int, error) {
	b := f.batches[batchID]
	if b.Status == BatchApplied {
		// idempotent: count prior retail rows, no writes
		n := 0
		for _, r := range f.log {
			if r.BatchID == batchID && r.Field == "retail_cents" {
				n++
			}
		}
		return n, nil
	}
	if b.Status != BatchApproved {
		return 0, errBadBatchState
	}
	applied := 0
	for _, l := range f.lines[batchID] {
		if l.ProductID == "" || l.AfterRetailCents == nil {
			continue
		}
		f.listPrice[l.ProductID] = *l.AfterRetailCents
		f.log = append(f.log, ChangeLogRow{
			ID: f.id("log"), ProductID: l.ProductID, BatchID: batchID, Field: "retail_cents",
			BeforeCents: l.BeforeRetailCents, AfterCents: l.AfterRetailCents,
			EffectiveDate: b.EffectiveDate, ChangedBy: userID, CreatedAt: time.Now(),
		})
		if l.AfterCostCents != nil {
			f.log = append(f.log, ChangeLogRow{
				ID: f.id("log"), ProductID: l.ProductID, BatchID: batchID, Field: "cost_cents",
				BeforeCents: l.BeforeCostCents, AfterCents: l.AfterCostCents,
				EffectiveDate: b.EffectiveDate, ChangedBy: userID, CreatedAt: time.Now(),
			})
		}
		applied++
	}
	b.Status = BatchApplied
	now := time.Now()
	b.AppliedAt = &now
	f.batches[batchID] = b
	return applied, nil
}
func (f *fakeBatchStore) ListChangeLog(_ context.Context, fl ChangeLogFilter) ([]ChangeLogRow, error) {
	var out []ChangeLogRow
	for _, r := range f.log {
		if fl.ProductID != "" && r.ProductID != fl.ProductID {
			continue
		}
		if fl.BatchID != "" && r.BatchID != fl.BatchID {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// svcWithBatch builds a resolver-free Service wired to the batch fake at a fixed clock.
func svcWithBatch(f *fakeBatchStore) *Service {
	return (&Service{now: func() time.Time { return time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC) }}).WithBatchStore(f)
}

func ptrI64(v int64) *int64 { return &v }

// seedProfile registers a cost/retail mapping profile + a set of matched products.
func seedProfile(f *fakeBatchStore, mapping map[string]string, skus ...string) string {
	pid := f.id("profile")
	f.profiles[pid] = ImportProfile{ID: pid, VendorID: "v-1", Name: "test", Mapping: mapping, IsActive: true}
	for _, sku := range skus {
		f.products[sku] = ProductRef{ID: "prod-" + sku, SKU: sku, PriceUOM: "SF"}
	}
	return pid
}

// - AC: derivation anchor rules (pure precedence engine) -

func TestDeriveRetail_AnchorPrecedence(t *testing.T) {
	policy := AnchorPolicy{MarkupBps: 18500, FOBLowCents: 40, FOBHighCents: 50}
	// trade wins → trade + FOB midpoint (45), freight ESTIMATED
	got := deriveRetail(policy, derivationInput{tradeCents: ptrI64(2000), costCents: ptrI64(1000), mfrRetailCents: ptrI64(9999)})
	if got.source != DeriveTradePlusFOB || got.retailCents != 2045 || !got.freightEstimated || got.freightCents != 45 {
		t.Fatalf("trade path: %+v", got)
	}
	// no trade → cost × 1.85 (round half up): 1050*1.85 = 1942.5 → 1943
	got = deriveRetail(policy, derivationInput{costCents: ptrI64(1050), mfrRetailCents: ptrI64(9999)})
	if got.source != DeriveCostMarkup || got.retailCents != 1943 || got.freightEstimated {
		t.Fatalf("cost path: %+v", got)
	}
	// no trade, no cost → manufacturer retail used as-is
	got = deriveRetail(policy, derivationInput{mfrRetailCents: ptrI64(2650)})
	if got.source != DeriveMfrRetail || got.retailCents != 2650 {
		t.Fatalf("mfr path: %+v", got)
	}
	// nothing → not derivable
	got = deriveRetail(policy, derivationInput{})
	if got.ok || got.source != DeriveNone {
		t.Fatalf("none path: %+v", got)
	}
}

func TestParseMoneyCents(t *testing.T) {
	for in, want := range map[string]*int64{"": nil, "10": ptrI64(1000), "10.5": ptrI64(1050), "10.50": ptrI64(1050), "$1,050.00": ptrI64(105000), "  8.75 ": ptrI64(875)} {
		got, err := parseMoneyCents(in)
		if err != nil {
			t.Fatalf("parse %q: %v", in, err)
		}
		if (got == nil) != (want == nil) || (got != nil && *got != *want) {
			t.Fatalf("parse %q = %v, want %v", in, got, want)
		}
	}
}

// - AC: import idempotent (golden fixture) + preview + unmatched + outlier -

func TestImportBatch_GoldenFixtureIdempotent(t *testing.T) {
	f := newFakeBatchStore()
	pid := seedProfile(f, map[string]string{"sku_col": "sku", "cost_col": "cost", "retail_col": "retail"},
		"DBS-TB-BLU60", "DBS-TB-ABERDEEN", "DBS-TB-BOREALIS", "DBS-TB-MINICRETA", "DBS-TB-HYDR")
	svc := svcWithBatch(f)
	b, err := svc.CreateBatch(context.Background(), PriceBatch{BatchNo: "PB-TECHO-1", ProfileID: pid}, "maker-1")
	if err != nil {
		t.Fatal(err)
	}

	read := func() string {
		data, err := os.ReadFile("testdata/techo_sample.csv")
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	res, err := svc.ImportBatch(context.Background(), b.ID, strings.NewReader(read()), "techo_sample.csv")
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalLines != 6 || res.MatchedLines != 5 {
		t.Fatalf("counts: total=%d matched=%d", res.TotalLines, res.MatchedLines)
	}
	if len(res.Unmatched) != 1 || res.Unmatched[0].SKU != "DBS-TB-GHOST-SKU" {
		t.Fatalf("unmatched report wrong: %+v", res.Unmatched)
	}
	if len(res.Preview) != 5 {
		t.Fatalf("preview should be capped at 5, got %d", len(res.Preview))
	}
	// cost×1.85 derivation: BLU60 cost 1050 → 1943
	var blu *PriceChangeLine
	for i := range f.lines[b.ID] {
		if f.lines[b.ID][i].SKU == "DBS-TB-BLU60" {
			blu = &f.lines[b.ID][i]
		}
	}
	if blu == nil || blu.AfterRetailCents == nil || *blu.AfterRetailCents != 1943 {
		t.Fatalf("BLU60 derived retail wrong: %+v", blu)
	}

	// Idempotent re-import: same file → same line count, no duplication.
	res2, err := svc.ImportBatch(context.Background(), b.ID, strings.NewReader(read()), "techo_sample.csv")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.lines[b.ID]) != 6 || res2.TotalLines != 6 {
		t.Fatalf("re-import not idempotent: stored=%d", len(f.lines[b.ID]))
	}
}

// - AC: variance-alert threshold (outlier stamped at derivation) -

func TestImportBatch_VarianceOutlierStamp(t *testing.T) {
	f := newFakeBatchStore()
	f.policy.VarianceAlertBps = 1500 // 15%
	pid := seedProfile(f, map[string]string{"sku_col": "sku", "cost_col": "cost"}, "SKU-CALM", "SKU-SPIKE")
	// prior anchors so variance is defined
	f.listPrice["prod-SKU-CALM"] = 1850  // 10.00 cost×1.85 = 1850 → 0% variance
	f.listPrice["prod-SKU-SPIKE"] = 1000 // new 18.50 vs 10.00 = +85% → outlier
	svc := svcWithBatch(f)
	b, _ := svc.CreateBatch(context.Background(), PriceBatch{BatchNo: "PB-VAR", ProfileID: pid}, "maker-1")
	csv := "sku,cost\nSKU-CALM,10.00\nSKU-SPIKE,10.00\n"
	res, err := svc.ImportBatch(context.Background(), b.ID, strings.NewReader(csv), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Outliers) != 1 || res.Outliers[0].SKU != "SKU-SPIKE" {
		t.Fatalf("expected 1 outlier SKU-SPIKE, got %+v", res.Outliers)
	}
	if res.Batch.OutlierCount != 1 {
		t.Fatalf("batch outlier_count = %d, want 1", res.Batch.OutlierCount)
	}
}

// - AC: approval separation-of-duties (maker ≠ checker) -

func TestApproveBatch_SeparationOfDuties(t *testing.T) {
	f := newFakeBatchStore()
	pid := seedProfile(f, map[string]string{"sku_col": "sku", "cost_col": "cost"}, "SKU-A")
	svc := svcWithBatch(f)
	b, _ := svc.CreateBatch(context.Background(), PriceBatch{BatchNo: "PB-SOD", ProfileID: pid}, "maker-1")
	_, _ = svc.ImportBatch(context.Background(), b.ID, strings.NewReader("sku,cost\nSKU-A,10.00\n"), "")

	// same user tries to approve → blocked
	if _, err := svc.ApproveBatch(context.Background(), b.ID, "maker-1"); !errors.Is(err, errSeparation) {
		t.Fatalf("expected separation error, got %v", err)
	}
	// a different manager approves → ok
	out, err := svc.ApproveBatch(context.Background(), b.ID, "checker-2")
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != BatchApproved || out.ApprovedBy != "checker-2" {
		t.Fatalf("approve result wrong: %+v", out)
	}
}

// - AC: effective-date gate + apply idempotent + change-log append-only -

func TestApplyBatch_EffectiveDateAndIdempotent(t *testing.T) {
	f := newFakeBatchStore()
	pid := seedProfile(f, map[string]string{"sku_col": "sku", "cost_col": "cost"}, "SKU-A", "SKU-B")
	svc := svcWithBatch(f)
	future := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	b, _ := svc.CreateBatch(context.Background(), PriceBatch{BatchNo: "PB-APPLY", ProfileID: pid, EffectiveDate: &future}, "maker-1")
	_, _ = svc.ImportBatch(context.Background(), b.ID, strings.NewReader("sku,cost\nSKU-A,10.00\nSKU-B,20.00\n"), "")
	if _, err := svc.ApproveBatch(context.Background(), b.ID, "checker-2"); err != nil {
		t.Fatal(err)
	}

	// asOf before effective_date → blocked
	early := time.Date(2026, 7, 15, 9, 0, 0, 0, time.UTC)
	if _, _, err := svc.ApplyBatch(context.Background(), b.ID, "checker-2", early); !errors.Is(err, errNotEffective) {
		t.Fatalf("expected not-effective error, got %v", err)
	}

	// asOf on/after effective_date → applies, writes unified anchor + append-only log
	onDay := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	_, applied, err := svc.ApplyBatch(context.Background(), b.ID, "checker-2", onDay)
	if err != nil {
		t.Fatal(err)
	}
	if applied != 2 {
		t.Fatalf("applied = %d, want 2", applied)
	}
	if f.listPrice["prod-SKU-A"] != 1850 || f.listPrice["prod-SKU-B"] != 3700 {
		t.Fatalf("anchors not written: %+v", f.listPrice)
	}
	retailRows := 0
	for _, r := range f.log {
		if r.Field == "retail_cents" {
			retailRows++
		}
	}
	if retailRows != 2 {
		t.Fatalf("retail log rows = %d, want 2", retailRows)
	}

	// Idempotent re-apply → no new rows, no error.
	logLen := len(f.log)
	_, applied2, err := svc.ApplyBatch(context.Background(), b.ID, "checker-2", onDay)
	if err != nil {
		t.Fatal(err)
	}
	if applied2 != 0 || len(f.log) != logLen {
		t.Fatalf("re-apply not idempotent: applied=%d logGrew=%d", applied2, len(f.log)-logLen)
	}
}

// - AC: apply-due sweep (no scheduler, endpoint/lazy) -

func TestApplyDueBatches(t *testing.T) {
	f := newFakeBatchStore()
	pid := seedProfile(f, map[string]string{"sku_col": "sku", "cost_col": "cost"}, "SKU-A")
	svc := svcWithBatch(f)
	due := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	notDue := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for _, d := range []time.Time{due, notDue} {
		dd := d
		b, _ := svc.CreateBatch(context.Background(), PriceBatch{BatchNo: "PB-" + d.Format("2006-01-02"), ProfileID: pid, EffectiveDate: &dd}, "maker-1")
		_, _ = svc.ImportBatch(context.Background(), b.ID, strings.NewReader("sku,cost\nSKU-A,10.00\n"), "")
		_, _ = svc.ApproveBatch(context.Background(), b.ID, "checker-2")
	}
	asOf := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	n, err := svc.ApplyDueBatches(context.Background(), "checker-2", asOf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("apply-due applied %d, want 1 (only the past-effective batch)", n)
	}
}

// - AC: change-log read is append-only & scoped -

func TestChangeLog_ScopedNewestFirst(t *testing.T) {
	f := newFakeBatchStore()
	pid := seedProfile(f, map[string]string{"sku_col": "sku", "cost_col": "cost"}, "SKU-A")
	svc := svcWithBatch(f)
	past := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	b, _ := svc.CreateBatch(context.Background(), PriceBatch{BatchNo: "PB-LOG", ProfileID: pid, EffectiveDate: &past}, "maker-1")
	_, _ = svc.ImportBatch(context.Background(), b.ID, strings.NewReader("sku,cost\nSKU-A,10.00\n"), "")
	_, _ = svc.ApproveBatch(context.Background(), b.ID, "checker-2")
	if _, _, err := svc.ApplyBatch(context.Background(), b.ID, "checker-2", time.Now()); err != nil {
		t.Fatal(err)
	}
	rows, err := svc.ChangeLog(context.Background(), ChangeLogFilter{ProductID: "prod-SKU-A"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("expected change-log rows for product")
	}
	for _, r := range rows {
		if r.ProductID != "prod-SKU-A" {
			t.Fatalf("scope leak: %+v", r)
		}
	}
}
