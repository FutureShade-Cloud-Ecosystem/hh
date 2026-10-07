package pos

// grant_test.go, the payment grant read's handler without a database: the answer is the reader's,
// the caller's ids reach the reader, and a read that cannot be made is a 503, never a "false" a
// client would read as "may not take payment".

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

type fakeGrants struct {
	held         bool
	err          error
	gotUser, org string
}

func (f *fakeGrants) HoldsPayment(_ context.Context, user, org string, _ identity.StaffContext) (bool, error) {
	f.gotUser, f.org = user, org
	return f.held, f.err
}

func askGrant(h *Handler) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.paymentGrant(rec, httptest.NewRequest(http.MethodGet, "/api/v1/pos/payment-grant", nil),
		guards.StaffCaller{StaffScope: authz.StaffScope{PlatformUserID: "sub-1", OrgID: "org-1"}})
	return rec
}

func TestPaymentGrant_AnswersTheReadersWordForTheCallersIds(t *testing.T) {
	for _, held := range []bool{true, false} {
		g := &fakeGrants{held: held}
		rec := askGrant(NewHandler(newHarness().svc, nil).WithGrantReader(g))
		want := `{"can_take_payment":false}`
		if held {
			want = `{"can_take_payment":true}`
		}
		if rec.Code != http.StatusOK || rec.Body.String() != want+"\n" {
			t.Fatalf("held=%v answered %d %q, want 200 %s", held, rec.Code, rec.Body.String(), want)
		}
		if g.gotUser != "sub-1" || g.org != "org-1" {
			t.Fatalf("the reader was asked about %q in %q, want the caller's own", g.gotUser, g.org)
		}
	}
}

func TestPaymentGrant_AReadThatCannotBeMadeIsA503NotAFalse(t *testing.T) {
	if rec := askGrant(NewHandler(newHarness().svc, nil)); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no reader wired answers %d, want 503", rec.Code)
	}
	rec := askGrant(NewHandler(newHarness().svc, nil).WithGrantReader(&fakeGrants{err: errors.New("roles unreachable")}))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("a failed read answers %d %s, want 503", rec.Code, rec.Body.String())
	}
}
