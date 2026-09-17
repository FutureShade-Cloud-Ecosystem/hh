-- 0065: D-PA-3 daily low-margin report — demo cost + committed-sale fixtures so the
-- auto-populated live-sales scan (/api/v1/pricing/reports/low-margin/scan) shows REAL rows.
--
-- WHY A MIGRATION: staging deploys by ./bin/migrate and NEVER ./bin/seed (scripts/staging_apply.sh),
-- and product_branch_costs is EMPTY there (all cost 0) → the scan would grade every line as
-- "no cost on file" and never show a real breach. This seeds the minimum to demonstrate a CRITICAL
-- and a WARN row (plus a HEALTHY line that must NOT appear), fully guarded + idempotent so it is safe
-- on a FRESH migrate (0001→0065, no seed) AND re-run.
--
-- MIGRATE-BEFORE-SEED / FRESH-DB DISCIPLINE (repeat lesson): every FK is resolved by NATURAL KEY
-- (sku / account_no / category code) or by a migration-created id, and every INSERT is guarded on its
-- prerequisites existing. On a fresh migrate the only priced paver that exists is "Unilock Brussels
-- Block" (DBS-UL-BRUSSELS, seeded by 0061 with its SF factor 4.10); the WALK-IN cash customer (0029)
-- and Trenton branch (0003) also exist by migration. NO cmd/seed-only id is referenced.
--
-- MARGIN MATH (the report grades the ACTUAL sold price vs current per-branch WAC, converted to the
-- line's price_uom the SAME way the resolver does — a float64 factor × roundCents, so the engine's
-- cost/SF is 717¢, not the 718¢ of exact arithmetic; consistency with the resolver is the point):
--   Brussels cost 175¢/PC × SF factor 4.10 (float64) = 717.4999… → roundCents → 717¢/SF.
--     850¢/SF  → margin ((850-717)*10000)/850  = 1564 bps (15.64%) < floor 2000  → CRITICAL
--     975¢/SF  → margin ((975-717)*10000)/975  = 2646 bps (26.46%) in [2000,3300) → WARN
--    1400¢/SF  → margin ((1400-717)*10000)/1400= 4878 bps (48.78%) ≥ target 3300 → HEALTHY (omitted)
--   (A full-seed PAVER policy of 3000/2000 classifies these three identically — robust either way.)
--
-- Money is integer cents CAD; header tax figures are illustrative (the report is pre-tax; M5 owns tax).

-- ── 1. PAVER category + margin policy so Brussels is graded against a real target/floor ──
-- On a fresh migrate product_categories + pricing_category_policy are EMPTY (the category-dependent
-- pricing reference data seeds in cmd/seed, which staging never runs). Bootstrap the PAVER category
-- with the canonical catalog id/code (matches cmd/seed/dibbits_seed.sql, so a later full seed is a
-- no-op) and its margin policy. ON CONFLICT keys never clobber an existing real row.
INSERT INTO product_categories (id, code, name)
VALUES ('ca700000-0000-4000-9000-000000000001', 'PAVER', 'Pavers')
ON CONFLICT (code) DO NOTHING;

-- Assign Brussels to PAVER (0061 left category_id NULL on a fresh migrate; resolve category by CODE so
-- this also works if a full seed created PAVER under a different id). Only touches an UNCATEGORISED row
-- → never clobbers a real assignment, and re-run is a no-op once set.
UPDATE products
   SET category_id = (SELECT id FROM product_categories WHERE code = 'PAVER'), updated_at = now()
 WHERE sku = 'DBS-UL-BRUSSELS'
   AND category_id IS NULL
   AND EXISTS (SELECT 1 FROM product_categories WHERE code = 'PAVER');

-- PAVER margin policy: target 33% / floor 20% (the live homework policy). FK → the PAVER category.
-- ON CONFLICT (category_id) DO NOTHING keeps an existing policy (e.g. a full seed's 3000/2000) intact.
INSERT INTO pricing_category_policy (category_id, margin_target_bps, margin_floor_bps, margin_enforcement_disabled)
SELECT id, 3300, 2000, FALSE FROM product_categories WHERE code = 'PAVER'
ON CONFLICT (category_id) DO NOTHING;

-- ── 2. Per-branch WAC cost rows (product_branch_costs) at Trenton ──
-- Brussels 175¢/PC (≈ $7.18/SF) is LOAD-BEARING for the demo. ON CONFLICT DO UPDATE so a re-run
-- refreshes the demo cost (staging's stale all-0 rows would otherwise render 100% margin).
INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents, updated_at)
SELECT p.id, '5d1b3f6e-0000-4000-9000-000000000001'::uuid, 175, now()
  FROM products p
 WHERE p.sku = 'DBS-UL-BRUSSELS'
   AND EXISTS (SELECT 1 FROM locations WHERE id = '5d1b3f6e-0000-4000-9000-000000000001')
ON CONFLICT (product_id, branch_id) DO UPDATE
  SET avg_unit_cost_cents = EXCLUDED.avg_unit_cost_cents, updated_at = now();

-- One more paver for cost VARIETY (Unilock Il Campo). Il Campo is cmd/seed-only, so this GUARDED
-- insert applies ONLY on a full-seed env and safely no-ops on a fresh migrate. Inert in the report
-- (no Il Campo sale is seeded — the product is absent on migrate-only staging); present so the cost
-- table is not single-product on a seeded env.
INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents, updated_at)
SELECT p.id, '5d1b3f6e-0000-4000-9000-000000000001'::uuid, 300, now()
  FROM products p
 WHERE p.sku = 'DBS-UL-ILCAMPO'
   AND EXISTS (SELECT 1 FROM locations WHERE id = '5d1b3f6e-0000-4000-9000-000000000001')
