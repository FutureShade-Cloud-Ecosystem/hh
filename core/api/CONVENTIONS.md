# Conventions for core/api fragments

Every fragment under `core/api/fragments/` is a YAML file with exactly two
top-level keys, `paths` and `components`, transcribed from one or more
HardscapeOS ERP modules. `core/api/tools/merge.mjs` deep-merges the skeleton
and every fragment into `core/api/openapi.yaml`. These rules are what make
the merge safe and the document uniform.

## Absolute rules (house rules, context section 0)

- No em dashes or en dashes anywhere, including descriptions and summaries.
  Use commas, colons, or parentheses.
- No dates, no durations, no calendar references in prose. Schema field names
  such as `valid_until`, `created_at` or `issue_date` are vocabulary and are
  fine; prose must not state or imply a specific date or duration.
- No secrets. Never copy a credential, token value, or connection string out
  of the source repositories.

## Source discipline

- Transcribe, never invent. Every path in a fragment must come from a literal
  `"<METHOD> /api/..."` route registration in
  `hardscapeos_dibbits/backend/internal/<module>/` (non-test Go files). Keep
  the path string byte-identical, including path parameter names
  (`{customerID}`, `{lineNo}` stay exactly so).
- Schemas come from the module's `model.go` (and the handler request and
  response structs in the module's other files). JSON tags become property
  names verbatim. A Go field with `omitempty` is optional; without it,
  required. Pointer types become nullable: `type: [string, "null"]` style
  (OpenAPI 3.1 type arrays, never the 3.0 `nullable:` keyword).
- If a route exists but its request or response body is not representable
  from the structs (raw bytes, streaming), say so in the description and use
  `type: string, format: binary` or a note; never drop the route.

## Type mapping

| Go | OpenAPI |
|---|---|
| `string` | `type: string` |
| id columns (uuid) | `type: string, format: uuid` |
| `time.Time` | `type: string, format: date-time` |
| `int`, `int32` | `type: integer, format: int32` |
| `int64`, cents fields | `type: integer, format: int64` |
| `float64` | `type: number` |
| `bool` | `type: boolean` |
| `[]T` | `type: array, items: $ref or inline` |
| `map[string]any`, `json.RawMessage` | `type: object, additionalProperties: true` |
| embedded struct | flatten its fields, or a named schema plus `$ref` |

Money is integer cents in `*_cents` fields, format int64. Enum values use the
database CHECK vocabularies verbatim (uppercase, for example `DRAFT`,
`CONFIRMED`); the inventory at
`hh_pro_dibbits/analysis/hardscapeos-inventory.md` section 8.1 lists them.

## Operation shape

Every operation object carries:

- `operationId`: camelCase, unique across the whole document. Prefix it with
  the module name when the verb alone would collide (`posCommitSale`,
  `tillOpenSession`).
- `tags`: exactly one entry, the tag assigned to the fragment.
- `summary`: one line, no period required.
- `x-role`: the gate, one of:
  - `requireStaff`, `requireManager`, `requireAdmin`: the identity guards,
    resolved from the `RegisterRoutes` call site in
    `hardscapeos_dibbits/backend/cmd/server/main.go`. Where a module takes
    guards under other names (`readGuard`, `writeGuard`, `adminGuard`), the
    call site shows which identity guard each one receives. Where a route is
    additionally wrapped by an internal guard (`requireBranch`,
    `requirePicked`), keep `x-role` as the identity gate and list the extra
    guards in `x-guards`.
  - `operator`: a platform operator acting through the launcher's
    operator-console section (D4 endpoints only).
  - `tenant`: any authenticated tenant user (the portal's own `h.authn`).
  - `capability:<name>`: portal capability gates (tenant fragment only).
  - `scope-member`: sync surface; membership in the scope's declared roles,
    enforced by the core from the manifest.
  - `public`: pre-auth routes (for example under `/api/public/v1/`). These
    get `security: []` and, when credentialed by a path token, a description
    saying so.
- `x-guards`: optional array of internal guard names applied on top of
  `x-role`.
- parameters: every staff operation (any route under `/api/v1/` that is not
  `public`) takes the shared org header:
  `- $ref: "#/components/parameters/XOrg"`. Path parameters are declared
  individually with types from the handler's parsing (`format: uuid` where
  the handler parses a UUID, plain string otherwise).
