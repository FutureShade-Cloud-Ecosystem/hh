//go:build integration

// reapply_handler_integration_test.go: the re-application route through the real guards (SF-2l). The manager gate is
// the route's, the status words are the contract's: 201 applied, 200 a replay, 400 a refusal in words, 404 a payment
// or invoice that is not there for the caller, 409 held money that says no.
package payments_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	coreid "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func (w *reversalGLWorld) reapplyRoute(tier int, orig, body string) (int, string) {
	w.r.t.Helper()
	return w.route(tier, http.MethodPost, "/api/v1/ar/payments/"+orig+"/reapply", body)
}

func (w *reversalGLWorld) route(tier int, method, path, body string) (int, string) {
	w.r.t.Helper()
	h := payments.NewReversalHandler(payments.NewReverseService(w.r.repo), slog.New(slog.DiscardHandler)).
		WithUserLinker(func(context.Context, string) (string, bool) { return w.r.userID, true })
	sg := testkit.NewStaffGuards(w.r.t)
	sg.SetAuthority(coreid.StaffAuthority{RoleID: "payables_receivables", Tier: tier, Scope: "global"})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, sg.Guards)
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+sg.Bearer(w.r.t))
	r.Header.Set("X-Org", testkit.OrgNorthSlug)
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	return rec.Code, rec.Body.String()
}

func TestIntegration_ReapplyRoute_StatusesAndWords(t *testing.T) {
	w := newReversalGLWorld(t)
	orig := w.receipt("CHEQUE", 10_000)
	inv := w.invoiceOf(orig)
	w.drain()
	body := func(key string, cents int64) string {
		return fmt.Sprintf(`{"reason":"wrong account","idempotency_key":%q,"allocations":[{"invoice_id":%q,"amount_cents":%d}]}`, key, inv, cents)
	}
	k := "route-" + w.r.suffix
	held := func() payments.HeldFigure {
		code, out := w.route(2, http.MethodGet, "/api/v1/ar/payments/"+orig+"/held", "")
		var h payments.HeldFigure
		if code != http.StatusOK || json.Unmarshal([]byte(out), &h) != nil {
			t.Fatalf("the held read answered %d %s", code, out)
		}
		return h
	}
	if h := held(); h.Reversed || h.HeldCents != 0 || h.AmountCents != 10_000 {
		t.Errorf("a payment never reversed reads %+v, want it holding nothing", h)
	}
	if code, _ := w.route(3, http.MethodGet, "/api/v1/ar/payments/"+orig+"/held", ""); code != http.StatusForbidden {
		t.Errorf("a front line caller read the held figure: %d, want 403", code)
	}
	if code, _ := w.route(2, http.MethodGet, "/api/v1/ar/payments/00000000-0000-4000-8000-0000000000aa/held", ""); code != http.StatusNotFound {
		t.Errorf("the held figure of a missing payment answered %d, want 404", code)
	}

	if code, _ := w.reapplyRoute(3, orig, body(k+"-a", 1_000)); code != http.StatusForbidden {
		t.Errorf("a front line caller answered %d, want 403 from the manager gate", code)
	}
	if code, out := w.reapplyRoute(2, orig, body(k+"-a", 1_000)); code != http.StatusConflict || !strings.Contains(out, "has not been reversed") {
		t.Errorf("a payment never reversed answered %d %s, want 409 in words", code, out)
	}
	w.reverse(orig)
	if h := held(); !h.Reversed || h.HeldCents != 10_000 || h.AppliedCents != 0 {
		t.Errorf("a reversed payment reads %+v, want 10000 held", h)
	}
	if code, out := w.reapplyRoute(2, orig, `{"reason":"","idempotency_key":"x","allocations":[]}`); code != http.StatusBadRequest {
		t.Errorf("no reason answered %d %s, want 400", code, out)
	}
	if code, out := w.reapplyRoute(2, "00000000-0000-4000-8000-0000000000aa", body(k+"-b", 1_000)); code != http.StatusNotFound {
		t.Errorf("a missing payment answered %d %s, want 404", code, out)
	}
	if code, out := w.reapplyRoute(2, orig, body(k+"-c", 10_001)); code != http.StatusConflict || !strings.Contains(out, "holds") {
		t.Errorf("above the held amount answered %d %s, want 409 in words", code, out)
	}
	code, out := w.reapplyRoute(2, orig, body(k+"-d", 6_000))
	if code != http.StatusCreated {
		t.Fatalf("the apply answered %d %s, want 201", code, out)
	}
	var got payments.Reapplication
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.AppliedCents != 6_000 || got.HeldBeforeCents != 10_000 || got.HeldAfterCents != 4_000 || len(got.Lines) != 1 || got.Replayed {
		t.Errorf("the 201 body reads %s (%v)", out, err)
	}
	if h := held(); h.HeldCents != 4_000 || h.AppliedCents != 6_000 {
		t.Errorf("after 6000 the held read says %+v, want 4000 held and 6000 applied", h)
	}
	if code, out := w.reapplyRoute(2, orig, body(k+"-d", 6_000)); code != http.StatusOK || !strings.Contains(out, `"replayed":true`) {
		t.Errorf("a replay answered %d %s, want 200 flagged as a replay", code, out)
	}
	if code, out := w.reapplyRoute(2, orig, body(k+"-d", 7_000)); code != http.StatusConflict {
		t.Errorf("a spent key with another amount answered %d %s, want 409", code, out)
	}
}

