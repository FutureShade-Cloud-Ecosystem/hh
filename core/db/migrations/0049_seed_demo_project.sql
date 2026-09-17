-- 0049: seed one demo Project and link the UAT fixture order SO-UAT-0001 to it, so CP-03's
-- Projects/Jobs rollup is immediately demoable (scope reconciliation 2026-07-14, A-3).
--
-- Same fresh-migrate-safety discipline as 0046: migrations run BEFORE `make seed`, so on a
-- FRESH database the referenced customer/order do not exist yet — every statement is guarded
-- `WHERE EXISTS (…)` so it safely no-ops on a fresh migrate and applies once on the seeded
-- staging DB. Fixed UUID + idempotent (ON CONFLICT DO NOTHING) so repeated runs are stable.
--
-- Fixtures (all on the seeded "speaker-0004" customer c7…0001, matching 0046):
--   * project  PRJ-UAT-0001 "Maple Glen Patio" (the FEATURE_BRIEF's demo job)
--   * link order SO-UAT-0001 (07…a1, INVOICED, total 24860) → that project, so the rollup
--     shows 1 order, spend = its invoice total, 0 scheduled deliveries.

INSERT INTO projects (id, customer_id, name, job_address)
SELECT 'b8000000-0000-4000-9000-0000000000a1'::uuid,
       'c7000000-0000-4000-9000-000000000001'::uuid,
       'Maple Glen Patio',
       '0003 Placeholder Way'
WHERE EXISTS (SELECT 1 FROM customers WHERE id = 'c7000000-0000-4000-9000-000000000001')
ON CONFLICT (id) DO NOTHING;

UPDATE orders
   SET project_id = 'b8000000-0000-4000-9000-0000000000a1', updated_at = now()
 WHERE id = '07000000-0000-4000-9000-0000000000a1'
   AND EXISTS (SELECT 1 FROM projects WHERE id = 'b8000000-0000-4000-9000-0000000000a1');
