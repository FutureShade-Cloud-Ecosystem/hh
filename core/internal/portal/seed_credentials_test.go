package portal

// DisableSeedCredentials, the finance seed's portal password leg (the
// deploy spec FINANCE-BOX-SPEC-r1, section 3). Portal hashes live in
// portal_users.password_hash, which is NOT NULL and spells no password with
// the literal marker "seeded-no-password". A fresh finance world has no real
// user, so the clear is deliberately blanket: every portal hash takes the
// marker, so the seed's logins AND the migrations' demonstration logins end
// usable by no password. This file owns the statement; cmd/core calls the
// seam beside the identity module's own, never SQL of its own.

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
)

// seedCredFake is the least Execer double: it records every statement and
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

func TestDisableSeedCredentials_RefusesNilDatabase(t *testing.T) {
	if _, err := DisableSeedCredentials(context.Background(), nil); err == nil {
		t.Fatal("a nil database handle must refuse, never guess")
	}
}

func TestDisableSeedCredentials_MarksEveryPortalPasswordUnusable(t *testing.T) {
	fake := &seedCredFake{tags: []pgconn.CommandTag{pgconn.NewCommandTag("UPDATE 2")}}
	cleared, err := DisableSeedCredentials(context.Background(), fake)
	if err != nil {
		t.Fatalf("DisableSeedCredentials: %v", err)
	}
	if cleared != 2 {
		t.Fatalf("the seam must report how many rows it marked, got %d", cleared)
	}
	if len(fake.sqls) != 1 {
		t.Fatalf("the portal table is the one credential table this module owns, got %d statements", len(fake.sqls))
	}
	if !strings.Contains(fake.sqls[0], "UPDATE portal_users") || !strings.Contains(fake.sqls[0], "password_hash") {
		t.Fatalf("the statement must rewrite the password hash on portal_users, got: %s", fake.sqls[0])
	}
	// The marker is the one identity plants the portal accounts with, so the
	// marker that clears the passwords is the marker they were planted with.
	if len(fake.args[0]) != 1 || fake.args[0][0] != identity.PortalNoPasswordMarker {
		t.Fatalf("the portal statement must carry only the %q marker, got: %v", identity.PortalNoPasswordMarker, fake.args[0])
	}
	if !strings.Contains(fake.sqls[0], "password_hash <> $1") {
		t.Fatalf("the portal statement must touch only rows off the marker (idempotency), got: %s", fake.sqls[0])
	}
}

// TestDisableSeedCredentials_TouchesNoOtherModuleTable is the census rule
// proved where the statement lives: this module owns portal_users and writes
// no other module's table. The staff half of the seed's password leg lives
// in the identity module, which owns users.
func TestDisableSeedCredentials_TouchesNoOtherModuleTable(t *testing.T) {
	fake := &seedCredFake{}
	if _, err := DisableSeedCredentials(context.Background(), fake); err != nil {
		t.Fatalf("DisableSeedCredentials: %v", err)
	}
	for _, sql := range fake.sqls {
		if strings.Contains(sql, "UPDATE users") {
			t.Fatalf("the portal module must not write the identity module's table, got: %s", sql)
		}
	}
}
