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
