# @hardscapehouse/front-door

The demo box's front door: one Vite entry, one bundle (web/apps/README.md).
It replaces the Counter Till's entry view as the box's first screen
(HH-DEMO-1 D2): the phrase gate, then this page. Pick a role, land on that
role's home with its modules as tiles, one header across the states with the
demo badge, the role's name, Switch role and Sign out.

- The thirteen live roles are the demo mint's own with their screens on the box
  (`inside_sales` to flow `sales`, `sales_manager` to flow `sales-manager`,
  `yard_manager` to flow `yard-manager`, `yard_team` to flow `yard-team`,
  `payables_receivables` to flow `payables-receivables`,
  `procurement_manager` to flow `procurement-manager`, `driver` to flow
  `driver`, `financial_controller` to flow `financial-controller`, `general_manager` to
  flow `general-manager`, `hr` to flow `hr`, `branch_manager` to flow `branch-manager`, `logistics_manager` to flow
  `logistics-manager`, `outside_sales` to flow `outside-sales`), labelled
  on the cards the way the demo adapter's contract names them ("Sales",
  "Sales manager", "Yard manager", "Yard Team", "Payables and Receivables",
  "Procurement manager", "Driver", "Financial Controller", "Logistics Manager", "General Manager", "HR", "Branch Manager", "Outside Sales"), the same rule the
  till's entry view follows. The yard manager's live module is Manager Home
  at `/home/`; the Yard Team's live module is the Yard Pick app at `/yard/`;
  the Money Desk at `/money-desk/` is the live module of Payables and
  Receivables and of the Financial Controller; the dispatch board at
  `/dispatch/` is the live module of the Logistics Manager and the yard manager's
  Dispatch tile.
- Every role of ROLE-ACCESS-r3 is live, so no coming row is drawn and the
  coming disclosure does not render; a role the matrix adds later would sit
  in `COMING_ROLES` first, shown with the modules the matrix gives it at
  work, manage or admin level and never tappable. The catalog lives in `src/roles.ts`, distilled once from the
  decided record; the record itself is read only and is not carried here.
- The sign-in path is the original demo's pattern (the role picker of
  `futurebuildai/hardscapeos_dibbits`, branch `Demo`, read only): one mint at
  a time, a refusal verbatim inside the card that earned it while the other
  card keeps working, a polite region rendered unconditionally.
