-- 0360: HH-207's rest, the fix-up for databases seeded before the seeded conversion factors were
-- corrected. PR 78 fixed six flat slabs in the seed; the seed now also carries the eleven remaining
-- face-derived factors and the Magnumstone invoice line that followed the old Magnumstone factor.
-- This migration carries an ALREADY SEEDED database to the same state. It is UPDATE only, and every
-- statement is guarded to the exact old seeded value, so it is idempotent (a second run matches
-- nothing) and a value a person changed is never touched: a factor no longer at the old seeded
-- figure is somebody's number, not ours. A database seeded after PR 78 merged already carries the
-- six new values below and those rows fall through the guards naturally.
--
-- THE CONVENTION (the one PR 78 proved, and its round 1 review note for walls): pieces_per_sf is
-- pieces per SQUARE FOOT, 92903.04 square mm, over the product's own sold face. Pavers and slabs
-- sell their length x width; a wall block sells length x HEIGHT, the width being the depth into the
-- wall. The per SQUARE METRE figure (1e6 over the face) was the defect.
--
-- products.pieces_per_sf, old -> new (the six PR 78 already corrected, then the eleven this lane
-- adds; paver and slab faces are length x width, wall faces length x height):
--   DBS-TB-BLU60          2.78 -> 0.2581   (600x600)
--   DBS-PM-BORANJA        2.78 -> 0.2581   (600x600)
--   DBS-PORCEA-2424       2.78 -> 0.2581   (600x600)
--   DBS-PORCEA-PLANK      2.78 -> 0.2581   (1200x300)
--   DBS-PM-MELVILLE       4.17 -> 0.3871   (600x400)
--   DBS-BWS-TREVISTA      5.56 -> 0.5161   (600x300)
--   DBS-UL-SERIES3000     2.07 -> 2.0645   (300x150)
--   DBS-TB-HYDR           3.95 -> 3.6866   (225x112)
--   DBS-OAKS-VINTAGE      3.55 -> 2.6400   (230x153)
--   DBS-UL-ILCAMPO        1.55 -> 1.0323   (300x300)
--   DBS-BWS-ANTICO        5.10 -> 3.1600   (210x140)
--   DBS-PM-LAFITT         3.10 -> 1.1613   (400x200)
--   DBS-TB-ABERDEEN       5.14 -> 3.3180   (280x100 wall face)
--   DBS-TB-MINICRETA      6.00 -> 4.6452   (200x100 wall face)
--   DBS-PM-MAGNUMSTONE    2.40 -> 1.0323   (450x200 wall face)
--   DBS-OAKS-PROMA        3.45 -> 2.1357   (290x150 wall face)
--   DBS-UL-THORNBURY      3.57 -> 3.3180   (280x100 wall face)
--
-- The two seeded transaction lines whose qty_base was hard-coded from the old factors:
--   quote  17000000-0000-4000-9000-000000000001 line 1 (Blu 60):      2669    -> 247.776  (960 x 0.2581)
--   invoice 19000000-0000-4000-9000-000000000002 line 1 (Magnumstone): 3600    -> 1548.45  (1500 x 1.0323)
-- Pieces are not rounded up: the figure is exactly what the live conversion path writes.
--
-- pricing_uom_conversions: where an environment's SF to PLT edges were derived from the old factors
-- (0113 derives them from product_uoms), the SEEDED_DEFAULT edges still sitting at the old derived
-- factor are recomputed from the corrected product_uoms rows. An edge the client confirmed, read off
-- a vendor sheet, or measured is never touched here; nor is an edge whose factor somebody has since
-- changed, because the guard is the edge's own value. Brussels keeps the factor 0061 gave it.

-- 1. the seventeen product factors, only where the seed's old figure still stands.
UPDATE products p
   SET pieces_per_sf = v.new_factor, updated_at = now()
  FROM (VALUES
    ('DBS-TB-BLU60',        2.78, 0.2581),
    ('DBS-PM-BORANJA',      2.78, 0.2581),
    ('DBS-PORCEA-2424',     2.78, 0.2581),
    ('DBS-PORCEA-PLANK',    2.78, 0.2581),
    ('DBS-PM-MELVILLE',     4.17, 0.3871),
    ('DBS-BWS-TREVISTA',    5.56, 0.5161),
    ('DBS-UL-SERIES3000',   2.07, 2.0645),
    ('DBS-TB-HYDR',         3.95, 3.6866),
    ('DBS-OAKS-VINTAGE',    3.55, 2.6400),
    ('DBS-UL-ILCAMPO',      1.55, 1.0323),
    ('DBS-BWS-ANTICO',      5.10, 3.1600),
    ('DBS-PM-LAFITT',       3.10, 1.1613),
    ('DBS-TB-ABERDEEN',     5.14, 3.3180),
    ('DBS-TB-MINICRETA',    6.00, 4.6452),
    ('DBS-PM-MAGNUMSTONE',  2.40, 1.0323),
    ('DBS-OAKS-PROMA',      3.45, 2.1357),
    ('DBS-UL-THORNBURY',    3.57, 3.3180)
  ) AS v(sku, old_factor, new_factor)
 WHERE p.sku = v.sku AND p.pieces_per_sf = v.old_factor;

