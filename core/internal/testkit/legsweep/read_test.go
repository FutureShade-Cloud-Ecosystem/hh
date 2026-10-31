package legsweep

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The sweep's reads never plan parallel workers, so a server with a small /dev/shm cannot
// refuse them (SQLSTATE 53100), and the setting does not leak onto the pooled connection.
func TestSweepReadPlansNoParallelWorkers(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var inRead string
	err = sweepRead(ctx, pool, `SELECT current_setting('max_parallel_workers_per_gather')`, func(rows pgx.Rows) error {
		for rows.Next() {
			if err := rows.Scan(&inRead); err != nil {
				return err
			}
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatalf("sweepRead: %v", err)
	}
	if inRead != "0" {
		t.Fatalf("max_parallel_workers_per_gather inside the sweep's read = %s, want 0", inRead)
	}
	var after string
	if err := pool.QueryRow(ctx, `SELECT current_setting('max_parallel_workers_per_gather')`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after == "0" {
		t.Fatalf("the setting leaked onto the pooled connection")
	}
}
