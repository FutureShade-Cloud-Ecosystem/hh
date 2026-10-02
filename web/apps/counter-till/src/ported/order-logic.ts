// Ported from hardscapeos_dibbits master 87d7941b app/src/order-logic.ts; adapted for HH.

// order-logic.ts: the orders module's lifecycle VOCABULARY: what an order's, a change order's and a
// shipment group's stored states are CALLED on a screen, as against what the three columns store.
//
// WHY THIS FILE EXISTS. Lifecycle knowledge lives in the matrix and is never branched in a component
//: dispatch-logic.ts holds STOP_LABEL / RUN_LABEL / WILL_CALL_LABEL for exactly that reason. Orders
// had nowhere to put it: the module has no `-logic`, `-shared` or `-api` file, and the maps these
// words belong beside are CSS CLASS maps (order-lifecycle.ts's STATUS_CHIP, _order-depth.ts's
// CHANGE_STATUS_CHIP and GROUP_STATUS_CHIP): those pick a colour, they do not write a word. Five
// surfaces render these three columns: order-lifecycle, order-edit, order-change, order-shipments
// and the related-documents panel. A map in any one of them would be the second place the same fact
// lived, and the second place is where the two copies start to disagree.
//
// DOM-FREE on purpose (the `_order-*.ts` house convention, applied to a `-logic` name), so the words
// are unit-testable without a browser: order-logic.test.mjs.
//
// WHAT THIS IS NOT. It is not a second copy of the transition matrix. The server still decides what
// is legal and hands each surface its `legal_actions`; nothing here says what MAY happen, only what
// the thing that already happened is called. And nothing here changes what a state MEANS: where the
// product already has a word for a state, this file uses that word rather than minting a rival.

/**
 * ORDER_STATUS_LABEL is what an order's nine states are CALLED, as against what the column stores.
 *
 * `orders.status` is DRAFT | CONFIRMED | PICKING | READY | PARTIALLY_FULFILLED | FULFILLED |
 * INVOICED | CLOSED | CANCELLED (the CHECK is migration 0008:51-52). Those are the right words for a
 * row and the wrong shape for a screen: nobody at a counter SHOUTS, and PARTIALLY_FULFILLED:
 * underscore and all: is a database column printed where a word belongs.
 *
 * The words themselves are ALREADY the product's, so this map reuses them rather than inventing a
 * second name for the same state. The order-history screen has rendered "Partially fulfilled" since
 * it shipped (order-history.ts fmtStatus), the server's activity feed writes the same
 * (order/activity.go humanStatus) and the assistant reads it aloud that way
 * (assist/commands_activity.go statusWord). "In progress" would be a different fact, and a wrong one.
 *
 * The old ratification of the RAW vocabulary: that a prettified label could drift from what the API
 * and the timeline report: is kept, and satisfied differently: the enum is still what this client
 * COMPARES, posts, keys its chip colour on and hangs its `data-` handles from. Only the value that
 * lands between the tags becomes a word.
 */
export const ORDER_STATUS_LABEL: Record<string, string> = {
  DRAFT: 'Draft',
  CONFIRMED: 'Confirmed',
  PICKING: 'Picking',
  READY: 'Ready',
  PARTIALLY_FULFILLED: 'Partially fulfilled',
  FULFILLED: 'Fulfilled',
  INVOICED: 'Invoiced',
  CLOSED: 'Closed',
  CANCELLED: 'Cancelled',
};

/**
 * orderStatusWord renders the chip form, degrading HONESTLY: an unmapped value comes back as itself,
 * lowercased with its underscores opened out, rather than as an invented phrase. A tenth status added
 * to the 0008 CHECK backend-first therefore shows up as STRANGE instead of as a confident lie, which
 * is the whole point of the default arm. Never 'Unknown', never ''. Same rule and same arm as
 * willCallWord (dispatch-logic.ts) and the server's pickStateWord (dispatch/willcall.go).
 */
export function orderStatusWord(status: string): string {
  return ORDER_STATUS_LABEL[status] ?? status.toLowerCase().replace(/_/g, ' ');
}

/**
 * ORDER_STATUS_PHRASE is the same nine states said MID-SENTENCE.
 *
 * It exists because one surface puts the status inside a sentence a person reads rather than into a
 * chip: the lifecycle panel's stale-view 409, "order is …: confirming is no longer available". A
 * chip word dropped into that slot either shouts or reads as a proper noun mid-clause, and DRAFT
 * needs an article to be a sentence at all.
 *
 * Same meanings, different grammar: and a second MAP rather than a `.toLowerCase()` over the first,
 * because lowercasing is a cosmetic mask that writes no words: it would leave the sentence still
 * saying the column's name. The server phrases the same sentence the same way (willcall.go
 * orderStateWord, "this order is already closed"), and this is the client staying in step with it.
 */
