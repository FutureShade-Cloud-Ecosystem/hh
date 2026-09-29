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
  surfaces as a rejected promise. A header value the platform would refuse
  (a control character, a bare CR or LF) is refused by the transport before
  anything dials, and the error names the header, never its value: header
  values are where tokens travel, and the platform's own refusal message
  echoes the value. Redirects are refused (`redirect: "error"`): a redirect
  could replay a request's headers and body against another origin, so a
  redirected request resolves a transport error Result instead of following.
  The dial is raced against the armed deadline and abort, so a custom
  FetchLike that ignores its signal still resolves an error Result at the
  deadline (see "What a custom FetchLike must honour" below), and a caller's
  multipart file name is guarded like a path value: refused as an error
  Result before anything dials when it is empty, a dot segment, path-like,
  control carrying, bidirectional control carrying (the whole
  `\p{Bidi_Control}` class), invisible format or control character carrying
  (`\p{Cf}` and `\p{Cc}`, the two allowed joiners U+200C and U+200D
  excepted), carrying a line separator (U+2028, U+2029) or a Hangul filler
  (U+115F, U+1160, U+3164) inside the name, only spaces, edged with any
  white space or a dot, or over 160 UTF-8 bytes. Every refusal names what it
  refused in words a person understands: contains a tab, ends with a line
  break, contains a hidden character (U+00AD).
- `src/pagination.ts` is the cursor page walker. A cursor is the previous
  page's `next_cursor` verbatim; the walk ends at `next_cursor: null`. The
  yard pending-tickets feed's position cursor (see
  `core/api/CONVENTIONS.md`, Pagination) is honored as data: passed through
  opaquely, never inspected or re-derived. The walker is cancellable: pass an
  `AbortSignal` as `walkPages`' `signal` option and the walk resolves an
  error Result naming the page it stopped at, fetching nothing further; the
  same signal rides to every page fetch.
