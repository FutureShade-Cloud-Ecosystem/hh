//go:build integration

// fw1_01_round4_integration_test.go, PR 614 round 4's legs (S2's round 3
// read): every audited gl action driven through the REAL audit recorder
// (audit.NewService writing audit_log, the way wire_money wires production),
// where every earlier leg used a fake, so the one defect between the fake and
// production was invisible. The open year's audit named "fiscal_year:2028" in
// audit_log.entity_id, a uuid column (0013), so through the real recorder the
// insert failed with "invalid input syntax for type uuid" and the year's
// transaction rolled back: in production the Controller could never open a
// year.
package gl

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/branches"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fw1R4BranchFacts binds the branches module's location reads to gl's BranchFacts seam for these
// legs, the way wire_money's glBranchFacts binds it in production.
type fw1R4BranchFacts struct{ repo *branches.Repository }

func (f fw1R4BranchFacts) Exists(ctx context.Context, id string) (bool, error) {
	return f.repo.Exists(ctx, id)
}

// LiveBranchExists is the live branch read (FW1-08b part B round 2, P3-1): a soft deleted branch
// is still a BRANCH row, but it is never a head office.
func (f fw1R4BranchFacts) LiveBranchExists(ctx context.Context, id string) (bool, error) {
	return f.repo.LiveBranchExists(ctx, id)
}

func (f fw1R4BranchFacts) BranchInOrg(ctx context.Context, orgID, branchID string) (bool, error) {
	return f.repo.BranchInOrg(ctx, orgID, branchID)
}

func (f fw1R4BranchFacts) BranchRowByCodeTx(ctx context.Context, tx pgx.Tx, code string) (string, bool, error) {
	return branches.BranchCodeReader{}.BranchRowByCodeTx(ctx, tx, code)
}

// fw1R4Controller builds a Financial Controller's service at the round's pinned clock inside the
// given house year.
func fw1R4Controller(pool *pgxpool.Pool, user string, house int) *Service {
	return svcAt(pool, identity.StaffContext{UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, time.Date(house, 6, 15, 12, 0, 0, 0, houseday.DefaultZone().Location())).
		WithBranchFacts(fw1R4BranchFacts{repo: branches.NewRepository(pool)})
}

// fw1R4PolicyHeadOffice reads the org default policy row's head office branch: the value the head
// office leg must put back when it ends. The empty string means the policy names none.
func fw1R4PolicyHeadOffice(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`SELECT COALESCE(head_office_branch_id::text, '') FROM gl_policy WHERE branch_id IS NULL LIMIT 1`).Scan(&id); err != nil {
		t.Fatalf("read the policy's head office branch: %v", err)
	}
	return id
}

// fw1R4WritePolicyHeadOffice writes the org default policy row's head office branch. The empty
// string writes NULL, the value a policy that named no head office held.
func fw1R4WritePolicyHeadOffice(ctx context.Context, pool *pgxpool.Pool, branchID string) {
	if branchID == "" {
		_, _ = pool.Exec(ctx, `UPDATE gl_policy SET head_office_branch_id = NULL WHERE branch_id IS NULL`)
		return
	}
	_, _ = pool.Exec(ctx, `UPDATE gl_policy SET head_office_branch_id = $1::uuid WHERE branch_id IS NULL`, branchID)
}

// fw1R4RestoreHeadOffice is the head office leg's cleanup: it puts the policy's head office back
// to the value the leg found.
//
// FLAKE-9: it writes the value the policy held, NULL only when the policy held none. Writing
// NULL whatever it found left a box whose policy named a head office (migration 0445 backfills
// one) with the leg's own write instead of its own, and the FW1-10 leg read the wrong head
// office on a reused box.
func fw1R4RestoreHeadOffice(ctx context.Context, pool *pgxpool.Pool, prior string) {
	fw1R4WritePolicyHeadOffice(ctx, pool, prior)
}

