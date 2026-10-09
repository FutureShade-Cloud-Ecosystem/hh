# Returns, the till's desk: the testers' brief

A walk of the Counter Till's returns desk for the JV team. Plain speech; the shape is the shape the seed and the acceptance legs hold, the wording is the app's own. What is "later" is named in the app's own words. Sources: the demo seed on main from PR 277 and the app's own logic in `web/apps/counter-till/src/returns/`. The acceptance legs in `web/apps/counter-till/test/returns.pw.ts` and `web/apps/counter-till/test/returns-surface.pw.ts` are the source of the named numbers.

## The role

The Counter Till's returns desk works a return against an already-ringed sale. The clerk finds the sale by its number, picks the lines to return, picks the reason and the refund method, and commits. The clerk's reach is the branch the front door signs in at. The dealer's approval amount governs the size of a cash return that does not need a manager's hand; a return above the amount is refused in words and the dialog asks the manager to approve on the clerk's chrome, the till stays the clerk's, the receipt names the manager and the clerk.

## How to sign in

On the front door, tap the Sales card. The desk inside is the till, the new counter sale on the entry. The bar at the top of the desk carries a Returns entry, separate from the sale entry. Tap Returns and the desk lands on the returns surface, at the hash `#/returns`. The sale is held in custody, the keyboard's own keys stay quiet while the returns desk is up. The sign-in rides the front door's handoff so no second sign-in is asked for.

A card marked Coming cannot be tapped yet. The clerk's other cards sit on the same door.

## The demo world

The returns world is the day's ringed sales at the dealer's branch. The acceptance leg's seed plants at least two cash sales and at least one card sale, each with the same line shapes the returns desk expects to find. The Wall block line at six pieces is the figure that lands over the dealer's $250.00 approval amount; the Holland paver line at one is the figure that lands under it; the Garden edger lines (grey and sandstone) are the card sales' returnable lines.

The demo dealer's configuration turns the approval rule off (decision log rows 1047 and 1073), and the stub starts the same way, so the legs that exercise the approval path switch the stub to a dealer that keeps the 250.00 rule first and put it back after; one leg at the end pins the demo's own side, a refund over 250.00 committing as the clerk with no approval.

## The surface and the four moves

The returns surface lives at `#/returns`. The bar carries a Returns entry; the sale's bar carries a Returns entry, too, so the clerk can pop in and out of the desk without losing the sale's state. The four moves the clerk makes are:

1. Find sale. Type the sale number. The desk looks up the sale and lists its returnable lines, the quantity sold and the quantity already returned for each line.
2. Pick the lines. Type the quantity to return for each line, with a reason from the contract's list (DAMAGED, DEFECT, WRONG_ITEM, NO_LONGER_NEEDED, OTHER with a note).
3. Pick the refund method. Cash, Card (only when the original sale was paid by card; the rail is disabled and the reason is in words beside it), or Account credit. A cash sale has no card capture to reverse; the card method is greyed, the reason is in words beside it.
4. Refund. Press Refund cash, Refund card or Refund to account. The receipt appears in the desk's foot, the drawer's Z report drops by the refund's amount, the audit row holds the clerk and the approver.

## A short walk to try

The acceptance leg `web/apps/counter-till/test/returns.pw.ts` is the whole walk in one:

- Sign in as Sales. The till's new counter sale is on screen. Press the Returns entry in the bar. The desk lands on the returns surface, the sale's bar carries the same Returns entry. The URL's hash is `#/returns`.
- Find sale. Type CT-1001. Press Find sale. The desk lists the Holland paver line (sold 2, returned 0) and the Wall block line (sold 6, returned 0). The card method is greyed; the reason is in words beside it: "No card refund: this sale was not paid by card."
- Pick the Holland paver line. Type 1 in the quantity field. The line is "1 to return."
- Press Cash. The card method is greyed; the cash method is on.
- Press Refund cash. The receipt appears: "$60.00, cash, from the drawer, Rung by Sales (demo)." The drawer's Z report dropped by 6000 cents.
- Press Find sale again. Type CT-1002 (the Wall block cash sale). Press Find sale. The Wall block line is "sold 6, returned 0." The line is the over-the-amount one.
- Type 6 in the quantity field. Press Cash. Press Refund cash. The refusal appears: "$250.00, Sales manager, Branch manager." Press Manager approval. The dialog opens. Press Sales manager. The receipt appears: "$360.00, Approved by Sales manager (demo), Rung by Sales (demo)." The till is still the clerk's.
- Find sale. Type the first card sale. The Garden edger (grey) line is on. Type 1. Press Card (the rail is live because the original sale was by card). Press Refund card. The receipt appears: "$89.00, card, Rung by Sales (demo)."

The acceptance leg's other moves:

- A return on the manager's chrome: sign out as Sales, sign in as Sales manager. Find the same sale. The return completes with no refusal and no dialog. The receipt names the manager as the one who approved it.
- A second find clears the last draft's fields on the screen and in the body. A changed draft drops the refusal it earned and the idempotency key it rode in on.
- A plain refusal without the approval key shows its own words and opens no dialog.
- A return on the simulator's true side shows the simulated notice. A return on the false side makes no simulated claim.

## What the moves do in the app's own words

- Find sale: "Found <number>." A sale not found answers in words: "That sale is not in the register."
- Refund cash: the receipt names the amount, "cash, from the drawer," and the clerk. A card sale's refund cash is refused in words, the reason beside the disabled rail.
- Refund card: the receipt names the amount, "card," and the clerk.
- Refund to account: the receipt names the amount, "to the account," and the clerk.
- A refusal: the refusal names the amount and the approvers (Sales manager, Branch manager), in the matrix's own order. The dialog opens; the wall sentence reads: "Manager approval. The till stays the clerk's."
- A receipt on a manager's chrome: the receipt names the manager as "Approved by Sales manager (demo)" and the clerk as "Rung by Sales (demo)."

## What the role can not do

The clerk's reach is the returns desk on the chrome, the branch the front door signed in at, the desk's own moves. The walls are:

- A return on a card the original sale did not pay by is refused. The card rail is disabled; the reason is in words beside it: "No card refund: this sale was not paid by card."
- A return above the dealer's approval amount is refused. The refusal names the amount and the approvers, in the matrix's own order (Sales manager, Branch manager). The dialog asks the manager to approve on the clerk's chrome; the till is the clerk's still.
- A sign out under an in flight approval leaves the desk blank for the next person. The send's answer landing after the sign out writes nothing, the receipt included.
- A change of reason after the dialog commits drops the refusal it earned. A second refund on the same line with the same idempotency key is refused in words; a fresh draft takes a fresh key.

A wall sentence the app does not carry keeps the server's own word: an honest refusal beats an invented permission.

## What is "later"

The clerk's own moves (cancel a return, void a receipt) are out of scope on this round. The desk's other modules (the discount, the hot list, the change order) are not on the returns desk. The refund to account is on the desk, but the account credit's reconcile to the books is later.

## Where feedback goes

One channel: send everything to Colton, and he files every note with the principal in one place. What helps most: what you tapped, what you expected, and what you saw instead. The older demo's feedback path stays open too, but what you say about this box is what decides what gets built next.
