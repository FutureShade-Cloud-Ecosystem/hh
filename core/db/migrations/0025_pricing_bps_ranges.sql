-- 0025: defense-in-depth bps range CHECKs on the rule + override calc-value columns.
-- Owner module: internal/pricing. Trace: A-07b code-review finding #1 (real-money admin surface).
--
-- WHY: discount_bps / markup_bps were unbounded INTEGERs. The service layer now rejects values
-- outside 0..10000 with a 422 (validateRule / validateOverride, mirroring UpsertCategoryPolicy's
-- 0..10000 guard), but a stray write that bypasses the service (a raw INSERT, a future code path)
-- could still land a 15000-bps "150% off" rule and drive the resolver to a NEGATIVE price
-- (applyPctOff(base, 15000) = base × (10000-15000)/10000 < 0). These CHECKs make the DB itself
-- refuse an out-of-range basis-point value — the same belt-and-suspenders posture as 0023's
-- pricing_category_policy (margin_*_bps BETWEEN 0 AND 10000).
--
-- NULL is allowed (the column is only populated for the matching calc_type — exactly-one-value is
-- already enforced by the 0023 CHECKs); these constraints only bound the NON-NULL case.
-- Idempotent-friendly: a fresh `make reset` applies 0023..0025 in order, so the columns exist.

ALTER TABLE pricing_rules
  ADD CONSTRAINT pricing_rules_discount_bps_range
    CHECK (discount_bps IS NULL OR discount_bps BETWEEN 0 AND 10000),
  ADD CONSTRAINT pricing_rules_markup_bps_range
    CHECK (markup_bps IS NULL OR markup_bps BETWEEN 0 AND 10000);

ALTER TABLE customer_category_overrides
  ADD CONSTRAINT cust_cat_overrides_discount_bps_range
    CHECK (discount_bps IS NULL OR discount_bps BETWEEN 0 AND 10000),
  ADD CONSTRAINT cust_cat_overrides_markup_bps_range
    CHECK (markup_bps IS NULL OR markup_bps BETWEEN 0 AND 10000);
