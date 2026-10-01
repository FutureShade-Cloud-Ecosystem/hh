//go:build demo

package boot

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/flows"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DemoCheck is the demo-check one-shot's substance: the entrypoint's first
// command, before migrate, before the seed, before anything writes. It
// refuses unless the box says demo twice (the G2 checks), both database
// variables are present and equal, and the database is either empty (no
// schema_migrations table, so it is a fresh database the populate will
// fill) or passes the demo database check (G3). The variables are
// compared, not trusted: the serve role reads CORE_DATABASE_URL and every
// one-shot reads DATABASE_URL, so a mismatch is two databases wearing one
// name, and that is exactly the state a reset must never be able to
// create.
func DemoCheck(ctx context.Context, lookupEnv func(string) (string, bool)) error {
	if err := CheckDemoBox(lookupEnv); err != nil {
		return err
	}
	databaseURL, ok := lookupEnv("DATABASE_URL")
	if !ok || strings.TrimSpace(databaseURL) == "" {
		return errors.New("DATABASE_URL must be set: demo-check compares it with CORE_DATABASE_URL and refuses a box where they could drift apart")
	}
	coreURL, ok := lookupEnv("CORE_DATABASE_URL")
	if !ok || strings.TrimSpace(coreURL) == "" {
		return errors.New("CORE_DATABASE_URL must be set: demo-check compares it with DATABASE_URL and refuses a box where they could drift apart")
	}
	if databaseURL != coreURL {
		return errors.New("DATABASE_URL and CORE_DATABASE_URL differ: the serve role reads CORE_DATABASE_URL and every one-shot reads DATABASE_URL, so a mismatch here is two databases and demo-check stops the entrypoint")
	}
	pool, err := authz.NewPgxPoolWithTracer(ctx, databaseURL, nil)
	if err != nil {
		return fmt.Errorf("demo-check: %w", err)
	}
	defer pool.Close()
	empty, err := schemaEmpty(ctx, pool)
	if err != nil {
		return err
	}
	if empty {
		return nil
	}
	return CheckDemoDatabase(ctx, pool)
}

// CheckDemoDatabase refuses any database that is not the demo's invented
// world (G3): exactly one org and it is hh-demo, the one-row demo_box
// table only the demo populate writes (outside the migration chain, so
// its presence is the populate's own signature and no migration or seed
// can produce it), no ACTIVE user at an address outside the reserved demo
// domain and the reserved example top level domains, and no active
// payment configuration but the demo's invented one. The serve role runs
// it before the listener opens, so a demo image pointed at a real
// database refuses before it answers a request.
func CheckDemoDatabase(ctx context.Context, pool *pgxpool.Pool) error {
	var orgCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM org`).Scan(&orgCount); err != nil {
		return fmt.Errorf("demo database check: org anchor: %w", err)
	}
	if orgCount != 1 {
		return fmt.Errorf("demo database check: the database holds %d org rows, want exactly one", orgCount)
	}
	var slug string
	if err := pool.QueryRow(ctx, `SELECT slug::text FROM org`).Scan(&slug); err != nil {
		return fmt.Errorf("demo database check: org anchor: %w", err)
	}
	if slug != flows.OrgSlug {
		return fmt.Errorf("demo database check: the one org is %q, want %q: this is not the demo's database", slug, flows.OrgSlug)
	}

	ok, err := tableExists(ctx, pool, "demo_box")
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("demo database check: no demo_box table: only the demo populate writes it, so the demo world has not been built here")
	}
	var boxRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM demo_box`).Scan(&boxRows); err != nil {
		return fmt.Errorf("demo database check: demo_box: %w", err)
	}
	if boxRows != 1 {
		return fmt.Errorf("demo database check: demo_box holds %d rows, want the populate's one row", boxRows)
	}
	// The row must be the populate's, by the marker the populate writes:
	// existence and a row count alone would accept any one-row table of
	// the name. A marker column that is not there errors here and fails
	// closed.
	var marker string
	if err := pool.QueryRow(ctx, `SELECT COALESCE(marker, '') FROM demo_box`).Scan(&marker); err != nil {
		return fmt.Errorf("demo database check: demo_box marker: %w", err)
	}
	if marker != flows.PopulateMarker {
		return fmt.Errorf("demo database check: demo_box does not carry the populate's marker: this is not the demo populate's world")
	}

	var outsiders int
	// A tombstoned row is not an ACTIVE user: deleted_at is the row's
	// end, so the count reads only rows still standing. The plan's words
	// are "no ACTIVE user"; the tombstone is what makes a row not a user
	// anymore, and this comment is that reading's record.
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM users
		  WHERE status = 'ACTIVE' AND deleted_at IS NULL
		    AND NOT (lower(email::text) LIKE '%@' || $1
		             OR lower(email::text) LIKE '%.' || $1
		             OR right(lower(email::text), 8) = '.example'
		             OR right(lower(email::text), 8) = '.invalid')`,
		flows.ReservedDomain).Scan(&outsiders); err != nil {
		return fmt.Errorf("demo database check: users: %w", err)
	}
	if outsiders > 0 {
		return fmt.Errorf("demo database check: %d ACTIVE user(s) sit outside the reserved demo domains: this is not the demo's database", outsiders)
	}

	// The payment configuration: exactly one active row, and it is the
	// populate's invented one, the demo merchant id with no host. The
	// merchant id alone marks nothing (it is a public constant of this
	// tree); the empty host is the half that says the row cannot reach a
	// processor. A second active row, another merchant, a host set, or
	// none at all is a refusal.
	var activeCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM payment_config WHERE status = 'active'`).Scan(&activeCount); err != nil {
		return fmt.Errorf("demo database check: payment configuration: %w", err)
	}
	if activeCount != 1 {
		return fmt.Errorf("demo database check: %d active payment configuration row(s), want exactly the populate's invented one", activeCount)
	}
	var merchant, host string
	if err := pool.QueryRow(ctx,
		`SELECT merchant_id, COALESCE(base_url, '') FROM payment_config WHERE status = 'active'`).
		Scan(&merchant, &host); err != nil {
		return fmt.Errorf("demo database check: payment configuration: %w", err)
	}
	if merchant != flows.DemoMerchantID {
		return fmt.Errorf("demo database check: the active payment configuration's merchant id is not the populate's invented one: this is not the demo's database")
	}
	if host != "" {
		return errors.New("demo database check: the active payment configuration carries a host: the demo's row must have no host, so the demo's card rail can reach no processor")
	}
	return nil
}

// schemaEmpty reports whether the database has no schema_migrations
// table, which is the entrypoint's sign of a fresh database.
func schemaEmpty(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	ok, err := tableExists(ctx, pool, "schema_migrations")
	if err != nil {
		return false, err
	}
	return !ok, nil
}

// tableExists reports whether the named table exists in the pool's
// current schema.
func tableExists(ctx context.Context, pool *pgxpool.Pool, name string) (bool, error) {
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables
		  WHERE table_schema = current_schema() AND table_name = $1`, name).Scan(&n); err != nil {
		return false, fmt.Errorf("demo database check: %s: %w", name, err)
	}
	return n > 0, nil
}
