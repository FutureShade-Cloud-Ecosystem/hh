package customer

import (
	"bytes"
	"context"
	coreidentity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// dupWriter is a customerWriter whose InsertCustomer always reports the account_no UNIQUE
// collision via the ErrDuplicateAccountNo sentinel, the exact value the pgx Repository
// returns when it detects Postgres 23505 on customers.account_no (migration 0006).
type dupWriter struct{}

func (dupWriter) InsertCustomer(context.Context, CreateInput) (Account, error) {
	return Account{}, ErrDuplicateAccountNo
}

func (dupWriter) UpdateBillingAddress(_ context.Context, id string, _ map[string]any) (Account, error) {
	return Account{CustomerID: id}, nil
}

// TestCreate_DuplicateAccountNo_Returns409 is the regression guard for the MAJOR finding:
// a duplicate account_no must surface as HTTP 409 with a clear message, NOT a 500. Before the
// fix, InsertCustomer wrapped the pg 23505 in a plain fmt.Errorf that writeErr fell through to
// the default 500 branch, so this test asserts 409 and fails on the old behavior.
func TestCreate_DuplicateAccountNo_Returns409(t *testing.T) {
	svc := NewService(nil).WithWriter(dupWriter{})
	h := NewHandler(svc, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))

	// Guards-swap carry note (HH-3.2 fold): the 409 mapping is the handler's,
	// so the regression drives the handler directly with the caller the
	// guard would have resolved (the positional-guard mux roundtrip the pin
	// used is the superseded wiring).
	body := bytes.NewBufferString(`{"account_no":"DIB-DUP","name":"Dup Co"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/customers", body)
	rec := httptest.NewRecorder()
	// A GLOBAL caller: a null home account is the global caller's to file (UX-89 round 2).
	h.create(rec, req, fenceCaller(coreidentity.ScopeGlobal))

	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate account_no: status = %d, want 409; body %s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("account number already in use")) {
		t.Fatalf("409 body should explain the collision, got %s", rec.Body.String())
	}
}
