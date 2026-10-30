//go:build integration

package billing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

type creditChangeIntRig struct {
	pool           *pgxpool.Pool
	repo           *Repository
	svc            *CreditChangeService
	customerID     string
	branchID       string
	userSalesMgr   string
	userBranchMgr  string
	userBranchMgr2 string
	userController string
	userGM         string
	userClerk      string
}

func newCreditChangeIntRig(t *testing.T, initialLimitCents int64) *creditChangeIntRig {
	t.Helper()
	ctx := context.Background()
	pool := intPool(t)

	var branchID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type = 'BRANCH' LIMIT 1`).Scan(&branchID); err != nil {
		t.Skipf("no seeded BRANCH: %v", err)
	}
	suffix := fmt.Sprintf("cc1-%d", time.Now().UnixNano())

	var customerID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, payment_terms_days, credit_limit_cents)
		 VALUES ($1, $2, 'CONTRACTOR', $3::uuid, 30, $4)
		 RETURNING id::text`,
		"ACC-"+suffix, "Contractor "+suffix, branchID, initialLimitCents).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}

	seedUser := func(tag string) string {
		var uid string
		if err := pool.QueryRow(ctx,
			`INSERT INTO users (email, display_name, password_hash, status)
			 VALUES ($1, $2, 'hash', 'ACTIVE')
			 RETURNING id::text`,
			fmt.Sprintf("%s-%s@example.test", tag, suffix), "Staff "+tag).Scan(&uid); err != nil {
			t.Fatalf("seed user %s: %v", tag, err)
		}
		return uid
	}

	uSales := seedUser("salesmgr")
	uBM1 := seedUser("bm1")
	uBM2 := seedUser("bm2")
	uFC := seedUser("fc")
	uGM := seedUser("gm")
	uClerk := seedUser("clerk")

	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM credit_change_requests WHERE customer_id = $1::uuid`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM credit_holds WHERE customer_id = $1::uuid`, customerID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, customerID)
		for _, uid := range []string{uSales, uBM1, uBM2, uFC, uGM, uClerk} {
			_, _ = pool.Exec(c, `DELETE FROM users WHERE id = $1::uuid`, uid)
		}
	})

	repo := NewRepository(pool)
	custRepo := customer.NewRepository(pool)
	custSvc := customer.NewService(custRepo).WithWriter(custRepo)
	auditSvc := audit.NewService(audit.NewRepository(pool), nil)
	svc := NewCreditChangeService(repo, NewCustomerCreditStore(custSvc)).WithAudit(auditSvc)

	return &creditChangeIntRig{
		pool:           pool,
		repo:           repo,
		svc:            svc,
		customerID:     customerID,
		branchID:       branchID,
		userSalesMgr:   uSales,
		userBranchMgr:  uBM1,
		userBranchMgr2: uBM2,
		userController: uFC,
		userGM:         uGM,
		userClerk:      uClerk,
	}
}

func (r *creditChangeIntRig) currentLimit(t *testing.T) int64 {
	t.Helper()
	var limit int64
	if err := r.pool.QueryRow(context.Background(),
		`SELECT credit_limit_cents FROM customers WHERE id = $1::uuid`, r.customerID).Scan(&limit); err != nil {
		t.Fatalf("read customer credit limit: %v", err)
	}
	return limit
}

