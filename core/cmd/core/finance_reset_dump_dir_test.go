package main

// finance_reset_dump_dir_test.go pins the one thing about the reset's dump
// file that has to hold on every box: it is written on disk, under a
// directory the reset is given, and never on the shared memory tmpfs.
//
// The shared memory tmpfs is sized by the platform, not by the box: Docker's
// default is 64 MB and the world's decoded dump is larger than that, so a
// reset that wrote its dump there refused on a box that was big enough to
// serve the world.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFinanceResetDumpDirectory(t *testing.T) {
	// The default is on disk and never the shared memory tmpfs.
	def := financeResetDumpDirectory(func(string) string { return "" })
	if def != financeResetDumpDir {
		t.Fatalf("the default dump directory is %q, want %q", def, financeResetDumpDir)
	}
	if strings.HasPrefix(def, "/dev/shm") {
		t.Fatalf("the default dump directory is on the shared memory tmpfs: %q", def)
	}
	if !filepath.IsAbs(def) {
		t.Fatalf("the default dump directory is not an absolute path: %q", def)
	}

	// The environment names the directory when it names one.
	named := financeResetDumpDirectory(func(k string) string {
		if k == envFinanceResetDir {
			return "/srv/box/reset"
		}
		return ""
	})
	if named != "/srv/box/reset" {
		t.Fatalf("the named dump directory is %q, want the one the environment named", named)
	}

	// A blank name is the default, not the current directory: a relative
	// path would land the world's data wherever the boot happened to be.
	blank := financeResetDumpDirectory(func(k string) string {
		if k == envFinanceResetDir {
			return "   "
		}
		return ""
	})
	if blank != financeResetDumpDir {
		t.Fatalf("a blank dump directory is %q, want the default %q", blank, financeResetDumpDir)
	}
}

func TestFinanceResetWriteDump(t *testing.T) {
	dir := t.TempDir()
	dump := []byte("the world's dump bytes")

	path, cleanup, err := financeResetWriteDump(dir, dump)
	if err != nil {
		t.Fatalf("writing the dump: %v", err)
	}
	defer cleanup()
	if filepath.Dir(path) != dir {
		t.Fatalf("the dump file is %q, want it under %q", path, dir)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the dump file: %v", err)
	}
	if string(got) != string(dump) {
		t.Fatalf("the dump file holds %q, want the dump", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the dump file's mode: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Fatalf("the dump file's mode is %o, want 600", mode)
	}

	// A directory that cannot hold the file is a refusal, and the file is
	// not left behind.
	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0600); err != nil {
		t.Fatalf("the blocked parent: %v", err)
	}
	if _, _, err := financeResetWriteDump(filepath.Join(blocked, "reset"), dump); err == nil {
		t.Fatalf("the dump was written into a directory that cannot be made")
	}

	// The remover takes the file away.
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the dump file is still there after the remover ran: %v", err)
	}
}

func TestFinanceResetTransactionSafe(t *testing.T) {
	// The world's own statements are all allowed inside the reset's
	// transaction.
	safe := `CREATE TABLE public.customers (id integer NOT NULL);
CREATE INDEX customers_id ON public.customers (id);
CREATE VIEW ai.orders AS SELECT 1;
CREATE FUNCTION ai.claims() RETURNS jsonb AS $body$
BEGIN
      RETURN '{}'::jsonb;
END;
$body$ LANGUAGE plpgsql;
CREATE POLICY assist_staff_orders ON public.orders TO assist_view_owner_finworld USING (true);
ALTER TABLE public.orders ADD CONSTRAINT orders_pkey PRIMARY KEY (id);
GRANT SELECT ON public.orders TO assist_view_owner_finworld;
COPY public.customers (id) FROM stdin;
1
\.
ALTER DEFAULT PRIVILEGES FOR ROLE assist_view_owner_finworld REVOKE ALL ON FUNCTIONS FROM PUBLIC;
`
	if err := financeResetTransactionSafe([]byte(safe)); err != nil {
		t.Fatalf("the world's own statements were refused: %v", err)
	}

	// The statements that may not run inside a transaction are refused
	// before anything is dropped.
	for _, statement := range []string{
		"CREATE DATABASE other;",
		"CREATE INDEX CONCURRENTLY customers_id ON public.customers (id);",
		"VACUUM public.customers;",
		"CLUSTER public.customers USING customers_pkey;",
		"ALTER SYSTEM SET work_mem = '8MB';",
		"CREATE TABLESPACE other LOCATION '/tmp';",
		"SET TRANSACTION ISOLATION LEVEL SERIALIZABLE;",
	} {
		if err := financeResetTransactionSafe([]byte(statement)); err == nil {
			t.Fatalf("the statement %q was accepted inside the reset's transaction", statement)
		}
	}
}

