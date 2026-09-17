-- 0186 — the counter hot list opens on the YARD'S BULK BOARD (commander's ruling, 27 Aug, R2).
--
-- THE RULING: "Bulk aggregates - but should be configurable."
--
-- THE WORDS BEHIND IT. Scrubname0025 Scrubname0024 asked for touchscreen tiles with photos, drilled "to bulk and then
-- mulch and then black or whatever the categories are with pictures because if they're going to go
-- touchscreen I think that would be pretty awesome." speaker-0007 put the volume behind it: "the
-- volume of sales we do in bulk material is higher than contracts/projects", and "if we had a hot
-- list sort of touchscreen looking all the different color mulches and the different aggregates.
-- That would be neat."
--
-- SO A COLD DEPLOYMENT MUST LOOK LIKE A BULK BOARD, not like a general catalogue root. Two halves,
-- and only the second one is in this file:
--
--   1. THE RANKING, which ships for every dealer and needs no seed at all: the derived hot list is
--      ordered bulk-first off facts a real catalogue already carries — the BULK/AGGREGATE category
--      (0067) and the bulk sell units (tonnes, cubic yards). See TopSellingProducts in
--      internal/pos/repository_quickpicks.go. A real dealer's own bulk lines rise on their own.
--
--   2. THE DEMO CATALOGUE, below. The seeded database has two aggregates and no mulch at all, so a
--      cold `make reset` could not show the board the client described. This adds the yard's bulk
--      lines — mulch broken out by COLOUR, aggregates broken out by SIZE, soils — and curates them
--      as the dealer's default tile set so the hot list opens on them in a chosen order rather than
--      a computed one.
--
-- WHY THE CURATION ROWS ARE SAFE ON A REAL DEALER. A curated SKU that does not exist resolves to no
-- product and the set falls back to the derived one (quickpicks.go says so, and its test pins it).
-- A dealer who has never heard of DBS-MUL-BLACK is therefore unaffected by these rows; a dealer who
-- edits the board in Counter settings overwrites them wholesale.
--
-- NUMBERED 0186 BY THE UNION GATE (train 14). The lane authored this as 0190, holding 0186-0189
-- clear because it could not see how the concurrent dispatch commit-vocabulary lane's 0185 would
-- land. Both lanes are now merged, 0185 is that lane's and only that lane's, and the run is
-- contiguous again at 0186. Renumbering is safe here because NOTHING references this file by
-- number or by name except its own header and the lane NOTES: no Go embed, no test that reads a
-- migration by path, no fixture. The runner sorts filenames and records what it applied, so the
-- only cost of the old gap would have been a run nobody could read at a glance.
--
-- MIGRATE-BEFORE-SEED / FRESH-DB DISCIPLINE: every FK resolves by NATURAL KEY (sku, category code)
-- and every statement is guarded and idempotent, so this is safe on a fresh migrate, on a re-run,
-- and under a later full cmd/seed.

