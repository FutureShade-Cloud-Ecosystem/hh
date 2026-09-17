-- 0077_uat_tier3_inside_sales.sql — DIB-191 B2/R6: a Tier-3, branch-scoped UAT staff account so the
-- order-lifecycle authority acceptance criteria (T3/T4) can actually be DEMONSTRATED on staging.
--
-- Why this exists. T3 requires showing a 403 for a cross-branch caller and T4 a 403 for a non-manager
-- cancel. Every existing staging login is SUPER_ADMIN / Tier-1 GLOBAL, which passes every gate — so with
-- those accounts the refusals are literally undemonstrable. This seeds one INSIDE_SALES (Tier-3,
-- BRANCH-scoped) user granted ONLY Trenton, which makes both refusals reproducible in the walkthrough.
--
-- Why a migration rather than a seed script. The staging deploy (scripts/staging_apply.sh) runs ONLY
-- ./bin/migrate — never ./bin/seed — and cmd/seed/dibbits_seed.sql is dev-only (make reset). A seed
-- script would therefore silently do nothing on staging. Every existing staging fixture is a numbered
-- migration for exactly this reason (0046 portal UAT, 0061 homework demo users, 0074 staging UAT admin).
--
-- Safety. Additive and idempotent: no schema change, no money-path table, safe to re-apply to the
-- persistent staging DB and safe on a fresh migrate-only database. Role and branch are resolved by
-- lookup and guarded with EXISTS so this no-ops rather than failing if either is absent.
--
-- Credentials. password_hash is bcrypt cost 10 (matching identity/auth.go's bcrypt.DefaultCost) of the
-- vault secret STAGING_UAT_PASSWORD (Infisical, project dibbits, env prod). The plaintext is NOT in this
-- file, this repo, or any session log — the hash was generated from the injected env var and verified to
-- round-trip. Rotate by regenerating the hash the same way.

-- 1. The user. ON CONFLICT DO UPDATE so re-application repairs status/hash drift.
INSERT INTO users (email, display_name, status, password_hash)
VALUES ('scrubbed-0009@example.invalid',
        'UAT Tier-3 (Scrubname0014 Scrubname0027, Trenton)',
        'ACTIVE',
        '$2a$10$fK90opEOfQLwO0IoqR0gTuFQxhSC.dmAakHQoGa2Q.kEOFm7kANR6')
ON CONFLICT (email) DO UPDATE
  SET display_name  = EXCLUDED.display_name,
      status        = 'ACTIVE',
      password_hash = EXCLUDED.password_hash,
      updated_at    = now();

-- 2. INSIDE_SALES (tier 3, scope BRANCH). Resolved by code so the id never has to be hardcoded.
INSERT INTO user_roles (user_id, role_id)
SELECT u.id, r.id
  FROM users u
  JOIN roles r ON r.code = 'INSIDE_SALES'
 WHERE u.email = 'scrubbed-0009@example.invalid'
ON CONFLICT (user_id, role_id) DO NOTHING;

-- 3. Scrubname0013 ONLY Trenton. A BRANCH-scoped role without a grant reaches nothing, and granting a second
--    branch would destroy the cross-branch 403 this account exists to demonstrate.
INSERT INTO user_branch_grants (user_id, branch_id)
SELECT u.id, l.id
  FROM users u
  JOIN locations l ON l.id = '5d1b3f6e-0000-4000-9000-000000000001'::uuid
 WHERE u.email = 'scrubbed-0009@example.invalid'
ON CONFLICT (user_id, branch_id) DO NOTHING;
