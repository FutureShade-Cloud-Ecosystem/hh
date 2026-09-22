package customer

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// fakeStore is an in-memory accountStore so the service tests exercise input
// validation and not-found mapping without pgx. Contacts are keyed by customer id
// and filtered the same way the SQL does (is_primary DESC, then is_active for the
// primary lookup), so the tests pin the service's pass-through behavior.
type fakeStore struct {
	accounts map[string]Account
	contacts map[string][]Contact
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		accounts: map[string]Account{},
		contacts: map[string][]Contact{},
	}
}

func (f *fakeStore) ResolveAccount(_ context.Context, customerID string) (Account, error) {
	a, ok := f.accounts[customerID]
	if !ok {
		return Account{}, ErrNotFound
	}
	return a, nil
}

func (f *fakeStore) ListContacts(_ context.Context, customerID string) ([]Contact, error) {
	// Primary first (stable order), mirroring ORDER BY is_primary DESC, name.
	var primary, rest []Contact
	for _, c := range f.contacts[customerID] {
		if c.IsPrimary {
			primary = append(primary, c)
		} else {
			rest = append(rest, c)
		}
	}
	return append(primary, rest...), nil
}

func (f *fakeStore) GetPrimaryContact(_ context.Context, customerID string) (Contact, error) {
	for _, c := range f.contacts[customerID] {
		if c.IsPrimary && c.IsActive {
			return c, nil
		}
	}
	return Contact{}, ErrNotFound
}

// SearchAccounts mirrors the SQL: case-insensitive contains on name OR account_no,
// active only, walk-in excluded, ordered by name, respecting limit.
func (f *fakeStore) SearchAccounts(_ context.Context, q string, limit int) ([]SearchResult, error) {
	ql := strings.ToLower(q)
	var out []SearchResult
	for _, a := range f.accounts {
		if !a.IsActive {
			continue
		}
		if a.AccountNo == "WALK-IN" || strings.HasPrefix(strings.ToLower(a.Name), "walk-in") {
			continue
		}
		if !strings.Contains(strings.ToLower(a.Name), ql) && !strings.Contains(strings.ToLower(a.AccountNo), ql) {
			continue
		}
		out = append(out, SearchResult{
			ID:               a.CustomerID,
			Name:             a.Name,
			AccountNo:        a.AccountNo,
			CustomerType:     a.CustomerType,
			PaymentTermsDays: a.PaymentTermsDays,
			CreditLimitCents: a.CreditLimitCents,
			OpenBalanceCents: a.BalanceCents,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func seedStore() *fakeStore {
	f := newFakeStore()
	f.accounts["acct-1"] = Account{
		CustomerID:       "acct-1",
		AccountNo:        "C-1001",
		Name:             "Northland Hardscapes",
		CustomerType:     "CONTRACTOR",
		BranchID:         "branch-9",
		PaymentTermsDays: 30,
		CreditLimitCents: 5_000_00,
		OnHold:           false,
		TaxExempt:        false,
		IsActive:         true,
	}
	// acct-2 has an unset home branch (BranchID == "") and no primary contact.
	f.accounts["acct-2"] = Account{
		CustomerID:   "acct-2",
		AccountNo:    "C-1002",
		Name:         "Retail Walk-In",
		CustomerType: "RETAIL",
		BranchID:     "",
		IsActive:     true,
	}
	f.contacts["acct-1"] = []Contact{
		{ID: "ct-1", CustomerID: "acct-1", Name: "Jane Owner", Email: "jane@north.example", Role: "Owner", IsPrimary: true, IsActive: true},
		{ID: "ct-2", CustomerID: "acct-1", Name: "Bob Buyer", Phone: "555-0100", Role: "Purchasing", IsPrimary: false, IsActive: true},
	}
	// acct-2's only contact is primary but INACTIVE -> GetPrimaryContact must 404.
	f.contacts["acct-2"] = []Contact{
		{ID: "ct-3", CustomerID: "acct-2", Name: "Stale Primary", IsPrimary: true, IsActive: false},
	}
	// acct-3 exercises account_no matching + an open balance.
	f.accounts["acct-3"] = Account{
		CustomerID:       "acct-3",
		AccountNo:        "C-2050",
		Name:             "Aberdeen Builders",
		CustomerType:     "BUILDER",
		PaymentTermsDays: 45,
		CreditLimitCents: 10_000_00,
		BalanceCents:     2_500_00,
		IsActive:         true,
	}
	// The WALK-IN house account must NEVER surface in search results.
	f.accounts["walkin"] = Account{
		CustomerID:   "walkin",
		AccountNo:    "WALK-IN",
		Name:         "Walk-In / Cash",
		CustomerType: "RETAIL",
		IsActive:     true,
	}
	return f
}

// globalCtx stamps a GLOBAL staff caller, the pre-fence posture, so the tests below that predate
// U-park E4 keep asserting exactly what they always asserted (an unfenced search). The fence itself
// is covered by TestSearchAccounts_BranchFence_* below and by the integration tests.
func globalCtx() context.Context {
	return identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: "u-global", RoleCode: identity.RoleSuperAdmin, Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: "ACTIVE",
	})
}

// scopedCtx stamps a branch-scoped staff caller granted exactly the named branches.
func scopedCtx(branches ...string) context.Context {
	granted := map[string]bool{}
	for _, b := range branches {
		granted[b] = true
	}
	return identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: "u-scoped", RoleCode: "COUNTER_SALES", Tier: identity.TierFrontline,
		Scope: identity.ScopeBranch, Status: "ACTIVE", Branches: granted,
	})
}

