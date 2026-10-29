package billing

// credit_change_test.go tests the six CC-1 rules against an in-memory store and audit recorder:
//   1. A decrease (or placing a hold) applies at once, by any manager.
//   2. An increase of $20,000 (up to the $25,000 threshold) with one approval applies.
//   3. An increase of $30,000 (above the $25,000 threshold) with one approval stays pending,
//      and the second approver applies it.
//   4. The same person cannot approve twice, nor the requester at all.
//   5. A clerk cannot request an increase.
//   6. Each change writes an audit row with who asked, who approved, before and after.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

const (
	ccTestCustomerID = "11111111-1111-4111-8111-111111111111"
	ccTestBranchA    = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	ccTestBranchB    = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"

	ccUserSalesMgr   = "22222222-2222-4222-8222-222222222201"
	ccUserBranchMgr  = "22222222-2222-4222-8222-222222222202"
	ccUserBranchMgr2 = "22222222-2222-4222-8222-222222222206"
	ccUserCtrl       = "22222222-2222-4222-8222-222222222203"
	ccUserGM         = "22222222-2222-4222-8222-222222222204"
	ccUserClerk      = "22222222-2222-4222-8222-222222222205"
)

type fakeCreditChangeWorld struct {
	mu          sync.Mutex
	policy      CreditPolicy
	customers   map[string]*CustomerCreditFacts
	requests    map[string]CreditChangeRequest
	order       []string
	holds       map[string]string
	holdIDs     map[string]string
	audits      []audit.SensitiveAccess
	seq         int
	afterSetLim func()
}

func newFakeCreditChangeWorld(initialLimitCents int64) (*fakeCreditChangeWorld, *CreditChangeService) {
	w := &fakeCreditChangeWorld{
		policy: CreditPolicy{
			IncreaseFirstThresholdCents:  DefaultCreditIncreaseFirstThresholdCents,
			IncreaseSecondThresholdCents: DefaultCreditIncreaseSecondThresholdCents,
		},
		customers: map[string]*CustomerCreditFacts{
			ccTestCustomerID: {
				CustomerID:       ccTestCustomerID,
				HomeBranchID:     ccTestBranchA,
				CreditLimitCents: initialLimitCents,
			},
		},
		requests: map[string]CreditChangeRequest{},
		holds:    map[string]string{},
		holdIDs:  map[string]string{},
	}
	svc := NewCreditChangeService(w, w).WithAudit(w).WithClock(func() time.Time {
		return time.Unix(1700000000, 0).UTC()
	})
	return w, svc
}

type fakeCreditChangeTxKey struct{}

func (w *fakeCreditChangeWorld) lockIfNoTx(ctx context.Context) func() {
	if ctx != nil && ctx.Value(fakeCreditChangeTxKey{}) != nil {
		return func() {}
	}
	w.mu.Lock()
	return w.mu.Unlock
}

func (w *fakeCreditChangeWorld) InCreditChangeTx(ctx context.Context, fn func(context.Context) error) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return fn(context.WithValue(ctx, fakeCreditChangeTxKey{}, true))
}

func (w *fakeCreditChangeWorld) Record(ctx context.Context, a audit.SensitiveAccess) error {
	defer w.lockIfNoTx(ctx)()
	w.audits = append(w.audits, a)
	return nil
}

func (w *fakeCreditChangeWorld) CustomerCreditFacts(ctx context.Context, customerID string) (CustomerCreditFacts, bool, error) {
	defer w.lockIfNoTx(ctx)()
	c, ok := w.customers[customerID]
	if !ok {
		return CustomerCreditFacts{}, false, nil
	}
	return *c, true, nil
}

func (w *fakeCreditChangeWorld) SetCustomerCreditLimit(ctx context.Context, customerID string, newLimitCents int64) (int64, error) {
	unlock := w.lockIfNoTx(ctx)
	c, ok := w.customers[customerID]
	if !ok {
		unlock()
		return 0, ErrCreditChangeCustomerNotFound
	}
	prev := c.CreditLimitCents
	c.CreditLimitCents = newLimitCents
	hook := w.afterSetLim
	unlock()
	if hook != nil {
		hook()
	}
	return prev, nil
}