// fw1R4HeadOfficeName names a head office branch the way a reader wants it: the branch code and
// its id, or "none" for the empty string.
func fw1R4HeadOfficeName(t *testing.T, pool *pgxpool.Pool, id string) string {
	t.Helper()
	if id == "" {
		return "none"
	}
	var code string
	if err := pool.QueryRow(context.Background(),
		`SELECT COALESCE(code, '') FROM locations WHERE id = $1::uuid`, id).Scan(&code); err != nil {
		t.Fatalf("read the head office branch %s: %v", id, err)
	}
	if code == "" {
		return id
	}
	return fmt.Sprintf("%s (%s)", code, id)
}

// fw1R4HeadOfficeCandidate answers a live branch the head office set may take, with the caller's
// org scope for it. The candidate is never the branch the policy already names as the head
// office when exclude names one, so the leg's own write is visible.
//
// The candidate must be a LIVE branch of the caller's own org (FW1-08b part B round 2): the org
// half of the set is unconditional and the branch half asks for a live branch, so this fixture
// carries the org scope a request context carries and plants an org on the branch when the seed
// leaves it without one.
func fw1R4HeadOfficeCandidate(t *testing.T, pool *pgxpool.Pool, exclude string) (string, context.Context) {
	t.Helper()
	ctx := context.Background()
	var branchID, orgID string
	if err := pool.QueryRow(ctx,
		`SELECT id::text, COALESCE(org_id::text, '') FROM locations
		  WHERE loc_type = 'BRANCH' AND deleted_at IS NULL AND ($1 = '' OR id::text <> $1)
		  ORDER BY code LIMIT 1`, exclude).Scan(&branchID, &orgID); err != nil {
		t.Skipf("no seeded branch: %v", err)
	}
	if orgID == "" {
		orgID = uuid.NewString()
		if _, err := pool.Exec(ctx, `INSERT INTO org (id, slug, name) VALUES ($1::uuid, $2, 'FW1 01 R4 org')`, orgID, "fw108b01r4-"+orgID[:8]); err != nil {
			t.Fatalf("plant the org: %v", err)
		}
		if _, err := pool.Exec(ctx, `UPDATE locations SET org_id = $1::uuid WHERE id::text = $2`, orgID, branchID); err != nil {
			t.Fatalf("plant the org on the branch: %v", err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, `UPDATE locations SET org_id = NULL WHERE id::text = $1`, branchID)
			_, _ = pool.Exec(ctx, `DELETE FROM org WHERE id = $1::uuid`, orgID)
		})
	}
	return branchID, authz.WithStaffScope(ctx, authz.StaffScope{OrgID: orgID})
}

