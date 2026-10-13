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

## Statements slice (`src/finstmt/`)

Ported from the original app's financial statements screen (`finstmt-home.ts`, `finstmt-income.ts`, `finstmt-balance.ts`,
`finstmt-gl-detail.ts`, `finstmt-aging.ts`, `finstmt-api.ts`, `finstmt-export.ts`, `_finstmt-heading.ts`), read only, at the
commit the cycle names. The logic ports as plain code onto HH's generated api-client; the screens are rebuilt on HH's design
system against the Money Desk references (`hh-sup/design-refs/money-desk/`, trial-balance for the frame).

| What | Source path in the original app | Lands in | What changed |
| --- | --- | --- | --- |
| The six tabs and their titles | `app/src/_finstmt-heading.ts` | `TABS`, `heading` | Plainer names ("Saved packs" for the schedules registry); the title, period and scope are on the page and the printed sheet |
| Period words | `formatPeriod` in `finstmt-export.ts` | `periodLabel` | The books' own words (fiscal year, numbered period) from the ledger's period list; no month names in code; each period marked closed or open |
| Income statement, balance sheet, trial balance | `finstmt-income.ts`, `finstmt-balance.ts`, `finstmt-home.ts` | `incomeView`, `balanceView`, `trialView` | The server's totals and flags are shown, never recomputed; compare with the year before reads the contract's comparative rows |
| Drill down to the ledger and the tie | `finstmt-gl-detail.ts` | `detailView`, `tieWords` | The tie is the server's `tie_cents` held against the line's figure, said in words either way |
| Receivables aging | `finstmt-aging.ts` | `agingView` | Five bands in the core's order, cards on a phone |
| Saved packs (schedules) | `finstmt-home.ts` schedules panel | `packsView`, `packForm`, `validatePack` | Change and delete added; delete asks first; the form is checked in the core's order before anything is sent |
| CSV export | `finstmt-export.ts`, `_csv.ts` | `exportCsv`, `defangCsv` | The core's own CSV fetched as text through the staff client so the core records the export, then read and written again so no cell can run as a formula; the comparison, which the core answers as JSON, is made from the rows on the page |
| Print | `renderStatementHtml`, `printStatement` | the print stylesheet | A print stylesheet on the page itself rather than a hidden frame |

Not ported on purpose: the Excel download (bytes, not text),
the entity name on the sheet (the app holds no tenant name), a cash flow statement (the contract has none; marked later in
words), and sending packs by e-mail (the core keeps a registry only; marked later in words).

Contract notes: the core writes the saved packs under `schedules` while the contract's page names them `items` (recorded in
the core's shape census); the slice reads both and refuses anything else. Saving a pack carries no idempotency key (the
contract's route takes none), so the screen holds a second press while one is in flight and, when no answer comes back, says
the pack may or may not be saved.

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
