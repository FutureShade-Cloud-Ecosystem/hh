-- 0090: dispatch core — the physical leg of the order lifecycle (DIB-64, bundle W2-A).
--
-- WHAT THIS CLOSES. An order could be quoted, confirmed, picked and marked READY, and then the
-- system went silent: nothing recorded which truck it went on, who drove it, whether it arrived, or
-- who signed for it. "Delivered" lived in someone's memory and a paper clipboard. Every table below
-- exists to make one of those facts a row.
--
-- NO POSTGIS (ruled, 2026-08-04). Stops carry plain latitude/longitude DOUBLE PRECISION columns and
-- distance is computed in Go (haversine, internal/dispatch/geo.go). A geography type would buy
-- exact geodesics and spatial indexes; neither is worth a PostGIS dependency for a run of a dozen
-- stops inside one county, and the extension is a deployment constraint that outlives the feature.
-- The columns are NULLable because an address that never geocoded is the normal case, not an error:
-- a stop with no coordinates still dispatches, it just does not contribute to a distance estimate.
--
-- BRANCH IS A COLUMN ON EVERY TABLE, NOT A JOIN. Trucks, drivers, runs and stops each carry
-- branch_id. The fence (identity.StaffContext.CanAccessBranch) is checked on every read and every
-- write, and a fence that has to JOIN three tables to find the branch it is fencing is a fence that
-- will eventually be forgotten in one query. delivery_stops.branch_id is denormalized from
-- orders.branch_id at creation — the service copies it through the order seam and refuses a stop
-- whose run belongs to a different branch (see the cross-branch guard in service.go).

-- ── fleet registry ────────────────────────────────────────────────────────────────────────────────
-- Deliberately SMALL. This is the registry a dispatcher needs to assign a run: what it is called,
-- what shape it is, and whether it may be assigned today. Maintenance schedules, odometers,
-- insurance expiries and licence renewals are a fleet module, and a fleet module is not this ticket.

CREATE TABLE dispatch_trucks (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  branch_id   UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  code        CITEXT NOT NULL,                    -- what the yard calls it: "TRK-1", "BOOM-2"
  name        TEXT NOT NULL DEFAULT '',
  truck_type  TEXT NOT NULL DEFAULT 'FLATBED'
                CHECK (truck_type IN ('FLATBED','BOOM','DUMP','PICKUP','TRAILER')),
  -- Capacity is ADVISORY: the board surfaces it so a dispatcher can see a run is over-loaded, and
  -- nothing refuses a load on it. Refusing would demand accurate per-line weights the catalogue does
  -- not yet carry, and a hard stop built on a soft number gets disabled within a week.
  capacity_kg NUMERIC(12,2),
  is_active   BOOLEAN NOT NULL DEFAULT TRUE,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (branch_id, code)
);
CREATE INDEX idx_dispatch_trucks_branch ON dispatch_trucks (branch_id) WHERE is_active;

