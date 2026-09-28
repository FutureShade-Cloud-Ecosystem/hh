# @hh/auth

The web auth package: session custody with the JWT in memory only, device
unlock for shared devices, and the header wiring into the api-client
(HH-4.2). Sign-in methods for the first release, confirmed: Google plus the
house sign-in through the Console bridge; device unlock is in scope.

The package has zero runtime dependencies and takes no npm dependency at
all: every collaborator arrives as a narrow injected interface. The
workspace's `Result` convention and `HeaderProvider` type are reused from
the api-client by type-level import (`src/client.ts` imports them from
`../api-client/src/result.ts` and `../api-client/src/http.ts`); the shell
wires the two packages together at run time, and the test suite imports the
real `ApiClient` to prove that wiring.

## Custody: the JWT lives in memory only

`SessionStore` (src/session.ts) holds the token in a module variable.
`adopt()` takes a full session handed over from out of band (the ERP's
`adoptSession` posture): a sign-in answer, a Google callback through the
Console bridge, or a reload's restored platform session. `adopt` returns
false and adopts nothing when the input is unusable.

### What a page reload may restore, stated plainly

The throw-safe mirror (sessionStorage-backed) holds exactly two things: the
short-lived refresh handle (an opaque, adapter-defined value; the Appwrite
session secret is the natural fit) and the display profile it belongs to
(user id, email, name), with the handle's expiry. It never holds the JWT.
An expired handle is dropped at load, never surfaced. `clear()` (sign-out,
or a new identity without a handle) leaves nothing: a new identity inherits
nobody's handle.

When the storage accessor is broken or denied, the mirror degrades to a
memory map and the session simply does not survive a reload, which is the
honest behaviour for that posture. The degradation is observable:
`store.degraded` (and `lock.degraded`) is the flag the shell reads. The
Node test process has no sessionStorage and therefore runs degraded, which
keeps the degraded path exercised on every `npm test`.

### The throw-safe mirror's provenance

The mirror carries the ERP storage shim's rationale from
`hardscapeos_dibbits/app/src/api.ts` (READ ONLY reference): browsers set to
deny site data make the sessionStorage accessor itself throw (SecurityError),
which used to kill the bundle at import time; the shim degrades to memory
instead. Some browsers throw on access of the accessor, others on use, so
the mirror probes a read, a write and a remove before trusting any backing,
every write is itself throw-safe (quota and serialisation failures report
false and throw nothing), and reads are validated rather than cast, so a
corrupt or hand-edited payload is dropped, never trusted.

## Device unlock for shared devices

`DeviceLock` (src/lock.ts) locks a cached session behind the platform's user
verification, through WebAuthn where the platform exposes it. The PIN and
the biometric are the platform prompt's user verification: the platform
collects the factor, and this package never holds a typed PIN at all.

- `engage(userId)` registers the platform credential (one create ceremony)
  and writes the engaged lock to the mirror: who it protects, which
  credential unlocks it, when it was engaged. Refused as an error Result
  where the platform offers no credential (WebAuthn absent); the shell then
  falls back to not caching a lockable session.
- `unlock()` runs one verification against the registered credential id and
  records who unlocked and when. Every failure (wrong PIN, cancelled
  prompt, platform refusal) feeds a local exponential backoff (in memory,
  one second after the first failure, doubling, capped at one minute) that
  refuses further attempts until it elapses; a success resets it.
- The engaged lock survives a reload through the same throw-safe mirror
  discipline as the session. An open unlock does not survive a reload: a
  reload re-locks, which is also why the in-memory backoff resetting on
  reload is not a bypass. `lock()` re-engages; `disengage()` removes the
  lock from memory and mirror.
- `auditHeaders()` returns the audit pair, `X-Unlocked-By` (the unlocked
  user) and `X-Unlocked-At` (the unlock instant, unix epoch milliseconds),
  exactly while a lock is open and empty otherwise. The header provider
  stamps it onto every request made under the unlock.

