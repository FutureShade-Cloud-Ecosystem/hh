# @hh/auth

The web auth package: session custody with the JWT in memory only, a device
lock that is a screen courtesy, and the header wiring into the api-client
for staff and tenant callers (HH-4.2). Sign-in methods for the first
release, confirmed: Google plus the house sign-in through the Console
bridge; device unlock is in scope.

The package takes no dependency from any registry: every collaborator
arrives as a narrow injected interface. Its one dependency is the workspace
sibling `@hh/api-client` (declared in package.json, linked by the web
workspace, never installed from a registry): the `Result` convention
(`err`, `ok`, `Result`) and the `HeaderProvider` type are imported from
`@hh/api-client` through its exports map, at run time and type level alike
(`src/client.ts` and `src/lock.ts` import them by the bare package name,
never by a relative path into the api-client's src, and a gate in
`test/gates.test.mjs` keeps it that way); the shell wires the two packages
together, and the test suite imports the real `ApiClient` the same way to
prove that wiring.

## Custody: the token lives in memory, and storage holds nothing

`SessionStore` (src/session.ts) holds the token in private memory. `adopt()`
takes a full session handed over from out of band (the ERP's `adoptSession`
posture): a sign-in answer, a Google callback through the Console bridge, or
a reload's resume through the issuer. `adopt` returns false and adopts
nothing when the input is unusable.

**No credential of any kind goes into any browser storage, sessionStorage
included.** Not the token, not a refresh handle, not a session secret,
nothing bearer-capable in any shape. The package holds nothing a reload can
trade for a session.

### What a page reload may restore, stated plainly

The throw-safe mirror carries the display profile only: user id, email,
name. That is what a reload may show while it signs in again through the
issuer. After a reload there is no live session (`store.current` is null),
and the shell signs in again through the issuer: the platform's own session
is an httpOnly cookie script cannot read, so `AccountSurface.getSession()`
answers from the issuer while this package holds nothing to trade.
`AuthController.resume()` is that path and doubles as the token refresh:
getSession, then a fresh minted JWT into memory. In degraded mode the
mirror holds nothing that survives a reload. `clear()` (sign-out) leaves
nothing. A payload the constructor cannot validate is dropped from storage,
not merely hidden from view, so nothing credential-shaped survives under
the mirror's key; a payload that validates but carries fields this version
does not know (a leftover from an older version) is rewritten on read down
to the known fields.

When the storage accessor is broken or denied, the mirror degrades to a
memory map and the session simply does not survive a reload, which is the
honest behaviour for that posture. The degradation is observable:
`store.degraded` (and `lock.degraded`) is the flag the shell reads. The
Node test process has no browser storage and therefore runs degraded, which
keeps the degraded path exercised on every `npm test`.

### Sign-out goes through signOut()

The shell must sign out through `AuthController.signOut()`, never by
calling `SessionStore.clear()` directly. The generation counter that
cancels a sign-in or a resume in flight lives in the controller, so a
direct `sessions.clear()` empties the custody but does not bump it: a
resume that is already running is not cancelled, and when the platform
answers it adopts a token into a custody the user asked to sign out.
`signOut()` ends the platform session, bumps the generation on both sides
of that await, and wipes custody; the deferred-fake tests in
`test/client.test.mjs` hold that ordering against a resume and a sign-in
begun before and during the sign-out.

### The throw-safe mirror's provenance

The mirror carries the ERP storage shim's rationale from
`hardscapeos_dibbits/app/src/api.ts` (READ ONLY reference tree): browsers
set to deny site data make the storage accessor itself throw
(SecurityError), which used to kill the bundle at import time; the shim
degrades to memory instead. Some browsers throw on access of the accessor,
others on use, so the mirror probes a read, a write and a remove before
trusting any backing, every write is itself throw-safe (quota and
serialisation failures report false and throw nothing), and reads are
validated rather than cast, so a corrupt or hand-edited payload is dropped,
never trusted.

## Device unlock: a courtesy that hides the screen

`DeviceLock` (src/lock.ts) hides the screen behind the platform's user
verification, through WebAuthn where the platform exposes it. **The lock is
not a security boundary and this package never claims it is one.** The
token is presented for every request whether the lock is open, locked, or
absent, because the request must still work while the screen is hidden. The
PIN and the biometric are the platform prompt's user verification: the
platform collects the factor, and this package never holds a typed PIN at
all. The platform authenticator proves that someone who knows the device's
own lock was present; it does not prove which HH person that was.

- `engage()` takes no identity argument. It binds to the signed-in user's
  id, read from custody, and no name is stored anywhere: refused when no
  live session is held, when the platform offers no credential (WebAuthn
  absent; the shell then falls back to not caching a lockable session), and
  when a lock is already engaged. The custody is read again after each of
  its awaits: a sign-out and another person's sign-in while the platform's
  credential prompt is open leave the custody holding a different user, and
  the engage refuses instead of storing the lock for the person who is no
  longer signed in.
