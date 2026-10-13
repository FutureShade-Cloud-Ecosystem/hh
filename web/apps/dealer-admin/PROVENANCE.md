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
| The curation editor's shape: the kind and id of a row, the whole set saved in one PUT, the reset per kind as a DELETE | `app/src/quote-surface-settings.ts` (the items half, DIB-507's screen settings page) | `src/views-config.ts` (the dealer's curation draft), `src/config-api.ts`, `src/config-logic.ts` | Rebuilt on HH's generated client (`settingsPutRoleItems`, `settingsDeleteRoleItems`); the original edited one surface's registry from inside that surface, this editor edits the stored rows of every surface from the dealer's administration, because apps do not import apps and item ids stay each surface's own registry's |
| The five job shapes and their words | `app/src/quote-surface-settings.ts` (`PROFILES`), the settings kit's own `validProfiles` and its 422 words (core/internal/settings/settings.go) | `JOB_SHAPES` in `src/config-logic.ts` | The kit's five, the order and the refusal words kept |
| The item set's shape checks and their words (kind enum, id alphabet, positions, duplicates, the 128 bound, empty means DELETE) | the settings kit's own validators (core/internal/settings/settings.go `ValidateItems`) mirrored client side | `itemRowError`, `itemSetError`, `canSaveItems` in `src/config-logic.ts` | The server's checks restated in the server's words so a row that cannot pass spares the round trip; the server's 422 stays the last word |
| The role to shape catalog (which staff roles hold which job shape) | the seed of record (core/db/migrations/0140_dashboard_layouts.sql, the `dashboard_role_profiles` rows) and the kit's ProfileFallback | `SHAPE_ROLE_CODES`, `profileForRoleCode`, `rolesForShape` in `src/config-logic.ts`, the catalog card in `src/views-config.ts` | Presented as words beside the grid, from the core's own seed; the server resolves each person's real shape and a role with no row answers to the counter shape, the kit's own fallback |

What the original's audit surface carried that this port does not, said
plainly: the roving tabindex cursor with its arrow key model and the
aria-selected discipline around it (the original's own accessibility round
machinery). The rows here are tab stops whose Enter opens, and Escape
closes, the drawer; the full two hundred row keyboard model is not carried.

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
