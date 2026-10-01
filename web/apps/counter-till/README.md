# @hardscapehouse/counter-till

The Counter Till micro-app: one Vite entry, one bundle (web/apps/README.md).
This slice is the demo way in: role cards from `GET /api/v1/demo/flows`, the
demo adapter behind the auth package's `AccountSurface` seam, the api-client
sending `X-Org: hh-demo`, a header with the Demo banner, the role's name and
sign out, and a placeholder till. The sale at the counter builds on this shell
elsewhere in the cycle.

## Running the app against the local stub

The app calls the core's demo routes on its own origin. Without a core, a
local stub serves the same shapes (the contract's `DemoFlowList` and
`DemoSessionResponse`):

```
node test/stub/demo-stub.mjs        # the demo routes on 127.0.0.1:4821
npm run dev                         # the app on 127.0.0.1:4820, /api proxied to the stub
```

With no stub (and no core) answering, the entry page says so plainly and
renders no cards: that is the same page a default-build core or a non-demo box
serves, because there the route answers nothing too.

## Tests

`npm test` runs the Playwright suite (playwright.config.ts). It starts the
stub and the dev server itself and drives the system Chrome through the
`chrome` channel: the same browser the design system's browser leg expects on
the runner, so no browser download rides the npm install. The suite's ports
are hashed from this app's own path, so a sibling worktree's servers can
never stand in for its own, and `reuseExistingServer` is false, so the suite
only ever tests the servers it started; a hand run can still pin ports with
`CT_APP_PORT` and `CT_STUB_PORT`. The spec
files are named `*.pw.ts` so the repository root's bun drift gate (which
discovers `*.test.*` suites) does not pick them up.

The stub is a development and test double only: the real routes are the core's
(`POST /api/v1/demo/session`, `GET /api/v1/demo/flows` in
`core/api/openapi.yaml`), and nothing in the app knows the stub exists.
