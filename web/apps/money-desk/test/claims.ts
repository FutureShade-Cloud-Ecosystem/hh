// The testers' page and the proof of each sentence on it: the one ledger between what
// public/testers.html states and what the suites and the sources hold. Every sentence
// on the page carries a data-claim id; each id here names its proofs, and
// test/testers-page.test.mjs holds three things together: the page's id set, this
// ledger's key set, and the proofs themselves, so a sentence with no proof fails, a
// proof naming a test that does not exist fails, and a retired proof cannot outlive
// its sentence.
//
// A proof is one of two kinds. A test proof names a spec file (a path from the app's
// folder) and the EXACT test title inside it. A source proof names a file and a
// fragment that must appear in it word for word, for the facts only the core's seed or
// the app's own source can state: the world's data is the core's, and the browser legs
// run on the stub, so a sentence about the seeded world rests on the seed itself.
export type ClaimProof = { readonly file: string; readonly test: string } | { readonly file: string; readonly source: string };

const CARD = "test/acceptance/card-lands.pw.ts";
const RECEIVABLES = "test/receivables.pw.ts";
const LEDGER = "test/ledger.pw.ts";
const FRONT_DOOR = "../front-door/test/acceptance/front-door.pw.ts";
const CORE = "../../../core/cmd/core/";

const PAYREC_CARD = { file: CARD, test: "the front door's Payables and Receivables card opens the Money Desk on the receivables landing" };
const CONTROLLER_CARD = { file: CARD, test: "the front door's Financial Controller card opens the same Money Desk tile and lands on the ledger" };
const PICKER = { file: FRONT_DOOR, test: "the picker renders the five live cards and the eight coming rows" };
const DESK_OPENS = { file: RECEIVABLES, test: "the desk opens on the register: four tiles, the rows overdue first, Needs you, and later marked in words" };
const APPLY_SPLIT = { file: RECEIVABLES, test: "one payment across two invoices: the server splits it oldest first and posts it as one payment" };
const BOUNCED_WORLD = { file: RECEIVABLES, test: "the bounced payment the demo world starts with: what happened, and a reminder sent from the queue" };
const QUEUE = { file: LEDGER, test: "the card lands on the approvals queue: the floor request, both parked journals and the freight raises" };

