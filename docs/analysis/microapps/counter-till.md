> Origin: `analysis/microapps/counter-till.md` in `futurebuildai/hh_pro_dibbits` at commit 829d387baec93556ffa0e9242f4091e9be9544ec (read only source).
> Copied into `hh/docs/analysis/` by Session HH-2 lane HH-2.5; content below this header is unchanged.
> In-body `analysis/...` paths resolve to `docs/analysis/...` in this copy.

# counter-till: Counter Till

Audience: internal   Roles: inside_sales, sales_manager, branch_manager, general_manager   Devices: desktop, tablet, kiosk   Environment: counter
Kit: scan-and-confirm   Brand: fb-tokens   Shell: hh-work
Offline: read_cache_only (DECISIONS.md D5: the till has no outbox). Scopes: customers, products, price_matrix, parked_tickets, invoices, warranty_claims, all cached for reading; nothing queues, and a parked ticket edited with no signal is held in interaction state until the signal returns. Why this mode and not an outbox: the till commits money, and the two gates that decide whether it may (the margin floor and the credit or COD check) resolve server side in `backend/internal/identity/rbac.go` (`CheckMarginAt`, `CheckCredit`). A queued tender would be a promise the gate never saw. Reads (a customer, a price, a parked ticket) cache; a tender does not queue.

## Three jobs
1. Ring a sale: scan or search, resolve the customer, take the tender, hand over the receipt. Today this is the whole of `hos-pos-terminal` (`hardscapeos_dibbits/app/src/pos-terminal.ts`), whose own imports name the pieces: `pos-scan-box`, `pos-search-drop`, `pos-customer-picker`, `pos-split-tender`, `till-close`.
2. Find an invoice and act on it: print it, email it, take a cheque against it. That is the DIB-510 invoice register, filed in Sales rather than Finance precisely because the registry records it as the screen a counter person keeps open continuously (`hardscapeos_dibbits/app/src/dashboard-nav.ts`).
3. Take a return, or capture a warranty claim from the person in front of you. Return approvals is `sales/returns`; claim capture and lifecycle is the staff-gated half of the warranty surface (`hardscapeos_dibbits/app/src/nav-access.ts`, the `warranty: 'authed'` entry and its comment).

## Shared entities and handoffs
- order: write; handed to yard-pick as a pick ticket and to dispatch-board as a load candidate via `fb://hh/order/<id>`
- invoice: write; read by money-desk and by pro-pay via `fb://hh/invoice/<id>`
- quote: read; handed from quote-desk and converted here, which is where the selling-role gate applies (`CanSellOnCredit`)
- customer: read; written by dealer-admin via `fb://hh/customer/<id>`
- warranty_claim: write; handed to manager-home for the board via `fb://hh/warranty_claim/<id>`
- payment: write; read by money-desk via `fb://hh/payment/<id>`

## What it replaces now
- HardscapeOS: `sales/pos` (`app/src/pos-terminal.ts`), `sales/invoices` (`hos-invoice-register`, mounted at `app/src/app-shell.ts`), `sales/returns` (`hos-return-approvals`), and the claim-capture half of `warranty/warranty` (`app/src/warranty-surface.ts`)
- HH Pro: nothing. The contractor never touches the till; the counter sale reaches them as an invoice in pro-pay.

## States to design once
- empty: no ticket open is the resting state, not an error. The screen shows the scan field focused and the hot list, never a blank panel.
- loading: the ticket skeleton holds the line rows and the tender bar in place, so focus does not move under the hands.
- offline: a banner names what still works (lookup, parking a ticket) and what does not (tender), because the gates that refuse are server side.
- conflict: a parked ticket edited on another terminal surfaces a review card rather than merging, since a merged line quantity is a quantity nobody entered.
- error: the counter wording, never the wire status. `counterRingMessage` and `salePillLabel` already exist for this (`app/src/_pos.ts`).

## Device and environment notes
The person is standing, mid-conversation, with gloves nearby and a line forming, and every decision serves time-to-next-action (`hardscapeos_dibbits/.claude/skills/hardscape-ui-design/SKILL.md`). Focus returns to the scan field after every add, because muscle memory is sacred (same file). The terminal is a shared device: a PIN or biometric unlock per context 4.5 is required, and the audit row records the unlocked user, since the till already parks a ticket per person (`pos-parked`). Kiosk is listed because the same document already hosts the forced-acknowledgement overlay on a shared screen (`app/src/kiosk-takeover.ts`).

## Sources
- hardscapeos_dibbits/app/src/pos-terminal.ts
- hardscapeos_dibbits/app/src/_pos.ts
- hardscapeos_dibbits/app/src/dashboard-nav.ts
- hardscapeos_dibbits/app/src/nav-access.ts
- hardscapeos_dibbits/app/src/app-shell.ts
- hardscapeos_dibbits/app/src/warranty-surface.ts
- hardscapeos_dibbits/backend/internal/identity/rbac.go
- hardscapeos_dibbits/.claude/skills/hardscape-ui-design/SKILL.md
