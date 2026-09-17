-- 0027: payment processor config (encrypted, write-only creds) + payments processor/token/card_type
--       columns + widen payments.status to RESERVE 'PENDING'.
-- Spec: RUN-PAY (W4) — the CARD rail. Pairs with 0010 payments (gateway_ref reserved there for this).
-- D5/PCI: only a TOKEN reaches the server; creds are AES-256-GCM at rest and never read back.
-- Owner module: internal/payment.

CREATE TABLE payment_config (
  id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  processor              TEXT NOT NULL DEFAULT 'run_payments',
  merchant_id            TEXT NOT NULL,
  currency               TEXT NOT NULL DEFAULT 'CAD' CHECK (currency = 'CAD'),  -- CAD only (no multi-currency)
  credentials_ciphertext BYTEA NOT NULL,                 -- AES-256-GCM (nonce||ciphertext); NEVER plaintext
  key_version            INTEGER NOT NULL DEFAULT 1,      -- which vault key sealed this blob
  base_url               TEXT NOT NULL DEFAULT '',        -- optional per-dealer Run host override (UAT/prod)
  mdr_bps                INTEGER NOT NULL DEFAULT 0 CHECK (mdr_bps BETWEEN 0 AND 10000),  -- merchant discount rate (surcharge calc input)
  surcharge_enabled      BOOLEAN NOT NULL DEFAULT FALSE,  -- RESERVED-OFF: surcharge-at-launch deferred to W16 (D-RUNPAY-2)
  status                 TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
  created_by             UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- At most one ACTIVE config per processor (the resolver picks the active row; reconfigure = new row + retire old).
CREATE UNIQUE INDEX ux_payment_config_active ON payment_config(processor) WHERE status = 'active';

-- payments gains the processor name + the client-side token. gateway_ref ALREADY exists (0010:35,
-- reserved "RUN gateway (W16) fills gateway_ref") and is the home for ProcessorRef.
ALTER TABLE payments ADD COLUMN processor       TEXT NOT NULL DEFAULT '';   -- e.g. 'run_payments'
ALTER TABLE payments ADD COLUMN payment_token   TEXT NOT NULL DEFAULT '';   -- client-tokenized instrument (NEVER a PAN)
ALTER TABLE payments ADD COLUMN surcharge_cents BIGINT NOT NULL DEFAULT 0 CHECK (surcharge_cents >= 0); -- RESERVED-0 until W16

-- W4 cheap-insurance add: capture the instrument class now. NULL default (unknown/cash/cheque rows stay NULL).
-- debit/prepaid are surcharge-EXEMPT in Canada -> capturing card_type at the source makes W16 surcharge a
-- flag-flip (not a migration) and feeds debit-vs-credit reporting. Advisory at the gate; no behaviour rides on it yet.
ALTER TABLE payments ADD COLUMN card_type TEXT CHECK (card_type IN ('DEBIT','CREDIT','PREPAID','UNKNOWN')); -- nullable, DEFAULT NULL

-- W4 cheap-insurance add: RESERVE 'PENDING' in the payments.status set (unused at the gate). It is the future
-- home for an offline-card "queued, not yet captured" tender — today card-offline BLOCKS (D-RUNPAY-3 / D-A19-3 /
-- D-A03-3), so flipping to queue-later needs NO schema change. The inline 0010 CHECK is auto-named
-- payments_status_check; drop + re-add it widened (additive — every existing value still validates).
ALTER TABLE payments DROP CONSTRAINT payments_status_check;
ALTER TABLE payments ADD CONSTRAINT payments_status_check
  CHECK (status IN ('RECEIVED','DEPOSITED','BOUNCED','REFUNDED','VOID','PENDING'));
