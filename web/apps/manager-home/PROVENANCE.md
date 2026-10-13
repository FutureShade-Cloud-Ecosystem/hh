# Manager Home: provenance of the ports

Everything here that was carried from the original HardscapeOS app (repository
`hardscapeos_dibbits`, read only, at `/home/colton/hh-lanes/refs/original-app`)
came over under the roadmap's row 563 rule (take the existing and improve it):
the decisions as plain code onto HH's generated api-client, the screens rebuilt
on HH's design system and the role app template's shell. The same facts sit in
the header of each file below.

| What | Source path in the original app | Lands in | What changed |
| --- | --- | --- | --- |
| Severity words (`Routine`, `Watch`, `Urgent`) | `app/src/_widget.ts` (`SEVERITY_WORD`, `severityWord`) | `src/manager-logic.ts` | Kept verbatim; colour never travels alone |
| Named window words (`Today`, `Everything open`, `Last 14 days`) | `app/src/home-bento-logic.ts` (`RANGE_WORD`, `rangeWord`) | `src/manager-logic.ts` | Kept verbatim |
| The stat rule: null is not zero, only `state === "ok"` with a total is a figure | `app/src/home-bento-logic.ts` (`headlineFor`) | `figureFor` in `src/manager-logic.ts` | Money renders through pos-kit's shared `formatDollars`; days and basis points added |
| Bars rows: label · track · value with the share as the note, one max so bars compare | `app/src/home-bento-logic.ts` (`hbarsRows`) | `renderBars` in `src/views.ts` | Rebuilt on this app's sheet; no second track for the same fact |
| The bento grid: twelve columns at desktop, six below, server-sent spans | `app/src/home-bento.ts` | `src/styles.ts`, `withSpan` in `src/views.ts` | Read-only landing: no builder, no grip, no drag in this first slice |
| Needs-you queue shape (rows, total, worst) | `app/src/home-bento-logic.ts` (`NeedsQueue`) | `src/manager-api.ts` | Whitelist projection over the generated client |
| The approvals surface's shape | `app/src/pending-approvals.ts` | `renderApprovals` in `src/views.ts` | By-kind tabs over the core's `kind` filter; decide actions; refusal words naming who decides |
| API call set | `app/src/home-bento-logic.ts` (the widget reads) | `src/manager-api.ts` | Over the generated client; every read goes through a whitelist projection |
| The builder's api door: every dashboard route the arrange surface calls | `app/src/dashboard-api.ts` | `src/builder-api.ts` | Rebuilt on HH's generated client; every read and write goes through a whitelist projection; the free span and the branch travel as a pair or not at all, never zeroed; the server's colour and glyph fields are dropped at the door |
| The builder's pure decisions: the preset table (S 3x1 to XL 12x1), the snap set, move, resize, remove, the whole-layout body, the draft reconcile, the gallery placement, the composed viz id, the pin strip's whole-set edit rule | `app/src/_widget.ts`, `app/src/home-bento-logic.ts`, `app/src/dashboard-logic.ts`, `app/src/dashboard-nav.ts` | `src/builder-logic.ts` | The core's arithmetic mirrored (SpanOf, the snap set, three rows); a move past the end clamps instead of refusing; the key mint strips a trailing dash; HH words (`sourceWord`) added |
| The builder desk: edit state, the gallery, the saves and resets | `app/src/dashboard-editor.ts`, `app/src/home-bento.ts` | `src/builder-desk.ts` | The state machine as a plain class over the generated client, node-testable; whole-or-absent writes kept (every save PUTs the whole renumbered list, every reset answers what it landed on); refusals shown in the core's own words; the admin gate never guessed client side |
| The arrange view: the edit grid, the gallery-first Add widget sheet, the seven-field form with its live preview, the pinned tabs and classic tiles blocks, the role defaults an admin sets | `app/src/home-bento.ts` (arrange mode), `app/src/widget-builder.ts`, `app/src/dashboard-editor.ts`, `app/src/widget-card.ts`, `app/src/widget-body.ts` | `src/builder-views.ts`, `src/builder-styles.ts` | Rebuilt on HH's tokens and the role app's sheet; the keyboard rule kept by making every control a labelled button (the original's drag grip and Alt+arrow walk become move, size and remove buttons, so nothing is drag only); the gallery stays in front of the form; the preview still composes the one viz read that can measure a draft; the empty home opens on the inventory's own sentence |
| The landing's one edit button and the arrange view kind | (new here) | `src/views.ts`, `src/manager-logic.ts`, `src/app-root.ts` | One button on the landing, one view kind in the switch; the desk lives beside the other doors and the landing re-reads when the caller comes back |

What has no source in the original app and is new here, each from its named
reference:

- The needs links' own views (`Shortages · open`, `Receiving · short`), the
  resolve sheet and its three resolution choices: the FB Design boards
  `manager-home__yard-manager-light.png` and
  `manager-home__needs-you-resolve-shortage.png` (PROPOSED, row 1199), with
  the two CYCLE-2-NOTES defects not copied (this board has neither of the
  named defects).
- The walled refusal words (who may resolve a shortage, who decides each
  approval kind): `shortage_resolvers` and `exception_approvers` as the role
  matrix names them (FROM-PRINCIPAL 39, ROLE-APP-OBLIGATIONS 1).
- The receiving difference words: the ruled definition (decision log row
  1245), stated in words where it helps (MANAGER-HOME-UI-NOTES).
- The rows the board drew that nothing measures (`Weights pending`,
  `Unread messages`): said plainly, never faked (MANAGER-HOME-UI-NOTES).
