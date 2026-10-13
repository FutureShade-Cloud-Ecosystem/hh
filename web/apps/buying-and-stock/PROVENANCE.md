# Buying and Stock: provenance of the ports

The procurement slice (`src/po/`) carries logic and words from the original
HardscapeOS app (repository `hardscapeos_dibbits`, read only), commit
`f8f4973db317b92296ae8ec7e62a3f54f5dc82ab`, under the roadmap's row 563 rule
(take the existing and improve it). The logic ports as plain code onto HH's
generated api-client; the screens are rebuilt on HH's design system and the
role app template's shell, held to the procurement references in
`hh-sup/design-refs/procurement/` first, the Buying and Stock board second.

| What | Source path in the original app | Lands in | What changed |
| --- | --- | --- | --- |
| Reorder worklist (the shortfall grading, its columns, the worst first order) | `app/src/inventory-replenish.ts` | `worklist`, `worklistRow` in `src/po/po-views.ts` | One row picked at a time and drafted through the reorder PO route; the hot row is a negative projected position; low priority columns drop on narrow screens |
| PO register, order states and their words | `app/src/purchasing-console.ts` | `register`, `statusWord`, `statusTone` in `src/po/po-views.ts`, `src/po/po-logic.ts` | States are outline words, never buttons; no money is summed in the app |
| New PO form and PO detail | `app/src/purchasing-console.ts` | `draftPanel` in `src/po/po-views.ts` | The draft starts from the shortfall row with the server's suggested quantity; Cancel draft, Save draft and Place order |
| Incoming (what is on order, with the promised date) | `app/src/purchasing-console.ts` | `incoming`, `incomingWords` | A missing date reads "no date", never an invented one |
| `formatQty` and the dollars formatter | the Counter Till's `_money-format` port in pos-kit | `src/po/po-money.ts` | Absent figures read as placeholders |
| Visual references | `walkthrough-w4b/`, `walkthrough-w4c/`, `walkthrough-472/` | the screens | Rebuilt to HH's design system, light and brand |

The inventory and counts areas (`src/inventory/`, `src/counts/`, the area bar
in `src/app-root.ts`) continue the same port from the original app's inventory
home and count screens, same commit, same rule.

| What | Source path in the original app | Lands in | What changed |
| --- | --- | --- | --- |
| The area bar (the sub-tab navigation over the stock spine) | `app/src/inventory-home.ts`, `app/src/inventory-console.ts` | the areas nav in `src/app-root.ts`, the tabs in `src/inventory/inventory-views.ts` | One switcher holds all seven areas (Purchase orders, Receiving, Vendors, Vendor bills, Landed cost, Inventory, Cycle counts); the list is `#newAreas` in `src/app-root.ts`, so an area adds one row |
| Stock lookup (the on-hand buckets, the move ledger, the graded empty state) | `app/src/inventory-on-hand.ts`, `app/src/inventory-stock-logic.ts` | the lookup pane in `src/inventory/inventory-views.ts` | The product picker is the catalogue select (by SKU and name), never a bare id field; the one-state-at-a-time rule (error before counts) is kept |
| The move ledger's words | `app/src/inventory-logic.ts` | `moveTypeWord`, `moveDirectionWord` in `src/inventory/inventory-logic.ts` | One vocabulary matrix, honest degradation for an unmapped state |
| Browse (rows, facets, the honest total) | `app/src/inventory-browse.ts` | the browse pane | Facets render as counted chips; the pager's echoed limit and offset are the server's |
| Branch stock (the four buckets, the negative on hand named) | `app/src/inventory-branch-stock.ts` | the branch stock pane | The sold-before-receipt row is named where it stands |
| Pending fulfilment | `app/src/inventory-pending.ts` | the pending pane | The mode reads as words (Delivered, Collect later) |
| Reservations and the allocation policy | the reservations half of `app/src/inventory-branch-stock.ts`, `app/src/inventory-stock-api.ts` | the reservations pane | Hold, release and the expiry sweep on one screen; the policy's modes read as words |
| Transfers (raise, ship with the route's day, receive, no cancel once shipped) | `app/src/inventory-transfers.ts`, `app/src/inventory-stock-logic.ts` | the transfers pane, `transferActions`, `transitDaysFor`, `defaultShipETA` | The ship confirm names the route and its days; an unconfigured route pre-fills nothing, never an invented date; receive and cancel each ask first, naming the transfer, the yard and the quantity in transit |
| Transit times table and the admin wall | the delimited admin section of `app/src/inventory-transfers.ts` | the transit table | Read only on this box: the write is the tech admin's tier, and the screen says so in words |
| Receiving (the whole outstanding, confirm first) | `app/src/inventory-receiving.ts` | the receive pane | The confirm names the order, the yard and every line's quantity before the stock lands |
| Replenishment (the managed lines, the nudge) | `app/src/inventory-replenish.ts`, `app/src/inventory-stock-api.ts` | the replenishment pane | The point editor names the product, the branch and the figures it starts from; the worklist itself stays in the Purchasing area |
| Count sheets and their states | `app/src/count-sheets.ts` | the sheets pane in `src/counts/counts-views.ts` | The five states read as words, the scope as the counter says it |
| Count entry (freeze, blind count, reasons) | `app/src/count-entry.ts` | the entry pane | Blind by default; every variance needs a reason before the sheet submits |
| Variance and post (the maker-checker gate) | `app/src/count-variance.ts` | the variance pane, `confirmPostWords` in `src/counts/counts-logic.ts` | The post confirm names the sheet, the yard and each moved line (product, book to counted) and states no dollar figure before the core has valued it; cancel asks first; a count is zero or more |
| Inventory to GL reconciliation and the negative stock | `app/src/inventory-recon.ts` | the recon pane | The run-once rule is the server's; the history and the negative stock sit under the run |

The transit-times write has no control on this box at any role: the tier gate alone
holds that wall.

## Landed cost (`src/landed/`)

| What | Source path in the original app | Lands in | What changed |
| --- | --- | --- | --- |
| Landed cost list, its statuses and the bases | `app/src/landed-cost.ts`, `app/src/landedcost-api.ts` | `list`, `statusWord`, `basisWord` in `src/landed/` | Counts beside the list; a filter by state; the figures are the core's, never summed in the app (freight, duty and handling stand in their own columns) |
| Capture form (freight, duty, handling, USD with a manual rate, the basis, special buys, late freight) | `app/src/landedcost-capture.ts` | `capture`, `buildCreate` | The order is picked from the register (the original typed a reference and no order id, so an apply was refused 422); the branch comes from the order; amounts parse through the decimal string; a late freight counts what is left per received line, picked from the order's own lines instead of typed product ids |
| Allocate (per basis preview, apply, retro, void) | `app/src/landedcost-allocate.ts` | `detailView`, `confirmWords`, `settleWords` | A confirm step before apply, late freight apply and void, naming the order, the basis and the server's total; the screen says the compared basis is not the one applied (the original's note implied it was); each press re-reads the document first and settles against what the core holds, because the core answers an apply of a document that is not a draft like a real one |

