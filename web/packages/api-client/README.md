# @hh/api-client

Generated from `core/api/openapi.yaml` (authored by lane HH-2.3 before any core
code exists). Never hand-edit generated output; regenerate from the document
with, from the repository root:

    node scripts/gen-client/gen-client.mjs            # emit the committed tree
    node scripts/gen-client/gen-client.mjs --out DIR  # emit into DIR (gate scratch mode)

The generator reads the contract and emits `src/gen/`: every wire type the
document defines (one TypeScript type per `components/schemas` entry), the
manifest the package's gates read (`manifest.ts`), and one typed operation
function per documented operation, named with the contract operationId and
grouped one namespace per document tag (`index.ts` re-exports each as, for
example, `tenantOrders`). Output is deterministic: run twice, byte-identical.
The YAML dependency is the contract tools' own (`core/api/tools/package.json`),
so the generator needs its install first: `npm --prefix core/api/tools ci`.

## Layout

- `src/gen/` is generated, entire. Every file starts with the generated
  marker; nothing else in the package defines a wire type. The generated
  entry carries the wire types, the manifest, and the operations; it does not
  carry the descriptors (see `src/testing.ts`).
- `src/gen/descriptors.ts` is the structural descriptor per wire type: the
  data the test-time interpreter validates captures against. It is test-only
  data (312 KB of it), so it sits behind the test entry point `src/testing.ts`
  and never rides the public entry into a browser bundle.
- `src/testing.ts` is that test entry point: tests and gates import the
  descriptors through it; runtime callers never need it.
- `src/result.ts` is the Result convention ported from HH Pro
  (`hh_pro_dibbits/src/core/lib/result.ts`; the provenance comment names it).
  Every operation resolves `Result<T>` instead of throwing. An error Result
  carries `status` when the refusal is an HTTP status (a 4xx or 5xx): the
  status the server answered. A transport failure, an aborted or timed-out
  request, a failed header provider, and a malformed success body carry no
  status, because either nothing answered or what answered answered success.
- `src/http.ts` is the transport: an injectable `FetchLike` (default binding
  to the platform's global fetch), a base URL, and a caller-set header
  provider. It owns no identity: no token storage, no credential handling;
  those belong to HH-4.2. The contract's shared `X-Org` header is a
  caller-set header exactly because of that: a staff caller sets it once on
  the client, for example `new ApiClient({ baseUrl, headers: () => ({ "X-Org": org }) })`.
  A header provider that throws or rejects resolves an error Result; it never
  surfaces as a rejected promise.
- `src/pagination.ts` is the cursor page walker. A cursor is the previous
  page's `next_cursor` verbatim; the walk ends at `next_cursor: null`. The
  yard pending-tickets feed's position cursor (see
  `core/api/CONVENTIONS.md`, Pagination) is honored as data: passed through
  opaquely, never inspected or re-derived. The walker is cancellable: pass an
  `AbortSignal` as `walkPages`' `signal` option and the walk resolves an
  error Result naming the page it stopped at, fetching nothing further; the
  same signal rides to every page fetch.
- `src/index.ts` re-exports the public surface.
- `test/` holds the fixture contract tests and the gates.

## Types

- Path parameters are typed `string | number` (the wire spelling is text
  either way); the transport converts with `String()` when it builds the
  request URL, and refuses a missing, empty, or dot-segment (`.` or `..`)
  value as an error Result, dialing nothing.
- A tag whose namespace would collide with a reserved word is renamed with an
  `Api` suffix: the document's `public` tag is generated as `publicApi`. The
  full tag to namespace mapping is generated as `TAG_NAMESPACES` in
  `manifest.ts`; nothing re-derives it.
- A `oneOf` (or `anyOf`) whose legs are all `$ref` legs generates the real
  union of the named types, with the matching union validator in the
  descriptors: `MessagingMessage.ref` is
  `MessagingEventRef | MessagingApprovalRef`, and the two finstmt report
  routes resolve
  `FinStmtIncomeStatementReport | FinStmtComparativeRows` and
  `FinStmtBalanceSheetReport | FinStmtComparativeRows`. Any other unmapped
  `oneOf`/`anyOf` fails generation naming the schema, so a silently weakened
  `unknown` cannot appear.
- A property the document marks `deprecated: true` carries a `@deprecated`
  note on the emitted type (the legacy offset fields `total` and `offset` on
  the tenant page envelopes, kept because the recorded captures still carry
  them).
