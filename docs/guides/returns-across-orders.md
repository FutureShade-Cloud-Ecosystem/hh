# Returns, the till's desk: the testers' brief

A walk of the Counter Till's returns desk for the JV team. Plain speech; the shape is the shape the seed and the acceptance legs hold, the wording is the app's own. What is "later" is named in the app's own words. Sources: the seed and screens on `till/returns-across-screens` (PR 279), and the app's own logic in `web/apps/counter-till/src/returns/`. The acceptance legs in `web/apps/counter-till/test/acceptance/20-sales.pw.ts` on that branch are the source of the named words.

## The role

The Counter Till's returns desk works a return against the customer first, not the sale. The clerk picks the customer from the search bar (name or account number), the desk lists every line the customer can return across all of their orders, the clerk picks lines from several orders into one credit note, sets each line's disposition and the refund rail, and the desk records the credit note in one send. A walk in sale is a return found only by its receipt or order number, and the walk in rail is cash or card only. A swap is a return whose credit pays a new order, the leftover going back by the original tender.

The clerk's reach is the branch the front door signs in at. The dealer's refund approval limit is off on the demo (the demo_on config's policy difference; the demo's refunds run from tens of dollars to thousands and the gate is not in the dealer's config), so the desk never asks for an approver.

## How to sign in

On the front door, tap the Sales card. The desk inside is the till, the new counter sale on the entry. The bar at the top of the desk carries a Returns entry, separate from the sale entry. Tap Returns and the desk lands on the returns surface, at the hash `#/returns`. The sale is held in custody, the keyboard's own keys stay quiet while the returns desk is up. The sign-in rides the front door's handoff so no second sign-in is asked for.

A card marked Coming cannot be tapped yet. The clerk's other cards sit on the same door.

## The demo world

The returns world is the day's ringed sales at the dealer's branch. The acceptance leg's seed plants the customer Bay Ridge Landscaping (demo) with two sales on the same day, and the walk in fixture with a single line rung with no customer. The two-order return's first sale is one Holland Paver, Charcoal (demo), the second is one Aluminum Edging 8 ft (demo), the swap's sale is forty Holland Pavers and the replacement is the Aluminum Edging the credit pays for. The walk in return's sale is the Aluminum Edging.

The dealer's refund approval limit is off on the demo image (the `demoRoleConfig`'s policy difference, the demo's refunds run from tens to thousands and the gate is not in the dealer's config). The desk's refusal rails are the server's: a cash refund is paid from the open drawer, a card refund needs the original capture to be refundable, the account rail is offered when the account can spend credit.

## The surface and the four steps

The returns surface lives at `#/returns`. The bar carries a Returns entry; the sale's bar carries a Returns entry, too, so the clerk can pop in and out of the desk without losing the sale's state. The four steps the desk carries, in the stepper's own order:

1. Customer. The clerk searches by name or account number, or chooses Walk in sale. The desk shows every order the customer holds and every line that is still returnable, in the server's order.
2. Lines. The clerk picks the lines from one or several orders into the draft. Each line carries its quantity (capped at what the server said is open) and its disposition. The credit so far is the server's preview, the figures the commit books.
3. Refund. The clerk picks the rail (cash, card, or on the account), the desk books the credit note, the drawer's expected cash drops by exactly the note's total when the rail is cash.
4. Done. The credit note is recorded. The Done step shows the credit's total, every order the note covered, and the rung-by line.

## A short walk to try

The acceptance leg `web/apps/counter-till/test/acceptance/20-sales.pw.ts` is the whole walk in one, on the `till/returns-across-screens` branch.

- Sign in as Sales. The till's new counter sale is on screen. Press the Returns entry in the bar. The desk lands on the returns surface, the sale's bar carries the same Returns entry. The URL's hash is `#/returns`.
- The customer step. Type "Bay Ridge" in the search bar. Press the customer's row. The desk moves to the lines step, with every order the customer holds on screen, every order's lines listed under its order number. The step's data step is "lines."
- The two-order return. The first order is the one Holland Paver, the second is the one Aluminum Edging. Press "Return to stock" on the first line of the first order, then on the first line of the second order. The lines carry the picked mark. The credit count reads "2 lines from 2 orders" in the matrix's own words. The credit total is the server's preview.
- Continue to the refund. Press "Continue to the refund." The step moves to the refund. The rails are the matrix's three: Cash, Card, On the account.
- Record the credit note. Press "Cash." Press "Record the credit note." The Done step shows: "Credit note recorded," the two order numbers, the rung-by line, "Rung by Sales (demo)," and the note total. The drawer's expected cash dropped by exactly the note's total, the server's figure.

