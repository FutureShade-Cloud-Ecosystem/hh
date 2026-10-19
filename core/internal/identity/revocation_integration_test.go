//go:build integration

package identity

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The restart proof (HH-678): a revocation written through one store is
// read back by a brand new store and a brand new verifier over the same
// database, with empty in memory caches, exactly what a fresh core process
// after a restart holds. (The sign out route writes through this same
// Revoke call; its own gates are the signout package's unit tests.)

func revocationTestPool(t *testing.T) *pgxpool.Pool {
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

func TestRevocationSurvivesACoreRestart(t *testing.T) {
	pool := revocationTestPool(t)
	ctx := context.Background()

	m := newTestMinter(t, freshPriv(t))
	set, err := NewSessionKeySet(m.PublicKey(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()

	// The first core process: verifier and store.
	revocations1 := NewSessionRevocations(pool).WithClock(func() time.Time { return now })
	verifier1, err := NewVerifier(Config{
		KeySource: set, Issuer: signerIssuer,
		StaffAudience: signerAud, TenantAudience: signerTenant,
		Now: func() time.Time { return now }, Revocations: revocations1,
	})
	if err != nil {
		t.Fatal(err)
	}
	token, err := m.MintAt("console-user-restart", 2*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := verifier1.Verify(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM revoked_sessions WHERE jti = $1`, tok.JTI)
	})
	// The sign out route's write, made here directly: Revoke with the
	// verified jti and exp.
	if err := revocations1.Revoke(ctx, tok.JTI, tok.ExpiresAt); err != nil {
		t.Fatal(err)
	}

	// The brand new process: a fresh store (empty caches) and a fresh
	// verifier, the same database. The revocation must still hold.
	revocations2 := NewSessionRevocations(pool).WithClock(func() time.Time { return now })
	verifier2, err := NewVerifier(Config{
		KeySource: set, Issuer: signerIssuer,
		StaffAudience: signerAud, TenantAudience: signerTenant,
		Now: func() time.Time { return now }, Revocations: revocations2,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = verifier2.Verify(ctx, token)
	if err == nil {
		t.Fatal("the revoked token verified after a simulated restart")
	}
	ve, ok := err.(*VerifyError)
	if !ok || ve.Class() != ClassCredential || ve.Reason() != "token revoked" {
		t.Fatalf("err = %v, want credential token revoked", err)
	}
}

func TestRevocationSweepRemovesExpiredRows(t *testing.T) {
	pool := revocationTestPool(t)
	ctx := context.Background()
	now := time.Now()
	s := NewSessionRevocations(pool).WithClock(func() time.Time { return now })
	// A revocation whose exp has passed writes nothing and is swept: seed
	// one expired row directly, then let a live revocation's sweep remove it.
	_, err := pool.Exec(ctx,
		`INSERT INTO revoked_sessions (jti, exp, revoked_at) VALUES ($1, $2, $3) ON CONFLICT (jti) DO NOTHING`,
		"sweep-expired-integration", now.Add(-time.Minute), now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM revoked_sessions WHERE jti IN ($1, $2)`, "sweep-live-integration", "sweep-expired-integration")
	})
	if err := s.Revoke(ctx, "sweep-live-integration", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM revoked_sessions WHERE jti = $1`, "sweep-expired-integration").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("the expired row survived the sweep (%d rows)", n)
	}
}