func (w *fakeCreditChangeWorld) CreditPolicy(ctx context.Context, _ string) (CreditPolicy, error) {
	defer w.lockIfNoTx(ctx)()
	return w.policy, nil
}

func (w *fakeCreditChangeWorld) PlaceHold(ctx context.Context, customerID, kind, _, _ string) (string, bool, error) {
	defer w.lockIfNoTx(ctx)()
	if _, exists := w.holds[customerID]; exists {
		return "", false, nil
	}
	w.seq++
	id := fmt.Sprintf("33333333-3333-4333-8333-%012d", w.seq)
	w.holds[customerID] = kind
	w.holdIDs[customerID] = id
	return id, true, nil
}

func (w *fakeCreditChangeWorld) ActiveHold(ctx context.Context, customerID string) (CreditHold, bool, error) {
	defer w.lockIfNoTx(ctx)()
	kind, ok := w.holds[customerID]
	if !ok {
		return CreditHold{}, false, nil
	}
	return CreditHold{
		ID:         w.holdIDs[customerID],
		CustomerID: customerID,
		Status:     "ACTIVE",
		Kind:       kind,
	}, true, nil
}

func (w *fakeCreditChangeWorld) CreateCreditChangeRequest(ctx context.Context, req CreditChangeRequest) (CreditChangeRequest, error) {
	defer w.lockIfNoTx(ctx)()
	if req.ID == "" {
		w.seq++
		req.ID = fmt.Sprintf("44444444-4444-4444-8444-%012d", w.seq)
	}
	w.requests[req.ID] = req
	w.order = append(w.order, req.ID)
	return req, nil
}

func (w *fakeCreditChangeWorld) GetCreditChangeRequest(ctx context.Context, id string) (CreditChangeRequest, error) {
	defer w.lockIfNoTx(ctx)()
	req, ok := w.requests[id]
	if !ok {
		return CreditChangeRequest{}, ErrCreditChangeNotFound
	}
	return req, nil
}

func (w *fakeCreditChangeWorld) UpdateCreditChangeRequest(ctx context.Context, req CreditChangeRequest) (CreditChangeRequest, error) {
	defer w.lockIfNoTx(ctx)()
	cur, ok := w.requests[req.ID]
	if !ok {
		return CreditChangeRequest{}, ErrCreditChangeNotFound
	}
	if cur.Status != CreditChangeStatusPending {
		return CreditChangeRequest{}, ErrCreditChangeNotPending
	}
	w.requests[req.ID] = req
	return req, nil
}

func (w *fakeCreditChangeWorld) ListCreditChangeRequests(ctx context.Context, f CreditChangeFilter, reach BranchReach) ([]CreditChangeRequest, error) {
	defer w.lockIfNoTx(ctx)()
	var out []CreditChangeRequest
	for i := len(w.order) - 1; i >= 0; i-- {
		r := w.requests[w.order[i]]
		if f.CustomerID != "" && r.CustomerID != f.CustomerID {
			continue
		}
		if f.Status != "" && r.Status != f.Status {
			continue
		}
		if !reach.Global {
			allowed := r.BranchID == ""
			for _, b := range reach.IDs {
				if r.BranchID == b {
					allowed = true
					break
				}
			}
			if !allowed {
				continue
			}
		}
		out = append(out, r)
	}
	return out, nil
}

func ccCallerCtx(userID, roleCode string, tier identity.Tier, scope identity.Scope, branchID string) context.Context {
	branches := map[string]bool{}
	if branchID != "" {
		branches[branchID] = true
	}
	return identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID:   userID,
		RoleCode: roleCode,
		Tier:     tier,
		Scope:    scope,
		Status:   "ACTIVE",
		Branches: branches,
	})
}

func i64Ptr(v int64) *int64 { return &v }

