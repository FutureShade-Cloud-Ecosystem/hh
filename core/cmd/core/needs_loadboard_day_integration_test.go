//go:build integration

// needs_loadboard_day_integration_test.go, HH-201: the load board's needs rows ask for the DEALER'S
// day, not the process's.
//
// The adapter hands LoadBoard a date literal. If it formats the raw instant, the date is whatever
// zone the instant happens to carry (UTC on an unzoned droplet), so in the evening after the UTC
// rollover the "unassigned" and "due to commit" rows count tomorrow's board. The house helper
// dashboard.HouseDay exists for exactly this and the "today" tiles already use it.
//
// The dispatch service sits on a concrete pgx repository, so the seam here is the pool's own query
// tracer: the real service runs against a real (migrated) database and the tracer records the date
// argument of the board's runs read.
//
//	Run: DATABASE_URL='postgres://.../scratch?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -run LoadBoardNeeds -count=1
package main

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/dashboard"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/dispatch"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// runsDayTracer records the $2 date argument of every board runs read.
type runsDayTracer struct {
	mu   sync.Mutex
	days []string
}

func (r *runsDayTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if len(d.Args) == 2 && containsAll(d.SQL, "delivery_runs", "r.scheduled_date = $2::date") {
		if day, ok := d.Args[1].(string); ok {
			r.mu.Lock()
			r.days = append(r.days, day)
			r.mu.Unlock()
		}
	}
	return ctx
}

func (r *runsDayTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}

func TestIntegration_LoadBoardNeedsAskForTheDealersDay(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL is unset, the load board needs probe needs a migrated Postgres")
	}
	tracer := &runsDayTracer{}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.ConnConfig.Tracer = tracer
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)

	const branch = "00000000-0000-0000-0000-0000000000b1"
	ctx := identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: "00000000-0000-0000-0000-0000000000a1", RoleCode: "LOGISTICS_MANAGER",
		Tier: identity.TierManager, Scope: identity.ScopeGlobal, Status: identity.StatusActive,
	})
	svc := dispatch.NewService(dispatch.NewRepository(pool))

	// Each instant is in UTC (the zone of an unzoned droplet) and falls on a DIFFERENT calendar date
	// there than in the house zone. The house is behind UTC, so the process day is always the later
	// one: an evening instant after the UTC rollover but before house midnight.
	for _, tc := range []struct {
		name string
		now  time.Time
		want string
	}{
		{"summer evening after the UTC rollover", time.Date(2026, time.July, 15, 2, 0, 0, 0, time.UTC), "2026-07-14"},
		{"winter evening after the UTC rollover", time.Date(2026, time.January, 15, 4, 30, 0, 0, time.UTC), "2026-01-14"},
		{"the evening of the spring forward day", time.Date(2026, time.March, 9, 3, 30, 0, 0, time.UTC), "2026-03-08"},
		{"the evening of the fall back day", time.Date(2026, time.November, 2, 4, 30, 0, 0, time.UTC), "2026-11-01"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := dashboard.HouseDay(tc.now); got != tc.want {
				t.Fatalf("fixture is wrong: HouseDay(%s) = %s, want %s", tc.now, got, tc.want)
			}
			if tc.now.Format("2006-01-02") == tc.want {
				t.Fatalf("fixture does not separate the process day from the house day")
			}
			tracer.mu.Lock()
			tracer.days = nil
			tracer.mu.Unlock()

			sc := dashboard.Scope{BranchIDs: []string{branch}, Now: tc.now}
			for _, due := range []bool{false, true} {
				if _, err := w6bLoadBoard(pool, svc, due)(ctx, sc); err != nil {
					t.Fatalf("needs source (dueToCommit=%v): %v", due, err)
				}
			}
			tracer.mu.Lock()
			defer tracer.mu.Unlock()
			if len(tracer.days) != 2 {
				t.Fatalf("recorded %d board reads, want 2: %v", len(tracer.days), tracer.days)
			}
			for _, got := range tracer.days {
				if got != tc.want {
					t.Fatalf("board read for %s, want the dealer's day %s", got, tc.want)
				}
			}
		})
	}
}
