-- 0219: queue the AR posting event for every finalized invoice and received payment that has no GL
-- journal behind it, so the running server's dispatcher posts it — the same repair cmd/glbackfill
-- performs, expressed as a migration because bin/glbackfill is not in the deploy bundle and the deploy
-- workflow is not this branch's to change.
--
-- WHY THERE IS ANYTHING TO REPAIR. internal/gl's posting consumer learns about a new invoice or payment
-- through ONE channel: an events_outbox row written inside the same transaction as the document. Two
-- populations of rows never got one:
--   * cmd/demoseed's invoices and payments, until the fix that ships alongside this migration — that one
--     is now closed at source, and demoseed plants the outbox row with every document it plants.
--   * demo documents planted DIRECTLY by earlier migrations. 0055_portal_demo_fixtures.sql inserts two
--     FINALIZED invoices (INV-DEMO-CP03-Q 20,000 and -X 50,000) straight into `invoices` and writes no
--     outbox row at all. demoseed does not own them and never will. 70,000 cents of open AR with nothing
--     behind it in the ledger is the whole of the remaining gap between GL 1100 and the AR subledger.
-- A tester-created document is NOT in either population: the live write path already writes its outbox
-- row. If one is nevertheless missing, this migration queues it too, which is the point of expressing
-- the condition as "has no journal" rather than naming the two fixture ids.
--
-- WHAT IT WRITES, AND WHAT IT CANNOT. `events_outbox` rows and nothing else. It opens no statement
-- against invoices, payments, gl_journals or gl_journal_lines. It cannot post a journal: only the
-- dispatcher does that, through internal/gl's own builders, which re-read the document's facts from
-- billing.InvoiceForGL / till.PaymentForGL rather than trusting the payload — so nothing in this file
-- can make the GL post a number. The payload shape mirrors cmd/glbackfill's candidate.go, which in turn
-- mirrors what pos/service.go and billing/invoice_writer.go emit on the live path.
--
-- IDEMPOTENT THREE WAYS. The insert skips any document that already has a journal, and any that already
-- has an event of that topic — so it composes with cmd/glbackfill rather than racing it, whichever runs
-- first and whatever event ids that one minted. And even a redelivered event is a no-op at the journal
-- layer: InsertJournal pre-checks its own idempotency key. Re-applying this file writes nothing.
--
-- THE DEAD REQUEUE, stated plainly because it is the one thing here that touches an existing row.
-- Until the fix shipping beside this migration, buildInvoiceFinalized appended a tax leg unconditionally,
-- so a TAX-EXEMPT invoice failed gl_journal_lines_check on every attempt and the event went DEAD after
-- five. Both 0055 invoices are tax-exempt, and so are two of demoseed's. A DEAD event whose document
-- still has no journal is a delivery that failed under code that has since been fixed; leaving it DEAD
-- leaves that invoice out of the ledger permanently, and the insert below would not replace it because
-- an event for that document already exists. So it is put back on the queue: status and attempts only,
-- scoped to the two posting topics, and ONLY where the document still has no journal — an event whose
-- journal exists is left exactly as it is. No payload is rewritten and no journal is touched.
--
-- ORDERING. This runs during `migrate`, before the artifact swap, so the events it queues are drained by
-- whichever server is up. That is safe from this deploy forward because the server already running by
-- then carries the tax-leg guard; on the deploy that introduced the guard it did not, which is exactly
-- the history the requeue above exists to clean up after.

DO $$
DECLARE
  v_requeued INT := 0;
  v_invoices INT := 0;
  v_payments INT := 0;
BEGIN
  -- ── 1. Put back on the queue what failed under posting code that has since been fixed ────────────
  UPDATE events_outbox e
     SET status = 'PENDING', attempts = 0, last_error = ''
   WHERE e.status = 'DEAD'
     AND e.topic = 'ar.invoice.finalized'
     AND e.payload ? 'invoice_id'
     AND NOT EXISTS (SELECT 1 FROM gl_journals j
                      WHERE j.idempotency_key = 'ar.invoice.finalized:' || (e.payload->>'invoice_id'));
  GET DIAGNOSTICS v_requeued = ROW_COUNT;

  UPDATE events_outbox e
     SET status = 'PENDING', attempts = 0, last_error = ''
   WHERE e.status = 'DEAD'
     AND e.topic = 'payment.received'
     AND e.payload ? 'payment_id'
     AND NOT EXISTS (SELECT 1 FROM gl_journals j
                      WHERE j.idempotency_key = 'payment.received:' || (e.payload->>'payment_id'));

  -- ── 2. Queue the event a document never got ──────────────────────────────────────────────────────
  -- Scoped to doc_type = 'INVOICE'. A credit memo reverses an EXISTING journal through its own poster
  -- (billing/credit_memo.go: it may NOT re-use ar.invoice.finalized, because posting a reduction as a
  -- fresh accrual doubles the wrong direction). Backfilling one is a different, unproven problem, and
  -- cmd/glbackfill declines it for the same reason.
  INSERT INTO events_outbox (topic, payload)
  SELECT 'ar.invoice.finalized',
         jsonb_build_object(
           'invoice_id',  i.id::text,
           'customer_id', i.customer_id::text,
           'total_cents', i.total_cents,
           'due_date',    to_char(i.due_date, 'YYYY-MM-DD'))
    FROM invoices i
   WHERE i.doc_type = 'INVOICE'
     AND i.status IN ('FINALIZED', 'PARTIALLY_PAID', 'PAID')
     AND NOT EXISTS (SELECT 1 FROM gl_journals j
                      WHERE j.idempotency_key = 'ar.invoice.finalized:' || i.id::text)
     AND NOT EXISTS (SELECT 1 FROM events_outbox e
                      WHERE e.topic = 'ar.invoice.finalized'
                        AND e.payload->>'invoice_id' = i.id::text);
  GET DIAGNOSTICS v_invoices = ROW_COUNT;

  INSERT INTO events_outbox (topic, payload)
  SELECT 'payment.received',
         jsonb_build_object(
           'payment_id',   p.id::text,
           'order_id',     '',
           'amount_cents', p.amount_cents,
           'method',       p.method,
           'gateway_ref',  '')
    FROM payments p
   WHERE p.status = 'RECEIVED'
     AND NOT EXISTS (SELECT 1 FROM gl_journals j
                      WHERE j.idempotency_key = 'payment.received:' || p.id::text)
     AND NOT EXISTS (SELECT 1 FROM events_outbox e
                      WHERE e.topic = 'payment.received'
                        AND e.payload->>'payment_id' = p.id::text);
  GET DIAGNOSTICS v_payments = ROW_COUNT;

  RAISE NOTICE '0219: requeued % dead invoice event(s); queued % invoice and % payment event(s) for posting',
    v_requeued, v_invoices, v_payments;
END $$;