// Leg 1: A decrease (and placing a hold) applies at once, by any manager.
func TestCreditChange_DecreaseAppliesAtOnce(t *testing.T) {
	w, svc := newFakeCreditChangeWorld(5000000) // $50,000.00
	mgrCtx := ccCallerCtx(ccUserSalesMgr, "SALES_MANAGER", identity.TierManager, identity.ScopeBranch, ccTestBranchA)

	res, err := svc.Request(mgrCtx, CreateCreditChangeInput{
		CustomerID:    ccTestCustomerID,
		NewLimitCents: i64Ptr(2000000), // lower to $20,000.00
		Reason:        "seasonal slowdown",
	})
	if err != nil {
		t.Fatalf("decrease request failed: %v", err)
	}
	if res.Status != CreditChangeStatusApplied {
		t.Fatalf("decrease status = %q, want %q", res.Status, CreditChangeStatusApplied)
	}
	if got := w.customers[ccTestCustomerID].CreditLimitCents; got != 2000000 {
		t.Fatalf("customer credit limit after decrease = %d, want 2000000", got)
	}

	holdRes, err := svc.Request(mgrCtx, CreateCreditChangeInput{
		CustomerID: ccTestCustomerID,
		PlaceHold:  true,
		Reason:     "disputed account review",
	})
	if err != nil {
		t.Fatalf("place hold request failed: %v", err)
	}
	if holdRes.Status != CreditChangeStatusApplied {
		t.Fatalf("hold status = %q, want %q", holdRes.Status, CreditChangeStatusApplied)
	}
	if w.holds[ccTestCustomerID] != HoldManual {
		t.Fatalf("customer hold kind = %q, want %q", w.holds[ccTestCustomerID], HoldManual)
	}
}

// Leg 2: An increase of $20,000 with one approval applies.
func TestCreditChange_IncreaseUnder25kAppliesWithOneApproval(t *testing.T) {
	w, svc := newFakeCreditChangeWorld(1000000) // $10,000.00
	reqCtx := ccCallerCtx(ccUserSalesMgr, "SALES_MANAGER", identity.TierManager, identity.ScopeBranch, ccTestBranchA)
	bmCtx := ccCallerCtx(ccUserBranchMgr, RoleBranchManager, identity.TierManager, identity.ScopeBranch, ccTestBranchA)

	// Request an increase of $20,000.00 (to $30,000.00).
	req, err := svc.Request(reqCtx, CreateCreditChangeInput{
		CustomerID:    ccTestCustomerID,
		NewLimitCents: i64Ptr(3000000),
		Reason:        "new framing job contract",
	})
	if err != nil {
		t.Fatalf("request $20,000 increase: %v", err)
	}
	if req.Status != CreditChangeStatusPending {
		t.Fatalf("status after request = %q, want PENDING", req.Status)
	}
	if req.RequiredApprovals != 1 {
		t.Fatalf("required approvals = %d, want 1", req.RequiredApprovals)
	}
	if got := w.customers[ccTestCustomerID].CreditLimitCents; got != 1000000 {
		t.Fatalf("customer limit moved before approval: got %d, want 1000000", got)
	}

	// Branch manager approves -> applies immediately.
	approved, err := svc.Approve(bmCtx, req.ID, DecideCreditChangeInput{Note: "verified contract"})
	if err != nil {
		t.Fatalf("branch manager approve: %v", err)
	}
	if approved.Status != CreditChangeStatusApplied {
		t.Fatalf("status after one approval = %q, want APPLIED", approved.Status)
	}
	if approved.FirstApprovedBy != ccUserBranchMgr {
		t.Fatalf("first_approved_by = %q, want %q", approved.FirstApprovedBy, ccUserBranchMgr)
	}
	if got := w.customers[ccTestCustomerID].CreditLimitCents; got != 3000000 {
		t.Fatalf("customer limit after approval = %d, want 3000000", got)
	}
}

