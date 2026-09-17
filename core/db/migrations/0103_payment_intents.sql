-- 0103: payment_intents — the durable PRE-CHARGE intent that makes the portal replay guard real
-- (DIB-415, Wave 3 Swarm B). Owner module: internal/payments.
--
-- WHY THIS EXISTS, IN PLAIN LANGUAGE. `payments.Service.Pay` documents an idempotent-replay guard:
-- charge, and if a payment already exists for the returned gateway ref, hand back the prior payment
-- instead of writing a second one. That guard never runs, and when it does run it cannot work on a
-- live processor. Two separate reasons, both of which this table closes:
--
--   1. The guard is keyed on the GATEWAY's ref, which we only learn AFTER the money has moved. By
--      then a double charge has already happened; all the guard can do is decline to write the
--      second settlement row. The customer is still out twice the money.
--   2. It only ever de-duplicates when the gateway returns a STABLE ref for a repeated idempotency
--      key. StubGateway does that by construction (its ref is a hash of the key), which is exactly
--      why the hole was invisible for two waves. Run exposes NO idempotency header on /charge — the
--      client's own documentation says so — so a retried live charge comes back with a NEW trans_id
--      and the guard misses completely.
--
-- The window is real and it is the ordinary one: a portal user taps Pay, the capture succeeds at the
-- processor, and the pod dies (or the connection drops, or the settle transaction deadlocks) before
-- the settle commits. The invoices are still FINALIZED with an open balance, so the pre-charge
-- payability check that currently absorbs replays waves the retry straight through to the gateway,
-- and the card is charged a second time.
--
-- ── THE FIX: WRITE DOWN THE INTENT BEFORE THE IRREVERSIBLE ACT ────────────────────────────────────
-- One row, committed BEFORE the gateway call, keyed on the SERVER-BOUND idempotency key (a sha256
-- over {customer_id, sorted invoice selection, funding source} — the client's key is advisory and
-- deliberately excluded so a client cannot force or collide a key across accounts). The unique index
-- on that key is the guard: the SECOND request to arrive cannot insert, so it never reaches the
-- gateway. It reads the existing row instead and answers from its status.
--
-- Status vocabulary, and what a retry does with each:
--   PENDING   — claimed, not yet dispatched. A concurrent retry loses the claim UPDATE and is refused.
--   CHARGING  — dispatched to the gateway; the outcome is unknown to us. A retry is REFUSED, loudly,
--               and never re-charges. This is the crash window, and refusing is the only safe answer:
--               we cannot tell "the processor never saw it" from "the processor took the money and
--               the ack was lost", and guessing wrong costs the customer real money. An operator
--               reconciles against the processor and moves the row on.
--   SETTLED   — the payment row exists. A retry returns THAT payment. This is the replay the service
--               comment always described; now it is keyed on something we own, before the fact.
--   FAILED    — the gateway declined or errored with no money moved. A retry may re-claim it.
--   ABANDONED — an operator has reconciled a stuck CHARGING row and released it.
--
-- gateway_ref is stamped as soon as the processor answers, so even the CHARGING → crash path leaves
-- the trans_id on disk to reconcile against. attempts + last_error make a stuck row diagnosable
-- without reading application logs.
--
-- WHAT THIS IS NOT. It is not a distributed transaction and it does not make the gateway call atomic
-- with the settle — nothing can. It converts an UNBOUNDED failure (silent double charge) into a
-- BOUNDED one (one refused retry that names itself and points at a reconcilable trans_id).
--
-- Conventions: UUID PK · money BIGINT *_cents · TEXT + CHECK for enums · TIMESTAMPTZ · explicit
-- ON DELETE. Rollback: purely additive (one new table). Dropping it re-opens DIB-415 and nothing else.

CREATE TABLE payment_intents (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  -- The server-bound idempotency key. UNIQUE is the whole guard: two identical requests produce one
  -- row, and the loser of the insert is the one that must not charge.
  idempotency_key TEXT NOT NULL UNIQUE,
  customer_id     UUID NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
  amount_cents    BIGINT NOT NULL CHECK (amount_cents > 0),
  -- NO currency column, deliberately. The whole-DB DX-19 invariant
  -- (internal/platform/db, TestInvariant_GLStaysCADOnUSDPO) allows a currency column on exactly three
  -- tables — payment_config, purchase_orders, landed_costs — because GL and payments are CAD-only and a
  -- currency column anywhere else is an invitation to start storing something that is not CAD in it.
  -- The first draft of this table carried `currency TEXT DEFAULT 'CAD' CHECK (currency = 'CAD')`, which
  -- was harmless-looking, never read by any Go code, and caught by that invariant on the first full
  -- parallel sweep. The settlement currency lives where it already lived: payments.chargeCurrency.
  -- The canonical (sorted, comma-joined) invoice selection this intent covers. RECORDED, not an FK
  -- array: the intent is a record of what was ATTEMPTED and must stay readable after an invoice is
  -- voided or merged away — the same posture print_queue.ref_id and audit_log.entity_id take.
  invoice_ids     TEXT NOT NULL DEFAULT '',
  status          TEXT NOT NULL DEFAULT 'PENDING'
                    CHECK (status IN ('PENDING','CHARGING','SETTLED','FAILED','ABANDONED')),
  gateway         TEXT NOT NULL DEFAULT '',   -- 'stub' | 'run' — which processor was dispatched
  gateway_ref     TEXT NOT NULL DEFAULT '',   -- the processor trans_id, stamped the moment it is known
  payment_id      UUID REFERENCES payments(id) ON DELETE SET NULL, -- set when the settle commits
  attempts        INT NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  last_error      TEXT NOT NULL DEFAULT '',   -- diagnostics only; NEVER credentials or a PAN
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- A SETTLED intent must name its payment. Without this a settle that half-committed could leave a
  -- SETTLED row with nothing to replay, and the next retry would have no answer to give.
  CONSTRAINT payment_intents_settled_has_payment CHECK (status <> 'SETTLED' OR payment_id IS NOT NULL)
);

-- The operator read: "what is stuck". A CHARGING row older than a few minutes is the reconcile queue.
CREATE INDEX idx_payment_intents_stuck ON payment_intents (status, updated_at) WHERE status = 'CHARGING';
-- The tenant read behind the portal's payment history / support lookups.
CREATE INDEX idx_payment_intents_customer ON payment_intents (customer_id, created_at DESC);
