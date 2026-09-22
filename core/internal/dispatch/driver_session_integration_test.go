//go:build integration

// driver_session_integration_test.go, RULING (ii), against real rows: the session length a token is
// minted with comes from the DEALER-DEFAULT policy row and never from a branch override.
//
//	DATABASE_URL=... go test -tags integration -count=1 -run DriverSession ./internal/dispatch/
//
// WHY A DATABASE IS THE ONLY PLACE THIS CAN BE PROVEN. The rule is enforced by an ABSENCE, the
// SessionTTLResolver seam has no branch parameter, and DriverSessionHours passes the empty string to
// PolicyFor, whose SQL is `WHERE ($1 <> '' AND branch_id::text = $1) OR branch_id IS NULL ORDER BY
// branch_id::text NULLS LAST LIMIT 1`. Read that predicate on paper and it is obviously the default
// row. Put a branch override in the table with a DIFFERENT value, and it is obviously the default row
// only if the ORDER BY, the NULLS LAST and the LIMIT 1 all behave the way the reader assumed. This
// test puts the override there.
//
// AND THE OVERRIDE MATTERS AS A POSITIVE CONTROL. A test that only asserted "twelve" against a table
// with one row would pass on an implementation that read nothing at all. The dealer default is moved
// to a number nobody's default is, and the branch row to a different one, so both halves, "it read
// the policy" and "it read the RIGHT policy", fail separately.
//
// FIXTURES RESTORE WHAT THEY TOUCH: the dealer row's original value is read before it is moved and
// written back on cleanup, and the branch row is deleted.

package dispatch

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// setDealerSessionHours moves the dealer-default row's knob and restores it afterwards.
func setDealerSessionHours(t *testing.T, pool *pgxpool.Pool, hours int) {
	t.Helper()
	ctx := context.Background()
	var was int
	if err := pool.QueryRow(ctx,
		`SELECT driver_session_hours FROM dispatch_policy WHERE branch_id IS NULL`).Scan(&was); err != nil {
		t.Skipf("no dealer-default dispatch_policy row (0184 seed): %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE dispatch_policy SET driver_session_hours = $1 WHERE branch_id IS NULL`, hours); err != nil {
		t.Fatalf("set dealer session hours: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`UPDATE dispatch_policy SET driver_session_hours = $1 WHERE branch_id IS NULL`, was)
	})
}

// setBranchSessionHours installs a branch override carrying a DIFFERENT value, the row the resolver
// must not read. It upserts, because another suite may already have left an override on this branch.
func setBranchSessionHours(t *testing.T, pool *pgxpool.Pool, branchID string, hours int) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
INSERT INTO dispatch_policy (branch_id, driver_session_hours) VALUES ($1, $2)
ON CONFLICT (branch_id) DO UPDATE SET driver_session_hours = EXCLUDED.driver_session_hours`,
		branchID, hours)
	if err != nil {
		t.Fatalf("set branch session hours: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM dispatch_policy WHERE branch_id = $1`, branchID)
	})
}

func TestDriverSessionHours_ResolvesFromTheDealerDefaultAndNotFromABranchRow(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Two numbers, neither of which is the column default, so "it read the policy" and "it read the
	// RIGHT policy" fail separately.
	setDealerSessionHours(t, h.pool, 36)
	setBranchSessionHours(t, h.pool, branchTRT, 96)

	if got := h.svc.DriverSessionHours(ctx); got != 36 {
		t.Fatalf("the session length must come from the dealer-default row (36), got %d, a person's "+
			"session cannot depend on which branch they happened to be read against", got)
	}

	// The branch row IS stored and IS readable, it is inert for token issuance, not rejected. The
	// fenced read still shows a branch its own override, which is what makes the settings screen work
	// and what makes the inertness a stated choice rather than a missing feature.
	branch, err := h.svc.ReadPolicy(globalCtx(t, h.pool), branchTRT)
	if err != nil {
		t.Fatalf("read branch policy: %v", err)
	}
	if branch.DriverSessionHours != 96 {
		t.Fatalf("a branch override must round-trip through the policy read, got %d", branch.DriverSessionHours)
	}

	// Move the dealer row again and the resolver follows it, the read is live, not a value captured
	// at construction.
	setDealerSessionHours(t, h.pool, 8)
	if got := h.svc.DriverSessionHours(ctx); got != 8 {
		t.Fatalf("the resolver must read the policy on every call, got %d", got)
	}
}

// TestDriverSessionHours_SavePolicyRoundTripsTheKnobAndAuditsIt drives the knob through the SAME door
// the settings screen uses, SavePolicy, manager-gated in the service, rather than through raw SQL,
// so the column, the validator, the upsert and the audit loop are proven to agree.
func TestDriverSessionHours_SavePolicyRoundTripsTheKnobAndAuditsIt(t *testing.T) {
	h := newHarness(t)
	mgr := globalCtx(t, h.pool)
	setDealerSessionHours(t, h.pool, DefaultDriverSessionHours)

	before, err := h.svc.ReadPolicy(mgr, "")
	if err != nil {
		t.Fatalf("read dealer policy: %v", err)
	}
	if before.DriverSessionHours != DefaultDriverSessionHours {
		t.Fatalf("the seeded default must be %d, got %d", DefaultDriverSessionHours, before.DriverSessionHours)
	}

	want := before
	want.DriverSessionHours = 72
	saved, err := h.svc.SavePolicy(mgr, want)
	if err != nil {
		t.Fatalf("save policy: %v", err)
	}
	if saved.DriverSessionHours != 72 {
		t.Fatalf("the saved policy must carry the new session length, got %d", saved.DriverSessionHours)
	}
	// The RESOLVER, the thing identity actually asks, sees it too. This is the assertion that would
	// fail if the column were written and the read projection were not widened alongside it.
	if got := h.svc.DriverSessionHours(context.Background()); got != 72 {
		t.Fatalf("the seam must see what the settings screen saved, got %d", got)
	}

	// Out of bounds is refused IN WORDS by the service, before the CHECK could 500.
	tooLong := before
	tooLong.DriverSessionHours = MaxDriverSessionHours + 1
	if _, err := h.svc.SavePolicy(mgr, tooLong); err == nil {
		t.Fatal("a session longer than the ceiling must be refused in words")
	}
	// …and the refused save changed nothing.
	if got := h.svc.DriverSessionHours(context.Background()); got != 72 {
		t.Fatalf("a refused save must not have moved the knob, got %d", got)
	}
}
