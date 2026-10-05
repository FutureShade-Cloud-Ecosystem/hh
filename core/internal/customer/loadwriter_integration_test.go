//go:build integration

// loadwriter_integration_test.go proves the load doors' customer and
// contact writers (HH-LOAD-1 LD-6) through the frame: the stamps a
// loaded customer carries (org, seq, idempotency key), the references
// by legacy key (home branch, reps), the replay arms (unchanged draws
// nothing, a change updates one row and draws exactly one sequence),
// both conflict directions, a missing reference, the Canadian address
// normalisation and the left out refusal shape (field and reason, never
// a value), contacts under their customer with their org riding through
// it, and exit line 6: a loaded customer appears in the staff sync pull
// of its org. The writers never open a connection and never commit: the
// transaction they run on is the frame's.
//
// Fixtures are invented: the fxld6 account numbers, the org and actor
// ids. The fixture rows leave with the test.
//
// Run (the database URL rides the command line, never a default):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5660/ld6a?sslmode=disable' \
//	  go test -tags integration ./internal/customer/... -count=1 -run TestCustomerLoad
package customer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
	psync "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The fixture constants, invented.
const (
	fxLoadOrgID      = "4442a800-0000-4000-8000-00000000e001"
	fxLoadOtherOrgID = "4442a800-0000-4000-8000-00000000e002"
	fxLoadActorID    = "4442a800-0000-4000-8000-00000000e00a"
	fxLoadSource     = "testsuite"
	// The seeded Toronto branch row and a seeded staff row, the rows the
	// planted registry names stand for.
	fxSeedBranchID = "5d1b3f6e-0000-4000-9000-000000000001"
	fxSeedUserID   = "d7000000-0000-4000-9000-000000000010"
)

// loadAuditSpy is the suite's stand in for the platform's audit path.
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

type custLoadEnv struct {
	pool        *pgxpool.Pool
	store       *load.Store
	custDoor    *load.Door
	contactDoor *load.Door
}

func cleanCustLoad(ctx context.Context, pool *pgxpool.Pool) {
	for _, org := range []string{fxLoadOrgID, fxLoadOtherOrgID} {
		_, _ = pool.Exec(ctx, `DELETE FROM load_key_registry WHERE org_id = $1::uuid`, org)
		_, _ = pool.Exec(ctx, `DELETE FROM load_batch WHERE org_id = $1::uuid`, org)
	}
	// Every row a load could leave, including one a regressed reload
	// rewrote to its legacy id: by the fixture account prefix, by the
	// fixture orgs' stamp, and by the legacy ids the items carry.
	_, _ = pool.Exec(ctx, `
		DELETE FROM customers
		WHERE account_no::text LIKE 'fxld6-%'
		   OR account_no::text IN ('CUST-A', 'CUST-B', 'CUST-C', 'CUST-X', 'CUST-Y', 'CUST-Z', 'CUST-O', 'CUST-N1', 'CUST-N2')
		   OR org_id IN ($1::uuid, $2::uuid)`, fxLoadOrgID, fxLoadOtherOrgID)
}

func custLoadSetup(t *testing.T) *custLoadEnv {
	t.Helper()
	pool := intPool(t)
	ctx := context.Background()
	cleanCustLoad(ctx, pool)
	t.Cleanup(func() { cleanCustLoad(context.Background(), pool) })
	// The map doors ran before these doors in a real load: plant the two
	// reference names they would have recorded.
	for _, ref := range []struct{ kind, legacy, hh string }{
		{load.KindBranch, "BR-1", fxSeedBranchID},
		{load.KindUser, "REP-1", fxSeedUserID},
	} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO load_key_registry (org_id, source_system, kind, legacy_id, hh_id, content_hash)
			VALUES ($1::uuid, $2, $3, $4, $5::uuid, repeat('a', 64))`,
			fxLoadOrgID, fxLoadSource, ref.kind, ref.legacy, ref.hh); err != nil {
			t.Fatalf("plant reference name: %v", err)
		}
	}
	spy := &loadAuditSpy{}
	store := load.NewStore(pool)
	return &custLoadEnv{
		pool:        pool,
		store:       store,
		custDoor:    load.NewDoor(store, load.KindCustomer, CustomerLoadWriter{}, spy),
		contactDoor: load.NewDoor(store, load.KindContact, ContactLoadWriter{}, spy),
	}
}

// custItem is one customer batch item; the derived key is a canonical
// fixture uuid keyed off the legacy id.
func custItem(legacyID, accountNo, name string, extra map[string]any, requires ...load.Ref) load.Item {
	rec := map[string]any{"account_no": accountNo, "name": name}
	for k, v := range extra {
		rec[k] = v
	}
	suffix := map[string]string{
		"CUST-A": "000000000001", "CUST-B": "000000000002", "CUST-C": "000000000003",
		"CUST-X": "000000000004", "CUST-Y": "000000000005", "CUST-Z": "000000000006",
		"CON-1": "000000000011", "CON-2": "000000000012",
	}[legacyID]
	return load.Item{LegacyID: legacyID, DerivedKey: "d6000000-0000-4000-8000-" + suffix,
		Record: rec, Requires: requires}
}

func (e *custLoadEnv) submit(t *testing.T, door *load.Door, batchID string, items ...load.Item) load.BatchResult {
	t.Helper()
	return e.submitFrom(t, door, fxLoadSource, batchID, items...)
}

// submitFrom is submit from a named source system.
func (e *custLoadEnv) submitFrom(t *testing.T, door *load.Door, source, batchID string, items ...load.Item) load.BatchResult {
	t.Helper()
	r, err := door.UpsertBatch(context.Background(), load.Batch{OrgID: fxLoadOrgID, SourceSystem: source,
		BatchID: batchID, Caller: fxLoadActorID, Items: items})
	if err != nil {
		t.Fatalf("batch %s: %v", batchID, err)
	}
	return r
}

func outcome(t *testing.T, r load.BatchResult, legacyID string) load.Outcome {
	t.Helper()
	for _, o := range r.Items {
		if o.LegacyID == legacyID {
			return o
		}
	}
	t.Fatalf("batch carries no outcome for %s: %+v", legacyID, r.Items)
	return load.Outcome{}
}

type custRow struct {
	id, name, org, key, home, addr, meta string
	seq                                  int64
}

func (e *custLoadEnv) custRow(t *testing.T, accountNo string) custRow {
	t.Helper()
	var r custRow
	err := e.pool.QueryRow(context.Background(), `
		SELECT id::text, name, coalesce(org_id::text, ''), coalesce(idempotency_key::text, ''),
		       coalesce(home_branch_id::text, ''), billing_address::text, metadata::text, coalesce(seq, 0)
		FROM customers WHERE account_no = $1`, accountNo).
		Scan(&r.id, &r.name, &r.org, &r.key, &r.home, &r.addr, &r.meta, &r.seq)
	if err != nil {
		t.Fatalf("customer row %s: %v", accountNo, err)
	}
	return r
}

func (e *custLoadEnv) scopeNext(t *testing.T) int64 {
	t.Helper()
	var n int64
	if err := e.pool.QueryRow(context.Background(),
		`SELECT coalesce((SELECT next_value FROM scope_sequences WHERE scope = 'customers'), 0)`).Scan(&n); err != nil {
		t.Fatalf("scope read: %v", err)
	}
	return n
}

func (e *custLoadEnv) customerCount(t *testing.T, accountNo string) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM customers WHERE account_no = $1`, accountNo).Scan(&n); err != nil {
		t.Fatalf("customer count: %v", err)
	}
	return n
}

