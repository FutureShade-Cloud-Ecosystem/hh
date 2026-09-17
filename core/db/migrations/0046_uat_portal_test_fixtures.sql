-- 0046: UAT portal test fixtures — a fresh contractor login + a returnable/reorderable order.
--
-- WHY a migration: the staging droplet's DB is NOT reseeded on deploy (its empty-DB guard keys
-- on existing DBS- products), so demo-seed edits never reach it — same reason 0043/0044 bootstrap
-- the platform-admin account here. These rows let the commander exercise the W7 portal flows
-- (CP-04 reorder, A-06 return request → staff approval) end-to-end against real data.
--
-- IMPORTANT ordering note: migrations run BEFORE `make seed`, so on a FRESH database the seed
-- customer/products/branch these fixtures reference do not exist yet. Every insert is therefore
-- guarded `WHERE EXISTS (… the seeded prerequisite …)`: on a fresh migrate it safely no-ops
-- (the seed later creates its own data), and on the already-seeded staging DB it applies once.
-- All idempotent (ON CONFLICT DO NOTHING).
--
-- Fixtures, all on the seeded "speaker-0004" customer (c7…0001):
--   * portal login  uat.tester@quintehardscapes.example  (account_admin, throwaway password)
--   * order SO-UAT-0001 (INVOICED, 2 lines) + pos_sale facet (makes it RETURNABLE); Brussels
--     line qty is well under on-hand + engine-priceable so it REORDERS clean.
--   * invoice INV-UAT-0001 (FINALIZED, open balance — an ACCOUNT sale).
-- Money is integer cents CAD; HST 13% split per line.

-- ── retail anchors (list prices) for the two fixture products. The demo seed only lists
-- 4 of 30 products, so the pricing resolver returns "no retail anchor" for most — which is
-- why reorder (and the live POS ring) flag PRICE_UNAVAILABLE. Seed the two we use so the
-- reorder happy-path reprices cleanly. Guarded on product existence (fresh-migrate-safe). ──
INSERT INTO pricing_list_prices (product_id, list_price_cents, price_uom)
SELECT v.pid, v.cents, v.uom FROM (VALUES
  ('a7000000-0000-4000-9000-000000000005'::uuid, 300::bigint, 'SF'),
  ('a7000000-0000-4000-9000-000000000023'::uuid, 2500::bigint,'BAG')
) AS v(pid,cents,uom)
WHERE EXISTS (SELECT 1 FROM products p WHERE p.id = v.pid)
ON CONFLICT (product_id) DO NOTHING;

-- ── portal login (throwaway credential; rotate/disable after UAT) ──
INSERT INTO portal_users (id, customer_id, email, name, password_hash, role, status)
SELECT 'e7000000-0000-4000-9000-0000000000a1','c7000000-0000-4000-9000-000000000001',
       'uat.tester@quintehardscapes.example','Scrubbed Party 0008',
       '$2a$10$bz6oUfb8Z1p1Y7rnJsizpO6hi1dURBiyfOnws.Y.cXwCINnuMeqQK','account_admin','active'
WHERE EXISTS (SELECT 1 FROM customers WHERE id = 'c7000000-0000-4000-9000-000000000001')
ON CONFLICT (email) DO NOTHING;

-- ── order (Brussels Block 40 SF ≈ 164 PC base < on-hand; Gator sand 4 BAG). subtotal 22000 · tax 2860 · total 24860 ──
INSERT INTO orders (id, order_no, customer_id, branch_id, status, fulfillment, requested_date,
                    subtotal_cents, tax_cents, total_cents, created_by)
SELECT '07000000-0000-4000-9000-0000000000a1','SO-UAT-0001','c7000000-0000-4000-9000-000000000001',
       '5d1b3f6e-0000-4000-9000-000000000001','INVOICED','PICKUP', CURRENT_DATE,
       22000, 2860, 24860, 'd7000000-0000-4000-9000-000000000010'
WHERE EXISTS (SELECT 1 FROM customers WHERE id = 'c7000000-0000-4000-9000-000000000001')
  AND EXISTS (SELECT 1 FROM products  WHERE id = 'a7000000-0000-4000-9000-000000000005')
ON CONFLICT (order_no) DO NOTHING;

INSERT INTO order_lines (order_id, line_no, product_id, description, qty_entered, uom_entered,
                         qty_base, unit_price_cents, price_uom, line_total_cents, tax_cents)
SELECT v.order_id, v.line_no, v.product_id, v.description, v.qty_entered, v.uom_entered,
       v.qty_base, v.unit_price_cents, v.price_uom, v.line_total_cents, v.tax_cents
FROM (VALUES
  ('07000000-0000-4000-9000-0000000000a1'::uuid,1,'a7000000-0000-4000-9000-000000000005'::uuid,'Brussels Block paver',40::numeric,'SF',164::numeric,300::bigint,'SF',12000::bigint,1560::bigint),
  ('07000000-0000-4000-9000-0000000000a1'::uuid,2,'a7000000-0000-4000-9000-000000000023'::uuid,'Gator polymeric sand', 4::numeric,'BAG',  4::numeric,2500::bigint,'BAG',10000::bigint,1300::bigint)
) AS v(order_id,line_no,product_id,description,qty_entered,uom_entered,qty_base,unit_price_cents,price_uom,line_total_cents,tax_cents)
WHERE EXISTS (SELECT 1 FROM orders WHERE id = '07000000-0000-4000-9000-0000000000a1')
ON CONFLICT DO NOTHING;

-- ── invoice (FINALIZED, open balance = total; an ACCOUNT sale) ──
INSERT INTO invoices (id, invoice_no, customer_id, branch_id, status, issue_date, due_date,
                      subtotal_cents, tax_cents, total_cents, balance_cents, created_by)
SELECT '19000000-0000-4000-9000-0000000000a1','INV-UAT-0001','c7000000-0000-4000-9000-000000000001',
       '5d1b3f6e-0000-4000-9000-000000000001','FINALIZED', CURRENT_DATE, CURRENT_DATE + 30,
       22000, 2860, 24860, 24860, 'd7000000-0000-4000-9000-000000000010'
WHERE EXISTS (SELECT 1 FROM customers WHERE id = 'c7000000-0000-4000-9000-000000000001')
ON CONFLICT (invoice_no) DO NOTHING;

-- ── pos_sale facet — makes the order RETURNABLE (A-06 return-eligibility joins it).
-- ACCOUNT tender → payment_id NULL by design; status COMMITTED. ──
INSERT INTO pos_sale (id, idempotency_key, order_id, invoice_id, payment_id, branch_id,
                      till_session_id, sale_type, cash_sale, tender, tax_reason_code, status, created_by)
SELECT '50000000-0000-4000-9000-0000000000a1','5a1e0000-0000-4000-9000-0000000000a1',
       '07000000-0000-4000-9000-0000000000a1','19000000-0000-4000-9000-0000000000a1', NULL,
       '5d1b3f6e-0000-4000-9000-000000000001', NULL,
       'COLLECT_NOW', FALSE, 'ACCOUNT', 'STD_ON_HST_13', 'COMMITTED', 'd7000000-0000-4000-9000-000000000010'
WHERE EXISTS (SELECT 1 FROM orders WHERE id = '07000000-0000-4000-9000-0000000000a1')
ON CONFLICT (idempotency_key) DO NOTHING;
