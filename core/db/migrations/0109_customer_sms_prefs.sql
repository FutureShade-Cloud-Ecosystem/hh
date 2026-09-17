-- 0109: SMS consent, quiet hours, and a send-window column on the notice queue.
--       Owner: internal/messagingprovider (Wave 3, Swarm D). Finishes the substrate 0097 started.
--
-- Plain-language intent: Wave 2 built a queue that can carry a text message and a resolver seam that
-- deliberately resolved NOBODY, because nothing in this program knew which customers had agreed to be
-- texted. This migration is that missing fact. It records, per customer: the one phone number that has
-- been verified as reachable by SMS, whether they have opted out, and the hours of day they have asked
-- not to be disturbed.
--
-- ─────────────────────────────────────────────────────────────────────────────────────────────────
-- CONSENT IS OPT-IN, AND ABSENCE OF A ROW MEANS "NO".
-- There is no default that texts somebody. A customer with no row here is not a customer we may text;
-- the resolver returns ErrNoRecipient and the notice is skipped, exactly as it did with the empty
-- fixture. This is the difference between a CRM that has a phone number on file and a business that
-- has permission to use it, and it is not a distinction to leave to a nullable boolean: a
-- customer_contacts.phone is how we CALL somebody, and calling is not texting. Ontario's CASL and the
-- CTIA's messaging principles both start from express consent for commercial messages; a table where
-- the absent state is silence is the only shape that can honour that.
--
-- WHY NOT A COLUMN ON customers OR customer_contacts.
-- Those tables are owned by internal/customer (migration 0006) and read by half this program. Adding
-- a messaging concern to them would make every customer writer a party to consent bookkeeping and put
-- an opt-out flag inside a row that a routine "update the billing address" write rewrites. Consent has
-- its own lifecycle (granted, revoked, re-granted, never touched by a name change), so it gets its own
-- row, keyed 1:1 to the customer and cascading with them.
-- ─────────────────────────────────────────────────────────────────────────────────────────────────
--
-- NO CARRIER CREDENTIALS AND NO PROVIDER STATE LIVE HERE. This table holds a customer's own number and
-- their own stated preferences, nothing else.
--
-- Rollback: additive only — one new table, one nullable column and one replaced partial index on
-- sms_outbox. Dropping the table degrades the resolver to "resolves nobody" (the Wave 2 default);
-- dropping not_before degrades scheduled notices to "send on the next drain".

-- ── Consent + quiet hours ───────────────────────────────────────────────────────
CREATE TABLE customer_sms_prefs (
  customer_id     UUID PRIMARY KEY REFERENCES customers(id) ON DELETE CASCADE,

  -- The number we may text, in E.164. It is stored SEPARATELY from customer_contacts.phone on purpose:
  -- that column is free text ('613-555-0102', '(613) 555-0102 x4'), it is a landline as often as a
  -- mobile, and nobody confirmed it can receive a text. This column is only ever written with a number
  -- that has been normalised and confirmed, which is why it is paired with sms_verified below.
  phone_e164      TEXT NOT NULL DEFAULT '',

  -- TRUE only once we have positive evidence the number is a mobile that accepts our messages. FALSE
  -- with a populated phone_e164 is a legitimate, common state: "we have a number, we have not
  -- confirmed it". The resolver treats it as no recipient.
  sms_verified    BOOLEAN NOT NULL DEFAULT FALSE,
  verified_at     TIMESTAMPTZ,

  -- The kill switch. Once TRUE, no automation may text this customer again for any reason — AR
  -- collections, quote delivery, follow-up cadence, fulfilment. There is no "important enough to
  -- override" flag, deliberately: the moment one exists, everything becomes important.
  opted_out       BOOLEAN NOT NULL DEFAULT FALSE,
  opted_out_at    TIMESTAMPTZ,
  opt_out_reason  TEXT NOT NULL DEFAULT '',   -- 'STOP reply' | 'phoned in' | 'staff request' | …

  -- Quiet hours, as minutes past local midnight in the customer's own zone. Defaults are the province's
  -- ordinary decency hours and match the global send window the consumer enforces (08:00–20:00). A
  -- customer who works nights can widen or narrow them without a code change.
  quiet_start_min INT NOT NULL DEFAULT 480  CHECK (quiet_start_min BETWEEN 0 AND 1439), -- 08:00
  quiet_end_min   INT NOT NULL DEFAULT 1200 CHECK (quiet_end_min   BETWEEN 0 AND 1439), -- 20:00
  -- An IANA zone name, not an offset: an offset cannot follow a DST change and would silently text
  -- somebody at 07:00 for half the year. Unparseable names fall back to the program default rather
  -- than sending — see messagingprovider.QuietHours.
  time_zone       TEXT NOT NULL DEFAULT 'America/Toronto',

  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

  -- A verified row must carry a number. This stops the one combination that would read as consent and
  -- resolve to nothing.
  CONSTRAINT customer_sms_prefs_verified_needs_phone
    CHECK (NOT sms_verified OR phone_e164 <> '')
);

-- The resolver's only read: "may we text this customer, and at what number". The partial index covers
-- exactly the sendable population, which is a small fraction of customers.
CREATE INDEX idx_customer_sms_prefs_sendable
  ON customer_sms_prefs (customer_id) WHERE sms_verified AND NOT opted_out;

-- ── A send-window column on the notice queue ────────────────────────────────────
-- not_before is "do not claim this row before this instant". It exists because three of the notices
-- this program now produces are SCHEDULED rather than immediate — a quote follow-up due in three days,
-- an AR notice held until the customer's quiet hours end — and the alternative shapes are both worse:
-- a producer that sleeps until the send time loses the notice on restart, and a consumer that claims a
-- row and puts it back burns dead-letter attempts on a row that has not failed at anything.
--
-- NULL means "as soon as possible", which is every row 0097 ever wrote, so this is backward compatible
-- with no backfill.
ALTER TABLE sms_outbox ADD COLUMN not_before TIMESTAMPTZ;

-- The dispatcher's hot read changes from "oldest pending" to "oldest pending that is due". The old
-- index cannot serve it (it does not carry not_before), so it is replaced rather than supplemented —
-- two overlapping partial indexes on the same predicate is write amplification for no read benefit.
DROP INDEX IF EXISTS idx_sms_outbox_pending;
CREATE INDEX idx_sms_outbox_due ON sms_outbox (occurred_at) WHERE status = 'PENDING';
CREATE INDEX idx_sms_outbox_not_before ON sms_outbox (not_before) WHERE status = 'PENDING';

-- ── Demo consent for the walkthrough ────────────────────────────────────────────
-- Three seeded demo customers are given verified consent so the Chromium walkthrough and the
-- integration suite have somebody to (not) text. The numbers are 555-line fictional numbers — the
-- North American reserved range for exactly this purpose — normalised to E.164 from what the demo
-- seed already put on customer_contacts. No real person is reachable at any of them.
--
-- Guarded by a SELECT over customers so this migration is safe on a database that was never seeded:
-- an empty source set inserts nothing rather than failing on a missing FK.
INSERT INTO customer_sms_prefs (customer_id, phone_e164, sms_verified, verified_at, opted_out)
SELECT c.id,
       '+1613555' || substr(c.account_no, 6, 4),  -- CUST-1001 → +16135550101-shaped fictional number
       TRUE, now(), FALSE
FROM customers c
WHERE c.account_no IN ('CUST-1001', 'CUST-1002', 'CUST-1003')
ON CONFLICT (customer_id) DO NOTHING;
