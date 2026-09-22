//go:build integration

// train9_autohold_audit_integration_test.go, the auto-hold trail, against a LIVE audit_log.
//
// WHY THIS FILE EXISTS.
//
// DIB-483 shipped the AR auto-hold master switch and stated, in NOTES-FIX-483 §5, that "both
// directions are audited, with the direction recorded, the OFF flip is the one somebody will have to
// explain later". Two tests asserted it and both passed. Neither could see that in production the row
// did not exist:
//
//   - dib483_autohold_test.go asserts against a FAKE audit.Recorder that collects SensitiveAccess
//     values. A fake never applies audit's deny-by-default Extra allowlist and never touches the uuid
//     column, so it cannot observe a value the database will reject or a key persistence will drop.
//   - dib483_autohold_integration_test.go builds NewCreditService(repo) with no .WithAudit(…) at all,
//     so on the one path that does have a real database the recorder is nil and record() returns
//     immediately.
//
// The gap between them is exactly the shape of the defect, and it took flipping the real switch in a
// browser and then looking in audit_log to find it: EntityID was the literal "ORG" at organisation
// scope against a `NULLIF($3,”)::uuid` column, so the INSERT raised 22P02, CreditService.record
// swallowed it (`_ = s.audit.Record(…)`), and the screen said "Automatic credit holds are ON" over an
// empty trail. Separately, from/to/scope/no_op are not on allowedExtraKeys, so even a row that landed
// carried no direction.
//
// So this test drives the REAL service over the REAL repository with the REAL audit.Service and then
// reads audit_log with SQL. It is deliberately the least clever test in this package: the only way to
// catch "the write silently did not happen" is to go and look.
//
// Run: DATABASE_URL=… go test -tags=integration ./internal/billing/... -run Train9 -count=1
package billing

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
)

// countAutoHoldRows counts the trail rows for the switch, org scope included.
func countAutoHoldRows(t *testing.T, ctx context.Context, r *Repository) int {
	t.Helper()
	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = $1`, ActAutoHoldToggled).Scan(&n); err != nil {
		t.Fatalf("count auto-hold audit rows: %v", err)
	}
	return n
}

// latestAutoHoldChanges returns the newest trail row's changes payload, as persisted.
func latestAutoHoldChanges(t *testing.T, ctx context.Context, r *Repository) map[string]any {
	t.Helper()
	var blob []byte
	if err := r.pool.QueryRow(ctx,
		`SELECT changes FROM audit_log WHERE action = $1 ORDER BY seq DESC LIMIT 1`,
		ActAutoHoldToggled).Scan(&blob); err != nil {
		t.Fatalf("read newest auto-hold audit row: %v", err)
	}
	out := map[string]any{}
	if err := json.Unmarshal(blob, &out); err != nil {
		t.Fatalf("decode changes: %v", err)
	}
	return out
}

// TestIntegrationTrain9_AutoHoldFlipLandsARowAtOrgScope is the acceptance for the fix: flipping the
// switch at the ORGANISATION scope, the scope the shipped admin panel writes, puts a row in
// audit_log. Before the fix this count did not move, in either direction.
func TestIntegrationTrain9_AutoHoldFlipLandsARowAtOrgScope(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	// The REAL recorder, not a fake: the whole defect lives between the two.
	svc := NewCreditService(repo).WithAudit(audit.NewService(audit.NewRepository(pool), nil))
	ctx := context.Background()

	before, _, _, _, _, _, _ := readOrgPolicyRow(t, ctx, repo)
	t.Cleanup(func() { _ = repo.SetAutoHoldEnabled(context.Background(), "", before) })

	rowsBefore := countAutoHoldRows(t, ctx, repo)

	if _, err := svc.SetAutoHold(ctx, "", true, "d7000000-0000-4000-9000-000000000003", "train 9 gate"); err != nil {
		t.Fatalf("turn on: %v", err)
	}
	if got := countAutoHoldRows(t, ctx, repo); got != rowsBefore+1 {
		t.Fatalf("turning the switch ON wrote %d audit rows, want 1. The master credit switch flipped "+
			"with no trail, CreditService.record is best-effort, so a refused INSERT is silent and the "+
			"screen still says it worked", got-rowsBefore)
	}
	on := latestAutoHoldChanges(t, ctx, repo)
	if on["field"] != "auto_hold_enabled" || on["before"] != "off" || on["after"] != "on" {
		t.Fatalf("the ON row does not record the DIRECTION: %v, keys outside audit's allowlist are "+
			"dropped before persistence, so a trail can exist and still not answer the question it is "+
			"kept for", on)
	}

	// OFF is the direction somebody will have to explain later, so it is asserted separately rather
	// than assumed symmetric.
	if _, err := svc.SetAutoHold(ctx, "", false, "d7000000-0000-4000-9000-000000000003", "train 9 gate"); err != nil {
		t.Fatalf("turn off: %v", err)
	}
	if got := countAutoHoldRows(t, ctx, repo); got != rowsBefore+2 {
		t.Fatalf("turning the switch OFF wrote %d audit rows, want 1", got-rowsBefore-1)
	}
	off := latestAutoHoldChanges(t, ctx, repo)
	if off["before"] != "on" || off["after"] != "off" {
		t.Fatalf("the OFF row does not record the direction: %v", off)
	}
	if off["status"] == nil || off["status"] == "" {
		t.Fatalf("the row does not say WHICH policy was flipped: %v, the scope used to ride in the "+
			"entity id, which is a uuid column and rejected it", off)
	}
}
