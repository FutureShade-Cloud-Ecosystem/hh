> Origin: `analysis/microapps/money-desk.md` in `futurebuildai/hh_pro_dibbits` at commit 829d387baec93556ffa0e9242f4091e9be9544ec (read only source).
> Copied into `hh/docs/analysis/` by Session HH-2 lane HH-2.5; content below this header is unchanged.
> In-body `analysis/...` paths resolve to `docs/analysis/...` in this copy.

# money-desk: Money Desk

Audience: internal   Roles: financial_controller, payables_receivables, general_manager   Devices: desktop   Environment: office
Kit: list-detail   Brand: fb-tokens   Shell: hh-work
Offline: none. Scopes: none. Conflict policy: none. Why none: every screen here writes to a double-entry ledger or decides money against a customer, and the product's own conventions make that non-negotiable (integer cents, no float money, the ledger landing before the first financial document table, in `hardscapeos_dibbits/CLAUDE.md`). A queued journal is a posting nobody balanced. A cached aging number read at a customer's expense is worse than an honest refusal to open.

## Three jobs
1. Work the ledger and the posting queue, and produce the period pack: income statement, balance sheet, general ledger detail, aging. The statements surface is classified `reading` precisely because it is a document the dealer prints and exports (`hardscapeos_dibbits/app/src/app-shell.ts`, the `PAGE_TYPE` note on `statements`).
2. Reconcile the bank import through import, match and rules (`hos-bank-recon`, mounted in `app/src/app-shell.ts`).
3. Work receivables and payables: aging, credit holds, statements, the bounced-payment queue, collections, and the vendor bill with its three-way match (`hos-ar-console` in `app/src/ar-console.ts`; the BILLS tab in `app/src/purchasing-console.ts`).

## Shared entities and handoffs
- invoice: read; written by counter-till, and payment applied here is read by pro-pay via `fb://hh/invoice/<id>`
- payment: read and write; a bounce marked here reaches the contractor in pro-pay via `fb://hh/payment/<id>`
- vendor_bill: write against a purchase_order from buying-and-stock via `fb://hh/vendor_bill/<id>`
- journal: write; an over-threshold journal parks an approval_request for manager-home via `fb://hh/approval_request/<id>`
- lien holdback: write; the co-signer step already asks for a second finance role by name (`app/src/lien-holdbacks.ts`)
- tax rule: write; read by counter-till and quote-desk at price time

## What it replaces now
- HardscapeOS: `finance/gl`, `finance/bankrec`, `finance/statements`, `finance/ar`, `compliance/lien`, `compliance/tax` (all registered in `app/src/dashboard-nav.ts`, all manager-gated in `app/src/nav-access.ts`), plus the bills tab of `purchasing/purchasing` (`app/src/purchasing-console.ts`)
- HH Pro: nothing directly. The contractor's payment lands here; the contractor's view of it is pro-pay.

## States to design once
- empty: an empty posting queue or an empty bounce queue is a result, stated as one, with the period it covers.
- loading: figures render only when complete. A partially summed total is a wrong number with a spinner next to it.
- offline: the app declines to open and says the ledger is not readable offline, which is the honest answer here.
- conflict: not applicable, because nothing is queued. A stale read is refused rather than reconciled.
- error: the module prefix is stripped at the one door out, so a controller reads the failure, not the package layout (`internal/platform/httpx`, described in `hardscapeos_dibbits/CLAUDE.md`).

## Device and environment notes
Desktop only, seated, with a printer and an export in reach. This is the one app in the internal shell where a reading-width measure is right rather than wrong, and the shipped layout contract already says so in the same breath as saying the load board is not the same kind of object (`app/src/app-shell.ts`). Nothing here is designed for gloves, and nothing here should be reachable from a shared yard tablet: the approval authority it carries is narrower than a tier test, by explicit ruling (`backend/internal/identity/rbac.go`, `railApproverRoles`).

## Sources
- hardscapeos_dibbits/app/src/ar-console.ts
- hardscapeos_dibbits/app/src/purchasing-console.ts
- hardscapeos_dibbits/app/src/lien-holdbacks.ts
- hardscapeos_dibbits/app/src/app-shell.ts
- hardscapeos_dibbits/app/src/dashboard-nav.ts
- hardscapeos_dibbits/app/src/nav-access.ts
- hardscapeos_dibbits/backend/internal/identity/rbac.go
- hardscapeos_dibbits/CLAUDE.md
