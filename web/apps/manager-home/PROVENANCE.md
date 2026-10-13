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
| The builder's api door: every dashboard route the arrange surface calls | `app/src/dashboard-api.ts` | `src/builder-api.ts` | Rebuilt on HH's generated client; every read and write goes through a whitelist projection; the free span and the branch travel as a pair or not at all, never zeroed; the server's colour and glyph fields are dropped at the door |
| The builder's pure decisions: the preset table (S 3x1 to XL 12x1), the snap set, move, resize, remove, the whole-layout body, the draft reconcile, the gallery placement, the composed viz id, the pin strip's whole-set edit rule | `app/src/_widget.ts`, `app/src/home-bento-logic.ts`, `app/src/dashboard-logic.ts`, `app/src/dashboard-nav.ts` | `src/builder-logic.ts` | The core's arithmetic mirrored (SpanOf, the snap set, three rows); a move past the end clamps instead of refusing; the key mint strips a trailing dash; HH words (`sourceWord`) added |
| The builder desk: edit state, the gallery, the saves and resets | `app/src/dashboard-editor.ts`, `app/src/home-bento.ts` | `src/builder-desk.ts` | The state machine as a plain class over the generated client, node-testable; whole-or-absent writes kept (every save PUTs the whole renumbered list, every reset answers what it landed on); refusals shown in the core's own words; the admin gate never guessed client side |
| The arrange view: the edit grid, the gallery-first Add widget sheet, the seven-field form with its live preview, the pinned tabs and classic tiles blocks, the role defaults an admin sets | `app/src/home-bento.ts` (arrange mode), `app/src/widget-builder.ts`, `app/src/dashboard-editor.ts`, `app/src/widget-card.ts`, `app/src/widget-body.ts` | `src/builder-views.ts`, `src/builder-styles.ts` | Rebuilt on HH's tokens and the role app's sheet; the keyboard rule kept by making every control a labelled button (the original's drag grip and Alt+arrow walk become move, size and remove buttons, so nothing is drag only); the gallery stays in front of the form; the preview still composes the one viz read that can measure a draft; the empty home opens on the inventory's own sentence |
| The landing's one edit button and the arrange view kind | (new here) | `src/views.ts`, `src/manager-logic.ts`, `src/app-root.ts` | One button on the landing, one view kind in the switch; the desk lives beside the other doors and the landing re-reads when the caller comes back |
| The result grid: columns by the server's declared kind, one cell formatter, totals foot, the summary line naming rows and the leading money total | `app/src/reporting-logic.ts` (`formatCell`, `totalCell`, `resultSummary`), `app/src/reporting-console.ts` (`renderGrid`) | `formatCell`, `totalCell`, `resultSummary` in `src/reporting-logic.ts`, `renderResultGrid` in `src/reporting-views.ts` | Kept, on HH tokens; the summary names the first total cents column as the server sent it |
| The parameter form is GENERATED from the server's param specs, never written per report; dates default to month to date; the one guard is a required field left empty; empty values are dropped, not sent | `app/src/reporting-console.ts` (`renderParam`, and the header comment), `app/src/reporting-logic.ts` (`defaultParams`, `missingRequired`, `runQuery`) | `defaultParams`, `missingRequired`, `runParams`, `printAtNeeded` in `src/reporting-logic.ts`, `renderParam` in `src/reporting-views.ts` | Kept; the branch parameter renders the caller's own branches by name, resolved to ids at the door |
| The print flow: freeze the artefact server side, open the STORED document (never a rerun); the "print at" pick when the effective parameters name no branch | `app/src/reporting-console.ts` (`print`, `printBranch`), `app/src/reporting-api.ts` (`printReport`, `fetchDocument`, `openPrintWindow`) | `printTemplate`, `runDocument` in `src/reporting-api.ts`, `#printReportingTemplate` in `src/app-root.ts` | The document writer is refused by this app's raw-markup fence, so the fetched document rides the shared blob carriage the till's receipt uses (pos-kit's `printReceiptInto`); the pop-up-blocked case keeps its operator words |
| The commission register: preview on the fly, freeze per rep (a per-rep button, never freeze all), drill-through lines, a line that earned nothing is MARKED, the accrual caveat rendered from the server's own `note` | `app/src/reporting-commission.ts`, `app/src/reporting-logic.ts` (`unattributedWarning`, `lineIsUnpaid`, `basisLabel`, `periodLabel`), `app/src/reporting-commission.ts` (`renderAccrual`) | `src/reporting-views.ts` (the Accruals pane), `unattributedWords`, `lineIsUnpaid`, `basisLabel`, `periodLabel` in `src/reporting-logic.ts` | Kept; the client side period totals strip and the derived margin percent are NOT ported (commission figures are server figures only); the unattributed caveat words restated in this app's voice over the same server fields |
| The commission rules list, create and retire | `app/src/reporting-api.ts` (`listRules`, `createRule`, `retireRule`) | `commissionRules`, `createCommissionRule`, `retireCommissionRule` in `src/reporting-api.ts`, the Commission rules pane in `src/reporting-views.ts` | The original's surface never rendered them; this pane does, behind the core's own guard windows (read for managers, write for tier one global, the shared Forbidden body worded in plain words) |
| The saved views (store, apply, delete a parameter set) | No source in the original app's surface (the HH contract's `reportingListSavedViews`, `reportingSaveView`, `reportingDeleteSavedView`) | the saved-views strip in `src/reporting-views.ts`, `#saveReportingView` in `src/app-root.ts` | New here: save the form's current parameters under a name, apply them back, delete; the server validates the params at save time |
| The frozen runs pane (list, expand, open the stored document) | `app/src/reporting-api.ts` (`listRuns`, `getRun`, `documentUrl`) | the Runs pane in `src/reporting-views.ts`, `#loadRuns`, `#expandReportingRun`, `#openRunDocument` in `src/app-root.ts` | The original's api carried runs; no screen did. This pane is that screen |
| The attribution check | `app/src/reporting-api.ts` (`attributionCheck`) | `reportingAttributionCheck` wired in `src/reporting-api.ts`, rendered in the Accruals pane | The original's api carried it; no screen drew it. This pane states the server's explanation and lists the disagreeing invoices |
| The warranty desk's call set and wire vocabulary (phases, outcomes, registry enums, recovery buckets, money paths, SLA states) | `app/src/warranty-api.ts` | `src/warranty-api.ts` | Rebuilt on the generated client's `staffWarranty` operations; every answer goes through a whitelist projection; money stays integer cents end to end |
| The Kanban board (columns in the confirmed phase order, the exits last; cards open the claim) and the capture sheet with the back-date field (FR-2, FR-9) | `app/src/warranty-board.ts` | `renderBoardTab` and `renderCaptureForm` in `src/warranty-views.ts`, `#capture` in `src/warranty-desk.ts` | The branch is the caller's own resolved branch, never a hardcoded list; the vendor pick list carries words and maps back by row; capture goes to the server in one call and opens the answer's detail |
| The claims list with the phase filter | `app/src/warranty-api.ts` (`listClaims`) | `renderClaimsTab` in `src/warranty-views.ts` | A tab of its own (the original exported the call without a screen); filter chips carry the phase words |
| The claim detail: lifecycle moves (the transition map), the per-phase checklist, the evidence-ref seam, the audit-spine timeline | `app/src/warranty-claim.ts` | `renderClaimDetail` and its boxes in `src/warranty-views.ts`, `#advance`, `#tick`, `#attachEvidence` in `src/warranty-desk.ts` | Checkbox rows are gate-clean buttons; the tick and the move re-read the server's answer; the gate refusal is the core's own sentence naming the missing items |
| Manager settlement recording (FR-5) and the per-outcome money paths (ERP:warranty-p2) | `app/src/warranty-claim.ts` (`onSettle`), `app/src/warranty-api.ts` (`SettlementResult`, `claimPostings`, `PATH_STATUS_LABEL`, `MONEY_PATH_LABEL`) | `settlementBox`, `postingsBox` in `src/warranty-views.ts`, `#settle` in `src/warranty-desk.ts` | Settle goes to the server and never queues; the answer's figures are what the claim then shows; the sheet is offered only from the decision phase on, in the core's own sequence |
| The vendor warranty registry with the explicit UNDOCUMENTED state (FR-1, the Oaks case) and the product overrides | `app/src/warranty-registry.ts`, `app/src/warranty-api.ts` (`upsertVendorProfile`, `VENDOR_STATUS_LABEL`, `CLAIM_CHANNEL_LABEL`) | `renderVendorsTab` in `src/warranty-views.ts`, `#saveVendor`, `#addOverride`, `#expandVendor` in `src/warranty-desk.ts` | The expansion reads the whole profile through the single-vendor route; the write is offered to everyone and the tier 1 wall is the walled words, never a client-side gate |
| The recovery worklist: outstanding is the server's subtraction, aging from the accrual, the chase log with the five actions | `app/src/warranty-recovery.ts` | `renderRecoveryTab` in `src/warranty-views.ts`, `#logChase`, `#loadChaseLog` in `src/warranty-desk.ts` | A receipt re-reads the board rather than patching it; an over-receipt is the server's refusal in its own words |
| The service levels: timers, the durable breach alerts with their notification status, the configured targets, the sweep | `app/src/warranty-sla.ts` | `renderSlaTab` in `src/warranty-views.ts`, `#sweep`, `#ack` in `src/warranty-desk.ts` | The targets section reads the targets route (the board's own target rows are dropped at the projection door); acknowledging closes the alert |
| The FR-7 reports: funnel, by manufacturer with win/loss, time to settlement, exposure by vendor, open claims by age | `app/src/warranty-reports.ts` | `renderReportsTab` in `src/warranty-views.ts` | The funnel bars reuse the landing's quantized width classes and the landing's data ink, never the action gold |
| The board tabs (Board, Claims, Vendor registry, Recovery, Service levels, Reports) and the claim overlay | `app/src/warranty-surface.ts` | `renderWarranty` in `src/warranty-views.ts`, `WarrantyDesk` in `src/warranty-desk.ts` | One Warranty view kind on the landing, reachable for every staff role (the core's reads are RequireStaff); per-call refusals carry the core's words |
| The landing's quick-action bar: the caller's resolved registry rows, the visible word minus one leading plus, the add mark for a leading-plus label, the one-sentence accessible name, instruct then navigate on click | `app/src/quick-action-bar.ts`, `app/src/quick-action-registry.ts` (`QuickAction`, `findAction`), `app/src/quick-action-intent.ts` | `renderQuickBar` in `src/quick-views.ts`, `src/quick-logic.ts`, `src/quick-intent.ts`, `#runQuick`, `#loadQuickBar` in `src/app-root.ts`, `QuickApi.mine` in `src/quick-api.ts` | The one shell's `#unit/module` hash becomes this box's closed table of app prefixes (`sales/pos` opens the till at `/till/`, the front door's own tile address); the bar wears the original's quiet variant because the landing already spends its one gold on the day's hero; an action never starts money or a write, it navigates; empty, loading and failed all draw nothing, the optional chrome's honest degradation; the intent channel is the publisher half, and the destination's own guard and start-guard dialog stay in the counter-till app that consumes them (no second copy of the never-discard decision outside the till) |
| The admin's quick-actions config: one row per registered action, the global switch first (its off is final, the core's own gate order), every job shape resolved so no cell is blank, drafts local until Save, the saved answer drawn in place | `app/src/quick-action-admin.ts` | `renderQuickAdmin` in `src/quick-views.ts`, `QuickApi.config`, `QuickApi.putConfig`, `toQuickActionConfig`, `toQuickConfigAnswer` in `src/quick-api.ts`, `#loadQuickAdmin`, `#patchQuick`, `#saveQuick` in `src/app-root.ts` | The switches are gate-clean buttons with aria-pressed (the position gate allows no checked binding); the thirteen shapes render as words, never raw profile keys; the gate is the server's alone and its shared Forbidden body shows verbatim; a save re-reads the bar so an admin sees their own change without a reload |

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
- The quick actions' destination table (`sales/pos` opens the till at
  `/till/`): HH's shell is one micro app per unit, so the original's frozen
  `#unit/module` deep link becomes the closed map to the deployment prefixes
  the front door names (roles.ts's tile hrefs). A destination no app serves
  yet answers null and the bar says so in words, never a dead button.
- The quick actions' job-shape words (`Counter`, `Yard manager`, `Payables and
  Receivables`): the core sends the closed set of profile keys, and this app
  renders them as the role names the core's own comments and the demo flow
  table carry.
- The bar's visual reference is the original's quick-action bar (its glyph,
  words and one-sentence title kept; its gold stood down per its own union
  train ruling) beside Manager Home's landing style (the pill chips, the HH
  tokens); the config view's visual reference is the original's
  quick-action-admin card (switch first, shapes under a dashed rule, the
  shipped-default note) rebuilt on this app's list cards.
- The walled refusal words (who may resolve a shortage, who decides each
  approval kind): `shortage_resolvers` and `exception_approvers` as the role
  matrix names them (FROM-PRINCIPAL 39, ROLE-APP-OBLIGATIONS 1).
- The receiving difference words: the ruled definition (decision log row
  1245), stated in words where it helps (MANAGER-HOME-UI-NOTES).
- The rows the board drew that nothing measures (`Weights pending`,
  `Unread messages`): said plainly, never faked (MANAGER-HOME-UI-NOTES).
- The reporting view's registration: one `Reporting` view kind, reachable from
  the landing's view links for the core's manager roles only, mirrored from
  the mint's own tier facts (`canOpenReporting`, `canAdminCommissionRules` in
  `src/reporting-logic.ts`). The core answers every request itself; the link
  and the rules pane's write controls are only where the doors are drawn.
- The catalogue order, kept from the original (`orderCategories`), and the
  runs pane's naming of the two run kinds (a report and a commission
  statement).
- The walled refusal words for the tier gates (who settles, who sweeps, who
  configures the registry, who chases): the guards' tier lists
  (identity/guards.go, the role catalog 0215) as the approvals view words its
  walls.
- The public intake's photos on the claim detail: the original's public form
  predates the photos seam. The list and the bytes come from the intake photo
  routes; an object address cannot ride a template binding, so the picture is
  hydrated after render, as the drive-run app's drop thumbs are.
- The assign-to-vendor sheet: the original exported the assign call without a
  screen. The owner rides through unchanged; only the vendor moves.
- The public intake form itself (`warrantySubmitPublicIntake`) is the public
  desk's item, not this app's.
| Per role homes, gap 1: every needs row and widget card links to the live app that owns it (Money Desk, Buying and Stock, Counter Till, Yard Pick) or to this app's own view, and says "not on this box yet" only where no app exists (dispatch, the order list) | `app/src/home-bento.ts` (widget deep link by `unit/module`), `app/src/home-needs.ts` | `src/landing-links.ts` (the closed table), `needsRowTarget` in `src/manager-logic.ts`, `renderFoot` and `renderNeedsRow` in `src/views.ts`, the `open-app` action in `src/app-root.ts` | The one shell's `#unit/module` hash becomes the closed table of app prefixes the front door serves; a click may carry only an address the table names (`isLiveHref`); the row's chip names the app for a screen reader |
| Per role homes, gap 15 (test double only): the stub world covers all eight manager profiles with the real widget counts, needs sources and approval deciders, so a leg can land each role | `backend/migrations/0140_dashboard_layouts.sql`, `0203_dispatcher_dock_home.sql` (the shapes the profiles were born from) | `test/stub/profile-world.mjs`, `FLOW_PROFILE` and `decidesKind` in `test/stub/manager-stub.mjs`, `test/roles.pw.ts`, the profile cases in `test/stub-contract.test.mjs` | No product code; the lists mirror the core's 0201, 0381 and 0392 rows (procurement, sales and branch carry the 0392 landings); the freight raises are decided by the controller and the general manager only |
| Per role homes, gap 2: the Approvals view opens on the first kind that has a waiting row, not on Margin | none for the opening rule (new behaviour); the original's pending approvals list is `app/src/pending-approvals.ts` | `openingKind` in `src/approvals-logic.ts`, `#openApprovals` in `src/app-root.ts` | The whole list the caller decides (empty kind, the core answers only what the caller may decide) names what is waiting; the vocabulary's own order breaks a tie; a list that did not load leaves the first kind, whose own read words the refusal; the tab clicks still pick any kind |
| Per role homes, gap 9: the landing sentence and the link strip differ by role, decided by the core's dashboard profile name; a payables role gets no Shortages, a yard role gets no Approvals it cannot decide | none (the original keyed its nav by job shape only, `app/src/nav-access.ts`; the per role words are new work from `docs/analysis/role-catalog.yaml` top jobs) | `roleWords` in `src/role-words.ts`, `renderLanding` in `src/views.ts`, `test/roles.pw.ts` | Words and order only, never authority: every view's read is still gated by the core; an unknown profile keeps the plain sentence and the whole strip; the reporting link still rides the tier |
| Per role homes, gap 10: the trend shape draws a column sparkline and the gauge shape a segmented meter with a legend, from the server's points, beside the figure | `app/src/home-bento.ts`, `app/src/home-bento-logic.ts` (the trend sparkline and gauge forms) | `sparkColumns`, `sparkLabel`, `gaugeSegments` in `src/widget-shapes.ts`, `renderTrend` and `renderGauge` in `src/views.ts`, the trend and gauge block in `src/styles.ts` | Bound style attributes become the sheet's static five percent classes, as the bars do; fewer than two points is no line and no positive sum is no meter, never an invented series; the core measures the gauge as a count with no ceiling, so the meter shows composition (shares of the points' own sum), not "how full"; every exact figure rides as text and a spoken label; nothing animates; tokens only |