## Vendors (`src/vendors/`)

| What | Source path in the original app | Lands in | What changed |
| --- | --- | --- | --- |
| Vendor records (list, add, correct, active or inactive) | `app/src/purchasing-console.ts`, `app/src/purchasing-api.ts` | `src/vendors/` | A search and a show inactive switch; a correction sends only the fields that changed; the list never carries the email on file, so the screen says so and clearing it is its own act; the checks mirror the core's validators and its refusals are shown as it words them |
| Ordering contact (the desk an order is sent to) | `app/src/purchasing-console.ts` | `contactTab`, `contactBody` | Tag, change and remove, the removal behind a confirm step; a phone only contact is kept and the screen says it cannot be mailed |
| The vendor's own codes for our products | `app/src/purchasing-console.ts` | `codesTab`, `mappingBody` | Add, correct and remove from a picker over the catalogue; the unit and the number of ours it holds are asked for together |
| The order a vendor quoted back | `app/src/purchasing-logic.ts` | `lookup`, `refWords` | Found by the number however the vendor wrote it |

## Receiving (`src/receiving/`)

| What | Source path in the original app | Lands in | What changed |
| --- | --- | --- | --- |
| The receiving queue (orders with something still to arrive) | `app/src/inventory-receiving.ts`, `app/src/purchasing-console.ts` | `queue` in `src/receiving/receiving-views.ts` | Soonest promised first, as the core orders it; the value open is the core's and is said to be hidden where the policy hides cost |
| Receive goods against an order | `app/src/inventory-receiving.ts` | `receiveBody`, `receiveConfirmWords`, `receiveBlock` | A per line count checked in the core's own words (including its over receipt sentence at four decimals), a confirm step naming every line, and no retry of a lost answer: the order is read again instead, because the receive carries no key |
| Receipts and a landed cost attached to one | `app/src/landed-cost.ts` | `receiptPanel`, `attachBody`, `attachConfirmWords` | One landed cost per receipt, applied at once, behind a confirm step that lists each amount and adds none |
| The order document and the send to the vendor | `app/src/purchasing-document-api.ts`, `app/src/purchasing-document-logic.ts` | `documentAndSends`, `sendOutcome` | The document downloads as a spreadsheet or to print; a send names the desk it goes to before it goes, asks for the fallback address only when the core says so, keeps one key for one attempt across every retry, and reads the delivery status because the request succeeds even when the delivery failed |

## Vendor bills and the three way match (`src/bills/`)

| What | Source path in the original app | Lands in | What changed |
| --- | --- | --- | --- |
| Bill list with the match state and the filters | `app/src/purchasing-console.ts` | `list`, `billRow` in `src/bills/bills-views.ts` | Drafts to post, in variance and posted counts beside the list; the filters narrow by state and by match; a read only seat is told in words who enters and posts |
| Bill entry against an order | `app/src/purchasing-logic.ts` | `entry`, `billBody`, `prefillQty`, `prefillCost` | The form starts from what has arrived and not been billed at the order's price; amounts through the decimal string; the vendor's own rate for a USD bill; no instalment schedule is entered (an empty one is legal: the bill is due in full), existing ones are shown |
| Three way match, line by line | `app/src/purchasing-logic.ts` | `matchLine`, `qtyVarianceWords`, `priceVarianceWords` | The verdict and both variances are the core's figures, said in words; the app recomputes none |
| Post, and accept a variance | `app/src/purchasing-console.ts` | `postActions`, `postConfirmWords`, `acceptWords` | A confirm step naming the bill, the vendor, the order and the server's total; the bill is read again before and after the press (no key exists, the status is the guard); a variance is refused by the core with its figure, and the acceptance asks for a note that is kept with the bill |
| Match tolerance and cost policy | `app/src/purchasing-console.ts` | `rules`, `toleranceBody`, `policyBody` | Read by everyone in a sentence; changed only by the super admin behind a confirm step, with the core's own 25 percent ceiling |

Not ported on purpose: the vendor bill instalment schedule is read but not entered (the
total it must equal is the server's, and an empty schedule is legal); transfers and counts are
the inventory slice's. The bill entry, post and tolerance routes sit on the Money Desk's surface
in the contract (x-access), so the bills area offers those controls only to the seats that hold
them, and everyone else reads the bills and the match and is told in words who posts.