func TestIntegration_PayoutRoute_StatusesAndWords(t *testing.T) {
	w := newReversalGLWorld(t)
	w.cleanupPayouts()
	orig := w.receipt("CHEQUE", 10_000)
	w.drain()
	path := "/api/v1/ar/payments/" + orig + "/payout"
	body := func(key, kind string, cents int64, ref string) string {
		return fmt.Sprintf(`{"kind":%q,"amount_cents":%d,"reason":"paid back","reference":%q,"idempotency_key":%q}`, kind, cents, ref, key)
	}
	k := "pout-route-" + w.r.suffix
	if code, _ := w.route(3, http.MethodPost, path, body(k+"-a", "BANK", 1_000, "CHQ 1")); code != http.StatusForbidden {
		t.Errorf("a front line caller answered %d, want 403 from the manager gate", code)
	}
	if code, out := w.route(2, http.MethodPost, path, body(k+"-a", "BANK", 1_000, "CHQ 1")); code != http.StatusConflict || !strings.Contains(out, "has not been reversed") {
		t.Errorf("a payment never reversed answered %d %s, want 409 in words", code, out)
	}
	w.reverse(orig)
	if code, out := w.route(2, http.MethodPost, path, body(k+"-b", "BANK", 1_000, "")); code != http.StatusBadRequest || !strings.Contains(out, "cheque number or transfer reference") {
		t.Errorf("a bank pay out with no reference answered %d %s, want 400 in words", code, out)
	}
	if code, out := w.route(2, http.MethodPost, path, body(k+"-c", "CASHBACK", 1_000, "x")); code != http.StatusBadRequest {
		t.Errorf("an unknown kind answered %d %s, want 400", code, out)
	}
	if code, out := w.route(2, http.MethodPost, "/api/v1/ar/payments/00000000-0000-4000-8000-0000000000aa/payout", body(k+"-d", "BANK", 1_000, "x")); code != http.StatusNotFound {
		t.Errorf("a missing payment answered %d %s, want 404", code, out)
	}
	if code, out := w.route(2, http.MethodPost, path, body(k+"-e", "BANK", 10_001, "CHQ 2")); code != http.StatusConflict || !strings.Contains(out, "holds") {
		t.Errorf("above the held amount answered %d %s, want 409 in words", code, out)
	}
	code, out := w.route(2, http.MethodPost, path, body(k+"-f", "BANK", 6_000, "CHQ 3"))
	var got payments.Payout
	if code != http.StatusCreated || json.Unmarshal([]byte(out), &got) != nil || got.AmountCents != 6_000 || got.HeldBeforeCents != 10_000 || got.HeldAfterCents != 4_000 || got.Replayed {
		t.Fatalf("the pay out answered %d %s, want 201 with the held figure falling 10000 to 4000", code, out)
	}
	if code, out := w.route(2, http.MethodPost, path, body(k+"-f", "BANK", 6_000, "CHQ 3")); code != http.StatusOK || !strings.Contains(out, `"replayed":true`) {
		t.Errorf("a replay answered %d %s, want 200 flagged as a replay", code, out)
	}
	if code, out := w.route(2, http.MethodPost, path, body(k+"-f", "BANK", 7_000, "CHQ 3")); code != http.StatusConflict {
		t.Errorf("a spent key with another amount answered %d %s, want 409", code, out)
	}
	hc, hout := w.route(2, http.MethodGet, "/api/v1/ar/payments/"+orig+"/held", "")
	var h payments.HeldFigure
	if hc != http.StatusOK || json.Unmarshal([]byte(hout), &h) != nil || h.HeldCents != 4_000 || h.PaidOutCents != 6_000 {
		t.Errorf("the held read answered %d %s, want 4000 held and 6000 paid out", hc, hout)
	}
}
