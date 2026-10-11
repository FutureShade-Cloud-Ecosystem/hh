# Yard Pick: provenance of the ports

Everything here that was carried from the original HardscapeOS app (repository
`hardscapeos_dibbits`, read only) came from commit
`f8f4973db317b92296ae8ec7e62a3f54f5dc82ab`, under the roadmap's row 563 rule
(take the existing and improve it): the logic ports as plain code onto HH's
generated api-client, and the screens are rebuilt on HH's design system and the
role app template's shell. The same facts sit in the header of each file below.

| What | Source | Lands in | What changed |
| --- | --- | --- | --- |
| Task lifecycle, `nextAction`, `actionLabel`, `actionDone`, `statusTone`, `isOpen`, `queueOrder`, `ageLabel`, `qtyText`, `shortageSummary`, `ticketIsFixture`, `priorityWord`, `mtoLabel`, `ticketHandles`/`orderHandle` | `app/src/yard-logic.ts` | `src/yard-logic.ts` | Dashes removed from words; the queue's own split into unclaimed and claimed halves added; a uuid never reaches a picker's hands |
| The stage form's rule | `app/src/yard-logic.ts` (`validateStage`) | `validateStage` | Wording kept, the dash replaced |
| The shortage sheet's rule | `app/src/yard-logic.ts` (`validateShortage`) | `validateShortage` | The description is the line's own, chosen on the sheet, so the check is the quantity alone |
| The recorded pick: `lineIsShort`, `validatePickQty`, `pickSummary`, `pickedByLine` | `app/src/yard-logic.ts` | `src/yard-logic.ts` | Zero stays an answer; the summary keeps saying what was pulled, not what is missing |
| API call set | `app/src/yard-api.ts` | `src/yard-api.ts` | Over the generated client; every read goes through a whitelist projection that drops the two contract-declared money fields (the held ticket's owed figure, the skid answer's deposit figures); pending tickets, weights, receiving and gaps are the later increments and are not called |
| The queue board, the pick sheet, the shortage sheet | `app/src/yard-console.ts`, the boards' screens `walkthrough-w2b/`, `walkthrough-w3c/`, `walkthrough-w6a/08` | `src/views.ts` | Rebuilt for 390 px and 1024 px, gloves and sunlight, on the design system; the desktop two-pane console, the pager's totals, the rack tag and will-call doors are not the phone's first increment |
| Short pick reasons and their phrases | HH core, `core/internal/tasks/yard_short.go` and `yard_notice.go` | `SHORT_REASONS`, `reasonPhrase` | The core's own closed list and sentence forms, not the original app's free text |
| The short pick's answer words | HH core, `core/internal/tasks/yard_notice.go` (`shortPickMessage`) | `shortPickWords` | The numbers come from the server's record and the re-read ticket's backorder figure; the money sentence the rep also receives never reaches the yard |
| The staged words | HH core, `core/internal/tasks/yard_service.go` (the counter event) | `actionDone("stage")` | The two facts the core's own event carries: the spot and the ref |
| The shortage resolvers wall's names | HH core, `core/internal/roleconfig/default.json` (the `shortage_resolvers` list), PR 274's wiring | `RESOLVE_WALL_NAMES` | The matrix's own labels, so the refusal names who may, in words |
| The role gate's shared Forbidden body | HH core, `core/internal/platform/authz/middleware.go` | `FORBIDDEN_BODY`, `yardError` | The app maps that bare body to the plain words naming the list |
| Bundle fence word list | `app/scripts/bundle-fence.mjs` (by way of HH Drive's `test/_lib/fence.mjs`) | `test/_lib/fence.mjs` | HH Drive's widened list, unchanged |

The boards are FB Design's "HH: field apps" (proposed): the pick queue in
light and brand, the pick with the skid stepper and the reason, and the short
pick sheet, drawn on the phone. The receive screen on the same board is the
second increment's, not this one's.

The stub's world (Trenton, five picks, a held order, an open shortage) is the
demo world's shape: the branch is the populate's own Trenton id, the persona
is the flows table's yard-team row (PR 274), and every refusal sentence is
the core's own, held to the core's source by the contract test.
