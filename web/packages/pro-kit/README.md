# @hardscapehouse/pro-kit

The shared ground of the three Pro contractor apps (Pro Procurement, Pro Pay, Pro Field), so three
builds can stand on it in parallel without colliding. Plain logic is imported from the package root;
the custom elements are imported by their own path (importing one registers it, which needs a browser).

## What is here

| Piece | File | What it is |
| --- | --- | --- |
| Capability gate | `src/capabilities.ts` | `can(role, capability)`, the nine capabilities and five roles copied from the core. `test/capabilities.test.mjs` re-reads the core's Go source (both `CapabilitiesFor` twins) and the generated wire types on every run, so a capability or role added there and not here fails. |
| TenantSession | `src/session.ts` | The seam: sign in, sign out, the live token and expiry, the role and capabilities exactly as `GET /api/v1/tenant/me` answers. Over the auth package's `SessionStore` and `AuthController`. Memory only: the custody mirror is given an in memory double, never web storage. |
| Tenant client | `src/client.ts` | `tenantClient(session)`: an `ApiClient` whose header provider is the auth package's tenant one (bearer, never `X-Org`). |
| Sign in adapters | `src/stub-adapter.ts`, `src/real-exchange.ts` | The stub (dev server and tests, through the stub's own door) and the named seam `realExchangeAdapter()`, the one place the auth lead's go fills. The file header lists what the real adapter needs. |
| Words | `src/format.ts` | US dollars through pos-kit's `formatDollars`, tax exactly as the core returns it (never recomputed), a placeholder for an unknown figure, bare calendar days and the dealer's own zone, the mono face. |
| Chips | `src/chips.ts`, `src/board.ts` | Stage chips (Plan, Quote, Order, Invoice) and status and tracking chips on design system tokens, never the dealer's gold. `board.ts` is the contract's `OrderCard` state map, pinned by test. |
| Surfaces | `src/surfaces.ts` | The three apps and their surfaces (the board's names), each gated by one capability. |
| Elements | `src/frame.ts`, `src/signin.ts`, `src/state-view.ts`, `src/app-base.ts` | `hh-pro-frame` (the thin frame the shells cycle can replace), `hh-pro-signin`, `hh-pro-state` (empty, loading, error, refusal) and `ProAppBase`, which wires them to the session and runs a landing surface. |
| Tenant stub | `test-stub/tenant-stub.mjs`, `test-stub/tenant-data.mjs` | A node http stub serving the 47 tenant routes the core serves, with Summit Ridge Builders data. `test/stub-contract.test.mjs` holds it to the generated client, the core's own route registrations, the contract's response shapes and the core's refusal sentences. |

## Using the stub

`node test-stub/tenant-stub.mjs` serves on `ROLE_STUB_PORT` (default 56810). Beside the core's routes it has its own
door, used only by tests and the dev server: `POST /_stub/session` (sign in), `POST /_stub/reset`, and
`POST /_stub/control` (`{fault: {path, status}}`, `{delayMs}`, `{empty: "orders"}`) so a leg can show a
loading, empty, error or refusal state on demand. The five personas are the contractor roles:
`contractor.owner@hh-demo.invalid`, `.buyer`, `.pm`, `.ap` and `.field`, all with the stub password
`stub-password` (a fake, for the stub only).

## Rules this package holds

- No secret, no token, no card data anywhere. Money never queues. Sandbox only.
- A tenant call never carries `X-Org`.
- The design system's position gate scans this package as a dependent: no `.` or `@` binding, and bound
  values only in class, role, `data-` and `aria-` attributes.
