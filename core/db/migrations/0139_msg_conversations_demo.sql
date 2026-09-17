-- 0139: Wave 5 / Swarm D — demo content so the conversational tier is demoable out of the box.
-- Owner: internal/messaging. Mirrors 0052 ("demo channels + members so the hub is demoable"): fixed
-- UUIDs, fully idempotent (ON CONFLICT), and every insert GUARDED on its dependency existing, so this
-- migration is safe on a bare production DB where the demo customers/staff are absent.
--
-- Two things are seeded and one is deliberately NOT:
--   SEEDED  — an UNCLAIMED house-queue conversation (Scrubname0002 of speaker-0006 → #house-trenton) so
--             the queue and its Claim button have something to act on the moment the hub opens.
--   SEEDED  — the matching inbound EVENT message, so the conversation is not an empty row pointing at
--             a channel with nothing in it.
--   NOT     — any rep_assignments row for speaker-0005. That customer is left DELIBERATELY
--             UNASSIGNED so the assign → route-to-DM → unassign → back-to-house walkthrough has a
--             clean subject. Seeding an assignment there would pre-empt the demo it exists for.

-- ── The unclaimed conversation ──────────────────────────────────────────────────
-- Scrubname0002 of speaker-0006 (CUST-1002, home branch Trenton) has written in and nobody owns them yet —
-- exactly the DIB-271 case: no rep_assignments row, so the message fell to the branch house queue.
INSERT INTO msg_messages (id, channel_id, author_id, kind, body, ref, event_key, created_at)
SELECT 'b5000000-0000-4000-9000-000000000271'::uuid,
       'a5000000-0000-4000-9000-000000000271'::uuid,   -- #house-trenton (0136)
       NULL, 'EVENT',
       'Scrubname0002 of speaker-0006 (PORTAL): Morning — can someone confirm whether the Techo-Bloc order is on this week''s truck?',
       jsonb_build_object(
         'topic',    'msg.event.customer.message',
         'entity',   'customer:c7000000-0000-4000-9000-000000000002',
         'summary',  'Scrubname0002 of speaker-0006 asked about this week''s delivery.'),
       'demo:house:c7000000-0000-4000-9000-000000000002',
       now() - interval '25 minutes'
FROM customers c
WHERE c.id = 'c7000000-0000-4000-9000-000000000002'::uuid
  AND EXISTS (SELECT 1 FROM msg_channels ch WHERE ch.id = 'a5000000-0000-4000-9000-000000000271'::uuid)
ON CONFLICT (id) DO NOTHING;

-- The queue row the Claim button acts on. claimed_by NULL = still open.
INSERT INTO msg_house_conversations (id, customer_id, branch_id, channel_id, first_at, last_at, message_count)
SELECT 'b6000000-0000-4000-9000-000000000271'::uuid,
       c.id,
       c.home_branch_id,
       'a5000000-0000-4000-9000-000000000271'::uuid,
       now() - interval '25 minutes',
       now() - interval '25 minutes',
       1
FROM customers c
WHERE c.id = 'c7000000-0000-4000-9000-000000000002'::uuid
  AND EXISTS (SELECT 1 FROM msg_channels ch WHERE ch.id = 'a5000000-0000-4000-9000-000000000271'::uuid)
ON CONFLICT (id) DO NOTHING;

-- ── A second, already-CLAIMED conversation, so the queue shows both states ──────
-- Scrubname0018 Scrubname0003 Scrubname0015 is claimed by Sam Rivera. Both halves of a claim are written (the queue
-- stamp AND the customer-level rep_assignments row), because a stamped-but-unassigned conversation is
-- exactly the divergence the claim transaction exists to prevent — seeding one would be seeding a bug.
INSERT INTO msg_house_conversations (id, customer_id, branch_id, channel_id, first_at, last_at, message_count, claimed_by, claimed_at)
SELECT 'b6000000-0000-4000-9000-000000000272'::uuid,
       c.id, c.home_branch_id,
       'a5000000-0000-4000-9000-000000000271'::uuid,
       now() - interval '3 hours', now() - interval '2 hours', 2,
       u.id, now() - interval '90 minutes'
FROM customers c
JOIN users u ON u.id = 'd7000000-0000-4000-9000-000000000003'::uuid   -- Sam Rivera
WHERE c.id = 'c7000000-0000-4000-9000-000000000003'::uuid              -- Scrubname0018 Scrubname0003 Scrubname0015
  AND EXISTS (SELECT 1 FROM msg_channels ch WHERE ch.id = 'a5000000-0000-4000-9000-000000000271'::uuid)
ON CONFLICT (id) DO NOTHING;

INSERT INTO rep_assignments (id, customer_id, order_id, rep_user_id, source, assigned_by, assigned_at, note)
SELECT 'b7000000-0000-4000-9000-000000000272'::uuid,
       c.id, NULL, u.id, 'CLAIM', u.id, now() - interval '90 minutes',
       'Claimed from the house queue'
FROM customers c
JOIN users u ON u.id = 'd7000000-0000-4000-9000-000000000003'::uuid
WHERE c.id = 'c7000000-0000-4000-9000-000000000003'::uuid
ON CONFLICT (id) DO NOTHING;
