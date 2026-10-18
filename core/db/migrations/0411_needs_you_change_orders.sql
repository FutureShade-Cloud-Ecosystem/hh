-- 0411: Needs You row for the change orders waiting on the manager.
--
-- The signing managers (admin, manager, branch, sales, controller, payables, general) carry
-- orders.changes_waiting at the end of their Needs You policy with the approvals source's severity
-- edges (warn at 1, stop at 5, stale after 240 minutes). Forward only and idempotent: a profile a
-- dealer emptied on purpose stays empty, and a second run adds nothing.

INSERT INTO dashboard_needs_sources
    (profile, source_key, position, visible, warn_at, stop_at, stale_after_minutes)
SELECT v.profile, v.source_key, v.position, v.visible, v.warn_at, v.stop_at, v.stale_after_minutes
  FROM (VALUES
    ('admin',              'orders.changes_waiting', 4, true, 1, 5, 240),
    ('manager',            'orders.changes_waiting', 3, true, 1, 5, 240),
    ('manager.branch',     'orders.changes_waiting', 5, true, 1, 5, 240),
    ('manager.sales',      'orders.changes_waiting', 3, true, 1, 5, 240),
    ('manager.controller', 'orders.changes_waiting', 3, true, 1, 5, 240),
    ('manager.payables',   'orders.changes_waiting', 3, true, 1, 5, 240),
    ('manager.general',    'orders.changes_waiting', 4, true, 1, 5, 240)
  ) AS v (profile, source_key, position, visible, warn_at, stop_at, stale_after_minutes)
 WHERE EXISTS (SELECT 1 FROM dashboard_needs_sources d WHERE d.profile = v.profile)
ON CONFLICT (profile, source_key) DO NOTHING;
