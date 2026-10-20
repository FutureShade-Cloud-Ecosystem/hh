// roles_oneshot.go carries the migrate, seed and demoseed roles (the
// plan's fold of the pin's remaining three commands into cmd/core; the
// decode_udf command is the recorded leave-behind with the demo
// apparatus). Migrate applies the frozen migration set read-only (the
// schema_migrations bookkeeping carries as-is; per D39 the two runners'
// applied-history mechanisms are HH-3.7's to report before HH-5, so no
// mechanism changes here). Seed and demoseed preserve their plan and apply
// modes.
package main

import (
	"context"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/config"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/db"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/logging"
)

//go:embed dibbits_seed.sql
var seedFS embed.FS

// migrateRole folds the pin's cmd/migrate behaviour: numbered SQL files
// from the frozen migrations directory, applied exactly once, recorded in
// schema_migrations. The directory is core/db/migrations in this tree (the
// ERP's backend/migrations is the frozen set HH-3.1 ported); the glob is
// read relative to the working directory, exactly as the pin's runner did.
func migrateRole(getenv func(string) string) error {
	cfg := config.Load()
	log := logging.New(cfg.Env)
	ctx := context.Background()

	// The target comes from the one-shot resolver, not config.Load:
	// HH-199's rule (oneshot_db.go), refused and printed before any
	// connection is attempted.
	dsn, err := resolveOneshotDatabaseURL(getenv)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	printOneshotDatabaseTarget(os.Stdout, "migrate", dsn)

	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("migrate: cannot connect: %w", err)
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("migrate: bookkeeping table: %w", err)
	}

	files, err := filepath.Glob("db/migrations/*.sql")
	if err != nil || len(files) == 0 {
		log.Info("migrate: no migration files found", "glob_err", err)
		return nil
	}
	sort.Strings(files)

	for _, f := range files {
		version := strings.TrimSuffix(filepath.Base(f), ".sql")
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, version).Scan(&exists); err != nil {
			return fmt.Errorf("migrate: check %s: %w", version, err)
		}
		if exists {
			continue
		}
		body, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("migrate: read %s: %w", f, err)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("migrate: begin %s: %w", version, err)
		}
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migrate: apply failed %s: %w", version, err)
		}
		// A file that switches role (SET ROLE, SET LOCAL ROLE) must RESET ROLE before it
		// ends: the bookkeeping below must run as the migrating role, and an object a
		// switched role made must not be committed on a connection still switched. The
		// assist migrations create their objects under SET LOCAL ROLE so the owner is
		// right by construction (plan 3.1a); this turns a forgotten reset into a loud
		// failure that rolls the whole file back.
		var current, session string
		if err := tx.QueryRow(ctx, `SELECT current_user::text, session_user::text`).Scan(&current, &session); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migrate: role check %s: %w", version, err)
		}
		if current != session {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migrate: %s left the role set (current user %q, session user %q): a migration that uses SET ROLE or SET LOCAL ROLE must RESET ROLE before the file ends", version, current, session)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, version); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migrate: record failed %s: %w", version, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("migrate: commit failed %s: %w", version, err)
		}
		fmt.Printf("applied %s\n", version)
	}
	log.Info("migrations complete")
	return nil
}

// seedRole folds the pin's cmd/seed behaviour: the representative demo
// world from the embedded SQL file, prod-guarded and idempotent.
func seedRole(getenv func(string) string) error {
	cfg := config.Load()
	log := logging.New(cfg.Env)
	ctx := context.Background()
	seedSQL, err := seedFS.ReadFile("dibbits_seed.sql")
	if err != nil {
		return fmt.Errorf("seed: embedded seed file: %w", err)
	}

	// The target comes from the one-shot resolver, not config.Load:
	// HH-199's rule (oneshot_db.go), refused before anything here runs
	// and printed before the connection is attempted.
	dsn, err := resolveOneshotDatabaseURL(getenv)
	if err != nil {
		return fmt.Errorf("seed: %w", err)
	}

	// Refuse to seed anywhere but dev unless explicitly overridden: the
	// seed plants Tier-1 GLOBAL admins with a demo password, so this must
	// match the config's everything-that-is-not-dev-is-production-grade
	// stance, not just the literal word.
	if cfg.Env != "dev" && os.Getenv("ALLOW_SEED") != "true" {
		return fmt.Errorf("seed: refusing to run outside dev (set ALLOW_SEED=true to override), env %q", cfg.Env)
	}

	printOneshotDatabaseTarget(os.Stdout, "seed", dsn)
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("seed: cannot connect: %w", err)
	}
	defer pool.Close()

	// Idempotency guard: if the demo data already exists, do nothing. Keys
	// on the demo GM login rather than a product SKU (migrations insert
	// DBS-% products of their own, so a SKU guard trips on a fresh
	// migrated-only database and silently skips users/customers/pricing,
	// the pin's DIB-378 finding).
	var seeded bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE email = 'alex.morgan@dibbits.example')`).Scan(&seeded); err != nil {
		return fmt.Errorf("seed: guard check failed: %w", err)
	}
	if seeded {
		log.Info("seed: Dibbits demo data already present, nothing to do")
		return nil
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("seed: begin: %w", err)
	}
	if _, err := tx.Exec(ctx, string(seedSQL)); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("seed: apply failed: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("seed: commit failed: %w", err)
	}

	var products, vendors, customers, moves int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM products WHERE sku LIKE 'DBS-%'`).Scan(&products)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM vendors WHERE vendor_no LIKE 'V-%'`).Scan(&vendors)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM customers WHERE account_no LIKE 'CUST-%'`).Scan(&customers)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM stock_moves WHERE reason = 'M7 opening balance'`).Scan(&moves)
	log.Info("seed: Dibbits demo data loaded",
		"products", products, "vendors", vendors, "customers", customers, "opening_moves", moves)
	return nil
}
