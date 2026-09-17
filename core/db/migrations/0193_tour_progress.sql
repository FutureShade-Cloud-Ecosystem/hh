-- 0193 — WHERE ONE PERSON HAD GOT TO, so a tour can be left and picked up.
--
-- THE REQUIREMENT (item 1d): "Resumable, dismissible, restartable per user (user substrate).
-- Progress pill, never a modal wall."
--
-- ── WHY NOT surface_user_workspace, WHICH IS THE USER SUBSTRATE ─────────────────────────────────
--
-- 0172's `surface_user_workspace` is one row per (user, surface) with a FIXED column set — split,
-- arrangement, two collapse flags, cost_hidden, open_ref. 0174 added `open_ref` to it and its own
-- header is careful about why that was allowed: it is surface-NEUTRAL display state. "Which step of
-- which chapter" is neither neutral nor one value — it is a row per chapter — so putting it there
-- would mean either a tour-shaped column on a surface-neutral table (the thing 0174 was careful not
-- to do) or a `surface_user_items` row per step, which is a schema hiding inside an item id.
--
-- So: its own table, keyed the way the thing actually is.
--
-- ── WHOLE-OR-ABSENT, AND RESET IS A DELETE ──────────────────────────────────────────────────────
--
-- 0172's ruling, verbatim in shape: "deleting the rows IS 'reset to default' — there is no merge to
-- unwind". A person who restarts a chapter has their row DELETED, not overwritten with step 0, so
-- "never started" and "started and went back to the beginning" are the same state — because to the
-- person they are.
--
-- The same rule one level up is why 0191's reset had to be fixed: writing the defaults INTO the level
-- being reset makes a one-way door. There is no level beneath this one, but the habit is the point.
--
-- ── NO updated_by, AND NO AUDIT ─────────────────────────────────────────────────────────────────
--
-- 0172:39-48 made `surface_user_items` carry no `updated_by` on the argument that a person's override
-- of their own screen needs no attribution. The same is true here and more so: where somebody got to
-- in a walkthrough is not a business fact, it is a bookmark. Nothing audits it, nothing reports on it,
-- and a dealer's manager has no read of it.
--
-- ── WHY NOT localStorage, WHICH IS WHERE THE APP PUTS BOOKMARKS ─────────────────────────────────
--
-- `_catalogue-view.ts:12-19` sets out the house rule: a per-viewer convenience with no role default
-- and no admin surface belongs in a browser key. This one fails the first half of that test. The
-- point of the demo is that Scrubname0025 opens it on his laptop after seeing it on somebody's phone, and a
-- bookmark that lives in one browser is not a resumable walkthrough — it is a resumable browser.
--
-- REVERT COST: one DROP TABLE. Nothing else references it. Safe against a staging database that is
-- never wiped; this file is ADD-only.

CREATE TABLE IF NOT EXISTS tour_user_progress (
    user_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    chapter_key TEXT NOT NULL,
    -- The step they are ON, not the step they finished — so a resume puts them back where they were
    -- looking rather than one past it. '' means the chapter is done or dismissed and there is no
    -- step to return to.
    step_key    TEXT NOT NULL DEFAULT '',
    -- OPEN      partway through; resume here.
    -- DONE      ran it to the end. Restartable, but not offered again unprompted.
    -- DISMISSED closed it deliberately. The strongest signal a person can send, and the one the
    --           product must respect hardest: a tour that comes back after being dismissed is the
    --           defect every SaaS onboarding is remembered for.
    state       TEXT NOT NULL DEFAULT 'OPEN' CHECK (state IN ('OPEN', 'DONE', 'DISMISSED')),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, chapter_key)
);

CREATE INDEX IF NOT EXISTS tour_user_progress_user_idx
    ON tour_user_progress (user_id);

COMMENT ON TABLE tour_user_progress IS
  'Scrubname0032 one person had got to in one chapter of the guided walkthrough. One row per (user, chapter), whole-or-absent: restarting DELETES the row rather than writing step 0, because "never started" and "back to the beginning" are the same state to the person. No updated_by and no audit — a bookmark is not a business fact. DISMISSED is the state the product must respect hardest.';

COMMENT ON COLUMN tour_user_progress.step_key IS
  'The step they are ON, not the one they finished, so a resume returns them to what they were looking at. Empty when the chapter is DONE or DISMISSED. Not a foreign key: 0192''s ids are add-only and a retired step must leave a stale bookmark readable rather than cascade a person''s history away.';
