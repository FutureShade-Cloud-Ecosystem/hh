//go:build integration

// Integration cover for the assistant's governance tables (migrations 0153/0154/0155) against a
// real migrated Postgres. Run with:
//
//	DATABASE_URL=... go test -tags integration -count=1 ./internal/assist/
//
// WHAT ONLY A DATABASE CAN PROVE, AND IS THEREFORE PROVED HERE RATHER THAN AGAINST THE FAKE:
//
//   - the ledger is APPEND-ONLY in SQL. The fake refuses edits because I wrote it that way; the
//     table refuses them because 0154's trigger does, and that is the claim the client was made.
//     UPDATE, DELETE and TRUNCATE are each attempted by hand here, outside the application's code
//     paths, which is exactly how the property would be broken in real life.
//   - the month window is a real half-open boundary against real timestamptz values, not against
//     Go time arithmetic that happens to agree with itself.
//   - the limit flips at spent >= budget when the numbers come from SUM() over rows rather than
//     from a slice, the boundary that mutation M2 attacks, re-proved end to end.
//   - whole-or-absent survives the round trip: an override REPLACES, and reset-to-role is a DELETE
//     that leaves no fragment, in SQL, through the service's own methods.
//   - the 0155 seed maps shipped role codes to budgets, and the 0153 seed maps every registered
//     command to a policy row, a Go map would only prove that I can type.
//
// FIXTURES RESTORE WHAT THEY TOUCH, with ONE HONEST EXCEPTION, which is the point of the feature.
// The ledger cannot be cleaned up: its rows cannot be deleted, by anyone, including this test. So
// every ledger test writes against a SYNTHETIC USER created for that test, and the rows are left
// behind deliberately. They are attributed to a user whose email ends .invalid, they are scoped to
// a year far from any real month window, and a test that could tidy them up would be a test running
// against a table that is not append-only.

package assist

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, skipping assist integration tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return pool
}

