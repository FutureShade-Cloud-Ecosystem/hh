// apply.go, the only writer in this command, and the reason the staging promise holds.
//
// THE APPLIER CAN ONLY DO ONE THING. It emits INSERT … ON CONFLICT … DO NOTHING. There is no
// UPDATE path, no DELETE path, no TRUNCATE path and no "refresh" path, and that is a structural
// guarantee rather than a discipline: to destroy a row on a staging database somebody would have to
// add a new statement kind to this file, which is a diff a reviewer cannot miss.
//
// ONE TRANSACTION. Every row goes in one transaction. A half-applied demo world is worse than no
// demo world, the tiles would show a distribution that does not correspond to any orders, so the
// applier is all-or-nothing. On a unique-constraint collision it does NOT swallow the error: an
// unexpected collision (someone has already taken a DEMO- number by hand) means the plan and the
// database disagree about what the demo world is, and the honest response is to write nothing and
// say so.
// demoseed_apply carries the pin's cmd/demoseed/apply.go (the fold:
// one-shot subcommand of cmd/core, plan and apply modes preserved).
package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// row is one INSERT. conflict names the arbiter, a column list the table has a unique index on -
// so the DO NOTHING lands on the RIGHT key: for tables with a natural key (product_uoms,
// stock_balances, load_scan_checkpoints) that key is the arbiter, not the surrogate id, because the
// row a human already created under that natural key is the one that must survive.
type row struct {
	table    string
	cols     []string
	vals     []any
	conflict string // e.g. "(id)", the ON CONFLICT arbiter, always DO NOTHING
}

// insert is the row constructor. Keeping it a function rather than a literal keeps the call sites
// in plan.go readable as data.
func insert(table, conflict string, pairs ...any) row {
	if len(pairs)%2 != 0 {
		panic("demoseed: insert() takes column/value pairs")
	}
	r := row{table: table, conflict: conflict}
	for i := 0; i < len(pairs); i += 2 {
		name, ok := pairs[i].(string)
		if !ok {
			panic("demoseed: insert() column name must be a string")
		}
		r.cols = append(r.cols, name)
		r.vals = append(r.vals, pairs[i+1])
	}
	return r
}

// auditAnchor is a column value that is the seq of an audit_log row the plan wrote earlier in the
// same transaction, named by that row's id. audit_log.seq is GENERATED ALWAYS, so no plan can know it;
// an acceptance record anchors to its QUOTE_ACCEPTED row by it (quote_acceptances.audit_seq). It
// renders as a read-only lookup around ONE bound parameter, so the applier still writes INSERTs only.
type auditAnchor string

// sql renders the statement. Column names come from this file's own call sites, never from input,
// so they are interpolated; every VALUE is a bound parameter.
func (r row) sql() string {
	ph := make([]string, len(r.cols))
	for i := range r.cols {
		ph[i] = fmt.Sprintf("$%d", i+1)
		if _, ok := r.vals[i].(auditAnchor); ok {
			ph[i] = fmt.Sprintf("(SELECT seq FROM audit_log WHERE id = $%d::uuid)", i+1)
		}
	}
	return fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ON CONFLICT %s DO NOTHING",
		r.table, strings.Join(r.cols, ", "), strings.Join(ph, ", "), r.conflict)
}

// args is the bound parameter list, in column order.
func (r row) args() []any {
	out := make([]any, len(r.vals))
	for i, v := range r.vals {
		if a, ok := v.(auditAnchor); ok {
			v = string(a)
		}
		out[i] = v
	}
	return out
}

// applyResult is the arithmetic a run reports: how many rows the plan proposed and how many the
// database actually took. On a second run against the same clock, created is 0 and skipped is the
// whole plan, that IS the idempotency proof, stated in the command's own output.
type applyResult struct {
	Proposed int
	Created  int
	Skipped  int
	ByTable  map[string]int // created, per table
}

// txExec is the one database capability the applier needs: execute a statement. Narrowing it to
// this is not ceremony, it is the guarantee restated as a type. A writer handed only Exec over an
// INSERT-only statement builder has no reachable path to a DELETE, a TRUNCATE or an UPDATE.
type txExec interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// rowQuery is the one READ capability the plan's reporting needs: whether a row the repair pass
// would move still sits where the pass moves it from, so the plan says the repair only when the
// apply would actually make it. A pool and a transaction both satisfy it; nothing in this command
// gains any wider read than the one row count this interface names.
type rowQuery interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// apply writes the plan inside one transaction.
func apply(ctx context.Context, tx txExec, rows []row) (applyResult, error) {
	res := applyResult{Proposed: len(rows), ByTable: map[string]int{}}
	for i, r := range rows {
		tag, err := tx.Exec(ctx, r.sql(), r.args()...)
		if err != nil {
			return res, fmt.Errorf("demoseed: row %d into %s: %w", i, r.table, err)
		}
		if n := int(tag.RowsAffected()); n > 0 {
			res.Created += n
			res.ByTable[r.table] += n
		} else {
			res.Skipped++
		}
	}
	return res, nil
}
