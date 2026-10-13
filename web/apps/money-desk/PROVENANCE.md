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
