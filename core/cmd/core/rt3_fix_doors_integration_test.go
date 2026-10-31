//go:build integration

package main

// rt3_fix_doors_integration_test.go, the RT-3 fix round, item 4: a preserved lien and a release reversal
// have doors, so the block fires in practice and a release made in error can be undone. Driven over the
// served mux as the Controller.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

// call drives one request over the leg's served mux as the Controller and answers the status and body.
func (l *rt2Leg) call(method, path, body string) (int, string) {
	l.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+l.token)
	req.Header.Set(authz.HeaderXOrg, l.slug)
	req.Header.Set("Content-Type", "application/json")
	rsp := httptest.NewRecorder()
	l.mux.ServeHTTP(rsp, req)
	return rsp.Code, rsp.Body.String()
}

func (l *rt2Leg) auditCount(action string) int {
	l.t.Helper()
	var n int
	if err := l.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE action = $1`, action).Scan(&n); err != nil {
		l.t.Fatalf("count the audit rows of %s: %v", action, err)
	}
	return n
}

// A preserved lien set over the route blocks the release; a discharge over the route lifts it. Both are
// recorded.
func TestRT3Fix_APreservedLienSetOverTheRouteBlocksTheRelease(t *testing.T) {
	l := rt2Setup(t, "fxi")
	l.newJob(true)
	doc := l.invoice(100_000, 0)
	l.rt3PerfOn(70)
	today := rt3Today().Format("2006-01-02")

	if code, body := l.call("POST", "/api/v1/lien/jobs/"+l.lien+"/preserved-lien", `{"note":"no date"}`); code != http.StatusUnprocessableEntity {
		t.Errorf("a preserve with no date: want 422, got %d %s", code, body)
	}
	if code, body := l.call("POST", "/api/v1/lien/jobs/"+l.lien+"/preserved-lien/discharge", `{"discharged_on":"`+today+`","note":"x"}`); code != http.StatusUnprocessableEntity {
		t.Errorf("a discharge of a lien never preserved: want 422, got %d %s", code, body)
	}
	if code, body := l.call("POST", "/api/v1/lien/jobs/"+l.lien+"/preserved-lien", `{"preserved_on":"`+today+`","note":"claim registered by the supplier"}`); code != http.StatusOK {
		t.Fatalf("preserve: want 200, got %d %s", code, body)
	}
	_, err := l.rt3Service(time.Now()).Release(context.Background(), l.rt3Input("fxi-1"))
	if rt3Refusal(err) != billing.RefusedLienPreserved {
		t.Fatalf("a lien preserved over the route must block the release, got %v", err)
	}
	eq(t, "the column unchanged", l.holdbackOf(doc.InvoiceID), int64(10_000))
	eq(t, "the preserve is recorded", l.auditCount("HOLDBACK_LIEN_PRESERVED"), 1)

	if code, body := l.call("POST", "/api/v1/lien/jobs/"+l.lien+"/preserved-lien/discharge", `{"discharged_on":"`+today+`","note":"claim discharged"}`); code != http.StatusOK {
		t.Fatalf("discharge: want 200, got %d %s", code, body)
	}
	eq(t, "the discharge is recorded", l.auditCount("HOLDBACK_LIEN_DISCHARGED"), 1)
	if _, err := l.rt3Service(time.Now()).Release(context.Background(), l.rt3Input("fxi-2")); err != nil {
		t.Fatalf("a discharged lien no longer blocks: %v", err)
	}
}

type reversalBody struct {
	ReleaseID string `json:"release_id"`
	Restored  int64  `json:"restored_cents"`
}

// A Controller reverses a release made in error: the column, the ledger, 1150 and 1100 are as they were
// before the release, the reason is recorded, a second reversal is refused, and the holdback can be
// released again.
func TestRT3Fix_AControllerReversesAReleaseMadeInError(t *testing.T) {
	l := rt2Setup(t, "fxj")
	l.newJob(true)
	doc := l.invoice(100_000, 0)
	l.rt3PerfOn(70)
	h1150, h1100, ledger, aging := l.glBalance("1150"), l.glBalance("1100"), l.ledgerNet(), l.agingTotal()

	res, err := l.rt3Service(time.Now()).Release(context.Background(), l.rt3Input("fxj-1"))
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	l.drain()
	releaseID := res.Items[0].ReleaseID

	path := "/api/v1/ar/holdback/releases/" + releaseID + "/reversal"
	if code, body := l.call("POST", path, `{"reason":""}`); code != http.StatusBadRequest {
		t.Errorf("a reversal with no reason: want 400, got %d %s", code, body)
	}
	code, body := l.call("POST", path, `{"reason":"released in error, the lien was about to be preserved"}`)
	if code != http.StatusOK {
		t.Fatalf("reverse: want 200, got %d %s", code, body)
	}
	var rb reversalBody
	if err := json.Unmarshal([]byte(body), &rb); err != nil || rb.Restored != 10_000 {
		t.Errorf("the answer must name the restored amount, got %s (%v)", body, err)
	}
	l.drain()

	eq(t, "the column is restored", l.holdbackOf(doc.InvoiceID), int64(10_000))
	eq(t, "the ledger is restored", l.ledgerNet(), ledger)
	eq(t, "1150 is as it was", l.glBalance("1150")[0]-l.glBalance("1150")[1], h1150[0]-h1150[1])
	eq(t, "1100 is as it was", l.glBalance("1100")[0]-l.glBalance("1100")[1], h1100[0]-h1100[1])
	eq(t, "the aging is as it was", l.agingTotal(), aging)
	l.ties("after the reversal")
	eq(t, "the reversal is recorded with its reason", l.auditCount("HOLDBACK_RELEASE_REVERSED"), 1)
	var reason string
	_ = l.pool.QueryRow(context.Background(), `SELECT COALESCE(changes->>'reason','') FROM audit_log WHERE action = 'HOLDBACK_RELEASE_REVERSED' ORDER BY seq DESC LIMIT 1`).Scan(&reason)
	if !strings.Contains(reason, "released in error") {
		t.Errorf("the audit row must carry the reason, got %q", reason)
	}

	if code, body := l.call("POST", path, `{"reason":"again"}`); code != http.StatusConflict {
		t.Errorf("a second reversal: want 409, got %d %s", code, body)
	}
	if code, body := l.call("POST", "/api/v1/ar/holdback/releases/00000000-0000-4000-8000-000000000000/reversal", `{"reason":"x"}`); code != http.StatusNotFound {
		t.Errorf("an unknown release: want 404, got %d %s", code, body)
	}
	eq(t, "still restored after the refusals", l.holdbackOf(doc.InvoiceID), int64(10_000))

	// The holdback can be released again, correctly, once.
	if _, err := l.rt3Service(time.Now()).Release(context.Background(), l.rt3Input("fxj-2")); err != nil {
		t.Fatalf("a reversed release can be released again: %v", err)
	}
	l.drain()
	eq(t, "released again", l.holdbackOf(doc.InvoiceID), int64(0))
	l.ties("after the second release")
}