// TestFinanceResetCopyStartIsRecognised is item 5: the copy block's
// detection compared an upper cased line against a mixed case spelling, so
// it never matched and the world's own data was read as script.
func TestFinanceResetCopyStartIsRecognised(t *testing.T) {
	for _, line := range []string{
		"COPY public.customers (id) FROM stdin;",
		"COPY public.customers (id) FROM STDIN;",
		"copy public.customers (id) from stdin;",
	} {
		if !financeResetCopyStart(strings.TrimSpace(line)) {
			t.Fatalf("the copy block %q was not recognised", line)
		}
	}
	if financeResetCopyStart("COPY public.customers (id) FROM 'other';") {
		t.Fatalf("a line that does not open a copy block was taken for one")
	}
}

// TestFinanceResetNonDataLinesLeavesTheWorldsDataAlone is item 5: the
// world's own data is never read as script, so a data row that opens with
// the name of a statement that may not run inside a transaction may not make
// the reset refuse.
func TestFinanceResetNonDataLinesLeavesTheWorldsDataAlone(t *testing.T) {
	dump := []byte("COPY public.customers (id, note) FROM stdin;\n1\tVACUUM is only a word here\n\\.\nSELECT 1;\n")
	if err := financeResetTransactionSafe(dump); err != nil {
		t.Fatalf("the world's own data was read as script: %v", err)
	}
	for _, line := range financeResetNonDataLines(dump) {
		if strings.Contains(line, "is only a word here") {
			t.Fatalf("the world's own data was read as script:\n%s", line)
		}
	}
}

// TestFinanceResetGuardReadsWholeWords is main's red on 5696d1eb: a newer
// pg_dump writes "SET transaction_timeout = 0;", a harmless session setting,
// and the guard's prefix match read it as SET TRANSACTION. The guard refuses
// what truly breaks the one transaction and lets session settings through.
func TestFinanceResetGuardReadsWholeWords(t *testing.T) {
	for _, statement := range []string{
		"SET transaction_timeout = 0;",
		"SET statement_timeout = 0;",
		"SET idle_in_transaction_session_timeout = 0;",
		"CREATE FUNCTION public.f() RETURNS void LANGUAGE plpgsql AS $$\nBEGIN\n  PERFORM 1;\nEND;\n$$;",
	} {
		if err := financeResetTransactionSafe([]byte(statement)); err != nil {
			t.Fatalf("the harmless statement %q was refused: %v", statement, err)
		}
	}
	for _, statement := range []string{
		"SET TRANSACTION ISOLATION LEVEL SERIALIZABLE;",
		"SET TRANSACTION READ WRITE;",
		"COMMIT;",
		"ROLLBACK;",
		"\\connect other",
	} {
		if err := financeResetTransactionSafe([]byte(statement)); err == nil {
			t.Fatalf("the statement %q was accepted inside the reset's transaction", statement)
		}
	}
}