func TestCreditChangeIntegration_AllLegsAndAuditTrail(t *testing.T) {
	rig := newCreditChangeIntRig(t, 2000000) // $20,000.00
	ctx := context.Background()

	salesMgrCtx := ccCallerCtx(rig.userSalesMgr, "SALES_MANAGER", identity.TierManager, identity.ScopeBranch, rig.branchID)
	bm1Ctx := ccCallerCtx(rig.userBranchMgr, RoleBranchManager, identity.TierManager, identity.ScopeBranch, rig.branchID)
	bm2Ctx := ccCallerCtx(rig.userBranchMgr2, RoleBranchManager, identity.TierManager, identity.ScopeBranch, rig.branchID)
	fcCtx := ccCallerCtx(rig.userController, RoleFinancialController, identity.TierLeadership, identity.ScopeGlobal, "")
	gmCtx := ccCallerCtx(rig.userGM, RoleGeneralManager, identity.TierLeadership, identity.ScopeGlobal, "")
	clerkCtx := ccCallerCtx(rig.userClerk, "INSIDE_SALES", identity.TierFrontline, identity.ScopeBranch, rig.branchID)

	// Leg 5: Clerk cannot request an increase.
	_, err := rig.svc.Request(clerkCtx, CreateCreditChangeInput{
		CustomerID:    rig.customerID,
		NewLimitCents: i64Ptr(2500000),
		Reason:        "clerk attempt",
	})
	if !errors.Is(err, ErrCreditChangeForbidden) {
		t.Fatalf("clerk request: got %v, want ErrCreditChangeForbidden", err)
	}

	// Leg 1: Manager lowers limit from $20,000 to $10,000 -> applied at once.
	dec, err := rig.svc.Request(bm1Ctx, CreateCreditChangeInput{
		CustomerID:    rig.customerID,
		NewLimitCents: i64Ptr(1000000),
		Reason:        "lower exposure",
	})
	if err != nil {
		t.Fatalf("decrease request: %v", err)
	}
	if dec.Status != CreditChangeStatusApplied || rig.currentLimit(t) != 1000000 {
		t.Fatalf("after decrease: status=%q limit=%d, want APPLIED and 1000000", dec.Status, rig.currentLimit(t))
	}

	// Leg 1b: Manager places credit hold -> applied at once.
	holdReq, err := rig.svc.Request(bm1Ctx, CreateCreditChangeInput{
		CustomerID: rig.customerID,
		PlaceHold:  true,
		HoldKind:   HoldManual,
		Reason:     "disputed account review",
	})
	if err != nil {
		t.Fatalf("hold request: %v", err)
	}
	if holdReq.Status != CreditChangeStatusApplied || holdReq.HoldID == "" {
		t.Fatalf("hold request: status=%q hold_id=%q, want APPLIED with hold_id", holdReq.Status, holdReq.HoldID)
	}

	// Leg 2: Increase by $15,000 (from $10,000 to $25,000, <= $25,000 threshold) -> PENDING, 1 approval applies.
	inc1, err := rig.svc.Request(salesMgrCtx, CreateCreditChangeInput{
		CustomerID:    rig.customerID,
		NewLimitCents: i64Ptr(2500000),
		Reason:        "contract expansion",
	})
	if err != nil {
		t.Fatalf("request $15k increase: %v", err)
	}
	if inc1.Status != CreditChangeStatusPending || inc1.RequiredApprovals != 1 || rig.currentLimit(t) != 1000000 {
		t.Fatalf("inc1: status=%q required=%d limit=%d", inc1.Status, inc1.RequiredApprovals, rig.currentLimit(t))
	}

	// Leg 4a: Requester cannot approve their own request.
	if _, err := rig.svc.Approve(salesMgrCtx, inc1.ID, DecideCreditChangeInput{Note: "self"}); !errors.Is(err, ErrCreditChangeSelfApproval) {
		t.Fatalf("self approve: got %v, want ErrCreditChangeSelfApproval", err)
	}

	// Branch manager approves -> APPLIED at once.
	inc1Approved, err := rig.svc.Approve(bm1Ctx, inc1.ID, DecideCreditChangeInput{Note: "verified"})
	if err != nil {
		t.Fatalf("approve $15k increase: %v", err)
	}
	if inc1Approved.Status != CreditChangeStatusApplied || rig.currentLimit(t) != 2500000 {
		t.Fatalf("after 1st approval on <=25k: status=%q limit=%d, want APPLIED and 2500000", inc1Approved.Status, rig.currentLimit(t))
	}

	// Leg 3: Increase by $30,000 (from $25,000 to $55,000, > $25,000 threshold) -> requires 2 approvals.
	inc2, err := rig.svc.Request(salesMgrCtx, CreateCreditChangeInput{
		CustomerID:    rig.customerID,
		NewLimitCents: i64Ptr(5500000),
		Reason:        "commercial subdivision",
	})
	if err != nil {
		t.Fatalf("request $30k increase: %v", err)
	}
	if inc2.Status != CreditChangeStatusPending || inc2.RequiredApprovals != 2 {
		t.Fatalf("inc2: status=%q required=%d, want PENDING and 2", inc2.Status, inc2.RequiredApprovals)
	}

	// First approval by branch manager -> stays PENDING, limit stays 2500000.
	afterFirst, err := rig.svc.Approve(bm1Ctx, inc2.ID, DecideCreditChangeInput{Note: "branch ok"})
	if err != nil {
		t.Fatalf("first approve $30k: %v", err)
	}
	if afterFirst.Status != CreditChangeStatusPending || rig.currentLimit(t) != 2500000 {
		t.Fatalf("after first approval on >25k: status=%q limit=%d, want PENDING and 2500000", afterFirst.Status, rig.currentLimit(t))
	}

	// Leg 4b: Same person cannot approve twice.
	if _, err := rig.svc.Approve(bm1Ctx, inc2.ID, DecideCreditChangeInput{Note: "twice"}); !errors.Is(err, ErrCreditChangeDuplicateApprover) {
		t.Fatalf("duplicate approver: got %v, want ErrCreditChangeDuplicateApprover", err)
	}

	// Another branch manager cannot perform the second approval (must be Financial Controller or General Manager).
	if _, err := rig.svc.Approve(bm2Ctx, inc2.ID, DecideCreditChangeInput{Note: "peer bm"}); !errors.Is(err, ErrCreditChangeRoleForbidden) {
		t.Fatalf("second approval by branch manager: got %v, want ErrCreditChangeRoleForbidden", err)
	}

	// Financial Controller performs second approval -> APPLIED, limit becomes 5500000.
	afterSecond, err := rig.svc.Approve(fcCtx, inc2.ID, DecideCreditChangeInput{Note: "controller approved"})
	if err != nil {
		t.Fatalf("second approve $30k: %v", err)
	}
	if afterSecond.Status != CreditChangeStatusApplied || rig.currentLimit(t) != 5500000 {
		t.Fatalf("after second approval: status=%q limit=%d, want APPLIED and 5500000", afterSecond.Status, rig.currentLimit(t))
	}
	if afterSecond.FirstApprovedBy != rig.userBranchMgr || afterSecond.SecondApprovedBy != rig.userController {
		t.Fatalf("approvers = (%q, %q), want (%q, %q)",
			afterSecond.FirstApprovedBy, afterSecond.SecondApprovedBy, rig.userBranchMgr, rig.userController)
	}

	// Leg 6: Verify audit_log rows in Postgres carry before, after, requested_by, first_approved_by, second_approved_by.
	var (
		beforeCents int64
		afterCents  int64
		reqBy       string
		firstBy     string
		secondBy    string
	)
	if err := rig.pool.QueryRow(ctx, `
		SELECT (changes->>'before')::bigint,
		       (changes->>'after')::bigint,
		       COALESCE(changes->>'requested_by', ''),
		       COALESCE(changes->>'first_approved_by', ''),
		       COALESCE(changes->>'second_approved_by', '')
		  FROM audit_log
		 WHERE entity_type = 'customer'
		   AND entity_id = $1
		   AND action = $2
		   AND changes->>'field' = 'credit_limit_cents'
		 ORDER BY seq DESC
		 LIMIT 1`,
		rig.customerID, ActCreditChangeApplied).Scan(&beforeCents, &afterCents, &reqBy, &firstBy, &secondBy); err != nil {
		t.Fatalf("read audit_log row: %v", err)
	}
	if beforeCents != 2500000 || afterCents != 5500000 || reqBy != rig.userSalesMgr || firstBy != rig.userBranchMgr || secondBy != rig.userController {
		t.Fatalf("audit_log = before:%d after:%d req:%q first:%q second:%q",
			beforeCents, afterCents, reqBy, firstBy, secondBy)
	}

	// Also verify dealer-configurable thresholds in ar_credit_policy.
	if err := rig.repo.SetCreditChangeThresholds(ctx, "", 500000, 1000000); err != nil {
		t.Fatalf("SetCreditChangeThresholds: %v", err)
	}
	t.Cleanup(func() {
		_ = rig.repo.SetCreditChangeThresholds(context.Background(), "", DefaultCreditIncreaseFirstThresholdCents, DefaultCreditIncreaseSecondThresholdCents)
	})
	// With second threshold lowered to $10,000 (1000000 cents), a $15,000 increase now requires 2 approvals.
	incCustom, err := rig.svc.Request(salesMgrCtx, CreateCreditChangeInput{
		CustomerID:    rig.customerID,
		NewLimitCents: i64Ptr(7000000),
		Reason:        "custom threshold check",
	})
	if err != nil {
		t.Fatalf("request under custom threshold: %v", err)
	}
	if incCustom.RequiredApprovals != 2 {
		t.Fatalf("custom threshold required_approvals = %d, want 2", incCustom.RequiredApprovals)
	}
	rejected, err := rig.svc.Reject(gmCtx, incCustom.ID, DecideCreditChangeInput{Reason: "declined"})
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if rejected.Status != CreditChangeStatusRejected || rig.currentLimit(t) != 5500000 {
		t.Fatalf("after reject: status=%q limit=%d, want REJECTED and 5500000", rejected.Status, rig.currentLimit(t))
	}
}

