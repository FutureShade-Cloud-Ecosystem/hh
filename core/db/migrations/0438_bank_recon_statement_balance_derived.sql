-- 0438: bank_reconciliations stores whether its statement balance was derived from the month's lines (SF-2s round 2).
--
-- Opening a month with a zero balance derives statement_balance_cents from the month's statement lines and sets
-- statement_balance_derived TRUE; opening with a typed balance sets it FALSE, and a refresh keeps the flag as it is.
-- CompleteReconciliation re-derives the statement balance from the lines if and only if statement_balance_derived is
-- TRUE, removing the storedStmt == netAtOpen heuristic and the updated_at read.
--
-- Existing rows stay FALSE: neither the migrations nor the seeds plant any bank_reconciliations row, and on a box
-- that already holds an OPEN row, FALSE holds completion to the stored statement_balance_cents rather than
-- overwriting a stated closing balance from the lines; re-opening with 0 sets it TRUE.
ALTER TABLE bank_reconciliations
    ADD COLUMN statement_balance_derived BOOLEAN NOT NULL DEFAULT FALSE;