func TestCustomerLoadCreatesWithStampsAndReferences(t *testing.T) {
	e := custLoadSetup(t)
	before := e.scopeNext(t)
	r := e.submit(t, e.custDoor, "b-c-1", custItem("CUST-A", "fxld6-a", "Fixture Customer A",
		map[string]any{"customer_type": "BUILDER", "payment_terms_days": 45, "credit_limit_cents": 250000},
		load.Ref{Kind: load.KindBranch, LegacyID: "BR-1"}, load.Ref{Kind: load.KindUser, LegacyID: "REP-1"}))
	if o := outcome(t, r, "CUST-A"); o.Status != load.OutcomeCreated || o.HHID == "" {
		t.Fatalf("the customer answered %+v, want created", o)
	}
	row := e.custRow(t, "fxld6-a")
	if row.org != fxLoadOrgID {
		t.Fatalf("org %q, want the machine principal's org", row.org)
	}
	if row.seq == 0 || row.seq <= before {
		t.Fatalf("seq %d: the allocator drew nothing for the row (scope was %d)", row.seq, before)
	}
	if row.key != "d6000000-0000-4000-8000-000000000001" {
		t.Fatalf("idempotency key %q, want the derived key", row.key)
	}
	if row.home != fxSeedBranchID {
		t.Fatalf("home branch %q, want the row the branch key names", row.home)
	}
	if !strings.Contains(row.meta, fxSeedUserID) {
		t.Fatalf("metadata %s does not carry the rep the user key names", row.meta)
	}
}

func TestCustomerLoadReplayUnchangedDrawsNothingAndUpdateDrawsOne(t *testing.T) {
	e := custLoadSetup(t)
	item := func(name string) load.Item {
		return custItem("CUST-A", "fxld6-a", name, nil, load.Ref{Kind: load.KindBranch, LegacyID: "BR-1"})
	}
	e.submit(t, e.custDoor, "b-c-r1", item("Fixture Customer A"))
	base := e.scopeNext(t)
	r := e.submit(t, e.custDoor, "b-c-r2", item("Fixture Customer A"))
	if o := outcome(t, r, "CUST-A"); o.Status != load.OutcomeUnchanged {
		t.Fatalf("the identical replay answered %q, want unchanged", o.Status)
	}
	if got := e.scopeNext(t); got != base {
		t.Fatalf("the unchanged replay drew sequences: %d over %d", got, base)
	}
	r = e.submit(t, e.custDoor, "b-c-r3", item("Fixture Customer A Renamed"))
	if o := outcome(t, r, "CUST-A"); o.Status != load.OutcomeUpdated {
		t.Fatalf("the changed name answered %q, want updated", o.Status)
	}
	if got := e.scopeNext(t); got != base+1 {
		t.Fatalf("the update drew %d over %d, want exactly one new draw", got, base)
	}
	if row := e.custRow(t, "fxld6-a"); row.name != "Fixture Customer A Renamed" || e.customerCount(t, "fxld6-a") != 1 {
		t.Fatalf("the update left %+v: want the one row renamed", row)
	}
}

func TestCustomerLoadRefusesAMissingReferenceNamingKindAndKey(t *testing.T) {
	e := custLoadSetup(t)
	r := e.submit(t, e.custDoor, "b-c-m1",
		custItem("CUST-A", "fxld6-a", "Fixture Customer A", nil, load.Ref{Kind: load.KindBranch, LegacyID: "BR-NOPE"}),
		custItem("CUST-B", "fxld6-b", "Fixture Customer B", nil))
	o := outcome(t, r, "CUST-A")
	if o.Status != load.OutcomeRefused || o.Reason != load.ReasonMissingReference ||
		!strings.Contains(o.Detail, "branch") || !strings.Contains(o.Detail, "BR-NOPE") {
		t.Fatalf("the missing branch answered %+v, want refused naming kind and key", o)
	}
	if e.customerCount(t, "fxld6-a") != 0 {
		t.Fatalf("a refused item left a row")
	}
	if outcome(t, r, "CUST-B").Status != load.OutcomeCreated {
		t.Fatalf("the refusal stopped the rest of the batch")
	}
}

