package pricing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// - the DB-free fakes -
//
// A SEPARATE FAKE FROM fakeBatchStore, deliberately. The A-08 fake models A-08's semantics and every
// existing test in this package leans on it; widening it with six new methods would make every one of
// those tests depend on this surface's shape. This one models the 0198 semantics and nothing else,
// and the two are wired into the same Service side by side exactly as the pgx *Repository is.

type fakePriceFileStore struct {
	visibility string
	profiles   map[string]ImportProfile // vendorID → profile
	origins    map[string]string        // vendorID → origin
	anchors    map[string]int64         // productID → list price cents
	runs       []PriceImportRun
	staged     []priceFileStage
	seq        int
	anchorErr  error
}

func newFakePriceFileStore() *fakePriceFileStore {
	return &fakePriceFileStore{
		visibility: "MANAGER_UP",
		profiles:   map[string]ImportProfile{},
		origins:    map[string]string{},
		anchors:    map[string]int64{},
	}
}

func (f *fakePriceFileStore) ImportCostVisibility(context.Context, string) (string, error) {
	return f.visibility, nil
}

func (f *fakePriceFileStore) ActiveProfileForVendor(_ context.Context, vendorID string) (ImportProfile, string, error) {
	p, ok := f.profiles[vendorID]
	if !ok {
		return ImportProfile{}, "", errNoProfile
	}
	return p, f.origins[vendorID], nil
}

func (f *fakePriceFileStore) ListPricesForProducts(_ context.Context, ids []string) (map[string]int64, error) {
	if f.anchorErr != nil {
		return nil, f.anchorErr
	}
	out := map[string]int64{}
	for _, id := range ids {
		if v, ok := f.anchors[id]; ok {
			out[id] = v
		}
	}
	return out, nil
}

func (f *fakePriceFileStore) StagePriceFile(_ context.Context, in priceFileStage) (priceFileStaged, error) {
	f.seq++
	f.staged = append(f.staged, in)
	out := priceFileStaged{
		BatchID: "batch-pf", BatchNo: in.BatchNo,
		ProfileID: in.ProfileID, RunID: "run-pf", Staged: len(in.Lines),
	}
	if in.ProfileID == "" {
		out.ProfileID, out.ProfileCreated = "profile-new", true
	}
	prof := f.profiles[in.VendorID]
	prof.ID, prof.VendorID, prof.Mapping = out.ProfileID, in.VendorID, in.Mapping
	f.profiles[in.VendorID] = prof
	f.origins[in.VendorID] = "FIRST_IMPORT"
	run := in.Run
	run.ID = out.RunID
	f.runs = append(f.runs, run)
	return out, nil
}

