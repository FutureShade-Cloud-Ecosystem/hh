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

Not ported on purpose: the pop out and duplicate windows. (Auto credit hold settings, the paper path and reminders
were on this list through the first increment; the office rows below bring them in.)

## The receivables office (`src/ar/office-*.ts`, Session 4 item 1f)

Same original, same rule: take the existing and improve it. Reminders and statements leave only through the messaging
sink, so every screen says what would go and to which contact by its role label, and none draws an address or number.

| What | Source path in the original app | Lands in | What changed |
| --- | --- | --- | --- |
| Statement run, read back by id | `app/src/ar-monthend-api.ts`, `app/src/ar-monthend.ts` | `showRun`, `renderRunPanel` | The run's own counts and one row per statement with where it would go; read back from the server, not echoed from the press |
| AP contact tag, replace, remove | `app/src/ar-reminders.ts`, `app/src/ar-console.ts` | `renderContacts`, `validateApContact`, `contactLine` | Said as what is on file, never the address; the core's limits checked before a request is sent |
| Send a reminder, reminder history | `app/src/ar-reminders.ts`, `app/src/ar-console.ts` | `remindForm`, `historyTable`, `reminderPreview`, `reminderResultWords` | Says before the send what would go and to which contact by role; the base plan and a switched off channel are said in words; one key per attempt, kept on a lost answer; the history reads recipient kind, never the address |
| Paper path: list, sweep, close with a note | `app/src/ar-console.ts`, `app/src/ar-landing-surface.ts` | `paperPanel`, `paperResolveCheck` | A flag closes as moved to paper or dismissed only with a note, said before the request is sent; Needs you reloads |
| Send an invoice, view its document | `app/src/ar-invoices-api.ts`, `app/src/ar-invoice-register.ts` | `renderInvoiceActions`, `mountDocument` (app root) | The send goes through the messaging sink and never shows the address; the document opens in a sandboxed frame with no scripts, never set into the page |
| Open and close a dispute | `app/src/ar-invoice-register.ts` | `disputeCheck`, `renderInvoiceActions` | The amount is parsed through the decimal string and checked against the balance before the request; closing says it moves no money |
| Reverse a payment | `app/src/ar-console.ts`, `app/src/ar-invoice-register.ts` | `reversalCheck`, `renderReverse`, `reversalWords` | A reason is required before the request; the invoice reloads to the server's balance; a returned cheque stays on Mark bounced |
| Receivables rules (aging edges, reminder cadence, paper path window, plan) and their change history | `app/src/ar-landing.ts`, `app/src/ar-settings.ts` | `landingPolicyCheck`, `SettingsDesk`, `rulesPanel`, `historyPanel` | A manager reads, an administrator writes; the core's ranges and edge order checked before the request; history reads as words, never ids |
| Register settings | `app/src/ar-invoice-register.ts` | `registerPolicyCheck`, `registerPanel` | The panel split and arrangement are layout of the original and ride along unchanged; HH lays its own panels out |
| Returned payment fee | `app/src/ar-console.ts` | `nsfPolicyCheck`, `nsfPanel` | Typed in dollars through the decimal string, sent in cents; the bounce charges what was saved |
| Automatic credit hold switch | `app/src/ar-console.ts` | `autoHoldLines`, `holdPanel` | Read as words with its thresholds read only; two presses and a reason; shown but not offered when the server says the switch is not wired |
| Holds a returned payment placed, lifted by the till's path | `app/src/ar-console.ts` | `OfficeApi.releaseTillHold`, `placedBy` | The billing release refuses an NSF hold in the core's words, so the controller's Lift goes through the till's own route for one |
| Dispatch gate read | `app/src/ar-console.ts` | `dispatchWords`, `renderInvoiceActions` | Asked from the invoice that names the order, answered in the server's reason |
| Credit sweep: report or place | `app/src/ar-console.ts` | `sweepWords` | The sweep says it only reported (and how many it would hold) while automatic holds are off, instead of a flat "placed 0" |

Not wired, with the reason: `billingRecordInvoicePayment` (one payment on one invoice: `paymentsApplyReceived` replaced it in round 3 and a second path to the
same money is a way to take it twice), `billingTrackInvoiceOpened`, `billingTrackReminderOpen` and `billingTrackReminderClick` (public tracking pixels a
customer's mail client fetches, not screens), `tillRecordPaymentAttempt` (the Counter Till's declined card log; its surface is the till, not this desk).

## Ledger beyond its three reads (`src/gl/`)