-- ── 1. the bulk catalogue ───────────────────────────────────────────────────────────────────────
-- application is the FAMILY and collection is the COLOUR OR SIZE, because that pair is exactly the
-- two levels the hot list drills: bulk board -> Mulch -> Black. Sold by the cubic yard (mulch,
-- soils) or the tonne (aggregates), which is also what puts them in the bulk-first partition.
--
-- ONLY SKUs cmd/seed DOES NOT ALREADY OWN ARE INSERTED HERE. (Union train 14 correction.) As
-- authored, this block also inserted DBS-AGG-SCREEN and DBS-AGG-HPB under FRESH uuids (…111/…112)
-- while cmd/seed files those same two SKUs under …020/…019. Migrations run BEFORE the seed, so this
-- file won the `products_sku_key` unique index, the seed's own rows were silently skipped by its
-- ON CONFLICT (sku) DO NOTHING, and every fixture holding the seeded uuid was orphaned — 21
-- integration tests across internal/order and internal/quote, all of them hanging off the constant
--     w5aProduct = "a7000000-0000-4000-9000-000000000019" // DBS-AGG-HPB
-- The two SKUs need no INSERT at all: the seed creates them and §2 below already RE-FILES them onto
-- the bulk board, which is this file's actual job for a row it does not own. Section 2's own comment
-- said as much ("cmd/seed files two more the same way") — §1 and §2 simply disagreed, and §1 won.
-- Nothing on the board changes: HPB and Screenings still tile under Aggregate, from the seeded rows,
-- and their curation rows in §4 key on the SKU-derived item id, not on a uuid.
INSERT INTO products (id, sku, name, base_uom, price_uom, sold_by, manufacturer, application, collection, product_size, category_id)
SELECT v.id::uuid, v.sku, v.name, v.uom, v.uom, v.uom, 'Dibbits Yard', v.app, v.coll, v.size,
       (SELECT id FROM product_categories WHERE code = 'BULK')
  FROM (VALUES
    ('a7000000-0000-4000-9000-000000000101','DBS-MUL-BLACK',  'Black Mulch',           'CYD','Mulch',    'Black',       'Per cubic yard'),
    ('a7000000-0000-4000-9000-000000000102','DBS-MUL-BROWN',  'Brown Mulch',           'CYD','Mulch',    'Brown',       'Per cubic yard'),
    ('a7000000-0000-4000-9000-000000000103','DBS-MUL-RED',    'Red Mulch',             'CYD','Mulch',    'Red',         'Per cubic yard'),
    ('a7000000-0000-4000-9000-000000000104','DBS-MUL-CEDAR',  'Cedar Mulch',           'CYD','Mulch',    'Cedar',       'Per cubic yard'),
    ('a7000000-0000-4000-9000-000000000113','DBS-AGG-RIVER',  'River Rock 1-3"',       'T',  'Aggregate','River Rock',  'Per tonne'),
    ('a7000000-0000-4000-9000-000000000121','DBS-SOIL-TRIPLE','Triple Mix',            'CYD','Soil',     'Triple Mix',  'Per cubic yard'),
    ('a7000000-0000-4000-9000-000000000122','DBS-SOIL-TOP',   'Screened Topsoil',      'CYD','Soil',     'Topsoil',     'Per cubic yard')
  ) AS v(id, sku, name, uom, app, coll, size)
ON CONFLICT (sku) DO NOTHING;

-- Base sell unit for each, so a tap adds in the unit the tile advertises.
INSERT INTO product_uoms (product_id, uom_code, qty_in_base, is_base)
  SELECT id, base_uom, 1, TRUE FROM products
   WHERE sku IN ('DBS-MUL-BLACK','DBS-MUL-BROWN','DBS-MUL-RED','DBS-MUL-CEDAR',
                 'DBS-AGG-RIVER','DBS-SOIL-TRIPLE','DBS-SOIL-TOP')
ON CONFLICT (product_id, uom_code) DO NOTHING;

-- PROVISIONAL retail anchors, so a bulk tile carries a price and a walk-in bulk sale actually rings
-- (the 0061 lesson: with no list anchor the RETAIL waterfall aborts the sale). Real numbers land
-- with the GM's worksheet, exactly as 0061 and 0067 already record.
INSERT INTO pricing_list_prices (product_id, list_price_cents, price_uom)
SELECT p.id, v.cents, v.uom
  FROM (VALUES
    ('DBS-MUL-BLACK',   5400, 'CYD'),
    ('DBS-MUL-BROWN',   5400, 'CYD'),
    ('DBS-MUL-RED',     5800, 'CYD'),
    ('DBS-MUL-CEDAR',   7200, 'CYD'),
    ('DBS-AGG-RIVER',   9200, 'T'),
    ('DBS-SOIL-TRIPLE', 4600, 'CYD'),
    ('DBS-SOIL-TOP',    3800, 'CYD')
  ) AS v(sku, cents, uom)
  JOIN products p ON p.sku = v.sku
ON CONFLICT (product_id) DO NOTHING;

-- ── 2. the two 0061 aggregates read as a bulk board too ─────────────────────────────────────────
-- They were filed use-first (application 'Bedding' / 'Drainage', collection 'Aggregate'), which
-- drills as two one-item root tiles named after uses instead of one Aggregate tile broken out by
-- size. Guarded to those exact two SKUs AND to the exact 0061 values, so a dealer who has since
-- classified them by hand is never overwritten.
UPDATE products SET application = 'Aggregate', collection = 'Bedding Sand', updated_at = now()
 WHERE sku = 'DBS-AGG-SAND' AND application = 'Bedding' AND collection = 'Aggregate';
UPDATE products SET application = 'Aggregate', collection = '3/4" Clear', updated_at = now()
 WHERE sku = 'DBS-AGG-34CLEAR' AND application = 'Drainage' AND collection = 'Aggregate';
