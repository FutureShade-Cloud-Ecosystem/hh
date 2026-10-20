//go:build integration

// seqtrigger_drawbeforelock_integration_test.go: the DRAW BEFORE LOCK class (PR 556, rounds 3c and 3d).
//
// On a trigger table a write that relies on the trigger takes the ROW first (the BEFORE trigger fires after the
// row lock) and the COUNTER after. A statement that draws the counter explicitly in a data modifying CTE
// (WITH seq AS (INSERT INTO scope_sequences ...) UPDATE ... SET seq = (SELECT next_value FROM seq)) evaluates
// the CTE when it builds the new row, which is BEFORE the update locks the row: the COUNTER first, the row
// after. Two such writers on one row cross.
//
// THE METHOD. A leg that parks a writer through an AFTER trigger on scope_sequences can never stand a CTE
// statement in that window: a data modifying CTE's AFTER triggers fire at the end of the whole statement, after
// the outer UPDATE has locked every row. So the legs here park the OTHER writer, the one that relies on the
// trigger, between its row lock and its draw, with a BEFORE INSERT trigger on scope_sequences, which fires
// before the upsert takes the counter row. That writer holds the row and no counter. The CTE writer then runs:
// in the crossing shape it draws the counter and waits for the row; the test releases the park only when it is
// seen waiting; the parked writer then wants the counter. The fixed writers lock their rows first (or rely on
// the trigger), so they wait on the row holding nothing.
package dispatch

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// - Part B: S2's two multi row writers, and the freight raise path -

// The sync cancel's un-assign: a statement that draws the stops counter in a CTE and updates the run's READY
// stops. A single stop write (here the trigger relying touch) holds one of those rows between its lock and its
// draw. The cancel locks the run's READY stops, ascending, before the statement.
func TestIntegration_DrawBeforeLock_SyncCancelUnassignBesideAStopWrite(t *testing.T) {
	f := newSyncFixture(t)
	run := syncOpenRun(t, f)
	orderID := syncSeedOrder(t, f, testkit.BranchNorthAID)
	key := syncFreshKey()
	syncApplied(t, f, "stops", f.north, "create", key, map[string]any{
		"order_id": orderID, "scheduled_date": syncToday(), "run_id": run.ID,
		"address_line": "Fixture address", "city": "Fixture City"}, nil)
	stopID := syncRowOf(t, f, "stops", f.north, key).ID

	p := testkit.NewDrawPark(t, f.pool, "stops", 4242010)
	errStop := p.TouchRow("delivery_stops", "updated_at = now()", stopID)
	p.WaitParked("the stop write between its row lock and its draw")

	errCancel := make(chan error, 1)
	go func() { errCancel <- f.cancelRun(run.ID) }()
	p.WaitBlocked("the cancel waiting for the stop row")
	p.Answers(map[string]chan error{"stop write": errStop, "sync cancel": errCancel})
}

// AttachPODPhotos draws the pod counter in a CTE and updates every unattached photo of the stop; a pod caption
// edit holds one photo between its row lock and its draw. The attach is a transaction that locks the photos,
// ascending, first.
func TestIntegration_DrawBeforeLock_AttachPODPhotosBesideAPhotoWrite(t *testing.T) {
	f := newSyncFixture(t)
	run := syncOpenRun(t, f)
	stop := syncEnRouteStop(t, f, run)
	tiny := "ZklYIFNZTkMgUEhPVE8="
	pkey := syncFreshKey()
	syncApplied(t, f, "pod", f.north, "create", pkey, map[string]any{
		"stop_id": stop.ID, "content_type": "image/png", "image_bytes_b64": tiny, "caption": "at the gate"}, nil)
	photoID := syncRowOf(t, f, "pod", f.north, pkey).ID

	p := testkit.NewDrawPark(t, f.pool, "pod", 4242011)
	errPhoto := p.TouchRow("delivery_pod_photos", "caption = 'checked at the gate'", photoID)
	p.WaitParked("the photo write between its row lock and its draw")

	errAttach := make(chan error, 1)
	go func() {
		_, err := NewRepository(f.pool).AttachPODPhotos(context.Background(), stop.ID, "FXSIG-"+uniqueSuffix(), time.Now().UTC())
		errAttach <- err
	}()
	p.WaitBlocked("the attach waiting for the photo row")
	p.Answers(map[string]chan error{"photo write": errPhoto, "AttachPODPhotos": errAttach})
}

