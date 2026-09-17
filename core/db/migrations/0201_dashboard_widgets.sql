-- 0201_dashboard_widgets.sql — THE WIDGET SUBSTRATE. Home stops being a grid of equal boxes and
-- becomes a bento of configured widgets: {shape, measure, split, branch, range, size, title,
-- position}, per person, over a per-role default.
--
-- ── WHY TYPED TABLES AND NOT 0172's surface_user_items ─────────────────────────────────────────
--
-- The direction says "on the presentation substrate … the 0140/0149 idiom", and that is exactly what
-- this file follows — but it is worth being precise about what that idiom IS, because there are two
-- things in this codebase wearing the name. 0140 is a TYPED TABLE (dashboard_user_tiles: a per-user
-- layer, a per-role layer, a whole-or-absent override, a reset that is a DELETE). 0172's
-- surface_user_items is the GENERIC three-string form of the same idea, and its row is exactly
-- (surface_id, user_id, item_kind, item_id, position, visible).
--
-- A WIDGET HAS EIGHT FIELDS AND SIX OF THEM DO NOT FIT THAT ROW. The only place they could go is the
-- opaque 64-character `item_id`, which is precisely what 0182 already does for the composed CHART
-- tile — `viz.<metric>-<dimension>-<shape>` — and which worked because a chart is three closed-set
-- choices and nothing else. A widget is not: it carries a SIZE that changes on a drag, a TITLE a
-- person types, a BRANCH filter, and a RANGE. Encoding those into the id would make every resize a
-- primary-key change (delete-and-insert, losing the row's identity and its place in the order), make
-- the title part of the key, and put a second schema inside a string — the exact drift
-- 0192_tour_substrate.sql refused in writing this session, for a step that carried a route, an
-- anchor, a line of copy and an advance rule. The same three arguments apply here and any one of
-- them is enough.
--
-- SO: TYPED TABLES, KEYED THE WAY 0140 KEYS ITS OWN. 0140's key is (owner, item key) —
-- (user_id, tile_id) and (profile, tile_id) — with everything else an ATTRIBUTE that can change in
-- place. This file does the same with (user_id, widget_key) and (profile, widget_key). A resize, a
-- retitle and a re-split are UPDATEs of a row that keeps its identity; only add and remove change
-- the key set.
--
-- WHAT 0172 STILL OWNS, unchanged: which METRICS a job shape may compose from (surface
-- `home.metrics`, kind `chip`, 0182) — dealer curation over a code-side catalog, editable without a
-- release. This file adds no second curation of that.
--
-- ── IDS ARE ADD-ONLY (0140:16, restated at 0172:22 and 0192) ───────────────────────────────────
--
-- `widget_key` is half the primary key of every layout somebody saved, and `measure` is half of what
-- a widget MEANS. Add ids; retire one with visible = false; NEVER rename one. A renamed widget_key
-- orphans the person's arrangement silently; a renamed measure orphans every widget drawing it. The
-- same rule already governs tile ids (0140), item ids (0172), chapter/step keys (0192) and metric
-- ids (0182), and it governs these.
--
-- ── WHAT IS CHECK-CONSTRAINED HERE AND WHAT IS NOT, AND WHY THE LINE IS THERE ──────────────────
--
-- `shape` and `size` carry VALUE checks: the six body forms and the four bento spans. They are the
-- SUBSTRATE's own vocabulary — size decides a grid span (1x1 · 2x1 · 2x2 · 4x1) and shape decides
-- which body form renders — and neither can be widened by adding a measure. A seventh shape is a new
-- component and therefore a release anyway, so a migration to go with it costs nothing.
--
-- `measure`, `split` and `time_range` carry SHAPE-ONLY checks (alphabet and length), exactly as
-- 0172 validates its item ids and for the identical reason: their meaning lives in the catalog
-- (internal/dashboard/viz.go), the catalog is add-only Go, and a dealer adding a measure must not
-- need a schema change to store a widget that names it. The Go gate is what refuses a measure this
-- build does not have — and, far more importantly, what refuses a measure this CALLER may not see.
--
-- CURATION IS NOT THE GATE, AGAIN (0182's sentence, and it is load-bearing here too). Nothing in
-- this schema grants anything. A role default that names `margin.gross_bps` for the counter shape
-- still sends a counter clerk a payload with no margin widget in it: the read intersects every
-- widget's measure with MetricsFor(curation, costAllowed) and a widget whose measure is absent is
-- NOT RETURNED — not rendered empty, not nulled, absent. These rows decide the MENU.
--
-- ── RESET IS A DELETE, AND ONLY A DELETE ───────────────────────────────────────────────────────
--
-- A person's override is WHOLE OR ABSENT (0140's ruling, verbatim): if they have any rows those rows
-- are the layout and the profile rows are not consulted. That is what makes "Reset to role default"
-- one DELETE of the person's own rows with nothing to unwind. 0191 had to unpick a reset that wrote
-- the defaults DOWN into the person's level — a one-way door, because afterwards nobody could tell a
-- default from a choice — and 0192/0193 restate the rule. There is no route in this feature that
-- writes a role default into dashboard_user_widgets. The one exception is a MIGRATION (0202), which
-- is a one-time act with a stated reason, not a runtime path.
--
-- ── REVERT COST ────────────────────────────────────────────────────────────────────────────────
--
-- ADD-only: three new tables, no ALTER of anything that exists. A revert is three DROPs and loses
-- personal widget layouts and the dealer's needs-queue policy. Safe against a staging database that
-- is never wiped.

-- ── §1 · the role default layout ───────────────────────────────────────────────────────────────
--
-- The dealer's default Home for a JOB SHAPE — what a new hire sees before they have arranged
-- anything. `profile` is 0140's five-value vocabulary and this file does not widen it: a sixth value
-- would file something that is not a job shape alongside the job shapes, which is the argument
-- 0191:15-23 made in writing and 0192 inherited.
CREATE TABLE IF NOT EXISTS dashboard_profile_widgets (
    profile     text NOT NULL CHECK (profile IN ('counter', 'yard', 'driver', 'manager', 'admin')),
    widget_key  text NOT NULL CHECK (widget_key ~ '^[a-z0-9][a-z0-9._-]{0,63}$'),
    shape       text NOT NULL CHECK (shape IN ('stat', 'trend', 'bars', 'list', 'board', 'gauge')),
    measure     text NOT NULL CHECK (measure ~ '^[a-z0-9][a-z0-9._]{0,63}$'),
    split       text NOT NULL DEFAULT 'none' CHECK (split ~ '^[a-z][a-z_]{0,31}$'),
    branch_id   uuid REFERENCES locations (id) ON DELETE SET NULL,
    time_range  text NOT NULL DEFAULT 'today' CHECK (time_range ~ '^[a-z][a-z0-9_]{0,31}$'),
    size        text NOT NULL DEFAULT 'M' CHECK (size IN ('S', 'M', 'L', 'XL')),
    title       text NOT NULL DEFAULT '' CHECK (char_length(title) <= 48),
    position    integer NOT NULL DEFAULT 0 CHECK (position >= 0),
    visible     boolean NOT NULL DEFAULT true,
    updated_by  uuid REFERENCES users (id),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (profile, widget_key)
);

CREATE INDEX IF NOT EXISTS dashboard_profile_widgets_order_idx
    ON dashboard_profile_widgets (profile, position);

-- ── §2 · one person's layout ───────────────────────────────────────────────────────────────────
--
-- Same columns, same key shape, one owner column different — deliberately a MIRROR of §1 rather than
-- a single table with a nullable user_id. 0140 made that choice first and it is right for the same
-- reason: "this row belongs to a job shape" and "this row belongs to a person" are different facts
-- with different lifetimes (the person's cascade away with the person; the shape's outlive everyone),
-- and a nullable owner column would make every read carry an IS NULL that means "which layer am I".
-- The override is whole-or-absent, so the two layers are never merged and never joined.
--
-- NO updated_by: a person's own layout is only ever written by that person. A column recording who
-- changed it would be a column that always holds the same value as user_id, which is worse than no
-- column because a reader would reasonably expect it to sometimes differ.
CREATE TABLE IF NOT EXISTS dashboard_user_widgets (
    user_id     uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    widget_key  text NOT NULL CHECK (widget_key ~ '^[a-z0-9][a-z0-9._-]{0,63}$'),
    shape       text NOT NULL CHECK (shape IN ('stat', 'trend', 'bars', 'list', 'board', 'gauge')),
    measure     text NOT NULL CHECK (measure ~ '^[a-z0-9][a-z0-9._]{0,63}$'),
    split       text NOT NULL DEFAULT 'none' CHECK (split ~ '^[a-z][a-z_]{0,31}$'),
    branch_id   uuid REFERENCES locations (id) ON DELETE SET NULL,
    time_range  text NOT NULL DEFAULT 'today' CHECK (time_range ~ '^[a-z][a-z0-9_]{0,31}$'),
    size        text NOT NULL DEFAULT 'M' CHECK (size IN ('S', 'M', 'L', 'XL')),
    title       text NOT NULL DEFAULT '' CHECK (char_length(title) <= 48),
    position    integer NOT NULL DEFAULT 0 CHECK (position >= 0),
    visible     boolean NOT NULL DEFAULT true,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, widget_key)
);

