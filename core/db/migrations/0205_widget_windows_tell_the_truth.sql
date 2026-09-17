-- 0205 — a widget's window says what its number actually measures.
--
-- APPROVED by the commander, 1 Sep 2026 (max(branch) 0204 + 1, confirmed before writing).
--
-- ── THE DEFECT ──────────────────────────────────────────────────────────────────────────────────
--
-- Item 6 gave every widget a `time_range` and the builder offered all ten named windows for every
-- measure. Nothing on the measurement path takes a window: SeriesProvider.Series is
-- (ctx, scope, dimension), and not one query below it carries a caller-supplied date predicate. So
-- the window was stored, echoed to the builder, and PRINTED as the card's context line — the one
-- sentence under the number, and on a small widget the only sentence there — while the number
-- itself was measured over something else entirely.
--
-- ELEVEN OF THE THIRTY ROLE DEFAULTS 0201 SEEDS SHIP THAT WAY, on all four personas. This is not a
-- dealer's misconfiguration to be tolerated; it is what a fresh install shows before anybody has
-- touched anything:
--
--   admin    orders.trend      "Open orders · last 14 days"    orders.open has NO date column in
--                                                              either of its queries. It drew every
--                                                              open order ever written.
--   admin    margin.branch     "Margin by branch · this month" every order line on every order that
--   manager  margin.branch                                     is not DRAFT or CANCELLED, ever.
--   admin    picks.queue       "Pick queue · today"            a status set, not a period. Three of
--   yard     picks.board                                       the four shapes seeded it this way.
--   yard     picks.gauge
--   counter  picks.queue
--   counter  orders.trend      "Open orders · last 14 days"
--   manager  sales.trend       "Scrubname0027 trend · last 30 days"    the SQL is hard-filtered to ONE day
--                                                              (s.created_at >= $2 AND < $2 + 1d).
--   admin    inventory.value   "Inventory value · everything   a live count from stock_balances. See
--   manager  inventory.value    open"                          the vocabulary note below.
--
-- The Go half of this change is committed alongside: MetricMeta.Ranges declares, per measure, the
-- windows its provider can actually honour (one each, today); ValidateWidgets refuses any other;
-- WidgetViewsFor repairs a stored one on read. This file fixes the ROWS, and it is not optional —
-- see "why a migration and not just the repair" below.
--
-- ── THE TWO NEW WINDOW WORDS, AND WHY `open` COULD NOT COVER THEM ───────────────────────────────
--
-- `open` means "the measure's own natural population" and reads on screen as "Everything open".
-- That is exactly right for a population that is genuinely open — unpaid invoices, unclosed orders,
-- unresolved claims, a pick queue — and wrong for the other two shapes a measure with no date
-- predicate can have. Correcting those two to `open` would have replaced one false caption with
-- another, which is not a fix:
--
--   all_time  "All time"   — margin.gross_bps counts INVOICED and CLOSED order lines too, so its
--                            population is not open, it is everything.
--   now       "Right now"  — inventory.value reads stock_balances, a current-state table with no
--                            history behind it. There is no window to open, only this moment.
--
-- Both fit the existing shape-only CHECK (`^[a-z][a-z0-9_]{0,31}$`) so there is no ALTER here. That
-- is by 0201's own design, quoted from its line 50: the column carries an alphabet-and-length check
-- and "their meaning lives in the Go catalog" — so a vocabulary change is a Go change plus this.
--
-- ── WHY A MIGRATION AND NOT JUST THE READ REPAIR ────────────────────────────────────────────────
--
-- WidgetViewsFor repairs a bad window on the way to a person's screen, so every CARD tells the truth
-- the moment the Go half deploys. It is not enough, for two reasons, and the second is a live
-- regression rather than a nicety:
--
--   1. The admin role-default editor reads profile rows RAW. ProfileWidgetsFor returns them through
--      ResolveWidgets and never through WidgetViewsFor, so the repair does not reach that screen —
--      and its save runs ValidateWidgets. Without this file, an admin who opens the role-default
--      editor and presses save gets a 422 on a layout they did not write, on all four profiles.
--      TestEverySeededRoleDefaultSurvivesAGetThenPut is red until this migration runs.
--
--   2. Leaving the rows wrong makes the repair the only thing standing between a dealer and a false
--      caption, permanently, on every read. Storing the truth once is cheaper and it survives the
--      next person who reads the table directly.
--
-- ── WHAT THIS TOUCHES, AND THE ONE PART A FROM-ZERO RUN CANNOT SEE ──────────────────────────────
--
-- BOTH TABLES, and the second one matters more than it looks. 0202's carry-over copies a profile
-- default's time_range verbatim into personal rows (0202 §2), and hard-codes 'open' for every chart
-- it carries from the composer (0202:138) — including sales.today and deliveries.today, whose one
-- window is `today`. But 0202 plants NOTHING on a from-zero database: no migration ever INSERTs into
-- surface_user_items, so there is no composed tile for it to carry. Every row it creates exists only
-- on a live or staging database.
--
-- So a from-zero CI run would show this file matching nothing in dashboard_user_widgets and report
-- itself green, while staging carries rows it does correct. That is the argument for writing this as
-- a data-driven UPDATE over both tables rather than as eleven scoped statements against 0201's
-- literals: the statement below repairs a row it has never seen, which is the only shape that can.
--
-- ── SAFETY ──────────────────────────────────────────────────────────────────────────────────────
--
-- Keyed on (measure, time_range) → the correct window, so it touches ONLY rows that are actually
-- wrong. A dealer who has already set a sensible window is not overwritten, a row already correct is
-- not rewritten, and a second run matches nothing — the WHERE clause excludes its own result.
-- Reverting means restoring 0201's literals, which is a data question rather than a schema one.

