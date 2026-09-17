-- 0114: E3 unpark — pricing audit rows can point at the DOCUMENT they were written for.
--
-- THE PARK, RESTATED
-- ------------------
-- 0098 shipped ref_entity_type/ref_entity_id with the CHECK deliberately narrowed to
-- ('product','customer','category'), and said why in its own comment:
--
--     "'quote' and 'order' are NOT in this set ... pricing.ResolveInput carries customer/product/
--      category/branch and NO document id, so the resolver physically does not know which quote or
--      order it is pricing for. Adding that seam is a resolver-contract change, not an audit-log
--      change. The CHECK is written so widening it later is a one-line ALTER rather than a data
--      migration."
--
-- That resolver-contract change is what W4-A does: ResolveInput gains an optional DocRefType /
-- DocRefID pair, and the BYPASS write site prefers it when present. This migration is the one-line
-- ALTER that 0098 promised, plus the bypass-reason column the same write site now fills.
--
-- NO BACKFILL, AND THAT IS CORRECT. Every audit row written before this wave was written by a
-- resolver that did not know its document. There is no join, no derivation and no heuristic that
-- recovers which quote an old BYPASS row belonged to — the information was never captured. Rows
-- written before today keep pointing at the product, which is what they legitimately knew.

-- ── 1 · widen the ref-kind CHECK ──────────────────────────────────────────────────────────────
-- Postgres has no ALTER ... ALTER CONSTRAINT for a CHECK body, so the pair is drop-then-add. Both
-- statements name the constraint that 0098 created, so this fails loudly if 0098 was skipped
-- rather than silently creating a differently-shaped table.
ALTER TABLE pricing_audit_log DROP CONSTRAINT pricing_audit_ref_type_known;
ALTER TABLE pricing_audit_log
  ADD CONSTRAINT pricing_audit_ref_type_known
  CHECK (ref_entity_type IS NULL OR ref_entity_type IN ('product', 'customer', 'category', 'quote', 'order'));

-- ref_entity_id stays UUID and stays WITHOUT a foreign key, for the reason 0098 gave: an audit row
-- must survive the deletion of what it describes. quotes.id and orders.id are both uuid, so the
-- column type already fits — the widening is genuinely only the CHECK.

-- ── 2 · bypass_reason ─────────────────────────────────────────────────────────────────────────
-- A BYPASS row records that the margin gate did not run. Until now it recorded WHICH policy row
-- carried the disable flag (in entity_id) and the resulting margin (in new_value), but not the
-- REASON in a form anyone can filter or count — you had to know that entity_id being a product id
-- meant "product policy" and a category id meant "category policy", which is precisely the kind of
-- inference 0098 spent a page arguing against.
--
-- Overloading old_value was the cheap alternative and was rejected: old_value means "the value
-- before the change" everywhere else in this table, and a reader running a report over the audit
-- log should not have to special-case one action to read one column. A named column costs a
-- nullable TEXT and makes "how many lines bypassed the floor last month, and why" a GROUP BY.
ALTER TABLE pricing_audit_log ADD COLUMN bypass_reason TEXT;

ALTER TABLE pricing_audit_log
  ADD CONSTRAINT pricing_audit_bypass_reason_known
  CHECK (bypass_reason IS NULL OR bypass_reason IN
         ('PRODUCT_POLICY_DISABLED','CATEGORY_POLICY_DISABLED','APPROVED_OVERRIDE'));

-- Only BYPASS rows carry a reason. Without this a CREATE row could acquire one and the GROUP BY
-- above would double-count.
ALTER TABLE pricing_audit_log
  ADD CONSTRAINT pricing_audit_bypass_reason_scope
  CHECK (bypass_reason IS NULL OR action = 'BYPASS');

CREATE INDEX idx_pricing_audit_bypass_reason
  ON pricing_audit_log (bypass_reason, created_at DESC)
  WHERE bypass_reason IS NOT NULL;

COMMENT ON COLUMN pricing_audit_log.bypass_reason IS
  'W4-A: why the margin gate did not run on this line. NULL for every non-BYPASS row (CHECK-enforced).';
