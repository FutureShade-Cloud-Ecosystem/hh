-- 0059: DIB-159 ORDER-HISTORY FEATURE — one DEMO order with a realistic, multi-step
-- recorded status history so GET /api/v1/orders/{demoId}/history returns a multi-row
-- from→to timeline that renders the feature end-to-end.
--
-- WHY THIS EXISTS (the DEMO-GAP, aka DIB-191 lifecycle scope gap)
-- ─────────────────────────────────────────────────────────────────────────────────────
-- The order-history read (order/history.go) reconstructs a status timeline from the
-- append-only ORDER_STATUS_CHANGED audit trail. That read side is shipped and correct.
-- What is NOT shipped is a PRODUCTION lifecycle DRIVER: no deployed workflow calls the
-- SetStatus choke-point (order/service.go), so in production an order only ever gets its
-- CREATION recorded (""→CONFIRMED/DRAFT) and never transitions again. With at most one
-- recorded edge, the timeline cannot DEMONSTRATE multi-transition history.
--
-- This migration makes the FEATURE demonstrable WITHOUT faking a lifecycle in production
-- code: it seeds ONE clearly-labeled DEMO order and writes a hand-authored, realistic
-- sequence of ORDER_STATUS_CHANGED audit rows (DRAFT→CONFIRMED→PICKING→READY→FULFILLED→
-- INVOICED→CLOSED) with strictly-increasing occurred_at and real staff actors, using the
-- SAME audit_log shape the SetStatus choke-point would emit (changes = {old_status,
-- new_status, reason}). The history read then returns a 7-row from→to timeline for this
-- order — the exact surface a reviewer sees.
--
-- SCOPE / HONESTY: this is a DEMO FIXTURE, not a production lifecycle. DIB-191 (no
-- production status-transition DRIVER routing through SetStatus) remains the real gap;
-- SetStatus stays the ready choke-point awaiting that driver. We deliberately do NOT add a
-- fake production SetStatus caller — the ONLY thing seeded here is demo history data.
--
-- FRESH-MIGRATE SAFETY (same discipline as 0046/0049): migrations run BEFORE `make seed`,
-- so on a FRESH database the referenced customer/branch/actor rows do not exist yet. Every
-- statement is guarded `WHERE EXISTS (…)` so it safely NO-OPS on a fresh migrate and
-- applies exactly once on the seeded staging DB. Fixed UUIDs + ON CONFLICT DO NOTHING make
-- repeated runs stable (idempotent).

-- ── 1. the DEMO order (clearly labeled; guarded on the seeded Scrubname0021 customer + TRT branch) ──
-- order_no is prefixed SO-DEMO-HIST so it is unmistakably a demo fixture in any list view.
INSERT INTO orders (id, order_no, customer_id, branch_id, status, fulfillment, requested_date,
                    subtotal_cents, tax_cents, total_cents, created_by, metadata)
SELECT '07de0000-0000-4000-9000-000000000059'::uuid,
       'SO-DEMO-HIST-0001',
       'df000000-0000-4000-9000-000000000001'::uuid,  -- Scrubbed Party 0004 (CUST-DEMO-CP03, seeded by 0055)
       '5d1b3f6e-0000-4000-9000-000000000001'::uuid,  -- Trenton branch (seeded, 0003)
       'CLOSED',                                       -- terminal state matching the seeded history below
       'DELIVERY', CURRENT_DATE,
       48000, 6240, 54240,
       'd7000000-0000-4000-9000-000000000010'::uuid,   -- colton@futurebuild.ai (platform admin, seeded 0044)
       '{"demo":true,"purpose":"DIB-159 order-history feature demo","note":"status history is a demo fixture (migration 0059); no production lifecycle driver — see DIB-191"}'::jsonb
WHERE EXISTS (SELECT 1 FROM customers WHERE id = 'df000000-0000-4000-9000-000000000001')
  AND EXISTS (SELECT 1 FROM locations WHERE id = '5d1b3f6e-0000-4000-9000-000000000001')
ON CONFLICT (id) DO NOTHING;