-- cmd/seed files two more the same way ('Base' / 'Bedding' as the application, 'Aggregate' as the
-- collection). Same guard, same reason: four one-item root tiles named after uses is not a bulk
-- board, and an aggregate broken out by SIZE is what the client described.
--
-- THESE TWO UPDATES ONLY BITE ON AN ALREADY-SEEDED DATABASE — which is the one that matters, since
-- staging is seeded and is never reseeded. On a fresh `migrate -> seed` the rows do not exist yet
-- when this file runs, so cmd/seed now files them correctly at birth (see the UNION TRAIN 14 note
-- above the aggregates in dibbits_seed.sql). Both orders converge on the same board. The 0061 pair
-- above needs no such help: those rows are planted by a migration, so they are already here.
UPDATE products SET application = 'Aggregate', collection = 'Screenings', updated_at = now()
 WHERE sku = 'DBS-AGG-SCREEN' AND application = 'Base' AND collection = 'Aggregate';
UPDATE products SET application = 'Aggregate', collection = 'HPB', updated_at = now()
 WHERE sku = 'DBS-AGG-HPB' AND application = 'Bedding' AND collection = 'Aggregate';

-- ── 3. photographs on SOME tiles, deliberately not all ──────────────────────────────────────────
-- The mulch colours carry a real 1×1 PNG in their own colour, because a colour swatch is genuinely
-- what a mulch tile wants and because the drill's whole point was "with pictures". The aggregates
-- and soils carry NONE — on purpose: a missing photo is the ordinary case in a real catalogue, and
-- the board must be demonstrably fine without one (hos-product-thumb draws a stone-stack mark, not
-- an empty square and not the browser's torn-page glyph). A demo where every tile has a picture
-- would hide the state that actually needs proving.
INSERT INTO product_images (product_id, content_type, byte_size, image_bytes, alt_text, sort_order, checksum)
SELECT p.id, 'image/png', octet_length(decode(v.hex, 'hex')), decode(v.hex, 'hex'), p.name, 0,
       md5(decode(v.hex, 'hex'))
  FROM (VALUES
    ('DBS-MUL-BLACK', '89504e470d0a1a0a0000000d4948445200000001000000010802000000907753de0000000c4944415478da6330d6d706000126008e67d88cc60000000049454e44ae426082'),
    ('DBS-MUL-BROWN', '89504e470d0a1a0a0000000d4948445200000001000000010802000000907753de0000000c4944415478da63c8f6d20700020800e5d0e20fc40000000049454e44ae426082'),
    ('DBS-MUL-RED',   '89504e470d0a1a0a0000000d4948445200000001000000010802000000907753de0000000c4944415478da63a8d257020001f200cc930a77ae0000000049454e44ae426082'),
    ('DBS-MUL-CEDAR', '89504e470d0a1a0a0000000d4948445200000001000000010802000000907753de0000000c4944415478da63d8d0160c000373018a35a0c0630000000049454e44ae426082')
  ) AS v(sku, hex)
  JOIN products p ON p.sku = v.sku
 WHERE NOT EXISTS (SELECT 1 FROM product_images pi WHERE pi.product_id = p.id);

-- ── 4. the dealer's default board — curated, in a CHOSEN order ──────────────────────────────────
-- Kind `chip` under surface `pos.counter` is the same substrate the counter already reads its tiles
-- from; position is the board's order, and because the tree now groups in first-appearance order
-- (see _pos-hotlist.ts) these positions decide the CATEGORY order too: mulch leads, then the
-- aggregates by size, then the soils. That is the "categories and their order" half of the ruling,
-- expressed in the substrate an admin edits rather than in a sort nobody can reach.
INSERT INTO surface_role_items (surface_id, profile, item_kind, item_id, position, visible)
SELECT 'pos.counter', p.profile, 'chip', i.item_id, i.position, TRUE
FROM (VALUES ('counter'), ('yard'), ('driver'), ('manager'), ('admin')) AS p (profile)
CROSS JOIN (VALUES
    ('dbs-mul-black',    0),
    ('dbs-mul-brown',    1),
    ('dbs-mul-red',      2),
    ('dbs-mul-cedar',    3),
    ('dbs-agg-34clear',  4),
    ('dbs-agg-screen',   5),
    ('dbs-agg-hpb',      6),
    ('dbs-agg-river',    7),
    ('dbs-agg-sand',     8),
    ('dbs-soil-triple',  9),
    ('dbs-soil-top',    10)
) AS i (item_id, position)
ON CONFLICT (surface_id, profile, item_kind, item_id) DO NOTHING;

COMMENT ON TABLE product_images IS
  'GABLE:product-images-ui product thumbnails. 0186 seeds colour swatches for the demo mulch tiles and deliberately leaves the aggregates without one, so the no-photo state stays demonstrable.';
