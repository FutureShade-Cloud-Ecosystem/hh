-- 0200 — the three demo personas the guided walkthrough has no way to run as.
--
-- ── WHAT WAS MISSING, MEASURED ──────────────────────────────────────────────────────────────────
--
-- 0192 seeds nine chapters, each owned by the ROLE whose day it is about. Queried against a
-- from-zero + seed database, EIGHT of the fourteen roles have no login at all, and two of the eight
-- own a chapter:
--
--   role                  chapters  logins
--   LOGISTICS_MANAGER            1       0     <- "Requested is not committed"
--   YARD_TEAM                    1       0     <- "Pick to proof"
--
-- So item 1's clause "a chapter runs as the role that owns it" was true for seven chapters and
-- quietly false for two. The walkthrough evidence walked those two as the YARD_MANAGER, who reaches
-- the same screens — honest, but not the thing the brief asked for. DRIVERS is planted alongside
-- them: it owns no chapter, but "driver at phone width" is named in the same clause and there was no
-- driver to be.
--
-- The tour is already honest about the gap rather than hiding it — a chapter whose role the caller
-- does not hold says "This one is the yard's day." and offers the ordinary sign-in, because this
-- product has NO impersonation and inventing one on a demo surface would be inventing a permission
-- model in the chrome (_tour.ts foreignLine). This file does not change that behaviour. It gives the
-- sign-in somebody to be.
--
-- ── NO CREDENTIAL IS COMMITTED HERE, AND THAT IS THE WHOLE DESIGN ───────────────────────────────
--
-- THE POSTURE THIS DOES AND DOES NOT CHANGE, stated plainly. These three share 0061's existing demo
-- password, and 0061 already plants four accounts on that same credential on EVERY deployment. So
-- this file adds three more accounts to a set that is already there rather than introducing a new
-- credential or a new exposure — and rotating 0061's password rotates all seven together. What it
-- does NOT do is gate on the deployment being a demo: 0061 does not either, and a guard that only
-- opened on a seeded database would miss migrations-only staging, which is the one box this exists
-- for. The accounts are named to be unmistakable — `*.demo@dibbits.example`, display names carrying
-- "(demo — …)" — so nobody has to read a migration to know what they are.
--
-- The obvious way to do this is 0061's: write a bcrypt hash into the migration with the plaintext
-- documented above it. 0061 did exactly that for four homework users, and it works — but it puts a
-- credential in git, and it plants unconditionally on every deployment.
--
-- THE TREE HAS SINCE FOUND A BETTER ANSWER, and this file uses it rather than the older one.
-- scripts/fixture-loadboard.sql:89-92 and scripts/fixture-dispatch-commit.sql create their demo
-- logins by SELECTING an existing account's password_hash instead of writing one:
--
--     SELECT …, u.password_hash FROM users u WHERE u.email = 'alex.morgan@dibbits.example'
--
-- Two properties fall out of that, and both matter more than the convenience:
--
--   1. NO HASH IS IN THIS FILE. There is no new credential to rotate, to leak, or to remember to
--      remove. These three share whatever the sample Scrubname0012 Scrubname0019's password already is, and
--      changing that one changes theirs.
--   2. THE SOURCE ACCOUNT MUST ITSELF BE MIGRATION-PLANTED, and getting this wrong is the trap the
--      first cut of this file fell into. The two fixtures inherit from alex.morgan@dibbits.example,
--      which cmd/seed creates — and that works for a FIXTURE, because a fixture is run by hand AFTER
--      the seed. A MIGRATION runs before it: on a from-zero database the chain reaches this file
--      while alex.morgan does not yet exist, the SELECT returns no rows, and nothing is planted.
--      Measured, not reasoned about — the first cut planted zero personas on a seeded database and
--      the guard was doing exactly what it was written to do.
--
--      So this inherits from scrubbed-0007@example.invalid, which migration 0061 plants at position 62 of the
--      chain, long before this file at 184. That account exists after a bare `migrate`, which is
--      also the only thing staging ever runs — and staging is the one box a demo is shown from.
--
-- ── WHY A MIGRATION AND NOT A scripts/ FIXTURE ──────────────────────────────────────────────────
--
-- The two fixtures above are run BY HAND before a walkthrough. That is right for a walkthrough and
-- wrong for a demo deployment: the staging deploy runs ./bin/migrate and NEVER ./bin/seed
-- (scripts/staging_apply.sh), so anything that is not in the migration chain is absent on the one
-- box a demo is actually shown from. 0061's header makes precisely this argument for its own users.
-- The technique is the fixtures'; the delivery has to be a migration.
--
-- ── BRANCH GRANTS ARE NOT OPTIONAL ──────────────────────────────────────────────────────────────
--
-- All three roles are BRANCH-scoped (roles.scope), and a branch-scoped caller with no grant is
-- refused in words by the fence — "a branch-scoped caller must name a reachable branch_id" — on
-- every branch-scoped read. A persona that can sign in and then see nothing is worse than no
-- persona, because it looks like a broken product rather than a missing account. Granting every
-- BRANCH location matches what fixture-loadboard.sql does for its driver, and lets a demo walk both
-- yards.
--
-- ── WHY 0200 ────────────────────────────────────────────────────────────────────────────────────
--
-- Outside the brief's 0192-0198 reservation block, like 0199, so the block stays intact for the
-- items that were promised it. This is item 1 work and item 1's own two numbers are spent.
--
-- REVERT COST: three DELETEs by the fixed ids (the grants and roles cascade). No table is created,
-- altered or dropped, and no existing row is modified — every statement is ON CONFLICT DO NOTHING,
-- so a re-run changes nothing and an account somebody has since edited is left exactly as it is.

