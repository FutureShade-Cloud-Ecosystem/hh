//go:build integration

// agreement_integration_test.go, the 0404 warranty_vendor_agreements table against a LIVE Postgres.
//
// WHY, WHEN agreement_test.go ALREADY PASSES: because the failures being guarded are DATABASE
// behaviours a fake cannot have. The CHECK constraints refusing an off-vocabulary kind, period or
// allowance, the UNIQUE vendor_id holding exactly one row per vendor, the FK turning an unknown
// vendor into a client error, and the audit rows surviving audit's deny-by-default Extra allowlist
// are all SQL facts, the same class the 0396 policy integration tests pin.
//
// Run: DATABASE_URL=… go test -tags=integration ./internal/warranty/... -run VendorAgreements -count=1
package warranty

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// oneSeededVendor picks a vendor the seed carries, so the FK holds the way production's does.
func oneSeededVendor(t *testing.T, pool *pgxpool.Pool, vendorNo string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `SELECT id::text FROM vendors WHERE vendor_no = $1`, vendorNo).Scan(&id); err != nil {
		t.Fatalf("find the seeded vendor %s: %v", vendorNo, err)
	}
	return id
}

func TestIntegrationVendorAgreements_EveryVendorAnswersTheDefaultKind(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	rows, err := repo.VendorAgreements(context.Background())
	if err != nil {
		t.Fatalf("read the agreements: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("the agreements read answered no vendors; the Dealer Admin page would render an empty card")
	}
	for _, r := range rows {
		if r.AgreementKind != AgreementKindClaimByClaim {
			t.Fatalf("vendor %s (%s) answers kind %q, want the default %q", r.VendorID, r.VendorName, r.AgreementKind, AgreementKindClaimByClaim)
		}
		if r.VendorName == "" || r.VendorNo == "" {
			t.Fatalf("vendor %s answers an empty master field (name %q no %q)", r.VendorID, r.VendorName, r.VendorNo)
		}
	}
}

func TestIntegrationVendorAgreements_UpsertRoundTripHoldsOneRowPerVendor(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	vendorID := oneSeededVendor(t, pool, "V-TECHO")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM warranty_vendor_agreements WHERE vendor_id::text = $1`, vendorID)
	})

	saved, err := repo.UpsertVendorAgreement(ctx, vendorID, AgreementInput{
		AgreementKind: AgreementKindFixedAllowance, Period: AgreementPeriodQuarterly, AllowanceBP: 100,
	}, adminUserID)
	if err != nil {
		t.Fatalf("upsert the agreement: %v", err)
	}
	if saved.AgreementKind != AgreementKindFixedAllowance || saved.Period != AgreementPeriodQuarterly || saved.AllowanceBP != 100 {
		t.Fatalf("read-back = %s %s %d, want fixed_allowance quarterly 100", saved.AgreementKind, saved.Period, saved.AllowanceBP)
	}
	if saved.UpdatedBy != adminUserID {
		t.Fatalf("updated_by = %q, want the acting admin %q", saved.UpdatedBy, adminUserID)
	}

	// The second write updates the one row in place: UNIQUE (vendor_id) holds exactly one.
	if _, err := repo.UpsertVendorAgreement(ctx, vendorID, AgreementInput{
		AgreementKind: AgreementKindPeriodicCreditNote, Period: AgreementPeriodMonthly,
	}, adminUserID); err != nil {
		t.Fatalf("re-upsert the agreement: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM warranty_vendor_agreements WHERE vendor_id::text = $1`, vendorID).Scan(&n); err != nil {
		t.Fatalf("count the vendor's rows: %v", err)
	}
	if n != 1 {
		t.Fatalf("%d agreement rows for the vendor after a re-upsert, want exactly 1 (UNIQUE vendor_id)", n)
	}
	rows, err := repo.VendorAgreements(ctx)
	if err != nil {
		t.Fatalf("re-read the agreements: %v", err)
	}
	for _, r := range rows {
		if r.VendorID == vendorID {
			continue
		}
		if r.AgreementKind != AgreementKindClaimByClaim {
			t.Fatalf("vendor %s moved to %q untouched by another vendor's write", r.VendorID, r.AgreementKind)
		}
	}
}

