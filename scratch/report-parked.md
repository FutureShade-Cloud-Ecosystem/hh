# Report: the parked dialog defect (W9 item, row 692 item 2, row 700 item 1)

Coder A, Counter Till lane. Branch `till/parked-dialog-refresh`, cut from
origin/main f63878e. Final head: `1f55c3ca2550994bfa413cab4aee55236d768ad6`,
pushed (three commits, each signed off, fast-forward only). Size: about 2
dev-hour equivalents, including the environment detour recorded below.

## The source reading

- `web/apps/counter-till/src/ported/pos-terminal.ts`, `parkCurrent` (line
  3931 on the base): awaits the park POST, snapshots the display facts under
  the server ticket id, clears the cart through `newSale()`, sets the util
  message, then fires `void this.refreshParkedCount()` (line 2827): a
  fire-and-forget `listParked` read that updates only the button's count.
  The same fire-and-forget read runs at mount (line 2647), after resume
  (line 4023), and on dialog close (line 6505).
- `web/apps/counter-till/src/ported/pos-parked.ts`: `connectedCallback`
  (line 180) calls `load()` exactly once on open; `load()` (line 186) reads
  the branch's parked list and nothing ever re-reads it. The dialog's own
  Resume and Discard paths re-read, but a park landing while the dialog is
  open does not.
- The spec's `resumeFirst` (acceptance `30-manager.pw.ts` line 114) clicks
  "Park ticket" and opens the dialog through `openParked`
  (`helpers.ts` lines 377 to 391), whose poll computes the dialog's row count
  minus the count the button states. The click returns at dispatch while the
  park POST is still in flight, so the dialog's one read can land before the
  park commits: from then on the open dialog is frozen at the stale list
  while the button beside it states the newer figure. That is the recorded
  failure: expected 0, received -1, at `helpers.ts:384`.
- Evidence agrees: `w9loc-run.log` and `w9loc-run3.log` (fresh worlds) fail
  the same story at the same poll, with snapshots showing "Parked as
  #PARK-5", the button reading "Parked (3)", and the open dialog listing 2.
- The reference carries the same read-once shape
  (`ref-master/app/src/pos-parked.ts:173`, `ref-master/app/src/pos-terminal.ts:2590`),
  so the fix is an improvement on the reference, which the take-and-improve
  ruling (row 563) asks for. Provenance is named in the comments where it
  matters.

Verdict implemented (ruled by the lead): the wrong side is the SURFACE. A
person who parks and opens the Parked dialog in one motion sees their ticket
missing until close and reopen, and the button and the open dialog disagree
on the same screen. The spec's poll is the honest wait and stays as it is.

## The fix shape

Two source files, no contract change, no new dependency, no polling loop:

1. `pos-parked.ts`: the private `load()` becomes a public `reload()` (same
   body, same behavior on open; the internal resume and discard callers
   renamed along). The doc comment states why the once-on-open shape needs a
   re-read entry point and names the reference.
2. `pos-terminal.ts`: when `refreshParkedCount()`'s read lands and the
   dialog is open, the terminal queries its own render tree for
   `hos-pos-parked` (the codebase's typed `querySelector` shape) and calls
   `reload()`. The count read already fires exactly at the moments the list
   changes through the terminal (park, resume, dialog close), so the open
   dialog converges within one read of any such change.

Untouched on purpose: the acceptance spec and its poll, the stub, the
reference checkout, every other file. The committed diff is exactly the two
ported sources plus the one new story in `test/sale.pw.ts`.

## Fail first, and the bite proof

The story lives beside the parked stories (`test/sale.pw.ts:1851`): "a park
landing while the parked dialog is open converges the open dialog's list".
It reads the branch's parked count through the contract's own read first,
then holds the park POST with `page.route` (the request is not released
until the story releases it), clicks "Park ticket", opens the dialog, waits
for the dialog's first list read to be answered from the uncommitted world,
asserts the dialog lists the old count, then releases the park and asks the
open dialog to converge: the just-parked ticket's number visible, and the
button's count and the dialog's rows in agreement.

- RED, unmodified head (sources untouched; only the new story present):
  the old-count assertion held, then:

  ```
  Error: expect(locator).toBeVisible() failed
    Locator: locator('hos-pos-parked .hos-dialog').getByText('PARK-1001')
    Expected: visible
    Timeout: 10000ms
    Error: element(s) not found
  1 failed
    test/sale.pw.ts:1851:1 › a park landing while the parked dialog is open converges the open dialog's list
  ```

  The open dialog never showed the just-parked ticket: the defect itself.

- GREEN with the fix: `1 passed (2.1s)`.
- Bite proof, the wiring temporarily lifted out of `refreshParkedCount`:
  red again at the same assertion (same tail as the RED above, same line).
- Wiring restored: `1 passed (2.1s)`.

## Gates (run in the worktree)

- `cd web/apps/counter-till && CI=true npm test`, under node 22 (the version
  CI pins): the node test leg answers `# pass 394 / # fail 0`, the Playwright
  stub suites answer `96 passed`.
- `../../node_modules/.bin/tsc --noEmit` from `web/apps/counter-till`: clean,
  no output.
- `bash scripts/check-scope.sh` from the repo root: `check-scope: OK`.

## Environment notes for the lead (nothing here is committed)

- Every Playwright run in this report executed with a temporary, never
  committed `server.watch.usePolling` override in the app's
  `vite.config.ts`, reverted before each commit (the committed tree carries
  no change to that file). Reason: this machine's per-user inotify instance
  budget is exhausted (this user account's processes hold 456 instances
  against a limit of 128), so every new `fs.watch` fails with EMFILE and the vite dev server
  cannot start. The override changes file watching only, nothing the suites
  exercise. Worth a host-level look: `fs.inotify.max_user_instances` sits at
  the kernel default while hundreds of instances are already held, so any
  lane that needs a fresh watcher on this machine will hit the same wall,
  and raising the limit needs a sudo password this lane does not hold.
- The repo's node test leg needs node 22 (the `_ts-js-loader.mjs` hook relies
  on native type stripping): the shell's default node 20 fails the leg with
  ERR_UNKNOWN_FILE_EXTENSION. The machine's `/home/colton/.local/node22/bin`
  matches CI's pin and was used for every run above.
- The in-progress blocker is recorded here instead of `docs/BLOCKERS.md` to
  keep the committed diff inside the brief's Touches; move it there if the
  record belongs in the repository.

## Deliberately left alone

- The acceptance spec, its `openParked` poll, and the stub: untouched per
  the brief (no character changed there).
- The reference checkout `ref-master/`: the ruling improves on it here, it
  is not edited there.
- The other read-once dialogs (customer picker, assess relief, return, till
  close): they were not in the defect, and widening would break the brief's
  size guide.
- No restyling, no extra features, no unrelated files in the diff.
