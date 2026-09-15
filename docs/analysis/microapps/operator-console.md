> Origin: `analysis/microapps/operator-console.md` in `futurebuildai/hh_pro_dibbits` at commit 829d387baec93556ffa0e9242f4091e9be9544ec (read only source).
> Copied into `hh/docs/analysis/` by Session HH-2 lane HH-2.5; content below this header is unchanged.

# operator-console: Operator Console

Per DECISIONS.md D4 this is a launcher section owned by the launcher session (S3-L), rendered over hh's org-scoped operator endpoints; it is not an hh micro-app and not a manifest frontend. The brief is kept for the coverage it records.

Audience: operator (rendered here as an internal audience with cross-org reach)   Roles: fb_operator, super_admin, dealer_admin   Devices: desktop   Environment: office
Kit: dashboard   Brand: fb-tokens   Shell: fb-launcher
Offline: none. Scopes: none. Conflict policy: none. Why none: this surface holds the platform floor and the credential. The credential is write only, validated against the provider before it is stored so a bad paste is learned about at once rather than through a contractor's failed message, and nothing returns it (`hh_pro_dibbits/server/admin-api.ts`, `hh_pro_dibbits/CLAUDE.md`). Nothing here may be drafted offline and replayed.

## Three jobs
1. Read and write the platform floor a dealer's own override is overriding. The shipped roster already splits this exactly: the read is staff-wide so a dealer's administrator can see what their override is overriding, and the server marks the write with a capability flag rather than a fifth gate rung predicting it (`hardscapeos_dibbits/app/src/nav-access.ts`, the `hotkeys` entry).
2. Hold the assistant credential and set the per-account spend cap, then read usage. Usage is counted per contractor account, not in one shared counter, so the operator can answer whether to raise a cap for one crew rather than only whether anyone is using it (`hh_pro_dibbits/CLAUDE.md`). Counting never touches the credential file (`hh_pro_dibbits/server/admin-store.ts`).
3. Mint a demo session per role against a demo deployment, without touching a dealer's own data. The chooser is pre-auth and inert on every box but the demo droplet (`hardscapeos_dibbits/app/demo.html`, `app/src/_demo.ts`, `hardscapeos_dibbits/CLAUDE.md`).

## Shared entities and handoffs
- dealer config: write; read by dealer-admin and by every external micro-app for branding and the supplier's name via `fb://hh/dealer_config/<id>`
- credential: write only; no route out of the process, and a request carrying a forwarded key spends the dealer's or gets refused (`hh_pro_dibbits/CLAUDE.md`)
- usage counter: read; per contractor account, with the account id bounded and validated because a key-shaped string that stops behaving like a key makes the count vanish (same file)
- demo session: write; a role-shaped session handed to whichever internal micro-app that role lands on

## What it replaces now
- HardscapeOS: `admin/hotkeys` (`hos-platform-hotkeys`, registered in `app/src/dashboard-nav.ts`, gated admin in `app/src/nav-access.ts`) and the demo chooser entry `demo.html` (`app/src/_demo.ts`)
- HH Pro: the credential and usage half of `admin.html` (`server/admin-api.ts`, `server/admin-store.ts`). The branding, terms and flags half belongs to dealer-admin.

## States to design once
- empty: no dealer configured shows the provisioning action, not a blank org list.
- loading: the org list and the usage figures skeleton separately, since one is cheap and one is not.
- offline: the console declines to open; a platform floor read from a cache is a floor nobody can vouch for.
- conflict: another operator's write is refused with the current revision named, the same optimistic-concurrency rule the dealer console uses.
- error: an unreachable provider refuses the credential with a service-unavailable answer rather than storing an unverified key, because only a success proves a key (`hh_pro_dibbits/CLAUDE.md`).

## Device and environment notes
Desktop, seated, and never on a shared device. The gate is a development gate and says so in its own docstring: one shared secret, no identity, no audit, no rotation, compared in constant time and loopback only unless explicitly widened (`hh_pro_dibbits/CLAUDE.md`, `server/admin-api.ts`). Whether the operator gets a micro-app inside the app at all, or only the launcher, is an open question in the catalog: the shipped platform roster argues for a surface inside the app whose read is staff-wide and whose write is the operator's, which is not the same shape as a separate console.

## Sources
- hardscapeos_dibbits/app/src/nav-access.ts
- hardscapeos_dibbits/app/src/dashboard-nav.ts
- hardscapeos_dibbits/app/demo.html
- hardscapeos_dibbits/app/src/_demo.ts
- hardscapeos_dibbits/CLAUDE.md
- hh_pro_dibbits/server/admin-api.ts
- hh_pro_dibbits/server/admin-store.ts
- hh_pro_dibbits/CLAUDE.md
