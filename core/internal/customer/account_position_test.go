package customer

// account_position_test.go, UX-97: the dealer's account position policy on the staff search and the
// by-id read. Allowed and refused each way, a HIDDEN branch, an unbound gate, and the wire asserted
// byte absent (the key is not there), never 0.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// branchGate admits only the branches it lists, recording what it was asked.
type branchGate struct {
	allow map[string]bool
	asked []string
}

func (g *branchGate) AccountPositionAllowed(_ context.Context, branchID string) bool {
	g.asked = append(g.asked, branchID)
	return g.allow[branchID]
}

func wireJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var someAccount = Account{CustomerID: readID, AccountNo: "A-1", Name: "Fixture Account", BranchID: "b-1",
	CreditLimitCents: 500_000, BalanceCents: 123_456, IsActive: true}

func TestAccountForWire_AllowedCarriesLimitAndBalance(t *testing.T) {
	g := &branchGate{allow: map[string]bool{"b-1": true}}
	got := wireJSON(t, NewService(newFakeStore()).WithPositionGate(g).AccountForWire(context.Background(), someAccount))
	for _, want := range []string{`"credit_limit_cents":500000`, `"balance_cents":123456`, `"available_credit_cents":376544`, `"account_no":"A-1"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	if len(g.asked) != 1 || g.asked[0] != "b-1" {
		t.Errorf("the policy is the account's home branch: asked %v", g.asked)
	}
}

func TestAccountForWire_RefusedIsByteAbsent(t *testing.T) {
	hidden := &branchGate{allow: map[string]bool{"b-1": false}}
	for name, svc := range map[string]*Service{
		"HIDDEN or refused": NewService(newFakeStore()).WithPositionGate(hidden),
		"unbound gate":      NewService(newFakeStore()),
	} {
		got := wireJSON(t, svc.AccountForWire(context.Background(), someAccount))
		for _, bad := range []string{"credit_limit_cents", "balance_cents", "available_credit_cents", "500000", "123456", "376544"} {
			if strings.Contains(got, bad) {
				t.Errorf("%s: %s must be off the wire: %s", name, bad, got)
			}
		}
		// The operational facts stay: a refused caller still knows who it is and whether it is on hold.
		for _, keep := range []string{`"on_hold":false`, `"account_no":"A-1"`, `"is_active":true`} {
			if !strings.Contains(got, keep) {
				t.Errorf("%s: %s must stay: %s", name, keep, got)
			}
		}
	}
}

func TestSearchForWire_PolicyIsTheRequestsBranchAndByteAbsent(t *testing.T) {
	rows := []SearchResult{{ID: "1", Name: "A", AccountNo: "A-1", CreditLimitCents: 900, OpenBalanceCents: 400}}
	g := &branchGate{allow: map[string]bool{"b-open": true}}
	svc := NewService(newFakeStore()).WithPositionGate(g)

	allowed := wireJSON(t, svc.SearchForWire(context.Background(), rows, "b-open"))
	if !strings.Contains(allowed, `"credit_limit_cents":900`) || !strings.Contains(allowed, `"open_balance_cents":400`) ||
		!strings.Contains(allowed, `"available_credit_cents":500`) {
		t.Errorf("an admitted caller keeps the figures: %s", allowed)
	}
	refused := wireJSON(t, svc.SearchForWire(context.Background(), rows, "b-hidden"))
	for _, bad := range []string{"credit_limit_cents", "open_balance_cents", "available_credit_cents", "900", "400", "500"} {
		if strings.Contains(refused, bad) {
			t.Errorf("%s must be off the wire for a refused branch: %s", bad, refused)
		}
	}
	if !strings.Contains(refused, `"account_no":"A-1"`) {
		t.Errorf("the picker still finds the account: %s", refused)
	}
	if empty := wireJSON(t, svc.SearchForWire(context.Background(), nil, "b-open")); empty != "[]" {
		t.Errorf("an empty search is [] not null: %s", empty)
	}
}

// The handler path end to end over fakes: the same body the clerk's picker reads.
func TestHandler_GetAndSearchApplyThePolicy(t *testing.T) {
	f := &fakeReadStore{fakeStore: newFakeStore(), acct: someAccount, found: true}
	sc := identity.StaffContext{UserID: "u", Scope: identity.ScopeGlobal}
	for _, tc := range []struct {
		name  string
		allow bool
	}{{"manager admitted", true}, {"clerk refused", false}} {
		g := &branchGate{allow: map[string]bool{"b-1": tc.allow, "": tc.allow}}
		h := NewHandler(NewService(f).WithPositionGate(g), nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/customers/"+readID, nil).WithContext(readCtx(sc))
		req.SetPathValue("id", readID)
		rr := httptest.NewRecorder()
		h.get(rr, req, guards.StaffCaller{})
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: get %d %s", tc.name, rr.Code, rr.Body.String())
		}
		if has := strings.Contains(rr.Body.String(), "credit_limit_cents"); has != tc.allow {
			t.Errorf("%s: get carries the limit = %v: %s", tc.name, has, rr.Body.String())
		}
	}
}

func TestAvailableCredit_IsFlooredAtZero(t *testing.T) {
	for _, c := range []struct{ limit, open, want int64 }{{500, 100, 400}, {500, 500, 0}, {500, 900, 0}, {0, 0, 0}} {
		if got := availableCredit(c.limit, c.open); got != c.want {
			t.Errorf("availableCredit(%d, %d) = %d, want %d", c.limit, c.open, got, c.want)
		}
	}
}

// UX-97 round 2: the cash on demand posture is a payment rule, not a figure. It rides the wire whatever
// the visibility policy says, so a refused clerk still learns the account pays before release.
func TestCashOnDemandRule(t *testing.T) {
	for _, c := range []struct {
		terms int
		limit int64
		want  bool
	}{{0, 500_000, true}, {30, 500_000, false}, {0, 0, false}, {30, 0, false}} {
		if got := IsCashOnDemand(c.terms, c.limit); got != c.want {
			t.Errorf("IsCashOnDemand(%d, %d) = %v, want %v", c.terms, c.limit, got, c.want)
		}
	}
}

func TestCashOnDemandRidesTheWireWhateverThePolicy(t *testing.T) {
	cod := Account{CustomerID: readID, BranchID: "b-1", PaymentTermsDays: 0, CreditLimitCents: 500_000}
	rows := []SearchResult{{ID: "1", PaymentTermsDays: 0, CreditLimitCents: 500_000}, {ID: "2", PaymentTermsDays: 30, CreditLimitCents: 500_000}}
	for name, svc := range map[string]*Service{
		"refused": NewService(newFakeStore()).WithPositionGate(&branchGate{allow: map[string]bool{}}),
		"unbound": NewService(newFakeStore()),
		"allowed": NewService(newFakeStore()).WithPositionGate(&branchGate{allow: map[string]bool{"b-1": true}}),
	} {
		if got := wireJSON(t, svc.AccountForWire(context.Background(), cod)); !strings.Contains(got, `"cash_on_demand":true`) {
			t.Errorf("%s: by-id read must carry cash_on_demand true: %s", name, got)
		}
		got := wireJSON(t, svc.SearchForWire(context.Background(), rows, "b-1"))
		if !strings.Contains(got, `"id":"1"`) || strings.Count(got, `"cash_on_demand":true`) != 1 || strings.Count(got, `"cash_on_demand":false`) != 1 {
			t.Errorf("%s: search must say true for the zero day account and false for the terms account: %s", name, got)
		}
	}
	// A refused response still carries no figure.
	refused := wireJSON(t, NewService(newFakeStore()).AccountForWire(context.Background(), cod))
	for _, bad := range []string{"credit_limit_cents", "balance_cents", "available_credit_cents", "500000"} {
		if strings.Contains(refused, bad) {
			t.Errorf("%s must stay off the wire: %s", bad, refused)
		}
	}
}
