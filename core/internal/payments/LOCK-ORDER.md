# The one money lock order

Every writer that touches a payment, an invoice balance or a feed counter takes its rows in one order:

1. the order row (a writer that works on one order)
2. the payments sequence row (`scope_sequences`, scope `payments`), without advancing it
3. the existing invoice rows (`FOR NO KEY UPDATE`; a writer that walks the rows an already-written payment
   allocated, the reversal and `MarkNSF`, takes them in ascending id order)
4. the invoices sequence row (`scope_sequences`, scope `invoices`)

A writer that has no use for a step skips it and keeps the order of the steps it does take.

The till's returns with a cash or card refund, the single return and the credit note across orders alike, draw
the payments counter BEFORE their invoice rows, and ONLY when `lockReturnInvoicesTx` has decided invoice rows will
be taken at all (an owed order's own invoice, or a paid order's deposit documents): both drew the counter last, at
the refund payment's insert, until the Opus review's P2-4 found the cycle that closed with a received payment
apply (the return holding an owed invoice `FOR NO KEY UPDATE`, the apply holding the counter and waiting on that
invoice, the return then waiting on the counter); the delta reviews' P1 found the note path (`returnAcrossInTx`)
still on the old order after the single return's fix, aborted at its child's payment insert the same way, and
their P2 found the counter drawn by every cash or card return for its whole transaction, a paid sale's included,
which held the one counter row every payments writer serialises on for no cycle it could close. A return on a
fully paid sale with no deposit document locks no invoice, never draws the counter, and its later `FOR KEY SHARE`
(the `pos_returns` foreign key) is a share, not a lock in this order, conflicting with no payments-walking
writer's `FOR NO KEY UPDATE`. The POS account credit writes no payment row and draws no payments counter, so it
skips step 2 and keeps the order of the steps it does take; its `FOR UPDATE` on the invoice conflicts with a
share, but returns of one sale are serialised by the per-sale lock before either.

Every return that puts goods back on the shelf also takes those goods' `stock_balances` rows FIRST, through
inventory's own seam (`LockStockTx`, sorted product then location, the same pass the swap takes for its two
halves), before its first counter draw and before any processor refund call: the POS ring takes a stock row
before it draws the invoices counter, and the order cancel's settlement now takes its stock rows before the same
counter (round 2), so a return whose rail drew the counter before its restock could meet either writer head on
(the round 2 Opus read's P2-1, both meetings legged in round 3). The rows are the requested restocking lines'
products at the sale's branch, resolved before the transaction opens (a rung sale's lines are immutable), so the
guard's own restock set inside the transaction is a subset of rows already held; a return that restocks nothing
(a damaged or credit only return) locks no stock row at all.

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

What an owed card return costs under this order: a cash or card return that takes invoice rows (an owed order's, or a
paid order's deposit documents) holds the payments counter from before those rows to commit, and the card rail runs
its processor call (`RefundCard`, the external round trip) inside that window, after the locks and before the refund
payment's insert. Every payments writer in the company (every settle, apply, reversal, pay balance take and till
tender) queues behind one owed card refund's processor call. That is the price of keeping the owed path cycle free,
and it is why the counter is drawn only when invoice rows will be taken: a card return on a fully paid sale with no
deposit document holds no counter through its processor call at all, and draws the counter only at its refund
payment's insert, a local statement's length.

