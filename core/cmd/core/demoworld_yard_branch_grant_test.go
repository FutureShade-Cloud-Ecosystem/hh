//go:build demo

package main

// demoworld_yard_branch_grant_test.go: proof that after seeding the demo world,
// yard.demo holds only its home branch grant in user_branch_grants while the
// dispatch and driver personas remain unchanged at seed time, and that after
// population yard.demo is seated only in its home branch's rooms.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/messaging"
)

func TestDemoWorld_YardPersonaHoldsHomeBranchGrantOnly(t *testing.T) {
	dsn := os.Getenv(tillDemoDBEnvVar)
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip(tillDemoDBEnvVar + " and DATABASE_URL are not set: the demo world proof needs a local throwaway Postgres")
	}
	requireLocalDSN(t, dsn)
	ctx := context.Background()

	rawKey := make([]byte, 32)
	if _, err := rand.Read(rawKey); err != nil {
		t.Fatalf("generate vault key: %v", err)
	}
	pool, testDSN := qwFreshDB(t, ctx, dsn, "demoworld_yard_grant")
	t.Setenv("DATABASE_URL", testDSN)
	t.Setenv("CORE_DATABASE_URL", testDSN)
	t.Setenv("PAYMENT_VAULT_KEY", hex.EncodeToString(rawKey))
	t.Setenv("APP_ENV", "dev")

	branches, err := resolveBranches(ctx, pool)
	if err != nil {
		t.Fatalf("resolveBranches: %v", err)
	}
	if len(branches) < 2 {
		t.Fatalf("expected at least 2 seeded branches, got %d", len(branches))
	}
	homeBranchID := branches[len(branches)-1].ID

	if _, created := qwApply(t); created == 0 {
		t.Fatal("the first apply created nothing on a fresh database")
	}

	var yardTotal, yardHome int
	if err := pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE ubg.branch_id = $2::uuid)
		  FROM user_branch_grants ubg
		  JOIN users u ON u.id = ubg.user_id
		 WHERE u.email = $1::citext`,
		personaYard, homeBranchID).Scan(&yardTotal, &yardHome); err != nil {
		t.Fatalf("reading yard.demo branch grants: %v", err)
	}
	if yardTotal != 1 || yardHome != 1 {
		t.Errorf("after seeding, %s holds %d branch grant row(s) (%d at home branch %s), want 1 at home branch only",
			personaYard, yardTotal, yardHome, homeBranchID)
	}

	for _, email := range []string{personaDispatch, personaDriver} {
		var total int
		if err := pool.QueryRow(ctx, `
			SELECT count(*)
			  FROM user_branch_grants ubg
			  JOIN users u ON u.id = ubg.user_id
			 WHERE u.email = $1::citext`,
			email).Scan(&total); err != nil {
			t.Fatalf("reading %s branch grants: %v", email, err)
		}
		if total != len(branches) {
			t.Errorf("after seeding, %s holds %d branch grant row(s), want %d (unchanged)", email, total, len(branches))
		}
	}

	now := qwClock
	if _, err := demo.RunPopulate(ctx, demo.Options{Getenv: os.Getenv, Now: &now}); err != nil {
		t.Fatalf("demo populate: %v", err)
	}
	qwApply(t)

	var yardID string
	yardAliases := []string{personaYard, personaEmailAliases()[personaYard]}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users WHERE email = ANY($1::citext[])`, yardAliases).Scan(&yardID); err != nil {
		t.Fatalf("resolving yard.demo user id after populate: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE branch_id = $2::uuid)
		  FROM user_branch_grants
		 WHERE user_id = $1::uuid`,
		yardID, homeBranchID).Scan(&yardTotal, &yardHome); err != nil {
		t.Fatalf("reading yard.demo branch grants after populate: %v", err)
	}
	if yardTotal != 1 || yardHome != 1 {
		t.Errorf("after populate and second seed, yard.demo holds %d branch grant row(s) (%d at home branch %s), want 1 at home branch only",
			yardTotal, yardHome, homeBranchID)
	}

	channels, err := messaging.NewRepository(pool).ListChannelsForUser(ctx, yardID)
	if err != nil {
		t.Fatalf("ListChannelsForUser(yard.demo): %v", err)
	}
	for _, ch := range channels {
		if ch.BranchID != "" && ch.BranchID != homeBranchID {
			t.Errorf("yard.demo channel list still includes non-home branch room %s (branch %s, home %s)",
				ch.Slug, ch.BranchID, homeBranchID)
		}
	}
}