-- The one true window per measure, from each provider's own SQL. The column is `named_window` and
-- not `window` because WINDOW is a reserved word in Postgres (the window-function clause) — as a
-- VALUES column name it is a syntax error, not a quoting inconvenience. This list is the SAME list as
-- MetricMeta.Ranges in internal/dashboard/viz.go, and widget_test.go's drift gate reads BOTH so the
-- two cannot separate.
WITH truth(measure, named_window) AS (
    VALUES ('sales.today',          'today'),
           ('deliveries.today',     'today'),
           ('orders.open',          'open'),
           ('ar.aging',             'open'),
           ('warranty.open_claims', 'open'),
           ('picks.queue',          'open'),
           ('needs.you',            'open'),
           ('inventory.value',      'now'),
           ('margin.gross_bps',     'all_time')
)
UPDATE dashboard_profile_widgets w
   SET time_range = t.named_window,
       updated_at = now()
  FROM truth t
 WHERE w.measure    = t.measure
   AND w.time_range <> t.named_window;

WITH truth(measure, named_window) AS (
    VALUES ('sales.today',          'today'),
           ('deliveries.today',     'today'),
           ('orders.open',          'open'),
           ('ar.aging',             'open'),
           ('warranty.open_claims', 'open'),
           ('picks.queue',          'open'),
           ('needs.you',            'open'),
           ('inventory.value',      'now'),
           ('margin.gross_bps',     'all_time')
)
UPDATE dashboard_user_widgets w
   SET time_range = t.named_window,
       updated_at = now()
  FROM truth t
 WHERE w.measure    = t.measure
   AND w.time_range <> t.named_window;

COMMENT ON COLUMN dashboard_profile_widgets.time_range IS
  'The named window this widget is measured over. Each measure declares the windows its series provider can actually honour (MetricMeta.Ranges, internal/dashboard/viz.go) — one each today, because no provider takes a window yet. A window outside that list is refused on write and repaired on read: it produced a true number under a false caption, which is the one error a person reading a dashboard cannot catch. Corrected across eleven seeded role defaults by migration 0205.';

COMMENT ON COLUMN dashboard_user_widgets.time_range IS
  'The named window this widget is measured over — see dashboard_profile_widgets.time_range. Rows here can also arrive from 0202''s carry-over, which hard-codes ''open'' for every composed chart it converts; migration 0205 corrects those for the measures whose window is not open.';