// P2-1 Red leg: approve and reject raced on real Postgres, 20 rounds.
// In every round, exactly one wins, a request already decided is refused in words,
// and the customer's limit and audit trail match the request's final status.
func TestCreditChangeIntegration_ApproveAndRejectRaced20Rounds(t *testing.T) {
	rig := newCreditChangeIntRig(t, 1000000) // $10,000.00
	ctx := context.Background()

	salesMgrCtx := ccCallerCtx(rig.userSalesMgr, "SALES_MANAGER", identity.TierManager, identity.ScopeBranch, rig.branchID)
	bm1Ctx := ccCallerCtx(rig.userBranchMgr, RoleBranchManager, identity.TierManager, identity.ScopeBranch, rig.branchID)
	bm2Ctx := ccCallerCtx(rig.userBranchMgr2, RoleBranchManager, identity.TierManager, identity.ScopeBranch, rig.branchID)

	for round := 0; round < 20; round++ {
		if _, err := rig.pool.Exec(ctx, `UPDATE customers SET credit_limit_cents = 1000000 WHERE id = $1::uuid`, rig.customerID); err != nil {
			t.Fatalf("round %d reset limit: %v", round, err)
		}

		req, err := rig.svc.Request(salesMgrCtx, CreateCreditChangeInput{
			CustomerID:    rig.customerID,
			NewLimitCents: i64Ptr(1100000),
			Reason:        fmt.Sprintf("race round %d", round),
		})
		if err != nil {
			t.Fatalf("round %d request: %v", round, err)
		}

		start := make(chan struct{})
		var (
			wg     sync.WaitGroup
			appRes CreditChangeRequest
			appErr error
			rejRes CreditChangeRequest
			rejErr error
		)
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			appRes, appErr = rig.svc.Approve(bm1Ctx, req.ID, DecideCreditChangeInput{Note: "approve race"})
		}()
		go func() {
			defer wg.Done()
			<-start
			rejRes, rejErr = rig.svc.Reject(bm2Ctx, req.ID, DecideCreditChangeInput{Reason: "reject race"})
		}()
		close(start)
		wg.Wait()

		if (appErr == nil) == (rejErr == nil) {
			t.Fatalf("round %d: expected exactly one winner, got approveErr=%v rejectErr=%v", round, appErr, rejErr)
		}

		finalReq, err := rig.svc.Get(bm1Ctx, req.ID)
		if err != nil {
			t.Fatalf("round %d get request: %v", round, err)
		}
		gotLimit := rig.currentLimit(t)

		if appErr == nil {
			if finalReq.Status != CreditChangeStatusApplied || appRes.Status != CreditChangeStatusApplied {
				t.Fatalf("round %d: approve won but status = %q (resp %q), want APPLIED", round, finalReq.Status, appRes.Status)
			}
			if gotLimit != 1100000 {
				t.Fatalf("round %d: approve won (status=%s) but customer limit = %d, want 1100000", round, finalReq.Status, gotLimit)
			}
		} else {
			if finalReq.Status != CreditChangeStatusRejected || rejRes.Status != CreditChangeStatusRejected {
				t.Fatalf("round %d: reject won but status = %q (resp %q), want REJECTED", round, finalReq.Status, rejRes.Status)
			}
			if gotLimit != 1000000 {
				t.Fatalf("round %d: reject won (status=%s) but customer limit moved to %d, want 1000000", round, finalReq.Status, gotLimit)
			}
		}

		loserErr := appErr
		if loserErr == nil {
			loserErr = rejErr
		}
		if !errors.Is(loserErr, ErrCreditChangeNotPending) || !strings.Contains(loserErr.Error(), "already") {
			t.Fatalf("round %d: loser error = %v, want ErrCreditChangeNotPending naming already decided in words", round, loserErr)
		}
	}
}

