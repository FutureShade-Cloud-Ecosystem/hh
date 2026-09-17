-- 0203 — the dispatcher gets the dock Home.
--
-- RULED by the commander, 1 Sep 2026, on the open question 0201 left behind.
--
-- ── THE QUESTION 0201 COULD NOT ANSWER ──────────────────────────────────────────────────────────
--
-- Item 6 asks for role default layouts for four personas: general manager, dispatcher, controller,
-- counter lead. But 0140 files LOGISTICS_MANAGER under the `manager` job shape, alongside
-- FINANCIAL_CONTROLLER — so two of the four resolve to the SAME shape, and a dispatcher opened on
-- the controller's Home: an AR-aging hero, an aging chart, a sales trend and margin.
--
-- 0201 seeded `yard` as the dock Home (a deliveries hero, the pick board, by-branch bars, a queue
-- gauge) and deliberately did NOT re-file the dispatcher, because that is one row of data an admin
-- edits and moving it in a widget migration would have changed more than widgets, silently. This
-- file makes the move, having first measured exactly what else it changes.
--
-- ── WHAT THIS ROW ACTUALLY DRIVES, MEASURED BEFORE MOVING IT ────────────────────────────────────
--
-- `dashboard_role_profiles` is, in settings/store.go:4's own words, "one role→shape truth
-- program-wide; a second mapping for the same question would drift". So this is not a Home setting.
-- Every consequence below was queried out of a from-zero database rather than reasoned about:
--
--   THE WIDGET HOME — the point of the change.
--     manager: ar.aging hero, aging, sales trend, margin      (8 widgets)
--     yard:    deliveries.today hero, pick board, branch bars  (7 widgets)
--
--   THE CLASSIC TILES — 2 gained, 6 lost.
--     gains  picks.stale · weight.pending
--     loses  ar.aging · inventory.low_stock · orders.open · payments.today · sales.today ·
--            warranty.open_claims
--
--   THE PINNED TAB STRIP — the dispatcher's day, and this one argues for the move on its own.
--     manager: sales/pos, finance/ar, finance/statements
--     yard:    yard/yard, dispatch/dispatch
--     A dispatcher whose pinned tabs were the till and the statements had the wrong three tabs.
--
--   THE COMPOSER'S METRIC MENU — 3 fewer measures OFFERED: ar.aging, inventory.value,
--     margin.gross_bps. Note OFFERED: 0182's own header is explicit that curation decides the menu
--     and never the gate, and the Go gate (viz.go MetricsFor) drops cost-class metrics for a caller
--     who is not manager-up REGARDLESS of curation. A LOGISTICS_MANAGER is Tier 2, so they keep
--     every permission they had; three measures simply stop being offered in the builder. If a
--     dealer wants margin back on a dispatcher's composer that is one curation row, not a code change.
--
--   EVERY OTHER CURATED SURFACE IS IDENTICAL between the two shapes — ar.invoices, ar.landing,
--     dispatch.loadboard, pos.counter and quotes.builder all carry the same items for `manager` and
--     `yard`, verified item id by item id and not merely by count. So the quote builder's columns,
--     the counter's chips and the load board's own curation do not move at all.
--
-- ── THE ONE CONSEQUENCE THAT IS NOT OBVIOUSLY WANTED, RECORDED RATHER THAN GLOSSED ──────────────
--
-- `dashboard_profile_home` gives the `yard` shape a LANDING TAKEOVER: landing_unit=yard,
-- landing_module=yard, takeover=true, mobile_only=true. So a dispatcher ON A PHONE will now land on
-- the yard console rather than on Home. On a desktop nothing changes.
--
-- That is arguably right — the dock is where a dispatcher stands — and arguably not, since their own
-- surface is the load board. It is NOT changed here, for the reason this file exists at all:
-- `dashboard_profile_home` is keyed by PROFILE, so re-pointing it would move the YARD TEAM's landing
-- too, and nobody asked for that. If the commander wants dispatchers landing on the load board, that
-- needs either its own ruling on the yard shape's landing or a sixth job shape — and a sixth shape is
-- the thing 0191:15-23 refused for a different question, so it would need arguing on its own terms.
--
-- ── WHO THIS MOVES ─────────────────────────────────────────────────────────────────────────────
--
-- One account holds LOGISTICS_MANAGER on a from-zero database (0200's dispatch.demo persona), and it
-- has NO saved tile layout of its own — so nobody loses a Home they arranged. A person who HAS
-- arranged one keeps it either way: a user layout overrides the role default, and this file does not
-- touch a single user row.
--
-- ── SAFETY ──────────────────────────────────────────────────────────────────────────────────────
--
-- One UPDATE of one row, scoped to the role code AND to the value it is moving FROM — so a dealer
-- who has already re-filed this role themselves is not overwritten, which is the discipline 0191:116
-- set for a scoped write. Idempotent: a second run matches nothing. Reverting is the same UPDATE
-- with the values swapped.

UPDATE dashboard_role_profiles
   SET profile    = 'yard',
       updated_at = now()
 WHERE role_code = 'LOGISTICS_MANAGER'
   AND profile   = 'manager';

COMMENT ON TABLE dashboard_role_profiles IS
  'The one role→job-shape truth program-wide: it drives the widget Home, the classic tiles, the pinned tab strip, the composer''s metric menu and every surface_role_items curation. LOGISTICS_MANAGER files under `yard` (the dock) rather than `manager` — ruled 1 Sep 2026, see migration 0203 for the full measured consequence.';
