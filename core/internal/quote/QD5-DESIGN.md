# QD-5 design: taking payment on a converted quote, and the cash customer

This note says what the code does and is the design the review reads the code against. It was
rewritten for review round 1: the action is now one transaction, the counter's invoice no longer
moves the order, and an order is billed once whichever door bills it.

## The door

`POST /api/v1/quotes/{id}/order-payment` (`quoteTakeOrderPayment`) is a door on the quote module, as
the staff conversion is. The money stays in the modules that own it. The quote package declares two
small interfaces and never imports `order`, `billing` or `payments` and never touches their tables:

- an invoicer: answers what the agreed invoice of an order is (an existing document, or what would be
  raised) and raises it;
- a payer: records one already taken payment against one invoice through the payments settle, with the
  caller stamped as the receiver.

Every method of both takes the door's transaction (`pgx.Tx`). `cmd/core/wire_money.go` implements both
over the real modules: the order module answers which lines are billable (`order/invoice_source.go`),
billing shapes and writes the document (`billing/invoice_writer.go`), payments settles
(`payments/counter_receipt.go`). Each of those three gained an entry that takes the caller's
transaction beside the existing entry, which is unchanged for every other caller.

## The one transaction

The whole action is ONE transaction on ONE pool connection:

1. The quote's own repository opens the transaction and takes the quote's transaction scoped advisory
   lock (`pg_advisory_xact_lock`) as the first statement. The lock is therefore not a second
   connection and not a session lock: it leaves with the commit or the rollback. Calls on one quote
   (the same key or different keys) run one after another and each sees the previous one's committed
   rows; calls on other quotes run beside them.
