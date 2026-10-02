package tax

// relief_gate_test.go, the failing legs for the ungated relief-reason hole (PR 101 round 3 P3-1): the
// engine resolves ANY non-empty reason through the effective-dated table and CreateRate plants ANY
// non-empty reason, so a planted rate row binds below the standard with no evidence gate at all. The
// lawful reason universe is the frozen CHECK on tax_decision_snapshot.tax_reason_code (migration 0029):
// eleven codes. Every defect leg here demands a refusal main does not give (it bites for the defect's
// own reason, a bind that must be a refusal); the regression pins hold the behavior the fix must leave
// unchanged (the five seeded reasons, the gated fallback, the manual exception, the no-row fallback the
// W4 recompute depends on).

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// The codes the legs speak that the package does not name yet: FNT_PRODUCT is one of the eleven (the
// CHECK carries it) but is neither evidence gated nor the manual exception; the free-form code is
// outside the eleven altogether; the treaty review code is one of the eleven with no seeded row.
// Literals, not constants: these legs must compile against main, where the reason universe does not
// exist yet (commit 1 is tests only).
const (
	legReasonFNTProduct = "FNT_PRODUCT"
	legReasonFreeForm   = "FROB_NOMINAL_RELIEF"
	legReasonTreaty     = "TREATY_OR_SELF_GOV_REVIEW"
)

// (a) A free-form reason outside the eleven, with a planted rate row AT the standard rate (so the bite
// below can only ever be the universe check, never the below-standard check beside it): main binds the
// row and answers a quote; the leg demands the 400-shaped refusal.
func TestAssessSale_FreeFormReasonRefused(t *testing.T) {
	f := newFakeStore()
	f.rates["ON|"+legReasonFreeForm] = stdRateBps
	svc, _ := newSvc(f)
	q, err := svc.AssessSale(context.Background(), SaleAssessment{SubtotalCents: 100_000, ReasonCode: legReasonFreeForm})
	if err == nil {
		t.Fatalf("main bound the planted row for the free-form reason %q (rate %d, tax %d): the engine must refuse a reason outside the lawful universe", legReasonFreeForm, q.RateBps, q.TaxCents)
	}
	var unk UnknownReasonError
	if !errors.As(err, &unk) {
		t.Fatalf("the refusal must be the TYPED unknown-reason error (the adapters key on the type, never the 400 shape), got %T: %v", err, err)
	}
	if unk.ReasonCode != legReasonFreeForm {
		t.Fatalf("the typed refusal must name the offending reason, got %+v", unk)
	}
}

// (b) The eleven-but-ungated shape (FNT_PRODUCT's): a planted 0 bps row binds UNGATED on main, the sale
// would commit untaxed. The leg demands the refusal as the typed error naming the reason and the
// resolved rate (commit 1, tests only, asserted the refusal alone: the type does not exist on main).
func TestAssessSale_UngatedBelowStandardRefused(t *testing.T) {
	f := newFakeStore()
	f.rates["ON|"+legReasonFNTProduct] = 0
	svc, _ := newSvc(f)
	q, err := svc.AssessSale(context.Background(), SaleAssessment{SubtotalCents: 100_000, ReasonCode: legReasonFNTProduct})
	if err == nil {
		t.Fatalf("main bound the planted 0 bps row for the ungated reason %q (tax %d): the sale would commit untaxed", legReasonFNTProduct, q.TaxCents)
	}
	var mis MisconfiguredRateError
	if !errors.As(err, &mis) {
		t.Fatalf("the refusal must be the typed misconfigured-rate error, got %T: %v", err, err)
	}
	if mis.ReasonCode != legReasonFNTProduct || mis.RateBps != 0 {
		t.Fatalf("the typed error must name the reason and the resolved rate: %+v", mis)
	}
}

