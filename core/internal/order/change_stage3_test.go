package order

// change_stage3_test.go, SCOPE:F10 stage 3 (HH-681): the manager signs and applies, in the service,
// over fakes. The money legs (tax re-assessed inside the apply, one outbox event, one idempotent
// write) need a database and live in change_stage3_integration_test.go.
//
// What these pin is the AUTHORITY ladder in front of the store: who may reach the one transaction at
// all, and that every refusal happens BEFORE it (a refused caller leaves the store untouched), plus
// the rule that a change's next_actions read the same from GetChange and from the queue.

import (
	"context"
	"errors"
	"slices"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

const (
	s3Branch = "bbbbbbbb-0000-4000-9000-000000000001"
	s3Other  = "bbbbbbbb-0000-4000-9000-000000000002"
	s3Order  = "dddddddd-0000-4000-9000-000000000001"
	s3Change = "cccccccc-0000-4000-9000-000000000001"
	s3User   = "eeeeeeee-0000-4000-9000-000000000001"
	s3Key    = "11111111-1111-4111-8111-111111111111"
)

// stage3Store is a changeStore over canned rows. The sign and apply is the call under test, so the
// fake records it and answers an APPLIED change; every other write is unreachable in these legs.
type stage3Store struct {
	*fakeStore
	change     ChangeOrder
	queue      []ChangeQueueItem
	signCalls  []signApplyWrite
	closeCalls []string
}

var _ changeStore = (*stage3Store)(nil)
var _ changeQueueStore = (*stage3Store)(nil)

func (f *stage3Store) GetChangeOrder(_ context.Context, id string) (ChangeOrder, error) {
	if id != f.change.ID {
		return ChangeOrder{}, ErrChangeNotFound
	}
	return f.change, nil
}
func (f *stage3Store) InsertChangeOrder(context.Context, ChangeOrder) (ChangeOrder, error) {
	return ChangeOrder{}, errors.New("unreachable")
}
func (f *stage3Store) ListChangeOrders(context.Context, string) ([]ChangeOrder, error) {
	return nil, errors.New("unreachable")
}
func (f *stage3Store) RecordYardAck(context.Context, string, string, string, string) (ChangeOrder, error) {
	return ChangeOrder{}, errors.New("unreachable")
}
func (f *stage3Store) RecordFinanceApproval(context.Context, string, string, string, string) (ChangeOrder, error) {
	return ChangeOrder{}, errors.New("unreachable")
}
func (f *stage3Store) CloseChangeOrder(_ context.Context, _, target, _, _ string) (ChangeOrder, error) {
	f.closeCalls = append(f.closeCalls, target)
	out := f.change
	out.Status = target
	return out, nil
}
func (f *stage3Store) ApplyChangeTx(context.Context, string, changeApplyHooks) (ChangeOrder, error) {
	return ChangeOrder{}, errors.New("unreachable")
}
func (f *stage3Store) ListChangeQueue(context.Context, changeQueueQuery) ([]ChangeQueueItem, error) {
	return f.queue, nil
}

// SignApplyChangeTx is the stage 3 store call. Its signature is the proposal in the design note.
func (f *stage3Store) SignApplyChangeTx(_ context.Context, w signApplyWrite) (ChangeOrder, error) {
	f.signCalls = append(f.signCalls, w)
	out := f.change
	out.Status = ChangeStatusApplied
	return out, nil
}

// countingApprover is the FinanceApprover seam with a call counter.
type countingApprover struct {
	err   error
	calls int
}

func (a *countingApprover) CheckApprover(context.Context, string, string, string) error {
	a.calls++
	return a.err
}

func s3Fixture(status string, fin FinanceApprover) (*Service, *stage3Store) {
	store := &stage3Store{
		fakeStore: newFakeStore(),
		change: ChangeOrder{
			ID: s3Change, OrderID: s3Order, BranchID: s3Branch, ChangeNo: "#CO-1", Status: status,
			DeltaCents: 60000, FinanceThresholdCents: 50000, FinanceRequired: true,
		},
		queue: []ChangeQueueItem{{
			ID: s3Change, OrderID: s3Order, BranchID: s3Branch, ChangeNo: "#CO-1", Status: status,
			DeltaCents: 60000, FinanceThresholdCents: 50000, FinanceRequired: true,
		}},
	}
	svc := NewService(store).withCaller(testCallerFrom)
	if fin != nil {
		svc.WithFinanceApprover(fin)
	}
	return svc, store
}

func s3Manager() identity.StaffContext { return managerAt(s3User, s3Branch) }

func TestStage3_SignApply_PendingFinanceManagerSignsAndAppliesOnce(t *testing.T) {
	svc, store := s3Fixture(ChangeStatusPendingFinance, &countingApprover{})
	out, err := svc.SignApplyChange(ctxAs(s3Manager()), s3Change, s3Key, "ok")
	if err != nil {
		t.Fatalf("a named manager signing a pending-finance change = %v, want it signed and applied", err)
	}
	if out.Status != ChangeStatusApplied {
		t.Fatalf("status = %q, want APPLIED", out.Status)
	}
	if len(store.signCalls) != 1 {
		t.Fatalf("the store saw %d sign-and-apply calls, want exactly 1 (one transaction)", len(store.signCalls))
	}
	if w := store.signCalls[0]; w.Signer != s3User || w.Key != s3Key || w.ChangeID != s3Change {
		t.Fatalf("sign-and-apply write = %+v, want the caller, the key and the change", w)
	}
}

func TestStage3_SignApply_ApprovedAppliesWithoutAskingFinanceAgain(t *testing.T) {
	fin := &countingApprover{err: errors.New("not a named approver")}
	svc, store := s3Fixture(ChangeStatusApproved, fin)
	if _, err := svc.SignApplyChange(ctxAs(s3Manager()), s3Change, s3Key, ""); err != nil {
		t.Fatalf("a manager applying an already signed change = %v, want it applied", err)
	}
	if fin.calls != 0 {
		t.Fatalf("the approver seam was asked %d times for an APPROVED change, want 0: the signature exists", fin.calls)
	}
	if len(store.signCalls) != 1 {
		t.Fatalf("the store saw %d calls, want 1", len(store.signCalls))
	}
}

func TestStage3_SignApply_RefusalsHappenBeforeTheStore(t *testing.T) {
	cases := []struct {
		name   string
		status string
		fin    *countingApprover
		caller identity.StaffContext
		key    string
		want   func(error) bool
	}{
		{"pending yard names the missing ack", ChangeStatusPendingYard, &countingApprover{}, s3Manager(), s3Key,
			func(e error) bool { return errors.Is(e, ErrChangeYardAckRequired) }},
		{"a non-manager cannot sign or apply", ChangeStatusApproved, &countingApprover{}, staffAt(s3User, s3Branch), s3Key,
			func(e error) bool { return errors.Is(e, ErrForbidden) }},
		{"a manager of another branch reads not found", ChangeStatusPendingFinance, &countingApprover{}, managerAt(s3User, s3Other), s3Key,
			func(e error) bool { return errors.Is(e, ErrChangeNotFound) }},
		{"a manager outside the approver list cannot sign a material delta", ChangeStatusPendingFinance,
			&countingApprover{err: errors.New("not a named approver")}, s3Manager(), s3Key,
			func(e error) bool { return errors.Is(e, ErrForbidden) }},
		{"an inactive manager is refused", ChangeStatusApproved, &countingApprover{}, inactiveStaff(s3User, s3Branch), s3Key,
			func(e error) bool { return errors.Is(e, ErrForbidden) }},
		{"the idempotency key is required", ChangeStatusApproved, &countingApprover{}, s3Manager(), "", isBadRequest},
		{"the idempotency key must be a uuid", ChangeStatusApproved, &countingApprover{}, s3Manager(), "not-a-key", isBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc, store := s3Fixture(c.status, c.fin)
			_, err := svc.SignApplyChange(ctxAs(c.caller), s3Change, c.key, "")
			if err == nil || !c.want(err) {
				t.Fatalf("SignApplyChange = %v, want the refusal named by the case", err)
			}
			if len(store.signCalls) != 0 {
				t.Fatalf("a refused caller reached the store %d times, want 0", len(store.signCalls))
			}
		})
	}
}

