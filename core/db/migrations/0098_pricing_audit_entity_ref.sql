-- 0098: DIB-402 — entity-reference columns on pricing_audit_log, so an audit row can be
-- clicked through to the RECORD it changed.
--
-- WHY NEW COLUMNS AT ALL — the park reason, restated precisely
-- -----------------------------------------------------------
-- pricing_audit_log already has entity_type + entity_id (0023:171-172). Those name the PRICING
-- CONFIGURATION ROW that was written: a pricing_rules id, a customer_category_overrides id, a
-- quantity_break_headers id, a pricing_category_policy category_id, or — for a BYPASS — whichever
-- of category/product carried the enforcement-disabled flag. None of those is a screen. There is
-- no #<unit>/<module> in this app that renders "pricing rule 3f2a…", so the viewer could not
-- navigate anywhere from a row without GUESSING which business record the id belonged to.
--
-- The genuinely missing thing is therefore a reference to the NAVIGABLE record — the product,
-- customer or category the change lands on. That is what this migration adds. The existing
-- entity_type/entity_id are untouched: they still identify the config row that was written, and
-- the drill-through is a SECOND, explicitly-stored pointer rather than a reinterpretation of the
-- first. Nothing here renames or edits an existing column or index.
--
-- NULL IS A FIRST-CLASS ANSWER. A row whose target cannot be DERIVED (a TIER_DEFAULT rule belongs
-- to no product/customer/category; a BYPASS whose entity_id no longer resolves after a cascade
-- delete) keeps ref_entity_type/ref_entity_id NULL, and the viewer renders it as plain text with
-- no link. An audit log that invents a plausible target is worse than one that admits it has none.
ALTER TABLE pricing_audit_log
  ADD COLUMN ref_entity_type TEXT,
  ADD COLUMN ref_entity_id   UUID;

