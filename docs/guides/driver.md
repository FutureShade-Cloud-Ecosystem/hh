# Driver: the testers' brief

A walk of HH Drive for the JV team. Plain speech; the shape is the shape the seed and the acceptance legs hold, the wording is the app's own. What is "later" is named in the app's own words. Sources: the demo seed on main and the app's own logic in `web/apps/drive-run/src/drive-logic.ts`. The acceptance legs in `web/apps/drive-run/test/acceptance/` are the source of the named numbers.

## The role

The Driver works the run sheet. A run is the day's deliveries the back office dispatched, with the stops in the yard's own order; the driver marks a stop loaded, en route, delivered (with proof) or failed (with a reason), and closes the run. The driver's reach is the one branch the front door signs in at; cost and margin are not on the driver's desk, the counter (counter-till none), the dispatch's board (dispatch-board none) and the price rules are all refused in the role gate's shared body.

## How to sign in

On the front door, tap the Driver card. The tile the card opens is HH Drive. The desk inside the tile is the run sheet, on the dealer's day. The sign-in rides the front door's handoff so no second sign-in is asked for.

A card marked Coming cannot be tapped yet. The other roles sit on the same door.

## The demo world

The driver world is the day's dispatched runs, with the stops in the yard's own order. The seed plants at least one dispatched run to the seeded driver on the dealer's local day, with at least two stops: one to deliver, one to hand back. The acceptance leg asserts the run carries the same stops the day seeded.

## The run sheet and the four moves

The run sheet lists every dispatched run, ordered by time. The first card is the next run to mark loaded. Each run card carries the run number, the day, the truck, the stop count, the state chip and the run summary in words.

The four moves the driver makes are:

1. Mark loaded. The state moves from Dispatched to Loaded. The first stop on the run unlocks.
2. Mark en route. The state moves to En route. The stop carries the next move.
3. Delivered. Proof of delivery. The state moves to Delivered. A photo, a signature, the customer's name and a note are the fields the proof holds.
4. Could not deliver. A reason. The state moves to Failed. The reason is the contract's own (the receiver absent, the address wrong, the goods refused, the access blocked, the truck broke, the weather, or Other with a note).

A failed stop is not a closed run. A run with any open stop is not a closed run. The driver closes a run by completing the last stop.

## A short walk to try

The acceptance leg `web/apps/drive-run/test/acceptance/20-stop-to-pod.pw.ts` and `40-close-the-run.pw.ts` are the whole walk in two:

- Tap the Driver card. The run sheet lands with the dispatched runs, on the dealer's day. The first run carries the seed's first stop.
- Press Mark loaded. The state moves to Loaded.
- Press Mark en route. The state moves to En route.
- Press Delivered. The proof form opens. Type the customer's name, type a note, snap the photo (the camera in the stub simulates the capture), press Confirm proof. The state moves to Delivered.
- Move to the second stop. Press Mark loaded. Press Mark en route. Press Delivered. The proof form opens again. The state moves to Delivered.
- The run summary at the top of the card changes: "2 stops left · 0 delivered" becomes "1 stop left · 1 delivered" becomes "Run finished · all 2 delivered." The close affordance appears on the closed run.

The leg's other moves:

- A failed stop: press Could not deliver. Pick a reason. Press Confirm. The state moves to Failed. The run summary carries the failure's count.
- A closed run, after the last stop: the run summary says "Run finished." No close button; the run is closed.

## What the moves do in the app's own words

- Mark loaded: "Loaded."
- Mark en route: "En route."
- Delivered: "Delivered."
- Could not deliver: "Could not deliver."

Each move's chip carries the state word and a tone. The colour never carries the meaning alone; the word is the carrier.

The run summary in words, for the open run: "1 stop left · 1 delivered · 1 could not be delivered." For the finished run: "Run finished · 1 delivered · 1 could not be delivered" or "Run finished · all 2 delivered." A run with no stops is "No stops on this run." A run with stops and at least one open is "open." A run with stops and none open is "done."

## What the role can not do

The driver's reach is the run sheet, the branch the front door signed in at, the moves the matrix names. The walls are:

- A surface at None for the role is refused by the server. The counter (counter-till none), the dispatch's board (dispatch-board none) and the price rules all return 403 in the role gate's shared body.
- A branch the driver is not signed in at is out of scope. The driver's own read answers the branches list with one row, the branch the front door signed in at.
- Cost and margin are hidden for the role. The glance the role holds at read level answers 403 in the role gate's shared body.
- A driver may not record a Delivered as a plain status move. The state route refuses it; the only path to Delivered is the proof of delivery. The app does not offer a Delivered button; the proof form is the only door.

A wall sentence the app does not carry keeps the server's own word: an honest refusal beats an invented permission.

## What is "later"

The driver's other modules (the dispatch's board, the buyer's view of stock) are not on the driver's desk on this round. The run sheet's own moves (cancel a stop, dispatch a new run, route reassign) are out of scope for the driver. The card's other tiles are not in scope for the driver on this round.

## Where feedback goes

One channel: send everything to Colton, and he files every note with the principal in one place. What helps most: what you tapped, what you expected, and what you saw instead. The older demo's feedback path stays open too, but what you say about this box is what decides what gets built next.
