-- 0055: SELF-CONTAINED portal demo fixtures for CP-03 (Projects/Jobs rollup) demonstrability
-- on STAGING, plus a live demonstration of the quoted-vs-actual VARIANCE fix (portal/projects.go).
--
-- WHY THIS MIGRATION EXISTS (the L8 demo-gap):
--   The staging deploy runs ONLY migrations (bin/migrate) — it does NOT run `cmd/seed`
--   (dibbits_seed.sql). The seed is what creates customer c7…0001 "speaker-0004", so on
--   staging that customer never exists. Because 0046 (portal user uat.tester@…) and 0049 (demo
--   project + order link) are BOTH guarded `WHERE EXISTS (… customers c7…0001)`, they silently
--   no-op on staging: no portal user, no project, no orders → the Projects rollup is not
--   demonstrable and the audit's demo credential (mark@…, a seed-only user) does not exist.
--
--   This migration is therefore fully SELF-CONTAINED: it creates its OWN customer, portal user,
--   project, quote, orders and invoices. The ONLY external dependency is branch/location
--   5d1b3f6e-…-001 (Trenton), which is created by migration 0003 (guaranteed present on staging).
--
-- DEMO CREDENTIAL (documented — this is the account to log in with):
--   email:    portal.demo@dibbits.example
--   password: DibbitsDemo!2026
--   The password_hash below is bcrypt(cost 10) of exactly that password; role account_admin
--   grants ViewBilling, which the CP-03 routes require. ON CONFLICT (email) DO UPDATE re-asserts
--   the hash/role/status/customer on every run, so the credential is guaranteed correct even if
--   a stale row exists.
--
-- LIVE demonstration after deploy:
--   POST /api/portal/v1/login {"email":"portal.demo@dibbits.example","password":"DibbitsDemo!2026"}
--   GET  /api/portal/v1/projects            → rollup lists "Riverside Walkway (Demo)"
--   GET  /api/portal/v1/projects/{id}        → spend 70000, quoted 20000, quoted_spend 20000,
--                                              variance 0 (on quote). The ad-hoc 50000 order
--                                              raises SPEND but not VARIANCE — the fix, live.
--
-- Idempotency: fixed UUIDs everywhere; every insert ON CONFLICT DO NOTHING except the portal
-- user (ON CONFLICT DO UPDATE, so the documented password always wins). Money is integer cents
-- CAD. Re-running is stable.

-- Fixed UUIDs for this demo island (df… = "Dibbits Fixture").
--   customer  df000000-0000-4000-9000-000000000001
--   portal usr df000000-0000-4000-9000-0000000000a1
--   project   df000000-0000-4000-9000-0000000000b1
--   quote     df000000-0000-4000-9000-0000000000c1
--   order Oq  df000000-0000-4000-9000-0000000000d1  (quote-originated, on quote)
--   order Ox  df000000-0000-4000-9000-0000000000d2  (ad-hoc, quote_id NULL)
--   invoice q df000000-0000-4000-9000-0000000000e1  (20000, FINALIZED)
--   invoice x df000000-0000-4000-9000-0000000000e2  (50000, FINALIZED)

-- ── demo customer (self-contained; does NOT depend on the seed) ──
INSERT INTO customers (id, account_no, name, customer_type, home_branch_id, payment_terms_days,
                       credit_limit_cents, billing_address)
VALUES ('df000000-0000-4000-9000-000000000001', 'CUST-DEMO-CP03', 'Scrubbed Party 0004',
        'CONTRACTOR', '5d1b3f6e-0000-4000-9000-000000000001', 30, 5000000,
        '{"city":"Trenton","province":"ON","country":"CA"}')
ON CONFLICT (id) DO NOTHING;

-- ── portal login (DOCUMENTED credential; password DibbitsDemo!2026, bcrypt cost 10) ──
-- ON CONFLICT DO UPDATE so the documented password/role/status/customer are re-asserted on
-- every run — the login is guaranteed to work even over a pre-existing/stale row.
INSERT INTO portal_users (id, customer_id, email, name, password_hash, role, status)
VALUES ('df000000-0000-4000-9000-0000000000a1', 'df000000-0000-4000-9000-000000000001',
        'portal.demo@dibbits.example', 'Scrubbed Party 0003',
        '$2a$10$YBqvGsdG2meQ80xdbOyk9eAl937n9xCKc1F.a/I9MNfccmlXGmU8q', 'account_admin', 'active')
