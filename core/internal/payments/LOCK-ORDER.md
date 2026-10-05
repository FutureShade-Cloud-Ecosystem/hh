# The one money lock order

Every writer that touches a payment, an invoice balance or a feed counter takes its rows in one order:

1. the order row (a writer that works on one order)
2. the payments sequence row (`scope_sequences`, scope `payments`), without advancing it
3. the existing invoice rows (`FOR UPDATE`)
4. the invoices sequence row (`scope_sequences`, scope `invoices`)

A writer that has no use for a step skips it and keeps the order of the steps it does take.

## Why the payments counter comes first

A settle holds an invoice row while it waits for the payments counter row. Any other writer that holds the counter on
its way to that same invoice closes a cycle, and Postgres aborts one side with a 40P01. On the portal card path the card
is charged before the settle runs, so an aborted settle leaves money captured, the intent in CHARGING and the invoice
unpaid. The cycle existed between the register settle and `ReversePayment` before this order was written down, and it
would have appeared again between a settle and the quote counter door once the door took the counter first.

`LockPaymentsSequenceTx` takes step 2 on the caller's transaction and does not advance the counter: the upsert writes
the value the row already holds, and a missing row is seeded at 0 so the settle's own increment still hands out 1. A
feed number is used up only by the statement that stamps a row, once per payment.

## The writers

| Writer | Rows, in the order taken | Follows the order |
|---|---|---|
| Register `RecordPayment` (through the counter `RecordReceipt`) | payments counter, invoices, payment insert, invoices counter | yes |
| Counter `RecordReceipt` | the same, in `lockAndTotal` | yes |
| `PayWithStamps`, `PayWithIntent`, `Pay` (portal and card settle) | the same, in `lockAndTotal` | yes |
| `ReversePayment` | payment row, payments counter, invoice (share lock from the contra allocation's foreign key), invoices counter, invoice update | yes: the payment row is no other writer's first lock except NSF's |
| POS ring | stock rows, payments counter (the tender's payment insert), invoices counter (a new invoice) | yes: no existing invoice |
| Till refund and POS return, cash or card | per-sale advisory lock, returned order lines, payments counter (the refund payment's insert) | yes: no invoice |
| POS return to account credit | per-sale advisory lock, returned order lines, invoice row, invoices counter | yes: no payments row |
| Group and whole-order invoice writers | the order row its caller locked, invoices counter (a new invoice) | yes: no payments row, no existing invoice |
| Quote counter door (once it lands) | order row, `LockPaymentsSequenceTx`, existing invoice row, invoices counter | yes |
| Till `MarkNSF` | payment row, invoices counter, invoice | **no**, see below |
| Outbox observer | pool read after commit | takes none of these rows |

## Open item: NSF against a settle

`MarkNSF` re-opens each invoice with `WITH iseq AS (INSERT ... scope_sequences invoices ...) UPDATE invoices ...`. The
UPDATE computes the new row, which draws the invoices counter, before it locks the invoice row. A settle holds the
invoice row and then wants the invoices counter, so a bounce of an earlier payment on an invoice, against a settle on the
same invoice, meets head on and one side is aborted with a 40P01. This was already so before the order was written down,
and this change neither causes nor cures it: it lives in `till/repository.go`, outside this change's reach.

The leg `TestIntegration_LockOrder_NSFBesideSettle` reproduces it deterministically at pool 4 and 16 and is held behind
`nsfLockOrderFixed` until the till lane lands the fix. The fix that turns the leg green was tried and reverted: before
the loop that re-opens the invoices, take `SELECT 1 FROM invoices WHERE id = $1 FOR UPDATE` on each allocated invoice.

## The legs

`lockorder_integration_test.go`, each at pool 4 and pool 16, each parked in a widened window and released by reading
`pg_stat_activity`, never by a bare sleep:

- a register settle and a payments settle, each against `ReversePayment` on the same invoice
- a door-shaped writer (order row, `LockPaymentsSequenceTx`, then its invoice) against each settle, with an existing
  invoice and with none
- the multi-invoice card settle that locks the door's invoice first and closes the cycle last
- the lock does not advance the feed: a settle's feed number advances by exactly one
- `ReversePayment` against the POS return's account credit
- the NSF pair (held, see above)
