//go:build integration

// vendorload_integration_test.go proves the load doors' supplier writer
// (HH-LOAD-1 LD-4) through the frame: the vendor record with its 0011
// columns (name, terms, the EDI flag, the remit address, the active
// flag), the ordering contact through the module's own contact store
// (0197, one row per vendor, reachable or refused), the stamps a loaded
// row carries (org, seq from the vendors scope, idempotency key), the
// replay arms, and the attach to a vendor a staff member already made.
// The vendor number is the source's natural key: the legacy id itself.
//
// Fixtures are invented: the FXV numbers, the org and actor ids, the
// contact addresses. The fixture rows leave with the test.
//
// Run (the database URL rides the command line, never a default):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5625/ld4doors?sslmode=disable' \
//	  go test -tags integration ./internal/purchasing/... -count=1 -run TestVendorLoad
package purchasing

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The supplier door's fixture constants, invented.
const (
	fxVendorOrgID   = "4442a800-0000-4000-8000-00000000e001"
	fxVendorActorID = "4442a800-0000-4000-8000-00000000e00a"
	fxVendorSource  = "testsuite"
)

// loadAuditSpy captures the audit rows the frame writes.
type loadAuditSpy struct {
	mu   sync.Mutex
	rows int
}

func (s *loadAuditSpy) RecordBatchTx(_ context.Context, _ pgx.Tx, _ load.BatchAuditRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows++
	return nil
}

// vendorLoadEnv is one test's fixtures.
type vendorLoadEnv struct {
	pool *pgxpool.Pool
	door *load.Door
	spy  *loadAuditSpy
}

func vendorLoadSetup(t *testing.T) *vendorLoadEnv {
	t.Helper()
	pool := intPool(t)
	ctx := context.Background()
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM load_key_registry WHERE org_id = $1::uuid`, []any{fxVendorOrgID}},
		{`DELETE FROM load_batch WHERE org_id = $1::uuid`, []any{fxVendorOrgID}},
		{`DELETE FROM vendors WHERE vendor_no LIKE 'FXV-%'`, nil},
		{`DELETE FROM scope_sequences WHERE scope = 'vendors' AND next_value < 1000`, nil},
	} {
		if _, err := pool.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("vendor fixture clean: %v", err)
		}
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM load_key_registry WHERE org_id = $1::uuid`, fxVendorOrgID)
		_, _ = pool.Exec(ctx, `DELETE FROM load_batch WHERE org_id = $1::uuid`, fxVendorOrgID)
		_, _ = pool.Exec(ctx, `DELETE FROM vendors WHERE vendor_no LIKE 'FXV-%'`)
		pool.Close()
	})
	spy := &loadAuditSpy{}
	store := load.NewStore(pool)
	return &vendorLoadEnv{
		pool: pool,
		door: load.NewDoor(store, "supplier", VendorLoadWriter{}, spy),
		spy:  spy,
	}
}

// vendItem is one supplier batch item. The legacy id is the vendor
// number, the source's natural key as text; the derived key is a
// canonical fixture uuid keyed off it.
func vendItem(legacyID, name string, extra map[string]any) load.Item {
	rec := map[string]any{"name": name}
	for k, v := range extra {
		rec[k] = v
	}
	return load.Item{LegacyID: legacyID, DerivedKey: vendDerivedKey(legacyID), Record: rec}
}

// vendDerivedKey maps a fixture legacy id onto a canonical uuid.
func vendDerivedKey(legacyID string) string {
	suffix := map[string]string{
		"FXV-0001": "0000000000e1",
		"FXV-0002": "0000000000e2",
		"FXV-0008": "0000000000e8",
		"FXV-0009": "0000000000e9",
	}[legacyID]
	return "e1000000-0000-4000-8000-" + suffix
}

// vendRow reads one fixture vendor row's id, terms, org, seq and key.
func (e *vendorLoadEnv) vendRow(t *testing.T, vendorNo string) (id, name string, terms int, org string, seq int64, key string) {
	t.Helper()
	err := e.pool.QueryRow(context.Background(), `
		SELECT id::text, name, payment_terms_days, coalesce(org_id::text, ''),
		       coalesce(seq, 0), coalesce(idempotency_key::text, '')
		FROM vendors WHERE vendor_no = $1`, vendorNo).
		Scan(&id, &name, &terms, &org, &seq, &key)
	if err != nil {
		t.Fatalf("vendor row %s: %v", vendorNo, err)
	}
	return id, name, terms, org, seq, key
}

