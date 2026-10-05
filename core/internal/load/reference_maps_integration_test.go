//go:build integration

// reference_maps_integration_test.go proves the two map doors of
// HH-LOAD-1 LD-4: the branch map and the user map, which record names
// against rows that already exist and create nothing. The attach
// semantics: a name never seen answers attached, not created, and binds
// to the pre existing row; a replay answers unchanged; a record that
// would re-point the name at a second row refuses as a key conflict; a
// code or email that answers no row refuses naming the field, never a
// value; and no branch row and no staff row is ever created by a map
// batch. The file also proves the contention ruling end to end: a batch
// that Postgres rolls back whole as a deadlock victim answers the
// ErrBatchContended sentinel, leaves nothing partial behind, and its
// resubmission converges.
//
// Every fixture is invented: the branch codes, the emails, the org and
// actor ids, the scope rows. No dealer value appears anywhere.
//
// Run (the database URL rides the command line, never a default):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5625/ld4doors?sslmode=disable' \
//	  go test -tags integration ./internal/load/... -count=1 -run 'TestBranchMap|TestUserMap|TestContended'
package load

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/branches"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The map doors' fixture rows: two branch rows and two staff rows,
// invented, planted by the suite and removed with it. The org and actor
// ids are the load suite's own.
const (
	fxBranchOneID   = "4442a800-0000-4000-8000-000000000b01"
	fxBranchTwoID   = "4442a800-0000-4000-8000-000000000b02"
	fxBranchOneCode = "FXB1"
	fxBranchTwoCode = "FXB2"
	fxStaffOneID    = "4442a800-0000-4000-8000-000000000c01"
	fxStaffTwoID    = "4442a800-0000-4000-8000-000000000c02"
	fxStaffOneEmail = "fixture.rep.one@example.invalid"
	fxStaffTwoEmail = "fixture.rep.two@example.invalid"
)

