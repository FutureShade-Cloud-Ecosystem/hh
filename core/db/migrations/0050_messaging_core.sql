-- 0050: MSG-01 Internal Messaging Hub — P1 core schema (channels / members / messages / attachments).
-- Owner: internal/messaging. Feature: MSG-01 / DIB-152. PRD §5 data-model sketch (G-LOCK-076).
-- Plain-language intent: one internal place where the ERP talks to the team (system EVENT messages)
-- and the team talks back (TEXT messages, threads, DMs). This is the P1 slice: channels/DMs/threads,
-- system event messages, unread badges. Approvals-in-chat / slash-commands / assignment routing /
-- AI (FR-4/5/6/11/12) are OUT of P1 — the schema leaves clean seams (kind enums, ref JSONB) but P1
-- writes only TEXT + EVENT.
--
-- Conventions (portal/CLAUDE.md): UUID v4 PKs (gen_random_uuid()); TIMESTAMPTZ; JSONB for ref;
-- identity is the SoT — channels reference users(id) and locations(id) (the branch roots), NEVER a
-- private copy. Auto-membership (FR-1) is DERIVED from the role/branch model, materialized into
-- msg_members rows by the service at resolve time (see internal/messaging).

-- ── Channels ──────────────────────────────────────────────────────────────────
-- A conversation surface. kind partitions the taxonomy (FR-1):
--   BRANCH — one per yard (all staff granted that branch)         e.g. #yard-trenton
--   TEAM   — a functional team channel (office/sales/drivers)     e.g. #office
--   TOPIC  — a subject channel (manual join/leave, deferred admin) e.g. #feedback
--   DM     — a 1:1 (or small-group) direct message (no branch)
-- branch_id is set for BRANCH channels (and optionally scopes a TEAM channel to one yard); it is a
-- locations(id) BRANCH root, matching user_branch_grants. auto_role/auto_scope drive FR-1 derived
-- membership: a channel with auto_role='*' + auto_scope='BRANCH' auto-includes every user granted
-- branch_id; auto_role='<ROLE_CODE>' narrows it to that role. DM channels carry no auto rule
-- (membership is explicit).
CREATE TABLE msg_channels (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  kind        TEXT NOT NULL CHECK (kind IN ('BRANCH','TEAM','TOPIC','DM')),
  branch_id   UUID REFERENCES locations(id) ON DELETE CASCADE,
  slug        CITEXT UNIQUE,               -- stable handle for BRANCH/TEAM/TOPIC (e.g. 'yard-trenton'); NULL for DMs
  name        TEXT NOT NULL,
  topic       TEXT NOT NULL DEFAULT '',    -- human description shown in the header
  -- FR-1 derived-membership rule (NULL for DM/manual TOPIC channels):
  --   auto_scope: 'GLOBAL' (all active staff) | 'BRANCH' (users granted branch_id)
  --   auto_role : '*' (any role) | a role code from roles.code (narrows to that role)
  auto_scope  TEXT CHECK (auto_scope IN ('GLOBAL','BRANCH')),
  auto_role   CITEXT,
  is_archived BOOLEAN NOT NULL DEFAULT FALSE,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- A BRANCH channel must name its branch; a derived channel must name a scope.
  CONSTRAINT msg_channels_branch_kind CHECK (kind <> 'BRANCH' OR branch_id IS NOT NULL),
  CONSTRAINT msg_channels_auto_scope_branch CHECK (auto_scope IS DISTINCT FROM 'BRANCH' OR branch_id IS NOT NULL)
);
CREATE INDEX idx_msg_channels_kind ON msg_channels(kind);
CREATE INDEX idx_msg_channels_branch ON msg_channels(branch_id);
-- Derived-membership channels are scanned on every membership resolve; index the rule columns.
CREATE INDEX idx_msg_channels_auto ON msg_channels(auto_scope, auto_role) WHERE auto_scope IS NOT NULL;

