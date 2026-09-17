-- 0132: DIB-223 — WTY-SLA, per-phase SLA timers and overdue alerts. Owner: internal/warranty (W5-B).
--
-- WHAT P1 HAD. One constant — defaultSLADays = 14 — stamped on entering SUBMITTED, and a comment saying
-- vendor-specific SLAs were a residual open item (PRD §9 Q3). That measures ONE hop of a seven-phase lifecycle.
-- The claims that rot are not the ones stuck at the vendor; they are the ones that sat in TRIAGE for three
-- weeks because nobody owned them, and P1 could not see those at all.
--
-- WHAT THIS ADDS. A target per PHASE, overridable per VENDOR (Techo answering in two weeks and Oaks answering
-- when it feels like it are different clocks and always were), plus an alert row per (claim, phase) so an
-- overdue claim is a durable object with an acknowledgement, not a computed red badge that vanishes when the
-- page reloads.
--
-- THE CLOCK IS INJECTED, NEVER SLEPT ON. Every timer read goes through the service clock, which the tests
-- replace with a fake. There is no time.Sleep in this feature and there must never be one: a sleep-based SLA
-- test is a flake with a countdown on it.
--
-- DEFAULTS, STATED. These are DOCUMENTED DEFAULTS, not a client ruling — PRD §9 Q3 is still open. They are
-- chosen so the first breach a desk sees is a real problem and not noise:
--   INTAKE               24h  — a claim that arrives (especially from the public form) is triaged next business day.
--   TRIAGE               48h  — identify the vendor, check coverage, gather proof.
--   SUBMITTED            72h  — from "we sent it" to "the vendor acknowledged".
--   WAITING_ON_DECISION 336h  — 14 days, PRESERVING P1's defaultSLADays exactly. This is the vendor's clock.
--   DECISION             48h  — the verdict is in; book the settlement.
--   SETTLEMENT          120h  — 5 days to actually execute the money path and close.
-- Terminal phases (CLOSED/REJECTED/ABANDONED) have no timer: nothing is overdue about a finished claim.

-- ─────────────────────────────────────────────────────────────────────────────
-- 1. The per-phase target. vendor_id NULL = the default that applies to every vendor; a vendor row overrides
--    it for that (phase, vendor) — the exact shape warranty_checklist_items (0053) already uses, so the
--    resolution rule ("most specific wins") is the one this module already implements.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE warranty_sla_config (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  phase         TEXT NOT NULL
                  CHECK (phase IN ('INTAKE','TRIAGE','SUBMITTED','WAITING_ON_DECISION','DECISION','SETTLEMENT')),
  vendor_id     UUID REFERENCES vendors(id) ON DELETE CASCADE,   -- NULL = default for all vendors
  target_hours  INTEGER NOT NULL CHECK (target_hours > 0),
  is_active     BOOLEAN NOT NULL DEFAULT TRUE,
  notes         TEXT NOT NULL DEFAULT '',
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (phase, vendor_id)
);
CREATE INDEX idx_warranty_sla_config_phase ON warranty_sla_config(phase, is_active);

INSERT INTO warranty_sla_config (phase, vendor_id, target_hours, notes) VALUES
  ('INTAKE',              NULL, 24,  'Documented default (PRD §9 Q3 open): a new claim is triaged next business day.'),
  ('TRIAGE',              NULL, 48,  'Documented default: identify vendor, check coverage, gather proof.'),
  ('SUBMITTED',           NULL, 72,  'Documented default: from sent to vendor acknowledgement.'),
  ('WAITING_ON_DECISION', NULL, 336, 'Documented default: 14 days — preserves P1 defaultSLADays exactly. The vendor clock.'),
  ('DECISION',            NULL, 48,  'Documented default: verdict in, book the settlement.'),
  ('SETTLEMENT',          NULL, 120, 'Documented default: 5 days to execute the money path and close.')
ON CONFLICT (phase, vendor_id) DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────────────
-- 2. The alert. One row per (claim, phase) — a claim kicked back to TRIAGE and overdue there a second time
--    updates the same row rather than accumulating duplicates a desk has to dismiss twice.
--
--    notify_status is the FLAG-OFF seam to the notification tier (Swarm D's fence this wave). warranty emits
--    through its own SLANotifier port; unbound, every alert records SUPPRESSED and the in-module surface still
--    shows it. The alert is real either way — the notification is the optional part, which is the correct
--    dependency direction for an alert nobody wants to lose because a downstream service is down.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE warranty_sla_alerts (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  claim_id        UUID NOT NULL REFERENCES warranty_claims(id) ON DELETE CASCADE,
  phase           TEXT NOT NULL
                    CHECK (phase IN ('INTAKE','TRIAGE','SUBMITTED','WAITING_ON_DECISION','DECISION','SETTLEMENT')),
  due_at          TIMESTAMPTZ NOT NULL,          -- phase_entered_at + target
  breached_at     TIMESTAMPTZ NOT NULL,          -- the clock reading when the breach was RAISED
  overdue_hours   INTEGER NOT NULL DEFAULT 0 CHECK (overdue_hours >= 0),
  acknowledged_at TIMESTAMPTZ,
  acknowledged_by UUID REFERENCES users(id) ON DELETE SET NULL,
  notify_status   TEXT NOT NULL DEFAULT 'SUPPRESSED'
                    CHECK (notify_status IN ('SUPPRESSED','SENT','FAILED')),
  notify_ref      TEXT NOT NULL DEFAULT '',
  notify_error    TEXT NOT NULL DEFAULT '',
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (claim_id, phase),
  -- An acknowledgement needs both halves or neither: an acknowledged alert with no acknowledger is an alert
  -- nobody can be asked about.
  CONSTRAINT warranty_sla_alerts_ack_shape CHECK (
    (acknowledged_at IS NULL) = (acknowledged_by IS NULL)
  )
);
CREATE INDEX idx_warranty_sla_alerts_open ON warranty_sla_alerts(claim_id) WHERE acknowledged_at IS NULL;
CREATE INDEX idx_warranty_sla_alerts_phase ON warranty_sla_alerts(phase, breached_at);