ON CONFLICT (email) DO UPDATE
   SET customer_id   = EXCLUDED.customer_id,
       password_hash = EXCLUDED.password_hash,
       name          = EXCLUDED.name,
       role          = EXCLUDED.role,
       status        = EXCLUDED.status,
       updated_at    = now();

-- ── demo project (the CP-03 job the rollup renders) ──
INSERT INTO projects (id, customer_id, name, job_address)
VALUES ('df000000-0000-4000-9000-0000000000b1', 'df000000-0000-4000-9000-000000000001',
        'Riverside Walkway (Demo)', '0007 Placeholder Way')
ON CONFLICT (id) DO NOTHING;

-- ── quote (20000, ACCEPTED) backing the quoted order ──
INSERT INTO quotes (id, quote_no, customer_id, branch_id, status, valid_until,
                    subtotal_cents, tax_cents, total_cents)
VALUES ('df000000-0000-4000-9000-0000000000c1', 'Q-DEMO-CP03',
        'df000000-0000-4000-9000-000000000001', '5d1b3f6e-0000-4000-9000-000000000001',
        'ACCEPTED', CURRENT_DATE + 30, 20000, 0, 20000)
ON CONFLICT (id) DO NOTHING;

-- ── quoted order Oq (quote_id set) — the like-for-like quoted work, INVOICED, total 20000 ──
INSERT INTO orders (id, order_no, customer_id, branch_id, quote_id, status, fulfillment,
                    requested_date, subtotal_cents, tax_cents, total_cents, project_id)
VALUES ('df000000-0000-4000-9000-0000000000d1', 'SO-DEMO-CP03-Q',
        'df000000-0000-4000-9000-000000000001', '5d1b3f6e-0000-4000-9000-000000000001',
        'df000000-0000-4000-9000-0000000000c1', 'INVOICED', 'PICKUP', CURRENT_DATE,
        20000, 0, 20000, 'df000000-0000-4000-9000-0000000000b1')
ON CONFLICT (id) DO NOTHING;

-- ── ad-hoc order Ox (quote_id NULL) — pure add-on spend, INVOICED, total 50000. This is the
-- order that used to inflate the variance into a false 50000 overrun; post-fix it raises SPEND
-- but leaves VARIANCE at 0. ──
INSERT INTO orders (id, order_no, customer_id, branch_id, quote_id, status, fulfillment,
                    requested_date, subtotal_cents, tax_cents, total_cents, project_id)
VALUES ('df000000-0000-4000-9000-0000000000d2', 'SO-DEMO-CP03-X',
        'df000000-0000-4000-9000-000000000001', '5d1b3f6e-0000-4000-9000-000000000001',
        NULL, 'INVOICED', 'PICKUP', CURRENT_DATE,
        50000, 0, 50000, 'df000000-0000-4000-9000-0000000000b1')
ON CONFLICT (id) DO NOTHING;

-- ── invoices (both FINALIZED → money-bearing, counted in spend). 20000 on the quoted order,
-- 50000 on the ad-hoc order. spend = 70000; quoted-spend (quoted order only) = 20000. ──
INSERT INTO invoices (id, invoice_no, customer_id, order_id, branch_id, status, issue_date,
                      due_date, subtotal_cents, tax_cents, total_cents, balance_cents)
VALUES
  ('df000000-0000-4000-9000-0000000000e1', 'INV-DEMO-CP03-Q',
   'df000000-0000-4000-9000-000000000001', 'df000000-0000-4000-9000-0000000000d1',
   '5d1b3f6e-0000-4000-9000-000000000001', 'FINALIZED', CURRENT_DATE, CURRENT_DATE + 30,
   20000, 0, 20000, 20000),
  ('df000000-0000-4000-9000-0000000000e2', 'INV-DEMO-CP03-X',
   'df000000-0000-4000-9000-000000000001', 'df000000-0000-4000-9000-0000000000d2',
   '5d1b3f6e-0000-4000-9000-000000000001', 'FINALIZED', CURRENT_DATE, CURRENT_DATE + 30,
   50000, 0, 50000, 50000)
ON CONFLICT (id) DO NOTHING;
