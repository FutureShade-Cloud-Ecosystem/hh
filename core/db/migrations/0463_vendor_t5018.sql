-- 0463: the T5018 subcontractor flag, and the business number the form asks for (W3-T51, row 153).
--
-- The CRA's Form T5018 is what a contractor hands a payer: it names the subcontractor's business
-- number and the amounts the payer paid that contractor in the calendar year. The dealer had
-- nowhere to keep either half. A vendor record carried the name, the terms and the remit address
-- and nothing a tax form could be built from, so the answer to "who are our subcontractors and
-- what did we pay them last year" had to be assembled by hand from the bills, and the business
-- number was never captured at all.
--
-- TWO COLUMNS, ON THE VENDOR RECORD:
--
--   is_t5018_subcontractor  the Financial Controller's own mark on the vendor. False is the
--                           default for every vendor that has never been marked, so the mark is
--                           a fact about a vendor rather than a gap in the record. The route that
--                           writes it is walled to the Controller by the role gate (the census
--                           row names money-desk at admin, and the controller is the matrix's
--                           only admin seat on that surface).
--
--   business_number        the contractor's CRA business number. NULL, not empty, because a
--                           vendor that has never been given one is not the same as a vendor
--                           whose number was cleared: the report says the number is MISSING
--                           either way, and the row keeps the distinction. The program account
--                           form (the nine digits with RC0001 after them) is a legal value.
--
-- The column is ADDED, not backfilled: no vendor was a subcontractor before this flag existed,
-- and the business numbers the seeded vendors carry were never captured, so guessing them from
-- the vendor number would be worse than admitting they are missing.

ALTER TABLE vendors
    ADD COLUMN is_t5018_subcontractor BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN business_number TEXT;
