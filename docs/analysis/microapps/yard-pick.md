> Origin: `analysis/microapps/yard-pick.md` in `futurebuildai/hh_pro_dibbits` at commit 829d387baec93556ffa0e9242f4091e9be9544ec (read only source).
> Copied into `hh/docs/analysis/` by Session HH-2 lane HH-2.5; content below this header is unchanged.
> In-body `analysis/...` paths resolve to `docs/analysis/...` in this copy.

# yard-pick: Yard Pick and Receive

Audience: internal   Roles: yard_team, yard_manager   Devices: tablet, phone, kiosk   Environment: yard
Kit: scan-and-confirm   Brand: fb-tokens   Shell: hh-field
Offline: full. Scopes: pick_tasks, shortages, receiving, inventory_lookup, weights, attachments. Conflict policy: pick_tasks server_wins, shortages manual, receiving manual, weights server_wins, attachments last_write_wins_by_field. Why full: this is the declared yard-style scope in context 4.5, and the reason is physical. The rack is where signal dies, the work is a sequence of small confirmations against a ticket already assigned, and the outcome is a stock move that must not be lost. A shortage and a receipt are `manual` because each is a human claim about what was physically there; auto-merging two of them invents a quantity.

## Three jobs
1. Take the next pick ticket off the queue, pick it line by line, weigh it and stage it. The shipped surface is `hos-yard-console`, whose API surface names exactly this (`listPickQueue`, `getPick`, `recordPick`, `stagePick`, `submitWeight` in `hardscapeos_dibbits/app/src/yard-console.ts`).
2. Report a shortage against a line rather than picking short in silence (`reportShortage`, `resolveShortage`, same file).
3. Receive a truck against the outstanding purchase order lines. The purchasing console already describes this as the yard's job and pre-fills every line with everything outstanding because the common case is that the whole load turned up (`hardscapeos_dibbits/app/src/purchasing-console.ts`).

## Shared entities and handoffs
- pick_task: write; taken from dispatch-board and counter-till via `fb://hh/pick_task/<id>`
- stock_move: write; read by buying-and-stock and by money-desk for landed cost via `fb://hh/stock_move/<id>`
- purchase_order: read; written by buying-and-stock, the receipt written back to it via `fb://hh/purchase_order/<id>`
- will_call: write; minted here or by dispatch-board and handed to public-desk as a printed code via `fb://hh/will_call/<id>`
- order: read; a gap between what was sold and what is on hand is handed to buying-and-stock (`fetchOrderGaps`, `yard-console.ts`)

## What it replaces now
- HardscapeOS: `yard/yard` (`hos-yard-console`, `app/src/yard-console.ts`) and the receiving tab of `purchasing/purchasing` (`app/src/purchasing-console.ts`)
- HH Pro: nothing. The contractor sees the result of a pick as a tracking step in pro-field.

## States to design once
- empty: no ticket waiting is good news and says so, with the next expected arrival, not a blank queue.
- loading: the queue rows hold their height so a thumb does not land on a row that moved.
- offline: the sync chip is always visible with the queued count, because this app is expected to be offline and a silent queue is a lost pick.
- conflict: a shortage or a receipt that disagrees with the server opens a review card naming both numbers and who entered the other.
- error: a weight or a pick that will not post says what to do next at the rack, not what failed in a service.

## Device and environment notes
Gloves, sunlight, one hand, noise (context 4.5). The landing is already data rather than code: the `yard` shape lands on the pick queue on a phone or tablet, and the shipped migration says why, that a yard hand should not have to find the cockpit and read tiles they cannot act on (`hardscapeos_dibbits/backend/migrations/0141_dashboard_profile_home.sql`). The later ruling then split the shape so a dispatcher on a phone is not dragged to the yard console (`backend/migrations/0204_yard_landing_retires_its_takeover.sql`), which is exactly the "micro-app per role, not per unit" rule arriving early. The tablet is shared, so device unlock applies and the audit row records the unlocked person.

## Sources
- hardscapeos_dibbits/app/src/yard-console.ts
- hardscapeos_dibbits/app/src/purchasing-console.ts
- hardscapeos_dibbits/app/src/dashboard-nav.ts
- hardscapeos_dibbits/backend/migrations/0141_dashboard_profile_home.sql
- hardscapeos_dibbits/backend/migrations/0204_yard_landing_retires_its_takeover.sql
- hardscapeos_dibbits/backend/migrations/0140_dashboard_layouts.sql
