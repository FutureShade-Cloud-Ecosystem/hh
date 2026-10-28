package main

// boxkind.go is the finance box boot guard and the one definition of the
// box database's shape (the deploy spec FINANCE-BOX-SPEC-r1, section 6,
// rules 2 and 3). A copy whose environment says HH_BOX_KIND=finance is the
// finance box, and the finance box refuses to boot:
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
// THE GUARD RUNS IN BOTH DIRECTIONS, so it cannot be reversed:
//
//   - the environment claims the box: the database must be the box's own
//     world (an EMPTY database is first boot territory and passes, and a
//     database the box did not seed is refused before anything writes);
//   - the environment does not claim the box, but the database IS the box's
//     own world: the boot is refused anyway, because the box statement can
//     be LOST (a platform entrypoint override, a hand run of the binary, a
//     future role in the image) and a lost statement must never turn a
//     finance world into a copy served as somebody else's. A database that
//     is not the box's own world is nobody's business here, so a demo or a
//     staging box is unaffected.
//
// The guard reads and writes box_kind only through the settings module, the
// owner of system_settings, and reads the seed's own idempotency key only
// through the identity module, the owner of users. cmd/core carries no SQL
// of its own.
//
// serve and worker both build their graph through registerRoutes, so the one
// call site there covers both roles.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/config"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/db"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

const (
	// envBoxKind is the box statement the guard reads; any value other than
	// boxKindFinance (including unset) leaves the copy an ordinary copy,
	// unless the database says it is the finance box's own world.
	envBoxKind = "HH_BOX_KIND"
	// boxKindFinance is the only kind this tree knows how to be.
	boxKindFinance = "finance"
	// financeBoxBootstrapSchema is the finance bootstrap ownership mark: the
	// one schema the finance entrypoint creates in a completely empty
	// database, after the empty check and before the first migrate, and
	// nothing else ever creates. It is a schema in the catalog rather than a
	// row because it must exist before any migration does (it needs no
	// migration, and a migration needs the mark first), and a schema takes
	// no table. The word is spelled here and in the entrypoint, and the
	// image files test reads the two against each other.
	financeBoxBootstrapSchema = "finance_box_bootstrap"
)

// The database's state, as the box reads it. One definition, in one place:
// the boot guard and the entrypoint's classifier (core finance-box-state)
// read these words, so the two can never drift apart.
const (
	// financeBoxStateEmpty: no migration bookkeeping, no table in any user
	// schema, no large object and no user schema outside public and the
	// box's own bootstrap mark. A fresh database: first boot territory.
	financeBoxStateEmpty = "empty"
	// financeBoxStateUnseeded: the box's own bootstrap mark is present, the
	// migrations are applied, the seed is not, and nothing is marked. An
	// interrupted first boot: the seed's own guards decide whether the seed
	// may run. Without the mark the same shape is foreign.
	financeBoxStateUnseeded = "unseeded"
	// financeBoxStateFinance: the database is the finance box's own world.
	financeBoxStateFinance = "finance"
	// financeBoxStateForeign: anything else. A world the box did not plant,
	// a foreign schema, or a database marked as another box.
	financeBoxStateForeign = "foreign"
)

// financeBoxBootGuard is the guard, in the spec's order, and it runs in both
// directions. cfg is the already validated configuration; pool may be nil, in
// which case only the environment legs run and the database leg is skipped
// (the legs that refuse before any connection is attempted).
func financeBoxBootGuard(getenv func(string) string, cfg config.Config, pool *pgxpool.Pool) error {
	claims := strings.TrimSpace(getenv(envBoxKind)) == boxKindFinance
	if claims {
		if err := financeBoxEnvironmentLegs(getenv, cfg); err != nil {
			return err
		}
	}
	if pool == nil {
		return nil
	}
	ctx := context.Background()
	if claims {
		// The environment claims the finance box: the database must be the
		// box's own world.
		return financeBoxDatabaseGuard(ctx, pool)
	}
	// The environment does not claim the finance box: the database decides
	// whether the guard has anything to say.
	return financeBoxReverseGuard(ctx, pool)
}

