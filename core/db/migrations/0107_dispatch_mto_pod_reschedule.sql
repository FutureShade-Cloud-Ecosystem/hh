-- 0107: dispatch follow-through — MTO compliance on the manifest, POD photo attachment, and the
-- audited delivery-date reschedule (Wave 3, Swarm C: DIB-63 + DIB-382).
--
-- THREE ADDITIONS, ONE THEME: the run manifest stops being a projection nobody can audit.
--
-- 1 · MTO CLASSIFICATION (DIB-63). docs/design/DATA_MODEL.md §12 rules that a durable manifest carries
--     an mto_class in ('STANDARD','MTO','MIXED') and a per-line weight, because truck-compliance is a
--     made-to-order rule ("this load contains cut-to-order material; it is not interchangeable and it
--     is not returnable") and a manifest that cannot say so is the BisTrack report view all over
--     again. Wave 2 shipped the manifest as a RENDER over delivery_runs/delivery_stops rather than as
--     a manifests table, so the classification lands on the stop — which is the row that actually
--     carries one order's material — and the run's class is ROLLED UP from its stops rather than
--     stored twice. Two copies of a classification is one copy that goes stale.
--
--     weight_kg is NULLable and stays NULL for most stops: the catalogue does not carry per-product
--     weights, so a number here is one somebody typed. A guessed weight printed next to a truck's
--     capacity is worse than a blank, and the manifest prints "—" rather than inventing a total.
--
-- 2 · POD PHOTO ATTACHMENT (DIB-63). 0091 stores photos against a stop with a captured_at. What it
--     could not say is WHICH photos are part of the sealed proof-of-delivery record: a driver
--     photographs a blocked driveway at 09:14, delivers at 09:40, and photographs the pallet on the
--     pad at 09:41. Both are stop photos; only the second is evidence of the delivery. The two
--     columns below record the moment a photo was folded into the POD record and the attestation ref
--     it was folded into, so "show me the proof for this delivery" is a query rather than a judgement
--     call about timestamps.
--
--     The attach is an UPDATE, never a copy: the bytes stay in the row 0091 wrote. Copying evidence
--     to mark it as evidence is how two versions of the truth appear.
--
-- 3 · RESCHEDULE AUDIT (DIB-382). Moving a delivery to another date is a promise being changed, and
--     until now the only trace was scheduled_date having a different value than it used to. The table
--     below is append-only and records the move, who made it, and WHY — because "why did this slip
--     three days" is the question a customer asks and an order history could not answer.

-- ── 1 · MTO classification on the stop ────────────────────────────────────────────────────────────

ALTER TABLE delivery_stops
  -- STANDARD — stock material, interchangeable, returnable.
  -- MTO      — made/cut to order for this customer: it cannot be substituted, and a failed delivery
  --            is a return-to-yard problem rather than a re-stock.
  -- MIXED    — the order carries both; the driver has to be told, because the handling rule differs
  --            per pallet and "mostly standard" is not a handling instruction.
  ADD COLUMN mto_class TEXT NOT NULL DEFAULT 'STANDARD'
    CHECK (mto_class IN ('STANDARD','MTO','MIXED')),
  -- The human sentence that rides on the manifest under the class: "cut to 1150mm, do not tip".
  ADD COLUMN mto_note TEXT NOT NULL DEFAULT '',
  -- Advisory, like dispatch_trucks.capacity_kg and for the same reason (0090's header). NULL means
  -- nobody weighed it; it is NOT zero, and the manifest must not add it into a total as if it were.
  ADD COLUMN weight_kg NUMERIC(19,4) CHECK (weight_kg IS NULL OR weight_kg >= 0);

COMMENT ON COLUMN delivery_stops.mto_class IS
  'DIB-63 made-to-order classification (DATA_MODEL §12). Rolled up to the run on the manifest; never stored on the run.';

-- The manifest read: "every stop on this run, with its class". The index is on (run_id, mto_class)
-- so the roll-up is an index scan rather than a table read on a busy branch.
CREATE INDEX idx_delivery_stops_run_mto ON delivery_stops (run_id, mto_class) WHERE run_id IS NOT NULL;

-- ── 2 · POD photo attachment ──────────────────────────────────────────────────────────────────────

ALTER TABLE delivery_pod_photos
  -- When this photo became part of the sealed POD record. NULL = a stop photo that is not (yet)
  -- delivery evidence.
  ADD COLUMN pod_attached_at TIMESTAMPTZ,
  -- The attestation the photo was attached to (delivery_stops.pod_signature_ref). Denormalized on
  -- purpose: a photo must be able to name the signature it evidences without a join, so that a
  -- re-delivery on the same stop id (which cannot happen today, but a schema outlives its guards)
  -- can never silently re-point old photos at a new signature.
  ADD COLUMN pod_signature_ref TEXT NOT NULL DEFAULT '',
  -- An attached photo names its attestation, and an unattached one names none. This is the constraint
  -- that stops a half-attach — a ref with no timestamp, or a timestamp with no ref — from existing.
  ADD CONSTRAINT delivery_pod_photos_attach_is_whole
    CHECK ((pod_attached_at IS NULL) = (pod_signature_ref = ''));

-- "Show me the proof for this delivery" — the attached photos for a stop, newest last.
CREATE INDEX idx_delivery_pod_photos_attached ON delivery_pod_photos (stop_id, pod_attached_at)
  WHERE pod_attached_at IS NOT NULL;

-- ── 3 · the reschedule trail ──────────────────────────────────────────────────────────────────────

CREATE TABLE dispatch_reschedules (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  -- Branch is a COLUMN, not a JOIN — the 0090 rule, restated. The reschedule log is fenced by the
  -- same predicate as everything else in this module.
  branch_id   UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  -- What moved. A run move and a stop move are the same ACT recorded at different grain, so they
  -- share one table rather than two that will drift.
  entity_type TEXT NOT NULL CHECK (entity_type IN ('delivery_stop','delivery_run')),
  -- No FK: the trail must survive the row it describes (a stop can be deleted in a data cleanup and
  -- the promise that was changed still happened). Same posture as audit_log.entity_id.
  entity_id   UUID NOT NULL,
  from_date   DATE NOT NULL,
  to_date     DATE NOT NULL,
  -- Required, and enforced here as well as in the service: a date change with no stated reason is
  -- the exact record that makes a customer conversation unwinnable three weeks later.
  reason      TEXT NOT NULL CHECK (btrim(reason) <> ''),
  -- How many stops the move carried with it (a run move drags its stops; a stop move is always 1).
  stops_moved INTEGER NOT NULL DEFAULT 1 CHECK (stops_moved >= 0),
  actor_id    UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- A "reschedule" to the same date is a no-op someone clicked, not an event. The service refuses it
  -- with a sentence; this is the backstop that keeps the trail free of noise rows.
  CONSTRAINT dispatch_reschedules_actually_moves CHECK (from_date <> to_date)
);

-- "What has moved on this stop/run" — the drawer read, newest first.
CREATE INDEX idx_dispatch_reschedules_entity ON dispatch_reschedules (entity_type, entity_id, created_at DESC);
-- "What slipped at this branch this month" — the operational review read.
CREATE INDEX idx_dispatch_reschedules_branch ON dispatch_reschedules (branch_id, created_at DESC);
