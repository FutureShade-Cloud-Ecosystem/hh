package order

// change_selfsign_test.go, separation of duties on a change order (HH-681 follow up, row 2268): a manager may
// not give the finance signature on a change they requested themselves. Both signing doors refuse the
// requester (FinanceApprove and the one tap from PENDING_FINANCE), the actions follow, a different manager
// still signs, and the requester may still apply a change that is already APPROVED.

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

const s3Other2 = "eeeeeeee-0000-4000-9000-000000000002" // a second manager of the same branch

func s3Requester(status string, fin FinanceApprover) (*Service, *stage3Store) {
	svc, store := s3Fixture(status, fin)
	store.change.RequestedBy = s3User
	store.queue[0].requestedByID = s3User
	return svc, store
}

func TestSelfSign_FinanceApproveRefusesTheRequester(t *testing.T) {
	svc, store := s3Requester(ChangeStatusPendingFinance, &countingApprover{})
	_, err := svc.FinanceApprove(ctxAs(s3Manager()), s3Change, "ok")
	if !errors.Is(err, ErrChangeSelfSign) {
		t.Fatalf("the requester signing their own change = %v, want ErrChangeSelfSign", err)
	}
	if len(store.financeCalls) != 0 {
		t.Fatalf("the signature reached the store %d times, want 0", len(store.financeCalls))
	}
}

func TestSelfSign_SignApplyRefusesTheRequesterOnPendingFinance(t *testing.T) {
	svc, store := s3Requester(ChangeStatusPendingFinance, &countingApprover{})
	_, err := svc.SignApplyChange(ctxAs(s3Manager()), s3Change, s3Key, "ok")
	if !errors.Is(err, ErrChangeSelfSign) {
		t.Fatalf("the requester signing and applying their own change = %v, want ErrChangeSelfSign", err)
	}
	if len(store.signCalls) != 0 {
		t.Fatalf("the one tap reached the store %d times, want 0", len(store.signCalls))
	}
}

func TestSelfSign_AnApprovedChangeStaysOpenToTheRequester(t *testing.T) {
	svc, store := s3Requester(ChangeStatusApproved, &countingApprover{})
	if _, err := svc.SignApplyChange(ctxAs(s3Manager()), s3Change, s3Key, ""); err != nil {
		t.Fatalf("the requester applying an already approved change = %v, want it applied", err)
	}
	if len(store.signCalls) != 1 {
		t.Fatalf("the store saw %d calls, want 1", len(store.signCalls))
	}
}

func TestSelfSign_ADifferentManagerStillSignsThroughBothDoors(t *testing.T) {
	other := managerAt(s3Other2, s3Branch)
	svc, store := s3Requester(ChangeStatusPendingFinance, &countingApprover{})
	if _, err := svc.FinanceApprove(ctxAs(other), s3Change, "ok"); err != nil {
		t.Fatalf("another manager signing = %v, want the signature", err)
	}
	if len(store.financeCalls) != 1 || store.financeCalls[0] != s3Other2 {
		t.Fatalf("the signature calls = %v, want one by the other manager", store.financeCalls)
	}
	svc, store = s3Requester(ChangeStatusPendingFinance, &countingApprover{})
	if _, err := svc.SignApplyChange(ctxAs(other), s3Change, s3Key, ""); err != nil {
		t.Fatalf("another manager's one tap = %v, want it signed and applied", err)
	}
	if len(store.signCalls) != 1 || store.signCalls[0].Signer != s3Other2 {
		t.Fatalf("the one tap calls = %+v, want one by the other manager", store.signCalls)
	}
}

// TestSelfSign_NextActionsNeverOfferWhatTheDoorRefuses: on PENDING_FINANCE the requester is offered neither
// sign_apply nor finance_approve (reject and cancel stay), a different manager is offered both, and GetChange
// and the queue agree for both callers.
func TestSelfSign_NextActionsNeverOfferWhatTheDoorRefuses(t *testing.T) {
	for _, c := range []struct {
		name   string
		caller identity.StaffContext
		offers bool
	}{
		{"the requester", s3Manager(), false},
		{"another manager", managerAt(s3Other2, s3Branch), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			svc, _ := s3Requester(ChangeStatusPendingFinance, &countingApprover{})
			ctx := ctxAs(c.caller)
			got, err := svc.GetChange(ctx, s3Change)
			if err != nil {
				t.Fatalf("GetChange: %v", err)
			}
			for _, a := range []string{"sign_apply", "finance_approve"} {
				if has := slices.Contains(got.NextActions, a); has != c.offers {
					t.Errorf("%s offered = %v for %s, want %v (%v)", a, has, c.name, c.offers, got.NextActions)
				}
			}
			if !slices.Contains(got.NextActions, "reject") {
				t.Errorf("reject must stay offered to a manager: %v", got.NextActions)
			}
			page, err := svc.ListChangeQueue(ctx, ChangeQueueRequest{})
			if err != nil || len(page.Items) != 1 {
				t.Fatalf("queue = %v, %d items", err, len(page.Items))
			}
			if !slices.Equal(got.NextActions, page.Items[0].NextActions) {
				t.Errorf("GetChange offers %v but the queue offers %v", got.NextActions, page.Items[0].NextActions)
			}
		})
	}
	// An approved change still offers the requester the apply.
	svc, _ := s3Requester(ChangeStatusApproved, &countingApprover{})
	got, _ := svc.GetChange(ctxAs(s3Manager()), s3Change)
	if !slices.Contains(got.NextActions, "sign_apply") || !slices.Contains(got.NextActions, "apply") {
		t.Errorf("an approved change must stay open to the requester: %v", got.NextActions)
	}
}

func TestSelfSign_TheRouteAnswers403InPlainWords(t *testing.T) {
	h, _ := s3Handler(ChangeStatusPendingFinance, nil)
	rr := httptest.NewRecorder()
	h.writeChangeErr(rr, ErrChangeSelfSign)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "You raised this change, so another manager signs it.") {
		t.Fatalf("body = %s, want the plain words", rr.Body.String())
	}
	// And through the real route handlers: the requester's finance approve and one tap both answer 403.
	svc, store := s3Requester(ChangeStatusPendingFinance, nil)
	h = NewHandler(svc)
	rr = httptest.NewRecorder()
	h.signApplyChange(rr, s3SignApplyRequest(s3Manager(), s3Key, ""), guards.StaffCaller{})
	if rr.Code != http.StatusForbidden || len(store.signCalls) != 0 {
		t.Fatalf("one tap by the requester = %d (store calls %d), want 403 and none", rr.Code, len(store.signCalls))
	}
}
