-- 0207 — the bento becomes twelve columns wide, and a widget may say its own span.
--
-- APPROVED by the commander, 2 Sep 2026 (max(branch) 0206 + 1, confirmed by `ls backend/migrations
-- | tail -1` at the moment of writing, which answered 0206_a_demo_row_names_a_demo_person.sql).
--
-- ── THE DISCOVERY THAT MAKES THIS FILE SMALL, AND WHY THERE IS NO DATA MIGRATION ────────────────
--
-- Home v3.5 rules the grid to TWELVE columns at >=1280px (six at tablet and phone) on a 132px row
-- unit, with six named presets:
--
--     S 3x1  ·  Tall 3x2  ·  M 6x1  ·  L 6x2  ·  Wide 12x1  ·  free 3-12 cols x 1-3 rows
--
-- The old grid was FOUR columns: S 1x1 · M 2x1 · L 2x2 · XL 4x1. Read the two lists beside each
-- other and the new grid is the old one SCALED EXACTLY BY THREE — 1 becomes 3, 2 becomes 6, 4
-- becomes 12, and every row count is untouched. That is not a coincidence anybody has to maintain;
-- it is what makes this migration cheap, and it is worth stating plainly:
--
--   EVERY STORED ROW HOLDS A PRESET LETTER, NOT A SPAN. 0201 chose `size text CHECK (size IN
--   ('S','M','L','XL'))` and put the arithmetic in Go (dashboard.SpanOf) and nowhere else, precisely
--   so the grid could be re-ruled without touching a row. Remapping SpanOf therefore moves all
--   thirty seeded role defaults AND every person's saved layout onto the twelve-column grid at
--   IDENTICAL PROPORTIONS — a card that was a quarter of the width is still a quarter of the width,
--   a card that was half is still half — with nothing lost, nothing recomputed and no UPDATE.
--
-- So the brief's requirement that "existing layouts migrate by preset->span mapping" is satisfied BY
-- CONSTRUCTION, before this file executes a single statement. What this file is actually for is the
-- two things the x3 map cannot give: FREE SPANS (the grip resizes to real grid lines, not only to
-- the five presets) and the SIXTH PRESET, `T` for Tall.
--
-- ── §1 · WHY cols/rows ARE NULLABLE, AND WHY size STAYS THE PRIMARY VOCABULARY ──────────────────
--
-- NULL means "the preset decides". It is not "unknown" and it is not zero: it is the ordinary state
-- of every row in both tables today and of most rows forever after, because a person who never
-- touches the grip has no opinion about columns — they picked a size.
--
-- The alternative was to store a span on every row and retire `size` to a label. That is worse for
-- the reason 0201 gives about `title`: a stored derivation freezes a decision the product has not
-- made yet. `size` is the vocabulary the MENU speaks (the `...` menu's explicit S/M/L/XL choice, the
-- keyboard Alt-up/down resize, the doctrine's own table), and it is the vocabulary a role default is
-- authored in. If every row carried 6x2 instead of 'L', the day the doctrine re-rules L to 6x3 no
-- existing layout would follow it — every card would be frozen at whatever the grid meant on the
-- afternoon somebody dragged it. Keeping the letter primary is what let THIS file be free.
--
-- So: size is the answer, and cols/rows are an OVERRIDE somebody had to reach for. Both or neither —
-- half a span is not a span, and Go's NormalizeWidgets clears a half-pair back to the preset rather
-- than refusing the save, because an older client that omits the two new fields must never have its
-- Home refused (the lesson widget.go:445-450 already learned the hard way about `range`).
--
-- THE COLUMN SET IS THE SNAP SET, NOT AN ARBITRARY RANGE. 3, 4, 6, 8, 9, 12 — the divisors and
-- half-divisors of twelve that also survive the six-column breakpoint. 5, 7, 10 and 11 are excluded
-- deliberately: they leave a gap no other preset can fill on the row beside them, and a bento only
-- works if every widget lands on a line the next widget can start from. Rows are 1-3; a fourth row
-- is 560px of one card, which is a page, not a widget.
--
-- ── §2 · WIDENING size TO ADMIT 'T', AND WHY THAT IS SAFE ───────────────────────────────────────
--
-- 0201 licensed this in writing, at its line 47: "A seventh shape is a new component and therefore a
-- release anyway, so a migration to go with it costs nothing." The same sentence governs a sixth
-- SIZE, and for the stronger reason — `size` decides a grid span, which is the substrate's own
-- vocabulary and cannot be widened by adding a measure.
--
-- 0201 wrote the constraint INLINE and UNNAMED, so Postgres named it for us. Verified against the
-- live database before writing this file:
--
--     dashboard_profile_widgets_size_check   CHECK (size = ANY (ARRAY['S','M','L','XL']))
--     dashboard_user_widgets_size_check      CHECK (size = ANY (ARRAY['S','M','L','XL']))
--
-- The change is DROP IF EXISTS then ADD under the SAME NAME. Naming it explicitly on the way back in
-- is the point: the constraint stops being an accident of the parser and becomes something the next
-- migration can address by name without first querying pg_constraint, which is what this file had to
-- do. `\d` reads identically afterwards, so nothing looks renamed.
--
-- IT IS A WIDENING AND ONLY A WIDENING. Every value the old constraint admitted the new one admits;
-- no stored row can fail the re-add, so the validating scan Postgres runs over both tables cannot
-- abort the transaction. The revert is symmetric — drop, re-add the four-value list — and is only
-- unsafe if somebody has already stored a 'T', which is exactly the shape every widening has.
--
-- ── §3 · WHAT WAS RE-AUTHORED IN THE ROLE DEFAULTS: ONE ROW OF THIRTY ───────────────────────────
--
-- Stated at the top and argued below: `yard` / `picks.gauge` moves S -> T. Nothing else changes,
-- every widget_key stays, no row is inserted and no row is deleted.
--
-- WHY SO LITTLE. Because the x3 map is PROPORTION-PRESERVING, no seeded layout got worse — each of
-- the thirty cards occupies exactly the fraction of the screen it occupied yesterday. A re-author
-- can therefore only be justified where the NEW vocabulary says something the old grid could not,
-- and there is exactly one such place in thirty rows.
--
-- WHY THAT ONE. `T` is 3x2, and the doctrine's own table calls it "a stat with a ring gauge or short
-- list under it"; the pop-kit ruling is more specific still — "gauges at Tall+ render as RINGS
-- (stroke = severity, track = --hos-track, value centred); the flat meter remains the S form."
-- `yard`/`picks.gauge` is the ONLY gauge in all thirty seeded rows, it is the dispatcher's severity
-- read (how deep is the queue, right now), and at S it is a flat meter under a number — the one form
-- that cannot show severity as a fill. Tall is the form the preset was ruled for and this is the row
-- it was ruled for. The measure (`picks.queue`) offers the gauge shape in the Go catalog, so the row
-- stays legal in this build.
--
-- WHY NOT THE OTHER TWENTY-NINE, since the question will be asked:
--   · THE FOUR HEROES STAY L. Not only because the doctrine says the hero is M or L, but because
--     two committed tests assert it — widget_test.go's seeded-defaults gate over 0201's text and
--     widget_integration_test.go's over the rows Postgres actually holds. A migration that quietly
--     broke a gate about the thing it is changing would be the worst possible way to change it.
--   · THE FOUR needs.you ROWS STAY L AT POSITION 1. The doctrine names Needs-you as L outright and
--     it is the one widget the doctrine says a person may never lose.
--   · THE ELEVEN OTHER `S` ROWS ARE PLAIN STATS, and a plain stat at Tall is a number with 132px of
--     nothing under it. The doctrine's S row reads "a stat, a clock/status"; that is what they are.
--   · NOBODY IS PROMOTED TO `XL`/Wide. Wide (12x1) was XL (4x1) yesterday — full width both times —
--     so promoting a card to it is not the wider grid making a better default, it is a redesign
--     wearing this migration's clothes. 0201 refused to re-file a role for the same reason: "a
--     migration that edits somebody's existing screen to make its own seed read better is a
--     migration nobody can trust."
--
-- THIS TOUCHES ONLY dashboard_profile_widgets. Not one row of dashboard_user_widgets is read or
-- written here. A person who has arranged their own Home chose those sizes, and a migration that
-- re-sized somebody's personal layout because the grid got wider would be doing to them exactly what
-- 0191's reset did — changing a choice and leaving no way to tell it had been changed.
--
-- ── REVERT COST ────────────────────────────────────────────────────────────────────────────────
--
-- Two DROP COLUMNs, two constraint swaps back to the four-value list, and one UPDATE putting
-- `yard`/`picks.gauge` back to 'S'. The only thing lost is free spans people have dragged and any
-- row stored as 'T'. Safe against a staging database that is never wiped, with that one caveat.

-- ── §1 · free spans ────────────────────────────────────────────────────────────────────────────

ALTER TABLE dashboard_profile_widgets
    ADD COLUMN IF NOT EXISTS cols smallint,
    ADD COLUMN IF NOT EXISTS rows smallint;

ALTER TABLE dashboard_profile_widgets
    DROP CONSTRAINT IF EXISTS dashboard_profile_widgets_cols_check;
ALTER TABLE dashboard_profile_widgets
    ADD CONSTRAINT dashboard_profile_widgets_cols_check
        CHECK (cols IS NULL OR cols IN (3, 4, 6, 8, 9, 12));

ALTER TABLE dashboard_profile_widgets
    DROP CONSTRAINT IF EXISTS dashboard_profile_widgets_rows_check;
ALTER TABLE dashboard_profile_widgets
    ADD CONSTRAINT dashboard_profile_widgets_rows_check
        CHECK (rows IS NULL OR rows BETWEEN 1 AND 3);

ALTER TABLE dashboard_user_widgets
    ADD COLUMN IF NOT EXISTS cols smallint,
    ADD COLUMN IF NOT EXISTS rows smallint;

ALTER TABLE dashboard_user_widgets
    DROP CONSTRAINT IF EXISTS dashboard_user_widgets_cols_check;
ALTER TABLE dashboard_user_widgets
    ADD CONSTRAINT dashboard_user_widgets_cols_check
        CHECK (cols IS NULL OR cols IN (3, 4, 6, 8, 9, 12));

ALTER TABLE dashboard_user_widgets
    DROP CONSTRAINT IF EXISTS dashboard_user_widgets_rows_check;
ALTER TABLE dashboard_user_widgets
    ADD CONSTRAINT dashboard_user_widgets_rows_check
        CHECK (rows IS NULL OR rows BETWEEN 1 AND 3);

-- ── §2 · the sixth preset ──────────────────────────────────────────────────────────────────────
--
-- Drop the unnamed-and-therefore-auto-named constraint, re-add it under the same name with 'T' in
-- the list. See §2 of the header for why this cannot fail on stored data.

ALTER TABLE dashboard_profile_widgets
    DROP CONSTRAINT IF EXISTS dashboard_profile_widgets_size_check;
ALTER TABLE dashboard_profile_widgets
    ADD CONSTRAINT dashboard_profile_widgets_size_check
        CHECK (size IN ('S', 'T', 'M', 'L', 'XL'));

ALTER TABLE dashboard_user_widgets
    DROP CONSTRAINT IF EXISTS dashboard_user_widgets_size_check;
ALTER TABLE dashboard_user_widgets
    ADD CONSTRAINT dashboard_user_widgets_size_check
        CHECK (size IN ('S', 'T', 'M', 'L', 'XL'));

-- ── §3 · the one re-authored role default ──────────────────────────────────────────────────────
--
-- The dispatcher's queue-depth gauge becomes a ring. Guarded on the CURRENT value so this is a no-op
-- on a dealer who has already re-authored that row through the admin editor — a migration must never
-- overwrite a decision somebody made after it was written.

UPDATE dashboard_profile_widgets
   SET size = 'T', updated_at = now()
 WHERE profile = 'yard'
   AND widget_key = 'picks.gauge'
   AND size = 'S';

-- ── comments ───────────────────────────────────────────────────────────────────────────────────

COMMENT ON COLUMN dashboard_profile_widgets.size IS
  'Bento preset, and the PRIMARY vocabulary: S 3x1 | T 3x2 | M 6x1 | L 6x2 | XL 12x1 on the twelve-column grid. Value-CHECKed because it is the grid''s own vocabulary; cols/rows override it when somebody reached for the grip.';
COMMENT ON COLUMN dashboard_user_widgets.size IS
  'Bento preset, and the PRIMARY vocabulary: S 3x1 | T 3x2 | M 6x1 | L 6x2 | XL 12x1. See the profile table''s column comment.';

COMMENT ON COLUMN dashboard_profile_widgets.cols IS
  'FREE column span, 3-12 on the snap set (3,4,6,8,9,12). NULL means "the preset decides" and is the ordinary state of a row nobody dragged — not unknown, not zero. Set only with rows: half a span is not a span.';
COMMENT ON COLUMN dashboard_profile_widgets.rows IS
  'FREE row span, 1-3 at the 132px row unit. NULL means "the preset decides". A fourth row is 560px of one card, which is a page rather than a widget, so the CHECK stops at three.';

COMMENT ON COLUMN dashboard_user_widgets.cols IS
  'FREE column span the person dragged to, on the snap set (3,4,6,8,9,12). NULL means "the preset decides" — see the profile table''s column comment.';
COMMENT ON COLUMN dashboard_user_widgets.rows IS
  'FREE row span the person dragged to, 1-3. NULL means "the preset decides" — see the profile table''s column comment.';