CREATE INDEX IF NOT EXISTS dashboard_user_widgets_order_idx
    ON dashboard_user_widgets (user_id, position);

-- ── §3 · the Needs-you queue's SOURCES, per role ───────────────────────────────────────────────
--
-- "Needs you" is the widget that earns Home, and the doctrine is that its sources are DECLARED PER
-- ROLE IN CONFIGURATION — not a switch on profile in Go. These rows are that declaration.
--
-- WHAT IS DATA HERE AND WHAT IS NECESSARILY CODE. The row says WHICH source, in WHAT ORDER, and at
-- WHAT SEVERITY EDGES. It cannot say HOW to count, because every source is a read that already
-- exists in another module (the load board's quick cards, the AR escalation card, the approvals
-- queue, the parked-ticket count) reached through a consumer-declared adapter in the composition
-- root. That is the identical split 0140 and 0182 already make — tile ids and metric ids are code,
-- the layout and the menu are rows — and it is the split that keeps a config table from being able
-- to invent a query.
--
-- THE THRESHOLDS ARE ROWS BECAUSE THE EDGE IS THE DEALER'S. 0183 made this argument for the AR
-- escalation edge and it is the same argument: "how many parked tickets is too many" is a dealer
-- opinion, it differs between a two-till branch and a ten-till branch, and burning it into Go means
-- a release every time somebody wants to move it. warn_at/stop_at are COUNT edges; a count below
-- warn_at is `good`. 0 in stop_at means "this source never reaches stop" — a real setting, and the
-- reason these are integers rather than a nullable pair is that NULL would have to mean the same
-- thing while also being the shape a missing row has.
--
-- stale_after_minutes ESCALATES BY EXACTLY ONE STEP (good→watch, watch→stop) rather than setting a
-- severity of its own. Age and volume are different complaints and a queue that shouted `stop`
-- because one item was old would drown the queue that is actually deep. 0 disables it. The unit is
-- MINUTES for every source, and the numbers are wildly different between them (45 for a parked
-- ticket, 129,600 for a 90-day invoice) — which is the whole reason this is a per-source row and not
-- one global constant that would have to mean both.
CREATE TABLE IF NOT EXISTS dashboard_needs_sources (
    profile             text NOT NULL CHECK (profile IN ('counter', 'yard', 'driver', 'manager', 'admin')),
    source_key          text NOT NULL CHECK (source_key ~ '^[a-z0-9][a-z0-9._-]{0,63}$'),
    position            integer NOT NULL DEFAULT 0 CHECK (position >= 0),
    visible             boolean NOT NULL DEFAULT true,
    warn_at             integer NOT NULL DEFAULT 1 CHECK (warn_at >= 0),
    stop_at             integer NOT NULL DEFAULT 0 CHECK (stop_at >= 0),
    stale_after_minutes integer NOT NULL DEFAULT 0 CHECK (stale_after_minutes >= 0),
    updated_by          uuid REFERENCES users (id),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (profile, source_key)
);

