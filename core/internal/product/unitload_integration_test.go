//go:build integration

// unitload_integration_test.go proves the load doors' unit writer
// (HH-LOAD-1 LD-3): the catalogue upsert by code, the attach of a legacy
// key to a unit that already exists with the same code and dimension
// (outcome attached), the refusal of a code clash of another dimension
// naming the field, the org mismatch refusal, the replay arms, and the
// registry record. The catalogue is the deployment wide vocabulary of
// the plan's reading 3: the unit's HH identity is its code, and the
// registry names it with the code's derived row id (UnitRowID), a
// reading this file pins and the item report carries.
//
// Fixtures are invented: the FXU codes, the org and actor ids. The
// fixture rows leave with the test.
//
// Run (the database URL rides the command line, never a default):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5670/ld3units?sslmode=disable' \
//	  go test -tags integration ./internal/product/... -count=1 -run TestUnitLoad
package product

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The unit door's fixture constants, invented.
const (
	fxUnitOrgID   = "4542a800-0000-4000-8000-00000000d101"
	fxUnitOtherID = "4542a800-0000-4000-8000-00000000d102"
	fxUnitActorID = "4542a800-0000-4000-8000-00000000d10a"
	fxUnitSource  = "testsuite"
)

// unitLoadEnv is one test's fixtures: the pool, the door, the spy.
type unitLoadEnv struct {
	pool *pgxpool.Pool
	door *load.Door
	spy  *loadAuditSpy
}

func unitLoadSetup(t *testing.T) *unitLoadEnv {
	t.Helper()
	pool := intPool(t)
	ctx := context.Background()
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM load_key_registry WHERE org_id = ANY($1::uuid[])`, []any{[]string{fxUnitOrgID, fxUnitOtherID}}},
		{`DELETE FROM load_batch WHERE org_id = ANY($1::uuid[])`, []any{[]string{fxUnitOrgID, fxUnitOtherID}}},
		{`DELETE FROM uoms WHERE code LIKE 'FXU-%'`, nil},
		{`UPDATE uoms SET org_id = NULL, idempotency_key = NULL WHERE code IN ('PLT','SF')`, nil},
	} {
		if _, err := pool.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("unit fixture clean: %v", err)
		}
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM load_key_registry WHERE org_id = ANY($1::uuid[])`, []string{fxUnitOrgID, fxUnitOtherID})
		_, _ = pool.Exec(ctx, `DELETE FROM load_batch WHERE org_id = ANY($1::uuid[])`, []string{fxUnitOrgID, fxUnitOtherID})
		_, _ = pool.Exec(ctx, `DELETE FROM uoms WHERE code LIKE 'FXU-%'`)
		_, _ = pool.Exec(ctx, `UPDATE uoms SET org_id = NULL, idempotency_key = NULL WHERE code IN ('PLT','SF')`)
		pool.Close()
	})
	spy := &loadAuditSpy{}
	store := load.NewStore(pool)
	return &unitLoadEnv{
		pool: pool,
		door: load.NewDoor(store, load.KindUnit, UnitLoadWriter{}, spy),
		spy:  spy,
	}
}

// unitItem is one unit batch item; the derived key is a canonical
// fixture uuid keyed off the legacy id.
func unitItem(legacyID, code, name, dimension string) load.Item {
	suffix := map[string]string{
		"UU-1":     "000000000101",
		"UU-2":     "000000000102",
		"UU-CLASH": "000000000103",
		"UU-DIM":   "000000000104",
		"UU-ORG":   "000000000105",
		"UU-CASE2": "000000000106",
		"UU-PAD":   "000000000107",
	}[legacyID]
	return load.Item{
		LegacyID:   legacyID,
		DerivedKey: "d2000000-0000-4000-8000-" + suffix,
		Record:     map[string]any{"code": code, "name": name, "dimension": dimension},
	}
}

// unitOutcome picks one item's outcome out of a batch result.
func unitOutcome(t *testing.T, r load.BatchResult, legacyID string) load.Outcome {
	t.Helper()
	for _, o := range r.Items {
		if o.LegacyID == legacyID {
			return o
		}
	}
	t.Fatalf("batch answered no outcome for %s: %+v", legacyID, r.Items)
	return load.Outcome{}
}

