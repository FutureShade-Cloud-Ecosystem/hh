//go:build integration

package main

// FW1-08b part B (FROM-PRINCIPAL 162, 176) integration legs on real Postgres.
//
// TestFW1_08b_PartB_HeadOfficeSetChecksItsCandidate pins the head office set's candidate check:
// only a location with loc_type = 'BRANCH' in the caller's org is accepted, another org's branch
// and a non branch location are refused in words, and the non-controller refusal carries the
// set's own words, not the reopen ones (FROM-PRINCIPAL 206).

import (
	"context"
	"log/slog"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/branches"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/google/uuid"
)

func TestFW1_08b_PartB_HeadOfficeSetChecksItsCandidate(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "fw108bpbho")

	var trtID, kgnID, userID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE code = 'TRT'`).Scan(&trtID); err != nil {
		t.Fatalf("resolve TRT: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE code = 'KGN'`).Scan(&kgnID); err != nil {
		t.Fatalf("resolve KGN: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("resolve a user: %v", err)
	}

	// The caller's org and a second dealer org with a branch of its own, plus a zone row under
	// Trenton: the three candidates the check must tell apart.
	orgA, orgB := uuid.NewString(), uuid.NewString()
	foreignBranchID, zoneID := uuid.NewString(), uuid.NewString()
	mustExec(t, ctx, pool, `INSERT INTO org (id, slug, name) VALUES ($1, 'fw108bpba', 'FW1 08b Part B Org A')`, orgA)
	mustExec(t, ctx, pool, `INSERT INTO org (id, slug, name) VALUES ($1, 'fw108bpbb', 'FW1 08b Part B Org B')`, orgB)
	mustExec(t, ctx, pool, `UPDATE locations SET org_id = $1 WHERE loc_type = 'BRANCH'`, orgA)
	mustExec(t, ctx, pool, `
		INSERT INTO locations (id, branch_id, parent_id, loc_type, code, name, org_id)
		VALUES ($1::uuid, $1::uuid, NULL, 'BRANCH', 'FBD', 'Another org yard', $2::uuid)
	`, foreignBranchID, orgB)
	mustExec(t, ctx, pool, `
		INSERT INTO locations (id, branch_id, parent_id, loc_type, code, name, org_id)
		VALUES ($1::uuid, $2::uuid, $2::uuid, 'ZONE', 'ZONE-PB', 'Part B zone row', $3::uuid)
	`, zoneID, trtID, orgA)

	auditSvc := audit.NewService(audit.NewRepository(pool), slog.Default())
	glSvc := gl.NewService(gl.NewRepository(pool).WithZone(newDealerZone(pool))).
		WithAudit(auditSvc).
		WithAuditTxRecorder(auditSvc).
		WithBranchFacts(glBranchFacts{repo: branches.NewRepository(pool)})

	ctrl := identity.StaffContext{UserID: userID, RoleCode: "FINANCIAL_CONTROLLER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal}
	mgr := identity.StaffContext{UserID: userID, RoleCode: "SALES_MANAGER", Tier: identity.TierManager, Scope: identity.ScopeGlobal}
	scopedCtrl := identity.ContextWithStaff(authz.WithStaffScope(ctx, authz.StaffScope{OrgID: orgA}), ctrl)
	scopedMgr := identity.ContextWithStaff(authz.WithStaffScope(ctx, authz.StaffScope{OrgID: orgA}), mgr)

	t.Run("another_orgs_branch_refused_in_words", func(t *testing.T) {
		err := glSvc.SetHeadOfficeBranch(scopedCtrl, foreignBranchID, "part B: another org's branch")
		if err == nil {
			t.Fatal("want a refusal for another org's branch, got <nil>")
		}
		if status, ok := gl.ErrorHTTPStatus(err); !ok || status != 403 {
			t.Fatalf("another org's branch refusal status = (%d, %v), want (403, true); err=%v", status, ok, err)
		}
	})

	t.Run("non_branch_location_refused_in_words", func(t *testing.T) {
		err := glSvc.SetHeadOfficeBranch(scopedCtrl, zoneID, "part B: a zone is not a branch")
		if err == nil {
			t.Fatal("want a refusal for a non branch location, got <nil>")
		}
		if status, ok := gl.ErrorHTTPStatus(err); !ok || status != 422 {
			t.Fatalf("non branch location refusal status = (%d, %v), want (422, true); err=%v", status, ok, err)
		}
	})

	t.Run("the_non_controller_words_name_the_set_not_the_reopen", func(t *testing.T) {
		err := glSvc.SetHeadOfficeBranch(scopedMgr, kgnID, "part B: the set's own words")
		want := "gl: only the Financial Controller may set the head office branch"
		if err == nil || err.Error() != want {
			t.Fatalf("want the head office set's own refusal words %q, got %v", want, err)
		}
	})

	t.Run("a_branch_of_the_callers_org_is_still_accepted", func(t *testing.T) {
		if err := glSvc.SetHeadOfficeBranch(scopedCtrl, kgnID, "part B: relocate within the caller's org"); err != nil {
			t.Fatalf("SetHeadOfficeBranch(KGN): %v", err)
		}
		if ho, err := glSvc.HeadOfficeBranchID(scopedCtrl); err != nil || ho != kgnID {
			t.Fatalf("HeadOfficeBranchID after the set = %q (err=%v), want KGN (%q)", ho, err, kgnID)
		}
		if err := glSvc.SetHeadOfficeBranch(scopedCtrl, trtID, "part B: restore Trenton"); err != nil {
			t.Fatalf("SetHeadOfficeBranch(TRT) restore: %v", err)
		}
	})
}

// TestFW1_08b_PartB_HeadOfficeFallbackReadsTRTByCode pins the head office default's fallback
// (FW1-08b part B, ruling 162): when gl_policy sets no head office branch, the read answers the
// branch the TRT yard code names, and when no branch answers it refuses in words, never stamping
// the fixed Trenton uuid a box with history does not carry.
func TestFW1_08b_PartB_HeadOfficeFallbackReadsTRTByCode(t *testing.T) {
	ctx := context.Background()

	t.Run("reads_trt_by_code_when_the_policy_row_is_gone", func(t *testing.T) {
		pool := freshMigratedDB(t, "fw108bpbcody")
		mustExec(t, ctx, pool, `DELETE FROM gl_policy WHERE branch_id IS NULL`)
		// Retire the fixed Trenton uuid's yard code and mint a NEW branch carrying the TRT code:
		// the fallback must answer the code's branch, not the fixed uuid (ruling 162).
		mustExec(t, ctx, pool, `UPDATE locations SET code = 'XRT' WHERE code = 'TRT'`)
		var newTrtID string
		if err := pool.QueryRow(ctx, `
			INSERT INTO locations (id, branch_id, parent_id, loc_type, code, name)
			VALUES ($1::uuid, $1::uuid, NULL, 'BRANCH', 'TRT', 'Trenton renewed')
			RETURNING id::text
		`, uuid.NewString()).Scan(&newTrtID); err != nil {
			t.Fatalf("mint the renewed TRT branch: %v", err)
		}
		repo := gl.NewRepository(pool)
		svc := gl.NewService(repo).WithBranchFacts(glBranchFacts{repo: branches.NewRepository(pool)})
		ho, err := svc.HeadOfficeBranchID(ctx)
		if err != nil {
			t.Fatalf("HeadOfficeBranchID through the code read: %v", err)
		}
		if ho != newTrtID {
			t.Fatalf("HeadOfficeBranchID fallback = %q, want the branch the TRT code names (%q)", ho, newTrtID)
		}
	})

	t.Run("refuses_in_words_when_no_branch_answers_the_code", func(t *testing.T) {
		pool := freshMigratedDB(t, "fw108bpbnobranch")
		mustExec(t, ctx, pool, `DELETE FROM gl_policy WHERE branch_id IS NULL`)
		mustExec(t, ctx, pool, `UPDATE locations SET code = 'XRT' WHERE code = 'TRT'`)
		repo := gl.NewRepository(pool)
		svc := gl.NewService(repo).WithBranchFacts(glBranchFacts{repo: branches.NewRepository(pool)})
		ho, err := svc.HeadOfficeBranchID(ctx)
		if err == nil {
			t.Fatalf("want the fallback's refusal in words when no branch answers the TRT code, got the uuid %q with no error", ho)
		}
		status, ok := gl.ErrorHTTPStatus(err)
		if !ok {
			t.Fatalf("the fallback's refusal must be a client error in words, never a 500; got %v", err)
		}
		if status != 422 {
			t.Fatalf("the fallback's refusal status = %d, want 422; err=%v", status, err)
		}
	})
}

// TestFW1_08b_PartB_Migration0446BranchCheck pins migration 0446 (FROM-PRINCIPAL 162, 176): a
// CHECK that gl_journal_lines.branch_id is NOT NULL, added NOT VALID then VALIDATEd, whose pre
// check counts the unstamped lines FIRST and refuses in words naming each branchless journal
// (its id, source and date), so a box with an old close is never validated blind. The migration
// backfills nothing: a pre 629 year end close is not backfilled to the head office, and there is
// no reverse and rerun tool.
func TestFW1_08b_PartB_Migration0446BranchCheck(t *testing.T) {
	ctx := context.Background()
	const checkName = "gl_journal_lines_branch_id_not_null"

	// plantBranchlessJournal inserts one POSTED journal carrying two balanced branchless lines,
	// the shape a pre 629 year end close left behind, and answers the journal's id.
	plantBranchlessJournal := func(t *testing.T, pool *pgxpool.Pool) string {
		t.Helper()
		mustExec(t, ctx, pool, `
			INSERT INTO fiscal_periods (year, month, status)
			SELECT 2026, m, 'OPEN' FROM generate_series(1, 12) AS m
			ON CONFLICT (year, month) DO NOTHING
		`)
		var jid string
		if err := pool.QueryRow(ctx, `
			INSERT INTO gl_journals (fiscal_year, entry_no, period_id, source, memo, status, posted_at, idempotency_key)
			VALUES (2026,
			        (SELECT COALESCE(MAX(entry_no), 0) + 1 FROM gl_journals WHERE fiscal_year = 2026),
			        (SELECT id FROM fiscal_periods WHERE year = 2026 AND month = 6),
			        'MANUAL', 'fw1 08b part b branchless probe', 'POSTED', '2026-06-15 15:00:00+00',
			        'fw108pb-branchless-' || gen_random_uuid()::text)
			RETURNING id::text
		`).Scan(&jid); err != nil {
			t.Fatalf("plant the branchless journal: %v", err)
		}
		mustExec(t, ctx, pool, `
			INSERT INTO gl_journal_lines (journal_id, line_no, account_id, debit_cents, memo)
			SELECT $1::uuid, 9001, (SELECT id FROM gl_accounts WHERE is_active ORDER BY code LIMIT 1), 100, 'probe dr'
			UNION ALL
			SELECT $1::uuid, 9002, (SELECT id FROM gl_accounts WHERE is_active ORDER BY code LIMIT 1), NULL, 'probe cr'
			 WHERE NOT EXISTS (SELECT 1 FROM gl_journal_lines WHERE journal_id = $1::uuid AND line_no IN (9001, 9002))
		`, jid)
		// The credit leg needs its own row: the UNION above inserts the debit twice, so write the
		// credit explicitly.
		mustExec(t, ctx, pool, `
			INSERT INTO gl_journal_lines (journal_id, line_no, account_id, credit_cents, memo)
			SELECT $1::uuid, 9002, (SELECT id FROM gl_accounts WHERE is_active ORDER BY code LIMIT 1), 100, 'probe cr'
		`, jid)
		var nullLines int
		if err := pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM gl_journal_lines WHERE journal_id = $1::uuid AND branch_id IS NULL`, jid,
		).Scan(&nullLines); err != nil {
			t.Fatalf("count the branchless probe lines: %v", err)
		}
		if nullLines != 2 {
			t.Fatalf("the probe journal carries %d branchless lines, want 2", nullLines)
		}
		return jid
	}

	t.Run("a_fresh_box_carries_the_check_and_refuses_a_branchless_line", func(t *testing.T) {
		pool := freshMigratedDB(t, "fw108pbchk")
		var validated bool
		err := pool.QueryRow(ctx, `
			SELECT convalidated FROM pg_constraint
			 WHERE conname = $1 AND conrelid = 'gl_journal_lines'::regclass
		`, checkName).Scan(&validated)
		if err != nil {
			t.Fatalf("migration 0446's CHECK is missing on a fresh box: %v", err)
		}
		if !validated {
			t.Fatal("migration 0446's CHECK landed NOT VALIDATED, want VALIDATEd")
		}
		jid := plantBranchlessJournal(t, pool)
		_, err = pool.Exec(ctx, `
			INSERT INTO gl_journal_lines (journal_id, line_no, account_id, debit_cents, memo)
			VALUES ($1::uuid, 9003, (SELECT id FROM gl_accounts WHERE is_active ORDER BY code LIMIT 1), 100, 'the database must refuse this line')
		`, jid)
		if err == nil {
			t.Fatal("want the database to refuse a journal line with no branch after migration 0446, got <nil>")
		}
	})

	t.Run("an_upgraded_seeded_box_validates_clean_and_refuses_a_branchless_line", func(t *testing.T) {
		pool := freshMigratedDBWithout(t, "fw108pbsd", "0446_gl_branch_stamp.sql")
		body, err := seedFS.ReadFile("dibbits_seed.sql")
		if err != nil {
			t.Fatalf("reading the embedded seed: %v", err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			t.Fatalf("applying the seed: %v", err)
		}
		var nullLines int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM gl_journal_lines WHERE branch_id IS NULL`).Scan(&nullLines); err != nil {
			t.Fatalf("count branchless lines on the seeded box: %v", err)
		}
		if nullLines != 0 {
			t.Fatalf("the seeded box carries %d branchless lines, want 0 before 0446 validates", nullLines)
		}
		migration, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", "0446_gl_branch_stamp.sql"))
		if err != nil {
			t.Fatalf("read migration 0446: %v", err)
		}
		if _, err := pool.Exec(ctx, string(migration)); err != nil {
			t.Fatalf("apply migration 0446 to the upgraded seeded box: %v", err)
		}
		var validated bool
		if err := pool.QueryRow(ctx, `
			SELECT convalidated FROM pg_constraint
			 WHERE conname = $1 AND conrelid = 'gl_journal_lines'::regclass
		`, checkName).Scan(&validated); err != nil || !validated {
			t.Fatalf("the CHECK after 0446 on the upgraded seeded box: validated=%v err=%v, want true and no error", validated, err)
		}
		jid := plantBranchlessJournal(t, pool)
		_, err = pool.Exec(ctx, `
			INSERT INTO gl_journal_lines (journal_id, line_no, account_id, debit_cents, memo)
			VALUES ($1::uuid, 9003, (SELECT id FROM gl_accounts WHERE is_active ORDER BY code LIMIT 1), 100, 'the database must refuse this line')
		`, jid)
		if err == nil {
			t.Fatal("want the database to refuse a journal line with no branch after migration 0446, got <nil>")
		}
	})

	t.Run("the_pre_check_refuses_naming_each_branchless_journal", func(t *testing.T) {
		pool := freshMigratedDBWithout(t, "fw108pbref", "0446_gl_branch_stamp.sql")
		jid := plantBranchlessJournal(t, pool)
		migration, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", "0446_gl_branch_stamp.sql"))
		if err != nil {
			t.Fatalf("read migration 0446: %v", err)
		}
		_, applyErr := pool.Exec(ctx, string(migration))
		if applyErr == nil {
			t.Fatal("want migration 0446's pre check to refuse a box holding a branchless line, got success")
		}
		// The refusal names the branchless journal: its id, its source and its date.
		for _, want := range []string{jid, "MANUAL", "2026-06-15"} {
			if !strings.Contains(applyErr.Error(), want) {
				t.Fatalf("the pre check's refusal does not name %q in: %v", want, applyErr)
			}
		}
		var constraints int
		if err := pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM pg_constraint
			 WHERE conname = $1 AND conrelid = 'gl_journal_lines'::regclass
		`, checkName).Scan(&constraints); err != nil {
			t.Fatalf("count the CHECK after the refused migration: %v", err)
		}
		if constraints != 0 {
			t.Fatal("the refused migration must leave no CHECK behind")
		}
	})
}
