package identity

// DisableSeedCredentials, the finance seed's staff password leg (the deploy
// spec FINANCE-BOX-SPEC-r1, section 3). Passwords in this tree are stored as
// bcrypt hashes: staff in users.password_hash, portal in
// portal_users.password_hash. A fresh finance world has no real user, so the
// clear is deliberately blanket: every staff hash NULLed, so the seed's
// logins AND the migrations' demonstration logins end usable by no
// password. This file owns the staff statement; cmd/core calls the seam
// beside the portal module's own, never SQL of its own.

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

func TestDisableSeedCredentials_ClearsEveryStaffCredential(t *testing.T) {
	fake := &seedCredFake{tags: []pgconn.CommandTag{pgconn.NewCommandTag("UPDATE 6")}}
	cleared, err := DisableSeedCredentials(context.Background(), fake)
	if err != nil {
		t.Fatalf("DisableSeedCredentials: %v", err)
	}
	if cleared != 6 {
		t.Fatalf("the seam must report how many rows it cleared, got %d", cleared)
	}
	if len(fake.sqls) != 1 {
		t.Fatalf("the staff table is the one credential table this module owns, got %d statements", len(fake.sqls))
	}
	if !strings.Contains(fake.sqls[0], "UPDATE users") || !strings.Contains(fake.sqls[0], "password_hash") {
		t.Fatalf("the statement must rewrite the password hash on users, got: %s", fake.sqls[0])
	}
	// The statement is blanket: no id list, no email, no other key names
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
}

// TestDisableSeedCredentials_TouchesNoOtherModuleTable is the census rule
// proved where the statement lives: this module owns the users table and
// writes no other module's table. The portal half of the seed's password
// leg lives in the portal module, which owns portal_users.
func TestDisableSeedCredentials_TouchesNoOtherModuleTable(t *testing.T) {
	fake := &seedCredFake{}
	if _, err := DisableSeedCredentials(context.Background(), fake); err != nil {
		t.Fatalf("DisableSeedCredentials: %v", err)
	}
	for _, sql := range fake.sqls {
		if strings.Contains(sql, "portal_users") {
			t.Fatalf("the identity module must not write the portal module's table, got: %s", sql)
		}
	}
}