// P2-2 Red leg (S2's probe on Postgres): customer at $45,000, request for $50,000 pending,
// limit lowered to $0, one approval. The limit must not reach $50,000 on that single approval,
// and the audit's "before" is the limit at approval ($0).
func TestCreditChangeIntegration_JudgeOnCurrentLimitAtApproval(t *testing.T) {
	rig := newCreditChangeIntRig(t, 4500000) // $45,000.00
	ctx := context.Background()

	salesMgrCtx := ccCallerCtx(rig.userSalesMgr, "SALES_MANAGER", identity.TierManager, identity.ScopeBranch, rig.branchID)
	bm1Ctx := ccCallerCtx(rig.userBranchMgr, RoleBranchManager, identity.TierManager, identity.ScopeBranch, rig.branchID)
	fcCtx := ccCallerCtx(rig.userController, RoleFinancialController, identity.TierLeadership, identity.ScopeGlobal, "")

	req, err := rig.svc.Request(salesMgrCtx, CreateCreditChangeInput{
		CustomerID:    rig.customerID,
		NewLimitCents: i64Ptr(5000000),
		Reason:        "modest top-up",
	})
	if err != nil {
		t.Fatalf("request $50,000: %v", err)
	}
	if req.Status != CreditChangeStatusPending || req.RequiredApprovals != 1 {
		t.Fatalf("after request: status=%q required=%d, want PENDING and 1", req.Status, req.RequiredApprovals)
	}

	dec, err := rig.svc.Request(salesMgrCtx, CreateCreditChangeInput{
		CustomerID:    rig.customerID,
		NewLimitCents: i64Ptr(0),
		Reason:        "pause credit exposure",
	})
	if err != nil {
		t.Fatalf("decrease to $0: %v", err)
	}
	if dec.Status != CreditChangeStatusApplied || rig.currentLimit(t) != 0 {
		t.Fatalf("after decrease: status=%q limit=%d, want APPLIED and 0", dec.Status, rig.currentLimit(t))
	}

	afterFirst, err := rig.svc.Approve(bm1Ctx, req.ID, DecideCreditChangeInput{Note: "branch approval"})
	if err != nil {
		t.Fatalf("first approval: %v", err)
	}
	if got := rig.currentLimit(t); got != 0 {
		t.Fatalf("customer limit after single approval = %d, want 0 (must not reach 5000000 on one approval)", got)
	}
	if afterFirst.Status != CreditChangeStatusPending || afterFirst.RequiredApprovals != 2 {
		t.Fatalf("after first approval: status=%q required=%d, want PENDING and 2", afterFirst.Status, afterFirst.RequiredApprovals)
	}
	if afterFirst.PreviousLimitCents != 0 || afterFirst.DeltaCents != 5000000 {
		t.Fatalf("after first approval: previous=%d delta=%d, want 0 and 5000000", afterFirst.PreviousLimitCents, afterFirst.DeltaCents)
	}

	afterSecond, err := rig.svc.Approve(fcCtx, req.ID, DecideCreditChangeInput{Note: "controller second approval"})
	if err != nil {
		t.Fatalf("second approval: %v", err)
	}
	if afterSecond.Status != CreditChangeStatusApplied || rig.currentLimit(t) != 5000000 {
		t.Fatalf("after second approval: status=%q limit=%d, want APPLIED and 5000000", afterSecond.Status, rig.currentLimit(t))
	}

	var beforeCents, afterCents int64
	if err := rig.pool.QueryRow(ctx, `
		SELECT (changes->>'before')::bigint, (changes->>'after')::bigint
		  FROM audit_log
		 WHERE entity_type = 'customer'
		   AND entity_id = $1
		   AND action = $2
		   AND changes->>'field' = 'credit_limit_cents'
		 ORDER BY seq DESC
		 LIMIT 1`,
		rig.customerID, ActCreditChangeApplied).Scan(&beforeCents, &afterCents); err != nil {
		t.Fatalf("read audit_log: %v", err)
	}
	if beforeCents != 0 || afterCents != 5000000 {
		t.Fatalf("applied audit_log before/after = %d/%d, want 0/5000000", beforeCents, afterCents)
	}
}

