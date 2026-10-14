//go:build demo

package main

// demoworld_customernames_test.go: proof that no two customers on a booted demo world carry one
// name. The demo world has two sources of customers, the seed plan's ten accounts (renamed by the
// world dictionary) and the populate's one contractor account, and the dictionary once gave the
// seed's first account the contractor account's name, so a tester searching the name met two rows.
// The world is built the way a demo box builds it and every customer name is counted.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo"
)

func TestDemoWorld_CustomerNamesAreUnique(t *testing.T) {
	dsn := os.Getenv(tillDemoDBEnvVar)
	if dsn == "" {
		t.Skip(tillDemoDBEnvVar + " is not set: the demo world proof needs its own throwaway local Postgres")
	}
	requireLocalDSN(t, dsn)
	ctx := context.Background()

	rawKey := make([]byte, 32)
	if _, err := rand.Read(rawKey); err != nil {
		t.Fatalf("generate vault key: %v", err)
	}
	pool, testDSN := qwFreshDB(t, ctx, dsn, "demoworld_custnames")
	t.Setenv("DATABASE_URL", testDSN)
	t.Setenv("CORE_DATABASE_URL", testDSN)
	t.Setenv("PAYMENT_VAULT_KEY", hex.EncodeToString(rawKey))
	t.Setenv("APP_ENV", "dev")
	if _, created := qwApply(t); created == 0 {
		t.Fatal("the first apply created nothing on a fresh database")
	}
	now := qwClock
	if _, err := demo.RunPopulate(ctx, demo.Options{Getenv: os.Getenv, Now: &now}); err != nil {
		t.Fatalf("demo populate: %v", err)
	}
	qwApply(t)

	if n := qwCount(t, ctx, pool, `SELECT count(*) FROM customers`); n < 11 {
		t.Fatalf("%d customers on the booted world, want the seed's ten and the contractor account", n)
	}
	rows, err := pool.Query(ctx, `SELECT name, count(*) FROM customers GROUP BY name HAVING count(*) > 1 ORDER BY name`)
	if err != nil {
		t.Fatalf("counting customer names: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var n int
		if err := rows.Scan(&name, &n); err != nil {
			t.Fatalf("scanning a name: %v", err)
		}
		t.Errorf("%d customers are named %q, want one", n, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("walking the names: %v", err)
	}
}
