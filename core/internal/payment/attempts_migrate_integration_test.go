//go:build integration

// attempts_migrate_integration_test.go: migration 0361 on a database built from zero. The leg makes
// its own throwaway database on the DATABASE_URL server, applies every migration in order, and holds
// the new table, its constraints, its indexes and the sweep's schedule row to the design (section 5.1).
package payment

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

const migrate0361File = "0361_card_charge_attempts.sql"

func swapDBName(dsn, name string) string {
	slash := strings.LastIndex(dsn, "/")
	if slash < 0 {
		return dsn
	}
	rest := dsn[slash+1:]
	if q := strings.Index(rest, "?"); q >= 0 {
		return dsn[:slash+1] + name + rest[q:]
	}
	return dsn[:slash+1] + name
}

func migratedThrowaway(t *testing.T, name string) (*pgxpool.Pool, string) {
	t.Helper()
	base, ok := os.LookupEnv("DATABASE_URL")
	if !ok || base == "" {
		t.Skip("DATABASE_URL is unset, the migrate leg needs a Postgres to build a database on")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, swapDBName(base, "postgres"))
	if err != nil {
		t.Fatalf("admin connect: %v", err)
	}
	t.Cleanup(admin.Close)
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
		t.Fatalf("drop %s: %v", name, err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	pool, err := pgxpool.New(ctx, swapDBName(base, name))
	if err != nil {
		t.Fatalf("connect %s: %v", name, err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})
	files, err := filepath.Glob(filepath.Join("..", "..", "db", "migrations", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no migrations found (err=%v)", err)
	}
	sort.Strings(files)
	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			t.Fatalf("apply %s: %v", filepath.Base(f), err)
		}
	}
	return pool, filepath.Join("..", "..", "db", "migrations", migrate0361File)
}

func TestMigration0361_AttemptTableAndSchedule(t *testing.T) {
	ctx := context.Background()
	pool, file := migratedThrowaway(t, "payment_a1_migrate_it")

	var reg *string
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.card_charge_attempts')::text`).Scan(&reg); err != nil || reg == nil {
		t.Fatalf("card_charge_attempts is absent after every migration (err=%v)", err)
	}

	for _, col := range []string{"id", "leg_key", "sale_key", "leg_seq", "amount_cents", "branch_id", "till_session_id",
		"customer_id", "card_type", "processor", "status", "processor_ref", "reversal_kind", "reversal_ref",
		"resp_code", "last_error", "payment_id", "resolution", "resolution_note", "resolved_by", "created_by",
		"created_at", "updated_at"} {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
		    WHERE table_name = 'card_charge_attempts' AND column_name = $1`, col).Scan(&n); err != nil || n != 1 {
			t.Errorf("column %s missing (n=%d err=%v)", col, n, err)
		}
	}

	for _, con := range []string{"cca_bound_has_payment", "cca_money_has_ref", "cca_voided_says_how", "cca_resolved_says_how",
		"cca_payment_only_bound", "cca_reversal_only_on_voided", "cca_resolution_only_on_resolved"} {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_constraint
		    WHERE conrelid = 'card_charge_attempts'::regclass AND conname = $1 AND contype = 'c'`, con).Scan(&n); err != nil || n != 1 {
			t.Errorf("check constraint %s missing (n=%d err=%v)", con, n, err)
		}
	}
	var uniqueOnLegKey int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_index i
	    WHERE i.indrelid = 'card_charge_attempts'::regclass AND i.indisunique AND NOT i.indisprimary
	      AND pg_get_indexdef(i.indexrelid) LIKE '%(leg_key)%'`).Scan(&uniqueOnLegKey); err != nil || uniqueOnLegKey != 1 {
		t.Errorf("leg_key is not unique (n=%d err=%v)", uniqueOnLegKey, err)
	}
	var statusDef string
	if err := pool.QueryRow(ctx, `SELECT string_agg(pg_get_constraintdef(oid), ' ') FROM pg_constraint
	    WHERE conrelid = 'card_charge_attempts'::regclass AND contype = 'c' AND pg_get_constraintdef(oid) LIKE '%CHARGING%'`).Scan(&statusDef); err != nil {
		t.Fatalf("status check: %v", err)
	}
	for _, s := range []string{AttemptCharging, AttemptCaptured, AttemptDeclined, AttemptUnknown, AttemptBound,
		AttemptVoiding, AttemptVoided, AttemptVoidUnknown, AttemptVoidDeclined, AttemptResolved} {
		if !strings.Contains(statusDef, "'"+s+"'") {
			t.Errorf("status check does not admit %s: %s", s, statusDef)
		}
	}

	for _, ix := range []string{"idx_cca_sweep", "idx_cca_till_guard", "idx_cca_processor_ref", "idx_cca_sale_key"} {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE tablename = 'card_charge_attempts' AND indexname = $1`, ix).Scan(&n); err != nil || n != 1 {
			t.Errorf("index %s missing (n=%d err=%v)", ix, n, err)
		}
	}

	var kind string
	var secs int
	var enabled bool
	if err := pool.QueryRow(ctx, `SELECT kind, interval_secs, enabled FROM job_schedules WHERE name = 'payment.card_attempt_sweep'`).
		Scan(&kind, &secs, &enabled); err != nil {
		t.Fatalf("schedule row for payment.card_attempt_sweep: %v", err)
	}
	if kind != "INTERVAL" || secs != 60 || !enabled {
		t.Errorf("schedule = %s every %ds enabled=%v, want INTERVAL every 60s enabled", kind, secs, enabled)
	}

	// Idempotent: a second application changes nothing and does not fail.
	body, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	if _, err := pool.Exec(ctx, string(body)); err != nil {
		t.Fatalf("0361 applied twice: %v", err)
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM job_schedules WHERE name = 'payment.card_attempt_sweep'`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("schedule rows after a second application = %d (err=%v), want 1", rows, err)
	}
}
