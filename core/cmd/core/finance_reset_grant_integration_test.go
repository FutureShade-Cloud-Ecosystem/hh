//go:build integration

package main

// finance_reset_grant_integration_test.go is finding 2 (W2-RESET-FIX-r1): the
// public schema the reset recreates carries the initdb shape again, so a
// migration run as a non owner role that reads public (the 0419 shape)
// succeeds.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/db"
)

// TestIntegration_FinanceResetPublicGrant is finding 2: after the reset, a
// migration run as a non owner role that reads public (the 0419 shape)
// succeeds.
//
// The box's world is migrated up to 0418, and the reset puts that world
// back. The reset's own migration rerun then applies 0419, which creates
// its views under the per database view owner role: a role that is not the
// owner of public and that reads public's tables. Without the initdb grant
// on the recreated public schema that role has no USAGE on public, and the
// migration refuses with permission denied for schema public.
//
// The migrating role is the box's plain role, never a superuser.
func TestIntegration_FinanceResetPublicGrant(t *testing.T) {
	ctx := context.Background()
	dsn := financeResetFreshDB(t, "demoseed_it_finrstgrant", "0418")

	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting to the box: %v", err)
	}
	defer pool.Close()
	financeResetMarkBox(t, ctx, pool)

	// The world the reset puts back: the box's own world up to 0418, dumped
	// the way the world's own build dumps it, with the owner statements left
	// out and the privileges kept.
	dump := financeResetDumpWorld(t, swapDatabase(financeResetAdminDSN(t), "demoseed_it_finrstgrant"))
	digest := financeResetDigest(dump)
	worldFile := filepath.Join(t.TempDir(), "world.sql.gz")
	if err := writeWorldGzip(t, worldFile, dump); err != nil {
		t.Fatalf("the world file: %v", err)
	}

	env := finEnvFor(dsn, nil, map[string]string{
		envFinanceWorldFile: worldFile,
		envFinanceWorldSHA:  digest,
		// The dump file is written on disk under the directory the reset is
		// given; the test gives it one of its own.
		envFinanceResetDir: t.TempDir(),
	})
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	// The reset's own migration rerun reads the migration set relative to the
	// core directory, the way the boot's migrate does, so the reset runs
	// from there: the world's bookkeeping stops at 0418, and the rerun
	// applies 0419, the migration that reads public.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("the working directory: %v", err)
	}
	if err := os.Chdir(filepath.Join(wd, "..", "..")); err != nil {
		t.Fatalf("the core directory: %v", err)
	}
	resetErr := financeResetRole([]string{"--confirm", finOrgSlug}, out, errOut, env)
	if err := os.Chdir(wd); err != nil {
		t.Fatalf("the working directory: %v", err)
	}
	if resetErr != nil {
		t.Fatalf("the reset: %v", resetErr)
	}

	// The 0419 shape: the migration ran, and the view it creates under the
	// view owner role is there, owned by the box's own view owner role.
	restored, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("reconnecting to the restored world: %v", err)
	}
	defer restored.Close()
	var applied bool
	if err := restored.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)`,
		"0419_assist_orders_views_and_query_log").Scan(&applied); err != nil {
		t.Fatalf("the migration bookkeeping: %v", err)
	}
	if !applied {
		t.Fatalf("the reset did not apply the migration that reads public")
	}
	var owner string
	if err := restored.QueryRow(ctx, `SELECT pg_get_userbyid(relowner) FROM pg_class WHERE relname = 'orders' AND relnamespace = 'ai'::regnamespace`).Scan(&owner); err != nil {
		t.Fatalf("reading the view's owner: %v", err)
	}
	want := "assist_view_owner_" + financeResetSuffix(t, ctx, restored)
	if owner != want {
		t.Fatalf("the view is owned by %q, want the box's own view owner role %q", owner, want)
	}

	// The public schema carries the initdb shape again: pg_database_owner owns
	// it and PUBLIC holds USAGE on it.
	var schemaOwner string
	var publicUsage bool
	if err := restored.QueryRow(ctx, `SELECT pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname = 'public'`).Scan(&schemaOwner); err != nil {
		t.Fatalf("reading the public schema's owner: %v", err)
	}
	if schemaOwner != "pg_database_owner" {
		t.Fatalf("the public schema is owned by %q, want the initdb owner pg_database_owner", schemaOwner)
	}
	if err := restored.QueryRow(ctx, `SELECT has_schema_privilege('public', 'public', 'USAGE')`).Scan(&publicUsage); err != nil {
		t.Fatalf("reading the public grant: %v", err)
	}
	if !publicUsage {
		t.Fatalf("PUBLIC holds no USAGE on the public schema: %t", publicUsage)
	}
}

// financeResetSuffix answers the suffix the assist role names carry on this
// database: the one the migrations derive from the database's own name.
func financeResetSuffix(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	var database string
	if err := pool.QueryRow(ctx, `SELECT current_database()`).Scan(&database); err != nil {
		t.Fatalf("reading the database's name: %v", err)
	}
	return readtx.Suffix(database)
}