-- The closed set of things the pricing audit can point AT. Deliberately narrow: these are exactly
-- the record kinds this codebase has a route for today (product -> #sales/catalogue, customer ->
-- #sales/pricing Customer Pricing, category -> #sales/pricing Margin Policy).
--
-- 'quote' and 'order' are NOT in this set and are not written by any code path in this migration's
-- wave. pricing.ResolveInput (internal/pricing/model.go:164) carries customer/product/category/
-- branch and NO document id, so the resolver physically does not know which quote or order it is
-- pricing for. Adding that seam is a resolver-contract change, not an audit-log change. The CHECK
-- is written so widening it later is a one-line ALTER rather than a data migration.
ALTER TABLE pricing_audit_log
  ADD CONSTRAINT pricing_audit_ref_type_known
  CHECK (ref_entity_type IS NULL OR ref_entity_type IN ('product', 'customer', 'category'));

-- Both-or-neither. A type with no id (or an id with no type) is unnavigable and would show up in
-- the viewer as a link that goes nowhere — the exact failure this ticket exists to remove.
ALTER TABLE pricing_audit_log
  ADD CONSTRAINT pricing_audit_ref_pair
  CHECK ((ref_entity_type IS NULL) = (ref_entity_id IS NULL));

-- Partial index: the drill-through viewer only ever filters on rows that HAVE a target.
CREATE INDEX idx_pricing_audit_ref
  ON pricing_audit_log (ref_entity_type, ref_entity_id)
  WHERE ref_entity_type IS NOT NULL;

-- ── BACKFILL ──────────────────────────────────────────────────────────────────────────────────
-- Every statement below derives the target by JOINING the config row the audit entry already
-- names. Nothing is inferred from field_name, old_value or new_value — those are free text, and
-- parsing them would be exactly the string-guessing this ticket forbids. Every UPDATE is
-- idempotent (guarded by ref_entity_type IS NULL) so a re-run is a no-op.
--
-- Coverage is NOT uniform, and the shape of the shortfall is knowable in advance:
--   * pricing_rule       — derivable when the rule carries a product, customer or category.
--                          A TIER_DEFAULT rule (scope='TIER_DEFAULT') carries NONE of the three
--                          by construction, so it backfills to NULL. That is a real, permanent
--                          gap, not a defect.
--   * customer_override  — always derivable (customer_category_overrides.customer_id is NOT NULL).
--   * category_policy    — always derivable (entity_id IS the category_id; PK of the policy table).
--   * quantity_break     — derivable when the header still exists (product XOR category, CHECK-enforced).
--   * margin_enforcement — entity_id is a category_id OR a product_id, and the write site does not
--                          record which (service.go:573-585 picks one). Resolved here by EXISTENCE
--                          against the two tables, which is a fact lookup rather than a guess; a row
--                          whose id resolves in NEITHER (cascade-deleted product/category) stays NULL.
--   * any DELETEd parent — ON DELETE CASCADE removes pricing_rules / quantity_break_headers rows
--                          outright, so audit rows outliving their config row cannot backfill.
-- The measured fraction on a real population is recorded in REPORT-W2-U.md rather than asserted
-- here, because it depends entirely on which rows a given database happens to hold.

-- 1 · pricing_rule → the most specific dimension the rule actually carries.
UPDATE pricing_audit_log a
   SET ref_entity_type = 'product', ref_entity_id = r.product_id
  FROM pricing_rules r
 WHERE a.ref_entity_type IS NULL
   AND a.entity_type = 'pricing_rule'
   AND r.id = a.entity_id
   AND r.product_id IS NOT NULL;

UPDATE pricing_audit_log a
   SET ref_entity_type = 'customer', ref_entity_id = r.customer_id
  FROM pricing_rules r
 WHERE a.ref_entity_type IS NULL
   AND a.entity_type = 'pricing_rule'
   AND r.id = a.entity_id
   AND r.customer_id IS NOT NULL;

UPDATE pricing_audit_log a
   SET ref_entity_type = 'category', ref_entity_id = r.category_id
  FROM pricing_rules r
 WHERE a.ref_entity_type IS NULL
   AND a.entity_type = 'pricing_rule'
   AND r.id = a.entity_id
   AND r.category_id IS NOT NULL;

-- 2 · customer_override → the customer whose pricing moved.
UPDATE pricing_audit_log a
   SET ref_entity_type = 'customer', ref_entity_id = o.customer_id
  FROM customer_category_overrides o
 WHERE a.ref_entity_type IS NULL
   AND a.entity_type = 'customer_override'
   AND o.id = a.entity_id;

-- 3 · category_policy → entity_id already IS the category (pricing_category_policy PK). The join
--     to product_categories is the existence check that keeps a stale id from minting a dead link.
UPDATE pricing_audit_log a
   SET ref_entity_type = 'category', ref_entity_id = a.entity_id
  FROM product_categories c
 WHERE a.ref_entity_type IS NULL
   AND a.entity_type = 'category_policy'
   AND c.id = a.entity_id;

-- 4 · quantity_break → the header's product, else its category (CHECK guarantees one is set).
UPDATE pricing_audit_log a
   SET ref_entity_type = 'product', ref_entity_id = h.product_id
  FROM quantity_break_headers h
 WHERE a.ref_entity_type IS NULL
   AND a.entity_type = 'quantity_break'
   AND h.id = a.entity_id
   AND h.product_id IS NOT NULL;

UPDATE pricing_audit_log a
   SET ref_entity_type = 'category', ref_entity_id = h.category_id
  FROM quantity_break_headers h
 WHERE a.ref_entity_type IS NULL
   AND a.entity_type = 'quantity_break'
   AND h.id = a.entity_id
   AND h.category_id IS NOT NULL;

-- 5 · margin_enforcement (BYPASS) → disambiguate the overloaded entity_id by existence.
--     Product first: the write site prefers the product id when a product policy disabled the gate.
UPDATE pricing_audit_log a
   SET ref_entity_type = 'product', ref_entity_id = a.entity_id
  FROM products p
 WHERE a.ref_entity_type IS NULL
   AND a.entity_type = 'margin_enforcement'
   AND p.id = a.entity_id;

UPDATE pricing_audit_log a
   SET ref_entity_type = 'category', ref_entity_id = a.entity_id
  FROM product_categories c
 WHERE a.ref_entity_type IS NULL
   AND a.entity_type = 'margin_enforcement'
   AND c.id = a.entity_id;

COMMENT ON COLUMN pricing_audit_log.ref_entity_type IS
  'DIB-402 drill-through target kind (product|customer|category), NULL when the row has no derivable navigable record.';
COMMENT ON COLUMN pricing_audit_log.ref_entity_id IS
  'DIB-402 drill-through target id. Intentionally NOT a foreign key: an audit row must survive the deletion of what it describes.';