// syntheticUser creates a throwaway ACTIVE user. Its assist_user_limits row cascades away on
// cleanup; its LEDGER rows do not, and cannot, see the file header.
func syntheticUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	ctx := context.Background()
	var id string
	err := pool.QueryRow(ctx, `
		INSERT INTO users (email, display_name, status)
		VALUES ('dib431-assist-fixture-' || gen_random_uuid() || '@example.invalid', 'DIB-431 Fixture', 'ACTIVE')
		RETURNING id::text`).Scan(&id)
	if err != nil {
		t.Fatalf("create fixture user: %v", err)
	}
	t.Cleanup(func() {
		// ON DELETE RESTRICT on the ledger means a user with rows cannot be removed, which is the
		// correct behaviour and is asserted below. Clear the override, then try the user and accept
		// the refusal.
		_, _ = pool.Exec(context.Background(), `DELETE FROM assist_user_limits WHERE user_id = $1::uuid`, id)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1::uuid`, id)
	})
	return id
}

// fixtureMonth is a window far from any real one, so fixture rows can never be mistaken for, or
// pollute, a real month's spend report.
func fixtureMonth() (time.Time, time.Time) {
	return MonthWindow(time.Date(2031, 3, 12, 10, 0, 0, 0, time.UTC))
}

// -- the ledger is append-only, in SQL -----------------------------------------------------------

// TestIntegration_LedgerIsAppendOnly, governance 1's load-bearing claim, attacked three ways from
// outside the application entirely.
func TestIntegration_LedgerIsAppendOnly(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	user := syntheticUser(t, pool)
	from, _ := fixtureMonth()

	if err := repo.Append(ctx, Entry{
		UserID: user, RoleCode: "INSIDE_SALES", Provider: "stub", Model: "hos-stub-1",
		TokensIn: 0, TokensOut: 0, Kind: KindChat, At: from.Add(2 * time.Hour),
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// 1. UPDATE, the well-meant "fix" in a later release.
	_, err := pool.Exec(ctx, `UPDATE assist_token_ledger SET tokens_in = 999999 WHERE user_id = $1::uuid`, user)
	if err == nil {
		t.Fatal("UPDATE on assist_token_ledger succeeded, the append-only trigger is not doing its job")
	}
	if !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("UPDATE was refused, but not by the append-only trigger: %v", err)
	}

	// 2. DELETE, the hand-run cleanup during an incident.
	_, err = pool.Exec(ctx, `DELETE FROM assist_token_ledger WHERE user_id = $1::uuid`, user)
	if err == nil {
		t.Fatal("DELETE on assist_token_ledger succeeded, billing history can be quietly erased")
	}
	if !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("DELETE was refused, but not by the append-only trigger: %v", err)
	}

	// 3. TRUNCATE, the one that bypasses row triggers, and therefore needs its own.
	_, err = pool.Exec(ctx, `TRUNCATE assist_token_ledger`)
	if err == nil {
		t.Fatal("TRUNCATE emptied the ledger, the statement-level trigger is missing")
	}
	if !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("TRUNCATE was refused, but not by the append-only trigger: %v", err)
	}

	// The row is still there and still says what it said.
	winFrom, winTo := fixtureMonth()
	spend, err := repo.SpendFor(ctx, user, winFrom, winTo)
	if err != nil {
		t.Fatalf("SpendFor: %v", err)
	}
	if spend.Exchanges != 1 || spend.TokensIn != 0 || spend.TokensOut != 0 {
		t.Fatalf("after three failed attacks the ledger reads %+v, want 1 exchange of zeros", spend)
	}
}

// TestIntegration_ARemovedUserCannotTakeTheirSpendHistoryWithThem, ON DELETE RESTRICT on the
// ledger's FK. Deleting a login must not silently delete what it cost.
func TestIntegration_ARemovedUserCannotTakeTheirSpendHistoryWithThem(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	user := syntheticUser(t, pool)
	from, _ := fixtureMonth()

	if err := repo.Append(ctx, Entry{
		UserID: user, RoleCode: "INSIDE_SALES", Provider: "stub", Model: "hos-stub-1", At: from,
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, user); err == nil {
		t.Fatal("a user with ledger rows was deleted, their spend history went with them")
	}
}

// TestIntegration_TheStubsZerosAreAcceptedByTheCheck: the CHECK is >= 0, not > 0, because the
// pilot's default provider costs nothing and its rows must still land (governance 1).
func TestIntegration_TheStubsZerosAreAcceptedByTheCheck(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	user := syntheticUser(t, pool)
	from, _ := fixtureMonth()

	for i := 0; i < 3; i++ {
		if err := repo.Append(ctx, Entry{
			UserID: user, RoleCode: "YARD_TEAM", Provider: "stub", Model: "hos-stub-1", At: from.Add(time.Duration(i) * time.Hour),
		}); err != nil {
			t.Fatalf("a zero-token exchange was refused: %v", err)
		}
	}
	winFrom, winTo := fixtureMonth()
	spend, err := repo.SpendFor(ctx, user, winFrom, winTo)
	if err != nil {
		t.Fatalf("SpendFor: %v", err)
	}
	// EXCHANGES is the measurement the pilot needs, "did anyone use this after the demo?", and it
	// must not read zero just because the tokens do.
	if spend.Exchanges != 3 {
		t.Fatalf("Exchanges = %d, want 3, a zero-token exchange is still an exchange", spend.Exchanges)
	}
	if spend.Total() != 0 {
		t.Fatalf("Total() = %d, want 0", spend.Total())
	}
}

// TestIntegration_MonthWindowIsHalfOpenAgainstRealTimestamps.
func TestIntegration_MonthWindowIsHalfOpenAgainstRealTimestamps(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	user := syntheticUser(t, pool)
	from, to := fixtureMonth()

	rows := []struct {
		name string
		at   time.Time
		in   int
	}{
		{"the instant before the window", from.Add(-time.Millisecond), 1000},
		{"the first instant of the window", from, 7},
		{"the last instant of the window", to.Add(-time.Millisecond), 11},
		{"the first instant of the next window", to, 2000},
	}
	for _, r := range rows {
		if err := repo.Append(ctx, Entry{
			UserID: user, RoleCode: "INSIDE_SALES", Provider: "stub", Model: "hos-stub-1",
			TokensIn: r.in, At: r.at,
		}); err != nil {
			t.Fatalf("Append(%s): %v", r.name, err)
		}
	}

	spend, err := repo.SpendFor(ctx, user, from, to)
	if err != nil {
		t.Fatalf("SpendFor: %v", err)
	}
	if spend.Exchanges != 2 || spend.TokensIn != 18 {
		t.Fatalf("window holds %+v, want exactly the two inside rows (18 tokens over 2 exchanges)", spend)
	}

	// And the same rows, seen by the whole-population query the admin surface uses.
	all, err := repo.SpendAll(ctx, from, to)
	if err != nil {
		t.Fatalf("SpendAll: %v", err)
	}
	if got := all[user]; got.Exchanges != 2 || got.TokensIn != 18 {
		t.Fatalf("SpendAll disagrees with SpendFor: %+v", got)
	}
}

// -- the limit flips at the boundary, end to end ------------------------------------------------

// TestIntegration_LimitFlipsAtTheBoundary, M2's boundary, re-proved with the spend coming from a
// SUM over real rows and the budget from a real table.
func TestIntegration_LimitFlipsAtTheBoundary(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	user := syntheticUser(t, pool)
	from, to := fixtureMonth()

	const budgetTokens = 1000
	if err := repo.SetUserBudget(ctx, user, budgetTokens, user); err != nil {
		t.Fatalf("SetUserBudget: %v", err)
	}
	budget, err := BudgetFor(ctx, repo, Caller{UserID: user, RoleCode: "INSIDE_SALES", Tier: 3})
	if err != nil {
		t.Fatalf("BudgetFor: %v", err)
	}
	if budget.Tokens != budgetTokens || budget.Source != SourceUser {
		t.Fatalf("budget = %d/%s, want %d/user", budget.Tokens, budget.Source, budgetTokens)
	}

	// One token short: still inside.
	if err := repo.Append(ctx, Entry{
		UserID: user, RoleCode: "INSIDE_SALES", Provider: "anthropic", Model: "test-model",
		TokensIn: 600, TokensOut: 399, At: from.Add(time.Hour),
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	spend, err := repo.SpendFor(ctx, user, from, to)
	if err != nil {
		t.Fatalf("SpendFor: %v", err)
	}
	if spend.Total() != 999 {
		t.Fatalf("Total() = %d, want 999", spend.Total())
	}
	if OverBudget(spend, budget) {
		t.Fatal("999 of 1000 was refused, a user one token short of their limit must still be served")
	}

	// The token that crosses it. Exactly at the limit is OVER.
	if err := repo.Append(ctx, Entry{
		UserID: user, RoleCode: "INSIDE_SALES", Provider: "anthropic", Model: "test-model",
		TokensIn: 1, At: from.Add(2 * time.Hour),
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	spend, err = repo.SpendFor(ctx, user, from, to)
	if err != nil {
		t.Fatalf("SpendFor: %v", err)
	}
	if spend.Total() != 1000 {
		t.Fatalf("Total() = %d, want 1000", spend.Total())
	}
	if !OverBudget(spend, budget) {
		t.Fatal("1000 of 1000 was allowed, the boundary is spent >= budget (mutation M2)")
	}
}

// TestIntegration_AZeroBudgetRefusesWithNothingSpent: 0 is legal and means "no AI for this person".
func TestIntegration_AZeroBudgetRefusesWithNothingSpent(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	user := syntheticUser(t, pool)

	if err := repo.SetUserBudget(ctx, user, 0, user); err != nil {
		t.Fatalf("a zero budget was refused by the store: %v", err)
	}
	budget, err := BudgetFor(ctx, repo, Caller{UserID: user, RoleCode: "INSIDE_SALES", Tier: 3})
	if err != nil {
		t.Fatalf("BudgetFor: %v", err)
	}
	if budget.Tokens != 0 || budget.Source != SourceUser {
		t.Fatalf("budget = %d/%s, want 0/user, a zero override must not read as absent", budget.Tokens, budget.Source)
	}
	if !OverBudget(Spend{}, budget) {
		t.Fatal("a zero budget allowed a first exchange")
	}
}

// TestIntegration_ANegativeBudgetIsRefusedByTheCheckConstraint, belt to the service's braces.
func TestIntegration_ANegativeBudgetIsRefusedByTheCheckConstraint(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	user := syntheticUser(t, pool)

	_, err := pool.Exec(ctx,
		`INSERT INTO assist_user_limits (user_id, monthly_token_budget) VALUES ($1::uuid, -1)`, user)
	if err == nil {
		t.Fatal("a negative budget was stored, the CHECK constraint is missing")
	}
}

// -- whole-or-absent, in SQL --------------------------------------------------------------------

// TestIntegration_OverrideIsWholeOrAbsent, governance 2's rule, through the service's own methods.
func TestIntegration_OverrideIsWholeOrAbsent(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	user := syntheticUser(t, pool)

	svc := NewService(NewRegistry(Deps{}, repo), NewStubProvider(), repo, repo, repo, nil)
	caller := Caller{UserID: user, RoleCode: "SALES_MANAGER", Tier: 2}

	// No override: the role default from the 0155 seed.
	budget, err := svc.Budget(ctx, caller)
	if err != nil {
		t.Fatalf("Budget: %v", err)
	}
	if budget.Source != SourceRole {
		t.Fatalf("source = %s, want role, the 0155 seed should cover SALES_MANAGER", budget.Source)
	}
	roleDefault := budget.Tokens
	if roleDefault <= 0 {
		t.Fatalf("SALES_MANAGER's seeded budget is %d", roleDefault)
	}

	// An override REPLACES it, whole.
	if err := svc.SetUserBudget(ctx, user, 12345, user); err != nil {
		t.Fatalf("SetUserBudget: %v", err)
	}
	budget, err = svc.Budget(ctx, caller)
	if err != nil {
		t.Fatalf("Budget: %v", err)
	}
	if budget.Tokens != 12345 || budget.Source != SourceUser {
		t.Fatalf("budget = %d/%s, want 12345/user", budget.Tokens, budget.Source)
	}

	// Writing it again REPLACES rather than accumulating, the failure mode a naive upsert has.
	if err := svc.SetUserBudget(ctx, user, 500, user); err != nil {
		t.Fatalf("SetUserBudget (second): %v", err)
	}
	budget, _ = svc.Budget(ctx, caller)
	if budget.Tokens != 500 {
		t.Fatalf("budget = %d after rewrite, want 500", budget.Tokens)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM assist_user_limits WHERE user_id = $1::uuid`, user).Scan(&n); err != nil {
		t.Fatalf("count overrides: %v", err)
	}
	if n != 1 {
		t.Fatalf("%d override rows for one person, the override must be whole, not a history", n)
	}

	// Reset-to-role is a DELETE that leaves NO fragment behind.
	if err := svc.ResetUserBudget(ctx, user); err != nil {
		t.Fatalf("ResetUserBudget: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM assist_user_limits WHERE user_id = $1::uuid`, user).Scan(&n); err != nil {
		t.Fatalf("count overrides after reset: %v", err)
	}
	if n != 0 {
		t.Fatalf("%d override rows survived reset-to-role, it must be a DELETE, not a blanking", n)
	}
	budget, err = svc.Budget(ctx, caller)
	if err != nil {
		t.Fatalf("Budget after reset: %v", err)
	}
	if budget.Tokens != roleDefault || budget.Source != SourceRole {
		t.Fatalf("after reset budget = %d/%s, want the byte-identical role default %d/role",
			budget.Tokens, budget.Source, roleDefault)
	}

	// Reset is IDEMPOTENT: resetting a person who has no override succeeds.
	if err := svc.ResetUserBudget(ctx, user); err != nil {
		t.Fatalf("a second reset must succeed, got %v", err)
	}
}

// TestIntegration_AnOverrideIsAPreferenceAndCascadesAway, unlike the ledger, which is a record.
func TestIntegration_AnOverrideIsAPreferenceAndCascadesAway(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()

	var user string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, display_name, status)
		VALUES ('dib431-cascade-' || gen_random_uuid() || '@example.invalid', 'DIB-431 Cascade', 'ACTIVE')
		RETURNING id::text`).Scan(&user); err != nil {
		t.Fatalf("create fixture user: %v", err)
	}
	if err := repo.SetUserBudget(ctx, user, 4242, user); err != nil {
		t.Fatalf("SetUserBudget: %v", err)
	}
	// This user has NO ledger rows, so the delete is allowed and the override must go with it.
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, user); err != nil {
		t.Fatalf("delete user with no ledger rows: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM assist_user_limits WHERE user_id = $1::uuid`, user).Scan(&n); err != nil {
		t.Fatalf("count overrides: %v", err)
	}
	if n != 0 {
		t.Fatalf("%d override rows survived the person, an override is a preference, not a record", n)
	}
}