// TestIntegration_FW1_01R4_OpenYearAuditRowThroughTheRealRecorder is the
// round 4 P1 red: the Controller's open through the real recorder must
// succeed AND leave exactly one GL_FISCAL_YEAR_OPEN row in audit_log, keyed
// to an entity id that is a uuid.
func TestIntegration_FW1_01R4_OpenYearAuditRowThroughTheRealRecorder(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	// The band is the database's own frontier, the way the roll forward leg's dynamic `beyond`
	// takes it: the house year IS the latest year that exists, the roll forward's target and the
	// cap are the two years after it.
	house := fw1NextYear(t, pool) - 1
	target, beyond := house+1, house+2
	fw1ScrubYear(t, pool, target, beyond)
	t.Cleanup(func() { fw1ScrubYear(t, pool, target, beyond) })
	// A far future band above the leg's own, the shape finstmt's suite leaves on the shared CI
	// database: the open must follow the clock's house year, never the latest year that exists.
	fw1PlantFuturePeriod(t, pool, beyond+80)
	// The REAL recorder, not a fake: the whole defect lives between the two.
	recorder := audit.NewService(audit.NewRepository(pool), nil)
	controller := fw1R4Controller(pool, user, house).WithAuditTxRecorder(recorder)

	var before int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'GL_FISCAL_YEAR_OPEN'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	res, err := controller.OpenNextFiscalYear(ctx, "FW1-01 round 4 real recorder proof")
	if err != nil {
		t.Fatalf("the Controller's open through the real audit recorder: %v", err)
	}
	if res.FiscalYear != beyond {
		t.Fatalf("the open answered %d, want %d (the cap year)", res.FiscalYear, beyond)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'GL_FISCAL_YEAR_OPEN'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != before+1 {
		t.Fatalf("audit_log holds %d GL_FISCAL_YEAR_OPEN rows after the open (before %d), want exactly one more", n, before)
	}
	var entityID, yearWords, actor string
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(entity_id::text,''), COALESCE(changes->>'fiscal_year',''), COALESCE(actor_id::text,'')
		 FROM audit_log WHERE action = 'GL_FISCAL_YEAR_OPEN' ORDER BY seq DESC LIMIT 1`).Scan(&entityID, &yearWords, &actor); err != nil {
		t.Fatal(err)
	}
	var januaryID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM fiscal_periods WHERE year = $1 AND month = 1`, beyond).Scan(&januaryID); err != nil {
		t.Fatalf("read the opened year's January period: %v", err)
	}
	if entityID != januaryID {
		t.Fatalf("the audit's entity_id is %q, want the opened year's January period id %q (audit_log.entity_id is a uuid column)", entityID, januaryID)
	}
	if yearWords != fmt.Sprintf("%d", beyond) {
		t.Fatalf("the audit's changes carry fiscal_year %q, want %d (the year rides Extra once the entity is the period)", yearWords, beyond)
	}
	if actor != user {
		t.Fatalf("the audit's actor is %q, want the Controller %q", actor, user)
	}
}

// TestIntegration_FW1_01R4_PeriodCloseLeavesItsOverrideRow is the period
// close leg through the real recorder: the close's audit trail is the
// gl_period_overrides row (CLOSE, the actor, the reason), which the store
// writes inside the close's transaction. The close writes no audit_log row by
// design (the reopen is the privileged, reason-bearing act that does); the
// recorder is wired anyway, the way production wires it.
func TestIntegration_FW1_01R4_PeriodCloseLeavesItsOverrideRow(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	// The closed period sits in the year after the books' latest (the roll forward's target),
	// planted by the leg, so no books' year can move under it.
	house := fw1NextYear(t, pool) - 1
	target := house + 1
	fw1ScrubYear(t, pool, target)
	fw1EnsureHouseYear(t, pool, target)
	t.Cleanup(func() { fw1ScrubYear(t, pool, target) })
	recorder := audit.NewService(audit.NewRepository(pool), nil)
	controller := fw1R4Controller(pool, user, house).WithAudit(recorder)

	var periodID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM fiscal_periods WHERE year = $1 AND month = 2`, target).Scan(&periodID); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gl_period_overrides WHERE period_id = $1`, periodID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.ClosePeriod(ctx, periodID, "FW1-01 round 4 close through the real recorder"); err != nil {
		t.Fatalf("the period close through the real audit recorder: %v", err)
	}
	var n int
	var action, actor, reason string
	if err := pool.QueryRow(ctx,
		`SELECT count(*), COALESCE(max(action),''), COALESCE(max(actor_id::text),''), COALESCE(max(reason),'')
		 FROM gl_period_overrides WHERE period_id = $1`, periodID).Scan(&n, &action, &actor, &reason); err != nil {
		t.Fatal(err)
	}
	if n != before+1 {
		t.Fatalf("the close left %d override rows (before %d), want exactly one more", n, before)
	}
	if action != "CLOSE" {
		t.Fatalf("the close's override action is %q, want CLOSE", action)
	}
	if actor != user {
		t.Fatalf("the close's override actor is %q, want the caller %q", actor, user)
	}
	if reason != "FW1-01 round 4 close through the real recorder" {
		t.Fatalf("the close's override reason is %q, want the close's reason", reason)
	}
}

