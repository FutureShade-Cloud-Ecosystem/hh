-- 0389: the per-role Manager Home landings, second pass (HH Session 4, item 2D).
--
-- 0381 filed eight manager roles under their own profile but BORN seven of them as byte copies of a
-- base shape, so five roles shared the controller's landing. This migration gives three of them
-- their own: the procurement manager (needs and widgets), the sales manager (widgets) and the
-- branch manager (needs). Each profile's rows named below are re-filed whole, so a dealer's earlier
-- edit to THESE profiles' widget or needs rows is replaced by the role's shipped default; every
-- other profile and every other table is untouched. The general manager and HR are not touched
-- here. The Go fallbacks (needs.go) carry the same needs rows, and the drift test reads the table
-- against them.

-- ── §1 · the procurement manager's Needs-you ────────────────────────────────────────────────────
--
-- The gaps the yard reports and where each purchase order got to: open shortages, then receipts that
-- landed short. Approvals stay (the adapter counts only what this caller may decide, so a zero is an absent row). The AR escalation and
-- the parked tickets are the controller's and the counter's work and leave. Edges follow the yard
-- manager's rows for the two sources they share (0381 §5) and the manager shape's for approvals.
DELETE FROM dashboard_needs_sources WHERE profile = 'manager.procurement';
INSERT INTO dashboard_needs_sources
    (profile, source_key, position, visible, warn_at, stop_at, stale_after_minutes) VALUES
    ('manager.procurement', 'tasks.open_shortages',             0, true, 1, 5, 0),
    ('manager.procurement', 'purchasing.receiving_differences', 1, true, 1, 3, 0),
    ('manager.procurement', 'approvals.waiting',                2, true, 1, 5, 240);

-- ── §2 · the branch manager's Needs-you ─────────────────────────────────────────────────────────
--
-- The branch manager commits a delivery date and stands behind it when dispatch escalates, so the
-- two dispatch sources lead with the dock shape's edges (0201), and the manager shape's three rows
-- follow unchanged.
DELETE FROM dashboard_needs_sources WHERE profile = 'manager.branch';
INSERT INTO dashboard_needs_sources
    (profile, source_key, position, visible, warn_at, stop_at, stale_after_minutes) VALUES
    ('manager.branch', 'dispatch.due_to_commit', 0, true, 1,  3, 0),
    ('manager.branch', 'dispatch.unassigned',    1, true, 1,  5, 0),
    ('manager.branch', 'approvals.waiting',      2, true, 1,  5, 240),
    ('manager.branch', 'ar.escalation',          3, true, 1,  3, 0),
    ('manager.branch', 'pos.parked',             4, true, 3, 10, 120);

-- ── §3 · the sales manager's widgets ────────────────────────────────────────────────────────────
--
-- The sales manager's duties are pricing and selling, so sales today is the hero, the branch split
-- and the trend say how selling is going, and margin by branch and the gross margin figure are the
-- pricing facts. The receivables book, the inventory value and the claims count are other roles'
-- and leave. Every measure is an existing catalog id offering exactly this shape, split and window
-- (the integration gate runs the rows through the write gate). There is no quotes or commission
-- measure in the catalog yet; when one lands it joins this list by a migration of its own.
DELETE FROM dashboard_profile_widgets WHERE profile = 'manager.sales';
INSERT INTO dashboard_profile_widgets
    (profile, widget_key, shape, measure, split, time_range, size, title, position, visible) VALUES
    ('manager.sales', 'hero.sales',    'stat',  'sales.today',      'none',   'today',    'L', 'Sales today',      0, true),
    ('manager.sales', 'needs.you',     'list',  'needs.you',        'none',   'open',     'L', 'Needs you',        1, true),
    ('manager.sales', 'sales.branch',  'bars',  'sales.today',      'branch', 'today',    'L', 'Sales by branch',  2, true),
    ('manager.sales', 'margin.branch', 'bars',  'margin.gross_bps', 'branch', 'all_time', 'M', 'Margin by branch', 3, true),
    ('manager.sales', 'sales.trend',   'trend', 'sales.today',      'none',   'today',    'M', 'Sales trend',      4, true),
    ('manager.sales', 'orders.status', 'bars',  'orders.open',      'status', 'open',     'M', 'Orders by status', 5, true),
    ('manager.sales', 'orders.open',   'stat',  'orders.open',      'none',   'open',     'S', 'Open orders',      6, true),
    ('manager.sales', 'margin.gross',  'stat',  'margin.gross_bps', 'none',   'all_time', 'S', 'Gross margin',     7, true);

-- ── §4 · the procurement manager's widgets (the re-seed half of the hero) ───────────────────────
--
-- The catalog has no purchase order measure yet, so the hero is the stock on hand and the board is
-- built from the measures that exist: inventory by branch, orders by status, the pick queue as a
-- board and a gauge, open orders and open claims. The receivables book and the sales trend are the
-- controller's and the sales manager's and leave. Two PO measures (open POs, incoming) are a
-- separate build that will replace the hero's neighbours by a later migration.
DELETE FROM dashboard_profile_widgets WHERE profile = 'manager.procurement';
INSERT INTO dashboard_profile_widgets
    (profile, widget_key, shape, measure, split, time_range, size, title, position, visible) VALUES
    ('manager.procurement', 'hero.inventory',   'stat',  'inventory.value',      'none',   'now',  'L', 'Inventory value',     0, true),
    ('manager.procurement', 'needs.you',        'list',  'needs.you',            'none',   'open', 'L', 'Needs you',           1, true),
    ('manager.procurement', 'inventory.branch', 'bars',  'inventory.value',      'branch', 'now',  'L', 'Inventory by branch', 2, true),
    ('manager.procurement', 'orders.status',    'bars',  'orders.open',          'status', 'open', 'M', 'Orders by status',    3, true),
    ('manager.procurement', 'picks.board',      'board', 'picks.queue',          'status', 'open', 'M', 'Pick queue',          4, true),
    ('manager.procurement', 'picks.gauge',      'gauge', 'picks.queue',          'none',   'open', 'S', 'Queue depth',         5, true),
    ('manager.procurement', 'orders.open',      'stat',  'orders.open',          'none',   'open', 'S', 'Open orders',         6, true),
    ('manager.procurement', 'warranty.open',    'stat',  'warranty.open_claims', 'none',   'open', 'S', 'Open claims',         7, true);
