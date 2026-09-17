-- 0075: MSG-APPROVALS generic approvals rail v1 (DIB-216 / B1) — additive CHECK widenings ONLY.
-- Owner: internal/messaging + internal/identity. Feature: the generic approvals rail (rev B1.1).
--
-- Plain-language intent: the approvals rail is a VIEW over the tables that already exist — it adds NO
-- new approval table and NO GL DDL. Approval state stays in permission_approval_requests (0016/0020),
-- decided via the existing POST /api/v1/staff/approvals/{id}/decide. The hub renders APPROVAL-kind
-- msg_messages that carry a typed `ref` + `event_key` pointing back at the request. This migration only
-- widens three existing CHECKs so those new-but-additive values validate; every value that was legal
-- before is still legal, so it is backward-compatible with the live hub + the pending-approvals surface.
--
-- Rollback: purely additive (three CHECK widenings, no new tables) — widened CHECKs are harmless if the
-- features unwire (main.go nil-seams). No destructive rollback needed.
--
-- NOTE (config, not schema — commander ruling, DIB-216 decision #2): the JE maker-checker threshold
-- gl_policy.je_approval_threshold_cents shipped in 0032 seeded at $5,000 (500000 cents) and is BLESSED
-- AS-IS here — the JE trigger reuses that live gate, this migration does NOT touch it. It remains a
-- go-live-configurable POLICY ROW (per-org default, branch override), NOT a new table/endpoint; raising
-- it at go-live is an UPDATE gl_policy, not a code change. See internal/gl/journal.go thresholdFor().

-- ── 1. msg_messages.kind → admit 'APPROVAL' (system-authored, like EVENT) ─────────────────────────────
-- The rail's cards are APPROVAL-kind messages authored by the ERP itself (author_id NULL). Widen the kind
-- CHECK and add the matching system-authored guard (mirrors the 0050 msg_messages_event_author pattern:
-- an APPROVAL message must have NULL author, so a person can never masquerade as the system on a card).
ALTER TABLE msg_messages DROP CONSTRAINT msg_messages_kind_check;
ALTER TABLE msg_messages ADD CONSTRAINT msg_messages_kind_check
  CHECK (kind IN ('TEXT','EVENT','APPROVAL'));
ALTER TABLE msg_messages ADD CONSTRAINT msg_messages_approval_author
  CHECK (kind <> 'APPROVAL' OR author_id IS NULL);

-- ── 2. msg_channels.kind → admit 'APPROVAL' (per-person approval channels) ─────────────────────────────
-- Each approver gets a dedicated APPROVAL channel (slug approvals-<username>, sole member = the approver),
-- provisioned lazily + idempotently by EnsureApprovalChannel. Widen the channel-kind CHECK to admit it.
ALTER TABLE msg_channels DROP CONSTRAINT msg_channels_kind_check;
ALTER TABLE msg_channels ADD CONSTRAINT msg_channels_kind_check
  CHECK (kind IN ('BRANCH','TEAM','TOPIC','DM','APPROVAL'));

-- ── 3. permission_approval_requests.policy_type → admit 'JE_OVER_THRESHOLD' ─────────────────────────────
-- The JE-over-threshold trigger files a request against the EXISTING approval table with this new policy
-- type (subject_ref = the parked DRAFT journal id). No new columns: the JE amount rides in the existing
-- `data` JSONB (data->>'amount_cents'); status/decided_by/decided_at are the shipped 0016 columns.
ALTER TABLE permission_approval_requests DROP CONSTRAINT permission_approval_requests_policy_type_check;
ALTER TABLE permission_approval_requests ADD CONSTRAINT permission_approval_requests_policy_type_check
  CHECK (policy_type IN ('MARGIN_BELOW_MINIMUM','CREDIT_LIMIT_EXCEEDED','COD_CONSTRAINT','JE_OVER_THRESHOLD'));
