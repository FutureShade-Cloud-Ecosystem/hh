-- 0166: DIB-488 rev 2 — the POS two-panel WORKSPACE layout: dealer defaults + per-user ergonomics.
--
-- The counter page is now two peer panels — INVENTORY LOOKUP and THE SALE — with a draggable
-- divider, per-panel collapse, and a swap-sides arrangement. Two kinds of preference fall out of
-- that, and the client's standing rulings assign each a home:
--
--   DEALER LEVEL (pos_policy — the 0043/0165 pattern; "configure once at dealer level")
--     The DEFAULTS a till opens on: the default split and the default arrangement. These join
--     line_presentation / quick_chips_enabled on pos_policy, flipped through the same admin-gated
--     PUT /api/v1/pos/policy — no new admin surface invented.
--
--   PER USER (pos_user_workspace — NEW, the dashboard_user_tiles/_user_tabs idiom)
--     One counter person's ERGONOMICS: where THEY parked the divider, which panel THEY collapsed,
--     whether THEY swapped the sides. Keyed by user (not terminal, not branch) so the workspace
--     follows the person across tills. The override is WHOLE OR ABSENT, exactly like the dashboard
--     precedent (0140/0149): if a row exists it IS the workspace, and "reset to dealer defaults"
--     is a DELETE — there is no field-by-field merge to unwind.
--
--   NOT PERSISTED AT ALL: temporary maximize. "Temporarily maximized" is a gesture, not a
--   preference — it dies with the session on purpose.
--
--   split_pct is the INVENTORY-LOOKUP panel's share of the workspace width, side-independent
--   (swapping sides does not renumber the split). Bounds 20–80: below 20 a panel is a rail that
--   pretends to be a panel; the collapse affordance owns that posture honestly.

ALTER TABLE pos_policy
  ADD COLUMN workspace_split_pct INTEGER NOT NULL DEFAULT 55
    CHECK (workspace_split_pct BETWEEN 20 AND 80),
  ADD COLUMN workspace_arrangement TEXT NOT NULL DEFAULT 'LOOKUP_LEFT'
    CHECK (workspace_arrangement IN ('LOOKUP_LEFT', 'SALE_LEFT'));

COMMENT ON COLUMN pos_policy.workspace_split_pct IS
  'DIB-488 rev2: dealer-default share (%) of the POS workspace given to the inventory-lookup panel (20-80).';
COMMENT ON COLUMN pos_policy.workspace_arrangement IS
  'DIB-488 rev2: dealer-default panel arrangement — LOOKUP_LEFT (lookup panel on the left) or SALE_LEFT.';

CREATE TABLE IF NOT EXISTS pos_user_workspace (
    user_id          uuid PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    split_pct        integer NOT NULL CHECK (split_pct BETWEEN 20 AND 80),
    arrangement      text NOT NULL CHECK (arrangement IN ('LOOKUP_LEFT', 'SALE_LEFT')),
    lookup_collapsed boolean NOT NULL DEFAULT false,
    sale_collapsed   boolean NOT NULL DEFAULT false,
    -- The one-time idempotency key of the LAST accepted write (settings-write idiom): a client
    -- retrying a save verbatim after a network error replays instead of double-writing.
    write_key        text NOT NULL DEFAULT '',
    updated_at       timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE pos_user_workspace IS
  'DIB-488 rev2: one counter person''s POS workspace ergonomics (split/collapse/swap). Whole-or-absent override of the pos_policy dealer defaults; reset = DELETE (dashboard_user_tiles precedent).';