// Leg 3: An increase of $30,000 with one approval stays pending, and the second approver applies it.
func TestCreditChange_IncreaseOver25kStaysPendingUntilSecondApproverApplies(t *testing.T) {
	w, svc := newFakeCreditChangeWorld(1000000) // $10,000.00
	reqCtx := ccCallerCtx(ccUserSalesMgr, "SALES_MANAGER", identity.TierManager, identity.ScopeBranch, ccTestBranchA)
	bmCtx := ccCallerCtx(ccUserBranchMgr, RoleBranchManager, identity.TierManager, identity.ScopeBranch, ccTestBranchA)
	ctrlCtx := ccCallerCtx(ccUserCtrl, RoleFinancialController, identity.TierLeadership, identity.ScopeGlobal, "")

	// Request an increase of $30,000.00 (to $40,000.00).
	req, err := svc.Request(reqCtx, CreateCreditChangeInput{
		CustomerID:    ccTestCustomerID,
		NewLimitCents: i64Ptr(4000000),
		Reason:        "multi-unit subdivision project",
	})
	if err != nil {
		t.Fatalf("request $30,000 increase: %v", err)
	}
	if req.Status != CreditChangeStatusPending || req.RequiredApprovals != 2 {
		t.Fatalf("after request: status=%q required=%d, want PENDING and 2", req.Status, req.RequiredApprovals)
	}

	// First approval by branch manager -> stays PENDING.
	afterFirst, err := svc.Approve(bmCtx, req.ID, DecideCreditChangeInput{Note: "first approval"})
	if err != nil {
		t.Fatalf("first approval failed: %v", err)
	}
	if afterFirst.Status != CreditChangeStatusPending {
		t.Fatalf("status after first approval on $30,000 increase = %q, want PENDING", afterFirst.Status)
	}
	if got := w.customers[ccTestCustomerID].CreditLimitCents; got != 1000000 {
		t.Fatalf("customer limit moved after only one approval: got %d, want 1000000", got)
	}

	// Second approval by Controller -> applies.
	afterSecond, err := svc.Approve(ctrlCtx, req.ID, DecideCreditChangeInput{Note: "controller sign-off"})
	if err != nil {
		t.Fatalf("second approval failed: %v", err)
	}
	if afterSecond.Status != CreditChangeStatusApplied {
		t.Fatalf("status after second approval = %q, want APPLIED", afterSecond.Status)
	}
	if afterSecond.SecondApprovedBy != ccUserCtrl {
		t.Fatalf("second_approved_by = %q, want %q", afterSecond.SecondApprovedBy, ccUserCtrl)
	}
	if got := w.customers[ccTestCustomerID].CreditLimitCents; got != 4000000 {
		t.Fatalf("customer limit after second approval = %d, want 4000000", got)
	}
}

// Leg 4: The same person cannot approve twice, nor the requester at all.
func TestCreditChange_SamePersonAndRequesterCannotApprove(t *testing.T) {
	_, svc := newFakeCreditChangeWorld(1000000)
	bmCtx := ccCallerCtx(ccUserBranchMgr, RoleBranchManager, identity.TierManager, identity.ScopeBranch, ccTestBranchA)
	ctrlCtx := ccCallerCtx(ccUserCtrl, RoleFinancialController, identity.TierLeadership, identity.ScopeGlobal, "")

	// Branch manager requests a $30,000 increase.
	req, err := svc.Request(bmCtx, CreateCreditChangeInput{
		CustomerID:    ccTestCustomerID,
		NewLimitCents: i64Ptr(4000000),
		Reason:        "commercial build",
	})
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	// Requester tries to approve their own request -> refused.
	if _, err := svc.Approve(bmCtx, req.ID, DecideCreditChangeInput{}); !errors.Is(err, ErrCreditChangeSelfApproval) {
		t.Fatalf("requester approving own request: got %v, want ErrCreditChangeSelfApproval", err)
	}

	// Controller gives the first approval -> succeeds and stays PENDING.
	if _, err := svc.Approve(ctrlCtx, req.ID, DecideCreditChangeInput{}); err != nil {
		t.Fatalf("controller first approval: %v", err)
	}

	// Controller tries to approve a second time -> refused.
	if _, err := svc.Approve(ctrlCtx, req.ID, DecideCreditChangeInput{}); !errors.Is(err, ErrCreditChangeDuplicateApprover) {
		t.Fatalf("same person approving twice: got %v, want ErrCreditChangeDuplicateApprover", err)
	}

	// Requester tries to be the second approver -> still refused.
	if _, err := svc.Approve(bmCtx, req.ID, DecideCreditChangeInput{}); !errors.Is(err, ErrCreditChangeSelfApproval) {
		t.Fatalf("requester as second approver: got %v, want ErrCreditChangeSelfApproval", err)
	}
}

