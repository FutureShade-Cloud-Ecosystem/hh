//go:build integration

package houseday

// The database half of the evening guard's proof: the clock the SQL reads (now(), CURRENT_DATE) must
// sit in the same window as the clock Go reads, or a fixture seeded from one and read by the other
// would fail for the wrong reason.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestEveningGuard_TheDatabaseClockAgreesWithTheGoClock(t *testing.T) {
	if mode := os.Getenv("HH_EVENING_GUARD"); mode != "1" && mode != "local" {
		t.Skip("not the evening guard run")
	}
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Fatal("DATABASE_URL is unset in the guard run")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)
	var dbNow time.Time
	var dbToday string
	if err := conn.QueryRow(ctx, `SELECT now(), CURRENT_DATE::text`).Scan(&dbNow, &dbToday); err != nil {
		t.Fatalf("read the database clock: %v", err)
	}
	if d := time.Since(dbNow); d > time.Minute || d < -time.Minute {
		t.Fatalf("the database clock reads %s, the Go clock %s: the guard moved one and not the other",
			dbNow.UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339))
	}
	if dbToday != time.Now().UTC().Format("2006-01-02") {
		t.Fatalf("CURRENT_DATE = %s, want the UTC day of the pinned clock", dbToday)
	}
}
