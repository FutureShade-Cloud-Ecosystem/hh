-- 0100: DIB-165 POS split tender — ONE sale settled across MULTIPLE tenders (cash + card + account).
--
-- WHY A CHILD TABLE. pos_sale.tender is a single TEXT column (0029) and pos_sale.payment_id a single FK.
-- That shape can only describe a sale settled one way. A counter that takes $200 cash + $150 card + the
-- remainder on account has three money facts, each with its own payments row, its own drawer movement and
-- its own GL leg. Widening pos_sale would have meant tender2/tender3 columns — a shape that lies as soon as
-- someone splits four ways. The child table is the honest cardinality.
--
-- pos_sale.tender IS RETAINED AND STILL MEANINGFUL: for a split sale it holds the PRIMARY leg (the largest
-- amount; ties break on the ACCOUNT/CASH/CARD/CHEQUE order the legs were submitted in), so every existing
-- reader — the return refund path's card_refundable, the sale reopen view, the receipt — keeps working
-- unchanged against a value that is true rather than invented. split_tender tells a reader that there is
-- more to the story and where to find it.
--
-- THE SUM GUARD IS THE POINT. A split that does not settle the sale exactly is a drawer that will not
-- balance at close and an invoice whose allocations do not reconcile. A row-level CHECK cannot express a
-- cross-row sum, so the guard is a DEFERRABLE CONSTRAINT TRIGGER: it fires at COMMIT, after every leg of
-- the split has landed, and compares Σ(amount_cents) against the sale's own order total. Deferred is not a
-- weakening — it is the only correct time to assert a sum over rows that are inserted one at a time inside
-- one transaction. The service applies the same check before it opens the tx (a 400 the cashier can act on);
-- this trigger is the backstop that no code path, migration or manual psql session can walk around.

ALTER TABLE pos_sale
  ADD COLUMN split_tender BOOLEAN NOT NULL DEFAULT FALSE;

COMMENT ON COLUMN pos_sale.split_tender IS
  'TRUE when the sale was settled across multiple tenders; the legs live in pos_sale_tenders. pos_sale.tender then carries the PRIMARY (largest) leg.';

-- One leg of a split settlement. A single-tender sale writes NO rows here (behavior freeze).
CREATE TABLE pos_sale_tenders (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  pos_sale_id     UUID NOT NULL REFERENCES pos_sale(id) ON DELETE CASCADE,
  seq             INTEGER NOT NULL CHECK (seq > 0),          -- 1-based submission order (stable display order)
  method          TEXT NOT NULL CHECK (method IN ('ACCOUNT','CASH','CARD','CHEQUE')),
  amount_cents    BIGINT NOT NULL CHECK (amount_cents > 0),  -- a zero/negative leg is a client bug, not a tender
  -- NULL for the ACCOUNT leg by design: nothing is captured at ring, the remainder rides the invoice
  -- balance exactly as a single-tender ACCOUNT sale does.
  payment_id      UUID REFERENCES payments(id) ON DELETE SET NULL,
  till_session_id UUID REFERENCES till_sessions(id) ON DELETE SET NULL, -- the drawer the captured leg ran through
  gateway_ref     TEXT NOT NULL DEFAULT '',                  -- processor ref for a CARD leg; '' otherwise
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (pos_sale_id, seq)
);
CREATE INDEX idx_pos_sale_tenders_sale ON pos_sale_tenders (pos_sale_id, seq);
CREATE INDEX idx_pos_sale_tenders_payment ON pos_sale_tenders (payment_id);

-- At most ONE account leg per sale: two account legs on one sale would post the remainder to AR twice.
CREATE UNIQUE INDEX uq_pos_sale_tenders_one_account
  ON pos_sale_tenders (pos_sale_id) WHERE method = 'ACCOUNT';

-- The sum guard. Basis is orders.total_cents reached through pos_sale.order_id — the sale's OWN committed
-- total, so the assertion cannot drift from a denormalized copy. A cascade delete of the parent leaves
-- nothing to assert (v_total IS NULL) and passes silently.
CREATE OR REPLACE FUNCTION pos_sale_tenders_sum_guard() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
  v_sale  UUID;
  v_total BIGINT;
  v_sum   BIGINT;
BEGIN
  v_sale := COALESCE(NEW.pos_sale_id, OLD.pos_sale_id);
  SELECT o.total_cents INTO v_total
    FROM pos_sale ps JOIN orders o ON o.id = ps.order_id
   WHERE ps.id = v_sale;
  IF v_total IS NULL THEN
    RETURN NULL; -- parent gone (ON DELETE CASCADE): no sale to settle
  END IF;
  SELECT COALESCE(SUM(amount_cents), 0) INTO v_sum
    FROM pos_sale_tenders WHERE pos_sale_id = v_sale;
  -- NO LEGS IS NOT AN UNDER-TENDER. amount_cents > 0 is a row CHECK, so v_sum = 0 can only mean the
  -- sale has no leg rows at all — which is the state of every single-tender sale in this table's
  -- lifetime and of a split whose legs were removed. "This sale is not settled by legs" is a
  -- different claim from "these legs do not add up", and only the second is this guard's business:
  -- an under-tender is unreachable by writing NOTHING, because writing nothing does not fire a
  -- trigger. Without this arm the constraint also forbids ever undoing a split, which turns a
  -- correctness guard into an irreversible one.
  IF v_sum = 0 THEN
    RETURN NULL;
  END IF;
  IF v_sum <> v_total THEN
    RAISE EXCEPTION
      'pos_sale_tenders: split tenders for sale % sum to % cents but the sale total is % cents',
      v_sale, v_sum, v_total
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NULL;
END $$;

CREATE CONSTRAINT TRIGGER pos_sale_tenders_sum_guard
  AFTER INSERT OR UPDATE OR DELETE ON pos_sale_tenders
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION pos_sale_tenders_sum_guard();
