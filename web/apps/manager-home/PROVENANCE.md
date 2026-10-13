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
| The one-branch-one-day board shape (runs, stops, unassigned lane) | `app/src/dispatch-board.ts` (`fetchBoard`, the `hos-dispatch-board` day view) | `src/drivers-api.ts` (`toDispatchBoard`, `DriversApi.board`) | The core's `dispatchGetBoard` answers the whole day in one read, so the Drivers view needs no per run follow-ups; date never sent, the server's own today answers |
| The run and stop status words (`Planned`, `Dispatched`, `Completed`, `Cancelled`; `Ready`, `Loaded`, `En route`, `Delivered`, `Failed`, `Cancelled`) | `app/src/dispatch-logic.ts` (`RUN_LABEL`, `STOP_LABEL`) | `runStatusWord`, `stopStatusWord` in `src/drivers-logic.ts` | Kept verbatim; an unknown value is omitted, never shown raw |
| The run row's crew line (`truck · driver`) and the status word as the row's state | `app/src/dispatch-board.ts` (`runHeader`: `run.truck_code · run.driver_name`, the status pill) | `renderDriverRow` in `src/drivers-views.ts` | Read only: no actions, no crew pickers; the row is a manager's glance, not a dispatcher's controls |
| The refresh control and the empty board's sentence | `app/src/dispatch-board.ts` (`#refresh` button, `no-runs` empty words) | `renderBoard` in `src/drivers-views.ts` | The empty words are this item's brief's own sentence ("No runs on the board today"); no auto-refresh anywhere |

What has no source in the original app and is new here, each from its named
reference:

- The whole Drivers view (item 2E) has no original screen: the original's
  day board serves a dispatcher, this view serves the three manager roles
  (Logistics Manager, Branch Manager, General Manager) asking where the
  drivers are, read only. The four state groups (On a stop, Between stops,
  Not started, Done) with the row's place line (stop n of m, done, left, did
  not go) and the change line (last change, from the wire's own status
  timestamps) are new, built on the same board read. Visual reference: the
  original's dispatch day board and this app's own landing style (the view
  header, the sheet, the eyebrow labels).
- The roster line ("N of the M drivers on the roster hold no run today"),
  counted from `dispatchListDrivers` against the board's runs: the demo
  tenant sheet names drivers by role label only (GLS-TENANT-r2), so the board
  shows the registry's names as the wire carries them and the fixtures use
  role labels.
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