- The session crosses to the box in `src/session.ts`: the mint validated
  field by field (the till's demo-account.ts discipline), held under the
  front door's own sessionStorage key with the token the brief's handoff
  rule stores, beside the till custody's own mirror key written in the
  mirror's own payload shape. Sign out and Switch role clear both keys.
- The theme is the token sheet's own contract, the till's theme.ts mechanism:
  the `data-theme` attribute on the document root. Light is the default and
  is applied at boot; dark stays the second opt-in through the same
  attribute. No theme control in the header yet.
- The icons come from the ported registry (the one registry: the Counter
  Till's `src/ported/_icon-consumer.ts` and `_icon-registry.ts`, imported
  across the app boundary). No second registry lives here.
- The Counter Till hangs off `/till/`, the Quote Desk off `/quote/`, the
  Buying and Stock app off `/buying-and-stock/`, Manager Home off `/home/`
  the Yard Pick app off `/yard/` and the Money Desk off `/money-desk/`; the
  demo image serves those six. The manager's two tiles whose apps are not on
  the box yet open the polite coming-soon page.

The app is demo-lane scaffolding, not a catalog frontend: it takes no entry
in `manifest.yaml` (that list is generated from the role catalog, and the
front door is not a role's surface; it is the door in front of them).

## Two modes, chosen by the served config.json

The page reads `/config.json` once at boot (`@hardscapehouse/auth/runtime-config`).
**There is no default mode.** A missing file, a file that is not JSON, no `mode`
and an unknown `mode` each show an honest configuration error page (its words are
in `src/signin.ts`), never the demo cards. The file holds public facts only:

    { "mode": "demo" }

    { "mode": "console",
      "org": "gable",
      "consoleEndpoint": "https://<console host>/v1",
      "consoleProjectId": "<the HH project id>",
      "coreBaseUrl": "",
      "personas": false,
      "personasOpen": false,
      "dealer": { "name": "Gable Landscape Supply", "mark": "gable" } }

`coreBaseUrl` empty means the page's own origin. `personas` says the persona
route exists on this deployment (the testers' copy and the public copy);
`personasOpen` says it answers without a bearer (the public copy), so the cards
show on the sign in screen itself. `personaCaller` (`"public"` or `"partner"`, mirroring the core's
`CORE_PERSONA_CALLER`; absent, it follows `personasOpen`, and a value that disagrees with it is an
incomplete config) decides which cards show: on `public` the admin personas (general-manager,
financial-controller, hr, dealer-admin) are neither drawn nor enterable (principal ruling A6); on `partner`
every card shows. The deployment writes the file; this repository
ships none (the dev server's stub serves one, `test/stub/demo-stub.mjs`).

**demo** is everything described above: the phrase gated staging copy keeps its
tap a role page exactly, with its two sessionStorage keys.

**console** is the real sign in (item 7b of the auth cycle), against FB Console and
the core's exchange:

- `src/console.ts` is the state machine (starting, signed out, signing in, signed in
  at the role entry, landed), over the auth package's `AuthController` and a
  memory-only `SessionStore`. `src/console-views.ts` and `src/console-styles.ts`
  draw it; `src/signin.ts` holds the words of every state, the methods route and
  the persona route.
- The sign in screen shows the dealer's lockup and only the methods
  `GET /api/v1/auth/methods?org=<slug>` returns: `email_password` (an email and a
  password form) and `google` ("Continue with Google"); `microsoft` and
  `magic_link` draw nothing yet. Idle, signing in (disabled button, spinner, a
  polite status line), wrong credentials (the fields kept, focus back on the
  password), not allowed (the core's 403: "This account is not set up for this
  site. Ask your administrator to add you."), too many attempts (429), service
  unavailable (503), offline, a failed return from Google, and session ended each
  have their own words. The core never explains a refusal; the screen does.
- The password is a local of one submit call: never stored, logged or put in an
  address (the form is `method="post"` and its submit never navigates), with
  `autocomplete` `username` and `current-password`, and Enter submits.
- After sign in the header band carries the dealer, the Apps menu (the role's
  modules), the person's name and role from the exchange's user block, Theme,
  Feedback and Sign out. The role entry says "Continue as <the person's own role
  label>" and lands them where the matrix says (`landingFor` over `src/roles.ts`).
  A role with no screens on this site says so and offers no way in.
- Where `personas` is true the eight live and five coming cards of the demo appear
  (after sign in on the testers' copy, on the sign in screen on the public copy);
  a card calls `POST /api/v1/auth/persona {persona}` with the role code, adopts the
  answer through the same custody with no refresh, and shows a refusal in the card
  that earned it while the others keep working.
- **Custody.** The core's token lives in the controller's memory and nowhere else.
  The demo's `hh-demo.front-door.session.v1` key is never written in console mode
  (a leg reads both storages), and the display profile mirror carries no token.
  The page returns to sign in when a refresh ends the session, a persona's token
  runs out, or the person signs out.
- **The micro apps are NOT wired for console mode by this item.** Each of them
  reads the demo handoff in a demo-only seam (`import.meta.env.MODE === "demo"`)
  and builds its custody from pos-kit's `DemoAccount`; none of that is reachable
  from a shared package. Opened from a console front door they find no handoff and
  fall to their own demo entry. The wiring needed in each app is listed in the item
  7b report.

## Running the app against the local stub

Without a core, a local stub serves the mint in the contract's shape
(`DemoSessionResponse`, a JWT-shaped token whose exp claim the app reads):

    npm run dev

The dev server proxies `/api` to the stub (`FD_STUB_PORT`, default 4927) and
serves the app on `FD_APP_PORT` (default 4926).

## Tests

    npm run typecheck
    npm test

The node suites hold the mint validation, the session custody's two keys and the
catalog's shape (`test/unit.test.mjs`), the console words, methods and persona
routes (`test/signin.test.mjs`), the console state machine
(`test/console.test.mjs`) and its text contrast in both halves
(`test/contrast-console.test.mjs`). The console mode's legs are
`test/acceptance/console.pw.ts`; they also photograph every state, light and dark,
at 1280 and 390, into `FD_SHOTS_DIR` (default `/tmp/fd-console-shots`). The demo mode's Playwright suite
(`test/acceptance/front-door.pw.ts`, the app's acceptance spec) walks the
seven behaviours the D2 brief names against the stub harness. The exit test
(`test/acceptance/exit-test.sh`) builds the app, boots a throwaway Postgres
and the core's demo build behind a local stand-in for the image's phrase
gate, and walks the served picker with curl.
