-- 0212 — Reports is a surface now, so the manager and admin strips can stop pointing at a stand-in.
--
-- APPROVED by the commander, 8 Sep 2026 (max(branch) 0211 + 1, confirmed by `ls backend/migrations
-- | sort | tail -1` at the moment of writing, which answered
-- 0211_the_receivables_flow_gets_a_chapter_of_its_own.sql, and by a search of every branch for an
-- 0212 that might be in flight — there is none).
--
-- ── WHY THIS ROW SAYS SOMETHING IT DID NOT MEAN ─────────────────────────────────────────────────
--
-- The 0140/0141 role-default rulings name a REPORTS tab for the manager and admin strips. When
-- those defaults were seeded there was no reports surface to name: DIB-95's console had shipped —
-- twenty backend routes, generated parameter forms, its own gates — and was reachable only from
-- src/reporting-walkthrough.html, a screenshot harness. NOTES-FIX-433 wrote that down rather than
-- quietly dropping the tab:
--
--     "Reports is not a surface. … So Reports resolves to Finance -> Scrubname0029
--     (finance/statements — Income Statement, Balance Sheet, GL Detail, AR Aging), the reports
--     surface that IS registered, and the tab wears its registry label, 'Scrubname0029'. Escalated."
--
-- and recorded the way out in the same file:
--
--     "Registering the DIB-95 reporting console as a first-class unit (registry entry, pane, rail,
--     gate, launcher tile) is its own ticket; once it exists, the manager and admin strips should be
--     re-pointed at it — which is one UPDATE on dashboard_profile_tabs, not a release."
--
-- The unit was registered in the commit before this one. This is the UPDATE, and it is a migration
-- rather than a one-off because a data fix nobody remembers to run on staging is a data fix that
-- exists on one laptop.
--
-- ── WHAT IT WILL NOT DO ─────────────────────────────────────────────────────────────────────────
--
-- IT DOES NOT TOUCH A STRIP A HUMAN HAS EDITED. dashboard_profile_tabs.updated_by is NULL for a
-- seeded default and carries a user id once somebody has saved through
-- PUT /api/v1/dashboard/profiles/{profile}/tabs. A dealer who has arranged their own manager strip
-- has made a decision, and a migration that overwrote it would be this file deciding it knew better
-- than the administrator who was there. The WHERE clause is the whole safeguard: on a box where
-- these rows have been customised, this migration correctly does nothing at all.
--
-- IT DOES NOT ADD A TAB. Scrubname0029 is not being removed from the product or from Finance — it
-- keeps its own place under the finance unit, where it belongs. What is happening is narrower: the
-- REPORTS slot stops being filled by a stand-in and starts being filled by Reports. Adding a fourth
-- tab instead would hand every manager a strip nobody ruled on.
--
-- IT DOES NOT FIRE TWICE. Re-running matches nothing, because the source value is gone after the
-- first run; and the NOT EXISTS guard means that even on a box where somebody has already added
-- reporting/reporting by hand, this cannot collide with the (profile, tab_key) primary key.

UPDATE dashboard_profile_tabs AS t
   SET tab_key    = 'reporting/reporting',
       updated_at = now()
 WHERE t.profile IN ('manager', 'admin')
   AND t.tab_key = 'finance/statements'
   AND t.updated_by IS NULL
   AND NOT EXISTS (
         SELECT 1
           FROM dashboard_profile_tabs x
          WHERE x.profile = t.profile
            AND x.tab_key = 'reporting/reporting'
       );