- The int64 bound: the contract's money and sequence fields, 454 `*_cents`
  properties and 8 `seq` properties (462 in total), are declared integer
  (`int64`) in the document and typed `number` here. TypeScript's `number` is
  an IEEE 754 double, exact for integers up to 2^53; that is
  9,007,199,254,740,992 cents, about 90 trillion dollars, far above any
  figure the dealer modules move, so every value these fields carry is exact.

## Event streams

`messagingStream` (GET /api/v1/messaging/stream, the server-sent event feed)
is deliberately NOT generated as an operation function; the manifest census
marks its route `excluded`. Why: this client is fetch-based, and an operation
function resolves one `Result`, which cannot represent an open stream.
Typing it `Promise<Result<string>>` would silently read the first body chunk
and call it the value, which is a lie about what the route is. The caller
mints a short-lived ticket with the generated `messagingMintStreamTicket`
(or presents its Bearer credential) and opens the stream with an
`EventSource` in the shell, where the event loop lives; the document's own
reconciliation guidance (poll `GET /api/v1/messaging/unread` after a drop)
stays with that caller.

## Request bodies and responses

An `application/json` request body is typed from the document and sent as
JSON; a `multipart/form-data` body is typed from its schema and sent as
`FormData`. The three raw uploads the document declares `format: binary`
(`productPutImage`, `dispatchUploadStopPhoto`, `pricingImportBatch`) accept
`string | Uint8Array` and ride the wire verbatim with the first declared
content type.

The success type is the lowest declared 2xx response. Where that response's
content is plain string formats (`text/csv`, `text/html`) the operation
resolves the raw response text; where it is `format: binary` (the images and
the `.xlsx` workbooks) the operation resolves the raw bytes as `Uint8Array`,
read from the wire as bytes, never decoded as text. Any 4xx or 5xx resolves
`{ ok: false, error, status }` with the contract's Error envelope message
verbatim, and so does a transport failure: an operation never throws on a
refusal.

Every request carries an optional `call` argument (`{ signal, timeoutMs }`):
the signal aborts the request, the timeout aborts it after the given
milliseconds, and either resolves an error Result naming which one fired.

## Tests and gates

    npm test        # from web/packages/api-client
    npm run typecheck

The suite runs on Node 22's native TypeScript type stripping. Package sources
import siblings with the runtime-correct `.js` extension, so the test process
registers `src/_ts-js-loader.mjs`, a minimal `--import` resolve hook following
the ERP app's loader pattern, which rewrites a relative `.js` specifier to
`.ts` when that file exists.

The runtime has zero dependencies. The one devDependency, `typescript`
(pinned), serves the typecheck gate only: `npm run typecheck` runs
`tsc --noEmit`, strict, over the whole package, and the repository CI runs it
for the web packages on every pull request.

The gates, as tests:

- `test/fixtures-live.test.mjs` replays every vendored live capture in
  `core/api/fixtures-live/` through the operation it belongs to with a
  recorded FetchLike (no network), asserting the request the client builds
  and that the response parses and structurally validates against the
  operation's own generated success schema: the schema name comes from the
  generated manifest's `SUCCESS_SCHEMAS` table keyed by the replayed
  operationId, never a test-picked schema.
- `test/result-shape.test.mjs` proves the Result shape: every generated
  operation's signature returns `Promise<Result<...>>`, and every refusal
  path (4xx, 5xx, transport error, malformed JSON, unusable path parameter,
  failed header provider, abort, timeout) resolves
  `{ ok: false, error }` and never throws or rejects, with the status riding
  the HTTP-status refusals.
- `test/binary.test.mjs` proves the binary story byte for byte: a
  `format: binary` success resolves exactly the bytes the wire carried (the
  fixtures are a real PNG signature and IHDR and a real ZIP local file
  header), and the three binary-bodied uploads send exactly the bytes the
  caller held.
- `test/pagination.test.mjs` walks a synthetic multi-page cursor sequence,
  proves termination at the documented end condition, surfaces the
  position cursor opaquely, and proves the walk cancellable by signal.
- `test/gen-scan.test.mjs` is the no-hand-written-wire-type scan: every file
  under `src/gen` carries the generated marker, and no type named by the
  document's schemas is defined outside `src/gen`.
- `test/idempotence.test.mjs` regenerates into scratch directories and
  byte-compares against the committed tree and between runs.

The vendored captures are the D22 set: byte for byte what the live server
said, with tokens redacted. A capture that does not validate against the
schema the contract assigns to its route is a finding, never a schema to
loosen.
