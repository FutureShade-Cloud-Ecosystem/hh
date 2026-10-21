-- 0431: re-applying the money a reversal holds in 2310 to an invoice (SF-2l).
--
-- Every payment.reversed posts Dr AR 1100 / Cr 2310 "Customer Refunds and Unapplied Cash" (0426): the money stays with
-- the dealer, held for the customer. Re-application is the act that clears it by applying that money to an invoice of
-- the same customer: Dr 2310 / Cr AR, with the invoice balance and the AR subledger moving through the same settle
-- slice the payment settle runs (payments/repository.go).
--
-- 1. THE HELD FIGURE LIVES HERE. A reversed payment's held money is its amount less the sum of its rows in this table.
--    There is no column on payments to keep in step (the payments row is a sync feed row, and a stamp on it would draw
--    the feed counter for a fact no device reads), and no second ledger: this table is the whole record of what has been
--    taken back out of 2310 by re-application. The pay out and the write off (SF-2m, SF-2n) are their own acts and
--    subtract from the same held figure in their own rows.
-- 2. THE CAP IS ENFORCED TWICE. The service takes the payment row FOR UPDATE and refuses an apply above the held
--    figure; the trigger below refuses the same thing at the insert, so a second call site cannot forget. The trigger
--    takes the payment row itself (a no-op for the service, which already holds it), so two inserts for one payment
--    serialise at the row and the second sees the first's rows.
-- 3. A ROW IS ONE INVOICE'S SHARE OF ONE REQUEST. batch_id names the request (the GL journal's key,
--    payment.reapplied:<batch_id>); idempotency_key is the caller's key, unique per invoice, so a replay of a request
--    is judged against the rows it already wrote.

CREATE TABLE payment_reapplications (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  batch_id        UUID NOT NULL,
  payment_id      UUID NOT NULL REFERENCES payments(id) ON DELETE RESTRICT,
  invoice_id      UUID NOT NULL REFERENCES invoices(id) ON DELETE RESTRICT,
  customer_id     UUID NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
  branch_id       UUID NOT NULL REFERENCES locations(id) ON DELETE RESTRICT,
  amount_cents    BIGINT NOT NULL CHECK (amount_cents > 0),
  reason          TEXT NOT NULL CHECK (btrim(reason) <> ''),
  applied_by      UUID REFERENCES users(id) ON DELETE SET NULL,
  idempotency_key TEXT NOT NULL CHECK (idempotency_key <> ''),
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (idempotency_key, invoice_id)
);
CREATE INDEX idx_payment_reapplications_payment ON payment_reapplications(payment_id);
CREATE INDEX idx_payment_reapplications_invoice ON payment_reapplications(invoice_id);
CREATE INDEX idx_payment_reapplications_batch ON payment_reapplications(batch_id);

-- LOCK ORDER: the guard takes the payment row at the insert, so a writer must already hold the payment row before it
-- locks any invoice (payments/LOCK-ORDER.md: the payment row, then the invoice rows ascending, then the counters). A
-- call site that locked invoices first and then inserted here would invert that order against the reversal, the bounce
-- and the service, each of which takes the payment row first.
CREATE FUNCTION payment_reapplications_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  v_amount   BIGINT;
  v_reversed TIMESTAMPTZ;
  v_taken    BIGINT;
BEGIN
  SELECT amount_cents, reversed_at INTO v_amount, v_reversed
    FROM payments WHERE id = NEW.payment_id FOR UPDATE;
  IF v_reversed IS NULL THEN
    RAISE EXCEPTION 'payment_reapplications: payment % has not been reversed, no money is held for it', NEW.payment_id
      USING ERRCODE = 'check_violation';
  END IF;
  SELECT COALESCE(SUM(amount_cents), 0) INTO v_taken FROM payment_reapplications WHERE payment_id = NEW.payment_id;
  IF v_taken + NEW.amount_cents > v_amount THEN
    RAISE EXCEPTION 'payment_reapplications: payment % holds % cents and % are already applied, % more would exceed it',
      NEW.payment_id, v_amount, v_taken, NEW.amount_cents
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END
$$;

CREATE TRIGGER payment_reapplications_guard BEFORE INSERT ON payment_reapplications
  FOR EACH ROW EXECUTE FUNCTION payment_reapplications_guard();

-- The posting pair for the new topic, the default branch-NULL rows (a branch override rides the same partial index every
-- other rule does). gl.ValidatePostingRules requires both roles at boot.
INSERT INTO posting_rules (event_type, line_role, account_code) VALUES
    ('payment.reapplied', 'ar',             '1100'),
    ('payment.reapplied', 'refund_payable', '2310')
ON CONFLICT (event_type, line_role) WHERE branch_id IS NULL DO NOTHING;
