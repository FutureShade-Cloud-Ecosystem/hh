//go:build integration

package main

// finance_user_env_integration_test.go, the finance box's user list applied
// from the environment (W2-8): the command reads HH_FINANCE_USERS, provisions
// every entry and revokes every finance person the list left out, all of it in
// ONE transaction, and a bad entry refuses naming the entry by its email
// alone. The box is seeded by core finance-seed, the way the image seeds it.
//
//	Run: DATABASE_URL='postgres://postgres:postgres@127.0.0.1:57737/postgres?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -count=1 -run FinanceUserApplyFromEnv

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/jackc/pgx/v5/pgxpool"
)

// finEnvUsersVar is the one name the box's finance user list lives in.
const finEnvUsersVar = "HH_FINANCE_USERS"

// finEnvEntry is one person as the list carries them: email, platform user
// id, display name and role.
func finEnvEntry(email, platformUserID, name, role string) string {
	return strings.Join([]string{email, platformUserID, name, role}, "|")
}

// finApplyEnv is the finance box's environment for the apply path: the
// database, the box statement, the sign in mode, the org slug, the sign in
// allow list and the list itself.
func finApplyEnv(dsn string, addresses []string, users string) map[string]string {
	return map[string]string{
		"DATABASE_URL":                dsn,
		"CORE_DATABASE_URL":           dsn,
		"HH_BOX_KIND":                 "finance",
		"APP_ENV":                     "staging",
		"CORE_SIGNIN_MODE":            "console",
		"HH_WEB_ORG":                  finOrgSlug,
		"CORE_SIGNIN_EMAIL_ADDRESSES": strings.Join(addresses, ","),
		finEnvUsersVar:                users,
	}
}

// finApply runs the apply path and hands back the error and what it printed.
func finApply(t *testing.T, env map[string]string) (error, string) {
	t.Helper()
	out := &bytes.Buffer{}
	err := financeUserRole([]string{"--apply-from-env"}, out, out, mapGetenv(env))
	return err, out.String()
}

