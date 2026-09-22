//go:build integration

// dib478_report_contract_integration_test.go, the parameter names `payments.by_day` binds are a
// contract with a SEEDED ROW, and this file is the only thing that checks it.
//
//	DATABASE_URL=... go test -tags integration -count=1 ./internal/assist/adapters/
//
// WHY THIS FILE EXISTS. money.go reaches the cash-receipts figures by running reporting's
// `payments_by_day` template, and it passes its parameters as a map[string]string. Nothing about
// that map is type-checked: reporting validates it against the template's declared ParamSpecs at
// RUN time and answers `unknown report parameter "x"`, a 400, when a name does not match.
//
// Every unit test in this package hands the adapter a FAKE ReportRunner, and a fake accepts whatever
// map it is given. So the whole class of "the adapter and the template disagree about what the
// parameters are called" is invisible to them, by construction.
//
// It is not hypothetical. This seam shipped binding "from"/"to", the field names on
// assist.PaymentRangeQuery, which is where the value comes from, against a template that declares
// "from_date"/"to_date". Unit tests: green. Live server: `payments.by_day` returned an error for
// every question anyone asked it, and the DIB-478 walkthrough found it on the first "and last week?".
//
// The test reads the parameter names out of the DATABASE rather than restating them, so it fails if
// either side moves: rename the constants in money.go and it fails; change the seeded param_schema
// in a migration and it fails.

package adapters

import (
	"context"
	"encoding/json"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/reporting"
	"github.com/jackc/pgx/v5/pgxpool"
)

// reportingServiceForTest builds the SHIPPED reporting service over the test pool, the same
// constructor main.go calls, so the template catalogue under test is the one that ships.
func reportingServiceForTest(t *testing.T, pool *pgxpool.Pool) *reporting.Service {
	t.Helper()
	return reporting.NewService(reporting.NewRepository(pool))
}

// reportingCtxForTest stamps a GLOBAL leadership caller exactly as the HTTP guard does. Global,
// because this test is about the template's SHAPE and not about the branch fence, which has its own
// tests, in the file next door, where a scoped caller is the point.
func reportingCtxForTest(t *testing.T) context.Context {
	t.Helper()
	return identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID:   "00000000-0000-4000-9000-0000000000aa",
		RoleCode: "GENERAL_MANAGER",
		Tier:     identity.TierLeadership,
		Scope:    identity.ScopeGlobal,
		Status:   "ACTIVE",
	})
}

// TestPaymentsByDayBindsTheTemplatesOwnParameterNames pins money.go's three parameter constants to
// the names the seeded template actually declares.
func TestPaymentsByDayBindsTheTemplatesOwnParameterNames(t *testing.T) {
	pool := testPool(t)

	var raw []byte
	err := pool.QueryRow(context.Background(),
		`SELECT param_schema FROM report_templates WHERE code = $1`, paymentsByDayTemplate).Scan(&raw)
	if err != nil {
		t.Fatalf("template %q is not seeded, the adapter's report code is wrong: %v", paymentsByDayTemplate, err)
	}

	var specs []struct {
		Name     string `json:"name"`
		Required bool   `json:"required"`
	}
	if err := json.Unmarshal(raw, &specs); err != nil {
		t.Fatalf("param_schema is not the array reporting.ParamSpec expects: %v", err)
	}

	declared := make(map[string]bool, len(specs))
	for _, s := range specs {
		declared[s.Name] = true
	}

	// Each constant must BE one of the declared names. bindParams refuses an unknown name outright,
	// so a miss here is a 400 on a live server and nothing less.
	for _, name := range []string{paramFromDate, paramToDate, paramBranchID} {
		if !declared[name] {
			t.Errorf("adapter binds %q, which %s does not declare; declared: %v",
				name, paymentsByDayTemplate, keysOf(declared))
		}
	}

	// The two date parameters are REQUIRED by the template, which is the other half of the contract:
	// a range command that omitted one would be refused, so PaymentsByDay must always send both.
	for _, s := range specs {
		if (s.Name == paramFromDate || s.Name == paramToDate) && !s.Required {
			t.Errorf("%s: %q is no longer required, PaymentsByDay assumes both dates are mandatory", paymentsByDayTemplate, s.Name)
		}
	}
}

// TestPaymentsByDayReadsTheTemplatesOwnColumnNames does the same for the three columns money.go
// looks up by key. columnIndex returns -1 for a name the result does not carry, and cellInt/cellString
// answer zero for -1, so a renamed column does not error, it QUIETLY REPORTS ZERO PAYMENTS, which is
// worse than an error and is exactly why this assertion is separate from the one above.
func TestPaymentsByDayReadsTheTemplatesOwnColumnNames(t *testing.T) {
	pool := testPool(t)

	// The columns live in the compiled catalog, not the row, so they are proved the only way an
	// adapter can prove them: run the report and look at what came back.
	svc := reportingServiceForTest(t, pool)
	res, err := svc.Run(reportingCtxForTest(t), paymentsByDayTemplate, map[string]string{
		paramFromDate: "2026-08-01",
		paramToDate:   "2026-08-31",
	})
	if err != nil {
		t.Fatalf("running %s with the adapter's own parameter names failed: %v", paymentsByDayTemplate, err)
	}
	for _, key := range []string{"day", "payment_count", "amount_cents"} {
		if columnIndex(res.Columns, key) < 0 {
			t.Errorf("column %q is gone from %s, PaymentsByDay would silently report zeroes", key, paymentsByDayTemplate)
		}
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
