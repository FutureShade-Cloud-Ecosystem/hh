//go:build integration

package main

// finance_user_one_rule_integration_test.go: the boot time apply and the
// one-shot command provision through ONE function (W2-BOX-FOLD-A). The list
// the box carries and the person an operator names are the same rule: a
// role change demotes the person's other live grants, a run that wrote
// nothing records no audit row, and a platform id held with another email
// refuses, naming the entry by its email alone.
//
//	Run: DATABASE_URL='postgres://postgres:postgres@127.0.0.1:57743/postgres?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -count=1 -run FinanceUserApply

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

// finAuditCount counts the audit rows of one action, so a leg can say
// whether a run wrote or stood still.
func finAuditCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, action string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = $1`, action).Scan(&n); err != nil {
		t.Fatalf("the %s audit rows: %v", action, err)
	}
	return n
}

// TestIntegration_FinanceUserApplyDemotesOnARoleChange is the demote rule
// through the boot time apply: the list changes a person's role, and the
// next apply leaves that person with exactly one live grant, the new one.
// The one-shot command already demotes; the apply path must do the same,
// because it is the same rule and not a second one.
func TestIntegration_FinanceUserApplyDemotesOnARoleChange(t *testing.T) {
	ctx := context.Background()
	pool, dsn := freshMigratedBoxDB(t, "finuserone01")
	t.Setenv("APP_ENV", "staging")
	if err := financeSeedRole(mapGetenv(financeSeedEnv(dsn))); err != nil {
		t.Fatalf("finance-seed: %v", err)
	}

	email := "fin-role-change@example.invalid"
	controller := finEnvEntry(email, "fin-console-role-1", "Role Change Person", "financial_controller")
	payables := finEnvEntry(email, "fin-console-role-1", "Role Change Person", "payables_receivables")

	if err, _ := finApply(t, finApplyEnv(dsn, []string{email}, controller)); err != nil {
		t.Fatalf("the first apply: %v", err)
	}

	resolver, err := authz.NewPgxOrgResolver(pool)
	if err != nil {
		t.Fatalf("the org resolver: %v", err)
	}
	orgID, ok, err := resolver.ResolveSlug(ctx, finOrgSlug)
	if err != nil || !ok {
		t.Fatalf("the org anchor: ok=%v err=%v", ok, err)
	}
	if roles := finLiveGrantRoles(t, ctx, pool, orgID, "fin-console-role-1"); len(roles) != 1 || roles[0] != "financial_controller" {
		t.Fatalf("the person must start with the controller role alone, got %v", roles)
	}

	// The list changes the role: the same person, the same address, the
	// other finance role.
	if err, _ := finApply(t, finApplyEnv(dsn, []string{email}, payables)); err != nil {
		t.Fatalf("the apply after the role change: %v", err)
	}

	roles := finLiveGrantRoles(t, ctx, pool, orgID, "fin-console-role-1")
	if len(roles) != 1 || roles[0] != "payables_receivables" {
		t.Fatalf("after the role change the person must hold one live grant, the new one, got %v", roles)
	}
}

// TestIntegration_FinanceUserApplyWritesNoAuditRowWhenTheListDidNotChange
// is the audit rule through the boot time apply: a second apply of the same
// list writes nothing, so the box's boots leave no trail of their own.
func TestIntegration_FinanceUserApplyWritesNoAuditRowWhenTheListDidNotChange(t *testing.T) {
	ctx := context.Background()
	pool, dsn := freshMigratedBoxDB(t, "finuserone02")
	t.Setenv("APP_ENV", "staging")
	if err := financeSeedRole(mapGetenv(financeSeedEnv(dsn))); err != nil {
		t.Fatalf("finance-seed: %v", err)
	}

	emailA := "fin-unchanged-a@example.invalid"
	emailB := "fin-unchanged-b@example.invalid"
	list := finEnvEntry(emailA, "fin-console-unchanged-1", "Unchanged Person", "financial_controller") + ";" +
		finEnvEntry(emailB, "fin-console-unchanged-2", "Other Unchanged Person", "payables_receivables")
	env := finApplyEnv(dsn, []string{emailA, emailB}, list)

	if err, _ := finApply(t, env); err != nil {
		t.Fatalf("the first apply: %v", err)
	}
	first := finAuditCount(t, ctx, pool, "STAFF_PROVISION")
	if first != 2 {
		t.Fatalf("the first apply must write one provision audit row per person it linked, got %d", first)
	}

	if err, _ := finApply(t, env); err != nil {
		t.Fatalf("the second apply: %v", err)
	}
	if second := finAuditCount(t, ctx, pool, "STAFF_PROVISION"); second != first {
		t.Fatalf("an unchanged list must write no audit row, got %d rows, want %d", second, first)
	}
}

// TestIntegration_FinanceUserApplyRefusesAHeldIdWithAnotherEmail is the
// refusal rule through the boot time apply: an entry naming a platform id
// that already holds another email refuses the boot, naming the entry by
// its email alone, and the row and the grant the first apply wrote stand.
func TestIntegration_FinanceUserApplyRefusesAHeldIdWithAnotherEmail(t *testing.T) {
	ctx := context.Background()
	pool, dsn := freshMigratedBoxDB(t, "finuserone03")
	t.Setenv("APP_ENV", "staging")
	if err := financeSeedRole(mapGetenv(financeSeedEnv(dsn))); err != nil {
		t.Fatalf("finance-seed: %v", err)
	}

	heldEmail := "fin-held-id-a@example.invalid"
	otherEmail := "fin-held-id-b@example.invalid"
	held := finEnvEntry(heldEmail, "fin-console-held-1", "Held Person", "financial_controller")
	other := finEnvEntry(otherEmail, "fin-console-held-1", "Other Person", "financial_controller")

	if err, _ := finApply(t, finApplyEnv(dsn, []string{heldEmail, otherEmail}, held)); err != nil {
		t.Fatalf("the first apply: %v", err)
	}

	err, _ := finApply(t, finApplyEnv(dsn, []string{heldEmail, otherEmail}, other))
	if err == nil {
		t.Fatal("a platform id held with another email must refuse")
	}
	if !strings.Contains(err.Error(), otherEmail) {
		t.Fatalf("the refusal must name the entry by its email, got %v", err)
	}
	if strings.Contains(err.Error(), "fin-console-held-1") || strings.Contains(err.Error(), "Other Person") {
		t.Fatalf("the refusal must carry the email alone, got %v", err)
	}

	// The refusal rolled back: the row still holds the first address.
	var rowEmail string
	if err := pool.QueryRow(ctx, `SELECT email::text FROM users WHERE platform_user_id = $1`, "fin-console-held-1").Scan(&rowEmail); err != nil {
		t.Fatalf("the held person's row: %v", err)
	}
	if rowEmail != heldEmail {
		t.Fatalf("the held person's row must keep the first address, got %q", rowEmail)
	}
}
