-- 0108: the pallet/load scan trail, the NEED_WEIGHT round trip, and the task→order advance gate
-- (Wave 3, Swarm C: DIB-75 + DIB-324 + DIB-325).
--
-- 1 · THE SCAN TRAIL (DIB-75). Between "the truck arrived from the supplier" and "the customer has
--     it", a pallet passes through five places and the system remembered none of them. When a
--     customer says a pallet is missing, the only honest answer today is "it was delivered" or "it
--     was not", with nothing in between. Each row below is one CHECKPOINT: a load handle, seen at a
--     place, at a time, by a person.
--
--     WHY A CHECKPOINT TABLE AND NOT MORE STATUS COLUMNS. A status column answers "where is it now".
--     A trail answers "where has it been, and when did it stop moving" — which is the question that
--     resolves a dispute. The five checkpoints are deliberately the PHYSICAL stages, not the document
--     lifecycle: RECEIVED (off a supplier truck) → YARD (racked/binned) → PICKED (pulled for an
--     order) → IN_TRANSIT (on our truck) → DELIVERED (handed over). They run FORWARD ONLY, enforced
--     in Go (dispatch/scantrail.go) against the ordinal below.
--
--     KEYED TO ORDER, OPTIONALLY TO STOP. An order is the constant — RECEIVED and YARD happen before
--     any stop exists, and a stop appears only once the delivery is scheduled. Making stop_id
--     required would have meant either back-filling a stop that does not exist yet or refusing to
--     record the two earliest checkpoints, which are the ones nobody has today.
--
-- 2 · NEED_WEIGHT (DIB-324). tasks.weight_entered/weight_uom have existed since 0073 as a GATED seam
--     with no writer. This migration adds the ONE column the round trip needs that 0073 could not
--     have anticipated: whether the weight made it back onto the ORDER LINE. It is a timestamp, not
--     a boolean, and it is NULLable, because a task that recorded a weight the order refused is a
--     QUERYABLE GAP — the same doctrine delivery_stops.handoff_at established. Swallowing the failure
--     would make the gap invisible; failing the task would throw away the yard's measurement.
--
-- 3 · THE ADVANCE GATE (DIB-325). Completing a task MAY advance its order's status. That is a
--     decision with money at the end of it (an order that reaches FULFILLED is an order billing will
--     act on), so it is OFF for every task type until somebody turns it on, and turning it on is a
--     row in this table rather than a redeploy. The rule lives in the database rather than in an
--     environment variable precisely so the change is attributable: updated_by/updated_at say who
--     decided that a picker finishing a pick may move an order toward invoicing.

-- ── 1 · the scan trail ────────────────────────────────────────────────────────────────────────────

CREATE TABLE load_scan_checkpoints (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  -- Branch is a column (the 0090 rule). The trail is fenced by the same predicate as the stop.
  branch_id    UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  order_id     UUID NOT NULL REFERENCES orders(id) ON DELETE RESTRICT,
  -- NULL until the delivery is scheduled. SET NULL on stop delete: the scan happened even if the
  -- stop was later withdrawn, and losing the trail because a dispatcher cancelled a stop would
  -- destroy exactly the evidence this table exists to hold.
  stop_id      UUID REFERENCES delivery_stops(id) ON DELETE SET NULL,
  -- The handle physically on the pallet/load — a yard label, a wrap tag, a bundle number. Scrubname0011 text
  -- because it is somebody else's numbering scheme half the time (a supplier's pallet label is not
  -- ours to reformat), and CITEXT because a scan gun and a human type different cases.
  load_ref     CITEXT NOT NULL CHECK (btrim(load_ref::text) <> ''),
  checkpoint   TEXT NOT NULL CHECK (checkpoint IN ('RECEIVED','YARD','PICKED','IN_TRANSIT','DELIVERED')),
  scanned_by   UUID REFERENCES users(id) ON DELETE SET NULL,
  scanned_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  note         TEXT NOT NULL DEFAULT '',
  -- Set ONLY on the DELIVERED checkpoint: the proof-of-delivery attestation that closes the trail.
  -- This is the tie the ticket asks for — the last checkpoint is not "someone pressed delivered", it
  -- is "this signature says it arrived". A DELIVERED row with a blank ref is a trail whose end has
  -- no evidence, and the constraint below lets it exist (a scan can legitimately precede the POD by
  -- seconds) while the service back-fills it at capture time.
  pod_signature_ref TEXT NOT NULL DEFAULT '',
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- Only the final checkpoint may carry an attestation. A YARD scan with a POD ref is a bug, not a
  -- fact, and it would make the trail's ending ambiguous.
  CONSTRAINT load_scan_checkpoints_pod_is_terminal
    CHECK (pod_signature_ref = '' OR checkpoint = 'DELIVERED'),
  -- One checkpoint per load per stage. A re-scan (the gun beeped twice, the driver pressed it again
  -- on a dead signal) is IDEMPOTENT rather than a second row: a trail that shows "PICKED" three times
  -- is a trail nobody trusts. The service upserts on this key.
  UNIQUE (order_id, load_ref, checkpoint)
);

