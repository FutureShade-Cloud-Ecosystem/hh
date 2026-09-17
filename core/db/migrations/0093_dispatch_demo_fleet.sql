-- 0093: demo fleet + depot coordinates for the two seeded branches (DIB-64, bundle W2-A).
--
-- WHY THIS IS A MIGRATION AND NOT cmd/seed. cmd/seed rebuilds catalogue/customer/stock demo data and
-- is re-run freely; a truck the dispatcher assigned a run to last week must not vanish because
-- someone re-seeded. Fleet rows are reference data with foreign keys pointing at them
-- (delivery_runs.truck_id is ON DELETE RESTRICT), so they belong in the schema timeline where they
-- are written exactly once.
--
-- Fixed UUIDs, ON CONFLICT DO NOTHING: this migration is safe on a database where a dispatcher has
-- already added their own trucks, and it never overwrites an edit someone made to these rows.
--
-- The depot coordinates are the real Trenton and Kingston, Ontario town centres (4 decimal places —
-- about 10 m, which is the honest precision of "the yard is here" without a survey). They exist so
-- the manifest can print a distance estimate; nothing routes on them.

INSERT INTO dispatch_branch_depots (branch_id, latitude, longitude)
SELECT l.id, d.lat, d.lng
  FROM locations l
  JOIN (VALUES ('TRT', 44.1001, -77.5766), ('KGN', 44.2312, -76.4860)) AS d(code, lat, lng)
    ON l.code::text = d.code
 WHERE l.loc_type = 'BRANCH'
ON CONFLICT (branch_id) DO NOTHING;

INSERT INTO dispatch_trucks (id, branch_id, code, name, truck_type, capacity_kg, is_active) VALUES
  ('d1000000-0000-4000-9000-000000000001', '5d1b3f6e-0000-4000-9000-000000000001', 'TRK-1', 'Trenton Flatbed 1',  'FLATBED', 9000.00, TRUE),
  ('d1000000-0000-4000-9000-000000000002', '5d1b3f6e-0000-4000-9000-000000000001', 'BOOM-1','Trenton Boom Truck',  'BOOM',    7000.00, TRUE),
  ('d1000000-0000-4000-9000-000000000003', '5d1b3f6e-0000-4000-9000-000000000002', 'TRK-1', 'Kingston Flatbed 1', 'FLATBED', 9000.00, TRUE)
ON CONFLICT (id) DO NOTHING;

-- Drivers are seeded WITHOUT a user_id. Linking a driver to a staff login is a deliberate act by a
-- branch manager (the driver PWA reads "my runs" off that link), and inventing the link here would
-- silently give a seeded account a run list it never asked for.
INSERT INTO dispatch_drivers (id, branch_id, name, phone, licence_class, is_active) VALUES
  ('d2000000-0000-4000-9000-000000000001', '5d1b3f6e-0000-4000-9000-000000000001', 'Wade Prentice',  '613-555-0142', 'DZ', TRUE),
  ('d2000000-0000-4000-9000-000000000002', '5d1b3f6e-0000-4000-9000-000000000001', 'Marta Kolenko',  '613-555-0177', 'AZ', TRUE),
  ('d2000000-0000-4000-9000-000000000003', '5d1b3f6e-0000-4000-9000-000000000002', 'Desmond Barrow', '613-555-0198', 'DZ', TRUE)
ON CONFLICT (id) DO NOTHING;
