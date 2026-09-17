-- 0170 — DIB-488 rev 8: margin visibility (dealer config + per-user quick-hide), the curated
-- quick-chip set, and the two JOB fields on the counter sale.
--
-- ── pos_policy.margin_visibility — WHO may receive margin/profit in a payload ────────────────────
-- The commander's critical gating ruling: per-line Margin % and the footer Margin/Est. Profit are
-- computed SERVER-SIDE and must be gated at the MARSHAL layer — the fields are present in the
-- response ONLY for roles the dealer allows. This column is the dealer-level switch (the
-- cod_enabled / line_presentation idiom — flipped through the existing admin PUT /api/v1/pos/policy,
-- validated 422-in-words in the service before this CHECK could 500):
--
--   MANAGER_UP  (default) Tier-1/Tier-2 staff (identity.StaffContext.IsManager) receive the fields;
--               frontline callers receive a payload with the fields BYTE-ABSENT.
--   ALL_STAFF   every staff caller receives them.
--   HIDDEN      nobody does — the dealer has switched the surface off entirely.
--
-- ── pos_policy.quick_chip_set — the curated single-row chip set (rev-8 item 4) ──────────────────
-- Comma-separated products.application values the dealer curates for the lookup panel's short chip
-- row ('' = not curated yet → the service derives the set from the TOP-SELLING categories of
-- committed sales, so the demo opens on an honest data-driven default until the client curates).
-- Same dealer-settings idiom as every other pos_policy column.
--
-- ── pos_user_workspace.margin_hidden — the one-click quick-hide (eye) toggle ────────────────────
-- Counter privacy: a manager serving a customer can hide the margin column with one click. A
-- per-PERSON ergonomic exactly like the divider position, so it rides the SAME whole-or-absent
-- workspace row (0166/0167 precedent; merge-save contract per rev 6 item G). Hiding is display
-- only — the ROLE gate above decides whether the data ever leaves the server.
--
-- ── orders.job_address / orders.job_reference — rev-8 item 11 ───────────────────────────────────
-- The two JOB fields joining po_job_ref / salesperson_id (0168) under More options, same
-- persistence pattern: on the ORDER (every downstream reader reads orders), optional free text,
-- length-capped, validated 422-in-words in the service BEFORE these CHECKs could 500, carried to
-- the committed order + the audit Extra + the sold-panel facts, replayed verbatim.

ALTER TABLE pos_policy
  ADD COLUMN margin_visibility TEXT NOT NULL DEFAULT 'MANAGER_UP'
    CHECK (margin_visibility IN ('MANAGER_UP', 'ALL_STAFF', 'HIDDEN')),
  ADD COLUMN quick_chip_set TEXT NOT NULL DEFAULT '';

COMMENT ON COLUMN pos_policy.margin_visibility IS
  'DIB-488 rev8: which staff tiers receive margin/est-profit fields in POS payloads (marshal-gated server-side). MANAGER_UP (default) | ALL_STAFF | HIDDEN.';
COMMENT ON COLUMN pos_policy.quick_chip_set IS
  'DIB-488 rev8: comma-separated curated category (products.application) chip set for the lookup panel; empty = derive from top-selling categories.';

ALTER TABLE pos_user_workspace
  ADD COLUMN margin_hidden boolean NOT NULL DEFAULT false;

COMMENT ON COLUMN pos_user_workspace.margin_hidden IS
  'DIB-488 rev8: the counter-privacy quick-hide (eye) toggle for the margin column — per-user display ergonomic; the role gate on pos_policy.margin_visibility owns whether the data is ever sent.';

ALTER TABLE orders ADD COLUMN job_address TEXT NOT NULL DEFAULT '';
ALTER TABLE orders ADD CONSTRAINT ck_orders_job_address_len CHECK (char_length(job_address) <= 200);
ALTER TABLE orders ADD COLUMN job_reference TEXT NOT NULL DEFAULT '';
ALTER TABLE orders ADD CONSTRAINT ck_orders_job_reference_len CHECK (char_length(job_reference) <= 64);
