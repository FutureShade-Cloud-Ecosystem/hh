-- 0111: DIB-70 — seasonal promotions + clearance.
--
-- WHAT THIS ADDS AND WHY IT IS A NEW TABLE RATHER THAN A pricing_rules SCOPE
-- --------------------------------------------------------------------------
-- pricing_rules (0023) answers "what does THIS customer pay for THIS thing, indefinitely". Its
-- rows are contract terms: they carry customer_id / customer_type and they do not expire in
-- practice (valid_from/valid_to exist but every seeded row leaves them NULL). A promotion is the
-- opposite shape on every axis that matters:
--
--   * it is DATE-BOUNDED by construction — a promo with no end date is a price change, not a promo,
--     so starts_at/ends_at are NOT NULL here where pricing_rules leaves them nullable;
--   * it is CUSTOMER-BLIND — "20% off all pavers in June" applies to whoever walks in, so there is
--     no customer_id / customer_type column to mis-set;
--   * it is BRANCH-SCOPABLE — a yard clearing its own overstock runs the promo at ONE location;
--   * it must be VISIBLE as a promotion in the breakdown. Folding it into pricing_rules would make
--     Breakdown.Source say TIER_CATEGORY for a line the customer was told was on sale.
--
-- Overloading pricing_rules would therefore have meant nullable-date semantics that differ by
-- scope, a customer_id that must be NULL for one scope and NOT NULL for another, and a Source enum
-- that lies. A separate table costs one extra SELECT on the non-contract path and keeps both
-- CHECKs honest.

