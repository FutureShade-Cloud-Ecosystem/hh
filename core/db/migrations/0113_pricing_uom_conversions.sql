-- 0113: SCOPE M2-uom — the sales-flow UoM conversion table (SF → PLT → TON) and its pick policy.
--
-- WHY THIS EXISTS WHEN product_uoms ALREADY DOES
-- ---------------------------------------------
-- product_uoms (0002) answers ONE question: how many BASE units is one X. That is enough to price
-- (the resolver's UOMConverter seam uses exactly that) and it is not enough to SELL. Two facts the
-- counter needs are absent from it and cannot be derived from it:
--
--   1. WEIGHT. A customer ordering 400 SF of paver wants to know it is 2.4 tonnes because that
--      decides whether it goes on the tandem or the tri-axle. products.weight_per_unit_kg exists,
--      but pricing is zero-trust about catalogue tables — the resolver has a CategoryResolver seam
--      precisely so it never SELECTs products — so the weight leg of the chain has to live here.
--   2. PICK GRANULARITY. You cannot pick 0.63 of a pallet. Somewhere between "the customer ordered
--      77 SF" and "the yard pulls stock" the quantity ROUNDS UP to a whole handling unit, and
--      nothing in product_uoms says which unit is the handling unit or that it is indivisible.
--      qty_in_base = 50 for PLT is silent about whether half a pallet is a thing.
--
-- SO THIS TABLE IS THE PRICING-OWNED SALES CONVERSION GRAPH: directed edges (from_uom → to_uom)
-- with a factor, scoped product > category > global, plus the round-up-at-pick flag on the edges
-- whose TARGET is a handling unit. The service walks it as an undirected graph (an edge is usable
-- in reverse at 1/factor), which is what makes SF → PLT → TON resolvable from two rows instead of
-- requiring every pair to be authored.
--
-- ⚠ THE FACTORS BELOW ARE NOT AUTHORITATIVE — THIS IS AN OPEN CLIENT QUESTION ⚠
-- -----------------------------------------------------------------------------
-- Scrubname0032 does Dibbits' conversion data actually come from? Three candidate sources were visible and
-- none of them is confirmed: (a) the manufacturer spec sheets (per-SKU, most accurate, not held in
-- this system), (b) the BisTrack product master being migrated (held, but its own provenance is
-- unknown), (c) the yard's own weigh-scale history. Until that is answered, EVERY row this
-- migration writes carries source='SEEDED_DEFAULT' and a source_note that says so, and the admin
-- surface renders SEEDED_DEFAULT rows with a visible "unconfirmed" marker. The SF↔PLT edges are at
-- least INTERNALLY consistent — they are computed from the product's own product_uoms factors at
-- migrate time rather than typed in — but the PLT→T weights are placeholders and are labelled as
-- such. Nothing here should be read off a screen as fact. See REPORT-W4-A.md, "open questions".

CREATE TABLE pricing_uom_conversions (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  -- SCOPE: product XOR category XOR global (both NULL). Most specific wins per EDGE, not per
  -- lookup — a product can override just its weight edge and inherit its category's pallet edge.
  product_id  UUID REFERENCES products(id) ON DELETE CASCADE,
  category_id UUID REFERENCES product_categories(id) ON DELETE CASCADE,
  CHECK (product_id IS NULL OR category_id IS NULL),

  from_uom    CITEXT NOT NULL REFERENCES uoms(code) ON DELETE RESTRICT,
  to_uom      CITEXT NOT NULL REFERENCES uoms(code) ON DELETE RESTRICT,
  CHECK (from_uom <> to_uom),
  -- 1 from_uom = factor to_uom. Strictly positive: a zero or negative factor makes the graph walk
  -- produce a zero or negative quantity, and a zero-quantity pick ticket is worse than an error.
  factor      NUMERIC(19,8) NOT NULL CHECK (factor > 0),

  -- to_uom is an INDIVISIBLE HANDLING UNIT: a pick quantity expressed in it rounds UP to a whole
  -- number. This is the flag, and the ONLY flag, that makes 77 SF become 2 pallets rather than
  -- 1.27 pallets. It lives on the edge (not on the uom) because the same code can be divisible for
  -- one product and not another — a partial pallet of bagged goods is normal, a partial pallet of
  -- banded slabs is not.
  round_up_at_pick BOOLEAN NOT NULL DEFAULT FALSE,

  -- PROVENANCE, first-class. The open client question above is the reason this column is NOT
  -- NULLABLE and NOT defaulted to something reassuring: every row must declare where its number
  -- came from, and 'SEEDED_DEFAULT' is the honest answer for everything this migration writes.
  source      TEXT NOT NULL DEFAULT 'SEEDED_DEFAULT'
              CHECK (source IN ('SEEDED_DEFAULT','CLIENT_CONFIRMED','VENDOR_SHEET','MEASURED')),
  source_note TEXT NOT NULL DEFAULT '',

  is_active   BOOLEAN NOT NULL DEFAULT TRUE,
  version     BIGINT NOT NULL DEFAULT 1,
  created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One active edge per (scope, from, to). Two active rows for the same edge would make the graph
-- walk order-dependent, which is the exact failure mode that makes a conversion bug unreproducible.
-- COALESCE to the nil uuid so the global (both-NULL) scope participates in the uniqueness instead
-- of being exempt from it — NULLs would otherwise never collide.
CREATE UNIQUE INDEX ux_pricing_uom_conv_edge ON pricing_uom_conversions (
  COALESCE(product_id,  '00000000-0000-0000-0000-000000000000'::uuid),
  COALESCE(category_id, '00000000-0000-0000-0000-000000000000'::uuid),
  from_uom, to_uom
) WHERE is_active;

CREATE INDEX idx_pricing_uom_conv_product  ON pricing_uom_conversions (product_id)  WHERE is_active AND product_id  IS NOT NULL;
CREATE INDEX idx_pricing_uom_conv_category ON pricing_uom_conversions (category_id) WHERE is_active AND category_id IS NOT NULL;

-- ── SEEDED DEFAULTS ───────────────────────────────────────────────────────────────────────────
-- 1 · SF → PLT, per product, COMPUTED from that product's own product_uoms rows so the sales graph
--     cannot disagree with the pricing graph. factor = (base per SF) / (base per PLT) = PLT per SF.
--     round_up_at_pick = TRUE: a pallet is banded, you take whole ones.
INSERT INTO pricing_uom_conversions (product_id, from_uom, to_uom, factor, round_up_at_pick, source, source_note)
SELECT sf.product_id, 'SF', 'PLT',
       ROUND(sf.qty_in_base / plt.qty_in_base, 8),
       TRUE, 'SEEDED_DEFAULT',
       'Derived at migrate time from product_uoms (SF and PLT qty_in_base). Internally consistent; provenance of the underlying catalogue factors is the OPEN client question.'
  FROM product_uoms sf
  JOIN product_uoms plt ON plt.product_id = sf.product_id AND plt.uom_code = 'PLT'
 WHERE sf.uom_code = 'SF' AND plt.qty_in_base > 0
ON CONFLICT DO NOTHING;

-- 2 · PLT → T (tonne), per product. THESE ARE PLACEHOLDERS. There is no weight data in this
--     system that pricing is allowed to read, and inventing a per-SKU pallet weight would be
--     exactly the fabrication this ticket warns against — so a single conservative value is
--     applied uniformly and labelled, rather than a plausible-looking spread that would read as
--     researched. The admin surface shows these as unconfirmed.
INSERT INTO pricing_uom_conversions (product_id, from_uom, to_uom, factor, round_up_at_pick, source, source_note)
SELECT DISTINCT plt.product_id, 'PLT', 'T', 1.10000000, FALSE, 'SEEDED_DEFAULT',
       'PLACEHOLDER pallet weight (1.10 t). NOT measured, NOT vendor-supplied, NOT client-confirmed. Replace before any load-planning decision depends on it.'
  FROM product_uoms plt
 WHERE plt.uom_code = 'PLT'
ON CONFLICT DO NOTHING;

COMMENT ON TABLE pricing_uom_conversions IS
  'M2-uom sales conversion graph (SF->PLT->TON) plus pick granularity. source=SEEDED_DEFAULT means the factor is UNCONFIRMED — see REPORT-W4-A.md open questions.';
COMMENT ON COLUMN pricing_uom_conversions.round_up_at_pick IS
  'to_uom is an indivisible handling unit: pick quantities round UP to a whole number of it.';
