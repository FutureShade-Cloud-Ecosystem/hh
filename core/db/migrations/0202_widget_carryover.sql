-- 0202_widget_carryover.sql — MOVE EVERY CHART SOMEBODY BUILT ONTO THE WIDGET SUBSTRATE, LOSING
-- NOTHING.
--
-- THE ACCEPTANCE CLAUSE, IN ONE SENTENCE: nobody loses a chart they built. Train 13 shipped the
-- composed-metric composer, and the charts people made with it live in the 0172 substrate — surface
-- `home.dashboard`, kind `panel`, item_id `viz.<metric>-<dimension>-<shape>` (0182's header says so
-- exactly). 0201 replaced that presentation model with typed widget rows. This file carries the
-- charts across. The proof is a before/after COUNT in a migration test, not this comment.
--
-- ── WHAT IS PARSED, AND WHAT HAPPENS TO WHAT IS NOT ────────────────────────────────────────────
--
-- The stored id is parsed DEFENSIVELY, against the closed sets and not against a general shape:
--
--   ^viz\.<measure>-(branch|status|bucket)-(bar|split|list)$
--
-- and then the (measure, dimension) pair is checked against the VETTED PAIRS listed below, which are
-- the catalog's own. An id that fails either check is LEFT EXACTLY WHERE IT IS: not migrated, not
-- deleted, not guessed at, not "repaired" by picking the nearest legal dimension. That last option
-- is the tempting one and it is the wrong one — a chart whose id does not parse is a chart whose
-- MEANING is unknown, and inventing a meaning for it puts a number on somebody's home screen that
-- nobody chose. It stays an inert row in surface_user_items, exactly as it is today, and the
-- composed-tile read (viz_service.go ResolveViz) goes on refusing it for the same reason it always
-- has.
--
-- NOTHING IS DELETED FROM surface_user_items EITHER. A migration that reads a row and then removes
-- it is a migration you cannot run twice and cannot revert without a backup. Leaving the rows costs
-- a handful of bytes, keeps 0201+0202 revertible with three DROPs, and means the old composed-tile
-- read keeps answering for anything still on it while the widget Home lands.
--
-- ── THE SHAPE MAPPING, AND THE ONE IT REFUSES ──────────────────────────────────────────────────
--
-- A composed chart becomes a widget of shape `bars` (or `stat` for a measure whose catalog entry does
-- not offer bars — the guard for a measure added later, not a case that fires today). The tempting
-- alternative was to map the chart shape `list` onto the widget shape `list`, since both draw rows.
-- It is refused because those two words mean different things: a widget `list` is a QUEUE — one fact,
-- one state, one tap-through per row — and a chart `list` is a series with labels. Promoting a
-- person's chart into a queue shape would change what their widget SAYS, which is a strange thing for
-- a migration whose promise is that nothing is lost to do.
--
-- Split carries across unchanged (the dimension is the split). Size is M for every migrated chart:
-- the composer had no size, M is the bento's honest default for "a number with a compact form", and
-- guessing L from a chart's shape would hand somebody a layout they did not arrange. Title is empty,
-- which in 0201 means "derive from measure + split" — the same title the composer showed them.
-- time_range is `open`, which names the measure's own natural window: the composer never asked for a
-- range, so no migrated widget acquires a filter its owner never chose.
--
-- widget_key IS THE OLD ITEM ID, VERBATIM. That is what makes this file re-runnable to a no-op
-- (ON CONFLICT DO NOTHING on (user_id, widget_key)), what makes a second run preserve any edit the
-- person has since made to that widget, and what makes the carry-over auditable: you can read a
-- widget_key and see which composed tile it came from. It is inside 0201's `^[a-z0-9][a-z0-9._-]{0,63}$`
-- alphabet because 0182 already proved every composed id is (viz_test.go asserts the 64-char bound).
--
-- ── WHY THE ROLE DEFAULT IS MATERIALISED UNDERNEATH, ONCE ──────────────────────────────────────
--
-- A person's widget layout is WHOLE OR ABSENT (0140's ruling, restated in 0201). So the moment this
-- file gives somebody three chart rows, those three rows ARE their Home — and they would lose the
-- hero and the Needs-you queue their role default gives everyone else. The doctrine is explicit that
-- Needs-you is the one widget a person may never end up without.
--
-- So for exactly the people this file touches, and only them, their role default is written in
-- BENEATH their charts, once. Two alternatives were considered and refused:
--   · leave them chart-only — loses Needs-you, which is the one thing the skill says cannot be lost;
--   · make the runtime read MERGE the two layers — buys this at the cost of whole-or-absent, and
--     with it the reset: 0191 spent a migration undoing a world where nobody could tell a default
--     from a choice.
-- Materialising here is a ONE-TIME ACT WITH A STATED REASON, not a runtime path. Reset still works
-- and still means the same thing: DELETE the person's rows and the role default answers again.
--
-- The accepted cost, named: these people stop receiving future changes to their role's default —
-- which is also what happens the first time anybody drags a widget, so it is the cost of having
-- arranged a Home rather than a cost this file invents.
--
-- WHICH ROLE DEFAULT. A person may hold several roles and SQL has no notion of the one they are
-- signed in as. The pick is the MOST SENIOR job shape among their roles (admin > manager > yard >
-- counter > driver), falling back to `counter` — the same least-privileged-useful default
-- Service.ProfileFor uses for an unmapped role. Seniority is safe here and only here: this decides
-- PRESENTATION, and every measure on the resulting layout is still intersected with the caller's own
-- catalog on every read, so a widget naming something they may not see is byte-absent. Curation
-- hides things; it never grants anything (0182).
--
-- ── REVERT COST ────────────────────────────────────────────────────────────────────────────────
--
-- This file only INSERTs into 0201's tables and reads two others. Reverting 0201 reverts this.