// -- the seeds ----------------------------------------------------------------------------------

// TestIntegration_EveryRegisteredCommandHasAPolicyRow, 0153's seed against the code's own list. A
// command with no row still runs (at its code floor), but an admin cannot see it to switch it off,
// which makes the config surface quietly incomplete.
func TestIntegration_EveryRegisteredCommandHasAPolicyRow(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()

	policy, err := repo.CommandPolicy(ctx)
	if err != nil {
		t.Fatalf("CommandPolicy: %v", err)
	}
	reg := NewRegistry(Deps{}, repo)
	for _, code := range reg.Codes() {
		p, ok := policy[code]
		if !ok {
			t.Errorf("command %q has no row in assist_command_policy, an admin cannot switch it off", code)
			continue
		}
		if !p.Enabled {
			t.Errorf("command %q ships switched OFF", code)
		}
		cmd, _ := reg.Lookup(code)
		// The seeded tier must not WIDEN the code floor. effectiveTier would clamp it anyway; this
		// asserts the migration and the code agree in the first place, so the clamp is a safety net
		// rather than a load-bearing correction.
		if p.MaxTier > cmd.MaxTier {
			t.Errorf("command %q is seeded at tier %d but its code floor is %d, the migration is trying to widen access",
				code, p.MaxTier, cmd.MaxTier)
		}
	}
}

