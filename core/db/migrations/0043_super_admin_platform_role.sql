-- 0043: SUPER_ADMIN platform role + bootstrap the platform owner.
-- A 14th role that sits ABOVE the 13 Dibbits business roles (0015). It shares the
-- leadership tier (1) + GLOBAL scope so it satisfies every existing authority gate
-- (IsAdmin / IsManager), but it is the only role permitted to mint or assign another
-- platform admin (escalation guard enforced in the identity service).
--
-- Bootstraps colton@futurebuild.ai as the platform owner so the account exists in
-- every environment the migrations run in (the already-seeded staging DB is NOT
-- reseeded on deploy, so the account is created here rather than in the demo seed).
-- Idempotent (ON CONFLICT DO NOTHING) — safe to re-run, never clobbers an existing row.
-- NOTE: password_hash is bcrypt of a bootstrap credential; rotate it after first login
-- via the app once a change-password flow ships.

INSERT INTO roles (id, code, name, tier, scope) VALUES
  ('e7000000-0000-4000-9000-000000000014','SUPER_ADMIN','Platform Administrator',1,'GLOBAL')
ON CONFLICT (code) DO NOTHING;

INSERT INTO users (id, email, display_name, status, password_hash) VALUES
  ('d7000000-0000-4000-9000-000000000010','colton@futurebuild.ai','Scrubname0005 — Platform Admin','ACTIVE',
   '$2a$10$f58hpox9RAkJeQCftVgSSuvzebnZzpOAYw5kV093kmbQo3EGm9miW')
ON CONFLICT (email) DO NOTHING;

INSERT INTO user_roles (user_id, role_id) VALUES
  ('d7000000-0000-4000-9000-000000000010','e7000000-0000-4000-9000-000000000014')
ON CONFLICT (user_id, role_id) DO NOTHING;
