-- 0067: DIB-260 (Bulk as a first-class pricing category) + DIB-252 (enable AGGREGATE margin
-- enforcement). Both are client-directed pricing-policy changes from the W7/W8 UAT feedback
-- burn-down (DIB-169); the GM asked for Bulk to be a real category (front-desk bulk sales:
-- sand / base / aggregate by the ton) and for the Aggregates floor to be LOCKED, not audit-only.
--
-- SUPERSEDES the note in cmd/seed/dibbits_seed.sql ("bulk sand/base fold into AGGREGATE for now;
-- do NOT invent a category here") — that was the interim stance; DIB-260 is the decision to split.
--
-- MIGRATE-BEFORE-SEED / FRESH-DB DISCIPLINE (repeat lesson): every FK resolves by NATURAL KEY
-- (category code / sku) and every statement is guarded + idempotent, so this is safe on a FRESH
-- migrate (0001→0067, no cmd/seed) AND re-run, AND a later full cmd/seed is a no-op over it.
-- Money is integer cents CAD; margins are basis points (bps).

-- ── 1. BULK category (canonical catalog id/code so a later full seed ON CONFLICT is a no-op) ──
INSERT INTO product_categories (id, code, name)
VALUES ('ca700000-0000-4000-9000-000000000006', 'BULK', 'Bulk')
ON CONFLICT (code) DO NOTHING;

-- ── 2. BULK margin policy — GM's number for bulk/aggregates was 20 floor / 40 target (DIB-252).
-- ENFORCED (margin_enforcement_disabled = FALSE): a below-floor bulk line routes to approval, it is
-- never sold silently. FK → the BULK category by code so this works whatever id created it.
INSERT INTO pricing_category_policy (category_id, margin_target_bps, margin_floor_bps, margin_enforcement_disabled)
SELECT id, 4000, 2000, FALSE FROM product_categories WHERE code = 'BULK'
ON CONFLICT (category_id) DO NOTHING;

-- ── 3. A default BULK pricing rule so bulk lines resolve against a real tier (mirrors the PAVER /
-- ACCESSORY provisional defaults in cmd/seed). Contractor tier, list price, lowest priority.
INSERT INTO pricing_rules (scope, customer_type, category_id, calc_type, discount_bps, price_uom, priority, detail_text)
SELECT 'TIER_CATEGORY', 'CONTRACTOR', pc.id, 'PCT_OFF_LIST', 0, 'T', 100, 'PROVISIONAL — Bulk default (DIB-260); real breaks land with the GM worksheet'
  FROM product_categories pc WHERE pc.code = 'BULK'
ON CONFLICT DO NOTHING;

-- ── 4. Reassign the by-the-ton bulk aggregate products from AGGREGATE → BULK. Scope tightly to the
-- three bulk SKUs the GM means (sand / clear stone / screenings sold by the ton), matched by SKU so
-- we never touch a bagged/packaged product. Only moves rows still on AGGREGATE (or uncategorised) →
-- re-run is a no-op, and a hand-made assignment is never clobbered.
UPDATE products p
   SET category_id = (SELECT id FROM product_categories WHERE code = 'BULK'),
       updated_at = now()
 WHERE p.sku IN ('DBS-AGG-SAND', 'DBS-AGG-34CLEAR', 'DBS-AGG-SCREEN', 'DBS-AGG-HPB')
   AND (
        p.category_id = (SELECT id FROM product_categories WHERE code = 'AGGREGATE')
        OR p.category_id IS NULL
   )
   AND EXISTS (SELECT 1 FROM product_categories WHERE code = 'BULK');

-- ── 5. DIB-252 — enable margin enforcement on AGGREGATE. It was seeded audit-only
-- (margin_enforcement_disabled = TRUE, "bulk overhead"); with true bulk now split into its own
-- enforced BULK category, the remaining AGGREGATE items should be LOCKED at their floor like every
-- other category. Only flips the row if it is currently disabled → idempotent, and a no-op on a
-- fresh migrate where the AGGREGATE policy may not exist yet (cmd/seed owns it).
UPDATE pricing_category_policy
   SET margin_enforcement_disabled = FALSE
 WHERE category_id = (SELECT id FROM product_categories WHERE code = 'AGGREGATE')
   AND margin_enforcement_disabled = TRUE;
