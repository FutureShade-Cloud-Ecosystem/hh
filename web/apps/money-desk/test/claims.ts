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
const LIENS = "test/liens.pw.ts";
const COMPLIANCE = "test/compliance.pw.ts";
const BANKREC = "test/bankrec.pw.ts";
const FRONT_DOOR = "../front-door/test/acceptance/front-door.pw.ts";
const TAX = "test/tax.pw.ts";
const CORE = "../../../core/cmd/core/";

const PAYREC_CARD = { file: CARD, test: "the front door's Payables and Receivables card opens the Money Desk on the receivables landing" };
const CONTROLLER_CARD = { file: CARD, test: "the front door's Financial Controller card opens the same Money Desk tile and lands on the ledger" };
const PICKER = { file: FRONT_DOOR, test: "the picker renders the eight live cards and the five coming rows" };
const DESK_OPENS = { file: RECEIVABLES, test: "the desk opens on the register: four tiles, the rows overdue first, Needs you, and later marked in words" };
const APPLY_SPLIT = { file: RECEIVABLES, test: "one payment across two invoices: the server splits it oldest first and posts it as one payment" };
const BOUNCED_WORLD = { file: RECEIVABLES, test: "the bounced payment the demo world starts with: what happened, and a reminder sent from the queue" };
const BOOKS = "test/books.pw.ts";
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
  "no-gm": [{ file: "../../../core/internal/demo/flows/flows_test.go", source: "want the closed set of eight" }],
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
    { file: RECEIVABLES, test: "a lost answer then a changed cheque number sends the same key; the refusal keeps the form and the next press records the cheque" },
    { file: RECEIVABLES, test: "a held key re-used for a different payment: the error words say both, the form is kept, and the next press records the new cheque" },
    { file: RECEIVABLES, test: "the same re-use from a duplicated tab: the copied key refuses the new cheque in the error words, and the next press records it" },
    { file: RECEIVABLES, test: "a payment recorded on another account leaves this account's pending payment key alone" },
    { file: RECEIVABLES, test: "opening the invoice's payments after a lost answer never ends the held key: the next press carries it and reads already applied, with one payment" },
    { file: RECEIVABLES, test: "a 401 on the retry after a lost answer is the gate's answer: the key is kept, the words stay unknown, and the next press carries it" },
    { file: RECEIVABLES, test: "a 403 on the retry after a lost answer is the gate's answer: the key is kept, the words stay unknown, and the next press carries it" },
    { file: RECEIVABLES, test: "a first press answered 401 wrote nothing: it is refused in words and the next press is a new payment" },
    { file: RECEIVABLES, test: "the held key survives a page reload: a lost answer, a reload, and the next press carries the same key" },
    { file: RECEIVABLES, test: "the held key survives a sign out and a sign in of the same user" },
    { file: RECEIVABLES, test: "held keys are never shared across users: another user starts with none, and the first user's key is left untouched" },
    { file: RECEIVABLES, test: "on a phone the unknown outcome message is in view and focused when it appears" },
  ],
  "apply-refused": [
    { file: RECEIVABLES, test: "a worded 4xx says the payment was not recorded in the route's own words, and the next press is a new payment with a new key" },
    { file: RECEIVABLES, test: "a payment that cannot be posted is stopped in words, and the server's refusal shows beside the split" },
    { file: RECEIVABLES, test: "a pick of more than 100 invoices is refused in the browser with the route's words: no body is sent and the held key is kept" },
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
  "liens-areas": [
    { file: LIENS, test: "the General Manager's area switch holds Liens and Compliance, and the controller's holds all six" },
    { file: LIENS, test: "Payables and Receivables reads the jobs and the calendar and is offered no write" },
    { file: "../../../core/internal/demo/flows/flows_test.go", source: "want the closed set of eight" },
  ],
  "liens-payrec-reads": [
    { file: LIENS, test: "Payables and Receivables reads the jobs and the calendar and is offered no write" },
    { file: "test/lien-stub-contract.test.mjs", test: "the role gate: Payables and Receivables reads the liens and the calendar and is refused every write, as the default configuration and the census say" },
    { file: "../../../core/internal/roleconfig/default.json", source: '"role": "payables_receivables",\n      "surface": "money-desk",\n      "level": "work"' },
  ],
  "liens-banner": [
    { file: LIENS, test: "the controller opens Liens on the Monday triage: the core's banner and four jobs, oldest first, each with the core's badge and note" },
    { file: COMPLIANCE, test: "the review status is the core's own draft banner until it is set; setting it changes the banner every lien screen carries" },
  ],
  "liens-core-words": [
    { file: "../../../core/internal/lien/aging.go", source: "at/over the 45-day window, eligible to release" },
    { file: "../../../core/internal/lien/template.go", source: 'Title: "Statutory Declaration (Construction Act)"' },
  ],
  "liens-triage": [
    { file: LIENS, test: "the controller opens Liens on the Monday triage: the core's banner and four jobs, oldest first, each with the core's badge and note" },
    { file: LIENS, test: "a triage row opens its job on the jobs tab, with the facts the core holds and what is held now" },
  ],
  "liens-jobs": [
    { file: LIENS, test: "the jobs tab lists every job newest first and a press on a row opens it" },
    { file: LIENS, test: "a new job is refused in words before anything is sent, then it lands first in the list with its deadline registered" },
    { file: "src/lien/lien-logic.ts", source: "hst_on_holdback: false," },
  ],
  "liens-sync": [{ file: LIENS, test: "syncing a job's holdbacks books the held invoice once and a second press books nothing" }],
  "liens-aging": [{ file: LIENS, test: "the holdbacks tab ages the retention; a payable holdback is released by hand and has no release control" }],
  "liens-release": [
    { file: LIENS, test: "the General Manager releases a holdback: the co-signer is picked from the roster, the confirm names the job and the amount, and the job leaves the aging" },
    { file: LIENS, test: "a co-signer the core will not take is refused in the core's words and nothing is released" },
    { file: LIENS, test: "a holdback released by another hand meanwhile is refused as nothing outstanding, and the list is read again" },
  ],
  "liens-release-read": [{ file: LIENS, test: "the controller can read a release but not make one: the screen says who releases, and offers no confirm" }],
  "liens-documents": [{ file: LIENS, test: "the documents tab renders a form from its own merge fields, a second render is version 2, and the history reads both" }],
  "compliance-upcoming": [
    { file: COMPLIANCE, test: "Compliance opens on what is due soon: five open deadlines, soonest first, the two past due marked Overdue in words" },
    { file: COMPLIANCE, test: "the horizon box narrows the list, a bad horizon is refused in words before anything is asked, and blank reads the core's own" },
  ],
  "compliance-calendar": [{ file: COMPLIANCE, test: "the calendar lists every deadline by due date, a status filter reads what the core holds, and an empty status says so" }],
  "compliance-review": [
    { file: COMPLIANCE, test: "the review status is the core's own draft banner until it is set; setting it changes the banner every lien screen carries" },
    { file: COMPLIANCE, test: "a job's review is its own: the cockpit stays as it was, and a blank banner is the core's default words" },
  ],
  "liens-states": [
    { file: LIENS, test: "a load that fails says so in words and Try again reads the area again" },
    { file: LIENS, test: "an empty worklist, an empty aging and an empty job list say they are empty" },
    { file: COMPLIANCE, test: "a load that fails says so in words and Try again reads the area again" },
    { file: COMPLIANCE, test: "an empty calendar and nothing due soon say so" },
  ],
  "liens-phone": [
    { file: LIENS, test: "every tab fits the screen with cards in place of tables" },
    { file: LIENS, test: "the keys reach every tab and the release confirm, and focus lands where the screen changed" },
    { file: COMPLIANCE, test: "the keys reach the tabs, the filter and the save, and focus stays where the person was" },
    { file: COMPLIANCE, test: "every tab fits the screen with cards in place of tables" },
  ],
  "journal-register": [{ file: LEDGER, test: "the journal register keeps a journal number on one line and offers void and reverse where they apply" }],
  "trial-balance": [
    { file: LEDGER, test: "it reads the trial balance in balance, with the server's totals" },
    { file: BOOKS, test: "the trial balance takes a month, shows that month alone, and the export downloads the rows on screen" },
  ],
  chart: [
    { file: BOOKS, test: "the chart lists every account with its status in words, and adds an account the journals can use" },
    { file: BOOKS, test: "deactivating an account is a review that names what it keeps and what it ends, and cancel changes nothing" },
  ],
  "journal-post": [
    { file: BOOKS, test: "a journal below the threshold posts after a review, the register shows it posted and the trial balance carries it" },
    { file: BOOKS, test: "a journal at the threshold parks, files its request in the approvals queue, and says so" },
    { file: BOOKS, test: "the form checks each line before anything is sent: a memo, one side, a real amount" },
    { file: BOOKS, test: "an unbalanced journal is refused by the server in words, nothing is saved, and the form keeps what was typed" },
  ],
  "post-unknown": [{ file: BOOKS, test: "a lost answer to a post says it may or may not have landed, and the desk never sends it a second time" }],
  "reverse-void": [
    { file: BOOKS, test: "reversing a posted journal is a review that lists its lines, cancel changes nothing, and the reversal links back" },
    { file: BOOKS, test: "voiding a draft is a review, and the register then reads it Void" },
  ],
  recurring: [{ file: BOOKS, test: "the recurring templates list what they post and run into a draft that never posts by itself" }],
  periods: [
    { file: BOOKS, test: "closing a period names the period and what changes, then the period reads Closed and reopens only with a reason" },
    { file: BOOKS, test: "a journal dated into a closed month is refused in words that say who can reopen it" },
  ],
  "year-close": [{ file: BOOKS, test: "the year end close is a review naming the year, retained earnings and the periods it seals, and reports the server's net income" }],
  "trial-export": [{ file: BOOKS, test: "the trial balance takes a month, shows that month alone, and the export downloads the rows on screen" }],
  checks: [
    { file: BOOKS, test: "the checks read the receivables tie out in words beside its colour, and run the BisTrack check" },
    { file: BOOKS, test: "a journal that moves receivables puts the tie out out of tie, said in words with the difference" },
  ],
  policy: [{ file: BOOKS, test: "the policy reads the approval threshold and says changing it is not on this screen" }],
  reader: [{ file: BOOKS, test: "Payables and Receivables reads the ledger and holds every write, naming who may" }],
  "bankrec-area": [
    { file: BANKREC, test: "the controller's third area lists the accounts, what waits on each, and the cards as tracking only" },
    { file: BANKREC, test: "Payables and Receivables has no bank reconciliation area" },
  ],
  "bankrec-import": [
    { file: BANKREC, test: "a statement pasted in imports, the same rows again add nothing, and a file the core cannot read says what it lacked" },
    { file: BANKREC, test: "a statement file chosen in the browser fills the rows and imports to the card account" },
    { file: "src/bankrec/bankrec-views.ts", source: "Uploading the file to FB Console Storage, and the bank feed, are not in this increment" },
  ],
  "bankrec-auto": [
    { file: BANKREC, test: "auto match pairs the card, lodgement and batch lines and leaves the rest for the hand" },
    { file: BANKREC, test: "auto match can be held to a window of dates, and a window that ends before it starts is refused in the browser" },
  ],
  "bankrec-hand": [
    { file: BANKREC, test: "a line matched by hand to a payment leaves the unmatched list, and the refusals say what to do" },
    { file: BANKREC, test: "a line matched by hand to a posted journal posts nothing new" },
  ],
  "bankrec-undo": [{ file: BANKREC, test: "an undo is confirmed, returns the line to unmatched and keeps the history" }],
  "bankrec-month": [
    { file: BANKREC, test: "a month opens with the difference in words, sign off is held until it is zero, and the refusal for unmatched lines is in words" },
    { file: BANKREC, test: "matching the last lines lets the month sign off, and a signed off month is closed" },
  ],
  "bankrec-zero": [
    { file: BANKREC, test: "a zero closing balance is not sent quietly: the core's replacement is said" },
    { file: BANKREC, test: "a blank or invalid closing balance is refused in the browser and nothing is sent" },
  ],
  "bankrec-rules": [{ file: BANKREC, test: "the rules list what is in use, and approving a new version retires the old one" }],
  "bankrec-refused": [
    { file: BANKREC, test: "a refused write names who may do it, never the bare gate word" },
    { file: BANKREC, test: "a refused sign off and a refused undo are said in words and change nothing" },
  ],
  "bankrec-states": [
    { file: BANKREC, test: "while the accounts load the screen says so, a failed load offers Try again, and an empty list says so" },
    { file: BANKREC, test: "the core's bare arrays and the contract's pages are both read, and anything else is refused, never half read" },
    { file: BANKREC, test: "more than a page of lines shows more on request and follows the core's cursor to the end" },
  ],
  "bankrec-phone": [
    { file: BANKREC, test: "on a phone the tables give way to cards and no screen scrolls the page sideways" },
    { file: BANKREC, test: "the tabs and the controls work from the keyboard, and focus is visible" },
  ],
  "phone-sideways": [{ file: RECEIVABLES, test: "on a phone no screen scrolls the page sideways" }],
  "empty-world": [{ file: RECEIVABLES, test: "the empty world: each screen says so in words" }],
  "tax-area": [
    { file: TAX, test: "the controller's Tax area lists the rate table in California words, and Payables and Receivables does not have it" },
    { file: "test/no-tax-money.test.mjs", source: "no screen word names a Canadian tax or currency" },
  ],
  "tax-rates": [{ file: TAX, test: "the controller's Tax area lists the rate table in California words, and Payables and Receivables does not have it" }],
  "tax-rate-add": [{ file: TAX, test: "a rate is added through a confirm step that names what changes, and a wrong one sends nothing" }],
  "tax-rate-refused": [{ file: TAX, test: "a rate the gate refuses, a server fault and a lost answer are each said in words, and a lost answer rereads the table" }],
  "tax-queue": [{ file: TAX, test: "the review queue lists what waits, the tab counts it, and a profile is put in force through a confirm step" }],
  "tax-profile": [{ file: TAX, test: "a new profile is saved, an existing one edited, and a wrong one is held or refused in words without the core's old ones" }],
  "tax-certificates": [
    { file: TAX, test: "the certificates on file: masked numbers only, newest first, opened, checked against their dates and downloaded" },
    { file: TAX, test: "the certificates filter by order, hold an id that is not an id, and say when none is on file" },
  ],
  "tax-capture": [{ file: TAX, test: "a certificate is recorded with its number masked and thrown away, and what the profile forces is held before anything is sent" }],
  "tax-settings": [{ file: TAX, test: "the settings read the company wide row, ask before a change, and keep the two switches that are not available off" }],
  "tax-recon": [{ file: TAX, test: "the reconciliation ties out one month, is off in another in words, and holds a year that is not a year" }],
  "tax-recompute": [{ file: TAX, test: "the recompute asks first, adds once, and a second run says nothing new; a bad range is held" }],
  "tax-states": [
    { file: TAX, test: "while the tables load the screen says so, a failed load offers Try again, and a wall on the read is named" },
    { file: TAX, test: "an empty world says so on each tab, and a list that is not the shape the core sends is refused, never half read" },
  ],
  "tax-phone": [
    { file: TAX, test: "on a phone every tab becomes cards and none scrolls the page sideways" },
    { file: TAX, test: "every control works from the keyboard and focus lands on the confirm step" },
  ],
};
