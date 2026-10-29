package main

// finance_reset_locks.go carries the reset's lock pre-flight (W3-RESET-LOCKS-r1):
// the answer to a measured fact about the box.
//
// One world restore runs in ONE transaction: the schemas are dropped, the
// world is recreated and the rows are copied, all inside that one
// transaction. A transaction that creates and alters thousands of objects
// holds thousands of locks, and the server's lock table is a fixed number of
// slots: max_locks_per_transaction x (max_connections +
// max_prepared_transactions). On a default server that table is 6,400 slots
// and one restore of the real world measured 15,053 locks, so the restore
// succeeds only by borrowing the server's spare shared memory: it works on an
// idle server and fails with "out of shared memory" on a busy one.
//
// So the reset asks the server what it can hold BEFORE it drops anything:
//
//   - the estimate is the catalog objects a drop of the box's three schemas
//     locks now (every pg_class row in public, ai and ai_pro, their TOAST
//     tables and TOAST indexes, pg_proc, pg_type with row and array types
//     included, pg_constraint, pg_attrdef, pg_trigger, pg_rewrite and
//     pg_policy) plus the dump's own CREATE and ALTER heads outside the
//     world's data weighted by 3 locks per head;
//   - the weights come from measuring pg_locks on the real world inside the
//     reset's single transaction before a ROLLBACK: the drop side held 9,135
//     locks across 1,384 top-level relations, functions and named types (about
//     6.6 per object, accounted for by the TOAST relations, row and array
//     types, constraints, column defaults, triggers, rewrite rules and
//     policies), and the create side held 5,918 locks across 2,065 CREATE and
//     ALTER heads (about 2.9 per head, rounded up to 3);
//   - the capacity is the server's own lock table;
//   - a table that cannot hold TWICE the estimate is refused, in words,
//     naming the estimate, the capacity and the setting to raise.
//
// Twice, because the one transaction holds the world's locks and the box's
// own at the same time: a table that holds only one of them is a table that
// runs out half way through the restore. On the box and in CI (512 locks per
// transaction x 100 connections = 51,200 slots), twice the calibrated estimate
// (about 30,300 slots) fits with room to spare.

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// financeResetBoxSchemas are the box's three schemas: the public one and the
// assistant's two. The pre-flight counts what they hold today, and the reset
// drops what the world owns, which is these three.
var financeResetBoxSchemas = []string{"public", "ai", "ai_pro"}

// financeResetLockSettingFloor is the lock table the finance box runs with:
// 512 locks per transaction, which holds 51,200 slots on a server with 100
// connections, over three times what one restore of the real world needs.
//
// The pre-flight never suggests less than this, so the number it names is
// the number the box's own README and the CI integration job already use.
const financeResetLockSettingFloor = 512

// financeResetLocks is the server's lock table and the reset's estimate of
// what its one transaction needs.
type financeResetLocks struct {
	// Estimate is the locks the one transaction is expected to hold: the
	// box's own objects plus the dump's own object statements.
	Estimate int
	// Capacity is the server's lock table, in slots.
	Capacity int
	// PerTransaction, Connections and Prepared are the server's own three
	// settings, the ones the table is made of.
	PerTransaction int
	Connections    int
	Prepared       int
}

// financeResetLockCapacity answers the server's nominal lock table:
// max_locks_per_transaction x (max_connections + max_prepared_transactions).
func financeResetLockCapacity(perTransaction, connections, prepared int) int {
	return perTransaction * (connections + prepared)
}

// financeResetDumpObjectStatements answers how many object statements the
// dump carries: the CREATE and ALTER heads outside the world's own data.
//
// The world's rows are copied and are never objects, and a comment is never
// a statement, so neither is counted. The lines come from the same reader
// the transaction guard walks, so the pre-flight and the guard read the same
// dump the same way.
func financeResetDumpObjectStatements(dump []byte) int {
	count := 0
	for _, line := range financeResetNonDataLines(dump) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		upper := strings.ToUpper(trimmed)
		if strings.HasPrefix(upper, "CREATE ") || strings.HasPrefix(upper, "ALTER ") {
			count++
		}
	}
	return count
}

// financeResetDumpStatementLocks is the measured lock weight of one CREATE or
// ALTER head in the world's dump: restoring the real world into an empty
// database held 5,918 locks across 2,065 heads (about 2.9 locks per head,
// rounded up to 3).
const financeResetDumpStatementLocks = 3

// financeResetLockEstimate answers the locks the one transaction is expected
// to hold: the catalog objects a drop of the box's own schemas locks now, plus
// the dump's own CREATE and ALTER heads weighted by the locks each head takes.
// The one transaction holds both at once: it drops what the box holds and
// creates what the world carries.
func financeResetLockEstimate(catalogObjects, dumpStatements int) int {
	return catalogObjects + financeResetDumpStatementLocks*dumpStatements
}

