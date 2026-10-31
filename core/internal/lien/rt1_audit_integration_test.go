//go:build integration

package lien

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
)

// RT-1 fix round (PR 757 read): a rule change and its audit record are one transaction, and the record is
// valid and carries the percent. These legs use the REAL audit recorder, never a fake that accepts anything.

// realAudit is the real audit service on the world's pool.
func (w *rt1World) realAudit(t *testing.T) *audit.Service {
	t.Helper()
	a := audit.NewService(audit.NewRepository(w.pool), slog.Default())
	t.Cleanup(func() { rt1Del(t, w.pool, "audit_log", "actor_id = $1", w.userID) })
	return a
}

// trail counts this world's audit rows for one action and returns the percent field of the newest as text
// ("" when the row has no such field).
func (w *rt1World) trail(t *testing.T, action string) (n int, pct string) {
	t.Helper()
	ctx := context.Background()
	if err := w.pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE actor_id = $1 AND action = $2`, w.userID, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		return 0, ""
	}
	if err := w.pool.QueryRow(ctx,
		`SELECT COALESCE((changes->'holdback_pct_bps')::text, 'absent') FROM audit_log WHERE actor_id = $1 AND action = $2 ORDER BY seq DESC LIMIT 1`,
		w.userID, action).Scan(&pct); err != nil {
		t.Fatal(err)
	}
	return n, pct
}

func (w *rt1World) wantTrail(t *testing.T, action string, wantN int, wantPct string) {
	t.Helper()
	n, pct := w.trail(t, action)
	if n != wantN || (wantPct != "" && pct != wantPct) {
		t.Fatalf("%s: %d records, newest percent %q; want %d, %q", action, n, pct, wantN, wantPct)
	}
}

func TestIntegration_RT1_EverySetAndClearIsOneRecord(t *testing.T) {
	w := rt1Plant(t)
	w.svc.WithAudit(w.realAudit(t))
	ctx := w.as(w.manager(w.branchA))

	if _, err := w.svc.SetDealerOverride(ctx, ip(500)); err != nil {
		t.Fatalf("set the dealer override: %v", err)
	}
	w.wantTrail(t, actHoldbackRuleSet, 1, "500")
	if _, err := w.svc.SetDealerOverride(ctx, nil); err != nil {
		t.Fatalf("clear the dealer override: %v", err)
	}
	// A set and a clear are told apart: the clear carries no percent field (audit skips nil).
	w.wantTrail(t, actHoldbackRuleSet, 2, "absent")

	if err := w.svc.SetJobOverride(ctx, w.jobOnSiteON, ip(700)); err != nil {
		t.Fatalf("set the job override: %v", err)
	}
	w.wantTrail(t, actHoldbackRuleSet, 3, "700")
	if err := w.svc.SetJobOverride(ctx, w.jobOnSiteON, nil); err != nil {
		t.Fatalf("clear the job override: %v", err)
	}
	w.wantTrail(t, actHoldbackRuleSet, 4, "absent")

	lj := w.lienJob(t, w.customerA, w.branchA)
	if _, err := w.svc.LinkCustomerJob(ctx, lj, w.jobOnSiteON); err != nil {
		t.Fatalf("link: %v", err)
	}
	w.wantTrail(t, actHoldbackLink, 1, "")
	if _, err := w.svc.LinkCustomerJob(ctx, lj, ""); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	w.wantTrail(t, actHoldbackLink, 2, "")
	var withAfter int
	if err := w.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE actor_id = $1 AND action = $2 AND changes->>'after' = $3`, w.userID, actHoldbackLink, w.jobOnSiteON).Scan(&withAfter); err != nil || withAfter != 1 {
		t.Fatalf("exactly the link (not the unlink) names the job it linked to: %d, %v", withAfter, err)
	}
	// The dealer record names the dealer rule row by its own uuid.
	var eid string
	if err := w.pool.QueryRow(context.Background(),
		`SELECT entity_id::text FROM audit_log WHERE actor_id = $1 AND entity_type = 'lien.holdback_dealer_rule' LIMIT 1`, w.userID).Scan(&eid); err != nil {
		t.Fatalf("the dealer record must carry an entity id: %v", err)
	}
	if _, err := uuid.Parse(eid); err != nil {
		t.Fatalf("dealer record entity id %q is not a uuid", eid)
	}
}