// financeBoxEnvironmentLegs are the legs that need no database: the staging
// env and the two stub rails. They run before any connection is attempted,
// so a copy that claims the finance box wrongly stops before it opens
// anything.
func financeBoxEnvironmentLegs(getenv func(string) string, cfg config.Config) error {
	if cfg.Env != "staging" {
		return fmt.Errorf("finance box: %s=finance requires APP_ENV=staging: this box runs the stub payment rails against the finance test world, and the rails refuse production by themselves (set APP_ENV to staging or unset %s)", envBoxKind, envBoxKind)
	}
	if mode := strings.ToLower(strings.TrimSpace(getenv(payment.EnvCardMode))); mode != "" && mode != payment.CardModeStub {
		return fmt.Errorf("finance box: %s must be unset or %s (the card rail stays on the stub on this box)", payment.EnvCardMode, payment.CardModeStub)
	}
	if mode := strings.ToLower(strings.TrimSpace(getenv(payments.EnvGatewayMode))); mode != "" && mode != payments.ModeStub {
		return fmt.Errorf("finance box: %s must be unset or %s (the portal rail stays on the stub on this box)", payments.EnvGatewayMode, payments.ModeStub)
	}
	if domains := strings.TrimSpace(getenv(envSigninEmailDomains)); domains != "" {
		return fmt.Errorf("finance box: %s must be unset on the finance box: the box's people are exactly the list in %s, and a domain allow would let a whole domain in past that list (set it empty or unset it)", envSigninEmailDomains, envFinanceUsers)
	}
	return nil
}

// financeBoxEmptyBranchCheck is the empty branch's own check: the box's
// database role must be able to create roles. Migration 0418 creates the
// assist roles, and the migrate runner applies each file in its own
// transaction, so a role without that right leaves a fresh database half
// migrated: every migration before 0418 commits and 0418 rolls back whole,
// and the box is left with a mark, a bookkeeping table and no seed.
//
// The check runs in the empty branch alone. Only migrations that create roles
// need the right (0418 and 0427 today), and a database the box has already
// migrated holds those roles, so a finance marked database boots with a role
// that has no CREATEROLE, and only a fresh database is refused. A later
// migration that creates a role would stop such a box at its migrate, which
// rolls that file back whole; the deploy README says to keep the right.
//
// It refuses in words, naming the right and the migration that needs it, so
// the operator is told what to change and not merely that something failed.
func financeBoxEmptyBranchCheck(ctx context.Context, pool *pgxpool.Pool) error {
	can, err := CurrentRoleCanCreateRoles(ctx, pool)
	if err != nil {
		return fmt.Errorf("finance box: the box's database role could not be read: %w", err)
	}
	if !can {
		return errors.New("finance box: the box's database role needs CREATEROLE: migration 0418 creates the assist roles")
	}
	return nil
}

// financeBoxDatabaseGuard is the database leg of the direction where the
// environment claims the box, in this order: an empty database is first boot
// territory and passes, but only through the empty branch's own check (the
// role must be able to create the roles migration 0418 creates); anything non
// empty must answer box_kind=finance or the boot stops. A database whose
// bookkeeping is present but whose settings
// table cannot say finance (a missing table is a foreign schema, a malformed
// row is a damaged one) is somebody else's world and is refused, never
// guessed at.
func financeBoxDatabaseGuard(ctx context.Context, pool *pgxpool.Pool) error {
	empty, err := financeBoxDatabaseEmpty(ctx, pool)
	if err != nil {
		return fmt.Errorf("finance box: the database's shape could not be read: %w", err)
	}
	if empty {
		return financeBoxEmptyBranchCheck(ctx, pool)
	}
	kind, err := settings.NewDealerConfigReader(pool).BoxKind(ctx)
	if err != nil {
		return fmt.Errorf("finance box: %s could not be read: %w", settings.KeyBoxKind, err)
	}
	if kind == boxKindFinance {
		return nil
	}
	return fmt.Errorf("finance box: this database's %s is not %s (it is unset or names another box): the box was pointed at a database it did not seed, and it refuses to boot against it", settings.KeyBoxKind, boxKindFinance)
}

