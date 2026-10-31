//go:build integration

// Integration tests for the DIB-411 print_queue substrate against the real Postgres: 0088 applies
// clean with the enum the future needs, and the enqueue → PRINTED lifecycle holds under the real
// guarded UPDATE (including the refusal to restamp a job that is not QUEUED). The shared fixtures
// here are also used by receipt_integration_test.go (DIB-410).
//
// Run with: DATABASE_URL=... go test -tags integration ./internal/printing/
package printing

import (
	"context"
	"crypto/rand"
	"fmt"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit/legsweep"
	"os"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
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
	t.Cleanup(pool.Close)
	// Registered after pool.Close, so it runs before the pool closes; a leg's own cleanups
	// (added later) run first and the sweep deletes what they left (plan row 0.43).
	legsweep.Register(t, pool)
	return pool
}

func seededUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `SELECT id::text FROM users LIMIT 1`).Scan(&id); err != nil {
		t.Skipf("no seeded user: %v", err)
	}
	return id
}

// staffCtx stamps a guard-shaped caller exactly as identity's RequireStaff does in production.
func staffCtx(t *testing.T, pool *pgxpool.Pool, scope identity.Scope, branches ...string) context.Context {
	t.Helper()
	granted := map[string]bool{}
	for _, b := range branches {
		granted[b] = true
	}
	return identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: seededUser(t, pool), RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership,
		Scope: scope, Status: identity.StatusActive, Branches: granted,
	})
}

func globalCtx(t *testing.T, pool *pgxpool.Pool) context.Context {
	t.Helper()
	return staffCtx(t, pool, identity.ScopeGlobal)
}

// aPaver returns an SF-based product + the branch to ring it at (the pos integration fixture: base
// UOM SF keeps the ring qty and the anchor in the same unit, so the margin floor is not tripped).
func aPaver(t *testing.T, pool *pgxpool.Pool) (productID, branchID string) {
	t.Helper()
	ctx := context.Background()
	err := pool.QueryRow(ctx, `
		SELECT p.id::text FROM products p
		  JOIN pricing_list_prices lp ON lp.product_id = p.id AND lp.price_uom = 'SF'
		 WHERE p.is_active AND p.base_uom = 'SF' LIMIT 1`).Scan(&productID)
	if err != nil {
		t.Skipf("no seeded SF-priceable product: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM locations WHERE loc_type='BRANCH' ORDER BY code LIMIT 1`).Scan(&branchID); err != nil {
		t.Skipf("no seeded branch: %v", err)
	}
	return productID, branchID
}

// - DIB-411: the print_queue substrate -

// 0088 applies clean and the table exists with the enum the future needs.
func TestIntegration_PrintQueue_MigrationApplies(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables WHERE table_name='print_queue'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("print_queue missing (0088 not applied): n=%d err=%v", n, err)
	}
	// The doc_type enum is designed for the destination NOW, labels and delivery docs were the two
	// found gaps, and a later ALTER on a live table is exactly what this avoids.
	for _, dt := range []string{DocReceipt, DocLabelZPL, DocDeliveryDoc} {
		var def string
		if err := pool.QueryRow(ctx, `
			SELECT pg_get_constraintdef(oid) FROM pg_constraint
			 WHERE conrelid = 'print_queue'::regclass AND conname = 'print_queue_doc_type_check'`).Scan(&def); err != nil {
			t.Fatalf("doc_type check: %v", err)
		}
		if !strings.Contains(def, dt) {
			t.Fatalf("doc_type CHECK must permit %s: %s", dt, def)
		}
	}
}

// DIB-411, the whole lifecycle against the real guarded UPDATE: enqueue lands QUEUED, MarkPrinted
// moves it to PRINTED with a printed_at, and a SECOND mark is refused. The refusal is what keeps the
// receipt's reprint count honest: one PRINTED row must mean exactly one print.
func TestIntegration_PrintQueue_EnqueuePrintedLifecycle(t *testing.T) {
	pool := intPool(t)
	ctx := globalCtx(t, pool)
	_, branchID := aPaver(t, pool)
	q := NewQueueService(NewRepository(pool))
	ref := newUUID()

	t.Run("enqueue lands QUEUED", func(t *testing.T) {
		job, err := q.Enqueue(ctx, EnqueueInput{
			DocType: DocLabelZPL, RefID: ref, BranchID: branchID, RenderTarget: TargetZPL,
		})
		if err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		if job.Status != StatusQueued || job.PrintedAt != nil {
			t.Fatalf("a fresh job is QUEUED and unprinted: %+v", job)
		}
		if job.RequestedBy == "" {
			t.Fatal("the job must record WHO asked for the paper")
		}
	})

	var jobID string
	t.Run("ListForRef finds it", func(t *testing.T) {
		jobs, err := q.ListForRef(ctx, DocLabelZPL, ref)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(jobs) != 1 {
			t.Fatalf("want 1 job for the ref, got %d", len(jobs))
		}
		jobID = jobs[0].ID
	})

	t.Run("MarkPrinted completes it", func(t *testing.T) {
		if err := q.MarkPrinted(ctx, jobID); err != nil {
			t.Fatalf("mark printed: %v", err)
		}
		jobs, err := q.ListForRef(ctx, DocLabelZPL, ref)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if jobs[0].Status != StatusPrinted {
			t.Fatalf("status = %q, want PRINTED", jobs[0].Status)
		}
		if jobs[0].PrintedAt == nil || jobs[0].PrintedAt.IsZero() {
			t.Fatal("a PRINTED job must carry printed_at (0088 CHECK ties the two together)")
		}
	})

	t.Run("a second mark is refused", func(t *testing.T) {
		if err := q.MarkPrinted(ctx, jobID); err == nil {
			t.Fatal("an already-PRINTED job must NOT be restamped, re-enqueue instead")
		}
	})

	t.Run("an unknown job is not found", func(t *testing.T) {
		if err := q.MarkPrinted(ctx, newUUID()); err == nil {
			t.Fatal("an unknown job id must be refused")
		}
	})
}

// newUUID mints a fresh v4 uuid for a ref that must not collide across runs.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
