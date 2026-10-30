-- 0467: the applied total backstop on a vendor credit (wave 3 item 0.12).
--
-- THE GUARD THE DATABASE OWNS. The credit's open guard already reads the applied sum in its own
-- statement after the credit's row lock (S6 on 704's P1-1 fix), and that closed the race. This
-- column is the defence under it: the applied total is kept ON THE CREDIT'S OWN ROW, moved by every
-- application inside the transaction that holds the row lock, and the CHECK refuses a total that
-- would pass the credit's total at the database itself. A future path that somehow skipped the
-- service's guard (a new writer, a hand-run INSERT in a support session) cannot commit an over
-- application: the row move fails the CHECK and the whole transaction rolls back, application row
-- and all. The service guard still answers first and in words; the CHECK is the floor that cannot
-- be argued with.
--
-- WHY A COLUMN AND NOT ANOTHER TRIGGER. The applications table already carries the truth as rows,
-- and the aging and the projections keep reading their sums. The column is not a second copy the
-- reader must trust: it is the writer's tally under the lock, and its CHECK is the only part the
-- database enforces. A trigger on the applications table would enforce the same sum, but through
-- a path nobody sees in a code review; a column the apply moves beside its INSERT is one the
-- reviewer reads in the same function.
--
-- THE BACKFILL names its own failure. Any credit already applied above its total (only a database
-- that lived through the pre-fix race could carry one) fails here loudly, at the migration, with
-- the credit's id in the error: an over-applied credit is 2100 miscounted by the over-applied
-- cents, not a figure to average away. A migrated database either carries no such credit or stops
-- and says so.

ALTER TABLE purchasing_vendor_credits
  ADD COLUMN applied_cents BIGINT NOT NULL DEFAULT 0;

ALTER TABLE purchasing_vendor_credits
  ADD CONSTRAINT purchasing_vendor_credits_applied_within_total
  CHECK (applied_cents >= 0 AND applied_cents <= total_cents);

UPDATE purchasing_vendor_credits c
   SET applied_cents = COALESCE((SELECT SUM(a.amount_cents)
                                   FROM purchasing_vendor_credit_applications a
                                  WHERE a.credit_id = c.id), 0);
