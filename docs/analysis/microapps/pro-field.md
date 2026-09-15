> Origin: `analysis/microapps/pro-field.md` in `futurebuildai/hh_pro_dibbits` at commit 829d387baec93556ffa0e9242f4091e9be9544ec (read only source).
> Copied into `hh/docs/analysis/` by Session HH-2 lane HH-2.5; content below this header is unchanged.
> In-body `analysis/...` paths resolve to `docs/analysis/...` in this copy.

# pro-field: Pro Field

Audience: external   Roles: contractor_field, contractor_buyer, contractor_owner   Devices: phone   Environment: site
Kit: list-detail   Brand: hh-pro   Shell: hh-pro
Offline: read_cache_plus_outbox. Scopes: orders, tracking_events, delivery_instructions, pickups. Conflict policy: tracking_events server_wins, delivery_instructions last_write_wins_by_field, pickups manual. Why this mode: the field crew is on a site with poor signal and needs to know where the material is without a connection. Tracking is the supplier's fact, so it is server authoritative and never queued. A pickup confirmation is `manual` on conflict because the supplier may have auto-collected it, and appending an additional delivered event plus a duplicate invoice is the exact failure the shipped action guards against (`hh_pro_dibbits/src/core/actions/fulfillment.ts`, `confirmWillCallPickup`).

## Three jobs
1. Read where the order is in the step flow, with the will-call step substituted for out-for-delivery rather than a second flow constant (`hh_pro_dibbits/src/core/selectors/tracking.ts`, `src/ui/pages/OrderTrackingPage.tsx`).
2. Confirm a will-call pickup, which is the one capability this role holds (`confirm-pickup` in `src/core/domain/team.ts`) and which cancels the pending auto-collect task.
3. Edit the delivery instructions the driver will read at the stop (`edit_delivery_instructions` in `hardscapeos_dibbits/app/src/portal/portal-api.ts`; the detail screen is `app/src/portal/portal-order-detail.ts`).

## Shared entities and handoffs
- delivery_stop: read; written by drive-run via `fb://hh/delivery_stop/<id>`
- order: read; written by pro-procurement via `fb://hh/order/<id>`
- delivery_instructions: write; read by dispatch-board and drive-run via `fb://hh/order/<id>`
- pickup: write; read by counter-till and yard-pick at the collection counter via `fb://hh/will_call/<id>`
- pod: read; written by drive-run

## What it replaces now
- HardscapeOS: the tracking and delivery-instruction half of `portal /orders/:id` (`app/src/portal/portal-order-detail.ts`, `app/src/portal/portal-timeline.ts`), gated on `view_orders_deliveries` and `edit_delivery_instructions` (`app/src/portal/portal-api.ts`)
- HH Pro: `/orders/:orderId/tracking` (`src/ui/pages/OrderTrackingPage.tsx`)

## States to design once
- empty: an order with no supplier artifact behind it must not exist; if it does, the screen says the supplier has not started rather than drawing an empty flow (a seeded card with no artifact caused two bugs, `hh_pro_dibbits/CLAUDE.md`).
- loading: the step flow keeps all its steps visible and greys the unreached ones, so the shape of the journey never changes under the thumb.
- offline: the last pull is stamped on the flow, and the confirm control queues visibly rather than appearing to succeed.
- conflict: a pickup the supplier already collected shows their record and who signed, and does not write a second one.
- error: a refusal names who can act. A field member who cannot reschedule is told the project manager can, in the same sentence the assistant would relay (`src/core/actions/team.ts`).

## Device and environment notes
One phone, one hand, outdoors, in sun. The tracking pill maps status to success, info or neutral rather than one colour for everything, and a thin margin gets an icon as well as a hue, because colour must encode state and never alone (`hh_pro_dibbits/CLAUDE.md`). Lead-time and date consequences are written as the consequence, not the arithmetic. Rescheduling is blocked once a truck is rolling, because moving a load already out is a phone call and not a form (same file). Who you are acting as is per window, so this phone stays the field member's even if the office switches people elsewhere.

## Sources
- hh_pro_dibbits/src/ui/pages/OrderTrackingPage.tsx
- hh_pro_dibbits/src/core/selectors/tracking.ts
- hh_pro_dibbits/src/core/actions/fulfillment.ts
- hh_pro_dibbits/src/core/domain/team.ts
- hh_pro_dibbits/src/core/actions/team.ts
- hardscapeos_dibbits/app/src/portal/portal-order-detail.ts
- hardscapeos_dibbits/app/src/portal/portal-api.ts
- hh_pro_dibbits/CLAUDE.md