// mapSetup plants the fixture branch and staff rows beside a clean org
// slice of the registry and batch tables, and undoes all of it at the
// end. The load module's own setup brings the probe shapes; the maps
// read the real locations and users tables through the real readers.
func mapSetup(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := setup(t).pool
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO locations (id, branch_id, parent_id, loc_type, code, name, address) VALUES
			($1::uuid, $1::uuid, NULL, 'BRANCH', $3, 'Fixture Branch One', '{}'),
			($2::uuid, $2::uuid, NULL, 'BRANCH', $4, 'Fixture Branch Two', '{}')`,
		fxBranchOneID, fxBranchTwoID, fxBranchOneCode, fxBranchTwoCode); err != nil {
		t.Fatalf("plant map fixtures (locations): %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, display_name, status) VALUES
			($1::uuid, $3, 'Fixture Rep One', 'ACTIVE'),
			($2::uuid, $4, 'Fixture Rep Two', 'INVITED')`,
		fxStaffOneID, fxStaffTwoID, fxStaffOneEmail, fxStaffTwoEmail); err != nil {
		t.Fatalf("plant map fixtures (users): %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM locations WHERE id = ANY($1::uuid[])`,
			[]string{fxBranchOneID, fxBranchTwoID})
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1::uuid[])`,
			[]string{fxStaffOneID, fxStaffTwoID})
	})
	return pool
}

// mapItem is one branch map item: the record names the yard code.
func mapItem(legacyID, code string) Item {
	return Item{LegacyID: legacyID, DerivedKey: "", Record: map[string]any{"code": code}}
}

// userItem is one user map item: the record names the verified email.
func userItem(legacyID, email string) Item {
	return Item{LegacyID: legacyID, DerivedKey: "", Record: map[string]any{"email": email}}
}

func TestBranchMapAttachesToExistingRowsAndAnswersAttached(t *testing.T) {
	pool := mapSetup(t)
	ctx := context.Background()
	sink := &countingAuditSink{}
	door := NewAttachDoor(NewStore(pool), KindBranch, &BranchMapWriter{Branches: branches.BranchCodeReader{}}, sink)

	r, err := door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-map-1", Caller: testActorID,
		Items: []Item{mapItem("B-1", fxBranchOneCode), mapItem("B-2", fxBranchTwoCode)}})
	if err != nil {
		t.Fatalf("branch map batch: %v", err)
	}
	if o := outcomeOf(t, r, "B-1"); o.Status != OutcomeAttached {
		t.Fatalf("first sight of a branch name answered %q, want %s", o.Status, OutcomeAttached)
	}
	if o := outcomeOf(t, r, "B-1"); o.HHID != fxBranchOneID {
		t.Fatalf("the attach answered hh id %q, want the pre existing branch %q", o.HHID, fxBranchOneID)
	}
	if o := outcomeOf(t, r, "B-2"); o.Status != OutcomeAttached || o.HHID != fxBranchTwoID {
		t.Fatalf("the second attach answered %+v, want attached to the second branch", r.Items[1])
	}
	if r.Counts[OutcomeAttached] != 2 || r.Counts[OutcomeCreated] != 0 {
		t.Fatalf("counts %+v, want two attached and none created", r.Counts)
	}
	// The map created no branch row: the two planted fixtures are still
	// the only rows under their own ids.
	var branches int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM locations WHERE id = ANY($1::uuid[])`,
		[]string{fxBranchOneID, fxBranchTwoID}).Scan(&branches); err != nil {
		t.Fatalf("branch count: %v", err)
	}
	if branches != 2 {
		t.Fatalf("%d fixture branch rows stand, want the two planted: the map created a branch", branches)
	}
	// The registry names carry the batch stamp and the content hash.
	var hash string
	if err := pool.QueryRow(ctx, `
		SELECT content_hash FROM load_key_registry
		WHERE org_id = $1::uuid AND source_system = $2 AND kind = $3 AND legacy_id = 'B-1'`,
		testOrgID, testSource, KindBranch).Scan(&hash); err != nil {
		t.Fatalf("registry read: %v", err)
	}
	if len(hash) != 64 {
		t.Fatalf("the attach recorded hash %q, want the content hash", hash)
	}
}

func TestBranchMapReplayAnswersUnchanged(t *testing.T) {
	pool := mapSetup(t)
	ctx := context.Background()
	door := NewAttachDoor(NewStore(pool), KindBranch, &BranchMapWriter{Branches: branches.BranchCodeReader{}}, &countingAuditSink{})
	item := mapItem("B-1", fxBranchOneCode)
	if _, err := door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-map-r1", Caller: testActorID, Items: []Item{item}}); err != nil {
		t.Fatalf("first batch: %v", err)
	}
	r, err := door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-map-r2", Caller: testActorID, Items: []Item{item}})
	if err != nil {
		t.Fatalf("replay batch: %v", err)
	}
	if o := outcomeOf(t, r, "B-1"); o.Status != OutcomeUnchanged {
		t.Fatalf("the identical replay answered %q, want unchanged", o.Status)
	}
}

func TestBranchMapRepointRefusesAsAConflict(t *testing.T) {
	pool := mapSetup(t)
	ctx := context.Background()
	door := NewAttachDoor(NewStore(pool), KindBranch, &BranchMapWriter{Branches: branches.BranchCodeReader{}}, &countingAuditSink{})
	if _, err := door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-map-c1", Caller: testActorID, Items: []Item{mapItem("B-1", fxBranchOneCode)}}); err != nil {
		t.Fatalf("first batch: %v", err)
	}
	r, err := door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-map-c2", Caller: testActorID, Items: []Item{mapItem("B-1", fxBranchTwoCode)}})
	if err != nil {
		t.Fatalf("re-point batch: %v", err)
	}
	o := outcomeOf(t, r, "B-1")
	if o.Status != OutcomeRefused || o.Reason != ReasonKeyConflict {
		t.Fatalf("a re-point answered %+v, want refused as %s", o, ReasonKeyConflict)
	}
	// The name still binds the first branch: nothing re-pointed.
	var hh string
	if err := pool.QueryRow(ctx, `
		SELECT hh_id::text FROM load_key_registry
		WHERE org_id = $1::uuid AND source_system = $2 AND kind = $3 AND legacy_id = 'B-1'`,
		testOrgID, testSource, KindBranch).Scan(&hh); err != nil {
		t.Fatalf("registry read: %v", err)
	}
	if hh != fxBranchOneID {
		t.Fatalf("the refused re-point moved the name to %q, want the first branch", hh)
	}
}

