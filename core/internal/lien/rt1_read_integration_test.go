//go:build integration

package lien

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
)

// RT-1 item 3: one read answers the rule for a job: the job's override, else the dealer's, else the
// province's (the job's site province, else the dealer's).

func wantRule(t *testing.T, got HoldbackRule, pct int, source string, preserve, perfect int, province string) {
	t.Helper()
	if got.HoldbackPctBps != pct || got.PctSource != source || got.PreserveDays != preserve || got.PerfectDays != perfect || got.Province != province {
		t.Fatalf("rule = %+v; want %d bps from %s, %d and %d days, province %s", got, pct, source, preserve, perfect, province)
	}
}

func TestIntegration_RT1_ReadOrder_ProvinceThenDealerThenJob(t *testing.T) {
	w := rt1Plant(t)
	mgr := w.as(w.manager(w.branchA))

	// Level 1: the province. A job with no site province takes the dealer's province (ON).
	got, err := w.svc.HoldbackRuleForJob(mgr, w.jobOnSiteON)
	if err != nil {
		t.Fatal(err)
	}
	wantRule(t, got, 1000, "PROVINCE", 60, 90, "ON")
	if got.ProvinceSource != "DEALER" {
		t.Fatalf("province source = %q, want DEALER", got.ProvinceSource)
	}
	// A job whose site names a province takes that province's rule, whatever the dealer's province.
	got, err = w.svc.HoldbackRuleForJob(mgr, w.jobOnSiteZA)
	if err != nil {
		t.Fatal(err)
	}
	wantRule(t, got, 500, "PROVINCE", 30, 45, "ZA")
	if got.ProvinceSource != "SITE" {
		t.Fatalf("province source = %q, want SITE", got.ProvinceSource)
	}

	// Level 2: the dealer's override beats the province's, and keeps the province's days.
	if _, err := w.svc.SetDealerOverride(mgr, ip(750)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{w.jobOnSiteON, w.jobOnSiteZA} {
		got, err = w.svc.HoldbackRuleForJob(mgr, id)
		if err != nil {
			t.Fatal(err)
		}
		if got.HoldbackPctBps != 750 || got.PctSource != "DEALER" {
			t.Fatalf("dealer level: %+v", got)
		}
	}
	if got.PreserveDays != 30 || got.PerfectDays != 45 {
		t.Fatalf("an override must keep the province's days: %+v", got)
	}

	// Level 3: the job's own override beats the dealer's, including zero percent.
	if err := w.svc.SetJobOverride(mgr, w.jobOnSiteON, ip(0)); err != nil {
		t.Fatal(err)
	}
	got, err = w.svc.HoldbackRuleForJob(mgr, w.jobOnSiteON)
	if err != nil {
		t.Fatal(err)
	}
	wantRule(t, got, 0, "JOB", 60, 90, "ON")
	// The other job still reads the dealer's.
	got, _ = w.svc.HoldbackRuleForJob(mgr, w.jobOnSiteZA)
	if got.PctSource != "DEALER" {
		t.Fatalf("a job override leaked to another job: %+v", got)
	}

	// Clearing falls back down the order.
	if err := w.svc.SetJobOverride(mgr, w.jobOnSiteON, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.SetDealerOverride(mgr, nil); err != nil {
		t.Fatal(err)
	}
	got, _ = w.svc.HoldbackRuleForJob(mgr, w.jobOnSiteON)
	wantRule(t, got, 1000, "PROVINCE", 60, 90, "ON")
}

func TestIntegration_RT1_ReadRefusals(t *testing.T) {
	w := rt1Plant(t)
	mgr := w.as(w.manager(w.branchA))
	if _, err := w.pool.Exec(context.Background(), `UPDATE customer_jobs SET ship_to_address = '{"province":"QQ"}' WHERE id = $1`, w.jobOnSiteON); err != nil {
		t.Fatal(err)
	}
	_, err := w.svc.HoldbackRuleForJob(mgr, w.jobOnSiteON)
	wantClientErr(t, err, 422, "no holdback rule for the province QQ")

	_, err = w.svc.HoldbackRuleForJob(mgr, "00000000-0000-4000-8000-000000000000")
	wantClientErr(t, err, 404, "not found")

	// A caller whose grants do not reach the job's branch is told the job is not found.
	_, err = w.svc.HoldbackRuleForJob(w.as(w.manager(w.branchB)), w.jobOnSiteZA)
	wantClientErr(t, err, 404, "not found")
}

// The read must ride a caller's transaction: on a pool of ONE connection, inside a transaction that holds
// that connection, a read that asks the pool for another would wait forever (the car 18 lesson).
func TestIntegration_RT1_ReadRidesCallerTxOnAPoolOfOne(t *testing.T) {
	w := rt1Plant(t)
	cfg, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	one, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()
	custRepo := customer.NewRepository(one)
	svc := NewService(NewRepository(one)).WithCustomerJobs(NewCustomerJobs(customer.NewService(custRepo).WithJobs(custRepo)))
	svc.callerFrom = testCallerFrom

	ctx, cancel := context.WithTimeout(w.as(w.manager(w.branchA)), 10*time.Second)
	defer cancel()
	tx, err := one.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	got, err := svc.HoldbackRuleForJob(customer.WithCallerTx(ctx, tx), w.jobOnSiteZA)
	if err != nil {
		t.Fatalf("the read did not ride the caller's transaction on a pool of one: %v", err)
	}
	wantRule(t, got, 500, "PROVINCE", 30, 45, "ZA")
}
