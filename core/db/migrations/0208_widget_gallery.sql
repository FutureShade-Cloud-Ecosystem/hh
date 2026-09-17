-- 0208 — "Add widget" opens a GALLERY first, and the gallery is rows.
--
-- APPROVED by the commander, 2 Sep 2026 (max(branch) 0207 + 1, confirmed by `ls backend/migrations
-- | tail -1` immediately before writing — it answered 0206 at the top of this pair, and 0207 is this
-- session's own file, landed one statement earlier).
--
-- ── WHAT THE DOCTRINE ASKS FOR ─────────────────────────────────────────────────────────────────
--
-- Home v3.5: "Add widget opens a GALLERY first — ready-made, role-relevant widgets with mini
-- previews placed in one tap — with 'Build your own' opening the full form for anything the gallery
-- doesn't cover." The seven-field builder is not going away; it stops being the FIRST thing a person
-- meets, because most people adding a widget want one of six obvious ones and should not have to
-- compose it out of a measure, a split, a shape, a window and a size to get it.
--
-- "Ready-made" and "role-relevant" are the two words that decide where this lives. Ready-made means
-- somebody chose the five fields already. Role-relevant means the choice differs per job shape. Both
-- are CURATION — a dealer's opinion about what their counter staff should be offered — and this
-- product's standing rule for curation is that it is rows a dealer can edit, never a switch on
-- profile in Go. 0201 §3 states it for the Needs-you sources; 0182 states it for the metric menu;
-- 0140 states it for the tile layout. A gallery hardcoded in TypeScript would be the fourth place
-- this decision is made and the only one nobody can change without a release.
--
-- ── WHY A TYPED TABLE, WHICH IS 0201'S ARGUMENT AND NOT A NEW ONE ──────────────────────────────
--
-- The obvious cheap answer is 0172's generic three-string surface row — a new surface id,
-- `home.gallery`, kind `chip`, one item id per entry. 0201 refused exactly that for the widget
-- tables and every word of its refusal applies here unchanged. Quoting it directly:
--
--   0201:11-12  "0172's surface_user_items is the GENERIC three-string form of the same idea, and
--                its row is exactly (surface_id, user_id, item_kind, item_id, position, visible)."
--   0201:13-14  "A WIDGET HAS EIGHT FIELDS AND SIX OF THEM DO NOT FIT THAT ROW. The only place they
--                could go is the opaque 64-character `item_id`."
--   0201:19-22  "...would make every resize a primary-key change (delete-and-insert, losing the
--                row's identity and its place in the order), make the title part of the key, and put
--                a second schema inside a string — the exact drift 0192_tour_substrate.sql refused
--                in writing this session... The same three arguments apply here and any one of them
--                is enough."
--
-- A GALLERY ENTRY IS A WIDGET PLUS A SENTENCE. It carries the same shape, measure, split, window,
-- size and title a widget carries, plus a free span (0207) and a `blurb` — the one line under the
-- mini preview that says what the thing is. That is eleven fields, three more than the widget row
-- 0201 already judged unfittable. Encoding them into an item id would mean an entry id like
-- `gal.bars.picks.queue.status.open.M` in which fixing a typo in the blurb is a primary-key change
-- and reordering the gallery is a delete-and-insert. The same three arguments; the same answer.
--
-- KEYED THE WAY 0140 AND 0201 KEY THEIR OWN: (profile, entry_key), with every other field an
-- ATTRIBUTE that changes in place. Editing an entry's size, title or blurb is an UPDATE of a row
-- that keeps its identity and its position; only adding and removing an entry changes the key set.
--
-- ── IDS ARE ADD-ONLY, FOR A WEAKER REASON THAN 0201's AND STILL A REAL ONE ─────────────────────
--
-- 0201's rule is absolute because `widget_key` is half the primary key of a layout somebody SAVED —
-- renaming one orphans an arrangement silently. `entry_key` is not stored on anybody's layout: the
-- gallery hands the browser a template and the browser PUTs a widget, so a retired entry key breaks
-- nothing downstream. What a rename does break is the dealer's own edits (a renamed key arrives as
-- delete-plus-insert and loses its position and any retitling) and every admin bookmark and audit
-- line naming it. Retire with visible = false, the way 0140/0172/0182/0201 all do.
--
-- ── WHAT IS CHECK-CONSTRAINED, WHICH IS 0201's LINE IN THE SAME PLACE ──────────────────────────
--
-- `shape`, `size`, `cols` and `rows` carry VALUE checks: they are the SUBSTRATE's own vocabulary and
-- cannot be widened by adding a measure (0201:44-48, extended by 0207 for the sixth preset and the
-- snap set). `measure`, `split` and `time_range` carry SHAPE-ONLY checks, because their meaning lives
-- in the add-only Go catalog and a dealer adding a measure must not need a schema change to curate a
-- gallery entry that names it (0201:50-55).
--
-- CURATION IS NOT THE GATE, FOR THE THIRD TIME (0182's sentence, 0201's line 57). Nothing in this
-- table grants anything. The read intersects every entry's measure with MetricsFor(curation,
-- costAllowed) — the caller's own catalog, the same two layers as their Home and their chart
-- composer — and an entry whose measure is not in it is NOT RETURNED. Not greyed, not returned with
-- a null measure: absent, byte for byte, exactly as WidgetViewsFor drops a widget. These rows decide
-- a MENU and the Go gate decides what may exist in the answer.
--
-- ── THERE IS NO branch_id ON A GALLERY ENTRY, DELIBERATELY ─────────────────────────────────────
--
-- widget.go's NoBranch already argues this in full for the ROLE DEFAULT and a gallery entry is the
-- same object with the same problem, only more so: one row read by many people with different
-- grants, and every answer for the reader who lacks the branch is bad — measure their own branches
-- and the preview answers a different question than the one it shows; measure nothing and the
-- preview is permanently blank; drop the entry and the gallery has a hole no admin authored. A
-- per-viewer answer cannot be stored in a per-role row. A person who wants "Scrubname0027 at Trenton" places
-- the ungated entry and narrows it on their own card, where the gate is theirs.
--
-- ── §1 · THE TABLE ─────────────────────────────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS dashboard_gallery_entries (
    profile     text NOT NULL CHECK (profile IN ('counter', 'yard', 'driver', 'manager', 'admin')),
    entry_key   text NOT NULL CHECK (entry_key ~ '^[a-z0-9][a-z0-9._-]{0,63}$'),
    shape       text NOT NULL CHECK (shape IN ('stat', 'trend', 'bars', 'list', 'board', 'gauge')),
    measure     text NOT NULL CHECK (measure ~ '^[a-z0-9][a-z0-9._]{0,63}$'),
    split       text NOT NULL DEFAULT 'none' CHECK (split ~ '^[a-z][a-z_]{0,31}$'),
    time_range  text NOT NULL DEFAULT 'today' CHECK (time_range ~ '^[a-z][a-z0-9_]{0,31}$'),
    size        text NOT NULL DEFAULT 'M' CHECK (size IN ('S', 'T', 'M', 'L', 'XL')),
    cols        smallint CHECK (cols IS NULL OR cols IN (3, 4, 6, 8, 9, 12)),
    rows        smallint CHECK (rows IS NULL OR rows BETWEEN 1 AND 3),
    title       text NOT NULL DEFAULT '' CHECK (char_length(title) <= 48),
    blurb       text NOT NULL DEFAULT '' CHECK (char_length(blurb) <= 120),
    position    integer NOT NULL DEFAULT 0 CHECK (position >= 0),
    visible     boolean NOT NULL DEFAULT true,
    updated_by  uuid REFERENCES users (id),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (profile, entry_key)
);

