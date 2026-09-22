package reporting

// runner.go, params in, rows and totals out. The three things this file is responsible for:
//
//	1. binding    (params.go does the validation; this file passes the result to pgx as $1..$n)
//	2. ALLOWLIST  the columns Postgres actually returned are compared, in order and by name, against the
//	              template's declared allowlist. A mismatch fails the run.
//	3. totals     computed here, in Go, from the same values the caller sees, never by a second query.
//
// -- WHY THE ALLOWLIST IS CHECKED AGAINST THE DATABASE, NOT AGAINST THE SQL STRING --------------------
// Reading the declared columns and trusting them would make the allowlist documentation. Reading
// pgx's FieldDescriptions makes it a CHECK: if a catalog query is edited to project one more column and
// its Column slice is not updated in the same edit, the very first run of that report fails with
// ErrColumnDrift naming the report, instead of the extra column flowing out to a CSV, a screen and
// whatever the operator pastes it into. This has one cost worth naming: it means every catalog edit
// must touch two places. That is the intended friction.
//
// -- WHY TOTALS ARE COMPUTED IN GO --------------------------------------------------------------------
// A totals row from a second query (or a GROUPING SETS rollup) can disagree with the detail rows under
// concurrent writes: the two statements see two snapshots. Summing the rows the caller was handed makes
// "the total is the sum of what you can see" true by construction, which is the only property a person
// checking a report by hand actually cares about.

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// runTemplate executes ONE compiled template with already-bound args.
//
// pool may be nil (scaffold mode), the caller checks that before reaching here; the guard is repeated
// because a nil pool inside pgx is a panic, not an error, and a report is not worth a crashed process.
func runTemplate(ctx context.Context, pool *pgxpool.Pool, t Template, args []any) ([][]any, error) {
	if pool == nil {
		return nil, ErrNoDatabase
	}
	rows, err := pool.Query(ctx, t.sqlText(), args...)
	if err != nil {
		return nil, fmt.Errorf("run report %s: %w", t.Code, err)
	}
	defer rows.Close()

	if err := checkColumns(rows, t); err != nil {
		return nil, err
	}

	out := make([][]any, 0, 64)
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, fmt.Errorf("run report %s: read row: %w", t.Code, err)
		}
		coerced := make([]any, len(t.Columns))
		for i, c := range t.Columns {
			coerced[i] = coerce(c.Kind, vals[i])
		}
		out = append(out, coerced)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("run report %s: %w", t.Code, err)
	}
	return out, nil
}

// checkColumns is the allowlist enforcement. Same count, same names, same order.
func checkColumns(rows pgx.Rows, t Template) error {
	fds := rows.FieldDescriptions()
	if len(fds) != len(t.Columns) {
		return fmt.Errorf("%w: %s returned %d columns, allowlist declares %d",
			ErrColumnDrift, t.Code, len(fds), len(t.Columns))
	}
	for i, fd := range fds {
		if fd.Name != t.Columns[i].Name {
			return fmt.Errorf("%w: %s column %d is %q, allowlist declares %q",
				ErrColumnDrift, t.Code, i, fd.Name, t.Columns[i].Name)
		}
	}
	return nil
}

// coerce normalises one database value to the Go type the column's kind promises, so a JSON or CSV
// consumer never has to ask what a cell might be. A NULL becomes the kind's zero, reports render a
// blank money cell as 0.00 and a blank count as 0, and pretending otherwise would put "null" in a
// column somebody adds up by hand.
func coerce(kind string, v any) any {
	switch kind {
	case KindCents, KindInt:
		switch n := v.(type) {
		case int64:
			return n
		case int32:
			return int64(n)
		case int:
			return int64(n)
		case float64: // defensive: a catalog query that forgot its ::bigint
			return int64(n)
		default:
			return int64(0)
		}
	case KindQty:
		switch n := v.(type) {
		case float64:
			return n
		case float32:
			return float64(n)
		case int64:
			return float64(n)
		default:
			return float64(0)
		}
	case KindBool:
		b, _ := v.(bool)
		return b
	default: // KindText, KindDate, both arrive as TEXT because the SQL cast them
		s, _ := v.(string)
		return s
	}
}

// totalsFor sums the columns flagged Total. Money and counts accumulate as int64 (never float);
// quantities as float64. Columns not flagged are absent from the map rather than present as zero -
// a totals row with a meaningless "Total: 4" under a bucket name is worse than a blank cell.
func totalsFor(cols []Column, rows [][]any) map[string]any {
	out := make(map[string]any, len(cols))
	for i, c := range cols {
		if !c.Total {
			continue
		}
		switch c.Kind {
		case KindCents, KindInt:
			var sum int64
			for _, r := range rows {
				if n, ok := r[i].(int64); ok {
					sum += n
				}
			}
			out[c.Name] = sum
		case KindQty:
			var sum float64
			for _, r := range rows {
				if f, ok := r[i].(float64); ok {
					sum += f
				}
			}
			out[c.Name] = sum
		}
	}
	return out
}
