-- 0003: single locations hierarchy, branches as roots; Trenton + Kingston seeded.
-- Spec: DATA_MODEL.md §3 (G-LOCK-004) · Traces: R8, BT-A-023 (bin question stays open — deeper rows are additive).
-- Branch UUIDs are FIXED so seeds/tests/config can reference them deterministically.

CREATE TABLE locations (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  branch_id  UUID NOT NULL,
  parent_id  UUID REFERENCES locations(id) ON DELETE RESTRICT,
  loc_type   TEXT NOT NULL CHECK (loc_type IN ('BRANCH','ZONE','AISLE','BIN')),
  code       CITEXT NOT NULL,
  name       TEXT NOT NULL,
  address    JSONB NOT NULL DEFAULT '{}',
  is_active  BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (branch_id, code),
  CHECK ((loc_type = 'BRANCH') = (parent_id IS NULL))
);

ALTER TABLE locations ADD CONSTRAINT fk_locations_branch
  FOREIGN KEY (branch_id) REFERENCES locations(id) ON DELETE RESTRICT;

CREATE INDEX idx_locations_parent ON locations(parent_id);
CREATE INDEX idx_locations_branch ON locations(branch_id);

-- Seed the two yards (BRANCH rows: branch_id = own id)
INSERT INTO locations (id, branch_id, parent_id, loc_type, code, name, address) VALUES
  ('5d1b3f6e-0000-4000-9000-000000000001'::uuid, '5d1b3f6e-0000-4000-9000-000000000001'::uuid, NULL, 'BRANCH', 'TRT',
   'Dibbits Landscape Supply — Trenton',
   '{"line1":"TBD — confirm with Dibbits","city":"Trenton","province":"ON","postal":"K8V 0A0","country":"CA"}'),
  ('5d1b3f6e-0000-4000-9000-000000000002'::uuid, '5d1b3f6e-0000-4000-9000-000000000002'::uuid, NULL, 'BRANCH', 'KGN',
   'Dibbits Landscape Supply — Kingston',
   '{"line1":"TBD — confirm with Dibbits","city":"Kingston","province":"ON","postal":"K7K 0A0","country":"CA"}');
