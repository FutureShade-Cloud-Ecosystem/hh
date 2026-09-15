> Origin: `analysis/microapps/dispatch-board.md` in `futurebuildai/hh_pro_dibbits` at commit 829d387baec93556ffa0e9242f4091e9be9544ec (read only source).
> Copied into `hh/docs/analysis/` by Session HH-2 lane HH-2.5; content below this header is unchanged.
> In-body `analysis/...` paths resolve to `docs/analysis/...` in this copy.

# dispatch-board: Dispatch and Load Building

Audience: internal   Roles: logistics_manager, yard_manager, branch_manager   Devices: desktop, tablet, phone   Environment: office, yard
Kit: list-detail   Brand: fb-tokens   Shell: hh-work
Offline: read_cache_plus_outbox. Scopes: loads, delivery_stops, trucks, drivers, will_call. Conflict policy: loads server_wins, delivery_stops server_wins, will_call server_wins. Why this mode and not full: committing a delivery date is a promise to a customer, and the shipped authority set makes that an explicit gate (`CanCommitDelivery` in `hardscapeos_dibbits/backend/internal/identity/rbac.go`). Requesting a date is open; committing is not. So the board caches everything for reading and queues only the non-committing edits; a commit and a freight raise resolve online, where the approval rail can see them.

## Three jobs
1. Build the loads on the load board. It opens first for this role and the shipped comment says why: three audiences ask three different questions, and assembling the schedule is what the dispatcher does at the moment the app is opened (`hardscapeos_dibbits/app/src/dispatch-surface.ts`).
2. Commit or re-time a delivery date, raising an approval when the freight would go above what the customer was quoted. That policy exists and carries no money on the card, deliberately (`FREIGHT_ABOVE_QUOTE` in `backend/internal/identity/rbac.go`).
3. Keep the truck and driver registry, and mint a will-call code for a counter collection (`dispatch-fleet`, `dispatch-willcall`, imported by `app/src/dispatch-surface.ts`).

## Shared entities and handoffs
- order: read; written by counter-till and quote-desk via `fb://hh/order/<id>`
- delivery_stop: write; handed to drive-run via `fb://hh/delivery_stop/<id>`
- pick_task: write; handed to yard-pick via `fb://hh/pick_task/<id>`
- approval_request: write; handed to manager-home via `fb://hh/approval_request/<id>`
- will_call: write; handed to public-desk as a printed code via `fb://hh/will_call/<id>`
- tracking event: write; read by pro-field via `fb://hh/order/<id>`

## What it replaces now
- HardscapeOS: `dispatch/dispatch` (`hos-dispatch-surface`, `app/src/dispatch-surface.ts`) in five of its six sub-tabs: load board, board, schedule, fleet registry, will-call codes. The sixth, the driver view, becomes drive-run.
- HH Pro: nothing on the dealer side. The contractor's reschedule request arrives here from pro-field, and the shipped contractor rule is that rescheduling must re-time the already-scheduled dispatch task or the date is cosmetic (`hh_pro_dibbits/CLAUDE.md`).

## States to design once
- empty: a board with nothing on it names what would put something on it (an order with a requested date) rather than showing four empty columns.
- loading: column heights are reserved so a card does not jump under a drag.
- offline: reading is fine and says so; the commit control is visibly unavailable, since the gate it must pass is server side.
- conflict: the server's sequence wins for a stop and the change is narrated ("the office moved this stop"), not applied in silence.
- error: a refused commit names the role that may commit, because the shipped gate already excludes sales by name and a bare refusal sends someone hunting for a manager login.

## Device and environment notes
This role reads a phone at a desk as often as a desktop, which the yard-landing ruling calls out explicitly for the dispatcher (`hardscapeos_dibbits/backend/migrations/0204_yard_landing_retires_its_takeover.sql`). The board is a work page, not a document: the window is the workspace, and the layout contract already classifies the load board as `work` for exactly that reason (`app/src/app-shell.ts`, the `PAGE_TYPE` record and its header). On a yard tablet it is read standing up, so the row targets follow the yard rules even though the role is a manager.

## Sources
- hardscapeos_dibbits/app/src/dispatch-surface.ts
- hardscapeos_dibbits/app/src/app-shell.ts
- hardscapeos_dibbits/backend/internal/identity/rbac.go
- hardscapeos_dibbits/backend/migrations/0204_yard_landing_retires_its_takeover.sql
- hardscapeos_dibbits/app/src/dashboard-nav.ts
- hh_pro_dibbits/CLAUDE.md
