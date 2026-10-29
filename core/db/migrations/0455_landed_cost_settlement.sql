-- 0455: W2-1d item 4, the landed-cost payable is paid on its terms.
--
-- A landed cost posts its freight/duty/handling payable to GL 2100 and nothing
-- ever relieved it: the payable sat open on the aging forever, and a real
-- dealer pays its carriers and brokers on their terms. The header now carries
-- its settlement: the CAD amount paid and the instant it was paid. The
-- settlement posts DR 2100 / CR 1000 through the module's own settle route,
-- keyed landedcost.settle:<id>, so the AP aging reads the payment from the
-- settlement journal's effective instant the same rule it reads a bill's
-- payment by.
--
-- The columns stay on the header (a landed-cost payable is settled once, in
-- full): no settlement table, no second document to tie out.

ALTER TABLE landed_costs
  ADD COLUMN IF NOT EXISTS settled_cad_cents BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS settled_at TIMESTAMPTZ;

COMMENT ON COLUMN landed_costs.settled_cad_cents IS
  'the CAD amount paid the payee vendor; 0 while the payable is open';
COMMENT ON COLUMN landed_costs.settled_at IS
  'the instant the settlement was paid; NULL while the payable is open';
