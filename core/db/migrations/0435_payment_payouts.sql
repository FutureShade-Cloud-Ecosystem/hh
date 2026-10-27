-- 0435: money a reversal holds in 2310 leaves it by the act that moves the money (SF-2m, SF-2n; ruling 52.3).
--
-- Every payment.reversed posts Dr AR / Cr 2310 (0426) and the money is held for the customer. SF-2l (0431) clears it by
-- applying it to an invoice. This migration is the other acts:
--   DRAWER  cash handed back from the till, Dr 2310 / Cr 1020, with a PAID_OUT till movement written by the till;
--   BANK    a cheque or transfer written to the customer, Dr 2310 / Cr 1000, with its reference;
--   WRITE_OFF (SF-2n) the cash left the drawer unrecorded and the count has posted the shortage, Dr 2310 / Cr 6350;
-- and the card and console kinds (CARD_VOID, CARD_REFUND, CONSOLE_*), which credit 1010 card_clearing (ruling 74.2).
--
-- 1. THE HELD FIGURE NOW SUBTRACTS TWO TABLES: a reversed payment's amount less payment_reapplications (0431) and less
--    payment_payouts (this table). One guard function takes the payment row FOR UPDATE, re-sums both tables and refuses
--    an insert into either that would take more than the payment holds, so a pay out and a re-application racing on one
--    payment cannot together exceed it even from a call site that forgot the service's own check. 0431's trigger
--    function is replaced to call it.
-- 2. LOCK ORDER (payments/LOCK-ORDER.md): the guard takes the payment row at the insert, so a writer must already hold the
--    payment row before it locks an invoice or a till session row; every writer of either table takes the payment row first.
-- 3. ONE ROW IS ONE ACT, keyed by the caller's idempotency key (unique), booked by one payment.paid_out outbox row whose
--    journal key is its id.

CREATE TABLE payment_payouts (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  payment_id      UUID NOT NULL REFERENCES payments(id) ON DELETE RESTRICT,
  customer_id     UUID NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
  branch_id       UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  kind            TEXT NOT NULL CHECK (kind IN ('DRAWER','BANK','WRITE_OFF','CARD_VOID','CARD_REFUND','CONSOLE_VOID','CONSOLE_REFUND','CONSOLE_CHARGEBACK')),
  amount_cents    BIGINT NOT NULL CHECK (amount_cents > 0),
  reason          TEXT NOT NULL CHECK (btrim(reason) <> ''),
  reference       TEXT NOT NULL DEFAULT '',
  till_session_id UUID REFERENCES till_sessions(id) ON DELETE RESTRICT,
  applied_by      UUID REFERENCES users(id) ON DELETE SET NULL,
  idempotency_key TEXT NOT NULL CHECK (idempotency_key <> '') UNIQUE,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_payment_payouts_payment ON payment_payouts(payment_id);
CREATE INDEX idx_payment_payouts_session ON payment_payouts(till_session_id) WHERE till_session_id IS NOT NULL;

CREATE FUNCTION payment_held_guard(p_payment UUID, p_new BIGINT) RETURNS void LANGUAGE plpgsql AS $$
DECLARE
  v_amount   BIGINT;
  v_reversed TIMESTAMPTZ;
  v_taken    BIGINT;
BEGIN
  SELECT amount_cents, reversed_at INTO v_amount, v_reversed FROM payments WHERE id = p_payment FOR UPDATE;
  IF v_reversed IS NULL THEN
    RAISE EXCEPTION 'payment %: it has not been reversed, no money is held for it', p_payment USING ERRCODE = 'check_violation';
  END IF;
  SELECT COALESCE((SELECT SUM(amount_cents) FROM payment_reapplications WHERE payment_id = p_payment), 0)
       + COALESCE((SELECT SUM(amount_cents) FROM payment_payouts WHERE payment_id = p_payment), 0) INTO v_taken;
  IF v_taken + p_new > v_amount THEN
    RAISE EXCEPTION 'payment % holds % cents and % are already out, % more would exceed it', p_payment, v_amount, v_taken, p_new
      USING ERRCODE = 'check_violation';
  END IF;
END
$$;

CREATE OR REPLACE FUNCTION payment_reapplications_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  PERFORM payment_held_guard(NEW.payment_id, NEW.amount_cents);
  RETURN NEW;
END
$$;

CREATE FUNCTION payment_payouts_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  PERFORM payment_held_guard(NEW.payment_id, NEW.amount_cents);
  RETURN NEW;
END
$$;

CREATE TRIGGER payment_payouts_guard BEFORE INSERT ON payment_payouts
  FOR EACH ROW EXECUTE FUNCTION payment_payouts_guard();

-- The posting rules for payment.paid_out, the default branch-NULL rows: Dr refund_payable (2310) against the account the
-- act moved. gl.ValidatePostingRules requires every role at boot.
INSERT INTO posting_rules (event_type, line_role, account_code) VALUES
    ('payment.paid_out', 'refund_payable', '2310'),
    ('payment.paid_out', 'drawer',         '1020'),
    ('payment.paid_out', 'bank',           '1000'),
    ('payment.paid_out', 'card_clearing',  '1010'),
    ('payment.paid_out', 'write_off',      '6350')
ON CONFLICT (event_type, line_role) WHERE branch_id IS NULL DO NOTHING;
