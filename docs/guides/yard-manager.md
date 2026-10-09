# Yard Manager: the testers' brief

A walk of the Manager Home app for the JV team. Plain speech; the shape is the shape the seed and the acceptance legs hold, the wording is the app's own. What is "later" is named in the app's own words. Sources: the demo seed on `demo/manager-home-app` and the app's own logic in `web/apps/manager-home/src/manager-logic.ts`. The acceptance legs in `web/apps/manager-home/test/acceptance/` are the source of the named numbers.

## The role

The Yard Manager reads the home bento for the yard at Trenton. The home is the seven widgets the manager's profile carries, in the matrix's own order: deliveries, needs, picks board, deliveries by branch, picks gauge, open orders, open warranty. The reach is the branch the front door signs in at. The home lands read-only on this slice; the bento shows what the core answers, the words the app shows, and the rows the resolver list names.

## How to sign in

On the front door, tap the Yard manager card. The card's line is "The yard: picks, shortages and deliveries." The tile the card opens is Manager Home. The desk inside the tile is the home bento, on the dealer's day. The sign-in rides the front door's handoff so no second sign-in is asked for.

A card marked Coming cannot be tapped yet. The manager's other cards sit on the same door.

## The demo world

The yard world is the inventory's opening stock at Trenton, with the order book's open yard pulls and the procurement world's seeded shortages. The seed plants exactly two unresolved shortages at Trenton (the K08 gap on Granular A and the HPB dust shortage on the same pick) and one PARTIAL purchase order with a short line; the stale picks sit at the other branch, out of this grant's reach, so their row is absent (a count of zero is absent by design, never shown). The hero's measured figure for the dealer's day is the core's answer, never a dash.

## The home bento, in the matrix's order

The seven widgets, in the matrix's own order:

1. Hero · deliveries. The day's measured number, the yard's own.
2. Needs you. The resolver queue. Two open shortages and one receiving difference on the seeded Trenton work.
3. Picks board. The yard's open picks, the matrix's own shape.
4. Deliveries · branch. The day's deliveries, the branch's own.
5. Picks · gauge. The yard's progress, the gauge's own.
6. Orders · open. The open orders, the matrix's own.
7. Warranty · open. The open warranty tickets, the matrix's own.

The home's state carries a severity and a window word. The colour never travels alone: the word is the carrier (Routine, Watch, Action). The unsourced row in the matrix's own list is said plainly and carries no number.

## A short walk to try

The acceptance leg `web/apps/manager-home/test/acceptance/20-resolve.pw.ts` is the whole walk in one:

- Tap the Yard manager card. The home lands with the seven widgets, the needs row at two open shortages and one receiving difference (the count "3 waiting · Watch" in the matrix's own words).
- Press Resolve beside the open shortages row. The shortages view opens, listing the two seeded shortages, in the queue's own order: "Only 8 yards left in the bin" and "Pile is down to fines." The resolver sentence is the matrix's own: "Shortages are resolved by the yard team and the yard manager."
- Press Resolve on the first open shortage. The resolver's three choices appear, in the matrix's own order (Backorder the shortfall, Mark as resolved, Cancel the short). Pick one, type a note, press Confirm.
- The notice appears, in the server's own words: "1 still open." The resolved row carries the resolver's words beside the action: "Backordered the shortfall; the order keeps the short line open. Customer ok with Thursday" with the note the manager typed.
- Press Back to Home. The needs row's count dropped from 2 to 1.

## What the home does in the app's own words

- A needs row: the matrix's own label, the count in the matrix's own number, the action button the matrix names (Resolve, Open).
- A resolver sentence: "Shortages are resolved by the yard team and the yard manager."
- An unsourced row: the matrix's own label, no number. A count of zero is absent by design, never shown.
- A hero figure: the day's measured number, the yard's own, never a dash.

The home's buttons and arrows open the related view; the home does not compute, the core answers, the app draws the core's answer.

## Approvals, the own-branch and the wall

The manager's home carries the approvals widget, which lists every kind the matrix names, in the matrix's own order: Margin, Credit, Cash on delivery, Journal entry, Freight above quote. Each kind's decider sentence is the matrix's own:

- Margin approvals: "decided by the financial controller, the general manager, and a sales manager or branch manager in their own branch."
- Credit approvals: "decided by the financial controller and the general manager."
- Cash on delivery approvals: "decided by the financial controller and the general manager."
- Journal entry approvals: "decided by the financial controller and the general manager."
- Freight above quote approvals: "decided by the financial controller and the general manager."

A wall sentence the map does not carry keeps the server's own word: an honest refusal beats an invented permission. The wall replaces the gate's bare word; the gate's word never reaches the screen.

## What the role can not do

The role's reach is the home, the branch the front door signed in at, the read of the matrix's own. The walls are:

- A surface at None for the role is refused by the server. The counter, the dispatch and the price rules all return 403 in the role gate's shared body.
- A pick on the other branch is out of scope. The server answers in words: "branch out of scope."
- A resolve of a shortage that names the yard only returns 403. The app's wall replaces the bare word with "You may not resolve this shortage. Shortages are resolved by the yard team and the yard manager."
- An approval kind the manager does not decide returns 403. The app's wall names the matrix's deciders for that kind.

## What is "later"

The home's resolve is the only move the manager can make on this slice. The yards dispatch and load building, the buyer's view of stock, and the manager's other home moves (the resolve sheet's other actions, the day view) are not on this round. The home's "later" tiles are dashed between the others and named in plain text.

## Where feedback goes

One channel: send everything to Colton, and he files every note with the principal in one place. What helps most: what you tapped, what you expected, and what you saw instead. The older demo's feedback path stays open too, but what you say about this box is what decides what gets built next.
