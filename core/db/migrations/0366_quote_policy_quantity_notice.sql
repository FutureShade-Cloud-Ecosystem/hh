-- 0366: the dealer's quantity notice, a text setting on quote_policy.
--
-- Customers blame the dealer for quantities they chose, so every quote carries a notice that the
-- quantities are the customer's to confirm. The wording is the dealer's: the dealer default row holds
-- it and a branch row overrides it (the 0043 idiom quote_policy already follows). The column default is
-- the house wording, so every existing row, the seeded dealer default included, serves it with no
-- backfill. internal/quote.DefaultQuantityNotice carries the same text for a deployment whose policy row
-- is missing; a test pins the two equal.
ALTER TABLE quote_policy
  ADD COLUMN IF NOT EXISTS quantity_notice TEXT NOT NULL
    DEFAULT 'All quantities are to be confirmed by the customer. Please review them before you accept it.'
    CHECK (char_length(quantity_notice) BETWEEN 1 AND 500);

COMMENT ON COLUMN quote_policy.quantity_notice IS
  'The notice every quote and sales order carries that the quantities are the customer''s to confirm; branch row over dealer default';