CREATE INDEX IF NOT EXISTS dashboard_needs_sources_order_idx
    ON dashboard_needs_sources (profile, position);

-- ── comments ───────────────────────────────────────────────────────────────────────────────────

COMMENT ON TABLE dashboard_profile_widgets IS
  'Home widget DEFAULT layout per 0140 job shape — what a new hire sees. Typed because a widget has eight fields and 0172 item rows have three. Ids add-only; a rename orphans saved layouts.';
COMMENT ON TABLE dashboard_user_widgets IS
  'One person''s Home widget layout. WHOLE OR ABSENT: any row here and the role default is not consulted. Reset to role default = DELETE these rows, never a write of the defaults into this level (0191).';
COMMENT ON TABLE dashboard_needs_sources IS
  'Which Needs-you sources a job shape shows, in what order, at what severity edges. Declares WHICH source; HOW to count it is an adapter in the composition root, so a config row can never invent a query.';

COMMENT ON COLUMN dashboard_profile_widgets.widget_key IS
  'Stable per-layout widget id — the 0140 (owner, item key) shape. Add-only: renaming one orphans the arrangement silently.';
COMMENT ON COLUMN dashboard_profile_widgets.shape IS
  'Body form: stat | trend | bars | list | board | gauge. Value-CHECKed because a shape is a component, not a catalog entry.';
