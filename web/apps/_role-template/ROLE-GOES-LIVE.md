# Role goes live checklist

Every step a new role takes to go live on the demo box, in the order the changes must land.

## 1. Demo flows row

`core/internal/demo/flows/flows.go`, the `accounts` map around line 77.

Add one entry per new role:

```go
"<flow-id>": {
    Flow:           "<flow-id>",
    PlatformUserID: "<new-uuid-v4>",
    Email:          "<localpart>@hh-demo.invalid",
    DisplayName:    "<Display Name (demo)>",
    Label:          "<Card word>",
    Role:           "<role-code>",
},
```

`PlatformUserID` must be a new UUID that does not appear elsewhere in the codebase.
`Email` lives at `hh-demo.invalid`, the reserved demo domain.
`Label` is the card's accessible name: the demo adapter's contract word, never decorated.
`Role` is the staff role code the grant in step 2 names.

## 2. Demo populate user and grant

`core/internal/demo/populate.go`.

### 2a. User constants

Around lines 62 to 87, add a new block of constants after the existing `sales` and `manager` blocks:

```go
<varName>UserID      = "<same-uuid-as-platformuserid-in-flows-go>"
<varName>PlatformID  = "<same-uuid>"
<varName>Email       = "<same-email>"
<varName>DisplayName = "<same-displayname>"
<varName>RoleCode   = "<same-role-code>"
```

`RoleCode` values match the roles catalog (migration 0015's planted rows, resolved by code in `insertRoleAccountAccess`).

### 2b. Insert the user row

`insertRoleAccounts`, around line 384. Add the new user to the slice:

```go
{<varName>UserID, <varName>Email, <varName>DisplayName},
```

### 2c. Role grant and branch grant

`insertRoleAccountAccess`, around lines 410 and 425. Add the new user to both loops:

```go
// role grant
{<varName>UserID, <varName>RoleCode},
// branch grant
<varName>UserID,
```

Both inserts are `INSERT ... SELECT` that refuse when the catalog lacks the role or the locations table lacks the branch. A missing role causes a run-time refusal; the roles catalog must have the entry before the populate runs.

### 2d. Static directory linkage

`RunPopulate`, around line 342. Add the new email to `staticDirectory`:

```go
Directory: staticDirectory{
    salesEmail:   salesPlatformID,
    managerEmail: managerPlatformID,
    <newEmail>:   <newPlatformID>,  // <-- add this
},
```

This links the user row to its platform identity so the identity layer resolves authority correctly.

## 3. Move from COMING_ROLES to LIVE_ROLES

`web/apps/front-door/src/roles.ts`, the `LIVE_ROLES` array around line 85.

Add a `LiveRole` entry:

```typescript
{
  id: "<role-id>",
  flow: "<flow-id>",
  label: "<Card word>",
  line: "<One sentence of what the role opens.>",
  icon: "<icon-registry-id>",
  tiles: [<ModuleTile>, ...],
},
```

`flow` must match the `Flow` field from `flows.go`.
`label` must match the `Label` field from `flows.go`.
`tiles` lists the `ModuleTile` objects the role can open; each tile is either a live module (with an `href`) or a coming module (with `href: null`, which opens the polite coming-soon page).

If the role has a new module not yet on the box, that module appears in the role's `tiles` as `href: null` and also appears in `COMING_ROLES` until its app is built.

## 4. App prefix in the image

`deploy/demo/Dockerfile`: add a new stage for the role app, following the existing pattern:

```dockerfile
FROM node:22-alpine AS build-<app-name>
WORKDIR /app
COPY web/package.json web/pnpm-lock.yaml ./
RUN corepack enable && pnpm install --frozen-lockfile
COPY web/apps/<app-name>/ ./web/apps/<app-name>/
RUN VITE_BASE=<prefix>/ pnpm --filter <app-name> run build
```

Add a corresponding `COPY --from=build-<app-name>` to the final stage and `COPY web/apps/<app-name>/dist/ /srv/app/<app-name>/`.

`deploy/demo/Caddyfile.template`: add a route for the new prefix. If the app requires authentication, follow the `/till/` pattern (basic auth phrase gate, `handle_path` to strip prefix). If unauthenticated, follow the `/public/` pattern. The phrase gate lives in the environment and is injected by the entrypoint; never commit a phrase value.

## 5. Role exit test leg

`deploy/demo/exit-test.sh`: add a leg that confirms the role app is served at its prefix and returns the expected HTTP status.

Example unauthenticated leg:

```sh
# pd-app-root
pd_app_root=$(curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:58080/public/)
test "$pd_app_root" = "200" || failed="$failed pd-app-root "
```

If the app requires auth, mint a session first and send the bearer token:

```sh
# ct-app-root
ct_token=$(curl -s -X POST http://127.0.0.1:58080/api/v1/demo/session \
  -H "Content-Type: application/json" \
  -d '{"flow":"<flow-id>"}' | jq -r '.token')
ct_app_root=$(curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $ct_token" \
  -H "X-Org: hh-demo" \
  http://127.0.0.1:58080/<prefix>/)
test "$ct_app_root" = "200" || failed="$failed ct-app-root "
```

## 6. Common acceptance legs

After the image boots and the database is populated, every role app passes the four common legs before the exit test reports pass. These legs are documented in roadmap section 2 (HH-ROLES-ROADMAP-r1-draft.md):

- **L-card** — The role card appears on the front door picker with the correct label and accessible name.
- **L-wall** — After tapping the card and adopting the session, the role app renders without a credential error or a redirect to the gate.
- **L-branch** — The role app shows data scoped to the demo branch (Trenton), not a blank state caused by a missing branch grant.
- **L-cost** — The role app's primary workflow completes at least one costed action (a sale, a quote, a stock movement) without a payment or pricing error.

Run the full exit test suite from `deploy/demo/exit-test.sh` to confirm all legs pass.