func TestUnitLoadCreatesAttachesAndRefuses(t *testing.T) {
	e := unitLoadSetup(t)
	ctx := context.Background()

	// The create arm: a code the catalogue does not carry lands a new
	// unit, stamped the load's own, and the registry records the name
	// against the unit's derived row id.
	r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxUnitOrgID, SourceSystem: fxUnitSource,
		BatchID: "b-unit-1", Caller: fxUnitActorID,
		Items: []load.Item{unitItem("UU-1", "FXU-1", "Fixture Unit One", "count")}})
	if err != nil {
		t.Fatalf("create batch: %v", err)
	}
	if o := unitOutcome(t, r, "UU-1"); o.Status != load.OutcomeCreated {
		t.Fatalf("the fresh unit answered %+v, want created", o)
	}
	var name, dimension string
	var org, idem string
	if err := e.pool.QueryRow(ctx,
		`SELECT name, dimension, coalesce(org_id::text, ''), coalesce(idempotency_key::text, '')
		 FROM uoms WHERE code = 'FXU-1'`).Scan(&name, &dimension, &org, &idem); err != nil {
		t.Fatalf("created unit read: %v", err)
	}
	if name != "Fixture Unit One" || dimension != "count" {
		t.Fatalf("created unit carries name %q dimension %q", name, dimension)
	}
	if org != fxUnitOrgID || idem == "" {
		t.Fatalf("created unit carries org %q idempotency %q: the load's stamps are missing", org, idem)
	}
	// The registry name stands, and it names the derived row id.
	wantID := UnitRowID("FXU-1")
	var hhID string
	if err := e.pool.QueryRow(ctx,
		`SELECT hh_id::text FROM load_key_registry
		 WHERE org_id = $1::uuid AND source_system = $2 AND kind = $3 AND legacy_id = 'UU-1'`,
		fxUnitOrgID, fxUnitSource, load.KindUnit).Scan(&hhID); err != nil {
		t.Fatalf("unit name read: %v", err)
	}
	if hhID != wantID {
		t.Fatalf("the registry names the unit %s, want the derived row id %s", hhID, wantID)
	}

	// The attach arm: the seeded PLT already exists as a count unit, so
	// the item attaches to it and stamps it the load's own; no second
	// row appears and the outcome is the attached word.
	r, err = e.door.UpsertBatch(ctx, load.Batch{OrgID: fxUnitOrgID, SourceSystem: fxUnitSource,
		BatchID: "b-unit-2", Caller: fxUnitActorID,
		Items: []load.Item{unitItem("UU-2", "PLT", "Pallet", "count")}})
	if err != nil {
		t.Fatalf("attach batch: %v", err)
	}
	if o := unitOutcome(t, r, "UU-2"); o.Status != load.OutcomeAttached {
		t.Fatalf("the seeded unit answered %+v, want attached", o)
	}
	if o := unitOutcome(t, r, "UU-2"); o.HHID != UnitRowID("PLT") {
		t.Fatalf("the attach answered hh id %q, want the seeded row's derived id", o.HHID)
	}
	var rows int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM uoms WHERE code = 'PLT'`).Scan(&rows); err != nil {
		t.Fatalf("attach row count: %v", err)
	}
	if rows != 1 {
		t.Fatalf("%d PLT rows stand after the attach, want the one seeded row", rows)
	}
	var org2 string
	if err := e.pool.QueryRow(ctx, `SELECT coalesce(org_id::text, '') FROM uoms WHERE code = 'PLT'`).Scan(&org2); err != nil {
		t.Fatalf("attach org read: %v", err)
	}
	if org2 != fxUnitOrgID {
		t.Fatalf("the attached unit carries org %q, want the load's own stamp", org2)
	}

	// The replay arm: the identical record answers unchanged and writes
	// nothing new; a changed name answers updated.
	r, err = e.door.UpsertBatch(ctx, load.Batch{OrgID: fxUnitOrgID, SourceSystem: fxUnitSource,
		BatchID: "b-unit-3", Caller: fxUnitActorID,
		Items: []load.Item{unitItem("UU-2", "PLT", "Pallet", "count")}})
	if err != nil {
		t.Fatalf("replay batch: %v", err)
	}
	if o := unitOutcome(t, r, "UU-2"); o.Status != load.OutcomeUnchanged {
		t.Fatalf("the replayed unit answered %+v, want unchanged", o)
	}
	r, err = e.door.UpsertBatch(ctx, load.Batch{OrgID: fxUnitOrgID, SourceSystem: fxUnitSource,
		BatchID: "b-unit-4", Caller: fxUnitActorID,
		Items: []load.Item{unitItem("UU-2", "PLT", "Pallet (fixture)", "count")}})
	if err != nil {
		t.Fatalf("update batch: %v", err)
	}
	if o := unitOutcome(t, r, "UU-2"); o.Status != load.OutcomeUpdated {
		t.Fatalf("the changed unit answered %+v, want updated", o)
	}
	var name2 string
	if err := e.pool.QueryRow(ctx, `SELECT name FROM uoms WHERE code = 'PLT'`).Scan(&name2); err != nil {
		t.Fatalf("update read: %v", err)
	}
	if name2 != "Pallet (fixture)" {
		t.Fatalf("the updated unit carries name %q", name2)
	}
}

func TestUnitLoadRefusesACodeClashOfAnotherDimension(t *testing.T) {
	e := unitLoadSetup(t)
	ctx := context.Background()
	// PLT is a count unit; the same code naming an area dimension is a
	// code clash of another dimension and refuses naming the field and
	// the legacy key, with no value in the detail.
	r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxUnitOrgID, SourceSystem: fxUnitSource,
		BatchID: "b-unit-clash", Caller: fxUnitActorID,
		Items: []load.Item{unitItem("UU-CLASH", "PLT", "Pallet Area", "area")}})
	if err != nil {
		t.Fatalf("clash batch: %v", err)
	}
	o := unitOutcome(t, r, "UU-CLASH")
	if o.Status != load.OutcomeRefused {
		t.Fatalf("the clash answered %+v, want refused", o)
	}
	if o.Reason != "dimension_clash" || o.Field != "dimension" {
		t.Fatalf("the clash refusal carries reason %q field %q, want dimension_clash on dimension", o.Reason, o.Field)
	}
	if !strings.Contains(o.Detail, "UU-CLASH") {
		t.Fatalf("the clash refusal names no legacy key: %q", o.Detail)
	}
	if strings.Contains(o.Detail, "Pallet") || strings.Contains(o.Detail, "area") {
		t.Fatalf("the clash refusal carries a record value: %q", o.Detail)
	}
	var rows int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM uoms WHERE code = 'PLT' AND dimension = 'area'`).Scan(&rows); err != nil {
		t.Fatalf("clash row read: %v", err)
	}
	if rows != 0 {
		t.Fatalf("the clash wrote a row: nothing of a refused item stands")
	}
}

