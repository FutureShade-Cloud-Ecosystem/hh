package warranty

import (
	"context"
	"strings"
	"testing"
)

// agreement_test.go, the walk change 6 vendor credit agreements over the DB-free fakeStore: the
// default kind when a vendor holds no row, the closed validation the write runs in words, the
// claim_by_claim normalization, and the audit spine entry that records each moved knob the way the
// threshold's save does (one row per moved knob, nothing when nothing moved).

// agreementVendors is the fake's vendor roster: the four demo suppliers the boards name, already in
// the name order the read answers.
var agreementVendors = []struct{ id, no, name string }{
	{"11111111-1111-4111-8111-000000000001", "V-COPPER", "Copperline Pavers"},
	{"22222222-2222-4222-8222-000000000002", "V-GRANITE", "Granite Run Quarry"},
	{"33333333-3333-4333-8333-000000000003", "V-HERON", "Heronfield Stone"},
	{"44444444-4444-4444-8444-000000000004", "V-PRAIRIE", "Prairie Bend Soils"},
}

// newAgreementService wires the fake store with the four vendor roster rows and an admin caller.
// A write rides ctxWith(identityAdmin()); the service reads the stamped caller for updated_by and
// the audit row, never a test hook on the resolver itself.
func newAgreementService(rec *recordingAudit) (*Service, *fakeStore) {
	f := newFakeStore()
	f.vendorRoster = map[string]VendorAgreement{}
	for _, v := range agreementVendors {
		f.vendorRoster[v.id] = VendorAgreement{VendorID: v.id, VendorNo: v.no, VendorName: v.name}
	}
	return NewService(nil).withStore(f).withCaller(testCallerFrom).WithAudit(rec), f
}

func adminWrite(ctx context.Context, svc *Service, vendorID string, in AgreementInput) (VendorAgreement, error) {
	return svc.UpdateAgreement(ctxWith(identityAdmin()), vendorID, in)
}

// - fake store agreement methods -

// effective resolves the stored row the way the real LEFT JOIN does: a vendor with no row (or an
// empty kind) answers the default kind, the period and allowance cleared with it.
func (f *fakeStore) effective(v VendorAgreement) VendorAgreement {
	row, ok := f.agreements[v.VendorID]
	if !ok || row.AgreementKind == "" {
		return VendorAgreement{VendorID: v.VendorID, VendorNo: v.VendorNo, VendorName: v.VendorName, AgreementKind: AgreementKindClaimByClaim}
	}
	period, bp := "", 0
	if row.AgreementKind != AgreementKindClaimByClaim {
		period, bp = row.Period, row.AllowanceBP
	}
	return VendorAgreement{VendorID: v.VendorID, VendorNo: v.VendorNo, VendorName: v.VendorName, AgreementKind: row.AgreementKind, Period: period, AllowanceBP: bp}
}

func (f *fakeStore) VendorAgreements(_ context.Context) ([]VendorAgreement, error) {
	if err := f.fail("VendorAgreements"); err != nil {
		return nil, err
	}
	rows := make([]VendorAgreement, 0, len(f.vendorRoster))
	for _, v := range f.vendorRoster {
		rows = append(rows, f.effective(v))
	}
	// The read answers in vendor name order.
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rows[j].VendorName < rows[j-1].VendorName; j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
	return rows, nil
}

func (f *fakeStore) VendorAgreement(_ context.Context, vendorID string) (VendorAgreement, error) {
	if err := f.fail("VendorAgreement"); err != nil {
		return VendorAgreement{}, err
	}
	v, ok := f.vendorRoster[vendorID]
	if !ok {
		return VendorAgreement{}, notFound("warranty: unknown vendor %q", vendorID)
	}
	return f.effective(v), nil
}

func (f *fakeStore) UpsertVendorAgreement(_ context.Context, vendorID string, in AgreementInput, updatedBy string) (VendorAgreement, error) {
	if err := f.fail("UpsertVendorAgreement"); err != nil {
		return VendorAgreement{}, err
	}
	v, ok := f.vendorRoster[vendorID]
	if !ok {
		// The real repository maps the FK to this same 422; the fake answers it directly.
		return VendorAgreement{}, unprocessable("warranty: unknown vendor %q", vendorID)
	}
	f.agreements[vendorID] = in
	saved := f.effective(v)
	saved.UpdatedBy = updatedBy
	return saved, nil
}

