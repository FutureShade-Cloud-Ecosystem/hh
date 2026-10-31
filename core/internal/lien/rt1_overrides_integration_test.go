//go:build integration

package lien

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// RT-1 item 2: a dealer override and a job override, each optional, bounded 0 to 100 percent, set only
// by a manager (the lien routes' gate is RequireManager, and the service holds the same line), and recorded.

func wantClientErr(t *testing.T, err error, status int, words string) {
	t.Helper()
	var ce clientError
	if !errors.As(err, &ce) {
		t.Fatalf("want a client error %d containing %q, got %v", status, words, err)
	}
	if ce.status != status || !strings.Contains(ce.msg, words) {
		t.Fatalf("want %d containing %q, got %d %q", status, words, ce.status, ce.msg)
	}
}

func TestIntegration_RT1_DealerOverrideBounds(t *testing.T) {
	w := rt1Plant(t)
	ctx := w.as(w.manager(w.branchA))
	for _, bad := range []int{-1, 10001} {
		_, err := w.svc.SetDealerOverride(ctx, ip(bad))
		wantClientErr(t, err, 422, "between 0 and 100 percent")
	}
	for _, ok := range []int{0, 10000, 750} {
		got, err := w.svc.SetDealerOverride(ctx, ip(ok))
		if err != nil || got.HoldbackPctBps == nil || *got.HoldbackPctBps != ok {
			t.Fatalf("set %d: %+v, %v", ok, got, err)
		}
	}
	got, err := w.svc.SetDealerOverride(ctx, nil)
	if err != nil || got.HoldbackPctBps != nil {
		t.Fatalf("clearing the override must leave none, not zero: %+v, %v", got, err)
	}
}

func TestIntegration_RT1_JobOverrideBounds(t *testing.T) {
	w := rt1Plant(t)
	ctx := w.as(w.manager(w.branchA))
	for _, bad := range []int{-1, 10001} {
		wantClientErr(t, w.svc.SetJobOverride(ctx, w.jobOnSiteON, ip(bad)), 422, "between 0 and 100 percent")
	}
	if err := w.svc.SetJobOverride(ctx, w.jobOnSiteON, ip(0)); err != nil {
		t.Fatalf("zero percent is a rule, not an absence: %v", err)
	}
	var pct int
	if err := w.pool.QueryRow(context.Background(), `SELECT holdback_pct_bps FROM holdback_job_overrides WHERE customer_job_id = $1`, w.jobOnSiteON).Scan(&pct); err != nil || pct != 0 {
		t.Fatalf("stored override = %d, %v; want 0", pct, err)
	}
	if err := w.svc.SetJobOverride(ctx, w.jobOnSiteON, nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	var n int
	if err := w.pool.QueryRow(context.Background(), `SELECT count(*) FROM holdback_job_overrides WHERE customer_job_id = $1`, w.jobOnSiteON).Scan(&n); err != nil || n != 0 {
		t.Fatalf("after a clear the job has %d overrides, %v; want none", n, err)
	}
}

func TestIntegration_RT1_RoleGateAndRecord(t *testing.T) {
	w := rt1Plant(t)
	rec := &rt1Audit{}
	w.svc.WithAudit(rec)

	clerk := w.as(w.clerk(w.branchA))
	_, err := w.svc.SetDealerOverride(clerk, ip(500))
	wantClientErr(t, err, 403, "manager")
	wantClientErr(t, w.svc.SetJobOverride(clerk, w.jobOnSiteON, ip(500)), 403, "manager")
	if len(rec.seen) != 0 {
		t.Fatalf("a refused write must record nothing, saw %d", len(rec.seen))
	}

	// A manager whose grants do not reach the job's branch is refused in words, and nothing is stored.
	other := w.as(w.manager(w.branchB))
	wantClientErr(t, w.svc.SetJobOverride(other, w.jobOnSiteON, ip(500)), 403, "outside your reach")

	mgr := w.as(w.manager(w.branchA))
	if _, err := w.svc.SetDealerOverride(mgr, ip(500)); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.SetJobOverride(mgr, w.jobOnSiteON, ip(600)); err != nil {
		t.Fatal(err)
	}
	if len(rec.seen) != 2 {
		t.Fatalf("want 2 records, got %d", len(rec.seen))
	}
	if rec.seen[0].Action != actHoldbackRuleSet || rec.seen[1].EntityID != w.jobOnSiteON || rec.seen[1].ActorID == "" {
		t.Fatalf("records = %+v", rec.seen)
	}
}