| What | Source path in the original app | Lands in | What changed |
| --- | --- | --- | --- |
| Chart of accounts, add, deactivate | `app/src/gl-accounts.ts`, `app/src/gl-api.ts` | `chartView` in `src/gl/gl-books.ts`, `GlApi.accounts` | Deactivate opens a review (no route turns an account back on); inactive accounts stay listed with a word |
| New journal composer | `app/src/gl-journal-entry.ts` | `composerView`, `checkDraft` in `src/gl/gl-logic.ts` | A review step before posting; no running totals on screen (the app adds no money, the server refuses an unbalanced journal in words); a lost answer is never retried |
| Reverse guard with a summary | `app/src/_gl-workbench-rules.ts` (`reverseSummary`, `reverseNeedsConfirm`) | `reverseConfirm` | The review lists the journal's lines instead of a computed total; Void gets a review too |
| Recurring templates | `app/src/gl-api.ts` | `recurringView` | Run adds a draft for a second approver |
| Periods, close, reopen, year end close | `app/src/gl-periods.ts` | `periodsView`, confirm panels | In page reviews replace `window.confirm` and `window.prompt`; each names the period or year and what changes |
| Trial balance period, export | `app/src/gl-trial-balance.ts`, `app/src/_gl-export.ts` | `trialView`, `GlApi.exportBooks` | The server's parity export route replaces the client CSV projection; the Excel workbook stays later |
| Tie out, BisTrack check, policy | `app/src/gl-workbench.ts`, `app/src/gl-trial-balance.ts` | `checksView`, `policyView` | A word beside each colour; the BisTrack check says it reads a synthetic snapshot |
| Payables and Receivables as a reader | none | `mode: "reader"` in `src/gl/gl-desk.ts` | Reads only; writes held with the roles that may |

Visual reference: the Money Desk boards (`hh-sup/design-refs/money-desk/`, trial-balance and controller-home) for the cards and
pills; the original's GL workbench tabs second. No FB Design board for these tabs existed when this was built.

## Liens slice (`src/lien/`) and compliance slice (`src/compliance/`)

Ported from the original app's Lien Cockpit (`app/src/lien-cockpit.ts` and its five tabs: `lien-triage.ts`, `lien-jobs.ts`,
`lien-holdbacks.ts`, `lien-documents.ts`, `lien-calendar.ts`, with `lien-shared.ts` and `lien-api.ts`), read only, at the
commit the cycle names. The logic ports as plain code onto HH's generated api-client; the screens are rebuilt on HH's
design system against the Money Desk references (`hh-sup/design-refs/money-desk/`, trial-balance and controller-home for
the frame: pill tabs, bordered cards with no shadow, status as a word before a colour, a table that gives way to cards on
a phone). The original's cockpit hosted the calendar as a fifth tab; here Compliance is its own area, as the cycle's
brief names it.

| What | Source path in the original app | Lands in | What changed |
| --- | --- | --- | --- |
| Job, direction, badge, bucket and deadline words | `app/src/lien-shared.ts` | `src/lien/lien-logic.ts`, `src/compliance/compliance-logic.ts` | Stored tokens opened out into plain words; the statutory phrase "Substantially performed" kept; a word always sits beside the colour |
| Monday triage | `app/src/lien-triage.ts` | `triage` in `src/lien/lien-views.ts` | The core's note is shown as a sentence beside the row; the row opens its job; cards on a phone |
| Jobs and new job | `app/src/lien-jobs.ts` | `jobs`, `jobForm` | The contract was read as `Number(...) * 100`; it is parsed through the decimal string and refused in words when it is not dollars and cents. A blank holdback percent is the core's default. The tax on holdback box is not offered: the core's flag adds a fixed placeholder rate that is not California's |
| Sync holdbacks | `app/src/lien-jobs.ts` | `#sync` in `src/lien/lien-desk.ts` | The result says how many invoices were booked and how many already were |
| Holdback aging and release | `app/src/lien-holdbacks.ts` | `holdbacks`, `releasePanel`, `#confirmRelease` | The release asked for the co-signer's user id and role in two browser prompts; it is a pick from the staff roster (Financial Controller or Payables and Receivables, never the caller), a confirm naming the job and the amount, and the core's refusal sentences shown in words. The General Manager's gate is shown before a press and enforced again by the core |
| Documents and versions | `app/src/lien-documents.ts` | `documents`, `renderForm` | The merge data was a line of JSON; each form shows its own fields, the core's labels. The latest of each form lists, History reads every version, newest first |
| Release calendar | `app/src/lien-calendar.ts` | `upcoming`, `calendar` in `src/compliance/compliance-views.ts` | The upcoming list takes a look ahead typed as a whole number; the calendar reads the upcoming list's overdue mark, because the core stores a past due deadline as open |
| Shared review status banner | `app/src/lien-shared.ts` (`bannerFor`) | `bannerStrip`, `banner` | The banner words and state are the core's; Compliance can set them, for the whole cockpit or for one job |
| `parseAmountToCents` | the receivables slice's `src/ar/ar-money.ts` | `src/lien/lien-money.ts` | Same parse; a holdback percent parses to basis points the same way, through the string |

Choices the cycle's brief left open: the lien screens carry no idempotency key because the contract has none (the sync is
idempotent on the invoice, the release's journal on the job, a render appends a version by design). The core answers an
empty list as `{"items": null}`; the slice reads that as an empty list and the stub copies it. The review status for the
whole cockpit keeps the original's one subject id, so rows the core already holds still match.

Not ported on purpose: a customer pick on the new job form (the customer search is the Counter Till's and Quote Desk's
surface, not the Money Desk's, so a job is made with no customer and its sync sweeps the whole branch), the tax on holdback
box (above), and any hard coded legal wording or date: what a lien form says, and what a deadline is, is the core's answer.

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
