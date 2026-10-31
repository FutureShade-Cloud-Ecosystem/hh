//go:build integration

// job_columns_integration_test.go, migration 0469's columns (wave 3 JA-2, item 1).
//
// The table leg pins the migration against the real migrated database: the three columns are
// nullable, name a real job, refuse a job that is not there, and the by job indexes are the
// partial ones the reads need.
//
//	Run: DATABASE_URL='postgres://.../scratch?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -run TestIntegration_JobDocuments_Migration0469 -count=1
package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestIntegration_JobDocuments_Migration0469CarriesTheJobColumns pins the table against the real
// migrated database: the three columns are nullable, name a real job, refuse a job that is not
// there, and the by job indexes are the partial ones the reads need.
func TestIntegration_JobDocuments_Migration0469CarriesTheJobColumns(t *testing.T) {
	pool := freshMigratedDB(t, "s4fin2ja2tbl")
	ctx := context.Background()

	var customerID, branchID, jobID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type, credit_limit_cents)
		VALUES ('JA2-MIG', 'Migration Yard', 'CONTRACTOR', 0) RETURNING id::text`).Scan(&customerID); err != nil {
		t.Fatalf("plant the customer: %v", err)
	}
	// A branch row is its own branch_id, the shape migration 0003 plants for the two yards.
	branchID = uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO locations (id, branch_id, parent_id, loc_type, code, name)
		VALUES ($1, $1, NULL, 'BRANCH', 'JA2MIG', 'JA2 Migration')`, branchID); err != nil {
		t.Fatalf("plant the branch: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO customer_jobs (customer_id, branch_id, job_no, name)
		VALUES ($1, $2, 'J-MIG', 'Driveway') RETURNING id::text`, customerID, branchID).Scan(&jobID); err != nil {
		t.Fatalf("plant the job: %v", err)
	}

	// Every document names the job, and every document may name none.
	for _, doc := range []struct{ table, insert, bare, badJob string }{
		{"quotes",
			`INSERT INTO quotes (quote_no, customer_id, branch_id, job_id)
			 VALUES ('#Q-JA2MIG', $1, $2, $3) RETURNING id::text`,
			`INSERT INTO quotes (quote_no, customer_id, branch_id, job_id)
			 VALUES ('#Q-JA2MIG-BARE', $1, $2, NULL) RETURNING id::text`,
			`INSERT INTO quotes (quote_no, customer_id, branch_id, job_id)
			 VALUES ('#Q-JA2MIG-BAD', $1, $2, gen_random_uuid())`},
		{"orders",
			`INSERT INTO orders (order_no, customer_id, branch_id, job_id)
			 VALUES (next_order_no($2), $1, $2, $3) RETURNING id::text`,
			`INSERT INTO orders (order_no, customer_id, branch_id, job_id)
			 VALUES (next_order_no($2), $1, $2, NULL) RETURNING id::text`,
			`INSERT INTO orders (order_no, customer_id, branch_id, job_id)
			 VALUES (next_order_no($2), $1, $2, gen_random_uuid())`},
		{"invoices",
			`INSERT INTO invoices (invoice_no, customer_id, branch_id, job_id, due_date)
			 VALUES ('#INV-JA2MIG', $1, $2, $3, CURRENT_DATE) RETURNING id::text`,
			`INSERT INTO invoices (invoice_no, customer_id, branch_id, job_id, due_date)
			 VALUES ('#INV-JA2MIG-BARE', $1, $2, NULL, CURRENT_DATE) RETURNING id::text`,
			`INSERT INTO invoices (invoice_no, customer_id, branch_id, job_id, due_date)
			 VALUES ('#INV-JA2MIG-BAD', $1, $2, gen_random_uuid(), CURRENT_DATE)`},
	} {
		var id string
		if err := pool.QueryRow(ctx, doc.insert, customerID, branchID, jobID).Scan(&id); err != nil {
			t.Fatalf("write the %s row on the job: %v", doc.table, err)
		}
		if got := readDocJob(t, pool, doc.table, id); got != jobID {
			t.Fatalf("the %s row does not carry its job: got %q", doc.table, got)
		}
		var bare string
		if err := pool.QueryRow(ctx, doc.bare, customerID, branchID).Scan(&bare); err != nil {
			t.Fatalf("write the %s row with no job: %v", doc.table, err)
		}
		if got := readDocJob(t, pool, doc.table, bare); got != "" {
			t.Fatalf("a %s row with no job carries none: got %q", doc.table, got)
		}

		// A job that is not there is refused: the FK is the last line of defence behind the fence.
		_, err := pool.Exec(ctx, doc.badJob, customerID, branchID)
		if err == nil {
			t.Fatalf("a %s row took a job that does not exist", doc.table)
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
			t.Fatalf("the %s row failed with %v, want a foreign key violation", doc.table, err)
		}
	}

	// The partial indexes the by job reads need.
	for _, idx := range []string{"idx_quotes_job", "idx_orders_job", "idx_invoices_job"} {
		var where string
		if err := pool.QueryRow(ctx, `
			SELECT COALESCE(i.indexdef, '') FROM pg_indexes i WHERE i.indexname = $1`, idx).Scan(&where); err != nil {
			t.Fatalf("the index %s is missing: %v", idx, err)
		}
		if !strings.Contains(where, "job_id IS NOT NULL") {
			t.Fatalf("the index %s is not partial on job_id IS NOT NULL: %s", idx, where)
		}
	}
}

func readDocJob(t *testing.T, pool *pgxpool.Pool, table, id string) string {
	t.Helper()
	var jobID string
	if err := pool.QueryRow(context.Background(),
		fmt.Sprintf(`SELECT COALESCE(job_id::text, '') FROM %s WHERE id = $1::uuid`, table), id).Scan(&jobID); err != nil {
		t.Fatalf("read the %s row: %v", table, err)
	}
	return jobID
}
