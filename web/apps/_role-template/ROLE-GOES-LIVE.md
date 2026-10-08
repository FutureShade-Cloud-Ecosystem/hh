# A role goes live

The steps a role takes from a coming card to a working app on the demo box. Every
line number is on main as this file was written; check the file before you edit it.
Do the steps in order, and keep one signed off commit per step.

## 0. Start from the template

1. Copy `web/apps/_role-template/` to `web/apps/<app-name>/`. The folder name, the
   `name` in `package.json` (`@hardscapehouse/<app-name>`), the element tag in
   `index.html` and `src/app-root.ts` (`defineElement`, last line), the class name,
   the `<title>`, and the stub and app ports (`ROLE_STUB_PORT` and `ROLE_APP_PORT` in
   `vite.config.ts` and `test/stub/role-stub.mjs`; pick a free pair) are the names a
   copy changes. Delete this file from the copy.
2. Run `npm install` in `web/` so the workspace lock names the new app (CI runs
   `npm ci`, which refuses a lock that lacks a workspace). Commit `web/package-lock.json`
   with the app.
3. Name the app in the design system's dependent lock, `web/packages/design-system/test/no-raw-markup.test.mjs`
   (the list of dependent packages in the test that walks the workspace, line 327), and keep
   the copy's templates inside that package's position rules: attribute bindings only, no `.` or `@`
   bindings. The design system's gates scan every dependent.
4. In the copy, `npm run typecheck` and `npm test` must pass before anything else
   changes. CI runs both for every folder under `web/apps/`, the template included.
5. Add the app to `manifest.yaml` the way `counter-till` is (line 52), if the manifest
   names it as a frontend.

## 1. The demo flow row (core)

`core/internal/demo/flows/flows.go`:

- Add the role's account to the `accounts` map (line 77): flow name, platform user id,
  email at the reserved domain, display name, label, and the role code in lower case.
- Add it to `All()` (line 106). `All()` lists the accounts by hand, so a map entry
  alone never reaches the flows route.
- Update the closed table's comment (line 17) and `flows_test.go` (line 14 checks the
  order and the count).

## 2. Populate's user and branch grant (core)

`core/internal/demo/populate.go`:

- Add the account's constants beside the other two (lines 62 to 77): user id, platform
  id, email, display name, and the role code in upper case (the roles catalog's spelling).
- Add the account to the three lists that walk the accounts: the users insert (line 385),
  the role insert (line 411) and the role verify (line 440).
- Add the user id to the two branch grant lists: the insert (line 425) and the verify
  (line 455). Every grant points at the one demo branch (line 80).
- The ids, emails and names here must equal the flows row by hand: nothing checks the
  copy for you.

## 3. The card moves from coming to live (front door)

`web/apps/front-door/src/roles.ts`:

- Widen the two unions on `LiveRole` (`id` at line 60, `flow` at line 62).
- Remove the role from `COMING_ROLES` (line 115) and add it to `LIVE_ROLES` (line 89)
  with its label, its line and its tiles. The label must equal the flows row's label.
- Add a tile constant beside `TILL_TILE` (line 48) whose `href` is the app's prefix,
  and give it to the role's tiles. A tile with `href: null` opens the coming soon page.
- If the role also appears as a tile on another live role, update that role's tiles.

`web/packages/pos-kit/src/ported/entry.ts` keeps the card marks and one liners by flow
name (lines 62 to 69); add the flow to both maps or the till's entry view draws the
neutral square. Update `web/apps/front-door/test/unit.test.mjs` and the front door's
stub (`test/stub/demo-stub.mjs`, flows at line 37) for the new count.

## 4. The app's prefix in the image

- `deploy/demo/Dockerfile`: one build line after the public desk's (line 58), with the
  app's own `--base=/<prefix>/` (add `--mode demo` for a staff app, so the front door
  handoff is compiled in), one `mkdir` entry on line 52 and one `COPY` after line 78.
- `deploy/demo/Caddyfile.template`: one `handle /<prefix>/*` block beside the till's
  (line 70), behind the same `basic_auth`, with its own `try_files` fallback.
- `web/apps/front-door/src/roles.ts`: the tile's `href` from step 3 is the same prefix.

## 5. The role's exit test leg

`deploy/demo/exit-test.sh`: a `page_has` line and a `page_gated` line beside the others
(lines 247 to 253) for the new prefix, and, if the flows route now answers more than two
flows, the flows leg (line 273) widened to match. Run the whole script; it builds the
image and boots it on a fresh database.

## 6. The common acceptance legs

Each role's first increment carries these four (the roadmap's section 2), each red first:

- **L-card:** the role signs in from its card on the front door.
- **L-wall:** a surface at None for the role is refused by the server.
- **L-branch:** the role reads its own branch only.
- **L-cost:** cost is seen or hidden for the role as the decided matrix says.

The till's acceptance folder (`web/apps/counter-till/test/acceptance/`, legs `10-world`,
`20-sales`, `30-manager`) is the pattern. A leg that rewords or retitles anything a
person or a test sees also updates `test/acceptance/claims.ts`.