func TestCustomerLoadRefusesWrongReferenceKindsAndDeferredFields(t *testing.T) {
	e := custLoadSetup(t)
	r := e.submit(t, e.custDoor, "b-c-w1",
		custItem("CUST-A", "fxld6-a", "Fixture Customer A", nil, load.Ref{Kind: load.KindCategory, LegacyID: "X"}),
		custItem("CUST-B", "fxld6-b", "Fixture Customer B", map[string]any{"on_hold": true}),
		custItem("CUST-C", "fxld6-c", "Fixture Customer C", map[string]any{"customer_type": "NOPE"}))
	if o := outcome(t, r, "CUST-B"); o.Status != load.OutcomeRefused || o.Field != "on_hold" {
		t.Fatalf("a credit hold answered %+v, want refused naming the field: holds wait for the receivables load", o)
	}
	if o := outcome(t, r, "CUST-C"); o.Status != load.OutcomeRefused || o.Field != "customer_type" {
		t.Fatalf("a bad type answered %+v, want refused naming the field", o)
	}
	if o := outcome(t, r, "CUST-A"); o.Status != load.OutcomeRefused {
		t.Fatalf("a category reference answered %+v, want refused: customers take branch and user references", o)
	}
}

func TestCustomerLoadBothConflictDirections(t *testing.T) {
	e := custLoadSetup(t)
	e.submit(t, e.custDoor, "b-c-k1",
		custItem("CUST-A", "fxld6-a", "Fixture Customer A", nil),
		custItem("CUST-B", "fxld6-b", "Fixture Customer B", nil))
	// A second legacy key claiming a row a name already claims.
	r := e.submit(t, e.custDoor, "b-c-k2", custItem("CUST-C", "fxld6-a", "Fixture Customer C", nil))
	o := outcome(t, r, "CUST-C")
	if o.Status != load.OutcomeRefused || o.Reason != load.ReasonKeyConflict ||
		!strings.Contains(o.Detail, "CUST-C") || !strings.Contains(o.Detail, "CUST-A") {
		t.Fatalf("the second claim answered %+v, want refused naming both legacy ids", o)
	}
	// A recorded name re-pointing at the row another name owns.
	r = e.submit(t, e.custDoor, "b-c-k3", custItem("CUST-A", "fxld6-b", "Fixture Customer A", nil))
	o = outcome(t, r, "CUST-A")
	if o.Status != load.OutcomeRefused || o.Reason != load.ReasonKeyConflict ||
		!strings.Contains(o.Detail, "CUST-A") || !strings.Contains(o.Detail, "CUST-B") {
		t.Fatalf("the re-point answered %+v, want a key conflict naming both legacy ids", o)
	}
	if strings.Contains(o.Detail, "fxld6-") {
		t.Fatalf("the refusal names an account number value: %q", o.Detail)
	}
	if row := e.custRow(t, "fxld6-b"); row.name != "Fixture Customer B" {
		t.Fatalf("the refused re-point changed the other row: %+v", row)
	}
	if row := e.custRow(t, "fxld6-a"); row.name != "Fixture Customer A" {
		t.Fatalf("the refused re-point changed the item's own row: %+v", row)
	}
}

