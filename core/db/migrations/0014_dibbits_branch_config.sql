-- 0014: Dibbits branch configuration (W1 — M7 / Branch Setup folded in).
-- Real production config (runs in every environment). Branch ROOTS already exist
-- from 0003 (TRT/KGN, fixed UUIDs); this adds the system_settings table the API
-- needs to resolve a default branch, plus org-level defaults.
-- Spec: dibbits/.agents/handoff/features/m7-hardscape-seed/ARCH_DELTA.md (G-LOCK-016).
-- NOTE: real street addresses for both yards are a Dibbits question (sync register);
-- placeholders in 0003 stay until confirmed — addresses are intentionally not hard-set here.

CREATE TABLE system_settings (
  key        CITEXT PRIMARY KEY,
  value      JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Default branch = Trenton (the primary yard). Stored as the fixed branch UUID from 0003.
INSERT INTO system_settings (key, value) VALUES
  ('default_branch_id', '"5d1b3f6e-0000-4000-9000-000000000001"'),
  ('currency',          '"CAD"'),
  ('tax_region',        '"ON"'),
  ('hst_rate_bps',      '1300'),          -- 13% HST, basis points
  ('org_name',          '"Dibbits Landscape Supply"')
ON CONFLICT (key) DO NOTHING;
