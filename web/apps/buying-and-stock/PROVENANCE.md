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
| The area bar (the sub-tab navigation over the stock spine) | `app/src/inventory-home.ts`, `app/src/inventory-console.ts` | the areas nav in `src/app-root.ts`, the tabs in `src/inventory/inventory-views.ts` | Three areas on one bar (Purchasing, Inventory, Cycle counts); the list is a table so landed cost and the purchasing back office add one row later |
| Stock lookup (the on-hand buckets, the move ledger, the graded empty state) | `app/src/inventory-on-hand.ts`, `app/src/inventory-stock-logic.ts` | the lookup pane in `src/inventory/inventory-views.ts` | The product picker is the catalogue select (by SKU and name), never a bare id field; the one-state-at-a-time rule (error before counts) is kept |
| The move ledger's words | `app/src/inventory-logic.ts` | `moveTypeWord`, `moveDirectionWord` in `src/inventory/inventory-logic.ts` | One vocabulary matrix, honest degradation for an unmapped state |
| Browse (rows, facets, the honest total) | `app/src/inventory-browse.ts` | the browse pane | Facets render as counted chips; the pager's echoed limit and offset are the server's |
| Branch stock (the four buckets, the negative on hand named) | `app/src/inventory-branch-stock.ts` | the branch stock pane | The sold-before-receipt row is named where it stands |
| Pending fulfilment | `app/src/inventory-pending.ts` | the pending pane | The mode reads as words (Delivered, Collect later) |
| Reservations and the allocation policy | the reservations half of `app/src/inventory-branch-stock.ts`, `app/src/inventory-stock-api.ts` | the reservations pane | Hold, release and the expiry sweep on one screen; the policy's modes read as words |
| Transfers (raise, ship with the route's day, receive, no cancel once shipped) | `app/src/inventory-transfers.ts`, `app/src/inventory-stock-logic.ts` | the transfers pane, `transferActions`, `transitDaysFor`, `defaultShipETA` | The ship confirm names the route and its days; an unconfigured route pre-fills nothing, never an invented date |
| Transit times table and the admin wall | the delimited admin section of `app/src/inventory-transfers.ts` | the transit table | Read only on this box: the write is the tech admin's tier, and the screen says so in words |
| Receiving (the whole outstanding, confirm first) | `app/src/inventory-receiving.ts` | the receive pane | The confirm names the order, the yard and every line's quantity before the stock lands |
| Replenishment (the managed lines, the nudge) | `app/src/inventory-replenish.ts`, `app/src/inventory-stock-api.ts` | the replenishment pane | The point editor names the product, the branch and the figures it starts from; the worklist itself stays in the Purchasing area |
| Count sheets and their states | `app/src/count-sheets.ts` | the sheets pane in `src/counts/counts-views.ts` | The five states read as words, the scope as the counter says it |
| Count entry (freeze, blind count, reasons) | `app/src/count-entry.ts` | the entry pane | Blind by default; every variance needs a reason before the sheet submits |
| Variance and post (the maker-checker gate) | `app/src/count-variance.ts` | the variance pane, `confirmPostWords` in `src/counts/counts-logic.ts` | The post confirm names the sheet, the yard, the line count and the net value |
| Inventory to GL reconciliation and the negative stock | `app/src/inventory-recon.ts` | the recon pane | The run-once rule is the server's; the history and the negative stock sit under the run |

Not ported on purpose (later, named on screen as Later): the vendor screens
(the core has no vendor create or edit route yet), vendor bills and the three
way match (the Money Desk's), and landed cost. There is no vendor bill control
at all; the screen says in words who posts a bill. The transit-times write has
no control on this box at any role: the tier gate alone holds that wall.
