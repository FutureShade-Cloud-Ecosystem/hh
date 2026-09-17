-- 0162: DIB-491 — quote acceptance controls (D-097, Aug-18 sync).
--
-- THE RULING. Accepting a quote more than once is a DEALER CHOICE with a RESTRICTIVE default:
-- multi-accept ships OFF, and every accept/duplicate must carry provenance so staff can see where a
-- document came from. This migration is the floor under both halves.
--
-- ── the switch ────────────────────────────────────────────────────────────────────────────────────
-- One org-level boolean in system_settings (0014), the same table that answers default_branch_id and
-- org_name. It is seeded 'false' here rather than left absent so the restrictive default is VISIBLE
-- in the data, not merely implied by a fallback in Go — an operator running
-- `SELECT * FROM system_settings` should see the policy, not infer it. The code treats an absent key
-- as false anyway (acceptcontrols_repository.go), so a database that skips this seed still refuses.
--
-- The value is JSONB true/false, matching the table's existing scalar style ('"CAD"', 1300).
INSERT INTO system_settings (key, value) VALUES
  ('allow_quote_multi_accept', 'false')
ON CONFLICT (key) DO NOTHING;

-- ── the provenance ────────────────────────────────────────────────────────────────────────────────
-- One row per ORDER minted from a quote acceptance. orders.quote_id (0008) already links an order
-- back to its quote; what it cannot say is WHICH acceptance minted it, at WHICH door, by WHOM, and
-- whether it was the first acceptance or a repeat. Those four facts are the difference between a
-- lineage view and a join.
--
--   accept_seq   1 = the original acceptance's order; 2+ = repeat acceptances (multi-accept ON).
--                Computed under the quote's FOR UPDATE lock inside Convert's own transaction, so two
--                concurrent converts serialize and cannot claim one sequence number twice — the
--                UNIQUE below is the backstop, not the mechanism.
--   origin       which DOOR the conversion came through. PORTAL = the customer's own session;
--                COUNTER = a staff member at the counter (DIB-483's staff door).
--   actor_id     who: the staff user at the counter, or the customer id at the portal. No FK — this
--                column names a portal customer as readily as a staff user, and the row must not be
--                reshaped by either lifecycle (the audit_log.entity_id posture).
--
-- The document FKs, by contrast, ARE foreign keys with CASCADE: a provenance row describes a LIVE
-- pair of documents (the lineage read joins through them), and an orphan row about a deleted order
-- is noise, not history — quote_acceptances (0086) is the immutable record of the acceptance itself
-- and is deliberately not touched here.
CREATE TABLE quote_accept_provenance (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  quote_id   UUID NOT NULL REFERENCES quotes(id) ON DELETE CASCADE,
  order_id   UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  quote_no   CITEXT NOT NULL, -- snapshots: the row names its documents without a display-time join
  order_no   CITEXT NOT NULL,
  accept_seq INTEGER NOT NULL CHECK (accept_seq >= 1),
  origin     TEXT NOT NULL CHECK (origin IN ('PORTAL','COUNTER')),
  actor_id   UUID,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (order_id),             -- an order has exactly one birth
  UNIQUE (quote_id, accept_seq)  -- a quote's acceptances are numbered without gaps a race could leave
);

CREATE INDEX idx_qap_quote ON quote_accept_provenance (quote_id, accept_seq);

COMMENT ON TABLE quote_accept_provenance IS
  'DIB-491 (D-097): one row per order minted from a quote acceptance — which acceptance, which door, who. The related-documents lineage view reads it.';
