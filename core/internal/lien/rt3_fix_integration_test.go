//go:build integration

package lien

import (
	"context"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"strings"
	"testing"
)

// RT-3 fix, item 1: the old job level release route refuses a lien job linked to a customer job (a
// receivable job), in words that name RT-3's release. Nothing is written, in either order of use.
func TestIntegration_RT3Fix_OldRouteRefusesALinkedJob(t *testing.T) {
	w := rt1Plant(t)
	ctx := context.Background()
	w.svc.WithGLPoster(realGLPoster{svc: gl.NewService(gl.NewRepository(w.pool))}).WithCoSigner(intCoSigner{})
	lj := w.lienJob(t, w.customerA, w.branchA)
	if _, err := w.svc.LinkCustomerJob(w.as(w.manager(w.branchA)), lj, w.jobOnSiteON); err != nil {
		t.Fatalf("link: %v", err)
	}
	if _, err := w.svc.store.InsertHoldbackEntry(ctx, HoldbackEntry{JobID: lj, BranchID: w.branchA, EntryType: "RETENTION", AmountCents: 10000}); err != nil {
		t.Fatalf("seed retention: %v", err)
	}
	t.Cleanup(func() { rt1Del(t, w.pool, "holdback_ledger", "job_id = $1", lj) })

	_, err := w.svc.ReleaseHoldbackApproved(w.as(gm("gm-rt3fix")), lj, ReleaseApproval{ApproverID: "ctrl-int", ApproverRole: roleController})
	wantClientErr(t, err, 422, "/api/v1/ar/holdback/releases")
	if err != nil && !strings.Contains(err.Error(), "customer job") {
		t.Errorf("the refusal must say why (linked to a customer job), got %v", err)
	}
	var n int
	if e := w.pool.QueryRow(ctx, `SELECT count(*) FROM holdback_ledger WHERE job_id = $1 AND entry_type = 'RELEASE'`, lj).Scan(&n); e != nil || n != 0 {
		t.Fatalf("a refused release writes no row (n=%d, err=%v)", n, e)
	}
	if e := w.pool.QueryRow(ctx, `SELECT count(*) FROM gl_journals WHERE idempotency_key = $1`, "holdback-release:"+lj).Scan(&n); e != nil || n != 0 {
		t.Fatalf("a refused release posts no journal (n=%d, err=%v)", n, e)
	}
}
