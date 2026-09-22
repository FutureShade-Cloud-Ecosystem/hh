// resolve.go, the three things the plan needs FROM the target database before it can be built.
//
// All three are reads of rows this command did not create and will not touch. That is the whole
// posture: the demo world ATTACHES to the deployment that already exists, its branches, its
// category taxonomy, its demo LOGINS, rather than standing up a parallel one beside it. A seed that
// invented its own branch would put a fake yard in every tester's branch picker; a seed that
// invented its own categories would fork the taxonomy somebody curates; and a seed that invented its
// own demo accounts would put a second 0200 dispatcher persona beside 0200's, with a password nobody knows.
//
// THE THIRD ONE IS WHY THIS FILE EXISTS AT ALL RATHER THAN THE LOOKUP LIVING IN plan.go. buildPlan is
// a pure function of planInput and two tests hold it to that, TestBuild_UsesNoWallClock reads the
// source, TestPlan_IsDeterministicOnAFixedClock reads the output, so a query cannot be added there.
// Threading the answer through planInput beside Branches and Categories is the pattern already in
// front of us, and it keeps the plan a value the tests can build on any machine with no database.
// demoseed_resolve carries the pin's cmd/demoseed/resolve.go (the fold:
// one-shot subcommand of cmd/core, plan and apply modes preserved).
package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// resolveBranches reads the active BRANCH locations, ordered by code so the mapping from the plan's
// branch 0 / branch 1 to real branches is STABLE across runs and across machines. An unordered read
// would silently swap which branch got which orders between two runs, and the idempotency guarantee
// only holds because the ids are derived from the plan, not from the branch, so the swap would go
// unnoticed while the demo world quietly said something different.
func resolveBranches(ctx context.Context, pool *pgxpool.Pool) ([]branch, error) {
	rows, err := pool.Query(ctx,
		`SELECT id::text, code::text, name FROM locations
		  WHERE loc_type = 'BRANCH' AND is_active ORDER BY code`)
	if err != nil {
		return nil, fmt.Errorf("demoseed: reading branches: %w", err)
	}
	defer rows.Close()
	var out []branch
	for rows.Next() {
		var b branch
		if err := rows.Scan(&b.ID, &b.Code, &b.Name); err != nil {
			return nil, fmt.Errorf("demoseed: scanning branch: %w", err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("demoseed: this database has no active BRANCH locations, there is nothing to attach a demo world to")
	}
	return out, nil
}

// resolveCategories maps the two category codes the demo catalogue uses onto whatever ids they
// carry on this database. A code that is absent is simply not in the map, and build.go leaves those
// products uncategorised, which renders correctly everywhere, because category_id is nullable and
// an uncategorised product is a real state the catalogue already handles.
func resolveCategories(ctx context.Context, pool *pgxpool.Pool) (map[string]string, error) {
	rows, err := pool.Query(ctx,
		`SELECT code::text, id::text FROM product_categories WHERE code IN ('PAVER','BULK')`)
	if err != nil {
		return nil, fmt.Errorf("demoseed: reading categories: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var code, id string
		if err := rows.Scan(&code, &id); err != nil {
			return nil, fmt.Errorf("demoseed: scanning category: %w", err)
		}
		out[code] = id
	}
	return out, rows.Err()
}

// resolvePersonas maps each demo PERSONA'S LOGIN EMAIL to the users.id it carries on THIS database.
//
// WHY THIS IS A RESOLVE AND NOT A CONSTANT. 0200 plants the three personas with fixed ids
// (d0200…0001/2/3) and then guards the insert with `ON CONFLICT (email) DO NOTHING`, which means an
// account that already existed under one of those emails KEEPS ITS OWN id and 0200's literal is
// never written. 0200's own section 2 draws the conclusion for its role grants and this file obeys
// it for the same reason: the email is the stable handle, the uuid is not. A demoseed that
// hard-coded d0200…0003 would write a dispatch_drivers.user_id pointing at nobody on exactly the
// deployments where somebody had already made that account by hand, and the FK would refuse it.
//
// AN ABSENT PERSONA IS NOT AN ERROR, and this is the same posture resolveCategories takes above. A
// database migrated only as far as 0199 has none of these accounts; a database that has them all has
// three. Either way the map simply carries what is there, and build.go omits the column for a
// persona it cannot resolve, a driver row with no login is the ordinary case the schema was built
// for (0090:47 "a driver MAY be a staff user … and may not be"), not a degraded one.
func resolvePersonas(ctx context.Context, pool *pgxpool.Pool) (map[string]string, error) {
	rows, err := pool.Query(ctx,
		`SELECT email::text, id::text FROM users WHERE email = ANY($1)`, demoPersonaEmails)
	if err != nil {
		return nil, fmt.Errorf("demoseed: reading demo personas: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var email, id string
		if err := rows.Scan(&email, &id); err != nil {
			return nil, fmt.Errorf("demoseed: scanning persona: %w", err)
		}
		out[email] = id
	}
	return out, rows.Err()
}