ON CONFLICT (product_id, branch_id) DO UPDATE
  SET avg_unit_cost_cents = EXCLUDED.avg_unit_cost_cents, updated_at = now();

-- ── 3. Committed sale A — a POS CASH ring (CRITICAL Brussels line) ──
-- Status stays CONFIRMED, exactly like a real POS ring (internal/pos/service.go inserts the order at
-- CONFIRMED and never advances orders.status). So this order is caught by the scan's committed
-- predicate ONLY via its COMMITTED pos_sale facet — proving the facet branch is load-bearing for cash
-- counter sales. created_at defaults to now() → inside the report's default 30-day window.
INSERT INTO orders (id, order_no, customer_id, branch_id, status, fulfillment, requested_date,
                    subtotal_cents, tax_cents, total_cents)
SELECT '0da30000-0000-4000-9000-000000000001', 'SO-DPA3-CRIT',
       (SELECT id FROM customers WHERE account_no = 'WALK-IN'),
       '5d1b3f6e-0000-4000-9000-000000000001', 'CONFIRMED', 'PICKUP', CURRENT_DATE,
       34000, 4420, 38420
 WHERE EXISTS (SELECT 1 FROM customers  WHERE account_no = 'WALK-IN')
   AND EXISTS (SELECT 1 FROM locations  WHERE id = '5d1b3f6e-0000-4000-9000-000000000001')
   AND EXISTS (SELECT 1 FROM products   WHERE sku = 'DBS-UL-BRUSSELS')
ON CONFLICT (order_no) DO NOTHING;

INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered,
                         qty_base, unit_price_cents, price_uom, line_total_cents)
SELECT '0da30000-0000-4000-9000-000000000001', 1,
       (SELECT id FROM products WHERE sku = 'DBS-UL-BRUSSELS'),
       'Brussels Block — manual discount below floor (D-PA-3 CRITICAL demo)',
       40, 'SF', 164, 850, 'SF', 34000
 WHERE EXISTS (SELECT 1 FROM orders   WHERE id  = '0da30000-0000-4000-9000-000000000001')
   AND EXISTS (SELECT 1 FROM products WHERE sku = 'DBS-UL-BRUSSELS')
ON CONFLICT (order_id, line_no) DO NOTHING;

-- COMMITTED cash pos_sale facet → the only thing that makes order A a "committed sale" for the scan.
INSERT INTO pos_sale (id, idempotency_key, order_id, branch_id, sale_type, cash_sale, tender,
                      tax_reason_code, status)
SELECT '0da30000-0000-4000-9000-0000000000a1', '0da3f000-0000-4000-9000-0000000000a1',
       '0da30000-0000-4000-9000-000000000001', '5d1b3f6e-0000-4000-9000-000000000001',
       'COLLECT_NOW', TRUE, 'CASH', 'STD_ON_HST_13', 'COMMITTED'
 WHERE EXISTS (SELECT 1 FROM orders WHERE id = '0da30000-0000-4000-9000-000000000001')
ON CONFLICT (idempotency_key) DO NOTHING;

-- ── 4. Committed sale B — an INVOICED order (WARN + HEALTHY Brussels lines) ──
-- INVOICED is in the status branch of the committed predicate, so this order needs no pos_sale facet.
-- Line 1 (975¢/SF) is a WARN; line 2 (1400¢/SF) is HEALTHY and must NOT appear — proving the filter.
INSERT INTO orders (id, order_no, customer_id, branch_id, status, fulfillment, requested_date,
                    subtotal_cents, tax_cents, total_cents)
SELECT '0da30000-0000-4000-9000-000000000002', 'SO-DPA3-WARN',
       (SELECT id FROM customers WHERE account_no = 'WALK-IN'),
       '5d1b3f6e-0000-4000-9000-000000000001', 'INVOICED', 'PICKUP', CURRENT_DATE,
       95000, 12350, 107350
 WHERE EXISTS (SELECT 1 FROM customers  WHERE account_no = 'WALK-IN')
   AND EXISTS (SELECT 1 FROM locations  WHERE id = '5d1b3f6e-0000-4000-9000-000000000001')
   AND EXISTS (SELECT 1 FROM products   WHERE sku = 'DBS-UL-BRUSSELS')
ON CONFLICT (order_no) DO NOTHING;

INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered,
                         qty_base, unit_price_cents, price_uom, line_total_cents)
SELECT v.order_id, v.line_no,
       (SELECT id FROM products WHERE sku = 'DBS-UL-BRUSSELS'),
       v.description, v.qty_entered, v.uom_entered, v.qty_base, v.unit_price_cents, v.price_uom, v.line_total_cents
FROM (VALUES
  ('0da30000-0000-4000-9000-000000000002'::uuid, 1, 'Brussels Block — below-target sale (D-PA-3 WARN demo)',    40::numeric, 'SF', 164::numeric,  975::bigint, 'SF', 39000::bigint),
  ('0da30000-0000-4000-9000-000000000002'::uuid, 2, 'Brussels Block — healthy sale (D-PA-3: must NOT appear)',  40::numeric, 'SF', 164::numeric, 1400::bigint, 'SF', 56000::bigint)
) AS v(order_id, line_no, description, qty_entered, uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents)
WHERE EXISTS (SELECT 1 FROM orders   WHERE id  = '0da30000-0000-4000-9000-000000000002')
  AND EXISTS (SELECT 1 FROM products WHERE sku = 'DBS-UL-BRUSSELS')
ON CONFLICT (order_id, line_no) DO NOTHING;