// TestStage3_NextActions_GetChangeAgreesWithTheQueue is PR 363's own flag: on PENDING_FINANCE the queue
// dropped finance_approve for a manager who is not a named approver while GetChange still offered it
// to every manager. Narrowing, never widening: both go through the FinanceApprover seam now.
func TestStage3_NextActions_GetChangeAgreesWithTheQueue(t *testing.T) {
	named := &countingApprover{}
	unnamed := &countingApprover{err: errors.New("not a named approver")}
	callers := map[string]identity.StaffContext{
		"manager": s3Manager(), "frontline": staffAt(s3User, s3Branch),
	}
	for _, status := range []string{ChangeStatusPendingYard, ChangeStatusPendingFinance, ChangeStatusApproved} {
		for callerName, caller := range callers {
			for finName, fin := range map[string]*countingApprover{"named": named, "unnamed": unnamed} {
				t.Run(status+"/"+callerName+"/"+finName, func(t *testing.T) {
					svc, _ := s3Fixture(status, fin)
					ctx := ctxAs(caller)
					got, err := svc.GetChange(ctx, s3Change)
					if err != nil {
						t.Fatalf("GetChange: %v", err)
					}
					page, err := svc.ListChangeQueue(ctx, ChangeQueueRequest{})
					if err != nil || len(page.Items) != 1 {
						t.Fatalf("ListChangeQueue = %v, %d items", err, len(page.Items))
					}
					if q := page.Items[0].NextActions; !slices.Equal(got.NextActions, q) {
						t.Fatalf("GetChange offers %v but the queue offers %v for the same caller and change",
							got.NextActions, q)
					}
				})
			}
		}
	}
}