CREATE INDEX IF NOT EXISTS dashboard_gallery_entries_order_idx
    ON dashboard_gallery_entries (profile, position);

-- ── comments ───────────────────────────────────────────────────────────────────────────────────

COMMENT ON TABLE dashboard_gallery_entries IS
  'The "Add widget" GALLERY, per 0140 job shape — ready-made widgets a person places in one tap. Typed for 0201''s reason: an entry has eleven fields and a 0172 item row has three. Curation only: the read intersects each entry''s measure with the caller''s own catalog, so these rows decide a menu and never a grant.';

COMMENT ON COLUMN dashboard_gallery_entries.entry_key IS
  'Stable per-role entry id — the 0140 (owner, item key) shape. Add-only: a rename arrives as delete-plus-insert and loses the dealer''s ordering and any retitling. Retire with visible = false.';
COMMENT ON COLUMN dashboard_gallery_entries.measure IS
  'Catalog measure id (internal/dashboard/viz.go). Shape-CHECKed only: meaning lives in the add-only Go catalog, and the caller''s catalog — not this column — decides whether the entry is offered at all.';
COMMENT ON COLUMN dashboard_gallery_entries.size IS
  'The preset the placed widget lands at: S 3x1 | T 3x2 | M 6x1 | L 6x2 | XL 12x1 (0207). Value-CHECKed because it is the grid''s own vocabulary.';
COMMENT ON COLUMN dashboard_gallery_entries.cols IS
  'FREE column span for entries the presets cannot express, on the 0207 snap set. NULL means "the preset decides" and is the ordinary state. Set only with rows.';