func TestAgreementListAnswersEveryVendorWithTheDefaultKind(t *testing.T) {
	svc, _ := newAgreementService(&recordingAudit{})
	rows, err := svc.Agreements(context.Background())
	if err != nil {
		t.Fatalf("read the agreements: %v", err)
	}
	if len(rows) != len(agreementVendors) {
		t.Fatalf("the list answered %d rows, want %d", len(rows), len(agreementVendors))
	}
	for _, r := range rows {
		if r.AgreementKind != AgreementKindClaimByClaim {
			t.Fatalf("vendor %s answers kind %q, want the default %q", r.VendorID, r.AgreementKind, AgreementKindClaimByClaim)
		}
		if r.Period != "" || r.AllowanceBP != 0 {
			t.Fatalf("vendor %s answers period %q bp %d, want the defaults cleared", r.VendorID, r.Period, r.AllowanceBP)
		}
		if r.VendorName == "" {
			t.Fatalf("vendor %s answers no name; the screen's row would be blank", r.VendorID)
		}
	}
}

func TestAgreementPutStoresKindPeriodAndAllowance(t *testing.T) {
	rec := &recordingAudit{}
	svc, f := newAgreementService(rec)
	heron := agreementVendors[2].id
	saved, err := adminWrite(context.Background(), svc, heron, AgreementInput{
		AgreementKind: AgreementKindFixedAllowance, Period: AgreementPeriodQuarterly, AllowanceBP: 100,
	})
	if err != nil {
		t.Fatalf("save the fixed allowance: %v", err)
	}
	if saved.AgreementKind != AgreementKindFixedAllowance || saved.Period != AgreementPeriodQuarterly || saved.AllowanceBP != 100 {
		t.Fatalf("read-back = %s %s %d, want fixed_allowance quarterly 100", saved.AgreementKind, saved.Period, saved.AllowanceBP)
	}
	if f.agreements[heron].AllowanceBP != 100 {
		t.Fatalf("the store holds %d bp, want 100", f.agreements[heron].AllowanceBP)
	}
}

func TestAgreementPutRefusesAnUnknownKind(t *testing.T) {
	svc, f := newAgreementService(&recordingAudit{})
	copper := agreementVendors[0].id
	_, err := adminWrite(context.Background(), svc, copper, AgreementInput{AgreementKind: "yearly_cheque"})
	if err == nil {
		t.Fatal("an unknown agreement kind was stored")
	}
	if !strings.Contains(err.Error(), "agreement_kind") {
		t.Fatalf("the refusal %q does not name the knob", err.Error())
	}
	if _, stored := f.agreements[copper]; stored {
		t.Fatalf("a refused write stored the row %+v", f.agreements[copper])
	}
}

func TestAgreementPutRefusesAPeriodlessPeriodicOrFixedAgreement(t *testing.T) {
	svc, _ := newAgreementService(&recordingAudit{})
	for _, kind := range []string{AgreementKindPeriodicCreditNote, AgreementKindFixedAllowance} {
		_, err := adminWrite(context.Background(), svc, agreementVendors[0].id, AgreementInput{AgreementKind: kind})
		if err == nil {
			t.Fatalf("%s without a period was stored", kind)
		}
		if !strings.Contains(err.Error(), "period") {
			t.Fatalf("the %s refusal %q does not name the period", kind, err.Error())
		}
		_, err = adminWrite(context.Background(), svc, agreementVendors[0].id, AgreementInput{AgreementKind: kind, Period: "weekly"})
		if err == nil {
			t.Fatalf("%s with the off-vocabulary period %q was stored", kind, "weekly")
		}
	}
}

func TestAgreementPutRefusesAnOutOfRangeAllowance(t *testing.T) {
	svc, _ := newAgreementService(&recordingAudit{})
	for _, bp := range []int{-1, 10001} {
		_, err := adminWrite(context.Background(), svc, agreementVendors[0].id, AgreementInput{
			AgreementKind: AgreementKindFixedAllowance, Period: AgreementPeriodMonthly, AllowanceBP: bp,
		})
		if err == nil {
			t.Fatalf("a fixed allowance of %d bp was stored", bp)
		}
		if !strings.Contains(err.Error(), "allowance_bp") {
			t.Fatalf("the %d bp refusal %q does not name the knob", bp, err.Error())
		}
	}
}

