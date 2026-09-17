-- 0140_dashboard_layouts.sql — DIB-353: the cockpit's role-based tile catalog + per-user overrides.
--
-- THREE TABLES, ONE IDEA: what a person sees on the home screen is DATA, not a switch statement.
--
--   dashboard_role_profiles   role code → one of five job shapes. Adding the 15th role means
--                             inserting a row, not shipping a release.
--   dashboard_profile_tiles   the default layout for a job shape. Edited by an admin.
--   dashboard_user_tiles      one person's override. Deleting these rows IS "reset to role" —
--                             there is no merge to unwind, which is why the resolver treats a
--                             user override as whole-or-absent (internal/dashboard/layout.go).
--
-- Tile ids are the strings in internal/dashboard/model.go. They are the primary key of every row
-- here, so a rename orphans saved layouts: add tiles, never rename them.

CREATE TABLE IF NOT EXISTS dashboard_role_profiles (
    role_code  citext PRIMARY KEY REFERENCES roles (code) ON DELETE CASCADE,
    profile    text NOT NULL CHECK (profile IN ('counter', 'yard', 'driver', 'manager', 'admin')),
    updated_by uuid REFERENCES users (id),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS dashboard_profile_tiles (
    profile    text NOT NULL CHECK (profile IN ('counter', 'yard', 'driver', 'manager', 'admin')),
    tile_id    text NOT NULL,
    position   integer NOT NULL CHECK (position >= 0),
    visible    boolean NOT NULL DEFAULT true,
    updated_by uuid REFERENCES users (id),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (profile, tile_id)
);

CREATE INDEX IF NOT EXISTS dashboard_profile_tiles_order_idx
    ON dashboard_profile_tiles (profile, position);

CREATE TABLE IF NOT EXISTS dashboard_user_tiles (
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    tile_id    text NOT NULL,
    position   integer NOT NULL CHECK (position >= 0),
    visible    boolean NOT NULL DEFAULT true,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, tile_id)
);

CREATE INDEX IF NOT EXISTS dashboard_user_tiles_order_idx
    ON dashboard_user_tiles (user_id, position);

-- ── role → profile ─────────────────────────────────────────────────────────────────────────────
-- The five shapes over the fourteen shipped roles. A role added later with no row here resolves to
-- 'counter' in code (Service.ProfileFor) — the least-privileged useful home, never a blank page
-- and never a manager's view by accident.
INSERT INTO dashboard_role_profiles (role_code, profile) VALUES
    ('INSIDE_SALES',         'counter'),
    ('OUTSIDE_SALES',        'counter'),
    ('YARD_TEAM',            'yard'),
    ('YARD_MANAGER',         'yard'),
    ('DRIVERS',              'driver'),
    ('BRANCH_MANAGER',       'manager'),
    ('SALES_MANAGER',        'manager'),
    ('LOGISTICS_MANAGER',    'manager'),
    ('PROCUREMENT_MANAGER',  'manager'),
    ('PAYABLES_RECEIVABLES', 'manager'),
    ('FINANCIAL_CONTROLLER', 'manager'),
    ('GENERAL_MANAGER',      'admin'),
    ('SUPER_ADMIN',          'admin'),
    ('HR',                   'admin')
ON CONFLICT (role_code) DO NOTHING;

-- ── profile → tiles ────────────────────────────────────────────────────────────────────────────
-- Each profile gets the numbers its day is made of, in the order it reads them. Everything else in
-- the catalog is appended hidden by the resolver, so an admin can switch a tile on without a
-- migration and a person can hide one without asking.
INSERT INTO dashboard_profile_tiles (profile, tile_id, position, visible) VALUES
    -- counter: the phone is ringing and someone is at the desk.
    ('counter', 'sales.today',          0, true),
    ('counter', 'orders.open',          1, true),
    ('counter', 'messaging.unread',     2, true),
    ('counter', 'inventory.low_stock',  3, true),
    ('counter', 'deliveries.today',     4, true),
    -- yard: the queue, then what is stuck in it.
    ('yard',    'picks.queue',          0, true),
    ('yard',    'picks.stale',          1, true),
    ('yard',    'weight.pending',       2, true),
    ('yard',    'deliveries.today',     3, true),
    ('yard',    'messaging.unread',     4, true),
    -- driver: the day's stops and the radio. A driver's home is normally taken over entirely
    -- (0141) — these are what they see when they open the cockpit on a desktop.
    ('driver',  'deliveries.today',     0, true),
    ('driver',  'messaging.unread',     1, true),
    -- manager: money first, then the two queues that turn into complaints.
    ('manager', 'sales.today',          0, true),
    ('manager', 'ar.aging',             1, true),
    ('manager', 'orders.open',          2, true),
    ('manager', 'deliveries.today',     3, true),
    ('manager', 'picks.queue',          4, true),
    ('manager', 'warranty.open_claims', 5, true),
    ('manager', 'inventory.low_stock',  6, true),
    ('manager', 'messaging.unread',     7, true),
    -- admin: everything, because the person who wires it is the person who checks it.
    ('admin',   'sales.today',          0, true),
    ('admin',   'ar.aging',             1, true),
    ('admin',   'orders.open',          2, true),
    ('admin',   'deliveries.today',     3, true),
    ('admin',   'picks.queue',          4, true),
    ('admin',   'picks.stale',          5, true),
    ('admin',   'weight.pending',       6, true),
    ('admin',   'warranty.open_claims', 7, true),
    ('admin',   'inventory.low_stock',  8, true),
    ('admin',   'messaging.unread',     9, true)
ON CONFLICT (profile, tile_id) DO NOTHING;