func TestCustomerLoadAttachesAStaffMadeRowAndStampsIt(t *testing.T) {
	e := custLoadSetup(t)
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO customers (account_no, name) VALUES ('fxld6-s', 'Made By Staff')`); err != nil {
		t.Fatalf("plant: %v", err)
	}
	r := e.submit(t, e.custDoor, "b-c-s1", custItem("CUST-A", "fxld6-s", "Made By Staff", nil))
	if o := outcome(t, r, "CUST-A"); o.Status != load.OutcomeAttached {
		t.Fatalf("the first bind to a staff made row answered %+v, want attached", o)
	}
	row := e.custRow(t, "fxld6-s")
	if e.customerCount(t, "fxld6-s") != 1 || row.org != fxLoadOrgID || row.seq == 0 {
		t.Fatalf("the attached row %+v: want one row carrying the load's stamps", row)
	}
}

func TestCustomerLoadAddressNormalisedAndLeftOutWithoutAValue(t *testing.T) {
	e := custLoadSetup(t)
	good := map[string]any{"line1": "1 Fixture Rd", "city": "Fixtureville", "province": "on", "postal": "k8v5p7"}
	bad := map[string]any{"line1": "2 Fixture Rd", "city": "Fixtureville", "province": "ZZ", "postal": "90210-1234"}
	r := e.submit(t, e.custDoor, "b-a-1",
		custItem("CUST-A", "fxld6-a", "Fixture Customer A", map[string]any{"billing_address": good}),
		custItem("CUST-B", "fxld6-b", "Fixture Customer B", map[string]any{"billing_address": bad}))
	if row := e.custRow(t, "fxld6-a"); !strings.Contains(row.addr, `"K8V 5P7"`) || !strings.Contains(row.addr, `"ON"`) {
		t.Fatalf("stored address %s, want the Canadian normalisation", row.addr)
	}
	if o := outcome(t, r, "CUST-A"); o.Status != load.OutcomeCreated || o.Reason != "" {
		t.Fatalf("a valid address answered %+v, want created with no note", o)
	}
	o := outcome(t, r, "CUST-B")
	if o.Status != load.OutcomeCreated || o.Reason != "address_left_out" || o.Field != "billing_address" {
		t.Fatalf("a failing address answered %+v, want the row created, noted left out on billing_address", o)
	}
	for _, v := range []string{"90210", "ZZ", "2 Fixture Rd", "Fixtureville"} {
		if strings.Contains(o.Detail, v) || strings.Contains(o.Reason, v) || strings.Contains(o.Field, v) {
			t.Fatalf("the outcome %+v names a record value %q", o, v)
		}
	}
	if row := e.custRow(t, "fxld6-b"); row.addr != "{}" || row.name != "Fixture Customer B" {
		t.Fatalf("the failing address row %+v: want the address left out and the rest landed", row)
	}
	// On an update the address that fails is left out: the stored one stays.
	r = e.submit(t, e.custDoor, "b-a-2",
		custItem("CUST-A", "fxld6-a", "Fixture Customer A", map[string]any{"billing_address": bad}))
	if o := outcome(t, r, "CUST-A"); o.Status != load.OutcomeUpdated || o.Reason != "address_left_out" {
		t.Fatalf("the failing update answered %+v", o)
	}
	if row := e.custRow(t, "fxld6-a"); !strings.Contains(row.addr, `"K8V 5P7"`) {
		t.Fatalf("the left out address overwrote the stored one: %s", row.addr)
	}
}

func TestCustomerLoadStaffCreateRouteBehaviourUnchanged(t *testing.T) {
	e := custLoadSetup(t)
	e.submit(t, e.custDoor, "b-s-1", custItem("CUST-A", "fxld6-a", "Fixture Customer A", nil))
	svc := &Service{writer: NewRepository(e.pool)}
	_, err := svc.CreateCustomer(context.Background(), CreateInput{AccountNo: "fxld6-a", Name: "Staff Dup"})
	if err != ErrDuplicateAccountNo {
		t.Fatalf("the staff create on a taken account number answered %v, want ErrDuplicateAccountNo (the 409 stays)", err)
	}
}

func TestCustomerLoadAppearsInTheStaffSyncPullOfItsOrg(t *testing.T) {
	e := custLoadSetup(t)
	e.submit(t, e.custDoor, "b-p-1", custItem("CUST-A", "fxld6-a", "Fixture Customer A", nil))
	store := NewCustomersStore(e.pool)
	ctx := context.Background()
	items, _, err := store.Pull(ctx, psync.ScopeFilter{OrgID: fxLoadOrgID, Actor: "fixture-staff"}, 0, 500)
	if err != nil {
		t.Fatalf("staff pull: %v", err)
	}
	found := false
	for _, it := range items {
		if acct, _ := it.Data["account_no"].(string); acct == "fxld6-a" {
			found = it.Seq > 0 && it.Data["name"] == "Fixture Customer A"
		}
	}
	if !found {
		t.Fatalf("the loaded customer is absent from its org's staff pull (%d rows)", len(items))
	}
	other, _, err := store.Pull(ctx, psync.ScopeFilter{OrgID: fxLoadOtherOrgID, Actor: "fixture-staff"}, 0, 500)
	if err != nil {
		t.Fatalf("other org pull: %v", err)
	}
	for _, it := range other {
		if acct, _ := it.Data["account_no"].(string); acct == "fxld6-a" {
			t.Fatalf("the loaded customer appears in another org's pull")
		}
	}
}

func TestCustomerLoadContactsRideUnderTheirCustomer(t *testing.T) {
	e := custLoadSetup(t)
	e.submit(t, e.custDoor, "b-k-0", custItem("CUST-A", "fxld6-a", "Fixture Customer A", nil))
	ref := load.Ref{Kind: load.KindCustomer, LegacyID: "CUST-A"}
	r := e.submit(t, e.contactDoor, "b-k-1",
		custItem("CON-1", "", "", map[string]any{"name": "Fixture Contact", "email": "fx.contact@example.invalid", "phone": "555-0100", "role": "buyer", "is_primary": true}, ref),
		custItem("CON-2", "", "", map[string]any{"name": "No Parent"}))
	if o := outcome(t, r, "CON-1"); o.Status != load.OutcomeCreated {
		t.Fatalf("the contact answered %+v, want created", o)
	}
	if o := outcome(t, r, "CON-2"); o.Status != load.OutcomeRefused || o.Field != load.KindCustomer {
		t.Fatalf("a contact with no customer answered %+v, want refused naming the customer reference", o)
	}
	var cust, name, email string
	var primary bool
	if err := e.pool.QueryRow(context.Background(), `
		SELECT customer_id::text, name, email::text, is_primary FROM customer_contacts WHERE name = 'Fixture Contact'`).
		Scan(&cust, &name, &email, &primary); err != nil {
		t.Fatalf("contact row: %v", err)
	}
	if cust != e.custRow(t, "fxld6-a").id || email != "fx.contact@example.invalid" || !primary {
		t.Fatalf("contact landed under %q with %q primary %v", cust, email, primary)
	}
	// Replay unchanged, then a change updates the one row.
	r = e.submit(t, e.contactDoor, "b-k-2", custItem("CON-1", "", "", map[string]any{"name": "Fixture Contact", "email": "fx.contact@example.invalid", "phone": "555-0100", "role": "buyer", "is_primary": true}, ref))
	if outcome(t, r, "CON-1").Status != load.OutcomeUnchanged {
		t.Fatalf("the identical contact replay did not answer unchanged")
	}
	r = e.submit(t, e.contactDoor, "b-k-3", custItem("CON-1", "", "", map[string]any{"name": "Fixture Contact", "email": "fx.contact@example.invalid", "phone": "555-0199", "role": "buyer", "is_primary": true}, ref))
	if outcome(t, r, "CON-1").Status != load.OutcomeUpdated {
		t.Fatalf("the changed contact did not answer updated")
	}
	var n int
	var phone string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*), max(phone) FROM customer_contacts WHERE customer_id = $1::uuid`, e.custRow(t, "fxld6-a").id).Scan(&n, &phone); err != nil {
		t.Fatalf("contact count: %v", err)
	}
	if n != 1 || phone != "555-0199" {
		t.Fatalf("%d contact rows with phone %q, want the one row updated", n, phone)
	}
}