func TestBranchMapSecondNameForOneRowRefusesNamingBoth(t *testing.T) {
	pool := mapSetup(t)
	ctx := context.Background()
	door := NewAttachDoor(NewStore(pool), KindBranch, &BranchMapWriter{Branches: branches.BranchCodeReader{}}, &countingAuditSink{})
	if _, err := door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-map-d1", Caller: testActorID, Items: []Item{mapItem("B-1", fxBranchOneCode)}}); err != nil {
		t.Fatalf("first batch: %v", err)
	}
	// A second legacy key whose record names the same yard: the row a
	// second name would claim, refused naming both legacy ids.
	r, err := door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-map-d2", Caller: testActorID, Items: []Item{mapItem("B-7", fxBranchOneCode)}})
	if err != nil {
		t.Fatalf("second name batch: %v", err)
	}
	o := outcomeOf(t, r, "B-7")
	if o.Status != OutcomeRefused || o.Reason != ReasonKeyConflict {
		t.Fatalf("the second name answered %+v, want refused as %s", o, ReasonKeyConflict)
	}
	if !stringsContain(o.Detail, "B-1") || !stringsContain(o.Detail, "B-7") {
		t.Fatalf("conflict detail %q does not name both legacy ids", o.Detail)
	}
}

func TestBranchMapUnknownCodeRefusesNamingTheFieldNeverAValue(t *testing.T) {
	pool := mapSetup(t)
	door := NewAttachDoor(NewStore(pool), KindBranch, &BranchMapWriter{Branches: branches.BranchCodeReader{}}, &countingAuditSink{})
	r, err := door.UpsertBatch(context.Background(), Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-map-u1", Caller: testActorID, Items: []Item{mapItem("B-9", "NO-SUCH-YARD")}})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	o := outcomeOf(t, r, "B-9")
	if o.Status != OutcomeRefused || o.Reason != "no_branch_row" {
		t.Fatalf("an unknown code answered %+v, want refused as no_branch_row", o)
	}
	if o.Field != "code" {
		t.Fatalf("the refusal names field %q, want code", o.Field)
	}
	if stringsContain(o.Detail, "NO-SUCH-YARD") {
		t.Fatalf("the refusal detail %q carries the record value; refusals name the field and the legacy key, never a value", o.Detail)
	}
}

func TestBranchMapRefusesAReferenceItTakesNone(t *testing.T) {
	pool := mapSetup(t)
	ctx := context.Background()
	// A recorded category name the item could wrongly require: planted
	// directly, the map door itself never writes one.
	if _, err := pool.Exec(ctx, `
		INSERT INTO load_key_registry (org_id, source_system, kind, legacy_id, hh_id, content_hash)
		VALUES ($1::uuid, $2, 'category', 'CAT-1', gen_random_uuid(), repeat('a', 64))`,
		testOrgID, testSource); err != nil {
		t.Fatalf("plant category name: %v", err)
	}
	door := NewAttachDoor(NewStore(pool), KindBranch, &BranchMapWriter{Branches: branches.BranchCodeReader{}}, &countingAuditSink{})
	item := mapItem("B-1", fxBranchOneCode)
	item.Requires = []Ref{{Kind: "category", LegacyID: "CAT-1"}}
	r, err := door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-map-x1", Caller: testActorID, Items: []Item{item}})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if o := outcomeOf(t, r, "B-1"); o.Status != OutcomeRefused || o.Reason != ReasonWrongReference {
		t.Fatalf("a map item carrying a reference answered %+v, want refused as %s", o, ReasonWrongReference)
	}
}

