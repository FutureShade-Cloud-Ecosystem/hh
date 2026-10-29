//go:build integration

package main

// w2_6b_fix_starter_codes_integration_test.go, W2-6b fix round 2: every starter code the seed gives a
// branch points at an expense account of its own.
//
// Round 1 shipped four starter codes. Postage pointed at 5100 Freight-In and staff meals at 6000
// Payroll, so two spends landed on the wrong P&L line: postage is not cost of goods, and staff meals
// are not payroll. The fourth code was CASH_REFUND, a customer cash refund at 2300 Customer
// Deposits, a liability. A cash refund to a customer goes through the returns flow, with its own
// document and its own tax reversal, so it is never a drawer paid out, and it is not an expense.
//
// A list that is off today is turned on some day. The starter list therefore has to be right on the
// day it ships, because the day a branch turns a code on is the day that spend starts landing on that
// P&L line.
//
// The legs:
//   - every starter code the seed gives a branch points at an account the chart holds, that is live,
//     and that is of type EXPENSE
//   - no starter code is a drawer cash refund
//   - the starter list is the three spends, each at its own expense account, and each account carries
//     the name a dealer's accountant recognises
//   - the list ships off: a branch turns a code on through the till's own list API
//
// The legs run on a real migrated database seeded with the default build's Ontario Dibbits seed, every
// step through the real services wired exactly as cmd/core wires them (wireMoney and wireOperations
// over one mux; no fakes for money).
//
//	Run: DATABASE_URL='postgres://postgres@127.0.0.1:57733/postgres?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -run W2B6FixStarterCodes -count=1 -v

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	coreid "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// w2b6StarterMigration is the migration file this item edits, read by the dealer chart leg so the leg
// proves the file as it stands and not a copy of it.
const w2b6StarterMigration = "0451_till_counter_reason_codes.sql"

// w2b6StarterAccounts are the three spends the drawer names, and the account each one posts to. The
// name is the part that matters: a dealer's accountant reads the chart, not the code.
var w2b6StarterAccounts = []struct {
	code        string
	kind        string
	accountName string
}{
	{"POSTAGE", "PAID_OUT", "Office Supplies and Postage"},
	{"CLEANING", "PAID_OUT", "Cleaning and Janitorial"},
	{"STAFF_MEALS", "PAID_OUT", "Staff Meals and Amenities"},
}

// TestW2B6FixStarterCodes is the fix's exit test: every starter code the seed gives a branch points at
// an expense account of its own, and none of them is a drawer cash refund.
func TestW2B6FixStarterCodes(t *testing.T) {
	w := newW2B6StarterWalk(t)
	ctx := context.Background()

	// 1. The starter list the seed gives the branch, read through the till's own API as a manager.
	items := w.listReasonCodes(w2b6TRT)
	if len(items) == 0 {
		t.Fatalf("the seed gave the branch no reason codes at all")
	}

	// 2. Every starter code points at an account the chart holds, that is live, and that is an expense.
	//    A code that lands on a liability, on an account nobody owns, or on an account that has been
	//    turned off puts money on the wrong line the day the branch turns the code on.
	for _, item := range items {
		var accountType string
		var active bool
		err := w.pool.QueryRow(ctx,
			`SELECT account_type, is_active FROM gl_accounts WHERE code = $1`, item.GLAccountCode).
			Scan(&accountType, &active)
		if errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("the starter code %s points at the account %s, which is not in the chart", item.Code, item.GLAccountCode)
			continue
		}
		if err != nil {
			t.Fatalf("reading the account %s: %v", item.GLAccountCode, err)
		}
		if accountType != "EXPENSE" {
			t.Errorf("the starter code %s points at the account %s, which is a %s, want an expense", item.Code, item.GLAccountCode, accountType)
		}
		if !active {
			t.Errorf("the starter code %s points at the account %s, which is turned off", item.Code, item.GLAccountCode)
		}
	}

	// 3. No starter code is a drawer cash refund. A refund to a customer goes through the returns
	//    flow, with its document and its tax reversal; it is never a drawer paid out.
	for _, item := range items {
		if item.Code == "CASH_REFUND" {
			t.Errorf("the starter list carries the code CASH_REFUND, a drawer cash refund; a refund to a customer goes through the returns flow")
		}
	}

	// 4. The starter list is the three spends, each at its own expense account.
	if len(items) != len(w2b6StarterAccounts) {
		t.Errorf("the seed gave the branch %d starter codes, want the %d spends: %+v", len(items), len(w2b6StarterAccounts), items)
	}
	for _, want := range w2b6StarterAccounts {
		item, held := w.codeMaybe(w2b6TRT, want.code)
		if !held {
			t.Errorf("the starter list carries no %s code", want.code)
			continue
		}
		if item.MovementKind != want.kind {
			t.Errorf("the starter code %s is a %s, want a %s", want.code, item.MovementKind, want.kind)
		}
		// The account it posts to is named for what the spend is, so the branch is never guessing
		// which line of the P&L it is about to open.
		var name string
		err := w.pool.QueryRow(ctx,
			`SELECT name FROM gl_accounts WHERE code = $1`, item.GLAccountCode).Scan(&name)
		if errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("the starter code %s points at the account %s, which is not in the chart", want.code, item.GLAccountCode)
			continue
		}
		if err != nil {
			t.Fatalf("reading the account of the starter code %s: %v", want.code, err)
		}
		if name != want.accountName {
			t.Errorf("the starter code %s posts to the account %s (%s), want the account %s", want.code, item.GLAccountCode, name, want.accountName)
		}
	}

	// 5. The three accounts are three accounts of their own, one per spend, and each one is a live
	//    expense in the chart.
	for _, want := range w2b6StarterAccounts {
		var count int
		if err := w.pool.QueryRow(ctx,
			`SELECT count(*) FROM gl_accounts WHERE name = $1`, want.accountName).Scan(&count); err != nil {
			t.Fatalf("reading the account %s in the chart: %v", want.accountName, err)
		}
		if count != 1 {
			t.Errorf("the chart holds %d accounts named %s, want exactly one", count, want.accountName)
			continue
		}
		var accountType string
		var active bool
		if err := w.pool.QueryRow(ctx,
			`SELECT account_type, is_active FROM gl_accounts WHERE name = $1`, want.accountName).
			Scan(&accountType, &active); err != nil {
			t.Fatalf("reading the account %s in the chart: %v", want.accountName, err)
		}
		if accountType != "EXPENSE" {
			t.Errorf("the account %s is a %s, want an expense", want.accountName, accountType)
		}
		if !active {
			t.Errorf("the account %s is turned off", want.accountName)
		}
	}

	// 6. The list ships off. A branch that has not turned a code on keeps today's behaviour, so the
	//    starter rows are inactive until a manager turns one on through the till's own list API.
	for _, item := range items {
		if item.Active {
			t.Errorf("the starter code %s is live; the starter list ships off until a manager turns it on", item.Code)
		}
	}
}

