//go:build integration

package main

// TestFW1_08b_Migration0445HeadOfficeBranch pins migration 0445 (FROM-PRINCIPAL 162) on a real
// Postgres built from zero, one database per leg:
//
//  1. The head office branch is a REAL gl_policy column: the Controller's SetHeadOfficeBranch
//     writes it, the sensitive access audit row lands, and a Repository built after the set (a
//     restart, a second pod) reads the set branch back, not the Trenton default.
//  2. On a box whose branches include TRT, the migration backfills the org policy row and every
//     0056/0221 setup entry's journal lines with the TRT branch.
//  3. On a box whose branches have no TRT code, the migration backfills the earliest branch
//     (created_at, then id) instead, so an existing dealer box migrates cleanly too.
//  4. A per-branch gl_policy row (the JE threshold override path) carries the head office branch
//     as well, so the migration's NOT NULL cannot break SetJEThreshold the first time a branch
//     configures its own maker-checker threshold.

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

func fw10445SetupEntries(t *testing.T, ctx context.Context, pool *pgxpool.Pool, hoBranchID string) {
	t.Helper()
	var total, nullLines, wrongBranch int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE l.branch_id IS NULL),
		       COUNT(*) FILTER (WHERE l.branch_id <> $1::uuid)
		  FROM gl_journal_lines l
		  JOIN gl_journals j ON j.id = l.journal_id
		 WHERE j.idempotency_key LIKE 'm2c1-demo-%' OR j.idempotency_key LIKE 'm2c1-retire-%'
	`, hoBranchID).Scan(&total, &nullLines, &wrongBranch); err != nil {
		t.Fatalf("setup entry lines: %v", err)
	}
	if total == 0 {
		t.Fatal("no 0056/0221 setup entry journal lines found on a from-zero box")
	}
	if nullLines != 0 {
		t.Fatalf("setup entry journal lines with branch_id IS NULL = %d/%d, want 0 (migration 0445 backfill)", nullLines, total)
	}
	if wrongBranch != 0 {
		t.Fatalf("setup entry journal lines not on the head office branch = %d/%d, want 0", wrongBranch, total)
	}
}

func TestFW1_08b_Migration0445HeadOfficeBranch(t *testing.T) {
	ctx := context.Background()

	t.Run("head_office_branch_sets_audits_and_reads_through_the_real_column", func(t *testing.T) {
		pool := freshMigratedDB(t, "fw108bho")

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

		auditSvc := audit.NewService(audit.NewRepository(pool), slog.Default())
		glSvc := gl.NewService(gl.NewRepository(pool).WithZone(newDealerZone(pool))).
			WithAudit(auditSvc).
			WithAuditTxRecorder(auditSvc)
		ctrlCtx := identity.ContextWithStaff(ctx, identity.StaffContext{
			UserID:   userID,
			RoleCode: "FINANCIAL_CONTROLLER",
			Tier:     identity.TierLeadership,
			Scope:    identity.ScopeGlobal,
		})

		if ho, err := glSvc.HeadOfficeBranchID(ctrlCtx); err != nil || ho != trtID {
			t.Fatalf("HeadOfficeBranchID default = %q (err=%v), want TRT (%q)", ho, err, trtID)
		}
		if err := glSvc.SetHeadOfficeBranch(ctrlCtx, kgnID, "FW1-08b migration 0445 head office leg"); err != nil {
			t.Fatalf("SetHeadOfficeBranch(KGN): %v", err)
		}
		var auditCount int
		if err := pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM audit_log
			 WHERE action = 'GL_HEAD_OFFICE_BRANCH_SET'
			   AND entity_type = 'gl.policy'
			   AND branch_id = $1::uuid
		`, kgnID).Scan(&auditCount); err != nil {
			t.Fatalf("query audit_log: %v", err)
		}
		if auditCount != 1 {
			t.Fatalf("audit_log GL_HEAD_OFFICE_BRANCH_SET rows = %d, want 1", auditCount)
		}
		// A Repository built AFTER the set has no in-memory override: only the real
		// column can answer it. Before 0445 the set falls back to a savepoint touch and
		// this read returns the Trenton default again.
		fresh := gl.NewRepository(pool)
		if ho, err := fresh.HeadOfficeBranchID(ctx); err != nil || ho != kgnID {
			t.Fatalf("fresh Repository HeadOfficeBranchID after SetHeadOfficeBranch = %q (err=%v), want KGN (%q)", ho, err, kgnID)
		}
		var colID string
		if err := pool.QueryRow(ctx,
			`SELECT head_office_branch_id::text FROM gl_policy WHERE branch_id IS NULL`,
		).Scan(&colID); err != nil {
			t.Fatalf("read gl_policy.head_office_branch_id directly: %v", err)
		}
		if colID != kgnID {
			t.Fatalf("gl_policy.head_office_branch_id = %q, want KGN (%q)", colID, kgnID)
		}
	})

	t.Run("backfills_the_trt_branch_when_the_box_has_one", func(t *testing.T) {
		pool := freshMigratedDB(t, "fw108btrt")

		var trtID string
		if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE code = 'TRT'`).Scan(&trtID); err != nil {
			t.Fatalf("resolve TRT: %v", err)
		}
		var hoID string
		if err := pool.QueryRow(ctx,
			`SELECT head_office_branch_id::text FROM gl_policy WHERE branch_id IS NULL`,
		).Scan(&hoID); err != nil {
			t.Fatalf("read org head_office_branch_id: %v", err)
		}
		if hoID != trtID {
			t.Fatalf("org head_office_branch_id = %q, want the TRT branch (%q)", hoID, trtID)
		}
		var unfilled int
		if err := pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM gl_policy WHERE head_office_branch_id IS NULL`,
		).Scan(&unfilled); err != nil {
			t.Fatalf("count unfilled policy rows: %v", err)
		}
		if unfilled != 0 {
			t.Fatalf("gl_policy rows still without a head office branch = %d, want 0 (NOT NULL must hold)", unfilled)
		}
		fw10445SetupEntries(t, ctx, pool, trtID)

		// gl_recurring_templates learn their own branch column (nullable).
		if _, err := pool.Exec(ctx, `SELECT branch_id FROM gl_recurring_templates LIMIT 0`); err != nil {
			t.Fatalf("gl_recurring_templates.branch_id missing: %v", err)
		}
	})

	t.Run("backfills_the_earliest_branch_when_the_box_has_no_trt", func(t *testing.T) {
		pool := freshMigratedDBWithout(t, "fw108bnotrt", "0445_gl_branch_stamp.sql")

		// Make the box one whose branches carry no TRT code at all, and make the
		// former TRT row the NEWEST branch, so "earliest" can only mean KGN.
		if _, err := pool.Exec(ctx, `
			UPDATE locations SET code = 'XRT', created_at = created_at + interval '30 days'
			 WHERE code = 'TRT'
		`); err != nil {
			t.Fatalf("retire the TRT code: %v", err)
		}
		body, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", "0445_gl_branch_stamp.sql"))
		if err != nil {
			t.Fatalf("read migration 0445: %v", err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			t.Fatalf("apply migration 0445: %v", err)
		}

		var earliestID string
		if err := pool.QueryRow(ctx, `
			SELECT id::text FROM locations WHERE loc_type = 'BRANCH'
			 ORDER BY created_at, id LIMIT 1
		`).Scan(&earliestID); err != nil {
			t.Fatalf("resolve earliest branch: %v", err)
		}
		var hoID string
		if err := pool.QueryRow(ctx,
			`SELECT head_office_branch_id::text FROM gl_policy WHERE branch_id IS NULL`,
		).Scan(&hoID); err != nil {
			t.Fatalf("read org head_office_branch_id: %v", err)
		}
		if hoID != earliestID {
			t.Fatalf("org head_office_branch_id = %q, want the earliest branch (%q) on a box without TRT", hoID, earliestID)
		}
		fw10445SetupEntries(t, ctx, pool, earliestID)
	})

	t.Run("a_per_branch_policy_row_carries_the_head_office_branch", func(t *testing.T) {
		pool := freshMigratedDB(t, "fw108bje")
		repo := gl.NewRepository(pool)

		var trtID, orgHO string
		if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE code = 'TRT'`).Scan(&trtID); err != nil {
			t.Fatalf("resolve TRT: %v", err)
		}
		if err := pool.QueryRow(ctx,
			`SELECT head_office_branch_id::text FROM gl_policy WHERE branch_id IS NULL`,
		).Scan(&orgHO); err != nil {
			t.Fatalf("read org head_office_branch_id: %v", err)
		}
		if err := repo.SetJEThreshold(ctx, trtID, 100000); err != nil {
			t.Fatalf("SetJEThreshold on a migrated box: %v", err)
		}
		var branchHO string
		if err := pool.QueryRow(ctx,
			`SELECT head_office_branch_id::text FROM gl_policy WHERE branch_id = $1::uuid`, trtID,
		).Scan(&branchHO); err != nil {
			t.Fatalf("read branch policy head_office_branch_id: %v", err)
		}
		if branchHO != orgHO {
			t.Fatalf("branch gl_policy row head_office_branch_id = %q, want the org value (%q)", branchHO, orgHO)
		}
	})
}