// financeResetCatalogObjects answers how many catalog objects a drop of the
// box's three schemas locks right now: every pg_class row in the three
// schemas, their TOAST tables and TOAST indexes, pg_proc, pg_type (row and
// array types included), pg_constraint, pg_attrdef, pg_trigger, pg_rewrite
// and pg_policy.
func financeResetCatalogObjects(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	const q = `
		SELECT
		  (SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		    WHERE n.nspname = ANY($1)),
		  (SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		    WHERE n.nspname = ANY($1) AND c.reltoastrelid <> 0),
		  (SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		    JOIN pg_index i ON i.indrelid = c.reltoastrelid
		    WHERE n.nspname = ANY($1) AND c.reltoastrelid <> 0),
		  (SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		    WHERE n.nspname = ANY($1)),
		  (SELECT count(*) FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
		    WHERE n.nspname = ANY($1)),
		  (SELECT count(*) FROM pg_constraint con JOIN pg_namespace n ON n.oid = con.connamespace
		    WHERE n.nspname = ANY($1)),
		  (SELECT count(*) FROM pg_attrdef ad JOIN pg_class c ON c.oid = ad.adrelid
		    JOIN pg_namespace n ON n.oid = c.relnamespace
		    WHERE n.nspname = ANY($1)),
		  (SELECT count(*) FROM pg_trigger tg JOIN pg_class c ON c.oid = tg.tgrelid
		    JOIN pg_namespace n ON n.oid = c.relnamespace
		    WHERE n.nspname = ANY($1)),
		  (SELECT count(*) FROM pg_rewrite rw JOIN pg_class c ON c.oid = rw.ev_class
		    JOIN pg_namespace n ON n.oid = c.relnamespace
		    WHERE n.nspname = ANY($1)),
		  (SELECT count(*) FROM pg_policy pol JOIN pg_class c ON c.oid = pol.polrelid
		    JOIN pg_namespace n ON n.oid = c.relnamespace
		    WHERE n.nspname = ANY($1))`
	var relations, toastTables, toastIndexes, functions, types, constraints, defaults, triggers, rewrites, policies int
	if err := pool.QueryRow(ctx, q, financeResetBoxSchemas).Scan(
		&relations, &toastTables, &toastIndexes, &functions, &types,
		&constraints, &defaults, &triggers, &rewrites, &policies,
	); err != nil {
		return 0, fmt.Errorf("finance-reset: the box's own catalog could not be read: %w", err)
	}
	return relations + toastTables + toastIndexes + functions + types + constraints + defaults + triggers + rewrites + policies, nil
}

// financeResetLocksOf answers the server's lock table and the reset's own
// estimate of what its one transaction needs.
func financeResetLocksOf(ctx context.Context, pool *pgxpool.Pool, dump []byte) (financeResetLocks, error) {
	var locks financeResetLocks
	settings := []struct {
		name string
		into *int
	}{
		{"max_locks_per_transaction", &locks.PerTransaction},
		{"max_connections", &locks.Connections},
		{"max_prepared_transactions", &locks.Prepared},
	}
	for _, setting := range settings {
		// current_setting and not SHOW: SHOW answers in text, and the
		// numbers are read as numbers.
		q := "SELECT current_setting('" + setting.name + "')::int"
		if err := pool.QueryRow(ctx, q).Scan(setting.into); err != nil {
			return locks, fmt.Errorf("finance-reset: the server's %s could not be read: %w", setting.name, err)
		}
	}
	catalog, err := financeResetCatalogObjects(ctx, pool)
	if err != nil {
		return locks, err
	}
	locks.Capacity = financeResetLockCapacity(locks.PerTransaction, locks.Connections, locks.Prepared)
	locks.Estimate = financeResetLockEstimate(catalog, financeResetDumpObjectStatements(dump))
	return locks, nil
}

// Fits answers whether the server's lock table can hold the one transaction:
// the table has to hold twice the estimate, because the transaction holds
// the world's locks and the box's own at the same time.
func (l financeResetLocks) Fits() bool {
	return l.Capacity >= 2*l.Estimate
}

// Refusal answers the refusal a server that cannot hold the one transaction
// gets: the estimate, the capacity, the rule and the setting to raise.
//
// It is a refusal before anything is dropped, so the box keeps the tables
// and the rows it had, and the operator knows which setting to raise before
// the next attempt rather than after it.
func (l financeResetLocks) Refusal() error {
	return fmt.Errorf("finance-reset: the server's lock table cannot hold this reset: the one transaction needs about %d locks and the server's table holds %d (max_locks_per_transaction %d x (max_connections %d + max_prepared_transactions %d)), which is below twice the estimate: the reset refuses before it drops anything. Start the server with -c max_locks_per_transaction=%d and try again",
		l.Estimate, l.Capacity, l.PerTransaction, l.Connections, l.Prepared, l.SuggestedSetting())
}

// SuggestedSetting answers the per transaction setting that holds twice the
// estimate: the smallest one the server's connection count needs, rounded up
// to a power of two, and never below the box's own floor.
func (l financeResetLocks) SuggestedSetting() int {
	setting := financeResetLockSettingFloor
	slots := l.Connections + l.Prepared
	if slots > 0 {
		need := 2 * l.Estimate / slots
		if 2*l.Estimate%slots != 0 {
			need++
		}
		for setting < need {
			setting *= 2
		}
	}
	return setting
}