COMMENT ON COLUMN dashboard_profile_widgets.measure IS
  'Catalog measure id (internal/dashboard/viz.go). Shape-CHECKed only: meaning lives in the add-only Go catalog, and the caller''s catalog — not this column — decides whether it may be seen at all.';
COMMENT ON COLUMN dashboard_profile_widgets.split IS
  '"none" or a dimension the measure declares. Shape-CHECKed only, for the same reason as measure.';
COMMENT ON COLUMN dashboard_profile_widgets.branch_id IS
  'A NARROWING filter, never a grant: NULL means "the branches this caller may see", and the read intersects any value here with the caller''s own grants. ON DELETE SET NULL so a retired branch widens a widget back to the caller''s scope instead of deleting their widget.';
COMMENT ON COLUMN dashboard_profile_widgets.time_range IS
  'Named window (today | wtd | mtd | qtd | ytd | last_7 | last_14 | last_30 | last_90 | open). Shape-CHECKed; the closed set is the Go catalog''s.';
COMMENT ON COLUMN dashboard_profile_widgets.size IS
  'Bento span: S 1x1 | M 2x1 | L 2x2 | XL 4x1. Value-CHECKed because it is the grid''s own vocabulary.';
COMMENT ON COLUMN dashboard_profile_widgets.title IS
  'Empty means "derive from measure + split" — the restrictive default, so a widget nobody titled still reads correctly and a retitle is a real decision on record.';

COMMENT ON COLUMN dashboard_user_widgets.widget_key IS
  'Stable per-layout widget id. A resize or retitle UPDATEs the row and keeps this key; only add/remove change the key set.';
COMMENT ON COLUMN dashboard_user_widgets.branch_id IS
  'A NARROWING filter, never a grant (see the profile table''s column comment).';
COMMENT ON COLUMN dashboard_user_widgets.visible IS
  'A role default this person switched OFF. Stored rather than omitted, because an override is whole-or-absent and an omission would read as "no override".';

COMMENT ON COLUMN dashboard_needs_sources.source_key IS
  'Named source in the code-side source catalog (internal/dashboard/needs.go). A key this build does not have renders nothing, silently — the 0172 posture.';
COMMENT ON COLUMN dashboard_needs_sources.warn_at IS
  'Count at or above which the row is `watch`. Below it, `good`.';
COMMENT ON COLUMN dashboard_needs_sources.stop_at IS
  'Count at or above which the row is `stop`. 0 means this source never reaches stop.';
COMMENT ON COLUMN dashboard_needs_sources.stale_after_minutes IS
  'Age of the OLDEST item at or above which the row escalates by ONE step (good→watch, watch→stop). 0 disables. Minutes for every source; the numbers differ by orders of magnitude between sources, which is why this is a per-source row.';

