//go:build integration

// workspace_trail_integration_test.go, UX-93 through the real route: the customer workspace's payment
// list is held to the payment trail (ruling 87) and the customer to the caller's branch reach.
package finstmt

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	coreid "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"github.com/jackc/pgx/v5/pgxpool"
)

type trailFake bool

func (f trailFake) HoldsPaymentTrail(context.Context, string, string) (bool, error) {
	return bool(f), nil
}

func TestIntegration_Workspace_TrailAndReach(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	var rb [4]byte
	_, _ = rand.Read(rb[:])
	tag := "UX93-" + strings.ToUpper(hex.EncodeToString(rb[:]))
	seed := func(suffix, branch string) string {
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents, payment_terms_days)
			 VALUES ($1, $2, 'CONTRACTOR', $3::uuid, 500000, 30) RETURNING id::text`,
			tag+suffix, "UX-93 "+tag+suffix, branch).Scan(&id); err != nil {
			t.Fatalf("seed customer: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO payments (payment_no, customer_id, method, amount_cents, status, branch_id)
			 VALUES (nextval('payment_no_seq')::text, $1::uuid, 'CHEQUE', 41234, 'RECEIVED', $2::uuid)`, id, branch); err != nil {
			t.Fatalf("seed payment: %v", err)
		}
		t.Cleanup(func() {
			c := context.Background()
			_, _ = pool.Exec(c, `DELETE FROM payments WHERE customer_id = $1::uuid`, id)
			_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, id)
		})
		return id
	}
	own := seed("-T", trenton)
	other := seed("-K", kingston)

	call := func(held bool, id string) (int, string) {
		custRepo := customer.NewRepository(pool)
		h := NewHandler(NewService(NewRepository(pool)), slog.New(slog.DiscardHandler)).
			WithUserLinker(func(context.Context, string) (string, bool) { return "it-user", true }).
			WithPaymentTrail(trailFake(held)).
			WithCustomerReach(billing.NewCustomerModuleReach(customer.NewService(custRepo)))
		sg := testkit.NewStaffGuards(t)
		sg.SetAuthority(coreid.StaffAuthority{RoleID: "payables_receivables", Tier: 2, Scope: "branch", Branches: []string{trenton}})
		mux := http.NewServeMux()
		h.RegisterRoutes(mux, sg.Guards)
		r := httptest.NewRequest(http.MethodGet, "/api/v1/billing/ar/customers/"+id+"/workspace", nil)
		r.Header.Set("Authorization", "Bearer "+sg.Bearer(t))
		r.Header.Set("X-Org", testkit.OrgNorthSlug)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		return rec.Code, rec.Body.String()
	}

	t.Run("trail held sees the full list", func(t *testing.T) {
		code, body := call(true, own)
		if code != http.StatusOK {
			t.Fatalf("status=%d body=%s", code, body)
		}
		var w struct {
			Payments []struct {
				AmountCents int64  `json:"amount_cents"`
				Method      string `json:"method"`
			} `json:"payments"`
			Withheld bool `json:"payments_withheld"`
		}
		if err := json.Unmarshal([]byte(body), &w); err != nil {
			t.Fatal(err)
		}
		if len(w.Payments) != 1 || w.Payments[0].AmountCents != 41234 || w.Payments[0].Method != "CHEQUE" || w.Withheld {
			t.Fatalf("full list expected: %s", body)
		}
	})
	t.Run("no trail sees no payment list", func(t *testing.T) {
		code, body := call(false, own)
		if code != http.StatusOK {
			t.Fatalf("status=%d body=%s", code, body)
		}
		if strings.Contains(body, "41234") || strings.Contains(body, "CHEQUE") || strings.Contains(body, `"payments":`) {
			t.Fatalf("payment detail leaked: %s", body)
		}
		if !strings.Contains(body, `"payments_withheld":true`) {
			t.Fatalf("withheld flag missing: %s", body)
		}
	})
	t.Run("a customer of another branch is not found", func(t *testing.T) {
		for _, held := range []bool{true, false} {
			if code, body := call(held, other); code != http.StatusNotFound || strings.Contains(body, "41234") {
				t.Fatalf("held=%v status=%d body=%s", held, code, body)
			}
		}
	})
}

// -- round 2 (PR 579 review): the feed and the notes door, through the real route --------------------

func workspaceCall(t *testing.T, pool *pgxpool.Pool, held bool, link func(context.Context, string) (string, bool), method, path, body string) (int, string) {
	t.Helper()
	h := NewHandler(NewService(NewRepository(pool)), slog.New(slog.DiscardHandler)).
		WithUserLinker(link).
		WithPaymentTrail(trailFake(held)).
		WithCustomerReach(billing.NewCustomerModuleReach(customer.NewService(customer.NewRepository(pool))))
	sg := testkit.NewStaffGuards(t)
	// The platform id the guard resolves is the bearer's subject, which is not a uuid, like FB Console's.
	sg.SetAuthority(coreid.StaffAuthority{RoleID: "payables_receivables", Tier: 2, Scope: "branch", Branches: []string{trenton}})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+sg.Bearer(t))
	r.Header.Set("X-Org", testkit.OrgNorthSlug)
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	return rec.Code, rec.Body.String()
}

