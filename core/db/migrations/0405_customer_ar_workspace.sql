-- 0405: customer account workspace notes. One durable seam for collections-side free text.
--
-- WHY THIS TABLE EXISTS. Change 8 S2 + S2M ships the customer account workspace, and the
-- activity + notes panel is one of its five tabs. Notes are how a collections person records
-- "promised by phone, see text", "owner travelling, ring after the 15th", "site closed on
-- Fridays, drop a paper copy", facts that have to outlive the conversation and that no
-- other AR table has a column for. The AR console does not have a place to put free text
-- today: invoice_lines.memo is per-line, customer_transactions.memo is a one-off at posting
-- time, ar_ap_contacts is contact data, and ar_paper_path_flags is a queue state. A row
-- here is the smallest thing that lets the next caller pick up where the last one left off.
--
-- WHAT THIS TABLE DOES NOT DO.
--   * It is NOT a messaging table. Reminders and statements go through the house messaging
--     sink and never reach a customer address book from here; this table holds notes we
--     leave for ourselves, never outbound copy.
--   * It is NOT an audit table. created_by is the only author stamp; if a forensic trail is
--     ever needed it belongs in the audit log, not on a note a clerk wrote for the next
--     clerk.
--   * It is NOT a payment, promise, dispute, or hold. Notes never touch balances, due dates,
--     aging buckets or hold state. A note that quietly un-aged an invoice would let a
--     customer postpone a credit hold with a phone call; the discipline ar_promises_to_pay
--     established holds here too.
--
-- ONE ACCOUNT, MANY NOTES. Customers are people over years, and a single account can carry
-- a long string of notes, "all branches" reads, all actors. No uniqueness on (customer, body):
-- the same operator may legitimately write the same sentence about two different invoices.
--
-- PINNED OR UNPINNED. A pinned note is the one the workspace shows first on the activity
-- tab and the one the right-hand summary card carries when open; unpinning is the way an
-- operator dismisses the alert. Pinned is a column rather than a separate table because the
-- only consumer is one read, and a partial index is the right shape for that read.
--
-- KIND. A note may be freeform (the default), or carry one of three types so the workspace
-- can colour-code activity: CALL (a phone conversation), VISIT (a yard visit), EMAIL_RECEIVED
-- (a note about an email we received). The CHECK keeps the vocabulary bounded so a stray
-- value cannot render as a blank chip.
--
-- Additive: one table, two indexes, no backfill (no rows in the wild). Dropping it loses
-- free-text notes and nothing else.

CREATE TABLE IF NOT EXISTS customer_ar_notes (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),

  customer_id UUID NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
  author_id   UUID REFERENCES users(id) ON DELETE SET NULL,

  -- The free text itself. Bounded to 4 KiB so a paste of a long internal email does not
  -- turn one note into a row that bloats every read of the timeline.
  body        TEXT NOT NULL CHECK (char_length(body) BETWEEN 1 AND 4096),

  kind        TEXT NOT NULL DEFAULT 'NOTE'
              CHECK (kind IN ('NOTE','CALL','VISIT','EMAIL_RECEIVED')),

  -- True means the workspace activity tab renders this row first and the summary card
  -- carries a one-line summary while open; only one note per account is pinned, enforced by
  -- the partial unique index rather than by a read-then-write.
  pinned      BOOLEAN NOT NULL DEFAULT FALSE,

  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_customer_ar_notes_customer_recent
  ON customer_ar_notes(customer_id, created_at DESC);

CREATE UNIQUE INDEX uq_customer_ar_notes_one_pinned_per_account
  ON customer_ar_notes(customer_id) WHERE pinned;

COMMENT ON TABLE customer_ar_notes IS
  'Free-text notes a collections person leaves for the next collections person on the same account. Outbound copy never lives here. A pinned note is the one the workspace surfaces first; only one is allowed per account by the partial unique index. Additive; no backfill.';
COMMENT ON COLUMN customer_ar_notes.body IS
  'The free text. Bounded so a paste of a long email cannot bloat every timeline read.';
COMMENT ON COLUMN customer_ar_notes.kind IS
  'NOTE (default free text), CALL, VISIT, or EMAIL_RECEIVED. The CHECK keeps the colour vocabulary bounded.';
COMMENT ON COLUMN customer_ar_notes.pinned IS
  'True when this note is the one the workspace surfaces first. The partial unique index holds it to one per account.';