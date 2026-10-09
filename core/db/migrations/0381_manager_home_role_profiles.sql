-- 0381_manager_home_role_profiles.sql ── one Manager Home, one profile row per manager role.
--
-- THE DECISION THIS CARRIES (the principal's item 4, decision 5, recommended A): one Manager Home
-- app whose landing (needs you, widgets, tabs) is a per role profile in the dashboard's existing
-- profile tables, seeded from the role matrix. Until now the profiles grouped six manager roles
-- into one "manager" shape (0140), the yard manager sat on the yard team's "yard" shape, and the
-- general manager on "admin": one Home for seven different jobs, curatable only by giving every
-- manager the same screen.
--
-- WHAT CHANGES AND WHAT DELIBERATELY DOES NOT:
--
--   The five job shapes stay the five job shapes. counter, yard, driver, manager and admin remain
--   the vocabulary every non-manager role resolves to, and every row they carry today is untouched.
--   What this adds is eight REFINEMENTS, named manager.<role>, one per manager role the matrix
--   files under manager-home (default.json: work or manage): branch, sales, yard, logistics,
--   procurement, payables and receivables, financial controller, general manager.
--
--   Each manager profile is BORN FROM ITS BASE SHAPE, whole: the tiles, the pinned tabs, the
--   widgets and the Needs-you policy are copied, so the morning this lands on, no manager's screen
--   changes by one pixel. What changes is who curates it: the branch manager's Home can now diverge
--   from the controller's by editing rows, which is the entire point of the 0140 substrate ("what a
--   person sees is DATA, not a switch statement").
--
--   THE YARD MANAGER IS THE EXCEPTION, because the roadmap gives them the first per-role landing:
--   their widgets are the picks board and the deliveries by branch bars (0201's catalog) and their Needs-you
--   holds open shortages raised by the yard team, stale picks, and receiving differences. The
--   seeded source keys tasks.open_shortages and purchasing.receiving_differences name sources whose
--   adapters land with the sources themselves; until then the rows render nothing, silently, the
--   0172 posture a key this build does not have always had.
--
--   0203'S DISPATCHER RULING KEEPS ITS SUBSTANCE. The logistics manager was re-filed onto the dock
--   ("yard") by 0203 so a dispatcher would not open on the controller's Home. Their manager profile
--   is born from the dock, not from "manager", so they still lead with the day's deliveries, keep
--   the pick board, and keep the dock's tabs. The per-role filing changes WHO curates that Home,
--   not what it says.
--
--   NO HOME ROWS ARE SEEDED for the manager profiles, and that is the decision working: a profile
--   with no dashboard_profile_home row lands on the dashboard Home (0141's default for every desk
--   role), and the Manager Home IS the landing. A takeover would skip the screen this migration
--   exists to give each manager their own row in.
--
-- RE-FILING, SAID PLAINLY. 0201 warned that a widgets migration must not silently re-file a role,
-- because it would change somebody's screen as a side effect. This migration re-files eight roles
-- ON PURPOSE, with the copies above holding every screen byte-identical, because the principal's
-- decision is exactly that re-filing: the role to shape mapping moves from six roles sharing one
-- shape to one shape per role. Nothing else about their layouts moves with it.
--
-- REVERT COST: eight UPDATEs to undo the re-filing, plus DELETEs of the manager.* rows in five
-- tables. The CHECK widenings can stay widened; they gate a vocabulary, not a population.

-- ── §1 · the vocabulary: the profile CHECK in every profile-keyed table ────────────────────────
--
-- Nine constraints over eight tables carry 0140's five-value list. Each is widened to the same
-- thirteen values, the five job shapes plus the eight manager refinements, so no substrate refuses
-- a manager row its sibling substrates accept (a gallery an admin cannot save for a manager whose
-- widgets and needs rows name the same profile would be a half-open profile).
ALTER TABLE dashboard_role_profiles         DROP CONSTRAINT dashboard_role_profiles_profile_check;
ALTER TABLE dashboard_role_profiles         ADD CONSTRAINT dashboard_role_profiles_profile_check
    CHECK (profile IN ('counter', 'yard', 'driver', 'manager', 'admin',
                       'manager.branch', 'manager.sales', 'manager.yard', 'manager.logistics',
                       'manager.procurement', 'manager.payables', 'manager.controller',
                       'manager.general'));

ALTER TABLE dashboard_profile_tiles         DROP CONSTRAINT dashboard_profile_tiles_profile_check;
ALTER TABLE dashboard_profile_tiles         ADD CONSTRAINT dashboard_profile_tiles_profile_check
    CHECK (profile IN ('counter', 'yard', 'driver', 'manager', 'admin',
                       'manager.branch', 'manager.sales', 'manager.yard', 'manager.logistics',
                       'manager.procurement', 'manager.payables', 'manager.controller',
                       'manager.general'));

ALTER TABLE dashboard_profile_home          DROP CONSTRAINT dashboard_profile_home_profile_check;
ALTER TABLE dashboard_profile_home          ADD CONSTRAINT dashboard_profile_home_profile_check
    CHECK (profile IN ('counter', 'yard', 'driver', 'manager', 'admin',
                       'manager.branch', 'manager.sales', 'manager.yard', 'manager.logistics',
                       'manager.procurement', 'manager.payables', 'manager.controller',
                       'manager.general'));

ALTER TABLE dashboard_profile_tabs          DROP CONSTRAINT dashboard_profile_tabs_profile_check;
ALTER TABLE dashboard_profile_tabs          ADD CONSTRAINT dashboard_profile_tabs_profile_check
    CHECK (profile IN ('counter', 'yard', 'driver', 'manager', 'admin',
                       'manager.branch', 'manager.sales', 'manager.yard', 'manager.logistics',
                       'manager.procurement', 'manager.payables', 'manager.controller',
                       'manager.general'));

ALTER TABLE quick_action_profile_visibility DROP CONSTRAINT quick_action_profile_visibility_profile_check;
ALTER TABLE quick_action_profile_visibility ADD CONSTRAINT quick_action_profile_visibility_profile_check
    CHECK (profile IN ('counter', 'yard', 'driver', 'manager', 'admin',
                       'manager.branch', 'manager.sales', 'manager.yard', 'manager.logistics',
                       'manager.procurement', 'manager.payables', 'manager.controller',
                       'manager.general'));

ALTER TABLE surface_role_items              DROP CONSTRAINT surface_role_items_profile_check;
ALTER TABLE surface_role_items              ADD CONSTRAINT surface_role_items_profile_check
    CHECK (profile IN ('counter', 'yard', 'driver', 'manager', 'admin',
                       'manager.branch', 'manager.sales', 'manager.yard', 'manager.logistics',
                       'manager.procurement', 'manager.payables', 'manager.controller',
                       'manager.general'));

ALTER TABLE dashboard_profile_widgets       DROP CONSTRAINT dashboard_profile_widgets_profile_check;
ALTER TABLE dashboard_profile_widgets       ADD CONSTRAINT dashboard_profile_widgets_profile_check
    CHECK (profile IN ('counter', 'yard', 'driver', 'manager', 'admin',
                       'manager.branch', 'manager.sales', 'manager.yard', 'manager.logistics',
                       'manager.procurement', 'manager.payables', 'manager.controller',
                       'manager.general'));

ALTER TABLE dashboard_needs_sources         DROP CONSTRAINT dashboard_needs_sources_profile_check;
ALTER TABLE dashboard_needs_sources         ADD CONSTRAINT dashboard_needs_sources_profile_check
    CHECK (profile IN ('counter', 'yard', 'driver', 'manager', 'admin',
                       'manager.branch', 'manager.sales', 'manager.yard', 'manager.logistics',
                       'manager.procurement', 'manager.payables', 'manager.controller',
                       'manager.general'));

ALTER TABLE dashboard_gallery_entries       DROP CONSTRAINT dashboard_gallery_entries_profile_check;
ALTER TABLE dashboard_gallery_entries       ADD CONSTRAINT dashboard_gallery_entries_profile_check
    CHECK (profile IN ('counter', 'yard', 'driver', 'manager', 'admin',
                       'manager.branch', 'manager.sales', 'manager.yard', 'manager.logistics',
                       'manager.procurement', 'manager.payables', 'manager.controller',
                       'manager.general'));

-- ── §2 · the role re-filing ─────────────────────────────────────────────────────────────────────
--
-- Eight UPDATEs, one per manager role, each to its own profile. The role codes are 0140's ERP
-- vocabulary; the grouping follows the matrix (default.json) and 0203's dispatcher ruling: the six
-- roles 0140 shared as "manager" branch out from "manager", the general manager branches out from
-- "admin", the logistics manager branches out from the dock ("yard") they were ruled onto, and the
-- yard manager keeps the yard's TILES and TABS (their cockpit) while their widget Home and Needs-you
-- become their own in §4.
UPDATE dashboard_role_profiles SET profile = 'manager.branch',    updated_at = now() WHERE role_code = 'BRANCH_MANAGER';
UPDATE dashboard_role_profiles SET profile = 'manager.sales',     updated_at = now() WHERE role_code = 'SALES_MANAGER';
UPDATE dashboard_role_profiles SET profile = 'manager.yard',      updated_at = now() WHERE role_code = 'YARD_MANAGER';
UPDATE dashboard_role_profiles SET profile = 'manager.logistics', updated_at = now() WHERE role_code = 'LOGISTICS_MANAGER';
UPDATE dashboard_role_profiles SET profile = 'manager.procurement', updated_at = now() WHERE role_code = 'PROCUREMENT_MANAGER';
UPDATE dashboard_role_profiles SET profile = 'manager.payables',  updated_at = now() WHERE role_code = 'PAYABLES_RECEIVABLES';
UPDATE dashboard_role_profiles SET profile = 'manager.controller', updated_at = now() WHERE role_code = 'FINANCIAL_CONTROLLER';
UPDATE dashboard_role_profiles SET profile = 'manager.general',   updated_at = now() WHERE role_code = 'GENERAL_MANAGER';

-- ── §3 · the birth copies: tiles and tabs, whole ────────────────────────────────────────────────
--
-- INSERT ... SELECT from the base shape, so a later edit to the base shape does not silently reach
-- profiles that were born from it (the copy is a one-time act, like 0202's carry-over, not a live
-- inheritance: a manager's Home diverges by editing rows, and a base-shape edit reaching into it
-- afterwards would be the one-way door 0191 closed). ON CONFLICT DO NOTHING keeps a re-run honest.
INSERT INTO dashboard_profile_tiles (profile, tile_id, position, visible)
SELECT 'manager.branch', tile_id, position, visible FROM dashboard_profile_tiles WHERE profile = 'manager'
ON CONFLICT (profile, tile_id) DO NOTHING;
INSERT INTO dashboard_profile_tiles (profile, tile_id, position, visible)
SELECT 'manager.sales', tile_id, position, visible FROM dashboard_profile_tiles WHERE profile = 'manager'
ON CONFLICT (profile, tile_id) DO NOTHING;
INSERT INTO dashboard_profile_tiles (profile, tile_id, position, visible)
SELECT 'manager.yard', tile_id, position, visible FROM dashboard_profile_tiles WHERE profile = 'yard'
ON CONFLICT (profile, tile_id) DO NOTHING;
INSERT INTO dashboard_profile_tiles (profile, tile_id, position, visible)
SELECT 'manager.logistics', tile_id, position, visible FROM dashboard_profile_tiles WHERE profile = 'yard'
ON CONFLICT (profile, tile_id) DO NOTHING;
INSERT INTO dashboard_profile_tiles (profile, tile_id, position, visible)
SELECT 'manager.procurement', tile_id, position, visible FROM dashboard_profile_tiles WHERE profile = 'manager'
ON CONFLICT (profile, tile_id) DO NOTHING;
INSERT INTO dashboard_profile_tiles (profile, tile_id, position, visible)
SELECT 'manager.payables', tile_id, position, visible FROM dashboard_profile_tiles WHERE profile = 'manager'
ON CONFLICT (profile, tile_id) DO NOTHING;
INSERT INTO dashboard_profile_tiles (profile, tile_id, position, visible)
SELECT 'manager.controller', tile_id, position, visible FROM dashboard_profile_tiles WHERE profile = 'manager'
ON CONFLICT (profile, tile_id) DO NOTHING;
INSERT INTO dashboard_profile_tiles (profile, tile_id, position, visible)
SELECT 'manager.general', tile_id, position, visible FROM dashboard_profile_tiles WHERE profile = 'admin'
ON CONFLICT (profile, tile_id) DO NOTHING;

INSERT INTO dashboard_profile_tabs (profile, tab_key, position, visible)
SELECT 'manager.branch', tab_key, position, visible FROM dashboard_profile_tabs WHERE profile = 'manager'
ON CONFLICT (profile, tab_key) DO NOTHING;
INSERT INTO dashboard_profile_tabs (profile, tab_key, position, visible)
SELECT 'manager.sales', tab_key, position, visible FROM dashboard_profile_tabs WHERE profile = 'manager'
ON CONFLICT (profile, tab_key) DO NOTHING;
INSERT INTO dashboard_profile_tabs (profile, tab_key, position, visible)
SELECT 'manager.yard', tab_key, position, visible FROM dashboard_profile_tabs WHERE profile = 'yard'
ON CONFLICT (profile, tab_key) DO NOTHING;
INSERT INTO dashboard_profile_tabs (profile, tab_key, position, visible)
SELECT 'manager.logistics', tab_key, position, visible FROM dashboard_profile_tabs WHERE profile = 'yard'
ON CONFLICT (profile, tab_key) DO NOTHING;
INSERT INTO dashboard_profile_tabs (profile, tab_key, position, visible)
SELECT 'manager.procurement', tab_key, position, visible FROM dashboard_profile_tabs WHERE profile = 'manager'
ON CONFLICT (profile, tab_key) DO NOTHING;
INSERT INTO dashboard_profile_tabs (profile, tab_key, position, visible)
SELECT 'manager.payables', tab_key, position, visible FROM dashboard_profile_tabs WHERE profile = 'manager'
ON CONFLICT (profile, tab_key) DO NOTHING;
INSERT INTO dashboard_profile_tabs (profile, tab_key, position, visible)
SELECT 'manager.controller', tab_key, position, visible FROM dashboard_profile_tabs WHERE profile = 'manager'
ON CONFLICT (profile, tab_key) DO NOTHING;
INSERT INTO dashboard_profile_tabs (profile, tab_key, position, visible)
SELECT 'manager.general', tab_key, position, visible FROM dashboard_profile_tabs WHERE profile = 'admin'
ON CONFLICT (profile, tab_key) DO NOTHING;

-- ── §4 · the widget Homes ────────────────────────────────────────────────────────────────────────
--
-- Seven births from the base shape (the whole bento, position for position) and one first per-role
-- landing. Every measure named below is a real catalog id offering exactly that shape, split and
-- window in this build (the Go drift gate reads 0201 as 0205 leaves it; this file's rows are pinned
-- by the integration gate the same way, so a seeded row the write gate refuses cannot land).
INSERT INTO dashboard_profile_widgets
    (profile, widget_key, shape, measure, split, time_range, size, title, position, visible)
SELECT 'manager.branch', widget_key, shape, measure, split, time_range, size, title, position, visible
  FROM dashboard_profile_widgets WHERE profile = 'manager'
ON CONFLICT (profile, widget_key) DO NOTHING;
INSERT INTO dashboard_profile_widgets
    (profile, widget_key, shape, measure, split, time_range, size, title, position, visible)
SELECT 'manager.sales', widget_key, shape, measure, split, time_range, size, title, position, visible
  FROM dashboard_profile_widgets WHERE profile = 'manager'
ON CONFLICT (profile, widget_key) DO NOTHING;
INSERT INTO dashboard_profile_widgets
    (profile, widget_key, shape, measure, split, time_range, size, title, position, visible)
SELECT 'manager.logistics', widget_key, shape, measure, split, time_range, size, title, position, visible
  FROM dashboard_profile_widgets WHERE profile = 'yard'
ON CONFLICT (profile, widget_key) DO NOTHING;
INSERT INTO dashboard_profile_widgets
    (profile, widget_key, shape, measure, split, time_range, size, title, position, visible)
SELECT 'manager.procurement', widget_key, shape, measure, split, time_range, size, title, position, visible
  FROM dashboard_profile_widgets WHERE profile = 'manager'
ON CONFLICT (profile, widget_key) DO NOTHING;
INSERT INTO dashboard_profile_widgets
    (profile, widget_key, shape, measure, split, time_range, size, title, position, visible)
SELECT 'manager.payables', widget_key, shape, measure, split, time_range, size, title, position, visible
  FROM dashboard_profile_widgets WHERE profile = 'manager'
ON CONFLICT (profile, widget_key) DO NOTHING;
INSERT INTO dashboard_profile_widgets
    (profile, widget_key, shape, measure, split, time_range, size, title, position, visible)
SELECT 'manager.controller', widget_key, shape, measure, split, time_range, size, title, position, visible
  FROM dashboard_profile_widgets WHERE profile = 'manager'
ON CONFLICT (profile, widget_key) DO NOTHING;
INSERT INTO dashboard_profile_widgets
    (profile, widget_key, shape, measure, split, time_range, size, title, position, visible)
SELECT 'manager.general', widget_key, shape, measure, split, time_range, size, title, position, visible
  FROM dashboard_profile_widgets WHERE profile = 'admin'
ON CONFLICT (profile, widget_key) DO NOTHING;

-- The yard manager's own, the list its board draws (design-refs role-boards, yard manager): the day
-- going out is the hero, Needs-you beside it, the pick queue as 0201's board, the deliveries split BY
-- BRANCH as bars (the board draws the branch split, and the hero already says the day's count, so a
-- second status board of deliveries would repeat it), the queue depth gauge, open orders and open
-- claims. The board's "Unread messages" tile has no measure in 0201's catalog; it waits for the UI
-- item. picks.queue's one window is `open` (0205's truth: the queue is a status set, not a period),
-- deliveries.today's is `today`.
INSERT INTO dashboard_profile_widgets
    (profile, widget_key, shape, measure, split, time_range, size, title, position, visible) VALUES
    ('manager.yard', 'hero.deliveries',   'stat',  'deliveries.today',     'none',   'today', 'L', 'Deliveries today', 0, true),
    ('manager.yard', 'needs.you',         'list',  'needs.you',            'none',   'open',  'L', 'Needs you',        1, true),
    ('manager.yard', 'picks.board',       'board', 'picks.queue',          'status', 'open',  'L', 'Pick queue',       2, true),
    ('manager.yard', 'deliveries.branch', 'bars',  'deliveries.today',     'branch', 'today', 'M', 'By branch',        3, true),
    ('manager.yard', 'picks.gauge',       'gauge', 'picks.queue',          'none',   'open',  'S', 'Queue depth',      4, true),
    ('manager.yard', 'orders.open',       'stat',  'orders.open',          'none',   'open',  'S', 'Open orders',      5, true),
    ('manager.yard', 'warranty.open',     'stat',  'warranty.open_claims', 'none',   'open',  'S', 'Open claims',      6, true)
ON CONFLICT (profile, widget_key) DO NOTHING;

-- ── §5 · the Needs-you policy ────────────────────────────────────────────────────────────────────
--
-- Seven births from the base shape, and the yard manager's three sources (roadmap 2.3: open
-- shortages raised by the yard team, stale picks, receiving differences). The edges follow the
-- yard shape's own for the source they share (stale picks: warn at 1, stop at 5, stale after 60
-- minutes); the two new sources carry count edges only, because their ages ride their adapters
-- (a shortage is as old as its report, a receiving difference has no single clock).
INSERT INTO dashboard_needs_sources
    (profile, source_key, position, visible, warn_at, stop_at, stale_after_minutes)
