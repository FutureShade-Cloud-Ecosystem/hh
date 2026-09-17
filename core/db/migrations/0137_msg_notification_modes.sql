-- 0137: DIB-268 (per-channel notification preferences, respected by the badge pipeline) +
--       DIB-269 (the escalation tier that CANNOT be muted).
-- Owner: internal/messaging. Wave 5 / Swarm D. Additive, non-breaking, follows 0069.
--
-- Plain-language intent: 0069 gave each user a per-channel DESKTOP notification pref (desktop on/off,
-- muted, escalation-override) but that pref only ever gated a desktop toast — the UNREAD BADGE ignored
-- it entirely, so muting a channel still lit the sidebar. DIB-268 makes the pref a real three-way
-- MODE (all / mention-only / muted) and makes the BADGE PIPELINE honour it. DIB-269 then carves out a
-- class of channel that a mute cannot reach at all.

-- ── DIB-268: the three-way mode ─────────────────────────────────────────────────
-- ALL      — every message counts toward the badge (the default; matches pre-0137 behaviour).
-- MENTIONS — only messages that mention the user count (the "quiet but reachable" setting).
-- MUTED    — nothing counts; no badge, no desktop toast.
--
-- The legacy 0069 `muted` boolean is KEPT and kept IN SYNC (muted = (mode = 'MUTED')) rather than
-- dropped: shouldNotify (service.go) and its shipped FIREHOSE-override matrix read it, and a W2/W3
-- consumer may too. mode is the richer field; muted is its boolean shadow. The service writes both in
-- one upsert so they can never drift.
ALTER TABLE msg_notification_prefs
  ADD COLUMN mode TEXT NOT NULL DEFAULT 'ALL' CHECK (mode IN ('ALL','MENTIONS','MUTED'));

-- Backfill: an existing muted row becomes MUTED so no user silently gets un-muted by this migration.
UPDATE msg_notification_prefs SET mode = 'MUTED' WHERE muted;

-- ── DIB-269: the un-mutable escalation channel ─────────────────────────────────
-- #escalations is an ESCALATION-tier channel: the events that must never be missed land here, and a
-- preference write that would silence it is REFUSED at the service boundary (ErrChannelUnmutable →
-- HTTP 409; see internal/messaging/service.go SetNotifyPrefs and the named test
-- TestSetNotifyPrefs_EscalationChannelCannotBeMuted).
--
-- Why a refusal and not a silent override: a user who believes they muted a channel and still gets
-- paged has been lied to by the UI. Refusing the write tells them the truth — this channel is not
-- mutable — which is the only honest option for a tier whose whole purpose is that it always arrives.
--
-- It is GLOBAL/'*' (every active staffer, exactly like #alerts-all in 0069) and it is FED from the
-- priority tier of DIB-217: every forced-ack (requires_ack) EVENT is mirrored here in addition to its
-- home channel (internal/messaging/consumer.go: escalationSlug fan-out).
INSERT INTO msg_channels (id, kind, branch_id, slug, name, topic, auto_scope, auto_role, tier) VALUES
  ('a5000000-0000-4000-9000-000000000269','TEAM',NULL,
     'escalations','#escalations',
     'Escalations — events that must never be missed. This channel cannot be muted.',
     'GLOBAL','*','ESCALATION')
ON CONFLICT (id) DO NOTHING;

-- ── The prefs-aware badge pipeline ─────────────────────────────────────────────
-- msg_unread_summary (0051) is LEFT AS-IS — it is the raw, pref-blind unread truth and other readers
-- may depend on it. msg_unread_effective is the DIB-268 read model layered over the same shape: same
-- columns, plus the applied mode/tier, with the counts already adjusted. The badge endpoint
-- (UnreadSummary) and the channel-list projection (ListChannelsForUser) both read THIS view, which is
-- what makes "mute a channel → badge stops" true on both surfaces at once.
--
-- Mention detection: the 0051 view hard-coded mentioned = FALSE (mentions were never implemented).
-- Here a mention is the user's e-mail LOCAL PART preceded by '@' appearing in the body — '@alex.morgan'
-- mentions alex.morgan@dibbits.example. strpos() on lowercased text is used rather than ILIKE so a
-- handle containing a LIKE metacharacter ('_' or '%') cannot turn into a wildcard match.
--
-- DIB-269 defence-in-depth: an ESCALATION-tier channel's mode is forced to 'ALL' HERE as well as
-- refused at the write boundary. Two independent guards, so a legacy row, a direct-SQL write, or a
-- future endpoint that forgets the check still cannot silence an escalation badge.
CREATE VIEW msg_unread_effective AS
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
         CASE WHEN ch.tier = 'ESCALATION' THEN 'ALL' ELSE COALESCE(p.mode, 'ALL') END AS mode,
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
  GROUP BY mem.user_id, mem.channel_id, ch.tier, p.mode, u.email
) b;

-- GATED (do NOT build here): @here / @channel broadcast mentions and mention-by-display-name. Both
-- need a mention-parsing decision (and, for @here, a rate-limit policy) that has not been made; the
-- e-mail-handle rule above is the settled v1 and neither is blocked by this view.
