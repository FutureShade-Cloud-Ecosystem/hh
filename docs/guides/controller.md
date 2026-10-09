# Financial Controller: the testers' brief

The Money Desk's controller side. The full brief lives in the part 1 page, the testers' walk, on the demo box at the Money Desk app's "Testers' brief" link. The page's title is "Money Desk (controller): the testers' brief" and it opens from the Money Desk app's own "Testers' brief" link (the same convention the Counter Till carries: a `public/testers.html` served at the app's prefix, in the till's case `/till/testers.html`).

## One line, in the desk's own words

The role reads the approvals queue (margin, journal, freight, credit and COD exceptions), decides a parked request, approves a parked journal, reads the journal register, and reads the trial balance. The controller cannot decide its own request: a row that the controller filed is held in words, the four eyes rule, and the screen names who may. The other decider is the General Manager; the GM's sign-in is the second decider when the GM lands.

## Where the part 1 page holds

- The sign-in: the card on the front door (per the lane's core branch, where the role is live; the demo branch's `roles.ts` is still behind the lane's front door push), the tile the card opens, the desk's tabs.
- The demo world: FR1 (a floor request on Q1 line 1, twelve percent discount, twenty hours old, made by the Sales rep), the two parked journals J1 ($7,500, by the Sales manager, branch 1) and J2 ($6,200, by the controller itself, branch 1, separation of duties), the two freight raises from both branches, the trial balance in balance at $37,025.00 on both sides.
- The approvals queue: five waiting, oldest first, one decision per row.
- The decide floor: the panel names the detail, the line "One decision, one quote," the approve-as-asked button.
- The journal approve: the panel names the journal's two lines, the Approve and post button, the post and the decision commit together.
- The freight raise: the panel names the SO, the quoted freight, the asked freight, the difference.
- The own request: the row carries "Filed by you," the panel's alert names the four eyes rule, the choice cards read "Held: not yours to decide," the Decide button is disabled.
- The sales manager's own: the same wall, the same refusal body, the same wall in the matrix's own words.
- The decide panel hold: the wall replaces the gate's bare word; the matrix's own sentence names who may.
- The journal register: the filter, the row's word and tone, the entry number, the maker and the approver.
- The trial balance: in balance, read-only, the rows the core mints.
- The credit holds: the controller's lift, the override's "until the override expires" answer, no Place a hold button on this screen.
- The walls: the role's reach, the GM is the second decider, the branch fence is held by the read.
- Known issues: the world is dated, the restart signs out, the refused decision names who may, the bare array refuses, beyond reads are walled.

Read the part 1 page in full on the demo box; every claim on it is backed by the demo seed, the app's own logic, or the demoworld controller test.

## What is "later"

The part 1 page names the "later" tabs in the desk's own words: Period (later), Bank reconciliation (later), the role's own card on the demo branch's `roles.ts` (the lane B core's front door push is pending).

## Where feedback goes

One channel: send everything to Colton, and he files every note with the principal in one place. What helps most: what you tapped, what you expected, and what you saw instead. The older demo's feedback path stays open too, but what you say about this box is what decides what gets built next.
