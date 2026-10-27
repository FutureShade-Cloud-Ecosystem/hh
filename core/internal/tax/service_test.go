package tax

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/moneyutil"
)

// -- in-memory fakes (no DB) -----------------------------------------------------

type fakeStore struct {
	rates     map[string]int // "jur|reason" -> bps (effective-agnostic for the unit tests)
	rateRows  []Rate         // dated rows, what MaxRateInRange honestly resolves (the interval legs)
	profiles  map[string]StatusCardProfile
	evidence  map[string]Evidence
	config    map[string]Config
	w4        []Snapshot
	inserted  []Snapshot
	salesHST  int64 // the period's document tax, what the reconcile reads as collected
	returnHST int64 // the period's POS return tax, shown as returns
	billITCs  int64 // the period's posted vendor bill tax, shown apart
	evSeq     int
}

// DocumentSalesHSTCents, ReturnHSTCents and VendorBillITCCents stand in for the billing, pos and
// purchasing seams (FW1-11): the reconcile reads the period's documents through them, never snapshots.
func (f *fakeStore) DocumentSalesHSTCents(context.Context, int, int, string) (int64, error) {
	return f.salesHST, nil
}

func (f *fakeStore) ReturnHSTCents(context.Context, int, int, string) (int64, error) {
	return f.returnHST, nil
}

func (f *fakeStore) VendorBillITCCents(context.Context, int, int, string) (int64, error) {
	return f.billITCs, nil
}

func newFakeStore() *fakeStore {
	f := &fakeStore{
		rates: map[string]int{
			"ON|" + ReasonStdON13:    stdRateBps,
			"ON|" + ReasonONFNPos8:   ontarioNetBps,
			"ON|" + ReasonFedVendDel: 0,
			"ON|" + ReasonFNGST5:     500,
			"ON|" + ReasonManual:     stdRateBps,
		},
		profiles: map[string]StatusCardProfile{},
		evidence: map[string]Evidence{},
		config:   map[string]Config{},
	}
	// The dated mirror of the seeded five (the seed migration plants them open-ended), so the
	// interval guard sees the same world the map shows ResolveRate.
	for reason, bps := range f.rates {
		f.rateRows = append(f.rateRows, Rate{
			Jurisdiction:  "ON",
			ReasonCode:    reason[len("ON|"):],
			RateBps:       bps,
			EffectiveFrom: "2000-01-01",
		})
	}
	return f
}

func (f *fakeStore) ResolveRate(_ context.Context, jur, reason, _ string) (int, bool, error) {
	bps, ok := f.rates[jur+"|"+reason]
	return bps, ok, nil
}

// MaxRateInRange mirrors the repository's overlap honestly over the dated rows (an empty to is
// open-ended); the rates map is not consulted, so the interval legs really drive the date logic.
func (f *fakeStore) MaxRateInRange(_ context.Context, jur, reason, from, to string) (int, bool, error) {
	best, found := 0, false
	for _, r := range f.rateRows {
		if r.Jurisdiction != jur || r.ReasonCode != reason {
			continue
		}
		if r.EffectiveFrom > to && to != "" {
			continue // the row starts after the range ends
		}
		if r.EffectiveTo != "" && r.EffectiveTo < from {
			continue // the row ends before the range starts
		}
		if !found || r.RateBps > best {
			best, found = r.RateBps, true
		}
	}
	return best, found, nil
}

