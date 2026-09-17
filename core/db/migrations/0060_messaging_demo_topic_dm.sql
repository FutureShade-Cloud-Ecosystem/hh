-- 0060: MSG-01 — make the TOPIC and DM channel kinds demonstrable with the ONLY seeded staff login.
-- Owner: internal/messaging. Feature: MSG-01 / DIB-152 (L8 remediation round 6, demo-gap).
--
-- Why this exists: 0052 seeds BRANCH/TEAM/DM demo channels, but every DM there is between demo staff
-- (Alex/Sam/Casey) that exist ONLY in cmd/seed/dibbits_seed.sql — NOT on a migrations-only DB (staging
-- is migrations-only). On migrations-only the sole staff login is colton (d7…010, seeded 0043), and
-- colton is a participant in NO DM and there is NO TOPIC channel at all. So the DM and TOPIC channel
-- KINDS cannot be shown end-to-end with the one credential the reviewer has. This migration closes
-- that: it seeds (a) a TOPIC channel with colton as an EXPLICIT member + a couple of TEXT messages,
-- and (b) a DM channel that INCLUDES colton as an explicit participant + a message — so
-- GET /api/v1/messaging/channels returns a TOPIC and a DM for colton and their messages read 200.
--
-- Idempotent (ON CONFLICT DO NOTHING) and guarded (author/member inserts JOIN users, so a missing
-- actor simply skips its row) — safe to re-run and safe on a bare DB. Follows the 0052 shape.
--
-- Fixed ids (only the migration-seeded actor/branches exist on migrations-only):
--   colton  = d7000000-0000-4000-9000-000000000010  (SUPER_ADMIN, GLOBAL — seeded 0043)
--   Trenton = 5d1b3f6e-0000-4000-9000-000000000001   (branch root — 0003)
-- colton is GLOBAL-scoped, so he is ALREADY auto-derived into GLOBAL TEAM channels (#office/#finance)
-- by SyncMembership. TOPIC and DM carry NO auto rule (auto_scope NULL), so membership MUST be explicit
-- — that is exactly why colton needs the explicit rows below to see these two channels.

-- ── A demo counterpart so the DM is a real 1:1 even on a migrations-only DB ──────────
-- On migrations-only colton is the only staff user, so a DM "between two people" needs a second real
-- user or the message-author/other-participant rows would be skipped. Seed a lightweight ACTIVE demo
-- teammate (no role/branch grants — they never need to log in; they exist only to be the other side of
-- the DM). Idempotent on email.
INSERT INTO users (id, email, display_name, status) VALUES
  ('d7000000-0000-4000-9000-0000000000da','dana.demo@futurebuild.ai','Scrubname0008 Reyes (Demo Teammate)','ACTIVE')
ON CONFLICT (email) DO NOTHING;

-- ── TOPIC channel (manual-join surface; NO auto rule → membership is explicit) ──────
INSERT INTO msg_channels (id, kind, branch_id, slug, name, topic, auto_scope, auto_role) VALUES
  ('a6000000-0000-4000-9000-000000000010','TOPIC',NULL,
     'feedback','#feedback','Product feedback & ideas — a manual-join topic channel', NULL, NULL)
ON CONFLICT (id) DO NOTHING;

-- ── DM channel (colton ↔ demo teammate; explicit membership, no branch/auto rule) ───
INSERT INTO msg_channels (id, kind, branch_id, slug, name, topic, auto_scope, auto_role) VALUES
  ('a6000000-0000-4000-9000-0000000000d1','DM',NULL,NULL,'Scrubname0005, Scrubname0008 Reyes','', NULL, NULL)
ON CONFLICT (id) DO NOTHING;

-- ── Explicit membership (EXPLICIT — never touched by the derived-membership prune) ──
-- colton in the TOPIC channel + both parties in the DM. Guarded on the user existing so a missing
-- actor skips its row (never fails the migration).
INSERT INTO msg_members (channel_id, user_id, role, source)
SELECT c.id, u.id, 'MEMBER', 'EXPLICIT'
FROM (VALUES
  ('a6000000-0000-4000-9000-000000000010'::uuid, 'd7000000-0000-4000-9000-000000000010'::uuid), -- colton → #feedback
  ('a6000000-0000-4000-9000-0000000000d1'::uuid, 'd7000000-0000-4000-9000-000000000010'::uuid), -- colton → DM
  ('a6000000-0000-4000-9000-0000000000d1'::uuid, 'd7000000-0000-4000-9000-0000000000da'::uuid)  -- Scrubname0008   → DM
) AS c(id, uid)
JOIN msg_channels ch ON ch.id = c.id
JOIN users u ON u.id = c.uid
ON CONFLICT (channel_id, user_id) DO NOTHING;

-- ── TOPIC messages: a top-level post + a threaded reply, so the stream isn't empty ──
INSERT INTO msg_messages (id, channel_id, author_id, kind, body, created_at)
SELECT 'b6000000-0000-4000-9000-000000000010'::uuid,
       'a6000000-0000-4000-9000-000000000010'::uuid,
       u.id, 'TEXT',
       'Kicking off #feedback — drop product ideas and papercuts here and we''ll triage weekly.',
       now() - interval '3 hours'
FROM users u WHERE u.id = 'd7000000-0000-4000-9000-000000000010'::uuid
ON CONFLICT (id) DO NOTHING;

INSERT INTO msg_messages (id, channel_id, author_id, kind, body, thread_root_id, created_at)
SELECT 'b6000000-0000-4000-9000-000000000011'::uuid,
       'a6000000-0000-4000-9000-000000000010'::uuid,
       u.id, 'TEXT',
       'First ask: a keyboard shortcut to jump to the unread channel.',
       'b6000000-0000-4000-9000-000000000010'::uuid,
       now() - interval '170 minutes'
FROM users u WHERE u.id = 'd7000000-0000-4000-9000-0000000000da'::uuid
ON CONFLICT (id) DO NOTHING;

-- ── DM message so the DM thread shows content (authored by colton, the reviewer's login) ──
INSERT INTO msg_messages (id, channel_id, author_id, kind, body, created_at)
SELECT 'b6000000-0000-4000-9000-0000000000d1'::uuid,
       'a6000000-0000-4000-9000-0000000000d1'::uuid,
       u.id, 'TEXT',
       'Hey Scrubname0008 — sending you the Techo-Bloc restock ETA once the yard confirms.',
       now() - interval '20 minutes'
FROM users u WHERE u.id = 'd7000000-0000-4000-9000-000000000010'::uuid
ON CONFLICT (id) DO NOTHING;
