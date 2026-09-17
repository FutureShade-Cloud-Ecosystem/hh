-- 0182_home_metric_catalog.sql — the home screen's COMPOSABLE METRIC CATALOG, as curation.
--
-- WHAT THE CLIENT ASKED FOR: "the ability for a user to create a dashboard visualization of
-- whatever data source(s) they want to visualize." WHAT WAS RULED: that is a CURATED METRIC
-- CATALOG, not a free-form query tool. A person composes a tile from vetted metrics, with vetted
-- dimensions, in vetted shapes.
--
-- WHY THE RULING IS ALSO THE SECURITY MODEL. A free-form builder would have to re-derive every
-- role gate the ERP already owns, in a second place, over expressions nobody wrote by hand — and
-- cost or margin would be one clever GROUP BY from a counter clerk's home screen. With a catalog,
-- a metric that bears cost is ABSENT from what that role is offered: its id and its label are not
-- in the payload, so there is nothing to hide and nothing to strip.
--
-- NO NEW TABLE, AND THAT IS THE POINT. Which metrics a job shape is offered is presentation
-- curation, so it is rows in 0172's surface_role_items under the surface `home.metrics`, kind
-- `chip` — the 0140/0149 idiom the whole product curates with. A person's COMPOSED TILES are the
-- other half of the same substrate: surface_user_items, surface `home.dashboard`, kind `panel`,
-- item_id `viz.<metric>-<dimension>-<shape>`. Reset is a DELETE, exactly as it is for tiles, tabs,
-- columns and chips. One meaning of "reset" in the product, not two.
--
-- CURATION IS NOT THE GATE. These rows decide the MENU. The gate is in Go
-- (internal/dashboard/viz.go MetricsFor + viz_handler.go vizScopeFrom): every cost-class metric is
-- dropped for a caller who is not manager-up, AFTER curation and regardless of it. An admin who
-- marks 'margin.gross_bps' visible for the counter shape still sends a counter clerk a payload
-- with no margin metric in it. Curation hides things; it never grants anything.
--
-- METRIC IDS ARE THE FRONTEND/CATALOG REGISTRY'S (the 0149/0164/0172 ruling): the substrate
-- validates SHAPE only, and an id this build does not have renders nothing, silently. They are the
-- second half of the primary key of every composed tile somebody saved, so they are ADD-ONLY —
-- renaming one orphans saved charts, exactly as renaming a tile id orphans saved layouts.

INSERT INTO surface_role_items (surface_id, profile, item_kind, item_id, position, visible)
SELECT 'home.metrics', p.profile, 'chip', m.item_id, m.position, m.visible
FROM (VALUES ('counter'), ('yard'), ('driver')) AS p (profile)
CROSS JOIN (VALUES
    -- THE FRONTLINE MENU. Counts and queues: what the day is made of for somebody standing up.
    -- Revenue is present because the counter already sees the till total on their own home tile —
    -- a composer that refused to chart a number the same person is looking at would be theatre.
    -- Cost-class metrics are simply not listed, and would be dropped by the Go gate if they were.
    ('orders.open',          0, true),
    ('picks.queue',          1, true),
    ('deliveries.today',     2, true),
    ('warranty.open_claims', 3, true),
    ('sales.today',          4, true)
) AS m (item_id, position, visible)
ON CONFLICT (surface_id, profile, item_kind, item_id) DO NOTHING;

INSERT INTO surface_role_items (surface_id, profile, item_kind, item_id, position, visible)
SELECT 'home.metrics', p.profile, 'chip', m.item_id, m.position, m.visible
FROM (VALUES ('manager'), ('admin')) AS p (profile)
CROSS JOIN (VALUES
    -- THE LEADERSHIP MENU: money first, then the queues that turn into complaints, then the two
    -- cost-class metrics — which reach the payload only for a caller the marshal gate allows.
    ('sales.today',          0, true),
    ('ar.aging',             1, true),
    ('orders.open',          2, true),
    ('warranty.open_claims', 3, true),
    ('picks.queue',          4, true),
    ('deliveries.today',     5, true),
    ('inventory.value',      6, true),
    ('margin.gross_bps',     7, true)
) AS m (item_id, position, visible)
ON CONFLICT (surface_id, profile, item_kind, item_id) DO NOTHING;
