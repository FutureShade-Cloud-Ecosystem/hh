//go:build integration

// fw1_01_round3_integration_test.go, PR 614 round 3's red first leg (S2's
// round 2 read, FROM-PRINCIPAL 200): a request for a past missing year is
// answered with a silent 200 naming the cap year, and the audit row names an
// open that did not happen (P2-1). After the fix the request is refused in
// words, and no year and no audit row is written.
package gl

import (
	"context"
	"fmt"
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
// round 3's P2-1 red: the Controller asking for a year before the books'
// first year is answered 200 with the cap year today, and the audit names an
// open that did not happen. After the fix the answer is a refusal in words,
// with no year written and no audit row.
func TestIntegration_FW1_01R3_PastMissingYearIsRefusedInWordsWithNoAuditRow(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	// The band is the database's own frontier, the way the roll forward leg's dynamic `beyond`
	// takes it: the house year IS the latest year that exists, and the scrub keeps the two years
	// after it free so the refusal proves it writes nothing.
	house := fw1NextYear(t, pool) - 1
	target, beyond := house+1, house+2
	fw1EnsureHouseYear(t, pool, house)
	fw1ScrubYear(t, pool, target, beyond)
	t.Cleanup(func() { fw1ScrubYear(t, pool, target, beyond) })
	clockAt := time.Date(house, 6, 15, 12, 0, 0, 0, houseday.DefaultZone().Location())
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

	// The request names the year before the books' own first year, read off the books.
	var first int
	if err := pool.QueryRow(ctx, `SELECT COALESCE(MIN(year), 0) FROM fiscal_periods`).Scan(&first); err != nil {
		t.Fatal(err)
	}
	before := first - 1
	code, body := postOpenYear(fmt.Sprintf(`{"year":%d,"reason":"before the books"}`, before))
	if code == http.StatusOK || !strings.Contains(body, fmt.Sprintf("%d is before the books' first year; only the next year can be opened here", before)) {
		t.Fatalf("requesting %d, before the books' first year %d, answered %d %s, want refusal '%d is before the books' first year; only the next year can be opened here'", before, first, code, body, before)
	}
	if len(auditTx.recorded) != 0 {
		t.Fatalf("the refused open recorded %d audit rows, want none: the audit must never name an open that did not happen", len(auditTx.recorded))
	}
	for _, year := range []int{target, beyond} {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM fiscal_periods WHERE year=$1`, year).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("the refused open left %d periods behind in %d; a refusal writes nothing", n, year)
		}
	}
}
