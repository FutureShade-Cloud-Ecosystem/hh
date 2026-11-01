package legsweep

// GuardMain, plan row 0.52: the package level tripwire a TestMain calls. It fingerprints every
// table of the DATABASE_URL database before the package's tests run and again after, and fails
// the run, naming each table and its row counts, when any table differs. A package whose every
// pool helper registers the sweep leaves a zero diff; a package with one unregistered pool does
// not, and CI's one shared database carries the debris into the next package.
//
// It is a package guard, not a sweep: it deletes nothing. It does nothing when DATABASE_URL is
// unset or the database is unreachable (the legs skip politely in that case).

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// GuardMain runs the package's tests and returns the exit code for os.Exit.
func GuardMain(m *testing.M) int {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return m.Run()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return m.Run()
	}
	defer pool.Close()
	if pool.Ping(ctx) != nil {
		return m.Run()
	}
	db := &sweepDB{}
	plan, err := sweepCatalog(ctx, pool, db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "package guard: read the catalog: %v\n", err)
		return 1
	}
	before, err := sweepFingerprint(ctx, pool, plan, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "package guard: fingerprint: %v\n", err)
		return 1
	}
	code := m.Run()
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel2()
	after, err := sweepFingerprint(ctx2, pool, plan, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "package guard: fingerprint at the end: %v\n", err)
		return 1
	}
	if diff := sweepFPDiff(before, after); len(diff) > 0 {
		var b strings.Builder
		for _, n := range diff {
			fmt.Fprintf(&b, "  %s: %d rows before, %d after\n", n, before[n].n, after[n].n)
		}
		fmt.Fprintf(os.Stderr, "package guard: the package's tests left %d table(s) different from how they found them:\n%s", len(diff), b.String())
		if code == 0 {
			code = 1
		}
	}
	return code
}
