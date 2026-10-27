package main

// finance_seed.go is the finance seed role (the deploy spec
// FINANCE-BOX-SPEC-r1, sections 3 and 8). It applies the embedded dibbits
// seed to a fresh, empty database the one time the finance box first boots,
// records box_kind=finance in system_settings through the settings module,
// and leaves the seed's users with no usable password through the identity
// module, so the seeded world cannot be signed into with the seed's well
// known demo password. core seed itself is never the answer here: it refuses
// outside dev, and an unset APP_ENV would count as dev and plant tier 1
// admins with a usable password. finance-seed refuses unless the box says
// the finance statement in full:
//
//   - HH_BOX_KIND=finance (the box kind guard, boxkind.go);
//   - APP_ENV=staging (the rails refuse production by themselves, and the
//     box exists for the stub world);
//   - CORE_SIGNIN_MODE=console (the default build's only mode);
//   - the database has no seed applied and no box_kind: the seed's own
//     idempotency key (the demo GM login, the same key core seed guards on)
//     is absent, and box_kind is unset.
//
// Everything lands in one transaction: the seed, the box kind and the
// credential clearing commit or roll back together, so a database the seed
// touched can never read half seeded.

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/config"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/db"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/logging"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

// financeSeedGuardEmail is the seed's idempotency key, the same fixed login
// core seed guards on (roles_oneshot.go): a database that holds it has the
// seed applied, whatever else it holds.
const financeSeedGuardEmail = "alex.morgan@dibbits.example"

func financeSeedRole(getenv func(string) string) error {
	cfg := config.Load()
	log := logging.New(cfg.Env)
	ctx := context.Background()

	// The guards, in the spec's order, each naming the variable or the check
	// and never a value that is not the required word.
	if strings.TrimSpace(getenv(envBoxKind)) != boxKindFinance {
		return fmt.Errorf("finance-seed: refusing to run: %s must be %s (this command plants the finance box's world and is guarded to it)", envBoxKind, boxKindFinance)
	}
	if cfg.Env != "staging" {
		return fmt.Errorf("finance-seed: refusing to run: APP_ENV must be staging, got a box that is not staging")
	}
	mode, err := readSigninMode(getenv, false)
	if err != nil {
		return fmt.Errorf("finance-seed: refusing to run: %w", err)
	}
	if mode != signinConsole {
		return fmt.Errorf("finance-seed: refusing to run: %s must be %s", envSigninMode, signinConsole)
	}

	seedSQL, err := seedFS.ReadFile("dibbits_seed.sql")
	if err != nil {
		return fmt.Errorf("finance-seed: embedded seed file: %w", err)
	}

	dsn, err := resolveOneshotDatabaseURL(getenv)
	if err != nil {
		return fmt.Errorf("finance-seed: %w", err)
	}
	printOneshotDatabaseTarget(os.Stdout, "finance-seed", dsn)

	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("finance-seed: cannot connect: %w", err)
	}
	defer pool.Close()

	// The database guards, both reads, before anything writes.
	var seeded bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE email = $1)`, financeSeedGuardEmail).Scan(&seeded); err != nil {
		return fmt.Errorf("finance-seed: guard check failed: %w", err)
	}
	if seeded {
		return fmt.Errorf("finance-seed: refusing to run: the seed is already applied (its demo world is present), and nothing here reseeds")
	}
	kind, err := settings.ReadBoxKind(ctx, pool)
	if err != nil {
		return fmt.Errorf("finance-seed: %s could not be read: %w", settings.KeyBoxKind, err)
	}
	if kind != "" {
		return fmt.Errorf("finance-seed: refusing to run: %s is already set to a box kind, and a database that belongs to another box is never reseeded", settings.KeyBoxKind)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("finance-seed: begin: %w", err)
	}
	if _, err := tx.Exec(ctx, string(seedSQL)); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("finance-seed: apply failed: %w", err)
	}
	if err := settings.WriteBoxKindTx(ctx, tx, boxKindFinance); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("finance-seed: recording the box kind: %w", err)
	}
	creds, err := identity.DisableSeedCredentials(ctx, tx)
	if err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("finance-seed: clearing the seed credentials: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("finance-seed: commit: %w", err)
	}

	var products, customers int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM products WHERE sku LIKE 'DBS-%'`).Scan(&products)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM customers WHERE account_no LIKE 'CUST-%'`).Scan(&customers)
	log.Info("finance-seed: the Dibbits seed world is loaded",
		"products", products, "customers", customers,
		"box_kind", boxKindFinance,
		"staff_passwords_cleared", creds.StaffCleared, "portal_passwords_cleared", creds.PortalCleared)
	return nil
}
