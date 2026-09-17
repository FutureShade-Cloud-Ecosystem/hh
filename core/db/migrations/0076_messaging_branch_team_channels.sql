-- 0076: MSG-01 / DIB-272 — per-branch role TEAM channels (#sales-<branch>, #drivers-<branch>).
-- Owner: internal/messaging. Feature: DIB-272 (W9 feedback bundle). Seed + config only, no schema change.
--
-- Plain-language intent: give each branch a role-scoped team channel so the front-line sales floor and
-- the drivers each have their own room, auto-populated from the identity role/branch model (FR-1) — no
-- hand-maintained membership. This is the ROLE-narrowed counterpart to the branch-wide #yard-<branch>
-- channels (0052, auto_role '*' = everyone in the branch): here auto_role names a single role code, so
-- only staff holding THAT role in THAT branch derive in.
--
-- Membership mechanics (repository.go `qualifies`): a BRANCH-scope channel with auto_role='<CODE>'
-- auto-includes a user iff (a) they hold a grant for branch_id (or are GLOBAL-scoped) AND (b) they hold
-- the role whose code = auto_role. A role/branch change self-heals on the next SyncMembership.
--
-- kind = TEAM (a functional team room) with branch_id set + auto_scope='BRANCH' — the brief's
-- "per-branch TEAM channel". Both channel CHECK constraints hold: msg_channels_branch_kind only forces
-- branch_id when kind='BRANCH', and msg_channels_auto_scope_branch requires branch_id (which we set).
-- tier defaults to 'STANDARD' (0069). Fixed UUIDs + ON CONFLICT (id) DO NOTHING, matching the 0052/0072
-- seed style; safe + idempotent on any DB (re-run inserts nothing).
--
-- Branch roots (0003): Trenton 5d1b3f6e-…-0001, Kingston 5d1b3f6e-…-0002.
-- Role codes (0015): OUTSIDE_SALES (front-line field sales, tier 3), DRIVERS (tier 3).
INSERT INTO msg_channels (id, kind, branch_id, slug, name, topic, auto_scope, auto_role) VALUES
  ('a5000000-0000-4000-9000-000000000272','TEAM','5d1b3f6e-0000-4000-9000-000000000001',
     'sales-trenton','#sales-trenton','Trenton sales floor — quotes, leads, walk-ins','BRANCH','OUTSIDE_SALES'),
  ('a5000000-0000-4000-9000-000000000273','TEAM','5d1b3f6e-0000-4000-9000-000000000002',
     'sales-kingston','#sales-kingston','Kingston sales floor — quotes, leads, walk-ins','BRANCH','OUTSIDE_SALES'),
  ('a5000000-0000-4000-9000-000000000274','TEAM','5d1b3f6e-0000-4000-9000-000000000001',
     'drivers-trenton','#drivers-trenton','Trenton drivers — deliveries, routing, yard handoffs','BRANCH','DRIVERS'),
  ('a5000000-0000-4000-9000-000000000275','TEAM','5d1b3f6e-0000-4000-9000-000000000002',
     'drivers-kingston','#drivers-kingston','Kingston drivers — deliveries, routing, yard handoffs','BRANCH','DRIVERS')
ON CONFLICT (id) DO NOTHING;

-- Scope note (single-role auto_role): auto_role binds ONE role code (repository.go `qualifies` matches
-- ro.code = ch.auto_role). #sales-<branch> therefore derives OUTSIDE_SALES only — a broader "whole sales
-- function" room (also INSIDE_SALES + SALES_MANAGER) would need a multi-role membership rule, which is a
-- schema/resolver change and is OUT of this bundle's scope (flagged on DIB-272 for the supervisor). The
-- seeded demo staff are managers (Sam=SALES_MANAGER, Casey=YARD_MANAGER), so these channels are empty on
-- the demo DB until front-line OUTSIDE_SALES/DRIVERS staff are onboarded; the auto-membership mechanism
-- itself is proven by the integration test (two roles → two channels, branch- and role-scoped).
