# @hardscapehouse/auth

The web auth package: session custody with the JWT in memory only, a device
lock that is a screen courtesy, and the header wiring into the api-client
for staff and tenant callers (HH-4.2). Sign-in methods for the first
release: Google plus the house sign-in (email and password), both directly
against FB Console's HH project; device unlock is in scope. The bearer is
always the core's own session token, got by trading the signed-in FB Console
account at `POST /api/v1/auth/exchange`; an FB Console JWT is never the
bearer and is never stored.

The package root takes no dependency from any registry: every collaborator
arrives as a narrow injected interface. Its one dependency there is the workspace
sibling `@hardscapehouse/api-client` (declared in package.json, linked by the web
workspace, never installed from a registry): the `Result` convention
(`err`, `ok`, `Result`) and the `HeaderProvider` type are imported from
`@hardscapehouse/api-client` through its exports map, at run time and type level alike
(`src/client.ts` and `src/lock.ts` import them by the bare package name,
never by a relative path into the api-client's src, and a gate in
`test/gates.test.mjs` keeps it that way); the shell wires the two packages
together, and the test suite imports the real `ApiClient` the same way to
prove that wiring.

## Custody: the token lives in memory, and storage holds nothing

`SessionStore` (src/session.ts) holds the token in private memory. `adopt()`
takes a full session handed over from out of band (the ERP's `adoptSession`
posture): a sign-in answer, a Google callback through FB Console's OAuth
redirect, or a reload's resume through the issuer. The controller's own
paths adopt the core's exchange answer (token and expiry) and nothing else. `adopt` returns false and adopts
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
getSession, then the core's exchange for a fresh token into memory. In degraded mode the
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

**The ruling on the email in session storage, recorded:** Colton was put
three options on the one key (`hh-auth.session.v2`) holding the user id,
email and name: A, leave it; B, keep only the name and the user id; C,
store nothing. His word in chat was "A" (relay message 047, Colton's
ruling on the email in session storage). The display profile stays in
session storage as built for the first release; no token and no secret ride
with it, so the exit line "no token or secret is written to persistent
browser storage" stays met as written. It is revisited together with
per-person unlock, at the same trigger as the lock's own ruling: before
real staff share tablets in a yard. The four-field rule has two halves,
each with a test that bites: unknown fields are stripped on read (a
payload that validates but carries extra fields is rewritten to the four
known ones), and no field other than the four known ones (v, userId,
email, name) is ever written.

### Sign-out goes through signOut()

The shell must sign out through `AuthController.signOut()`, never by
calling `SessionStore.clear()` directly. Three counters live in the
controller. The generation counter cancels a resume in flight (and is
bumped by every successful adoption, so of two overlapping resumes with no
sign-out between them only the first adoption lands). The sign-out counter
cancels an explicit sign-in: only a sign-out bumps it, on both sides of
its own await, because a sign-in is the person's own act. An adoption that
lands first (a resume, an out-of-band callback) does not cancel it, and a
double submit of the sign-in form does not resolve an error while the
person is signed in. The sign-in sequence counter supersedes one sign-in
with another: every submit bumps it, and a sign-in that is no longer the
newest adopts nothing, minting nothing further, and waits for the newest
to settle before it resolves (the exact rule in the paragraph below it). A
direct `sessions.clear()` empties the custody but
bumps neither counter: a resume that is already running is not cancelled,
and when the platform answers it adopts a token into a custody the user
asked to sign out. `signOut()` ends the platform session, bumps all three
counters on the two sides of that await (the generation twice, the sign-out
counter twice; the sequence counter is never touched by it), and wipes
custody; the wired lock is disengaged beside that wipe, under the same
guards, so the lock drops with the session (the ruling on the lock at
sign-out, in the device unlock section below); a failed resume clears the
custody it failed from and the wired lock with it, because a lock belongs
to the signed-in person or to nobody. The deferred-fake tests in
`test/client.test.mjs` hold that
ordering against a resume and a sign-in begun before and during the
sign-out, a resume that adopts first, a double submit in both landing
orders, the two people's
and three sign-ins in flight, a sign-out landing between the two, and the
two adoption bumps.