// TestFinanceResetGuardReadsStatements is W3-RESET-GUARD2-r1 item 1 (S5's
// probe table on PR 699): the guard joins each statement up to its semicolon,
// collapses whitespace, upper cases it, tracks dollar quoting ($$ and $tag$),
// refuses transaction control in every form and psql meta commands that leave
// or read elsewhere, and accepts harmless pg_dump 16 lines.
func TestFinanceResetGuardReadsStatements(t *testing.T) {
	cases := []struct {
		name        string
		dump        string
		wantRefused bool
	}{
		{name: "COMMIT", dump: "COMMIT;", wantRefused: true},
		{name: "lower commit", dump: "commit;", wantRefused: true},
		{name: "ROLLBACK", dump: "ROLLBACK;", wantRefused: true},
		{name: "END TRANSACTION", dump: "END TRANSACTION;", wantRefused: true},
		{name: "psql c with arg", dump: `\c finworld`, wantRefused: true},
		{name: "psql connect with arg", dump: `\connect finworld`, wantRefused: true},
		{name: "SET TRANSACTION", dump: "SET TRANSACTION ISOLATION LEVEL SERIALIZABLE;", wantRefused: true},
		{name: "COMMIT TRANSACTION", dump: "COMMIT TRANSACTION;", wantRefused: true},
		{name: "COMMIT WORK", dump: "COMMIT WORK;", wantRefused: true},
		{name: "COMMIT AND CHAIN", dump: "COMMIT AND CHAIN;", wantRefused: true},
		{name: "commit with space before semicolon", dump: "commit ;", wantRefused: true},
		{name: "ROLLBACK TRANSACTION", dump: "ROLLBACK TRANSACTION;", wantRefused: true},
		{name: "ROLLBACK WORK", dump: "ROLLBACK WORK;", wantRefused: true},
		{name: "ROLLBACK AND CHAIN", dump: "ROLLBACK AND CHAIN;", wantRefused: true},
		{name: "ABORT", dump: "ABORT;", wantRefused: true},
		{name: "top level END", dump: "END;", wantRefused: true},
		{name: "END WORK", dump: "END WORK;", wantRefused: true},
		{name: "PREPARE TRANSACTION", dump: "PREPARE TRANSACTION 'x';", wantRefused: true},
		{name: "bare psql c", dump: `\c`, wantRefused: true},
		{name: "bare psql connect", dump: `\connect`, wantRefused: true},
		{name: "psql i", dump: `\i other.sql`, wantRefused: true},
		{name: "psql ir", dump: `\ir other.sql`, wantRefused: true},
		{name: "psql include", dump: `\include other.sql`, wantRefused: true},
		{name: "psql o", dump: `\o /tmp/out.sql`, wantRefused: true},
		{name: "psql bang", dump: `\! id`, wantRefused: true},
		{name: "SET tab TRANSACTION", dump: "SET\tTRANSACTION READ ONLY;", wantRefused: true},
		{name: "multiline SET TRANSACTION", dump: "SET\n  TRANSACTION\n  READ ONLY;", wantRefused: true},
		{name: "SET SESSION CHARACTERISTICS", dump: "SET SESSION CHARACTERISTICS AS TRANSACTION READ ONLY;", wantRefused: true},
		{name: "top level BEGIN", dump: "BEGIN;", wantRefused: true},
		{name: "BEGIN TRANSACTION", dump: "BEGIN TRANSACTION;", wantRefused: true},
		{name: "START TRANSACTION", dump: "START TRANSACTION;", wantRefused: true},
		{name: "multiline START TRANSACTION", dump: "START\n\tTRANSACTION ISOLATION LEVEL REPEATABLE READ;", wantRefused: true},
		{name: "SET transaction_timeout", dump: "SET transaction_timeout = 0;", wantRefused: false},
		{name: "SET statement_timeout", dump: "SET statement_timeout = 0;", wantRefused: false},
		{name: "SET idle_in_transaction_session_timeout", dump: "SET idle_in_transaction_session_timeout = 0;", wantRefused: false},
		{name: "psql restrict", dump: `\restrict d8f9a0b1c2`, wantRefused: false},
		{name: "psql unrestrict", dump: `\unrestrict d8f9a0b1c2`, wantRefused: false},
		{name: "set_config", dump: "SELECT pg_catalog.set_config('search_path', '', false);", wantRefused: false},
		{
			name:        "dollar quoted plpgsql with BEGIN, END IF, END LOOP and END",
			dump:        "CREATE FUNCTION public.f() RETURNS void LANGUAGE plpgsql AS $$\nBEGIN\n  IF true THEN\n    PERFORM 1;\n  END IF;\n  LOOP\n    EXIT;\n  END LOOP;\nEND;\n$$;",
			wantRefused: false,
		},
		{
			name:        "tagged dollar quoted plpgsql with BEGIN, END IF, END LOOP and END",
			dump:        "CREATE FUNCTION ai.claims() RETURNS jsonb AS $body$\nBEGIN\n  IF true THEN\n    RETURN '{}'::jsonb;\n  END IF;\n  LOOP\n    EXIT;\n  END LOOP;\n  RETURN '{}'::jsonb;\nEND;\n$body$ LANGUAGE plpgsql;",
			wantRefused: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := financeResetTransactionSafe([]byte(tc.dump))
			if tc.wantRefused && err == nil {
				t.Fatalf("the statement %q was accepted inside the reset's transaction", tc.dump)
			}
			if !tc.wantRefused && err != nil {
				t.Fatalf("the harmless statement %q was refused: %v", tc.dump, err)
			}
		})
	}
}
