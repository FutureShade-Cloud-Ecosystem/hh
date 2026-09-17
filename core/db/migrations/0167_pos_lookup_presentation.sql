-- 0167: DIB-488 rev 3 — the INVENTORY LOOKUP panel's results presentation: CARDS or ROWS.
--
-- Client direction (verbatim intent): "on the inventory side we should be able to toggle between
-- the current card view and more of a listed row view." Same data, same behaviors — search, chips,
-- scan feedback, click-to-price-&-add — only the SHAPE of the results changes: 'CARDS' is the
-- existing product-card grid (unchanged, still the default), 'ROWS' is a dense one-product-per-row
-- list (name · SKU · category tag · list price · per-branch stock inline).
--
-- The preference follows the rev-2 workspace persistence ruling EXACTLY (0166; workspace.go):
--
--   DEALER LEVEL (pos_policy — the 0043/0165/0166 pattern; "configure once at dealer level")
--     lookup_presentation joins line_presentation / workspace_split_pct on pos_policy: the DEFAULT
--     a till opens on, flipped through the same admin-gated PUT /api/v1/pos/policy. No new admin
--     surface invented; validated 422-in-words in the service before the CHECK could 500.
--
--   PER USER (pos_user_workspace — the SAME row, extended)
--     One counter person's ergonomic override rides the existing whole-or-absent workspace row:
--     if a row exists it IS the workspace (this column included — no field merge), and the reset
--     DELETE clears it along with split/collapse/swap. The quick toggle in the lookup panel saves
--     it exactly like a divider drag does.
--
--   Existing pos_user_workspace rows backfill to 'CARDS' (the column default) — the shipped
--   presentation, so nobody's saved workspace changes shape underneath them.

ALTER TABLE pos_policy
  ADD COLUMN lookup_presentation TEXT NOT NULL DEFAULT 'CARDS'
    CHECK (lookup_presentation IN ('CARDS', 'ROWS'));

ALTER TABLE pos_user_workspace
  ADD COLUMN lookup_presentation TEXT NOT NULL DEFAULT 'CARDS'
    CHECK (lookup_presentation IN ('CARDS', 'ROWS'));

COMMENT ON COLUMN pos_policy.lookup_presentation IS
  'DIB-488 rev3: dealer-default presentation of the POS inventory-lookup results — CARDS (product-card grid) or ROWS (dense listed rows).';
COMMENT ON COLUMN pos_user_workspace.lookup_presentation IS
  'DIB-488 rev3: the person''s own lookup-results presentation, riding the whole-or-absent workspace override (reset = DELETE).';