// vendContact reads one vendor's contact row.
func (e *vendorLoadEnv) vendContact(t *testing.T, vendorID string) (name, email, phone string) {
	t.Helper()
	err := e.pool.QueryRow(context.Background(), `
		SELECT contact_name, email, phone FROM purchasing_vendor_contacts WHERE vendor_id = $1::uuid`, vendorID).
		Scan(&name, &email, &phone)
	if err != nil {
		t.Fatalf("vendor contact %s: %v", vendorID, err)
	}
	return name, email, phone
}

func vendorOutcome(t *testing.T, r load.BatchResult, legacyID string) load.Outcome {
	t.Helper()
	for _, o := range r.Items {
		if o.LegacyID == legacyID {
			return o
		}
	}
	t.Fatalf("batch carries no outcome for %s: %+v", legacyID, r.Items)
	return load.Outcome{}
}

func TestVendorLoadUpsertsThe0011ShapeWithItsContact(t *testing.T) {
	e := vendorLoadSetup(t)
	ctx := context.Background()
	r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxVendorOrgID, SourceSystem: fxVendorSource,
		BatchID: "b-vend-1", Caller: fxVendorActorID,
		Items: []load.Item{vendItem("FXV-0001", "Fixture Supplier One", map[string]any{
			"payment_terms_days": 45,
			"is_edi":             true,
			"remit_address":      map[string]any{"line1": "Fixture Remit Line", "city": "Fixtureville"},
			"contact": map[string]any{
				"contact_name": "Fixture Order Desk",
				"email":        "desk@example.invalid",
			},
		})}})
	if err != nil {
		t.Fatalf("vendor batch: %v", err)
	}
	o := vendorOutcome(t, r, "FXV-0001")
	if o.Status != load.OutcomeCreated {
		t.Fatalf("the first sight answered %+v, want created", o)
	}
	id, name, terms, org, seq, key := e.vendRow(t, "FXV-0001")
	if name != "Fixture Supplier One" || terms != 45 {
		t.Fatalf("the row carries name %q terms %d, want the record's", name, terms)
	}
	if org != fxVendorOrgID || seq == 0 || key == "" {
		t.Fatalf("the loaded vendor carries org %q seq %d key %q, want the load stamps", org, seq, key)
	}
	var remit map[string]any
	if err := e.pool.QueryRow(ctx,
		`SELECT remit_address::jsonb FROM vendors WHERE id = $1::uuid`, id).Scan(&remit); err != nil {
		t.Fatalf("remit read: %v", err)
	}
	if remit["city"] != "Fixtureville" {
		t.Fatalf("the remit address is %v, want the record's", remit)
	}
	cname, email, phone := e.vendContact(t, id)
	if cname != "Fixture Order Desk" || email != "desk@example.invalid" || phone != "" {
		t.Fatalf("the contact row carries %q %q %q, want the record's desk and email", cname, email, phone)
	}

	// The identical replay answers unchanged; a changed term updates and
	// keeps the contact.
	r, err = e.door.UpsertBatch(ctx, load.Batch{OrgID: fxVendorOrgID, SourceSystem: fxVendorSource,
		BatchID: "b-vend-2", Caller: fxVendorActorID,
		Items: []load.Item{vendItem("FXV-0001", "Fixture Supplier One", map[string]any{
			"payment_terms_days": 45,
			"is_edi":             true,
			"remit_address":      map[string]any{"line1": "Fixture Remit Line", "city": "Fixtureville"},
			"contact": map[string]any{
				"contact_name": "Fixture Order Desk",
				"email":        "desk@example.invalid",
			},
		})}})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if o := vendorOutcome(t, r, "FXV-0001"); o.Status != load.OutcomeUnchanged {
		t.Fatalf("the identical replay answered %q, want unchanged", o.Status)
	}
	r, err = e.door.UpsertBatch(ctx, load.Batch{OrgID: fxVendorOrgID, SourceSystem: fxVendorSource,
		BatchID: "b-vend-3", Caller: fxVendorActorID,
		Items: []load.Item{vendItem("FXV-0001", "Fixture Supplier One", map[string]any{
			"payment_terms_days": 60,
			"contact": map[string]any{
				"contact_name": "Fixture Order Desk",
				"email":        "desk@example.invalid",
			},
		})}})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if o := vendorOutcome(t, r, "FXV-0001"); o.Status != load.OutcomeUpdated {
		t.Fatalf("the changed term answered %q, want updated", o.Status)
	}
	_, _, terms, _, _, _ = e.vendRow(t, "FXV-0001")
	if terms != 60 {
		t.Fatalf("the terms stand at %d, want the update's 60", terms)
	}
	_, email, _ = e.vendContact(t, id)
	if email != "desk@example.invalid" {
		t.Fatalf("the update moved the contact email to %q, want it kept", email)
	}
}

