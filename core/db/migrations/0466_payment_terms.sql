-- 0466: the terms master (FIN-WAVE3 1.3, wave 3 TM-1).
-- Owner: internal/customer. Additive, non-breaking, self-contained.
--
-- The dealer sells on terms. A term is a code, a name, the net days an invoice is due in, and
-- the early payment discount's rate (in basis points) and window (in days). Six terms are the
-- dealer's standard:
--
--   RECEIPT     due on receipt
--   NET15       net 15
--   NET30       net 30
--   NET45       net 45
--   1-10-NET30  one percent off when paid within ten days, net 30
--   2-10-NET30  two percent off when paid within ten days, net 30
--
-- A customer references a terms code. The customers table already carried payment_terms_days, the
-- flat number every reader counted; the code sits beside it and the days become its mirror, so a
-- customer on 2/10 net 30 reads as thirty days to everyone who still counts days, and reads as
-- the discount to everyone who reads the terms.
--
-- The mapping of what is there today: 0 days is RECEIPT, 15 is NET15, 30 is NET30, 45 is NET45,
-- and a value that matches none of them keeps its own days as a CUSTOM NET, named in words. A
-- customer on 60 days is "Net 60", never a silent NET30.
--
-- The invoices table gains the terms the document was dated by and the last day its discount
-- runs, so the document carries the terms it was raised under and not the terms of the day
-- after.

CREATE TABLE IF NOT EXISTS payment_terms (
    -- A stable code, not a uuid: the customer and the invoice both name the term by it, and a
    -- code that is renamed is a different term.
    code          TEXT PRIMARY KEY,

    -- What the staff reads on the terms list.
    name          TEXT NOT NULL,

    -- The net days the invoice is due in, counted in the dealer's house days. 0 is due on
    -- receipt.
    net_days      INTEGER NOT NULL CHECK (net_days >= 0),

    -- The early payment discount's rate in basis points (100 is one percent). 0 is no discount.
    discount_bps  INTEGER NOT NULL DEFAULT 0 CHECK (discount_bps BETWEEN 0 AND 10000),

    -- The window the discount runs for, in house days from the invoice's own issue day. 0 is no
    -- window.
    discount_days INTEGER NOT NULL DEFAULT 0 CHECK (discount_days >= 0),

    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT payment_terms_name_nonempty CHECK (btrim(name) <> ''),

    -- A rate with no window would never apply to anything, and a window with no rate discounts
    -- nothing: the two always stand or fall together.
    CONSTRAINT payment_terms_discount_has_a_window CHECK (discount_bps = 0 OR discount_days > 0)
);

-- The dealer's standard terms. ON CONFLICT DO NOTHING, not DO UPDATE: re-running this migration
-- must never overwrite a yard's own terms.
INSERT INTO payment_terms (code, name, net_days, discount_bps, discount_days) VALUES
  ('RECEIPT',    'Due on receipt',  0,   0,  0),
  ('NET15',      'Net 15',         15,   0,  0),
  ('NET30',      'Net 30',         30,   0,  0),
  ('NET45',      'Net 45',         45,   0,  0),
  ('1-10-NET30', '1/10 net 30',    30, 100, 10),
  ('2-10-NET30', '2/10 net 30',    30, 200, 10)
ON CONFLICT (code) DO NOTHING;

-- The customer names the term it is sold on. The column is nullable: a customer that predates the
-- master is given its code by the backfill below, and a customer written by a path that does not
-- know the master yet carries none and is read from its own days.
ALTER TABLE customers ADD COLUMN IF NOT EXISTS terms_code TEXT REFERENCES payment_terms(code);

-- Negative payment_terms_days cannot name a term and are refused naming the customer number.
DO $$
DECLARE
    v_acct TEXT;
    v_days INTEGER;
BEGIN
    SELECT account_no, payment_terms_days
      INTO v_acct, v_days
      FROM customers
     WHERE payment_terms_days < 0
     ORDER BY account_no
     LIMIT 1;
    IF FOUND THEN
        RAISE EXCEPTION '0466: customer % carries negative payment_terms_days (%)', v_acct, v_days;
    END IF;
END $$;

-- A customer whose days name no standard term keeps those days as a custom net, and the custom
-- net is a row in the master: the code a customer carries must name a term that exists.
INSERT INTO payment_terms (code, name, net_days)
SELECT 'CUSTOM-NET-' || d, 'Net ' || d, d
  FROM (SELECT DISTINCT payment_terms_days AS d
          FROM customers
         WHERE payment_terms_days NOT IN (0, 15, 30, 45)) AS s
ON CONFLICT (code) DO NOTHING;

-- The backfill: the days every customer already carries name the term for those days. The two
-- discount terms share net 30 with NET30, so a customer's days can only ever name the four terms
-- whose days are their own; a discount term is reached by its code.
UPDATE customers
   SET terms_code = CASE payment_terms_days
                      WHEN 0  THEN 'RECEIPT'
                      WHEN 15 THEN 'NET15'
                      WHEN 30 THEN 'NET30'
                      WHEN 45 THEN 'NET45'
                      ELSE 'CUSTOM-NET-' || payment_terms_days
                    END
 WHERE terms_code IS NULL;

-- The invoice carries the terms it was dated by and the last day its discount runs. Both are
-- nullable: a document raised on terms with no discount has no discount day, and a document
-- raised before the master (or by a path that does not read it) names no terms.
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS discount_date DATE;
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS terms_code TEXT REFERENCES payment_terms(code);