-- 2. the SF uom rows the product factor feeds, same guard on the row's own value.
UPDATE product_uoms pu
   SET qty_in_base = v.new_factor, updated_at = now()
  FROM products p
  JOIN (VALUES
    ('DBS-TB-BLU60',        2.78, 0.2581),
    ('DBS-PM-BORANJA',      2.78, 0.2581),
    ('DBS-PORCEA-2424',     2.78, 0.2581),
    ('DBS-PORCEA-PLANK',    2.78, 0.2581),
    ('DBS-PM-MELVILLE',     4.17, 0.3871),
    ('DBS-BWS-TREVISTA',    5.56, 0.5161),
    ('DBS-UL-SERIES3000',   2.07, 2.0645),
    ('DBS-TB-HYDR',         3.95, 3.6866),
    ('DBS-OAKS-VINTAGE',    3.55, 2.6400),
    ('DBS-UL-ILCAMPO',      1.55, 1.0323),
    ('DBS-BWS-ANTICO',      5.10, 3.1600),
    ('DBS-PM-LAFITT',       3.10, 1.1613),
    ('DBS-TB-ABERDEEN',     5.14, 3.3180),
    ('DBS-TB-MINICRETA',    6.00, 4.6452),
    ('DBS-PM-MAGNUMSTONE',  2.40, 1.0323),
    ('DBS-OAKS-PROMA',      3.45, 2.1357),
    ('DBS-UL-THORNBURY',    3.57, 3.3180)
  ) AS v(sku, old_factor, new_factor) ON p.sku = v.sku
 WHERE pu.product_id = p.id AND pu.uom_code = 'SF' AND pu.qty_in_base = v.old_factor;

-- 3. the two seeded transaction lines, guarded to the old hard-coded quantities.
UPDATE quote_lines SET qty_base = 247.776
 WHERE quote_id = '17000000-0000-4000-9000-000000000001' AND line_no = 1 AND qty_base = 2669;
UPDATE invoice_lines SET qty_base = 1548.45
 WHERE invoice_id = '19000000-0000-4000-9000-000000000002' AND line_no = 1 AND qty_base = 3600;

-- 4. the SEEDED_DEFAULT SF to PLT edges still sitting at the factor the old uom rows derive,
--    recomputed from the corrected rows exactly the way 0113 derived them.
UPDATE pricing_uom_conversions c
   SET factor = ROUND(sf.qty_in_base / plt.qty_in_base, 8), updated_at = now()
  FROM products p
  JOIN (VALUES
    ('DBS-TB-BLU60',        2.78),
    ('DBS-PM-BORANJA',      2.78),
    ('DBS-PORCEA-2424',     2.78),
    ('DBS-PORCEA-PLANK',    2.78),
    ('DBS-PM-MELVILLE',     4.17),
    ('DBS-BWS-TREVISTA',    5.56),
    ('DBS-UL-SERIES3000',   2.07),
    ('DBS-TB-HYDR',         3.95),
    ('DBS-OAKS-VINTAGE',    3.55),
    ('DBS-UL-ILCAMPO',      1.55),
    ('DBS-BWS-ANTICO',      5.10),
    ('DBS-PM-LAFITT',       3.10),
    ('DBS-TB-ABERDEEN',     5.14),
    ('DBS-TB-MINICRETA',    6.00),
    ('DBS-PM-MAGNUMSTONE',  2.40),
    ('DBS-OAKS-PROMA',      3.45),
    ('DBS-UL-THORNBURY',    3.57)
  ) AS v(sku, old_factor) ON p.sku = v.sku
  JOIN product_uoms sf  ON sf.product_id  = p.id AND sf.uom_code  = 'SF'
  JOIN product_uoms plt ON plt.product_id = p.id AND plt.uom_code = 'PLT' AND plt.qty_in_base > 0
 WHERE c.product_id = p.id AND c.from_uom = 'SF' AND c.to_uom = 'PLT'
   AND c.source = 'SEEDED_DEFAULT'
   AND c.factor = ROUND(v.old_factor / plt.qty_in_base, 8);
