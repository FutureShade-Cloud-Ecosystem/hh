package readtx

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type stmt struct {
	sql  string
	args []any
}

type fakeTx struct {
	pgx.Tx
	log        *[]stmt
	rolledBack *bool
	committed  *bool
	failOn     string
	failWith   error
}

func (f fakeTx) record(sql string, args []any) error {
	*f.log = append(*f.log, stmt{sql, args})
	if f.failOn != "" && strings.Contains(sql, f.failOn) {
		return f.failWith
	}
	return nil
}

func (f fakeTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, f.record(sql, args)
}

type fakeRow struct{ pid int }

func (r fakeRow) Scan(dest ...any) error {
	if p, ok := dest[0].(*int); ok {
		*p = r.pid
	}
	return nil
}

func (f fakeTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	_ = f.record(sql, args)
	return fakeRow{pid: 777}
}

type fakeRows struct {
	pgx.Rows
	closed *bool
}

func (r fakeRows) Next() bool { return false }
func (r fakeRows) Err() error { return nil }
func (r fakeRows) Close()     { *r.closed = true }

func (f fakeTx) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	if err := f.record(sql, args); err != nil {
		return nil, err
	}
	return fakeRows{closed: new(bool)}, nil
}

func (f fakeTx) Rollback(context.Context) error { *f.rolledBack = true; return nil }
func (f fakeTx) Commit(context.Context) error {
	*f.committed = true
	return errors.New("commit must never be called")
}

type harness struct {
	log        []stmt
	rolledBack bool
	committed  bool
	opts       pgx.TxOptions
	began      bool
	reader     *Reader
}

func newHarness(t *testing.T, failOn string, failWith error) *harness {
	h := &harness{}
	begin := func(_ context.Context, o pgx.TxOptions) (pgx.Tx, error) {
		h.began, h.opts = true, o
		return fakeTx{log: &h.log, rolledBack: &h.rolledBack, committed: &h.committed, failOn: failOn, failWith: failWith}, nil
	}
	h.reader = &Reader{
		audience: AudienceStaff,
		role:     "assist_reader_test",
		signer:   fixedSigner(t),
		limits:   Limits{Statement: 5 * time.Second, Lock: 2 * time.Second},
		begin:    begin,
	}
	return h
}

var okClaims = Claims{Audience: AudienceStaff, AskerID: "user-1", BranchIDs: []string{"b1"}}

func TestRunIsOneReadOnlyTransactionInTheFixedOrderAndAlwaysRollsBack(t *testing.T) {
	h := newHarness(t, "", nil)
	var scanned bool
	err := h.reader.Run(context.Background(), okClaims, "SELECT 1 FROM ai.t WHERE x = $1", []any{42}, func(pgx.Rows) error { scanned = true; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if h.opts.AccessMode != pgx.ReadOnly {
		t.Errorf("transaction access mode = %q, want read only", h.opts.AccessMode)
	}
	want := []string{
		"SELECT pg_backend_pid()",
		"SELECT set_config('ai.claims', $1, true)",
		"SET LOCAL statement_timeout = 5000",
		"SET LOCAL lock_timeout = 2000",
		`SET LOCAL ROLE "assist_reader_test"`,
		"SELECT 1 FROM ai.t WHERE x = $1",
	}
	if len(h.log) != len(want) {
		t.Fatalf("statements = %d, want %d: %v", len(h.log), len(want), h.log)
	}
	for i, w := range want {
		if h.log[i].sql != w {
			t.Errorf("statement %d = %q, want %q", i, h.log[i].sql, w)
		}
	}
	// The signed claim travels only as a bind parameter.
	if len(h.log[1].args) != 1 || !strings.HasPrefix(fmt.Sprint(h.log[1].args[0]), "v1.") {
		t.Errorf("set_config argument = %v", h.log[1].args)
	}
	for _, s := range h.log {
		if strings.Contains(s.sql, "v1.") {
			t.Errorf("the signed claim is in statement text: %q", s.sql)
		}
	}
	if !scanned || !h.rolledBack || h.committed {
		t.Errorf("scanned=%v rolledBack=%v committed=%v", scanned, h.rolledBack, h.committed)
	}
}

func TestRunRollsBackWhenTheStatementOrTheCallerFails(t *testing.T) {
	boom := &pgconn.PgError{Code: "57014", Message: "canceling statement due to statement timeout"}
	h := newHarness(t, "FROM ai.t", boom)
	err := h.reader.Run(context.Background(), okClaims, "SELECT 1 FROM ai.t", nil, func(pgx.Rows) error { return nil })
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeTimeout {
		t.Fatalf("err = %v, want a timeout code", err)
	}
	if !h.rolledBack {
		t.Error("no rollback after a failed statement")
	}
	h = newHarness(t, "", nil)
	callerErr := errors.New("scan failed")
	h.reader.Run(context.Background(), okClaims, "SELECT 1", nil, func(pgx.Rows) error { return callerErr })
	if !h.rolledBack {
		t.Error("no rollback after the caller's scan failed")
	}
}

func TestRunRefusesAClaimOfTheOtherAudienceBeforeTouchingTheDatabase(t *testing.T) {
	h := newHarness(t, "", nil)
	err := h.reader.Run(context.Background(), Claims{Audience: AudiencePro, AskerID: "p"}, "SELECT 1", nil, func(pgx.Rows) error { return nil })
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeAudience {
		t.Fatalf("err = %v, want an audience refusal", err)
	}
	if h.began || len(h.log) != 0 {
		t.Error("a mismatched audience reached the database")
	}
}

func TestRunErrorsNeverCarryTheClaimOrTheCause(t *testing.T) {
	leaky := errors.New("boom with v1.12345.PAYLOAD.MAC and password=hunter2")
	h := newHarness(t, "set_config", leaky)
	err := h.reader.Run(context.Background(), okClaims, "SELECT 1", nil, func(pgx.Rows) error { return nil })
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, shape := range []string{err.Error(), fmt.Sprintf("%v %+v %#v", err, err, err)} {
		if strings.Contains(shape, "v1.") || strings.Contains(shape, "hunter2") || strings.Contains(shape, "PAYLOAD") {
			t.Errorf("error leaked: %q", shape)
		}
	}
	if !h.rolledBack {
		t.Error("no rollback")
	}
}

func TestRunRefusesAnUnusableRoleName(t *testing.T) {
	h := newHarness(t, "", nil)
	h.reader.role = `x"; DROP TABLE y; --`
	// An odd name is quoted as one identifier, never spliced as a statement.
	_ = h.reader.Run(context.Background(), okClaims, "SELECT 1", nil, func(pgx.Rows) error { return nil })
	for _, s := range h.log {
		if strings.HasPrefix(s.sql, "SET LOCAL ROLE") && strings.Contains(s.sql, `x"; DROP`) {
			t.Errorf("role name not quoted: %q", s.sql)
		}
	}
}