The received payment apply's PREVIEW draws no counter either (item 127): each preview used to take the counter and
the invoice row locks before rolling back, so every debounced keystroke at the desk briefly serialised against
every payments writer, the till's tenders included. A preview writes nothing, so it needs no place in this order at
all; its split is computed on one lock free snapshot read (`readApplyInvoices`), and the apply re-judges everything
under the locks it takes, refusing in words on the ceiling the moved balances leave whenever the two disagree.

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
| Register received payment apply (item 64 Lane A pay, `received_apply.go`) | payments counter (not advancing), the named invoice rows (`FOR NO KEY UPDATE`, ascending id, every one before the first balance move), payment insert (payments feed +1), invoice balance moves (invoices counter +1 each, in the same ascending order) | yes: the whole set sorted, and the till's owed return now draws the payments counter before its own invoice locks, so the two writers meet at the counter, never head on |
| The same route's PREVIEW (item 127, `previewApply` / `readApplyInvoices`) | nothing: one lock free statement reads the named invoices' balances (one snapshot, so the split is consistent), the account and the branch reach folded into the WHERE exactly as the apply's locking statement folds them | yes, by taking none of the order: a preview writes nothing, so it draws no counter and no invoice row, and a desk's debounced keystroke never queues a till payment; on an unchanged body the apply answers the preview's split, and when the balances moved in between the apply's own ceiling refusal says so in words |
| Counter `RecordReceipt` | the same, in `lockAndTotal` | yes |
| POS pay balance take (item 9.4) | order row (`FOR NO KEY UPDATE`), payments counter (not advancing), the order's open invoice rows (`FOR NO KEY UPDATE`, ascending), the raise of an uninvoiced remainder's invoice through the desk's writer (a new invoice: invoices counter +1, item 9 review P2-2), payment insert through the till's tender (payments feed +1), invoice balance moves (invoices counter +1 each) | yes: the order row is the order's own first step, then the register settle's own sequence; the raise draws the invoices counter (step 4) before the settle's own counter draws, the same step every invoice-inserting writer takes after its invoice row locks |
| `PayWithStamps`, `PayWithIntent`, `Pay` (portal and card settle) | the same, in `lockAndTotal` | yes |
| `ReversePayment` | payment row, payments counter (the reversing insert and the stamp, feed +1 each), every allocated invoice row `FOR NO KEY UPDATE` ascending, contra allocations, invoice re-opens (invoices counter +1 each) | yes: among the writers that take a counter or an invoice, the payment row is first only for NSF and this one (the terminal writers `MarkDeposited` and bankrec's claim and release update payment rows but take no counter and no invoice) |
| POS ring | stock rows, order rows, payments counter (the tender's payment insert), new invoice (invoices counter) | yes: no existing invoice |
| POS return, cash or card refund (the till refund rail) | per-sale advisory lock, the restock's stock rows (the restocking lines' `stock_balances` rows, `FOR UPDATE`, sorted product then location, through inventory's own seam, taken only when the return restocks), payments counter (not advancing, drawn inside `lockReturnInvoicesTx` only when invoice rows will be taken), the owed order's invoice rows (`FOR NO KEY UPDATE`, sorted, PR 277's split), returned order lines, invoice `FOR KEY SHARE` (the `pos_returns` foreign key), refund payment insert (payments feed +1) | yes: the counter is drawn before the invoice rows the return takes, the order's own steps 2 then 3, and a paid sale's return (no invoice row) draws no counter at all (the delta reviews' P2); the stock rows sit ahead of every counter because the ring and the cancel's settlement both take stock rows before the invoices counter, so a return that drew a counter first could meet either head on (round 3's ring and cancel legs pin both meetings) |
| POS credit note across orders, cash or card refund (`returnAcrossInTx`) | per-sale advisory locks in ascending order id, the children's restock stock rows (the union of every child's restocking lines' `stock_balances` rows, `FOR UPDATE`, sorted product then location, through inventory's own seam; the swap is the one caller that pre-locks itself, taking the sorted union of both its halves' products ahead of the body, so the body adds no pass there), payments counter (not advancing, drawn inside `lockReturnInvoicesTx` only when invoice rows will be taken), every child's owed invoice rows (`FOR NO KEY UPDATE`, sorted), the note header, per child: returned order lines, invoice `FOR KEY SHARE`, refund payment insert (payments feed +1) | yes: the note draws the counter before its invoice rows exactly as the single return does (the delta reviews' P1: the note path was the one writer left on the old order), and a note of fully paid sales with no deposit documents draws no counter either; the stock rows sit ahead of every counter for the same reason the single return's do |
| POS return to account credit | per-sale advisory lock, the restock's stock rows (the restocking lines' `stock_balances` rows, `FOR UPDATE`, sorted product then location, through inventory's own seam, taken only when the return restocks), the order's invoice rows (`FOR NO KEY UPDATE`, sorted, every document the account rail offsets), returned order lines, invoice `FOR KEY SHARE` (the `pos_returns` foreign key), invoice `FOR UPDATE` (ApplyCreditToInvoice), invoices counter | yes: no payments row and no payments counter, so step 2 is skipped and the rest keep their order; the stock rows sit ahead of the invoices counter the rail draws because the ring and the cancel's settlement take stock rows before that same counter (round 3's legs: this was the one rail that drew it before its restock) |
| Group and whole-order invoice writers | the order row its caller locked, invoices counter (a new invoice) | yes: no payments row, no existing invoice |
| The order cancel's deposit settlement (billing `CancelOrderDeposits`) | order row (the caller's cancel transaction), the stock rows the cancel's release will write (the ACTIVE HARD holds' `stock_balances` rows, `FOR UPDATE`, sorted product then location, the order every balance locking caller in inventory uses, taken through inventory's own seam ahead of the settlement), the order's live deposit rows (`FOR NO KEY UPDATE`, ascending id, every one before the first balance move), invoices counter (+1 per cleared document, drawn inside the clearing) | yes: no payments row and no payments counter, so step 2 is skipped and the rest keep their order; the walk is id ordered like the apply's, so the two writers can only queue, never meet head on; the stock rows sit between the order row and the deposit rows because the POS ring takes a stock row first and this counter later, so a cancel that drew the counter before its stock rows could meet the ring head on (round 2's leg pins it: the settlement drew the counter inside the precheck and the release wrote stock after, a 40P01 either way) |
| Quote counter door (QD-5, PR 120) | order row, `LockPaymentsSequenceTx`, existing invoice row (`FOR NO KEY UPDATE`), invoices counter | yes |
| Till `MarkNSF` | payment row, payments counter (not advancing: the serialising lock, it writes no payments feed row), every allocated invoice row `FOR NO KEY UPDATE` ascending, invoice re-opens (invoices counter +1 each) | yes |
| Outbox observer | pool read after commit | takes none of these rows |

