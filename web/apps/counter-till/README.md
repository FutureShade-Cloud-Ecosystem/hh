# @hardscapehouse/counter-till

The Counter Till micro-app: one Vite entry, one bundle (web/apps/README.md).
This slice is the demo way in (role cards from `GET /api/v1/demo/flows`, the
demo adapter behind the auth package's `AccountSurface` seam, the api-client
sending `X-Org: hh-demo`), a header with the Demo banner, the role's name and
sign out, the sale at the counter (the branch's drawer, the priced ticket with
its margin column, cash, card and account tenders, the receipt, and the
branch's parked tickets), and the returns desk beside it: the original sale
found by its number through the branch fenced lookup route, its lines with
what is still returnable, the three refund rails (cash from the drawer, card
to the simulator, account credit), and the refund above the dealer's approval
amount: refused to the clerk with the amount and the approver roles named,
completed by a manager's own credential entered at the clerk's till through
the Manager approval dialog, the token held in memory for the one call and
dropped, the receipt naming the approver and the clerk, and the till still the
clerk's.

Money is never computed here: every figure on the desk is an integer-cent
amount the core stated, spelled by the one money helper (`src/money.ts`), and
no other module formats money.

## Running the app against the local stub

The app calls the core's routes on its own origin. Without a core, a local
stub serves the same shapes: the contract's `DemoFlowList` and
`DemoSessionResponse`; the counter routes the sale screen speaks, from
`Branch` and `TillSessionDetail` to `PosSaleView`; and the returns desk's
routes, `PosReturnLookup`, `PosReturn`, the approval refusal's 409,
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
then the Playwright suite (playwright.config.ts). Playwright starts the stub
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

The stub also serves `GET /_stub/state`, a harness-only peek at its own
ledgers (the api requests it has sighted and whether any carried the approver
header, the returns posts' idempotency keys in send order, the last return
body verbatim, and who each committed return named as clerk and approver): no
contract path shares the namespace, and the app never calls it.

The suite runs one worker, in order: the stub holds the demo world in memory
(an open drawer, committed sales, parked tickets), so each test reads only
what it itself caused. `test/entry.pw.ts` holds the way in (W6's screens);
`test/sale.pw.ts` holds the sale at the counter (W7's screens, including the
committed sale read back through `GET /api/v1/pos/sales/{id}` and compared
against every figure on screen); `test/returns.pw.ts` holds the returns desk
(W8's screens, the approval dialog and the one time credential included);
`test/scans.pw.ts` holds the two source scans (no money arithmetic outside
the one named money helper, and no card path) as tests, so the CI keeps them
green and not just a report.

The stub is a development and test double only: the real routes are the core's
(the mint is the demo build's, the pricing is the engine's, the returns rules
are RT-B's), and nothing in the app knows the stub exists.
