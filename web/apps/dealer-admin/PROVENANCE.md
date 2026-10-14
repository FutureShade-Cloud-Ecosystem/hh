# Dealer Admin: provenance of the ports

Everything here that was carried from the original HardscapeOS app (repository
`hardscapeos_dibbits`, read only) came over under the roadmap's port and polish
rule (take the existing and improve it): the decisions as plain code onto HH's
generated api-client, the screens rebuilt on HH's design system and the role
app template's shell. The same facts sit in the header of each file below.
The visual reference for every screen is the original's own newest screen:
the FB Design board audit (HH-BOARD-AUDIT-r1) records no boards for
dealer-admin in any file ("No built app, no boards in any file").

| What | Source path in the original app | Lands in | What changed |
| --- | --- | --- | --- |
| The staff register (person, role, tier, reach, branches, reset) | `app/src/staff-roles.ts` | `src/views-staff.ts` (the register table) | Rebuilt on HH's sheet; the tier column takes the rank words; the count is stated only when loaded |
| A role's reach words (`All branches`, `Per branch`, honest degrade) | `app/src/staff-roles-logic.ts` (`ROLE_SCOPE_LABEL`, `roleScopeWord`) | `src/dealer-logic.ts` | Kept verbatim in meaning; the same fallback arm |
| The yard out of a branch name (`Gable Landscape Supply - Point Loma` to `Point Loma`) | `app/src/staff-roles.ts` (`shortBranch`) | `shortBranch` in `src/dealer-logic.ts` | Same split; the dealer's own branch names come from the branches route, never a literal (the original's own DIB-513 fix, kept) |
| Branch grant chips; an unknown id as its own first block | `app/src/staff-roles.ts` (`branchLabel`) | `branchChips` in `src/dealer-logic.ts` | Read only: the grants editor waits, see the next row |
| The create staff form | `app/src/staff-roles.ts` (`renderCreate`, `submitCreate`) | `renderCreate` in `src/views-staff.ts`, `createFormError` in `src/dealer-logic.ts` | The core has no create staff route (the parity ledger holds the staff identity admin surface for the auth lane), so the form renders whole and disabled in the waits-for-sign-in state; the rules are the original's own checks, testable now |
| The reset link request and its copyable single use link | `app/src/staff-roles.ts` (`genReset`, `renderResetCell`, DIB-186) | the reset cell in `src/views-staff.ts`, `requestPasswordReset` in `src/dealer-api.ts` | The link renders as selectable text with a copy button; the named control keeps the original's accessibility fix |
| The set new password page's rules (eight characters, confirmed) | `app/src/staff-reset.ts` (`canSubmit`, `submit`) | `canSubmitNewPassword`, `newPasswordHint` in `src/dealer-logic.ts` | The rules only. The page itself is the shell's to host: the confirm route is the sign in work's, and this app sits behind sign in |
| Role assignment by inline select; platform roles read-only | `app/src/staff-roles.ts` (`renderRow`, `assign`) | the role cell in `src/views-staff.ts`, `assignRole` in `src/dealer-api.ts` | The select answers a change event through the host listener; the select's value is mirrored in code (the position gate binds no values into a template) |
| The refusal words (who may read, who may write) | the original's per status words (`errMessage`) | `staffRefusalWords` in `src/dealer-logic.ts` | The core's gates named: managers and above read, leadership writes; the shared Forbidden body mapped, never paraphrased into authority |
| The platform key configurator: two rosters side by side, per surface Save | `app/src/platform-hotkeys.ts` | `src/views-hotkeys.ts`, `src/hotkey-logic.ts`, the Hotkeys case in `src/app-root.ts` | Rebuilt on HH's sheet; the same two panels, each with its own scope line, its own grammar and its own Save (two writes, never one) |
| The chord grammars: shell `Ctrl+Shift+K`, counter `shift-f4` | `app/src/hotkey-logic.ts` (`bindingFromEvent`), `app/src/_pos-hotkeys.ts` (`chordFromEvent`, `validChord`, `keycap`, `HOTKEY_ORDER`) | `src/hotkey-logic.ts` | Kept in meaning; the counter's canonical order and the shell's modifier order carry over exactly, because a row the two disagree on is a key that can be stored but never pressed |
| The action registries and their sentence labels | `app/src/hotkey-logic.ts` (`HOTKEY_ACTIONS`), `app/src/_platform-hotkeys.ts` (`COUNTER_ACTION_LABEL`, `PLATFORM_SURFACE_LABEL`, `PLATFORM_SURFACE_SCOPE`) | `src/hotkey-logic.ts` | The same five shell actions and five counter actions, add never rename; the em dash in the pay label became a comma (the house dash rule) |
| The payload normaliser and the server's `can_edit` as the only write authority | `app/src/_platform-hotkeys.ts` (`asPlatformView`, `PlatformHotkeyView`) | `asPlatformView` in `src/hotkey-logic.ts`, `platformHotkeys` in `src/dealer-api.ts` | The wrong-shape body still yields empty rosters and canEdit false; the browser never computes authority |
| The effective verdict: reserved, then this roster's draft, then the dealer override; the cross substrate claim warns, never refuses | `app/src/_platform-hotkeys.ts` (`effectiveVerdict`, `reservedHolderFor`, `platformCollisionFor`, `platformClashSentence`) | `src/hotkey-logic.ts` | Kept whole, the order of the arms included; the reserved form still folds the four spellings one key arrives in |
| The capture: window listener in the capture phase, Escape abandons, Tab walks on, a refused press leaves the capture armed | `app/src/platform-hotkeys.ts` (`startCapture`, `onCapture`) | the capture half of `src/app-root.ts` | Same phase, same two ways out; the armed state is a model field, not a NUL joined string (the original packed surface and action into one string separated by a NUL byte, which no HH source may carry) |
| The read only posture: the server's `can_edit` gates Set, Unbind and Save; a non editor gets the rosters and one calm sentence | `app/src/platform-hotkeys.ts` (`body`, `renderRoster`) | `renderRosters` in `src/views-hotkeys.ts` | The sentence kept; the empty roster's empty state kept for the read only caller |
| The empty draft guard on save (`bind at least one key`) and the server's own sentence stripped of its module prefix | `app/src/platform-hotkeys.ts` (`save`), `app/src/_platform-hotkeys.ts` (`serverSentence`) | `#saveRoster` in `src/app-root.ts`, `serverSentence` in `src/hotkey-logic.ts` | Same strip (one leading lowercase word, a colon, a space), a no-op on a message that never had one |
| The browser owned keys marked, never refused (dashed keycap) | `app/src/platform-hotkeys.ts` (`BROWSER_KEYS`, `isBrowserKey`) | `src/hotkey-logic.ts`, the cap classes in `src/views-hotkeys.ts` | Same set, same escape rule rationale |
| The audit trail's list, filters, and the masked detail drawer | `app/src/audit-log.ts` | `src/views-audit.ts`, the audit methods in `src/app-root.ts`, `audit`/`auditEntry`/`auditTransaction` in `src/dealer-api.ts` | Rebuilt on HH's sheet over the generated client (list, entry, transaction); the filters ride the client's own query options |
| The sensitive action badges and the curated action filter list | `app/src/audit-log.ts` (`SENSITIVE_ACTIONS`, `ACTION_OPTIONS`) | `isSensitive`, `ACTION_OPTIONS` in `src/audit-logic.ts` | Kept verbatim |
| The two empty sentences: filtered empty is not nothing happened | `app/src/audit-log.ts` (`filtersActive`, the two `emptyState` calls) | `auditEmptyWords`, `filtersActive` in `src/audit-logic.ts` | The original's own DIB-425 rule, kept as words |
| The drawer's generation guard: opening a second row, filtering or closing voids the answer still in flight | `app/src/audit-log.ts` (`detailGen`, `openDetail`, `cancelDetail`) | `#auditDetailGen`, `#openAuditEntry`, `#closeAuditDetail` in `src/app-root.ts` | The counter carried over; the abort half is not, because the calls go through the generated client (a Result, no res.json() window); the drawer's three self describing attributes kept |
| The actor cell | `app/src/audit-log.ts` with `app/src/_audit-actor.ts` (`renderActorCell`) | `actorWords` in `src/audit-logic.ts` | The honest change: the HH core's audit projection carries no resolved name and production reads the role word empty (the core attributes by the linked users id), so the cell shows the role's words when a row carries one, else the id's own block, never a blank |
| The drilldown to one entity's whole trail | `app/src/audit-log.ts` with `app/src/_audit-drilldown.ts` | the trail card in `src/views-audit.ts`, `#openAuditTrail` in `src/app-root.ts` | The original's kind aware viewer became the one generic question the transaction route answers: this entity's rows |
| The assist admin's three panels in their order (spend, budgets by role, settings) | `app/src/assist-admin.ts` | `src/admin-assist.ts`, the Assist case in `src/admin-work.ts`, `assistSpend`/`assistLimits`/`assistSettings` and the writes in `src/admin-api.ts` | Rebuilt on HH's sheet over the generated client; the same three reads answered together and the same write-then-re-read discipline |
| The spend words: exchanges first, tokens second, a zero row as a fact | `app/src/assist-model.ts` (`spendSummary`, `spendTotal`, `formatTokens`) | `src/admin-logic.ts` | Kept in meaning; the budget sentence's em dashes became commas (the house dash rule, the hotkeys port's precedent) |
| The budget source printed, never inferred; the boundary `spent >= budget`; a zero budget reads full | `app/src/assist-model.ts` (`budgetSentence`, `overBudget`, `usedFraction`) | `src/admin-logic.ts` | The source still comes from the server's own `budget.source`, never by comparing to the role default; the operator matches the core's OverBudget exactly |
| The allowance meter, dimmed not red at the limit | `app/src/assist-admin.ts` (`.track`, `.fill.spent`) | the meter in `src/admin-assist.ts`, the mirror in `src/admin-work.ts` | The width is set in code through a data- attribute (the position gate binds no style); the at-limit dimming kept |
| The settings block: the four keys, the key variable's NAME and a yes/no, never a credential | `app/src/assist-admin.ts` (`renderSettings`, SOP-1) | `renderSettings` in `src/admin-assist.ts`, `assistSettings`/`putSetting` in `src/admin-api.ts` | The two toggles unrolled as static blocks (the position gate takes bound id and for attributes off the table) |
| The product editor's description fields and the changed-only save | `app/src/product-edit.ts` (the attribute form, `save`) | `src/admin-catalog.ts`, `PRODUCT_TEXT_FIELDS`/`productDraftFrom`/`productChangedPatch` in `src/admin-logic.ts`, `productUpdate` in `src/admin-api.ts` | The description allowlist the contract names; the body is the partial map (only changed fields ride the wire), which the original sent whole |
| The picture: set through the product's own image route, remove beside it | `app/src/product-edit.ts` (the image controls) | `renderPicture` in `src/admin-catalog.ts`, `productPutImage`/`productDeleteImage` in `src/admin-api.ts` | The contract's image route carries the bytes itself, so the attachments routes have nothing to add and are not called |
| The list price absent is not zero | `app/src/product-edit.ts` with `app/src/_quickview.ts` (DIB-404) | `listPriceWords` in `src/admin-logic.ts` | Through the shared `formatDollars` (pos-kit), no currency literal; an absent anchor reads as words |
| The curation editor's shape: the kind and id of a row, the whole set saved in one PUT, the reset per kind as a DELETE | `app/src/quote-surface-settings.ts` (the items half, DIB-507's screen settings page) | `src/views-config.ts` (the dealer's curation draft), `src/config-api.ts`, `src/config-logic.ts` | Rebuilt on HH's generated client (`settingsPutRoleItems`, `settingsDeleteRoleItems`); the original edited one surface's registry from inside that surface, this editor edits the stored rows of every surface from the dealer's administration, because apps do not import apps and item ids stay each surface's own registry's |
| The counter policy's knobs and their words (cash on demand, the restocking fee in basis points, lines and lookup presentation, the till's split and which side leads, cost and past due visibility, the curated chips) | `app/src/pos-counter-settings.ts` (the policy half, the same knobs) | the counter policy section in `src/views-settings.ts`, `src/settings-api.ts`, `src/settings-logic.ts` | Rebuilt on HH's generated client (`posGetPolicy`, `posPutPolicy`, `posPolicyHistory`); the knobs keep the core's bounds and its 422 words; no card data and no secret rides the screen, and a policy write goes to the server and never queues |
| The card rail's status and the test mode badge | the payment handler's own status half (core/internal/payment/handler.go: the status never echoes a credential) | the payments configuration section in `src/views-settings.ts`, `paymentsConfig` in `src/settings-api.ts` | Read only on purpose: the core's configure write is a whole replace that would retire the stored credentials, so the screen names the implementation as where the rail is set and offers no form and no save; a patch route is a core change for the PRR backlog |
| The thirteen profiles and their words (five base shapes, eight manager refinements) | `app/src/quote-surface-settings.ts` (`PROFILES`), the settings kit's own `validProfiles` (core/internal/settings/settings.go) and migration 0381 | `JOB_SHAPES` in `src/config-logic.ts` | The kit's thirteen, because the kit resolves every surface's profile through the role table and reads role items for exactly that profile; the core's 422 sentence still names five, a stale message |
| The item set's shape checks and their words (kind enum, id alphabet, positions, duplicates, the 128 bound, empty means DELETE) | the settings kit's own validators (core/internal/settings/settings.go `ValidateItems`) mirrored client side | `itemRowError`, `itemSetError`, `canSaveItems` in `src/config-logic.ts` | The server's checks restated in the server's words so a row that cannot pass spares the round trip; the server's 422 stays the last word |
| The role to shape catalog (which staff roles hold which job shape) | the seed of record (core/db/migrations/0140_dashboard_layouts.sql, the `dashboard_role_profiles` rows, as amended by 0381_manager_home_role_profiles.sql) and the kit's ProfileFallback | `SHAPE_ROLE_CODES`, `profileForRoleCode`, `rolesForShape` in `src/config-logic.ts`, the catalog card in `src/views-config.ts` | Presented as words beside the grid, from the core's own seed; the server resolves each person's real shape and a role with no row answers to the counter shape, the kit's own fallback |
| The Pricing surface: one console of sub screens, twelve tabs | `app/src/pricing-admin.ts` (the sub nav and its tab order) | the registry row in `src/surfaces.ts`, `src/views-pricing.ts` (the strip and the router), `src/pricing-controller.ts`, `src/pricing-api.ts` | The original's fourteen sub tabs regrouped into the item's twelve (promotions and clearance share a tab; imports and the price file each kept their own; the cost compare screen is not carried, see the note below) |
| The enum to word maps (scope, calc, audit action, price source, batch status, derivation, customer type, UoM source) | `app/src/pricing-words-logic.ts` and `app/src/pricing-shared.ts` (`SCOPE_LABELS`, `ENTITY_LABELS`) | `src/pricing-logic.ts` | Kept verbatim where the original had ratified a word; the honest degrade arm (lowercased, separators opened out) kept; the category id table (`CATEGORY_NAMES`) is NOT carried: HH asks the catalog read the contract serves the pricing screens, so one tenant's seed ids never wear another tenant's screen |
| The money and percent formats (cents to text, a typed percent to bps) | `app/src/pricing-shared.ts` (`formatMoney`, `formatPct`, `pctToBps`) | `src/pricing-logic.ts` (`moneyWords`, `pctWords`, `bpsFromPctInput`, `centsFromMoneyInput`) | The encoders refuse negatives and non numbers at the form; no figure is ever summed or derived client side (the money rule) |
| The floor warning's reading of the server's grading | the quote desk's own `app/src/_pricing-floor.ts` (same original lineage) and the core's floor preview contract | `src/pricing-logic.ts` (`affectedFloorItems`, `needsFloorConfirm`, `floorNoteLines`), the floor walk in `src/pricing-controller.ts` | The server grades, the screen shows the grade; the confirmation is logged by the server against the record the save wrote, its verdict shown whole; the no-cost branch list rides as notes, never a stop (the contract's own reading) |
| The margin policy tab: the category policies grid and the upsert editor | `app/src/margin-grid.ts` (the original's keystone grid, read only) | `renderMargin` in `src/views-pricing.ts`, the margin group in `src/pricing-controller.ts` and `src/pricing-api.ts` | Rebuilt on HH's sheet; the editor saves through the floor preview first; enforcement off renders as a state chip naming that bypasses are logged |
| The rules tab: the list, the scope filter, and the rule editor | `app/src/rule-editor.ts` (the original's editor, read only) | `renderRules` in `src/views-pricing.ts`, the rules group in `src/pricing-controller.ts` and `src/pricing-api.ts` | The original created only the three scopes its editor could spell and showed the fourth verbatim in the list; HH's editor offers all four scopes the CHECK admits, each with its own word; the edit re-reads the rule first so the form carries the version the server holds now (the quote desk's own reading, kept) |
| A rule's value cell in the calc's own unit, and the validity window words | `app/src/rule-editor.ts` (the value rendering) | `ruleValueWords`, `ruleCalcWords`, `validityWords` in `src/pricing-logic.ts` | The fixed price keeps the unit beside the figure; the window says from and to rather than showing raw timestamps |
| Retire asks twice | `app/src/rule-editor.ts` (the retire confirm dialog) | the two tap retire in `src/views-pricing.ts` and `#retireRule` in `src/pricing-controller.ts` | The window confirm became the two tap control (no dialog layer in this app); the second tap still carries the row's version, and a stale version is the server's own 409 words |
| The customer prices tab: one account's category overrides, added, edited, deleted | `app/src/customer-pricing.ts` (the original's tab, read only) | `renderCustomers` in `src/views-pricing.ts`, the customers group in `src/pricing-controller.ts` and `src/pricing-api.ts` | The original picked the customer from its own account catalog; the HH contract scopes the customer search and account reads to the counter till and the quote desk, so this tab is id driven and says so on the glass. A read or a write belongs to the customer it began for: the id is captured when the load starts and a late answer is dropped (the original's own custody rule, kept). The two empties (no customer named, a customer with no prices) are the original's own DIB-425 rule |
| The quantity breaks tab: the schedules, the tier editor, the retire | `app/src/quantity-breaks.ts` (the original's editor, read only) | `renderBreaks` in `src/views-pricing.ts`, the breaks group in `src/pricing-controller.ts` and `src/pricing-api.ts` | The original's band and adjustment rendering kept as words (a null top reads "and up"; the adjustment says off or surcharge in its own unit); the tier editor adds and drops rows instead of the original's fixed grid; a save rides the floor preview like every other priced write (the original's breaks editor had no floor walk: the contract's preview kinds name quantity_break, so HH's does) |
| The promotions and clearance tab: the two timed discount panels | `app/src/promo-admin.ts` (the original's promotions admin, read only) | `renderPromos` in `src/views-pricing.ts`, the promos group in `src/pricing-controller.ts` and `src/pricing-api.ts` | The original's placement kept (immediately after quantity breaks: a break and a promo can land on one line, and the precedence between them is what a manager checks); a promotion saves straight through with no floor walk, because the contract's floor preview grades rule, override, policy and quantity break only; clearance kept the original's badge only case, said plainly instead of a zero |
| The rebates tab: the programs, the vendor ledger, and the accrual write | `app/src/pricing-rebates.ts` (the original's read only console) | `renderRebates` in `src/views-pricing.ts`, the rebates group in `src/pricing-controller.ts` and `src/pricing-api.ts` | The rate rendering kept (the rate in the shape its basis means); the ledger kept as the claim side, said so on the glass (the original's own refusal to fake a scorecard); the accrual write is new here (the contract's accrue route, which the original had no screen for): the caller supplies the sale's facts, the verdict is the server's own counts, and a retry reads as skipped, not an error; the ledger follows the first vendor the programs name, so the screen demonstrates itself (the original's own default pick) |
| The units of measure tab: the conversion edges, the unconfirmed marker, and the line-entry preview | `app/src/pricing-uom.ts` and `app/src/pricing-uom-logic.ts` | `renderUom` in `src/views-pricing.ts`, `chainLabel`, `pickSentence`, `weightSentence`, `isUomConfirmed`, `trimNumber` in `src/pricing-logic.ts`, the uom group in `src/pricing-controller.ts` and `src/pricing-api.ts` | The sentences kept whole: the pick sentence always names the overage, the chain names its hops, the unconfirmed marker rides the row and the plan; the preview sits on the admin screen for the original's own reason (typing a quantity and reading what it becomes IS the review). The em dash in the original's pick sentence became a comma (the house dash rule). The source note moved from a hover title to text on the glass (the position gate binds no title) |
| The batches and imports tab: the staged updates, the maker check, and the import profiles | `app/src/price-batches.ts` and `app/src/price-imports.ts` (the original's two consoles, read only) | `renderBatches` in `src/views-pricing.ts`, the batches group in `src/pricing-controller.ts` and `src/pricing-api.ts` | The two consoles kept apart as the original kept them (a batch's own import inside the open batch; the profiles below); the status words are the map's own (Draft, Staged, Approved, Applied), the outlier and the unmatched SKU are named on the line as the contract reports them; the import reads the pasted file whole and the answer is the server's counts; a mapping edit names the column headers, the keys the contract's mapping object carries |
| The price file tab: the vendors, the preview with its digest, and the staged apply | `app/src/price-file-import.ts` and `app/src/price-file-logic.ts` (the original's road, read only) | `renderPriceFile` in `src/views-pricing.ts`, the price file group in `src/pricing-controller.ts` and `src/pricing-api.ts` | The original's placement kept (next to imports: the same job done two ways, and a reader comparing them is the point); the preview writes nothing and the apply must echo the preview's digest, a changed file being the server's own refusal; the counts over the whole file carry the unpriced diagnostic (a mis-mapped column is how it reads); the runs log answers who imported what when. The demo harness hands the file over as CSV text; an XLSX byte stream through the same door waits |
| The low margin tab: the scan of committed lines and the proposed line grader | `app/src/lowmargin-report.ts` (the original's report, read only) | `renderLowMargin` in `src/views-pricing.ts`, `lowMarginWords` in `src/pricing-logic.ts`, the low margin group in `src/pricing-controller.ts` and `src/pricing-api.ts` | The scan reads the committed lines at the price the document holds, net of discount, against current per branch cost (the contract's own grading); the clearing stamp is named with who and what it cleared, and a deeper discount since is not cleared (the server decides, the screen shows it); the grader is new here, the contract's batch report: one proposed line through the resolver before it is quoted, the server naming the breach and its source level |
| The pricing audit tab: the log with its filters, the bypass named | `app/src/pricing-audit.ts` (the original's read only viewer) | `renderPricingAudit` in `src/views-pricing.ts`, `auditActionWord` in `src/pricing-logic.ts`, the audit group in `src/pricing-controller.ts` and `src/pricing-api.ts` | The BYPASS row kept its red chip and now names its reason on the row beneath (the contract carries bypass_reason, the original showed it only in the drill-down); the delta reads from to to; the actor shows the id's block when the row names only an id (the app's actor rule); the original's client side date range and pager became the server's own entity and action filters (the contract filters server side), and the drill-through links wait for the app's audit surface to answer entity trails (the tab links nothing it cannot open) |
| The change log tab: who moved a cost or a retail | `app/src/price-change-log.ts` (the original's log, read only) | `renderChangeLog` in `src/views-pricing.ts`, the change log group in `src/pricing-controller.ts` and `src/pricing-api.ts` | The from and to money kept as the server's figures; the field word says cost or retail; the batch and the effective date ride beside the row, so a change traces to the import that caused it |

What the original's audit surface carried that this port does not, said
plainly: the roving tabindex cursor with its arrow key model and the
aria-selected discipline around it (the original's own accessibility round
machinery). The rows here are tab stops whose Enter opens, and Escape
closes, the drawer; the full two hundred row keyboard model is not carried.

What the original's pricing console carried that this port does not, said
plainly, with the reason each waited on:

- The cost compare sub tab (`app/src/price-cost-compare.ts`): the HH contract
  has no pricing cost compare route. The original called
  `/api/v1/pricing/costcompare`, which the HH core did not carry. When a
  route lands, the tab is one registry entry in `PRICING_TABS` and one
  controller group away.
- The printable pricing order document and its CSV projection
  (`app/src/pricing-doc.ts`, `app/src/pricing-doc-logic.ts`): the document's
  server is the pricing order family, and the HH contract scopes those
  routes to the counter till alone (`x-access: surfaces: [counter-till]`).
  A dealer-admin screen has no entitled source of a priced document to
  render or export. The projection's own logic (named columns, totals as
  total rows, the sanitised file name) ports mechanically the day a route
  admits this app.
- The audit drill-through focus event (the original's record-focus seam that
  flipped the console to the customer or category a pricing audit row
  named): the HH audit surface's drawer answers the entity's whole trail
  instead, and the pricing log's rows are readable in the Pricing audit tab
  with the same filters.

What has no source in the original app and is new here, each from its named
reference:

- The one app with a left list of surfaces (Staff, then Hotkeys and Audit)
  and the registry that names them (`src/surfaces.ts`): the mod-c item's own
  shape, so the next coder adds Pricing, Role configuration and Deployment
  settings one registry row and one views file at a time. The shell rail
  (row 1520) stays the shell's; this list is the app's own table of contents.
- The waits-for-sign-in marker and its sentence: the parity ledger's own
  fact (the staff identity admin surface is unregistered, the auth lane's),
  stated on the glass instead of a submit that could only fail.
- The demo door's own cards and marks: the template's entry, drawn with this
  app's flows (the stub's two; the demo image's closed flow table is the
  lead's wiring).
- The Print jobs surface (`src/admin-print.ts`, `printingListJobs`): the
  original app never called the print queue's list, so the screen is new.
  Visual reference: this app's own filter form and table (the audit trail's),
  on HH's tokens.
- The Exports surface (`src/admin-exports.ts`, `exportAuditXlsx` and
  `messagingExport`): one call, one file, no queue. The workbook's filters
  are the audit trail's own four; the messages card carries the server's
  masking rule as its own words. No original screen; same visual reference.
- The Platform keys surface (`src/admin-keys.ts`, `listPlatformKeys`): the
  contract marks the read for this surface (staff wide) and the write is the
  operator's alone, so the screen renders no write. The 404 the unregistered
  route answers is the waits state's input, the staff register's own
  precedent. No original screen; same visual reference.
- The catalog's finder and the units and categories cards: the original
  opened its editor from a catalogue list this app does not carry, so the
  finder (the catalogue's own list read, narrowed) and the two staff reads
  are new. The editor is the port, see the table above.
- The admin-rest controller (`src/admin-work.ts`) and the door
  (`src/admin-api.ts`): the Money Desk's receivables desk pattern (a
  controller the root element owns), so the root's own file stays small and
  the registry's surfaces land one file at a time.
- The role by surface grid, the editor for one cell, the caller's own view
  and workspace cards, and the job shape catalog (`src/views-config.ts`):
  there is no original screen for a per dealer role configuration. The
  visual references are this app's own staff surface (the card and table
  style, the refusal and empty states) and Manager Home's matrix words;
  the editor's row shape follows the original's DIB-507 items half above.
  The grid's caption is the settings kit's own sentence about what
  curation is and is not (core/internal/settings/settings.go, the package
  header), spoken on the glass.
- The caller's own override and workspace cards (the `my-items` and
  `my-workspace` halves): the kit's own two levels, whole or absent,
  reset IS a DELETE, the workspace save carrying the settings write
  idempotency key (core/internal/settings/service.go). No original screen.
- The role by surface grid's catalog card, the deployment settings as one
  surface of four sections, the walkthrough's chapter list and step copy
  editor, and the policy history table: each from its named reference above
  (the manager-home matrix words, the original's counter settings screen)
  or new here with the app's own card style for its reference, because no
  original screen gathered them.
- The brand section's waits state (`BRAND_WAITS_WORDS`): the parity
  ledger's own fact (the tenant brand pair is registered in the contract
  and held, unregistered in the core), stated on the glass with the form
  whole and disabled, the staff family's waits posture carried over.
- The walkthrough's platform gate words: the tour package's own refusal
  sentence, kept whole on the glass, and the calm read only line for a
  caller the server's `can_edit` refuses.
