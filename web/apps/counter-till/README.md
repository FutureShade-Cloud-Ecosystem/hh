# @hardscapehouse/counter-till

The Counter Till micro-app: one Vite entry, one bundle (web/apps/README.md).
This slice is the demo way in (role cards from `GET /api/v1/demo/flows`, the
demo adapter behind the auth package's `AccountSurface` seam, the api-client
sending `X-Org: hh-demo`), a header with the Demo banner, the role's name and
sign out, and the returns desk: the original sale found by its number through
the branch fenced lookup route, its lines with what is still returnable, the
three refund rails (cash from the drawer, card to the simulator, account
credit), and the refund above the dealer's approval amount: refused to the
clerk with the amount and the approver roles named, completed by a manager's
own credential entered at the clerk's till through the Manager approval
dialog, the token held in memory for the one call and dropped, the receipt
naming the approver and the clerk, and the till still the clerk's. The sale at
the counter builds on this shell elsewhere in the cycle.

Money is never computed here: every figure on the desk is an integer-cent
amount the core stated, spelled by the one money helper (`src/money.ts`), and
no other module formats money.

## Running the app against the local stub

The app calls the core's routes on its own origin. Without a core, a local
stub serves the same shapes (the contract's `DemoFlowList`,
`DemoSessionResponse`, `PosReturnLookup`, `PosReturn`, the approval refusal's
409, `TillOpenLookup`, `TillZReport`, and `PaymentConfigStatus`):

```
node test/stub/demo-stub.mjs        # the demo routes on 127.0.0.1:4821
npm run dev                         # the app on 127.0.0.1:4820, /api proxied to the stub
```

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

The stub is a development and test double only: the real routes are the core's
(`POST /api/v1/demo/session`, `GET /api/v1/demo/flows` in
`core/api/openapi.yaml`), and nothing in the app knows the stub exists.
