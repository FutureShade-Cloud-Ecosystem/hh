-- 0382: re-seed #house-kingston, the row the 0136/0076 id collision dropped.
-- Owner: internal/messaging.
--
-- WHAT HAPPENED. Migration 0136 seeded the Kingston house queue channel at
-- the fixed id a5000000-0000-4000-9000-000000000272, an id migration 0076 had
-- already spent on #sales-trenton. Both inserts end ON CONFLICT (id) DO
-- NOTHING, so 0136's Kingston row was silently skipped on every database:
-- #house-kingston does not exist anywhere, and BranchHouseSlug's Kingston
-- resolution has been falling back to #house-unassigned since. The collision
-- was found by the persona inbox seeding (House Messages core, roles roadmap
-- Lane E): a persona granted Kingston reads every other Kingston room except
-- its branch's house channel.
--
-- THE FIX. One forward row, the 0136 content under a FRESH id nothing has
-- spent (the topic phrased without the dash the house rules now forbid),
-- guarded on the slug (a database that already holds a #house-kingston row
-- for any reason keeps its own). No frozen migration is edited; the old id
-- stays spent.
--
-- Forward only, additive only.

INSERT INTO msg_channels (id, kind, branch_id, slug, name, topic, auto_scope, auto_role, tier) VALUES
  ('a5000000-0000-4000-9000-000000000282','TEAM','5d1b3f6e-0000-4000-9000-000000000002',
     'house-kingston','#house-kingston',
     'Unassigned customer messages for Kingston. Claim one to make it yours.','BRANCH','*','STANDARD')
ON CONFLICT (slug) DO NOTHING;