SELECT 'manager.branch', source_key, position, visible, warn_at, stop_at, stale_after_minutes
  FROM dashboard_needs_sources WHERE profile = 'manager'
ON CONFLICT (profile, source_key) DO NOTHING;
INSERT INTO dashboard_needs_sources
    (profile, source_key, position, visible, warn_at, stop_at, stale_after_minutes)
SELECT 'manager.sales', source_key, position, visible, warn_at, stop_at, stale_after_minutes
  FROM dashboard_needs_sources WHERE profile = 'manager'
ON CONFLICT (profile, source_key) DO NOTHING;
INSERT INTO dashboard_needs_sources
    (profile, source_key, position, visible, warn_at, stop_at, stale_after_minutes)
SELECT 'manager.logistics', source_key, position, visible, warn_at, stop_at, stale_after_minutes
  FROM dashboard_needs_sources WHERE profile = 'yard'
ON CONFLICT (profile, source_key) DO NOTHING;
INSERT INTO dashboard_needs_sources
    (profile, source_key, position, visible, warn_at, stop_at, stale_after_minutes)
SELECT 'manager.procurement', source_key, position, visible, warn_at, stop_at, stale_after_minutes
  FROM dashboard_needs_sources WHERE profile = 'manager'
ON CONFLICT (profile, source_key) DO NOTHING;
INSERT INTO dashboard_needs_sources
    (profile, source_key, position, visible, warn_at, stop_at, stale_after_minutes)