// Leg 5: A clerk cannot request an increase.
func TestCreditChange_ClerkCannotRequestIncrease(t *testing.T) {
	w, svc := newFakeCreditChangeWorld(1000000)
	clerkCtx := ccCallerCtx(ccUserClerk, "INSIDE_SALES", identity.TierFrontline, identity.ScopeBranch, ccTestBranchA)

	_, err := svc.Request(clerkCtx, CreateCreditChangeInput{
		CustomerID:    ccTestCustomerID,
		NewLimitCents: i64Ptr(1500000),
		Reason:        "customer asked at counter",
	})
	if !errors.Is(err, ErrCreditChangeForbidden) {
		t.Fatalf("clerk requesting increase: got %v, want ErrCreditChangeForbidden", err)
	}
	if len(w.requests) != 0 {
		t.Fatalf("clerk request created %d rows, want 0", len(w.requests))
	}
}

// Leg 6: Each change writes an audit row (who asked, who approved, before and after).
func TestCreditChange_EachChangeWritesAuditRow(t *testing.T) {
	w, svc := newFakeCreditChangeWorld(1000000) // $10,000.00
	reqCtx := ccCallerCtx(ccUserSalesMgr, "SALES_MANAGER", identity.TierManager, identity.ScopeBranch, ccTestBranchA)
	bmCtx := ccCallerCtx(ccUserBranchMgr, RoleBranchManager, identity.TierManager, identity.ScopeBranch, ccTestBranchA)
	gmCtx := ccCallerCtx(ccUserGM, RoleGeneralManager, identity.TierLeadership, identity.ScopeGlobal, "")

	// 1. Request a $30,000 increase (from $10,000 to $40,000) -> writes 1 audit row (requested).
	req, err := svc.Request(reqCtx, CreateCreditChangeInput{
		CustomerID:    ccTestCustomerID,
		NewLimitCents: i64Ptr(4000000),
		Reason:        "audit trail verification",
	})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if len(w.audits) != 1 {
		t.Fatalf("audit rows after request = %d, want 1", len(w.audits))
	}
	if w.audits[0].Extra["requested_by"] != ccUserSalesMgr || w.audits[0].Extra["before"] != int64(1000000) || w.audits[0].Extra["after"] != int64(4000000) {
		t.Fatalf("request audit row missing who asked or before/after: %+v", w.audits[0].Extra)
	}

	// 2. First approval by branch manager -> writes 2nd audit row (approved).
	if _, err := svc.Approve(bmCtx, req.ID, DecideCreditChangeInput{}); err != nil {
		t.Fatalf("first approve: %v", err)
	}
	if len(w.audits) != 2 {
		t.Fatalf("audit rows after first approval = %d, want 2", len(w.audits))
	}
	if w.audits[1].Extra["requested_by"] != ccUserSalesMgr || w.audits[1].Extra["first_approved_by"] != ccUserBranchMgr ||
		w.audits[1].Extra["before"] != int64(1000000) || w.audits[1].Extra["after"] != int64(4000000) {
		t.Fatalf("first approval audit row missing fields: %+v", w.audits[1].Extra)
	}

	// 3. Second approval by GM -> writes 3rd audit row (applied).
	if _, err := svc.Approve(gmCtx, req.ID, DecideCreditChangeInput{}); err != nil {
		t.Fatalf("second approve: %v", err)
	}
	if len(w.audits) != 3 {
		t.Fatalf("audit rows after second approval = %d, want 3", len(w.audits))
	}
	last := w.audits[2]
	if last.Extra["requested_by"] != ccUserSalesMgr ||
		last.Extra["first_approved_by"] != ccUserBranchMgr ||
		last.Extra["second_approved_by"] != ccUserGM ||
		last.Extra["before"] != int64(1000000) ||
		last.Extra["after"] != int64(4000000) {
		t.Fatalf("applied audit row missing who asked, who approved, or before/after: %+v", last.Extra)
	}
}

