-- 0015: A-20 RBAC — seed Dibbits's 13 staff roles across 3 tiers (W1).
-- Source: Scrubname0030's roles doc (D-014, ingested 2026-06-14). Production config (real roles).
--   tier  = authority level (1 highest, 3 front-line) — column from 0004.
--   scope = GLOBAL (operates across both branches) vs BRANCH (scoped via user_branch_grants).
-- Scrubname0006 ruling 2026-06-16: Scrubname0012 Scrubname0019, Scrubname0010 Scrubname0007, HR, and
-- Payables/Receivables run CENTRALLY across both yards (GLOBAL); the five operations
-- managers and four front-line roles are per-branch (BRANCH).
-- Spec: dibbits/.agents/handoff/features/a20-internal-permissions/ (adapted onto locked schema).

ALTER TABLE roles ADD COLUMN scope TEXT NOT NULL DEFAULT 'BRANCH'
  CHECK (scope IN ('GLOBAL','BRANCH'));

INSERT INTO roles (id, code, name, tier, scope) VALUES
  -- Tier 1 — leadership / org-wide (GLOBAL)
  ('e7000000-0000-4000-9000-000000000001','GENERAL_MANAGER',     'Scrubname0012 Scrubname0019',      1,'GLOBAL'),
  ('e7000000-0000-4000-9000-000000000002','FINANCIAL_CONTROLLER','Scrubname0010 Scrubname0007', 1,'GLOBAL'),
  ('e7000000-0000-4000-9000-000000000003','HR',                  'HR',                   1,'GLOBAL'),
  -- Tier 2 — managers (per-branch) + central finance ops (GLOBAL)
  ('e7000000-0000-4000-9000-000000000004','BRANCH_MANAGER',      'Branch Scrubname0019',       2,'BRANCH'),
  ('e7000000-0000-4000-9000-000000000005','PROCUREMENT_MANAGER', 'Procurement Scrubname0019',  2,'BRANCH'),
  ('e7000000-0000-4000-9000-000000000006','SALES_MANAGER',       'Scrubname0027 Scrubname0019',        2,'BRANCH'),
  ('e7000000-0000-4000-9000-000000000007','YARD_MANAGER',        'Yard Scrubname0019',         2,'BRANCH'),
  ('e7000000-0000-4000-9000-000000000008','LOGISTICS_MANAGER',   'Logistics Scrubname0019',    2,'BRANCH'),
  ('e7000000-0000-4000-9000-000000000009','PAYABLES_RECEIVABLES','Payables/Receivables', 2,'GLOBAL'),
  -- Tier 3 — front-line / hourly (per-branch)
  ('e7000000-0000-4000-9000-000000000010','INSIDE_SALES',        'Scrubname0014 Scrubname0027',         3,'BRANCH'),
  ('e7000000-0000-4000-9000-000000000011','OUTSIDE_SALES',       'Outside Scrubname0027',        3,'BRANCH'),
  ('e7000000-0000-4000-9000-000000000012','YARD_TEAM',           'Yard Team',            3,'BRANCH'),
  ('e7000000-0000-4000-9000-000000000013','DRIVERS',             'Drivers',              3,'BRANCH')
ON CONFLICT (code) DO NOTHING;
