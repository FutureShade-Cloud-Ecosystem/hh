//go:build integration

// syncscope_driver_fence_pool_integration_test.go: the attachments
// driver fence must never ask the shared pool for a second connection
// while a write already holds one in an open transaction, and the
// driver's pull must not stall behind a page of colleagues' rows.
//
// The pool the deploy runs is small (pgx sets max(4, CPUs)), and the
// real seam (dispatch's DriverStopFence) reads over a pool of its own
// that production wires to the same shared pool. So a fence call made
// inside a transaction is a second acquisition before the first is
// released: pool-size driver writes that overlap each hold a connection
// and wait for one more, and the cycle closes through the client side
// pool where the database cannot see it.
package attachments

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	hhsync "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// pooledFence is the seam double that behaves like the real adapter on
// the one point that matters here: answering costs a connection from
// the store's own pool.
type pooledFence struct {
	pool    *pgxpool.Pool
	driver  string
	allowed map[string]bool
}

func (s pooledFence) StopsOnDriverRuns(ctx context.Context, _, actor string, candidates []string) (map[string]bool, bool, error) {
	if actor != s.driver {
		return map[string]bool{}, false, nil
	}
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, true, err
	}
	defer conn.Release()
	var one int
	if err := conn.QueryRow(ctx, `SELECT 1`).Scan(&one); err != nil {
		return nil, true, err
	}
	out := map[string]bool{}
	for _, id := range candidates {
		if s.allowed[id] {
			out[id] = true
		}
	}
	return out, true, nil
}