func TestVendorLoadRefusesAnUnreachableContactNamingTheField(t *testing.T) {
	e := vendorLoadSetup(t)
	r, err := e.door.UpsertBatch(context.Background(), load.Batch{OrgID: fxVendorOrgID, SourceSystem: fxVendorSource,
		BatchID: "b-vend-u1", Caller: fxVendorActorID,
		Items: []load.Item{vendItem("FXV-0009", "Fixture Supplier Nine", map[string]any{
			"contact": map[string]any{"contact_name": "Fixture Desk"},
		})}})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	o := vendorOutcome(t, r, "FXV-0009")
	if o.Status != load.OutcomeRefused || o.Reason != "contact_unreachable" {
		t.Fatalf("an unreachable contact answered %+v, want refused as contact_unreachable", o)
	}
	if o.Field != "contact" {
		t.Fatalf("the refusal names field %q, want contact", o.Field)
	}
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM vendors WHERE vendor_no = 'FXV-0009'`).Scan(&n); err != nil {
		t.Fatalf("refused row read: %v", err)
	}
	if n != 0 {
		t.Fatalf("the refused item wrote %d vendor rows, want none", n)
	}
}

func TestVendorLoadRefusesAMissingName(t *testing.T) {
	e := vendorLoadSetup(t)
	r, err := e.door.UpsertBatch(context.Background(), load.Batch{OrgID: fxVendorOrgID, SourceSystem: fxVendorSource,
		BatchID: "b-vend-n1", Caller: fxVendorActorID,
		Items: []load.Item{vendItem("FXV-0008", "", nil)}})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	o := vendorOutcome(t, r, "FXV-0008")
	if o.Status != load.OutcomeRefused || o.Reason != "field_required" || o.Field != "name" {
		t.Fatalf("a nameless vendor answered %+v, want refused as field_required on name", o)
	}
}

func TestVendorLoadAttachesARowAStaffMemberAlreadyMade(t *testing.T) {
	e := vendorLoadSetup(t)
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO vendors (vendor_no, name) VALUES ('FXV-0002', 'Planted By Staff')`); err != nil {
		t.Fatalf("plant vendor: %v", err)
	}
	r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxVendorOrgID, SourceSystem: fxVendorSource,
		BatchID: "b-vend-a1", Caller: fxVendorActorID,
		Items: []load.Item{vendItem("FXV-0002", "Planted By Staff", nil)}})
	if err != nil {
		t.Fatalf("attach batch: %v", err)
	}
	if o := vendorOutcome(t, r, "FXV-0002"); o.Status != load.OutcomeCreated {
		t.Fatalf("the attach answered %+v, want created", o)
	}
	var n int
	if err := e.pool.QueryRow(ctx,
		`SELECT count(*) FROM vendors WHERE vendor_no = 'FXV-0002'`).Scan(&n); err != nil {
		t.Fatalf("attach count: %v", err)
	}
	if n != 1 {
		t.Fatalf("%d rows carry the vendor number, want the one attached row", n)
	}
	_, _, _, org, seq, _ := e.vendRow(t, "FXV-0002")
	if org != fxVendorOrgID || seq == 0 {
		t.Fatalf("the attached vendor carries org %q seq %d, want the load stamps", org, seq)
	}
	// The identical replay of an attached row answers unchanged.
	r, err = e.door.UpsertBatch(ctx, load.Batch{OrgID: fxVendorOrgID, SourceSystem: fxVendorSource,
		BatchID: "b-vend-a2", Caller: fxVendorActorID,
		Items: []load.Item{vendItem("FXV-0002", "Planted By Staff", nil)}})
	if err != nil {
		t.Fatalf("attach replay: %v", err)
	}
	if o := vendorOutcome(t, r, "FXV-0002"); o.Status != load.OutcomeUnchanged {
		t.Fatalf("the attach replay answered %q, want unchanged", o.Status)
	}
}