SELECT 'manager.payables', source_key, position, visible, warn_at, stop_at, stale_after_minutes
  FROM dashboard_needs_sources WHERE profile = 'manager'
ON CONFLICT (profile, source_key) DO NOTHING;
INSERT INTO dashboard_needs_sources
    (profile, source_key, position, visible, warn_at, stop_at, stale_after_minutes)
SELECT 'manager.controller', source_key, position, visible, warn_at, stop_at, stale_after_minutes
  FROM dashboard_needs_sources WHERE profile = 'manager'
ON CONFLICT (profile, source_key) DO NOTHING;
INSERT INTO dashboard_needs_sources
    (profile, source_key, position, visible, warn_at, stop_at, stale_after_minutes)
SELECT 'manager.general', source_key, position, visible, warn_at, stop_at, stale_after_minutes
  FROM dashboard_needs_sources WHERE profile = 'admin'
ON CONFLICT (profile, source_key) DO NOTHING;

INSERT INTO dashboard_needs_sources
    (profile, source_key, position, visible, warn_at, stop_at, stale_after_minutes) VALUES
    ('manager.yard', 'tasks.open_shortages',           0, true, 1, 5, 0),
    ('manager.yard', 'tasks.stale_picks',              1, true, 1, 5, 60),
    ('manager.yard', 'purchasing.receiving_differences', 2, true, 1, 3, 0)