// financeBoxReverseGuard is the guard's other direction: the environment
// does not claim the finance box, but the database IS the box's own world.
// The box statement can be lost, and it can never be reversed, so the boot
// is refused and the refusal names the three things the environment must
// state. A database that cannot say what it is (a foreign schema, a
// database this tree never migrated) is not the box's own world, and the
// guard has nothing to say about it.
func financeBoxReverseGuard(ctx context.Context, pool *pgxpool.Pool) error {
	kind, err := settings.NewDealerConfigReader(pool).BoxKind(ctx)
	if err != nil {
		return nil
	}
	if kind != boxKindFinance {
		return nil
	}
	return fmt.Errorf("finance box: this database's %s is %s, so the environment must state %s=%s, APP_ENV=staging and %s=%s: the box statement cannot be lost or reversed, and the box refuses to serve a finance world that does not claim itself", settings.KeyBoxKind, boxKindFinance, envBoxKind, boxKindFinance, envSigninMode, signinConsole)
}

// financeBoxDatabaseShape reads the database's shape in four facts: whether
// the migration bookkeeping is there (this tree's migrations were applied),
// how many tables the user schemas hold all told, how many large objects the
// database holds, and whether a user schema outside public and the box's own
// bootstrap mark is present. The bookkeeping read is the migrate runner's
// own (roles_oneshot.go), so the guard never spells the query itself; the
// guard must never treat a world it cannot read as empty, and a world that
// keeps its tables in a schema of its own, or its data in a large object, is
// not empty either.
func financeBoxDatabaseShape(ctx context.Context, pool *pgxpool.Pool) (bookkeeping bool, tables int, largeObjects int, foreignSchema bool, err error) {
	_, present, err := MigrationsAppliedCount(ctx, pool)
	if err != nil {
		return false, 0, 0, false, err
	}
	tables, err = UserTableCount(ctx, pool)
	if err != nil {
		return false, 0, 0, false, err
	}
	largeObjects, err = LargeObjectCount(ctx, pool)
	if err != nil {
		return false, 0, 0, false, err
	}
	foreignSchema, err = ForeignUserSchemaPresent(ctx, pool)
	if err != nil {
		return false, 0, 0, false, err
	}
	return present, tables, largeObjects, foreignSchema, nil
}

// financeBoxDatabaseEmpty reports whether the database holds nothing yet: no
// migration bookkeeping, no table in any user schema, no large object and no
// user schema outside public and the box's own bootstrap mark. One
// definition, read here by the boot guard and by the entrypoint's classifier
// (core finance-box-state), so the two can never drift apart.
func financeBoxDatabaseEmpty(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	bookkeeping, tables, largeObjects, foreignSchema, err := financeBoxDatabaseShape(ctx, pool)
	if err != nil {
		return false, err
	}
	return !bookkeeping && tables == 0 && largeObjects == 0 && !foreignSchema, nil
}

