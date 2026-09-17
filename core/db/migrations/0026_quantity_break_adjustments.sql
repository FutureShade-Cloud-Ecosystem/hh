-- 0026: quantity breaks become ADJUSTMENTS on the tier-resolved base (A-07 stacking fix + editor).
-- Owner module: internal/pricing. Trace: A-07 stacking fix — bulk breaks STACK, not compete.
--
-- WHY: a quantity break no longer COMPETES at a precedence level (it used to REPLACE the customer's
-- tier/category discount). It now applies ON TOP of the resolved base price — exactly like the
-- partial-skid surcharge already did. So the value_type vocabulary becomes a clean ADJUSTMENT set:
--   PCT_OFF / ABS_OFF   — a discount OFF the resolved base (percent / flat cents)
--   SURCHARGE_PCT / SURCHARGE_ABS — an uplift ON the resolved base (percent / flat cents)
-- The old ABSOLUTE (a replacement price) and PCT_OFF_LIST (off the LIST anchor) value types are gone:
-- a break is an adjustment, never a base price. ABS_OFF (a flat amount OFF) replaces ABSOLUTE; PCT_OFF
-- (percent off the base) replaces PCT_OFF_LIST. PROVISIONAL semantics + values pending Scrubname0025 B.
--
-- ZERO-TRUST: still pricing-OWNED tables only (quantity_break_headers / quantity_break_items). No
-- catalog ALTER. Greenfield + `make reset` re-runs 0023..0026 in order, so there is no live break data
-- to migrate (the seed inserts the first break rows under the new vocabulary).

-- Optimistic-lock version on the header (mirrors pricing_rules.version) — the editor saves the whole
-- header+items as a unit under a version guard.
ALTER TABLE quantity_break_headers
  ADD COLUMN version INTEGER NOT NULL DEFAULT 1;

-- ABS_OFF: a positive flat amount OFF the resolved base (in cents).
ALTER TABLE quantity_break_items
  ADD COLUMN discount_cents BIGINT;

-- Repoint value_type to the ADJUSTMENT vocabulary.
ALTER TABLE quantity_break_items DROP CONSTRAINT quantity_break_items_value_type_check;
ALTER TABLE quantity_break_items
  ADD CONSTRAINT quantity_break_items_value_type_check
    CHECK (value_type IN ('PCT_OFF','ABS_OFF','SURCHARGE_PCT','SURCHARGE_ABS'));

-- Exactly-one calc value matching the (new) value_type. price_cents is dropped below, so the old
-- ABSOLUTE replacement-price column is no longer part of the invariant.
ALTER TABLE quantity_break_items DROP CONSTRAINT quantity_break_items_check;
ALTER TABLE quantity_break_items
  ADD CONSTRAINT quantity_break_items_value_check CHECK (
       (value_type='PCT_OFF'       AND discount_bps    IS NOT NULL AND discount_cents IS NULL AND surcharge_bps IS NULL AND surcharge_cents IS NULL)
    OR (value_type='ABS_OFF'       AND discount_cents  IS NOT NULL AND discount_bps   IS NULL AND surcharge_bps IS NULL AND surcharge_cents IS NULL)
    OR (value_type='SURCHARGE_PCT' AND surcharge_bps   IS NOT NULL AND discount_bps   IS NULL AND discount_cents IS NULL AND surcharge_cents IS NULL)
    OR (value_type='SURCHARGE_ABS' AND surcharge_cents IS NOT NULL AND discount_bps   IS NULL AND discount_cents IS NULL AND surcharge_bps   IS NULL) );

-- Drop the now-unused replacement-price column (a break is an adjustment, not a set price).
ALTER TABLE quantity_break_items DROP COLUMN price_cents;

-- Defense-in-depth ranges (mirrors 0025): a stray write can never land a 150%-off / 150%-surcharge
-- break or a negative flat adjustment, even if it bypasses the service-layer validators.
ALTER TABLE quantity_break_items
  ADD CONSTRAINT qbreak_items_discount_bps_range
    CHECK (discount_bps IS NULL OR discount_bps BETWEEN 0 AND 10000),
  ADD CONSTRAINT qbreak_items_surcharge_bps_range
    CHECK (surcharge_bps IS NULL OR surcharge_bps BETWEEN 0 AND 10000),
  ADD CONSTRAINT qbreak_items_discount_cents_nonneg
    CHECK (discount_cents IS NULL OR discount_cents >= 0),
  ADD CONSTRAINT qbreak_items_surcharge_cents_nonneg
    CHECK (surcharge_cents IS NULL OR surcharge_cents >= 0);
