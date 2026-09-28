# @hh/api-client

Generated from `core/api/openapi.yaml` (authored by lane HH-2.3 before any core
code exists). Never hand-edit generated output; regenerate from the document
with, from the repository root:

    node scripts/gen-client/gen-client.mjs            # emit the committed tree
    node scripts/gen-client/gen-client.mjs --out DIR  # emit into DIR (gate scratch mode)

The generator reads the contract and emits `src/gen/`: every wire type the
document defines (one TypeScript type per `components/schemas` entry), a
structural descriptor per type (`descriptors.ts`), the manifest the package's
gates read (`manifest.ts`), and one typed operation function per documented
operation, named with the contract operationId and grouped one namespace per
document tag (`index.ts` re-exports each as, for example, `tenantOrders`).
Output is deterministic: run twice, byte-identical. The YAML dependency is the
contract tools' own (`core/api/tools/package.json`), so the generator needs
its install first: `npm --prefix core/api/tools ci`.

## Layout

- `src/gen/` is generated, entire. Every file starts with the generated
  marker; nothing else in the package defines a wire type.
- `src/result.ts` is the Result convention ported from HH Pro
  (`hh_pro_dibbits/src/core/lib/result.ts`; the provenance comment names it).
  Every operation resolves `Result<T>` instead of throwing.
- `src/http.ts` is the transport: an injectable `FetchLike` (default binding
  to the platform's global fetch), a base URL, and a caller-set header
  provider. It owns no identity: no token storage, no credential handling;
  those belong to HH-4.2. The contract's shared `X-Org` header is a
  caller-set header exactly because of that: a staff caller sets it once on
  the client, for example `new ApiClient({ baseUrl, headers: () => ({ "X-Org": org }) })`.
- `src/pagination.ts` is the cursor page walker. A cursor is the previous
  page's `next_cursor` verbatim; the walk ends at `next_cursor: null`. The
  yard pending-tickets feed's position cursor (see
  `core/api/CONVENTIONS.md`, Pagination) is honored as data: passed through
  opaquely, never inspected or re-derived.
- `src/index.ts` re-exports the public surface.
- `test/` holds the fixture contract tests and the gates.

## Request bodies and responses

An `application/json` request body is typed from the document and sent as
JSON; a `multipart/form-data` body is typed from its schema and sent as
`FormData`; the byte and CSV uploads (image types, `text/csv`) are typed
string and sent with the first declared content type. The success type is the
lowest declared 2xx response; where the document's success content is not JSON
(a document, a receipt, bytes) the operation resolves the raw response text.
Any 4xx or 5xx resolves `{ ok: false, error }` with the contract's Error
envelope message verbatim, and so does a transport failure: an operation never
throws on a refusal.

## Tests and gates

    npm test        # from web/packages/api-client

The suite runs on Node 22's native TypeScript type stripping. Package sources
import siblings with the runtime-correct `.js` extension, so the test process
registers `src/_ts-js-loader.mjs`, a minimal `--import` resolve hook following
the ERP app's loader pattern, which rewrites a relative `.js` specifier to
`.ts` when that file exists. Nothing here adds an npm dependency; the runtime
has zero.

The gates, as tests:

- `test/fixtures-live.test.mjs` replays every vendored live capture in
  `core/api/fixtures-live/` through the operation it belongs to with a
  recorded FetchLike (no network), asserting the request the client builds and
  that the response parses and structurally validates against the document's
  schema (via the generated descriptors).
- `test/result-shape.test.mjs` proves the Result shape: every generated
  operation's signature returns `Promise<Result<...>>`, and the refusal path
  (4xx, 5xx, transport error, malformed JSON) resolves
  `{ ok: false, error }` and never throws.
- `test/pagination.test.mjs` walks a synthetic multi-page cursor sequence,
  proves termination at the documented end condition, and surfaces the
  position cursor opaquely.
- `test/gen-scan.test.mjs` is the no-hand-written-wire-type scan: every file
  under `src/gen` carries the generated marker, and no type named by the
  document's schemas is defined outside `src/gen`.
- `test/idempotence.test.mjs` regenerates into scratch directories and
  byte-compares against the committed tree and between runs.

The vendored captures are the D22 set: byte for byte what the live server
said, with tokens redacted. A capture that does not validate against the
schema the contract assigns to its route is a finding, never a schema to
loosen.
