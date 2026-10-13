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
