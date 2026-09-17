-- 0024: SEED only the CATEGORY-INDEPENDENT pricing reference data — the TIER_DEFAULT CONTRACTOR rule,
-- which has NO category/product FK. ALL NUMBERS ARE PROVISIONAL (best-assumption) pending Scrubname0025 B. /
-- Scrubname0001 confirmation (D-031 gate). The ENGINE is real; the NUMBERS are tunable via admin CRUD / row
-- UPDATE post-confirmation (no migration edit). Trace: A-07 plan §5 step 2; ARCH_DELTA §1 (0024).
-- ON CONFLICT DO NOTHING → idempotent + reset-safe.
--
-- ZERO-TRUST + MIGRATE-BEFORE-SEED ORDERING (gate-critical correctness): migrations run BEFORE `make seed`,
-- so the AUTHORITATIVE catalog product_categories + products do NOT exist yet at this point. This migration
-- therefore MUST NOT bootstrap its own product_categories — doing so created an ORPHAN parallel category set
-- (PAVERS/RET_WALLS/STEPS_CAPS/AGGREGATES/SAND_BASE/ACCESSORIES) whose codes never matched the real catalog
-- codes (PAVER/WALL/SLABSTEP/AGGREGATE/ACCESSORY); ON CONFLICT (code) did not dedupe, so the live DB grew a
-- second category set that NO product belonged to, and the category policy + TIER_CATEGORY rules keyed to it
-- never matched a product. ALL CATEGORY/PRODUCT-DEPENDENT pricing reference data — the pricing_category_policy
-- rows, the TIER_CATEGORY rules, and the pricing_list_prices retail anchors — now seeds in
-- cmd/seed/dibbits_seed.sql AFTER the catalog categories + products are inserted, keyed to the REAL catalog
-- category codes. This migration keeps ONLY the category-independent TIER_DEFAULT rule.

-- Starter tier rule. RETAIL = list (no rule). CONTRACTOR shown (the dominant ~99% path); BUILDER/GOVERNMENT
-- analogous, unseeded at gate. PROVISIONAL discount depth. No category/product FK, so it is safe to seed at
-- migrate time (the only pricing reference row that is category-independent).
INSERT INTO pricing_rules (scope, customer_type, calc_type, discount_bps, price_uom, priority, detail_text)
VALUES ('TIER_DEFAULT','CONTRACTOR','PCT_OFF_LIST',1500,'EA',10,'Contractor — default: 15% off')  -- PROVISIONAL
ON CONFLICT DO NOTHING;
