//go:build integration

package main

// finance_check_integration_test.go, the finance box's read only check
// (W2-1b) proved through the production wiring.
//
// The check runs the month walk's own ties (fw1_ties.go) for every closed
// month and for the month the house is trading in, prints one line per tie
// per month, and exits non zero on any FAIL. The legs here are:
//
//   - the guard: the check refuses before it reads anything when the
//     database is not marked as the finance box;
//   - the wiring: the check reads through the money, operations and tops
//     composition the serve role builds, through that wiring's own GL
//     service, billing repository and tax engine, not through a copy of them;
//   - the shape: one line per tie per month, PASS or FAIL with the variance
//     in cents, and SKIP where a tie has nothing to say about the month;
//   - the write: the check writes nothing, proved by counting the rows of
//     the money tables before and after the run;
//   - the exit: a failing tie makes the role answer non zero.
//
// The Ontario seed's own books do not tie (the seed's opening receivable is
// not in the ledger and the seed leaves two events in the outbox), so the
// seeded world's legs expect FAILs and a non zero answer, and say which
// ones.

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/config"
	coreid "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// financeCheckTables are the tables the check reads, and the tables whose
// row counts prove it wrote nothing.
var financeCheckTables = []string{
	"gl_journals", "gl_journal_lines", "invoices", "customer_transactions",
	"bank_reconciliations", "ar_statements", "ar_statement_runs",
	"stock_balances", "events_outbox", "audit_log", "fiscal_periods",
}

// financeCheckCounts answers the row count of every table the check reads.
func financeCheckCounts(t *testing.T, ctx context.Context, pool *pgxpool.Pool) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, table := range financeCheckTables {
		var n int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			t.Fatalf("counting %s: %v", table, err)
		}
		out[table] = n
	}
	return out
}