func (f *fakeStore) ListRates(context.Context) ([]Rate, error) { return nil, nil }
func (f *fakeStore) InsertRate(_ context.Context, r Rate) (Rate, error) {
	f.rates[r.Jurisdiction+"|"+r.ReasonCode] = r.RateBps
	f.rateRows = append(f.rateRows, r)
	r.ID = "rate-1"
	return r, nil
}
func (f *fakeStore) ListProfiles(context.Context) ([]StatusCardProfile, error) {
	out := make([]StatusCardProfile, 0, len(f.profiles))
	for _, p := range f.profiles {
		out = append(out, p)
	}
	return out, nil
}
func (f *fakeStore) GetProfileByCode(_ context.Context, code string) (StatusCardProfile, bool, error) {
	p, ok := f.profiles[code]
	return p, ok, nil
}
func (f *fakeStore) UpsertProfile(_ context.Context, p StatusCardProfile) (StatusCardProfile, error) {
	if p.ID == "" {
		p.ID = "prof-" + p.Code
	}
	f.profiles[p.Code] = p
	return p, nil
}
func (f *fakeStore) InsertEvidence(_ context.Context, e Evidence) (Evidence, error) {
	f.evSeq++
	e.ID = "ev-" + string(rune('a'+f.evSeq))
	e.CreatedAt = "2026-07-07T00:00:00+00:00"
	f.evidence[e.ID] = e
	return e, nil
}
func (f *fakeStore) ListEvidence(_ context.Context, _ EvidenceFilter) ([]Evidence, error) {
	out := make([]Evidence, 0, len(f.evidence))
	for _, e := range f.evidence {
		out = append(out, e)
	}
	return out, nil
}
func (f *fakeStore) GetEvidence(_ context.Context, id string) (Evidence, bool, error) {
	e, ok := f.evidence[id]
	return e, ok, nil
}
func (f *fakeStore) GetConfig(_ context.Context, branchID string) (Config, error) {
	if c, ok := f.config[branchID]; ok {
		return c, nil
	}
	return Config{RoundMode: "INVOICE_TOTAL"}, nil
}
func (f *fakeStore) UpsertConfig(_ context.Context, c Config) (Config, error) {
	f.config[c.BranchID] = c
	return c, nil
}
func (f *fakeStore) ListW4Snapshots(context.Context, string, string) ([]Snapshot, error) {
	return f.w4, nil
}
func (f *fakeStore) SnapshotExists(_ context.Context, orderID, orderLineID, rv string) (bool, error) {
	for _, s := range f.inserted {
		if s.OrderID == orderID && s.OrderLineID == orderLineID && s.RuleVersion == rv {
			return true, nil
		}
	}
	return false, nil
}
func (f *fakeStore) InsertSnapshot(_ context.Context, s Snapshot) (string, error) {
	s.ID = "snap-" + string(rune('a'+len(f.inserted)))
	f.inserted = append(f.inserted, s)
	return s.ID, nil
}

type fakeRecorder struct{ calls []audit.SensitiveAccess }

func (r *fakeRecorder) Record(_ context.Context, a audit.SensitiveAccess) error {
	r.calls = append(r.calls, a)
	return nil
}

type fakeGL struct{ balances map[string]int64 }

func (g fakeGL) GLBalanceCents(_ context.Context, code string, _, _ int, _ string) (int64, error) {
	return g.balances[code], nil
}

func newSvc(f *fakeStore) (*Service, *fakeRecorder) {
	rec := &fakeRecorder{}
	svc := &Service{
		repo:  f,
		audit: rec,
		log:   slog.Default(),
		now:   func() time.Time { return time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC) },
	}
	return svc, rec
}

// -- AssessSale: round-once + parity with pos.flatHST13 ----------------------------

func TestAssessSale_StandardRoundsHalfUp_ParityWithFlat13(t *testing.T) {
	f := newFakeStore()
	svc, _ := newSvc(f)
	cases := []int64{0, 1, 99, 100, 12345, 999_99, 1_000_000, 7_777_77}
	for _, sub := range cases {
		q, err := svc.AssessSale(context.Background(), SaleAssessment{SubtotalCents: sub})
		if err != nil {
			t.Fatalf("assess %d: %v", sub, err)
		}
		want := (sub*1300 + 5000) / 10000 // pos.flatHST13
		if q.TaxCents != want {
			t.Errorf("subtotal %d: tax %d, want flat13 %d", sub, q.TaxCents, want)
		}
		if q.RateBps != stdRateBps {
			t.Errorf("subtotal %d: rate %d, want %d", sub, q.RateBps, stdRateBps)
		}
	}
}

