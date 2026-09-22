package assist

// limits_test.go, the budget resolver, the boundary, and the admin spend report.
//
// TestBudget_Boundary IS MUTATION TARGET M2. limits.go documents the enforcement predicate as
// `spent >= budget`; flipping it to `>` lets a user who is exactly at their limit start one more
// exchange. That is precisely the kind of off-by-one no reviewer catches by reading, so it is pinned
// by a test that names the three interesting points, under, exactly at, over, rather than by a
// test that only checks the easy ends.

import (
	"context"
	"testing"
	"time"
)

func ptr(v int) *int { return &v }

// TestBudget_WholeOrAbsent pins the resolution rule: an override REPLACES, it never merges.
func TestBudget_WholeOrAbsent(t *testing.T) {
	cases := []struct {
		name       string
		override   *int
		roleDflt   *int
		wantTokens int
		wantSource string
	}{
		{"an override wins outright", ptr(500000), ptr(300000), 500000, SourceUser},
		{"no override falls to the role", nil, ptr(300000), 300000, SourceRole},
		{"neither falls to the code default", nil, nil, DefaultBudget, SourceDefault},
		// A zero override is a REAL decision ("no AI for this person"), not an absent one. If zero
		// were treated as absent, switching one person off would silently give them the role's whole
		// allowance instead, the exact opposite of what the admin clicked.
		{"a zero override switches the person off", ptr(0), ptr(300000), 0, SourceUser},
		{"a zero role default switches the role off", nil, ptr(0), 0, SourceRole},
		// Equal numbers must still report their true source: two people can legitimately have an
		// override equal to their role's number, and inferring the source by comparison would call
		// that "inherited" and then silently move it when the role default changed.
		{"an override equal to the role default is still an override", ptr(300000), ptr(300000), 300000, SourceUser},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Resolve(tc.override, tc.roleDflt)
			if got.Tokens != tc.wantTokens || got.Source != tc.wantSource {
				t.Fatalf("Resolve() = %d/%s, want %d/%s", got.Tokens, got.Source, tc.wantTokens, tc.wantSource)
			}
		})
	}
}

// TestBudget_Boundary, MUTATION TARGET M2. The predicate is `spent >= budget`.
func TestBudget_Boundary(t *testing.T) {
	budget := Budget{Tokens: 1000, Source: SourceRole}

	cases := []struct {
		name  string
		spend Spend
		over  bool
	}{
		{"one token short is still inside", Spend{TokensIn: 600, TokensOut: 399}, false},
		// THE POINT OF THE TEST. Exactly at the limit is OVER: a budget of 1000 means you may spend
		// up to 1000, and once you have, you are done. Changing `>=` to `>` flips only this case,
		// and this case is the reason the test exists.
		{"exactly at the limit is over", Spend{TokensIn: 600, TokensOut: 400}, true},
		{"past the limit is over", Spend{TokensIn: 600, TokensOut: 401}, true},
		{"nothing spent is inside", Spend{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := OverBudget(tc.spend, budget); got != tc.over {
				t.Fatalf("OverBudget(total=%d, budget=%d) = %v, want %v",
					tc.spend.Total(), budget.Tokens, got, tc.over)
			}
		})
	}
}

// TestBudget_ZeroBudgetRefusesImmediately: a budget of zero must refuse on the FIRST question, with
// nothing spent. Under `>` it would not, 0 > 0 is false, so a role switched off would get one
// free exchange, which is the bug M2 exists to prevent, in its most visible form.
func TestBudget_ZeroBudgetRefusesImmediately(t *testing.T) {
	if !OverBudget(Spend{}, Budget{Tokens: 0, Source: SourceUser}) {
		t.Fatal("a zero budget must refuse before the first exchange, not after it")
	}
}

// TestSpend_TotalCountsBothDirections: input tokens are billed, so they are counted. A budget that
// only counted output would let a user paste a novel in for free.
func TestSpend_TotalCountsBothDirections(t *testing.T) {
	if got := (Spend{TokensIn: 900, TokensOut: 100}).Total(); got != 1000 {
		t.Fatalf("Total() = %d, want 1000, input tokens are billed and must be counted", got)
	}
}

// TestMonthWindow_IsAHalfOpenCalendarMonth: an exchange at the last millisecond of the month belongs
// to that month and to exactly one month.
func TestMonthWindow_IsAHalfOpenCalendarMonth(t *testing.T) {
	from, to := MonthWindow(time.Date(2026, 8, 11, 14, 3, 0, 0, time.UTC))
	if !from.Equal(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("window starts at %s, want 2026-08-01", from)
	}
	if !to.Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("window ends at %s, want 2026-09-01", to)
	}
	last := time.Date(2026, 8, 31, 23, 59, 59, int(time.Millisecond*999), time.UTC)
	if last.Before(from) || !last.Before(to) {
		t.Fatal("the last millisecond of August must fall inside August's window")
	}
	if first := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC); first.Before(to) {
		t.Fatal("the first instant of September must fall OUTSIDE August's window")
	}
}