func TestIntegrationVendorAgreements_ChecksRefuseTheOffVocabularyValues(t *testing.T) {
	pool := intPool(t)
	vendorID := oneSeededVendor(t, pool, "V-TECHO")
	for name, stmt := range map[string]string{
		"an unknown kind": `INSERT INTO warranty_vendor_agreements (vendor_id, agreement_kind) VALUES ($1, 'yearly_cheque')`,
		"an unknown period": `INSERT INTO warranty_vendor_agreements (vendor_id, agreement_kind, period)
			VALUES ($1, 'periodic_credit_note', 'weekly')`,
		"an over range allowance": `INSERT INTO warranty_vendor_agreements (vendor_id, agreement_kind, period, allowance_bp)
			VALUES ($1, 'fixed_allowance', 'monthly', 20000)`,
		"an under range allowance": `INSERT INTO warranty_vendor_agreements (vendor_id, agreement_kind, period, allowance_bp)
			VALUES ($1, 'fixed_allowance', 'monthly', -5)`,
	} {
		if _, err := pool.Exec(context.Background(), stmt, vendorID); err == nil {
			t.Fatalf("%s was stored; the 0404 CHECK constraints did not fire", name)
		}
	}
}

func TestIntegrationVendorAgreements_UnknownVendorIsAClientError(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo)
	// The stamped caller the route's guard would deliver; the caller check sits before the write, so
	// an unstamped call answers 401 before the FK is ever consulted.
	ctx := identity.ContextWithStaff(context.Background(), identityAdmin())
	_, err := svc.UpdateAgreement(ctx, "00000000-0000-4000-9000-00000000dead", AgreementInput{
		AgreementKind: AgreementKindClaimByClaim,
	})
	if err == nil {
		t.Fatal("an unknown vendor was stored; the FK did not fire")
	}
	clientErr, ok := err.(clientError)
	if !ok || clientErr.status != 422 {
		t.Fatalf("the unknown vendor answered %v, want a 422 client error in the core's own words", err)
	}
}

func TestIntegrationVendorAgreements_AuditRowsCarryOldAndNew(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	auditSvc := audit.NewService(audit.NewRepository(pool), nil)
	svc := NewService(repo).WithAudit(auditSvc)
	ctx := context.Background()
	vendorID := oneSeededVendor(t, pool, "V-TECHO")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM warranty_vendor_agreements WHERE vendor_id::text = $1`, vendorID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_log WHERE entity_type = $1 AND entity_id = $2::uuid`, agreementEntity, vendorID)
	})

	// The SUPER_ADMIN seeded by 0043, a real users row, so actor_id takes a uuid the way production does.
	callerCtx := identity.ContextWithStaff(ctx, identityAdmin())

	if _, err := svc.UpdateAgreement(callerCtx, vendorID, AgreementInput{
		AgreementKind: AgreementKindFixedAllowance, Period: AgreementPeriodQuarterly, AllowanceBP: 150,
	}); err != nil {
		t.Fatalf("save the agreement: %v", err)
	}

	const q = `
SELECT COALESCE(changes->>'field', ''), COALESCE(changes->>'before', ''), COALESCE(changes->>'after', '')
  FROM audit_log
 WHERE action = $1 AND entity_type = $2 AND entity_id = $3::uuid
 ORDER BY seq`
	rows, err := pool.Query(ctx, q, actWarrantyAgreementChanged, agreementEntity, vendorID)
	if err != nil {
		t.Fatalf("read the audit rows: %v", err)
	}
	defer rows.Close()
	moved := map[string][2]string{}
	for rows.Next() {
		var field, before, after string
		if err := rows.Scan(&field, &before, &after); err != nil {
			t.Fatalf("scan the audit row: %v", err)
		}
		moved[field] = [2]string{before, after}
	}
	if len(moved) != 3 {
		t.Fatalf("%d audit rows filed, want 3 (kind, period, allowance); audit's Extra allowlist is "+
			"deny-by-default and drops any key it does not name", len(moved))
	}
	if got := moved["agreement_kind"]; got[0] != AgreementKindClaimByClaim || got[1] != AgreementKindFixedAllowance {
		t.Fatalf("agreement_kind before/after = %v, want %s to %s", got, AgreementKindClaimByClaim, AgreementKindFixedAllowance)
	}
	if got := moved["allowance_bp"]; got[0] != "0" || got[1] != "150" {
		t.Fatalf("allowance_bp before/after = %v, want 0 to 150", got)
	}
	// An empty before rides as an ABSENT key (audit's buildEntry skips empty strings), so the empty
	// period pre-image reads back through the COALESCE: the read side renders absent as "was empty",
	// the pos policy history's own convention.
	if got := moved["period"]; got[0] != "" || got[1] != AgreementPeriodQuarterly {
		t.Fatalf("period before/after = %v, want empty to %s", got, AgreementPeriodQuarterly)
	}
}