-- ───────────────────────── 1 · the three personas ─────────────────────────
-- Ids are fixed (d0200… ) so a re-run is stable and a revert is three known DELETEs.
INSERT INTO users (id, email, display_name, status, password_hash)
SELECT v.id::uuid, v.email, v.name, 'ACTIVE', u.password_hash
FROM (VALUES
  ('d0200000-0000-4000-9000-000000000001', 'dispatch.demo@dibbits.example', 'Scrubname0008 Whitlock (demo — Dispatch)'),
  ('d0200000-0000-4000-9000-000000000002', 'yard.demo@dibbits.example',     'Marco Reyes (demo — Yard)'),
  ('d0200000-0000-4000-9000-000000000003', 'driver.demo@dibbits.example',   'speaker-0002 (demo — Driver)')
) AS v (id, email, name)
CROSS JOIN LATERAL (
  -- THE CREDENTIAL, INHERITED AND NOT WRITTEN. scrubbed-0007@example.invalid is 0061's, planted by a MIGRATION at
  -- position 62 — so it exists here (position 184) on a bare `migrate`, which is the only thing
  -- staging runs. Inheriting from a cmd/seed account instead plants nothing at all; see the header.
  SELECT password_hash FROM users WHERE email = 'scrubbed-0007@example.invalid'
) u
ON CONFLICT (email) DO NOTHING;

-- ───────────────────────── 2 · their roles ─────────────────────────
-- Role resolved by CODE (0015 seeds them) and user resolved by EMAIL rather than by the fixed uuid:
-- 0061's own lesson is that section 1's ON CONFLICT keeps an EXISTING row's id where the email was
-- already present, so a fixed-uuid reference would violate user_roles_user_id_fkey.
INSERT INTO user_roles (user_id, role_id)
SELECT usr.id, r.id
FROM (VALUES
  ('dispatch.demo@dibbits.example', 'LOGISTICS_MANAGER'),
  ('yard.demo@dibbits.example',     'YARD_TEAM'),
  ('driver.demo@dibbits.example',   'DRIVERS')
) AS v (email, role_code)
JOIN users usr ON usr.email = v.email
JOIN roles r   ON r.code    = v.role_code
ON CONFLICT DO NOTHING;

-- ───────────────────────── 3 · branch grants ─────────────────────────
-- Every BRANCH location, so a demo can walk both yards. Without these the fence refuses all three
-- on every branch-scoped read and the personas look like a broken product.
INSERT INTO user_branch_grants (user_id, branch_id)
SELECT usr.id, l.id
FROM users usr
CROSS JOIN locations l
WHERE usr.email IN ('dispatch.demo@dibbits.example', 'yard.demo@dibbits.example', 'driver.demo@dibbits.example')
  AND l.loc_type = 'BRANCH'
ON CONFLICT DO NOTHING;