- `unlock()` runs one verification against the registered credential id.
  Every failure (wrong PIN, cancelled prompt, platform refusal) feeds a
  local exponential backoff (in memory, one second after the first failure,
  doubling, capped at one minute) that refuses further attempts until it
  elapses; a success resets it. The backoff lives in memory, so a reload
  clears it and re-locks: it is a courtesy against fat fingers, not a
  control, and the platform authenticator's own lockout is the real
  limiter. The custody is read again after the prompt too: a session that
  changed while the platform prompt was open (a sign-out and another
  person's sign-in) refuses the attempt instead of opening the lock for the
  person who now is signed in.
- An open unlock relocks itself when its open window (`openWindowMs`, a
  configuration value with the default 300000) elapses: an ABSOLUTE window
  measured from the unlock, not an idle timeout. No activity is tracked and
  nothing extends it; reading `state` after the window finds it locked
  again. The engaged lock survives a reload through the same throw-safe
  mirror discipline as the session; an open unlock does not survive a
  reload. `lock()` re-engages; `disengage()` removes the lock from memory
  and mirror.
- **No audit pair.** The `X-Unlocked-By` and `X-Unlocked-At` headers are
  removed until the core can verify an unlock. Nothing in `core/` reads
  that pair, the authz middleware checks only Authorization and X-Org, and
  a header that claims who unlocked, when nothing server side checks the
  claim, poisons the audit trail the moment anything trusts it. The
  repository's own analysis is right that the audit must record which
  person acknowledged; until an unlock is server verified, this package
  records no person at all.

**Open question for Colton, what a real per-person unlock needs:** a
credential per person (not one device credential named "device-unlock"), a
challenge made by the server, the assertion and its user-verification flag
verified on the server, and the server stamping the acting user on the
audit entry from its own verified evidence. Anything the page stamps
itself can be rewritten by any script in the page.

Disclosures about the WebAuthn adapter: no test runs a real browser
ceremony (the adapter's options are asserted against a stubbed
`navigator.credentials`, and the unlock flow runs against the fake
credential provider), and each engage leaves a discoverable credential on
the device that `disengage()` does not remove (the platform owns deletion
of WebAuthn credentials; the page cannot do it).

## The wiring into the api-client, per caller class

`authHeaderProvider(options)` (src/client.ts) builds the header provider
the shell passes to `new ApiClient({ headers })`. The core's authz
middleware (`core/internal/platform/authz/middleware.go`) requires X-Org on
staff routes (400 when missing) and refuses it on every tenant route and on
sync under a tenant bearer (400 when present), so the provider is built for
one caller class, and the type carries the difference:

- `callerClass: "staff"` with `activeOrg`: sends `X-Org` from the active
  organisation and `Authorization: Bearer <token>` from memory, only while
  a live (unexpired) session exists. A staff request without an active
  organisation is refused before it leaves: the provider throws naming the
  missing header, and the transport converts a provider throw into an error
  Result without dialing, so the refusal is data with no status.
- `callerClass: "tenant"`: sends `Authorization` only. The tenant options
  carry no organisation input at all, so the provider cannot mint the
  header the core refuses.

A gate in `test/gates.test.mjs` reads the middleware source and fails when
either rule moves, so the providers and the server cannot drift apart
silently. There is deliberately no role input to configure, and no unlock
audit pair.

### No role picker

This package exposes no role state at all: no role field, no role header,
no role-based branch. Roles are the server's. The rule is HH Pro's,
carried with its provenance: `src/core/supplier/port.ts` lines 71 to 77 in
the HH Pro reference tree (READ ONLY): a role code is the account's own,
never re-expressed as another product's role vocabulary, because shipping a
role picker the server cannot enforce is the post-M8 bug class that spec's
R-4 exists to prevent. A gate in `test/gates.test.mjs` fails if an exported
name ever carries role state, and a scan fails if the package ever mints a
foreign X- header (the only X- header it owns is X-Org).

## The adapter seams (the one integration point a consuming shell wires)

- `AccountSurface` (src/client.ts) is the Appwrite account surface this
  package needs: `createEmailPasswordSession`, `createOAuth2Session`,
  `getSession`, `createJWT`, `deleteSession`. The Appwrite Web SDK is the
  one dependency real login cannot avoid, so it lives behind this seam in
  the consuming shell's adapter. This package takes no npm dependency on
  the SDK and vendors no fake of it. `getSession` is the reload's
  re-sign-in seam.
- `CredentialProvider` (src/lock.ts) is the WebAuthn surface: `isAvailable`,
  `create`, `verify`. `webAuthnCredentialProvider()` is the platform adapter
  a shell wires.
- `StorageLike` (src/session.ts) is the mirror's backing, injectable for
  tests.

## Public surface

The package declares an exports map: `.` is `src/index.ts` and `./testing`
is `src/testing.ts`. Nothing else is importable, so no caller deep-imports
an internal; the test doubles ride the `./testing` subpath and never the
public entry.

## Tests, gates, and the fake values

    npm test        # from web/packages/auth, on Node 22
    npm run typecheck

The suite runs on Node 22's native TypeScript type stripping with the same
minimal resolve hook the api-client carries (test/_ts-js-loader.mjs; it is
test-only and lives outside src). The typecheck is `tsc --noEmit`, strict,
with the api-client's tsconfig shape; `typescript` and `@types/node` are
the pinned devDependencies, reusing the web workspace lockfile. CI runs
this package's typecheck and this suite on Node 22, alongside the
api-client's.

A correction of record about that CI: the merged auth pull request's
description said CI did not run, refused for billing. It ran. The `ci`
check executed on the merged head on GitHub's runner (run 36457289814) and
passed, every step, and the review's fresh clone rerun of the whole
workflow matched it step for step with the same counts. A merged pull
request's description stays as it was written, so this note is the
correction: the merged auth work landed on real CI, green.

`src/testing.ts` is the test entry point (behind the `./testing` subpath,
never exported from src/index.ts, so it never rides a browser bundle):
`FakeCredentialProvider`, `FakeAccountSurface`, `RecordedStorage`,
`ThrowingStorage`, and the fake values. Every value these doubles mint is
obviously fake and documented as fake where it is defined: `FAKE_JWT`
(`FAKE.JWT.NOT-A-REAL-TOKEN`), the fake PIN, the fake password,
`fake.user@example.invalid`. No real credential, token, or secret exists in
any fixture.

The gates, as tests:

- `test/custody.test.mjs`: the token lives in memory and the mirror carries
  the display profile and nothing else; nothing in storage is
  bearer-capable; the store serialises to nothing secret and its token is
  not a run-time property; the reload story; expiry and unusable-input
  handling, with an unusable storage payload dropped from storage and not
  merely unread; the mirror degrades observably on a throwing accessor and
  a degraded reload restores nothing.
- `test/unlock.test.mjs`: the unlock flow with the fake credential
  provider, engage bound to the signed-in user's id, the wrong-PIN path,
  the cancelled-prompt path, the exponential rate limiter (growth, cap,
  reset), the open-window relock against the injected clock, the mirror discipline
  across a reload, no audit pair anywhere, and the platform adapter's
  user-verification options asserted against a stubbed
  `navigator.credentials`.
- `test/client.test.mjs`: the wiring proven against the real `ApiClient`
  with a recording fetch: the staff header set (Authorization and X-Org,
  with the lock open or closed), the tenant header set (Authorization
  only), the headerless refusal with zero fetches, the controller paths
  including the reload's resume through the issuer, and the no-role
  surface.
- `test/gates.test.mjs`: the structural scans (no localStorage, no
  document.cookie, no indexedDB anywhere in the sources; the browser-session
  storage touched only by the mirror; only the owned X- header; no absolute
  workstation path in a source or the README), the middleware rule read
  from `core/internal/platform/authz/middleware.go`, and the runtime
  capture proving no code path writes a token, a PIN, a password, or a
  handle into storage, against an injected clock.

### What the structural scans cannot catch

The source scans are tripwires for the plain spellings, and they are cheap
and partial. Each of these passes the scan, and the scan is not a proof: a
computed property name (`globalThis["local" + "Storage"]`), an alias, a
storage object passed in as a parameter, the Cache API, `window.name`,
`history.replaceState`, or a write under another key inside the mirror's
own module.

The gate that counts is the run-time capture, and it works by swapping the
platform's storage globals for recording fakes for the length of the test:
`globalThis.sessionStorage` and `globalThis.localStorage` become recording
storages, and `document.cookie`, the Cache API, and `indexedDB` become
recording stubs. The sign-in, custody, and unlock flows then run through
default-constructed stores, the production resolution path, so a write to
the real global session storage reached through the swapped global at call
time, including one through a computed property name evaluated when the
write happens, lands in a recording fake and is checked, key by key and
value by value, with the clock injected so the fixture is live and the
capture is never vacuous. What even this capture cannot see: an alias of a
storage object taken before the swap (an alias captured at module import
time holds the real accessor, and a write through it lands outside every
recording fake with no gate the wiser), a storage object a caller injects
into the mirror (the shell vouches for its own backing), and anything
written after the test has restored the globals.