// TestStage3_NextActions_OffersSignApplyToTheManagerWhoMayDoIt: one tap is offered exactly where the
// route would accept it, and withheld from a manager the approver list excludes.
func TestStage3_NextActions_OffersSignApplyToTheManagerWhoMayDoIt(t *testing.T) {
	cases := []struct {
		status string
		fin    *countingApprover
		want   bool
	}{
		{ChangeStatusPendingYard, &countingApprover{}, false},
		{ChangeStatusPendingFinance, &countingApprover{}, true},
		{ChangeStatusPendingFinance, &countingApprover{err: errors.New("no")}, false},
		{ChangeStatusApproved, &countingApprover{}, true},
		{ChangeStatusApproved, &countingApprover{err: errors.New("no")}, true},
		{ChangeStatusApplied, &countingApprover{}, false},
	}
	for _, c := range cases {
		svc, _ := s3Fixture(c.status, c.fin)
		got, err := svc.GetChange(ctxAs(s3Manager()), s3Change)
		if err != nil {
			t.Fatalf("GetChange: %v", err)
		}
		if has := slices.Contains(got.NextActions, "sign_apply"); has != c.want {
			t.Errorf("status %s, approver err %v: next_actions %v, sign_apply offered = %v, want %v",
				c.status, c.fin.err, got.NextActions, has, c.want)
		}
	}
}

// TestStage3_Decline_AuthorityIsEnforcedByTheService: reject is any staff while the yard decides and a
// manager's alone once the change waits on finance or is approved; cancel is the requester withdrawing, so
// the requester and a manager at every live state, no other staff. The service decides, not the route guard.
func TestStage3_Decline_AuthorityIsEnforcedByTheService(t *testing.T) {
	const requester = "ffffffff-0000-4000-9000-000000000001"
	other := staffAt(s3User, s3Branch)
	req := staffAt(requester, s3Branch)
	mgr := s3Manager()
	live := []string{ChangeStatusPendingYard, ChangeStatusPendingFinance, ChangeStatusApproved}
	for _, status := range live {
		for _, c := range []struct {
			who     string
			caller  identity.StaffContext
			target  string
			allowed bool
		}{
			{"manager rejects", mgr, ChangeStatusRejected, true},
			{"manager cancels", mgr, ChangeStatusCancelled, true},
			{"requester cancels", req, ChangeStatusCancelled, true},
			{"other staff cancels", other, ChangeStatusCancelled, false},
			{"other staff rejects", other, ChangeStatusRejected, status == ChangeStatusPendingYard},
			{"requester rejects", req, ChangeStatusRejected, status == ChangeStatusPendingYard},
		} {
			t.Run(status+"/"+c.who, func(t *testing.T) {
				svc, store := s3Fixture(status, nil)
				store.change.RequestedBy = requester
				ctx := ctxAs(c.caller)
				var err error
				if c.target == ChangeStatusRejected {
					_, err = svc.RejectChange(ctx, s3Change, "note")
				} else {
					_, err = svc.CancelChange(ctx, s3Change, "note")
				}
				if c.allowed && err != nil {
					t.Fatalf("%s at %s = %v, want it allowed", c.who, status, err)
				}
				if !c.allowed && !errors.Is(err, ErrForbidden) {
					t.Fatalf("%s at %s = %v, want ErrForbidden", c.who, status, err)
				}
				if got, want := len(store.closeCalls), map[bool]int{true: 1, false: 0}[c.allowed]; got != want {
					t.Fatalf("the store saw %d close calls, want %d", got, want)
				}
			})
		}
	}
}

// TestStage3_NextActions_RejectAndCancelFollowTheAuthority: a button is offered only where the route would
// accept it.
func TestStage3_NextActions_RejectAndCancelFollowTheAuthority(t *testing.T) {
	const requester = "ffffffff-0000-4000-9000-000000000001"
	for _, c := range []struct {
		status string
		caller identity.StaffContext
		reject bool
		cancel bool
	}{
		{ChangeStatusPendingYard, staffAt(s3User, s3Branch), true, false},
		{ChangeStatusPendingYard, staffAt(requester, s3Branch), true, true},
		{ChangeStatusPendingFinance, staffAt(s3User, s3Branch), false, false},
		{ChangeStatusPendingFinance, staffAt(requester, s3Branch), false, true},
		{ChangeStatusApproved, staffAt(requester, s3Branch), false, true},
		{ChangeStatusApproved, s3Manager(), true, true},
	} {
		svc, store := s3Fixture(c.status, nil)
		store.change.RequestedBy = requester
		got, err := svc.GetChange(ctxAs(c.caller), s3Change)
		if err != nil {
			t.Fatalf("GetChange: %v", err)
		}
		if has := slices.Contains(got.NextActions, "reject"); has != c.reject {
			t.Errorf("%s %s: reject offered = %v, want %v (%v)", c.status, c.caller.UserID, has, c.reject, got.NextActions)
		}
		if has := slices.Contains(got.NextActions, "cancel"); has != c.cancel {
			t.Errorf("%s %s: cancel offered = %v, want %v (%v)", c.status, c.caller.UserID, has, c.cancel, got.NextActions)
		}
	}
}