COMMENT ON COLUMN dashboard_gallery_entries.rows IS
  'FREE row span, 1-3 (0207). NULL means "the preset decides". Set only with cols.';
COMMENT ON COLUMN dashboard_gallery_entries.title IS
  'The name on the gallery card AND the title the placed widget carries. Unlike a widget''s title, empty is not the useful default here: a gallery of cards deriving their names from their measures would list "Pick queue" three times.';
COMMENT ON COLUMN dashboard_gallery_entries.blurb IS
  'The one line under the mini preview — what this widget tells you, in a sentence. Bounded at 120 characters because a gallery card that scrolls is a form with extra steps.';
COMMENT ON COLUMN dashboard_gallery_entries.visible IS
  'Retirement, not deletion. An entry switched off leaves the ordering and the dealer''s edits intact and can be switched back on.';

-- ── §2 · the seeded gallery ────────────────────────────────────────────────────────────────────
--
-- SIX ENTRIES PER SHAPE, FOR THE FOUR SHAPES WHOSE HOME IS A BENTO. Six because the gallery is a
-- glance, not a catalog — the builder is one tap away for everything else, and a grid of eighteen
-- previews is the thing "Add widget opens a gallery first" exists to replace.
--
-- THE DRIVER GETS NONE, for 0201's stated reason and not a new one: 0141 takes a driver's Home over
-- entirely, so there is no bento to add a widget to, and a gallery nobody can open would be dead
-- configuration. "No entries" is a true and readable state.
--
-- EVERY ENTRY NAMES A MEASURE THAT SHAPE IS ALREADY OFFERED. 0182 curates five measures to the
-- frontline (`counter`, `yard`, `driver`) and eight to leadership (`manager`, `admin`), and 0201's
-- header states the consequence for role defaults: "a role default that named a measure its own
-- people are not offered would render as a hole on first login." A gallery entry is worse than a
-- hole — it is an OFFER that vanishes, and the person who saw it yesterday cannot tell whether they
-- imagined it. So the counter and yard sets are drawn only from orders.open, picks.queue,
-- deliveries.today, warranty.open_claims and sales.today, and the two cost-class measures
-- (inventory.value, margin.gross_bps) appear only on manager and admin — where the Go cost gate
-- still has the final word, byte-absently, for any caller below manager who is filed there.
--
-- EVERY (measure, shape) AND (measure, split) PAIR IS ONE THE CATALOG DECLARES, and every window is
-- the one that measure's provider can actually honour (0205's ruling). A drift test asserts all
-- three against MetricByID, so an entry that would render a lie cannot be seeded here.
--
-- ONE ENTRY CARRIES A FREE SPAN — manager's `ar.bucket`, at 8x2. The book's aging chart wants more
-- than half the width and more than one row, and 8x2 is a real grid rectangle that no preset names.
-- It is here to prove the 0207 columns carry end to end rather than because the gallery needs many
-- of them: presets are the vocabulary, and a free span is what somebody reaches for when the
-- vocabulary runs out. `size` is still authored beside it, as the honest fallback for any client
-- that has not learned the override.
--
-- `needs.you` IS NOT IN THE GALLERY. It is the one reserved measure, it already sits at position 1
-- of all four role defaults, and the doctrine says it is the widget a person may never lose — so
-- offering a SECOND one would be offering a duplicate of something nobody is missing. Nothing here
-- forbids a dealer curating it; the Go gate allows the reserved measure for every caller. It is a
-- judgement about what belongs in a six-card glance, not a rule.

