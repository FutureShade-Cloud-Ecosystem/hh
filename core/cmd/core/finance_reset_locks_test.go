package main

// finance_reset_locks_test.go pins the reset's lock pre-flight: the numbers
// it reads off the server, the estimate it builds from the box and the dump,
// and the words it refuses with.
//
// The pre-flight answers a measured fact: one world restore runs in ONE
// transaction and takes thousands of locks, and the server's lock table is a
// fixed number of slots. A box whose server cannot hold that one transaction
// has to be told before anything is dropped, never half way through the
// restore, and the telling has to name the numbers and the setting to raise.

import (
	"strconv"
	"strings"
	"testing"
)

func TestFinanceResetLockCapacity(t *testing.T) {
	// The nominal table: max_locks_per_transaction x (max_connections +
	// max_prepared_transactions).
	if got := financeResetLockCapacity(64, 100, 0); got != 6400 {
		t.Fatalf("the nominal lock table is %d, want 6400", got)
	}
	// The prepared transactions take their own share of the table.
	if got := financeResetLockCapacity(64, 100, 10); got != 7040 {
		t.Fatalf("the lock table with prepared transactions is %d, want 7040", got)
	}
	// A server with no room at all has a table of no slots.
	if got := financeResetLockCapacity(0, 100, 0); got != 0 {
		t.Fatalf("the lock table of a server with no locks per transaction is %d, want 0", got)
	}
}

func TestFinanceResetDumpObjectStatements(t *testing.T) {
	// The dump's own heads count, the world's data does not, and a comment
	// does not either: the estimate is about the objects the one transaction
	// creates and alters, not about the rows it copies.
	dump := []byte(`--
-- PostgreSQL database dump
--

CREATE SCHEMA ai;

CREATE TABLE public.ledger (
    id bigint NOT NULL
);

ALTER TABLE public.ledger OWNER TO postgres;

ALTER DEFAULT PRIVILEGES FOR ROLE postgres IN SCHEMA public GRANT SELECT ON TABLES TO postgres;

-- CREATE TABLE public.commented_out (id bigint);

COPY public.ledger (id) FROM stdin;
1
CREATE TABLE this_row_is_data (id bigint);
\.
`)
	if got := financeResetDumpObjectStatements(dump); got != 4 {
		t.Fatalf("the dump carries %d object statements, want 4", got)
	}
	// A dump with nothing in it needs nothing.
	if got := financeResetDumpObjectStatements([]byte("-- an empty dump\n")); got != 0 {
		t.Fatalf("an empty dump carries %d object statements, want 0", got)
	}
}

func TestFinanceResetLockEstimate(t *testing.T) {
	// The estimate is the box's own objects plus the dump's own statements:
	// the one transaction holds the locks of both.
	if got := financeResetLockEstimate(1384, 2044); got != 3428 {
		t.Fatalf("the estimate is %d, want 3428", got)
	}
}

func TestFinanceResetLocksFit(t *testing.T) {
	// The table has to hold twice the estimate: the restore takes the
	// world's locks and the box's own, and a table that holds only one of
	// them is a table that runs out half way through.
	fits := financeResetLocks{Estimate: 1000, Capacity: 2000, PerTransaction: 64, Connections: 30, Prepared: 0}
	if !fits.Fits() {
		t.Fatalf("a table of %d holds twice the estimate of %d: %v", fits.Capacity, fits.Estimate, fits.Refusal())
	}
	// The boundary itself fits: a table of exactly twice the estimate holds it.
	exact := financeResetLocks{Estimate: 1000, Capacity: 2000}
	if !exact.Fits() {
		t.Fatalf("a table of exactly twice the estimate does not fit: %v", exact.Refusal())
	}
	// One slot short does not.
	short := financeResetLocks{Estimate: 1000, Capacity: 1999}
	if short.Fits() {
		t.Fatalf("a table one slot short of twice the estimate fits")
	}
}

func TestFinanceResetLockRefusal(t *testing.T) {
	// The estimate is computed from the catalog and dump counts, never
	// planted, against a default server's nominal table of 6,400 slots.
	estimate := financeResetLockEstimate(1384, 2044)
	capacity := financeResetLockCapacity(64, 100, 0)
	locks := financeResetLocks{
		Estimate:       estimate,
		Capacity:       capacity,
		PerTransaction: 64,
		Connections:    100,
		Prepared:       0,
	}
	err := locks.Refusal()
	if err == nil {
		t.Fatalf("a table of %d against an estimate of %d does not refuse", locks.Capacity, locks.Estimate)
	}
	msg := err.Error()
	t.Logf("the refusal reads: %s", msg)
	// The estimate.
	if !strings.Contains(msg, strconv.Itoa(estimate)) {
		t.Fatalf("the refusal does not name the estimate %d: %q", estimate, msg)
	}
	// The capacity.
	if !strings.Contains(msg, strconv.Itoa(capacity)) {
		t.Fatalf("the refusal does not name the capacity %d: %q", capacity, msg)
	}
	// The setting to raise.
	if !strings.Contains(msg, "-c max_locks_per_transaction=") {
		t.Fatalf("the refusal does not name the setting to raise: %q", msg)
	}
	// It says the reset refuses before it drops anything.
	if !strings.Contains(msg, "before it drops anything") {
		t.Fatalf("the refusal does not say that nothing is dropped: %q", msg)
	}
	// It names the setting the box runs with.
	want := "-c max_locks_per_transaction=" + strconv.Itoa(financeResetLockSettingFloor)
	if !strings.Contains(msg, want) {
		t.Fatalf("the refusal does not name %q: %q", want, msg)
	}
}

func TestFinanceResetLockSuggestedSetting(t *testing.T) {
	// The setting that holds twice the estimate, rounded up to a power of
	// two, and never below the box's own floor.
	estimate := financeResetLockEstimate(1384, 2044)
	locks := financeResetLocks{Estimate: estimate, Capacity: 6400, Connections: 100}
	if got := locks.SuggestedSetting(); got != 512 {
		t.Fatalf("the suggested setting for an estimate of %d is %d, want 512", estimate, got)
	}
	// A world that needs more than the floor gets a setting above it.
	big := financeResetLocks{Estimate: 40000, Capacity: 6400, Connections: 100}
	if got := big.SuggestedSetting(); got <= financeResetLockSettingFloor {
		t.Fatalf("the suggested setting for an estimate of 40000 is %d, want more than the floor %d", got, financeResetLockSettingFloor)
	}
	// The setting it names really does hold twice the estimate.
	for _, est := range []int{1, 100, estimate, 40000, 250000} {
		l := financeResetLocks{Estimate: est, Capacity: 6400, Connections: 100}
		setting := l.SuggestedSetting()
		capacity := financeResetLockCapacity(setting, 100, 0)
		if capacity < 2*est {
			t.Fatalf("the suggested setting %d holds %d slots, less than twice the estimate %d", setting, capacity, est)
		}
	}
}
