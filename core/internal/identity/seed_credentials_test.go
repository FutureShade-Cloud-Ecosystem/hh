package identity

// DisableSeedCredentials, the finance seed's password leg (the deploy spec
// FINANCE-BOX-SPEC-r1, section 3). Passwords in this tree are stored as
// bcrypt hashes: staff in users.password_hash, portal in
// portal_users.password_hash. A fresh finance world has no real user, so the
// clear is deliberately blanket: every staff hash NULLed, every portal hash
// on the no-password marker, so the seed's logins AND the migrations'
// demonstration logins end usable by no password. This file owns the
// statements; cmd/core calls the seam, never SQL of its own.

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

func TestDisableSeedCredentials_ClearsEveryCredentialOnBothTables(t *testing.T) {
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
		if !strings.Contains(fake.sqls[i], "UPDATE "+table) || !strings.Contains(fake.sqls[i], "password_hash") {
			t.Fatalf("statement %d must rewrite the password hash on %s, got: %s", i, table, fake.sqls[i])
		}
	}
	// The statements are blanket: no id list, no email, no other key names
	// rows. A demonstration login the migrations planted must be cleared by
	// the same statement that clears the seed's own logins, so a planted
	// credential can never survive for want of a list entry.
	if len(fake.args[0]) != 0 {
		t.Fatalf("the staff statement must be blanket, carrying no arguments, got: %v", fake.args[0])
	}
	if !strings.Contains(fake.sqls[0], "password_hash = NULL") {
		t.Fatalf("the staff statement must NULL the hash (the INVITED spelling), got: %s", fake.sqls[0])
	}
	if !strings.Contains(fake.sqls[0], "password_hash IS NOT NULL") {
		t.Fatalf("the staff statement must touch only rows that hold a hash (idempotency), got: %s", fake.sqls[0])
	}
	if len(fake.args[1]) != 1 || fake.args[1][0] != portalNoPassword {
		t.Fatalf("the portal statement must carry only the %q marker, got: %v", portalNoPassword, fake.args[1])
	}
	if !strings.Contains(fake.sqls[1], "password_hash <> $1") {
		t.Fatalf("the portal statement must touch only rows off the marker (idempotency), got: %s", fake.sqls[1])
	}
}
