# Money Desk: provenance of the ports

The receivables slice (`src/ar/`) carries logic and words from the original
HardscapeOS app (repository `hardscapeos_dibbits`, read only), commit
`f8f4973db317b92296ae8ec7e62a3f54f5dc82ab`, under the roadmap's row 563 rule
(take the existing and improve it). The logic ports as plain code onto HH's
generated api-client; the screens are rebuilt on HH's design system and the
role app template's shell. The same facts sit in the header of each file below.

| What | Source path in the original app | Lands in | What changed |
| --- | --- | --- | --- |
| Aging cards, their names and day ranges | `app/src/ar-landing.ts`, `app/src/ar-landing-surface.ts` | `cardTitle`, `cardRange`, `cards` in `src/ar/ar-logic.ts`, `src/ar/ar-views.ts` | A card filters the aging to the accounts the server counted (the landing's `account_ids`); words without dashes |
| Aging table by account | `app/src/ar-console.ts` | `agingTable` in `src/ar/ar-views.ts` | Column words "1 to 30", "31 to 60", "Over 60"; an On hold state beside the terms |
| Invoice register status words and pills | `app/src/ar-invoice-register.ts` | `statusWord`, `statusTone` in `src/ar/ar-logic.ts` | A word always beside the tone; one open invoice panel per account view |
| Record a payment, promise to pay | `app/src/ar-invoices-api.ts`, `app/src/ar-invoice-register.ts` | `src/ar/ar-api.ts`, `src/ar/ar-desk.ts` | Over the generated client; one idempotency key per attempt; amount parsed through the decimal string |
| Bounced payments queue and mark a bounce | `app/src/ar-api.ts`, `app/src/ar-console.ts` | `bouncedTab`, `bouncedWork`, `markBounced` | A payment is marked from the invoice it paid, not by typing a payment number; working it opens the payment's history and the account |
| Credit holds and the release | `app/src/ar-console.ts` | `holdsTab`, `releaseHold` | The release is wired so the refusal is the server's, shown in words naming who may do it |
| Statement for an account | `app/src/ar-monthend-api.ts`, `app/src/ar-monthend.ts` | `statementCard` | Latest statement or none, and a run for the account's open invoices |
| `parseAmountToCents` | the Counter Till, `web/apps/counter-till/src/money.ts` | `src/ar/ar-money.ts` | Same parse, same refusals |
| Visual references | `verification/ar-landing/`, `walkthrough-w3b/` | the screens | Rebuilt to HH's design system, light and brand; no FB Design board for Money Desk existed when this was built |

Round 2 rebuilt every AR screen against the Money Desk references (`hh-sup/design-refs/money-desk/`,
receivables-home, account-open-invoices, credit-hold), which now come first; the original app's screens are second. New in
that round: the register with its status pills, Needs you, the apply column and payment panel, the statement document, the
holds table with the controller's lift (the references' words, the core's routes), and the `later` tags.

Round 3: the payment panel no longer splits in the app; the received payment route is the server's (the generated
`paymentsApplyReceived`, with the stub held to the core's sentences). Final round: one key per payment, reused on a lost or
5xx answer, which says the outcome is unknown.

Not ported on purpose: auto credit hold settings, the paper path and reminders
(Base tier extras, not this role's first increment), the pop out and duplicate
windows.

## Bank reconciliation slice (`src/bankrec/`)

Ported from the original app's bank reconciliation console (the main bankrec screen under `app/src`, `bankrec-import.ts`,
`bankrec-match.ts`, `bankrec-rules.ts`, `bankrec-shared.ts`, `_bankrec-line-actions.ts`), read only, at the commit the
cycle names. The logic ports as plain code onto HH's generated api-client; the screens are rebuilt on HH's design system
against the Money Desk references (`hh-sup/design-refs/money-desk/`, trial-balance and controller-home for the frame).

| What | Source path in the original app | Lands in | What changed |
| --- | --- | --- | --- |
| State words (account type, line, method, target, rule, month) | `app/src/bankrec-shared.ts` | `src/bankrec/bankrec-logic.ts` | Plainer words ("In use" for an approved rule, "Signed off" for a completed month); an unknown value is opened out, never hidden |
| The row action a line admits | `app/src/_bankrec-line-actions.ts` | `lineAction` | Same three way rule; an excluded line gets nothing |
| Account grid and monthly reconciliation | the main bankrec screen under `app/src` | `accountsView`, `reconRows` | Statement balance parsed through the decimal string (the original used a float multiply), signed; sign off held until the difference is zero and asked twice; Refresh figures added because the core stores the variance when the month is opened |
| Statement import | `app/src/bankrec-import.ts` | `importView` | The core's parser sentences read as what the file lacked; the result says "nothing new" on a full re-import; the card format named from the account |
| Auto match, hand match, unmatch, history | `app/src/bankrec-match.ts` | `matchView`, `linePanel` | The hand match is a panel with a target choice, not a browser prompt; a journal is picked from the ledger's posted journals; undo is confirmed; lines follow the core's cursor |
| Match rule registry and approve | `app/src/bankrec-rules.ts` | `rulesView`, `ruleSpec` | A form for the spec the core stores instead of a JSON box; a stored rule fills the form to approve the next version |
| `parseBalanceToCents` | the receivables slice's `parseAmountToCents` | `src/bankrec/bankrec-money.ts` | Same parse, with a minus sign or brackets for a negative |

Not ported on purpose: excluding and restoring a line (the core has no route for either), adjusting entries (none on the
bank routes: a fee or interest is booked as a journal and the line matched to it), the bank feed, and file upload to FB
Console Storage (the contract has no storage seam on the import route: the rows ride the JSON body).
