//go:build integration

package main

// finance_reset_locks_integration_test.go proves the reset's lock pre-flight
// against a real Postgres (W3-RESET-LOCKS-r1).
//
// The fact behind it: one world restore runs in ONE transaction and takes
// thousands of locks, and the server's lock table is a fixed number of slots.
// A server whose table cannot hold that one transaction has to be refused
// BEFORE anything is dropped, with the estimate, the capacity and the setting
// to raise in words.
//
// The leg reads the server's own lock table and answers what the reset owes
// it, so the same leg proves both answers:
//
//   - a server whose table cannot hold twice the estimate refuses, in words,
//     and the box's own table and row are still there afterwards;
//   - a server whose table can hold it (the box's own setting, 512 locks per
//     transaction) passes, and the counts line carries the estimate and the
//     capacity.
//
// The numbers the leg compares against are its own: it reads the catalog with
// its own query and counts the dump's statements with its own reader, so a
// wrong estimate in the reset does not agree with the leg.

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/db"
)

func TestIntegration_FinanceResetLockPreflight(t *testing.T) {
	ctx := context.Background()
	dsn := financeResetFreshDB(t, "demoseed_it_finlocks", "")
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting to the box: %v", err)
	}
	defer pool.Close()
	financeResetMarkBox(t, ctx, pool)

	// The world: the box's own database, dumped by the admin connection (the
	// box's own role does not hold the assist tables), before the marker
	// table is made, so the restore that passes puts back the world without
	// it.
	worldDSN := swapDatabase(financeResetAdminDSN(t), "demoseed_it_finlocks")
	dump := financeResetDump(t, worldDSN)
	digest := financeResetDigest(dump)
	worldFile := filepath.Join(t.TempDir(), "world.sql.gz")
	if err := writeWorldGzip(t, worldFile, dump); err != nil {
		t.Fatalf("the world file: %v", err)
	}

	// The box's own table and row: a reset that refused before it dropped
	// anything leaves them in place.
	if _, err := pool.Exec(ctx, `CREATE TABLE box_marker (id int PRIMARY KEY, note text NOT NULL)`); err != nil {
		t.Fatalf("the box's own table: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO box_marker (id, note) VALUES (1, 'the box was here')`); err != nil {
		t.Fatalf("the box's own row: %v", err)
	}

	// The leg's own numbers, and the locks the reset's one transaction
	// actually holds in pg_locks before a ROLLBACK.
	catalog := financeLocksTestCatalog(t, ctx, pool)
	statements := financeLocksTestStatements(dump)
	estimate := catalog + statements
	perTransaction, connections, prepared := financeLocksTestServer(t, ctx, pool)
	capacity := perTransaction * (connections + prepared)

	gotCatalog, err := financeResetCatalogObjects(ctx, pool)
	if err != nil {
		t.Fatalf("financeResetCatalogObjects: %v", err)
	}
	gotStatements := financeResetDumpObjectStatements(dump)
	gotEstimate := financeResetLockEstimate(gotCatalog, gotStatements)
	measured := financeLocksTestMeasured(t, worldDSN, dump)

	t.Logf("the server's lock table: max_locks_per_transaction %d x (max_connections %d + max_prepared_transactions %d) = %d slots; the estimate is %d (%d objects in the box's schemas, %d statements in the dump); one transaction held %d locks in pg_locks",
		perTransaction, connections, prepared, capacity, gotEstimate, gotCatalog, gotStatements, measured)

	if gotEstimate < measured {
		t.Fatalf("financeResetLockEstimate answers %d (%d catalog objects, %d dump statements), less than the %d locks the one transaction held in pg_locks",
			gotEstimate, gotCatalog, gotStatements, measured)
	}

	env := finEnvFor(dsn, nil, map[string]string{
		envFinanceWorldFile: worldFile,
		envFinanceWorldSHA:  digest,
		envFinanceResetDir:  t.TempDir(),
	})
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	resetErr := financeResetRole([]string{"--confirm", finOrgSlug}, out, errOut, env)
	line := strings.TrimSpace(out.String())

	// The reset's own answer first: the table cannot hold twice the
	// estimate, so the reset refuses before it drops anything.
	if capacity < 2*estimate {
		if resetErr == nil {
			t.Fatalf("the reset ran on a table of %d slots against an estimate of %d: %q", capacity, estimate, line)
		}
		msg := resetErr.Error()
		t.Logf("the reset refused in words: %s", msg)
		if !strings.Contains(msg, strconv.Itoa(estimate)) {
			t.Fatalf("the refusal does not name the estimate %d: %q", estimate, msg)
		}
		if !strings.Contains(msg, strconv.Itoa(capacity)) {
			t.Fatalf("the refusal does not name the capacity %d: %q", capacity, msg)
		}
		if !strings.Contains(msg, "-c max_locks_per_transaction=") {
			t.Fatalf("the refusal does not name the setting to raise: %q", msg)
		}
		// The box's own table and its row are still there.
		var markers int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM box_marker`).Scan(&markers); err != nil {
			t.Fatalf("reading the box's own table: %v", err)
		}
		if markers != 1 {
			t.Fatalf("the refused reset left the box's own table with %d rows, want the one it had", markers)
		}
	} else {
		// The table can hold twice the estimate: the reset runs.
		if resetErr != nil {
			t.Fatalf("the reset refused on a table of %d slots against an estimate of %d: %v", capacity, estimate, resetErr)
		}
		// The world came back: the marker table was not in the dump, so the
		// restore dropped it.
		var markers int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM box_marker`).Scan(&markers); err == nil && markers != 0 {
			t.Fatalf("the restored world still carries the box's marker table: %d rows", markers)
		}
	}

	// The counts line carries the estimate and the capacity, refused or not.
	for _, field := range []string{"locks_estimated=", "lock_capacity="} {
		if !strings.Contains(line, field) {
			t.Fatalf("the counts line does not carry %s: %q", field, line)
		}
	}
	if got := financeLocksTestField(t, line, "locks_estimated="); got != estimate {
		t.Fatalf("the counts line estimates %d locks, want the leg's own %d", got, estimate)
	}
	if got := financeLocksTestField(t, line, "lock_capacity="); got != capacity {
		t.Fatalf("the counts line reads a capacity of %d, want the leg's own %d", got, capacity)
	}
}

