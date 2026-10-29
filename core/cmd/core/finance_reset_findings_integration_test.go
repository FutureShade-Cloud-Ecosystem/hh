//go:build integration

package main

// finance_reset_findings_integration_test.go proves the three findings the
// box rehearsal found, against a real Postgres (W2-RESET-FIX-r1).
//
//   - finding 1: a dump write that fails leaves the database's tables in
//     place. The reset decodes the dump, writes it and checks its digest
//     BEFORE it drops any schema, and the file lands on disk under a
//     directory the environment names, not on the shared memory tmpfs whose
//     size the box does not control.
//   - finding 2: after a reset, a migration run as a non owner role that
//     reads public (the 0419 shape) succeeds: the recreated public schema
//     carries the initdb grant again.
//   - finding 3: a world dumped from a database named one way restores into a
//     box database named another way, with a role that is not postgres, and
//     the assist grants land on the box's own roles.
//
// The box's role is never postgres here. It is a plain role that owns the
// box's database and holds the membership the assist migrations need, the
// way the deploy's own DATABASE_URL names it.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	coreid "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/db"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

// The box's own role: not a superuser, with CREATEROLE because the assist
// migrations create the roles they own. The password is an invented
// placeholder for the tests, never a credential.
const (
	financeResetAppRoleName = "finreset_app"
	financeResetAppPassword = "finreset-app-not-a-secret"
)

// financeResetAdminDSN answers the admin DSN: DATABASE_URL pointed at the
// postgres database, the one the tests create their databases from.
func financeResetAdminDSN(t *testing.T) string {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dsn == "" {
		t.Skip("DATABASE_URL is unset, the finance reset's integration tests need a Postgres to build their databases on")
	}
	return swapDatabase(dsn, "postgres")
}

// financeResetRoleDSN answers the DSN of one role against one database.
func financeResetRoleDSN(t *testing.T, dsn, user, password, database string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("the database url: %v", err)
	}
	u.User = url.UserPassword(user, password)
	u.Path = "/" + database
	return u.String()
}

// financeResetEnsureAppRole creates the box's plain role once per cluster.
// The role is created by the admin connection, so the tests never need a
// superuser for the box's own work.
func financeResetEnsureAppRole(t *testing.T, adminDSN string) {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatalf("connecting to the admin database: %v", err)
	}
	defer pool.Close()
	_, err = pool.Exec(ctx, `
		DO $do$
		BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '`+financeResetAppRoleName+`') THEN
				CREATE ROLE `+financeResetAppRoleName+` LOGIN PASSWORD '`+financeResetAppPassword+`'
					CREATEROLE NOSUPERUSER NOBYPASSRLS NOCREATEDB NOREPLICATION;
			END IF;
		END
		$do$`)
	if err != nil {
		t.Fatalf("the box's role: %v", err)
	}
}

// financeResetFreshDB builds a database owned by the box's plain role and
// migrates it as that role: the shape the deployed box has, where the
// migrating role is not a superuser and owns its own database. upTo names the
// last migration version the database is brought to ("" for the whole set).
func financeResetFreshDB(t *testing.T, name, upTo string) string {
	t.Helper()
	ctx := context.Background()
	adminDSN := financeResetAdminDSN(t)
	financeResetEnsureAppRole(t, adminDSN)
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatalf("connecting to the admin database: %v", err)
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
		t.Fatalf("dropping %s: %v", name, err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name+" OWNER "+financeResetAppRoleName); err != nil {
		t.Fatalf("creating %s: %v", name, err)
	}
	dsn := financeResetRoleDSN(t, adminDSN, financeResetAppRoleName, financeResetAppPassword, name)
	financeResetMigrate(t, dsn, upTo)
	return dsn
}

// financeResetMigrate applies the migration set as the caller of the DSN:
// the box's own migrating role, the way the boot's migrate runs. upTo names
// the last migration version to apply ("" for the whole set).
func financeResetMigrate(t *testing.T, dsn, upTo string) {
	t.Helper()
	ctx := context.Background()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting to the database to migrate: %v", err)
	}
	defer pool.Close()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("the working directory: %v", err)
	}
	// The migration set is read relative to the core directory.
	if err := os.Chdir(filepath.Join(wd, "..", "..")); err != nil {
		t.Fatalf("the core directory: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if upTo == "" {
		if _, err := applyMigrations(ctx, pool, io.Discard); err != nil {
			t.Fatalf("the migrations: %v", err)
		}
		return
	}
	// Up to a version means every file up to it, and nothing after it: the
	// files are applied by hand, in the order the migrate role applies them.
	// The bookkeeping table is the migrate step's own, and it is made first,
	// the way the migrate role makes it.
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		t.Fatalf("the migration bookkeeping table: %v", err)
	}
	files, err := filepath.Glob("db/migrations/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("the migration set: %v", err)
	}
	sort.Strings(files)
	for _, f := range files {
		version := strings.TrimSuffix(filepath.Base(f), ".sql")
		if financeResetVersionNumber(version) > financeResetVersionNumber(upTo) {
			continue
		}
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			t.Fatalf("applying %s: %v", version, err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1) ON CONFLICT DO NOTHING`, version); err != nil {
			t.Fatalf("recording %s: %v", version, err)
		}
	}
}

// financeResetVersionNumber answers the numeric part of a migration version:
// the four digits at its front, as a number, so two versions can be ordered.
func financeResetVersionNumber(version string) int {
	n := 0
	digits := 0
	for _, r := range version {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
		digits++
	}
	if digits == 0 {
		return 0
	}
	return n
}

// financeResetMarkBox marks the database as the finance box and gives it the
// org anchor the reset's audit row names.
func financeResetMarkBox(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if err := settings.WriteBoxKindTx(ctx, pool, "finance"); err != nil {
		t.Fatalf("marking the box: %v", err)
	}
	if _, _, err := coreid.ProvisionOrgAnchor(ctx, pool, finOrgSlug, "Finance Box"); err != nil {
		t.Fatalf("the org anchor: %v", err)
	}
}

// financeResetDigest answers the sha256 of a dump: the world's own digest.
func financeResetDigest(dump []byte) string {
	sum := sha256.Sum256(dump)
	return hex.EncodeToString(sum[:])
}
