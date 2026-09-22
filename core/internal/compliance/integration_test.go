//go:build integration

// Integration tests run the real pgx Repository against a PRIVATE Postgres. Run with:
//
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5435/hos_w6_lien?sslmode=disable' \
//	  go test -tags=integration ./internal/compliance/... -count=1 -p 1
package compliance

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func intPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hos_w6_lien?sslmode=disable"
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
	return pool
}

func realSvc(pool *pgxpool.Pool) *Service {
	return NewService(NewRepository(pool)).
		withClock(func() time.Time { return time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC) })
}

// I1: RegisterDeadline is idempotent on (job, kind, due) through the real NOT-EXISTS insert. NULL job_id
// (engagement-wide) avoids the lien_jobs FK.
func TestIntegration_RegisterDeadlineIdempotent(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := realSvc(pool)
	due := "2026-08-21"
	// DIB-396 order-independence: this fixture used to leave its compliance_calendar row behind on
	// every run. The row is harmless to THIS test (the insert is idempotent on job/kind/due, so a
	// rerun still finds exactly one), but it is a permanent, ever-growing addition to the shared
	// calendar that any later whole-table assertion inherits. Fixtures restore what they touch.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM compliance_calendar
			  WHERE job_id IS NULL AND deadline_kind = 'NOTICE_DEADLINE' AND due_date = $1::date`, due)
	})
	if err := svc.RegisterDeadline(ctx, "", "NOTICE_DEADLINE", due, 45); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := svc.RegisterDeadline(ctx, "", "NOTICE_DEADLINE", due, 45); err != nil {
		t.Fatalf("re-register: %v", err)
	}
	var n int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM compliance_calendar WHERE job_id IS NULL AND deadline_kind='NOTICE_DEADLINE' AND due_date=$1::date`, due).Scan(&n)
	if n != 1 {
		t.Fatalf("deadline rows=%d, want 1 (idempotent)", n)
	}
	// Upcoming within a wide horizon surfaces it and marks it OVERDUE if past asOf.
	items, err := svc.Upcoming(ctx, 90)
	if err != nil {
		t.Fatalf("upcoming: %v", err)
	}
	found := false
	for _, d := range items {
		if d.DueDate == due {
			found = true
		}
	}
	if !found {
		t.Fatalf("upcoming did not surface the registered deadline")
	}
}

// I2: the shared review-status banner round-trips (default DRAFT → APPROVED) through the real tables.
func TestIntegration_ReviewStatusRoundTrip(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	svc := realSvc(pool)
	// A fresh subject id per run keeps the round-trip isolated.
	var subject string
	if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&subject); err != nil {
		t.Fatalf("gen uuid: %v", err)
	}
	// DIB-396: a fresh id per run made this test order-independent but NOT self-cleaning, every run
	// left a review_status row behind forever. Isolation and cleanup are different properties.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM review_status WHERE subject_id = $1::uuid`, subject)
	})

	// No row → data-driven DRAFT default.
	rs, err := svc.ReviewStatus(ctx, "LIEN_JOB", subject)
	if err != nil {
		t.Fatalf("review status: %v", err)
	}
	if rs.State != "DRAFT" || !rs.IsDraft {
		t.Fatalf("default %+v, want DRAFT/isDraft", rs)
	}

	// Upsert to APPROVED, then read back.
	if _, err := svc.UpsertReviewStatus(ctx, ReviewStatusInput{SubjectType: "LIEN_JOB", SubjectID: subject, State: "APPROVED", BannerText: "Issued"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := svc.ReviewStatus(ctx, "LIEN_JOB", subject)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.State != "APPROVED" || got.IsDraft || got.BannerText != "Issued" {
		t.Fatalf("persisted %+v, want APPROVED/not-draft/Issued", got)
	}

	// A second upsert updates in place (no duplicate row).
	if _, err := svc.UpsertReviewStatus(ctx, ReviewStatusInput{SubjectType: "LIEN_JOB", SubjectID: subject, State: "REJECTED", BannerText: "Rejected"}); err != nil {
		t.Fatalf("upsert2: %v", err)
	}
	var n int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM review_status WHERE subject_id=$1::uuid`, subject).Scan(&n)
	if n != 1 {
		t.Fatalf("review_status rows=%d, want 1 (update-in-place)", n)
	}
}
