-- 0063: homework-flow blocker fixes surfaced by walking the flows live (sweep #46).
-- Two independent, idempotent fixes — no pricing/GL math change (service.go:481-482 is correct):
--   T1.1 (B1) DATA — the POS terminal rang the product BASE uom (e.g. Brussels PC) instead of its
--     PRICE/SELL uom (SF) because products.sold_by was empty everywhere, so a $9.75/SF paver rang at
--     ~$47.56 instead of ~$195 for 20 SF. Backfill the sell unit from the price unit so the ring
--     selects the price_uom. Idempotent + guarded (only fills empties).
--   T1.2 (B2) INDEX — the "finish the close next morning" SOP leaves yesterday's drawer in COUNTED.
--     uq_till_open_per_drawer (0028:26-27) covered every NON-CLOSED status, so opening today's session
--     on that drawer hit the unique index → errConflict → 409 (unrecoverable). Attach-recovery only
--     re-finds status='OPEN' (repository.go:184-187), so a COUNTED residual could neither be reopened
--     nor re-attached. Narrow the index to status='OPEN': a prior-shift COUNTED session no longer blocks
--     today's open (it stays completable by id), while two OPEN sessions on one drawer are still barred.
--     Safe on existing rows: the old index already guaranteed <=1 non-CLOSED per drawer, hence <=1 OPEN.

-- T1.1 — ring in the price/sell unit (fills every catalog product that has a price_uom but no sold_by).
UPDATE products
   SET sold_by = price_uom
 WHERE (sold_by IS NULL OR sold_by = '')
   AND price_uom IS NOT NULL AND price_uom <> '';

-- T1.2 — a COUNTED drawer from a prior shift must not block today's open.
DROP INDEX IF EXISTS uq_till_open_per_drawer;
CREATE UNIQUE INDEX uq_till_open_per_drawer
  ON till_sessions(branch_id, station) WHERE status = 'OPEN';