// financeBoxDatabaseState is the database's state, the one definition the
// entrypoint acts on:
//
//	empty     no bookkeeping, no table in any user schema, no large object
//	          and no user schema outside public and the box's own bootstrap
//	          mark: a fresh database.
//	unseeded  the box's own bootstrap mark is there, the box's own
//	          bookkeeping is there and nothing is marked and the seed is not
//	          applied: an interrupted first boot (the entrypoint marks the
//	          database before the first migrate, and a crash between migrate
//	          and seed leaves exactly that) or an interrupted first migrate
//	          (the bookkeeping table and nothing else). The seed's own
//	          guards decide whether the seed may run.
//	finance   the database is the finance box's own world.
//	foreign   anything else: a world the box did not plant, a foreign
//	          schema, a large object somebody else left here, a database
//	          marked as another box, or a migrated unmarked database with no
//	          bootstrap mark. Recovery is the mark's answer alone: a database
//	          the box never marked is nobody's to heal, whatever its shape.
//
// The seed's own idempotency key (the same login core seed guards on) is
// what says the seed is applied: a database that holds it holds the demo
// world, which the box did not plant, so the box refuses it.
func financeBoxDatabaseState(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	bookkeeping, tables, largeObjects, foreignSchema, err := financeBoxDatabaseShape(ctx, pool)
	if err != nil {
		return "", err
	}
	if !bookkeeping {
		if tables == 0 && largeObjects == 0 && !foreignSchema {
			return financeBoxStateEmpty, nil
		}
		// Tables, a large object or a foreign schema but no bookkeeping: a
		// foreign world. It is not empty (the box never migrates a database
		// it did not create) and it is not the box's own.
		return financeBoxStateForeign, nil
	}
	kind, marked, err := settings.NewDealerConfigReader(pool).BoxKindMark(ctx)
	if err != nil {
		return "", err
	}
	if kind == boxKindFinance {
		// The box's own world, marked at the first seed: it never takes the
		// bootstrap mark's path, and the mark's absence says nothing about
		// it.
		return financeBoxStateFinance, nil
	}
	if kind != "" {
		// Another box's mark: a world this box does not own, and never one
		// to seed into.
		return financeBoxStateForeign, nil
	}
	// Unmarked and migrated: recovery is the bootstrap mark's answer alone.
	// The entrypoint writes the mark into a completely empty database before
	// the first migrate, so a database without it was never this box's to
	// migrate, whatever its shape, and it is foreign.
	own, err := FinanceBootstrapMarkPresent(ctx, pool)
	if err != nil {
		return "", err
	}
	if !own {
		return financeBoxStateForeign, nil
	}
	if !marked {
		// The settings table is not there: this tree's migrations have not
		// reached it, so the world is the box's own world under
		// construction (an interrupted first migrate leaves exactly this:
		// the bookkeeping table and nothing else). Nothing is marked and
		// nothing is seeded, and the migrate and the seed that follow decide
		// what happens next.
		return financeBoxStateUnseeded, nil
	}
	// The settings table is there, so the migrations have reached migration
	// 0014, and the users table (migration 0004) is there with it.
	seeded, err := identity.HasUser(ctx, pool, financeSeedGuardEmail)
	if err != nil {
		return "", err
	}
	if seeded {
		return financeBoxStateForeign, nil
	}
	return financeBoxStateUnseeded, nil
}

// financeBoxCheckRole is the entrypoint's pre-migrate check on a non empty
// database (the one-shot spelling of the guard). It refuses unless the
// environment states the finance box and the database answers
// box_kind=finance, so the box migrates only a database it seeded.
func financeBoxCheckRole(getenv func(string) string) error {
	if strings.TrimSpace(getenv(envBoxKind)) != boxKindFinance {
		return fmt.Errorf("finance-box-check: refusing to run: %s must be %s (this check guards the finance box's database and answers the finance statement alone)", envBoxKind, boxKindFinance)
	}
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

// financeBoxStateRole is the entrypoint's classifier (the one-shot spelling
// of the shape definition): it prints the database's state, one word on
// standard output, so the entrypoint and the boot guard act on one
// definition. The target banner goes to standard error, so the word is the
// only thing on standard output. The role refuses on a database it cannot
// read, and on one more case: an empty database whose role cannot create
// roles. That refusal is the empty branch's own check (the one the boot
// guard runs too), and it belongs here because this role runs before the
// entrypoint writes the bootstrap ownership mark: a fresh database must not
// be marked and half migrated by a role that can never finish the migrate.
// The foreign state is an answer, and the entrypoint's own box kind check
// refuses it.
func financeBoxStateRole(getenv func(string) string) error {
	dsn, err := resolveOneshotDatabaseURL(getenv)
	if err != nil {
		return err
	}
	printOneshotDatabaseTarget(os.Stderr, "finance-box-state", dsn)
	pool, err := db.Connect(context.Background(), dsn)
	if err != nil {
		return fmt.Errorf("cannot connect: %w", err)
	}
	defer pool.Close()
	state, err := financeBoxDatabaseState(context.Background(), pool)
	if err != nil {
		return fmt.Errorf("the database's shape could not be read: %w", err)
	}
	if state == financeBoxStateEmpty {
		// The empty branch is the branch the entrypoint writes the
		// bootstrap ownership mark into, and the mark goes in before the
		// first migrate: the role is checked here, so a role that cannot
		// create the assist roles leaves nothing behind at all: no mark, no
		// migration row, no half migrated database.
		if err := financeBoxEmptyBranchCheck(context.Background(), pool); err != nil {
			return err
		}
	}
	fmt.Println(state)
	return nil
}
