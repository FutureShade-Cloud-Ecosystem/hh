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

## Tax administration (`src/tax/`)

The tax slice ports the original app's tax administration (`app/src/tax-admin.ts`, `tax-admin-logic.ts`,
`tax-api.ts`, `tax-evidence.ts`, same repository, read only) onto the generated client's fourteen `staff-tax` routes.

| What | Source path in the original app | Lands in | What changed |
| --- | --- | --- | --- |
| Rate table, effective dated rows, hot add | `app/src/tax-admin.ts` (Rates tab), `tax-api.ts` `listRates`, `createRate` | `src/tax/tax-views.ts` `ratesView`, `src/tax/tax-desk.ts` `#askRate` | The rate is typed as a percent and parsed through its decimal string to basis points; the add asks first, naming the reason, rate and days; new rows join the tax area of the standard rate in force |
| Reason words | `app/src/tax-api.ts` `taxReasonWord` | `reasonWord`, `reasonLine` in `src/tax/tax-logic.ts` | California trade words (standard sales tax, exempt delivery with proof, reduced rate with a certificate); no Canadian tax word on any screen |
| Exemption profiles and the review queue | `app/src/tax-admin.ts` (Profiles, Review Queue), `tax-admin-logic.ts` `forcedEvidence` | `profilesView`, `queuePanel`, `forcedEvidence` | One screen, queue first; Put in force and Edit load the form and ask before the upsert; the three required items read as one sentence |
| Certificates, masked only, view, download, validate | `app/src/tax-evidence.ts`, `tax-api.ts` | `certificatesView`, `evidenceDetail`, `captureForm` | Capture form added; the number is read at the press, sent once and cleared; a record is checked as of a day; download shows the photo's place and the file fetch is marked later |
| Tax configuration | `app/src/tax-admin.ts` (Certificates tab config card) | `settingsView`, `#askConfig` | Company wide row; the two dormant switches are shown off and marked later and always sent off |
| Recompute and reconciliation | `app/src/tax-admin.ts` (snapshot recompute, HST recon cards) | `checksView`, `#askRecompute`, `#runRecon` | Recompute asks first and says it adds audit rows only; the reconciliation names the ledger account and reads the variance in words |
| Money format | `app/src/tax-api.ts` `fmtCents`, `fmtBps` | `src/tax/tax-money.ts` | No locale call; US dollars; a rate is whole hundredths of a percent |
| Visual references | Money Desk boards (`hh-sup/design-refs/money-desk/`), original tax admin screens | the screens | Panels and pills as the ledger and bank reconciliation slices; cards border first, no shadow, HH tokens |

Not ported on purpose: the separate Certificates and Review Queue tabs (merged into Exemption profiles and
Certificates), per-branch config overrides, and any change to the core's Ontario reason codes or its Ontario
jurisdiction default (the modules cycle's).