func TestSearchAccounts_ByName(t *testing.T) {
	svc := NewService(seedStore())
	got, err := svc.SearchAccounts(globalCtx(), "northland", 0, "")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 1 || got[0].AccountNo != "C-1001" {
		t.Fatalf("name search: got %+v, want the Northland account", got)
	}
}

func TestSearchAccounts_ByAccountNo(t *testing.T) {
	svc := NewService(seedStore())
	got, err := svc.SearchAccounts(globalCtx(), "C-2050", 0, "")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 1 || got[0].ID != "acct-3" {
		t.Fatalf("account_no search: got %+v, want acct-3", got)
	}
	if got[0].OpenBalanceCents != 2_500_00 {
		t.Fatalf("open_balance_cents = %d, want 250000", got[0].OpenBalanceCents)
	}
}

func TestSearchAccounts_ExcludesWalkIn(t *testing.T) {
	svc := NewService(seedStore())
	// "walk" would match the WALK-IN house account by both name and account_no, it must
	// be filtered out so the picker never offers the no-account default as a chargeable one.
	got, err := svc.SearchAccounts(globalCtx(), "walk", 0, "")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, r := range got {
		if r.AccountNo == "WALK-IN" || strings.HasPrefix(strings.ToLower(r.Name), "walk-in") {
			t.Fatalf("walk-in house account leaked into results: %+v", r)
		}
	}
}

func TestSearchAccounts_RespectsLimit(t *testing.T) {
	svc := NewService(seedStore())
	// "" would match none (empty is a 400); use a term that matches multiple accounts.
	got, err := svc.SearchAccounts(globalCtx(), "e", 1, "")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("limit=1: got %d results, want 1", len(got))
	}
}

func TestSearchAccounts_EmptyTerm(t *testing.T) {
	svc := NewService(seedStore())
	_, err := svc.SearchAccounts(globalCtx(), "", 0, "")
	var bad httpx.BadRequestError
	if !errors.As(err, &bad) {
		t.Fatalf("empty term: err = %v, want BadRequestError", err)
	}
}

func TestResolveAccount_Found(t *testing.T) {
	svc := NewService(seedStore())
	got, err := svc.ResolveAccount(context.Background(), "acct-1")
	if err != nil {
		t.Fatalf("resolve acct-1: %v", err)
	}
	if got.AccountNo != "C-1001" || got.CustomerType != "CONTRACTOR" || got.BranchID != "branch-9" {
		t.Fatalf("unexpected account: %+v", got)
	}
	if got.CreditLimitCents != 5_000_00 {
		t.Fatalf("credit limit = %d, want 500000", got.CreditLimitCents)
	}
	// The resolver must never compose the AR balance, that is the BFF's job.
	if got.BalanceCents != 0 {
		t.Fatalf("balance_cents = %d, want 0 (composed by BFF)", got.BalanceCents)
	}
}

func TestResolveAccount_UnsetBranch(t *testing.T) {
	svc := NewService(seedStore())
	got, err := svc.ResolveAccount(context.Background(), "acct-2")
	if err != nil {
		t.Fatalf("resolve acct-2: %v", err)
	}
	if got.BranchID != "" {
		t.Fatalf("branch_id = %q, want empty for unset home branch", got.BranchID)
	}
}

func TestResolveAccount_NotFound(t *testing.T) {
	svc := NewService(seedStore())
	_, err := svc.ResolveAccount(context.Background(), "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing account: err = %v, want ErrNotFound", err)
	}
}

func TestResolveAccount_EmptyID(t *testing.T) {
	svc := NewService(seedStore())
	_, err := svc.ResolveAccount(context.Background(), "")
	if err == nil {
		t.Fatal("empty id should be rejected")
	}
	var bad httpx.BadRequestError
	if !errors.As(err, &bad) {
		t.Fatalf("empty id: err = %v, want badRequestError", err)
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatal("empty id must be a 400, not a 404")
	}
}

func TestListContacts_PrimaryFirst(t *testing.T) {
	svc := NewService(seedStore())
	got, err := svc.ListContacts(context.Background(), "acct-1")
	if err != nil {
		t.Fatalf("list contacts: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d contacts, want 2", len(got))
	}
	if !got[0].IsPrimary || got[0].ID != "ct-1" {
		t.Fatalf("primary contact must sort first, got %+v", got)
	}
}

func TestListContacts_EmptyID(t *testing.T) {
	svc := NewService(seedStore())
	_, err := svc.ListContacts(context.Background(), "")
	if err == nil {
		t.Fatal("empty id should be rejected")
	}
	var bad httpx.BadRequestError
	if !errors.As(err, &bad) {
		t.Fatalf("empty id: err = %v, want badRequestError", err)
	}
}

func TestGetPrimaryContact_Found(t *testing.T) {
	svc := NewService(seedStore())
	got, err := svc.GetPrimaryContact(context.Background(), "acct-1")
	if err != nil {
		t.Fatalf("get primary: %v", err)
	}
	if got.ID != "ct-1" || !got.IsPrimary || !got.IsActive {
		t.Fatalf("unexpected primary contact: %+v", got)
	}
}

func TestGetPrimaryContact_Missing(t *testing.T) {
	svc := NewService(seedStore())
	// acct-2's only primary contact is inactive -> 404.
	_, err := svc.GetPrimaryContact(context.Background(), "acct-2")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("inactive primary: err = %v, want ErrNotFound", err)
	}
}

func TestGetPrimaryContact_EmptyID(t *testing.T) {
	svc := NewService(seedStore())
	_, err := svc.GetPrimaryContact(context.Background(), "")
	if err == nil {
		t.Fatal("empty id should be rejected")
	}
	var bad httpx.BadRequestError
	if !errors.As(err, &bad) {
		t.Fatalf("empty id: err = %v, want badRequestError", err)
	}
}