// TestIntegration_EverySeededRoleLimitNamesARealRole: the FK guarantees it, but a role with no
// budget row is the interesting direction, it resolves to the small code default, and this test
// says out loud which roles are in that position so the number is a decision and not a surprise.
func TestIntegration_EverySeededRoleLimitNamesARealRole(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()

	budgets, err := repo.RoleBudgets(ctx)
	if err != nil {
		t.Fatalf("RoleBudgets: %v", err)
	}
	if len(budgets) == 0 {
		t.Fatal("no seeded role budgets at all, migration 0155's seed did not run")
	}

	rows, err := pool.Query(ctx, `SELECT code::text FROM roles ORDER BY code`)
	if err != nil {
		t.Fatalf("read roles: %v", err)
	}
	defer rows.Close()
	var uncovered []string
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			t.Fatalf("scan role: %v", err)
		}
		if _, ok := budgets[code]; !ok {
			uncovered = append(uncovered, code)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate roles: %v", err)
	}
	// Not a failure: an uncovered role resolves to DefaultBudget, which is deliberately the small
	// number. Reported so the list is visible when a later migration adds a role.
	if len(uncovered) > 0 {
		t.Logf("roles with no seeded budget (each resolves to the %d-token code default): %s",
			DefaultBudget, strings.Join(uncovered, ", "))
	}
}