// TestBudgetFor_ReadsBothStoresAndResolvesOnce proves the enforcement path uses the same Resolve the
// admin screen uses, the screen must never promise headroom the enforcer would refuse.
func TestBudgetFor_ReadsBothStoresAndResolvesOnce(t *testing.T) {
	lim := newFakeLimits()
	lim.roles["SALES_MANAGER"] = 300000
	lim.users["u-sm"] = 500000

	got, err := BudgetFor(context.Background(), lim, manager())
	if err != nil {
		t.Fatalf("BudgetFor: %v", err)
	}
	if got.Tokens != 500000 || got.Source != SourceUser {
		t.Fatalf("got %d/%s, want 500000/user", got.Tokens, got.Source)
	}

	// Reset-to-role is a DELETE and must leave no fragment behind.
	if err := lim.ClearUserBudget(context.Background(), "u-sm"); err != nil {
		t.Fatalf("ClearUserBudget: %v", err)
	}
	got, err = BudgetFor(context.Background(), lim, manager())
	if err != nil {
		t.Fatalf("BudgetFor after reset: %v", err)
	}
	if got.Tokens != 300000 || got.Source != SourceRole {
		t.Fatalf("after reset got %d/%s, want 300000/role", got.Tokens, got.Source)
	}
}

// TestBudgetFor_NoStoreIsTheCodeDefault: a deployment before migration 0155 still answers.
func TestBudgetFor_NoStoreIsTheCodeDefault(t *testing.T) {
	got, err := BudgetFor(context.Background(), nil, manager())
	if err != nil {
		t.Fatalf("BudgetFor(nil store): %v", err)
	}
	if got.Tokens != DefaultBudget || got.Source != SourceDefault {
		t.Fatalf("got %d/%s, want %d/default", got.Tokens, got.Source, DefaultBudget)
	}
}

// TestUserSpend_RemainingFloorsAtZero: "you are 400 over" is not a number anyone acts on.
func TestUserSpend_RemainingFloorsAtZero(t *testing.T) {
	u := UserSpend{Spend: Spend{TokensIn: 900, TokensOut: 500}, Budget: Budget{Tokens: 1000}}
	if got := u.Remaining(); got != 0 {
		t.Fatalf("Remaining() = %d, want 0 for an over-budget user", got)
	}
	u = UserSpend{Spend: Spend{TokensIn: 300}, Budget: Budget{Tokens: 1000}}
	if got := u.Remaining(); got != 700 {
		t.Fatalf("Remaining() = %d, want 700", got)
	}
}

// -- the admin spend report ---------------------------------------------------------------------

func spendService(t *testing.T, led *fakeLedger, lim *fakeLimits, staff fakeStaff) *Service {
	t.Helper()
	reg := NewRegistry(seededReaders().deps(), nil)
	svc := NewService(reg, &scriptedProvider{}, led, lim, StaticSettings{}, nil).
		WithStaff(staff).
		WithClock(func() time.Time { return time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC) })
	return svc
}