// The per-line split sums EXACTLY to the round-once header tax (round-once largest-remainder discipline).
func TestAssessSale_PerLineSplitSumsToHeader(t *testing.T) {
	f := newFakeStore()
	svc, _ := newSvc(f)
	lines := []int64{3333, 3333, 3334} // uneven → forces the largest-remainder pennies
	var subtotal int64
	for _, l := range lines {
		subtotal += l
	}
	q, _ := svc.AssessSale(context.Background(), SaleAssessment{SubtotalCents: subtotal})
	parts := moneyutil.AllocateLargestRemainder(q.TaxCents, lines)
	var sum int64
	for _, p := range parts {
		sum += p
	}
	if sum != q.TaxCents {
		t.Fatalf("per-line parts sum %d != header tax %d", sum, q.TaxCents)
	}
}

// -- status-card / relief matrix ----------------------------------------------------

func TestAssessSale_StatusCardMatrix(t *testing.T) {
	f := newFakeStore()
	svc, _ := newSvc(f)
	const sub = 100_000 // $1000.00
	// Every FN relief is evidence-gated (S3 W6 B3): the reduced rate AND the point-of-sale credit bind only with
	// evidence attached; without it the assessment reverts to the 13% standard and no credit is handed back.
	tests := []struct {
		name       string
		reason     string
		evidence   bool
		wantRate   int
		wantTax    int64
		wantCredit int64
	}{
		{"standard 13%", ReasonStdON13, false, 1300, 13_000, 0},
		{"ontario POS 5% WITH evidence", ReasonONFNPos8, true, 500, 5_000, 8_000},
		{"ontario POS blocked WITHOUT evidence", ReasonONFNPos8, false, 1300, 13_000, 0},
		{"fngst 5% WITH evidence", ReasonFNGST5, true, 500, 5_000, 0},
		{"fngst blocked WITHOUT evidence", ReasonFNGST5, false, 1300, 13_000, 0},
		{"vendor delivery 0% WITH evidence", ReasonFedVendDel, true, 0, 0, 0},
		{"vendor delivery blocked WITHOUT evidence", ReasonFedVendDel, false, 1300, 13_000, 0},
		{"manual review 13%", ReasonManual, false, 1300, 13_000, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q, err := svc.AssessSale(context.Background(), SaleAssessment{
				SubtotalCents: sub, ReasonCode: tc.reason, EvidenceAttached: tc.evidence,
			})
			if err != nil {
				t.Fatal(err)
			}
			if q.RateBps != tc.wantRate || q.TaxCents != tc.wantTax {
				t.Errorf("%s: got rate=%d tax=%d, want rate=%d tax=%d", tc.name, q.RateBps, q.TaxCents, tc.wantRate, tc.wantTax)
			}
			if q.OnCreditCents != tc.wantCredit {
				t.Errorf("%s: ontario credit got %d want %d", tc.name, q.OnCreditCents, tc.wantCredit)
			}
		})
	}
}

// The 0% on-reserve-delivery HARD GATE: reason retained, RequiresEvidence flagged, rate falls back to standard.
func TestAssessSale_EvidenceHardGate(t *testing.T) {
	f := newFakeStore()
	svc, _ := newSvc(f)
	q, _ := svc.AssessSale(context.Background(), SaleAssessment{
		SubtotalCents: 50_000, ReasonCode: ReasonFedVendDel, EvidenceAttached: false,
	})
	if !q.RequiresEvidence {
		t.Error("RequiresEvidence must be set for an evidence-gated reason")
	}
	if q.ReasonCode != ReasonFedVendDel {
		t.Errorf("reason must be retained, got %s", q.ReasonCode)
	}
	if q.RateBps != stdRateBps {
		t.Errorf("binding rate must fall back to standard, got %d", q.RateBps)
	}
}

// -- DB-rate hot change -------------------------------------------------------------

func TestAssessSale_DBRateHotChange(t *testing.T) {
	f := newFakeStore()
	svc, _ := newSvc(f)
	q1, _ := svc.AssessSale(context.Background(), SaleAssessment{SubtotalCents: 100_000})
	if q1.TaxCents != 13_000 {
		t.Fatalf("pre-change tax %d", q1.TaxCents)
	}
	// A config change lands as a new effective row; the fake models the live lookup returning the new bps.
	f.rates["ON|"+ReasonStdON13] = 1400
	q2, _ := svc.AssessSale(context.Background(), SaleAssessment{SubtotalCents: 100_000})
	if q2.TaxCents != 14_000 || q2.RateBps != 1400 {
		t.Fatalf("post-change tax %d rate %d, want 14000/1400", q2.TaxCents, q2.RateBps)
	}
}

