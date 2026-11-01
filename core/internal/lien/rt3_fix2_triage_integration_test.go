//go:build integration

package lien

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// RT-3 fix 2, P3-5: a linked job's triage badge follows RT-3's preservation rule (the period runs out on a
// date; a preserved lien blocks), not the old 45 day window from the retention's age.
func TestIntegration_RT3Fix2_ALinkedJobsTriageFollowsThePreservationRule(t *testing.T) {
	w := rt1Plant(t)
	ctx := context.Background()
	mgr := w.as(w.manager(w.branchA))
	rule, err := w.svc.HoldbackRuleForJob(mgr, w.jobOnSiteON)
	if err != nil {
		t.Fatalf("the job's rule: %v", err)
	}
	day := func(n int) string { return time.Now().AddDate(0, 0, -n).Format("2006-01-02") }

	cases := 0
	plant := func(perfDaysAgo int, preserved bool) string {
		// Each case sits on a customer job of its own: the badge judges every lien job linked to the job
		// together (fix 3), so cases sharing a job would read as one.
		cases++
		var cj string
		if err := w.pool.QueryRow(ctx,
			`INSERT INTO customer_jobs (customer_id, branch_id, job_no, name, status, ship_to_address)
			 VALUES ($1, $2, $3, 'RT3F2 triage job', 'OPEN', '{}'::jsonb) RETURNING id::text`,
			w.customerA, w.branchA, fmt.Sprintf("J-TRI%d-%s", cases, w.tag)).Scan(&cj); err != nil {
			t.Fatalf("plant a customer job: %v", err)
		}
		t.Cleanup(func() { rt1Del(t, w.pool, "customer_jobs", "id = $1", cj) })
		lj := w.lienJob(t, w.customerA, w.branchA)
		if _, err := w.svc.LinkCustomerJob(mgr, lj, cj); err != nil {
			t.Fatalf("link: %v", err)
		}
		if _, err := w.pool.Exec(ctx, `UPDATE lien_jobs SET substantial_perf_date = $2::date WHERE id = $1::uuid`, lj, day(perfDaysAgo)); err != nil {
			t.Fatalf("set the date: %v", err)
		}
		if preserved {
			if _, err := w.pool.Exec(ctx, `UPDATE lien_jobs SET lien_preserved_on = $2::date WHERE id = $1::uuid`, lj, day(1)); err != nil {
				t.Fatalf("preserve: %v", err)
			}
		}
		// Retention booked 100 days ago: the old window calls it OVERDUE whatever the period says.
		if _, err := w.svc.store.InsertHoldbackEntry(ctx, HoldbackEntry{JobID: lj, BranchID: w.branchA, EntryType: "RETENTION", AmountCents: 10000, CreatedAt: time.Now().AddDate(0, 0, -100)}); err != nil {
			t.Fatalf("seed retention: %v", err)
		}
		t.Cleanup(func() { rt1Del(t, w.pool, "holdback_ledger", "job_id = $1", lj) })
		return lj
	}
	waiting := plant(rule.PreserveDays-10, false)                 // the period has 10 days to run
	due := plant(rule.PreserveDays+10, false)                     // ran out 9 days ago
	farPast := plant(rule.PreserveDays+releaseRuleDays+10, false) // ran out well past the window
	blocked := plant(rule.PreserveDays+10, true)                  // ran out, but a lien is preserved

	items, err := w.svc.Triage(mgr, "")
	if err != nil {
		t.Fatalf("triage: %v", err)
	}
	badge := map[string]string{}
	for _, it := range items {
		badge[it.JobID] = it.Badge
	}
	for name, want := range map[string]struct{ id, badge string }{
		"the period has days to run": {waiting, "AGING"},
		"the period ran out":         {due, "RELEASE_DUE"},
		"ran out well past":          {farPast, "OVERDUE"},
		"a preserved lien blocks":    {blocked, "AGING"},
	} {
		if got := badge[want.id]; got != want.badge {
			t.Errorf("%s: want %s, got %q", name, want.badge, got)
		}
	}
}
