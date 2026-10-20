# @hardscapehouse/work-shell

The Work shell host: the one page the HH Work profile (`tauri/hh-work`) opens,
plan B.4 and the shell UX of decision log row 1520.

- **Door:** no session, so the front door (`web/apps/front-door`, built at
  `/door/`) is mounted in a frame as the sign in and the role picker. Its screen
  is not rewritten; it mints a demo session into the tab's session storage and
  the host picks it up (a storage event, with a short poll as the fallback).
  Inside the Tauri shell the host first tries the auth package's controller over
  the four Rust commands (`src/console-session.ts`; the role comes from the
  exchange answer, since the auth package keeps no role state).
- **Desk:** the shell chrome around one hosted app: the header band (the app and
  surface selector from `profile.json` by the signed in role, the pin control,
  org, sync chip, notifications, the account chip with Switch role), the offline
  banner, the collapsed pinned rail. Pins are per person (`PinBook`), kept on
  the device, and are never touched by Switch role.
- **Landing:** `src/landing.ts`. A manager role (a catalog role whose id ends in
  `_manager`) lands on Manager Home when it holds it (D48, Q8); every other role
  lands on the first app of its catalog list that its switcher holds. The table
  there is a copy of `docs/analysis/role-catalog.yaml`'s `micro_apps` orders,
  pinned to the catalog by `scripts/work-landing.test.ts`.
- **Switcher:** `src/switcher.ts`. The profile's apps that have a bundle
  (`launch.json`), filtered by the role with the chrome's own `appsForRoles`.
  The server's gates stay the boundary.
- **Frames:** each desk app is a complete bundle at its own base and registers
  the design system's elements under the same names, so apps cannot share a
  document; the host runs each in a frame (`frame-src 'self'` in the Work CSP).
  Each app reads the front door's handoff key once and drops it, so the host
  writes it again before it loads each app (`src/handoff.ts`). An app that ends
  its own walk with `location.assign("/")` loads the host inside the frame; a
  framed host mounts nothing and asks its parent to switch role
  (`src/frames.ts`).

## Tests

```
npm test            # the node suite, then the Playwright legs
npm run typecheck
```

The Playwright legs serve a composed directory (`test/support/compose.mjs`:
the host, the front door at `/door/`, `profile.json`, `launch.json` and one
fixture page per desk app) behind a stand-in for the demo mint, on a port the
system picks. With `WS_DIST=<an assembled dist>` (`bash
tauri/hh-work/assemble-dist.sh`) the same server serves the real bundles, holds
their `/api/` calls (there is no core), and a second spec checks each app of two
roles' switchers loads its real bundle.

## For the fidelity pass

The framed mode exists (GAPS-r2 row G1): a desk app standing alone renders the
board's white band (the shell chrome's band variant) above its app-local
header, and framed by this host it wears no band of its own, so the shell's
band is the one band on screen. The fixture pages of the composed dist carry
no band logic: the host keeps serving the compact band for them.