CREATE TABLE dispatch_drivers (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  branch_id     UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  -- A driver MAY be a staff user (the DRIVERS role exists in identity.Roles) and may not be: yards
  -- run casual and contract drivers who will never hold a login. SET NULL on user delete keeps the
  -- delivery history readable after the account is gone — the run happened either way.
  user_id       UUID REFERENCES users(id) ON DELETE SET NULL,
  name          TEXT NOT NULL,
  phone         TEXT NOT NULL DEFAULT '',
  licence_class TEXT NOT NULL DEFAULT '',
  is_active     BOOLEAN NOT NULL DEFAULT TRUE,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_dispatch_drivers_branch ON dispatch_drivers (branch_id) WHERE is_active;
CREATE INDEX idx_dispatch_drivers_user ON dispatch_drivers (user_id) WHERE user_id IS NOT NULL;

-- ── where a run starts ────────────────────────────────────────────────────────────────────────────
-- The depot coordinate is the first and last leg of every distance estimate. It is a DISPATCH-owned
-- side table rather than two columns on locations, because locations is a shared, heavily-referenced
-- table (23 foreign keys into it) whose shape is not this module's to widen for its own convenience.
-- A branch with no depot row simply produces no distance estimate — the manifest omits the figure
-- rather than inventing one from a guessed origin.

CREATE TABLE dispatch_branch_depots (
  branch_id  UUID PRIMARY KEY REFERENCES locations(id) ON DELETE RESTRICT,
  latitude   DOUBLE PRECISION NOT NULL,
  longitude  DOUBLE PRECISION NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT dispatch_branch_depots_lat_range CHECK (latitude BETWEEN -90 AND 90),
  CONSTRAINT dispatch_branch_depots_lng_range CHECK (longitude BETWEEN -180 AND 180)
);

-- ── run numbering ─────────────────────────────────────────────────────────────────────────────────
-- One mint, branch-prefixed, exactly as 0087 does for orders and for the same reason: a run number a
-- driver reads over the radio has to name a branch, and three writers inventing three formats is how
-- order numbering broke. next_run_no is the ONLY way a run number is produced.

CREATE TABLE run_no_sequences (
  branch_id  UUID PRIMARY KEY REFERENCES locations(id) ON DELETE RESTRICT,
  last_value BIGINT NOT NULL DEFAULT 0 CHECK (last_value >= 0),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE FUNCTION next_run_no(p_branch_id UUID) RETURNS TEXT AS $$
DECLARE
  v_code TEXT;
  v_root UUID;
  v_n    BIGINT;
BEGIN
  SELECT CASE WHEN l.loc_type = 'BRANCH' THEN l.id ELSE l.branch_id END
    INTO v_root
    FROM locations l
   WHERE l.id = p_branch_id;
  IF v_root IS NULL THEN
    RAISE EXCEPTION 'next_run_no: unknown branch %', p_branch_id;
  END IF;

  SELECT l.code::text INTO v_code FROM locations l WHERE l.id = v_root;
  IF v_code IS NULL OR v_code = '' THEN
    RAISE EXCEPTION 'next_run_no: branch % has no code', v_root;
  END IF;

  INSERT INTO run_no_sequences (branch_id, last_value)
       VALUES (v_root, 1)
  ON CONFLICT (branch_id) DO UPDATE
          SET last_value = run_no_sequences.last_value + 1, updated_at = now()
    RETURNING last_value INTO v_n;

  RETURN '#RUN-' || v_code || '-' || v_n::text;
END;
$$ LANGUAGE plpgsql;

-- ── the run (a truck + a driver + a day) ──────────────────────────────────────────────────────────

CREATE TABLE delivery_runs (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  run_no         TEXT NOT NULL UNIQUE,
  branch_id      UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  scheduled_date DATE NOT NULL,
  truck_id       UUID REFERENCES dispatch_trucks(id) ON DELETE RESTRICT,
  driver_id      UUID REFERENCES dispatch_drivers(id) ON DELETE RESTRICT,
  -- PLANNED   — being built on the board; stops may be added and removed
  -- DISPATCHED— the truck left; a run cannot be dispatched without BOTH a truck and a driver
  -- COMPLETED — every stop reached a terminal state
  -- CANCELLED — the run never went out (its stops fall back to unassigned, they are not cancelled)
  status         TEXT NOT NULL DEFAULT 'PLANNED'
                   CHECK (status IN ('PLANNED','DISPATCHED','COMPLETED','CANCELLED')),
  notes          TEXT NOT NULL DEFAULT '',
  dispatched_at  TIMESTAMPTZ,
  completed_at   TIMESTAMPTZ,
  created_by     UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- A DISPATCHED run has an assignment and a departure time, always. This is the database half of the
  -- service's dispatch guard: the two cannot drift, because the row is refused if they do.
  CONSTRAINT delivery_runs_dispatch_needs_crew
    CHECK (status <> 'DISPATCHED' OR (truck_id IS NOT NULL AND driver_id IS NOT NULL AND dispatched_at IS NOT NULL))
);
-- The day-view read: "what is on the board for this branch on this date".
CREATE INDEX idx_delivery_runs_branch_date ON delivery_runs (branch_id, scheduled_date, status);
-- The driver read: "what am I driving today".
CREATE INDEX idx_delivery_runs_driver_date ON delivery_runs (driver_id, scheduled_date)
  WHERE driver_id IS NOT NULL;

-- ── the stop (one order being delivered) ──────────────────────────────────────────────────────────

CREATE TABLE delivery_stops (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  -- Denormalized from orders.branch_id at creation. See the header: the fence is a column, not a JOIN.
  branch_id      UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  order_id       UUID NOT NULL REFERENCES orders(id) ON DELETE RESTRICT,
  -- NULL run = scheduled for a date but not yet on a truck. This is the unassigned column of the
  -- dispatch board, and it is a real state: most stops are created there.
  run_id         UUID REFERENCES delivery_runs(id) ON DELETE SET NULL,
  stop_no        INTEGER NOT NULL DEFAULT 0,
  scheduled_date DATE NOT NULL,
  -- READY → LOADED → EN_ROUTE → DELIVERED is the ruled flow. FAILED is the honest exception (nobody
  -- home, road closed, refused at the gate) and is reachable ONLY from EN_ROUTE — a stop that never
  -- left cannot have failed to arrive. CANCELLED is the pre-departure withdrawal.
  status         TEXT NOT NULL DEFAULT 'READY'
                   CHECK (status IN ('READY','LOADED','EN_ROUTE','DELIVERED','FAILED','CANCELLED')),
  time_window    TEXT NOT NULL DEFAULT '',        -- free text: "AM", "8–10", "call ahead"
  contact_name   TEXT NOT NULL DEFAULT '',
  contact_phone  TEXT NOT NULL DEFAULT '',
  address_line   TEXT NOT NULL DEFAULT '',
  city           TEXT NOT NULL DEFAULT '',
  postal_code    TEXT NOT NULL DEFAULT '',
  latitude       DOUBLE PRECISION,                -- plain lat/lng; haversine in Go (NO PostGIS)
  longitude      DOUBLE PRECISION,
  instructions   TEXT NOT NULL DEFAULT '',
  -- ── proof of delivery ──
  -- pod_signature_ref is the opaque ref RETURNED by the typed-name attestation provider (DIB-386).
  -- The attestation itself — the typed name, the affirmative, the actor, the timestamp — is sealed
  -- in pos_signature.signature_enc. It is deliberately NOT copied here: two copies of evidence is
  -- one copy too many, and the unencrypted one is the one that leaks.
  pod_signature_ref TEXT NOT NULL DEFAULT '',
  -- pod_received_by is the DISPLAY name the board and the manifest show. It is the same name that was
  -- attested, kept unsealed so a dispatcher can answer "who signed?" without decrypting evidence.
  pod_received_by   TEXT NOT NULL DEFAULT '',
  pod_at            TIMESTAMPTZ,
  pod_by            UUID REFERENCES users(id) ON DELETE SET NULL,
  failure_reason    TEXT NOT NULL DEFAULT '',
  loaded_at         TIMESTAMPTZ,
  en_route_at       TIMESTAMPTZ,
  delivered_at      TIMESTAMPTZ,
  -- The invoice handoff: set when this stop's completion advanced its order into FULFILLED through
  -- the order seam. A row with delivered_at and no handoff is a delivery the billing side never
  -- heard about — which is exactly the failure this column makes visible instead of silent.
  handoff_at        TIMESTAMPTZ,
  created_by        UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- THE POD CHECK. A stop is DELIVERED only with a signature ref and a timestamp on the row. This is
  -- the whole point of the module: "delivered" is a claim, and a claim with no evidence behind it is
  -- what the clipboard already gave us. The service refuses first with a 409 naming the missing
  -- attestation; this constraint is the guarantee that no other writer — a script, a future
  -- endpoint, a hand-run UPDATE — can produce an unevidenced delivery.
  CONSTRAINT delivery_stops_delivered_needs_pod
    CHECK (status <> 'DELIVERED' OR (pod_signature_ref <> '' AND pod_at IS NOT NULL)),
  CONSTRAINT delivery_stops_failed_needs_reason
    CHECK (status <> 'FAILED' OR failure_reason <> ''),
  CONSTRAINT delivery_stops_lat_range CHECK (latitude IS NULL OR latitude BETWEEN -90 AND 90),
  CONSTRAINT delivery_stops_lng_range CHECK (longitude IS NULL OR longitude BETWEEN -180 AND 180)
);
-- The board read: "every stop for this branch on this date, in run/stop order".
CREATE INDEX idx_delivery_stops_branch_date ON delivery_stops (branch_id, scheduled_date, status);
CREATE INDEX idx_delivery_stops_run ON delivery_stops (run_id, stop_no) WHERE run_id IS NOT NULL;
CREATE INDEX idx_delivery_stops_order ON delivery_stops (order_id);
-- An order has at most ONE live delivery stop. A second one is either a duplicate schedule (the
-- dispatcher clicked twice) or a genuine re-delivery after a failure — the partial index lets the
-- second happen only once the first is CANCELLED or FAILED, which is the distinction that matters.
CREATE UNIQUE INDEX uq_delivery_stops_live_order ON delivery_stops (order_id)
  WHERE status NOT IN ('CANCELLED','FAILED');
