-- 0450_payment_allocations_reversal_rows.sql
-- Owner module: internal/payments (payment_allocations is the payments module's table, 0010).
--
-- WHY. A bounce and a void took their payment's allocation rows with them. The rows went, so the
-- payment's split across invoices was unrecoverable, and an as-of aging rebuilt from the dated acts
-- found no allocation at all for a cheque that was good at the as-of date and bounced afterwards,
-- so the invoice read as owed at a date it had been paid. The money moved; the record of what it
-- paid did not.
--
-- THE ROW KIND. payment_allocations gains kind:
--   APPLICATION - money applied to an invoice. Every row that exists today is an APPLICATION, and
--                 the DEFAULT says so, so no existing row changes meaning.
--   REVERSAL    - the row that takes an application back, dated at the undo.
--
-- THE REVERSAL CARRIES THE NEGATIVE AMOUNT AND REFERENCES THE APPLICATION IT REVERSES. The
-- application it reverses is the row of the same payment and the same invoice: the primary key
-- carries the kind, so a (payment, invoice) pair holds at most one application and at most one
-- reversal, and the pair names the application exactly. reverses_allocation_id carries that
-- reference as a foreign key, and the guard below refuses a reversal that names no application, that
-- names an application of another payment or invoice, or that does not carry the application's
-- exact negative.
--
-- THE READERS KEEP ONE RULE, NOT TWO. A reversing payment's contra allocation (reversal.go) is a row
-- on the reversing payment with a POSITIVE amount, and the sign is carried by
-- payments.reverses_payment_id. A reversal row is a row on the original payment with a NEGATIVE
-- amount. Both net under the one expression the aging and the 1100 tie already carry,
--   SUM(CASE WHEN p.reverses_payment_id IS NULL THEN a.amount_cents ELSE -a.amount_cents END)
-- because a reversal row sits on a payment that reverses nothing and so contributes its own
-- negative, and a reversing payment's contra row is flipped by the CASE. The bounce and the void
-- could not take the reversing payment's shape: they act ON the original payment row (the till, the
-- feed, the NSF record and the statement all read that row), and a second payments row would put
-- two payments on one statement and move today's figures. So the kind carries their undo, and no
-- reader grows a second rule.
--
-- THE AMOUNT CHECK IS KIND-AWARE: an application is positive (as before), a reversal is negative.
--
-- WHAT IT DOES NOT MOVE. No figure moves. The rows a bounce or a void used to delete are kept, their
-- negative is written beside them, and every sum that read them nets to the figure it read before.
-- The one thing that changes is what the table can say: "what did this cheque used to pay?" is now
-- answerable from the rows themselves instead of from an audit trail.

-- 1. The row kind. Every row that exists today is an APPLICATION.
ALTER TABLE payment_allocations
  ADD COLUMN kind TEXT NOT NULL DEFAULT 'APPLICATION' CHECK (kind IN ('APPLICATION', 'REVERSAL'));

-- 2. The row handle a reversal names, and the reference itself.
ALTER TABLE payment_allocations
  ADD COLUMN id UUID NOT NULL DEFAULT gen_random_uuid(),
  ADD COLUMN reverses_allocation_id UUID;
CREATE UNIQUE INDEX ux_payment_allocations_id ON payment_allocations (id);
ALTER TABLE payment_allocations
  ADD CONSTRAINT fk_payment_allocations_reverses
  FOREIGN KEY (reverses_allocation_id) REFERENCES payment_allocations (id);

-- 3. The primary key carries the kind: a (payment, invoice) pair holds an application and the
--    reversal of it, and at most one of each.
ALTER TABLE payment_allocations DROP CONSTRAINT payment_allocations_pkey;
ALTER TABLE payment_allocations ADD PRIMARY KEY (payment_id, invoice_id, kind);

-- 4. The amount check is kind-aware.
ALTER TABLE payment_allocations DROP CONSTRAINT payment_allocations_amount_cents_check;
ALTER TABLE payment_allocations ADD CONSTRAINT payment_allocations_amount_cents_check
  CHECK ((kind = 'APPLICATION' AND amount_cents > 0) OR (kind = 'REVERSAL' AND amount_cents < 0));

-- 5. The guard: a reversal names the application it reverses and carries its exact negative, and an
--    application names no reversal. A second call site cannot write a reversal that points at
--    nothing or at the wrong money. And once a reversal stands, the application it names cannot be
--    changed at all: growing its amount or moving its payment or invoice would rewrite the history
--    the reversal row preserved, so the pair would stop netting zero.
CREATE OR REPLACE FUNCTION payment_allocations_reversal_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    app_amount  BIGINT;
    app_payment UUID;
    app_invoice UUID;
BEGIN
    IF TG_OP = 'UPDATE' AND OLD.kind = 'APPLICATION'
       AND (NEW.kind <> OLD.kind OR NEW.amount_cents <> OLD.amount_cents
            OR NEW.payment_id <> OLD.payment_id OR NEW.invoice_id <> OLD.invoice_id)
       AND EXISTS (SELECT 1 FROM payment_allocations
                    WHERE kind = 'REVERSAL' AND reverses_allocation_id = OLD.id) THEN
        RAISE EXCEPTION 'payment_allocations: an application a reversal row names cannot be changed (payment %, invoice %)',
            OLD.payment_id, OLD.invoice_id USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.kind = 'APPLICATION' THEN
        IF NEW.reverses_allocation_id IS NOT NULL THEN
            RAISE EXCEPTION 'payment_allocations: an application row must not name a reversal (payment %, invoice %)',
                NEW.payment_id, NEW.invoice_id USING ERRCODE = 'check_violation';
        END IF;
        RETURN NEW;
    END IF;
    IF NEW.reverses_allocation_id IS NULL THEN
        RAISE EXCEPTION 'payment_allocations: a reversal row must name the application it reverses (payment %, invoice %)',
            NEW.payment_id, NEW.invoice_id USING ERRCODE = 'check_violation';
    END IF;
    SELECT amount_cents, payment_id, invoice_id INTO app_amount, app_payment, app_invoice
      FROM payment_allocations WHERE id = NEW.reverses_allocation_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'payment_allocations: a reversal row names no application (payment %, invoice %)',
            NEW.payment_id, NEW.invoice_id USING ERRCODE = 'check_violation';
    END IF;
    IF app_payment <> NEW.payment_id OR app_invoice <> NEW.invoice_id THEN
        RAISE EXCEPTION 'payment_allocations: a reversal row must reverse an application of the same payment and invoice (payment %, invoice %)',
            NEW.payment_id, NEW.invoice_id USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.amount_cents <> -app_amount THEN
        RAISE EXCEPTION 'payment_allocations: a reversal row must carry the exact negative of the application it reverses (payment %, invoice %): % against %',
            NEW.payment_id, NEW.invoice_id, NEW.amount_cents, app_amount USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER payment_allocations_reversal_guard
  BEFORE INSERT OR UPDATE ON payment_allocations
  FOR EACH ROW EXECUTE FUNCTION payment_allocations_reversal_guard();
