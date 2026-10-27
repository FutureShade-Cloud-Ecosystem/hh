package identity

// DisableSeedCredentials, the finance seed's password leg (the deploy spec
// FINANCE-BOX-SPEC-r1, section 3). Passwords in this tree are stored as
// bcrypt hashes: staff in users.password_hash, portal in
// portal_users.password_hash. The dibbits demo seed plants both with a
// known demo hash, so a finance seeded world must leave those rows with no
// usable password: NULL, the same spelling the INVITED staff state carries.
// This file owns the statement; cmd/core calls the seam, never SQL of its
// own.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// seedCredFake is the least DBTX double: it records every statement and
// answers a fixed tag per call.
type seedCredFake struct {
	sqls []string
	args [][]any
	tags []pgconn.CommandTag
}

func (f *seedCredFake) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.sqls = append(f.sqls, sql)
	f.args = append(f.args, args)
	if len(f.tags) == 0 {
		return pgconn.NewCommandTag("UPDATE 0"), nil
	}
	tag := f.tags[0]
	f.tags = f.tags[1:]
	return tag, nil
}

func (f *seedCredFake) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("seedCredFake: no Query calls are expected")
}

func (f *seedCredFake) QueryRow(context.Context, string, ...any) pgx.Row { return nil }

func TestDisableSeedCredentials_RefusesNilDatabase(t *testing.T) {
	if _, err := DisableSeedCredentials(context.Background(), nil); err == nil {
		t.Fatal("a nil database handle must refuse, never guess")
	}
}

func TestDisableSeedCredentials_ClearsTheSeedUsersOnBothTables(t *testing.T) {
	fake := &seedCredFake{tags: []pgconn.CommandTag{
		pgconn.NewCommandTag("UPDATE 6"),
		pgconn.NewCommandTag("UPDATE 1"),
	}}
	report, err := DisableSeedCredentials(context.Background(), fake)
	if err != nil {
		t.Fatalf("DisableSeedCredentials: %v", err)
	}
	if len(fake.sqls) != 2 {
		t.Fatalf("both credential tables must be cleared, got %d statements", len(fake.sqls))
	}
	if report.StaffCleared != 6 || report.PortalCleared != 1 {
		t.Fatalf("the report must carry both counts, got %+v", report)
	}
	for i, table := range []string{"users", "portal_users"} {
		if !strings.Contains(fake.sqls[i], "UPDATE "+table) || !strings.Contains(fake.sqls[i], "password_hash = NULL") {
			t.Fatalf("statement %d must NULL the password hash on %s, got: %s", i, table, fake.sqls[i])
		}
	}
	// The staff statement must name every seed id, so a seed user can never
	// keep a usable password while its neighbour loses theirs.
	if len(fake.args[0]) != 1 {
		t.Fatalf("the staff statement takes the id list as one argument, got %v", fake.args[0])
	}
	ids, ok := fake.args[0][0].([]string)
	if !ok {
		t.Fatalf("the staff statement's argument must be the id list, got %T", fake.args[0][0])
	}
	if len(ids) != len(seedStaffIDs) {
		t.Fatalf("the statement must name all %d seed staff ids, got %d", len(seedStaffIDs), len(ids))
	}
	seen := map[string]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	for _, want := range seedStaffIDs {
		if !seen[want] {
			t.Fatalf("the id list is missing the seed id %s", want)
		}
	}
}
