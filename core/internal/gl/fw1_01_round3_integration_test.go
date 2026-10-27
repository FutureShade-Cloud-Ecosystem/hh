//go:build integration

// fw1_01_round3_integration_test.go, PR 614 round 3's red first leg (S2's
// round 2 read, FROM-PRINCIPAL 200): a request for a past missing year is
// answered with a silent 200 naming the cap year, and the audit row names an
// open that did not happen (P2-1). After the fix the request is refused in
// words, and no year and no audit row is written.
package gl

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// TestIntegration_FW1_01R3_PastMissingYearIsRefusedInWordsWithNoAuditRow is
// round 3's P2-1 red: the Controller asking for 2020, before the books' first
// year, is answered 200 with 2028 today, and the audit names an open that did
// not happen. After the fix the answer is a refusal in words, with no year
// written and no audit row.
func TestIntegration_FW1_01R3_PastMissingYearIsRefusedInWordsWithNoAuditRow(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	fw1EnsureHouseYear(t, pool, 2026)
	fw1ScrubYear(t, pool, 2027, 2028)
	t.Cleanup(func() { fw1ScrubYear(t, pool, 2027, 2028) })
	clockAt := time.Date(2026, 6, 15, 12, 0, 0, 0, houseday.DefaultZone().Location())
	auditTx := &fw1AuditTx{}
	controller := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, clockAt).WithAuditTxRecorder(auditTx)
	h := NewHandler(controller, nil)

	postOpenYear := func(body string) (int, string) {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/gl/periods/open-year", strings.NewReader(body))
		h.openYear(rec, req, guards.StaffCaller{})
		return rec.Code, rec.Body.String()
	}

	code, body := postOpenYear(`{"year":2020,"reason":"before the books"}`)
	if code == http.StatusOK || !strings.Contains(body, "2020 is before the books' first year; only the next year can be opened here") {
		t.Fatalf("requesting 2020, before the books' first year, answered %d %s, want refusal '2020 is before the books' first year; only the next year can be opened here'", code, body)
	}
	if len(auditTx.recorded) != 0 {
		t.Fatalf("the refused open recorded %d audit rows, want none: the audit must never name an open that did not happen", len(auditTx.recorded))
	}
	for _, year := range []int{2027, 2028} {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM fiscal_periods WHERE year=$1`, year).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("the refused open left %d periods behind in %d; a refusal writes nothing", n, year)
		}
	}
}