// failingTx is a recorder whose record always fails, on either write path.
type failingTx struct{}

func (failingTx) Record(context.Context, audit.SensitiveAccess) error {
	return errors.New("record failed")
}
func (failingTx) RecordTx(context.Context, audit.Execer, audit.SensitiveAccess) error {
	return errors.New("record failed")
}

func TestIntegration_RT1_AFailedRecordRollsTheChangeBack(t *testing.T) {
	w := rt1Plant(t)
	ctx := w.as(w.manager(w.branchA))
	// Start from stored values the failed writes must leave alone.
	if _, err := w.svc.SetDealerOverride(ctx, ip(500)); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.SetJobOverride(ctx, w.jobOnSiteON, ip(700)); err != nil {
		t.Fatal(err)
	}
	lj := w.lienJob(t, w.customerA, w.branchA)
	if _, err := w.svc.LinkCustomerJob(ctx, lj, w.jobOnSiteON); err != nil {
		t.Fatal(err)
	}
	w.svc.WithAudit(failingTx{})

	if _, err := w.svc.SetDealerOverride(ctx, ip(900)); err == nil {
		t.Fatal("a failed record must fail the dealer set")
	}
	if _, err := w.svc.SetDealerOverride(ctx, nil); err == nil {
		t.Fatal("a failed record must fail the dealer clear")
	}
	if err := w.svc.SetJobOverride(ctx, w.jobOnSiteON, ip(100)); err == nil {
		t.Fatal("a failed record must fail the job set")
	}
	if err := w.svc.SetJobOverride(ctx, w.jobOnSiteON, nil); err == nil {
		t.Fatal("a failed record must fail the job clear")
	}
	if _, err := w.svc.LinkCustomerJob(ctx, lj, ""); err == nil {
		t.Fatal("a failed record must fail the unlink")
	}

	var dealer, job *int
	if err := w.pool.QueryRow(context.Background(), `SELECT holdback_pct_bps FROM holdback_dealer_rule`).Scan(&dealer); err != nil || dealer == nil || *dealer != 500 {
		t.Fatalf("dealer value after failed records = %v, %v; want 500", dealer, err)
	}
	if err := w.pool.QueryRow(context.Background(), `SELECT holdback_pct_bps FROM holdback_job_overrides WHERE customer_job_id = $1`, w.jobOnSiteON).Scan(&job); err != nil || job == nil || *job != 700 {
		t.Fatalf("job override after failed records = %v, %v; want 700", job, err)
	}
	if got := w.linked(t, lj); got != w.jobOnSiteON {
		t.Fatalf("link after a failed record = %q; want it unchanged", got)
	}
}

func TestIntegration_RT1_AJobsShipToProvinceNameReadsItsRule(t *testing.T) {
	w := rt1Plant(t)
	ctx := w.as(w.manager(w.branchA))
	for _, shipTo := range []string{`{"province":"Ontario"}`, `{"province":" ont. "}`, `{"province":"ontario"}`, `{"province":"on"}`} {
		var id string
		if err := w.pool.QueryRow(context.Background(),
			`INSERT INTO customer_jobs (customer_id, branch_id, job_no, name, status, ship_to_address)
			 VALUES ($1, $2, $3, 'RT1 name job', 'OPEN', $4::jsonb) RETURNING id::text`,
			w.customerA, w.branchA, "J-N-"+uuid.NewString()[:8], shipTo).Scan(&id); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { rt1Del(t, w.pool, "customer_jobs", "id = $1", id) })
		got, err := w.svc.HoldbackRuleForJob(ctx, id)
		if err != nil || got.Province != "ON" || got.HoldbackPctBps != 1000 {
			t.Fatalf("ship to %s: %+v, %v; want Ontario's rule", shipTo, got, err)
		}
	}
}

func TestIntegration_RT1_AMalformedLienJobIDIsNotFound(t *testing.T) {
	w := rt1Plant(t)
	ctx := w.as(w.manager(w.branchA))
	_, err := w.svc.LinkCustomerJob(ctx, "nope", w.jobOnSiteON)
	wantClientErr(t, err, 404, "not found")
	_, err = w.svc.GetJob(ctx, "nope")
	wantClientErr(t, err, 404, "not found")
}