func fencePool(t *testing.T, url string, maxConns int32) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	cfg.MaxConns = maxConns
	p, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("build pool: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// seedFenceOrg plants one org and registers its cleanup.
func seedFenceOrg(t *testing.T, pool *pgxpool.Pool, orgID, slug string) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO org (id, slug, name) VALUES ($1, $2::text, $2::text) ON CONFLICT (id) DO NOTHING`, orgID, slug); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM attachment_link l USING attachment a
			WHERE l.attachment_id = a.id AND a.org_id = $1`, orgID)
		_, _ = pool.Exec(c, `DELETE FROM attachment WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(c, `DELETE FROM org WHERE id = $1`, orgID)
	})
}

func stopLink(stopID string) []any {
	return []any{map[string]any{"owner_kind": "stop", "owner_id": stopID, "role": "proof"}}
}

// TestIntegration_AttachmentsDriverFence_WritesNeverTakeASecondConnection
// forces the overlap: a blocker transaction on a separate pool holds the
// attachment row's lock, pool-size driver writes (and then deletes) on
// that row each open a transaction and queue inside it, and once the
// probe sees pool-size sessions waiting the blocker commits. Unfixed
// the first caller to win the lock asks the full pool for the fence's
// connection and the shared bound fires; fixed every caller answers.
func TestIntegration_AttachmentsDriverFence_WritesNeverTakeASecondConnection(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("attachments driver fence pool leg: DATABASE_URL not set")
	}
	const (
		poolSize   = 4
		stallBound = 10 * time.Second
		orgID      = "a0e10000-0000-4000-8000-0000000000e1"
		ownStop    = "a0e10000-0000-4000-8000-0000000000e2"
		driver     = "fx-pool-driver"
	)
	ctx := context.Background()
	scenario := fencePool(t, url, poolSize)
	blockerPool := fencePool(t, url, 1)
	probe := fencePool(t, url, 1)
	seedFenceOrg(t, probe, orgID, "attachments-driver-fence-pool-leg")

	store := NewSyncStore(scenario).WithDriverStopFence(pooledFence{
		pool: scenario, driver: driver, allowed: map[string]bool{ownStop: true},
	})
	// The row is registered by the desk (not bound), then held by a blocker.
	desk := hhsync.ScopeFilter{OrgID: orgID, Actor: "fx-pool-desk"}
	filter := hhsync.ScopeFilter{OrgID: orgID, Actor: driver}

	for _, op := range []string{"write", "delete"} {
		rs, err := store.Create(ctx, desk, fmt.Sprintf("a1e10000-0000-4000-8000-0000000000%02x", len(op)), map[string]any{
			"kind": "photo", "appwrite_file_id": "fx-pool-" + op, "bucket": "fx-bucket", "links": stopLink(ownStop),
		})
		if err != nil {
			t.Fatalf("create %s fixture: %v", op, err)
		}
		blocker, err := blockerPool.Begin(ctx)
		if err != nil {
			t.Fatalf("blocker begin: %v", err)
		}
		open := true
		t.Cleanup(func() {
			if open {
				_ = blocker.Rollback(context.Background())
			}
		})
		if _, err := blocker.Exec(ctx, `SELECT 1 FROM attachment WHERE id = $1::uuid FOR UPDATE`, rs.ID); err != nil {
			t.Fatalf("blocker lock: %v", err)
		}

		scenarioCtx, cancel := context.WithTimeout(ctx, stallBound)
		type outcome struct {
			tag string
			err error
		}
		outCh := make(chan outcome, poolSize)
		var wg sync.WaitGroup
		for i := 1; i <= poolSize; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				var err error
				key := fmt.Sprintf("a1e10000-0000-4000-8000-0000000001%02d", i)
				if op == "write" {
					_, err = store.Write(scenarioCtx, filter, rs.ID, key, map[string]any{"id": rs.ID, "mime": fmt.Sprintf("image/x-%d", i)})
				} else {
					_, err = store.Delete(scenarioCtx, filter, rs.ID, key)
				}
				outCh <- outcome{tag: fmt.Sprintf("%s-%d", op, i), err: err}
			}(i)
		}
		waiting := 0
		for scenarioCtx.Err() == nil {
			if err := probe.QueryRow(ctx, `
				SELECT count(*) FROM pg_stat_activity
				WHERE datname = current_database() AND wait_event_type = 'Lock' AND pid <> pg_backend_pid()`).Scan(&waiting); err != nil {
				t.Fatalf("probe: %v", err)
			}
			if waiting >= poolSize {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if waiting < poolSize {
			cancel()
			wg.Wait()
			t.Fatalf("%s: the rig did not put %d callers inside their transactions (saw %d waiting on a lock)", op, poolSize, waiting)
		}
		if err := blocker.Commit(ctx); err != nil {
			t.Fatalf("blocker commit: %v", err)
		}
		open = false
		wg.Wait()
		close(outCh)
		stalled := errors.Is(scenarioCtx.Err(), context.DeadlineExceeded)
		cancel()
		var bad []string
		for o := range outCh {
			// A delete races its colleagues: the first tombstones, the
			// rest answer not found. Neither is a stall.
			if o.err != nil && !errors.Is(o.err, hhsync.ErrRowNotFound) {
				bad = append(bad, fmt.Sprintf("%s: %v", o.tag, o.err))
			}
		}
		if stalled {
			t.Fatalf("STALL (%s): driver writes held their transactions and the %s bound fired; the fence asked the full pool for a second connection inside the transaction. Outcomes: %s",
				op, stallBound, strings.Join(bad, "; "))
		}
		if len(bad) > 0 {
			t.Fatalf("%s: unexpected outcomes: %s", op, strings.Join(bad, "; "))
		}
	}
}

// TestIntegration_AttachmentsDriverFence_AnOrderLinkedPhotoIsNotTheDrivers:
// an attachment with no stop link at all (an order photo, or a bare row)
// is the desk's business. The fence must still bind the driver caller
// when a page or a row reaches no stop, or the driver reads every such
// row in the org.
func TestIntegration_AttachmentsDriverFence_AnOrderLinkedPhotoIsNotTheDrivers(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("attachments driver fence order leg: DATABASE_URL not set")
	}
	const (
		orgID   = "a0e10000-0000-4000-8000-0000000000c1"
		ownStop = "a0e10000-0000-4000-8000-0000000000c2"
		driver  = "fx-order-driver"
	)
	ctx := context.Background()
	pool := fencePool(t, url, 4)
	seedFenceOrg(t, pool, orgID, "attachments-driver-fence-order-leg")
	store := NewSyncStore(pool).WithDriverStopFence(fenceStub{driver: driver, allowed: map[string]bool{ownStop: true}})
	desk := hhsync.ScopeFilter{OrgID: orgID, Actor: "fx-order-desk"}
	drv := hhsync.ScopeFilter{OrgID: orgID, Actor: driver}

	rs, err := store.Create(ctx, desk, "a1e10000-0000-4000-8000-000000000301", map[string]any{
		"kind": "photo", "appwrite_file_id": "fx-order", "bucket": "fx-bucket",
		"links": []any{map[string]any{"owner_kind": "order", "owner_id": "a0e10000-0000-4000-8000-0000000000c9", "role": "proof"}},
	})
	if err != nil {
		t.Fatalf("desk create order photo: %v", err)
	}
	// The only row in the org reaches no stop, so the page the driver
	// pulls carries no stop candidate at all.
	items, _, err := store.Pull(ctx, drv, 0, 50)
	if err != nil {
		t.Fatalf("driver pull: %v", err)
	}
	for _, e := range items {
		if e.ID == rs.ID {
			t.Errorf("the driver's pull answered the desk's order-linked attachment %s", rs.ID)
		}
	}
	if _, found, err := store.Load(ctx, drv, rs.ID); err != nil || found {
		t.Errorf("the driver's Load of the order-linked attachment: found=%v err=%v", found, err)
	}
	if _, err := store.Create(ctx, drv, "a1e10000-0000-4000-8000-000000000302", map[string]any{
		"kind": "photo", "appwrite_file_id": "fx-bare", "bucket": "fx-bucket",
	}); err == nil {
		t.Errorf("the driver registered an attachment that reaches no stop of theirs")
	}
}

// TestIntegration_AttachmentsDriverFence_AColleaguesPageDoesNotStallThePull:
// the pull filters after the page is cut, so a page made only of
// colleagues' rows used to answer empty with no cursor and the driver's
// own later photo never arrived.
func TestIntegration_AttachmentsDriverFence_AColleaguesPageDoesNotStallThePull(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("attachments driver fence pull leg: DATABASE_URL not set")
	}
	const (
		orgID    = "a0e10000-0000-4000-8000-0000000000d1"
		ownStop  = "a0e10000-0000-4000-8000-0000000000d2"
		awayStop = "a0e10000-0000-4000-8000-0000000000d3"
		driver   = "fx-stall-driver"
		limit    = 3
	)
	ctx := context.Background()
	pool := fencePool(t, url, 4)
	seedFenceOrg(t, pool, orgID, "attachments-driver-fence-stall-leg")
	store := NewSyncStore(pool).WithDriverStopFence(fenceStub{driver: driver, allowed: map[string]bool{ownStop: true}})
	desk := hhsync.ScopeFilter{OrgID: orgID, Actor: "fx-stall-desk"}

	mk := func(i int, stop string) string {
		rs, err := store.Create(ctx, desk, fmt.Sprintf("a1e10000-0000-4000-8000-0000000002%02d", i), map[string]any{
			"kind": "photo", "appwrite_file_id": fmt.Sprintf("fx-stall-%d", i), "bucket": "fx-bucket", "links": stopLink(stop),
		})
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		return rs.ID
	}
	for i := 1; i <= limit+2; i++ {
		mk(i, awayStop)
	}
	own := mk(limit+3, ownStop)

	items, _, err := store.Pull(ctx, hhsync.ScopeFilter{OrgID: orgID, Actor: driver}, 0, limit)
	if err != nil {
		t.Fatalf("driver pull: %v", err)
	}
	if len(items) != 1 || items[0].ID != own {
		t.Errorf("the driver's first pull must reach past the colleagues' page to their own photo %s, got %d items", own, len(items))
	}
}

// TestIntegration_AttachmentsDriverFence_AWriteCannotRelinkOntoAColleaguesStop:
// the target half of the write fence. A held driver's own attachment may
// be relinked only to stops on their own runs: a link set naming a
// colleague's stop (alone or beside their own), or none that reaches
// their stops, would write the photo onto a colleague's stop and hide it
// from the driver. Create holds the same target rule. The desk keeps
// every relink.
func TestIntegration_AttachmentsDriverFence_AWriteCannotRelinkOntoAColleaguesStop(t *testing.T) {
	w := newFenceWorld(t)
	ctx := context.Background()
	own := "a0e10000-0000-4000-8000-0000000000f2"
	away := "a0e10000-0000-4000-8000-0000000000f3"
	links := func(stops ...string) []any {
		out := []any{}
		for _, s := range stops {
			out = append(out, map[string]any{"owner_kind": "stop", "owner_id": s, "role": "proof"})
		}
		return out
	}
	relink := func(f hhsync.ScopeFilter, key string, ls []any) error {
		// every case starts from the attachment on the driver's own stop
		if _, err := w.store.Write(ctx, w.desk, w.own, fenceKey("reset"+key), map[string]any{"id": w.own, "links": links(own)}); err != nil {
			t.Fatalf("reset links: %v", err)
		}
		_, err := w.store.Write(ctx, f, w.own, fenceKey(key), map[string]any{"id": w.own, "links": ls})
		return err
	}
	if err := relink(w.driver, "r1", links(away)); err == nil {
		t.Errorf("the driver relinked their attachment onto a colleague's stop")
	}
	if err := relink(w.driver, "r2", links(own, away)); err == nil {
		t.Errorf("the driver added a colleague's stop beside their own")
	}
	if err := relink(w.driver, "r3", []any{map[string]any{"owner_kind": "order", "owner_id": "a0e10000-0000-4000-8000-0000000000f9", "role": "proof"}}); err == nil {
		t.Errorf("the driver relinked their attachment off their stops")
	}
	if err := relink(w.driver, "r4", links()); err == nil {
		t.Errorf("the driver emptied their attachment's link set")
	}
	if _, err := w.store.Write(ctx, w.desk, w.own, fenceKey("resetu"), map[string]any{"id": w.own, "links": links(own)}); err != nil {
		t.Fatalf("reset links: %v", err)
	}
	if _, err := w.store.UpdateMetadata(ctx, w.driver, w.own, map[string]any{"id": w.own, "links": links(away)}); err == nil {
		t.Errorf("the driver's desk-path update relinked onto a colleague's stop")
	}
	if _, err := w.store.Create(ctx, w.driver, "a1e10000-0000-4000-8000-0000000000c2", map[string]any{
		"kind": "photo", "appwrite_file_id": "fx-both", "bucket": "fx-bucket", "links": links(own, away),
	}); err == nil {
		t.Errorf("the driver registered an attachment onto a colleague's stop beside their own")
	}
	// the colleague's stop never gained a link
	if rs, found, _ := w.store.Load(ctx, w.desk, w.own); found {
		ls, _ := rs.Data["links"].([]any)
		for _, l := range ls {
			if m, _ := l.(map[string]any); m["owner_id"] == away {
				t.Errorf("the colleague's stop gained the driver's photo")
			}
		}
	}
	// controls
	if err := relink(w.driver, "r5", links(own)); err != nil {
		t.Errorf("the driver could not keep their attachment on their own stop: %v", err)
	}
	if err := relink(w.desk, "r6", links(away)); err != nil {
		t.Errorf("the desk could not relink: %v", err)
	}
}
