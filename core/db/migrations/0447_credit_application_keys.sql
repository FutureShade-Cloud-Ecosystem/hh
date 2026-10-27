-- 0447: FW1-12b round 8: the credit application door's idempotency keys become a table.
--
-- Round 7 gave the apply door's idempotency key a ROUTE wide scope by scanning the invoices'
-- metadata for the key under the key's own advisory slot (findCreditApplicationByKey in
-- billing/credit_apply.go). The scan has no index behind it: the key sits inside the metadata's
-- jsonb, so every application read the whole invoices table, a read that grows with every
-- dealer's history. This migration gives the route its own record store, billing owned, whose
-- primary key IS the key:
--
--   * idempotency_key, the PRIMARY KEY: the door's check becomes one index lookup, and the
--     database itself refuses a second row under a spent key. The key's scope is the bare key
--     string, the scope the route's callers use today: the handler passes the body's
--     idempotency_key through untouched, and the round 7 check matched it across the route with no
--     org or invoice qualifier, so the primary key carries no qualifier either.
--   * invoice_id, credit_kind and credit_id: the invoice and the credit the key was spent on, so
--     the route wide refusal can name where the key was spent.
--   * request_hash: the sha256 of the request's own body fields, so the same key with a DIFFERENT
--     request is refused 409 instead of replayed (the round 7 door compared only kind, credit and
--     amount, so a retry that changed the reason replayed).
--   * stored_result: the first application's answer as jsonb. A replay answers it with
--     Replayed=true and writes nothing a second time.
--   * application_id: the application the row records.
--   * created_at: when the key was spent.
--
-- stored_result and application_id are nullable ON PURPOSE, and no committed row carries a NULL in
-- either. The apply inserts its row as a RESERVATION in the application's own transaction, before
-- it judges the request: the reservation insert is the twin serialisation the advisory slot used
-- to provide (a twin's uncommitted row makes the insert wait for that transaction, so two requests
-- under one key cannot both find the key unspent, whichever invoice each names), and the same
-- transaction sets both columns before it commits. A request refused anywhere after the
-- reservation rolls the whole transaction back, reservation included, so a refusal leaves no row.
--
-- NO BACKFILL: the records of applications made before this migration live only on the invoices'
-- metadata and carry no request hash, so a backfilled row could not answer the hash rule. An older
-- application's retry is still answered, from the target invoice's own metadata (one primary key
-- read, no scan), exactly as round 7 answered it on the key's own invoice. What an older record
-- does NOT get is the route wide refusal beyond its own invoice: a pre 0447 key reused against
-- another invoice is caught by the money guards alone. No box outside this branch's review runs
-- carries any application record yet (the act has not merged), so nothing is backfilled; if the
-- lead wants the route wide refusal for pre 0447 keys, that is a follow up migration, and its
-- hash column would be empty by the same rule.

CREATE TABLE credit_application_keys (
    idempotency_key TEXT PRIMARY KEY,
    invoice_id UUID NOT NULL REFERENCES invoices(id),
    credit_kind TEXT NOT NULL,
    credit_id UUID NOT NULL,
    request_hash TEXT NOT NULL,
    stored_result JSONB,
    application_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
