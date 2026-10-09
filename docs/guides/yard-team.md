# Yard Team: the testers' brief

A walk of the Yard Pick app for the JV team. Plain speech; the shape is the shape the seed and the acceptance legs hold, the wording is the app's own. What is "later" is named in the app's own words. Sources: the demo seed on `demo/yard-pick-app` and the app's own logic in `web/apps/yard-pick/src/yard-logic.ts`. The acceptance legs in `web/apps/yard-pick/test/acceptance/` are the source of the named numbers.

## The role

The Yard Team works the queue of open picks at the dealer's yard (Trenton in the seed). A pick is a unit of work the back office pushes to the yard when a sale or transfer needs material pulled; the team claims a pick, pulls the lines against the order, records what was found, stages the pick in a parking spot, and hands the stage ref to the counter. The team's reach is the branch the front door signs in at. Cost and margin are not on the team's desk: the decided matrix gives the role read access without cost.

## How to sign in

On the front door, tap the Yard Team card. The card's line is "The yard: pulls, shortages and stages." The tile the card opens is Yard Pick and Receive. The desk inside the tile is the queue, on the dealer's day. The sign-in rides the front door's handoff so no second sign-in is asked for.

A card marked Coming cannot be tapped yet. The team is on the round's sign-in grid; the yard's other cards sit on the same door.

## The demo world

The yard world is the inventory's opening stock at Trenton, with the order book's open yard pulls. The queue the team lands on is the open pick list at Trenton, the only branch the team's grant reaches. The Kingston branch sits on the same seed but is out of the team's reach; the read fails in words.

The seed plants the K08 procurement gap as a real shortage on the Granular A pick at Trenton, and the HPB dust shortage on the same pick. The acceptance leg names them in their own words: "Only 8 yards left in the bin" and "Pile is down to fines." The first is a unit shortage (the team found less than the line asked for); the second is a fines shortage (the pile is down to dust).

## The queue and the four moves

The queue lists every open pick, ordered by status and time. The first card is the next pick to claim. Each card carries the order number, the customer, the line count, the unit count and the chip. The four moves the team makes are:

1. Claim. Press Claim and the pick is yours. The state changes to Claimed. Your name lands on the card.
2. Start picking. The state moves to Picking. The lines are unlocked.
3. Record lines. Type the count you pulled, with a reason if it is short. A short pick backorders the line and the order's rep sees a notice in their channels.
4. Stage. Type the parking spot. The state moves to Staged, the stage ref is the yard's own handle (the form `STG-NNNN`), the counter is told.

## A short walk to try

The acceptance leg `web/apps/yard-pick/test/acceptance/20-pick.pw.ts` is the whole walk in one:

- Tap the Yard Team card. The queue lands with the open pick list, Trenton only.
- Press Claim on the first card. The state moves to Claimed. The card carries your name.
- Press Start picking. The state moves to Picking.
- Open the pick. The lines are there. Type the full count for the first line, then the second.
- If a line is short, type the count you pulled and pick a reason. The line backorders. The rep's channel carries the notice. The notice's own words: "on backorder" and the reason you picked, in the server's own sentence.
- Press Stage. Type a parking spot. The pick is Staged, the stage ref is the yard's handle. The counter reads it.
- Open the second pick, or the second line of the same one. The actions the queue offers follow the state: Claimed offers Start, Picking offers Stage, Staged offers nothing.

## What the moves do in the app's own words

- Claim: "Claimed. It is yours to pull."
- Start picking: "Picking started."
- Stage: "Staged at <spot>, ref <handle>. The counter has been told it is ready."

Each move's chip carries the state word and a tone: Queued and Pushed are neutral, Claimed and Picking are active, Staged is ready, Done and Cancelled are stopped. The colour never carries the meaning alone; the word is the carrier.

## Shortages and the resolver

A line recorded short carries a reason from the contract's list. The team's resolver view names the shortage's own words and the resolver sentence, which is the matrix's own: "Shortages are resolved by the yard team and the yard manager."

When a shortage is resolved, the team's view names the resolver's words beside the action: "Backordered the shortfall; the order keeps the short line open. Customer ok with Thursday" is one form the resolver sentence takes, with the note the team typed beside the action. The word on the resolved row is the server's own.

## What the role can not do

The role's reach is the yard, the branch the front door signed in at, the read level without cost. The walls are:

- A surface at None for the role is refused by the server. The counter (counter-till none), the dispatch's board (dispatch-board none) and the price rules all return 403 in the role gate's shared body.
- A pick on the other branch is out of scope. The server answers in words: "branch out of scope." A branch-scoped caller must name a reachable branch.
- Cost and margin are hidden. The stock read the role holds at read level answers with no cost key at all, byte absent rather than a zero that reads as free. The inventory browse and the on-hand both omit every cost column.
- The resolve of a shortage that names the yard only returns 403 in the role gate's shared body. The app's wall replaces the bare word with "Only the Yard Team and the Yard Manager may resolve a shortage."

A wall sentence the map does not carry keeps the server's own word: an honest refusal beats an invented permission.

## What is "later"

Receiving and the yard's other modules (the dispatch and load building, the buyer's view of stock) are not on the team's desk on this round. The card's other tiles are not in scope for the team. The shortage's rep notice goes through the rep's channel read until House Messages is live; the team's view does not need to wait.

## Where feedback goes

One channel: send everything to Colton, and he files every note with the principal in one place. What helps most: what you tapped, what you expected, and what you saw instead. The older demo's feedback path stays open too, but what you say about this box is what decides what gets built next.