// (c1) The five seeded reasons answer unchanged: the standard at 1300, the two 5% nets and the 0%
// delivery with their evidence, and the manual row at the standard.
func TestAssessSale_SeededReasonsUnchanged(t *testing.T) {
	f := newFakeStore()
	svc, _ := newSvc(f)
	const sub = 100_000
	tests := []struct {
		name       string
		reason     string
		evidence   bool
		wantRate   int
		wantTax    int64
		wantCredit int64
	}{
		{"standard", ReasonStdON13, false, 1300, 13_000, 0},
		{"ontario POS net with evidence", ReasonONFNPos8, true, 500, 5_000, 8_000},
		{"vendor delivery with evidence", ReasonFedVendDel, true, 0, 0, 0},
		{"fngst with evidence", ReasonFNGST5, true, 500, 5_000, 0},
		{"manual review", ReasonManual, false, 1300, 13_000, 0},
	}
	for _, tc := range tests {
		q, err := svc.AssessSale(context.Background(), SaleAssessment{
			SubtotalCents: sub, ReasonCode: tc.reason, EvidenceAttached: tc.evidence,
		})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if q.RateBps != tc.wantRate || q.TaxCents != tc.wantTax || q.OnCreditCents != tc.wantCredit {
			t.Errorf("%s: rate=%d tax=%d credit=%d, want %d/%d/%d", tc.name, q.RateBps, q.TaxCents, q.OnCreditCents, tc.wantRate, tc.wantTax, tc.wantCredit)
		}
	}
}