// TestSpendReport_ListsPeopleWhoNeverUsedIt, the client's question is "is anyone using this", and a
// report containing only the people who did cannot answer it.
func TestSpendReport_ListsPeopleWhoNeverUsedIt(t *testing.T) {
	aug := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)
	led := &fakeLedger{entries: []Entry{
		{UserID: "u-sm", RoleCode: "SALES_MANAGER", TokensIn: 400, TokensOut: 100, At: aug},
		{UserID: "u-sm", RoleCode: "SALES_MANAGER", TokensIn: 200, TokensOut: 50, At: aug},
		{UserID: "u-is", RoleCode: "INSIDE_SALES", TokensIn: 10, TokensOut: 5, At: aug},
	}}
	lim := newFakeLimits()
	lim.roles["SALES_MANAGER"] = 300000
	lim.users["u-is"] = 1000

	svc := spendService(t, led, lim, fakeStaff{
		{UserID: "u-sm", DisplayName: "Sam Marsh", Email: "sam@dibbits.test", RoleCode: "SALES_MANAGER"},
		{UserID: "u-is", DisplayName: "Ida Stone", Email: "ida@dibbits.test", RoleCode: "INSIDE_SALES"},
		{UserID: "u-yh", DisplayName: "Yuri Hall", Email: "yuri@dibbits.test", RoleCode: "YARD"},
	})

	rows, err := svc.SpendReport(context.Background())
	if err != nil {
		t.Fatalf("SpendReport: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3, every active person appears, including the ones with no exchanges", len(rows))
	}
	// Biggest spender first, so the admin's answer is the first row.
	if rows[0].UserID != "u-sm" || rows[0].Total() != 750 || rows[0].Exchanges != 2 {
		t.Fatalf("row 0 = %+v, want u-sm with 750 tokens over 2 exchanges", rows[0])
	}
	if rows[0].Budget.Tokens != 300000 || rows[0].Budget.Source != SourceRole {
		t.Fatalf("u-sm budget = %d/%s, want 300000/role", rows[0].Budget.Tokens, rows[0].Budget.Source)
	}
	if rows[1].UserID != "u-is" || rows[1].Budget.Source != SourceUser || rows[1].Budget.Tokens != 1000 {
		t.Fatalf("row 1 = %+v, want u-is on a user override of 1000", rows[1])
	}
	// The person who never opened it: a zero row, on the code default, and it is a FACT not a gap.
	last := rows[2]
	if last.UserID != "u-yh" || last.Exchanges != 0 || last.Total() != 0 {
		t.Fatalf("row 2 = %+v, want u-yh with zeros", last)
	}
	if last.Budget.Source != SourceDefault || last.Budget.Tokens != DefaultBudget {
		t.Fatalf("u-yh budget = %d/%s, want the code default", last.Budget.Tokens, last.Budget.Source)
	}
	if last.DisplayName != "Yuri Hall" {
		t.Fatalf("a zero row still names the person, got %q", last.DisplayName)
	}
}

// TestSpendReport_IgnoresOtherMonths: the current-month window is applied, not "everything ever".
func TestSpendReport_IgnoresOtherMonths(t *testing.T) {
	led := &fakeLedger{entries: []Entry{
		{UserID: "u-sm", TokensIn: 999999, At: time.Date(2026, 7, 31, 23, 59, 0, 0, time.UTC)},
		{UserID: "u-sm", TokensIn: 100, At: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)},
		{UserID: "u-sm", TokensIn: 888888, At: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)},
	}}
	svc := spendService(t, led, newFakeLimits(), fakeStaff{
		{UserID: "u-sm", DisplayName: "Sam Marsh", RoleCode: "SALES_MANAGER"},
	})
	rows, err := svc.SpendReport(context.Background())
	if err != nil {
		t.Fatalf("SpendReport: %v", err)
	}
	if rows[0].Total() != 100 || rows[0].Exchanges != 1 {
		t.Fatalf("got %d tokens over %d exchanges, want 100 over 1, July and September must not count",
			rows[0].Total(), rows[0].Exchanges)
	}
}

// TestLimits_ViewCarriesTheCodeDefault: the editor has to be able to show what "no row" means.
func TestLimits_ViewCarriesTheCodeDefault(t *testing.T) {
	lim := newFakeLimits()
	lim.roles["YARD"] = 20000
	lim.users["u-is"] = 1000

	svc := spendService(t, &fakeLedger{}, lim, fakeStaff{})
	v, err := svc.Limits(context.Background())
	if err != nil {
		t.Fatalf("Limits: %v", err)
	}
	if v.DefaultBudget != DefaultBudget {
		t.Fatalf("DefaultBudget = %d, want %d", v.DefaultBudget, DefaultBudget)
	}
	if v.RoleBudgets["YARD"] != 20000 || v.UserOverrides["u-is"] != 1000 {
		t.Fatalf("limits view lost a row: %+v", v)
	}
}

// TestLimits_NegativeBudgetIsRefusedWithASentence: the CHECK constraint would also stop this, but a
// user deserves words, not a constraint violation.
func TestLimits_NegativeBudgetIsRefusedWithASentence(t *testing.T) {
	svc := spendService(t, &fakeLedger{}, newFakeLimits(), fakeStaff{})
	if err := svc.SetRoleBudget(context.Background(), "YARD", -1, "u-gm"); err == nil {
		t.Fatal("a negative role budget must be refused")
	}
	if err := svc.SetUserBudget(context.Background(), "u-is", -1, "u-gm"); err == nil {
		t.Fatal("a negative user budget must be refused")
	}
	// Zero is legal and means "switched off", it must NOT be refused.
	if err := svc.SetRoleBudget(context.Background(), "YARD", 0, "u-gm"); err != nil {
		t.Fatalf("zero is a legal budget meaning 'off', got %v", err)
	}
}
