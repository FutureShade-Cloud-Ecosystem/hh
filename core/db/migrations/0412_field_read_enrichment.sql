-- 0412_field_read_enrichment.sql
--
-- Field read enrichment for the Driver Run and Yard Pick surfaces:
--   1. delivery_stops.gate_code (TEXT NOT NULL DEFAULT '') so a delivery stop
--      carries its site access gate code on driver and dispatch reads.
--   2. delivery_runs.departs_at (TIMESTAMPTZ) so a delivery run carries its
--      scheduled departure timestamp on run and yard pick queue reads.

ALTER TABLE delivery_stops
    ADD COLUMN IF NOT EXISTS gate_code TEXT NOT NULL DEFAULT '';

ALTER TABLE delivery_runs
    ADD COLUMN IF NOT EXISTS departs_at TIMESTAMPTZ;

COMMENT ON COLUMN delivery_stops.gate_code IS
    'Site access or keypad gate code for the delivery stop; empty string when none is required.';

COMMENT ON COLUMN delivery_runs.departs_at IS
    'Scheduled departure timestamp for the delivery run; nullable when no departure time has been set.';

UPDATE delivery_runs
   SET departs_at = dispatched_at
 WHERE departs_at IS NULL
   AND dispatched_at IS NOT NULL;

UPDATE delivery_runs
   SET departs_at = (scheduled_date::timestamp AT TIME ZONE 'UTC') + INTERVAL '8 hours 15 minutes'
 WHERE departs_at IS NULL
   AND run_no LIKE '#DEMO-RUN-%-R5';

UPDATE delivery_stops
   SET gate_code = '#4421'
 WHERE gate_code = ''
   AND address_line IN ('1500 Princess St', '290 Counter St')
   AND order_id IN (SELECT id FROM orders WHERE metadata->>'demo_seed' IS NOT NULL);

