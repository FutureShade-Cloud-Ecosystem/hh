> Origin: `analysis/microapps/pro-pay.md` in `futurebuildai/hh_pro_dibbits` at commit 829d387baec93556ffa0e9242f4091e9be9544ec (read only source).
> Copied into `hh/docs/analysis/` by Session HH-2 lane HH-2.5; content below this header is unchanged.
> In-body `analysis/...` paths resolve to `docs/analysis/...` in this copy.

# pro-pay: Pro Pay

Audience: external   Roles: contractor_ap, contractor_owner   Devices: desktop, phone   Environment: office
Kit: list-detail   Brand: hh-pro   Shell: hh-pro
Offline: read_cache_only. Scopes: invoices, payments, payment_methods, aging. Conflict policy: invoices server_wins, payments server_wins. Why read cache only: paying is the one contractor action that moves money, and the amount charged is computed by the same function that renders the amount shown so the two cannot drift (`quotePayment`, described in `hh_pro_dibbits/CLAUDE.md`). A queued payment would be an amount agreed offline against an aging figure that has since moved. Reading what is owed offline is useful; charging offline is not.

## Three jobs
1. Select open invoices and pay them, with the card fee shown per method before the method is chosen, because that difference is real money (`hh_pro_dibbits/src/ui/pages/PayPage.tsx`, `src/core/actions/payments.ts`).
2. Read what is past due by the one definition of late the aging bucket and the overdue flag both derive from (`src/core/selectors/ar.ts`; the two used to disagree, which is why the definition is single now).
3. Add or remove a stored payment method (`src/ui/components/pay/`, and on the ERP side `app/src/portal/portal-payment-methods.ts`).

## Shared entities and handoffs
- invoice: read; written by counter-till and money-desk via `fb://hh/invoice/<id>`
- payment: write; read by money-desk, where a decline is data the screen must render rather than an error that throws the record away via `fb://hh/payment/<id>`
- payment_method: write; tokenized and vaulted, never held in the client (`hardscapeos_dibbits/docs/contractor-portal-plan.md`)
- order: read only; a link back to pro-procurement via `fb://hh/order/<id>`
- project: read; spend rolls up by job

## What it replaces now
- HardscapeOS: `portal /invoices` (`app/src/portal/portal-invoices.ts`) and `portal /payment-methods` (`app/src/portal/portal-payment-methods.ts`), both capability gated on `view_billing` and `manage_payment_methods` (`app/src/portal/portal-shell.ts`, `portal-api.ts`)
- HH Pro: `/pay` (`src/ui/pages/PayPage.tsx`)

## States to design once
- empty: nothing owed is stated as a result with the period it covers, not an empty table.
- loading: the pay bar stays visible and reads its teaching line, because the mechanic is taught in place (`hh_pro_dibbits/CLAUDE.md`).
- offline: balances are stamped with when they were pulled and the pay control is unavailable, coloured off rather than faded, since fade is not a state.
- conflict: an invoice paid elsewhere or reissued shows the server's version before anything is selected.
- error: a decline is rendered as a result with the reason and the retry path, not as a failure of the action.

## Device and environment notes
Often a desktop in a small office, sometimes a phone. Money and quantities use tabular figures globally so a column does not jitter (`src/ui/styles/theme.css`). The pay bar is always visible and names the mechanic when nothing is ticked. Disabled must look disabled everywhere and not only inside the shared button, which was the exact defect found on the screen where somebody decides to spend money (`hh_pro_dibbits/CLAUDE.md`). This app is the only one the accounts-payable role holds, and the refusal it shows elsewhere names who can act instead of saying no (`src/core/actions/team.ts`).

## Sources
- hh_pro_dibbits/src/ui/pages/PayPage.tsx
- hh_pro_dibbits/src/core/actions/payments.ts
- hh_pro_dibbits/src/core/selectors/ar.ts
- hh_pro_dibbits/src/core/domain/payment.ts
- hh_pro_dibbits/src/core/domain/team.ts
- hardscapeos_dibbits/app/src/portal/portal-invoices.ts
- hardscapeos_dibbits/app/src/portal/portal-payment-methods.ts
- hardscapeos_dibbits/app/src/portal/portal-shell.ts
- hardscapeos_dibbits/docs/contractor-portal-plan.md
