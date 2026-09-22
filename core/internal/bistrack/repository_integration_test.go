//go:build integration

// DIB-53 integration tests: the 0079 persistence against live Postgres. They prove the migration applies
// and that the two properties the tables exist for hold, a sync_jobs row round-trips with its RESUMABLE
// composite cursor intact, and the crosswalk is idempotent on (entity, bistrack_id) but REFUSES a
// many-to-one on native_id with ErrCrosswalkConflict (supervisor C3). No live BisTrack is touched.
package bistrack

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func intPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	fail := func(format string, a ...any) {
		if set {
			t.Fatalf(format, a...)
		}
		t.Skipf(format, a...)
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		fail("integration DB unavailable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		fail("integration DB not reachable: %v", err)
	}
	return pool
}

// uniqueSuffix keeps parallel/re-runs from colliding on the shared DB without needing Date.now in app code.
func cleanupBisTrack(t *testing.T, pool *pgxpool.Pool, tag string) {
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM sync_discrepancies WHERE bistrack_id LIKE $1`, tag+"%")
		_, _ = pool.Exec(c, `DELETE FROM bistrack_id_map WHERE bistrack_id LIKE $1`, tag+"%")
		_, _ = pool.Exec(c, `DELETE FROM sync_jobs WHERE error = $1`, tag)
	})
}

func TestIntegration_SyncJobRoundTrip_CompositeCursor(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	cleanupBisTrack(t, pool, "DIB53-JOB")
	ctx := context.Background()

	ts := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	id, err := repo.InsertJob(ctx, SyncJob{
		EntityType: EntityCustomer, Direction: DirectionPull, Status: StatusPending,
		Cursor: Cursor{UpdatedAt: ts, LastID: "C42"}, Error: "DIB53-JOB",
	})
	if err != nil {
		t.Fatalf("insert job: %v", err)
	}
	if id == "" {
		t.Fatal("want a job id")
	}
	last, err := repo.LatestJob(ctx)
	if err != nil {
		t.Fatalf("latest job: %v", err)
	}
	if last == nil || last.EntityType != EntityCustomer || last.Direction != DirectionPull {
		t.Fatalf("latest job = %+v, want the customer/pull job", last)
	}
	// The composite cursor must survive the round trip intact, both halves.
	if !last.Cursor.UpdatedAt.Equal(ts) || last.Cursor.LastID != "C42" {
		t.Errorf("cursor = %+v, want (%v, C42)", last.Cursor, ts)
	}
}

func TestIntegration_DiscrepancyInsertAndOpenCount(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	cleanupBisTrack(t, pool, "DIB53-DISC")
	ctx := context.Background()

	before, err := repo.OpenDiscrepancyCount(ctx)
	if err != nil {
		t.Fatalf("open count: %v", err)
	}
	for _, k := range []DiscrepancyKind{KindUnmapped, KindMappingError} {
		if _, err := repo.InsertDiscrepancy(ctx, SyncDiscrepancy{
			EntityType: EntityCustomer, BisTrackID: "DIB53-DISC-" + string(k), Kind: k,
			Detail: map[string]any{"reason": "test"},
		}); err != nil {
			t.Fatalf("insert discrepancy %s: %v", k, err)
		}
	}
	after, err := repo.OpenDiscrepancyCount(ctx)
	if err != nil {
		t.Fatalf("open count: %v", err)
	}
	if after-before != 2 {
		t.Errorf("open discrepancy delta = %d, want 2", after-before)
	}
}

func TestIntegration_IDMap_IdempotentAndConflict(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	cleanupBisTrack(t, pool, "DIB53-XW")
	ctx := context.Background()

	nativeA := "aaaaaaaa-0000-4000-9000-0000000000aa"
	// Idempotent on (entity, bistrack_id): the same pair upserts twice with no error.
	for i := 0; i < 2; i++ {
		if err := repo.UpsertIDMapping(ctx, IDMapping{EntityType: EntityCustomer, BisTrackID: "DIB53-XW-1", NativeID: nativeA}); err != nil {
			t.Fatalf("upsert %d: %v", i, err)
		}
	}
	if got, ok := repo.Resolve(EntityCustomer, "DIB53-XW-1"); !ok || got != nativeA {
		t.Errorf("resolve = (%s,%v), want (%s,true)", got, ok, nativeA)
	}
	// C3: a DIFFERENT bistrack id mapping to the SAME native id (a many-to-one) must CONFLICT, not
	// silently duplicate, the caller records that as a MAPPING_ERROR.
	err := repo.UpsertIDMapping(ctx, IDMapping{EntityType: EntityCustomer, BisTrackID: "DIB53-XW-2", NativeID: nativeA})
	if !errors.Is(err, ErrCrosswalkConflict) {
		t.Fatalf("want ErrCrosswalkConflict for a many-to-one, got %v", err)
	}
}
