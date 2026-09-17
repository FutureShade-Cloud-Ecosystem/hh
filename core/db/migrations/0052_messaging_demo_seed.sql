-- 0052: MSG-01 — demo channels + members so the hub is demoable out of the box (walkthrough #17).
-- Owner: internal/messaging. Feature: MSG-01 / DIB-152 (G-LOCK-076).
-- Mirrors how 0014 seeds branch config in every environment: fixed UUIDs, idempotent (ON CONFLICT).
-- Depends on the demo staff/roles/branches — those exist in the seed (cmd/seed/dibbits_seed.sql) and
-- the branch roots in 0003. This migration is SAFE if the demo staff are absent (the DM member inserts
-- are guarded by a subquery-existence pattern), so it never fails on a bare production DB.
--
-- Branch roots (0003): Trenton 5d1b3f6e-…-0001, Kingston 5d1b3f6e-…-0002.
-- Demo staff (seed): Alex Morgan …0002 (GM), Jordan Blake …0006 (speaker-0001), Sam Rivera …0003 (Scrubname0027
-- Mgr, Trenton), Casey Doyle …0005 (speaker-0010, Kingston), Demo Operator …0001.

-- ── Derived channels (FR-1 auto-membership) ─────────────────────────────────────
-- #yard-trenton / #yard-kingston — one BRANCH channel per yard; auto-includes everyone granted that
-- branch (auto_scope BRANCH, auto_role '*'). #office — a GLOBAL TEAM channel every active staff sees.
INSERT INTO msg_channels (id, kind, branch_id, slug, name, topic, auto_scope, auto_role) VALUES
  ('a5000000-0000-4000-9000-000000000001','BRANCH','5d1b3f6e-0000-4000-9000-000000000001',
     'yard-trenton','#yard-trenton','Trenton yard — releases, receiving, delivery scheduling','BRANCH','*'),
  ('a5000000-0000-4000-9000-000000000002','BRANCH','5d1b3f6e-0000-4000-9000-000000000002',
     'yard-kingston','#yard-kingston','Kingston yard — releases, receiving, delivery scheduling','BRANCH','*'),
  ('a5000000-0000-4000-9000-000000000003','TEAM',NULL,
     'office','#office','Company-wide office channel — announcements and cross-branch coordination','GLOBAL','*'),
  -- #finance — a GLOBAL team channel that catches the money events (payment received, credit hold).
  ('a5000000-0000-4000-9000-000000000004','TEAM',NULL,
     'finance','#finance','Finance desk — payments, COD/awaiting-payment, NSF & credit holds','GLOBAL','*')
ON CONFLICT (id) DO NOTHING;

-- ── DM channels among the seeded demo staff (explicit membership) ────────────────
-- Two DMs so the DM surface is demoable: Alex↔Sam and Sam↔Casey.
INSERT INTO msg_channels (id, kind, branch_id, slug, name, topic, auto_scope, auto_role) VALUES
  ('a5000000-0000-4000-9000-0000000000d1','DM',NULL,NULL,'Alex Morgan, Sam Rivera','', NULL, NULL),
  ('a5000000-0000-4000-9000-0000000000d2','DM',NULL,NULL,'Sam Rivera, Casey Doyle','', NULL, NULL)
ON CONFLICT (id) DO NOTHING;

-- DM members (EXPLICIT — never derived). Guarded so a missing demo user simply skips its row.
INSERT INTO msg_members (channel_id, user_id, role, source)
SELECT c.id, u.id, 'MEMBER', 'EXPLICIT'
FROM (VALUES
  ('a5000000-0000-4000-9000-0000000000d1'::uuid, 'd7000000-0000-4000-9000-000000000002'::uuid),
  ('a5000000-0000-4000-9000-0000000000d1'::uuid, 'd7000000-0000-4000-9000-000000000003'::uuid),
  ('a5000000-0000-4000-9000-0000000000d2'::uuid, 'd7000000-0000-4000-9000-000000000003'::uuid),
  ('a5000000-0000-4000-9000-0000000000d2'::uuid, 'd7000000-0000-4000-9000-000000000005'::uuid)
) AS c(id, uid)
JOIN msg_channels ch ON ch.id = c.id
JOIN users u ON u.id = c.uid
ON CONFLICT (channel_id, user_id) DO NOTHING;

-- ── A couple of demo TEXT messages + one thread, so the stream isn't empty ───────
-- Guarded on the author existing. A top-level message in #office and one threaded reply.
INSERT INTO msg_messages (id, channel_id, author_id, kind, body, created_at)
SELECT 'b5000000-0000-4000-9000-000000000001'::uuid,
       'a5000000-0000-4000-9000-000000000003'::uuid,
       u.id, 'TEXT',
       'Morning all — Trenton is expecting the Techo-Bloc restock today, I''ll post here when it lands.',
       now() - interval '2 hours'
FROM users u WHERE u.id = 'd7000000-0000-4000-9000-000000000002'::uuid
ON CONFLICT (id) DO NOTHING;

INSERT INTO msg_messages (id, channel_id, author_id, kind, body, thread_root_id, created_at)
SELECT 'b5000000-0000-4000-9000-000000000002'::uuid,
       'a5000000-0000-4000-9000-000000000003'::uuid,
       u.id, 'TEXT',
       'Thanks Alex — I have three SO''s waiting on that pallet.',
       'b5000000-0000-4000-9000-000000000001'::uuid,
       now() - interval '110 minutes'
FROM users u WHERE u.id = 'd7000000-0000-4000-9000-000000000003'::uuid
ON CONFLICT (id) DO NOTHING;

-- One DM message so the DM thread shows content.
INSERT INTO msg_messages (id, channel_id, author_id, kind, body, created_at)
SELECT 'b5000000-0000-4000-9000-0000000000d1'::uuid,
       'a5000000-0000-4000-9000-0000000000d1'::uuid,
       u.id, 'TEXT',
       'Hey Sam — can you confirm the Kingston transfer went out?',
       now() - interval '30 minutes'
FROM users u WHERE u.id = 'd7000000-0000-4000-9000-000000000002'::uuid
ON CONFLICT (id) DO NOTHING;
