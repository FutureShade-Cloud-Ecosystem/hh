//go:build integration

// dib483_autohold_integration_test.go, M3-G7B-NO-HOLD-SWITCH against a LIVE Postgres.
//
// The unit tests prove the surface's shape. This proves the thing the gate actually cared about: that
// the column 0104 shipped FALSE can now be turned ON and back OFF from the application, that the write
// touches ONLY that column, and that a branch which inherits the org default is not silently forked
// off it.
//
// Run: DATABASE_URL=… go test -tags=integration ./internal/billing/... -run DIB483 -count=1
package billing

import (
	"context"
	"testing"
)

// readPolicyRow reads the org-default row directly, so the assertions are about the DATABASE and not
// about the code path that wrote it.
func readOrgPolicyRow(t *testing.T, ctx context.Context, r *Repository) (enabled bool, bucket int, aging, tol int64, agingOn, limitOn, cod bool) {
	t.Helper()
	err := r.pool.QueryRow(ctx, `
		SELECT auto_hold_enabled, aging_bucket_days, aging_threshold_cents, limit_tolerance_cents,
		       aging_hold_enabled, limit_hold_enabled, cod_enforced
		  FROM ar_credit_policy WHERE branch_id IS NULL`).
		Scan(&enabled, &bucket, &aging, &tol, &agingOn, &limitOn, &cod)
	if err != nil {
		t.Fatalf("read org policy row: %v", err)
	}
	return
}

// TestIntegration_DIB483_AutoHoldSwitchFlipsTheShippedColumn is the acceptance: off as shipped, on by
// the switch, off again, and nothing else moves.
func TestIntegration_DIB483_AutoHoldSwitchFlipsTheShippedColumn(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewCreditService(repo)
	ctx := context.Background()

	before, bucket0, aging0, tol0, agingOn0, limitOn0, cod0 := readOrgPolicyRow(t, ctx, repo)
	t.Cleanup(func() {
		// restore whatever the row said when this test started, including the shipped FALSE, so the
		// suite is re-runnable and cannot leave a machine armed.
		_ = repo.SetAutoHoldEnabled(context.Background(), "", before)
	})

	if !svc.AutoHoldSwitchWired() {
		t.Fatal("the real Repository must satisfy the write seam, otherwise the switch is dead in production")
	}

	// ON
	if _, err := svc.SetAutoHold(ctx, "", true, "integration-test", "DIB-483 acceptance"); err != nil {
		t.Fatalf("turn on: %v", err)
	}
	on, bucket1, aging1, tol1, agingOn1, limitOn1, cod1 := readOrgPolicyRow(t, ctx, repo)
	if !on {
		t.Fatal("the column did not flip to TRUE, before DIB-483 nothing in this codebase could write it")
	}
	if bucket1 != bucket0 || aging1 != aging0 || tol1 != tol0 ||
		agingOn1 != agingOn0 || limitOn1 != limitOn0 || cod1 != cod0 {
		t.Fatal("flipping the switch changed a THRESHOLD. The write must name one column: those numbers " +
			"are the client's and arrive by a different route entirely.")
	}

	// OFF again, reversible in one click is the property that makes it safe to offer at all.
	if _, err := svc.SetAutoHold(ctx, "", false, "integration-test", "DIB-483 acceptance"); err != nil {
		t.Fatalf("turn off: %v", err)
	}
	if off, _, _, _, _, _, _ := readOrgPolicyRow(t, ctx, repo); off {
		t.Fatal("the switch would not turn back off")
	}
}

// TestIntegration_DIB483_AutoHoldReadMatchesTheRow: what the admin surface shows is what the nightly
// sweep will read. Same row, same resolver.
func TestIntegration_DIB483_AutoHoldReadMatchesTheRow(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()

	got, err := NewCreditService(repo).AutoHold(ctx, "")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	enabled, bucket, aging, tol, agingOn, limitOn, cod := readOrgPolicyRow(t, ctx, repo)
	if got.Enabled != enabled || got.AgingBucketDays != bucket || got.AgingThresholdCents != aging ||
		got.LimitToleranceCents != tol || got.AgingHoldEnabled != agingOn ||
		got.LimitHoldEnabled != limitOn || got.CODEnforced != cod {
		t.Fatalf("the surface disagrees with the row it claims to show:\n surface %+v\n row     "+
			"(enabled=%v bucket=%d aging=%d tol=%d agingOn=%v limitOn=%v cod=%v)",
			got, enabled, bucket, aging, tol, agingOn, limitOn, cod)
	}
	if got.Scope != "ORG" {
		t.Fatalf("scope = %q, want ORG", got.Scope)
	}
	if got.ThresholdsEditable {
		t.Fatal("thresholds must never be reported editable")
	}
}

// TestIntegration_DIB483_BranchWithoutItsOwnRowIsRefusedClearly. A branch that INHERITS the org
// default has no row to update. Creating one silently would fork it off the org policy forever, a
// consequence nobody asked for while clicking a switch, so the refusal says what is actually true and
// what to do instead.
func TestIntegration_DIB483_BranchWithoutItsOwnRowIsRefusedClearly(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()

	var branchID string
	if err := pool.QueryRow(ctx,
		`SELECT l.id::text FROM locations l
		  WHERE l.loc_type = 'BRANCH'
		    AND NOT EXISTS (SELECT 1 FROM ar_credit_policy p WHERE p.branch_id = l.id)
		  LIMIT 1`).Scan(&branchID); err != nil {
		t.Skipf("every branch has its own policy row already: %v", err)
	}

	_, err := NewCreditService(repo).SetAutoHold(ctx, branchID, true, "integration-test", "")
	if err == nil {
		t.Fatal("a branch with no policy row was silently given one, it now stops inheriting the org " +
			"default, which is not what clicking a switch should mean")
	}
	for _, want := range []string{"inherits", "org"} {
		if !containsFold(err.Error(), want) {
			t.Fatalf("the refusal does not explain inheritance: %v", err)
		}
	}

	// and nothing was written.
	var rows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM ar_credit_policy WHERE branch_id = $1::uuid`, branchID).Scan(&rows); err != nil {
		t.Fatalf("count branch rows: %v", err)
	}
	if rows != 0 {
		t.Fatalf("the refused flip created %d branch policy row(s)", rows)
	}
}

func containsFold(haystack, needle string) bool {
	return len(needle) == 0 || indexFold(haystack, needle) >= 0
}

func indexFold(s, sub string) int {
	ls, lsub := lower(s), lower(sub)
	for i := 0; i+len(lsub) <= len(ls); i++ {
		if ls[i:i+len(lsub)] == lsub {
			return i
		}
	}
	return -1
}

func lower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}
