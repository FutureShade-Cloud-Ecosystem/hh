-- 0445: FW1-08b part 1 (FROM-PRINCIPAL 162, ruling 82.3): the head office branch becomes a real
-- gl_policy column, recurring templates learn their own branch, and the 0056/0221 setup entries'
-- journal lines take the head office branch, so a from-zero box has no NULL branch line left
-- outside the bankrec paths (their NOT NULL CHECK is 0446, after FW1-05).
--
-- NO DEFAULT on head_office_branch_id: the Trenton (TRT) branch uuid is only fixed on a from-zero
-- box (0003), so a DEFAULT would stamp a wrong id on any box with history. The backfill below
-- fills the org default row (branch_id IS NULL, the row the read and the Controller's
-- SetHeadOfficeBranch use) instead:
--   * the branch whose code is TRT when the box has one (ruling 82.3);
--   * else the earliest branch (created_at, then id, so the answer is stable), which is what an
--     existing dealer box without a TRT code gets.
-- A box with no branch rows at all keeps the column NULL on the org default row: the read
-- (gl.Repository.HeadOfficeBranchID) falls back to Trenton (TRT) by code
-- (DefaultHeadOfficeBranchID, ruling 82.3), so such a box migrates cleanly, and the NOT NULL
-- below is skipped rather than failing the migration.
-- NOT NULL is set only when every gl_policy row is filled by the backfill; on a box that still
-- carries per-branch policy rows the column stays nullable (the per-branch writer fills the
-- column from the org row, so the setting never reads as set where it is not).

ALTER TABLE gl_policy ADD COLUMN head_office_branch_id UUID REFERENCES locations(id);

UPDATE gl_policy p
   SET head_office_branch_id = (
     SELECT c.id
       FROM (
         (SELECT id, 0 AS pri FROM locations WHERE loc_type = 'BRANCH' AND code = 'TRT')
         UNION ALL
         (SELECT id, 1 AS pri FROM locations
           WHERE loc_type = 'BRANCH' AND code IS DISTINCT FROM 'TRT'
           ORDER BY created_at, id LIMIT 1)
       ) c
      ORDER BY c.pri
      LIMIT 1
   )
 WHERE p.branch_id IS NULL;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM gl_policy WHERE head_office_branch_id IS NULL) THEN
    ALTER TABLE gl_policy ALTER COLUMN head_office_branch_id SET NOT NULL;
  END IF;
END $$;

-- Recurring templates: nullable, no backfill. A template without a branch stamps the head office
-- branch when it runs (gl/journal.go RunRecurring).
ALTER TABLE gl_recurring_templates ADD COLUMN branch_id UUID REFERENCES locations(id);

-- The 0056 and 0221 setup entries (m2c1-demo-*, m2c1-retire-*) posted company level with no
-- branch at all; their lines take the head office branch the org row just got (ruling 82.3). A
-- box whose org row stayed NULL (no branches) backfills nothing here and stays as it was.
UPDATE gl_journal_lines l
   SET branch_id = p.head_office_branch_id
  FROM gl_journals j, gl_policy p
 WHERE l.journal_id = j.id
   AND p.branch_id IS NULL
   AND p.head_office_branch_id IS NOT NULL
   AND l.branch_id IS NULL
   AND (j.idempotency_key LIKE 'm2c1-demo-%' OR j.idempotency_key LIKE 'm2c1-retire-%');