ON CONFLICT (profile, source_key) DO NOTHING;

-- ── §6 · the remaining profile-keyed tables, born whole ─────────────────────────────────────────
--
-- Three more tables key on the profile and are READ by it: the per surface item curation (the
-- columns, chips and panels a screen offers), the quick action matrix and the widget gallery. A
-- manager profile with no rows there would open every screen with its curated columns gone and the
-- + Sale button off, which is the changed screen §3 promises nobody gets. One mapping, eight rows,
-- read by all three copies; the base shapes are the same as §3's.
INSERT INTO surface_role_items (surface_id, profile, item_kind, item_id, position, visible)
SELECT s.surface_id, m.profile, s.item_kind, s.item_id, s.position, s.visible
  FROM (VALUES ('manager.branch', 'manager'), ('manager.sales', 'manager'),
               ('manager.yard', 'yard'), ('manager.logistics', 'yard'),
               ('manager.procurement', 'manager'), ('manager.payables', 'manager'),
               ('manager.controller', 'manager'), ('manager.general', 'admin')) AS m (profile, base)
  JOIN surface_role_items s ON s.profile = m.base
ON CONFLICT (surface_id, profile, item_kind, item_id) DO NOTHING;

INSERT INTO quick_action_profile_visibility (action_id, profile, visible)
SELECT q.action_id, m.profile, q.visible
  FROM (VALUES ('manager.branch', 'manager'), ('manager.sales', 'manager'),
               ('manager.yard', 'yard'), ('manager.logistics', 'yard'),
               ('manager.procurement', 'manager'), ('manager.payables', 'manager'),
               ('manager.controller', 'manager'), ('manager.general', 'admin')) AS m (profile, base)
  JOIN quick_action_profile_visibility q ON q.profile = m.base
ON CONFLICT (action_id, profile) DO NOTHING;

INSERT INTO dashboard_gallery_entries
    (profile, entry_key, shape, measure, split, time_range, size, cols, rows, title, blurb, position, visible)
SELECT m.profile, g.entry_key, g.shape, g.measure, g.split, g.time_range, g.size, g.cols, g.rows,
       g.title, g.blurb, g.position, g.visible
  FROM (VALUES ('manager.branch', 'manager'), ('manager.sales', 'manager'),
               ('manager.yard', 'yard'), ('manager.logistics', 'yard'),
               ('manager.procurement', 'manager'), ('manager.payables', 'manager'),
               ('manager.controller', 'manager'), ('manager.general', 'admin')) AS m (profile, base)
  JOIN dashboard_gallery_entries g ON g.profile = m.base
ON CONFLICT (profile, entry_key) DO NOTHING;