func seedCustomer(t *testing.T, pool *pgxpool.Pool, suffix string) string {
	t.Helper()
	ctx := context.Background()
	var rb [4]byte
	_, _ = rand.Read(rb[:])
	tag := "UX93R2-" + strings.ToUpper(hex.EncodeToString(rb[:]))
	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents, payment_terms_days)
		 VALUES ($1, $2, 'CONTRACTOR', $3::uuid, 500000, 30) RETURNING id::text`,
		tag+suffix, "UX-93 r2 "+tag+suffix, trenton).Scan(&id); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM customer_ar_notes WHERE customer_id = $1::uuid`, id)
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, id)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, id)
	})
	return id
}

// The ledger: INVOICE 100000, PAYMENT -41234, INVOICE 5000, the NSF reversal row +41234 (source
// invoice, exactly as till/repository.go writes it, excluded from the aging union because the
// reopened invoice carries the AR), the NSF fee ADJUSTMENT 2500 (source_type payment_nsf), INVOICE 1000.
func TestIntegration_Workspace_FeedGivesNoPaymentBack(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	id := seedCustomer(t, pool, "-F")
	rows := []struct {
		typ, src, memo string
		amt, bal       int64
	}{
		{"INVOICE", "invoice", "", 100000, 100000},
		{"PAYMENT", "payment", "", -41234, 58766},
		{"INVOICE", "invoice", "", 5000, 63766},
		{"NSF", "invoice", "", 41234, 105000},
		{"ADJUSTMENT", "payment_nsf", "NSF fee", 2500, 107500},
		{"INVOICE", "invoice", "", 1000, 108500},
	}
	for i, r := range rows {
		if _, err := pool.Exec(ctx,
			`INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, memo, occurred_at)
			 VALUES ($1::uuid, $2, $3, $4, $5, gen_random_uuid(), $6, now() - make_interval(hours => $7))`,
			id, r.typ, r.amt, r.bal, r.src, r.memo, len(rows)-i); err != nil {
			t.Fatalf("seed row %d: %v", i, err)
		}
	}
	link := func(context.Context, string) (string, bool) { return "a1b2c3d4-0000-4000-9000-000000000001", true }
	path := "/api/v1/billing/ar/customers/" + id + "/workspace"

	code, body := workspaceCall(t, pool, false, link, http.MethodGet, path, "")
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%s", code, body)
	}
	var w struct {
		Activity []map[string]any `json:"activity"`
	}
	if err := json.Unmarshal([]byte(body), &w); err != nil {
		t.Fatal(err)
	}
	if len(w.Activity) != 3 {
		t.Fatalf("only the three invoice rows may remain: %s", body)
	}
	for _, a := range w.Activity {
		if _, has := a["balance_after_cents"]; has {
			t.Fatalf("a running balance would give the withheld payment back: %s", body)
		}
		if a["kind"] != "INVOICE" {
			t.Fatalf("only invoice rows remain: %v", a)
		}
	}
	// The header's open figure (2500, the fee) is the account's open AR under the same union the
	// aging reads, an account fact like the invoice balances; what stays withheld is the row
	// detail: the fee's memo, the bounced payment and its amount, the running balance.
	for _, leak := range []string{"NSF", "41234", "PAYMENT"} {
		if strings.Contains(body, leak) {
			t.Fatalf("%q reached a caller without the trail: %s", leak, body)
		}
	}

	// A caller with the trail keeps every row and the running balance.
	code, body = workspaceCall(t, pool, true, link, http.MethodGet, path, "")
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%s", code, body)
	}
	if err := json.Unmarshal([]byte(body), &w); err != nil {
		t.Fatal(err)
	}
	if len(w.Activity) != 6 || !strings.Contains(body, `"balance_after_cents":108500`) || !strings.Contains(body, "NSF fee") {
		t.Fatalf("the full feed expected: %s", body)
	}
}