func TestCustomerLoadContactOrgRidesThroughItsCustomer(t *testing.T) {
	e := custLoadSetup(t)
	// A customer of another org, named in this org's registry: a contact
	// may not hang under it.
	var other string
	if err := e.pool.QueryRow(context.Background(), `
		INSERT INTO customers (account_no, name, org_id, seq) VALUES ('fxld6-o', 'Other Org Customer', $1::uuid, 1) RETURNING id::text`,
		fxLoadOtherOrgID).Scan(&other); err != nil {
		t.Fatalf("plant other org customer: %v", err)
	}
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO load_key_registry (org_id, source_system, kind, legacy_id, hh_id, content_hash)
		VALUES ($1::uuid, $2, $3, 'CUST-O', $4::uuid, repeat('b', 64))`,
		fxLoadOrgID, fxLoadSource, load.KindCustomer, other); err != nil {
		t.Fatalf("plant name: %v", err)
	}
	r := e.submit(t, e.contactDoor, "b-o-1",
		custItem("CON-1", "", "", map[string]any{"name": "Fixture Contact"}, load.Ref{Kind: load.KindCustomer, LegacyID: "CUST-O"}))
	if o := outcome(t, r, "CON-1"); o.Status != load.OutcomeRefused || o.Reason != "org_mismatch" ||
		!strings.Contains(o.Detail, "customer") || !strings.Contains(o.Detail, "CUST-O") {
		t.Fatalf("a contact under another org's customer answered %+v, want refused org_mismatch naming kind and key", o)
	}
}

// ---- pool 4 ----

const poolFourConns = 4

func poolFour(t *testing.T) *pgxpool.Pool {
	t.Helper()
	base := intPool(t)
	cfg := base.Config().Copy()
	cfg.MaxConns = poolFourConns
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("pool four: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestCustomerLoadPoolFourThreeContendersEveryCallOnTheBatchTransaction(t *testing.T) {
	e := custLoadSetup(t)
	pool := poolFour(t)
	ctx := context.Background()
	held, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("hold one connection: %v", err)
	}
	defer held.Release()
	store := load.NewStore(pool)
	custDoor := load.NewDoor(store, load.KindCustomer, CustomerLoadWriter{}, &loadAuditSpy{})
	contactDoor := load.NewDoor(store, load.KindContact, ContactLoadWriter{}, &loadAuditSpy{})
	runContenders(t, func(ctx context.Context, k int) error {
		_, err := custDoor.UpsertBatch(ctx, load.Batch{OrgID: fxLoadOrgID, SourceSystem: fxLoadSource,
			BatchID: fmt.Sprintf("b-pool-c-%d", k), Caller: fxLoadActorID,
			Items: []load.Item{custItem("CUST-A", "fxld6-a", "Fixture Customer A", nil,
				load.Ref{Kind: load.KindBranch, LegacyID: "BR-1"})}})
		return err
	}, 20)
	if n := e.customerCount(t, "fxld6-a"); n != 1 {
		t.Fatalf("%d customer rows after three contenders, want one", n)
	}
	runContenders(t, func(ctx context.Context, k int) error {
		_, err := contactDoor.UpsertBatch(ctx, load.Batch{OrgID: fxLoadOrgID, SourceSystem: fxLoadSource,
			BatchID: fmt.Sprintf("b-pool-k-%d", k), Caller: fxLoadActorID,
			Items: []load.Item{custItem("CON-1", "", "", map[string]any{"name": "Fixture Contact"},
				load.Ref{Kind: load.KindCustomer, LegacyID: "CUST-A"})}})
		return err
	}, 20)
	var n int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM customer_contacts WHERE name = 'Fixture Contact'`).Scan(&n); err != nil {
		t.Fatalf("contact count: %v", err)
	}
	if n != 1 {
		t.Fatalf("%d contact rows after three contenders, want one", n)
	}
}

// runContenders runs three batches at once, each under the timeout the
// caller names: a writer that takes a pool connection while the batch
// holds its transaction stalls here, and the leg fails fast saying so
// instead of hanging to the test binary's own timeout.
func runContenders(t *testing.T, fn func(ctx context.Context, k int) error, seconds int) {
	t.Helper()
	var wg sync.WaitGroup
	errs := make([]error, 3)
	for k := 0; k < 3; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(context.Background(), time.Duration(seconds)*time.Second)
			defer cancel()
			errs[k] = fn(cctx, k)
		}(k)
	}
	wg.Wait()
	for k, err := range errs {
		if err != nil {
			t.Fatalf("contender %d: %v (a writer took a pool connection beyond the batch's transaction, or the batch stalled)", k, err)
		}
	}
}

// strayPoolWriter is the leg's deliberate violation: a writer that
// reaches for the pool while the batch holds its transaction. The leg
// above is only worth its name if this one fails it.
type strayPoolWriter struct {
	pool  *pgxpool.Pool
	inner load.Writer
}

func (w strayPoolWriter) Apply(ctx context.Context, tx pgx.Tx, in load.Write) (string, error) {
	if _, err := w.pool.Exec(ctx, `SELECT 1`); err != nil {
		return "", fmt.Errorf("stray pool use: %w", err)
	}
	return w.inner.Apply(ctx, tx, in)
}

