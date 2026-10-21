//go:build integration

// platform_id_uuid_integration_test.go, UX-96 through the real routes: a platform id (FB Console issues a
// 20 character hex id, not a uuid) is never written into a uuid column. The credit hold and the paylink
// delivery write the caller's linked staff users.id, and a caller with no linked staff row is refused
// with nothing written.
package finstmt

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	coreid "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"github.com/jackc/pgx/v5/pgxpool"
)

// consoleSubject is shaped like the subject the staging console issues: 20 hex characters, never a uuid.
const consoleSubject = "5f3a9c1e7b2d4a6c8e0f"

func consoleCall(t *testing.T, pool *pgxpool.Pool, link func(context.Context, string) (string, bool), path, body string) (int, string) {
	t.Helper()
	h := NewHandler(NewService(NewRepository(pool)), slog.New(slog.DiscardHandler)).WithUserLinker(link)
	sg := testkit.NewStaffGuards(t)
	sg.Harness.Membership.SetMember(consoleSubject, testkit.OrgNorthSlug, true)
	sg.SetAuthority(coreid.StaffAuthority{RoleID: "payables_receivables", Tier: 2, Scope: "branch", Branches: []string{trenton}})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+sg.Harness.Minter.StaffToken(t, consoleSubject))
	r.Header.Set("X-Org", testkit.OrgNorthSlug)
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	return rec.Code, rec.Body.String()
}

func TestIntegration_CreditHold_NonUuidPlatformId(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	id := seedCustomer(t, pool, "-H")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM credit_holds WHERE customer_id = $1::uuid`, id)
	})
	var staffUUID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users ORDER BY created_at LIMIT 1`).Scan(&staffUUID); err != nil {
		t.Fatalf("a users row: %v", err)
	}
	body := `{"customer_id":"` + id + `","reason":"UX-96 manual hold"}`
	const path = "/api/v1/ar/holds"
	count := func() int {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM credit_holds WHERE customer_id = $1::uuid`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// A caller with no linked staff row is refused and nothing is written.
	unlinked := func(context.Context, string) (string, bool) { return "", false }
	if code, b := consoleCall(t, pool, unlinked, path, body); code != http.StatusForbidden || count() != 0 {
		t.Fatalf("unlinked: status=%d rows=%d body=%s", code, count(), b)
	}

	linked := func(_ context.Context, sub string) (string, bool) {
		if sub != consoleSubject {
			t.Errorf("the linker saw %q, want the console subject", sub)
		}
		return staffUUID, true
	}
	if code, b := consoleCall(t, pool, linked, path, body); code != http.StatusCreated {
		t.Fatalf("linked: status=%d body=%s", code, b)
	}
	var got string
	if err := pool.QueryRow(ctx, `SELECT placed_by::text FROM credit_holds WHERE customer_id = $1::uuid`, id).Scan(&got); err != nil || got != staffUUID {
		t.Fatalf("placed_by=%q err=%v, want the linked staff row %q", got, err, staffUUID)
	}
}

func TestIntegration_Paylink_NonUuidPlatformId(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	id := seedCustomer(t, pool, "-P")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM ar_invoice_deliveries WHERE customer_id = $1::uuid`, id)
	})
	var staffUUID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users ORDER BY created_at LIMIT 1`).Scan(&staffUUID); err != nil {
		t.Fatalf("a users row: %v", err)
	}
	body := `{"customer_id":"` + id + `","amount_cents":12500,"channel":"EMAIL","recipient":"ap@example.test"}`
	const path = "/api/v1/ar/payments/paylink"
	count := func() int {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM ar_invoice_deliveries WHERE customer_id = $1::uuid`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	unlinked := func(context.Context, string) (string, bool) { return "", false }
	if code, b := consoleCall(t, pool, unlinked, path, body); code != http.StatusForbidden || count() != 0 {
		t.Fatalf("unlinked: status=%d rows=%d body=%s", code, count(), b)
	}

	linked := func(context.Context, string) (string, bool) { return staffUUID, true }
	if code, b := consoleCall(t, pool, linked, path, body); code != http.StatusOK {
		t.Fatalf("linked: status=%d body=%s", code, b)
	}
	var got string
	if err := pool.QueryRow(ctx, `SELECT sent_by::text FROM ar_invoice_deliveries WHERE customer_id = $1::uuid`, id).Scan(&got); err != nil || got != staffUUID {
		t.Fatalf("sent_by=%q err=%v, want the linked staff row %q", got, err, staffUUID)
	}
}
