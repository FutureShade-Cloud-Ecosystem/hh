-- 0169 — DIB-502 (built inside the DIB-488 rev-8 slate): the suggested add-ons CO-PURCHASE
-- PAIRING MAP behind GET /api/v1/pos/addon-suggestions.
--
-- WHY A TABLE AND NOT A CONSTANT: the design is DATA-DRIVEN on purpose. Today the rows are a
-- seeded, category-level domain map (pavers → polymeric sand / edge restraint; walls → adhesive /
-- caps; aggregate → fabric; …); the moment real co-purchase analysis exists, a job can replace
-- these rows from actual sales history WITHOUT a schema or code change — the endpoint only ever
-- reads the map. Rank orders the suggestions (lower = stronger pairing).
--
-- CATEGORY-LEVEL: both sides are products.application values (the same category vocabulary the
-- counter's chips and the browse grid already speak). A pairing row whose categories match no
-- active product simply yields nothing — the strip hides on an empty answer (empty-never-error).
--
-- NOTE ON THE REV-8 NUMBER ASSIGNMENT: 0169 was reserved for a product-weight column, but weight
-- was DISCOVERED already on the catalogue (products.weight_per_unit_kg, seeded since 0002-era
-- data) — so no weight migration exists and 0169 carries the DIB-502 pairing map instead.

CREATE TABLE IF NOT EXISTS pos_addon_pairings (
    source_application    citext NOT NULL,
    suggested_application citext NOT NULL,
    rank                  integer NOT NULL DEFAULT 100,
    created_at            timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (source_application, suggested_application),
    CHECK (source_application <> suggested_application)
);

COMMENT ON TABLE pos_addon_pairings IS
  'DIB-502: category-level co-purchase pairing map behind the POS suggested add-ons strip. Seeded domain pairs today; replaceable row-for-row from real sales data later (data-driven by design).';
COMMENT ON COLUMN pos_addon_pairings.rank IS
  'Suggestion ordering — lower ranks first when composing the (max 3) suggestion tiles.';