// TestW2B6FixStarterCodesDealerChart is the guard's leg: a dealer chart that already holds one of the
// three numbers under its own meaning keeps that account, and the starter list is not pointed at it.
//
// The numbers are free in the standard chart, but a dealer brings a chart of their own. The migration
// must never overwrite an account a dealer already carries, and it must never point a starter code at
// an account the dealer gave another meaning either: a POSTAGE code that posts to the dealer's own
// 6400 would put postage on somebody else's P&L line the day the branch turns the code on.
//
// The leg runs the migration file itself, on a database built without it, and then runs its two
// inserts a second time, because both are idempotent and a migration that lands twice must leave the
// chart and the list exactly as the first landing left them.
func TestW2B6FixStarterCodesDealerChart(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDBWithout(t, "w2b6dealer", w2b6StarterMigration)

	// The dealer's chart already holds 6400, under a meaning of their own.
	mustExec(t, ctx, pool,
		`INSERT INTO gl_accounts (code, name, account_type, normal_balance)
		 VALUES ('6400', 'Fuel and Vehicle Costs', 'EXPENSE', 'DEBIT')`)

	body, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", w2b6StarterMigration))
	if err != nil {
		t.Fatalf("reading the migration: %v", err)
	}
	if _, err := pool.Exec(ctx, string(body)); err != nil {
		t.Fatalf("applying the migration: %v", err)
	}

	// The dealer's account is exactly as the dealer left it.
	var name, accountType string
	var active bool
	if err := pool.QueryRow(ctx,
		`SELECT name, account_type, is_active FROM gl_accounts WHERE code = '6400'`).
		Scan(&name, &accountType, &active); err != nil {
		t.Fatalf("reading the dealer's account 6400: %v", err)
	}
	if name != "Fuel and Vehicle Costs" || accountType != "EXPENSE" || !active {
		t.Errorf("the migration wrote over the dealer's account 6400: it is now %s (%s, active=%t), want Fuel and Vehicle Costs (EXPENSE, active=true)",
			name, accountType, active)
	}

	// The starter list is not pointed at the dealer's account: no POSTAGE code is planted at all.
	var postage int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM till_counter_reason_codes WHERE code = 'POSTAGE'`).Scan(&postage); err != nil {
		t.Fatalf("reading the POSTAGE starter rows: %v", err)
	}
	if postage != 0 {
		t.Errorf("the migration planted %d POSTAGE starter codes against the dealer's own 6400, want none", postage)
	}

	// The other two spends are still planted: one colliding number costs the branch one starter code,
	// not the whole list.
	for code, account := range map[string]string{"CLEANING": "6500", "STAFF_MEALS": "6600"} {
		var planted int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM till_counter_reason_codes WHERE code = $1 AND gl_account_code = $2`,
			code, account).Scan(&planted); err != nil {
			t.Fatalf("reading the %s starter rows: %v", code, err)
		}
		if planted == 0 {
			t.Errorf("the migration planted no %s starter code at %s, want one", code, account)
		}
	}

	// The two inserts are idempotent. Running them again leaves the chart and the list exactly as
	// the first landing left them.
	inserts := string(body)
	at := strings.Index(inserts, "INSERT INTO gl_accounts")
	if at < 0 {
		t.Fatalf("the migration holds no account insert")
	}
	if _, err := pool.Exec(ctx, inserts[at:]); err != nil {
		t.Fatalf("applying the migration's inserts a second time: %v", err)
	}
	var accounts int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM gl_accounts WHERE code = '6400'`).Scan(&accounts); err != nil {
		t.Fatalf("counting the accounts under 6400: %v", err)
	}
	if accounts != 1 {
		t.Errorf("the second landing left %d accounts under 6400, want exactly one", accounts)
	}
	if err := pool.QueryRow(ctx,
		`SELECT name FROM gl_accounts WHERE code = '6400'`).Scan(&name); err != nil {
		t.Fatalf("reading the dealer's account 6400 again: %v", err)
	}
	if name != "Fuel and Vehicle Costs" {
		t.Errorf("the second landing wrote over the dealer's account 6400: it is now %s", name)
	}
	for _, one := range []struct {
		name string
		want int
	}{
		// The number 6400 is the dealer's, so the chart holds no office supplies account at all:
		// the migration does not give the number a second meaning, and it does not rename the
		// dealer's account either.
		{"Office Supplies and Postage", 0},
		{"Cleaning and Janitorial", 1},
		{"Staff Meals and Amenities", 1},
	} {
		var count int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM gl_accounts WHERE name = $1`, one.name).Scan(&count); err != nil {
			t.Fatalf("counting the accounts named %s: %v", one.name, err)
		}
		if count != one.want {
			t.Errorf("the chart holds %d accounts named %s, want %d", count, one.name, one.want)
		}
	}
}