// -- recompute: idempotent + parity with the live engine ----------------------------

func TestRecompute_ParityWithLiveEngineAndIdempotent(t *testing.T) {
	f := newFakeStore()
	// A settled W4 flat-13 snapshot: full_hst 1300 on a $100.00 base, standard-rated.
	f.w4 = []Snapshot{{
		OrderID: "order-1", OrderLineID: "", ReasonCode: ReasonStdON13, RuleVersion: ruleVersionW4,
		RateBps: 1300, FullHSTCents: 1300, NetCollectedCents: 1300,
	}}
	svc, _ := newSvc(f)

	res, err := svc.RecomputeReport(context.Background(), "2026-01-01", "2026-12-31")
	if err != nil {
		t.Fatal(err)
	}
	if res.Inserted != 1 || res.Scanned != 1 {
		t.Fatalf("first run: scanned=%d inserted=%d", res.Scanned, res.Inserted)
	}
	got := f.inserted[0]

	// PARITY: the recompute output must be byte-equal to what the live engine (AssessSale, the same code path
	// pos's TaxAssessor runs) produces on the reconstructed base.
	base := reconstructBase(f.w4[0])
	live, _ := svc.AssessSale(context.Background(), SaleAssessment{SubtotalCents: base, ReasonCode: ReasonStdON13})
	if got.NetCollectedCents != live.NetCollectedCents || got.RateBps != live.RateBps {
		t.Fatalf("parity mismatch: recompute net=%d rate=%d vs live net=%d rate=%d",
			got.NetCollectedCents, got.RateBps, live.NetCollectedCents, live.RateBps)
	}
	liveJSON, _ := json.Marshal(struct {
		Net  int64 `json:"net_collected_cents"`
		Rate int   `json:"rate_bps"`
		Full int64 `json:"full_hst_cents"`
	}{live.NetCollectedCents, live.RateBps, hstOn(base, stdRateBps)})
	recomputeJSON, _ := json.Marshal(struct {
		Net  int64 `json:"net_collected_cents"`
		Rate int   `json:"rate_bps"`
		Full int64 `json:"full_hst_cents"`
	}{got.NetCollectedCents, got.RateBps, got.FullHSTCents})
	if string(liveJSON) != string(recomputeJSON) {
		t.Fatalf("byte-inequality: live=%s recompute=%s", liveJSON, recomputeJSON)
	}

	// IDEMPOTENT: a second run inserts nothing (insert-only, no double-insert).
	res2, _ := svc.RecomputeReport(context.Background(), "2026-01-01", "2026-12-31")
	if res2.Inserted != 0 || res2.SkippedExists != 1 {
		t.Fatalf("second run not idempotent: inserted=%d skipped=%d", res2.Inserted, res2.SkippedExists)
	}
	// Insert-only: the original W4 row is untouched, a NEW M5 row was added.
	if got.RuleVersion != ruleVersionM5 {
		t.Errorf("recompute row must be M5-recompute, got %s", got.RuleVersion)
	}
}

// -- masked-only serialization ------------------------------------------------------