// TestIntegration_SettingsRoundTripAndRejectUnknownKeys.
func TestIntegration_SettingsRoundTripAndRejectUnknownKeys(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	user := syntheticUser(t, pool)

	before, err := repo.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	// Restore whatever the deployment had, so this file stays order-independent.
	t.Cleanup(func() {
		for _, k := range []string{KeyProvider, KeyModel, KeyChatEnabled, KeySpendVisibleToNonAdmin} {
			if v, ok := before[k]; ok {
				_ = repo.SetSetting(context.Background(), k, v, user)
			}
		}
	})

	svc := NewService(NewRegistry(Deps{}, repo), NewStubProvider(), repo, repo, repo, nil)
	if err := svc.SetSetting(ctx, KeySpendVisibleToNonAdmin, "true", user); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if !svc.Settings(ctx).SpendVisibleToNonAdmin {
		t.Fatal("the visibility toggle did not round-trip through the database")
	}
	if svc.MySpendVisible(ctx, Caller{UserID: user, RoleCode: "INSIDE_SALES", Tier: 3}) != true {
		t.Fatal("a non-admin cannot see spend with the toggle on")
	}
	if err := svc.SetSetting(ctx, KeySpendVisibleToNonAdmin, "false", user); err != nil {
		t.Fatalf("SetSetting(false): %v", err)
	}
	if svc.MySpendVisible(ctx, Caller{UserID: user, RoleCode: "INSIDE_SALES", Tier: 3}) {
		t.Fatal("a non-admin still sees spend with the toggle off")
	}
	// An admin always sees it.
	if !svc.MySpendVisible(ctx, Caller{UserID: user, RoleCode: "SUPER_ADMIN", Tier: 1, IsAdmin: true}) {
		t.Fatal("an admin must always see spend")
	}

	// An unknown key is refused, and NOT written.
	if err := svc.SetSetting(ctx, "chat_enable", "true", user); err == nil {
		t.Fatal("a misspelled setting key was accepted")
	}
	after, err := repo.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if _, ok := after["chat_enable"]; ok {
		t.Fatal("the misspelled key reached the table")
	}
}