-- The trail read: "every checkpoint for this order, in the order they happened".
CREATE INDEX idx_load_scan_checkpoints_order ON load_scan_checkpoints (order_id, scanned_at);
-- The driver read: "what is on this stop and how far has it got".
CREATE INDEX idx_load_scan_checkpoints_stop ON load_scan_checkpoints (stop_id, scanned_at)
  WHERE stop_id IS NOT NULL;
-- The yard read: "what has been scanned at this branch today".
CREATE INDEX idx_load_scan_checkpoints_branch ON load_scan_checkpoints (branch_id, scanned_at DESC);

-- ── 2 · the NEED_WEIGHT round trip ────────────────────────────────────────────────────────────────

ALTER TABLE tasks
  -- When the weight the yard typed was written back onto the order line. NULL on a NEED_WEIGHT task
  -- that has a weight_entered is the gap: the yard measured, the order did not take it. See the
  -- header — this is deliberately visible rather than swallowed or fatal.
  ADD COLUMN weight_synced_at TIMESTAMPTZ,
  -- The order module's own refusal, kept verbatim so the counter reads WHY rather than "sync failed".
  ADD COLUMN weight_sync_error TEXT NOT NULL DEFAULT '';

COMMENT ON COLUMN tasks.weight_synced_at IS
  'DIB-324: when weight_entered reached the order line. NULL with a weight present = a queryable gap, not a silent one.';

-- The reconciliation read: NEED_WEIGHT tasks that measured but never landed.
CREATE INDEX idx_tasks_weight_unsynced ON tasks (task_type, updated_at)
  WHERE task_type = 'NEED_WEIGHT' AND weight_entered IS NOT NULL AND weight_synced_at IS NULL;

-- ── 3 · the task→order advance gate ───────────────────────────────────────────────────────────────

CREATE TABLE task_status_advance_rules (
  -- One row per task type, mirroring the 0073 task_type CHECK. A PRIMARY KEY on the type means the
  -- question "may a finished PICK move its order" has exactly one answer.
  task_type     TEXT PRIMARY KEY CHECK (task_type IN ('PICK','DISPATCH','NEED_WEIGHT','OTHER')),
  -- DEFAULT FALSE, and every seeded row below is FALSE. Turning this on is a deliberate act.
  enabled       BOOLEAN NOT NULL DEFAULT FALSE,
  -- The order status a completed task of this type advances TOWARD. It is a TARGET, not a command:
  -- the order module grades the move against its own forward-only ladder (order.AdvanceFromEvent), so
  -- an order already past the target declines rather than being walked backwards.
  target_status TEXT NOT NULL DEFAULT '',
  updated_by    UUID REFERENCES users(id) ON DELETE SET NULL,
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- An enabled rule with no target is a switch wired to nothing. Refused here so a half-configured
  -- rule cannot reach the service and be silently skipped.
  CONSTRAINT task_status_advance_rules_enabled_needs_target
    CHECK (NOT enabled OR btrim(target_status) <> '')
);

INSERT INTO task_status_advance_rules (task_type, enabled, target_status) VALUES
  -- A finished pick means the material is off the rack and staged. FULFILLED is the honest target,
  -- and it is OFF: on a delivery order the truck has not left yet, and dispatch's own handoff is the
  -- correct writer. A branch that picks-and-collects at the counter may want this on; that is their
  -- decision to make and record, not this migration's.
  ('PICK',        FALSE, 'FULFILLED'),
  ('DISPATCH',    FALSE, 'FULFILLED'),
  -- A weight coming back does not move an order anywhere; it corrects a line. Left blank AND off.
  ('NEED_WEIGHT', FALSE, ''),
  ('OTHER',       FALSE, '');
