-- 0101: GABLE:pos-barcode + GABLE:search-synonyms — the two lookup tables the counter search needs.
--
-- ── product_barcodes ────────────────────────────────────────────────────────────────────────────────
-- A hardscape yard scans a lot of things that are not one SKU-shaped code: the manufacturer's UPC on a
-- bag of polymeric sand, the distributor's EAN on the same bag from a different pallet, and the yard's
-- own printed label on a bulk bin. That is many barcodes to ONE product, which is why this is a table and
-- not a products.barcode column.
--
-- UNIQUE ACROSS PRODUCTS, NOT MERELY WITHIN ONE. The whole value of a scan is that it is unambiguous: the
-- cashier scans, a line appears, nobody chooses. If the same code could sit on two products the scan would
-- have to open a picker, and the feature would be slower than typing the SKU. The uniqueness is therefore
-- on `code` alone (CITEXT — scanners emit digits, but a hand-typed alpha code must not become a second
-- row that differs only in case).
--
-- ── product_search_synonyms ─────────────────────────────────────────────────────────────────────────
-- Catalogue search fails on the words customers actually use. "Patio stone" is what a homeowner asks for;
-- the catalogue calls it a slab. "Flagstone" is what a landscaper says; the SKU says Banas Natural. A
-- trigram index cannot bridge those — they share no characters. The fix is an explicit alias table: a term
-- the human types, mapped to the canonical term the catalogue stores, applied as an OR-branch of the
-- search query.
--
-- SCOPE IS EITHER GLOBAL OR ONE PRODUCT. A global alias (product_id NULL) rewrites the query term for the
-- whole catalogue — that is the regional-vocabulary case. A product-scoped alias pins one term to one
-- product, for the "everyone at this branch calls DBS-TB-BLU60 'the sixty'" case. Both are the same row
-- shape, so the search does one lookup, not two.

CREATE TABLE product_barcodes (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  product_id UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
  code       CITEXT NOT NULL,
  -- symbology is descriptive (what kind of code this is), never a uniqueness dimension: the same digits
  -- under two symbologies is still one physical label.
  symbology  TEXT NOT NULL DEFAULT 'UNKNOWN'
             CHECK (symbology IN ('UPC_A','UPC_E','EAN_13','EAN_8','CODE_128','CODE_39','ITF_14','QR','INTERNAL','UNKNOWN')),
  -- The code printed on the yard's own label, as opposed to one that arrived on the vendor's packaging.
  -- Kept so a future label-reprint knows which code it owns.
  is_primary BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT product_barcodes_code_not_blank CHECK (length(btrim(code::text)) > 0)
);

-- THE guard: one code, one product, catalogue-wide.
CREATE UNIQUE INDEX uq_product_barcodes_code ON product_barcodes (code);
CREATE INDEX idx_product_barcodes_product ON product_barcodes (product_id);
-- At most one primary (yard-owned) label per product.
CREATE UNIQUE INDEX uq_product_barcodes_one_primary
  ON product_barcodes (product_id) WHERE is_primary;

-- Demo barcodes for the seeded catalogue: deterministic 12-digit codes derived from the SKU so a
-- walkthrough can scan a real number. Only the products a counter actually scans as packaged goods get
-- one — bulk aggregate is sold by weight and has no label to scan.
INSERT INTO product_barcodes (product_id, code, symbology, is_primary)
SELECT p.id,
       '062' || lpad((abs(hashtext(p.sku)) % 1000000000)::text, 9, '0'),
       'UPC_A', TRUE
  FROM products p
 WHERE p.sku IN ('DBS-GATOR-SAND','DBS-SIKA-SEALER','DBS-INLITE-LED','DBS-GEO-FABRIC',
                 'DBS-TB-BLU60','DBS-UL-BRUSSELS','DBS-PM-LAFITT','DBS-PORCEA-2424')
ON CONFLICT DO NOTHING;

CREATE TABLE product_search_synonyms (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  -- What the human types. CITEXT so "Flagstone" and "flagstone" are one row.
  term       CITEXT NOT NULL,
  -- What the catalogue stores. Matched against name/sku/collection/application.
  canonical  CITEXT NOT NULL,
  -- NULL = global alias (applies to the whole catalogue); set = alias pinned to one product.
  product_id UUID REFERENCES products(id) ON DELETE CASCADE,
  -- Provenance, for the people who will later ask "why does this match?".
  source     TEXT NOT NULL DEFAULT 'STARTER'
             CHECK (source IN ('STARTER','REGIONAL','VENDOR','STAFF')),
  is_active  BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT product_search_synonyms_term_not_blank CHECK (length(btrim(term::text)) > 0),
  CONSTRAINT product_search_synonyms_canonical_not_blank CHECK (length(btrim(canonical::text)) > 0),
  -- A term may map to several canonicals (an alias legitimately widens); the same pair twice is a dupe.
  CONSTRAINT uq_product_search_synonyms UNIQUE (term, canonical, product_id)
);
CREATE INDEX idx_product_search_synonyms_term ON product_search_synonyms (term) WHERE is_active;

-- Starter set: the vocabulary gaps a Southern-Ontario hardscape counter hits every week.
INSERT INTO product_search_synonyms (term, canonical, source) VALUES
  ('patio stone',   'Slab',        'STARTER'),
  ('patio stones',  'Slab',        'STARTER'),
  ('patio slab',    'Slab',        'STARTER'),
  ('flagstone',     'Natural',     'REGIONAL'),
  ('flag stone',    'Natural',     'REGIONAL'),
  ('paving stone',  'Paver',       'STARTER'),
  ('paving stones', 'Paver',       'STARTER'),
  ('brick',         'Paver',       'REGIONAL'),
  ('bricks',        'Paver',       'REGIONAL'),
  ('interlock',     'Paver',       'REGIONAL'),
  ('interlocking',  'Paver',       'REGIONAL'),
  ('retaining wall','Wall',        'STARTER'),
  ('block',         'Wall',        'STARTER'),
  ('armour stone',  'Natural',     'REGIONAL'),
  ('armourstone',   'Natural',     'REGIONAL'),
  ('poly sand',     'Polymeric',   'STARTER'),
  ('polysand',      'Polymeric',   'STARTER'),
  ('joint sand',    'Polymeric',   'STARTER'),
  ('crusher run',   'Screenings',  'REGIONAL'),
  ('screening',     'Screenings',  'STARTER'),
  ('quarter down',  'Screenings',  'REGIONAL'),
  ('clear stone',   'Clear',       'STARTER'),
  ('gravel',        'Clear Stone', 'REGIONAL'),
  ('bedding sand',  'Concrete Sand','STARTER'),
  ('landscape fabric','Geotextile','STARTER'),
  ('weed fabric',   'Geotextile',  'REGIONAL'),
  ('porcelain',     'Porcea',      'STARTER'),
  ('turf',          'Turf',        'STARTER'),
  ('fake grass',    'Turf',        'REGIONAL'),
  ('sealer',        'Sealer',      'STARTER'),
  ('step',          'Step',        'STARTER'),
  ('coping',        'Coping',      'STARTER'),
  ('cap',           'Coping',      'REGIONAL')
ON CONFLICT DO NOTHING;
