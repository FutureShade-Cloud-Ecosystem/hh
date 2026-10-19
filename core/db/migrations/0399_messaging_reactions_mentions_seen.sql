-- 0399: message reactions, stored mentions and the seen-by read (walk change 7, slice S3).
-- Owner: internal/messaging.
--
-- The change 7 rulings this slice carries (FROM-PRINCIPAL, row 2139, kept in one
-- place each):
--   Ruling 5: the mentions view lists messages naming the caller or the caller's
--   role. Until now a mention existed only as a read-time string heuristic (the
--   0152 badge view's strpos on '@' + the reader's e-mail local part); nothing
--   was stored, no role could be named, and the 0051 summary view's mentioned
--   column was its own recorded placeholder (FALSE).
--   Ruling 6: reactions are none on EVENT, approval or other system messages.
--   A system message is every row whose kind is not TEXT (EVENT, APPROVAL), and
--   the fence below holds at BOTH the service boundary and the reaction write
--   itself, so no caller of the repository can seat a reaction on one.
--
-- Seen-by needs no new table: a member's read cursor (msg_members.last_read_at,
-- 0050) is the fact "this member has read everything in the room up to here",
-- so the seen-by read answers from the cursors that already exist. The board's
-- "Seen 3" on a message is the count of the room's members whose cursor stands
-- at or past the message's created_at, the author excepted (nobody is seen by
-- themselves).
--
-- Forward only, additive only: two new tables, no column dropped, both badge
-- views replaced in place (CREATE OR REPLACE keeps every column name, type and
-- order, so no reader changes shape).

-- ── Reactions ─────────────────────────────────────────────────────────────────
-- One row per (message, person, emoji). The primary key makes a reaction
-- idempotent (a replayed PUT lands once) and its delete exact. ON DELETE CASCADE
-- follows the message row the way msg_acks does not (an ack is a fact that
-- outlives the message; a reaction is decoration on it and goes with it).
CREATE TABLE msg_reactions (
  message_id UUID NOT NULL REFERENCES msg_messages(id) ON DELETE CASCADE,
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  emoji      TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (message_id, user_id, emoji)
);
CREATE INDEX idx_msg_reactions_message ON msg_reactions(message_id);

-- ── Mentions ──────────────────────────────────────────────────────────────────
-- The parsed mention targets of one person's TEXT message, written when the
-- message is written (the parse lives in the TEXT insert paths, REST and sync
-- alike). One target per row: a person (user_id, role_code NULL) or a role
-- (role_code, user_id NULL); the CHECK admits exactly one of the two. role_code
-- is CITEXT so a stored code compares to roles.code case-insensitively, the way
-- the derived-membership rule compares auto_role (0050); the parser stores the
-- canonical code it read from the roles table, and the fold keeps a stored row
-- comparable whatever case a future writer uses.
--
-- uq_msg_mentions_target is the idempotency fence for the insert (NULLS NOT
-- DISTINCT so the two NULL shapes cannot collide): re-parsing the same body
-- into the same message writes nothing twice.
CREATE TABLE msg_mentions (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  message_id UUID NOT NULL REFERENCES msg_messages(id) ON DELETE CASCADE,
  user_id    UUID REFERENCES users(id) ON DELETE CASCADE,
  role_code  CITEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT msg_mentions_one_target CHECK (num_nonnulls(user_id, role_code) = 1)
);
CREATE UNIQUE INDEX uq_msg_mentions_target ON msg_mentions (message_id, user_id, role_code) NULLS NOT DISTINCT;
CREATE INDEX idx_msg_mentions_user ON msg_mentions(user_id);
CREATE INDEX idx_msg_mentions_role ON msg_mentions(role_code);

-- ── The badge views, mentioned made real ─────────────────────────────────────
-- msg_unread_summary (0051) keeps its five columns and its unread math word for
-- word; the mentioned placeholder becomes: the room holds at least one unread
-- message (the same predicate the LEFT JOIN applies) that names this member, by
-- person or by one of the member's own role codes (ruling 5). The author's own
-- message never mentions the author, the same exclusion the unread count applies.
CREATE OR REPLACE VIEW msg_unread_summary AS
SELECT
  mem.user_id                                        AS user_id,
  mem.channel_id                                     AS channel_id,
  COUNT(msg.id)                                      AS unread_count,
  MAX(msg.created_at)                                AS latest_at,
  COALESCE(EXISTS (
    SELECT 1
    FROM msg_messages mm
    WHERE mm.channel_id = mem.channel_id
      AND mm.created_at > mem.last_read_at
      AND mm.deleted_at IS NULL
      AND (mm.author_id IS NULL OR mm.author_id <> mem.user_id)
      AND EXISTS (
            SELECT 1 FROM msg_mentions men
            WHERE men.message_id = mm.id
              AND (men.user_id = mem.user_id
                OR EXISTS (SELECT 1 FROM user_roles ur JOIN roles ro ON ro.id = ur.role_id
                           WHERE ur.user_id = mem.user_id AND ro.code = men.role_code)))
  ), FALSE)                                          AS mentioned
FROM msg_members mem
LEFT JOIN msg_messages msg
  ON msg.channel_id = mem.channel_id
 AND msg.created_at > mem.last_read_at
 AND msg.deleted_at IS NULL
 AND (msg.author_id IS NULL OR msg.author_id <> mem.user_id)
GROUP BY mem.user_id, mem.channel_id;

-- msg_unread_effective (0137, restated 0152) keeps its mode arithmetic and its
-- escalation guard verbatim; only the mention_count predicate moves from the
-- e-mail strpos heuristic to the stored mentions (a person row for the reader,
-- or a role row for one of the reader's roles). The users join leaves with the
-- heuristic that needed it; the escalation tier still forces mode ALL before
-- anything is consulted, and DIB-269's second guard is untouched.
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
           WHERE EXISTS (
                 SELECT 1 FROM msg_mentions men
                 WHERE men.message_id = msg.id
                   AND (men.user_id = mem.user_id
                     OR EXISTS (SELECT 1 FROM user_roles ur JOIN roles ro ON ro.id = ur.role_id
                                WHERE ur.user_id = mem.user_id AND ro.code = men.role_code)))
         ) AS mention_count,
         max(msg.created_at) AS latest_at
  FROM msg_members mem
  JOIN msg_channels ch ON ch.id = mem.channel_id
  LEFT JOIN msg_notification_prefs p
         ON p.channel_id = mem.channel_id AND p.user_id = mem.user_id
  LEFT JOIN msg_messages msg
         ON msg.channel_id = mem.channel_id
        AND msg.created_at > mem.last_read_at
        AND msg.deleted_at IS NULL
        AND (msg.author_id IS NULL OR msg.author_id <> mem.user_id)
  GROUP BY mem.user_id, mem.channel_id, ch.tier, p.mode, ch.default_mode
) b;