// TestIntegration_FW1_01R4_ReopenAuditRowThroughTheRealRecorder is the reopen
// leg through the real recorder: the reopen is the privileged act whose
// recorded compensating control is the GL_PERIOD_REOPEN sensitive-access row,
// keyed to the period's uuid, beside the override row.
func TestIntegration_FW1_01R4_ReopenAuditRowThroughTheRealRecorder(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	// The reopened period sits in the year after the books' latest (the roll forward's target),
	// planted by the leg, so no books' year can move under it.
	house := fw1NextYear(t, pool) - 1
	target := house + 1
	fw1ScrubYear(t, pool, target)
	fw1EnsureHouseYear(t, pool, target)
	t.Cleanup(func() { fw1ScrubYear(t, pool, target) })
	recorder := audit.NewService(audit.NewRepository(pool), nil)
	controller := fw1R4Controller(pool, user, house).WithAudit(recorder)

	var periodID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM fiscal_periods WHERE year = $1 AND month = 3`, target).Scan(&periodID); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.ClosePeriod(ctx, periodID, "round 4 reopen setup"); err != nil {
		t.Fatalf("setup close: %v", err)
	}
	var before int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'GL_PERIOD_REOPEN'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.ReopenPeriod(ctx, periodID, "FW1-01 round 4 reopen through the real recorder"); err != nil {
		t.Fatalf("the reopen through the real audit recorder: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'GL_PERIOD_REOPEN'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != before+1 {
		t.Fatalf("audit_log holds %d GL_PERIOD_REOPEN rows after the reopen (before %d), want exactly one more", n, before)
	}
	var entityID, actor string
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(entity_id::text,''), COALESCE(actor_id::text,'')
		 FROM audit_log WHERE action = 'GL_PERIOD_REOPEN' ORDER BY seq DESC LIMIT 1`).Scan(&entityID, &actor); err != nil {
		t.Fatal(err)
	}
	if entityID != periodID {
		t.Fatalf("the reopen audit's entity_id is %q, want the period %q", entityID, periodID)
	}
	if actor != user {
		t.Fatalf("the reopen audit's actor is %q, want the Controller %q", actor, user)
	}
	var overrides int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gl_period_overrides WHERE period_id = $1 AND action = 'REOPEN'`, periodID).Scan(&overrides); err != nil {
		t.Fatal(err)
	}
	if overrides != 1 {
		t.Fatalf("the reopen left %d REOPEN override rows, want 1", overrides)
	}
}

// TestIntegration_FW1_01R4_YearEndLeavesItsOverrideRows is the year end leg
// through the real recorder: the close's audit trail is one CLOSE override
// row per sealed period (the year-end close seals them through
// ClosePeriodsForYear), beside the closing journal.
func TestIntegration_FW1_01R4_YearEndLeavesItsOverrideRows(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	// 2091 is the leg's own far future band (the package's 209x isolation rule): ensurePeriods
	// plants it and scrubs it on cleanup, and every assertion below reads only 2091's own rows.
	ensurePeriods(t, pool, 2091)
	recorder := audit.NewService(audit.NewRepository(pool), nil)
	// The clock stands AFTER 2091's last day (main 613's round 2 rule: the year end close is
	// refused until the year has ended in the dealer's zone), and the P&L journals carry a 2091
	// document date, so they file into 2091 (a posting files by its document's period).
	controller := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: controllerRole, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}, time.Date(2092, 1, 5, 12, 0, 0, 0, time.UTC)).WithAudit(recorder)

	// Post a revenue and an expense so the year end has a net income to sweep (the AC13 seed).
	if _, err := controller.PostExternal(ctx, ExternalJournal{
		Source: SourceAdjustment, IdempotencyKey: fmt.Sprintf("r4-rev-%d", rand.Uint64()),
		DocumentDate: time.Date(2091, 6, 10, 12, 0, 0, 0, time.UTC),
		Lines:        []ExternalLine{{AccountCode: "1000", DebitCents: 100000}, {AccountCode: "4000", CreditCents: 100000}},
	}); err != nil {
		t.Fatalf("post revenue: %v", err)
	}
	if _, err := controller.PostExternal(ctx, ExternalJournal{
		Source: SourceAdjustment, IdempotencyKey: fmt.Sprintf("r4-exp-%d", rand.Uint64()),
		DocumentDate: time.Date(2091, 6, 20, 12, 0, 0, 0, time.UTC),
		Lines:        []ExternalLine{{AccountCode: "6100", DebitCents: 40000}, {AccountCode: "1000", CreditCents: 40000}},
	}); err != nil {
		t.Fatalf("post expense: %v", err)
	}

	res, err := controller.CloseYear(ctx, 2091, "FW1-01 round 4 year end through the real recorder")
	if err != nil {
		t.Fatalf("the year end through the real audit recorder: %v", err)
	}
	if res.ClosingJournalID == "" || !res.PeriodsClosed {
		t.Fatalf("the year end must post a closing journal and seal the periods, got %+v", res)
	}
	var periods, overrides int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM fiscal_periods WHERE year = 2091 AND status = 'CLOSED'`).Scan(&periods); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM gl_period_overrides o JOIN fiscal_periods p ON p.id = o.period_id
		 WHERE p.year = 2091 AND o.action = 'CLOSE'`).Scan(&overrides); err != nil {
		t.Fatal(err)
	}
	if periods == 0 {
		t.Fatal("the year end sealed no period of 2091")
	}
	if overrides != periods {
		t.Fatalf("the year end left %d CLOSE override rows for %d sealed periods, want one per sealed period", overrides, periods)
	}
}

// TestIntegration_FW1_01R4_HeadOfficeSetAuditRowThroughTheRealRecorder is the
// head office set leg through the real recorder: the Controller's change
// stamps GL_HEAD_OFFICE_BRANCH_SET inside the policy write's transaction,
// keyed to the branch's uuid.
func TestIntegration_FW1_01R4_HeadOfficeSetAuditRowThroughTheRealRecorder(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	house := fw1NextYear(t, pool) - 1
	recorder := audit.NewService(audit.NewRepository(pool), nil)
	controller := fw1R4Controller(pool, user, house).WithAuditTxRecorder(recorder)

	// The head office the policy holds BEFORE the leg's write: the cleanup must put back exactly
	// this value (FLAKE-9), the way it found it.
	prior := fw1R4PolicyHeadOffice(t, pool)
	branchID, scoped := fw1R4HeadOfficeCandidate(t, pool, "")
	var before int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'GL_HEAD_OFFICE_BRANCH_SET'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := controller.SetHeadOfficeBranch(scoped, branchID, "FW1-01 round 4 head office set through the real recorder"); err != nil {
		t.Fatalf("the head office set through the real audit recorder: %v", err)
	}
	t.Cleanup(func() {
		fw1R4RestoreHeadOffice(ctx, pool, prior)
	})

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'GL_HEAD_OFFICE_BRANCH_SET'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != before+1 {
		t.Fatalf("audit_log holds %d GL_HEAD_OFFICE_BRANCH_SET rows after the set (before %d), want exactly one more", n, before)
	}
	var entityID, actor string
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(entity_id::text,''), COALESCE(actor_id::text,'')
		 FROM audit_log WHERE action = 'GL_HEAD_OFFICE_BRANCH_SET' ORDER BY seq DESC LIMIT 1`).Scan(&entityID, &actor); err != nil {
		t.Fatal(err)
	}
	if entityID != branchID {
		t.Fatalf("the head office audit's entity_id is %q, want the branch %q", entityID, branchID)
	}
	if actor != user {
		t.Fatalf("the head office audit's actor is %q, want the Controller %q", actor, user)
	}
}