One writer keeps the reservation rows' own opposite order: the expiry sweep (`ExpireDueReservations`) updates
reservation rows before balance rows, while the cancel's lock pass takes balance rows first; a cycle between the
two needs a branch whose allocation policy sets an order TTL (the default `OrderTTLHours` is 0, never expires),
and such a branch accepts the retry.

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

## The sync seq trigger (migration 0422): the audit of the counters it takes

Every served table now carries a BEFORE INSERT OR UPDATE row trigger that draws the table's stream counter when
the statement did not (docs/adr/0025-sync-seq-trigger.md). The draw takes the counter row to commit, so it is one
more place a counter is taken. The rule that keeps the order: **a table written inside a transaction that takes
other counters gets its counter's place in this order before it gets a trigger, and a writer that touches a row
ahead of a counter it needs takes that counter first, without advancing it.**

How the audit was done. Every non test write to the 25 trigger tables was listed from source (functions that
write two or more of them, and every function that takes a caller's transaction and writes one), the whole money
and sync integration suites were run once with a log trigger on `scope_sequences` that records every counter take
with its transaction (the trigger's own draws separate from the app's by trigger depth), and the order of takes
in each transaction was compared pairwise across transactions. What it found:

| Transaction | Counter the trigger now takes | Where it falls against the four steps | Verdict |
|---|---|---|---|
| `payments` and `invoices` writers with an explicit draw (settle, apply, ring, return, credit across orders, cancel's settlement, door, `ReversePayment`) | none: seq changes in the same statement, the trigger does nothing | unchanged | nothing added |
| `MarkNSF`: `UPDATE payments SET status = 'BOUNCED'` | `payments`, advancing (the bounce is now a payments feed row) | after the payment row (step 0), before `LockPaymentsSequenceTx`, which re-enters it, before the invoice rows (step 3) and the invoices counter (step 4) | re-entrant, in order. The bounce used to take the counter without advancing it; it now also advances it |
| `MarkDeposited` (till), bankrec `PersistMatch` claim and `Unmatch` release: `UPDATE payments SET status` | `payments`, advancing | after the payment row; these transactions take no other counter and no invoice row | the one counter they take; no pair to cross |
| Credit memo (`IssueCreditMemo`, own transaction): `INSERT INTO invoices` with no seq | `invoices` | step 4; the transaction holds no other counter and no existing invoice row | the one counter; no pair to cross |
| `RemoveMethod`, `AddMethod`, `SetDefaultMethod`: `portal_payment_methods` | `payment_methods` | not a money transaction: no payments or invoices counter, no money row | none |
| `UpdateTermsGuarded`: `quotes` terms and refold | `quotes` | not inside a money transaction (the quote order-payment door takes `quotes` by an explicit draw before `LockPaymentsSequenceTx`, which is the order it already ran in) | none new |
| customers, pos_parked_tickets, warranty_claims | `customers`, `parked_tickets`, `warranty_claims` | none of the POS ring, returns, order cancel, `MarkNSF`, `ReversePayment` or the credit memo writes these tables in its transaction (checked at source; the warranty credit memo runs in the billing writer's own transaction, and warranty updates its claim afterward) | none |
| `MarkSheetPostedTx` (count sheet post, inside the stock adjust transaction) | none: explicit draw | n/a | nothing added |
| `ReopenRun`, `MoveRunDate` (dispatch): `UPDATE delivery_runs` then `delivery_stops` | `runs` then `stops` | not money, but a crossing with the sync cancel of a run (`RunsSyncStore.Write`), which takes the run row `FOR UPDATE`, then un-assigns its stops (`stops`), then draws the run's seq (`runs`) | **deadlock twice over.** First, across two runs: the cancel took stops then runs, the reopen runs then stops (`TestIntegration_SeqTrigger_RunFirstWritersTakeStopsBeforeRuns`). The first fix (take `stops` first) closed that and opened the same pair on ONE run: the reopen holding the `stops` counter waiting on the run row, the cancel holding the row waiting on the counter (PR 556 review P2-1). The dispatch order is **the run row, then `stops`, then `runs`**, the cancel's own: `lockRunThenStopsSeqTx` (run row `FOR UPDATE`, then a non advancing take of `stops`) first in both writers; red then green on both shapes (`TestIntegration_SeqTrigger_RunFirstWritersAndTheSyncCancelOnTheSameRun`, ReopenRun and MoveRunDate) |

The pairwise comparison over every non fixture transaction in the run found one opposite order among the
trigger's draws, and it is the dispatch pair above. (`payments` before `invoices` is the only order the money
transactions take them in, and the trigger's `payments` draws all sit in front of an `invoices` take; the
`invoices` before `payments` entries in the log are the non advancing `payments` lock logged after an invoice
raise inside one transaction, the order this file documents.) `stock_balances` is not in the trigger: SF-7 adds
it with a take of the counter right after its stock rows, and a concurrent sale beside a restocking return.

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
- a reversal beside a bank match of the SAME payment, both orders (`lockorder_reversed_integration_test.go`): reversal first, the match is refused (the claim carries `reversed_at IS NULL` in the UPDATE that decides it), match first, the reversal proceeds (reversal.go refuses only a reversed, reversing or VOID payment); both take the payment row, then the payments counter, and neither deadlocks
- the NSF pair against each settle (single invoice)
- the multi-invoice NSF against a settle naming the invoices in the other order (red without the bounce's
  payments counter)
- the NSF beside the POS return's account credit, parked with a BEFORE UPDATE trigger inside the counter upsert
- the POS cash return and the POS card return against the settle on the returned sale's invoice, and the card
  settle as the parked side (cmd/core, red before the `FOR NO KEY UPDATE`)
- the owed cash return and the owed cash credit note across orders, each beside a received payment apply on the
  same invoice (cmd/core, red before each path drew the counter: the single return, then the note)
- a paid sale's cash return parked in its transaction beside an apply on an unrelated invoice: the apply answers
  while the return is still parked (cmd/core, red while every cash or card return held the counter whatever it
  locked)
- a received payment PREVIEW beside a REAL till tender parked inside its payments counter statement: the preview
  answers while the tender is still parked (cmd/core, red while each preview drew the counter; item 127, the
  money desk review's P3-4)
- the multi-invoice `ReversePayment` against a real POS cash return on the sale's invoice (cmd/core, red before
  the reversal's `FOR NO KEY UPDATE`)
- the REAL POS return to account credit against the ring's stock row and counter draw, and against the real order
  cancel's stock-first settlement through the real cancel handler (internal/order, round 3's legs: both red
  before the return took its restock's stock rows ahead of its first counter draw)

A reversal cannot cycle with a settle, in any id order: both take the payments counter row before any invoice row,
so they serialize there and never interleave their invoice rows. The settle-against-reversal leg
(`TestIntegration_LockOrder_SettleBesideReversePayment`) is a regression guard for exactly that: it is red
only on the old invoice-first settle. The same holds for every pair of payments-walking writers now that `MarkNSF` takes the counter too.