func TestUnitLoadRefusesADimensionTheCatalogueDoesNotCarry(t *testing.T) {
	e := unitLoadSetup(t)
	ctx := context.Background()
	r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxUnitOrgID, SourceSystem: fxUnitSource,
		BatchID: "b-unit-dim", Caller: fxUnitActorID,
		Items: []load.Item{unitItem("UU-DIM", "FXU-9", "Fixture Money Unit", "money")}})
	if err != nil {
		t.Fatalf("dimension batch: %v", err)
	}
	o := unitOutcome(t, r, "UU-DIM")
	if o.Status != load.OutcomeRefused || o.Field != "dimension" {
		t.Fatalf("the unknown dimension answered %+v, want refused on dimension", o)
	}
	var rows int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM uoms WHERE code = 'FXU-9'`).Scan(&rows); err != nil {
		t.Fatalf("dimension row read: %v", err)
	}
	if rows != 0 {
		t.Fatalf("the unknown dimension wrote a row")
	}
}

// TestUnitLoadRefusesAPaddedCode: a code with a leading or trailing
// space is a different spelling of the catalogue's row, not a unit
// (the delta review's probe: FXU-CASE with a trailing space landed its
// own row beside FXU-CASE). The door refuses as a record shape on code,
// naming the field, never the value, and nothing lands.
func TestUnitLoadRefusesAPaddedCode(t *testing.T) {
	e := unitLoadSetup(t)
	ctx := context.Background()
	submit := func(batchID, code string) load.Outcome {
		t.Helper()
		r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxUnitOrgID, SourceSystem: fxUnitSource,
			BatchID: batchID, Caller: fxUnitActorID,
			Items:   []load.Item{unitItem("UU-PAD", code, "Fixture Padded Unit", "count")}})
		if err != nil {
			t.Fatalf("padded code batch %s: %v", batchID, err)
		}
		return unitOutcome(t, r, "UU-PAD")
	}
	for _, probe := range []struct {
		batch, code string
	}{{"b-unit-pad-t", "FXU-PAD "}, {"b-unit-pad-l", " FXU-PAD"}} {
		o := submit(probe.batch, probe.code)
		if o.Status != load.OutcomeRefused || o.Reason != "record_shape" || o.Field != "code" {
			t.Fatalf("the padded code %q answered %+v, want refused as record_shape on code", probe.code, o)
		}
		if strings.Contains(o.Detail, "FXU") {
			t.Fatalf("the padded code refusal carries a value: %q", o.Detail)
		}
	}
	var rows int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM uoms WHERE code LIKE '%FXU-PAD%'`).Scan(&rows); err != nil {
		t.Fatalf("padded code row read: %v", err)
	}
	if rows != 0 {
		t.Fatalf("the padded code wrote %d rows, want none", rows)
	}
}

