//go:build integration

// syncscope_driver_fence_integration_test.go: the attachments scope's
// driver fence (the HH Drive fence's attachments leg, the drive-run
// app's declared scopes). A driver caller sees only the attachments
// linked to stops on their own runs, the fact the dispatch module owns
// and answers through the DriverStopFence seam this store fences
// through; the seam here is the kit double, and the dispatch-backed
// seam's own truth is pinned in the dispatch package's fence test plus
// the composition root's boot fail-fast.
package attachments

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	hhsync "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// fenceStub is the seam double: one driver caller whose stops are the
// allowed set, and every other actor a desk caller the fence does not
// bind.
type fenceStub struct {
	driver  string
	allowed map[string]bool
}

func (s fenceStub) StopsOnDriverRuns(_ context.Context, _, actor string, candidates []string) (map[string]bool, bool, error) {
	if actor != s.driver {
		return map[string]bool{}, false, nil
	}
	out := map[string]bool{}
	for _, id := range candidates {
		if s.allowed[id] {
			out[id] = true
		}
	}
	return out, true, nil
}

// fenceWorld seeds one org, two attachments (one linked to a stop on
// the driver's runs, one to a colleague's stop) and returns the store
// wired with the double seam plus the three filters.
type fenceWorld struct {
	store  *SyncStore
	orgID  string
	own    string
	away   string
	driver hhsync.ScopeFilter
	desk   hhsync.ScopeFilter
}

