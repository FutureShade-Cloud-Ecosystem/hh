-- 0149_dashboard_pinned_tabs.sql — DIB-433: the tab strip's role-pinned working set, as DATA.
--
-- The tester's words: "Instead of an entire page for the APPS can we just have each App as a tab at
-- the top to start? We can then go from there maybe and set it up so certain users only have
-- specific tabs to their requirements." Both halves of that sentence are answered here, and neither
-- is answered in code: the strip a person lands on is rows in a table, and "certain users" is the
-- per-user override the second table holds.
--
-- SAME SUBSTRATE AS 0140, DELIBERATELY. These two tables are dashboard_profile_tiles and
-- dashboard_user_tiles with `tab_key` where `tile_id` was. That is not laziness — it is the ruling:
-- a person's pinned tabs curate exactly the way their dashboard tiles curate, so there is ONE set of
-- semantics to learn, one meaning of "reset", and one place (internal/dashboard) that implements
-- both. A second, subtly different curation model for the same user, on the same screen, is how a
-- product teaches people that nothing quite works the way they expect.
--
--   dashboard_profile_tabs   the default strip for a job shape. Edited by an admin.
--   dashboard_user_tabs      one person's override. Deleting these rows IS "reset to role" —
--                            there is no merge to unwind, because the override is WHOLE OR ABSENT
--                            (internal/dashboard/tabs.go, mirroring layout.go).
--
-- WHY `visible` SURVIVES THE COPY. A pin could have been modelled as presence alone: pinned = a row
-- exists. It is not, because then a user who unpins everything stores nothing, and "no rows" already
-- means "use the role default" — their deliberate empty strip would silently become the manager's
-- strip on next login. With `visible`, an unpinned role default is stored as visible=false and the
-- override stays whole: the person gets the empty strip they asked for, and reset still means DELETE.
--
-- WHAT A tab_key IS. The BODY OF THE FROZEN DEEP LINK: 'sales/pos', 'finance', 'yard/yard' — exactly
-- what follows the '#'. Pinning is therefore not a second navigation mechanism; a pinned tab
-- activates through the same parseDeepLink → openUnit → setActiveModule path a bookmark does.
--
-- THE REGISTRY IS THE FRONT END'S, AND ONLY THE FRONT END'S. app/src/dashboard-nav.ts is the single
-- source of truth for which surfaces exist (that is what makes the hash contract enumerable). So the
-- server validates the SHAPE of a tab_key and nothing more, and a key naming a surface this build no
-- longer has is DROPPED SILENTLY BY THE SHELL at render. Teaching Go a second copy of the registry
-- would give the contract two sources, and the copy would rot the first time somebody added a screen
-- without touching the backend. The cost is a typo'd seed key that quietly renders nothing, which is
-- why dashboard-nav.test.mjs reads THIS FILE and asserts every key below resolves to a real surface.

CREATE TABLE IF NOT EXISTS dashboard_profile_tabs (
    profile    text NOT NULL CHECK (profile IN ('counter', 'yard', 'driver', 'manager', 'admin')),
    tab_key    text NOT NULL CHECK (tab_key ~ '^[a-z][a-z0-9_]*(/[a-z][a-z0-9_]*)?$'),
    position   integer NOT NULL CHECK (position >= 0),
    visible    boolean NOT NULL DEFAULT true,
    updated_by uuid REFERENCES users (id),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (profile, tab_key)
);

CREATE INDEX IF NOT EXISTS dashboard_profile_tabs_order_idx
    ON dashboard_profile_tabs (profile, position);

CREATE TABLE IF NOT EXISTS dashboard_user_tabs (
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    tab_key    text NOT NULL CHECK (tab_key ~ '^[a-z][a-z0-9_]*(/[a-z][a-z0-9_]*)?$'),
    position   integer NOT NULL CHECK (position >= 0),
    visible    boolean NOT NULL DEFAULT true,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, tab_key)
);

CREATE INDEX IF NOT EXISTS dashboard_user_tabs_order_idx
    ON dashboard_user_tabs (user_id, position);

-- ── profile → pinned tabs ──────────────────────────────────────────────────────────────────────
--
-- PRIVILEGE IS NOT WORKLOAD. The obvious default — more authority, more tabs — is wrong, and it is
-- wrong in the direction that hurts: it hands the general manager a wall of fourteen tabs they will
-- never click and the counter clerk, who is on three screens all day every day, the launcher.
--
-- So these sets are cut by WHAT THE DAY IS MADE OF:
--
--   counter   the whole day, pinned: the till, the catalogue, the quote pad. Nothing else is
--             within reach of a person with a customer at the desk and the phone ringing.
--   yard      the queue and the truck. Two tabs, because a yard hand on a tablet has two jobs.
--   driver    the run sheet and the radio. The driver's home is normally taken over entirely
--             (0141) — this is the strip behind that takeover on a desktop.
--   manager   the till they cover, the money they chase, the numbers they answer for.
--   admin     THE LEANEST STRIP IN THE BUILDING. A GM's power tools are the dashboard and the
--             launcher, not a permanent wall of every app they happen to have rights to. Home,
--             Reports, Administration, Finance. Four things, one of them free (Home).
--
-- A role with no dashboard_role_profiles row resolves to 'counter' (Service.ProfileFor, 0140) — the
-- least-privileged useful strip, never a blank one and never an admin's by accident.
INSERT INTO dashboard_profile_tabs (profile, tab_key, position, visible) VALUES
    -- counter: the three screens a counter day actually happens on.
    ('counter', 'sales/pos',            0, true),
    ('counter', 'sales/catalogue',      1, true),
    ('counter', 'sales/quotes',         2, true),
    -- yard: the pick queue, and the board that tells them what is leaving.
    ('yard',    'yard/yard',            0, true),
    ('yard',    'dispatch/dispatch',    1, true),
    -- driver: the stops, and the way to say something about them.
    ('driver',  'dispatch/dispatch',    0, true),
    ('driver',  'messaging/messaging',  1, true),
    -- manager: the counter they cover, receivables, and the statements they get asked about.
    ('manager', 'sales/pos',            0, true),
    ('manager', 'finance/ar',           1, true),
    ('manager', 'finance/statements',   2, true),
    -- admin: three. Reports, the admin console, and Finance's own front door.
    ('admin',   'finance/statements',   0, true),
    ('admin',   'admin',                1, true),
    ('admin',   'finance',              2, true)
ON CONFLICT (profile, tab_key) DO NOTHING;