- `src/index.ts` re-exports the public surface. The package's `main` and the
  exports map's `.` point at it, and the exports map carries one further
  subpath, `./testing`, for the descriptors; everything else is unreachable
  by import, so no caller deep-imports generated internals.
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
- The int64 bound: the contract declares 547 int64 fields
  (`grep -c 'format: int64' core/api/openapi.yaml`), and every one of them is
  typed `number` here. The count is the whole contract, not only the money
  and sequence fields: a walk of the YAML finds 537 object properties (454
  named `*_cents`, 6 named `*seq`, and 77 others such as `size_bytes` and the
  `*_bps` rates), 7 parameters, 1 array item, and 2 `additionalProperties`.
  TypeScript's `number` is an IEEE 754 double, exact for integers up to 2^53;
  that is 9,007,199,254,740,992, far above any figure the dealer modules
  move, so every value these fields carry is exact, and the bound is stated
  for all 547.

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
`FormData`, with a `format: binary` field typed `Blob` so the part arrives as
the caller's bytes, byte for byte. A multipart operation with a binary field
takes a filename option per field, so the caller names the part:
`pricingApplyPriceFile(client, form, { fileFilename: "price-list.xlsx" })`,
`feedbackCreateMultipart(client, form, { imageFilename: "board-frozen.png" })`.
A caller's name is guarded like a path value: a name that is empty, is one of
the dot segments (`.` or `..`), carries a path separator (`/` or `\`),
carries a tab, carries a line break, carries a NUL or any other control
character, carries a Unicode bidirectional
control character, carries an invisible formatting character, carries a line
separator (U+2028 or U+2029) or a Hangul filler (U+115F, U+1160, U+3164)
inside the name, is nothing but spaces, begins or
ends with any white space or a dot, or is longer than 160 UTF-8 bytes
resolves an error Result before anything dials. The refused classes, named:
the whole Unicode bidirectional control class, the binary property
`\p{Bidi_Control}` (twelve characters: U+061C, U+200E, U+200F, U+202A
through U+202E, and U+2066 through U+2069, the marks that reorder how text
displays, so a stored name can be made to read as something it is not); the
format characters, `\p{Cf}` (the zero width marks, the soft hyphen, and the
byte order mark among them), with the two allowed joiners excepted; and the
control characters, `\p{Cc}` (the C0 range and DEL that the per-character
loop refuses first, plus the C1 range U+0080 to U+009F). The two exceptions
are U+200C ZERO WIDTH NON-JOINER and U+200D ZERO WIDTH JOINER: they are
`\p{Cf}`, but they join letters (Persian words are spelled with the
non-joiner) and build emoji (the family and profession sequences are joiner
chains), and they hide nothing, so they are allowed where every other
format character is refused; a rule that refuses ordinary names gets
switched off. The allowance is between visible characters only: a name of
nothing but joiners is invisible and refused ("is nothing but invisible
joiners"), and a joiner at either edge is refused ("begins with an
invisible joiner", "ends with an invisible joiner"), because it would
defeat the edge rule: a trailing dot or a leading space would show as the
name's edge with the joiner hiding beside it. The line separators are
refused inside a name as well as at
its edges (the edge rule would catch them alone), and so are the Hangul
fillers: they are letters, not format characters and not white space, so no
other rule sees them, and a name of nothing but U+115F is invisible. Every
refusal NAMES what it refused, in words a person understands: "this name
contains a tab", "this name ends with a line break", "this name contains a
hidden character (U+00AD)", the code point included for everything
invisible, so a person who pasted from a spreadsheet or a document can fix
the name instead of guessing at a class. What is refused that a person
might reasonably meet, as a list:

- the three subdivision flags built from tag characters (England, Scotland,
  Wales): every tag character is `\p{Cf}`;
- a direction mark pasted with Arabic or Hebrew text (LRM U+200E, RLM
  U+200F, and their isolates): `\p{Bidi_Control}`;
- a name ending in a dot ("Acme Inc."): the edge rule, the way a Windows
  reader would strip it.

The edge rule is any white space or a dot at
either edge, not the ASCII space alone: the core's sanitizer trims edge
white space off the stored name with TrimSpace, a Unicode trim, so an
accepted NBSP, ideographic space, or line separator at an edge would arrive
as a name the core stores changed, and a Windows reader strips edge spaces
and a trailing dot while a leading dot hides the file on a Unix-like reader,
so the name would not stay the name the caller said. Interior white space
and interior dot runs are kept whole. 160 is the bound the core's own
stored name keeps
(`core/internal/pricing/pricefile_service.go`, `sanitizeFileName`, lines 845
to 861), and the core's cap counts BYTES (`strings.Builder.Len()` after each
`WriteRune`), so this guard counts bytes too, and a name that passes here is
one the core can store whole. The count was code points once: a name of 160
three-byte CJK characters (480 bytes) passed here and the core cut it to 54
characters. The bidirectional marks, the format characters (every one but
the two allowed joiners, and a name of nothing but the joiners, or a joiner
at an edge: the allowance is between visible characters only), the line
separators, and the Hangul fillers are refused
here though the core still accepts them: its sanitizer strips only the C0
controls and DEL, so the gap on the core's side is recorded for Colton
(nothing unsafe follows in the core
today, since no path, object key, or header is built from the stored name
anywhere, but a stored name carrying a mark can display as something it is
not). A name outside ASCII rides
as UTF-8 bytes inside the quoted `filename` parameter, which is the HTML
standard's own multipart encoding; a receiver that still decodes the
parameter as ISO-8859-1 (the older RFCs' default, `busboy`'s included) shows
it mangled. The client does not add the RFC 5987 `filename*` form, because
the platform's `FormData` owns the part headers; a caller that needs a
guaranteed round trip can keep its names to ASCII. A Blob named by no option
rides as `blob`, the fetch standard's default part name (Node 22 and the
browsers; bun's deviates and sends an empty name), so that is what the core
stores when the caller says nothing. When an operation
declares both `application/json` and `multipart/form-data` (that is
`feedbackCreate`), the client emits one function per declared type: the first
declared keeps the operationId, and the multipart form takes a `Multipart`
suffix (`feedbackCreateMultipart`, with the screenshot as an optional `Blob`).
The mapping is emitted as `BODY_VARIANTS` in the manifest.

The three raw uploads the document declares `format: binary`
(`productPutImage`, `dispatchUploadStopPhoto`, `pricingImportBatch`) accept
`string | Uint8Array` and ride the wire verbatim. Each takes an optional
`contentType` argument typed as the union of the operation's declared content
types: `pricingImportBatch` declares only `text/csv`, so that is the default;
the two image uploads declare `image/jpeg`, `image/png` and `image/webp`, and
there the caller must say, because the core checks the declared type against
the file's first bytes and stores the declared type on the row. With no
`contentType` the transport itself sets no Content-Type header; what the wire
carries then depends on the body the caller held, because the platform's
fetch supplies its own default for a string body: bytes (`Uint8Array`) go out
with no `Content-Type` header at all, while a string body goes out with the
fetch standard's default, `text/plain;charset=UTF-8` (Node 22's undici and
the browsers alike). Bun diverges for a string body: it sends no
Content-Type header at all where Node 22 and the browsers send the
text/plain default, so the same unlabeled string upload is labeled differently
by the runtime the caller builds for. Neither default is ever an image type,
so an unsaid image upload is still refused by the core, never mislabeled: a
PNG is never sent as a JPEG.

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
Both stay armed until the response body has been read: a fetch resolves at
the headers, and a body that stalls after them is still a pending request, so
an abort or a deadline that expired at the headers would miss it.

## What a custom FetchLike must honour

The transport is injectable (`new ApiClient({ baseUrl, fetch })`), and a
caller such as auth passes its own. The deadline, the caller's abort and the
redirect refusal all rest on the replacement honouring two fields of the
`RequestInitLike` it receives:

- `init.signal`: pass it to the underlying fetch, so a deadline or an abort
  actually cuts the connection and rejects a pending body read. The client
  also races the fetch promise against the armed deadline and abort, so a
  transport that ignores its signal and never settles still resolves an
  error Result at the deadline instead of hanging the caller; but the race
  cannot cut the connection, only the signal can. A transport whose response
  body ignores the signal can still strand a body read past the deadline.
- `init.redirect`: it is always `"error"`, and a replacement must refuse a
  redirect the way the platform's fetch does under that setting. The client
  cannot enforce this one: it sees only the response it is handed, and the
  `ResponseLike` contract carries no final URL to check, so a replacement
  that follows redirects silently defeats the replay refusal.

The client enforces what it can (the deadline race) and states the rest; a
replacement that honours both fields behaves like the platform's fetch for
every gate this package tests.

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
  operation function's signature returns `Promise<Result<...>>`, and every
  refusal path (4xx, 5xx, transport error, malformed JSON, unusable path
  parameter, failed header provider, refused header value, abort, timeout)
  resolves `{ ok: false, error }` and never throws or rejects, with the
  status riding the HTTP-status refusals; it also replays the two plain
  string formats (`text/csv`, `text/html`) and checks each declared body
  variant against the manifest.
- `test/binary.test.mjs` proves the binary story byte for byte: a
  `format: binary` success resolves exactly the bytes the wire carried (the
  fixtures are a real PNG signature and IHDR and a real ZIP local file
  header), the three binary-bodied uploads send exactly the bytes the caller
  held with the content type the caller said, and the multipart binary
  fields (the price file and the feedback screenshot) arrive byte for byte
  through a real multipart parse of the emitted body. The caller's file name
  is guarded there too: every refusal class (empty, dot segments, either
  path separator, a tab, a line break, NUL, control characters, the whole
  bidirectional control class `\p{Bidi_Control}`, the format characters
  `\p{Cf}` with the two allowed joiners excepted and named, the C1 controls
  the per-character loop names by code point, an interior line separator,
  the Hangul fillers, a name of nothing but U+115F, only spaces, an edge of
  any white space or a dot, past the 160 byte bound) resolves an
  error Result with the recorder empty, nothing dialed, each refusal naming
  the character it found, and a name of exactly the bound arrives whole;
  the byte bound itself is pinned on the multi-byte side, where 160 CJK characters (480 bytes) are refused and 53
  (159 bytes) arrive whole.
- `test/transport.test.mjs` is the one place the suite dials, and it dials
  only a loopback server bound for the duration of one test: a body that
  stalls after its headers still lands the timeout and the caller's abort, a
  redirect is refused, never followed (cross origin and same origin alike,
  each on the receiving server's own request count), and a custom FetchLike
  that never settles is cut off by the deadline and by the caller's abort.
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

## History

A record that was once claimed and not written, written now. Report 015
said the executor's mutation-revert incident "is recorded in the as-built".
That sentence was untrue when written: the reviewer searched the pushed
tree, the records and infra and found no such record. The record exists
only from this pull request: while the carried auth and api-client work was
under mutation testing, the executor discarded its own uncommitted edits
with a careless checkout. The loss was caught immediately, the edits were
restored, and the full suite was re-verified before anything was committed.
Nothing pushed was ever in a broken state.
