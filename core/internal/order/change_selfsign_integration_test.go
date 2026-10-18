//go:build integration

// change_selfsign_integration_test.go, separation of duties on a change order against the live database: the
// requester cannot give the finance signature through either door, the one tap re-decides it under the order
// lock in the store path itself, a different manager still signs, and the requester still applies a change
// that is already APPROVED.
package order

import (
	"context"
	"errors"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// a second seeded user, a manager of the same branch as w5aUser (the requester in these legs)
const sodOtherManager = "40000061-0000-4000-9000-000000000002"

func sodOtherCtx() context.Context {
	sc := w5aStaff(true)
	sc.UserID = sodOtherManager
	return identity.ContextWithStaff(context.Background(), sc)
}

func TestIntegration_SoD_TheRequesterCannotSignThroughEitherDoor(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "PICKING", 10, 6000)
	s3CleanOutbox(t, pool, ord)
	svc := s3Service(t, pool, 6000, s3RateBps)
	co := s3RaisePendingFinance(t, svc, ord) // raised by w5aUser, who is a manager

	if _, err := svc.FinanceApprove(s3Mgr(), co.ID, "ok"); !errors.Is(err, ErrChangeSelfSign) {
		t.Fatalf("finance approve by the requester = %v, want ErrChangeSelfSign", err)
	}
	if _, err := svc.SignApplyChange(s3Mgr(), co.ID, s3Key, "ok"); !errors.Is(err, ErrChangeSelfSign) {
		t.Fatalf("one tap by the requester = %v, want ErrChangeSelfSign", err)
	}
	if s := s3Status(t, pool, co.ID); s != ChangeStatusPendingFinance {
		t.Fatalf("status = %q after the refusals, want PENDING_FINANCE untouched", s)
	}
	if got := w5aLineQty(t, pool, ord, 1); got != 10 {
		t.Fatalf("line qty = %v, want the original 10", got)
	}
	if n := s3Outbox(t, pool, co.ID); n != 0 {
		t.Fatalf("%d outbox rows after refused signatures, want 0", n)
	}
}

// TestIntegration_SoD_TheStorePathRedecidesItUnderTheLock: the service refuses first, but the one tap's store
// transaction re-decides from the locked change row, so a caller that reaches the store directly is refused
// too and nothing is written.
func TestIntegration_SoD_TheStorePathRedecidesItUnderTheLock(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "PICKING", 10, 6000)
	s3CleanOutbox(t, pool, ord)
	svc := s3Service(t, pool, 6000, s3RateBps)
	co := s3RaisePendingFinance(t, svc, ord)

	_, err := NewRepository(pool).SignApplyChangeTx(context.Background(), signApplyWrite{
		ChangeID: co.ID, Key: s3Key, Signer: w5aUser, Hooks: svc.changeApplyHooks(w5aUser, ord, s3Key, ""),
	})
	if !errors.Is(err, ErrChangeSelfSign) {
		t.Fatalf("the store path for the requester = %v, want ErrChangeSelfSign", err)
	}
	if s := s3Status(t, pool, co.ID); s != ChangeStatusPendingFinance {
		t.Fatalf("status = %q, want PENDING_FINANCE untouched", s)
	}
	if got := w5aLineQty(t, pool, ord, 1); got != 10 {
		t.Fatalf("line qty = %v, want the original 10", got)
	}
}

func TestIntegration_SoD_ADifferentManagerStillSignsAndApplies(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "PICKING", 10, 6000)
	s3CleanOutbox(t, pool, ord)
	svc := s3Service(t, pool, 6000, s3RateBps)
	co := s3RaisePendingFinance(t, svc, ord)

	out, err := svc.SignApplyChange(sodOtherCtx(), co.ID, s3Key, "approved")
	if err != nil {
		t.Fatalf("another manager's one tap = %v, want it signed and applied", err)
	}
	if out.Status != ChangeStatusApplied || out.FinanceApprovedBy != sodOtherManager {
		t.Fatalf("status %q signed by %q, want APPLIED signed by the other manager", out.Status, out.FinanceApprovedBy)
	}
}

func TestIntegration_SoD_ADifferentManagerSignsThroughFinanceApprove(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "PICKING", 10, 6000)
	s3CleanOutbox(t, pool, ord)
	svc := s3Service(t, pool, 6000, s3RateBps)
	co := s3RaisePendingFinance(t, svc, ord)

	out, err := svc.FinanceApprove(sodOtherCtx(), co.ID, "approved")
	if err != nil || out.Status != ChangeStatusApproved {
		t.Fatalf("another manager's finance approve = %q, %v, want APPROVED", out.Status, err)
	}
	// The requester may then apply what someone else signed.
	applied, err := svc.SignApplyChange(s3Mgr(), co.ID, s3Key, "")
	if err != nil || applied.Status != ChangeStatusApplied {
		t.Fatalf("the requester applying a change another manager signed = %q, %v, want APPLIED", applied.Status, err)
	}
}

func TestIntegration_SoD_AnImmaterialChangeStaysOpenToTheRequester(t *testing.T) {
	pool := intPool(t)
	_, ord := w5aSeedOrderWithPricedLine(t, pool, "PICKING", 10, s3Immaterl)
	s3CleanOutbox(t, pool, ord)
	svc := s3Service(t, pool, s3Immaterl, s3RateBps)
	co := s3RaiseApproved(t, svc, ord, "12") // under the threshold: no signature owed

	out, err := svc.SignApplyChange(s3Mgr(), co.ID, s3Key, "")
	if err != nil || out.Status != ChangeStatusApplied {
		t.Fatalf("the requester applying an under threshold change = %q, %v, want APPLIED", out.Status, err)
	}
}
