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

Not ported on purpose (later, named on screen as Later): receiving a truck
(the yard team's, Yard Pick and Receive), the vendor screens (the core has no
vendor create or edit route yet), vendor bills and the three way match (the
Money Desk's), landed cost, transfers and counts. There is no vendor bill
control at all; the screen says in words who posts a bill.
