//go:build integration

// default_failloud_integration_test.go, regression guard for the MINOR recon finding:
// DefaultBranchID must ONLY swallow pgx.ErrNoRows as "no default configured". Any other
// query/scan error (connection loss, decode failure, …) must propagate, fail loud, instead
// of silently returning "" (which would flag every branch is_default=false and hide a real DB
// fault). Before the fix, the bare `if err != nil { return "", nil }` swallowed all errors, so
// this test (which forces a real query error via a closed pool) failed: it got ("", nil).
//
//	Run: DATABASE_URL=postgres://hardscape:hardscape@localhost:5435/hardscapeos go test \
//	  -tags integration ./internal/branches/... -run TestIntegration_DefaultBranchID_FailsLoud
package branches

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestIntegration_DefaultBranchID_FailsLoud(t *testing.T) {
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		if set {
			t.Fatalf("integration DB unavailable: %v", err)
		}
		t.Skipf("integration DB unavailable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		if set {
			t.Fatalf("integration DB not reachable: %v", err)
		}
		t.Skipf("integration DB not reachable: %v", err)
	}

	// Close the pool, then query: the resulting error is NOT pgx.ErrNoRows, so it MUST surface.
	pool.Close()
	repo := NewRepository(pool)

	id, err := repo.DefaultBranchID(context.Background())
	if err == nil {
		t.Fatalf("DefaultBranchID swallowed a real query error and returned id=%q, must fail loud", id)
	}
	if id != "" {
		t.Fatalf("on error, id should be empty, got %q", id)
	}
}
