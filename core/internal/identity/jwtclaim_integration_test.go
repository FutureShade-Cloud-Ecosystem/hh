//go:build integration

package identity

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func claimTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, integration tests skipped")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func newClaims(t *testing.T, pool *pgxpool.Pool, now func() time.Time) *JWTClaims {
	t.Helper()
	c, err := NewJWTClaims(pool, MinJWTClaimBound)
	if err != nil {
		t.Fatal(err)
	}
	if now != nil {
		c.now = now
	}
	return c
}

func TestClaimJWTIsSingleUse(t *testing.T) {
	pool := claimTestPool(t)
	c := newClaims(t, pool, nil)
	h := HashJWTSignature([]byte("single-use-signature"))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM console_jwt_claim WHERE sig_hash = $1`, h[:])
	})
	exp := time.Now().Add(10 * time.Minute)
	if ok, err := c.ClaimJWT(context.Background(), h, exp); err != nil || !ok {
		t.Fatalf("first claim: %v %v", ok, err)
	}
	if ok, err := c.ClaimJWT(context.Background(), h, exp); err != nil || ok {
		t.Fatalf("second claim: %v %v", ok, err)
	}
}

func TestClaimJWTConcurrentPresentationsOneWins(t *testing.T) {
	pool := claimTestPool(t)
	c := newClaims(t, pool, nil)
	h := HashJWTSignature([]byte("concurrent-signature"))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM console_jwt_claim WHERE sig_hash = $1`, h[:])
	})
	exp := time.Now().Add(10 * time.Minute)
	var wins int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := c.ClaimJWT(context.Background(), h, exp)
			if err != nil {
				t.Error(err)
			}
			if ok {
				atomic.AddInt32(&wins, 1)
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d claims won, want exactly one", wins)
	}
}

func TestClaimJWTRefusesAnExpOutsideTheBound(t *testing.T) {
	pool := claimTestPool(t)
	c := newClaims(t, pool, nil)
	h := HashJWTSignature([]byte("bound-signature"))
	for name, exp := range map[string]time.Time{
		"zero":            {},
		"beyond bound":    time.Now().Add(MinJWTClaimBound + time.Minute),
		"one second past": time.Now().Add(-time.Second),
		"exactly now":     time.Now(),
	} {
		if ok, err := c.ClaimJWT(context.Background(), h, exp); err == nil || ok {
			t.Errorf("%s: claimed=%v err=%v", name, ok, err)
		}
	}
	var n int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM console_jwt_claim WHERE sig_hash = $1`, h[:]).Scan(&n)
	if n != 0 {
		t.Fatal("a refused exp left a row")
	}
}

func TestClaimJWTPurgesExpiredRowsABoundedNumberPerClaim(t *testing.T) {
	pool := claimTestPool(t)
	ctx := context.Background()
	base := time.Now()
	// Plant more expired rows than one claim may purge.
	total := JWTClaimPurgeBatch + 10
	if _, err := pool.Exec(ctx, `
		INSERT INTO console_jwt_claim (sig_hash, exp)
		SELECT sha256(('purge-fixture-' || g)::bytea), now() - interval '1 hour'
		FROM generate_series(1, $1) g`, total); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM console_jwt_claim WHERE exp < now()`)
	})
	var before int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM console_jwt_claim WHERE exp < now()`).Scan(&before)
	c := newClaims(t, pool, func() time.Time { return base })
	h := HashJWTSignature([]byte("purge-driver"))
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM console_jwt_claim WHERE sig_hash = $1`, h[:]) })
	if ok, err := c.ClaimJWT(ctx, h, base.Add(5*time.Minute)); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	var after int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM console_jwt_claim WHERE exp < now()`).Scan(&after)
	if purged := before - after; purged != JWTClaimPurgeBatch {
		t.Fatalf("purged %d, want exactly the batch of %d", purged, JWTClaimPurgeBatch)
	}
}

func TestNewJWTClaimsRefusesABoundBelowTheConsoleJWTLife(t *testing.T) {
	if _, err := NewJWTClaims(nil, MinJWTClaimBound); err == nil {
		t.Fatal("nil pool accepted")
	}
}

// A past exp is refused before anything is written, so the purge can never
// forget a row the same call just inserted: claiming a hash with an exp one
// second in the past twice refuses both times and leaves no row.
func TestClaimJWTWithAPastExpRefusesTwiceAndKeepsNothing(t *testing.T) {
	pool := claimTestPool(t)
	c := newClaims(t, pool, nil)
	h := HashJWTSignature([]byte("past-exp-signature"))
	for i := 0; i < 2; i++ {
		ok, err := c.ClaimJWT(context.Background(), h, time.Now().Add(-time.Second))
		if !errors.Is(err, ErrJWTExpOutOfBound) || ok {
			t.Fatalf("attempt %d: claimed=%v err=%v, want ErrJWTExpOutOfBound", i, ok, err)
		}
	}
	var n int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM console_jwt_claim WHERE sig_hash = $1`, h[:]).Scan(&n)
	if n != 0 {
		t.Fatalf("%d row(s) left", n)
	}
}

// The purge keeps a row whose exp has only just passed (inside the bound
// margin) and removes one older than the margin.
func TestClaimJWTPurgeKeepsRowsInsideTheMargin(t *testing.T) {
	pool := claimTestPool(t)
	ctx := context.Background()
	recent := HashJWTSignature([]byte("margin-recent"))
	old := HashJWTSignature([]byte("margin-old"))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM console_jwt_claim WHERE sig_hash = ANY($1)`, [][]byte{recent[:], old[:]})
	})
	now := time.Now()
	if _, err := pool.Exec(ctx, `INSERT INTO console_jwt_claim (sig_hash, exp) VALUES ($1, $2), ($3, $4)`,
		recent[:], now.Add(-time.Minute), old[:], now.Add(-MinJWTClaimBound-time.Minute)); err != nil {
		t.Fatal(err)
	}
	c := newClaims(t, pool, func() time.Time { return now })
	h := HashJWTSignature([]byte("margin-driver"))
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM console_jwt_claim WHERE sig_hash = $1`, h[:]) })
	if ok, err := c.ClaimJWT(ctx, h, now.Add(5*time.Minute)); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	var keptRecent, keptOld int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM console_jwt_claim WHERE sig_hash = $1`, recent[:]).Scan(&keptRecent)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM console_jwt_claim WHERE sig_hash = $1`, old[:]).Scan(&keptOld)
	if keptRecent != 1 {
		t.Fatal("a row that expired inside the margin was purged")
	}
	if keptOld != 0 {
		t.Fatal("a row older than the margin survived the purge")
	}
}
