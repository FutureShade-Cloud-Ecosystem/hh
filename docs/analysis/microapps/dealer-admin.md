> Origin: `analysis/microapps/dealer-admin.md` in `futurebuildai/hh_pro_dibbits` at commit 829d387baec93556ffa0e9242f4091e9be9544ec (read only source).
> Copied into `hh/docs/analysis/` by Session HH-2 lane HH-2.5; content below this header is unchanged.

# dealer-admin: Dealer Admin

Audience: internal   Roles: super_admin, general_manager, hr, dealer_admin, sales_manager   Devices: desktop   Environment: office
Kit: list-detail   Brand: fb-tokens   Shell: hh-work
Offline: none. Scopes: none. Conflict policy: none. Why none: every write here changes who may do what, what something costs, or what a contractor is told. The config save is already optimistic-concurrent with a revision that a missing header fails exactly like a stale one, on the reasoning that a caller who sends none cannot have read the current state (`hh_pro_dibbits/CLAUDE.md`, `hh_pro_dibbits/server/admin-api.ts`). An offline queue would be a second way to lose that guarantee.

## Three jobs
1. Add and remove staff, assign roles and branch grants, deactivate an account. The role catalog is thirteen business roles across three tiers plus the platform role, and the assignable set is one function (`AllRoles` in `hardscapeos_dibbits/backend/internal/identity/rbac.go`; the seed is `backend/migrations/0015_rbac_roles.sql`).
2. Govern pricing: tier rules, category rules, customer overrides, quantity breaks (`hos-pricing-admin`, mounted in `app/src/app-shell.ts`, manager-gated in `app/src/nav-access.ts`).
3. Configure what a contractor sees: branding tokens, terms, company name, feature flags, assistant spend cap. The dealer's name is configuration and not a literal, and every contractor-facing sentence reads it through one function (`hh_pro_dibbits/src/core/config/runtime.ts`).

## Shared entities and handoffs
- user: write; every micro-app reads the acting identity from it, rebuilt from the database per request so a change takes effect at once (`StaffContext`, `backend/internal/identity/rbac.go`)
- product and price rule: write; read by quote-desk, counter-till and pro-procurement via `fb://hh/product/<id>`
- dealer config: write; read by every external micro-app for branding and the supplier's name via `fb://hh/dealer_config/<id>`
- audit entry: read; written by every privileged action anywhere, gated to tier one and global scope alone (`app/src/nav-access.ts`)
- usage counter: read; counted per contractor account so one busy crew cannot lock the rest out

## What it replaces now
- HardscapeOS: `admin/staff` (`hos-staff-roles`), `admin/audit` (`hos-audit-log`), `admin/assist-admin` (`hos-assist-admin`), `sales/pricing` (`hos-pricing-admin`), all in `app/src/dashboard-nav.ts` and `app/src/app-shell.ts`
- HH Pro: `admin.html` and `src/admin/AdminApp.tsx` in their branding, terms, flags and spend-cap halves. The credential and usage half belongs to operator-console.

## States to design once
- empty: a dealer with no branding set shows the platform defaults as the live preview, labelled as defaults, never as a blank form.
- loading: the token editors keep their swatches so a colour does not appear to change while state arrives.
- offline: the console declines to open, because a stale revision cannot be used to write.
- conflict: a stale revision becomes "reload and re-apply" rather than a silent overwrite, and edits typed during a save are kept unless the draft is byte identical to what was sent (`hh_pro_dibbits/CLAUDE.md`).
- error: a refused colour names the pattern it failed, because these strings are interpolated into a stylesheet and refusing is verifiable where escaping is not (same file).

## Device and environment notes
Desktop, seated, deliberate. It reads as an admin tool, dense and explicit about consequences, rather than borrowing a thumb-first layout (`hh_pro_dibbits/src/admin/AdminApp.tsx`). Nothing here is offered on a shared yard tablet. The credential is never displayed, because no route returns one, and the most the console can show is a mask and a present flag (same file). Feature flags gate presentation only: a hidden tab is not a permission, and the actions layer still guards every mutation (`hh_pro_dibbits/CLAUDE.md`).

## Sources
- hardscapeos_dibbits/backend/internal/identity/rbac.go
- hardscapeos_dibbits/backend/migrations/0015_rbac_roles.sql
- hardscapeos_dibbits/app/src/dashboard-nav.ts
- hardscapeos_dibbits/app/src/nav-access.ts
- hardscapeos_dibbits/app/src/app-shell.ts
- hh_pro_dibbits/src/admin/AdminApp.tsx
- hh_pro_dibbits/server/admin-api.ts
- hh_pro_dibbits/src/core/config/runtime.ts
- hh_pro_dibbits/src/core/domain/config.ts