func TestIntegration_FinanceUserApplyFromEnv(t *testing.T) {
	ctx := context.Background()
	pool, dsn := freshMigratedBoxDB(t, "finuserenv")
	t.Setenv("APP_ENV", "staging")
	if err := financeSeedRole(mapGetenv(financeSeedEnv(dsn))); err != nil {
		t.Fatalf("finance-seed: %v", err)
	}

	controllerEmail := "fin-controller-a@example.invalid"
	payablesEmail := "fin-payables-b@example.invalid"
	controller := finEnvEntry(controllerEmail, "fin-console-user-a", "Financial Controller", "financial_controller")
	payables := finEnvEntry(payablesEmail, "fin-console-user-b", "Payables and Receivables", "payables_receivables")

	// The whole list, applied once.
	env := finApplyEnv(dsn, []string{controllerEmail, payablesEmail}, controller+";"+payables)
	err, out := finApply(t, env)
	if err != nil {
		t.Fatalf("apply from the environment: %v", err)
	}
	if strings.Contains(out, "example.invalid") {
		t.Fatalf("the apply must print ids and counts, never an email: %q", out)
	}
	if !strings.Contains(out, "applied=2") || !strings.Contains(out, "revoked=0") {
		t.Fatalf("the apply must print its counts: %q", out)
	}

	resolver, err := authz.NewPgxOrgResolver(pool)
	if err != nil {
		t.Fatalf("the org resolver: %v", err)
	}
	orgID, ok, err := resolver.ResolveSlug(ctx, finOrgSlug)
	if err != nil || !ok {
		t.Fatalf("the org anchor: ok=%v err=%v", ok, err)
	}

	// Both people are linked with the role the list gave them.
	if n := finLiveGrants(t, ctx, pool, orgID, "fin-console-user-a"); n != 1 {
		t.Fatalf("the controller must hold one live grant, got %d", n)
	}
	if roles := finLiveGrantRoles(t, ctx, pool, orgID, "fin-console-user-a"); len(roles) != 1 || roles[0] != "financial_controller" {
		t.Fatalf("the controller must hold the financial_controller role, got %v", roles)
	}
	if roles := finLiveGrantRoles(t, ctx, pool, orgID, "fin-console-user-b"); len(roles) != 1 || roles[0] != "payables_receivables" {
		t.Fatalf("the payables person must hold the payables_receivables role, got %v", roles)
	}

	// A second run converges: the same ids, one live grant each, no second
	// grant row.
	err, out2 := finApply(t, env)
	if err != nil {
		t.Fatalf("a second apply from the environment: %v", err)
	}
	if out2 != out {
		t.Fatalf("a second apply must converge to the same ids and counts:\nfirst:  %q\nsecond: %q", out, out2)
	}
	if n := finLiveGrants(t, ctx, pool, orgID, "fin-console-user-a"); n != 1 {
		t.Fatalf("a second apply must not leave a second grant, got %d", n)
	}

	// The list leaves a person out: that person is revoked, the other is left
	// alone.
	env2 := finApplyEnv(dsn, []string{controllerEmail}, controller)
	err, out3 := finApply(t, env2)
	if err != nil {
		t.Fatalf("apply with a shorter list: %v", err)
	}
	if !strings.Contains(out3, "revoked=1") {
		t.Fatalf("the apply must count the person it revoked: %q", out3)
	}
	if n := finLiveGrants(t, ctx, pool, orgID, "fin-console-user-b"); n != 0 {
		t.Fatalf("the person the list left out must hold no live grant, got %d", n)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM users WHERE platform_user_id = $1`, "fin-console-user-b").Scan(&status); err != nil {
		t.Fatalf("the revoked person's row: %v", err)
	}
	if status != "DEACTIVATED" {
		t.Fatalf("the revoked person's row must be DEACTIVATED, got %q", status)
	}
	if n := finLiveGrants(t, ctx, pool, orgID, "fin-console-user-a"); n != 1 {
		t.Fatalf("the person the list kept must hold their live grant, got %d", n)
	}

	// The audit trail: one provision row per person whose access was
	// established, one revoke row for the person the list left out, and
	// nothing for the person the list kept.
	var provisioned, revoked int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'STAFF_PROVISION'`).Scan(&provisioned); err != nil {
		t.Fatalf("the provision audit rows: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'STAFF_REVOKE'`).Scan(&revoked); err != nil {
		t.Fatalf("the revoke audit rows: %v", err)
	}
	if provisioned != 2 {
		t.Fatalf("the apply must write one provision audit row per person it linked, got %d", provisioned)
	}
	if revoked != 1 {
		t.Fatalf("the apply must write one revoke audit row for the person it revoked, got %d", revoked)
	}
	var keptRevokes int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_log a JOIN users u ON u.id = a.entity_id
		 WHERE a.action = 'STAFF_REVOKE' AND u.platform_user_id = $1`, "fin-console-user-a").Scan(&keptRevokes); err != nil {
		t.Fatalf("the kept person's revoke audit rows: %v", err)
	}
	if keptRevokes != 0 {
		t.Fatalf("the person the list kept must have no revoke audit row, got %d", keptRevokes)
	}
}

func TestIntegration_FinanceUserApplyFromEnvRefusesABadEntry(t *testing.T) {
	ctx := context.Background()
	pool, dsn := freshMigratedBoxDB(t, "finuserenvbad")
	t.Setenv("APP_ENV", "staging")
	if err := financeSeedRole(mapGetenv(financeSeedEnv(dsn))); err != nil {
		t.Fatalf("finance-seed: %v", err)
	}

	goodEmail := "fin-good-d@example.invalid"
	good := finEnvEntry(goodEmail, "fin-console-user-d", "Good Person", "financial_controller")

	// A role off the finance list refuses, naming the entry by its email
	// alone, and the good entry in the same list is rolled back with it.
	badRole := finEnvEntry("fin-bad-e@example.invalid", "fin-console-user-e", "Bad Person", "general_manager")
	env := finApplyEnv(dsn, []string{goodEmail, "fin-bad-e@example.invalid"}, good+";"+badRole)
	err, _ := finApply(t, env)
	if err == nil {
		t.Fatal("an entry with a role off the finance list must refuse")
	}
	if !strings.Contains(err.Error(), "fin-bad-e@example.invalid") {
		t.Fatalf("the refusal must name the entry by its email, got %v", err)
	}
	if strings.Contains(err.Error(), "fin-console-user-e") || strings.Contains(err.Error(), "Bad Person") {
		t.Fatalf("the refusal must carry the email alone, got %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE platform_user_id = $1`, "fin-console-user-d").Scan(&n); err != nil {
		t.Fatalf("the rolled back entry: %v", err)
	}
	if n != 0 {
		t.Fatalf("one transaction means the good entry rolled back with the bad one, found %d rows", n)
	}

	// An entry of the wrong shape refuses, naming the entry by its email.
	wrongShape := "fin-shape-f@example.invalid|fin-console-user-f"
	env = finApplyEnv(dsn, []string{"fin-shape-f@example.invalid"}, wrongShape)
	err, _ = finApply(t, env)
	if err == nil {
		t.Fatal("an entry of the wrong shape must refuse")
	}
	if !strings.Contains(err.Error(), "fin-shape-f@example.invalid") {
		t.Fatalf("the refusal must name the entry by its email, got %v", err)
	}
	if strings.Contains(err.Error(), "fin-console-user-f") {
		t.Fatalf("the refusal must carry the email alone, got %v", err)
	}

	// An email another platform subject already holds refuses, naming the
	// entry by its email.
	holder := finEnvEntry("fin-held-g@example.invalid", "fin-console-user-g", "Holder Person", "financial_controller")
	env = finApplyEnv(dsn, []string{"fin-held-g@example.invalid"}, holder)
	if err, _ := finApply(t, env); err != nil {
		t.Fatalf("the first holder must be applied: %v", err)
	}
	taker := finEnvEntry("fin-held-g@example.invalid", "fin-console-user-h", "Other Person", "financial_controller")
	env = finApplyEnv(dsn, []string{"fin-held-g@example.invalid"}, taker)
	err, _ = finApply(t, env)
	if err == nil {
		t.Fatal("an email another platform subject holds must refuse")
	}
	if !strings.Contains(err.Error(), "fin-held-g@example.invalid") {
		t.Fatalf("the refusal must name the entry by its email, got %v", err)
	}
	if strings.Contains(err.Error(), "fin-console-user-h") || strings.Contains(err.Error(), "Other Person") {
		t.Fatalf("the refusal must carry the email alone, got %v", err)
	}

	// No list at all refuses, naming the variable.
	env = finApplyEnv(dsn, []string{}, "")
	err, _ = finApply(t, env)
	if err == nil {
		t.Fatal("an empty list must refuse")
	}
	if !strings.Contains(err.Error(), finEnvUsersVar) {
		t.Fatalf("the refusal must name %s, got %v", finEnvUsersVar, err)
	}
}

