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

Not ported on purpose (later, named on screen as Later): receiving a truck
(the yard team's, Yard Pick and Receive), the vendor screens (the core has no
vendor create or edit route yet), vendor bills and the three way match (the
Money Desk's), landed cost, transfers and counts. There is no vendor bill
control at all; the screen says in words who posts a bill.
