# Payables and Receivables: the testers' brief

The Money Desk's receivables side. The full brief lives in the part 1 page, the testers' walk:

[Money Desk (receivables): the testers' brief](/home/colton/Desktop/FBHQ/hh-sup/tester-pages/money-desk-ar/testers.html)

That page is the source for every figure, every name, every wall, every "later" tab. It is the JV team's walk for the receivables role on this build.

## One line, in the desk's own words

The role works the dealer's AR aging across the seeded book, works the one bounced payment the queue shows (DEMO-PAY-B01, cheque DEMO-CHQ-B01, on I08, the dealer's NSF policy carries the fee), takes a payment in, records a promise to pay, and runs a statement. Lifting a credit hold and approving a parked journal are refused in words on screen; both are the Financial Controller's, the role gate's shared body is the rule, and the screen names who may.

## Where the part 1 page holds

- The sign-in: the card on the front door, the tile the card opens, the desk's tabs.
- The demo world: the 18 book invoices, the one bounced payment, the four cards' buckets.
- The bounce and Needs you: the work panel's three ways (Replacement payment received, Promise to pay, Send a reminder), the fee line, the notice's name.
- Payments and promises: the receive form, the payment split's preview, the no-card rail, the promise not past, the payment history held to money desk work.
- Statements: the run for every account, the send to the AP contact, the all-accounts notice.
- Credit holds and the walls: the seed plants no credit hold; the lift and override are refused in the matrix's own words; the journal is refused; the Payables tab is later.
- Known issues: the world is dated, the restart signs out, the PDF is later, beyond reads are walled.

Read the part 1 page in full; every claim on it is backed by the demo seed, the app's own logic, or the acceptance leg on the lane.

## What is "later"

The part 1 page names the "later" tabs in the desk's own words: Payables (later), the statement's PDF (later), the role's own card on the demo branch's `roles.ts` (the lane's front door push is pending).

## Where feedback goes

One channel: send everything to Colton, and he files every note with the principal in one place. What helps most: what you tapped, what you expected, and what you saw instead. The older demo's feedback path stays open too, but what you say about this box is what decides what gets built next.
