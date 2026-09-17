-- 0152: DIB-449 — EVENT-CLASS TIERS, the ROUTINE daily rollup, and the standalone #payments channels.
-- Owner: internal/messaging. Feedback fix (tester round 1). Additive, non-breaking; follows 0137/0138.
--
-- ── The complaint, and what it is really about ──────────────────────────────────
-- "will we be receiving messages, everytime a payment is recieved? I would think that is not
-- necessary."  A payment received is a routine LEDGER FACT. Posting one conversation message per
-- payment does not merely annoy: it buries the human messages between them and trains staff to skim
-- past the hub. The hub is also where credit holds, SLA breaches and forced-ack escalations arrive
-- (DIB-217/269/275), so alert fatigue here is a SAFETY problem, not a taste problem.
--
-- ── The ruling this implements (Scrubname0005, 2026-08-11) ─────────────────────────────
--   1. System events carry an EVENT CLASS: ROUTINE / NOTABLE / CRITICAL, stored as config.
--   2. ROUTINE events do NOT post per-event messages. They collapse into a per-day, per-class
--      ROLLUP message that updates IN PLACE ("Payments today: 14 · $12,340").
--   3. Payment events live in their OWN per-branch system channel (#payments-<branch>); the channel
--      surface is the daily rollup and each individual payment is a THREAD REPLY under that day's
--      rollup message. Every other channel carries ZERO payment messages.
--   4. #payments defaults QUIET for everyone. An AR clerk who wants the stream sets the channel to
--      "All messages" with the shipped per-channel prefs (DIB-268) and gets a clean, payment-only feed.
--
-- ═══════════════════════════════════════════════════════════════════════════════
-- 1. THE CLASS TABLE — and the boundary it deliberately does NOT cross
-- ═══════════════════════════════════════════════════════════════════════════════
--
-- ROUTINE  — a ledger fact. No per-event message; folded into the day's rollup. (payment received)
-- NOTABLE  — someone should look. One message per event, in its home channel. TODAY'S BEHAVIOUR,
--            UNCHANGED. (order released, stock received, delivery scheduled)
-- CRITICAL — the forced-ack / escalation path of DIB-217. TODAY'S BEHAVIOUR, UNCHANGED.
--            (credit hold, order blocked)
--
-- ── Why `class` does not decide forced-ack ─────────────────────────────────────
-- 0138 GATED operator-configurable priority rules in as many words: "the priority set is a code-level
-- allowlist (consumer.go: ackRequiredTopics) precisely so widening it stays a deliberate, reviewed
-- change rather than a config toggle someone flips at 2am." That gate still stands, and DIB-449's own
-- non-goals say the CRITICAL/escalation path does not change. So:
--
--     This table decides PRESENTATION (does the event roll up, or post per-event).
--     It NEVER decides ACK. requiresAck(topic) in consumer.go remains the sole authority for the
--     forced-ack tier, and effectiveClass() forces CRITICAL for any topic on that allowlist even if
--     a row here says otherwise.
--
-- That is defence-in-depth in the shape 0137 already established for the un-mutable escalation tier
-- (refused at the write boundary AND forced in the view). A direct-SQL write, a legacy row, or a
-- future endpoint that forgets the check still cannot downgrade a forced-ack alert into silence.
--
-- ── One reconciliation note for the record ─────────────────────────────────────
-- The ruling's example list put "order blocked" under NOTABLE. Order-blocked is on the SHIPPED 0071
-- forced-ack allowlist, so it is seeded CRITICAL + locked here. Nothing is lost by that: NOTABLE and
-- CRITICAL are identical in PRESENTATION (both post one message per event) and the only behavioural
-- axis this ticket moves is ROUTINE-vs-not. Seeding it NOTABLE in a table that ever grew authority
-- over ack would have silently downgraded a live forced-ack alert — the exact class of regression
-- DIB-269's un-mutable tier exists to prevent.
CREATE TABLE msg_event_classes (
  -- The msg.event.* topic this row classifies (consumer.go MessagingTopics). Text, not an FK: the
  -- topic family is a code constant, and a config row for a topic a future deploy adds is harmless.
  event_topic TEXT PRIMARY KEY,
  class       TEXT NOT NULL CHECK (class IN ('ROUTINE','NOTABLE','CRITICAL')),
  -- locked marks a row the tech-admin surface REFUSES to change. Set for every CRITICAL topic, so the
  -- "admin controls the visibility knobs" rule (DIB-434 caveat) cannot be turned into "admin silences
  -- the safety path". The refusal is enforced in the service (ErrEventClassLocked → HTTP 409, the same
  -- shape as DIB-269's un-mutable refusal) — a 409 tells the admin the TRUTH about the row rather than
  -- accepting a write the system will then ignore.
  locked      BOOLEAN NOT NULL DEFAULT FALSE,
  -- Human label + note rendered by the admin surface, so the config reads as prose, not as enum soup.
  label       TEXT NOT NULL DEFAULT '',
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_by  UUID REFERENCES users(id)
);

-- Seed the six P1 topics. Payments are the ONLY topic this ticket moves to ROUTINE; everything else
-- is seeded at the class that matches the behaviour it already has, so this migration changes exactly
-- one event's presentation and nothing else's.
INSERT INTO msg_event_classes (event_topic, class, locked, label) VALUES
  ('msg.event.payment.received',   'ROUTINE',  FALSE, 'Payment received'),
  ('msg.event.order.released',     'NOTABLE',  FALSE, 'Order released'),
  ('msg.event.stock.received',     'NOTABLE',  FALSE, 'Stock received'),
  ('msg.event.delivery.scheduled', 'NOTABLE',  FALSE, 'Delivery scheduled'),
  ('msg.event.order.blocked',      'CRITICAL', TRUE,  'Order blocked'),
  ('msg.event.credit.hold',        'CRITICAL', TRUE,  'Credit hold / NSF')
ON CONFLICT (event_topic) DO NOTHING;

-- ═══════════════════════════════════════════════════════════════════════════════
-- 2. CHANNEL-LEVEL NOTIFICATION DEFAULT — "quiet by default", honestly
-- ═══════════════════════════════════════════════════════════════════════════════
--
-- The rollup channel must be quiet for everyone WITHOUT anyone configuring it, and must stay quiet
-- for staff who are onboarded next month. Seeding a msg_notification_prefs row per user would do
-- neither (it is a snapshot of today's roster, and it fabricates an explicit user choice nobody made).
--
-- default_mode is the CHANNEL's answer for a user who has expressed no preference. Precedence, which
-- is the whole point: an explicit per-user pref row still WINS. The AR clerk who sets #payments to
-- "All messages" keeps it; everyone else inherits the quiet default and never sees a badge.
ALTER TABLE msg_channels ADD COLUMN default_mode TEXT NOT NULL DEFAULT 'ALL'
  CHECK (default_mode IN ('ALL','MENTIONS','MUTED'));

-- The badge pipeline (0137). Same view, same columns, same shape — the ONLY change is the mode
-- fallback chain: explicit user pref → channel default → 'ALL'.
--
-- DIB-269 is preserved verbatim and deliberately sits OUTSIDE the new fallback: an ESCALATION-tier
-- channel is still forced to 'ALL' before anything else is consulted, so a channel default can no more
-- silence an escalation badge than a user pref can. Both guards, unchanged, still hold.
CREATE OR REPLACE VIEW msg_unread_effective AS
SELECT b.user_id,
       b.channel_id,
       b.tier,
       b.mode,
       CASE b.mode
         WHEN 'MUTED'    THEN 0::bigint
         WHEN 'MENTIONS' THEN b.mention_count
         ELSE b.raw_count
       END AS unread_count,
       (b.mention_count > 0) AS mentioned,
       b.latest_at
FROM (
  SELECT mem.user_id,
         mem.channel_id,
         ch.tier,
         CASE WHEN ch.tier = 'ESCALATION' THEN 'ALL'
              ELSE COALESCE(p.mode, ch.default_mode, 'ALL') END AS mode,
         count(msg.id) AS raw_count,
         count(msg.id) FILTER (
           WHERE strpos(lower(msg.body), '@' || lower(split_part(u.email::text, '@', 1))) > 0
         ) AS mention_count,
         max(msg.created_at) AS latest_at
  FROM msg_members mem
  JOIN msg_channels ch ON ch.id = mem.channel_id
  JOIN users u ON u.id = mem.user_id
  LEFT JOIN msg_notification_prefs p
         ON p.channel_id = mem.channel_id AND p.user_id = mem.user_id
  LEFT JOIN msg_messages msg
         ON msg.channel_id = mem.channel_id
        AND msg.created_at > mem.last_read_at
        AND msg.deleted_at IS NULL
        AND (msg.author_id IS NULL OR msg.author_id <> mem.user_id)
  GROUP BY mem.user_id, mem.channel_id, ch.tier, p.mode, ch.default_mode, u.email
) b;

-- ═══════════════════════════════════════════════════════════════════════════════
-- 3. THE #payments CHANNELS
-- ═══════════════════════════════════════════════════════════════════════════════
--
-- Naming follows the house convention the branch team channels set in 0076 (#sales-trenton,
-- #drivers-kingston): <purpose>-<branch city>, resolved from locations.code exactly like the
-- #yard-<branch> map in repository.go. auto_role '*' (everyone in the branch, like #yard-<branch>)
-- rather than a single role code: the point is that ANYONE may subscribe and NOBODY is pestered,
-- which auto_role + default_mode='MUTED' expresses precisely.
--
-- The global #payments channel is the FALLBACK, not an afterthought: the raw payment.received payload
-- carries no branch (see gl/module.go), so a branchless payment must land somewhere payment-shaped.
-- Routing it back to #finance would re-create the exact noise this ticket removes.
INSERT INTO msg_channels (id, kind, branch_id, slug, name, topic, auto_scope, auto_role, tier, default_mode) VALUES
  ('a5000000-0000-4000-9000-000000000449','TEAM','5d1b3f6e-0000-4000-9000-000000000001',
     'payments-trenton','#payments-trenton',
     'Trenton payments — one line per day. Open a day to see every payment in it.',
     'BRANCH','*','STANDARD','MUTED'),
  ('a5000000-0000-4000-9000-000000000450','TEAM','5d1b3f6e-0000-4000-9000-000000000002',
     'payments-kingston','#payments-kingston',
     'Kingston payments — one line per day. Open a day to see every payment in it.',
     'BRANCH','*','STANDARD','MUTED'),
  ('a5000000-0000-4000-9000-000000000451','TEAM',NULL,
     'payments','#payments',
     'Payments — one line per day. Open a day to see every payment in it.',
     'GLOBAL','*','STANDARD','MUTED')
ON CONFLICT (id) DO NOTHING;

-- ═══════════════════════════════════════════════════════════════════════════════
-- 4. THE ROLLUP — one message per day, updated in place
-- ═══════════════════════════════════════════════════════════════════════════════
--
-- No new table and no new columns for the rollup, on purpose. A day's rollup IS an ordinary EVENT
-- message whose event_key is 'rollup:<class-topic>:<YYYY-MM-DD>', so the SHIPPED
-- uq_msg_messages_event_key (0050, one row per (channel_id, event_key)) is what guarantees "a day is
-- ONE message" — the invariant is held by the schema, not by application discipline that a later
-- caller could forget. Individual payments are ordinary thread REPLIES under it (thread_root_id),
-- which is how the drill-down works with the threads MSG-01 already shipped.
--
-- The rollup body is RECOMPUTED from its replies on every payment (a fold over durable rows) rather
-- than incremented, so an at-least-once outbox redelivery re-derives the same "14 · $12,340" instead
-- of double-counting it. The reply's own event_key makes the redelivery a no-op first; the recompute
-- makes it harmless even if it were not.
--
-- The DAY is America/Toronto, the house zone (messagingprovider.DefaultZone) — not UTC. A UTC day
-- boundary would split the Ontario working day at 8pm EDT and file the evening's receipts under
-- tomorrow, which is wrong on the one surface whose entire job is "what came in today".
--
-- The lookup this index serves: "the rollup root for this channel + day", on every routine event.
CREATE INDEX idx_msg_messages_rollup ON msg_messages(channel_id, event_key)
  WHERE kind = 'EVENT' AND thread_root_id IS NULL AND event_key IS NOT NULL;

-- The aggregate the recompute folds: "every reply under this root".
CREATE INDEX idx_msg_messages_thread_root ON msg_messages(thread_root_id)
  WHERE thread_root_id IS NOT NULL AND deleted_at IS NULL;

-- GATED (do NOT build here): per-user rollup windows (weekly/hourly), rollups for classes other than
-- payments, and a digest e-mail of the day's rollup. All three are real requests and none is blocked
-- by this shape — the rollup writer is keyed on (channel, class, day) and takes another class the
-- moment one is ratified as ROUTINE.
