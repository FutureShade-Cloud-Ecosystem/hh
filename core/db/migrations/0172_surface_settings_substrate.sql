-- 0172 — DIB-507 (D-105): the configurable-surfaces settings kit's GENERIC substrate.
--
-- PURPOSE. The ratified hybrid (command/plans/configurable-surfaces-spec.md §4.3) splits screen
-- configuration into two halves: behavior/security-bearing knobs stay TYPED policy columns per
-- module (0173 is the quote pilot's), while PRESENTATION CURATION — which columns/chips/panels/
-- actions a surface shows, in what order — is content-free strings + positions and lives in ONE
-- generic substrate: the 0140/0149 dashboard tiles/tabs idiom, generalized by surface_id.
--
--   surface_role_items      the dealer's default item set per role PROFILE (0140's five job
--                           shapes). Edited by an admin from the surface's Settings page.
--   surface_user_items      one person's override for one surface. WHOLE OR ABSENT per
--                           (user, surface, kind): deleting the rows IS "reset to role default" —
--                           there is no merge to unwind (the 0140/0149 ruling, verbatim).
--   surface_user_workspace  one person's PANEL ERGONOMICS for one surface — split, arrangement,
--                           collapse, cost-hide — the pos_user_workspace (0166/0167/0170) pattern
--                           EXTENDED by surface_id rather than forked: one row per (user, surface),
--                           whole-or-absent, reset = DELETE, write_key idempotent replay.
--
-- Item ids are FRONTEND REGISTRY ids (app/src/quote-surface.ts for the pilot); the server
-- validates SHAPE only (the 0149/0164 ruling — no Go copy of the surface list to drift). An
-- unknown id renders nothing, silently; the per-surface registry test is the drift gate.
-- Ids are add-only: a rename orphans saved curation (0140's rule).

CREATE TABLE surface_role_items (
    surface_id text NOT NULL CHECK (char_length(surface_id) BETWEEN 1 AND 64),
    profile    text NOT NULL CHECK (profile IN ('counter', 'yard', 'driver', 'manager', 'admin')),
    item_kind  text NOT NULL CHECK (item_kind IN ('panel', 'column', 'chip', 'action')),
    item_id    text NOT NULL CHECK (char_length(item_id) BETWEEN 1 AND 64),
    position   integer NOT NULL CHECK (position >= 0),
    visible    boolean NOT NULL DEFAULT true,
    updated_by uuid REFERENCES users (id),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (surface_id, profile, item_kind, item_id)
);

CREATE INDEX surface_role_items_order_idx
    ON surface_role_items (surface_id, profile, item_kind, position);

CREATE TABLE surface_user_items (
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    surface_id text NOT NULL CHECK (char_length(surface_id) BETWEEN 1 AND 64),
    item_kind  text NOT NULL CHECK (item_kind IN ('panel', 'column', 'chip', 'action')),
    item_id    text NOT NULL CHECK (char_length(item_id) BETWEEN 1 AND 64),
    position   integer NOT NULL CHECK (position >= 0),
    visible    boolean NOT NULL DEFAULT true,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, surface_id, item_kind, item_id)
);

CREATE INDEX surface_user_items_order_idx
    ON surface_user_items (user_id, surface_id, item_kind, position);

-- The generalized per-user workspace row. Vocabulary is surface-neutral: PRIMARY is the surface's
-- registry-declared primary panel (the quote builder's `lines`), SECONDARY its counterpart
-- (`summary`). cost_hidden is the 0170 margin_hidden quick-hide generalized — a DISPLAY ergonomic
-- only; the module policy's cost_visibility (0173) owns whether cost data ever leaves the server.
CREATE TABLE surface_user_workspace (
    user_id            uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    surface_id         text NOT NULL CHECK (char_length(surface_id) BETWEEN 1 AND 64),
    split_pct          integer NOT NULL DEFAULT 58 CHECK (split_pct BETWEEN 20 AND 80),
    arrangement        text NOT NULL DEFAULT 'PRIMARY_LEFT'
                         CHECK (arrangement IN ('PRIMARY_LEFT', 'PRIMARY_RIGHT')),
    primary_collapsed  boolean NOT NULL DEFAULT false,
    secondary_collapsed boolean NOT NULL DEFAULT false,
    cost_hidden        boolean NOT NULL DEFAULT false,
    write_key          text NOT NULL DEFAULT '',
    updated_at         timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, surface_id)
);

COMMENT ON TABLE surface_role_items IS
  'DIB-507: dealer-curated default presentation items (panels/columns/chips/actions) per role profile per registered surface. Item ids belong to the frontend registry; server validates shape only.';
COMMENT ON TABLE surface_user_items IS
  'DIB-507: one person''s presentation override for one surface. Whole-or-absent per (user,surface,kind); reset to dealer default = DELETE.';
COMMENT ON TABLE surface_user_workspace IS
  'DIB-507: one person''s panel ergonomics (split/arrangement/collapse/cost-hide) for one surface — pos_user_workspace (0166) generalized by surface_id. Whole-or-absent; reset = DELETE; write_key = idempotent-replay guard.';

-- ── seed: the pilot surface `quotes.builder` (spec Appendix A, Dibbits defaults) ────────────────
-- Every profile opens on the same seeded set for the pilot: the two cost-class columns ship HIDDEN
-- for every profile (visibility of cost DATA is the 0173 policy's job — a curated-visible column
-- still renders empty for a caller the marshal gate refuses). Positions are the concept's order.
INSERT INTO surface_role_items (surface_id, profile, item_kind, item_id, position, visible)
SELECT 'quotes.builder', p.profile, i.item_kind, i.item_id, i.position, i.visible
FROM (VALUES ('counter'), ('yard'), ('driver'), ('manager'), ('admin')) AS p (profile)
CROSS JOIN (VALUES
    -- panels (minimum set lines+summary; addons is dealer-curatable off)
    ('panel',  'lines',           0, true),
    ('panel',  'summary',         1, true),
    ('panel',  'addons',          2, true),
    -- the lines table's columns, in the concept's order
    ('column', 'line.thumb',      0, true),
    ('column', 'line.item',       1, true),
    ('column', 'line.qty',        2, true),
    ('column', 'line.uom',        3, true),
    ('column', 'line.unit_price', 4, true),
    ('column', 'line.disc_pct',   5, true),
    ('column', 'line.total',      6, true),
    ('column', 'line.unit_cost',  7, false),  -- cost class: seeded hidden, server-gated regardless
    ('column', 'line.margin_pct', 8, false),  -- cost class: seeded hidden, server-gated regardless
    -- the action bar
    ('action', 'quote.save_draft',    0, true),
    ('action', 'quote.preview',       1, true),
    ('action', 'quote.print',         2, true),
    ('action', 'quote.copy_link',     3, true),
    ('action', 'quote.send',          4, true),
    ('action', 'quote.convert_order', 5, true)
) AS i (item_kind, item_id, position, visible)
ON CONFLICT (surface_id, profile, item_kind, item_id) DO NOTHING;
-- Chips are deliberately NOT seeded as rows: the chip row is `favorites` + the 0173
-- quote_policy.quick_chip_set curation ('' = derive from top-selling categories, the 0170 posture).
