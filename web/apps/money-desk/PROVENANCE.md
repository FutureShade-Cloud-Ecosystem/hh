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

Not ported on purpose: auto credit hold settings, the paper path and reminders
(Base tier extras, not this role's first increment), the pop out and duplicate
windows.
