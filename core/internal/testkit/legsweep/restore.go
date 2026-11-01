package legsweep

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RestoreSeededRows (row 0.54) captures each named table whole when a leg's pool opens and writes every row back
// at the leg's end: for seeded rows a leg rewrites in place (warranty's policy and vendor profiles, jobs'
// schedules, which the due-job loop claims). Register's sweep fails a leg that leaves a seeded row changed, and it
// deletes rows a leg added; this restores the rows a leg changed. The restore is an upsert by primary key of every
// column; a refused statement fails the leg, naming the table. Register it after Register, so it runs first.
func RestoreSeededRows(t *testing.T, pool *pgxpool.Pool, tables ...string) {
	t.Helper()
	ctx := context.Background()
	images := map[string][]string{}
	cols := map[string][]string{}
	for _, tb := range tables {
		crows, err := pool.Query(ctx, `SELECT column_name FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = $1 ORDER BY ordinal_position`, tb)
		if err != nil {
			t.Fatalf("restore %s: read columns: %v", tb, err)
		}
		for crows.Next() {
			var c string
			if err := crows.Scan(&c); err != nil {
				crows.Close()
				t.Fatalf("restore %s: scan column: %v", tb, err)
			}
			cols[tb] = append(cols[tb], c)
		}
		crows.Close()
		if err := crows.Err(); err != nil {
			t.Fatalf("restore %s: columns: %v", tb, err)
		}
		irows, err := pool.Query(ctx, fmt.Sprintf(`SELECT row_to_json(x)::text FROM %s x`, tb))
		if err != nil {
			t.Fatalf("restore %s: capture: %v", tb, err)
		}
		for irows.Next() {
			var j string
			if err := irows.Scan(&j); err != nil {
				irows.Close()
				t.Fatalf("restore %s: scan row: %v", tb, err)
			}
			images[tb] = append(images[tb], j)
		}
		irows.Close()
		if err := irows.Err(); err != nil {
			t.Fatalf("restore %s: rows: %v", tb, err)
		}
	}
	t.Cleanup(func() {
		for _, tb := range tables {
			var set []string
			for _, c := range cols[tb] {
				if c != "id" {
					set = append(set, fmt.Sprintf(`"%s" = EXCLUDED."%s"`, c, c))
				}
			}
			stmt := fmt.Sprintf(`INSERT INTO %s SELECT * FROM json_populate_record(NULL::%s, $1::json)
				ON CONFLICT (id) DO UPDATE SET %s`, tb, tb, strings.Join(set, ", "))
			for _, j := range images[tb] {
				if _, err := pool.Exec(context.Background(), stmt, j); err != nil {
					t.Errorf("restore %s: write the seeded row back: %v", tb, err)
				}
			}
		}
	})
}