func TestCustomerLoadPoolFourBitesAWriterThatUsesThePool(t *testing.T) {
	custLoadSetup(t)
	pool := poolFour(t)
	held, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("hold one connection: %v", err)
	}
	defer held.Release()
	door := load.NewDoor(load.NewStore(pool), load.KindCustomer, strayPoolWriter{pool: pool, inner: CustomerLoadWriter{}}, &loadAuditSpy{})
	var wg sync.WaitGroup
	errs := make([]error, 3)
	for k := 0; k < 3; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(context.Background(), 3_000_000_000)
			defer cancel()
			_, errs[k] = door.UpsertBatch(cctx, load.Batch{OrgID: fxLoadOrgID, SourceSystem: fxLoadSource,
				BatchID: fmt.Sprintf("b-stray-%d", k), Caller: fxLoadActorID,
				Items: []load.Item{custItem("CUST-A", "fxld6-a", "Fixture Customer A", nil)}})
		}(k)
	}
	wg.Wait()
	for k, err := range errs {
		if err == nil {
			t.Fatalf("contender %d finished: the pool 4 leg missed a writer that uses the pool", k)
		}
	}
}

// plantOtherOrgCustomer plants a customer another org owns and returns its id.
func (e *custLoadEnv) plantOtherOrgCustomer(t *testing.T, accountNo, name string) string {
	t.Helper()
	var id string
	if err := e.pool.QueryRow(context.Background(), `
		INSERT INTO customers (account_no, name, org_id, seq) VALUES ($1, $2, $3::uuid, 7) RETURNING id::text`,
		accountNo, name, fxLoadOtherOrgID).Scan(&id); err != nil {
		t.Fatalf("plant other org customer: %v", err)
	}
	return id
}

func (e *custLoadEnv) pullHas(t *testing.T, org, id, name string) bool {
	t.Helper()
	items, _, err := NewCustomersStore(e.pool).Pull(context.Background(), psync.ScopeFilter{OrgID: org, Actor: "fixture-staff"}, 0, 1000)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	for _, it := range items {
		if it.ID == id && it.Data["name"] == name {
			return true
		}
	}
	return false
}

func TestCustomerLoadRefusesARowOwnedByAnotherOrgAndNeverTakesItOver(t *testing.T) {
	e := custLoadSetup(t)
	other := e.plantOtherOrgCustomer(t, "fxld6-x", "Other Org Owns")
	// The create arm: the account number names the other org's row.
	r := e.submit(t, e.custDoor, "b-x-1", custItem("CUST-A", "fxld6-x", "Loaded Over It", nil))
	o := outcome(t, r, "CUST-A")
	if o.Status != load.OutcomeRefused || o.Reason != "org_mismatch" ||
		!strings.Contains(o.Detail, "customer") || !strings.Contains(o.Detail, "CUST-A") {
		t.Fatalf("a row another org owns answered %+v, want refused org_mismatch naming kind and key", o)
	}
	if strings.Contains(o.Detail, "fxld6-x") || strings.Contains(o.Detail, "Other Org Owns") {
		t.Fatalf("the refusal names a value: %q", o.Detail)
	}
	row := e.custRow(t, "fxld6-x")
	if row.org != fxLoadOtherOrgID || row.name != "Other Org Owns" || row.seq != 7 || row.id != other {
		t.Fatalf("the other org's row changed: %+v", row)
	}
	if !e.pullHas(t, fxLoadOtherOrgID, other, "Other Org Owns") || e.pullHas(t, fxLoadOrgID, other, "Loaded Over It") {
		t.Fatalf("the other org's staff pull no longer shows its own customer unchanged")
	}
	// The update arm: a recorded row whose org is no longer ours is never rewritten.
	e.submit(t, e.custDoor, "b-x-2", custItem("CUST-B", "fxld6-b", "Fixture Customer B", nil))
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE customers SET org_id = $1::uuid WHERE account_no = 'fxld6-b'`, fxLoadOtherOrgID); err != nil {
		t.Fatalf("reassign: %v", err)
	}
	r = e.submit(t, e.custDoor, "b-x-3", custItem("CUST-B", "fxld6-b", "Fixture Customer B Renamed", nil))
	if o := outcome(t, r, "CUST-B"); o.Status != load.OutcomeRefused || o.Reason != "org_mismatch" {
		t.Fatalf("the update arm on another org's row answered %+v, want refused org_mismatch", o)
	}
	if row := e.custRow(t, "fxld6-b"); row.org != fxLoadOtherOrgID || row.name != "Fixture Customer B" {
		t.Fatalf("the update arm changed the other org's row: %+v", row)
	}
	// A re-point onto the other org's account number refuses the same way.
	e.submit(t, e.custDoor, "b-x-4", custItem("CUST-C", "fxld6-c", "Fixture Customer C", nil))
	r = e.submit(t, e.custDoor, "b-x-5", custItem("CUST-C", "fxld6-x", "Fixture Customer C", nil))
	if o := outcome(t, r, "CUST-C"); o.Status != load.OutcomeRefused || o.Reason != "org_mismatch" {
		t.Fatalf("a re-point onto another org's number answered %+v, want org_mismatch", o)
	}
}

func TestCustomerLoadAccountConflictNamesBothKeysOrTheStaffRow(t *testing.T) {
	e := custLoadSetup(t)
	e.submit(t, e.custDoor, "b-n-1",
		custItem("CUST-A", "fxld6-a", "Fixture Customer A", nil),
		custItem("CUST-B", "fxld6-b", "Fixture Customer B", nil))
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO customers (account_no, name) VALUES ('fxld6-s', 'Made By Staff')`); err != nil {
		t.Fatalf("plant staff row: %v", err)
	}
	r := e.submit(t, e.custDoor, "b-n-2",
		custItem("CUST-A", "fxld6-b", "Fixture Customer A", nil))
	o := outcome(t, r, "CUST-A")
	if o.Reason != load.ReasonKeyConflict || !strings.Contains(o.Detail, "CUST-A") || !strings.Contains(o.Detail, "CUST-B") {
		t.Fatalf("the re-point answered %+v, want both legacy keys named", o)
	}
	r = e.submit(t, e.custDoor, "b-n-3", custItem("CUST-A", "fxld6-s", "Fixture Customer A", nil))
	o = outcome(t, r, "CUST-A")
	if o.Reason != load.ReasonKeyConflict || !strings.Contains(o.Detail, "CUST-A") || !strings.Contains(o.Detail, "staff") {
		t.Fatalf("the re-point onto a staff row answered %+v, want the item's key and the staff row named", o)
	}
	for _, v := range []string{"fxld6-s", "fxld6-b", "Made By Staff"} {
		if strings.Contains(o.Detail, v) {
			t.Fatalf("the refusal names a value %q: %q", v, o.Detail)
		}
	}
	if row := e.custRow(t, "fxld6-a"); row.name != "Fixture Customer A" {
		t.Fatalf("a refused conflict changed the row: %+v", row)
	}
}

