package main

// qdsr_fixture_test.go is the shared fixture of the Quote Desk server reads (the qd-server-reads
// lane): customer rows, a credit hold and a ledger balance planted on the composition gate
// database through plain SQL, the HTTP legs driven through the registered surface exactly as the
// role-gate matrix drives it, and the cleanup that removes every planted row with the test.
// The gate variable is HH_CORE_TEST_DATABASE_URL, absent it these legs skip like the rest of the
// composition suites.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// Fixed ids for the planted accounts, in a range no other fixture uses.
const (
	qdsrClearID  = "5d5a0000-0000-4000-8000-000000000001"
	qdsrHeldID   = "5d5a0000-0000-4000-8000-000000000002"
	qdsrOtherID  = "5d5a0000-0000-4000-8000-000000000003"
	qdsrAbsentID = "5d5a0000-0000-4000-8000-0000000000ff"
)

// qdsrAccount is one planted customer row.
type qdsrAccount struct {
	id, accountNo, name, branch string
	limitCents, balanceCents    int64
	held                        bool
}

// qdsrPlant inserts the accounts and removes them, and anything hung on them, when the test ends.
func qdsrPlant(t *testing.T, pool *pgxpool.Pool, accounts ...qdsrAccount) {
	t.Helper()
	ctx := context.Background()
	for _, a := range accounts {
		var branch any
		if a.branch != "" {
			branch = a.branch
		}
		if _, err := pool.Exec(ctx, `INSERT INTO customers (id, account_no, name, customer_type, home_branch_id,
				payment_terms_days, credit_limit_cents) VALUES ($1, $2, $3, 'CONTRACTOR', $4, 30, $5)`,
			a.id, a.accountNo, a.name, branch, a.limitCents); err != nil {
			t.Fatalf("plant customer %s: %v", a.accountNo, err)
		}
		if a.balanceCents != 0 {
			if _, err := pool.Exec(ctx, `INSERT INTO customer_transactions (customer_id, txn_type, amount_cents,
					balance_after, source_type, source_ref_id, branch_id) VALUES ($1, 'INVOICE', $2, $2, 'INVOICE', $1, $3)`,
				a.id, a.balanceCents, branch); err != nil {
				t.Fatalf("plant balance %s: %v", a.accountNo, err)
			}
		}
		if a.held {
			if _, err := pool.Exec(ctx, `INSERT INTO credit_holds (customer_id, status, hold_kind, reason)
					VALUES ($1, 'ACTIVE', 'MANUAL', 'fixture hold')`, a.id); err != nil {
				t.Fatalf("plant hold %s: %v", a.accountNo, err)
			}
		}
	}
	t.Cleanup(func() {
		for _, a := range accounts {
			for _, stmt := range []string{
				`DELETE FROM credit_holds WHERE customer_id = $1`,
				`DELETE FROM customer_transactions WHERE customer_id = $1`,
				`DELETE FROM customers WHERE id = $1`,
			} {
				if _, err := pool.Exec(context.Background(), stmt, a.id); err != nil {
					t.Errorf("cleanup %s: %v", a.accountNo, err)
				}
			}
		}
	})
}

// qdsrGet issues one GET through the registered surface as the named subject.
func qdsrGet(t *testing.T, fx *xrFixture, sub, path string) (int, string) {
	t.Helper()
	return fx.probe(t, http.MethodGet, path, rgHeaders(t, fx, sub))
}

// qdsrSend issues one request with a body through the registered surface as the named subject.
func qdsrSend(t *testing.T, fx *xrFixture, sub, method, path, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range rgHeaders(t, fx, sub) {
		req.Header.Set(k, v)
	}
	rsp := httptest.NewRecorder()
	fx.reg.inner.ServeHTTP(rsp, req)
	return rsp.Code, rsp.Body.String()
}

// qdsrJSON decodes a body the read answered with 200.
func qdsrJSON(t *testing.T, code int, body string) map[string]any {
	t.Helper()
	if code != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", code, body)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return out
}

// qdsrNorthA is the branch the inside sales fixture caller is granted.
const qdsrNorthA = testkit.BranchNorthAID
