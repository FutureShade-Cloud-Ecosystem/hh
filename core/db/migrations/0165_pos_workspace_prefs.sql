-- 0165: DIB-488 / D-096 — POS workspace presentation preferences, as dealer-configurable DATA.
--
-- Two knobs for the counter workspace, both riding pos_policy (the 0043 pattern: the branch-NULL
-- row is the org-wide — i.e. dealer-level — default, a branch row overrides; cod_enabled precedent).
-- D-096 ruled both dealer-level, so in practice they live on the NULL row; the branch-override
-- machinery comes with the table and costs nothing.
--
--   line_presentation    how the counter presents the ticket's items. 'LINES' is the compact
--                        thumbnail line table (the client's own concept uses lines, which settles
--                        the cards-vs-lines question in favour of lines as the DEFAULT); 'CARDS'
--                        keeps the tap-tile product grid as the primary items surface for dealers
--                        who prefer the tile-first counter.
--   quick_chips_enabled  whether the quick category chip row renders at all. Dealers with very
--                        large catalogues find the row useless, so OFF removes the row entirely —
--                        a configured absence, not an empty state and never an error.
ALTER TABLE pos_policy
  ADD COLUMN line_presentation TEXT NOT NULL DEFAULT 'LINES'
    CHECK (line_presentation IN ('LINES', 'CARDS')),
  ADD COLUMN quick_chips_enabled BOOLEAN NOT NULL DEFAULT TRUE;

COMMENT ON COLUMN pos_policy.line_presentation IS
  'DIB-488/D-096: default items presentation at the counter — LINES (compact thumbnail rows) or CARDS (tap-tile grid).';
COMMENT ON COLUMN pos_policy.quick_chips_enabled IS
  'DIB-488/D-096: whether the POS quick category chip row renders at all (dealer-level toggle).';