-- ── §1 · the charts ────────────────────────────────────────────────────────────────────────────

INSERT INTO dashboard_user_widgets
    (user_id, widget_key, shape, measure, split, time_range, size, title, position, visible)
WITH vetted (measure, dimension, widget_shape) AS (
    -- The catalog's own (measure, dimension) pairs, with the widget shape each becomes. A Go drift
    -- test reads this list and asserts every pair is one internal/dashboard/viz.go declares and every
    -- widget_shape is one that measure offers — the 0149/0182 gate idiom, applied to a data migration
    -- so this list cannot quietly disagree with the catalog it claims to mirror.
    VALUES ('sales.today',          'branch', 'bars'),
           ('orders.open',          'status', 'bars'),
           ('orders.open',          'branch', 'bars'),
           ('ar.aging',             'bucket', 'bars'),
           ('ar.aging',             'branch', 'bars'),
           ('warranty.open_claims', 'status', 'bars'),
           ('warranty.open_claims', 'branch', 'bars'),
           ('deliveries.today',     'status', 'bars'),
           ('deliveries.today',     'branch', 'bars'),
           ('picks.queue',          'status', 'bars'),
           ('picks.queue',          'branch', 'bars'),
           ('inventory.value',      'branch', 'bars'),
           ('margin.gross_bps',     'branch', 'bars')
),
parsed AS (
    SELECT i.user_id,
           i.item_id,
           i.position AS old_position,
           i.visible,
           x.m[1] AS measure,
           x.m[2] AS dimension
      FROM surface_user_items i
      CROSS JOIN LATERAL (
          SELECT regexp_match(i.item_id,
                     '^viz\.([a-z0-9._]+)-(branch|status|bucket)-(bar|split|list)$') AS m
      ) x
     WHERE i.surface_id = 'home.dashboard'
       AND i.item_kind = 'panel'
       AND x.m IS NOT NULL
),
carried AS (
    SELECT p.user_id,
           p.item_id,
           v.widget_shape,
           p.measure,
           p.dimension,
           p.visible,
           -- AT THE TOP OF THAT PERSON'S LAYOUT, in the order the composer had them. Their own
           -- charts open their Home; the role default lands beneath in §2.
           (row_number() OVER (PARTITION BY p.user_id ORDER BY p.old_position, p.item_id) - 1)::int
               AS position
      FROM parsed p
      JOIN vetted v ON v.measure = p.measure AND v.dimension = p.dimension
)
SELECT user_id, item_id, widget_shape, measure, dimension, 'open', 'M', '', position, visible
  FROM carried
ON CONFLICT (user_id, widget_key) DO NOTHING;

-- ── §2 · the role default, beneath ─────────────────────────────────────────────────────────────
--
-- Offset by however many charts §1 gave that person, so the two blocks never fight over a position.
-- ON CONFLICT DO NOTHING means a re-run adds nothing and, in the event a person already had a widget
-- under one of these keys, their row wins — which is right: a stored row is a decision.

INSERT INTO dashboard_user_widgets
    (user_id, widget_key, shape, measure, split, branch_id, time_range, size, title, position, visible)
WITH person AS (
    -- ONLY the people §1 carried a chart across for. Not "everyone with a widget row": a person who
    -- has since built a widget through the builder has a layout they arranged, and a re-run of this
    -- file must not tip a role default into it. The `viz.` prefix is exactly the set §1 wrote,
    -- because widget_key is the old composed-tile id verbatim.
    SELECT DISTINCT user_id FROM dashboard_user_widgets WHERE widget_key LIKE 'viz.%'
),
shape_rank AS (
    SELECT p.user_id,
           COALESCE(
               MIN(array_position(ARRAY['admin', 'manager', 'yard', 'counter', 'driver'], rp.profile)),
               4  -- `counter`: the least-privileged useful Home, Service.ProfileFor's own fallback
           ) AS rank
      FROM person p
      LEFT JOIN user_roles ur ON ur.user_id = p.user_id
      LEFT JOIN roles r ON r.id = ur.role_id
      LEFT JOIN dashboard_role_profiles rp ON rp.role_code = r.code
     GROUP BY p.user_id
),
prof AS (
    SELECT user_id, (ARRAY['admin', 'manager', 'yard', 'counter', 'driver'])[rank] AS profile
      FROM shape_rank
),
carried_count AS (
    -- The offset is the number of CHARTS carried, not the number of widget rows: counting rows would
    -- shift every default down by its own previous run and make this file non-idempotent.
    SELECT user_id, count(*)::int AS n
      FROM dashboard_user_widgets
     WHERE widget_key LIKE 'viz.%'
     GROUP BY user_id
)
SELECT pr.user_id, d.widget_key, d.shape, d.measure, d.split, d.branch_id, d.time_range,
       d.size, d.title, c.n + d.position, d.visible
  FROM prof pr
  JOIN carried_count c ON c.user_id = pr.user_id
  JOIN dashboard_profile_widgets d ON d.profile = pr.profile
ON CONFLICT (user_id, widget_key) DO NOTHING;