func TestCaptureEvidence_MaskedOnly_NoRawEverSerialized(t *testing.T) {
	f := newFakeStore()
	svc, rec := newSvc(f)
	f.profiles["ON_RESERVE_DELIVERY"] = StatusCardProfile{
		ID: "p1", Code: "ON_RESERVE_DELIVERY", RateBps: 0,
		RequiresPhoto: true, RequiresNumber: true, RequiresAttestation: true, Status: "ACTIVE",
	}
	const raw = "123456789012481"
	ev, err := svc.CaptureEvidence(context.Background(), EvidenceCapture{
		CardProfileCode: "ON_RESERVE_DELIVERY", PhotoRef: "vault://img/1", RawNumber: raw, Attestation: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	blob, _ := json.Marshal(ev)
	if strings.Contains(string(blob), raw) {
		t.Fatalf("raw card number leaked into serialization: %s", blob)
	}
	if ev.NumberMask != "•••• 2481" {
		t.Errorf("mask = %q, want '•••• 2481'", ev.NumberMask)
	}
	if ev.AttestationStatus != attestationPendingBanner {
		t.Errorf("attestation banner = %q, want %q", ev.AttestationStatus, attestationPendingBanner)
	}
	// A-21 evidence CHANGE audit was written, masked-only (no raw).
	if len(rec.calls) != 1 || rec.calls[0].Action != audit.ActEvidenceChange {
		t.Fatalf("expected one EVIDENCE_CHANGE audit, got %+v", rec.calls)
	}
	if strings.Contains(rec.calls[0].MaskedRef, raw) {
		t.Error("audit masked_ref leaked raw number")
	}
}

// Forced-evidence gate: missing photo/number/attestation is rejected.
func TestCaptureEvidence_ForcedEvidenceGate(t *testing.T) {
	f := newFakeStore()
	svc, _ := newSvc(f)
	f.profiles["ON_RESERVE_DELIVERY"] = StatusCardProfile{
		Code: "ON_RESERVE_DELIVERY", RequiresPhoto: true, RequiresNumber: true, RequiresAttestation: true, Status: "ACTIVE",
	}
	_, err := svc.CaptureEvidence(context.Background(), EvidenceCapture{
		CardProfileCode: "ON_RESERVE_DELIVERY", RawNumber: "123456782481", Attestation: true, // no photo
	})
	if err == nil {
		t.Fatal("expected forced-evidence rejection for missing photo")
	}
}

// -- A-21 evidence audit: VIEW / DOWNLOAD -------------------------------------------

func TestEvidenceAccess_AuditsViewAndDownload(t *testing.T) {
	f := newFakeStore()
	svc, rec := newSvc(f)
	f.evidence["ev-x"] = Evidence{ID: "ev-x", NumberMask: "•••• 4821"}

	if _, _, err := svc.ViewEvidence(context.Background(), "ev-x"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.DownloadEvidence(context.Background(), "ev-x"); err != nil {
		t.Fatal(err)
	}
	var view, dl bool
	for _, c := range rec.calls {
		if c.Action == audit.ActEvidenceView {
			view = true
		}
		if c.Action == audit.ActEvidenceDownload {
			dl = true
		}
	}
	if !view || !dl {
		t.Fatalf("expected VIEW+DOWNLOAD audit rows, got %+v", rec.calls)
	}
}

// -- tcrd_expiry blocks relief ------------------------------------------------------

func TestValidateReliefEvidence_ExpiredTCRDBlocks(t *testing.T) {
	f := newFakeStore()
	svc, _ := newSvc(f)
	f.evidence["ev-exp"] = Evidence{ID: "ev-exp", NumberMask: "•••• 4821", Attestation: true, TCRDExpiry: "2026-01-01"}
	f.evidence["ev-ok"] = Evidence{ID: "ev-ok", NumberMask: "•••• 4821", Attestation: true, TCRDExpiry: "2027-01-01"}

	expired, _ := svc.ValidateReliefEvidence(context.Background(), "ev-exp", "2026-07-07")
	if expired.Eligible {
		t.Error("expired TCRD must block relief")
	}
	ok, _ := svc.ValidateReliefEvidence(context.Background(), "ev-ok", "2026-07-07")
	if !ok.Eligible {
		t.Error("valid TCRD must permit relief")
	}
}

// -- HST recon ties to GL -----------------------------------------------------------

// FW1-11: the reconcile reads the period's documents, both sides shown apart, and nets them against
// 2200's month movement. An unwired document seam refuses; it never stands in snapshots or zeros.
func TestReconcile_TiesTheMonthsDocumentsToGL(t *testing.T) {
	f := newFakeStore()
	f.salesHST = 26_000
	f.returnHST = 1_500
	f.billITCs = 4_500
	svc, _ := newSvc(f)
	svc.salesHST = f
	svc.returnHST = f
	svc.billITCs = f
	svc.glBalance = fakeGL{balances: map[string]int64{acctHSTPayable: 20_000, acctHSTFederal: 10_000, acctHSTOntario: 16_000}}

	rep, err := svc.ReconcileReport(context.Background(), 2026, 7, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.CollectedCents != 26_000 || rep.ReturnTaxCents != 1_500 || rep.InputTaxCreditsCents != 4_500 {
		t.Fatalf("the document sides must ride apart, got collected=%d returns=%d itcs=%d", rep.CollectedCents, rep.ReturnTaxCents, rep.InputTaxCreditsCents)
	}
	// 2200's movement (20000) against the documents net (26000 − 1500 − 4500): tied.
	if !rep.TiedOut || rep.VarianceCents != 0 {
		t.Fatalf("expected tie-out, got variance=%d tied=%v", rep.VarianceCents, rep.TiedOut)
	}
	if rep.GLFederalCents != 10_000 || rep.GLOntarioCents != 16_000 {
		t.Errorf("split mismatch: fed=%d on=%d", rep.GLFederalCents, rep.GLOntarioCents)
	}

	// A drift on any side surfaces as a non-zero variance.
	f.salesHST = 25_000
	rep2, _ := svc.ReconcileReport(context.Background(), 2026, 7, "")
	if rep2.TiedOut || rep2.VarianceCents != 1_000 {
		t.Fatalf("expected variance 1000, got %d tied=%v", rep2.VarianceCents, rep2.TiedOut)
	}
	f.billITCs = 5_500
	rep3, _ := svc.ReconcileReport(context.Background(), 2026, 7, "")
	if rep3.TiedOut || rep3.VarianceCents != 2_000 {
		t.Fatalf("expected variance 2000 after the ITC moves, got %d tied=%v", rep3.VarianceCents, rep3.TiedOut)
	}
	f.returnHST = 2_000
	rep4, _ := svc.ReconcileReport(context.Background(), 2026, 7, "")
	if rep4.TiedOut || rep4.VarianceCents != 2_500 {
		t.Fatalf("expected variance 2500 after the return tax moves, got %d tied=%v", rep4.VarianceCents, rep4.TiedOut)
	}
}

// An unwired document seam refuses the reconcile (fail closed); it never computes on zeros.
func TestReconcile_UnwiredSeamsRefuse(t *testing.T) {
	f := newFakeStore()
	svc, _ := newSvc(f)
	if _, err := svc.ReconcileReport(context.Background(), 2026, 7, ""); err == nil || !strings.Contains(err.Error(), "sales document HST seam") {
		t.Fatalf("an unwired sales seam must refuse, got %v", err)
	}
	svc.salesHST = f
	if _, err := svc.ReconcileReport(context.Background(), 2026, 7, ""); err == nil || !strings.Contains(err.Error(), "POS return HST seam") {
		t.Fatalf("an unwired POS return seam must refuse, got %v", err)
	}
	svc.returnHST = f
	if _, err := svc.ReconcileReport(context.Background(), 2026, 7, ""); err == nil || !strings.Contains(err.Error(), "vendor bill input tax seam") {
		t.Fatalf("an unwired ITC seam must refuse, got %v", err)
	}
}

// -- invoice carries the configured HST registration number -------------------------

func TestConfig_CarriesHSTRegistrationNo(t *testing.T) {
	f := newFakeStore()
	svc, _ := newSvc(f)
	if _, err := svc.UpdateConfig(context.Background(), Config{HSTRegistrationNo: "123456789RT0001", RoundMode: "INVOICE_TOTAL"}); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.GetConfig(context.Background(), "")
	if got.HSTRegistrationNo != "123456789RT0001" {
		t.Fatalf("hst reg no not persisted: %q", got.HSTRegistrationNo)
	}
}

// Dormant Avalara/postal cannot be enabled at W6.
func TestConfig_DormantStubsCannotBeEnabled(t *testing.T) {
	f := newFakeStore()
	svc, _ := newSvc(f)
	if _, err := svc.UpdateConfig(context.Background(), Config{AvalaraEnabled: true, RoundMode: "INVOICE_TOTAL"}); err == nil {
		t.Error("enabling Avalara must be refused at W6")
	}
	if !(AvalaraStub{}).Dormant() || !(PostalRoutingStub{}).Dormant() {
		t.Error("stubs must report dormant")
	}
}
