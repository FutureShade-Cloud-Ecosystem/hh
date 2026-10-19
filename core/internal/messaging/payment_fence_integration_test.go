//go:build integration

// HH-424 integration tests: the half of the payment fence that lives in SQL, the recipient read
// (platform identity, grant orgs, branch reach) on real rows, and the fence end to end on the real
// repository.
package messaging

import (
	"context"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// listedTrail is a PaymentAccess keyed by platform user id, standing in for the role gate: the gate's
// own walk is proven in cmd/core, here the fence only needs a reader to ask.
type listedTrail map[string]bool

func (l listedTrail) HoldsPaymentTrail(_ context.Context, platformUserID, _ string) (bool, error) {
	return l[platformUserID], nil
}

// bindPaymentTrailFor gives each named seeded user a platform identity and a live grant in a scratch
// org, binds a reader that says yes to exactly those users, and restores every row at the end. The
// seed plants none of them, so an integration test that reads or hears a money room must.
func bindPaymentTrailFor(t *testing.T, pool *pgxpool.Pool, svc *Service, userIDs ...string) {
	t.Helper()
	ctx := context.Background()
	var org string
	if err := pool.QueryRow(ctx,
		`INSERT INTO org (slug, name) VALUES ('it-pay-fence-' || substr(gen_random_uuid()::text, 1, 8), 'it') RETURNING id::text`).
		Scan(&org); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	trail := listedTrail{}
	for _, uid := range userIDs {
		pid := "p-it-" + uid
		if _, err := pool.Exec(ctx, `UPDATE users SET platform_user_id = $2 WHERE id = $1::uuid AND platform_user_id IS NULL`, uid, pid); err != nil {
			t.Fatalf("seed platform id: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO staff_role_grant (platform_user_id, role_id, org_id) VALUES ($1, 'general_manager', $2::uuid)`, pid, org); err != nil {
			t.Fatalf("seed grant: %v", err)
		}
		trail[pid] = true
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM staff_role_grant WHERE org_id = $1::uuid`, org)
		_, _ = pool.Exec(bg, `UPDATE users SET platform_user_id = NULL WHERE platform_user_id LIKE 'p-it-%'`)
		_, _ = pool.Exec(bg, `DELETE FROM org WHERE id = $1::uuid`, org)
	})
	svc.WithPaymentAccess(trail)
}

func TestIntegration_PaymentFence_RecipientReadsIdentityOrgsAndReach(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	bindPaymentTrailFor(t, pool, NewService(repo), uAlexMorgan, uSamRivera)
	ctx := context.Background()

	gm, err := repo.PaymentRecipient(ctx, uAlexMorgan)
	if err != nil {
		t.Fatalf("recipient: %v", err)
	}
	if gm.PlatformUserID != "p-it-"+uAlexMorgan || !gm.Global || len(gm.OrgIDs) != 1 {
		t.Fatalf("the general manager is global with one org, got %+v", gm)
	}
	sam, err := repo.PaymentRecipient(ctx, uSamRivera)
	if err != nil {
		t.Fatalf("recipient: %v", err)
	}
	if sam.Global || !reflect.DeepEqual(sam.BranchIDs, []string{branchTrenton}) {
		t.Fatalf("the sales manager reaches Trenton only, got %+v", sam)
	}
	none, err := repo.PaymentRecipient(ctx, "d7000000-0000-4000-9000-0000000000ff")
	if err != nil || none.PlatformUserID != "" {
		t.Fatalf("an unknown staffer is the zero value, got %+v %v", none, err)
	}
}

// A staffer who switched a rule on but holds no platform identity (so no trail the gate can confirm)
// hears nothing, on real rows.
func TestIntegration_PaymentFence_OptedInWithoutIdentityHearsNothing(t *testing.T) {
	pool := intPool(t)
	rollupCleanup(t, pool, uAlexMorgan, chPaymentsTrenton, chPaymentsGlobal)
	clearRules(t, pool, uSamRivera)
	ctx := context.Background()
	repo := NewRepository(pool)
	svc := NewService(repo).WithPaymentAccess(listedTrail{})
	if _, err := repo.SetPaymentRule(ctx, uSamRivera, RulePaymentCrossBranch, true); err != nil {
		t.Fatalf("opt in: %v", err)
	}
	paymentID := seedPayment(t, pool, branchKingston, 124000, true, true)

	res, err := svc.NotifyPaymentRules(ctx, "itest-fence-"+paymentID, paymentID, EventRef{Topic: TopicPaymentReceived})
	if err != nil {
		t.Fatalf("notify: %v", err)
	}
	if len(res.Notices) != 0 {
		t.Fatalf("no trail, no notice, got %+v", res.Notices)
	}
}

// A branch scoped staffer outside the payment's branches hears nothing even holding the trail.
func TestIntegration_PaymentFence_BranchOutsideReachHearsNothing(t *testing.T) {
	pool := intPool(t)
	rollupCleanup(t, pool, uAlexMorgan, chPaymentsTrenton, chPaymentsGlobal)
	clearRules(t, pool, uSamRivera)
	ctx := context.Background()
	repo := NewRepository(pool)
	svc := NewService(repo)
	bindPaymentTrailFor(t, pool, svc, uSamRivera)
	if _, err := repo.SetPaymentRule(ctx, uSamRivera, RulePaymentOnline, true); err != nil {
		t.Fatalf("opt in: %v", err)
	}
	// Taken at Kingston with no order: Sam reaches Trenton only.
	paymentID := seedPayment(t, pool, branchKingston, 124000, false, false)

	res, err := svc.NotifyPaymentRules(ctx, "itest-fence-"+paymentID, paymentID, EventRef{Topic: TopicPaymentReceived})
	if err != nil {
		t.Fatalf("notify: %v", err)
	}
	if len(res.Notices) != 0 {
		t.Fatalf("a Kingston payment is outside a Trenton reach, got %+v", res.Notices)
	}
}