-- ── Members ───────────────────────────────────────────────────────────────────
-- A user's participation in a channel + their read cursor. last_read_at drives unread counts
-- (FR-7): unread = messages in the channel newer than last_read_at (authored by someone else).
-- source distinguishes DERIVED rows (materialized from the FR-1 role/branch rule; recomputed and
-- self-healing) from EXPLICIT rows (a DM participant, or a manual TOPIC join) that must never be
-- garbage-collected by the derivation pass.
CREATE TABLE msg_members (
  channel_id   UUID NOT NULL REFERENCES msg_channels(id) ON DELETE CASCADE,
  user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role         TEXT NOT NULL DEFAULT 'MEMBER' CHECK (role IN ('MEMBER','OWNER')),
  source       TEXT NOT NULL DEFAULT 'EXPLICIT' CHECK (source IN ('DERIVED','EXPLICIT')),
  last_read_at TIMESTAMPTZ NOT NULL DEFAULT to_timestamp(0),   -- epoch = "never read" (everything unread)
  muted        BOOLEAN NOT NULL DEFAULT FALSE,
  joined_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (channel_id, user_id)
);
CREATE INDEX idx_msg_members_user ON msg_members(user_id);

-- ── Messages ──────────────────────────────────────────────────────────────────
-- One post. author_id NULL => a SYSTEM message (kind EVENT), authored by the ERP itself (FR-3).
-- kind: TEXT (a person) | EVENT (a mapped domain event). The APPROVAL/ASSIGNMENT/REPORT kinds from
-- the PRD are P2/P3 — the CHECK admits only the two P1 kinds so an out-of-scope kind cannot be
-- written by mistake; widen the CHECK when those phases land.
-- ref (JSONB): {topic, entity, deep_link} for EVENT messages — the deep link back into the ERP.
-- thread_root_id: NULL for a top-level message; set to the root message id for a threaded reply
-- (one-level threads in P1, matching Slack). edited_at / deleted_at implement edit-with-history-lite
-- + soft-delete (audit doctrine: a deleted message is tombstoned, never physically removed in P1).
CREATE TABLE msg_messages (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  channel_id     UUID NOT NULL REFERENCES msg_channels(id) ON DELETE CASCADE,
  author_id      UUID REFERENCES users(id) ON DELETE SET NULL,   -- NULL = system
  kind           TEXT NOT NULL DEFAULT 'TEXT' CHECK (kind IN ('TEXT','EVENT')),
  body           TEXT NOT NULL DEFAULT '',
  ref            JSONB NOT NULL DEFAULT '{}',                     -- {topic, entity, deep_link} for EVENT
  thread_root_id UUID REFERENCES msg_messages(id) ON DELETE CASCADE,
  -- event_key deduplicates FR-3 event→message mapping: one domain event yields at most one message
  -- per channel even if the outbox row is redelivered (at-least-once delivery). NULL for TEXT.
  event_key      TEXT,
  edited_at      TIMESTAMPTZ,
  deleted_at     TIMESTAMPTZ,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT msg_messages_event_author CHECK (kind <> 'EVENT' OR author_id IS NULL),
  CONSTRAINT msg_messages_text_author  CHECK (kind <> 'TEXT'  OR author_id IS NOT NULL)
);
-- Primary read path: a channel's messages newest-first, cursor-paginated by (created_at, id).
CREATE INDEX idx_msg_messages_channel_created ON msg_messages(channel_id, created_at DESC, id DESC);
-- Thread fetch: a root's replies oldest-first.
CREATE INDEX idx_msg_messages_thread ON msg_messages(thread_root_id, created_at) WHERE thread_root_id IS NOT NULL;
-- FR-3 idempotency: an event's message is unique within its channel.
CREATE UNIQUE INDEX uq_msg_messages_event_key ON msg_messages(channel_id, event_key) WHERE event_key IS NOT NULL;

-- ── Attachments ───────────────────────────────────────────────────────────────
-- Schema only in P1 (file storage deferred — FR-2 reuses the evidence-storage pattern later). No
-- endpoint writes these yet; the table exists so the message read model is stable when files land.
CREATE TABLE msg_attachments (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  message_id   UUID NOT NULL REFERENCES msg_messages(id) ON DELETE CASCADE,
  filename     TEXT NOT NULL,
  content_type TEXT NOT NULL DEFAULT 'application/octet-stream',
  size_bytes   BIGINT NOT NULL DEFAULT 0,
  storage_key  TEXT NOT NULL DEFAULT '',   -- object-store key (populated when file storage lands)
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_msg_attachments_message ON msg_attachments(message_id);