func TestUserMapAttachesByVerifiedEmailAndNeverCreatesAUser(t *testing.T) {
	pool := mapSetup(t)
	ctx := context.Background()
	door := NewAttachDoor(NewStore(pool), KindUser, &UserMapWriter{Staff: identity.StaffEmailReader{}}, &countingAuditSink{})

	r, err := door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-umap-1", Caller: testActorID,
		Items: []Item{userItem("U-1", fxStaffOneEmail), userItem("U-2", fxStaffTwoEmail)}})
	if err != nil {
		t.Fatalf("user map batch: %v", err)
	}
	if o := outcomeOf(t, r, "U-1"); o.Status != OutcomeAttached || o.HHID != fxStaffOneID {
		t.Fatalf("the first user attach answered %+v, want attached to the first staff row", o)
	}
	if o := outcomeOf(t, r, "U-2"); o.Status != OutcomeAttached || o.HHID != fxStaffTwoID {
		t.Fatalf("the second user attach answered %+v, want attached to the invited staff row: the map reads rows that exist, whatever their invitation state", o)
	}
	// No user was created: the two planted fixtures are still the only
	// fixture domain rows in users.
	var staff int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM users WHERE email LIKE 'fixture.rep.%@example.invalid'`).Scan(&staff); err != nil {
		t.Fatalf("staff count: %v", err)
	}
	if staff != 2 {
		t.Fatalf("%d fixture staff rows stand, want the two planted: the user map created a user", staff)
	}
	// The identical replay answers unchanged.
	r2, err := door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-umap-2", Caller: testActorID,
		Items: []Item{userItem("U-1", fxStaffOneEmail)}})
	if err != nil {
		t.Fatalf("replay batch: %v", err)
	}
	if o := outcomeOf(t, r2, "U-1"); o.Status != OutcomeUnchanged {
		t.Fatalf("the identical replay answered %q, want unchanged", o.Status)
	}
}

func TestUserMapUnmatchedEmailRefusesNamingTheFieldNeverAValue(t *testing.T) {
	pool := mapSetup(t)
	door := NewAttachDoor(NewStore(pool), KindUser, &UserMapWriter{Staff: identity.StaffEmailReader{}}, &countingAuditSink{})
	r, err := door.UpsertBatch(context.Background(), Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-umap-u1", Caller: testActorID,
		Items: []Item{userItem("U-9", "fixture.nobody@example.invalid")}})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	o := outcomeOf(t, r, "U-9")
	if o.Status != OutcomeRefused || o.Reason != "no_staff_row" {
		t.Fatalf("an unmatched email answered %+v, want refused as no_staff_row", o)
	}
	if o.Field != "email" {
		t.Fatalf("the refusal names field %q, want email", o.Field)
	}
	if stringsContain(o.Detail, "fixture.nobody@example.invalid") {
		t.Fatalf("the refusal detail %q carries the email; refusals name the field and the legacy key, never a value", o.Detail)
	}
	// The item whose record names no email at all refuses the same field.
	r2, err := door.UpsertBatch(context.Background(), Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-umap-u2", Caller: testActorID,
		Items: []Item{{LegacyID: "U-8", Record: map[string]any{}}}})
	if err != nil {
		t.Fatalf("batch without an email: %v", err)
	}
	o2 := outcomeOf(t, r2, "U-8")
	if o2.Status != OutcomeRefused || o2.Field != "email" {
		t.Fatalf("a record with no email answered %+v, want refused naming the field email", o2)
	}
}

func TestUserMapRepointRefusesAsAConflict(t *testing.T) {
	pool := mapSetup(t)
	ctx := context.Background()
	door := NewAttachDoor(NewStore(pool), KindUser, &UserMapWriter{Staff: identity.StaffEmailReader{}}, &countingAuditSink{})
	if _, err := door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-umap-c1", Caller: testActorID, Items: []Item{userItem("U-1", fxStaffOneEmail)}}); err != nil {
		t.Fatalf("first batch: %v", err)
	}
	r, err := door.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: "b-umap-c2", Caller: testActorID, Items: []Item{userItem("U-1", fxStaffTwoEmail)}})
	if err != nil {
		t.Fatalf("re-point batch: %v", err)
	}
	if o := outcomeOf(t, r, "U-1"); o.Status != OutcomeRefused || o.Reason != ReasonKeyConflict {
		t.Fatalf("a re-point answered %+v, want refused as %s", o, ReasonKeyConflict)
	}
}

// The contention harness. contended_probe carries two shared rows the
// writers touch in opposite orders: one batch's writer locks s1 then
// wants s2 while the other holds s2 and wants s1. Postgres kills one
// whole, and that batch must answer the ErrBatchContended sentinel with
// nothing of it standing.
const contentionProbeTable = `CREATE TABLE IF NOT EXISTS contended_probe (
	id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	org_id     UUID NOT NULL,
	code       TEXT NOT NULL UNIQUE,
	payload    TEXT NOT NULL DEFAULT '',
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`

// contentionWriter is the coordinating test writer. Its Apply locks the
// two shared rows in its own order and waits for its peer at the pinned
// point, so the two batches always meet in the deadlock corner; the
// errors wrap the statement's own (%w) so the frame's deadlock
// recognition still sees the Postgres error, the rule the doors' real
// writers carry.
type contentionWriter struct {
	order   string // "s1s2" signals first, "s2s1" waits first
	peer    chan struct{}
	settled chan struct{} // closed once the first round is over; later runs wait free
}

func (w contentionWriter) Apply(ctx context.Context, tx pgx.Tx, in Write) (string, error) {
	code, _ := in.Record["code"].(string)
	first, second := "s1", "s2"
	if w.order == "s2s1" {
		first, second = "s2", "s1"
	}
	lock := func(k string) error {
		if _, err := tx.Exec(ctx,
			`UPDATE contended_probe SET payload = payload, updated_at = now() WHERE org_id = $1::uuid AND code = $2`,
			in.OrgID, k); err != nil {
			return fmt.Errorf("contended probe lock %s: %w", k, err)
		}
		return nil
	}
	if err := lock(first); err != nil {
		return "", err
	}
	if w.order == "s1s2" {
		select {
		case w.peer <- struct{}{}:
		default:
		}
	} else {
		select {
		case <-w.peer:
		case <-w.settled:
		}
	}
	if err := lock(second); err != nil {
		return "", err
	}
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO contended_probe (org_id, code, payload)
		VALUES ($1::uuid, $2, 'item')
		ON CONFLICT (code) DO UPDATE SET payload = 'item', updated_at = now()
		RETURNING id::text`, in.OrgID, code).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("contended probe write: %w", err)
	}
	return id, nil
}