// TestIntegration_FW1_01R4_TheHeadOfficeLegLeavesThePolicyHeadOfficeAsItFoundIt is FLAKE-9's red
// (FROM-PRINCIPAL 344): a leg leaves the shared gate database exactly as it found it.
//
// The head office leg's cleanup wrote NULL whatever the policy held, so a box whose policy named
// a head office (migration 0445 backfills one) was left with the leg's own write instead of its
// own. On the gate database the column is NOT NULL, so that write is refused and the policy KEEPS
// the leg's candidate: the head office the policy held before the leg is not the one it holds
// after it, and the FW1-10 leg then reads the wrong head office on a reused box.
//
// The leg reads the policy before the write and after the cleanup, and it fails when the two
// differ. Its own safety net puts the policy back whatever the cleanup under test does, so the
// red run leaves the gate database as it found it.
func TestIntegration_FW1_01R4_TheHeadOfficeLegLeavesThePolicyHeadOfficeAsItFoundIt(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	house := fw1NextYear(t, pool) - 1
	controller := fw1R4Controller(pool, user, house).WithAuditTxRecorder(audit.NewService(audit.NewRepository(pool), nil))

	// The head office the policy holds before the leg.
	prior := fw1R4PolicyHeadOffice(t, pool)
	if prior == "" {
		// A policy that names no head office cannot lose one: plant one through the real path
		// first, so the leg has a head office to put back.
		planted, scoped := fw1R4HeadOfficeCandidate(t, pool, "")
		if err := controller.SetHeadOfficeBranch(scoped, planted, "FLAKE-9 red: plant a head office to lose"); err != nil {
			t.Fatalf("plant the head office: %v", err)
		}
		prior = fw1R4PolicyHeadOffice(t, pool)
		if prior == "" {
			t.Fatal("the planted head office did not land in the policy")
		}
	}
	// This leg's own safety net: the red run must leave the gate database exactly as it found it,
	// whatever the cleanup under test does.
	t.Cleanup(func() {
		fw1R4WritePolicyHeadOffice(context.Background(), pool, prior)
	})

	// A candidate that is not the head office the policy holds, so the leg's own write is visible.
	branchID, scoped := fw1R4HeadOfficeCandidate(t, pool, prior)
	if err := controller.SetHeadOfficeBranch(scoped, branchID, "FLAKE-9 red: the head office leg must put the policy back"); err != nil {
		t.Fatalf("the head office set: %v", err)
	}
	// The cleanup under test: the one the round 4 leg runs when it ends.
	fw1R4RestoreHeadOffice(ctx, pool, prior)

	after := fw1R4PolicyHeadOffice(t, pool)
	if after != prior {
		t.Fatalf("the head office leg left the policy's head office at %s, want %s: it must put back exactly what it found",
			fw1R4HeadOfficeName(t, pool, after), fw1R4HeadOfficeName(t, pool, prior))
	}
}