-- ── §4 · the seeded role defaults ──────────────────────────────────────────────────────────────
--
-- FOUR LAYOUTS, FOR THE FOUR JOB SHAPES WHOSE HOME IS THE DASHBOARD. The fifth shape, `driver`, is
-- not seeded and that is not an omission: 0141 takes a driver's home over entirely — their phone
-- opens on the run sheet — so a bento nobody lands on would be dead configuration. When a driver
-- opens the cockpit on a desktop they get no widgets, and "no widgets configured" is a true and
-- readable state, where a half-considered driver layout would be a false one.
--
-- The four, and the person each is written for:
--   admin    the GENERAL MANAGER (0140 files GENERAL_MANAGER, SUPER_ADMIN, HR here) — the whole
--            business at a glance: money, the book, the day, the yard.
--   manager  the CONTROLLER (0140 files FINANCIAL_CONTROLLER and PAYABLES_RECEIVABLES here, with
--            the other managers) — the book first, then what turns into a phone call.
--   yard     the DISPATCHER and the yard lead (0140 files YARD_MANAGER and YARD_TEAM here) — the
--            dock: what is going out, what is picked, what is short.
--   counter  the COUNTER LEAD (0140 files INSIDE_SALES and OUTSIDE_SALES here) — the till and the
--            queue behind it.
--
-- ONE THING THIS FILE DELIBERATELY DOES NOT DO: re-file a role. 0140 maps LOGISTICS_MANAGER to
-- `manager`, so a person carrying that code today gets the controller's Home rather than the dock's.
-- Moving them is ONE ROW in dashboard_role_profiles — data an admin edits, which is the entire point
-- of 0140 — and changing it here would silently change that person's TILE layout too, as a side
-- effect of a migration about widgets. A migration that edits somebody's existing screen to make its
-- own seed read better is a migration nobody can trust.
--
-- EVERY LAYOUT OPENS WITH A HERO AT POSITION 0 AND NEEDS-YOU AT POSITION 1. The hero is not a flag
-- and there is no `is_hero` column: it is the first widget, at size L. "Nothing is decorated;
-- everything is placed" is the doctrine, and a boolean that meant "draw this one bigger" would be a
-- second, contradictory way to say what `size` and `position` already say.
--
-- `needs.you` IS A RESERVED MEASURE and it is deliberately NOT in the metric catalog. It names the
-- queue assembled by §3's sources rather than a series anybody can chart, so putting it in
-- MetricCatalog would have offered "Needs you" as a bar chart in the chart composer, which is
-- meaningless. The widget read knows the one reserved id; the composer never sees it.
--
-- Every other measure named below is a real catalog id AND one 0182 curates for that shape: a role
-- default that named a measure its own people are not offered would render as a hole on first login.
-- The Go drift test reads this file and asserts both.

INSERT INTO dashboard_profile_widgets (profile, widget_key, shape, measure, split, time_range, size, title, position, visible) VALUES
    -- admin — the general manager. Money is the hero; the book, the day and the yard are context.
    ('admin',   'hero.sales',         'stat',  'sales.today',          'none',   'today',   'L',  'Scrubname0027 today',        0, true),
    ('admin',   'needs.you',          'list',  'needs.you',            'none',   'open',    'L',  'Needs you',          1, true),
    ('admin',   'ar.aging',           'bars',  'ar.aging',             'bucket', 'open',    'L',  'Receivables aging',  2, true),
    ('admin',   'orders.trend',       'trend', 'orders.open',          'none',   'last_14', 'M',  'Open orders',        3, true),
    ('admin',   'margin.branch',      'bars',  'margin.gross_bps',     'branch', 'mtd',     'M',  'Margin by branch',   4, true),
    ('admin',   'deliveries.board',   'board', 'deliveries.today',     'status', 'today',   'M',  'Deliveries today',   5, true),
    ('admin',   'picks.queue',        'stat',  'picks.queue',          'none',   'today',   'S',  'Pick queue',         6, true),
    ('admin',   'warranty.open',      'stat',  'warranty.open_claims', 'none',   'open',    'S',  'Open claims',        7, true),
    ('admin',   'inventory.value',    'stat',  'inventory.value',      'none',   'open',    'S',  'Inventory value',    8, true),

    -- manager — the controller. The book is the hero: it is what they are asked about at 8am.
    ('manager', 'hero.ar',            'stat',  'ar.aging',             'none',   'open',    'L',  'Receivables',        0, true),
    ('manager', 'needs.you',          'list',  'needs.you',            'none',   'open',    'L',  'Needs you',          1, true),
    ('manager', 'ar.aging',           'bars',  'ar.aging',             'bucket', 'open',    'L',  'Aging by bucket',    2, true),
    ('manager', 'sales.trend',        'trend', 'sales.today',          'none',   'last_30', 'M',  'Scrubname0027 trend',        3, true),
    ('manager', 'margin.branch',      'bars',  'margin.gross_bps',     'branch', 'mtd',     'M',  'Margin by branch',   4, true),
    ('manager', 'orders.open',        'stat',  'orders.open',          'none',   'open',    'S',  'Open orders',        5, true),
    ('manager', 'inventory.value',    'stat',  'inventory.value',      'none',   'open',    'S',  'Inventory value',    6, true),
    ('manager', 'warranty.open',      'stat',  'warranty.open_claims', 'none',   'open',    'S',  'Open claims',        7, true),

    -- yard — the dispatcher and the yard lead. The day going out is the hero; nothing here bears
    -- cost, and that is the shape of the job rather than a gate: the Go gate refuses cost for this
    -- tier regardless of what any row says.
    ('yard',    'hero.deliveries',    'stat',  'deliveries.today',     'none',   'today',   'L',  'Deliveries today',   0, true),
    ('yard',    'needs.you',          'list',  'needs.you',            'none',   'open',    'L',  'Needs you',          1, true),
    ('yard',    'picks.board',        'board', 'picks.queue',          'status', 'today',   'L',  'Pick queue',         2, true),
    ('yard',    'deliveries.branch',  'bars',  'deliveries.today',     'branch', 'today',   'M',  'By branch',          3, true),
    ('yard',    'picks.gauge',        'gauge', 'picks.queue',          'none',   'today',   'S',  'Queue depth',        4, true),
    ('yard',    'orders.open',        'stat',  'orders.open',          'none',   'open',    'S',  'Open orders',        5, true),
    ('yard',    'warranty.open',      'stat',  'warranty.open_claims', 'none',   'open',    'S',  'Open claims',        6, true),

    -- counter — the counter lead. Six widgets, not nine: the counter's screen time is somebody
    -- else's waiting time, and the doctrine's 6-9 range exists so a role landing can be SHORT.
    ('counter', 'hero.sales',         'stat',  'sales.today',          'none',   'today',   'L',  'Scrubname0027 today',        0, true),
    ('counter', 'needs.you',          'list',  'needs.you',            'none',   'open',    'L',  'Needs you',          1, true),
    ('counter', 'orders.trend',       'trend', 'orders.open',          'none',   'last_14', 'M',  'Open orders',        2, true),
    ('counter', 'orders.status',      'bars',  'orders.open',          'status', 'open',    'M',  'By status',          3, true),
    ('counter', 'picks.queue',        'stat',  'picks.queue',          'none',   'today',   'S',  'Pick queue',         4, true),
    ('counter', 'deliveries.today',   'stat',  'deliveries.today',     'none',   'today',   'S',  'Deliveries today',   5, true)