func TestIntegration_FinanceCheck(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "fincheck")
	dsn := swapDatabase(os.Getenv("DATABASE_URL"), "demoseed_it_fincheck")

	// The Ontario dealer world, through the seed role itself.
	seedEnv := func(k string) string {
		switch k {
		case "DATABASE_URL", "CORE_DATABASE_URL":
			return dsn
		case "APP_ENV":
			return "dev"
		}
		return ""
	}
	if err := seedRole(seedEnv); err != nil {
		t.Fatalf("seed: %v", err)
	}

	env := finEnvFor(dsn, nil, nil)

	// The guard: a database that is not marked as the finance box is refused
	// before the check reads a single row.
	out := &bytes.Buffer{}
	err := financeCheckRole(nil, out, out, env)
	if err == nil || !strings.Contains(err.Error(), "box_kind") {
		t.Fatalf("the guard: a refusal naming the box kind, got %v", err)
	}

	// The box is the finance box now.
	if err := settings.WriteBoxKindTx(ctx, pool, "finance"); err != nil {
		t.Fatalf("marking the box: %v", err)
	}

	// The production wiring: the money, operations and tops composition the
	// serve role builds, over the seeded world.
	orgID, _, err := coreid.ProvisionOrgAnchor(ctx, pool, finOrgSlug, "Finance Box")
	if err != nil {
		t.Fatalf("the org anchor: %v", err)
	}
	sg := testkit.NewStaffGuards(t)
	sg.Harness.Membership.SetMember(testkit.SubSalesManager, finOrgSlug, true)
	sg.Harness.Resolver.Set(finOrgSlug, orgID)
	sg.SetAuthority(coreid.StaffAuthority{RoleID: "financial_controller", Tier: coreid.TierLeadership, Scope: coreid.ScopeGlobal})
	log := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	vaultKey := dashVaultKey(t)
	vault, err := payment.NewVault(vaultKey)
	if err != nil {
		t.Fatalf("the vault: %v", err)
	}
	if err := (payment.NewStore(pool, vault)).Configure(ctx, "run_payments", "MID-FINCHECK", "CAD", "", 0, false,
		map[string]string{"api_key": "fin-check-not-a-secret"}, ""); err != nil {
		t.Fatalf("configuring the card rail: %v", err)
	}
	mux := http.NewServeMux()
	belt := buildSmsBelt(pool, log)
	money := wireMoney(mux, pool, sg.Guards, log, "", mustRoleConfigForWire())
	ops := wireOperations(mux, pool, sg.Guards, log, money, "fin-check-fx-ticket", belt.outbox)
	tops := wireTops(mux, pool, sg.Guards, log, money, ops, config.Config{PaymentVaultKey: vaultKey}, mustRoleConfigForWire(), belt)

	// The check runs in one read only transaction.
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatalf("the read only transaction: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	before := financeCheckCounts(t, ctx, pool)
	out.Reset()
	failed, err := financeCheckRunWired(ctx, pool, tx, out, &financeCheckWiring{
		GL: money.glSvc, Billing: tops.billingRepo, Tax: tops.taxSvc,
	})
	if err != nil {
		t.Fatalf("the check: %v", err)
	}
	after := financeCheckCounts(t, ctx, pool)

	// The check wrote nothing.
	for table, n := range before {
		if after[table] != n {
			t.Fatalf("the check wrote to %s: %d rows before, %d after", table, n, after[table])
		}
	}

	// The shape: one line per tie per month, and every line says PASS, FAIL
	// with the variance in cents, or SKIP.
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("the check printed no lines")
	}
	months := map[string]bool{}
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			t.Fatalf("the check printed a line that names no month and no tie: %q", line)
		}
		months[fields[0]] = true
		switch fields[2] {
		case "PASS", "SKIP":
		case "FAIL":
			if len(fields) < 4 {
				t.Fatalf("the check printed a FAIL without a variance: %q", line)
			}
			var variance int64
			if _, err := fmt.Sscanf(fields[3], "%d", &variance); err != nil {
				t.Fatalf("the check printed a FAIL whose variance is not a number: %q", line)
			}
		default:
			t.Fatalf("the check printed a line that is neither PASS, FAIL nor SKIP: %q", line)
		}
	}
	if len(months) < 1 {
		t.Fatalf("the check ran for no month at all")
	}
	// The check reads the month in the dealer's zone, so the expectation does too: the process clock's
	// month is a month ahead on a month's last evening (the month end class).
	houseNow := time.Now().In(houseday.Location())
	if !months[fmt.Sprintf("%04d-%02d", houseNow.Year(), int(houseNow.Month()))] {
		t.Fatalf("the check did not run for the month the house is trading in:\n%s", out.String())
	}

	// The Ontario seed's books do not tie: the seed's opening receivable is
	// not in the ledger and the seed leaves two events in the outbox. The
	// check has to say so, and to name the variance in cents.
	if failed == 0 {
		t.Fatalf("the check found no failing tie on the seeded world, which leaves its opening receivable out of the ledger:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "FAIL ") {
		t.Fatalf("the check did not report the seeded world's untied books:\n%s", out.String())
	}

	// The exit: the role answers non zero when a tie fails.
	out.Reset()
	err = financeCheckRole(nil, out, out, env)
	if err == nil {
		t.Fatalf("the role answered zero on a world with failing ties:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "tie") {
		t.Fatalf("the role's refusal does not name the failing ties: %v", err)
	}

	// A closed month is a month the check runs too. The seeded world has no
	// closed month (the seed opens its house year), so the leg closes one:
	// the check has to cover it on the next run.
	zone, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatalf("the dealer zone: %v", err)
	}
	// The month before steps back from the first of this month, never from today's day of month, which on
	// the 31st after a 30 day month normalizes into the current month.
	nowLocal := time.Now().In(zone)
	closed := time.Date(nowLocal.Year(), nowLocal.Month(), 1, 0, 0, 0, 0, zone).AddDate(0, -1, 0)
	if _, err := pool.Exec(ctx, `UPDATE fiscal_periods SET status='CLOSED' WHERE year=$1 AND month=$2`,
		closed.Year(), int(closed.Month())); err != nil {
		t.Fatalf("closing a period for the leg: %v", err)
	}
	out.Reset()
	if _, err := financeCheckRunWired(ctx, pool, tx, out, &financeCheckWiring{
		GL: money.glSvc, Billing: tops.billingRepo, Tax: tops.taxSvc,
	}); err != nil {
		t.Fatalf("the check over a closed month: %v", err)
	}
	if !strings.Contains(out.String(), fmt.Sprintf("%04d-%02d", closed.Year(), int(closed.Month()))) {
		t.Fatalf("the check did not run for the closed month %04d-%02d:\n%s", closed.Year(), int(closed.Month()), out.String())
	}
}
