package identity

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// grantDB is a DBTX double: QueryRow answers the role's scope (or no row),
// Exec records the insert.
type grantDB struct {
	scope   string
	noRole  bool
	execs   int
	lastSQL string
	args    []any
}

type scopeRow struct {
	scope string
	err   error
}

func (r scopeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*(dest[0].(*string)) = r.scope
	return nil
}

func (d *grantDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	d.execs++
	d.lastSQL, d.args = sql, args
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}
func (d *grantDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("not used")
}
func (d *grantDB) QueryRow(context.Context, string, ...any) pgx.Row {
	if d.noRole {
		return scopeRow{err: pgx.ErrNoRows}
	}
	return scopeRow{scope: d.scope}
}

func TestEnsureStaffGrantWritesAGlobalRoleWithNoBranch(t *testing.T) {
	db := &grantDB{scope: ScopeGlobal}
	inserted, err := EnsureStaffGrant(context.Background(), db, "org-1", "persona:x", "dealer_admin", nil)
	if err != nil || !inserted || db.execs != 1 {
		t.Fatalf("inserted %v err %v execs %d", inserted, err, db.execs)
	}
}

func TestEnsureStaffGrantWritesABranchRoleWithItsBranch(t *testing.T) {
	db := &grantDB{scope: ScopeBranch}
	b := "b-1"
	if inserted, err := EnsureStaffGrant(context.Background(), db, "org-1", "persona:x", "driver", &b); err != nil || !inserted {
		t.Fatalf("inserted %v err %v", inserted, err)
	}
}

// The branch rule of the 0215 header holds at this boundary too, and an
// unknown role or an empty argument writes nothing.
func TestEnsureStaffGrantRefusesShapeDefectsBeforeAnyWrite(t *testing.T) {
	b := "b-1"
	for name, c := range map[string]struct {
		db     *grantDB
		sub    string
		role   string
		branch *string
	}{
		"branch role no branch":   {&grantDB{scope: ScopeBranch}, "p", "driver", nil},
		"global role with branch": {&grantDB{scope: ScopeGlobal}, "p", "hr", &b},
		"unknown scope":           {&grantDB{scope: "galaxy"}, "p", "hr", nil},
		"unknown role":            {&grantDB{noRole: true}, "p", "nope", nil},
		"empty subject":           {&grantDB{scope: ScopeGlobal}, "", "hr", nil},
		"empty role":              {&grantDB{scope: ScopeGlobal}, "p", "", nil},
	} {
		if _, err := EnsureStaffGrant(context.Background(), c.db, "org-1", c.sub, c.role, c.branch); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if c.db.execs != 0 {
			t.Errorf("%s: wrote", name)
		}
	}
	if _, err := EnsureStaffGrant(context.Background(), &grantDB{scope: ScopeGlobal}, "", "p", "hr", nil); err == nil {
		t.Error("empty org accepted")
	}
}

// EnsureStaffGrantAt takes the branch for a branch role and drops it for a
// global one, so the caller cannot trip the branch rule.
func TestEnsureStaffGrantAtFollowsTheRolesScope(t *testing.T) {
	for _, c := range []struct {
		scope      string
		wantBranch bool
	}{{ScopeBranch, true}, {ScopeGlobal, false}} {
		db := &grantDB{scope: c.scope}
		inserted, err := EnsureStaffGrantAt(context.Background(), db, "org-1", "persona:x", "r", "b-1")
		if err != nil || !inserted || db.execs != 1 {
			t.Fatalf("%s: inserted %v err %v", c.scope, inserted, err)
		}
		got, _ := db.args[3].(*string)
		if (got != nil) != c.wantBranch {
			t.Fatalf("%s: branch arg %v", c.scope, db.args[3])
		}
	}
	for name, db := range map[string]*grantDB{"unknown role": {noRole: true}, "unknown scope": {scope: "x"}} {
		if _, err := EnsureStaffGrantAt(context.Background(), db, "o", "p", "r", "b"); err == nil || db.execs != 0 {
			t.Errorf("%s: err %v execs %d", name, err, db.execs)
		}
	}
	if _, err := EnsureStaffGrantAt(context.Background(), &grantDB{scope: ScopeGlobal}, "o", "p", "r", ""); err == nil {
		t.Error("an empty branch was accepted")
	}
}