ON CONFLICT (profile, widget_key) DO NOTHING;

-- ── §5 · the seeded Needs-you policy ───────────────────────────────────────────────────────────
--
-- Each row names a source that is a READ THAT ALREADY EXISTS somewhere else in the product. Nothing
-- here is a new query and nothing here is a new number:
--
--   approvals.waiting      identity's approvals queue — what is waiting on THIS approver.
--   ar.escalation          billing's AR landing escalation card — the 90+ tail, at the dealer's own
--                          escalation edge (0183), not a second opinion about what "late" means.
--   dispatch.unassigned    the load board's quick cards — the staging lane's own count, so the row
--                          and the lane it links to cannot disagree.
--   dispatch.due_to_commit the same read's commit prompt.
--   pos.parked             the counter's parked-ticket list.
--   tasks.stale_picks      picks past their accept deadline (the picks.stale tile's population).
--
-- IF A SOURCE SEAM IS UNWIRED THE ROW IS SIMPLY ABSENT and the rest of the queue still answers. That
-- is why these rows can be seeded for every shape ahead of every adapter: a source nobody has wired
-- costs nothing and says nothing, where a queue that errored because one module was down would take
-- the most important widget on the screen off it.
--
-- A counter lead is given `approvals.waiting` even though most counter staff approve nothing. It
-- costs nothing when the answer is zero (a zero-count row is not rendered at all), and the shape
-- covers the counter LEAD who does hold override authority — which is the person this layout is
-- written for.
INSERT INTO dashboard_needs_sources (profile, source_key, position, visible, warn_at, stop_at, stale_after_minutes) VALUES
    ('admin',   'approvals.waiting',      0, true, 1,  5, 240),
    ('admin',   'ar.escalation',          1, true, 1,  3, 0),
    ('admin',   'dispatch.unassigned',    2, true, 1,  5, 0),
    ('admin',   'pos.parked',             3, true, 3, 10, 120),

    ('manager', 'approvals.waiting',      0, true, 1,  5, 240),
    ('manager', 'ar.escalation',          1, true, 1,  3, 0),
    ('manager', 'pos.parked',             2, true, 3, 10, 120),

    ('yard',    'dispatch.unassigned',    0, true, 1,  5, 0),
    ('yard',    'dispatch.due_to_commit', 1, true, 1,  3, 0),
    ('yard',    'tasks.stale_picks',      2, true, 1,  5, 60),

    ('counter', 'pos.parked',             0, true, 3, 10, 120),
    ('counter', 'tasks.stale_picks',      1, true, 1,  5, 60),
    ('counter', 'approvals.waiting',      2, true, 1,  5, 240)
ON CONFLICT (profile, source_key) DO NOTHING;