// newW2B6StarterWalk is the starter code legs' world: a real migrated database, the seed, and the
// production wiring over one mux. It is a world of its own on a database of its own, so these legs and
// round 1's legs can run in one package run without fighting over one database.
func newW2B6StarterWalk(t *testing.T) *w2b6Walk {
	t.Helper()
	ctx := context.Background()
	pool := freshMigratedDB(t, "w2b6starter")
	body, err := seedFS.ReadFile("dibbits_seed.sql")
	if err != nil {
		t.Fatalf("reading the embedded seed: %v", err)
	}
	if _, err := pool.Exec(ctx, string(body)); err != nil {
		t.Fatalf("applying the seed: %v", err)
	}
	mustExec(t, ctx, pool, `INSERT INTO org (id, slug, name) VALUES ($1, $2, 'W2B6 Starter Org') ON CONFLICT (id) DO NOTHING`, w2b6OrgID, w2b6OrgSlug)
	mustExec(t, ctx, pool, `UPDATE locations SET org_id = $1 WHERE loc_type = 'BRANCH'`, w2b6OrgID)
	mustExec(t, ctx, pool, `UPDATE users SET platform_user_id = $1 WHERE id = 'd7000000-0000-4000-9000-000000000006'`, testkit.SubSalesManager)

	sg := testkit.NewStaffGuards(t)
	sg.Harness.Membership.SetMember(testkit.SubSalesManager, w2b6OrgSlug, true)
	sg.Harness.Resolver.Set(w2b6OrgSlug, w2b6OrgID)
	sg.SetAuthority(coreid.StaffAuthority{RoleID: "financial_controller", Tier: coreid.TierLeadership, Scope: coreid.ScopeGlobal})

	log := slog.New(slog.NewTextHandler(w2b6Writer{t}, nil))
	mux := http.NewServeMux()
	belt := buildSmsBelt(pool, log)
	money := wireMoney(mux, pool, sg.Guards, log, "", mustRoleConfigForWire())
	wireOperations(mux, pool, sg.Guards, log, money, "w2b6starter-fx-ticket", belt.outbox)
	// The GL's own read seams, so the seed's invoice events post instead of dead lettering, exactly as
	// round 1's legs' world does.
	billingRepo := billing.NewRepository(pool)
	posRepo := pos.NewRepository(pool)
	money.glSvc.WithInvoiceReader(billingRepo).WithARSubledger(billingRepo).WithTaxSplitReader(posRepo)

	d := notify.NewDispatcher(money.outboxStore, log)
	money.glSvc.RegisterPostingHandlers(d)

	return &w2b6Walk{
		t:     t,
		pool:  pool,
		mux:   mux,
		sg:    sg,
		disp:  d,
		token: sg.Harness.Minter.StaffToken(t, testkit.SubSalesManager),
	}
}
