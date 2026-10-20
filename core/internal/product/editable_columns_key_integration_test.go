//go:build integration

package product

import (
	"context"
	"sort"
	"testing"
)

// TestEditableColumnsNameNoKeyColumn: Repository.Update builds its SET list at run time from
// editableColumns. products carries the sync seq trigger (migration 0422), and a BEFORE UPDATE row
// trigger makes Postgres lock the row FOR UPDATE, not FOR NO KEY UPDATE, whenever the SET list names a
// key column (a column of a unique index), even unchanged, which blocks a referencing insert (a
// barcode, a stock row) until the transaction ends. The catalogue's check (f) in internal/sync scans
// literal SQL and cannot see this builder, so this test reads the builder's allowed list instead and
// fails if it ever holds a key column of the table.
func TestEditableColumnsNameNoKeyColumn(t *testing.T) {
	pool := intPool(t)
	rows, err := pool.Query(context.Background(), `
		SELECT DISTINCT a.attname
		  FROM pg_index i
		  JOIN pg_class c ON c.oid = i.indrelid
		  JOIN pg_namespace n ON n.oid = c.relnamespace AND n.nspname = 'public'
		  JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = ANY (i.indkey)
		 WHERE i.indisunique AND i.indpred IS NULL AND i.indexprs IS NULL AND a.attnum > 0
		   AND c.relname = 'products'`)
	if err != nil {
		t.Fatalf("read the key columns: %v", err)
	}
	defer rows.Close()
	keys := map[string]bool{}
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			t.Fatalf("scan: %v", err)
		}
		keys[col] = true
	}
	if !keys["sku"] || !keys["id"] {
		t.Fatalf("the key columns read as %v; the query lost its grip (sku and id must be keys)", keys)
	}
	var bad []string
	for col := range editableColumns {
		if keys[col] {
			bad = append(bad, col)
		}
	}
	sort.Strings(bad)
	if len(bad) > 0 {
		t.Fatalf("editableColumns holds key columns of products %v: an UPDATE naming one locks the row FOR UPDATE under the sync seq trigger", bad)
	}
}
