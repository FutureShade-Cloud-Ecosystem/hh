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