// The box read's P1 and P2 on the boot apply (reviews/BOXUSERS-K3-r1.md):
// emptying the list must revoke the last person, but only when the
// entrypoint says the list was set and empty (--empty-list-revokes-all); a
// hand run with no list still refuses. A platform id listed twice is refused
// before anything is written, naming the repeated entry by its email only.
func TestIntegration_FinanceUserApplyEmptyListAndDuplicates(t *testing.T) {
	ctx := context.Background()
	pool, dsn := freshMigratedBoxDB(t, "finuseremp")
	t.Setenv("APP_ENV", "staging")
	if err := financeSeedRole(mapGetenv(financeSeedEnv(dsn))); err != nil {
		t.Fatalf("finance-seed: %v", err)
	}
	lastEmail := "fin-last-person@example.invalid"
	last := finEnvEntry(lastEmail, "fin-console-user-last", "Last Person", "financial_controller")
	if err, out := finApply(t, finApplyEnv(dsn, []string{lastEmail}, last)); err != nil {
		t.Fatalf("apply with one entry: %v (%s)", err, out)
	}

	// A duplicate platform id refuses, and nothing is written.
	dupEmail := "fin-duplicate@example.invalid"
	dup := finEnvEntry(dupEmail, "fin-console-user-last", "Duplicate", "payables_receivables")
	var before int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	err, out := finApply(t, finApplyEnv(dsn, []string{lastEmail, dupEmail}, last+";"+dup))
	if err == nil || !strings.Contains(err.Error(), dupEmail) || strings.Contains(err.Error(), "fin-console-user-last") {
		t.Fatalf("a repeated platform id must refuse naming the repeated entry's email only, got %v (%s)", err, out)
	}
	var after int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("a refused list must write nothing: audit rows %d before, %d after", before, after)
	}

	// The same person listed twice with two roles refuses too, rather than
	// the last entry silently winning.
	twice := finEnvEntry(lastEmail, "fin-console-user-last", "Last Person", "payables_receivables")
	if err, out := finApply(t, finApplyEnv(dsn, []string{lastEmail}, last+";"+twice)); err == nil || !strings.Contains(err.Error(), lastEmail) || strings.Contains(err.Error(), "fin-console-user-last") {
		t.Fatalf("a platform id listed twice must refuse naming its email only, got %v (%s)", err, out)
	}
	if roles := finLiveGrantRoles(t, ctx, pool, finOrgID(t, ctx, pool), "fin-console-user-last"); len(roles) != 1 || roles[0] != "financial_controller" {
		t.Fatalf("a refused list must leave the person's role as it was, got %v", roles)
	}

	// The emptied list without the entrypoint's word still refuses.
	emptyEnv := finApplyEnv(dsn, nil, "")
	if err, _ := finApply(t, emptyEnv); err == nil {
		t.Fatal("an empty list without --empty-list-revokes-all must refuse")
	}
	// With it, the last person is revoked.
	out2 := &bytes.Buffer{}
	if err := financeUserRole([]string{"--apply-from-env", "--empty-list-revokes-all"}, out2, out2, mapGetenv(emptyEnv)); err != nil {
		t.Fatalf("an emptied list with --empty-list-revokes-all must apply: %v (%s)", err, out2.String())
	}
	if !strings.Contains(out2.String(), "applied=0") || !strings.Contains(out2.String(), "revoked=1") {
		t.Fatalf("the emptied list must revoke the last person: %q", out2.String())
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM users WHERE platform_user_id = $1`, "fin-console-user-last").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "DEACTIVATED" {
		t.Fatalf("the last person must be DEACTIVATED, got %q", status)
	}
}

// finOrgID resolves the finance box's org anchor id.
func finOrgID(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM org WHERE slug = $1`, finOrgSlug).Scan(&id); err != nil {
		t.Fatalf("the org anchor: %v", err)
	}
	return id
}