// financeLocksTestMeasured runs the reset's own shape in one transaction
// (drop public, ai and ai_pro CASCADE, create public, then restore the dump),
// counts this backend's rows in pg_locks before a ROLLBACK, and leaves the
// box's database untouched.
func financeLocksTestMeasured(t *testing.T, dsn string, dump []byte) int {
	t.Helper()
	servicePath, cleanup, err := libpqServiceFile(dsn)
	if err != nil {
		t.Fatalf("the libpq service file: %v", err)
	}
	defer cleanup()
	var script bytes.Buffer
	script.WriteString("BEGIN;\n")
	script.WriteString("DROP SCHEMA IF EXISTS public, ai, ai_pro CASCADE;\n")
	script.WriteString("CREATE SCHEMA public;\n")
	script.Write(dump)
	script.WriteString("\nSELECT 'locks_held=' || count(*) FROM pg_locks WHERE pid = pg_backend_pid();\n")
	script.WriteString("ROLLBACK;\n")
	scriptPath := filepath.Join(t.TempDir(), "measure-locks.sql")
	if err := os.WriteFile(scriptPath, script.Bytes(), 0600); err != nil {
		t.Fatalf("writing the lock measurement script: %v", err)
	}
	cmd := exec.Command("psql", "-v", "ON_ERROR_STOP=1", "-tAq", "-f", scriptPath)
	cmd.Env = append(os.Environ(), "PGSERVICEFILE="+servicePath, "PGSERVICE="+pgServiceName)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("measuring the reset's locks: %v (%s)", err, stderr.String())
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(line, "locks_held="); ok {
			n, err := strconv.Atoi(after)
			if err != nil {
				t.Fatalf("the measured lock count is not a number: %q", line)
			}
			return n
		}
	}
	t.Fatalf("the lock measurement script printed no locks_held line: %q", string(out))
	return 0
}

// financeLocksTestCatalog answers the objects the box's three schemas hold,
// with the leg's own query: the relations, the functions and the named types
// the box owns today.
func financeLocksTestCatalog(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int {
	t.Helper()
	var relations, functions, types int
	err := pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		    WHERE n.nspname = ANY(ARRAY['public','ai','ai_pro'])
		      AND c.relkind IN ('r','p','v','m','S','f','i')),
		  (SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		    WHERE n.nspname = ANY(ARRAY['public','ai','ai_pro'])),
		  (SELECT count(*) FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
		    WHERE n.nspname = ANY(ARRAY['public','ai','ai_pro'])
		      AND (t.typtype IN ('e','d','r') OR (t.typtype = 'c' AND t.typrelid = 0)))`,
	).Scan(&relations, &functions, &types)
	if err != nil {
		t.Fatalf("the box's own catalog: %v", err)
	}
	return relations + functions + types
}

// financeLocksTestServer answers the server's own lock table: the three
// settings the table is made of, read with the leg's own queries.
func financeLocksTestServer(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (perTransaction, connections, prepared int) {
	t.Helper()
	if err := pool.QueryRow(ctx, "SELECT current_setting('max_locks_per_transaction')::int").Scan(&perTransaction); err != nil {
		t.Fatalf("the server's max_locks_per_transaction: %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT current_setting('max_connections')::int").Scan(&connections); err != nil {
		t.Fatalf("the server's max_connections: %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT current_setting('max_prepared_transactions')::int").Scan(&prepared); err != nil {
		t.Fatalf("the server's max_prepared_transactions: %v", err)
	}
	return perTransaction, connections, prepared
}

// financeLocksTestStatements counts the dump's object statements with the
// leg's own reader: the CREATE and ALTER heads outside the world's data.
func financeLocksTestStatements(dump []byte) int {
	count := 0
	inCopy := false
	for _, line := range strings.Split(string(dump), "\n") {
		trimmed := strings.TrimSpace(line)
		if inCopy {
			if trimmed == `\.` {
				inCopy = false
			}
			continue
		}
		upper := strings.ToUpper(trimmed)
		if strings.HasPrefix(upper, "COPY ") && strings.HasSuffix(upper, "FROM STDIN;") {
			inCopy = true
			continue
		}
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		if strings.HasPrefix(upper, "CREATE ") || strings.HasPrefix(upper, "ALTER ") {
			count++
		}
	}
	return count
}

// financeLocksTestField answers the number a counts line carries after a
// field's name.
func financeLocksTestField(t *testing.T, line, field string) int {
	t.Helper()
	at := strings.Index(line, field)
	if at < 0 {
		t.Fatalf("the counts line does not carry %s: %q", field, line)
	}
	rest := line[at+len(field):]
	end := strings.IndexAny(rest, " \t\n")
	if end < 0 {
		end = len(rest)
	}
	n, err := strconv.Atoi(rest[:end])
	if err != nil {
		t.Fatalf("the counts line's %s is not a number: %q", field, line)
	}
	return n
}
