# The one money lock order

Every writer that touches a payment, an invoice balance or a feed counter takes its rows in one order:

1. the order row (a writer that works on one order)
2. the payments sequence row (`scope_sequences`, scope `payments`), without advancing it
3. the existing invoice rows (`FOR NO KEY UPDATE`; a writer that walks the rows an already-written payment
   allocated, the reversal and `MarkNSF`, takes them in ascending id order)
4. the invoices sequence row (`scope_sequences`, scope `invoices`)

A writer that has no use for a step skips it and keeps the order of the steps it does take.

One writer sits deliberately outside the order's letter: a writer whose only touch on the invoice is a foreign key
check, which takes the `FOR KEY SHARE` the check needs BEFORE the payments counter. That writer is the POS return
with a cash or card refund: its `pos_returns` INSERT holds the returned sale's invoice share, and only afterwards
does its refund rail insert the refund payment and draw the counter. The share is safe before the counter because no
payments-walking writer's invoice lock conflicts with a key share any more (they are all `FOR NO KEY UPDATE`, see
below), so none of them can be made to wait on the return's share while the return waits on the counter; and the
return's own wait on the counter always ends at the holder's commit. The POS account credit's `FOR UPDATE` does
conflict with a share, but the credit takes no payments counter, so a return and a credit cannot close a cycle
either.

## Why the payments counter comes first

A settle holds an invoice row while it waits for the payments counter row. Any other writer that holds the counter on
its way to that same invoice closes a cycle, and Postgres aborts one side with a 40P01. On the portal card path the card
is charged before the settle runs, so an aborted settle leaves money captured, the intent in CHARGING and the invoice
unpaid. The cycle existed between the register settle and `ReversePayment` before this order was written down, and it
would have appeared again between a settle and the quote counter door once the door took the counter first.

`LockPaymentsSequenceTx` takes step 2 on the caller's transaction and does not advance the counter: the upsert writes
the value the row already holds, and a missing row is seeded at 0 so the settle's own increment still hands out 1. A
feed number is used up only by the statement that stamps a row, once per payment.

## The queueing cost of the one counter row

The counter-first rule buys deadlock freedom at a queueing cost: every settle holds the one payments counter row while
it waits on its invoice locks, so a settle held up on an invoice stalls every other payments writer behind it. The
worst case was a POS card return, whose transaction holds the returned sale's invoice key share through its processor
call: a settle waiting on that share held the counter for the length of an external round trip. The invoice rows are
now taken FOR NO KEY UPDATE, which does not conflict with a key share, so a settle never waits on a return at all, and
the writers a settle can still queue behind (an account credit, a reversal, a bounce) hold their rows for the length
of local statements only. The settle-beside-settle leg is the guard for the rule itself: two settles naming the same
invoices in opposite orders serialise at the counter instead of deadlocking on the rows.

## Why the invoice rows are FOR NO KEY UPDATE