export const ORDER_STATUS_PHRASE: Record<string, string> = {
  DRAFT: 'a draft',
  CONFIRMED: 'confirmed',
  PICKING: 'being picked',
  READY: 'ready',
  PARTIALLY_FULFILLED: 'partially fulfilled',
  FULFILLED: 'fulfilled',
  INVOICED: 'invoiced',
  CLOSED: 'closed',
  CANCELLED: 'cancelled',
};

/** orderStatusPhrase renders the mid-sentence form, degrading honestly on orderStatusWord's terms. */
export function orderStatusPhrase(status: string): string {
  return ORDER_STATUS_PHRASE[status] ?? status.toLowerCase().replace(/_/g, ' ');
}

/**
 * CHANGE_STATUS_LABEL is what a change order's seven states are CALLED.
 *
 * `change_orders.status` is DRAFT | PENDING_YARD | PENDING_FINANCE | APPROVED | APPLIED | REJECTED |
 * CANCELLED (the CHECK is migration 0125:95-96; the ladder is documented at 0125:36-45). The two
 * PENDING_ rows are the worst of them on a screen: they do not name a state of the change, they name
 * the QUEUE the change is sitting in, and an underscore is not a word. The chip says who the change
 * is WITH, which is the fact the person looking at it needs: and it is the fact changeStageSummary
 * already spells out underneath in a sentence ("Waiting on the yard.").
 *
 * CANCELLED reads 'Withdrawn' because that is what this product already calls it: 0125 defines the
 * state as "the requester withdrew it", the button is "Withdraw…" and the stage summary says
 * "Withdrawn." (_order-depth.ts). A second word for it here would be a second meaning.
 *
 * DRAFT is in the CHECK but is never persisted today: 0125 reserves it for a future "compose the
 * delta over several screens" flow. It is mapped anyway: a state the schema allows can arrive, and a
 * column value the map has never heard of is exactly what the default arm is for.
 */
export const CHANGE_STATUS_LABEL: Record<string, string> = {
  DRAFT: 'Draft',
  PENDING_YARD: 'With yard',
  PENDING_FINANCE: 'With finance',
  APPROVED: 'Approved',
  APPLIED: 'Applied',
  REJECTED: 'Rejected',
  CANCELLED: 'Withdrawn',
};

/** changeStatusWord renders a change order's state, degrading honestly on orderStatusWord's terms. */
export function changeStatusWord(status: string): string {
  return CHANGE_STATUS_LABEL[status] ?? status.toLowerCase().replace(/_/g, ' ');
}

/**
 * GROUP_STATUS_LABEL is what a shipment group's six states are CALLED.
 *
 * `shipment_groups.status` is PLANNED | PICKING | READY | DISPATCHED | COMPLETE | CANCELLED (the
 * CHECK is migration 0126:91-92). 0126 says that ladder deliberately mirrors the ORDER one, so "a
 * yard lead who knows what READY means on an order knows what it means on a group": which is why
 * PICKING, READY and CANCELLED read here exactly as they read in ORDER_STATUS_LABEL above. One
 * vocabulary for one word is the entire point of the mirroring.
 *
 * COMPLETE reads 'Delivered' because that is already this surface's own word for it: the invoice
 * policy control offers "One invoice when every group is delivered" and bills "the delivered subset"
 * (order-shipments.ts), and the button that puts a group here is "Mark delivered" (_order-depth.ts).
 * The state is reachable only from DISPATCHED (order/shipgroup.go groupTransitions), so a group that
 * holds it has been on a truck and arrived: 'Delivered' says that, and says nothing new.
 */
export const GROUP_STATUS_LABEL: Record<string, string> = {
  PLANNED: 'Planned',
  PICKING: 'Picking',
  READY: 'Ready',
  DISPATCHED: 'Dispatched',
  COMPLETE: 'Delivered',
  CANCELLED: 'Cancelled',
};

/** groupStatusWord renders a shipment group's state, degrading honestly on orderStatusWord's terms. */
export function groupStatusWord(status: string): string {
  return GROUP_STATUS_LABEL[status] ?? status.toLowerCase().replace(/_/g, ' ');
}
