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
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// drawPark parks sessions of its own pool (application name sf1-park) right before they take the counter row
// of one scope, on an advisory lock the test holds.
type drawPark struct {
	t       *testing.T
	base    *pgxpool.Pool
	Pool    *pgxpool.Pool // the park pool: run the single row writer on it
	hold    *pgxpool.Conn
	key     int64
	scope   string
	release func()
}

func newDrawPark(t *testing.T, base *pgxpool.Pool, scope string, key int64) *drawPark {
	t.Helper()
	ctx := context.Background()
	for _, q := range []string{
		`CREATE OR REPLACE FUNCTION sf1_park_before() RETURNS trigger LANGUAGE plpgsql AS $$
		 BEGIN
		   IF NEW.scope = TG_ARGV[0] AND current_setting('application_name') = 'sf1-park' THEN
		     PERFORM pg_advisory_xact_lock(TG_ARGV[1]::bigint);
		   END IF;
		   RETURN NEW;
		 END $$`,
		`DROP TRIGGER IF EXISTS sf1_park_before ON scope_sequences`,
		fmt.Sprintf(`CREATE TRIGGER sf1_park_before BEFORE INSERT ON scope_sequences FOR EACH ROW EXECUTE FUNCTION sf1_park_before('%s', '%d')`, scope, key),
		fmt.Sprintf(`INSERT INTO scope_sequences (scope, next_value) VALUES ('%s', 0) ON CONFLICT (scope) DO NOTHING`, scope),
	} {
		if _, err := base.Exec(ctx, q); err != nil {
			t.Fatalf("park trigger: %v", err)
		}
	}
	cfg := base.Config().Copy()
	cfg.ConnConfig.RuntimeParams["application_name"] = "sf1-park"
	cfg.MaxConns = 2
	park, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("park pool: %v", err)
	}
	hold, err := base.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if _, err := hold.Exec(ctx, `SELECT pg_advisory_lock($1)`, key); err != nil {
		t.Fatalf("hold: %v", err)
	}
	p := &drawPark{t: t, base: base, Pool: park, hold: hold, key: key, scope: scope}
	released := false
	p.release = func() {
		if !released {
			released = true
			_, _ = hold.Exec(ctx, `SELECT pg_advisory_unlock($1)`, key)
		}
	}
	t.Cleanup(func() {
		p.release()
		hold.Release()
		park.Close()
		c := context.Background()
		_, _ = base.Exec(c, `DROP TRIGGER IF EXISTS sf1_park_before ON scope_sequences`)
		_, _ = base.Exec(c, `DROP FUNCTION IF EXISTS sf1_park_before()`)
	})
	return p
}

// waitParked blocks until a park pool session stands on the advisory lock.
func (p *drawPark) waitParked(what string) {
	p.t.Helper()
	waitCount(p.t, p.base, "parked: "+what, `SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND NOT granted`)
}

// waitBlocked blocks until a session waits on a row or counter lock (not the park).
func (p *drawPark) waitBlocked(what string) {
	p.t.Helper()
	waitCount(p.t, p.base, "blocked: "+what,
		`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND wait_event <> 'advisory'`)
}

func waitCount(t *testing.T, pool *pgxpool.Pool, what, q string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		var n int
		if err := pool.QueryRow(context.Background(), q).Scan(&n); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if n > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("never saw: %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// answers releases the park and requires every writer to finish without a deadlock victim.
func (p *drawPark) answers(chs map[string]chan error) {
	p.t.Helper()
	p.release()
	for who, ch := range chs {
		select {
		case err := <-ch:
			if err != nil && (strings.Contains(err.Error(), "40P01") || strings.Contains(err.Error(), "deadlock")) {
				p.t.Fatalf("%s was killed as a deadlock victim: %v", who, err)
			}
			if err != nil {
				p.t.Logf("%s answered: %v", who, err)
			}
		case <-time.After(30 * time.Second):
			p.t.Fatalf("%s never finished", who)
		}
	}
}

// touchRow is the trigger relying single row writer: it changes a row and lets the BEFORE UPDATE trigger draw.
// Run on the park pool it stands between its row lock and its draw.
func (p *drawPark) touchRow(table, set, id string) chan error {
	ch := make(chan error, 1)
	go func() {
		_, err := p.Pool.Exec(context.Background(),
			fmt.Sprintf(`UPDATE %s SET %s WHERE id = $1::uuid`, table, set), id)
		ch <- err
	}()
	return ch
}

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

	p := newDrawPark(t, f.pool, "stops", 4242010)
	errStop := p.touchRow("delivery_stops", "updated_at = now()", stopID)
	p.waitParked("the stop write between its row lock and its draw")

	errCancel := make(chan error, 1)
	go func() { errCancel <- f.cancelRun(run.ID) }()
	p.waitBlocked("the cancel waiting for the stop row")
	p.answers(map[string]chan error{"stop write": errStop, "sync cancel": errCancel})
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

	p := newDrawPark(t, f.pool, "pod", 4242011)
	errPhoto := p.touchRow("delivery_pod_photos", "caption = 'checked at the gate'", photoID)
	p.waitParked("the photo write between its row lock and its draw")

	errAttach := make(chan error, 1)
	go func() {
		_, err := NewRepository(f.pool).AttachPODPhotos(context.Background(), stop.ID, "FXSIG-"+uniqueSuffix(), time.Now().UTC())
		errAttach <- err
	}()
	p.waitBlocked("the attach waiting for the photo row")
	p.answers(map[string]chan error{"photo write": errPhoto, "AttachPODPhotos": errAttach})
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

	p := newDrawPark(t, h.pool, "stops", 4242012)
	errStop := p.touchRow("delivery_stops", "updated_at = now()", stopID)
	p.waitParked("the stop write between its row lock and its draw")

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
	p.waitBlocked("the apply waiting after its mint")
	p.answers(map[string]chan error{"stop write": errStop, "ApplyFreightRaiseTx": errApply})
}
