//go:build integration

package pricing

// DIB-402 integration gates: the drill-through reference must survive the round trip, service
// write -> pricing_audit_log row -> ListAudit read -> JSON projection, and the migration's
// backfill must derive the same answer for rows written BEFORE the columns existed.
//
// Run with: DATABASE_URL=... go test -tags integration -count=1 ./internal/pricing/
//
// FIXTURE DISCIPLINE: every test here deletes exactly the rows it created (audit rows by id, config
// rows by id) in a t.Cleanup, so the suite is order-independent and leaves pricing_audit_log at the
// count it found. Nothing truncates the table, an audit log that a test can wipe is not an audit log.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// refPtrI64 is the local *int64 helper these fixtures need (the shared ptrI64 lives in a
// non-integration test file, which this build tag excludes).
func refPtrI64(v int64) *int64 { return &v }

// auditRefFixture grabs a seeded product + its category + a customer, and records the audit-row
// count so a test can assert on exactly the rows it caused.
type auditRefFixture struct {
	productID  string
	categoryID string
	customerID string
	baseUOM    string
}

func newAuditRefFixture(t *testing.T, pool *pgxpool.Pool) auditRefFixture {
	t.Helper()
	ctx := context.Background()
	var f auditRefFixture
	if err := pool.QueryRow(ctx, `
		SELECT p.id::text, p.category_id::text, p.base_uom::text
		  FROM products p JOIN product_categories c ON c.id = p.category_id
		 WHERE c.code='PAVER' LIMIT 1`).Scan(&f.productID, &f.categoryID, &f.baseUOM); err != nil {
		t.Skipf("no PAVER product seeded: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM customers WHERE customer_type='CONTRACTOR' LIMIT 1`).Scan(&f.customerID); err != nil {
		t.Skipf("no CONTRACTOR customer seeded: %v", err)
	}
	return f
}

// cleanupAudit removes the audit rows a test caused, keyed on the config row they describe.
func cleanupAudit(t *testing.T, pool *pgxpool.Pool, entityType, entityID string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM pricing_audit_log WHERE entity_type=$1 AND entity_id=$2`, entityType, entityID)
	})
}

// DIB-402 · the write path. A CUSTOMER_PRODUCT rule created through the SERVICE must land an audit
// row whose ref pair names the PRODUCT, not the rule id, and not the customer.
//
// This is the mutation target for the entity-ref write: break ruleRef (or drop RefEntityType from
// the CreateRule AppendAudit call) and this test fails on the ref assertion.
func TestIntegration_DIB402_RuleCreateWritesProductRef(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	f := newAuditRefFixture(t, pool)
	svc := realSvc(pool)

	out, err := svc.CreateRule(ctx, Rule{
		Scope:      ScopeCustomerProduct,
		CustomerID: f.customerID,
		ProductID:  f.productID,
		CalcType:   CalcAbsolute,
		PriceCents: refPtrI64(4321),
		PriceUOM:   f.baseUOM,
		Priority:   77,
		DetailText: "IT DIB-402: drill-through ref on create",
	}, "")
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM pricing_rules WHERE id=$1`, out.ID) })
	cleanupAudit(t, pool, "pricing_rule", out.ID)

	var refType, refID string
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(ref_entity_type,''), COALESCE(ref_entity_id::text,'')
		  FROM pricing_audit_log
		 WHERE entity_type='pricing_rule' AND entity_id=$1
		 ORDER BY created_at DESC LIMIT 1`, out.ID).Scan(&refType, &refID); err != nil {
		t.Fatalf("no audit row for the created rule: %v", err)
	}
	if refType != RefProduct {
		t.Fatalf("a CUSTOMER_PRODUCT rule must file its audit row under the PRODUCT, got ref_entity_type=%q", refType)
	}
	if refID != f.productID {
		t.Fatalf("ref_entity_id must be the product %s, got %s", f.productID, refID)
	}

	// …and the read path must hand the pair to the viewer. A column that is written but not
	// SELECTed is indistinguishable from one that was never written, from the UI's side of the wire.
	rows, err := NewRepository(pool).ListAudit(ctx, AuditFilter{EntityType: "pricing_rule", EntityID: out.ID, Limit: 10})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("ListAudit returned nothing for a rule that was just created")
	}
	if rows[0].RefEntityType != RefProduct || rows[0].RefEntityID != f.productID {
		t.Fatalf("ListAudit dropped the drill-through ref: got (%q,%q), want (%q,%q)",
			rows[0].RefEntityType, rows[0].RefEntityID, RefProduct, f.productID)
	}
}

// DIB-402 · a rule with NO navigable record writes NO reference. The viewer renders these plainly.
// Without this test the "always populate a ref" temptation would pass every other gate here.
func TestIntegration_DIB402_TierDefaultRuleWritesNoRef(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := realSvc(pool)

	out, err := svc.CreateRule(ctx, Rule{
		Scope:        ScopeTierDefault,
		CustomerType: "GOVERNMENT",
		CalcType:     CalcPctOffList,
		DiscountBps:  refPtrI64(1234),
		PriceUOM:     "EA",
		Priority:     93,
		DetailText:   "IT DIB-402: policy-shaped rule, no record to open",
	}, "")
	if err != nil {
		t.Fatalf("create TIER_DEFAULT rule: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM pricing_rules WHERE id=$1`, out.ID) })
	cleanupAudit(t, pool, "pricing_rule", out.ID)

	var refType, refID string
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(ref_entity_type,''), COALESCE(ref_entity_id::text,'')
		  FROM pricing_audit_log
		 WHERE entity_type='pricing_rule' AND entity_id=$1
		 ORDER BY created_at DESC LIMIT 1`, out.ID).Scan(&refType, &refID); err != nil {
		t.Fatalf("no audit row for the created TIER_DEFAULT rule: %v", err)
	}
	if refType != "" || refID != "" {
		t.Fatalf("a TIER_DEFAULT rule must write NO drill-through ref, got (%q,%q)", refType, refID)
	}
}

// DIB-402 · the DB refuses a half-set pair outright. This pins the CHECK from migration 0098, which
// is what makes normaliseAuditRef's defensive drop load-bearing rather than decorative.
func TestIntegration_DIB402_HalfSetRefIsRejectedByTheDatabase(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	f := newAuditRefFixture(t, pool)

	if _, err := pool.Exec(ctx, `
		INSERT INTO pricing_audit_log (entity_type, entity_id, action, ref_entity_type)
		VALUES ('pricing_rule', $1, 'CREATE', 'product')`, f.productID); err == nil {
		_, _ = pool.Exec(ctx, `DELETE FROM pricing_audit_log WHERE entity_id=$1 AND ref_entity_id IS NULL AND ref_entity_type='product'`, f.productID)
		t.Fatal("a ref_entity_type with no ref_entity_id must violate pricing_audit_ref_pair")
	}
	// W4-A: 'quote' USED to be the unknown kind here. Migration 0114 widened
	// pricing_audit_ref_type_known to admit 'quote' and 'order' once ResolveInput gained a document
	// ref, so the probe moved to a kind that is still genuinely outside the set. The CHECK itself is
	// unchanged in nature, it is still a closed set, just a larger one.
	if _, err := pool.Exec(ctx, `
		INSERT INTO pricing_audit_log (entity_type, entity_id, action, ref_entity_type, ref_entity_id)
		VALUES ('pricing_rule', $1, 'CREATE', 'invoice', $1)`, f.productID); err == nil {
		_, _ = pool.Exec(ctx, `DELETE FROM pricing_audit_log WHERE entity_id=$1 AND ref_entity_type='invoice'`, f.productID)
		t.Fatal("an unknown ref_entity_type must violate pricing_audit_ref_type_known")
	}
	// And the widening is REAL, not just permissive parsing: a quote ref now persists.
	if _, err := pool.Exec(ctx, `
		INSERT INTO pricing_audit_log (entity_type, entity_id, action, ref_entity_type, ref_entity_id)
		VALUES ('pricing_rule', $1, 'CREATE', 'quote', $1)`, f.productID); err != nil {
		t.Fatalf("0114 must admit a quote ref: %v", err)
	}
	_, _ = pool.Exec(ctx, `DELETE FROM pricing_audit_log WHERE entity_id=$1 AND ref_entity_type='quote'`, f.productID)
}

// DIB-402 · AppendAudit must still write the ROW when the reference is unusable. This is the
// consequence of the fire-and-forget call sites, tested end to end rather than argued about.
func TestIntegration_DIB402_UnusableRefStillWritesTheRow(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	f := newAuditRefFixture(t, pool)
	repo := NewRepository(pool)
	cleanupAudit(t, pool, "pricing_rule", f.productID)

	// 'invoice' is not a known ref type (and would trip the CHECK). The row must survive anyway,
	// minus its link. ('quote' was the example here until W4-A made it a REAL kind, see 0114.)
	if err := repo.AppendAudit(ctx, AuditEntry{
		EntityType: "pricing_rule", EntityID: f.productID, Action: "UPDATE",
		FieldName: "price_cents", OldValue: "1", NewValue: "2",
		RefEntityType: "invoice", RefEntityID: f.productID,
	}); err != nil {
		t.Fatalf("AppendAudit must not fail because a reference was unusable: %v", err)
	}

	var n int
	var refType string
	if err := pool.QueryRow(ctx, `
		SELECT count(*), COALESCE(max(ref_entity_type),'')
		  FROM pricing_audit_log WHERE entity_type='pricing_rule' AND entity_id=$1`, f.productID).Scan(&n, &refType); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if n != 1 {
		t.Fatalf("the audit row must be written even with a bad reference; found %d rows", n)
	}
	if refType != "" {
		t.Fatalf("the unusable reference must be dropped, not written; got %q", refType)
	}
}

// DIB-402 · the MIGRATION's backfill, re-run over deliberately legacy-shaped rows (refs NULL, as
// every row written before 0098 is). This proves the derivation in the migration agrees with the
// derivation in Go, the two could silently drift, and then a backfilled row and a freshly-written
// row for the same change would point at different records.
func TestIntegration_DIB402_BackfillDerivesTheSameTargets(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	f := newAuditRefFixture(t, pool)

	// A product-scoped rule and a category policy: one derived through a join, one where entity_id
	// IS the target.
	var ruleID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO pricing_rules (scope, customer_id, product_id, calc_type, price_cents, price_uom, priority, detail_text)
		VALUES ('CUSTOMER_PRODUCT',$1,$2,'ABSOLUTE',9999,$3,78,'IT DIB-402 backfill')
		RETURNING id::text`, f.customerID, f.productID, f.baseUOM).Scan(&ruleID); err != nil {
		t.Fatalf("seed rule: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM pricing_rules WHERE id=$1`, ruleID) })

	// Legacy-shaped audit rows: written directly, refs left NULL.
	for _, r := range []struct{ et, eid string }{
		{"pricing_rule", ruleID},
		{"category_policy", f.categoryID},
		{"margin_enforcement", f.productID},
	} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO pricing_audit_log (entity_type, entity_id, action, field_name)
			VALUES ($1, $2, 'UPDATE', 'IT-DIB402-backfill')`, r.et, r.eid); err != nil {
			t.Fatalf("seed legacy audit row (%s): %v", r.et, err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM pricing_audit_log WHERE field_name='IT-DIB402-backfill'`)
	})

	// The migration's statements, verbatim in shape, scoped to this test's rows.
	backfill := []string{
		`UPDATE pricing_audit_log a SET ref_entity_type='product', ref_entity_id=r.product_id
		   FROM pricing_rules r
		  WHERE a.ref_entity_type IS NULL AND a.field_name='IT-DIB402-backfill'
		    AND a.entity_type='pricing_rule' AND r.id=a.entity_id AND r.product_id IS NOT NULL`,
		`UPDATE pricing_audit_log a SET ref_entity_type='category', ref_entity_id=a.entity_id
		   FROM product_categories c
		  WHERE a.ref_entity_type IS NULL AND a.field_name='IT-DIB402-backfill'
		    AND a.entity_type='category_policy' AND c.id=a.entity_id`,
		`UPDATE pricing_audit_log a SET ref_entity_type='product', ref_entity_id=a.entity_id
		   FROM products p
		  WHERE a.ref_entity_type IS NULL AND a.field_name='IT-DIB402-backfill'
		    AND a.entity_type='margin_enforcement' AND p.id=a.entity_id`,
	}
	for _, q := range backfill {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("backfill statement failed: %v", err)
		}
	}

	want := map[string][2]string{
		"pricing_rule":       {RefProduct, f.productID},
		"category_policy":    {RefCategory, f.categoryID},
		"margin_enforcement": {RefProduct, f.productID},
	}
	rows, err := pool.Query(ctx, `
		SELECT entity_type, COALESCE(ref_entity_type,''), COALESCE(ref_entity_id::text,'')
		  FROM pricing_audit_log WHERE field_name='IT-DIB402-backfill'`)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var et, rt, rid string
		if err := rows.Scan(&et, &rt, &rid); err != nil {
			t.Fatalf("scan: %v", err)
		}
		w, ok := want[et]
		if !ok {
			t.Fatalf("unexpected entity_type %q in the fixture set", et)
		}
		if rt != w[0] || rid != w[1] {
			t.Fatalf("backfill for %s derived (%q,%q), want (%q,%q)", et, rt, rid, w[0], w[1])
		}
		seen++
	}
	if seen != len(want) {
		t.Fatalf("expected %d backfilled rows, saw %d", len(want), seen)
	}
}
