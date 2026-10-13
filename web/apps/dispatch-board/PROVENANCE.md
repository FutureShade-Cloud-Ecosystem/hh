# Dispatch and Load Building: provenance of the ports (parts A and B)

The load board and the day board carry logic and words from the original HardscapeOS app
(repository `hardscapeos_dibbits`, read only), master 87d7941, under the roadmap's row 563 rule
(take the existing and improve it). The logic ports as plain code onto HH's generated api-client;
the screens are rebuilt on HH's design system and the role app template's shell.

Visual reference: no FB Design board exists for dispatch yet, so the reference is the original's
newest dispatch screens (`dispatch-loadboard.ts`, `dispatch-board.ts`) rebuilt at the standard HH
Drive and Buying and Stock set: cards border first with no shadow, status as a word before a
colour, figures in tabular columns, 44 pixel targets, light by default and brand one tap away,
one column at 390 pixels. Screens are looked at in `hh-s4/modules/reports/dispatch-a-shots/`.

| What | Source path in the original app | Lands in | What changed |
| --- | --- | --- | --- |
| Lifecycle matrix, stop and run labels, words inside a sentence | `app/src/dispatch-logic.ts` | `src/dispatch-logic.ts` (`stopActions`, `STOP_LABEL`, `RUN_LABEL`, `stopWord`) | The proof of delivery action stays in the matrix but is a note on the board: the driver signs on HH Drive |
| Capacity bands, bars, gauge words | `app/src/dispatch-load-logic.ts` | `capacityBand`, `worstBand`, `barPct`, `bandWord`, `gauge` in the views | A bar's width is a class step (the position rules allow no bound style) |
| Requested and committed words, the ask beside the promise | `dispatch-load-logic.ts` | `dateStateLabel`, `askDiffers` | Same rule, plain words |
| The commitment: who may change a committed load, and the sentence that says why | `dispatch-load-logic.ts` | `rowEditable`, `commitBlockedReason`, `stopBlockedReason`, `reopenNotice` | Sentences lose their dashes; the caller flag, never the policy, draws a control |
| Quick cards, their filters and their scope words | `dispatch-load-logic.ts`, `dispatch-loadboard-surface.ts` | `cardCount`, `cardMatches`, `CARD_SCOPE`, `filterBoard`, the quick card row | The curation substrate is not ported: the cards, the axis and the views are the defaults |
| The keyboard path: every truck a load could go on, with the gauge it would have | `dispatch-load-logic.ts` | `assignTargets`, `targetLine`, the Put on a truck select | A select with each option naming its projected gauge; a blocked option says why |
| Status axis | `dispatch-load-logic.ts` | `groupByStatus`, the By status lanes | Same data, re-laned without a second read |
| Bulk load of many trucks and its per load outcome | `dispatch-load-logic.ts`, `dispatch-loadboard-api.ts` | `loadableStops`, `unloadableSummary`, `bulkLoadLabel`, `bulkLoadOutcome` | Refusals name the load and carry the core's sentence |
| Reschedule check | `dispatch-logic.ts` | `validateReschedule`, `rescheduleBlockedReason` | Inline sheet, not a dialog |
| Payment hold in words | `dispatch-logic.ts` (`paymentBannerText`) | `holdWords` | A hold is a hold: the amount shows only when the core sent one |
| The day board by stage, crew pickers, dispatch, cancel, complete | `app/src/dispatch-board.ts` | `renderDayBoard` | Columns collapse to one at 390; the matrix drives every button |
| The host and its tabs | `app/src/dispatch-surface.ts` | `tabs` | Two screens now; the other tabs are named Later |

## Original routes this part covers (Appendix A of the coverage map, the dispatch rows)

Reads: `GET /api/v1/dispatch/board`, `/loadboard`, `/loadboard/gaps`, `/loadboard/calendar`,
`/trucks`, `/drivers`, `/runs/{id}/payment-holds`.
Writes: `POST /api/v1/dispatch/auto-assign` (the core answers 501 and the board says what would
answer it), `/runs`, `/runs/{id}/cancel`, `/crew`, `/dispatch`, `/complete`, `/reopen`,
`/reopen/clear`, `/reschedule`, `/stops/{id}/assign`, `/commit`, `/reopen`, `/reschedule`,
`/status`, `/loads/mark-loaded`.

## Part B

Visual reference for every part B screen: the original's screen named in its row (the newest dispatch
screens in `hardscapeos_dibbits`, read only) rebuilt at the same HH Drive and Buying and Stock set as
part A, and the FB Design file `HH: Work`, page "PROPOSED: Dispatch and Load Building", which the
lead's Sonnet visual read uses. Screens are looked at in `hh-s4/modules/reports/dispatch-b-shots/`.

| What | Source path in the original app | Lands in | What changed |
| --- | --- | --- | --- |
| Schedule a delivery: the order number box, its help line, the form's fields, the day's table | `app/src/dispatch-schedule.ts`, `order-ref.ts` | `scheduleForm`, `renderSchedule`, `validateSchedule`, `scheduleBody`, `scheduleRefusal` | The form asks for a request, never a commitment; the day is cards, not a table; a move of date rides the part A reschedule sheet; the board follows the order to its day |
| Truck and driver registry: the lists, the add forms, the type list, the advisory capacity note | `app/src/dispatch-fleet.ts` | `renderFleet`, `fleetSheet`, `truckBody`, `driverBody`, `validateTruck`, `validateDriver`, `truckLine`, `driverLine` | Adds the update routes (change, out of use, back in use) the original never had; capacity typed in pounds, volume in cubic yards, shown in tons and pounds; out of use rows stay listed; the staff login link is a plain id field (the staff list route is the dealer admin surface's, which the dispatch roles do not hold) |
| The date move and its reason | `app/src/dispatch-reschedule.ts` | the part A `validateReschedule` and sheet, now also reached from the schedule tab | One sheet, two doors |

## Not ported on purpose (part B or never)

The schedule (`dispatch-schedule.ts`, which turns an order into a stop: the board starts from
stops), the truck and driver registry, will call, the settings tab and the policy, the freight
split and raise, the load facts editor, the manifest print, the pickups list, the driver view
(HH Drive is it), and the curation substrate behind the cards.
