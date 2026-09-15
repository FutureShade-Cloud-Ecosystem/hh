> Origin: `analysis/microapps/buying-and-stock.md` in `futurebuildai/hh_pro_dibbits` at commit 829d387baec93556ffa0e9242f4091e9be9544ec (read only source).
> Copied into `hh/docs/analysis/` by Session HH-2 lane HH-2.5; content below this header is unchanged.

# buying-and-stock: Buying and Stock

Audience: internal   Roles: procurement_manager, yard_manager, branch_manager   Devices: desktop, tablet   Environment: office
Kit: list-detail   Brand: fb-tokens   Shell: hh-work
Offline: read_cache_plus_outbox. Scopes: purchase_orders, vendors, inventory_lookup, cycle_counts. Conflict policy: purchase_orders server_wins, cycle_counts manual. Why this mode: a buyer drafts at a desk and sometimes counts on a tablet in the racks, so the draft and the count must survive a dead spot, but placing an order with a vendor is a commitment and resolves online. A cycle count is `manual` for the same reason a shortage is: two people counting the same rack is a disagreement to show, not an average to compute.

## Three jobs
1. Draft an order, place it, and read where each placed order got to. This is the ORDERS tab of the shipped console, which describes itself as three jobs done by three different people (`hardscapeos_dibbits/app/src/purchasing-console.ts`).
2. Close the gap the yard reported between what was sold and what is on hand (`fetchOrderGaps` and the procurement gap rows in `app/src/yard-console.ts`).
3. Count stock and settle the on-hand ledger, including landed cost and the weighted average behind it (`hos-inventory-home` and its On-Hand, Cycle Counts and Landed Cost sub-tabs, `app/src/inventory-home.ts`).

## Shared entities and handoffs
- purchase_order: write; handed to yard-pick for receiving and to money-desk for the vendor bill via `fb://hh/purchase_order/<id>`
- stock_move: read; written by yard-pick and counter-till via `fb://hh/stock_move/<id>`
- product: read; written by dealer-admin via `fb://hh/product/<id>`
- vendor: write; read by money-desk
- landed cost: write; the split is the same rounding authority every money split uses (`moneyutil.AllocateLargestRemainder`, `hardscapeos_dibbits/CLAUDE.md`)

## What it replaces now
- HardscapeOS: the orders tab of `purchasing/purchasing` (`app/src/purchasing-console.ts`) and `inventory/inventory` (`app/src/inventory-home.ts`), both registered in `app/src/dashboard-nav.ts` and both manager-gated in `app/src/nav-access.ts`
- HH Pro: nothing. The contractor never sees the buy side; a special-order line reaches them only as a price from the quote desk.

## States to design once
- empty: no outstanding order says so with the one control that creates one, rather than an empty table with headers.
- loading: the order list keeps its columns; a cost column that shifts is a cost somebody misreads.
- offline: the draft is marked queued and placing is unavailable, because a placed order is a message to a vendor.
- conflict: a receipt already entered by the yard wins, and the buyer sees who entered it and what it changed.
- error: a variance that blocks a post names the variance and who may accept it, which is the three-way match's own vocabulary.

## Device and environment notes
Mostly a desk, with a tablet in the racks for counting. Inventory is a manager-gated read (`app/src/nav-access.ts`), so this app is not offered to the frontline at all: a counter clerk who needs a stock answer gets it inside counter-till, not by opening a ledger. The console is classified `work` in the layout contract, which is what stops the reading-width cap from leaving nearly half the window dead on a wide screen (`app/src/app-shell.ts`, the `PAGE_TYPE` header and its measured numbers).

## Sources
- hardscapeos_dibbits/app/src/purchasing-console.ts
- hardscapeos_dibbits/app/src/inventory-home.ts
- hardscapeos_dibbits/app/src/yard-console.ts
- hardscapeos_dibbits/app/src/nav-access.ts
- hardscapeos_dibbits/app/src/app-shell.ts
- hardscapeos_dibbits/CLAUDE.md