export const CLAIM_COVERAGE: Readonly<Record<string, readonly ClaimProof[]>> = {
  "sign-in-cards": [PAYREC_CARD, CONTROLLER_CARD, PICKER],
  "payrec-lands": [PAYREC_CARD],
  "controller-lands": [
    CONTROLLER_CARD,
    { file: LEDGER, test: "the demo way in offers the controller's card and lands on the approvals queue" },
  ],
  "other-cards": [
    { file: CARD, test: "the front door's Sales card offers no Money Desk tile" },
    { file: CARD, test: "the front door's Sales manager card offers no Money Desk tile" },
    PICKER,
  ],
  "own-way-in": [{ file: RECEIVABLES, test: "the way in with no handoff offers the role cards" }],
  "no-gm": [{ file: "../../../core/internal/demo/flows/flows_test.go", source: "want the closed set of seven" }],
  "world-invoices": [{ file: `${CORE}demoworld_payables_receivables_test.go`, source: "want the 18 across the aging buckets" }],
  "world-bounce": [
    { file: `${CORE}demoseed_payables_receivables.go`, source: '{"B01", "I08", 12, 9, "Cheque returned by the bank, not sufficient funds", 4_500}' },
    { file: `${CORE}demoseed_payables_receivables.go`, source: 'demoPrefix+"CHQ-"+s.Key' },
    BOUNCED_WORLD,
  ],
  "no-seeded-hold": [
    { file: `${CORE}demoseed_payables_receivables.go`, source: "No credit hold is seeded" },
    { file: "src/ar/ar-views.ts", source: "The sweep places the Aged AR, Over limit and Pay before release holds, and a returned payment places its own." },
  ],
  "four-tiles": [
    DESK_OPENS,
    { file: RECEIVABLES, test: "a tile filters the register to the accounts it counted, and clears" },
  ],
  register: [
    DESK_OPENS,
    { file: RECEIVABLES, test: "the register's filters: overdue, promised, disputed and paid" },
    { file: RECEIVABLES, test: "Download CSV hands over the register" },
  ],
  "needs-you": [
    DESK_OPENS,
    { file: RECEIVABLES, test: "Remind on an account and the paper path item in Needs you chase what is overdue" },
  ],
  "payables-later": [DESK_OPENS],
  "record-asks-account": [
    { file: RECEIVABLES, test: "Record a payment on the home asks which account" },
    { file: RECEIVABLES, test: "aging to payment applied: Record payment on a row opens the account with the payment ready, and the server's split and balance show" },
  ],
  "apply-split": [
    APPLY_SPLIT,
    { file: RECEIVABLES, test: "the panel sends the route's body and nothing it computed, and every preview carries its own key" },
  ],
  "apply-no-card": [{ file: "src/ar/ar-views.ts", source: 'placeholder="Cheque number or transfer reference"' }],
  "apply-unknown": [
    { file: RECEIVABLES, test: "an answer lost after the server applied the payment says the outcome is unknown, and pressing again reuses the key and posts once" },
    { file: RECEIVABLES, test: "a 5xx answer says the outcome is unknown, and every retry carries the same key whatever the form now says" },
    { file: RECEIVABLES, test: "a lost answer then a changed cheque number sends the same key; the route's refusal reads already recorded and the account reloads" },
    { file: RECEIVABLES, test: "a payment recorded on another account leaves this account's pending payment key alone" },
    { file: RECEIVABLES, test: "a reload that shows the payment ends the held key, so pressing again with the same body is a new payment" },
    { file: RECEIVABLES, test: "on a phone the unknown outcome message is in view and focused when it appears" },
  ],
  "apply-refused": [
    { file: RECEIVABLES, test: "a worded 4xx says the payment was not recorded in the route's own words, and the next press is a new payment with a new key" },
    { file: RECEIVABLES, test: "a payment that cannot be posted is stopped in words, and the server's refusal shows beside the split" },
  ],
  "paid-context": [{ file: RECEIVABLES, test: "a paid invoice stays for context, dimmed, and takes nothing" }],
  promise: [{ file: RECEIVABLES, test: "a promise to pay is recorded, and a second is refused in the core's words" }],
  "bounced-work": [
    { file: RECEIVABLES, test: "a payment is marked bounced from its invoice, and worked from the queue" },
    { file: "src/ar/ar-views.ts", source: 'head: "Replacement payment received"' },
    { file: "src/ar/ar-views.ts", source: 'id: "reminder"' },
  ],
  statement: [{ file: RECEIVABLES, test: "the statement: none yet, run it, read the aging strip, send it; PDF reads as later" }],
  "statements-all": [{ file: RECEIVABLES, test: "the Statements tab lists every account and runs them all" }],
  "holds-payrec": [
    { file: RECEIVABLES, test: "credit holds: the sweep's holds are listed, lifting is held for this role and says who may, place a hold reads as later" },
    { file: "src/ar/ar-views.ts", source: 'data-choice="override" aria-disabled=' },
  ],
  "holds-controller": [
    { file: RECEIVABLES, test: "the controller lifts a hold with a reason, and overrides another" },
    { file: RECEIVABLES, test: "the controller's holds screen has no Place a hold button" },
  ],
  "walls-core": [
    { file: `${CORE}demoworld_payables_receivables_test.go`, source: '"journal approval": payRec.call("POST", "/api/v1/gl/journals/"' },
    { file: `${CORE}demoworld_payables_receivables_test.go`, source: '"hold release":     payRec.call("POST", "/api/v1/ar/holds/"' },
    { file: `${CORE}demoworld_payables_receivables_test.go`, source: "want the gate's shared refusal body" },
  ],
  "queue-world": [
    QUEUE,
    { file: `${CORE}demoseed_quotes.go`, source: '{"FR1", "Q1", 1, 12,' },
    { file: `${CORE}demoseed_controller.go`, source: "Maker: personaQuoteManager, Branch: 1, AgeHours: 30," },
    { file: `${CORE}demoseed_controller.go`, source: "{Account: \"1300\", Debit: 750_000}," },
    { file: `${CORE}demoseed_controller.go`, source: "Maker: personaController, Branch: 1, AgeHours: 12," },
    { file: `${CORE}demoseed_controller.go`, source: "{Account: \"5200\", Debit: 620_000}," },
    { file: `${CORE}demoseed_controller.go`, source: "Both totals sit over the org default threshold (500000 cents" },
    { file: `${CORE}demoseed_plan.go`, source: '{"AP1", 0, "O01"' },
    { file: `${CORE}demoseed_plan.go`, source: '{"AP2", 1, "O03"' },
  ],
  "queue-screen": [QUEUE],
  "decide-floor": [{ file: LEDGER, test: "it decides the floor request, and the answer says the approval is spent on that one quote" }],
  "approve-journal": [{ file: LEDGER, test: "it approves the parked journal, the journal posts, and the trial balance carries it" }],
  "own-request": [{ file: LEDGER, test: "it cannot decide its own request, and the screen says so before a press" }],
  "refused-decision": [{ file: LEDGER, test: "a refused decision names who may decide it, never the bare gate word" }],
  "bare-array": [{ file: LEDGER, test: "a bare array where the contract says a page is refused, never half read" }],
  "phone-decision": [{ file: LEDGER, test: "on a phone a chosen request brings its decision into view" }],
  "journal-register": [{ file: LEDGER, test: "the journal register keeps a journal number on one line and says what is later" }],
  "trial-balance": [{ file: LEDGER, test: "it reads the trial balance in balance, with the server's totals" }],
  "phone-sideways": [{ file: RECEIVABLES, test: "on a phone no screen scrolls the page sideways" }],
  "empty-world": [{ file: RECEIVABLES, test: "the empty world: each screen says so in words" }],
};