func TestUnitLoadRefusesARowAnotherOrgOwns(t *testing.T) {
	e := unitLoadSetup(t)
	ctx := context.Background()
	// Another org's row stands in the shared catalogue: the load never
	// re-stamps a row's org, so the item refuses naming the kind and the
	// key.
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO uoms (code, name, dimension, org_id) VALUES ('FXU-ORG', 'Fixture Held Unit', 'count', $1::uuid)
		 ON CONFLICT (code) DO UPDATE SET org_id = EXCLUDED.org_id`, fxUnitOtherID); err != nil {
		t.Fatalf("plant held unit: %v", err)
	}
	r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxUnitOrgID, SourceSystem: fxUnitSource,
		BatchID: "b-unit-org", Caller: fxUnitActorID,
		Items: []load.Item{unitItem("UU-ORG", "FXU-ORG", "Fixture Held Unit Renamed", "count")}})
	if err != nil {
		t.Fatalf("org batch: %v", err)
	}
	o := unitOutcome(t, r, "UU-ORG")
	if o.Status != load.OutcomeRefused || o.Reason != "org_mismatch" {
		t.Fatalf("the foreign org row answered %+v, want org_mismatch", o)
	}
	if !strings.Contains(o.Detail, "UU-ORG") {
		t.Fatalf("the org refusal names no legacy key: %q", o.Detail)
	}
}

func TestUnitLoadRefusesASecondNameOnOneUnitRow(t *testing.T) {
	e := unitLoadSetup(t)
	ctx := context.Background()
	// UU-1 already names the FXU-1 row; a second legacy key claiming the
	// same row refuses as a key conflict naming both keys.
	r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxUnitOrgID, SourceSystem: fxUnitSource,
		BatchID: "b-unit-1", Caller: fxUnitActorID,
		Items: []load.Item{unitItem("UU-1", "FXU-1", "Fixture Unit One", "count")}})
	if err != nil {
		t.Fatalf("first name batch: %v", err)
	}
	if o := unitOutcome(t, r, "UU-1"); o.Status != load.OutcomeCreated {
		t.Fatalf("the first name answered %+v, want created", o)
	}
	r, err = e.door.UpsertBatch(ctx, load.Batch{OrgID: fxUnitOrgID, SourceSystem: fxUnitSource,
		BatchID: "b-unit-claim", Caller: fxUnitActorID,
		Items: []load.Item{unitItem("UU-DIM", "FXU-1", "Fixture Unit One", "count")}})
	if err != nil {
		t.Fatalf("second name batch: %v", err)
	}
	o := unitOutcome(t, r, "UU-DIM")
	if o.Status != load.OutcomeRefused || o.Reason != load.ReasonKeyConflict {
		t.Fatalf("the second name answered %+v, want a key conflict", o)
	}
	if !strings.Contains(o.Detail, "UU-1") || !strings.Contains(o.Detail, "UU-DIM") {
		t.Fatalf("the conflict names not both keys: %q", o.Detail)
	}
}

// TestUnitLoadRefusesACaseVariantClaimingOneRow: the catalogue's code is
// CITEXT, so a case variant of a recorded code is the same row. The
// registry must name that row once: the second legacy key is refused as
// a key conflict naming both keys, and the first key replays unchanged.
func TestUnitLoadRefusesACaseVariantClaimingOneRow(t *testing.T) {
	e := unitLoadSetup(t)
	ctx := context.Background()
	r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxUnitOrgID, SourceSystem: fxUnitSource,
		BatchID: "b-unit-case1", Caller: fxUnitActorID,
		Items: []load.Item{unitItem("UU-1", "FXU-CASE", "Fixture Case Unit", "count")}})
	if err != nil {
		t.Fatalf("first spelling batch: %v", err)
	}
	if o := unitOutcome(t, r, "UU-1"); o.Status != load.OutcomeCreated {
		t.Fatalf("the first spelling answered %+v, want created", o)
	}
	if o := unitOutcome(t, r, "UU-1"); o.HHID != UnitRowID("FXU-CASE") {
		t.Fatalf("the first spelling named %s, want the stored code's derived id", o.HHID)
	}
	// The same row under the other spelling and a second legacy key: the
	// registry refuses the second name, naming both keys.
	r, err = e.door.UpsertBatch(ctx, load.Batch{OrgID: fxUnitOrgID, SourceSystem: fxUnitSource,
		BatchID: "b-unit-case2", Caller: fxUnitActorID,
		Items: []load.Item{unitItem("UU-CASE2", "fxu-case", "Fixture Case Unit", "count")}})
	if err != nil {
		t.Fatalf("case variant batch: %v", err)
	}
	o := unitOutcome(t, r, "UU-CASE2")
	if o.Status != load.OutcomeRefused || o.Reason != load.ReasonKeyConflict {
		t.Fatalf("the case variant answered %+v, want a key conflict: one unit row takes one name", o)
	}
	if !strings.Contains(o.Detail, "UU-1") || !strings.Contains(o.Detail, "UU-CASE2") {
		t.Fatalf("the conflict names not both keys: %q", o.Detail)
	}
	var names int
	if err := e.pool.QueryRow(ctx,
		`SELECT count(*) FROM load_key_registry
		 WHERE org_id = $1::uuid AND source_system = $2 AND kind = $3 AND hh_id = $4::uuid`,
		fxUnitOrgID, fxUnitSource, load.KindUnit, UnitRowID("FXU-CASE")).Scan(&names); err != nil {
		t.Fatalf("case variant name count: %v", err)
	}
	if names != 1 {
		t.Fatalf("%d names stand on one unit row, want one", names)
	}
	// The same key replays unchanged.
	r, err = e.door.UpsertBatch(ctx, load.Batch{OrgID: fxUnitOrgID, SourceSystem: fxUnitSource,
		BatchID: "b-unit-case3", Caller: fxUnitActorID,
		Items: []load.Item{unitItem("UU-1", "FXU-CASE", "Fixture Case Unit", "count")}})
	if err != nil {
		t.Fatalf("case replay batch: %v", err)
	}
	if o := unitOutcome(t, r, "UU-1"); o.Status != load.OutcomeUnchanged {
		t.Fatalf("the replayed first spelling answered %+v, want unchanged", o)
	}
}

func TestUnitLoadResolveKeysAnswersTheUnitName(t *testing.T) {
	e := unitLoadSetup(t)
	ctx := context.Background()
	store := load.NewStore(e.pool)
	door := load.NewDoor(store, load.KindUnit, UnitLoadWriter{}, &loadAuditSpy{})
	if _, err := door.UpsertBatch(ctx, load.Batch{OrgID: fxUnitOrgID, SourceSystem: fxUnitSource,
		BatchID: "b-unit-res", Caller: fxUnitActorID,
		Items: []load.Item{unitItem("UU-1", "FXU-1", "Fixture Unit One", "count")}}); err != nil {
		t.Fatalf("resolve setup batch: %v", err)
	}
	res, err := store.ResolveKeys(ctx, fxUnitOrgID, fxUnitSource,
		[]load.Ref{{Kind: load.KindUnit, LegacyID: "UU-1"}, {Kind: load.KindUnit, LegacyID: "UU-MISSING"}})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Counts["found"] != 1 || res.Counts["missing"] != 1 {
		t.Fatalf("resolve counts %+v, want one found and one missing", res.Counts)
	}
	if res.Items[0].HHID != UnitRowID("FXU-1") {
		t.Fatalf("resolve answered %q, want the derived unit row id", res.Items[0].HHID)
	}
}

// TestUnitLoadPoolFourThreeContendersEveryCallOnTheBatchTransaction is
// the pool 4 leg for the unit door (decision row 786): three contending
// batches on a pool of four, the suite holding the fourth connection,
// all finish, one created row per code, the rest attached or unchanged.
func TestUnitLoadPoolFourThreeContendersEveryCallOnTheBatchTransaction(t *testing.T) {
	e := unitLoadSetup(t)
	pool := poolFour(t)
	ctx := context.Background()
	held, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("hold one connection: %v", err)
	}
	defer held.Release()
	store := load.NewStore(pool)
	door := load.NewDoor(store, load.KindUnit, UnitLoadWriter{}, &loadAuditSpy{})
	items := []load.Item{unitItem("UU-1", "FXU-1", "Fixture Unit One", "count"),
		unitItem("UU-2", "PLT", "Pallet", "count")}
	var wg sync.WaitGroup
	results := make([]load.BatchResult, 3)
	errs := make([]error, 3)
	for k := 0; k < 3; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, poolFourBatchTimeout)
			defer cancel()
			results[k], errs[k] = door.UpsertBatch(cctx, load.Batch{OrgID: fxUnitOrgID,
				SourceSystem: fxUnitSource, BatchID: fmt.Sprintf("b-unit-pool-%d", k), Caller: fxUnitActorID,
				Items: append([]load.Item(nil), items...)})
		}(k)
	}
	wg.Wait()
	for k, err := range errs {
		if err != nil {
			t.Fatalf("contender %d: %v (a batch needed a connection beyond the transaction it holds)", k, err)
		}
	}
	attached := 0
	created := 0
	for k := range results {
		for _, o := range results[k].Items {
			switch o.Status {
			case load.OutcomeCreated:
				created++
			case load.OutcomeAttached:
				attached++
			case load.OutcomeUnchanged, load.OutcomeUpdated:
			default:
				t.Fatalf("contender %d answered %+v", k, o)
			}
		}
	}
	if created != 1 {
		t.Fatalf("%d creates across the contenders for the fresh code, want exactly one", created)
	}
	if attached != 1 {
		t.Fatalf("%d attaches across the contenders for the seeded code, want exactly one", attached)
	}
	var rows int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM uoms WHERE code LIKE 'FXU-1'`).Scan(&rows); err != nil {
		t.Fatalf("pool row count: %v", err)
	}
	if rows != 1 {
		t.Fatalf("%d fixture rows stand after three contenders, want one", rows)
	}
}