// P2-1 Red leg: approve and reject raced, 20 rounds. In every round, exactly one wins,
// a request already decided is refused in words, and the customer's limit matches the
// request's final status.
func TestCreditChange_ApproveAndRejectRaced20Rounds(t *testing.T) {
	reqCtx := ccCallerCtx(ccUserSalesMgr, "SALES_MANAGER", identity.TierManager, identity.ScopeBranch, ccTestBranchA)
	bm1Ctx := ccCallerCtx(ccUserBranchMgr, RoleBranchManager, identity.TierManager, identity.ScopeBranch, ccTestBranchA)
	bm2Ctx := ccCallerCtx(ccUserBranchMgr2, RoleBranchManager, identity.TierManager, identity.ScopeBranch, ccTestBranchA)

	for round := 0; round < 20; round++ {
		w, svc := newFakeCreditChangeWorld(1000000) // $10,000.00
		req, err := svc.Request(reqCtx, CreateCreditChangeInput{
			CustomerID:    ccTestCustomerID,
			NewLimitCents: i64Ptr(1100000), // +$1,000.00
			Reason:        fmt.Sprintf("race round %d", round),
		})
		if err != nil {
			t.Fatalf("round %d request: %v", round, err)
		}

		// Yield inside SetCustomerCreditLimit when no transaction lock is held so a concurrent
		// Reject lands in the gap between applying the limit and marking the request.
		limitSet := make(chan struct{})
		w.afterSetLim = func() {
			select {
			case <-limitSet:
			default:
				close(limitSet)
			}
			time.Sleep(time.Millisecond)
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
			appRes, appErr = svc.Approve(bm1Ctx, req.ID, DecideCreditChangeInput{Note: "approve"})
		}()
		go func() {
			defer wg.Done()
			<-start
			if round%2 == 0 {
				<-limitSet
			}
			rejRes, rejErr = svc.Reject(bm2Ctx, req.ID, DecideCreditChangeInput{Reason: "reject"})
		}()
		close(start)
		wg.Wait()

		if (appErr == nil) == (rejErr == nil) {
			t.Fatalf("round %d: expected exactly one winner, got approveErr=%v rejectErr=%v", round, appErr, rejErr)
		}

		finalReq, err := svc.Get(bm1Ctx, req.ID)
		if err != nil {
			t.Fatalf("round %d get request: %v", round, err)
		}
		gotLimit := w.customers[ccTestCustomerID].CreditLimitCents

		if appErr == nil {
			if finalReq.Status != CreditChangeStatusApplied || appRes.Status != CreditChangeStatusApplied {
				t.Fatalf("round %d: approve won but status = %q (resp %q), want APPLIED", round, finalReq.Status, appRes.Status)
			}
			if gotLimit != 1100000 {
				t.Fatalf("round %d: approve won but customer limit = %d, want 1100000", round, gotLimit)
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

// P2-2 Red leg (S2's probe): customer starts at $45,000, a request for $50,000 is pending
// (+$5,000 at request time, 1 approver), then the limit is lowered to $0, then one approval
// lands. The effective increase is now +$50,000 (above the $25,000 second threshold), so the
// single approval records as the first of two, the limit stays $0 (must not reach $50,000 on
// one approval), and the audit "before" is $0 (the limit at approval).
func TestCreditChange_JudgeOnCurrentLimitAtApproval(t *testing.T) {
	w, svc := newFakeCreditChangeWorld(4500000) // $45,000.00
	salesMgrCtx := ccCallerCtx(ccUserSalesMgr, "SALES_MANAGER", identity.TierManager, identity.ScopeBranch, ccTestBranchA)
	bmCtx := ccCallerCtx(ccUserBranchMgr, RoleBranchManager, identity.TierManager, identity.ScopeBranch, ccTestBranchA)
	ctrlCtx := ccCallerCtx(ccUserCtrl, RoleFinancialController, identity.TierLeadership, identity.ScopeGlobal, "")

	// 1. Sales manager requests $50,000 (+$5,000 from $45,000 -> 1 approval).
	req, err := svc.Request(salesMgrCtx, CreateCreditChangeInput{
		CustomerID:    ccTestCustomerID,
		NewLimitCents: i64Ptr(5000000),
		Reason:        "modest top-up",
	})
	if err != nil {
		t.Fatalf("request $50,000: %v", err)
	}
	if req.Status != CreditChangeStatusPending || req.RequiredApprovals != 1 {
		t.Fatalf("after request: status=%q required=%d, want PENDING and 1", req.Status, req.RequiredApprovals)
	}

	// 2. Sales manager lowers the limit to $0, which applies at once.
	dec, err := svc.Request(salesMgrCtx, CreateCreditChangeInput{
		CustomerID:    ccTestCustomerID,
		NewLimitCents: i64Ptr(0),
		Reason:        "pause credit exposure",
	})
	if err != nil {
		t.Fatalf("decrease to $0: %v", err)
	}
	if dec.Status != CreditChangeStatusApplied || w.customers[ccTestCustomerID].CreditLimitCents != 0 {
		t.Fatalf("after decrease: status=%q limit=%d, want APPLIED and 0", dec.Status, w.customers[ccTestCustomerID].CreditLimitCents)
	}

	// 3. Branch manager gives one approval. Because the current limit is $0, the effective
	// delta is +$50,000 (> $25,000 second threshold), so 2 approvals are now required.
	// The limit must stay $0 and must not reach $50,000 on that single approval.
	afterFirst, err := svc.Approve(bmCtx, req.ID, DecideCreditChangeInput{Note: "branch approval"})
	if err != nil {
		t.Fatalf("first approval: %v", err)
	}
	if got := w.customers[ccTestCustomerID].CreditLimitCents; got != 0 {
		t.Fatalf("customer limit after single approval = %d, want 0 (must not reach 5000000 on one approval)", got)
	}
	if afterFirst.Status != CreditChangeStatusPending || afterFirst.RequiredApprovals != 2 {
		t.Fatalf("after first approval: status=%q required=%d, want PENDING and 2", afterFirst.Status, afterFirst.RequiredApprovals)
	}
	if afterFirst.PreviousLimitCents != 0 || afterFirst.DeltaCents != 5000000 {
		t.Fatalf("after first approval: previous=%d delta=%d, want 0 and 5000000", afterFirst.PreviousLimitCents, afterFirst.DeltaCents)
	}
	firstAudit := w.audits[len(w.audits)-1]
	if firstAudit.Extra["before"] != int64(0) || firstAudit.Extra["after"] != int64(5000000) {
		t.Fatalf("first approval audit before/after = %v/%v, want 0/5000000", firstAudit.Extra["before"], firstAudit.Extra["after"])
	}

	// 4. Financial Controller gives the second approval -> applies $50,000 and audits before=0.
	afterSecond, err := svc.Approve(ctrlCtx, req.ID, DecideCreditChangeInput{Note: "controller second approval"})
	if err != nil {
		t.Fatalf("second approval: %v", err)
	}
	if afterSecond.Status != CreditChangeStatusApplied || w.customers[ccTestCustomerID].CreditLimitCents != 5000000 {
		t.Fatalf("after second approval: status=%q limit=%d, want APPLIED and 5000000", afterSecond.Status, w.customers[ccTestCustomerID].CreditLimitCents)
	}
	appliedAudit := w.audits[len(w.audits)-1]
	if appliedAudit.Extra["before"] != int64(0) || appliedAudit.Extra["after"] != int64(5000000) {
		t.Fatalf("applied audit before/after = %v/%v, want 0/5000000", appliedAudit.Extra["before"], appliedAudit.Extra["after"])
	}
}

// P3-1 Red leg: a second threshold of 0 is honoured (every increase needs two approvers),
// not read as unset ($25,000).
func TestCreditChange_SecondThresholdOfZeroRequiresTwoApprovals(t *testing.T) {
	w, svc := newFakeCreditChangeWorld(1000000)
	w.policy.IncreaseFirstThresholdCents = 0
	w.policy.IncreaseSecondThresholdCents = 0

	salesMgrCtx := ccCallerCtx(ccUserSalesMgr, "SALES_MANAGER", identity.TierManager, identity.ScopeBranch, ccTestBranchA)
	bmCtx := ccCallerCtx(ccUserBranchMgr, RoleBranchManager, identity.TierManager, identity.ScopeBranch, ccTestBranchA)
	ctrlCtx := ccCallerCtx(ccUserCtrl, RoleFinancialController, identity.TierLeadership, identity.ScopeGlobal, "")

	req, err := svc.Request(salesMgrCtx, CreateCreditChangeInput{
		CustomerID:    ccTestCustomerID,
		NewLimitCents: i64Ptr(1010000), // +$100.00
		Reason:        "small increase under zero second threshold",
	})
	if err != nil {
		t.Fatalf("request increase under 0/0 thresholds: %v", err)
	}
	if req.SecondThresholdCents != 0 || req.RequiredApprovals != 2 {
		t.Fatalf("second_threshold=%d required_approvals=%d, want 0 and 2", req.SecondThresholdCents, req.RequiredApprovals)
	}

	afterFirst, err := svc.Approve(bmCtx, req.ID, DecideCreditChangeInput{Note: "first approval"})
	if err != nil {
		t.Fatalf("first approval: %v", err)
	}
	if afterFirst.Status != CreditChangeStatusPending || w.customers[ccTestCustomerID].CreditLimitCents != 1000000 {
		t.Fatalf("after first approval: status=%q limit=%d, want PENDING and 1000000", afterFirst.Status, w.customers[ccTestCustomerID].CreditLimitCents)
	}

	afterSecond, err := svc.Approve(ctrlCtx, req.ID, DecideCreditChangeInput{Note: "second approval"})
	if err != nil {
		t.Fatalf("second approval: %v", err)
	}
	if afterSecond.Status != CreditChangeStatusApplied || w.customers[ccTestCustomerID].CreditLimitCents != 1010000 {
		t.Fatalf("after second approval: status=%q limit=%d, want APPLIED and 1010000", afterSecond.Status, w.customers[ccTestCustomerID].CreditLimitCents)
	}
}

// P3-2 Red leg: system hold kinds (NSF, AR_AGING, CREDIT_LIMIT, COD) cannot be placed by hand.
func TestCreditChange_SystemHoldKindsCannotBePlacedByHand(t *testing.T) {
	w, svc := newFakeCreditChangeWorld(1000000)
	bmCtx := ccCallerCtx(ccUserBranchMgr, RoleBranchManager, identity.TierManager, identity.ScopeBranch, ccTestBranchA)

	for _, kind := range []string{HoldNSF, HoldARAging, HoldCreditLimit, HoldCOD} {
		_, err := svc.Request(bmCtx, CreateCreditChangeInput{
			CustomerID: ccTestCustomerID,
			PlaceHold:  true,
			HoldKind:   kind,
			Reason:     "attempt system hold by hand",
		})
		if !errors.Is(err, ErrCreditChangeInvalidTarget) || !strings.Contains(err.Error(), "MANUAL") {
			t.Fatalf("placing system hold kind %q by hand: got %v, want ErrCreditChangeInvalidTarget naming MANUAL", kind, err)
		}
	}
	if len(w.holds) != 0 {
		t.Fatalf("holds placed = %d, want 0", len(w.holds))
	}
}

// P3-3 Red leg: a hold on a customer who already holds that kind answers with the existing hold, in words.
func TestCreditChange_HoldOnAlreadyHeldCustomerAnswersWithExistingHoldInWords(t *testing.T) {
	_, svc := newFakeCreditChangeWorld(1000000)
	bmCtx := ccCallerCtx(ccUserBranchMgr, RoleBranchManager, identity.TierManager, identity.ScopeBranch, ccTestBranchA)

	first, err := svc.Request(bmCtx, CreateCreditChangeInput{
		CustomerID: ccTestCustomerID,
		PlaceHold:  true,
		HoldKind:   HoldManual,
		Reason:     "initial manual hold",
	})
	if err != nil {
		t.Fatalf("first hold request: %v", err)
	}
	if first.HoldID == "" {
		t.Fatalf("first hold request returned empty hold_id")
	}

	second, err := svc.Request(bmCtx, CreateCreditChangeInput{
		CustomerID: ccTestCustomerID,
		PlaceHold:  true,
		HoldKind:   HoldManual,
		Reason:     "second manual hold while already held",
	})
	if err != nil {
		t.Fatalf("second hold request: %v", err)
	}
	if second.HoldID != first.HoldID {
		t.Fatalf("second hold_id = %q, want existing hold_id %q", second.HoldID, first.HoldID)
	}
	if !strings.Contains(second.FirstApprovalNote, "already") || !strings.Contains(second.FirstApprovalNote, first.HoldID) {
		t.Fatalf("second hold note = %q, want existing hold named in words", second.FirstApprovalNote)
	}
}
