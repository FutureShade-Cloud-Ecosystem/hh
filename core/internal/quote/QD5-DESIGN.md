# QD-5 design: taking payment on a converted quote, and the cash customer

This note is written before the code and says what the code must do. It is the
design the review reads the code against.

## The door

`POST /api/v1/quotes/{id}/order-payment` (`quoteTakeOrderPayment`) is a door on
the quote module, as the staff conversion is. The money stays in the modules
that own it. The quote package declares two small interfaces and never imports
`order`, `billing` or `payments` and never touches their tables:

- an invoicer: answers what the agreed invoice of an order is (an existing
  document, or what would be raised) and raises it;
- a payer: records one already taken payment against one invoice through the
  payments settle (`RecordReceipt`), with the caller stamped as the receiver.

`cmd/core/wire_money.go` implements both over the real modules. The order
module answers which lines are billable through its own interface
(`order/invoice_source.go`); billing shapes the document
(`billing/invoice_writer.go`).

## The role rule

The route's `x-access` is surface `counter-till`, level `work`. The gate
therefore admits every role the configuration gives Work or above on the
Counter Till: Inside Sales, Sales manager, Yard manager, Branch manager.
Outside Sales (none), the General Manager (read) and the rest are refused at
the gate. `default.json` already holds exactly that, so it is not edited. The
service holds what the gate cannot: an active caller, reach to the quote's
branch (refused as not found outside it), and that the quote is converted or is
a cash customer's accepted quote.

## The agreed price, and the invoice that equals it

The invoice is raised from the order's own figures, not from fulfilment:

- every order line at its agreed line total (a whole order key, the same
  `order-invoice:<order id>` the fulfilment path uses, so billing the order
  again when it leaves returns this document and bills nothing twice);
- for a delivery quote, one more line for the delivery fee the quote carried
  (`orders.quoted_freight_cents`, the frozen baseline, not the live freight
  figure dispatch may change);
- the tax the quote agreed (`orders.tax_cents`), taken as stated instead of
  assessed again, because the customer was shown that number. The quote taxes
  the lines plus the fee; the invoice writer taxes lines alone, so without this
  a delivery invoice would be short by the fee and its tax.

The order module refuses to answer when its own header does not agree with its
lines (lines plus fee plus tax must equal the order total). A disagreement is a
fact someone must look at, never a document to bill. Every other caller of the
writer sets none of the new facts and gets the document it always got.

An order that later gains a shipment plan must not be billed again under the
plan's group key. The writer looks for the whole order document first and, if
it exists, answers it for the groups and marks them covered by it.

## Replay and concurrency

The caller's idempotency key is bounded and namespaced by the quote
(`quote-order-payment:<quote id>:<key>`) before it reaches the settle, so a key
used on one quote can never replay another quote's payment. The settle is
idempotent on that key in the database; a replay posts nothing and answers the
first payment. The door also refuses a replay whose method or amount differ
from the first payment (409, never replayed). All payment actions on one quote
run under one per quote advisory lock held on a dedicated connection, so eight
parallel calls with one key do the work once and the other seven take the
replay path, and two different keys race for the balance one at a time (the
second is refused by the settle when the balance is gone).

## Amounts

Method is one of CASH, CARD, CHEQUE, EFT (the settle's four counter tenders).
A missing key, a zero or negative amount, an unknown method, or an amount above
what the invoice can take is refused before anything is written, so a refusal
leaves no invoice behind. A customer with a credit account may pay a part (a
deposit) and the balance stays on the invoice. A customer with no credit pays
the whole agreed total: a balance left on the invoice would be credit the
customer does not have.

## The customer with no credit

The conversion gate is told every conversion draws on credit, so a cash
customer cannot convert and there is no order to pay. For such a customer
(credit limit zero) the same operation, called on their ACCEPTED quote, is the
whole counter action: convert, raise the invoice, take the payment in full.
A customer with credit keeps today's gate and converts through the existing
conversion door first.

The mechanism:

1. Everything checkable is checked before the first write: the caller's reach
   and selling role (the conversion half keeps its own role rule, so the Yard
   manager still cannot convert), the quote is ACCEPTED, the amount equals the
   quote's agreed total exactly, the tender and key are valid, the quote's own
   totals agree with its lines. A short, zero, negative, over, unknown tender
   or keyless request is refused here, with no order and no invoice.
2. The conversion runs through the one conversion path with an unexported
   option that tells the credit gate this order is paid at the counter in
   this same action, so it is gated as a cash sale (never blocked by a credit
   limit or a hold, as the till's own cash sale is). Only the quote package can
   set the option; the portal door and the plain staff door never do.
3. The invoice is raised under the whole order key, then the payment is
   settled under the namespaced key, all under the per quote lock and on a
   context that a dropped client connection cannot cancel halfway.

Why a failed payment leaves no order: the payment is validated in step 1
against every input the settle validates, so the settle has no caller caused
refusal left to give. What remains is an infrastructure fault between two
commits (the order and invoice commit before the payment can see the invoice,
because the payments settle runs its own transaction and cannot join the
conversion's). That window cannot be removed from inside the quote, billing
and order files: a single transaction across all three needs a settle entry
that takes a transaction, in a payments file this item may not touch. It is
closed the other direction instead: every step is keyed and idempotent, so the
same request repeated with the same key finishes the action (finds the
converted quote and its order, finds the invoice, settles) and never posts
twice, and the failure answers by saying exactly what exists and that the
repeat finishes it. This is reported, not hidden.

## The discounted line

The conversion copied a line's unit price (the rule price, undiscounted) and
its net total, so a discounted line reached the order and invoice with a unit
price that does not multiply to its total and no discount shown. A net unit
price cannot always multiply out in whole cents, so the discount is carried
and shown instead: the order line's description gains `(N% off)` and its
metadata carries the percent and the undiscounted total. Totals are copied
unchanged. The description flows to the invoice line through the order's own
facts, so the order and the invoice say the same thing.

## The receipt

The settle already issues the receipt (payment number, tender, amount, time,
reference, the receiver). The door answers those fields together with the
invoice number, the order number and the balance. No new receipt format.