// strayUnitWriter is the deliberate violation: a writer that reaches for
// the pool while the batch holds its transaction. The pool leg is only
// worth its name if this one fails it.
type strayUnitWriter struct {
	pool  *pgxpool.Pool
	inner UnitLoadWriter
}

func (w strayUnitWriter) Apply(ctx context.Context, tx pgx.Tx, in load.Write) (string, error) {
	if _, err := w.pool.Exec(ctx, `SELECT 1`); err != nil {
		return "", err
	}
	return w.inner.Apply(ctx, tx, in)
}

func (w strayUnitWriter) ApplyAttached(ctx context.Context, tx pgx.Tx, in load.Write) (string, bool, error) {
	id, err := w.Apply(ctx, tx, in)
	return id, false, err
}

func TestUnitLoadPoolFourBitesAWriterThatUsesThePool(t *testing.T) {
	unitLoadSetup(t)
	pool := poolFour(t)
	ctx := context.Background()
	held, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("hold one connection: %v", err)
	}
	defer held.Release()
	door := load.NewDoor(load.NewStore(pool), load.KindUnit,
		strayUnitWriter{pool: pool}, &loadAuditSpy{})
	var wg sync.WaitGroup
	errs := make([]error, 3)
	for k := 0; k < 3; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			_, errs[k] = door.UpsertBatch(cctx, load.Batch{OrgID: fxUnitOrgID,
				SourceSystem: fxUnitSource, BatchID: fmt.Sprintf("b-unit-stray-%d", k), Caller: fxUnitActorID,
				Items: []load.Item{unitItem("UU-1", "FXU-1", "Fixture Unit One", "count")}})
		}(k)
	}
	wg.Wait()
	for k, err := range errs {
		if err == nil {
			t.Fatalf("contender %d finished: the pool 4 leg missed a writer that uses the pool", k)
		}
	}
}

