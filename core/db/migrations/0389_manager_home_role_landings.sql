-- 0389: the per-role Manager Home landings, second pass (HH Session 4, item 2D).
--
-- 0381 filed eight manager roles under their own profile but BORN seven of them as byte copies of a
-- base shape, so five roles shared the controller's landing. This migration gives three of them
-- their own: the procurement manager (needs and hero), the sales manager (hero and widgets) and the
-- branch manager (needs). It is forward-only data: a profile row is policy a dealer curates, so a
-- dealer who already edited a profile keeps their edit only where this file does not name the row;
-- the rows named below are the role's shipped default and are re-filed whole. The general manager
-- and HR are not touched here. The Go fallbacks (needs.go) carry the same rows, and the drift test
-- reads the table against them.

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
