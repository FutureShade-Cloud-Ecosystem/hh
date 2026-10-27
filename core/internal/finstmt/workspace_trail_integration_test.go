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

// The ledger: INVOICE 100000, PAYMENT -41234, INVOICE 5000, NSF +41234, the NSF fee ADJUSTMENT 2500
// (source_type payment_nsf, as till/repository.go writes it), INVOICE 1000.
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
		{"NSF", "payment_nsf", "", 41234, 105000},
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
	for _, leak := range []string{"NSF", "41234", "2500", "PAYMENT"} {
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