- `responses`: the success response with a real schema; plus `401`, `403`
  always, `404` when the route reads by id, `400` when the route takes a
  body, `409` where the handler returns one, each as
  `- $ref: "#/components/responses/<Name>"` with a key of the status string.
  The error envelope is `Error` (`{error: string}`), already in the skeleton.

## Pagination

Every list endpoint uses cursor pagination, replacing the ERP's offset pages:

- request parameters: `- $ref: "#/components/parameters/Cursor"` and
  `- $ref: "#/components/parameters/Limit"`, plus any filters the handler
  actually parses (status, customer id, date range) as optional query
  parameters with enums where the vocabulary is a CHECK set.
- response envelope: a named schema `<Prefix><Thing>Page` with
  `items` (array, required), `next_cursor` (`type: [string, "null"]`),
  `limit` (`type: integer, format: int32`). Never `offset`, never `total`:
  cursor pagination replaces offset on every list.

One endpoint's cursor is a position token rather than a row key, recorded
here as its convention difference: `GET /api/v1/tasks/yard/pending`
(the yard pending-tickets feed) is an in-memory merge of two dispatch
reads whose resume token carries the merge position, not a row key,
opaque under the shared v1 encoding; malformed and negative positions are
refused. The feed's own design ("a merge cannot be offset at the
database") leaves no row key the seam could filter on without crossing
module lines; if conformance ever wants a row key there, that is a seam
redesign with its own review, ordered above both lanes.

One deliberate difference is recorded here rather than transcribed: the ERP
handler for `GET /api/v1/audit/transaction/{entityType}/{entityId}`
(`audit/handler.go`, the `reopen` read path) returns the trail as a bare
JSON array with no envelope. The contract serves it as `AuditEntryPage`
with `cursor` and `limit` like every other list, because an unbounded array
breaks the pagination rule and gives the client no resume token; the
trail's causal sort key is `seq`, so a cursor over it is well defined.

Out-of-range limits are a second recorded divergence, pinned here by ruling of
the HH-3.4 Tech Lead review (change neither side): the document's `Limit`
parameter (`core/api/skeleton.yaml`) declares `minimum: 1, maximum: 200,
default: 50`, while the core's cursor helpers (Lane A's
`core/internal/platform/httpx` `ParseCursorPage`, `DefaultPageLimit` 50 and
`MaxPageLimit` 200) read an unparseable or non-positive `limit` as the default
and clamp an over-maximum value to the maximum, fail-quiet, carried from the
ERP's offset family. The declared minimum and maximum are client-facing
bounds, and the page envelope's `limit` echo is the caller's way to observe
the effective value. A malformed cursor is refused with 400; an out-of-range
limit is not. If strictness (refusal) is ever wanted instead, it is a
deliberate helper change with its own review and its own decision record,
ordered above both lanes; it must never arrive as a silent edit inside a
module batch.

## Naming

- Schema names are PascalCase and carry the fragment's assigned prefix
  (for example the dispatch fragment names everything `Dispatch*`). A name
  without the prefix is a merge collision with another fragment.
- Shared skeleton schemas (`Error`, `OrderCard`, `DealerQuote`,
  `CustomerQuote`, `QuoteLine`, `InvoiceView`, `Attachment`,
  `AttachmentLink`, `ProductImage`, `TenantBrand`, `Sync*`) exist already;
  `$ref` them where the module's wire shape is that entity, and do not
  redefine them.

## What a fragment file looks like

```yaml
paths:
  /api/v1/example/things:
    get:
      operationId: exampleListThings
      tags: [staff-example]
      summary: List things
      x-role: requireStaff
      parameters:
        - $ref: "#/components/parameters/XOrg"
        - $ref: "#/components/parameters/Cursor"
        - $ref: "#/components/parameters/Limit"
      responses:
        "200":
          description: The page of things
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ExampleThingPage"
        "401": { $ref: "#/components/responses/Unauthorized" }
        "403": { $ref: "#/components/responses/Forbidden" }
components:
  schemas:
    ExampleThing:
      type: object
      required: [id]
      properties:
        id: { type: string, format: uuid }
    ExampleThingPage:
      type: object
      required: [items]
      properties:
        items:
          type: array
          items: { $ref: "#/components/schemas/ExampleThing" }
        next_cursor: { type: [string, "null"] }
        limit: { type: integer, format: int32 }
```
