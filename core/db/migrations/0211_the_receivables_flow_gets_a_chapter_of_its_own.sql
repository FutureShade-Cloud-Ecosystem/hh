-- 0211 — the receivables flow gets a chapter of its own, and the platform keys get theirs back.
--
-- APPROVED by the commander, 7 Sep 2026 (max(branch) 0210 + 1, confirmed by `ls backend/migrations
-- | tail -1` at the moment of writing, which answered 0210_four_flows_that_finish.sql).
--
-- ── THE DEFECT, AS A PERSON MEETS IT ────────────────────────────────────────────────────────────
--
-- The demo landing's fourth card, "Receivables settings", signs you in as jordan.blake
-- (FINANCIAL_CONTROLLER) and lands you on #finance/ar — and the walkthrough that opens is
-- `chase-invoice`: three steps, two of whose anchors do not resolve on that view, ending on a
-- contact name having written nothing. The five receivables steps the card is about are not in it.
-- They are five positions down a SUPER_ADMIN chapter about keyboard shortcuts.
--
-- TWO MISTAKES MADE THAT, and only the second of them is in a file:
--
--   (a) THE BRIEF 0210 WAS WRITTEN FROM WAS WRONG. It called chapter `your-rules` "the AR settings
--       flow", inferred from its step keys `tiers` and `reset`. On disk `your-rules` is the PLATFORM
--       KEY CONFIGURATOR — owned by SUPER_ADMIN, routed at #admin/hotkeys — and it always was.
--
--       0210 DID NOT SWALLOW THIS. Its header says so in full, under "ONE OF THE FOUR IS NOT WHAT
--       ITS BRIEF CALLED IT" (0210:43-45), names both ways out, and takes the one available to it:
--       it appended five receivables steps (limits, rules, edge, save, history, at positions 3-7)
--       to `your-rules`, making a single chapter hop #admin/hotkeys → #finance/ar. That was the
--       right call for an agent that could not re-role a chapter on its own authority — re-pointing
--       and re-roling `your-rules` would have retired, silently, this deployment's only walkthrough
--       of the platform → dealer → person ladder, and would have been a rename in everything but
--       the id. Recording the discrepancy and building on the ground it had is what an agent should
--       do with a premise it cannot fix. The premise error is the commander's, is owned as such, and
--       this file is the half 0210 was not in a position to write.
--
--   (b) NOTHING NAMED WHICH CHAPTER A FLOW OPENS. tour-layer picked with nextChapter(), which
--       answers the first chapter the CALLER'S ROLE owns; FINANCIAL_CONTROLLER owns `chase-invoice`
--       at position 6, so that is what a controller got whatever the card said. That half is fixed
--       in the binary and the bundle (internal/demo carries a chapter key per flow now, and the
--       mint response hands it to the landing page), not here. It is named because a reader of this
--       file needs to know that the row below is only half the fix: even with a correctly-roled
--       receivables chapter, role-ownership picking would still have opened `chase-invoice`, which
--       is also owned by FINANCIAL_CONTROLLER and sorts ahead of it.
--
-- ── WHY A CORRECTION FILE AND NOT AN EDIT TO 0210 ───────────────────────────────────────────────
--
-- A COMMITTED MIGRATION NEVER MOVES, and the reason is mechanical rather than stylistic.
-- cmd/migrate keys its ledger on the FILENAME — `version := strings.TrimSuffix(filepath.Base(f),
-- ".sql")`, `SELECT EXISTS(… FROM schema_migrations WHERE version=$1)`, and `continue` when it is
-- there. So editing the bytes of 0210 leaves its version string unchanged, every box that has
-- already applied it skips it forever, and the correction exists in git and in no database anybody
-- is looking at. Worse than not fixing it: a from-zero box would then get a schema that disagrees
-- with the demo box and with staging, under one version number, with nothing in either database
-- able to say which of the two it holds. `schema_migrations` on this box already carries
-- 0210_four_flows_that_finish, which is the whole argument in one row.
--
-- The same reasoning is why this file does not "undo" 0210 either. 0210's nineteen added steps are
-- correct and measured; five of them are simply on the wrong chapter. This moves those five and
-- leaves the other fourteen exactly where they are.
--
-- ── WHAT A NEW CHAPTER COSTS, SINCE 0210 DECLINED TO ADD ONE ───────────────────────────────────
--
-- 0210's "DEEPENED IN PLACE, NOT RIVALLED" refused a fifth chapter, and the refusal was right for
-- what it was doing: a chapter BESIDE an existing one splits a role's tour in two and leaves the
-- pill's ordering to arbitrate. What has changed is that a flow now NAMES its chapter, so the demo
-- no longer depends on an ordering at all — which removes the entire cost 0210 was avoiding, and
-- turns "a controller-owned AR chapter would have had to out-sort chase-invoice" (0210's own words)
-- from a reason not to build one into a thing nothing needs any more.
--
-- What remains true either way: `your-rules` keeps its id, its role, its route and its three steps,
-- so nobody's bookmark on it means anything different than it did, and the platform administrator
-- still has a chapter.
--
-- POSITION 9, AT THE END, and not slotted beside chase-invoice at 6. Two reasons and the second is
-- the one that decides it: `position` is the platform operator's to arrange live (it is a column
-- precisely so re-ordering the demo is not a deploy), and a migration that renumbers eight rows to
-- make room for one is a migration that overwrites eight of somebody's decisions to express one of
-- ours. The ordering a controller actually experiences is unaffected — nextChapter sorts a caller's
-- OWN chapters first, so a controller meets chase-invoice then receivables-rules, which is also the
-- order that reads correctly: chase the invoice, then move the line that decided it was late.
--
-- ── THE LINE THAT LOST ITS ANTECEDENT, RECORDED RATHER THAN QUIETLY REWRITTEN ───────────────────
--
-- `limits` reads "Same ladder, different rule: what this dealer calls late." Scrubname0014 `your-rules`,
-- "same ladder" pointed at the three platform-key steps before it. As step 0 of a chapter of its
-- own it points at nothing. The line is moved VERBATIM anyway, because the steps are being RELOCATED
-- and not rewritten — the five lines were measured in a browser as they stand (0210's transcripts),
-- and a migration that edits copy while claiming to move rows is a migration whose diff cannot be
-- checked against what was verified. `line` is also the one column a platform administrator can
-- change live with no deploy, which is what that affordance is for. Whoever wants it to read better
-- runs one statement and no pipeline:
--
--   UPDATE platform_tour_steps SET line = 'The ladder again, on money: what this dealer calls late.'
--    WHERE chapter_key = 'receivables-rules' AND step_key = 'limits';
--
-- ── SAFETY ──────────────────────────────────────────────────────────────────────────────────────
--
-- ROWS ONLY. No CREATE, no ALTER, no DROP; it touches 0192's two tables and nothing else.
--
--   IDEMPOTENT IN BOTH HALVES, in 0205's and 0206's discipline — every statement's WHERE excludes
--   its own result, so a second run matches nothing:
--     §1 INSERT … ON CONFLICT (chapter_key) DO NOTHING — 0192's own posture for a chapter row, so a
--        title or promise a platform operator has edited is never overwritten by a re-run.
--     §2 INSERT … ON CONFLICT (chapter_key, step_key) DO NOTHING — the five steps land once. It
--        LEFT JOINs the rows it is moving from so a line somebody edited on `your-rules` in the last
--        five days travels with its step, while the ANCHOR is taken from the literal below: 0210's
--        rule that "an ANCHOR is not copy — it is the join between a row and a DOM node" cuts the
--        other way from the line beside it, and a stale anchor is the failure this whole lane is
--        about.
--     §3 DELETE, and it removes NOTHING it has not first confirmed landed: the EXISTS clause names
--        the twin on the new chapter, and the `route = '#finance/ar'` predicate means a step_key a
--        platform operator has since repurposed on `your-rules` for a platform-key screen is not
--        touched. After it, no `your-rules` step points at #finance/ar, so the second run is a
--        no-op by construction.
--
--   NO PROGRESS ROW IS HARMED. 0193's tour_user_progress carries chapter_key and step_key as plain
--   TEXT with no foreign key into either table (0193's own note on why), so deleting five step rows
--   cascades nothing. Somebody bookmarked at your-rules/`save` reads back as a step that is no
--   longer in that chapter, which _tour.ts's stepIndexFor already answers with the top of the
--   chapter — the documented behaviour for a retired id, reached here for the first time.
--
--   A BOX THAT NEVER RAN 0210 IS FINE. §2 writes all five steps from its own literals whether or not
--   `your-rules` ever held them, and §3 then finds nothing to delete. The new chapter is complete on
--   a from-zero database and on this one.
--
-- REVERT COST: delete the six rows §1 and §2 add, and re-run 0210's §4 INSERT to put the five steps
-- back on `your-rules` at positions 3-7. It loses the correctly-roled chapter and restores the
-- defect; it loses nothing else, and no row outside these two tables is touched in either direction.

-- ═════════════════ 1 · receivables gets a chapter, owned by the person whose rule it is ═════════
--
-- ROLE IS THE WHOLE POINT OF THIS ROW. `role` is editorial and not an authorization fence (0192 §2),
-- but it is the editorial claim the landing page's card makes out loud — "Signs you in as Scrubname0010
-- Scrubname0007" — and a chapter about a controller's own limits filed under the platform
-- administrator was a claim the data contradicted. The door is reachable by both either way:
-- ar-landing.ts draws "Receivables rules" on isAdmin(), which is tier 1 + GLOBAL scope.
--
-- THE TITLE AND THE PROMISE ARE THE CHAPTER'S OWN and say the two halves of what it does — where the
-- line is, and that moving it leaves a trail. Neither repeats a step's line, which is the budget
-- 0192 set: the promise is the chapter's sentence, not a preview of its first bubble.
INSERT INTO platform_tour_chapters (chapter_key, position, title, promise, role, route) VALUES
  ('receivables-rules', 9, 'What counts as late here',
   'Move the day an account escalates, and leave a trail.',
   'FINANCIAL_CONTROLLER', '#finance/ar')
ON CONFLICT (chapter_key) DO NOTHING;

-- ═════════════════ 2 · the five steps move across, renumbered from 0 ═══════════════════════════
--
-- The step_keys and the lines are 0210 §4's, verbatim; the positions are 3-7 renumbered to 0-4.
--
-- EVERY STEP KEEPS ITS ROUTE even though all five now sit on one surface and the chapter's own route
-- would carry step 0 there. It costs nothing (the layer writes the hash only when it differs) and it
-- buys the same thing 0210 wanted it for: Back is a control this layer draws, and a person who has
-- wandered off #finance/ar is put back on the screen the step is talking about rather than being
-- shown a bubble about a pane they are not looking at.
INSERT INTO platform_tour_steps (chapter_key, step_key, position, route, anchor, line, advance)
SELECT 'receivables-rules', w.step_key, w.position, w.route, w.anchor,
       -- The LINE is carried across if one exists to carry; the ANCHOR never is. See §2's note in
       -- the header: a line is copy and a platform operator may have improved it, an anchor is a
       -- join to a DOM node and the literal below is the one that was measured.
       COALESCE(old.line, w.line), w.advance
  FROM (VALUES
    ('limits',  0, '#finance/ar', 'testid:ar-landing',             'Same ladder, different rule: what this dealer calls late.',   'NEXT'),
    ('rules',   1, '#finance/ar', 'testid:ar-settings-open',       'The receivables rules live behind this one door.',            'NEXT'),
    ('edge',    2, '#finance/ar', 'testid:as-aging_escalate_days', 'Escalation is the day a late account becomes a phone call.',  'NEXT'),
    -- THE WRITE.
    ('save',    3, '#finance/ar', 'testid:as-save-edges',          'Save. The landing re-buckets every account on its next read.', 'CLICK'),
    ('history', 4, '#finance/ar', 'testid:as-history',             'Every change is kept: the rule, the old value, the new, who.', 'NEXT')
  ) AS w(step_key, position, route, anchor, line, advance)
  LEFT JOIN platform_tour_steps old
         ON old.chapter_key = 'your-rules'
        AND old.step_key    = w.step_key
        AND old.route       = '#finance/ar'
ON CONFLICT (chapter_key, step_key) DO NOTHING;

-- ═════════════════ 3 · `your-rules` is the platform-key chapter again, and only that ═══════════
--
-- Three steps, at #admin/hotkeys, which is what it has been since 0192 and what its role says it is.
-- Nothing is written here — `open`, `tiers` and `reset` keep 0210's improvements, which were the
-- right half of that file's work on this chapter: all three used to point at `tag:hos-platform-
-- hotkeys`, the whole screen three times, and two of them now point at the Set and Unbind controls
-- the sentences are actually about. Only the five borrowed steps leave.
--
-- THE PROMISE IS LEFT ALONE, and it over-promises by one noun. "The keys, the columns and the limits
-- are yours to set" is 0192's copy, and the limits it names are now a chapter away. Two things
-- decide it: 0210 reached for those five steps precisely BECAUSE of that sentence, so rewriting it
-- here would erase the evidence of how this happened; and the promise is a platform operator's to
-- edit live, which is the affordance to spend on a sentence that is merely ambitious rather than
-- wrong — the columns are on that screen and the limits are one chapter down the same pill.
DELETE FROM platform_tour_steps s
 WHERE s.chapter_key = 'your-rules'
   AND s.step_key IN ('limits', 'rules', 'edge', 'save', 'history')
   -- The receivables route is what marks these five as the borrowed ones. A step_key somebody has
   -- since re-pointed at a platform-key control does not match and is left where it is.
   AND s.route = '#finance/ar'
   -- AND IT LANDED. Never delete a row on the assumption that §2 wrote its twin — if §2 inserted
   -- nothing, this deletes nothing and the box keeps the defect rather than losing five rows to
   -- nowhere.
   AND EXISTS (SELECT 1
                 FROM platform_tour_steps n
                WHERE n.chapter_key = 'receivables-rules'
                  AND n.step_key    = s.step_key);
