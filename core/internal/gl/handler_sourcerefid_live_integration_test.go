//go:build integration

package gl

// handler_sourcerefid_live_integration_test.go, the LIVE end-to-end regression for the
// source_ref_id gate on GET /api/v1/gl/journals, driven through the real repository-backed
// Handler over the live Postgres pool (not the DB-free fake). It proves the "was 500 → now 400"
// claim against a real ::uuid cast, and that a valid source_ref_id filters to exactly the
// matching journal's rows through the full HTTP surface.
//
// Before the handler gate, a malformed ?source_ref_id= flowed into the `$3::uuid` cast in
// Repository.ListJournals, raised SQLSTATE 22P02 ("invalid input syntax for type uuid"), and
// h.writeErr mapped the opaque error to HTTP 500. The gate now short-circuits with 400
// 'invalid source_ref_id' before any DB call.

import (
	"encoding/json"
	"fmt"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

func TestIntegration_ListJournals_SourceRefID_HandlerGate(t *testing.T) {
	pool := intPool(t)
	user := aUser(t, pool)
	ensurePeriods(t, pool, 2099)
	at := time.Date(2099, 6, 15, 12, 0, 0, 0, time.UTC)
	// DIB-392: an UNFILTERED journals list is the whole org's ledger, so it is a GLOBAL-scope read. This
	// test is about the source_ref_id gate, so it poses as the Financial Controller who may run it.
	svc := svcAt(pool, identity.StaffContext{
		UserID: user, RoleCode: "FINANCIAL_CONTROLLER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal,
	}, at)
	h := NewHandler(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Post a journal under a known, valid source ref so the positive filter has a target.
	ref := "3f9c1d2e-5a6b-4c7d-8e9f-0a1b2c3d4e5f"
	id, err := svc.PostExternal(t.Context(), ExternalJournal{
		Source:         SourceAdjustment,
		SourceRefID:    ref,
		IdempotencyKey: fmt.Sprintf("srcref-live-%d", time.Now().UnixNano()),
		Memo:           "source_ref live gate test",
		Lines: []ExternalLine{
			{AccountCode: "1000", DebitCents: 1000, Memo: "bank"},
			{AccountCode: "1020", CreditCents: 1000, Memo: "undeposited"},
		},
	})
	if err != nil {
		t.Fatalf("post external: %v", err)
	}

	get := func(q url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/gl/journals?"+q.Encode(), nil)
		rr := httptest.NewRecorder()
		h.listJournals(rr, req, guards.StaffCaller{})
		return rr
	}

	// (1) malformed source_ref_id → 400 'invalid source_ref_id' (was HTTP 500 via 22P02).
	for _, bad := range []string{"not-a-uuid", "123", "'; DROP TABLE gl_journals;--"} {
		rr := get(url.Values{"source_ref_id": {bad}})
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("live bad source_ref_id %q: want 400, got %d (body %s)", bad, rr.Code, rr.Body.String())
		}
		if msg := bodyMsg(rr.Body.Bytes()); msg != "invalid source_ref_id" {
			t.Fatalf("live bad source_ref_id %q: want 'invalid source_ref_id', got %q", bad, msg)
		}
	}

	// (2) valid source_ref_id → 200, and every returned row is the matching journal.
	rr := get(url.Values{"source_ref_id": {ref}})
	if rr.Code != http.StatusOK {
		t.Fatalf("live valid source_ref_id: want 200, got %d (body %s)", rr.Code, rr.Body.String())
	}
	var page struct {
		Items []Journal `json:"items"`
		Total int       `json:"total"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("filter by source_ref_id must return exactly the 1 matching journal, got total=%d items=%d", page.Total, len(page.Items))
	}
	if page.Items[0].ID != id || page.Items[0].SourceRefID != ref {
		t.Fatalf("filtered row must be journal %s with source_ref_id %s, got %+v", id, ref, page.Items[0])
	}
}