func (f *fakePriceFileStore) ListImportRuns(_ context.Context, vendorID string, limit int) ([]PriceImportRun, error) {
	var out []PriceImportRun
	for _, r := range f.runs {
		if vendorID != "" && r.VendorID != vendorID {
			continue
		}
		out = append(out, r)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakePriceFileStore) ProfileVendorRows(context.Context) (map[string]PriceFileVendor, error) {
	out := map[string]PriceFileVendor{}
	for vid, p := range f.profiles {
		out[vid] = PriceFileVendor{VendorID: vid, ProfileID: p.ID, Origin: f.origins[vid], Mapping: p.Mapping}
	}
	return out, nil
}

// fakeCatalog satisfies CatalogSKUs. err non-nil is the "the catalogue did not answer" case, which
// the preview must survive with CatalogChecked=false rather than by claiming we stock nothing.
type fakeCatalog struct {
	byS   map[string]CatalogSKU
	err   error
	calls int
}

func (f *fakeCatalog) LookupSKUs(_ context.Context, skus []string) (map[string]CatalogSKU, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	out := map[string]CatalogSKU{}
	for _, s := range skus {
		if e, ok := f.byS[strings.ToLower(s)]; ok {
			out[s] = e
		}
	}
	return out, nil
}

// fakeCostVisibility satisfies CostVisibility. `err` models an identity outage, which must withhold
// cost rather than grant it.
type fakeCostVisibility struct {
	managers map[string]bool
	err      error
}

func (f *fakeCostVisibility) IsManager(_ context.Context, userID string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.managers[userID], nil
}

type fakeVendorDirectory struct {
	list []PriceFileVendor
	err  error
}

func (f *fakeVendorDirectory) ListVendors(context.Context) ([]PriceFileVendor, error) {
	return f.list, f.err
}

// pfHarness is one wired service plus the fakes behind it.
type pfHarness struct {
	svc   *Service
	store *fakePriceFileStore
	batch *fakeBatchStore
	cat   *fakeCatalog
	vis   *fakeCostVisibility
}

func newPFHarness(t *testing.T) *pfHarness {
	t.Helper()
	h := &pfHarness{
		store: newFakePriceFileStore(),
		batch: newFakeBatchStore(),
		cat:   &fakeCatalog{byS: map[string]CatalogSKU{}},
		vis:   &fakeCostVisibility{managers: map[string]bool{"mgr": true}},
	}
	svc := (&Service{now: func() time.Time { return time.Date(2026, 9, 1, 9, 30, 0, 0, time.UTC) }}).
		WithBatchStore(h.batch).
		WithCostVisibility(h.vis).
		WithCatalogSKUs(h.cat)
	svc.pricefile = h.store
	h.svc = svc
	return h
}

// stock registers a SKU as a catalogue product, optionally with a current retail anchor.
func (h *pfHarness) stock(sku, name string, anchorCents *int64) string {
	id := "prod-" + strings.ToLower(sku)
	h.cat.byS[strings.ToLower(sku)] = CatalogSKU{ProductID: id, SKU: sku, Name: name}
	if anchorCents != nil {
		h.store.anchors[id] = *anchorCents
	}
	return id
}

const vendorTecho = "11111111-1111-4111-8111-111111111111"

// A file with one of each outcome, plus a row that matches and cannot be priced.
const fourOutcomeCSV = "sku,cost,retail\n" +
	"DBS-TB-BLU60,10.00,24.00\n" + // matched, anchor 1000 → derived 1850 → CHANGED
	"DBS-TB-NEWONE,20.00,40.00\n" + // matched, no anchor → NEW
	"DBS-TB-SAME,10.00,24.00\n" + // matched, anchor already 1850 → UNCHANGED
	"DBS-XX-GHOST,5.00,12.00\n" + // no catalogue product → UNKNOWN
	"DBS-TB-BLANK,,\n" // matched, nothing to derive → UNCHANGED + UNPRICED

func (h *pfHarness) seedFourOutcomes() {
	c := int64(1000)
	h.stock("DBS-TB-BLU60", "Blu 60 Smooth", &c)
	h.stock("DBS-TB-NEWONE", "Borealis", nil)
	same := int64(1850)
	h.stock("DBS-TB-SAME", "Aberdeen", &same)
	h.stock("DBS-TB-BLANK", "Unpriced Slab", nil)
}

func previewInput(csv string) PriceFileInput {
	return PriceFileInput{VendorID: vendorTecho, UserID: "mgr", FileName: "techo.csv", Data: []byte(csv)}
}

// - 1 · the acceptance clause: a preview writes NOTHING -

func TestPreviewPriceFileWritesNothing(t *testing.T) {
	h := newPFHarness(t)
	h.seedFourOutcomes()
	for i := 0; i < 3; i++ {
		if _, err := h.svc.PreviewPriceFile(context.Background(), previewInput(fourOutcomeCSV)); err != nil {
			t.Fatalf("preview %d: %v", i, err)
		}
	}
	// Every write this surface can make goes through one of these three, and all three are empty.
	if len(h.store.staged) != 0 {
		t.Fatalf("a preview staged %d batches, the preview must write nothing", len(h.store.staged))
	}
	if len(h.store.runs) != 0 {
		t.Fatalf("a preview wrote %d audit rows, the preview must write nothing", len(h.store.runs))
	}
	if len(h.store.profiles) != 0 {
		t.Fatalf("a preview created %d profiles, lazy creation belongs to apply", len(h.store.profiles))
	}
	// GUARD THE GUARD: the same three counters must be non-zero after an apply, or this test would
	// pass just as well against a service that cannot write at all.
	pv, err := h.svc.PreviewPriceFile(context.Background(), previewInput(fourOutcomeCSV))
	if err != nil {
		t.Fatal(err)
	}
	in := previewInput(fourOutcomeCSV)
	in.Digest = pv.Digest
	if _, err := h.svc.ApplyPriceFile(context.Background(), in); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(h.store.staged) != 1 || len(h.store.runs) != 1 || len(h.store.profiles) != 1 {
		t.Fatalf("apply wrote staged=%d runs=%d profiles=%d, the counters this test reads are not live",
			len(h.store.staged), len(h.store.runs), len(h.store.profiles))
	}
}

// - 2 · the four outcomes, and the fifth number that keeps them honest -

func TestPreviewPriceFileFourOutcomes(t *testing.T) {
	h := newPFHarness(t)
	h.seedFourOutcomes()
	pv, err := h.svc.PreviewPriceFile(context.Background(), previewInput(fourOutcomeCSV))
	if err != nil {
		t.Fatal(err)
	}
	want := PriceFileCounts{Rows: 5, New: 1, Changed: 1, Unchanged: 2, Unknown: 1, Outliers: 1, Unpriced: 1}
	if pv.Counts != want {
		t.Fatalf("counts = %+v, want %+v", pv.Counts, want)
	}
	byS := map[string]PriceFileLine{}
	for _, l := range pv.Lines {
		byS[l.SKU] = l
	}
	if byS["DBS-TB-BLU60"].Outcome != OutcomeChanged {
		t.Fatalf("BLU60 = %q, want CHANGED", byS["DBS-TB-BLU60"].Outcome)
	}
	if got := byS["DBS-TB-BLU60"].NewRetailCents; got == nil || *got != 1850 {
		t.Fatalf("BLU60 derived retail = %v, want 1850 (1000 × 1.85)", got)
	}
	if byS["DBS-TB-NEWONE"].Outcome != OutcomeNew || byS["DBS-TB-NEWONE"].CurrentRetailCents != nil {
		t.Fatalf("NEWONE = %+v, want NEW with no current price", byS["DBS-TB-NEWONE"])
	}
	if byS["DBS-TB-SAME"].Outcome != OutcomeUnchanged {
		t.Fatalf("SAME = %q, want UNCHANGED", byS["DBS-TB-SAME"].Outcome)
	}
	if byS["DBS-XX-GHOST"].Outcome != OutcomeUnknown || byS["DBS-XX-GHOST"].ProductID != "" {
		t.Fatalf("GHOST = %+v, want UNKNOWN with no product", byS["DBS-XX-GHOST"])
	}
	// The unpriced row is UNCHANGED and counted separately, that separation is the whole point.
	blank := byS["DBS-TB-BLANK"]
	if blank.Outcome != OutcomeUnchanged || blank.DerivedFrom != DeriveNone || blank.NewRetailCents != nil {
		t.Fatalf("BLANK = %+v, want UNCHANGED / NONE / no derived price", blank)
	}
	// The product NAME reaches the row, because a worklist row is a name plus an identifier and a SKU
	// alone is not a name.
	if byS["DBS-TB-BLU60"].ProductName != "Blu 60 Smooth" {
		t.Fatalf("the matched row carries no product name: %+v", byS["DBS-TB-BLU60"])
	}
}

// A WHOLE FILE OF UNPRICED ROWS IS THE MIS-MAPPED-COLUMN CASE, and it must not read as agreement.
func TestPreviewReportsAMisMappedCostColumnAsUnpricedRatherThanUnchanged(t *testing.T) {
	h := newPFHarness(t)
	h.seedFourOutcomes()
	in := previewInput(fourOutcomeCSV)
	in.Mapping = map[string]string{MapSKU: "sku", MapCost: "dealer net"} // a header this file lacks
	pv, err := h.svc.PreviewPriceFile(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if pv.Counts.Unpriced != 4 || pv.Counts.Changed != 0 || pv.Counts.New != 0 {
		t.Fatalf("counts = %+v, want every matched row unpriced and nothing moving", pv.Counts)
	}
	// And apply refuses it rather than writing an empty batch.
	ap := previewInput(fourOutcomeCSV)
	ap.Mapping, ap.Digest = in.Mapping, pv.Digest
	if _, err := h.svc.ApplyPriceFile(context.Background(), ap); !errors.Is(err, errNothingToStage) {
		t.Fatalf("apply of a file that moves nothing = %v, want errNothingToStage", err)
	}
}

// - 3 · the trade_col trap is VISIBLE in the preview -

// Mapping a trade column silently switches every line from cost×markup to trade+estimated FOB. The
// preview is the only place that is visible before it is committed, so this pins that it IS visible
// , the derivation anchor changes on every line and the retail changes with it.
func TestMappingATradeColumnFlipsTheDerivationAndThePreviewShowsIt(t *testing.T) {
	h := newPFHarness(t)
	c := int64(1000)
	h.stock("DBS-TB-BLU60", "Blu 60 Smooth", &c)
	csv := "sku,cost,retail,trade\nDBS-TB-BLU60,10.00,24.00,20.00\n"

	without, err := h.svc.PreviewPriceFile(context.Background(), previewInput(csv))
	if err != nil {
		t.Fatal(err)
	}
	if without.Lines[0].DerivedFrom != DeriveCostMarkup || *without.Lines[0].NewRetailCents != 1850 {
		t.Fatalf("with no trade column mapped: %+v", without.Lines[0])
	}
	if without.Lines[0].FreightEstimated {
		t.Fatal("the cost path must not claim an estimated freight")
	}

	in := previewInput(csv)
	in.Mapping = map[string]string{MapSKU: "sku", MapCost: "cost", MapRetail: "retail", MapTrade: "trade"}
	with, err := h.svc.PreviewPriceFile(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if with.Lines[0].DerivedFrom != DeriveTradePlusFOB {
		t.Fatalf("mapping trade_col did not switch the anchor: %+v", with.Lines[0])
	}
	if got := *with.Lines[0].NewRetailCents; got != 2045 {
		t.Fatalf("trade+FOB retail = %d, want 2045 (2000 + the 45 band midpoint)", got)
	}
	if !with.Lines[0].FreightEstimated {
		t.Fatal("a trade-anchored line must be marked as carrying an ESTIMATED freight")
	}
	// The two previews must not share a digest, or applying one would satisfy the other.
	if with.Digest == without.Digest {
		t.Fatal("the digest ignores the mapping, an apply could commit a derivation nobody previewed")
	}
}

// - 4 · the preview→apply binding -

func TestApplyRefusesAFileOrMappingThatChangedSinceThePreview(t *testing.T) {
	h := newPFHarness(t)
	h.seedFourOutcomes()
	pv, err := h.svc.PreviewPriceFile(context.Background(), previewInput(fourOutcomeCSV))
	if err != nil {
		t.Fatal(err)
	}
	// No digest at all.
	if _, err := h.svc.ApplyPriceFile(context.Background(), previewInput(fourOutcomeCSV)); !errors.Is(err, errStalePreview) {
		t.Fatalf("apply with no digest = %v, want errStalePreview", err)
	}
	// The right digest over a DIFFERENT file.
	edited := previewInput(strings.Replace(fourOutcomeCSV, "10.00,24.00", "99.00,24.00", 1))
	edited.Digest = pv.Digest
	if _, err := h.svc.ApplyPriceFile(context.Background(), edited); !errors.Is(err, errStalePreview) {
		t.Fatalf("apply of an edited file with the old digest = %v, want errStalePreview", err)
	}
	// The right digest over a different MAPPING.
	remapped := previewInput(fourOutcomeCSV)
	remapped.Digest = pv.Digest
	remapped.Mapping = map[string]string{MapSKU: "sku", MapRetail: "retail"}
	if _, err := h.svc.ApplyPriceFile(context.Background(), remapped); !errors.Is(err, errStalePreview) {
		t.Fatalf("apply under a different mapping with the old digest = %v, want errStalePreview", err)
	}
	// And the matching pair goes through, or the three refusals above prove nothing.
	ok := previewInput(fourOutcomeCSV)
	ok.Digest = pv.Digest
	if _, err := h.svc.ApplyPriceFile(context.Background(), ok); err != nil {
		t.Fatalf("apply of the previewed file = %v, want success", err)
	}
}

// - 5 · what apply actually writes -

func TestApplyStagesMoversAndUnknownsAndWritesTheAuditRow(t *testing.T) {
	h := newPFHarness(t)
	h.seedFourOutcomes()
	pv, err := h.svc.PreviewPriceFile(context.Background(), previewInput(fourOutcomeCSV))
	if err != nil {
		t.Fatal(err)
	}
	in := previewInput(fourOutcomeCSV)
	in.Digest = pv.Digest
	eff := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	in.EffectiveDate = &eff
	res, err := h.svc.ApplyPriceFile(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	stage := h.store.staged[0]
	// CHANGED + NEW + UNKNOWN are staged; the two UNCHANGED rows are not.
	if len(stage.Lines) != 3 {
		t.Fatalf("staged %d lines, want 3 (1 changed + 1 new + 1 unknown)", len(stage.Lines))
	}
	unmatched := 0
	for _, l := range stage.Lines {
		if l.Unmatched {
			unmatched++
		}
		if l.SKU == "DBS-TB-SAME" || l.SKU == "DBS-TB-BLANK" {
			t.Fatalf("an UNCHANGED row was staged as a price change: %+v", l)
		}
	}
	if unmatched != 1 {
		t.Fatalf("staged %d unmatched lines, want the one UNKNOWN SKU recorded", unmatched)
	}
	if stage.EffectiveDate == nil || !stage.EffectiveDate.Equal(eff) {
		t.Fatalf("effective date = %v, want it carried onto the batch", stage.EffectiveDate)
	}
	// The audit row carries the whole file's counts, not the staged subset.
	run := h.store.runs[0]
	if run.Counts != pv.Counts {
		t.Fatalf("audit counts = %+v, want the preview's %+v", run.Counts, pv.Counts)
	}
	if run.SourceName != "techo.csv" || run.SourceFormat != FormatCSV || run.ContentDigest != pv.Digest {
		t.Fatalf("audit row does not identify the file: %+v", run)
	}
	if run.ImportedBy != "mgr" {
		t.Fatalf("audit row attributes the import to %q, want the caller the guard resolved", run.ImportedBy)
	}
	if !res.ProfileCreated {
		t.Fatal("the first import for a vendor must create its profile")
	}
	if !regexp.MustCompile(`^PF-20260901-093000-[0-9A-F]{6}$`).MatchString(stage.BatchNo) {
		t.Fatalf("batch no %q does not carry the import's date and a collision-proof suffix", stage.BatchNo)
	}
}

// - 6 · the mapping IS the profile, and a merge is not a replace -

func TestApplySavesTheMappingAndPreservesAKeyThisScreenDoesNotOffer(t *testing.T) {
	h := newPFHarness(t)
	h.seedFourOutcomes()
	// The 0039 seeded shape: it carries effective_col, which this screen never shows.
	h.store.profiles[vendorTecho] = ImportProfile{
		ID: "profile-seeded", VendorID: vendorTecho, VendorName: "Techo-Bloc", IsActive: true,
		Mapping: map[string]string{"sku_col": "sku", "cost_col": "cost", "retail_col": "retail", "effective_col": "effective_date"},
	}
	h.store.origins[vendorTecho] = "SEEDED"

	in := previewInput(fourOutcomeCSV)
	in.Mapping = map[string]string{MapSKU: "sku", MapCost: "cost"} // retail_col deliberately cleared
	pv, err := h.svc.PreviewPriceFile(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if pv.MappingSource != "REQUESTED" {
		t.Fatalf("mapping source = %q, want REQUESTED when the caller sent one", pv.MappingSource)
	}
	in.Digest = pv.Digest
	if _, err := h.svc.ApplyPriceFile(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	saved := h.store.staged[0].Mapping
	if saved["effective_col"] != "effective_date" {
		t.Fatalf("apply deleted a mapping key this screen does not display: %+v", saved)
	}
	if saved[MapCost] != "cost" {
		t.Fatalf("apply did not save the chosen cost column: %+v", saved)
	}
	if _, still := saved[MapRetail]; still {
		t.Fatalf("a column the operator cleared survived the merge: %+v", saved)
	}
}

// - 7 · the stored mapping is used when the caller sends none -

func TestPreviewUsesTheStoredMappingThenTheGuess(t *testing.T) {
	h := newPFHarness(t)
	h.seedFourOutcomes()
	// No profile: a guess, labelled as one.
	pv, err := h.svc.PreviewPriceFile(context.Background(), previewInput(fourOutcomeCSV))
	if err != nil {
		t.Fatal(err)
	}
	if pv.MappingSource != "GUESSED" || pv.Mapping[MapSKU] != "sku" || pv.Mapping[MapCost] != "cost" {
		t.Fatalf("no profile: source=%q mapping=%+v, want a labelled guess off the headers", pv.MappingSource, pv.Mapping)
	}
	if pv.ProfileID != "" {
		t.Fatalf("an unmapped vendor reported profile %q", pv.ProfileID)
	}
	// With a profile: the stored mapping, labelled as stored.
	h.store.profiles[vendorTecho] = ImportProfile{
		ID: "profile-seeded", VendorID: vendorTecho, VendorName: "Techo-Bloc", IsActive: true,
		Mapping: map[string]string{"sku_col": "sku", "retail_col": "retail"},
	}
	pv, err = h.svc.PreviewPriceFile(context.Background(), previewInput(fourOutcomeCSV))
	if err != nil {
		t.Fatal(err)
	}
	if pv.MappingSource != "STORED" || pv.Mapping[MapCost] != "" {
		t.Fatalf("with a profile: source=%q mapping=%+v, the stored mapping must win over the guess", pv.MappingSource, pv.Mapping)
	}
	if pv.ProfileID != "profile-seeded" {
		t.Fatalf("profile id = %q", pv.ProfileID)
	}
}

// - 8 · the catalogue seam degrades, and apply refuses in that state -

func TestAnUnreadableCatalogueDegradesThePreviewAndRefusesTheApply(t *testing.T) {
	h := newPFHarness(t)
	h.seedFourOutcomes()
	h.cat.err = errors.New("catalogue unreachable")
	pv, err := h.svc.PreviewPriceFile(context.Background(), previewInput(fourOutcomeCSV))
	if err != nil {
		t.Fatalf("a read path must not error when the catalogue is down: %v", err)
	}
	if pv.CatalogChecked {
		t.Fatal("the preview claims the catalogue was checked when the lookup failed")
	}
	if pv.Counts.Unknown != pv.Counts.Rows {
		t.Fatalf("counts = %+v, want every row unknown while the check is down", pv.Counts)
	}
	in := previewInput(fourOutcomeCSV)
	in.Digest = pv.Digest
	if _, err := h.svc.ApplyPriceFile(context.Background(), in); !errors.Is(err, errCatalogUnread) {
		t.Fatalf("apply with an unread catalogue = %v, want errCatalogUnread", err)
	}
}

// - 9 · the catalogue is asked ONCE -

func TestTheCatalogueIsLookedUpOncePerImportNotOncePerRow(t *testing.T) {
	h := newPFHarness(t)
	h.seedFourOutcomes()
	if _, err := h.svc.PreviewPriceFile(context.Background(), previewInput(fourOutcomeCSV)); err != nil {
		t.Fatal(err)
	}
	if h.cat.calls != 1 {
		t.Fatalf("the catalogue was asked %d times for a 5-row file, want 1", h.cat.calls)
	}
}

// - 10 · the listing is capped and ordered by what needs a decision -

func TestPreviewCapsTheListingAndPutsTheMoversFirst(t *testing.T) {
	h := newPFHarness(t)
	var b strings.Builder
	b.WriteString("sku,cost\n")
	// 30 unchanged rows written FIRST, then one changed row last. A cap that respected file order
	// would truncate away the only row worth reading.
	for i := 0; i < 30; i++ {
		sku := "DBS-SAME-" + string(rune('A'+i%26)) + itoa(i)
		anchor := int64(1850)
		h.stock(sku, "Same "+sku, &anchor)
		b.WriteString(sku + ",10.00\n")
	}
	mover := int64(500)
	h.stock("DBS-MOVER", "Mover", &mover)
	b.WriteString("DBS-MOVER,10.00\n")

	in := previewInput(b.String())
	in.Limit = 5
	pv, err := h.svc.PreviewPriceFile(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if pv.Counts.Rows != 31 || pv.Counts.Changed != 1 || pv.Counts.Unchanged != 30 {
		t.Fatalf("counts = %+v, the counts must cover the whole file, not the page", pv.Counts)
	}
	if len(pv.Lines) != 5 || !pv.Truncated {
		t.Fatalf("lines=%d truncated=%v, want a capped and honestly-flagged listing", len(pv.Lines), pv.Truncated)
	}
	if pv.Lines[0].SKU != "DBS-MOVER" {
		t.Fatalf("first listed row is %q, the changed row must survive the cap", pv.Lines[0].SKU)
	}
}

// - 11 · the money reader, on the shapes a manufacturer actually sends -

func TestPriceFileReadsTheMoneyShapesAManufacturerSends(t *testing.T) {
	h := newPFHarness(t)
	h.stock("A1", "One", nil)
	h.stock("A2", "Two", nil)
	h.stock("A3", "Three", nil)
	csv := "sku,cost\nA1,\"$1,234.56\"\nA2,7\nA3,0.005\n"
	pv, err := h.svc.PreviewPriceFile(context.Background(), previewInput(csv))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, l := range pv.Lines {
		if l.Costs == nil || l.Costs.FileCostCents == nil {
			t.Fatalf("a manager received no cost for %s", l.SKU)
		}
		got[l.SKU] = *l.Costs.FileCostCents
	}
	if got["A1"] != 123456 || got["A2"] != 700 || got["A3"] != 0 {
		t.Fatalf("parsed cents = %+v, want A1=123456 A2=700 A3=0", got)
	}
}

// - 12 · the shipped sample files still import -

func TestTheShippedSampleCSVsImport(t *testing.T) {
	for _, name := range []string{"techo_sample.csv", "unilock_sample.csv"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", name))
			if err != nil {
				t.Skipf("sample %s is not present", name)
			}
			h := newPFHarness(t)
			pf, perr := parsePriceFile(data)
			if perr != nil {
				t.Fatalf("parse: %v", perr)
			}
			for _, r := range pf.rows {
				h.stock(pf.value(r, "sku"), "seeded", nil)
			}
			pv, err := h.svc.PreviewPriceFile(context.Background(), PriceFileInput{
				VendorID: vendorTecho, UserID: "mgr", FileName: name, Data: data,
			})
			if err != nil {
				t.Fatal(err)
			}
			if pv.Counts.Rows == 0 || pv.Counts.New != pv.Counts.Rows {
				t.Fatalf("counts = %+v, want every row NEW against an empty anchor", pv.Counts)
			}
		})
	}
}
