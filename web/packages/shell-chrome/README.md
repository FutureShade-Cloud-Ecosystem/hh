# @hardscapehouse/shell-chrome

The identical shell chrome every audience shell shares: org, user, app
switcher, sync state and notifications, per context section 4.5. The shells
under `tauri/` mount this chrome and load role-allowed micro-apps; the shell is
never the security boundary, the core gates every endpoint by role.

The chrome is the shell direction (decision log row 1520): ONE simple
header band holding the app and surface selector, the pin control, the org,
the user, a sync state chip and notifications; and a COLLAPSED left rail
holding the person's OWN pinned surfaces across every micro app they hold,
the ones they use most. It is never a navigation tree: the selector's panel
is the whole map, the rail is the shortlist the person chose. Pins are kept
per user, so two users on one device never share them.

Ported and polished from the original HardscapeOS at master 87d7941b, read
only: `app/src/surface-switcher.ts` (the raised selector and its grouped
panel), `app/src/app-launcher.ts` (the menu keyboard), `app/src/unit-rail.ts`
(the pinned rail, its empty state, its keyboard), `app/src/_rail-state.ts`
(collapsed is the default posture). The words of the sync states, the offline
banner and the update prompt are the FB Design r4 Field shell frame's
(`hh-field-apps/build_r4_shell.py`, `build_r4_offline.py`, with the r6 voice
sweep's "Sign in expired"). The substrate is the house convention, not the
original's: Lit through the design system's `HHElement`, tokens only, host
delegation on `data-action` names.

## Layout

- `src/shell-state.ts`: the pure state, no DOM and no imports. The per user
  pin book over an injected `PinStorage` seam (add, remove, reorder, cap;
  an unpin keeps its place so a re-pin restores it; a corrupt payload reads
  as empty; a throwing seam is not a fault), the rail's collapsed posture
  (`railCollapsedFromStored`: silence means collapsed, the stored '0' of an
  explicit expand keeps winning), the app switcher model by role over an
  injected manifest slice (never a copy), the current surface's key and
  breadcrumb words, `initialsOf` for the avatar chip, and the sync chip's
  words (`syncChipView`).
- `src/shell-header.ts`: `hh-shell-header`, the band. Mark and org, the
  selector control and its panel (every app the roles hold, its surfaces,
  the current marked in words), the pin control (a pressed toggle, disabled
  without a current surface), the sync state chip, the notifications bell
  with its count, and the account chip. Under 500px of the band's own width
  the chip and the org word drop (the r4 phone frame's rule: the banner
  carries that state); phone and tablet widths raise the targets to 44 px.
- `src/shell-rail.ts`: `hh-shell-rail`, the pinned rail. Collapsed by
  default: a 56 px strip of icons whose labels ride as real-text tooltips;
  expanded on demand: the 196 px rail with the words on. The pins only. The
  D-107 empty state (a calm sentence naming pin and selector) is the first
  thing a new person sees. Collapsed, the rows are one roving tab stop
  (arrows move, Home and End jump); every row is a 44 px target at every
  size.
- `src/shell-banner.ts`: `hh-shell-offline-banner`, the strip under the band.
  Four modes with the board's words: `offline` (with the outbox depth in
  words and in a count chip), `unreachable`, `read-only` (a second tab owns
  the sync), `auth-expired` (the work is safe, a Sign in action rides a
  `data-action="sign-in"`). No mode renders nothing at all.
- `src/shell-update.ts`: `hh-shell-update-prompt`, the r4 updater card.
  Quiet until `open`; Update now calls the injected `ShellUpdater` seam
  (the Tauri updater is wired by the workspace item, never fetched here),
  Later emits and closes.
- `demo/`: the committed demo page over a fake manifest (the demo tenant's
  Field world: Yard Pick and Receive, HH Drive, House Messages for the yard
  manager), every state the package paints, phone 390 and tablet 834, in the
  brand mode default and the light build. `npm run build:demo` rebuilds the
  pages and the element sheets (held byte-fresh by the suite);
  `SHELL_DEMO_PORT=<free port in 56200 to 56299> npm run demo` serves them
  and refuses an occupied port. The page carries no client scripts: a shell
  integrates the chrome in code, which is the wiring section below.