// cancelRun pushes the sync cancel of a run through the engine on the fixture's pool.
func (f *syncFixture) cancelRun(runID string) error {
	info, _ := sync.Lookup("runs")
	res, err := f.engine.Push(context.Background(), info, f.north, []apigen.SyncPushOperation{
		syncOp("update", syncFreshKey(), map[string]any{"id": runID, "status": RunCancelled}, nil)})
	if err == nil && len(res.Results) == 1 && res.Results[0].Status != "applied" {
		err = fmt.Errorf("the sync cancel answered %+v", res.Results[0])
	}
	return err
}

// The freight raise apply mints the loads the raise asked for (an INSERT the trigger draws the stops counter
// for, held to commit) BEFORE the split locks the order's stops. A single stop write holds one of those stops
// between its row lock and its draw: the apply holds the counter and waits for the row. The apply locks the
// order's live loads, ascending, FOR NO KEY UPDATE, before its first mint.
func TestIntegration_DrawBeforeLock_FreightRaiseApplyWithAMintBesideAStopWrite(t *testing.T) {
	h := newFreightHarness(t)
	ctx := h.dispatcherCtx(t)
	orderID, _ := h.seedQuotedOrder(t, branchKGN, cents(26000), 26000)
	if _, err := h.svc.SplitFreight(ctx, SplitInput{OrderID: orderID, Loads: 3}); err != nil {
		t.Fatalf("in-quote split: %v", err)
	}
	res, err := h.svc.SplitFreight(ctx, SplitInput{OrderID: orderID, Loads: 4, FreightCents: cents(34000)})
	if err != nil || res.Parked == nil {
		t.Fatalf("the raise must be parked: %v %+v", err, res.Parked)
	}
	var raiseID, stopID, deciderID string
	if err := h.pool.QueryRow(context.Background(), `SELECT id::text FROM delivery_freight_raises WHERE order_id = $1::uuid`, orderID).Scan(&raiseID); err != nil {
		t.Fatalf("read the raise: %v", err)
	}
	if err := h.pool.QueryRow(context.Background(), `SELECT id::text FROM delivery_stops WHERE order_id = $1::uuid ORDER BY id DESC LIMIT 1`, orderID).Scan(&stopID); err != nil {
		t.Fatalf("read a load: %v", err)
	}
	if err := h.pool.QueryRow(context.Background(), `SELECT id::text FROM users WHERE status = 'ACTIVE' LIMIT 1`).Scan(&deciderID); err != nil {
		t.Skipf("no seeded user: %v", err)
	}

	p := testkit.NewDrawPark(t, h.pool, "stops", 4242012)
	errStop := p.TouchRow("delivery_stops", "updated_at = now()", stopID)
	p.WaitParked("the stop write between its row lock and its draw")

	errApply := make(chan error, 1)
	go func() {
		tx, err := h.pool.Begin(context.Background())
		if err != nil {
			errApply <- err
			return
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		if err := h.svc.ApplyFreightRaiseTx(context.Background(), tx, raiseID, deciderID); err != nil {
			errApply <- err
			return
		}
		errApply <- tx.Commit(context.Background())
	}()
	p.WaitBlocked("the apply waiting after its mint")
	p.Answers(map[string]chan error{"stop write": errStop, "ApplyFreightRaiseTx": errApply})
}

// - Part A: the single row writers that drew in a CTE before locking their row -

// AssignStop (delivery_stops) drew the stops counter in a CTE, then locked the stop. A writer that relies on the
// trigger (the touch here; the sync stop write and the freight and board writers are the real ones) holds the
// row between its lock and its draw. AssignStop now relies on the trigger too, so both take the row first.
func TestIntegration_DrawBeforeLock_AssignStopBesideAStopWrite(t *testing.T) {
	f := newSyncFixture(t)
	run := syncOpenRun(t, f)
	orderID := syncSeedOrder(t, f, testkit.BranchNorthAID)
	key := syncFreshKey()
	syncApplied(t, f, "stops", f.north, "create", key, map[string]any{
		"order_id": orderID, "scheduled_date": syncToday(),
		"address_line": "Fixture address", "city": "Fixture City"}, nil)
	stopID := syncRowOf(t, f, "stops", f.north, key).ID

	p := testkit.NewDrawPark(t, f.pool, "stops", 4242013)
	errTouch := p.TouchRow("delivery_stops", "updated_at = now()", stopID)
	p.WaitParked("the stop write between its row lock and its draw")
	errAssign := make(chan error, 1)
	go func() {
		ok, err := NewRepository(f.pool).AssignStop(context.Background(), stopID, run.ID, 1)
		if err == nil && !ok {
			err = fmt.Errorf("AssignStop moved no row")
		}
		errAssign <- err
	}()
	p.WaitBlocked("AssignStop waiting for the stop row")
	p.Answers(map[string]chan error{"stop write": errTouch, "AssignStop": errAssign})
}

// AssignRunCrew (delivery_runs): the same shape on the run row.
func TestIntegration_DrawBeforeLock_AssignRunCrewBesideARunWrite(t *testing.T) {
	f := newSyncFixture(t)
	run := syncOpenRun(t, f)
	p := testkit.NewDrawPark(t, f.pool, "runs", 4242014)
	errTouch := p.TouchRow("delivery_runs", "updated_at = now()", run.ID)
	p.WaitParked("the run write between its row lock and its draw")
	errCrew := make(chan error, 1)
	go func() {
		ok, err := NewRepository(f.pool).AssignRunCrew(context.Background(), run.ID, f.truck, f.driver)
		if err == nil && !ok {
			err = fmt.Errorf("AssignRunCrew moved no row")
		}
		errCrew <- err
	}()
	p.WaitBlocked("AssignRunCrew waiting for the run row")
	p.Answers(map[string]chan error{"run write": errTouch, "AssignRunCrew": errCrew})
}

// - Part B, the method recheck (PR 556, round 3d) -
//
// The run writers and the freight split were first proved beside a single stop write with the RUN WRITER parked
// through an AFTER trigger on scope_sequences. For a writer that takes its counter in a standalone statement or
// through a row trigger that park is valid; for a CTE statement it never reaches the draw before lock window.
// These legs re-prove the same verdicts the right way round: the SINGLE stop write is the parked session, holding
// one stop row between its lock and its draw (a BEFORE INSERT trigger on scope_sequences), and the writer under
// test runs on the same stop. A writer that took the stops counter before the stop rows waits for the row while
// holding the counter; the release makes the stop write want the counter: 40P01.
func TestIntegration_DrawBeforeLock_RunAndSplitWritersBesideAStopWrite(t *testing.T) {
	writers := map[string]func(f *syncFixture, runID string, stopIDs []string) error{
		"ReopenRun": func(f *syncFixture, runID string, _ []string) error {
			return NewRepository(f.pool).ReopenRun(context.Background(), runID, "recheck leg", "", time.Now())
		},
		"MoveRunDate": func(f *syncFixture, runID string, _ []string) error {
			from := syncToday()
			to := time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")
			_, _, _, err := NewRepository(f.pool).MoveRunDate(context.Background(), runID, from, to, Reschedule{
				BranchID: testkit.BranchNorthAID, EntityType: RescheduleEntityRun, EntityID: runID,
				FromDate: from, ToDate: to, Reason: "recheck leg"})
			return err
		},
		"WriteFreightSplit": func(f *syncFixture, _ string, stopIDs []string) error {
			return NewRepository(f.pool).WriteFreightSplit(context.Background(), stopIDs, []int64{1000, 2000}, true, 2)
		},
	}
	keys := map[string]int64{"ReopenRun": 4242012, "MoveRunDate": 4242013, "WriteFreightSplit": 4242014}
	// Each stop is parked in turn: a writer that updates the run's stops one row at a time takes the counter on the
	// first row it visits and closes the cycle on the second, and which stop that is follows the order the UPDATE
	// visits the rows, not the id order (P3-1). Parking both covers whichever it is.
	for name, write := range writers {
		for which := 0; which < 2; which++ {
			t.Run(fmt.Sprintf("%s beside a write of stop %d", name, which), func(t *testing.T) {
				f := newSyncFixture(t)
				run := syncOpenRun(t, f)
				var ids []string
				for i := 0; i < 2; i++ {
					orderID := syncSeedOrder(t, f, testkit.BranchNorthAID)
					key := syncFreshKey()
					syncApplied(t, f, "stops", f.north, "create", key, map[string]any{
						"order_id": orderID, "scheduled_date": syncToday(), "run_id": run.ID,
						"address_line": "Fixture address", "city": "Fixture City"}, nil)
					ids = append(ids, syncRowOf(t, f, "stops", f.north, key).ID)
				}
				sort.Strings(ids)

				p := testkit.NewDrawPark(t, f.pool, "stops", keys[name])
				errStop := p.TouchRow("delivery_stops", "updated_at = now()", ids[which])
				p.WaitParked("the stop write between its row lock and its draw")

				errWrite := make(chan error, 1)
				go func() { errWrite <- write(f, run.ID, ids) }()
				p.WaitBlocked("the writer waits")
				p.Answers(map[string]chan error{"stop write": errStop, name: errWrite})
			})
		}
	}
}