INSERT INTO dashboard_gallery_entries (profile, entry_key, shape, measure, split, time_range, size, cols, rows, title, blurb, position, visible) VALUES
    -- counter — the till and the queue behind it. Five measures, six ways in.
    ('counter', 'sales.spark',       'stat',  'sales.today',          'none',   'today',    'M',  NULL, NULL, 'Scrubname0027 today',          'The till total so far today, with a sparkline under it.',            0, true),
    ('counter', 'orders.status',     'bars',  'orders.open',          'status', 'open',     'M',  NULL, NULL, 'Orders by status',     'Scrubname0032 the open book is sitting — one bar per lifecycle state.',      1, true),
    ('counter', 'picks.gauge',       'gauge', 'picks.queue',          'none',   'open',     'T',  NULL, NULL, 'Pick queue depth',     'How deep the yard queue is right now, as a severity ring.',          2, true),
    ('counter', 'deliveries.board',  'board', 'deliveries.today',     'status', 'today',    'L',  NULL, NULL, 'Deliveries today',     'Every stop scheduled today, as status chips across the day.',        3, true),
    ('counter', 'warranty.list',     'list',  'warranty.open_claims', 'none',   'open',     'M',  NULL, NULL, 'Open claims',          'Claims that have not reached a closed phase, oldest first.',         4, true),
    ('counter', 'orders.count',      'stat',  'orders.open',          'none',   'open',     'S',  NULL, NULL, 'Open orders',          'One number: orders confirmed and not yet closed.',                   5, true),

    -- yard — the dock. The day going out, then the queue, then what is short.
    ('yard',    'deliveries.board',  'board', 'deliveries.today',     'status', 'today',    'XL', NULL, NULL, 'Deliveries today',     'The whole day''s stops as status chips, full width.',                0, true),
    ('yard',    'picks.gauge',       'gauge', 'picks.queue',          'none',   'open',     'T',  NULL, NULL, 'Pick queue depth',     'How deep the queue is right now, as a severity ring.',               1, true),
    ('yard',    'picks.status',      'bars',  'picks.queue',          'status', 'open',     'M',  NULL, NULL, 'Picks by status',      'Waiting, in progress, staged — one bar each.',                       2, true),
    ('yard',    'deliveries.branch', 'bars',  'deliveries.today',     'branch', 'today',    'M',  NULL, NULL, 'Deliveries by branch', 'Today''s stops split across the yards you cover.',                   3, true),
    ('yard',    'picks.list',        'list',  'picks.queue',          'none',   'open',     'L',  NULL, NULL, 'Pick queue',           'The queue itself: what is waiting, and how long it has waited.',     4, true),
    ('yard',    'warranty.count',    'stat',  'warranty.open_claims', 'none',   'open',     'S',  NULL, NULL, 'Open claims',          'One number: claims not yet in a closed phase.',                      5, true),

    -- manager — the controller. The book first, then what turns into a phone call.
    ('manager', 'ar.bucket',         'bars',  'ar.aging',             'bucket', 'open',     'L',  8,    2,    'Aging by bucket',      'The book by age band — current, 30, 60, 90+.',                       0, true),
    ('manager', 'ar.gauge',          'gauge', 'ar.aging',             'none',   'open',     'T',  NULL, NULL, 'Receivables',          'The whole book as one figure with a severity ring.',                 1, true),
    ('manager', 'margin.branch',     'bars',  'margin.gross_bps',     'branch', 'all_time', 'M',  NULL, NULL, 'Margin by branch',     'Gross margin in basis points, one bar per branch.',                  2, true),
    ('manager', 'sales.trend',       'trend', 'sales.today',          'none',   'today',    'M',  NULL, NULL, 'Scrubname0027 trend',          'Committed counter sales, drawn as a line.',                          3, true),
    ('manager', 'inventory.branch',  'bars',  'inventory.value',      'branch', 'now',      'M',  NULL, NULL, 'Inventory by branch',  'On-hand stock at weighted average cost, per yard.',                  4, true),
    ('manager', 'orders.status',     'bars',  'orders.open',          'status', 'open',     'M',  NULL, NULL, 'Orders by status',     'Scrubname0032 the open book is sitting — one bar per state.',                5, true),

    -- admin — the general manager. The whole business: money, the book, the day, the yard.
    ('admin',   'sales.branch',      'bars',  'sales.today',          'branch', 'today',    'M',  NULL, NULL, 'Scrubname0027 by branch',      'Today''s committed counter sales, split across the yards.',          0, true),
    ('admin',   'ar.bucket',         'bars',  'ar.aging',             'bucket', 'open',     'L',  NULL, NULL, 'Aging by bucket',      'The book by age band — current, 30, 60, 90+.',                       1, true),
    ('admin',   'margin.trend',      'trend', 'margin.gross_bps',     'none',   'all_time', 'M',  NULL, NULL, 'Gross margin',         'Gross margin on invoiced order lines, drawn as a line.',             2, true),
    ('admin',   'deliveries.board',  'board', 'deliveries.today',     'status', 'today',    'XL', NULL, NULL, 'Deliveries today',     'The whole day''s stops as status chips, full width.',                3, true),
    ('admin',   'picks.gauge',       'gauge', 'picks.queue',          'none',   'open',     'T',  NULL, NULL, 'Pick queue depth',     'How deep the yard queue is right now, as a severity ring.',          4, true),
    ('admin',   'inventory.branch',  'bars',  'inventory.value',      'branch', 'now',      'M',  NULL, NULL, 'Inventory by branch',  'On-hand stock at weighted average cost, per yard.',                  5, true)
ON CONFLICT (profile, entry_key) DO NOTHING;