// A reload that omits the contact removes the loaded contact (the record
// is the whole truth); a reload that carries one updates it, both
// through the module's own contact store.
func TestVendorLoadReloadWithoutContactRemovesIt(t *testing.T) {
	e := vendorLoadSetup(t)
	ctx := context.Background()
	with := func(email string) load.Item {
		return vendItem("FXV-0009", "Fixture Desk Supplier", map[string]any{
			"contact": map[string]any{"contact_name": "Fixture Desk", "email": email}})
	}
	if _, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxVendorOrgID, SourceSystem: fxVendorSource,
		BatchID: "b-vend-c1", Caller: fxVendorActorID, Items: []load.Item{with("first@example.invalid")}}); err != nil {
		t.Fatalf("first batch: %v", err)
	}
	r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxVendorOrgID, SourceSystem: fxVendorSource,
		BatchID: "b-vend-c2", Caller: fxVendorActorID, Items: []load.Item{with("second@example.invalid")}})
	if err != nil {
		t.Fatalf("update batch: %v", err)
	}
	if o := vendorOutcome(t, r, "FXV-0009"); o.Status != load.OutcomeUpdated {
		t.Fatalf("the contact change answered %+v, want updated", o)
	}
	id, _, _, _, _, _ := e.vendRow(t, "FXV-0009")
	if _, email, _ := e.vendContact(t, id); email != "second@example.invalid" {
		t.Fatalf("the contact email is %q, want the reload's", email)
	}
	r, err = e.door.UpsertBatch(ctx, load.Batch{OrgID: fxVendorOrgID, SourceSystem: fxVendorSource,
		BatchID: "b-vend-c3", Caller: fxVendorActorID,
		Items: []load.Item{vendItem("FXV-0009", "Fixture Desk Supplier", nil)}})
	if err != nil {
		t.Fatalf("drop batch: %v", err)
	}
	if o := vendorOutcome(t, r, "FXV-0009"); o.Status != load.OutcomeUpdated {
		t.Fatalf("the dropped contact answered %+v, want updated", o)
	}
	var n int
	if err := e.pool.QueryRow(ctx,
		`SELECT count(*) FROM purchasing_vendor_contacts WHERE vendor_id = $1::uuid`, id).Scan(&n); err != nil {
		t.Fatalf("contact count: %v", err)
	}
	if n != 0 {
		t.Fatalf("%d contact rows stand after a reload that names none, want none", n)
	}
}

func TestVendorLoadRefusesNegativeTermsNamingTheField(t *testing.T) {
	e := vendorLoadSetup(t)
	r, err := e.door.UpsertBatch(context.Background(), load.Batch{OrgID: fxVendorOrgID, SourceSystem: fxVendorSource,
		BatchID: "b-vend-n1", Caller: fxVendorActorID,
		Items: []load.Item{vendItem("FXV-0008", "Fixture Negative", map[string]any{"payment_terms_days": -5})}})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	o := vendorOutcome(t, r, "FXV-0008")
	if o.Status != load.OutcomeRefused || o.Field != "payment_terms_days" {
		t.Fatalf("negative terms answered %+v, want refused on payment_terms_days", o)
	}
}

func TestVendorLoadRefusesARowOwnedByAnotherOrg(t *testing.T) {
	e := vendorLoadSetup(t)
	ctx := context.Background()
	const otherOrg = "4442a800-0000-4000-8000-00000000e0ff"
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO vendors (vendor_no, name, org_id) VALUES ('FXV-0002', 'Other Org Vendor', $1::uuid)`, otherOrg); err != nil {
		t.Fatalf("plant: %v", err)
	}
	r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxVendorOrgID, SourceSystem: fxVendorSource,
		BatchID: "b-vend-o1", Caller: fxVendorActorID,
		Items: []load.Item{vendItem("FXV-0002", "Renamed By Load", nil)}})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	o := vendorOutcome(t, r, "FXV-0002")
	if o.Status != load.OutcomeRefused || o.Reason != "org_mismatch" ||
		!strings.Contains(o.Detail, "supplier") || !strings.Contains(o.Detail, "FXV-0002") {
		t.Fatalf("the other org's vendor answered %+v, want refused as org_mismatch naming the kind and key", o)
	}
	_, name, _, org, _, _ := e.vendRow(t, "FXV-0002")
	if org != otherOrg || name != "Other Org Vendor" {
		t.Fatalf("the vendor now carries org %q name %q, want it untouched", org, name)
	}
}
