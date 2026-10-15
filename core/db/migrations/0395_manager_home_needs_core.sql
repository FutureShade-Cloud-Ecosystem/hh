-- 0395: the per-role Manager Home needs, third pass (HH Session 4, item 2D gaps 11 and 12).
--
-- 0381 filed the manager roles under their own profiles and 0392 re-filed the procurement and
-- branch managers' Needs-you rows and the sales manager's widgets. This migration gives two more
-- profiles a source their duties name:
--   - the yard manager needs dispatch.due_to_commit, because committing a delivery date on a load
--     being built is one of their top jobs (role-catalog.yaml yard_manager);
--   - the logistics manager needs approvals.filed_pending, because the freight raise they file and
--     wait on is the one approval that originates from their own work (role-catalog.yaml
--     logistics_manager).
--
-- Both sources already have adapters wired in cmd/core/wire_tops.go; this migration only adds the
-- policy rows that surface them on the right Home. The rows for manager.yard and manager.logistics
-- are re-filed whole, so a dealer's earlier edit to THESE profiles' needs rows is replaced by the
-- role's shipped default; every other profile and every other table is untouched.

-- -- §1 · the yard manager: commit a delivery date on a load being built ----------------------------------
DELETE FROM dashboard_needs_sources WHERE profile = 'manager.yard';
INSERT INTO dashboard_needs_sources
    (profile, source_key, position, visible, warn_at, stop_at, stale_after_minutes) VALUES
    ('manager.yard', 'dispatch.due_to_commit',           0, true, 1, 3, 0),
    ('manager.yard', 'tasks.open_shortages',             1, true, 1, 5, 0),
    ('manager.yard', 'tasks.stale_picks',                2, true, 1, 5, 60),
    ('manager.yard', 'purchasing.receiving_differences', 3, true, 1, 3, 0);

-- -- §2 · the logistics manager: approvals they filed and are still pending ------------------------------
DELETE FROM dashboard_needs_sources WHERE profile = 'manager.logistics';
INSERT INTO dashboard_needs_sources
    (profile, source_key, position, visible, warn_at, stop_at, stale_after_minutes) VALUES
    ('manager.logistics', 'dispatch.unassigned',      0, true, 1, 5, 0),
    ('manager.logistics', 'dispatch.due_to_commit',   1, true, 1, 3, 0),
    ('manager.logistics', 'tasks.stale_picks',        2, true, 1, 5, 60),
    ('manager.logistics', 'approvals.filed_pending',  3, true, 1, 5, 240);