-- ── 1 · promotions ────────────────────────────────────────────────────────────────────────────
CREATE TABLE pricing_promotions (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code        CITEXT NOT NULL UNIQUE,
  name        TEXT NOT NULL,
  -- SCOPE. product_id and category_id are the target dimensions, most-specific-first at resolve
  -- time (product > category > everything). BOTH NULL is legal and means a store-wide promo —
  -- unlike quantity_break_headers, which CHECKs that one is set, because "everything on sale" is a
  -- real seasonal event and refusing to express it would push the user into authoring one promo
  -- per category and then forgetting one.
  product_id  UUID REFERENCES products(id) ON DELETE CASCADE,
  category_id UUID REFERENCES product_categories(id) ON DELETE CASCADE,
  -- BRANCH SCOPE. NULL = every branch. locations(id) is the branch table (pricing_rules.branch_id
  -- points at the same place).
  branch_id   UUID REFERENCES locations(id) ON DELETE CASCADE,
  -- product XOR category. A promo pinned to BOTH would be unreachable whenever the product is not
  -- in that category and redundant when it is.
  CHECK (product_id IS NULL OR category_id IS NULL),

  -- DATE BOUNDS. Half-open [starts_at, ends_at): a promo that ends "June 30" ends at 2026-07-01
  -- 00:00, and the resolver's boundary test is `now >= starts_at AND now < ends_at` with no
  -- inclusive/exclusive ambiguity to get wrong at midnight.
  starts_at   TIMESTAMPTZ NOT NULL,
  ends_at     TIMESTAMPTZ NOT NULL,
  CHECK (ends_at > starts_at),

  -- CALC. percent OR fixed, per DIB-70. Three shapes because "fixed" means two different things at
  -- a counter and conflating them is how a $2-off promo becomes a $2 price:
  --   PCT_OFF     — discount_bps off the resolved base
  --   AMT_OFF     — discount_cents off the resolved base
  --   FIXED_PRICE — price_cents replaces the base outright (the clearance shape)
  calc_type   TEXT NOT NULL CHECK (calc_type IN ('PCT_OFF','AMT_OFF','FIXED_PRICE')),
  discount_bps   INTEGER CHECK (discount_bps   IS NULL OR (discount_bps >= 0 AND discount_bps <= 10000)),
  discount_cents BIGINT  CHECK (discount_cents IS NULL OR discount_cents >= 0),
  price_cents    BIGINT  CHECK (price_cents    IS NULL OR price_cents    >= 0),
  -- price_uom is only meaningful for FIXED_PRICE (a set price is per some unit); the percent and
  -- amount shapes are applied in whatever UOM the base resolved to.
  price_uom   CITEXT REFERENCES uoms(code) ON DELETE RESTRICT,
  CHECK ( (calc_type='PCT_OFF'     AND discount_bps   IS NOT NULL AND discount_cents IS NULL AND price_cents IS NULL)
       OR (calc_type='AMT_OFF'     AND discount_cents IS NOT NULL AND discount_bps   IS NULL AND price_cents IS NULL)
       OR (calc_type='FIXED_PRICE' AND price_cents    IS NOT NULL AND discount_bps   IS NULL AND discount_cents IS NULL
                                    AND price_uom     IS NOT NULL) ),

  -- Tie-break when two promos of the SAME specificity both match. Higher wins; id ASC breaks a
  -- priority tie so the resolver is deterministic under a collision rather than order-of-SELECT.
  priority    INTEGER NOT NULL DEFAULT 0,
  detail_text TEXT NOT NULL DEFAULT '',
  is_active   BOOLEAN NOT NULL DEFAULT TRUE,
  version     BIGINT NOT NULL DEFAULT 1,
  created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The resolver's lookup: active rows whose window contains now(), narrowed by product/category/
-- branch. Partial on is_active because a retired promo is never a candidate.
CREATE INDEX idx_pricing_promotions_window
  ON pricing_promotions (starts_at, ends_at) WHERE is_active;
CREATE INDEX idx_pricing_promotions_product  ON pricing_promotions (product_id)  WHERE is_active AND product_id  IS NOT NULL;
CREATE INDEX idx_pricing_promotions_category ON pricing_promotions (category_id) WHERE is_active AND category_id IS NOT NULL;

-- ── 2 · clearance (DIB-154) ───────────────────────────────────────────────────────────────────
-- WHY NOT A COLUMN ON products
-- ----------------------------
-- The obvious move is `ALTER TABLE products ADD COLUMN is_clearance BOOLEAN`. It is the wrong one
-- twice over. (a) OWNERSHIP: products is the catalogue's table and pricing is zero-trust about
-- catalogue tables — the resolver has a CategoryResolver seam precisely so it never SELECTs
-- products. A pricing-owned flag living on a catalogue row would be the first exception. (b)
-- LIFECYCLE: clearance is not a product attribute, it is a temporary commercial state with a
-- reason, a start, an optional end and a price. Those five facts do not belong in a boolean, and
-- the boolean would have to be reset by hand when the last pallet sells.
--
-- So clearance is a pricing-owned MEMBERSHIP row. Absent row = not on clearance. That also makes
-- "what is on clearance right now" a single indexed scan instead of a full-catalogue filter.
CREATE TABLE pricing_clearance_items (
  product_id  UUID PRIMARY KEY REFERENCES products(id) ON DELETE CASCADE,
  reason      TEXT NOT NULL DEFAULT '',
  -- An optional clearance PRICE. When set, clearance behaves as the most specific promotion there
  -- is (see the resolver ruling in REPORT-W4-A.md). When NULL the row is a BADGE only: the item
  -- shows as clearance on search results and quote lines, and prices through the normal waterfall.
  -- Both are real: "clear these out at $4" and "flag these, the manager will deal per customer".
  clearance_price_cents BIGINT CHECK (clearance_price_cents IS NULL OR clearance_price_cents >= 0),
  price_uom   CITEXT REFERENCES uoms(code) ON DELETE RESTRICT,
  CHECK ((clearance_price_cents IS NULL) = (price_uom IS NULL)),
  started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- NULL = open-ended. Unlike a promotion, clearance runs until the stock is gone, so an end date
  -- is optional here where it is mandatory there.
  ends_at     TIMESTAMPTZ,
  CHECK (ends_at IS NULL OR ends_at > started_at),
  is_active   BOOLEAN NOT NULL DEFAULT TRUE,
  version     BIGINT NOT NULL DEFAULT 1,
  created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_pricing_clearance_active ON pricing_clearance_items (product_id) WHERE is_active;

COMMENT ON TABLE pricing_promotions IS
  'DIB-70 seasonal promotions: date-bounded, branch-scopable, percent/fixed. Resolves BELOW a CUSTOMER_PRODUCT contract price and ABOVE the tier waterfall (see REPORT-W4-A.md precedence ruling).';
COMMENT ON TABLE pricing_clearance_items IS
  'DIB-154 clearance membership. Absent row = not on clearance. A row with clearance_price_cents prices as the most specific promotion; a row without is a badge only.';