// An account whose only open AR is an NSF fee, written exactly as till writes it (ADJUSTMENT,
// source payment_nsf, till/repository.go): the workspace must answer 200 and the header's open
// figure must carry the fee, because the header reads the same union the aging reads (R2-P1-1).
func TestIntegration_Workspace_OpenNSFFeeAnswersOK(t *testing.T) {
	pool := intPool(t)
	id := seedCustomer(t, pool, "-FEE")
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, memo, occurred_at)
		 VALUES ($1::uuid, 'ADJUSTMENT', 2500, 2500, 'payment_nsf', gen_random_uuid(), 'NSF fee', now() - interval '1 hour')`, id); err != nil {
		t.Fatalf("seed fee: %v", err)
	}
	link := func(context.Context, string) (string, bool) { return "a1b2c3d4-0000-4000-9000-000000000001", true }
	code, body := workspaceCall(t, pool, true, link, http.MethodGet, "/api/v1/billing/ar/customers/"+id+"/workspace", "")
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%s", code, body)
	}
	var w struct {
		Header struct {
			OpenCents int64 `json:"open_cents"`
		} `json:"header"`
	}
	if err := json.Unmarshal([]byte(body), &w); err != nil {
		t.Fatal(err)
	}
	if w.Header.OpenCents != 2500 {
		t.Fatalf("header open_cents = %d, want 2500 (the open NSF fee, the same union the aging reads)", w.Header.OpenCents)
	}
}

// A platform id that is not a uuid (FB Console's are not) must not reach the author column.
func TestIntegration_Workspace_NoteSavesForANonUuidPlatformId(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	id := seedCustomer(t, pool, "-N")
	var staffUUID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users ORDER BY created_at LIMIT 1`).Scan(&staffUUID); err != nil {
		t.Fatalf("a users row: %v", err)
	}
	path := "/api/v1/billing/ar/customers/" + id + "/notes"
	linked := func(context.Context, string) (string, bool) { return staffUUID, true }
	unlinked := func(context.Context, string) (string, bool) { return "", false }

	code, body := workspaceCall(t, pool, true, linked, http.MethodPost, path, `{"body":"called, promised Friday"}`)
	if code != http.StatusCreated {
		t.Fatalf("linked: status=%d body=%s", code, body)
	}
	var got string
	if err := pool.QueryRow(ctx, `SELECT author_id::text FROM customer_ar_notes WHERE customer_id = $1::uuid ORDER BY created_at LIMIT 1`, id).Scan(&got); err != nil || got != staffUUID {
		t.Fatalf("author=%q err=%v, want the linked staff row %q", got, err, staffUUID)
	}

	// A caller with no linked staff row cannot be matched to a branch reach: not found, and no note.
	code, body = workspaceCall(t, pool, true, unlinked, http.MethodPost, path, `{"body":"second note"}`)
	if code != http.StatusNotFound {
		t.Fatalf("unlinked: status=%d body=%s", code, body)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM customer_ar_notes WHERE customer_id = $1::uuid`, id).Scan(&n); err != nil || n != 1 {
		t.Fatalf("only the linked caller's note is saved: n=%d err=%v", n, err)
	}
}

// -- UX-97: the customer workspace follows the dealer's account position policy -----------------------

func TestIntegration_Workspace_AccountPositionPolicy(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	id := seedCustomer(t, pool, "-P")
	if _, err := pool.Exec(ctx,
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                       subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at)
		 VALUES ('INV-UX97-'||$1, $1::uuid, $2::uuid, 'FINALIZED',
		         (now() AT TIME ZONE 'America/Toronto')::date - 45, (now() AT TIME ZONE 'America/Toronto')::date - 15, 88000, 0, 88000, 88000, now())`,
		id, trenton); err != nil {
		t.Fatalf("seed overdue invoice: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, occurred_at)
		 VALUES ($1::uuid, 'INVOICE', 88000, 88000, 'invoice', gen_random_uuid(), now())`, id); err != nil {
		t.Fatalf("seed customer_transactions: %v", err)
	}
	var runID, stmtID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO ar_statement_runs (mode, as_of_date, status, generated_count, queued_count)
		 VALUES ('MASS', CURRENT_DATE, 'COMPLETED', 1, 0) RETURNING id::text`).Scan(&runID); err != nil {
		t.Fatalf("seed statement run: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO ar_statements (run_id, customer_id, as_of_date, current_cents, days_30_cents, days_60_cents, days_90_plus_cents, closing_balance_cents, html, delivery_status)
		 VALUES ($1::uuid, $2::uuid, CURRENT_DATE, 0, 88000, 0, 0, 88000, '<p>88000</p>', 'NOT_QUEUED') RETURNING id::text`,
		runID, id).Scan(&stmtID); err != nil {
		t.Fatalf("seed statement: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM ar_statements WHERE id = $1::uuid`, stmtID)
		_, _ = pool.Exec(c, `DELETE FROM ar_statement_runs WHERE id = $1::uuid`, runID)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1::uuid`, id)
	})

	link := func(context.Context, string) (string, bool) { return "a1b2c3d4-0000-4000-9000-000000000001", true }
	path := "/api/v1/billing/ar/customers/" + id + "/workspace"
	previewPath := "/api/v1/billing/ar/customers/" + id + "/statements/" + stmtID + "/preview"

	callPath := func(tier int, role, reqPath string) (int, string) {
		h := NewHandler(NewService(NewRepository(pool)), slog.New(slog.DiscardHandler)).
			WithUserLinker(link).WithPaymentTrail(trailFake(true)).
			WithCustomerReach(billing.NewCustomerModuleReach(customer.NewService(customer.NewRepository(pool))))
		sg := testkit.NewStaffGuards(t)
		sg.SetAuthority(coreid.StaffAuthority{RoleID: role, Tier: tier, Scope: "branch", Branches: []string{trenton}})
		mux := http.NewServeMux()
		h.RegisterRoutes(mux, sg.Guards)
		r := httptest.NewRequest(http.MethodGet, reqPath, nil)
		r.Header.Set("Authorization", "Bearer "+sg.Bearer(t))
		r.Header.Set("X-Org", testkit.OrgNorthSlug)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		return rec.Code, rec.Body.String()
	}
	setDefault := func(v string) {
		if _, err := pool.Exec(ctx, `UPDATE ar_invoice_policy SET account_position_visibility = $1 WHERE branch_id IS NULL`, v); err != nil {
			t.Fatalf("set policy: %v", err)
		}
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `UPDATE ar_invoice_policy SET account_position_visibility = 'MANAGER_UP' WHERE branch_id IS NULL`)
		_, _ = pool.Exec(c, `DELETE FROM ar_invoice_policy WHERE branch_id = $1::uuid`, trenton)
	})
	figureKeys := []string{
		`"credit_limit_cents"`, `"open_cents"`, `"available_credit_cents"`, `"overdue_cents"`,
		`"utilization_bps"`, `"aging"`, `"days_overdue"`, `"days_overdue_bucket"`,
		`"closing_balance_cents"`, `"balance_after_cents"`,
	}
	previewFigureKeys := []string{
		`"aging"`, `"current_cents"`, `"days_30_cents"`, `"days_60_cents"`, `"days_90_plus_cents"`,
		`"closing_balance_cents"`, `"days_overdue"`, `"bucket_label"`,
	}
	check := func(label string, tier int, role string, wantFigures bool) {
		t.Helper()
		code, body := callPath(tier, role, path)
		if code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", label, code, body)
		}
		for _, k := range figureKeys {
			if has := strings.Contains(body, k); has != wantFigures {
				t.Errorf("%s: workspace %s present=%v, want %v: %s", label, k, has, wantFigures, body)
			}
		}
		for _, keep := range []string{`"invoice_no":"INV-UX97-`, `"issue_date":`, `"due_date":`, `"total_cents":88000`, `"balance_cents":88000`} {
			if !strings.Contains(body, keep) {
				t.Errorf("%s: workspace must keep invoice document figure %s: %s", label, keep, body)
			}
		}
		if wantFigures != strings.Contains(body, `"account_position_withheld":false`) {
			t.Errorf("%s: the withheld flag disagrees: %s", label, body)
		}

		pCode, pBody := callPath(tier, role, previewPath)
		if pCode != http.StatusOK {
			t.Fatalf("%s: preview status=%d body=%s", label, pCode, pBody)
		}
		for _, k := range previewFigureKeys {
			if has := strings.Contains(pBody, k); has != wantFigures {
				t.Errorf("%s: preview %s present=%v, want %v: %s", label, k, has, wantFigures, pBody)
			}
		}
		for _, keep := range []string{`"invoice_no":"INV-UX97-`, `"due_date":`, `"balance_cents":88000`} {
			if !strings.Contains(pBody, keep) {
				t.Errorf("%s: preview must keep invoice document figure %s: %s", label, keep, pBody)
			}
		}
	}

	// Default box first (nothing set by hand, MANAGER_UP as migrated).
	check("default MANAGER_UP clerk", 3, "inside_sales", false)
	check("default MANAGER_UP manager", 2, "payables_receivables", true)
	setDefault("ALL_STAFF")
	check("ALL_STAFF clerk", 3, "inside_sales", true)
	setDefault("HIDDEN")
	check("HIDDEN manager", 2, "payables_receivables", false)
	// The customer's home branch row decides: HIDDEN there refuses even under a permissive default.
	setDefault("ALL_STAFF")
	if _, err := pool.Exec(ctx, `INSERT INTO ar_invoice_policy (branch_id, account_position_visibility) VALUES ($1::uuid, 'HIDDEN')
		ON CONFLICT (branch_id) DO UPDATE SET account_position_visibility = 'HIDDEN'`, trenton); err != nil {
		t.Fatal(err)
	}
	check("HIDDEN home branch under ALL_STAFF default, manager", 2, "payables_receivables", false)
	check("HIDDEN home branch under ALL_STAFF default, clerk", 3, "inside_sales", false)
}
