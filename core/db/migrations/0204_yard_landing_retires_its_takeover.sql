-- 0204 — the yard shape lands on its own Home. The driver keeps the run sheet.
--
-- RULED by the commander, 1 Sep 2026, on the question 0203 left open.
--
-- ── THE QUESTION ────────────────────────────────────────────────────────────────────────────────
--
-- 0203 filed LOGISTICS_MANAGER under `yard`, which is right for their Home, their tiles and their
-- pinned tabs — and carried one consequence nobody had ruled on: the `yard` shape holds a LANDING
-- TAKEOVER, so a dispatcher on a phone landed on the yard console instead of Home. The shape now
-- holds three roles that want different phones:
--
--   YARD_TEAM         a tablet at a rack        -> the pick queue
--   YARD_MANAGER      a tablet in the yard      -> the pick queue
--   LOGISTICS_MANAGER a phone at a desk         -> the load board, not the yard console
--
-- Two of three wanted the console and one did not, and the table is keyed by PROFILE, so there was
-- no row to give the third a different answer.
--
-- ── THE ANSWER IS NOT "PICK ONE OF THE THREE" ───────────────────────────────────────────────────
--
-- 0141 wrote down WHY the takeover exists, and it is worth quoting because it is the thing that
-- changed:
--
--     "A driver opening the app on a phone should land on the run sheet. A yard hand on a tablet
--      should land on the pick queue. Neither should have to find the cockpit, READ TILES THEY
--      CANNOT ACT ON, and then navigate."
--
-- "Read tiles they cannot act on" was a true description of Home in July: a grid of equal boxes,
-- every tile the same weight, answering "what are the numbers" and never "what needs me". Skipping
-- it was better than showing it.
--
-- Item 6 replaced that Home. The `yard` shape's default layout now opens, in order:
--
--     0  L  stat   deliveries.today   the hero
--     1  L  list   needs.you          what is waiting on THIS person
--     2  L  board  picks.queue        the pick queue itself
--
-- So the takeover now skips a screen whose first three widgets are the day's deliveries, the queue
-- of things waiting on the person, and the pick board — to reach the pick board. And because every
-- widget is a link (ruled 1 Sep 2026), the board is one tap from the Home it was skipping.
--
-- Worse than redundant, it is now COSTLY: a yard hand who is taken over never sees the Needs-you
-- queue built for them, which is the one thing item 6's second rule exists to put in front of
-- somebody — "Home leads with what needs the person, not what the yard did."
--
-- ── THE DRIVER KEEPS THEIRS, AND THAT IS NOT AN INCONSISTENCY ───────────────────────────────────
--
-- The `driver` shape has NO widget Home. Item 6 seeded role defaults for admin, manager, yard and
-- counter — the four personas the addendum names — and deliberately not for drivers, whose surface
-- is the run sheet and who have no cockpit worth landing on. 0141's sentence about the driver is
-- therefore still exactly true, so their takeover is untouched.
--
-- The rule that falls out, and the one to apply to the next shape: A TAKEOVER IS RIGHT WHERE THERE
-- IS NO HOME WORTH LANDING ON, AND WRONG WHERE THERE IS. It is not a property of the role; it is a
-- property of what the role's Home has become.
--
-- ── WHAT THIS DOES AND DOES NOT CHANGE ──────────────────────────────────────────────────────────
--
-- Desktop is untouched — `mobile_only` was already true, so this only ever applied on a phone or a
-- tablet. The landing_unit/landing_module columns are LEFT IN PLACE rather than nulled: the shape
-- keeps its recorded "where the yard goes" so a dealer who disagrees turns the takeover back on with
-- one boolean instead of having to know the answer was `yard/yard`. That is also why the CHECK
-- (`NOT takeover OR landing_unit IS NOT NULL`) is satisfied either way.
--
-- THIS RULING IS COUPLED TO ITEM 6 SHIPPING. It is only correct because the yard shape has a Home
-- that leads with the queue. If item 6 were dropped from this train, this file should be reverted
-- with it — one UPDATE setting takeover back to true — or a yard hand lands on the old tile grid,
-- which is the screen 0141 was protecting them from.
--
-- ── SAFETY ──────────────────────────────────────────────────────────────────────────────────────
--
-- One UPDATE of one row, scoped to the profile AND to the value it moves FROM, so a dealer who has
-- already turned this off is not touched and a second run matches nothing. Reverting is the same
-- statement with the boolean flipped.

UPDATE dashboard_profile_home
   SET takeover   = false,
       updated_at = now()
 WHERE profile  = 'yard'
   AND takeover = true;

COMMENT ON COLUMN dashboard_profile_home.takeover IS
  'Whether this job shape skips Home on a small screen. TRUE only where the shape has no Home worth landing on — `driver`, whose surface is the run sheet. The `yard` shape carried one until item 6 gave it a Home that opens on the pick queue and the Needs-you list; skipping that screen meant a yard hand never saw the queue built for them (ruled 1 Sep 2026, migration 0204).';