A `FOR UPDATE` on an invoice conflicts with every row lock, including the `FOR KEY SHARE` a foreign key check takes
when a child row is inserted. A POS return's INSERT into `pos_returns` takes that share on the returned sale's
invoice before the return's refund rail draws the payments counter, so any payments writer that held the counter
while its `FOR UPDATE` waited on that share closed a cycle: the settle did (the card was already charged when the
abort landed, or the return's card refund was already issued and the return rolled back with no `pos_returns` row),
and round 2's reversal did the same against a return on its second invoice. The settle, the reversal and `MarkNSF`
write no key column, so their invoice rows are taken `FOR NO KEY UPDATE`: still in conflict with every invoice
UPDATE (any writer's balance move, which takes the row at the same strength) and with the account credit's explicit
`FOR UPDATE`, so each of them still serialises against every other invoice writer exactly as before, but never in
conflict with a key share. No writer deletes invoices or changes their keys, so nothing relies on `FOR UPDATE`'s key
conflict; the share was never something these writers needed to hold back.

## The writers

| Writer | Rows, in the order taken | Follows the order |
|---|---|---|
| Register `RecordPayment` (through the counter `RecordReceipt`) | payments counter (not advancing), invoice rows (`FOR NO KEY UPDATE`, the allocation order), payment insert (payments feed +1), invoice balance moves (invoices counter +1 each) | yes |
| Register received payment apply (item 64 Lane A pay, `received_apply.go`) | payments counter (not advancing), the named invoice rows (`FOR NO KEY UPDATE`, ascending id, every one before the first balance move: the order the till's returns take, PR 277), payment insert (payments feed +1), invoice balance moves (invoices counter +1 each, in the same ascending order) | yes: the multi invoice case takes the whole set sorted, so it cannot meet a return's own sorted set head on whatever order the caller's list arrived in |
| Counter `RecordReceipt` | the same, in `lockAndTotal` | yes |
| POS pay balance take (item 9.4) | order row (`FOR NO KEY UPDATE`), payments counter (not advancing), the order's open invoice rows (`FOR NO KEY UPDATE`, ascending), the raise of an uninvoiced remainder's invoice through the desk's writer (a new invoice: invoices counter +1, item 9 review P2-2), payment insert through the till's tender (payments feed +1), invoice balance moves (invoices counter +1 each) | yes: the order row is the order's own first step, then the register settle's own sequence; the raise draws the invoices counter (step 4) before the settle's own counter draws, the same step every invoice-inserting writer takes after its invoice row locks |
| `PayWithStamps`, `PayWithIntent`, `Pay` (portal and card settle) | the same, in `lockAndTotal` | yes |
| `ReversePayment` | payment row, payments counter (the reversing insert and the stamp, feed +1 each), every allocated invoice row `FOR NO KEY UPDATE` ascending, contra allocations, invoice re-opens (invoices counter +1 each) | yes: among the writers that take a counter or an invoice, the payment row is first only for NSF and this one (the terminal writers `MarkDeposited` and bankrec's claim and release update payment rows but take no counter and no invoice) |
| POS ring | stock rows, order rows, payments counter (the tender's payment insert), new invoice (invoices counter) | yes: no existing invoice |
| POS return, cash or card refund (the till refund rail) | per-sale advisory lock, returned order lines, invoice `FOR KEY SHARE` (the `pos_returns` foreign key), payments counter (the refund payment's insert) | the one exception, safe as written above |
| POS return to account credit | per-sale advisory lock, returned order lines, invoice `FOR KEY SHARE` (the `pos_returns` foreign key), invoice `FOR UPDATE` (ApplyCreditToInvoice), invoices counter | yes: no payments row, and the share and the FOR UPDATE are one writer's own sequence |
| Group and whole-order invoice writers | the order row its caller locked, invoices counter (a new invoice) | yes: no payments row, no existing invoice |
| Quote counter door (QD-5, PR 120) | order row, `LockPaymentsSequenceTx`, existing invoice row (`FOR NO KEY UPDATE`), invoices counter | yes |
| Till `MarkNSF` | payment row, payments counter (not advancing: the serialising lock, it writes no payments feed row), every allocated invoice row `FOR NO KEY UPDATE` ascending, invoice re-opens (invoices counter +1 each) | yes |
| Outbox observer | pool read after commit | takes none of these rows |

## Why the invoice rows come before the invoices counter

A settle holds an invoice row while it waits for the invoices counter row. Any other writer that
draws the counter on its way to that same invoice closes a cycle, and Postgres aborts one side with
a 40P01. Till `MarkNSF` did exactly that: its re-open statement (`WITH iseq AS (INSERT ...
scope_sequences invoices ...) UPDATE invoices ...`) draws the counter while it computes the new row,
before it locks the invoice, so a bounce of an earlier payment on an invoice met a settle on the
same invoice head on. The bounce now takes the payments counter row first (which serialises it
against every settle before either touches an invoice: the bounce walks ascending ids and a settle
locks the caller's allocation order, and without the counter the two could interleave their rows in
opposite orders), then `SELECT 1 FROM invoices WHERE id = $1 FOR NO KEY UPDATE` on each allocated
invoice, in ascending id order, before the re-open runs.

`ReversePayment` rested on a weaker accident: its contra allocation's insert takes a foreign key
share lock on the invoice row, which ordered the single-invoice case without anyone deciding it. A
share lock covers only the invoice the loop has already reached, so a multi-invoice reversal held
the counter with no lock at all on its second invoice, and met the POS return's account credit (or
an NSF bounce) holding that row head on. The reversal now takes the same explicit `FOR NO KEY
UPDATE` on every allocated invoice, in ascending id order, before its first counter draw.

## The legs

`lockorder_integration_test.go` (this package) and `lockorder_pos_return_test.go` (cmd/core, the ones that need the
REAL POS return over HTTP), each at pool 4 and pool 16, each parked in a widened window and released by reading
`pg_stat_activity` or an answer channel, never by a bare sleep:

- a register settle and a payments settle, each against `ReversePayment` on the same invoice
- a door-shaped writer (order row, `LockPaymentsSequenceTx`, then its invoice) against each settle, with an existing
  invoice and with none
- the multi-invoice card settle that locks the door's invoice first and closes the cycle last
- the lock does not advance the feed: a settle's feed number advances by exactly one
- two settles naming the same invoices in opposite orders, the counter-first guard (red on the main shape)
- the multi-invoice `ReversePayment` beside the POS return's account credit on its second invoice, parked with a
  BEFORE UPDATE trigger inside the counter upsert (red on the main shape), and beside credits on its invoices,
  parked past its first re-open
- the multi-invoice `ReversePayment` against NSF bounces on its invoices
- the NSF pair against each settle (single invoice)
- the multi-invoice NSF against a settle naming the invoices in the other order (red without the bounce's
  payments counter)
- the NSF beside the POS return's account credit, parked with a BEFORE UPDATE trigger inside the counter upsert
- the POS cash return and the POS card return against the settle on the returned sale's invoice, and the card
  settle as the parked side (cmd/core, red before the `FOR NO KEY UPDATE`)
- the multi-invoice `ReversePayment` against a real POS cash return on the sale's invoice (cmd/core, red before
  the reversal's `FOR NO KEY UPDATE`)

A reversal cannot cycle with a settle, in any id order: both take the payments counter row before any invoice row,
so they serialize there and never interleave their invoice rows. The settle-against-reversal leg
(`TestIntegration_LockOrder_SettleBesideReversePayment`) is a regression guard for exactly that: it is red
only on the old invoice-first settle. The same holds for every pair of payments-walking writers now that `MarkNSF` takes the counter too.