func TestCustomerLoadAttachMergesStaffMetadataAndReloadKeepsTheAccountNumber(t *testing.T) {
	e := custLoadSetup(t)
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO customers (account_no, name, metadata) VALUES ('fxld6-s', 'Made By Staff', '{"staff_key":"kept"}')`); err != nil {
		t.Fatalf("plant: %v", err)
	}
	e.submit(t, e.custDoor, "b-m-1", custItem("CUST-A", "fxld6-s", "Made By Staff", nil,
		load.Ref{Kind: load.KindUser, LegacyID: "REP-1"}))
	row := e.custRow(t, "fxld6-s")
	if !strings.Contains(row.meta, `"staff_key"`) || !strings.Contains(row.meta, fxSeedUserID) {
		t.Fatalf("metadata %s: want the staff key kept and the rep set", row.meta)
	}
	// A reload that stops naming the account number keeps the stored one.
	item := load.Item{LegacyID: "CUST-A", DerivedKey: "d6000000-0000-4000-8000-000000000001",
		Record: map[string]any{"name": "Made By Staff Renamed"}}
	r := e.submit(t, e.custDoor, "b-m-2", item)
	if o := outcome(t, r, "CUST-A"); o.Status != load.OutcomeUpdated {
		t.Fatalf("the reload answered %+v, want updated", o)
	}
	row = e.custRow(t, "fxld6-s")
	if row.name != "Made By Staff Renamed" || e.customerCount(t, "CUST-A") != 0 {
		t.Fatalf("the reload rewrote the account number to the legacy id: %+v", row)
	}
	// The rep the reload no longer names is dropped; the staff key stays.
	if strings.Contains(row.meta, fxSeedUserID) || !strings.Contains(row.meta, `"staff_key"`) {
		t.Fatalf("metadata %s after a reload with no reps: want the staff key only", row.meta)
	}
	// A first load with no account number uses the legacy id.
	e.submit(t, e.custDoor, "b-m-3", load.Item{LegacyID: "fxld6-l", DerivedKey: "d6000000-0000-4000-8000-000000000002",
		Record: map[string]any{"name": "Legacy Keyed"}})
	if e.customerCount(t, "fxld6-l") != 1 {
		t.Fatalf("a first load with no account number did not use the legacy id")
	}
}

func TestCustomerLoadRefusalsSayTheTrueThing(t *testing.T) {
	e := custLoadSetup(t)
	// A row recorded by another source system is named as that.
	e.submit(t, e.custDoor, "b-t-1", custItem("CUST-A", "fxld6-a", "Fixture Customer A", nil))
	e.submitFrom(t, e.custDoor, "othersys", "b-t-2", custItem("CUST-Z", "fxld6-z", "Fixture Customer Z", nil))
	r := e.submitFrom(t, e.custDoor, "othersys", "b-t-3", custItem("CUST-Z", "fxld6-a", "Fixture Customer Z", nil))
	o := outcome(t, r, "CUST-Z")
	if o.Reason != load.ReasonKeyConflict || !strings.Contains(o.Detail, "another source system") || strings.Contains(o.Detail, "staff") {
		t.Fatalf("a re-point onto a row another source system names answered %+v, want that said and not a staff row", o)
	}
	// A contact whose customer row is gone is a missing reference.
	r = e.submit(t, e.custDoor, "b-t-4", custItem("CUST-B", "fxld6-b", "Fixture Customer B", nil))
	if _, err := e.pool.Exec(context.Background(), `DELETE FROM customers WHERE account_no = 'fxld6-b'`); err != nil {
		t.Fatalf("delete customer: %v", err)
	}
	r = e.submit(t, e.contactDoor, "b-t-5", custItem("CON-1", "", "", map[string]any{"name": "Fixture Contact"},
		load.Ref{Kind: load.KindCustomer, LegacyID: "CUST-B"}))
	o = outcome(t, r, "CON-1")
	if o.Reason != load.ReasonMissingReference || strings.Contains(o.Detail, "another org") || !strings.Contains(o.Detail, "CUST-B") {
		t.Fatalf("a contact under a gone customer answered %+v, want missing_reference naming kind and key, not another org", o)
	}
}

// ---- deadlock victims ----

// waitForLockWait polls until some session in this database waits on a
// lock, the batch standing behind the outside transaction.
func waitForLockWait(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := pool.QueryRow(context.Background(), `
			SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&n); err != nil {
			t.Fatalf("lock wait poll: %v", err)
		}
		if n > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no session ever waited on a lock: the batch never reached the held row")
}

