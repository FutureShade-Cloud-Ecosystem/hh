-- 0365: an acceptance records that the customer confirmed the quantities.
--
-- Customers blame the dealer for quantities they chose, so the public accept now needs the customer to
-- confirm the quantities as a separate act, beside the typed name and the affirmative. The acceptance
-- record keeps that fact beside the name, so the staff quote and the acceptance history can say it.
--
-- THE BACKFILL. Acceptances that exist already were taken before the question was asked, so they read
-- false: the record says plainly that no confirmation was captured, and never claims one. The column
-- default stays false so a writer that does not know the column cannot assert a confirmation; the
-- accept path always writes the value it was given.
ALTER TABLE quote_acceptances
  ADD COLUMN IF NOT EXISTS quantities_confirmed BOOLEAN NOT NULL DEFAULT false;

COMMENT ON COLUMN quote_acceptances.quantities_confirmed IS
  'The customer ticked that they reviewed the quantities and they are what they want; false on acceptances taken before the question existed';