// P3 Red legs on Postgres:
// 1. A second threshold of 0 is honoured, not read as unset.
// 2. System hold kinds (NSF, AR_AGING, CREDIT_LIMIT, COD) cannot be placed by hand.
// 3. A hold on a customer who already holds that kind answers with the existing hold, in words.
func TestCreditChangeIntegration_ZeroSecondThresholdSystemHoldsAndExistingHold(t *testing.T) {
	rig := newCreditChangeIntRig(t, 1000000)
	ctx := context.Background()

	salesMgrCtx := ccCallerCtx(rig.userSalesMgr, "SALES_MANAGER", identity.TierManager, identity.ScopeBranch, rig.branchID)
	bm1Ctx := ccCallerCtx(rig.userBranchMgr, RoleBranchManager, identity.TierManager, identity.ScopeBranch, rig.branchID)
	fcCtx := ccCallerCtx(rig.userController, RoleFinancialController, identity.TierLeadership, identity.ScopeGlobal, "")

	// Leg 1: second threshold of 0 is honoured.
	if err := rig.repo.SetCreditChangeThresholds(ctx, "", 0, 0); err != nil {
		t.Fatalf("SetCreditChangeThresholds(0, 0): %v", err)
	}
	t.Cleanup(func() {
		_ = rig.repo.SetCreditChangeThresholds(context.Background(), "", DefaultCreditIncreaseFirstThresholdCents, DefaultCreditIncreaseSecondThresholdCents)
	})
	inc, err := rig.svc.Request(salesMgrCtx, CreateCreditChangeInput{
		CustomerID:    rig.customerID,
		NewLimitCents: i64Ptr(1010000), // +$100.00
		Reason:        "zero second threshold check",
	})
	if err != nil {
		t.Fatalf("request under 0/0 threshold: %v", err)
	}
	if inc.SecondThresholdCents != 0 || inc.RequiredApprovals != 2 {
		t.Fatalf("under 0/0 threshold: second_threshold=%d required_approvals=%d, want 0 and 2", inc.SecondThresholdCents, inc.RequiredApprovals)
	}
	if _, err := rig.svc.Approve(bm1Ctx, inc.ID, DecideCreditChangeInput{Note: "bm"}); err != nil {
		t.Fatalf("first approve under 0/0: %v", err)
	}
	if rig.currentLimit(t) != 1000000 {
		t.Fatalf("limit moved after 1st approval under 0/0: got %d, want 1000000", rig.currentLimit(t))
	}
	if _, err := rig.svc.Approve(fcCtx, inc.ID, DecideCreditChangeInput{Note: "fc"}); err != nil {
		t.Fatalf("second approve under 0/0: %v", err)
	}
	if rig.currentLimit(t) != 1010000 {
		t.Fatalf("limit after 2nd approval under 0/0: got %d, want 1010000", rig.currentLimit(t))
	}

	// Leg 2: system hold kinds cannot be placed by hand.
	for _, kind := range []string{HoldNSF, HoldARAging, HoldCreditLimit, HoldCOD} {
		_, err := rig.svc.Request(bm1Ctx, CreateCreditChangeInput{
			CustomerID: rig.customerID,
			PlaceHold:  true,
			HoldKind:   kind,
			Reason:     "attempt system hold by hand",
		})
		if !errors.Is(err, ErrCreditChangeInvalidTarget) || !strings.Contains(err.Error(), "MANUAL") {
			t.Fatalf("placing system hold kind %q by hand: got %v, want ErrCreditChangeInvalidTarget naming MANUAL", kind, err)
		}
	}

	// Leg 3: a hold on a customer who already holds that kind answers with the existing hold, in words.
	firstHold, err := rig.svc.Request(bm1Ctx, CreateCreditChangeInput{
		CustomerID: rig.customerID,
		PlaceHold:  true,
		HoldKind:   HoldManual,
		Reason:     "first manual hold",
	})
	if err != nil {
		t.Fatalf("first manual hold: %v", err)
	}
	if firstHold.HoldID == "" {
		t.Fatalf("first manual hold returned empty hold_id")
	}

	secondHold, err := rig.svc.Request(bm1Ctx, CreateCreditChangeInput{
		CustomerID: rig.customerID,
		PlaceHold:  true,
		HoldKind:   HoldManual,
		Reason:     "second manual hold while already held",
	})
	if err != nil {
		t.Fatalf("second manual hold: %v", err)
	}
	if secondHold.HoldID != firstHold.HoldID {
		t.Fatalf("second manual hold hold_id = %q, want existing %q", secondHold.HoldID, firstHold.HoldID)
	}
	if !strings.Contains(secondHold.FirstApprovalNote, "already") || !strings.Contains(secondHold.FirstApprovalNote, firstHold.HoldID) {
		t.Fatalf("second manual hold note = %q, want existing hold named in words", secondHold.FirstApprovalNote)
	}
}