// (c2) A gated reason WITHOUT evidence still falls back to the standard rate, the credit withheld.
func TestAssessSale_GatedWithoutEvidenceStillFallsBack(t *testing.T) {
	f := newFakeStore()
	svc, _ := newSvc(f)
	q, err := svc.AssessSale(context.Background(), SaleAssessment{
		SubtotalCents: 100_000, ReasonCode: ReasonONFNPos8, EvidenceAttached: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if q.RateBps != stdRateBps || q.TaxCents != 13_000 || q.OnCreditCents != 0 || !q.RequiresEvidence {
		t.Fatalf("gated without evidence must fall back to standard: rate=%d tax=%d credit=%d requires=%v",
			q.RateBps, q.TaxCents, q.OnCreditCents, q.RequiresEvidence)
	}
}

// (c3) The manual exception keeps its documented freedom: an edited below-standard MANUAL row still
// binds (routed to a person, the row is that person's decision), ungated and unrefused.
func TestAssessSale_ManualBelowStandardRowStillBinds(t *testing.T) {
	f := newFakeStore()
	f.rates["ON|"+ReasonManual] = 500
	svc, _ := newSvc(f)
	q, err := svc.AssessSale(context.Background(), SaleAssessment{SubtotalCents: 100_000, ReasonCode: ReasonManual})
	if err != nil {
		t.Fatalf("the manual exception must keep binding its edited below-standard row: %v", err)
	}
	if q.RateBps != 500 || q.TaxCents != 5_000 {
		t.Fatalf("manual edited row must bind: rate=%d tax=%d, want 500/5000", q.RateBps, q.TaxCents)
	}
}

// (c4) A reason among the eleven with NO rate row still falls back to the standard: the recompute
// feeds W4 snapshots' reasons back through this same engine and MUST keep working.
func TestAssessSale_UniverseReasonWithoutRowStillFallsBack(t *testing.T) {
	f := newFakeStore()
	svc, _ := newSvc(f)
	q, err := svc.AssessSale(context.Background(), SaleAssessment{SubtotalCents: 100_000, ReasonCode: legReasonTreaty})
	if err != nil {
		t.Fatalf("an in-universe reason with no row must fall back to standard, not refuse: %v", err)
	}
	if q.RateBps != stdRateBps || q.TaxCents != 13_000 {
		t.Fatalf("no-row fallback: rate=%d tax=%d, want 1300/13000", q.RateBps, q.TaxCents)
	}
}

// (c5) The recompute pin: a settled W4 snapshot whose reason has no rate row recomputes through the
// live engine onto the standard, the M5 row inserted as before.
func TestRecompute_UniverseReasonWithoutRowStillRecomputes(t *testing.T) {
	f := newFakeStore()
	f.w4 = []Snapshot{{
		OrderID: "order-1", ReasonCode: legReasonTreaty, RuleVersion: ruleVersionW4,
		RateBps: 1300, FullHSTCents: 1300, NetCollectedCents: 1300,
	}}
	svc, _ := newSvc(f)
	res, err := svc.RecomputeReport(context.Background(), "2026-01-01", "2026-12-31")
	if err != nil {
		t.Fatalf("recompute must keep working for in-universe snapshot reasons: %v", err)
	}
	if res.Inserted != 1 || len(f.inserted) != 1 {
		t.Fatalf("recompute inserted=%d scanned=%d, want 1", res.Inserted, res.Scanned)
	}
	if got := f.inserted[0]; got.ReasonCode != legReasonTreaty || got.RateBps != stdRateBps || got.NetCollectedCents != 1300 {
		t.Fatalf("recomputed row wrong: %+v", got)
	}
}

// (d) CreateRate refuses a free-form reason outside the eleven with the 400 shape: the admin edge
// cannot plant a row the engine cannot lawfully carry.
func TestCreateRate_FreeFormReasonRefused(t *testing.T) {
	f := newFakeStore()
	svc, _ := newSvc(f)
	_, err := svc.CreateRate(context.Background(), Rate{ReasonCode: legReasonFreeForm, RateBps: stdRateBps})
	if err == nil {
		t.Fatal("main accepted a free-form reason_code: the admin edge must refuse a reason outside the lawful universe")
	}
	var bad httpx.BadRequestError
	if !errors.As(err, &bad) {
		t.Fatalf("the refusal must be the 400-shaped BadRequestError, got %T: %v", err, err)
	}
}

// (e) CreateRate refuses to PLANT the ungated-below-standard shape (FNT_PRODUCT at 0) with the same
// words the engine refuses to BIND it, so the admin edge fails first and the ring never sees the row.
// Commit 1, tests only, asserted the refusal alone; the fix commit strengthens this to the same-words
// check against the typed error's message.
func TestCreateRate_UngatedBelowStandardRefused(t *testing.T) {
	f := newFakeStore()
	svc, _ := newSvc(f)
	_, err := svc.CreateRate(context.Background(), Rate{ReasonCode: legReasonFNTProduct, RateBps: 0})
	if err == nil {
		t.Fatal("main planted the ungated 0 bps row: the admin edge must refuse the ungated-below-standard shape")
	}
	var bad httpx.BadRequestError
	if !errors.As(err, &bad) {
		t.Fatalf("the refusal must be the 400-shaped BadRequestError, got %T: %v", err, err)
	}
	want := MisconfiguredRateError{ReasonCode: legReasonFNTProduct, RateBps: 0, StdBps: stdRateBps}.Error()
	if bad.Msg != want {
		t.Fatalf("the admin refusal must carry the same words the engine refuses to bind with:\n got %q\nwant %q", bad.Msg, want)
	}
}

// (f) The rows the table must keep taking: a gated reason's 0 bps row, the standard row, the manual
// row (below standard too, the documented freedom), and an in-universe ungated row AT the standard.
func TestCreateRate_LegalRowsStillAccepted(t *testing.T) {
	f := newFakeStore()
	svc, _ := newSvc(f)
	legal := []Rate{
		{ReasonCode: ReasonFedVendDel, RateBps: 0},   // gated: the evidence gate is the proof, not the rate
		{ReasonCode: ReasonStdON13, RateBps: 1300},   // the standard itself
		{ReasonCode: ReasonManual, RateBps: 1300},    // routed to a person
		{ReasonCode: ReasonManual, RateBps: 500},     // the manual freedom, below standard
		{ReasonCode: legReasonTreaty, RateBps: 1300}, // in the universe, at the standard, ungated is fine
	}
	for _, r := range legal {
		got, err := svc.CreateRate(context.Background(), r)
		if err != nil {
			t.Errorf("CreateRate %s at %d bps must still be accepted, got %v", r.ReasonCode, r.RateBps, err)
			continue
		}
		if got.ReasonCode != r.ReasonCode || got.RateBps != r.RateBps {
			t.Errorf("CreateRate echoed %s/%d, want %s/%d", got.ReasonCode, got.RateBps, r.ReasonCode, r.RateBps)
		}
	}
	if f.rates["ON|"+legReasonTreaty] != 1300 {
		t.Fatalf("the accepted row must land in the store: %v", f.rates["ON|"+legReasonTreaty])
	}
}

// (g) The effective standard, round 2's P2: the comparison is against the standard row the dealer
// actually configured, not the hard-coded constant. With the standard row lawfully raised to 1500
// through the admin edge, an ungated reason at 1300 (above the constant, 200 bps below the effective
// standard) must be refused at the bind edge. On the round 1 head the constant let it bind (the
// reviewer's probe: the relief ring committed at 260 tax where the standard ring taxed 300).
func TestAssessSale_UngatedBelowTheEffectiveStandardRefused(t *testing.T) {
	f := newFakeStore()
	f.rates["ON|"+ReasonStdON13] = 1500
	f.rates["ON|"+legReasonFNTProduct] = 1300
	svc, _ := newSvc(f)
	q, err := svc.AssessSale(context.Background(), SaleAssessment{SubtotalCents: 100_000, ReasonCode: legReasonFNTProduct})
	if err == nil {
		t.Fatalf("the ungated reason bound %d bps, %d bps below the effective standard (rate %d, tax %d): the comparison must be against the standard row in effect", 1300, 1500-1300, q.RateBps, q.TaxCents)
	}
	var mis MisconfiguredRateError
	if !errors.As(err, &mis) {
		t.Fatalf("the refusal must be the typed misconfigured-rate error, got %T: %v", err, err)
	}
	if mis.ReasonCode != legReasonFNTProduct || mis.RateBps != 1300 || mis.StdBps != 1500 {
		t.Fatalf("the typed error must name the reason, the resolved rate and the standard it fell short of: %+v", mis)
	}
}

// (h) The admin edge compares the same way: raising the standard to 1500 then planting FNT_PRODUCT at
// 1400 (above the constant, below the effective standard) must be refused. On the round 1 head both
// creates were accepted.
func TestCreateRate_UngatedBelowTheEffectiveStandardRefused(t *testing.T) {
	f := newFakeStore()
	svc, _ := newSvc(f)
	if _, err := svc.CreateRate(context.Background(), Rate{ReasonCode: ReasonStdON13, RateBps: 1500}); err != nil {
		t.Fatalf("raising the standard row must be lawful: %v", err)
	}
	_, err := svc.CreateRate(context.Background(), Rate{ReasonCode: legReasonFNTProduct, RateBps: 1400})
	if err == nil {
		t.Fatal("the admin edge planted an ungated row below the effective standard (above the constant): the comparison must be against the standard row in effect")
	}
	var bad httpx.BadRequestError
	if !errors.As(err, &bad) {
		t.Fatalf("the refusal must be the 400-shaped BadRequestError, got %T: %v", err, err)
	}
	want := MisconfiguredRateError{ReasonCode: legReasonFNTProduct, RateBps: 1400, StdBps: 1500}.Error()
	if bad.Msg != want {
		t.Fatalf("the admin refusal must carry the same words the engine refuses to bind with:\n got %q\nwant %q", bad.Msg, want)
	}
}

// (i) The standard reason itself is the reference, never the subject (round 2's P3 1, the other
// direction): a lawful lowering of the standard row must be enterable at the admin edge and bind at
// the ring. On the round 1 head CreateRate refused STD at 1299 as misconfigured and assessSale refused
// a standard ring under a directly planted 1200 row.
func TestStandardRowItselfIsNeverBelowStandard(t *testing.T) {
	f := newFakeStore()
	svc, _ := newSvc(f)
	if _, err := svc.CreateRate(context.Background(), Rate{ReasonCode: ReasonStdON13, RateBps: 1299}); err != nil {
		t.Fatalf("lowering the standard row must be lawful (the standard is the reference, not a subject): %v", err)
	}
	f.rates["ON|"+ReasonStdON13] = 1200
	q, err := svc.AssessSale(context.Background(), SaleAssessment{SubtotalCents: 100_000})
	if err != nil {
		t.Fatalf("a standard ring under a lowered standard row must bind at the lowered rate: %v", err)
	}
	if q.RateBps != 1200 {
		t.Fatalf("the standard ring bound %d bps, want the lowered 1200", q.RateBps)
	}
}

// (j) The typed refusal's echo is bounded: a caller's reason of unbounded length comes back bounded,
// because the refusal's words travel to a counter.
func TestUnknownReasonEchoIsBounded(t *testing.T) {
	long := make([]byte, 500)
	for i := range long {
		long[i] = 'A'
	}
	err := UnknownReasonError{ReasonCode: string(long)}
	msg := err.Error()
	if runeCount := len([]rune(msg)); runeCount > 256 {
		t.Fatalf("the refusal echoes an unbounded reason: the message is %d runes long", runeCount)
	}
	if !strings.Contains(msg, "...") {
		t.Fatalf("a cut echo must be marked, got %q", msg)
	}
}

// (k) The interval guard (round 3's P2): the admin edge measures the row's WHOLE life. A backdated
// ungated row, planted while the standard was 1300 but open-ended into an era where the standard row
// reads 1500, must be refused against the 1500 it will govern beside. On the round 2 head the guard
// measured only the row's first day, saw the 1300 standard of that day, and planted it; the ring then
// refused it as a server fault (the reviewer's probe).
func TestCreateRate_BackdatedUngatedRowRefusedAgainstTheLaterStandard(t *testing.T) {
	f := newFakeStore()
	// The standard eras: 1300 from the seed's day, then a raise to 1500 (dated rows only; the map
	// answer stays 1300 so the bind edge is untouched by the era plant).
	f.rateRows = append(f.rateRows,
		Rate{Jurisdiction: "ON", ReasonCode: ReasonStdON13, RateBps: 1500, EffectiveFrom: "2026-08-01"},
	)
	svc, _ := newSvc(f)
	_, err := svc.CreateRate(context.Background(), Rate{
		ReasonCode:    legReasonFNTProduct,
		RateBps:       1300,
		EffectiveFrom: "2026-01-01", // backdated: open-ended into the 1500 era
	})
	if err == nil {
		t.Fatal("the admin edge planted a backdated ungated row whose open end governs beside the later 1500 standard: the guard must measure the row's whole interval")
	}
	var bad httpx.BadRequestError
	if !errors.As(err, &bad) {
		t.Fatalf("the refusal must be the 400-shaped BadRequestError, got %T: %v", err, err)
	}
	want := MisconfiguredRateError{ReasonCode: legReasonFNTProduct, RateBps: 1300, StdBps: 1500}.Error()
	if bad.Msg != want {
		t.Fatalf("the admin refusal must name the later standard it fell short of:\n got %q\nwant %q", bad.Msg, want)
	}
	// The same row, closed BEFORE the raise, is lawful: it never governs beside the 1500 era.
	if _, err := svc.CreateRate(context.Background(), Rate{
		ReasonCode:    legReasonFNTProduct,
		RateBps:       1300,
		EffectiveFrom: "2026-01-01",
		EffectiveTo:   "2026-07-31",
	}); err != nil {
		t.Fatalf("a row closed before the raise never governs beside it and must be accepted: %v", err)
	}
}
