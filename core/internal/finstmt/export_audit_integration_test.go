//go:build integration

// export_audit_integration_test.go, the export access trail against a LIVE
// audit_log through the REAL recorder (audit.NewService, the way cmd/core
// wires it). Round 4's P1 (S2's round 3 read): the row named the report in
// audit_log.entity_id, a uuid column, so every insert failed and the
// best-effort swallow left NO export audited in production. The row carries
// a NULL entity and names the report and format in Extra.
package finstmt

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

func TestIntegration_Finstmt_ExportAuditRowThroughTheRealRecorder(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	var user string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users LIMIT 1`).Scan(&user); err != nil {
		t.Skipf("no seeded user: %v", err)
	}
	// The REAL recorder, not a fake: the whole defect lives between the two.
	svc := NewService(NewRepository(pool)).
		withCaller(func(context.Context) (identity.StaffContext, bool) {
			return identity.StaffContext{UserID: user, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal, Status: identity.StatusActive}, true
		}).
		WithAudit(audit.NewService(audit.NewRepository(pool), nil))
	h := NewHandler(svc, slog.New(slog.DiscardHandler))

	var before int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'FINSTMT_EXPORT'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/finstmt/income-statement?year=2026&format=csv", nil).
		WithContext(identity.ContextWithStaffUserID(ctx, user))
	rec := httptest.NewRecorder()
	h.incomeStatement(rec, req, guards.StaffCaller{})
	if rec.Code != http.StatusOK {
		t.Fatalf("the csv export answered %d: %s", rec.Code, rec.Body.String())
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'FINSTMT_EXPORT'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != before+1 {
		t.Fatalf("audit_log holds %d FINSTMT_EXPORT rows after the export (before %d), want exactly one more", n, before)
	}
	var entityID, report, format, actor string
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(entity_id::text,''), COALESCE(changes->>'report',''), COALESCE(changes->>'format',''), COALESCE(actor_id::text,'')
		 FROM audit_log WHERE action = 'FINSTMT_EXPORT' ORDER BY seq DESC LIMIT 1`).Scan(&entityID, &report, &format, &actor); err != nil {
		t.Fatal(err)
	}
	if entityID != "" {
		t.Fatalf("the export audit's entity_id is %q, want NULL (a report name is not a uuid)", entityID)
	}
	if report != "INCOME_STATEMENT" {
		t.Fatalf("the export audit's changes carry report %q, want INCOME_STATEMENT", report)
	}
	if format != "csv" {
		t.Fatalf("the export audit's changes carry format %q, want csv", format)
	}
	if actor != user {
		t.Fatalf("the export audit's actor is %q, want the caller %q", actor, user)
	}
}
