# ADR-0017: Shell split and first micro-app

Status: Proposed

Drafted in HH-2 lane HH-2.4 from `analysis/DECISIONS.md` D2, which Colton took at the close of HH-1. This draft records the decision; it does not reopen it.

## Decision

Two internal shells, split by work context rather than by app, which keeps the shell-per-audience rule while avoiding a desktop shell that asks every counter user for location permission:

- **HH Work**: office roles. Counter Till, Quote Desk, Dispatch Board, Buying and Stock, Money Desk, Manager Home, Dealer Admin, House Messages. Desktop and tablet.
- **HH Field**: Yard Pick and Drive Run, plus House Messages. Phone-first, camera and GPS permissions, offline full mode.

Counter Till and Quote Desk stay separate micro-apps with a shared quote-to-order entity view, not one tab. Homeowner acceptance renders under HH Pro's brand as a magic-link page with no install. Yard goes first, because it exercises the offline store, the hardest shared package, and does not wait on the pricing decision the way Counter does.

The catalog's former `hh-internal` shell has become two shells, `hh-work` and `hh-field` (applied in the PR review revision of `role-catalog.yaml`); `hh-public` stays a web shell with brand `hh-pro`.

## Amendment to context section 4.5

Context section 4.5 states: "Internal staff get one installable shell per product family that loads role-allowed micro-apps with an in-shell switcher." This ADR amends that rule for `hh`: one product family may ship two internal shells when the work contexts differ in device, permission surface and offline mode. Both shells serve the internal audience, so the shell-per-audience rule itself stands; both carry identical chrome (org, user, app switcher, sync state, notifications) and the in-shell switcher. The three shells on the skeleton are `tauri/hh-work`, `tauri/hh-field` and `tauri/hh-pro` (D2 as applied to HH-2.1).

## Open questions resolved

From `analysis/role-catalog.yaml`:

- Question 1: "Is HH Drive a micro-app inside the internal shell or its own standalone shell, given it pins its own theme, ships its own manifest and deliberately carries no navigation at all?" Resolved: Drive lives in HH Field, not its own shell.
- Question 2: "Do Counter Till and Quote Desk share one shell tab for inside sales, or stay two micro-apps because outside sales never touches the till?" Resolved: two micro-apps, shared entity view.
- Question 3: "Is the homeowner acceptance page a micro-app in a public shell, or a public route rendered by the shell that issued the token?" Resolved: a public magic-link page, no shell install.
- Question 4: "Which brand layer wins on an accepted proposal: the contractor tokens (which HH Pro rules must beat the dealer's) or the dealer tokens the ERP quote page uses?" Resolved: HH Pro's brand on acceptance.
- Question 10: "Do Yard Pick and Dispatch share a shell surface for the yard manager, who is the one role that opens both from the same tablet?" Resolved: Yard Pick and Dispatch stay in different shells; the yard manager installs both.

## Options rejected, and what each loses

### Drive as its own standalone shell (question 1)

Rejected. A third internal shell is a third install and a third chrome surface for the same audience, and it is the option that would put a location permission prompt in front of office roles or strip Drive of the field chrome it needs. What Drive gives up under the decision: its pinned theme and its deliberately navigation-free surface, absorbed into HH Field's identical chrome. What the decision protects: one install per work context, and no desktop shell asking a counter clerk for GPS.

### One shared shell tab for inside sales (question 2)

Rejected. A merged tab forces till chrome (tenders, parked tickets, the over-or-short count) onto roles that never touch it, because outside sales never opens the till. The shared quote-to-order entity view keeps the handoff in one place without merging the apps, so the separation costs the workflow nothing.

### Homeowner acceptance as an installable public micro-app (question 3)

Rejected. A homeowner arrives from a link in a message; requiring an install to view and sign a proposal would lose acceptances. The magic-link page renders under the `hh-public` shell family with brand `hh-pro` and no install. What the page gives up: nothing structural, since acceptance resolves online like every money-adjacent action (the path's standing rule 4).

### Contractor tokens or dealer tokens winning the frame outright (question 4)

Both rejected as stated. The HH Pro frame (brand `hh-pro`) wins the chrome, and the contractor's `tenant_brand` renders inside that frame on customer quotes and the public desk (ADR-0019, D12). Rejecting dealer-tokens-win protects HH Pro's design rule that on customer-facing quote pages "the contractor's brand must beat the dealer's" (`domain-map.md` C14, quoting `hh_pro_dibbits/CLAUDE.md`); the dealer's tokens never appear on a contractor's document. Rejecting contractor-tokens-win protects the one identifiable product surface: the contractor's brand is content inside the frame, never the frame.

### Yard Pick and Dispatch on one shared surface (question 10)

Rejected. Dispatch is an office board on desktop and tablet; Yard Pick is phone-first, gloved, offline full mode. One surface would either pin office chrome to a phone or strip the board to a phone layout, and the work-context split is the decision's premise. The yard manager, the one role that opens both, installs both shells.

## Sessions gated

- HH-2.1: the skeleton's three Tauri shells are `tauri/hh-work`, `tauri/hh-field` and `tauri/hh-pro` per this decision.
- HH-2.2: every manifest frontend carries its shell and brand from this split.
- HH-6: builds HH Field and yard-pick, the first micro-app.
- HH-7: builds HH Work; counter-till and quote-desk as separate micro-apps with the shared quote-to-order entity view.
- HH-8: drive-run lives in HH Field.
- HH-10: public-desk is the magic-link acceptance page under the `hh-pro` brand, in the `hh-public` shell family.