// TestIntegration_FW1_01R4_GapYearIsRefusedInWords is S2's round 3 P3 red:
// the first-year check refuses a year before the books' first year, but a
// year MISSING BETWEEN the books' first year and the house year (a hole in
// the fiscal calendar) passed the check and fell through to a silent 200 with
// the cap year. After the fix it is refused in the same words' shape, with no
// year written and no audit row.
func TestIntegration_FW1_01R4_GapYearIsRefusedInWords(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	// The band is the database's own frontier, the way the roll forward leg's dynamic `beyond`
	// takes it: the leg plants one period two years below the house (the books' first year), the
	// year between it and the house is the gap, and the two years above stay free.
	house := fw1NextYear(t, pool) - 1
	first, gap, target, beyond := house-2, house-1, house+1, house+2
	fw1EnsureHouseYear(t, pool, house)
	fw1ScrubYear(t, pool, first, gap, target, beyond)
	t.Cleanup(func() { fw1ScrubYear(t, pool, first, gap, target, beyond) })
	// The planted period makes the year two below the house the books' first year, so the scrubbed
	// year between it and the house is a gap, not a year before the books.
	if _, err := pool.Exec(ctx,
		`INSERT INTO fiscal_periods (year, month, status) VALUES ($1, 1, 'OPEN') ON CONFLICT (year, month) DO NOTHING`, first); err != nil {
		t.Fatalf("plant the first year's period: %v", err)
	}
	auditTx := &fw1AuditTx{}
	controller := fw1R4Controller(pool, user, house).WithAuditTxRecorder(auditTx)
	h := NewHandler(controller, nil)

	postOpenYear := func(body string) (int, string) {
		t.Helper()
		rec := httptest.NewRequest(http.MethodPost, "/api/v1/gl/periods/open-year", strings.NewReader(body))
		rsp := httptest.NewRecorder()
		h.openYear(rsp, rec, guards.StaffCaller{})
		return rsp.Code, rsp.Body.String()
	}

	code, body := postOpenYear(fmt.Sprintf(`{"year":%d,"reason":"the gap year"}`, gap))
	if code == http.StatusOK || !strings.Contains(body, fmt.Sprintf("%d is missing from the books; only the next year can be opened here", gap)) {
		t.Fatalf("requesting the gap year %d answered %d %s, want refusal '%d is missing from the books; only the next year can be opened here'", gap, code, body, gap)
	}
	if len(auditTx.recorded) != 0 {
		t.Fatalf("the refused gap year recorded %d audit rows, want none", len(auditTx.recorded))
	}
	for _, year := range []int{gap, target, beyond} {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM fiscal_periods WHERE year=$1`, year).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("the refused gap year left %d periods behind for %d, want none", n, year)
		}
	}
}

// TestIntegration_FW1_01R4_TheCoresRefusalWords pins the core's own refusal
// words at the handler (S2's round 3 P3: the open's word has no gl leg, only
// the stub's and the screen's were pinned). A non Controller asking for the
// early open is refused with the open's own word, never the reopen's; a non
// Controller reopen keeps the reopen's word.
func TestIntegration_FW1_01R4_TheCoresRefusalWords(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	user := aUser(t, pool)
	// The band is the database's own frontier, the way the roll forward leg's dynamic `beyond`
	// takes it: the house year IS the latest year that exists, and the two years after it stay
	// free.
	house := fw1NextYear(t, pool) - 1
	target, beyond := house+1, house+2
	fw1EnsureHouseYear(t, pool, house)
	fw1ScrubYear(t, pool, target, beyond)
	t.Cleanup(func() { fw1ScrubYear(t, pool, target, beyond) })
	fw1PlantFuturePeriod(t, pool, beyond+80)
	clockAt := time.Date(house, 6, 15, 12, 0, 0, 0, houseday.DefaultZone().Location())
	manager := svcAt(pool, identity.StaffContext{UserID: user, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager}, clockAt)
	h := NewHandler(manager, nil)

	post := func(path, id, body string) (int, string) {
		t.Helper()
		rec := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		if id != "" {
			// The direct call carries the path value the router would (pathID reads it).
			rec.SetPathValue("id", id)
		}
		rsp := httptest.NewRecorder()
		if path == "/api/v1/gl/periods/open-year" {
			h.openYear(rsp, rec, guards.StaffCaller{})
		} else {
			h.reopenPeriod(rsp, rec, guards.StaffCaller{})
		}
		return rsp.Code, rsp.Body.String()
	}

	code, body := post("/api/v1/gl/periods/open-year", "", `{"reason":"a manager asks"}`)
	if code != http.StatusForbidden || !strings.Contains(body, "only the Financial Controller may open the next fiscal year") {
		t.Fatalf("a non Controller open answered %d %s, want 403 'only the Financial Controller may open the next fiscal year'", code, body)
	}
	if strings.Contains(body, "reopen a closed period") {
		t.Fatalf("the open's refusal speaks of reopening: %s", body)
	}
	var periodID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM fiscal_periods WHERE year = $1 AND month = 8`, house).Scan(&periodID); err != nil {
		t.Fatal(err)
	}
	code, body = post("/api/v1/gl/periods/{id}/reopen", periodID, `{"reason":"a manager asks"}`)
	if code != http.StatusForbidden || !strings.Contains(body, "only the Financial Controller may reopen a closed period") {
		t.Fatalf("a non Controller reopen answered %d %s, want 403 'only the Financial Controller may reopen a closed period'", code, body)
	}
}
