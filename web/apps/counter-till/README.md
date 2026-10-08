# @hardscapehouse/counter-till

The Counter Till micro-app: one Vite entry, one bundle (web/apps/README.md).
This slice is the demo way in (role cards from `GET /api/v1/demo/flows`, the
demo adapter behind the auth package's `AccountSurface` seam, the api-client
sending `X-Org: hh-demo`), a chrome with the Demo banner, the role's name, a
Pop out control and sign out, and then the ported POS terminal as the till:
the surface carried over from the older product and wired to HH's contract
(the closure map in `src/ported/CLOSURE.md`). The pieces the Quote Desk work
area reuses, with the seam file (`ports.ts`) and the endpoint module
(`pos-api.ts`) they cannot run without, moved into the shared package
`@hardscapehouse/pos-kit` (web/packages/pos-kit); this app imports them from
there by deep path, and this directory keeps the till only remainder. The
house zone, the house day conversion and the demo card token moved with them
(`host-facts.ts`) and `src/till-client.ts` re-exports the same three
names. The terminal works in both its modes: the
linear flow (the Customer, Items, Payment and Summary accordion) and the
detached multi-card layout (the counter view menu's layout pick, persisted on
the workspace row, with the pop-out opening the same till on a second window
against the same branch drawer). The Returns surface is built like a credit, the
customer first (item 10): a stepper (Customer, Lines, Refund, Done), the customer
search over the customers search route (the walk-in is not a returns customer,
and the server says so in words), then every order the customer holds from
`GET /api/v1/pos/returns/customer-lines`, grouped by order and job, each line
with the quantity still open, a quantity stepper capped at it, and three
dispositions (return to stock, damaged, nothing returned). Lines from several
orders go into one draft (one branch per credit note); the sales order number
only filters the list. There is no search by one sale's number. The Refund step
chooses the rail (card where every order was paid by card, on the account,
cash from the open drawer) and commits ONE credit note through
`POST /api/v1/pos/returns-across` under one idempotency key; a note over the
dealer's approval amount (judged on the note total) is refused with the amount
and roles named and completes through a manager's own credential entered at the
clerk's till, the token held in memory for the one call and dropped. The Done
step names the note, each order's return with its number and the server's
figures, and a receipt line per return. The swap waits on its own contract.

Money on the counter is integer cents throughout. The figures the core states
(line prices from the pricing resolve, the committed sale's and return's own
totals, the account's limit and balance) are what the counter renders; the
port's host seams (`ports.ts`) assemble those figures for the surface's live
read-back. Before the ring the counter computes no tax and no total (decision
log row 661): HH's contract gives the till no rate, so the pre-ring surface
assembles the priced lines' subtotal only and names the ring for the rest,
and the ring's committed figures are the binding ones the confirmation
renders. The returns desk spells its figures through the one money helper
(`src/money.ts`).

## Running the app against the local stub

The app calls the core's routes on its own origin. Without a core, a local
stub serves the same shapes: the contract's `DemoFlowList` and
`DemoSessionResponse`; the counter routes the sale screen speaks, from
`Branch` and `TillSessionDetail` to `PosSaleView`; and the returns desk's
read, `PosCustomerLines` (its 422 for the house walk-in), the credit note write
`PosReturnNote` (its approval 409),
`TillOpenLookup`, `TillZReport`, and `PaymentConfigStatus`:

```
CT_STUB_PORT=4827 node test/stub/demo-stub.mjs   # the demo, counter and returns routes
npm run dev                                      # the app on 127.0.0.1:4826, /api proxied to the stub
```

The port pair stays away from the cycle's other lane's pair: `CT_APP_PORT`
and `CT_STUB_PORT` move either server when a run needs it.

With no stub (and no core) answering, the entry page says so plainly and
renders no cards: that is the same page a default-build core or a non-demo box
serves, because there the route answers nothing too.

## Tests

`npm test` runs the unit and scan files first (`test/unit.test.mjs`, the one
time credential slot's own assertions, and `test/sinks.test.mjs`, the scan
that refuses any place in `src` a credential could rest in or surface from),
the ported surface's own node suites (`src/ported/*.test.mjs`, the ported
reference tests plus the wiring item's money seam, add-path census and hotkey
roster cross-checks), then the Playwright suite (playwright.config.ts). Playwright starts the stub
and the dev server itself and drives the system Chrome through the `chrome`
channel: the same browser the design system's browser leg expects on the
runner, so no browser download rides the npm install. The suite's ports are
hashed from this app's own path (a sibling worktree hashes elsewhere) and the
servers are never reused, so a collision refuses loudly rather than testing a
stranger's server; the suite only ever tests the servers it started. A hand
run can still pin ports with `CT_APP_PORT` and `CT_STUB_PORT`. The Playwright
spec files are named `*.pw.ts` so the repository root's bun drift gate (which
discovers `*.test.*` suites) does not pick them up; the two unit and scan
files are `.test.mjs` and run under both the app's own script and that gate.