## Wiring a shell (the contract the tests hold)

```js
import { PinBook, currentBreadcrumb, switcherGroups, surfaceKey, syncChipView } from '@hardscapehouse/shell-chrome';

// the seams the shell injects; the browser copy uses localStorage, the
// Tauri shells their own store
const storage = {
  read: (key) => localStorage.getItem(key),
  write: (key, value) => localStorage.setItem(key, value),
};
const pins = new PinBook({ storage });  // keys pins under pinStorageKey(userId); a blank user id is refused
rail.storage = storage;                 // the rail's collapsed posture seam

// the model, over the manifest slice the shell injects
header.groups = switcherGroups({ frontends, roles: session.roles, surfaces });
header.breadcrumb = currentBreadcrumb({ frontends, surfaces, current });

// the chip, bound to the sync runner and the outbox health
header.syncState = runnerState;         // the statechart's state
header.cards = openCardCount;
header.waiting = health.waiting;
header.attempts = health.stuck?.attempts ?? 0;   // one attempt never alarms
header.stalls = health.stalls.length;            // a stall shows at once

// the events, on the host
header.addEventListener('hh-shell-surface-open', (e) => open(e.detail));
header.addEventListener('hh-shell-pin', (e) => {
  e.detail.pinned ? pins.add(userId, e.detail.key) : pins.remove(userId, e.detail.key);
});
rail.addEventListener('hh-shell-rail-select', (e) => openSurfaceByKey(e.detail.key));
rail.addEventListener('hh-shell-rail-toggle', () => {});
banner.addEventListener('click', (e) => { /* data-action="sign-in" */ });
update.updater = tauriUpdater;          // install(); Later needs no seam
update.addEventListener('hh-shell-update-later', () => hide());
```

The elements listen for clicks and keys on themselves and read the control
off `event.composedPath()` (the design system's delegation pattern); no
listener is ever spelled in a template. The sync chip's words come from
`syncChipView`, so a shell never re-derives them; the view's `alarm` is true
only from the second attempt on a stuck write, and a stall is named in words
the moment it exists.

## What is deliberately not here

- Sign in, the account surface, and the outbox stall's own settle UI belong
  to the identity item; the banner carries the auth-expired words and the
  Sign in action, and the chip carries the stall's words.
- The manifest slice is injected, never imported: the package holds no
  manifest copy, and the role to apps map is `appsForRoles`' answer over
  whatever the shell hands it.
- No icon registry: the rail's glyph is a short mark the shell renders until
  the ERP icon registry lands behind the design system's icon seam.
- The sync state list is pinned by the package's suite to the sync
  statechart's own states (`statecharts/sync/machine.ts`), so a state the
  machine grows and the chip misses turns this suite red.

## Tests

Node 22 (type stripping; this machine's Node 22 is `/snap/bin/node`).
Dependencies come from the workspace install at `web/`: `lit` at run time,
`@lit-labs/ssr`, `typescript` and `@types/node` beside it.

```
npm test          # node --import ./test/_ts-js-loader.mjs --test 'test/*.test.mjs'
npm run typecheck # strict tsc over the sources and the demo builder
npm run build:demo
```

The rendering tests ride Lit's server render the way the design system's
element tests do (the element instance is never connected; the template its
render() returns is the only thing that renders). The floors are source
scans in the original's house split: one name per row, every title matched
by an identical aria-label, tokens only (zero hex literals), the posture
resolved through the pure resolver, the toggle writing '1'/'0' under the one
key. No Playwright leg: the npm-test neighbours (auth, offline-store) run
none; the offline store's browser probe is a separate manual leg.

## For the fidelity pass

A visual check against the original app and the FB Design board is NOT done
by this package's author (GLM cannot view images). The demo page renders
everything to judge: the frame at tablet 834 (offline, three writes waiting)
and phone 390 (brand idle); the selector panel open; the sync chip in all
seven states plus the stuck alarm; the rail collapsed (phone), expanded, and
empty; the banner in its four modes; the update prompt. Both themes ship as
committed pages (`index.html` brand mode, `index-light.html` light). The
states and sizes listed here are the pass's checklist.
