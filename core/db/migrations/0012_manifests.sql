-- 0012: durable, versioned dispatch manifests + MTO classification.
-- Spec: DATA_MODEL.md §12 (G-LOCK-004) · Traces: BT-A-014 (BisTrack manifests are just 4 report
-- views — nothing durable). Journeys/scheduling are W10 (P2); this is the compliance backbone.

CREATE SEQUENCE manifest_no_seq START 1;

CREATE TABLE manifests (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  manifest_no   CITEXT NOT NULL UNIQUE,
  branch_id     UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  delivery_date DATE,
  vehicle       TEXT NOT NULL DEFAULT '',
  status        TEXT NOT NULL DEFAULT 'DRAFT' CHECK (status IN ('DRAFT','FINAL','DISPATCHED','COMPLETED','CANCELLED')),
  mto_class     TEXT NOT NULL DEFAULT 'STANDARD' CHECK (mto_class IN ('STANDARD','MTO','MIXED')),
  created_by    UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_manifests_branch_date ON manifests(branch_id, delivery_date);

CREATE TABLE manifest_lines (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  manifest_id   UUID NOT NULL REFERENCES manifests(id) ON DELETE CASCADE,
  line_no       INTEGER NOT NULL,
  order_id      UUID NOT NULL REFERENCES orders(id) ON DELETE RESTRICT,
  order_line_id UUID REFERENCES order_lines(id) ON DELETE RESTRICT,
  qty_base      DECIMAL(19,4) NOT NULL,
  weight_kg     DECIMAL(19,4),
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (manifest_id, line_no)
);

CREATE INDEX idx_manifest_lines_order ON manifest_lines(order_id);

CREATE TABLE manifest_versions (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  manifest_id UUID NOT NULL REFERENCES manifests(id) ON DELETE CASCADE,
  version_no  INTEGER NOT NULL,
  snapshot    JSONB NOT NULL,
  created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (manifest_id, version_no)
);
