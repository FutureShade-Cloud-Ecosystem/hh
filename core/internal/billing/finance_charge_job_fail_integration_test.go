//go:build integration

// finance_charge_job_fail_integration_test.go, the FC-1b fix round: the night job names each failing
// branch and its cause.
//
// Run: go test -tags integration ./internal/billing/ -run W3FC1BFix
package billing

import (
	"strings"
	"testing"
	"time"
)

// P3-3: one failing branch is named in the sweep, its error and the job's record.
func TestIntegration_W3FC1BFix_JobNamesTheFailingBranch(t *testing.T) {
	r := fcRigNew(t)
	cust := r.customer(r.branch, "JOBFAIL")
	r.tradeInvoice(cust, r.branch, fc1Due31, 100_000, "JOBFAIL")
	const ghost = "6c1fffff-0000-4000-8000-0000000000ee"
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, fc1Loc())

	sweep, err := r.sweep(now, r.branch, ghost)
	if err == nil || sweep.Failed != 1 || sweep.Charged != 1 {
		t.Fatalf("W3-FC1b-fix red: the sweep = %+v, %v, want 1 charged and 1 failed", sweep, err)
	}
	if !strings.Contains(err.Error(), ghost) {
		t.Fatalf("W3-FC1b-fix red: the sweep's error %q does not name branch %s", err, ghost)
	}
	if len(sweep.Failures) != 1 || sweep.Failures[0].BranchID != ghost || sweep.Failures[0].Cause == "" {
		t.Fatalf("W3-FC1b-fix red: the sweep's failures = %+v, want %s with its cause", sweep.Failures, ghost)
	}
	res, jerr := financeChargeJobResult(sweep, err)
	if jerr == nil {
		t.Fatalf("W3-FC1b-fix red: the job result lost the error")
	}
	fails, _ := res.Detail["failures"].([]map[string]any)
	if len(fails) != 1 || fails[0]["branch_id"] != ghost || fails[0]["cause"] == "" {
		t.Fatalf("W3-FC1b-fix red: the job's detail = %v, want the failing branch and its cause", res.Detail)
	}
}