// TestUnitLoadDeadlockAnswersTheContentionSentinel is the real deadlock
// leg through the real writer: an outside transaction holds the second
// unit row while the batch holds the first unit's locks and wants the
// second, the outside then wants the first, and Postgres kills the batch
// as the victim. The batch answers load.ErrBatchContended, nothing of it
// stands, and the resubmission converges.
func TestUnitLoadDeadlockAnswersTheContentionSentinel(t *testing.T) {
	e := unitLoadSetup(t)
	ctx := context.Background()
	// Two unit rows stand with names recorded, so the batch runs the
	// update arm and locks the uoms rows in its item order.
	if _, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxUnitOrgID, SourceSystem: fxUnitSource,
		BatchID: "b-unit-dl-seed", Caller: fxUnitActorID,
		Items: []load.Item{unitItem("UU-1", "FXU-1", "Fixture Unit One", "count"),
			unitItem("UU-2", "FXU-2", "Fixture Unit Two", "count")}}); err != nil {
		t.Fatalf("deadlock seed batch: %v", err)
	}
	// The names must take the update arm, so the recorded hash is
	// replaced by one no submission carries.
	if _, err := e.pool.Exec(ctx,
		`UPDATE load_key_registry SET content_hash = repeat('a', 64)
		 WHERE org_id = $1::uuid AND source_system = $2 AND kind = $3`,
		fxUnitOrgID, fxUnitSource, load.KindUnit); err != nil {
		t.Fatalf("deadlock hash plant: %v", err)
	}
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
	if _, err := outside.Exec(ctx, `UPDATE uoms SET name = name WHERE code = 'FXU-2'`); err != nil {
		t.Fatalf("outside lock: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		_, err := e.door.UpsertBatch(cctx, load.Batch{OrgID: fxUnitOrgID, SourceSystem: fxUnitSource,
			BatchID: "b-unit-dl", Caller: fxUnitActorID,
			Items: []load.Item{unitItem("UU-1", "FXU-1", "Fixture Unit One A", "count"),
				unitItem("UU-2", "FXU-2", "Fixture Unit Two A", "count")}})
		done <- err
	}()
	waitForLockWait(t, e.pool)
	// The outside now wants the first unit row the batch holds: the
	// cycle closes and Postgres kills the batch.
	_, _ = outside.Exec(ctx, `UPDATE uoms SET name = name WHERE code = 'FXU-1'`)
	select {
	case err := <-done:
		if !errors.Is(err, load.ErrBatchContended) {
			t.Fatalf("the deadlocked batch answered %v, want the ErrBatchContended sentinel", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatalf("the deadlock never resolved")
	}
	_ = outside.Rollback(ctx)
	// Nothing of the victim stands: no batch row, the first item's
	// update rolled back with it.
	var batches int
	var name string
	if err := e.pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM load_batch WHERE org_id = $1::uuid AND batch_id = 'b-unit-dl'),
		       (SELECT name FROM uoms WHERE code = 'FXU-1')`, fxUnitOrgID).Scan(&batches, &name); err != nil {
		t.Fatalf("victim remainder read: %v", err)
	}
	if batches != 0 || name != "Fixture Unit One" {
		t.Fatalf("the victim left %d batch rows and name %q: a contended batch rolls back whole", batches, name)
	}
	// The resubmission converges.
	r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxUnitOrgID, SourceSystem: fxUnitSource,
		BatchID: "b-unit-dl", Caller: fxUnitActorID,
		Items: []load.Item{unitItem("UU-1", "FXU-1", "Fixture Unit One A", "count"),
			unitItem("UU-2", "FXU-2", "Fixture Unit Two A", "count")}})
	if err != nil {
		t.Fatalf("deadlock resubmission: %v", err)
	}
	for _, o := range r.Items {
		if o.Status == load.OutcomeRefused {
			t.Fatalf("the resubmission refused %+v", o)
		}
	}
}