The sign-in sequence, stated exactly: every submit bumps the sequence
counter, and a sign-in that is no longer the newest adopts nothing and
mints nothing further. It does not resolve straight away: it WAITS for the
newest sign-in to settle, then resolves by what custody holds. The same
person signed in resolves success, so a double submit of one sign-in lands
both submits in either landing order (the first submit's answer landing
first, the usual order, and the second's landing first) and neither promise
of the two resolves an error while the person is signed in. A different
person, or nobody, resolves superseded, and the screen says so, because of
two people's sign-ins in flight the older one's late answer must never
replace the newer person's session on a shared tablet. A sign-out outranks
the supersession: a sign-in parked under a sign-out resolves cancelled,
never superseded, without waiting.

What is not verified about the double submit, and what the code does in
that case: what the real platform does with a SECOND session for one person
is unknown. If the platform refuses the second session while the first is
active, the newest submit resolves `sign-in failed`, the older submit
(waiting for it) resolves superseded with custody holding nobody, and the
person ends signed out with both promises answered. The code does not retry
and holds nothing session shaped of its own to fall back to: the platform's
session is the issuer's to own, and this package keeps only what custody
adopted.

The wiring that lets the lock see the sign-out: the shell hands the
controller the device lock (as `lock`, a narrow two method handle) and the
shared `SignOutCounter` (as `signOuts`), and hands the same counter
instance to the lock. The sign-out counter is the code's way of telling a
sign-out from the bound user's own token moving underneath: a sign-out is
an act, counted by the counter, and an expiry is not. A controller
constructed without a lock disengages nothing (a shell may run with no
lock at all), and a lock constructed without a counter sees no sign-out
and governs its prompts by the expiry rule alone.

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
  its awaits: another person's sign-in while the platform's credential
  prompt is open leaves the custody holding a different user, and the
  engage refuses instead of storing the lock for the person who is no
  longer signed in. The engaged user's own token moving underneath is not a
  change of person: a token refreshed or expired during the prompt leaves
  the custody holding either no session at all or the same user's fresh
  session, and both are accepted. A sign-out during the prompt is refused
  outright (the engage stores nothing): the sign-out counter moved, which
  is the ruling below.
- `unlock()` runs one verification against the registered credential id.
  Every failure (wrong PIN, cancelled prompt, platform refusal) feeds a
  local exponential backoff (in memory, one second after the first failure,
  doubling, capped at one minute) that refuses further attempts until it
  elapses; a success resets it. The backoff lives in memory, so a reload
  clears it and re-locks: it is a courtesy against fat fingers, not a
  control, and the platform authenticator's own lockout is the real
  limiter. The custody is read again after the prompt, on the success path
  and the failure path alike: a session for a different user (another
  person's sign-in while the platform prompt was open) refuses the attempt,
  and a failed attempt under that session records no backoff, instead of
  opening the lock for, or recording a failure against, the person who now
  is signed in. The bound user's own token moving underneath is not a
  refusal: a token refreshed or expired mid prompt leaves the custody
  holding no session at all or the same user's fresh session, and both are
  accepted. A sign-out during the prompt refuses the attempt on both paths
  (nothing opens, no backoff is recorded): the sign-out counter moved, the
  lock itself is already disengaged, and the screen shows the signed-out
  state.
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

**The ruling on the lock, recorded:** Colton was put two options, the lock
as built with per-person unlock as its own later item, or per-person unlock
required before release, and his words were "A is fine" (relay message 038,
Colton's ruling on the tablet lock). The lock ships as built for the first
release: it proves that someone who could unlock the tablet was present; it
does not prove which HH person that was, and the core verifies nothing
about it. The trigger that changes this is recorded with the ruling:
per-person unlock is required before real staff share tablets in a yard,
the point where knowing which person acted matters (on approvals or price
overrides, for example). Until then the lock is a courtesy. Per-person
unlock is not dropped: a credential per person (not one device credential
named "device-unlock"), a challenge made by the server, the assertion and
its user-verification flag verified on the server, and the server stamping
the acting user on the audit entry from its own verified evidence. Anything
the page stamps itself can be rewritten by any script in the page.

**The ruling on the lock at sign-out, recorded (the question the record
above left open):** Colton was put three options on the fact that at main
the lock survived sign-out and a change of user: A, leave it; B, drop the
lock at sign-out; C, drop it at sign-out AND when a different user signs
in. His word in chat was "C" (relay message 048, Colton's ruling on the
lock at sign-out). The rule staff are told, in one sentence: **the lock is
yours while you are signed in; a lock belongs to the signed-in person, or
to nobody.** What it means in the package:

- `signOut()` disengages the wired lock beside the custody it clears, under
  the same guards, so a sign-out that fails part way (the platform call
  throwing) still leaves no lock behind.
- Every successful adoption (a sign-in, an out-of-band `adopt()`, a
  resume) reconciles the lock with the user it adopted: a lock bound to
  somebody else is disengaged before the new session is shown, and the
  same user signing in again keeps their lock.
- A prompt that answers after its owner signed out stores nothing and
  opens nothing: engage stores nothing; unlock finds the lock already gone
  (sign-out disengaged it) and the screen shows the signed-out state. The
  code tells this apart from the bound user's own token refreshing or
  expiring during the prompt, which still lands, because a sign-out is an
  act, counted by the sign-out counter, and an expiry is not: engage and
  unlock capture the shared counter's count before they await the platform
  and refuse when it has moved, even in the window where the sign-out's own
  await is still pending and the custody is not yet cleared.
- A lock found at start-up with no session (a tab reopened after the
  session ended) is cleared, not shown: the display profile survives a
  reload and dies at a sign-out, so a lock in the mirror whose profile is
  gone, or names somebody else, is a lock whose session ended.

This replaces the accept-when-there-is-no-session behaviour for the
sign-out-ended case only; the acceptance of an absent custody remains the
expiry-shaped case, where no act moved anything. The nine tests that hold
the ruling live in `test/lock-signout.test.mjs`, each stating what is in
storage afterwards and what the person sees, each proven able to fail by
its own mutation.

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

## The exchange, the refresh and the adapter seams (the one integration point a consuming host wires)

- `AccountSurface` (src/client.ts) is the FB Console account surface this
  package needs: `createEmailPasswordSession`, `createOAuth2Session`,
  `getSession`, `exchange(org)` and `deleteSession`. `exchange` returns the
  core's answer (`{token, expiresAtMs, user}`, the user block in the core's
  snake case) or a typed refusal code: `bad_request`, `unauthorized`,
  `forbidden`, `rate_limited`, `unavailable`, `offline`, `no_session`. It
  never throws, and the FB Console JWT it mints inside one attempt never
  crosses the seam: each attempt mints a fresh JWT, an adapter never holds
  or reuses one, and a core 401 on the first attempt is retried once with a
  new JWT inside the adapter before the caller ends the session.
- The sign-in and the resume ask `exchange(org)` for the active
  organisation (`AuthController` takes `activeOrg`) and adopt its token and
  expiry, with the display profile from the user block. The display profile
  is all that is kept of the user block: this package holds no role state.
- **The refresh.** While a session is live the controller re-exchanges
  before `expiresAtMs`: `refreshLeadMs` before it (a minute by default,
  never more than half the window), on the injected `now` and `timers` so
  tests do not wait. A new token is adopted. `unauthorized`, `forbidden` or
  `no_session` ends the session through the same path as `signOut()` (the
  platform session is deleted, custody and the lock are cleared); so does an
  exchange that answers for a different person. `rate_limited`,
  `unavailable`, `bad_request`, `offline` and an adapter that throws keep the
  session and the current token, and try again after a doubling backoff
  (`refreshBackoffMs`, capped). Offline never ends a session. A sign-out
  that lands while a refresh is in flight wins: the late answer adopts
  nothing and arms nothing. A controller built without `activeOrg` neither
  signs in nor refreshes (the demo apps adopt a mint and hold nothing to
  refresh).
- **The browser adapter** is `createAppwriteAccountSurface({endpoint,
  projectId, coreBaseUrl, fetch?})` from the separate entry
  `@hardscapehouse/auth/appwrite-adapter` (`src/appwrite-adapter.ts`). It is
  plain REST over `fetch`, no SDK and no npm dependency: `POST
  /account/sessions/email`, `POST /account/jwts`, `GET /account`, `DELETE
  /account/sessions/current` under the configured endpoint, each with the
  `X-Appwrite-Project` header, `credentials: "include"` (the session cookie
  is FB Console's own, httpOnly and first party on the futurebuild.ai host)
  and `redirect: "error"`; Google navigates (through an injectable
  `navigate`) to the console's OAuth route with absolute success and failure
  URLs that must be on the page's own origin. The package root never imports
  the adapter. The Tauri shells forward `exchange` to their Rust
  `auth_exchange` command instead (`shared/AUTH-SHELL-CONTRACT.md`).
  The adapter was first built on the Appwrite web SDK and moved off it
  because that SDK copies a session fallback into local storage
  (`cookieFallback`) whenever FB Console sends `X-Fallback-Cookies`, which
  breaks the custody rule above. Now the adapter never reads that response
  header, never sets it on a request, and touches no storage of any kind.
- `CredentialProvider` (src/lock.ts) is the WebAuthn surface: `isAvailable`,
  `create`, `verify`. `webAuthnCredentialProvider()` is the platform adapter
  a shell wires.
- `StorageLike` (src/session.ts) is the mirror's backing, injectable for
  tests.

## Public surface

The package declares an exports map: `.` is `src/index.ts`, `./testing`
is `src/testing.ts` and `./appwrite-adapter` is `src/appwrite-adapter.ts`.
Nothing else is importable, so no caller deep-imports
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
`FakeCredentialProvider`, `FakeAccountSurface` (its `script()` queues
exchange refusals), `DeferredAccountSurface`, `FakeScheduler` (the fake
clock and timers the refresh tests run on), `fakeExchangeAnswer`,
`fakeExchangeRefusal`, `RecordedStorage`, `ThrowingStorage`, and the fake
values. Every value these doubles mint is
obviously fake and documented as fake where it is defined: `FAKE_JWT`
(`FAKE.JWT.NOT-A-REAL-TOKEN`), the fake PIN, the fake password,
`fake.user@example.invalid`. No real credential, token, or secret exists in
any fixture.

The gates, as tests:

- `test/exchange.test.mjs`: the controller adopts the exchange answer as the
  bearer and writes no token to storage; the refresh timing, the backoff,
  the refusals that end a session and those that never do, and a sign-out
  that wins a refresh in flight, all on the fake clock.
- `test/appwrite-adapter.test.mjs`: a fake fetch asserts the exact requests
  (method, path, headers, credentials, no secret in a URL), storage doubles
  throw on any write, a response carrying `X-Fallback-Cookies` is ignored,
  a fresh JWT per attempt, the one retry on a core 401, every refusal code,
  the JWT never leaving the adapter, same-origin OAuth redirects, and the
  package root and the adapter free of any SDK.

- `test/custody.test.mjs`: the token lives in memory and the mirror carries
  the display profile and nothing else; nothing in storage is
  bearer-capable; the store serialises to nothing secret and its token is
  not a run-time property; the reload story; expiry and unusable-input
  handling, with an unusable storage payload dropped from storage and not
  merely unread; unknown fields stripped on read and, the write side of the
  same rule (message 047), no field other than the four known ones ever
  written; the mirror degrades observably on a throwing accessor and
  a degraded reload restores nothing.
- `test/lock-signout.test.mjs`: the nine tests of the lock ruling at
  sign-out (relay message 048): a lock then a sign-out; a lock, a sign-out,
  another user signs in; a lock, another user signs in over a live session;
  a lock, the same user signs in again; a sign-out during the engage
  prompt; a sign-out during the unlock prompt; the token refreshed during
  the prompt; the token expired during the prompt; a stale lock at
  start-up. Each states what is in storage afterwards and what the person
  sees.
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
  including the reload's resume through the issuer, the sign-in's two
  guards (the sign-out counter and the sign-in sequence: two people's
  sign-ins in flight in both landing orders, three in flight, the double
  submit, a sign-out during two sign-ins, and the sign-out counter's bump
  before the await), and the no-role surface.
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