// deadlockLeg runs one batch into a deadlock with an outside transaction
// that holds a row the batch's second item needs (lockSQL) and then asks
// for a row the batch already holds (wantSQL): Postgres kills the batch,
// which must answer ErrBatchContended (the doors' handler maps it to the
// retryable 503, proven in the load package's handler test), leave
// nothing of itself behind (no batch record, no registry name, nothing of
// the items' rows), and converge when the same batch is resubmitted.
func (e *custLoadEnv) deadlockLeg(t *testing.T, door *load.Door, kind, batchID string, items []load.Item, lockSQL, wantSQL string, rowsLeft string) {
	t.Helper()
	ctx := context.Background()
	outside, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("outside begin: %v", err)
	}
	defer func() { _ = outside.Rollback(ctx) }()
	// A long deadlock timeout on the outside session, so the batch's own
	// detection (the default, a second) fires first and the batch is the
	// victim.
	if _, err := outside.Exec(ctx, `SET LOCAL deadlock_timeout = '30s'`); err != nil {
		t.Fatalf("outside timeout: %v", err)
	}
	if _, err := outside.Exec(ctx, lockSQL); err != nil {
		t.Fatalf("outside lock: %v", err)
	}
	batch := load.Batch{OrgID: fxLoadOrgID, SourceSystem: fxLoadSource, BatchID: batchID, Caller: fxLoadActorID, Items: items}
	done := make(chan error, 1)
	go func() {
		cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		_, err := door.UpsertBatch(cctx, batch)
		done <- err
	}()
	waitForLockWait(t, e.pool)
	// The outside transaction now wants what the batch holds: the cycle
	// closes and Postgres kills the batch.
	_, _ = outside.Exec(ctx, wantSQL)
	select {
	case err := <-done:
		if !errors.Is(err, load.ErrBatchContended) {
			t.Fatalf("the deadlocked batch answered %v, want the ErrBatchContended sentinel", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatalf("the deadlock never resolved")
	}
	_ = outside.Rollback(ctx)

	names := make([]string, 0, len(items))
	for _, it := range items {
		names = append(names, it.LegacyID)
	}
	var batches, registry, rows int
	if err := e.pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM load_batch WHERE org_id = $1::uuid AND batch_id = $2),
		       (SELECT count(*) FROM load_key_registry WHERE org_id = $1::uuid AND legacy_id = ANY($3) AND kind = $4),
		       (`+rowsLeft+`)`, fxLoadOrgID, batchID, names, kind).Scan(&batches, &registry, &rows); err != nil {
		t.Fatalf("remainder read: %v", err)
	}
	if batches != 0 || registry != 0 || rows != 0 {
		t.Fatalf("the victim left %d batch rows, %d names, %d item rows: a contended batch rolls back whole", batches, registry, rows)
	}
	r, err := door.UpsertBatch(ctx, batch)
	if err != nil {
		t.Fatalf("resubmission: %v", err)
	}
	for _, o := range r.Items {
		if o.Status != load.OutcomeCreated && o.Status != load.OutcomeAttached {
			t.Fatalf("the resubmission answered %+v, want it to converge", o)
		}
	}
}

func TestCustomerLoadDeadlockInTheWriterAnswersTheContentionSentinel(t *testing.T) {
	e := custLoadSetup(t)
	ctx := context.Background()
	// A staff made row the outside transaction will hold.
	if _, err := e.pool.Exec(ctx, `INSERT INTO customers (account_no, name) VALUES ('fxld6-d1', 'Fixture Held')`); err != nil {
		t.Fatalf("plant held row: %v", err)
	}
	// The scope's sequence row exists before the batch draws, so the outside
	// transaction's later update waits on the batch's hold.
	if _, err := e.pool.Exec(ctx, `INSERT INTO scope_sequences (scope) VALUES ('customers') ON CONFLICT (scope) DO NOTHING`); err != nil {
		t.Fatalf("plant scope row: %v", err)
	}
	// Item one lands fresh (it draws the scope's sequence and holds it to
	// the batch's end), item two takes the held row and waits on the outside.
	e.deadlockLeg(t, e.custDoor, load.KindCustomer, "b-dl-c",
		[]load.Item{
			custItem("CUST-X", "fxld6-d2", "Fixture Fresh", nil),
			custItem("CUST-Y", "fxld6-d1", "Fixture Held Renamed", nil),
		},
		`UPDATE customers SET name = name WHERE account_no = 'fxld6-d1'`,
		`UPDATE scope_sequences SET next_value = next_value WHERE scope = 'customers'`,
		`SELECT count(*) FROM customers WHERE account_no = 'fxld6-d2'`)
}

func TestContactLoadDeadlockInTheWriterAnswersTheContentionSentinel(t *testing.T) {
	e := custLoadSetup(t)
	ctx := context.Background()
	e.submit(t, e.custDoor, "b-dl-k0",
		custItem("CUST-A", "fxld6-a", "Fixture Customer A", nil),
		custItem("CUST-B", "fxld6-b", "Fixture Customer B", nil))
	idB := e.custRow(t, "fxld6-b").id
	// Item one inserts a contact and holds its registry name to the batch's
	// end; item two inserts a contact under customer B, whose row the
	// outside transaction holds, and waits. The outside then asks for the
	// registry name the batch holds.
	e.deadlockLeg(t, e.contactDoor, load.KindContact, "b-dl-k1",
		[]load.Item{
			custItem("CON-1", "", "", map[string]any{"name": "Fixture Contact One"}, load.Ref{Kind: load.KindCustomer, LegacyID: "CUST-A"}),
			custItem("CON-2", "", "", map[string]any{"name": "Fixture Contact Two"}, load.Ref{Kind: load.KindCustomer, LegacyID: "CUST-B"}),
		},
		`SELECT 1 FROM customers WHERE id = '`+idB+`'::uuid FOR UPDATE`,
		fmt.Sprintf(`INSERT INTO load_key_registry (org_id, source_system, kind, legacy_id, hh_id, content_hash)
			VALUES ('%s'::uuid, '%s', 'contact', 'CON-1', gen_random_uuid(), repeat('c', 64))`, fxLoadOrgID, fxLoadSource),
		`SELECT count(*) FROM customer_contacts WHERE name LIKE 'Fixture Contact %'`)
	_ = ctx
}
