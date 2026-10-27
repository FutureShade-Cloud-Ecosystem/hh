-- 0441: the lodgement's deposit slip: the link table from a LODGEMENT movement to the payments it
-- banked (FROM-PRINCIPAL 128 P1-2: a new lodgement names its payments when it is made, so the bank
-- match never guesses which payments a deposit carries). Owner module: internal/till. THE NUMBER IS A
-- PLACEHOLDER: this file lands on the branch only when the principal names the migration number
-- (TO-PRINCIPAL 213 and 221); it is asked under "Asks" in the FW1-13 round 2 report.
CREATE TABLE till_lodgement_payments (
  till_movement_id UUID NOT NULL REFERENCES till_movements(id) ON DELETE CASCADE,
  payment_id       UUID NOT NULL REFERENCES payments(id),
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (till_movement_id, payment_id)
);
CREATE INDEX idx_till_lodgement_payments_payment ON till_lodgement_payments(payment_id);