-- one line so the order renders as a real order in the detail view too (optional but realistic).
INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered,
                         qty_base, unit_price_cents, price_uom, line_total_cents)
SELECT '07de0000-0000-4000-9000-000000000059'::uuid, 1,
       NULL, 'DEMO — Brussels Block paver (order-history fixture)',
       160::numeric, 'SF', 160::numeric, 300::bigint, 'SF', 48000::bigint
WHERE EXISTS (SELECT 1 FROM orders WHERE id = '07de0000-0000-4000-9000-000000000059')
ON CONFLICT DO NOTHING;

-- ── 2. the recorded status history (ORDER_STATUS_CHANGED audit rows) ────────────────────
-- These are the SAME shape the SetStatus choke-point emits: action ORDER_STATUS_CHANGED,
-- entity_type 'order', entity_id = the demo order, actor_id = the resolving staff member,
-- changes = {old_status,new_status,reason}. occurred_at (created_at) strictly increases so
-- the read's oldest→newest ordering yields the full lifecycle. Fixed audit ids keep the
-- seed idempotent (ON CONFLICT DO NOTHING on the audit_log PK).
--
-- Genesis edge (""→DRAFT) mirrors how the choke-point records order creation; the remaining
-- six edges are the spec lifecycle DRAFT→CONFIRMED→PICKING→READY→FULFILLED→INVOICED→CLOSED.
-- Actor is the one staff user that exists on a MIGRATIONS-ONLY database (staging runs
-- migrations, not `make seed`): colton@futurebuild.ai (d7…010, platform admin, seeded 0044).
-- Using it for every edge keeps the read's ActorNames LEFT JOIN resolving a real display name
-- on staging (the earlier cmd/seed-only actor ids d7…003/005 do not exist there).
INSERT INTO audit_log (id, action, entity_type, entity_id, actor_id, branch_id, changes, created_at)
SELECT v.id::uuid, 'ORDER_STATUS_CHANGED', 'order',
       '07de0000-0000-4000-9000-000000000059'::uuid,
       v.actor_id::uuid,
       '5d1b3f6e-0000-4000-9000-000000000001'::uuid,
       v.changes::jsonb,
       v.occurred_at::timestamptz
FROM (VALUES
  ('a0590000-0000-4000-9000-000000000001','d7000000-0000-4000-9000-000000000010',
     '{"old_status":"","new_status":"DRAFT","reason":"quote_accepted"}',        '2026-06-01 09:05:00+00'),
  ('a0590000-0000-4000-9000-000000000002','d7000000-0000-4000-9000-000000000010',
     '{"old_status":"DRAFT","new_status":"CONFIRMED","reason":"deposit_received"}','2026-06-01 09:40:00+00'),
  ('a0590000-0000-4000-9000-000000000003','d7000000-0000-4000-9000-000000000010',
     '{"old_status":"CONFIRMED","new_status":"PICKING","reason":"picker_started"}','2026-06-02 08:15:00+00'),
  ('a0590000-0000-4000-9000-000000000004','d7000000-0000-4000-9000-000000000010',
     '{"old_status":"PICKING","new_status":"READY","reason":"picked_and_staged"}','2026-06-02 11:30:00+00'),
  ('a0590000-0000-4000-9000-000000000005','d7000000-0000-4000-9000-000000000010',
     '{"old_status":"READY","new_status":"FULFILLED","reason":"delivered_on_site"}','2026-06-03 14:20:00+00'),
  ('a0590000-0000-4000-9000-000000000006','d7000000-0000-4000-9000-000000000010',
     '{"old_status":"FULFILLED","new_status":"INVOICED","reason":"invoice_issued"}','2026-06-03 16:00:00+00'),
  ('a0590000-0000-4000-9000-000000000007','d7000000-0000-4000-9000-000000000010',
     '{"old_status":"INVOICED","new_status":"CLOSED","reason":"paid_in_full"}',    '2026-06-10 10:00:00+00')
) AS v(id, actor_id, changes, occurred_at)
WHERE EXISTS (SELECT 1 FROM orders WHERE id = '07de0000-0000-4000-9000-000000000059')
ON CONFLICT (id) DO NOTHING;