The stub answers every route the ported terminal speaks besides the ones the
older slices drove: the counter's policy, workspace (with the layout choice
echoed), hotkeys, salespeople and quick-picks reads; the catalogue list, scan
and the ungated thumbnail route (404: the demo world carries no images); the
stock glance and the empty add-ons strip; the pricing resolve in the
contract's breakdown shape; the signatures, the relief assessment and the
rendered receipt document.

The suite runs one worker, in order: the stub holds the demo world in memory
(an open drawer, committed sales, parked tickets), so each test reads only
what it itself caused. `test/entry.pw.ts` holds the way in (W6's screens);
`test/sale.pw.ts` holds the sale at the counter (W7's screens, including the
committed sale read back through `GET /api/v1/pos/sales/{id}` and compared
against every figure on screen); `test/returns.pw.ts` holds the returns surface
(item 10's customer and lines steps); `src/returns/*.test.mjs` hold the draft's
rules as unit tests;
`test/scans.pw.ts` holds the two source scans (no money arithmetic outside
the one named money helper, and no card path) as tests, so the CI keeps them
green and not just a report.

The stub is a development and test double only: the real routes are the core's
(the mint is the demo build's, the pricing is the engine's, the returns rules
are the core's), and nothing in the app knows the stub exists.

## The acceptance run and the testers' page (W9)

`npm run acceptance` drives the plan's eleven screens as each role against the
demo image itself, not the stub: the same app the box serves, the same core
behind it. Its Playwright config is `test/acceptance.config.ts`, its specs live
in `test/acceptance/`, and the run needs the box's address (`CT_IMAGE_URL`),
the gate phrase (`CT_GATE_PHRASE`, generated by whoever boots the box and held
in its shell), and a screenshot folder (`CT_SHOT_DIR`, default
`acceptance-screens/` beside this README, untracked). The suite is one worker
in story order: the world leg (`10-world`), the eleven screens as Sales
(`20-sales`), as Sales manager (`30-manager`), the testers' page held to its
claims ledger (`40-claims`), and the restart leg (`90-restart`), which the exit
script runs after it restarts the box. The run paces its mints to the human
pace the demo sign-in guard assumes.

The testers' page is `public/testers.html`, one plain page the image serves at
`/testers.html` behind the same phrase gate. Every behavior sentence on it
carries a `data-claim` id, and `test/acceptance/claims.ts` is the ledger: each
id names the spec files and the exact test titles that prove it, and
`test/acceptance/40-claims.pw.ts` holds the page's id set, the ledger, and the
suite's own titles together, so the page cannot claim what the run does not
prove.

`test/acceptance/exit-test.sh` is the whole exit test: it assembles the image
from the goal branch's head plus this lane's work (the app directory and the
one granted populate file, `core/internal/demo/populate.go`, whose dealer name
rename the review round's P2-1 granted), boots the throwaway Postgres
(`hh-till-l5d-pg`, port 5603) and the box, runs the acceptance run, restarts
the box for the restart leg, runs both lanes' stub suites and the typecheck,
the repository root's gates, and the two source scans and the sink scan, and
removes everything it raised. It needs docker, node 22 (CI's version), npm and
bun.

## The front door walk

`npm run walk-front-door` walks the assembled demo box the way a person walks
it at the front door: pick a role from the front door's picker, open each
module signed in (the Counter Till and the Quote Desk, each adopting the
front door's handoff with no second picker), open a not yet module's coming
soon page as the Sales manager, then Switch role and Sign out, holding after
each that the session keys are cleared. Its Playwright config is
`test/walk-front-door.config.ts` and its spec is
`test/acceptance/walk/walk-front-door.pw.ts`, one level below the acceptance
specs: the default config's `testIgnore` covers everything under
`test/acceptance/`, the acceptance config's match stops one level short, and
the popout and default-build configs name their own files, so the walk runs
only through its own config and never in the default legs.

The run needs the assembled demo image's box (the front door at `/`, the
till at `/till/`, the Quote Desk at `/quote/`), which the till exit image is
not: the walk is pointed at the box with `CT_IMAGE_URL` and answers the
phrase gate with `CT_GATE_PHRASE`, the same variables the acceptance run
reads, held by whoever boots the box. Check the box's `/healthz` before the
run spends its mint pace on a stranger. The green line is 1 passed (the walk
is one story, every landing asserted in its turn); it signs in five times at
the box's mint pace, so it takes its time. Like the acceptance run it needs
node 22, the web workspace installed, and the system Chrome.

The two module landings read different chrome on purpose (round 7): the
Quote Desk wears the design system's shared header, whose word is
`header-word`; the till keeps its own session bar's `signed-in-role`.
