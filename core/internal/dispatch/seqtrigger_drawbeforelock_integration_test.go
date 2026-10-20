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
