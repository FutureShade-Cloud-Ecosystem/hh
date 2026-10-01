# @hardscapehouse/counter-till

The Counter Till micro-app: one Vite entry, one bundle (web/apps/README.md).
This slice is the demo way in: role cards from `GET /api/v1/demo/flows`, the
demo adapter behind the auth package's `AccountSurface` seam, the api-client
sending `X-Org: hh-demo`, a header with the Demo banner, the role's name and
sign out, and a placeholder till. The sale at the counter builds on this shell
elsewhere in the cycle.

## Running the app against the local stub

The app calls the core's demo routes on its own origin. Without a core, a
local stub serves the same shapes (the contract's `DemoFlowList`,
`DemoSessionResponse`, and the counter routes the sale screen speaks, from
`Branch` and `TillSessionDetail` to `PosSaleView`):

```
node test/stub/demo-stub.mjs        # the demo and counter routes on 127.0.0.1:4827
npm run dev                         # the app on 127.0.0.1:4826, /api proxied to the stub
```

The port pair stays away from the cycle's other lane's pair: `CT_APP_PORT`
and `CT_STUB_PORT` move either server when a run needs it.

With no stub (and no core) answering, the entry page says so plainly and
renders no cards: that is the same page a default-build core or a non-demo box
serves, because there the route answers nothing too.

## Tests

`npm test` runs the Playwright suite (playwright.config.ts). It starts the
stub and the dev server itself and drives the system Chrome through the
`chrome` channel: the same browser the design system's browser leg expects on
the runner, so no browser download rides the npm install. The spec files are
named `*.pw.ts` so the repository root's bun drift gate (which discovers
`*.test.*` suites) does not pick them up.

The suite runs one worker, in order: the stub holds the demo world in memory
(an open drawer, committed sales, parked tickets), so each test reads only
what it itself caused. `test/entry.pw.ts` holds the way in (W6's screens);
`test/sale.pw.ts` holds the sale at the counter (W7's screens, including the
committed sale read back through `GET /api/v1/pos/sales/{id}` and compared
against every figure on screen); `test/scans.pw.ts` holds the two source
scans (no money arithmetic outside the one named money helper, and no card
path) as tests, so the CI keeps them green and not just a report.

The stub is a development and test double only: the real routes are the core's
(the mint is the demo build's, the pricing is the engine's), and nothing in
the app knows the stub exists.