// -- the admin spend report, over real rows -----------------------------------------------------

// TestIntegration_SpendReportIncludesPeopleWhoNeverUsedIt, the client's actual question.
func TestIntegration_SpendReportIncludesPeopleWhoNeverUsedIt(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	user := syntheticUser(t, pool)
	from, _ := fixtureMonth()

	if err := repo.Append(ctx, Entry{
		UserID: user, RoleCode: "INSIDE_SALES", Provider: "stub", Model: "hos-stub-1",
		TokensIn: 40, TokensOut: 12, At: from.Add(12 * time.Hour), // midday UTC, inside the house month
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	svc := NewService(NewRegistry(Deps{}, repo), NewStubProvider(), repo, repo, repo, nil).
		WithStaff(repo).
		WithClock(func() time.Time { return from.Add(48 * time.Hour) })

	rows, err := svc.SpendReport(ctx)
	if err != nil {
		t.Fatalf("SpendReport: %v", err)
	}
	if len(rows) < 2 {
		t.Fatalf("the report has %d rows, it should list every ACTIVE staff member", len(rows))
	}

	var found, zeroRows int
	for _, r := range rows {
		if r.UserID == user {
			found++
			if r.Exchanges != 1 || r.TokensIn != 40 || r.TokensOut != 12 {
				t.Fatalf("the fixture's row reads %+v, want 1 exchange of 40/12", r.Spend)
			}
			if r.Budget.Tokens <= 0 {
				t.Fatalf("the fixture has no resolved budget: %+v", r.Budget)
			}
		}
		if r.Exchanges == 0 {
			zeroRows++
		}
		if r.DisplayName == "" {
			t.Errorf("user %s has a blank name on the spend surface", r.UserID)
		}
	}
	if found != 1 {
		t.Fatalf("the fixture appears %d times in the report, want once", found)
	}
	if zeroRows == 0 {
		t.Fatal("no zero rows at all, people who never opened the assistant must still be listed")
	}
	// Biggest spender first, so the admin's answer is the first row.
	for i := 1; i < len(rows); i++ {
		if rows[i-1].Total() < rows[i].Total() {
			t.Fatalf("the report is not sorted by spend: row %d (%d) < row %d (%d)",
				i-1, rows[i-1].Total(), i, rows[i].Total())
		}
	}
}

// TestIntegration_TheLedgerKeepsTheRoleTheExchangeHappenedUnder: a promotion in September must not
// retroactively re-bucket August's spend.
func TestIntegration_TheLedgerKeepsTheRoleTheExchangeHappenedUnder(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	user := syntheticUser(t, pool)
	from, _ := fixtureMonth()

	if err := repo.Append(ctx, Entry{
		UserID: user, RoleCode: "INSIDE_SALES", Provider: "stub", Model: "hos-stub-1", At: from,
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	var role string
	if err := pool.QueryRow(ctx,
		`SELECT role_code::text FROM assist_token_ledger WHERE user_id = $1::uuid LIMIT 1`, user).Scan(&role); err != nil {
		t.Fatalf("read role_code: %v", err)
	}
	if role != "INSIDE_SALES" {
		t.Fatalf("role_code on the row = %q, want the role held at the time of the exchange", role)
	}
	// And it cannot be edited to say otherwise.
	if _, err := pool.Exec(ctx,
		`UPDATE assist_token_ledger SET role_code = 'SUPER_ADMIN' WHERE user_id = $1::uuid`, user); err == nil {
		t.Fatal("the historical role on a ledger row was rewritten")
	}
}

// TestIntegration_AStoredPolicyRowStillBeatsItsDefault is the guard on the DIB-478 change to
// CommandPolicy: completing the map from BuiltinCommands must not make the config TABLE ineffective.
//
// If the defaults were applied AFTER the query instead of before it, every command would read as
// enabled-at-its-code-floor for ever and an admin's "switch this off" would do nothing at all -
// while every other test in this file kept passing, because they all assert on the default values.
func TestIntegration_AStoredPolicyRowStillBeatsItsDefault(t *testing.T) {
	pool := testPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()

	const code = "stock.incoming" // a DIB-478 command: no seeded row, so it arrives as a default
	before, err := repo.CommandPolicy(ctx)
	if err != nil {
		t.Fatalf("CommandPolicy: %v", err)
	}
	if p, ok := before[code]; !ok || !p.Enabled {
		t.Fatalf("%s must default to present and enabled, got %+v (ok=%v)", code, p, ok)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM assist_command_policy WHERE command_code = $1`, code)
	})

	// An admin switches it off. The stored row must win.
	if err := repo.SetCommandPolicy(ctx, code, Policy{Enabled: false, MaxTier: 2}, ""); err != nil {
		t.Fatalf("SetCommandPolicy: %v", err)
	}
	after, err := repo.CommandPolicy(ctx)
	if err != nil {
		t.Fatalf("CommandPolicy: %v", err)
	}
	if after[code].Enabled {
		t.Fatalf("an admin disabled %s and the default overwrote it, the config table is decorative", code)
	}
	if after[code].MaxTier != 2 {
		t.Fatalf("the stored tier was lost: %+v", after[code])
	}
}
