package main

// boxkind.go is the finance box boot guard (the deploy spec FINANCE-BOX-SPEC-r1,
// section 6, rules 2 and 3). A copy whose environment says HH_BOX_KIND=finance
// is the finance box, and the finance box refuses to boot:
//
//   - unless APP_ENV is staging (the stub payment rails are refused in
//     production by the rails' own selectors, and the box exists to run the
//     stubs against the finance test world);
//   - unless both payment rails resolve to the stub (a finance box that
//     moved real money would be a different machine, and a rail flipped to
//     run behind the box statement's back is exactly the drift this guard
//     exists to catch);
//   - against a non empty database whose box_kind is not finance: the box was
//     pointed at another database (the testers' copy, staging, a demo world),
//     and it refuses before migrating, never writing into somebody else's
//     data.
//
// An EMPTY database passes: on first boot the entrypoint migrates and seeds
// before serve starts, so a finance boot never serves an unseeded database.
// The guard reads and writes box_kind only through the settings module, the
// owner of system_settings.
//
// serve and worker both build their graph through registerRoutes, so the one
// call site there covers both roles.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/config"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/db"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

const (
	// envBoxKind is the box statement the guard reads; any value other than
	// boxKindFinance (including unset) leaves the copy an ordinary copy.
	envBoxKind = "HH_BOX_KIND"
	// boxKindFinance is the only kind this tree knows how to be.
	boxKindFinance = "finance"
)

// financeBoxBootGuard is the guard, in the spec's order. cfg is the already
// validated configuration; pool may be nil, in which case only the
// environment legs run and the database leg is skipped (the legs that refuse
// before any connection is attempted).
func financeBoxBootGuard(getenv func(string) string, cfg config.Config, pool *pgxpool.Pool) error {
	if strings.TrimSpace(getenv(envBoxKind)) != boxKindFinance {
		return nil
	}
	if cfg.Env != "staging" {
		return fmt.Errorf("finance box: %s=finance requires APP_ENV=staging: this box runs the stub payment rails against the finance test world, and the rails refuse production by themselves (set APP_ENV to staging or unset %s)", envBoxKind, envBoxKind)
	}
	if mode := strings.ToLower(strings.TrimSpace(getenv(payment.EnvCardMode))); mode != "" && mode != payment.CardModeStub {
		return fmt.Errorf("finance box: %s must be unset or %s (the card rail stays on the stub on this box)", payment.EnvCardMode, payment.CardModeStub)
	}
	if mode := strings.ToLower(strings.TrimSpace(getenv(payments.EnvGatewayMode))); mode != "" && mode != payments.ModeStub {
		return fmt.Errorf("finance box: %s must be unset or %s (the portal rail stays on the stub on this box)", payments.EnvGatewayMode, payments.ModeStub)
	}
	if pool == nil {
		return nil
	}
	return financeBoxDatabaseGuard(context.Background(), pool)
}

// financeBoxDatabaseGuard is the database leg, in this order: an empty
// database is first boot territory and passes; anything non empty must
// answer box_kind=finance or the boot stops. A database whose bookkeeping
// is present but whose settings table cannot say finance (a missing table
// is a foreign schema, a malformed row is a damaged one) is somebody
// else's world and is refused, never guessed at.
func financeBoxDatabaseGuard(ctx context.Context, pool *pgxpool.Pool) error {
	empty, err := financeBoxDatabaseEmpty(ctx, pool)
	if err != nil {
		return fmt.Errorf("finance box: the database's shape could not be read: %w", err)
	}
	if empty {
		return nil
	}
	kind, err := settings.ReadBoxKind(ctx, pool)
	if err != nil {
		return fmt.Errorf("finance box: %s could not be read: %w", settings.KeyBoxKind, err)
	}
	if kind == boxKindFinance {
		return nil
	}
	return fmt.Errorf("finance box: this database's %s is not %s (it is unset or names another box): the box was pointed at a database it did not seed, and it refuses to boot against it", settings.KeyBoxKind, boxKindFinance)
}

// financeBoxDatabaseEmpty reports whether the database has nothing in it
// yet: no migration has ever been applied. A database without the
// bookkeeping table is empty by definition; the guard must never treat a
// migrated world as empty because the query failed.
func financeBoxDatabaseEmpty(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	var count int
	err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&count)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42P01" {
			return true, nil
		}
		return false, err
	}
	return count == 0, nil
}

// financeBoxCheckRole is the entrypoint's pre-migrate check on a non empty
// database (the one-shot spelling of the guard). It refuses unless the
// environment states the finance box and the database answers
// box_kind=finance, so the box migrates only a database it seeded.
func financeBoxCheckRole(getenv func(string) string) error {
	cfg := config.Load()
	if err := financeBoxBootGuard(getenv, cfg, nil); err != nil {
		return fmt.Errorf("finance-box-check: %w", err)
	}
	dsn, err := resolveOneshotDatabaseURL(getenv)
	if err != nil {
		return fmt.Errorf("finance-box-check: %w", err)
	}
	printOneshotDatabaseTarget(os.Stdout, "finance-box-check", dsn)
	pool, err := db.Connect(context.Background(), dsn)
	if err != nil {
		return fmt.Errorf("finance-box-check: cannot connect: %w", err)
	}
	defer pool.Close()
	return financeBoxDatabaseGuard(context.Background(), pool)
}