func TestAgreementPutClearsThePeriodAndAllowanceForClaimByClaim(t *testing.T) {
	svc, f := newAgreementService(&recordingAudit{})
	prairie := agreementVendors[3].id
	if _, err := adminWrite(context.Background(), svc, prairie, AgreementInput{
		AgreementKind: AgreementKindPeriodicCreditNote, Period: AgreementPeriodMonthly,
	}); err != nil {
		t.Fatalf("save the periodic agreement: %v", err)
	}
	// A stale client still riding the old period and allowance on a claim_by_claim write lands in a
	// sane row: the kind is the rule, the other two carry nothing on their own.
	saved, err := adminWrite(context.Background(), svc, prairie, AgreementInput{
		AgreementKind: AgreementKindClaimByClaim, Period: AgreementPeriodMonthly, AllowanceBP: 100,
	})
	if err != nil {
		t.Fatalf("save the claim by claim agreement: %v", err)
	}
	if saved.AgreementKind != AgreementKindClaimByClaim || saved.Period != "" || saved.AllowanceBP != 0 {
		t.Fatalf("read-back = %s %s %d, want claim_by_claim with the period and allowance cleared", saved.AgreementKind, saved.Period, saved.AllowanceBP)
	}
	if held := f.agreements[prairie]; held.Period != "" || held.AllowanceBP != 0 {
		t.Fatalf("the store holds %s %d, want the period and allowance cleared", held.Period, held.AllowanceBP)
	}
}

func TestAgreementChangeFilesOneAuditRowPerMovedKnob(t *testing.T) {
	rec := &recordingAudit{}
	svc, _ := newAgreementService(rec)
	heron := agreementVendors[2].id
	if _, err := adminWrite(context.Background(), svc, heron, AgreementInput{
		AgreementKind: AgreementKindFixedAllowance, Period: AgreementPeriodQuarterly, AllowanceBP: 100,
	}); err != nil {
		t.Fatalf("save the agreement: %v", err)
	}
	if rec.count() != 3 {
		t.Fatalf("a first save from the defaults filed %d audit rows, want 3 (kind, period, allowance)", rec.count())
	}
	seen := map[string]bool{}
	for _, row := range rec.rows {
		if row.Action != actWarrantyAgreementChanged {
			t.Fatalf("audit action = %q, want %q", row.Action, actWarrantyAgreementChanged)
		}
		if row.EntityType != agreementEntity || row.EntityID != heron {
			t.Fatalf("audit row files entity %s/%s, want %s/%s", row.EntityType, row.EntityID, agreementEntity, heron)
		}
		field, _ := row.Extra["field"].(string)
		seen[field] = true
		if role, _ := row.Extra["actor_role"].(string); role == "" {
			t.Fatalf("the audit row for %q carries no actor_role", field)
		}
	}
	for _, field := range []string{"agreement_kind", "period", "allowance_bp"} {
		if !seen[field] {
			t.Fatalf("no audit row names the moved knob %q (rows: %v)", field, seen)
		}
	}
}

func TestAgreementSaveThatMovedNothingFilesNoAuditRow(t *testing.T) {
	rec := &recordingAudit{}
	svc, _ := newAgreementService(rec)
	granite := agreementVendors[1].id
	in := AgreementInput{AgreementKind: AgreementKindPeriodicCreditNote, Period: AgreementPeriodMonthly}
	if _, err := adminWrite(context.Background(), svc, granite, in); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if rec.count() != 2 {
		t.Fatalf("the first save filed %d rows, want 2 (kind and period)", rec.count())
	}
	if _, err := adminWrite(context.Background(), svc, granite, in); err != nil {
		t.Fatalf("second save: %v", err)
	}
	if rec.count() != 2 {
		t.Fatalf("a save that moved nothing filed more rows (%d), want none", rec.count())
	}
}

func TestAgreementWriteRequiresTheStampedCaller(t *testing.T) {
	svc, _ := newAgreementService(&recordingAudit{})
	_, err := svc.UpdateAgreement(context.Background(), agreementVendors[0].id, AgreementInput{AgreementKind: AgreementKindClaimByClaim})
	if err == nil {
		t.Fatal("a write without a stamped caller was stored; updated_by would be an empty attribution")
	}
}