func TestContendedBatchAnswersTheSentinelAndResubmissionConverges(t *testing.T) {
	pool := mapSetup(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, contentionProbeTable); err != nil {
		t.Fatalf("contention table: %v", err)
	}
	for _, code := range []string{"s1", "s2"} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO contended_probe (org_id, code) VALUES ($1::uuid, $2) ON CONFLICT (code) DO NOTHING`,
			testOrgID, code); err != nil {
			t.Fatalf("plant shared row %s: %v", code, err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DROP TABLE IF EXISTS contended_probe`)
	})

	settled := make(chan struct{})
	one := contentionWriter{order: "s1s2", peer: make(chan struct{}, 1), settled: settled}
	two := contentionWriter{order: "s2s1", peer: one.peer, settled: settled}
	doorOne := NewDoor(NewStore(pool), "contended", one, &countingAuditSink{})
	doorTwo := NewDoor(NewStore(pool), "contended", two, &countingAuditSink{})

	results := make([]BatchResult, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	watchdog := time.AfterFunc(30*time.Second, func() {
		t.Errorf("the contention leg never finished even past its watchdog: the deadlock did not resolve")
	})
	defer watchdog.Stop()
	for k := 0; k < 2; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			batch := Batch{OrgID: testOrgID, SourceSystem: testSource, Caller: testActorID,
				BatchID: fmt.Sprintf("b-deadlock-%d", k+1),
				Items: []Item{{LegacyID: fmt.Sprintf("D-%d", k+1),
					Record: map[string]any{"code": fmt.Sprintf("d%d", k+1)}}}}
			door := doorOne
			if k == 1 {
				door = doorTwo
			}
			results[k], errs[k] = door.UpsertBatch(ctx, batch)
		}(k)
	}
	wg.Wait()
	close(settled)

	victim := -1
	for k, err := range errs {
		if err == nil {
			continue
		}
		if !errors.Is(err, ErrBatchContended) {
			t.Fatalf("racer %d answered %v, want the ErrBatchContended sentinel or a clean answer", k, err)
		}
		if victim != -1 {
			t.Fatalf("both racers answered the sentinel; Postgres kills one victim, not two")
		}
		victim = k
	}
	if victim == -1 {
		t.Fatalf("neither racer answered the sentinel: the opposite order locks did not deadlock")
	}
	winner := 1 - victim
	if o := outcomeOf(t, results[winner], fmt.Sprintf("D-%d", winner+1)); o.Status != OutcomeCreated {
		t.Fatalf("the survivor answered %+v, want created", o)
	}

	// Nothing of the victim stands: no batch record, no registry name, no
	// item row.
	victimBatchID := fmt.Sprintf("b-deadlock-%d", victim+1)
	victimName := fmt.Sprintf("D-%d", victim+1)
	victimCode := fmt.Sprintf("d%d", victim+1)
	var batches, names, rows int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM load_batch WHERE org_id = $1::uuid AND source_system = $2 AND batch_id = $3),
			(SELECT count(*) FROM load_key_registry WHERE org_id = $1::uuid AND source_system = $2 AND legacy_id = $4),
			(SELECT count(*) FROM contended_probe WHERE org_id = $1::uuid AND code = $5)`,
		testOrgID, testSource, victimBatchID, victimName, victimCode).Scan(&batches, &names, &rows); err != nil {
		t.Fatalf("victim remainder read: %v", err)
	}
	if batches != 0 || names != 0 || rows != 0 {
		t.Fatalf("the victim left %d batch rows, %d names, %d item rows: a contended batch must roll back whole", batches, names, rows)
	}

	// The resubmission converges: the victim's batch id is free again, the
	// batch lands, and both names stand with their rows.
	r, err := doorOne.UpsertBatch(ctx, Batch{OrgID: testOrgID, SourceSystem: testSource,
		BatchID: victimBatchID, Caller: testActorID,
		Items: []Item{{LegacyID: victimName, Record: map[string]any{"code": victimCode}}}})
	if err != nil {
		t.Fatalf("resubmission: %v", err)
	}
	if o := outcomeOf(t, r, victimName); o.Status != OutcomeCreated {
		t.Fatalf("the resubmission answered %+v, want created", o)
	}
	for _, name := range []string{"D-1", "D-2"} {
		var n int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM load_key_registry
			WHERE org_id = $1::uuid AND source_system = $2 AND kind = 'contended' AND legacy_id = $3`,
			testOrgID, testSource, name).Scan(&n); err != nil {
			t.Fatalf("converged name read %s: %v", name, err)
		}
		if n != 1 {
			t.Fatalf("name %s stands %d times after the resubmission, want one", name, n)
		}
	}
}