2. On that transaction, in order: the quote's facts; for a cash customer's ACCEPTED quote the
   conversion (the one conversion path, `runConvert`, running on the transaction the context carries,
   including the order's history row); the order's agreed facts (read on the transaction, because the
   pool cannot see the uncommitted order); the invoice (header, lines, the AR subledger row, the
   finalized event); the payment (header, allocation, the invoice's balance, the subledger payment row,
   the payment event).
3. One commit. Any refusal or fault before it rolls back everything: no order, no invoice, no balance,
   no payment, no event, the quote still ACCEPTED, its soft holds as they were.

Nothing inside the transaction calls out of the process. The four counter tenders (CASH, CARD, CHEQUE,
EFT) are money already taken across the desk: `RecordReceipt` talks to no gateway, claims no payment
intent and produces no gateway reference (its own header says so), and the card rail's charge is a
separate path (`PayWithIntent`) this door never calls. So there was no processor call to keep outside
the transaction. If a tender that calls a processor is ever added here, that call stays outside the
transaction under the till's own reconciliation and the transaction covers the database writes only.

Because the transaction commits whole, the earlier half state (the order and the invoice committed, the
payment not) and the retry that finished it are gone, along with the 503 that announced it. What stays
is idempotent replay: the settle's key is checked first, on the transaction; a replayed key posts
nothing and answers the first payment as stored (the reference as recorded and the receiver), and the
same key with a different method or amount is a 409.

The connection count is one per call, and every read the conversion makes while it holds the transaction
goes through that transaction. The credit gate's hold read joins the conversion's transaction (the
conversion stamps it on the context with `till.WithCallerTx` in `credit.go`, `checkCredit`), and
inventory's reserve reads its allocation policy through the caller's transaction as well. No second
connection is asked of the pool while a lock is held, so a pool of any size serves any number of
parallel callers: they queue for their one connection and never for each other's. There is no admission
gate in the store. The proof is `cmd/core/qd5_poolcycle_integration_test.go`: a pool of exactly four
(`pool_max_conns=4` on the command), one conversion held inside the credit gate and three callers
waiting on its quote (the counter's cash actions and plain confirms), all complete. The test is red
without the stamp (the held conversion's hold read waits for a connection the waiting callers hold
until the bound), and at the pool of one the probes run without holds.

## The contract text

For a cash customer the order never exists unpaid: the conversion, the invoice and the payment commit
together or not at all. For a customer with credit the order already exists when the payment is taken
(its own conversion came first); the invoice and the payment are written together, so a payment that
fails leaves no invoice and no balance. A credit order that is never paid at the counter is billed at
fulfilment (plan 1.1), and CP0-2 answer A's "billed whole at acceptance" applies to orders paid at the
counter. The contract says that and no more.

## The role rule

The route's `x-access` is surface `counter-till`, level `work`. The gate admits every role the
configuration gives Work or above on the Counter Till: Inside Sales, Sales manager, Yard manager,
Branch manager. Outside Sales (none), the General Manager (read), HR, the Yard team, the Financial
controller and the rest are refused at the gate. `default.json` already holds exactly that. The service
holds what the gate cannot: an active caller, reach to the quote's branch, and that the quote is
converted or is a cash customer's accepted quote.

The Yard manager is admitted at the cash customer's one action, on an ACCEPTED quote only, with nothing
in it editable: converting at the agreed totals as part of taking the payment asks no more authority
than ringing a sale, which the Yard manager does on the till. The separate conversion of a quote (no
payment) keeps the selling role rule, so the Yard manager still cannot convert on its own.

## The agreed price, and the invoice that equals it

The invoice is raised from the order's own figures, not from fulfilment:

- every order line at its agreed line total, under the whole order key `order-invoice:<order id>`, the
  key the fulfilment path uses, so billing the order again when it leaves returns this document;
- for a delivery quote, one more line for the delivery fee the quote carried (`orders.quoted_freight_cents`,
  the frozen baseline, not the live freight figure dispatch may change);
- the tax the quote agreed (`orders.tax_cents`), taken as stated. The quote taxes the lines plus the
  fee; the writer taxes lines alone, so without this a delivery invoice would be short by the fee and
  its tax. A test with an agreed tax that differs from a flat 13 percent pins it.

The order module refuses to answer when its own header does not agree with its lines (lines plus fee
plus tax must equal the order total). Every other caller of the writer sets none of the new facts and
gets the document it always got.

## Billed once, whichever door bills it

The counter's invoice header carries a source marker (`metadata.source = COUNTER`), and so does its
`ar.invoice.finalized` event.

- Before raising, the counter looks for ANY live invoice of the order under another key (a shipment
  group's, say). If there is one it refuses with a 409 naming the invoice and raises nothing; the
  payment is then recorded against that invoice by Payables and receivables, the General Manager or
  the Financial controller (the counter roles only read the Money Desk, so the refusal says who can,
  not a register they cannot open). The invoices of an order therefore never total more than the order.
- Both writers that can bill an order whole or by group take the order's row lock
  (`SELECT ... FROM orders WHERE id = $1 FOR UPDATE`) as the first statement of their own
  transaction and run their existence checks after it: the counter's (in the door's transaction, at
  its first read of the agreed invoice) and the group writer's (inside the transaction that inserts the
  group invoice; the whole order writer shares the path). A group invoice and a counter invoice of one
  order therefore queue behind each other, and whichever comes second sees the first one's committed
  document: the group writer finds the counter's document and links its groups to it, the counter finds
  the group's invoice and answers its 409. The race is forced in tests by a trigger that sleeps inside
  one side's transaction while the other is issued, both ways round.
- Lock order, so the lock cannot deadlock: the counter takes the quote's advisory lock first (only the
  counter takes it), then, in a credit order's payment, the order row, then the payments scope
  sequence row, then the invoices scope sequence and the invoice and payment rows. The payments row is
  taken through the payments module's own lock (`payments.LockPaymentsFeedSeqTx` on the door's
  transaction) BEFORE the invoice is raised, and the settle's numbering later in the same transaction
  re-takes a lock that transaction already holds: the door once raised the invoice (the invoices row)
  and then settled (the payments row), the opposite of every writer that both bills and settles, and
  the review reproduced the 40P01 that follows between the door and a real register settle at pool 4
  and 16, on an action whose card charge has already happened. Every writer that takes both scope
  sequence rows now takes the payments one first:

  | writer | payments sequence row | invoices sequence row | order |
  |---|---|---|---|
  | the counter's door (this action) | `LockPaymentsFeedSeqTx` before the raise, then the settle's own draw | the agreed invoice's raise, then the settle's balance move | payments then invoices (this fold; the raise alone took invoices first) |
  | the AR register settle (`billing`'s RecordPayment into `payments`' settleReceipt) | the payment header insert | the invoice balance move | payments then invoices |
  | the payments writer (`payments`' settle, PayWithStamps and the counter receipt) | the payment header insert | the invoice balance move | payments then invoices |
  | the POS ring (the sale transaction: the till tender, then the ring's invoice) | the till payment insert | the ring invoice insert | payments then invoices |
  | the billing group and whole order writers (`invoice_writer.go`) | never | every invoice they raise | invoices only, no payments row to hold or wait on |
  | the till refund (RefundTender) | the refund payment insert | never | payments only |
  | the till NSF reversal (MarkNSF) | never (the payment row is updated, none inserted) | the invoice re-open | invoices only |

  The group writer takes the order row first, then the same invoices scope sequence and invoice rows.
  Order edits, reservations and shipment groups already take the order
  row lock first and then the rows below it. For a cash customer the conversion runs before the lock
  and holds stock rows, but the order row it then locks is its own, written in the same transaction
  and visible to no one else, so no other session can be waiting on it. No path takes an invoice or a
  payment row and then waits for an order row, no writer holds the invoices sequence row and then
  waits for the payments one, and a writer that touches one sequence row only can never hold the
  other, so there is no cycle with the advisory lock, the conversion's locks or between the two
  sequence rows. The proof is `cmd/core/qd5_seqorder_integration_test.go`: the door beside the real
  register settle, the door's window stretched open inside its own transaction, deadlocked (40P01)
  before the fix at pool 4 and 16 and both answer with it.
- The manager's group invoice route reads the plan before the writer takes the order lock, so its group
  set can overlap a group a call ahead of it billed while it waited. After the lock the writer therefore
  decides against what committed: the same set under the same key answers that call's document, and a
  set that shares a group with another live group document is refused (a 400 that says to read the plan
  again), so no group is billed twice. The proof is `cmd/core/qd5_groupinvoice_integration_test.go`,
  through the served route at a pool of four and at a larger one.
- A shipment plan's group billing treats only the counter's own document (the marker) as covering its
  groups. Any other whole order document (the sync door's, which may bill only the fulfilled lines)
  covers nothing, so a later group is billed exactly as it was before QD-5.

## The counter's invoice does not move the order

An order paid at the counter has not left the yard. The consumer of `ar.invoice.finalized` skips the
status move for the counter source unless the order is already FULFILLED (then it completes the move
to INVOICED), so the order stays DRAFT, confirmable, with its holds ACTIVE and HARD. AR and the ledger
take the event whole, unchanged. When the order later reaches FULFILLED by event (the hand over) and
already has a counter invoice, the same move is completed there: the holds are consumed once, at
fulfilment, the status becomes INVOICED, and no second invoice is raised. Every other invoice source
(the POS ring, the fulfilment invoice, a shipment group invoice) moves the order exactly as before,
pinned by a test per source. The hook sits in `AdvanceFromEvent` (order/service.go); its logic is in
`order/consumer.go`. The completion is best effort, because the hand over has committed and must not be
failed by it, but it is never silent: a failed lookup or move is logged with the order id and the event
id. The dispatcher does not retry an observer, so nothing re-runs a failed completion by itself; the
hook is idempotent (a guarded FULFILLED to INVOICED move, a no-op once the order is past it), and
`AdvanceFromEvent` runs it again when a redelivered hand over finds the order already FULFILLED, so any
redelivery completes what the first try could not.

## Amounts

Method is one of CASH, CARD, CHEQUE, EFT. A missing key, a zero or negative amount, an unknown method
or an amount above what the invoice can take is refused, and the refusal rolls the transaction back, so
a refusal leaves no invoice behind. A customer with a credit account may pay a part (a deposit) and the
balance stays on the invoice. A customer with no credit pays the whole of what the invoice still owes,
which on a fresh order is the whole agreed total: a balance left on the invoice would be credit the
customer does not have. Both checks compare against the invoice's BALANCE, not its total, so a customer
whose limit dropped to zero after a deposit can still pay the rest, and an overpay is refused by the
door in words about the balance. A replayed key is told apart first (a read on the payments seam) and
answered with the stored payment, so a replay is never refused for a balance it has since moved.

## The customer with no credit

The conversion gate is told every conversion draws on credit, so a cash customer cannot convert and
there is no order to pay. For such a customer (credit limit zero) the same operation, called on their
ACCEPTED quote, is the whole counter action: convert, raise the invoice, take the payment in full. A
customer with credit converts through the existing conversion door first. The conversion inside the
action is told, through an option only this package can set, that the order is paid at the counter in
this same action, so the credit gate judges it as a cash sale (never blocked by a limit or a hold).

## The discounted line

The conversion copies a line's net total and carries the discount in the description (`(N% off)`) and
the order line's metadata (the percent and the undiscounted total). Totals are copied unchanged, and
the description reaches the invoice line through the order's own facts.

## The receipt

The settle already issues the receipt. The door answers the payment number, tender, amount, time, the
reference as stored and the receiver as stored (also on a replay), with the invoice number, the order
number and the balance. No new receipt format.
