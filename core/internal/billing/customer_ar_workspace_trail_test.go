package billing

// customer_ar_workspace_trail_test.go, UX-93: the workspace's payment list is held to the payment
// trail (ruling 87) and the customer is held to the caller's branch reach.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeTrail struct {
	held bool
	err  error
	// asked records the ids the handler passed.
	user, org string
}

func (f *fakeTrail) HoldsPaymentTrail(_ context.Context, user, org string) (bool, error) {
	f.user, f.org = user, org
	return f.held, f.err
}

type fakeReach struct {
	ok  bool
	err error
	id  string
}

func (f *fakeReach) CustomerReachable(_ context.Context, id string) (bool, error) {
	f.id = id
	return f.ok, f.err
}

const trailCustomerID = "c1b00000-0000-4000-a000-000000000099"

func trailStore() *fakeWorkspaceStore {
	at := time.Date(2026, 8, 11, 9, 30, 0, 0, time.UTC)
	return &fakeWorkspaceStore{
		header: CustomerWorkspaceHeader{CustomerID: trailCustomerID, Name: "Northgate", AccountNo: "NG-1", Status: AccountCurrent, TermsDays: 30,
			LastPaymentAt: &at, LastPaymentCents: 99_123},
		payments: []CustomerWorkspacePayment{
			{ID: "p-1", PaymentNo: "PAY-1", ReceivedAt: at, AmountCents: 50_000, Method: "EFT", UnappliedCents: 7_000},
		},
		creditNotes: []CustomerWorkspaceCreditNote{
			{ID: "c-1", MemoNo: "CM-1", IssuedAt: at, AmountCents: 25_000, UnappliedCents: 25_000},
		},
	}
}

func getWorkspace(t *testing.T, h *WorkspaceHandler) (int, map[string]json.RawMessage) {
	t.Helper()
	rr := httptest.NewRecorder()
	h.workspace(rr, newWorkspaceRequest(trailCustomerID, nil), staffCaller())
	var body map[string]json.RawMessage
	if rr.Code == http.StatusOK {
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return rr.Code, body
}

func TestWorkspacePayments_HeldWithTheTrail(t *testing.T) {
	trail := &fakeTrail{held: true}
	h := NewWorkspaceHandler(trailStore(), nil, nil).WithPaymentTrail(trail).WithCustomerReach(&fakeReach{ok: true})
	code, body := getWorkspace(t, h)
	if code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	var pays []CustomerWorkspacePayment
	if err := json.Unmarshal(body["payments"], &pays); err != nil || len(pays) != 1 {
		t.Fatalf("payments=%s err=%v", body["payments"], err)
	}
	if pays[0].AmountCents != 50_000 || pays[0].Method != "EFT" || pays[0].UnappliedCents != 7_000 {
		t.Fatalf("full row expected: %+v", pays[0])
	}
	if string(body["payments_withheld"]) != "false" {
		t.Fatalf("payments_withheld=%s", body["payments_withheld"])
	}
	if trail.user != staffCallerID {
		t.Fatalf("trail asked with user %q", trail.user)
	}
}

func TestWorkspacePayments_AbsentWithoutTheTrail(t *testing.T) {
	cases := map[string]*WorkspaceHandler{
		"denied":  NewWorkspaceHandler(trailStore(), nil, nil).WithPaymentTrail(&fakeTrail{held: false}).WithCustomerReach(&fakeReach{ok: true}),
		"unbound": NewWorkspaceHandler(trailStore(), nil, nil).WithCustomerReach(&fakeReach{ok: true}),
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			h.workspace(rr, newWorkspaceRequest(trailCustomerID, nil), staffCaller())
			if rr.Code != http.StatusOK {
				t.Fatalf("status=%d", rr.Code)
			}
			raw := rr.Body.String()
			// FLAKE-10: the word shaped needles stay byte scans, because a clock carries no
			// letters and no other field's figure can spell them. The FIGURES are read from the
			// PARSED body: a raw digit hunt over the body read the body's as_of clock's
			// nanoseconds, a uuid's hex digits and the tail of the seeded credit limit
			// ("500000") as the payment's figures.
			for _, leak := range []string{"PAY-1", "EFT", "last_payment"} {
				if strings.Contains(raw, leak) {
					t.Fatalf("payment detail %q leaked: %s", leak, raw)
				}
			}
			if leaks := workspaceFiguresIn(raw, 50000, 7000, 99123); len(leaks) != 0 {
				t.Fatalf("payment detail leaked: %v\n%s", leaks, raw)
			}
			var body map[string]json.RawMessage
			_ = json.Unmarshal(rr.Body.Bytes(), &body)
			if _, present := body["payments"]; present {
				t.Fatalf("payments must be absent, not blanked: %s", body["payments"])
			}
			if string(body["payments_withheld"]) != "true" {
				t.Fatalf("payments_withheld=%s", body["payments_withheld"])
			}
			// The register leaves the credit notes alone for a caller without the trail.
			if !strings.Contains(raw, "CM-1") {
				t.Fatalf("credit notes must stay: %s", raw)
			}
		})
	}
}

func TestWorkspaceActivity_PaymentRowsFollowTheTrail(t *testing.T) {
	for _, held := range []bool{true, false} {
		credit := &fakeCreditForWorkspace{found: false}
		h := NewWorkspaceHandler(trailStore(), credit, nil).WithPaymentTrail(&fakeTrail{held: held}).WithCustomerReach(&fakeReach{ok: true})
		if code, _ := getWorkspace(t, h); code != http.StatusOK {
			t.Fatalf("status=%d", code)
		}
		if credit.gotWithPayments != held {
			t.Fatalf("held=%v but activity asked withPayments=%v", held, credit.gotWithPayments)
		}
	}
}

func TestWorkspace_TrailErrorIs500(t *testing.T) {
	h := NewWorkspaceHandler(trailStore(), nil, nil).WithPaymentTrail(&fakeTrail{err: errors.New("boom")}).WithCustomerReach(&fakeReach{ok: true})
	if code, _ := getWorkspace(t, h); code != http.StatusInternalServerError {
		t.Fatalf("status=%d", code)
	}
}

func TestWorkspace_CustomerOutsideReachIsNotFound(t *testing.T) {
	store := trailStore()
	for name, reach := range map[string]WorkspaceReach{
		"out of reach": &fakeReach{ok: false},
		"unbound":      nil,
	} {
		t.Run(name, func(t *testing.T) {
			h := NewWorkspaceHandler(store, nil, nil).WithPaymentTrail(&fakeTrail{held: true})
			if reach != nil {
				h.WithCustomerReach(reach)
			}
			store.gotCustomerID = ""
			code, _ := getWorkspace(t, h)
			if code != http.StatusNotFound {
				t.Fatalf("status=%d", code)
			}
			if store.gotCustomerID != "" {
				t.Fatalf("the store was read for a customer out of reach")
			}
		})
	}
}

func TestWorkspaceNote_CustomerOutsideReachIsNotFound(t *testing.T) {
	store := trailStore()
	h := NewWorkspaceHandler(store, nil, nil).WithCustomerReach(&fakeReach{ok: false})
	r := httptest.NewRequest(http.MethodPost, "/notes", strings.NewReader(`{"kind":"NOTE","body":"x"}`))
	r.SetPathValue("customer_id", trailCustomerID)
	rr := httptest.NewRecorder()
	h.addNote(rr, r, staffCaller())
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rr.Code)
	}
	if len(store.addedNotes) != 0 {
		t.Fatalf("a note was written to a customer out of reach")
	}
}
