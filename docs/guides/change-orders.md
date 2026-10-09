# Change orders, the till's action: the testers' brief

A walk of the Counter Till's Change order action for the JV team. Plain speech; the shape is the shape the seed and the acceptance legs hold, the wording is the app's own. What is "later" is named in the app's own words. Sources: the demo seed on main from PR 286 and the app's own logic in `web/apps/counter-till/src/ported/_order-change.ts`. The acceptance legs in `web/apps/counter-till/test/change-orders.pw.ts` are the source of the named numbers.

## The role

The Counter Till's Change order action is the one button the clerk presses on a sold card to change the order. The action is the same on the post-pick surface (a change after the yard has pulled the goods) and on the line edit surface (a change before the pick, a manager only). The clerk's reach is the branch the front door signs in at. The server is the authority for which actions the desk offers: a control the server would refuse is never on the screen, because a control that refuses teaches people to distrust the buttons.

## How to sign in

On the front door, tap the Sales card. The desk inside is the till, the new counter sale on the entry. Ring a sale and the desk's sold card carries the Change order action. The action is the same button on the post-pick surface and on the line edit surface; the surface the button opens depends on the order's status.

A card marked Coming cannot be tapped yet. The clerk's other cards sit on the same door.

## The demo world

The change order world is the day's ringed sales at the dealer's branch. The acceptance leg seeds a sale the clerk can change (a Holland Stone sale at three pieces, the $12.00 line). The change order's own legs name the figures the server prices (a $40 change does not need a manager; a $600 change does).

## The action and the two surfaces

The Change order action opens one of two surfaces, depending on the order's status:

- Before the pick: the line edit. The clerk (Sales) sees the line edit's edit, no controls. The manager (Sales manager) sees the line edit's edit and the audit trail. The clerk's screen offers no line controls because the clerk is the actor who can record the change before the pick, not the one who decides it; the manager decides.
- After the pick: the change order surface. The clerk raises a change; the server prices the delta; the state chip names the stage; the buttons the server offers ride in the surface (Withdraw, Apply change when approved, Yard: acknowledge, Approve (finance), Reject). The clerk's reach is Withdraw, Reject, and Apply when the manager approved; the yard's reach is Yard: acknowledge; the finance reach is Approve (finance) when the change is over the threshold.

## A short walk to try

The acceptance leg `web/apps/counter-till/test/change-orders.pw.ts` is the whole walk in one:

- Sign in as Sales. Ring a sale: search "holland," pick Holland Stone 60 mm, type 3, press the complete sale. The sold card appears. The card carries the Change order action.
- Press Change order. The line edit opens. The clerk sees the line's quantity field; the field is editable; the audit trail is empty. No other controls. This is the clerk's surface before the pick.
- Sign out as Sales. Sign in as Sales manager. Open the same sale. Press Change order. The manager sees the line's quantity field and the audit trail grouped by the edit. The manager can save the edit; the audit row carries the edit. This is the manager's surface before the pick.
- Pick a different sale, one whose pick the yard has already started (the post-pick fixture). Press Change order. The post-pick change surface opens. The line's delta is the server's priced delta; the chip names the state; the buttons ride in the surface. The clerk's reach is Withdraw, Reject, and Apply when the manager approved.
- The yard's acknowledgement is walled on the till. Press the yard button and the app's wall replaces the gate's bare word: "Only the yard team or the yard manager may acknowledge a change for the yard."
- The finance approve is walled on the till. Press the finance button and the app's wall replaces the gate's bare word: "Only the financial controller or the general manager may sign a change over the threshold."
- The apply is walled on the till for the clerk when the approval is required. The app's wall reads: "Only the sales manager may apply a change to the order."
- Withdraw: the clerk withdraws the change. The state says withdrawn.
- Apply: the manager's chrome applies the change once. The second apply is refused; the server offers no button on an applied change (next_actions is empty), so the re-entrancy ruling is made visible.
- The acceptance walk: a $600 change waits for the manager, a $40 change does not.

## What the action does in the app's own words

- The action's label: "Change order."
- The line edit's audit trail: a row per edit, the actor named, the field named, the new value named.
- The change order's chip: the server's own state word. The colour never carries the meaning alone.
- The change order's buttons: the server's own labels, in the matrix's own order. A button the server does not offer is not on the screen.
- The wall: the matrix's own sentence, in plain words, never the bare gate word.

## What the role can not do

The clerk's reach is the change order action on the chrome, the branch the front door signed in at, the matrix's own actions. The walls are:

- The yard's acknowledgement is walled on the till. The app's wall reads: "Only the yard team or the yard manager may acknowledge a change for the yard."
- The finance approve is walled on the till. The app's wall reads: "Only the financial controller or the general manager may sign a change over the threshold."
- The apply is walled on the till when the approval is required. The app's wall reads: "Only the sales manager may apply a change to the order."
- The clerk's own reach on the post-pick surface is Withdraw, Reject, and Apply when the manager approved. The apply without the manager's approval is refused in the matrix's own words.
- A second apply on the same change is refused; the server offers no button on an applied change (next_actions is empty).

A wall sentence the app does not carry keeps the server's own word: an honest refusal beats an invented permission.

## What is "later"

The change order's other moves (a partial accept, a yard's acknowledge on the till, a finance approve on the till) are out of scope for the clerk on this round. The line edit's own moves (a multi-line change, a price change) are out of scope for the clerk; the manager's reach is the full edit. The desk's other modules (the discount, the hot list, the returns desk) are not on the change order surface.

## Where feedback goes

One channel: send everything to Colton, and he files every note with the principal in one place. What helps most: what you tapped, what you expected, and what you saw instead. The older demo's feedback path stays open too, but what you say about this box is what decides what gets built next.