func newFenceWorld(t *testing.T) *fenceWorld {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("attachments driver fence leg: DATABASE_URL not set (the leg needs a migrated database and skips politely without it)")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)

	orgID := "a0e10000-0000-4000-8000-0000000000f1"
	if _, err := pool.Exec(ctx,
		`INSERT INTO org (id, slug, name) VALUES ($1, 'attachments-driver-fence-leg', 'attachments driver fence leg')
		 ON CONFLICT (id) DO NOTHING`,
		orgID); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM attachment_link l USING attachment a
			WHERE l.attachment_id = a.id AND a.org_id = $1`, orgID)
		_, _ = pool.Exec(c, `DELETE FROM attachment WHERE org_id = $1`, orgID)
		_, _ = pool.Exec(c, `DELETE FROM org WHERE id = $1`, orgID)
	})

	const driverActor = "fx-driver-a"
	const deskActor = "fx-desk"
	ownStop := "a0e10000-0000-4000-8000-0000000000f2"  // a stop on the driver's run
	awayStop := "a0e10000-0000-4000-8000-0000000000f3" // a stop on the colleague's run
	store := NewSyncStore(pool).WithDriverStopFence(fenceStub{
		driver:  driverActor,
		allowed: map[string]bool{ownStop: true},
	})
	desk := hhsync.ScopeFilter{OrgID: orgID, Actor: deskActor}

	create := func(name, stopID string) string {
		t.Helper()
		rs, err := store.Create(ctx, desk, fenceKey(name), map[string]any{
			"kind": "photo", "appwrite_file_id": "fx-" + name, "bucket": "fx-bucket",
			"mime": "image/png", "size_bytes": float64(9),
			"links": []any{map[string]any{"owner_kind": "stop", "owner_id": stopID, "role": "proof"}},
		})
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		return rs.ID
	}
	return &fenceWorld{
		store: store, orgID: orgID,
		own:    create("own", ownStop),
		away:   create("away", awayStop),
		driver: hhsync.ScopeFilter{OrgID: orgID, Actor: driverActor},
		desk:   desk,
	}
}

// fenceKey mints one fixed-shape idempotency key per name.
func fenceKey(name string) string {
	switch name {
	case "own":
		return "a1e10000-0000-4000-8000-000000000001"
	case "away":
		return "a1e10000-0000-4000-8000-000000000002"
	default:
		return "a1e10000-0000-4000-8000-0000000000ff"
	}
}

// pullIDs drives one pull and returns the entity ids.
func pullIDs(t *testing.T, w *fenceWorld, f hhsync.ScopeFilter) []string {
	t.Helper()
	items, _, err := w.store.Pull(context.Background(), f, 0, 50)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	ids := make([]string, 0, len(items))
	for _, e := range items {
		ids = append(ids, e.ID)
	}
	return ids
}

func has(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// TestIntegration_AttachmentsDriverFence_PullAndRowsHoldToTheCallingDriver:
// the driver's pull answers their own stops' attachments alone, the
// colleague's row is the same as absent on Load, FindApplied, Write and
// Delete, and a driver's create against a colleague's stop refuses,
// while the desk caller keeps the org list on every path.
func TestIntegration_AttachmentsDriverFence_PullAndRowsHoldToTheCallingDriver(t *testing.T) {
	w := newFenceWorld(t)
	ctx := context.Background()

	if seen := pullIDs(t, w, w.driver); has(seen, w.away) || !has(seen, w.own) {
		t.Errorf("the driver's pull must answer their own attachment alone, got %v (own %s, away %s)", seen, w.own, w.away)
	}
	if seen := pullIDs(t, w, w.desk); !has(seen, w.own) || !has(seen, w.away) {
		t.Errorf("the desk caller's pull must keep both attachments, got %v", seen)
	}

	if _, found, err := w.store.Load(ctx, w.driver, w.away); err != nil {
		t.Fatalf("driver Load of the colleague's row: %v", err)
	} else if found {
		t.Errorf("the driver's Load found the colleague's attachment %s", w.away)
	}
	if _, found, err := w.store.FindApplied(ctx, w.driver, fenceKey("away")); err != nil {
		t.Fatalf("driver FindApplied of the colleague's key: %v", err)
	} else if found {
		t.Errorf("the driver's FindApplied resolved the colleague's key")
	}
	if _, err := w.store.Write(ctx, w.driver, w.away, fenceKey("w1"), map[string]any{"id": w.away, "mime": "image/jpeg"}); err == nil {
		t.Errorf("the driver's Write on the colleague's row applied; it must be the same as absent")
	}
	if _, err := w.store.Delete(ctx, w.driver, w.away, fenceKey("d1")); err == nil {
		t.Errorf("the driver's Delete on the colleague's row applied; it must be the same as absent")
	}

	if _, err := w.store.Create(ctx, w.driver, fenceKey("c1"), map[string]any{
		"kind": "photo", "appwrite_file_id": "fx-driver-create", "bucket": "fx-bucket",
		"links": []any{map[string]any{"owner_kind": "stop", "owner_id": "a0e10000-0000-4000-8000-0000000000f3", "role": "proof"}},
	}); err == nil {
		t.Errorf("the driver's create against a colleague's stop registered; it must refuse")
	}

	if _, found, err := w.store.Load(ctx, w.desk, w.away); err != nil || !found {
		t.Errorf("the desk caller's Load must keep finding the row: found=%v err=%v", found, err)
	}
}

// TestIntegration_AttachmentsDriverFence_ATombstoneNamesItsOwnerAlone:
// a tombstone is still a row naming the attachment, so it holds to the
// same fence: the driver sees their own row's tombstone and never the
// colleague's.
func TestIntegration_AttachmentsDriverFence_ATombstoneNamesItsOwnerAlone(t *testing.T) {
	w := newFenceWorld(t)
	ctx := context.Background()

	for _, id := range []string{w.own, w.away} {
		if _, err := w.store.Delete(ctx, w.desk, id, fenceKey("t"+id)); err != nil {
			t.Fatalf("desk delete %s: %v", id, err)
		}
	}
	_, driverTombs, err := w.store.Pull(ctx, w.driver, 0, 50)
	if err != nil {
		t.Fatalf("driver pull: %v", err)
	}
	for _, tb := range driverTombs {
		if tb.ID == w.away {
			t.Errorf("the driver's pull answered the colleague's tombstone %s", w.away)
		}
	}
	if len(driverTombs) == 0 {
		t.Errorf("the driver's pull lost their own tombstone")
	}
	_, deskTombs, err := w.store.Pull(ctx, w.desk, 0, 50)
	if err != nil {
		t.Fatalf("desk pull: %v", err)
	}
	if len(deskTombs) != 2 {
		t.Errorf("the desk caller's pull must keep both tombstones, got %d", len(deskTombs))
	}
}