The WebAuthn ceremony's point here is local user verification; it is a
device-local unlock, not a server-authenticated factor. The assertion is
not sent anywhere, and the JWT and the server remain the authority.

## The wiring into the api-client

`authHeaderProvider({ sessions, lock, activeOrg })` (src/client.ts) builds
the header provider the shell passes to `new ApiClient({ headers })`:

- `Authorization: Bearer <token>` from memory, only while a live
  (unexpired) session exists;
- `X-Org` from the shell's active organisation. A request without an active
  organisation is refused before it leaves: the provider throws naming the
  missing header, and the transport converts a provider throw into an error
  Result without dialing, so the refusal is data with no status;
- the audit pair exactly while a lock is open;
- nothing else, and there is deliberately no role input to configure.

### No role picker

This package exposes no role state at all: no role field, no role header,
no role-based branch. Roles are the server's. The rule is HH Pro's,
carried with its provenance:
`/home/colton/Desktop/hh_pro_dibbits/src/core/supplier/port.ts` lines 71 to
77: a role code is the account's own, never re-expressed as another
product's role vocabulary, because shipping a role picker the server cannot
enforce is the post-M8 bug class that spec's R-4 exists to prevent. A gate
in `test/gates.test.mjs` fails if an exported name ever carries role state,
and a scan fails if the package ever mints a foreign X- header.

## The adapter seams (the one integration point a consuming shell wires)

- `AccountSurface` (src/client.ts) is the Appwrite account surface this
  package needs: `createEmailPasswordSession`, `createOAuth2Session`,
  `getSession`, `createJWT`, `deleteSession`. The Appwrite Web SDK is the
  one dependency real login cannot avoid, so it lives behind this seam in
  the consuming shell's adapter. This package takes no npm dependency on
  the SDK and vendors no fake of it.
- `CredentialProvider` (src/lock.ts) is the WebAuthn surface: `isAvailable`,
  `create`, `verify`. `webAuthnCredentialProvider()` is the platform adapter
  a shell wires.
- `StorageLike` (src/session.ts) is the mirror's backing, injectable for
  tests.

## Tests, gates, and the fake values

    npm test        # from web/packages/auth, on Node 22
    npm run typecheck

The suite runs on Node 22's native TypeScript type stripping with the same
minimal resolve hook the api-client carries
(src/_ts-js-loader.mjs). The typecheck is `tsc --noEmit`, strict, with the
api-client's tsconfig shape; `typescript` and `@types/node` are the pinned
devDependencies, reusing the web workspace lockfile.

`src/testing.ts` is the test entry point (never exported from src/index.ts,
so it never rides a browser bundle): `FakeCredentialProvider`,
`FakeAccountSurface`, `RecordedStorage`, `ThrowingStorage`, and the fake
values. Every value these doubles mint is obviously fake and documented as
fake where it is defined: `FAKE_JWT` (`FAKE.JWT.NOT-A-REAL-TOKEN`), the
fake PIN, the fake password, `fake.user@example.invalid`. No real
credential, token, or secret exists in any fixture.

The gates, as tests:

- `test/custody.test.mjs`: the token lives in memory and the mirror carries
  the handle and the profile, never the token; the reload story both ways;
  expiry and corrupt-payload handling; the mirror degrades observably on a
  throwing accessor and a degraded reload restores nothing.
- `test/unlock.test.mjs`: the unlock flow with the fake credential
  provider, including the wrong-PIN path, the cancelled-prompt path, the
  exponential rate limiter (growth, cap, reset), the mirror discipline
  across a reload, and the audit headers present exactly while open.
- `test/client.test.mjs`: the wiring proven against the real `ApiClient`
  with a recording fetch: the exact header set in each posture, the
  headerless refusal with zero fetches, the controller paths, and the
  no-role surface.
- `test/gates.test.mjs`: the structural scans (no localStorage, no
  document.cookie, no indexedDB anywhere in the sources; sessionStorage
  touched only by the mirror; only the three owned X- headers) and the
  runtime capture proving no code path writes a token, the PIN, or a
  password into storage.
