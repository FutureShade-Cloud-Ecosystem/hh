//go:build integration

package pos

// taxrate_integration_test.go, the served tax-rate read at Trenton over the
// real composition on a throwaway local Postgres (the repository's
// integration database, the Trenton legs' world): the real handler behind the
// real guards, the tax assessor wired over the real tax.Service the way
// cmd/core wires it, so the 200's rate is the seeded world's own: 1300, the
// effective-dated STD_ON_HST_13 row migration 0042 carries, read through the
// same engine the ring binds with. The round rule is held against the seeded
// tax_config org default row, read live from the database in the leg: the
// served word must be the world's word. (This stack leaves the round-mode
// seam unwired, the flat composition, so the served word is the default; the
// seam's own flow is the unit legs', and the row comparison here fails loud
// the day the world's word and the served word part ways.) The legs: a staff
// bearer with the X-Org header reads the rate and the rounding rule; a bearer
// whose membership is another org's is refused at the org fence with the
// shared Forbidden body; no bearer is the shared 401.
//
// Run (own throwaway container, removed afterwards, an unused port):
//
//	docker run --name hh-taxrate-pg -e POSTGRES_USER=hardscape \
//	  -e POSTGRES_PASSWORD=hardscape -e POSTGRES_DB=hardscapeos \
//	  -p 5633:5432 -d postgres:16
//	cd core
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5633/hardscapeos?sslmode=disable' \
//	  APP_ENV=dev go run ./cmd/core migrate
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5633/hardscapeos?sslmode=disable' \
//	  APP_ENV=dev go run ./cmd/core seed
//	CI=true DATABASE_URL='postgres://hardscape:hardscape@localhost:5633/hardscapeos?sslmode=disable' \
//	  go test -tags integration ./internal/pos/ -run TestIntegration_Trenton_TaxRate -p 1 -v

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tax"
)

// servedTaxAssessor mirrors the cmd/core posTaxAdapter over *tax.Service (the
// M5 engine), which a test cannot import: the pos-declared input mapped onto
// tax.SaleAssessment with evidence pinned false, so the served read assesses
// through the real engine exactly as the ring does, and the engine's
// reason-universe refusal translates into the pos sentinel exactly as the
// adapter translates it (the mirror must stay the adapter's twin, the ring's
// 400 is the adapter's work).
type servedTaxAssessor struct{ svc *tax.Service }

func (a servedTaxAssessor) AssessSale(ctx context.Context, in TaxQuoteInput) (QuoteResult, error) {
	q, err := a.svc.AssessSale(ctx, tax.SaleAssessment{
		BranchID:         in.BranchID,
		SubtotalCents:    in.SubtotalCents,
		ReasonCode:       in.ReasonCode,
		EvidenceAttached: false,
	})
	if err != nil {
		var bad httpx.BadRequestError
		if errors.As(err, &bad) {
			return QuoteResult{}, fmt.Errorf("%w: %s", ErrUnknownTaxReason, bad.Msg)
		}
		return QuoteResult{}, err
	}
	return QuoteResult{TaxCents: q.TaxCents, RateBps: q.RateBps}, nil
}

// The served read: a staff bearer with the X-Org header reads the rate the
// ring binds (the same assessor the ring assesses with, here the real engine
// over the seeded world) and the configured rounding rule; a foreign org's
// bearer is refused at the org fence; no bearer is the shared 401.
func TestIntegration_Trenton_TaxRate(t *testing.T) {
	s := newTrentonStack(t)
	// The cmd/core composition's assessor wire, over the real engine on this
	// stack too, so the 200's rate is the seeded row's own.
	taxSvc := tax.NewService(tax.NewRepository(s.pool))
	s.svc.WithTaxAssessor(servedTaxAssessor{svc: taxSvc})

	// The world's round rule, read live: the org default row the seed carries.
	var roundMode string
	if err := s.pool.QueryRow(context.Background(),
		`SELECT round_mode FROM tax_config WHERE branch_id IS NULL`).Scan(&roundMode); err != nil {
		t.Fatalf("read the seeded round rule: %v", err)
	}

	rec := s.do(http.MethodGet, "/api/v1/pos/tax-rate", s.bearer(trentonClerkSub), "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the counter reads the tax rate: got %d: %s", rec.Code, rec.Body.String())
	}
	body := s.decodeBody(rec)
	if body["jurisdiction"] != "ON" {
		t.Fatalf("jurisdiction must read ON: %v", body)
	}
	if body["reason_code"] != "STD_ON_HST_13" {
		t.Fatalf("reason_code must read STD_ON_HST_13: %v", body)
	}
	if got, ok := body["rate_bps"].(float64); !ok || got != 1300 {
		t.Fatalf("rate_bps must read the seeded 1300 row: %v", body["rate_bps"])
	}
	if body["round_mode"] != roundMode {
		t.Fatalf("the served round rule must be the seeded world's word %q: %v", roundMode, body["round_mode"])
	}

	// The org fence: a bearer whose membership is another org's never reaches
	// the read, the shared Forbidden body.
	rec = s.do(http.MethodGet, "/api/v1/pos/tax-rate", s.bearer(elsewhereSub), "", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a foreign org's bearer must be refused at the fence: got %d: %s", rec.Code, rec.Body.String())
	}

	// No bearer: the shared unauthorized shape.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/pos/tax-rate", nil)
	req.Header.Set("X-Org", demoOrgSlug)
	rec = httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no bearer must be the shared 401: got %d: %s", rec.Code, rec.Body.String())
	}
}