The leg's other moves:

- The swap. Ring a sale: pick Bay Ridge, search "holland," set quantity to forty, ring. Open Returns, search "Bay Ridge," pick the customer. The order's first line is the one Holland Paver at forty. Press "Return to stock" on the first line. Press "Continue to the refund." The swap form opens. Search "Aluminum" for the replacement goods. Pick "Aluminum Edging 8 ft (demo)." The swap's new total and the leftover are the server's preview. Press "Ring the swap." The Done step shows: "Swap rung," the credit and the new sale point at each other, the credit's line, the swap's sale total matching the preview's new total. The drawer's expected cash dropped by exactly the leftover the Done step states.
- The walk in return. Sign in as Sales. Ring a sale with no customer: search "edge" and pick "Aluminum Edging 8 ft (demo)." Ring with cash. Open Returns. Press "Walk in sale." The desk shows the receipt or order number field. Type the walk in sale's order number. Press "Find the sale." The desk moves to the lines step, with the walk in sale's order. The "On the account" rail is disabled; the rail's reason is the matrix's own: a walk in sale has no account to credit. Press "Cash." Press "Record the credit note." The Done step shows the walk in credit's total; the drawer's expected cash dropped by exactly the credit's total.

## What the moves do in the app's own words

- Customer step. The search bar's label is "Search name or account number." A walk in sale's button is "Walk in sale."
- Lines step. The step's data step is "lines." Each order is a `returns-order` row, with the order number, the lines, and the pick button "Return to stock" (a disposition choice that lets the draft answer the server's restock flag). The credit count is "2 lines from 2 orders." The credit total is the matrix's own number, the server's preview.
- Refund step. The step's data step is "refund." The three rails are the matrix's three: "Cash," "Card," "On the account." The record button is "Record the credit note." The swap's record button is "Ring the swap."
- Done step. The step's data step is "done." The credit's notice is "Credit note recorded." The swap's notice is "Swap rung." The note's total carries its own test id (`returns-note-total`); each child return carries its own (`returns-done-return`); the swap's pair of documents point at each other (`returns-swap-link`, `returns-swap-sale`).
- The walk in rail. "On the account" is disabled. The reason is the matrix's own: a walk in sale has no account to credit.

## What the role can not do

The clerk's reach is the returns desk on the chrome, the branch the front door signed in at, the desk's own moves. The walls are:

- A return on a card the original sale did not pay by is refused. The card rail is disabled; the reason is in words beside it. A line of a return on a card sale's only card-refundable order holds the rail live only when every order in the note is card-refundable; an order that is not card-refundable is named in the desk's words.
- The walk in rail is "On the account" disabled. The reason is in words beside it: a walk in sale has no account to credit.
- A cash refund needs the open drawer. The rail is disabled and the reason is in words beside it when the drawer is closed; the desk's "Cash is paid from the open drawer, and no drawer is open at this branch" is the server's word.
- A card shortfall on a swap needs a card. The rail is refused in words when the simulator's card is not available.
- A quantity above the line's open figure is refused in the draft, before the wire, in the server's own words. A blank, zero, negative or unreadable quantity takes the line out of the draft, in the draft's own words.
- A branch the clerk is not signed in at is out of scope. A pick from the wrong branch is refused in the draft, in the server's own words.

A wall sentence the app does not carry keeps the server's own word: an honest refusal beats an invented permission.

## What is "later"

The clerk's own moves (cancel a credit note, void a receipt, edit a credit note after the Done step) are out of scope on this round. The desk's other modules (the discount, the hot list, the change order) are not on the returns desk. The drawer the cash rail needs is the branch's MAIN counter drawer, the figure the till opens it with (the seed's own $300.00 opening float).

## Where feedback goes

One channel: send everything to Colton, and he files every note with the principal in one place. What helps most: what you tapped, what you expected, and what you saw instead. The older demo's feedback path stays open too, but what you say about this box is what decides what gets built next.
