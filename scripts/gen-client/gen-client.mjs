#!/usr/bin/env node
// Generate the web api-client from the assembled contract
// (core/api/openapi.yaml) into web/packages/api-client/src/gen/. Nothing else
// generates client code, and the generated tree is never hand-edited, so this
// is the one generator: one deliberate mapping per OpenAPI 3.1 feature the
// document uses, and a loud failure on any shape the mapping does not cover,
// so document drift can never pass silently as a silently weakened type.
//
// Modes: run from the repository root with no arguments, it reads the contract
// and emits the committed generated tree; --out DIR emits into DIR instead
// (the idempotence gate's scratch mode). Output is deterministic by
// construction: schemas in document order, paths in byte order, methods in
// document order within each path, tags in the document's tag order, no
// timestamp or environment value enters the output. Run twice, byte-identical.
//
// The mapping, pinned (OpenAPI 3.1 feature to TypeScript):
//   $ref to a sibling schema            the named type
//   type: string                        string (uuid, email, password, date,
//                                       date-time and binary formats included;
//                                       every format keeps the wire string)
//   type: integer / type: number        number
//   type: boolean                       boolean
//   type: null                          null
//   type: [T, "null"]                   T | null (any type array reduces to a
//                                       union of its legs)
//   type: array                         T[] over the items mapping, parenthesized
//                                       when the element is a union
//   type: object with properties        an object type literal; a property not
//                                       in required gets ?; nullable stays
//                                       T | null
//   type: object, no properties, with   { [key: string]: T } over a typed
//     additionalProperties              additionalProperties, else
//                                       { [key: string]: unknown }
//   type: object, additionalProperties  the literal (the closure is test-time
//     false, with properties            data, not a type difference)
//   object with neither properties nor  unknown
//     additionalProperties
//   allOf                               the legs' mapping intersected with &
//                                       (a true leg constrains nothing and is
//                                       skipped)
//   anyOf / oneOf: exactly one $ref     the named type | null (the document's
//     leg plus one null leg             nullable reference)
//   anyOf / oneOf whose legs are        the union of the named types, with the
//     all $ref legs (two or more)       matching union descriptor (a real
//                                       discriminated-by-shape union, for
//                                       example MessagingMessage.ref and the
//                                       two finstmt report unions); any other
//                                       oneOf / anyOf fails generation naming
//                                       the schema where it sits, never a
//                                       silently weakened unknown
//   any other unmapped shape, and the   unknown
//   empty schema
//   enum                                a union of the values as JSON literals
//   a string schema with format:        bytes: the success type is Uint8Array
//     binary on a success response      and the transport reads the body as
//                                       bytes (arrayBuffer), never text; the
//                                       plain string formats (text/csv,
//                                       text/html, text/event-stream) stay
//                                       the wire string
//   a string schema with format:        the raw request body accepts
//     binary on a request body          string | Uint8Array and rides the
//                                       wire verbatim; a raw body declaring
//                                       more than one content type takes a
//                                       contentType argument typed as the
//                                       union of the declared types, and
//                                       with more than one declared, no
//                                       contentType means no Content-Type
//                                       header at all (the caller must say;
//                                       the core checks the declared type
//                                       against the file's first bytes, so a
//                                       silently chosen type mislabels the
//                                       bytes)
//   a string schema with format:        Blob: a binary field of a multipart
//     binary on a typed position        form body is the caller's bytes
//                                       (a string field there would arrive
//                                       decoded and re-encoded, never byte
//                                       for byte); the operation takes a
//                                       filename option per binary field
//                                       (fileFilename for a file field) so
//                                       the caller names the part, and an
//                                       unnamed Blob rides as "blob", the
//                                       platform's default part name;
//                                       success responses and
//                                       raw bodies are covered by their own
//                                       mappings above, so this branch sees
//                                       the multipart file fields only
//   a request body declaring both       one generated function per declared
//     application/json and              content type: the first declared
//     multipart/form-data, both         keeps the operationId, the rest
//     mappable                          take the content type suffix (the
//                                       multipart form of feedbackCreate is
//                                       feedbackCreateMultipart); the
//                                       mapping is emitted as BODY_VARIANTS
//                                       in manifest.ts
//   deprecated: true on a property      a /** @deprecated */ note on the
//     of a named wire type              emitted property, carrying the
//                                       description as its text
//
// Tag namespaces: camel() folds a tag to one identifier, and a namespace that
// would collide with a reserved word is suffixed Api so the generated export
// stays legal (the document's "public" tag becomes publicApi). The full tag
// to namespace mapping is emitted as TAG_NAMESPACES in manifest.ts; nothing
// re-derives it.
//
// Event streams: an operation whose success is a live text/event-stream feed
// (the document's messagingStream) is NOT emitted as an operation function. A
// fetch-based call resolves one Result, which cannot represent an open stream;
// typing it Promise<Result<string>> would silently read only the first body
// chunk and call it the value. The route stays in the manifest census marked
// excluded (STREAM_OPERATIONS below); the caller mints a ticket with
// messagingMintStreamTicket and opens the stream with an EventSource in the
// shell. Documented in web/packages/api-client/README.md.
//
// Operation functions: one per documented operation, named with the contract's
// operationId verbatim, grouped one file per tag. Each takes the ApiClient,
// then the path parameters in template order (every path parameter is typed
// string | number, and the transport converts it with String() when it builds
// the request URL: the wire spelling is text either way, and a missing
// parameter stays visible to the transport, which refuses it as a Result), then the JSON body (when the operation carries
// one), then the raw upload's contentType when the body declares more than
// one content type (typed as the union of the declared types), then an opts
// object typed from the query parameters, then an optional call object
// ({ signal, timeoutMs }) handed to the transport. Every function
// returns Promise<Result<success type>>: the success schema is the lowest
// declared 2xx response; a response whose content is not JSON resolves the raw
// text (plain string formats) or the bytes (format: binary). The contract's
// caller-set headers (the shared org header X-Org, and the return route's
// approver credential X-Approver-Authorization) are deliberately not operation
// parameters: they arrive through the transport's caller-set header provider
// (see web/packages/api-client/README.md).
//
// Request bodies: when application/json is among the declared content types it
// is typed from its schema and sent as JSON, and when multipart/form-data is
// declared alongside it the operation gets one function per declared type,
// the first keeping the operationId and the multipart one a Multipart suffix
// (BODY_VARIANTS in manifest.ts records the mapping); a lone multipart body is
// typed from its schema and the transport serializes it as FormData (a
// format: binary field is typed Blob and rides as one part, byte for byte);
// otherwise, when every declared content type carries a type: string schema
// (the binary and CSV uploads), the body is typed string, or
// string | Uint8Array when the schemas say format: binary, and the
// contentType argument says which declared type rides the wire: defaulted to
// the declared type when exactly one is declared, sent as no Content-Type at
// all when several are declared and the caller does not say. Anything else
// fails generation loudly.
//
// Alongside the types and operations the generator emits descriptors.ts (a
// structural descriptor per wire type, the data a test-time interpreter
// validates captures against, mirroring the contract tools' fixture check
// with formats unchecked) and manifest.ts (the schema name list, the route
// table, the tag namespace mapping, and the per-operation success schema
// name table the fixture gate validates captures against: an operation's own
// declared success type, never a test-picked one).
//
// YAML parsing uses the yaml package from the contract tools' own dependency
// set (core/api/tools/package.json, the lockfile ci.yml installs); this
// generator adds no package of its own.
import { createRequire } from "node:module";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const genDir = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(genDir, "..", "..");
const outArg = (() => {
  const i = process.argv.indexOf("--out");
  if (i === -1) return null;
  const value = process.argv[i + 1];
  if (!value) {
    console.error("--out needs a directory argument");
    process.exit(1);
  }
  return resolve(process.cwd(), value);
})();
const outDir = outArg ?? join(repoRoot, "web", "packages", "api-client", "src", "gen");

const requireTools = createRequire(join(repoRoot, "core", "api", "tools", "package.json"));
let YAML;
try {
  YAML = requireTools("yaml");
} catch {
  console.error("the yaml package is not installed; install the contract tools' dependencies first: npm --prefix core/api/tools ci");
  process.exit(1);
}

const MARKER = "// Generated by scripts/gen-client/gen-client.mjs from core/api/openapi.yaml. DO NOT EDIT.";
const HTTP_METHODS = ["get", "put", "post", "delete", "patch"];
// Live event-stream operations: excluded from the generated surface, kept in
// the manifest census marked excluded. See the header comment and the README.
const STREAM_OPERATIONS = new Set(["messagingStream"]);

const doc = YAML.parse(readFileSync(join(repoRoot, "core", "api", "openapi.yaml"), "utf8"));
const schemas = doc.components?.schemas ?? {};
const sharedParams = doc.components?.parameters ?? {};
const tagNames = doc.tags?.map((t) => t.name) ?? [];
const problems = [];

const refName = (s) => {
  const m = /^#\/components\/schemas\/(.+)$/.exec(s?.$ref ?? "");
  if (!m) return null;
  if (!schemas[m[1]]) problems.push(`unknown $ref target ${s.$ref}`);
  return m[1];
};

const isNullLeg = (leg) =>
  leg?.type === "null" || (Array.isArray(leg?.type) && leg.type.length > 0 && leg.type.every((x) => x === "null"));

// ident decides whether a property or parameter name can sit bare in a type
// literal or an argument list; anything else is quoted.
const IDENT = /^[A-Za-z_$][A-Za-z0-9_$]*$/;
const key = (name) => (IDENT.test(name) ? name : JSON.stringify(name));

const dedupe = (parts) => [...new Set(parts)];

// tsType maps one schema to its TypeScript spelling (the pinned mapping).
function tsType(s, where) {
  if (s === true || s === null || s === undefined) return "unknown";
  if (typeof s !== "object") {
    problems.push(`${where}: non-schema node ${JSON.stringify(s)}`);
    return "unknown";
  }
  const ref = refName(s);
  if (ref) return ref;
  if (s.oneOf || s.anyOf) {
    const legs = s.oneOf ?? s.anyOf;
    const refs = legs.map(refName).filter(Boolean);
    const nullLegs = legs.filter(isNullLeg);
    if (legs.length === 2 && refs.length === 1 && nullLegs.length === 1) return `${refs[0]} | null`;
    if (legs.length >= 2 && refs.length === legs.length) return dedupe(refs).join(" | ");
    problems.push(`${where}: oneOf/anyOf the mapping does not cover (legs: ${legs.map((l) => JSON.stringify(l?.$ref ?? l?.type ?? l)).join(", ")})`);
    return "unknown";
  }
  if (s.allOf) {
    const parts = [];
    for (const leg of s.allOf) {
      if (leg === true) continue; // a true leg constrains nothing
      const t = tsType(leg, `${where}&`);
      if (t !== "unknown") parts.push(t);
    }
    const joined = dedupe(parts).join(" & ");
    return joined || "unknown";
  }
  if (s.enum) return dedupe(s.enum.map((v) => JSON.stringify(v))).join(" | ");
  const legs = Array.isArray(s.type) ? s.type : [s.type];
  return dedupe(legs.map((leg) => (leg === "null" ? "null" : scalarTs(leg, s, where)))).join(" | ");
}

// wrapArray parenthesizes a union or intersection element type.
const wrapArray = (elem) => (/\s/.test(elem) ? `(${elem})[]` : `${elem}[]`);

// scalarTs maps one non-null leg of a type union (the other keys live on the
// shared schema object s).
function scalarTs(leg, s, where) {
  switch (leg) {
    case "string":
      // A format: binary string in a typed position is a multipart file
      // field: the caller holds the bytes as a Blob and the transport appends
      // it to the form verbatim, so the part is the caller's bytes, not a
      // decoded and re-encoded copy of them. The success and raw-body
      // mappings cover their own binary schemas, so this branch sees the
      // multipart file fields only.
      return s.format === "binary" ? "Blob" : "string";
    case "integer":
    case "number":
      return "number";
    case "boolean":
      return "boolean";
    case "array":
      if (s.items === undefined) {
        problems.push(`${where}: array without items`);
        return "unknown[]";
      }
      return wrapArray(tsType(s.items, `${where}[]`));
    case "object": {
      if (s.properties && Object.keys(s.properties).length > 0) return objectLiteral(s, where, " ");
      if (s.additionalProperties && s.additionalProperties !== false) {
        if (typeof s.additionalProperties === "object") {
          return `{ [key: string]: ${tsType(s.additionalProperties, `${where}{}`)} }`;
        }
        return "{ [key: string]: unknown }";
      }
      if (s.additionalProperties === false) return "Record<string, never>";
      return "unknown"; // object with neither properties nor additionalProperties
    }
    default:
      problems.push(`${where}: unmapped type ${JSON.stringify(leg)}`);
      return "unknown";
  }
}

// objectLiteral emits a type literal for one object schema; the separator
// distinguishes the compact nested spelling from the named multi-line form.
function objectLiteral(s, where, separator) {
  const required = new Set(s.required ?? []);
  const entries = Object.entries(s.properties ?? {}).map(([name, p]) => {
    const t = tsType(p, `${where}.${name}`);
    return `${key(name)}${required.has(name) ? "" : "?"}: ${t}`;
  });
  return `{ ${entries.join(`;${separator}`)}${entries.length && separator === "\n" ? ";" : ""} }`;
}

// descriptor maps one schema to the structural descriptor the test-time
// interpreter validates values against (see web/packages/api-client/test/
// _validate.mjs). Constraints ride along as data; formats stay unchecked,
// matching the contract tools' fixture check.
function descriptor(s, where) {
  if (s === true || s === null || s === undefined) return { k: "unknown" };
  if (typeof s !== "object") return { k: "unknown" };
  const ref = refName(s);
  if (ref) return { k: "ref", name: ref };
  if (s.oneOf || s.anyOf) {
    const legs = s.oneOf ?? s.anyOf;
    const refs = legs.map(refName).filter(Boolean);
    const nullLegs = legs.filter(isNullLeg);
    if (legs.length === 2 && refs.length === 1 && nullLegs.length === 1) {
      return { k: "union", of: [{ k: "ref", name: refs[0] }, { k: "null" }] };
    }
    if (legs.length >= 2 && refs.length === legs.length) {
      return { k: "union", of: dedupe(refs).map((name) => ({ k: "ref", name })) };
    }
    return { k: "unknown" }; // tsType already filed the problem and fails generation
  }
  if (s.allOf) {
    const of = s.allOf.filter((leg) => leg !== true).map((leg) => descriptor(leg, `${where}&`));
    return of.length ? { k: "allOf", of } : { k: "unknown" };
  }
  if (s.enum) return { k: "lit", v: s.enum };
  const legs = Array.isArray(s.type) ? s.type : [s.type];
  if (legs.length > 1) return { k: "union", of: legs.map((leg) => scalarDescriptor(leg, s, where)) };
  return scalarDescriptor(legs[0], s, where);
}

function bounds(s) {
  const d = {};
  for (const bound of ["minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum"]) {
    if (s[bound] !== undefined) d[bound] = s[bound];
  }
  return d;
}

function scalarDescriptor(leg, s, where) {
  switch (leg) {
    case "string": {
      const d = { k: "string" };
      if (s.minLength !== undefined) d.minLength = s.minLength;
      if (s.maxLength !== undefined) d.maxLength = s.maxLength;
      if (s.pattern !== undefined) d.pattern = s.pattern;
      return d;
    }
    case "integer":
      return { k: "integer", ...bounds(s) };
    case "number":
      return { k: "number", ...bounds(s) };
    case "boolean":
      return { k: "boolean" };
    case "null":
      return { k: "null" };
    case "array": {
      if (s.items === undefined) return { k: "array", of: { k: "unknown" } };
      const d = { k: "array", of: descriptor(s.items, `${where}[]`) };
      if (s.minItems !== undefined) d.minItems = s.minItems;
      if (s.maxItems !== undefined) d.maxItems = s.maxItems;
      return d;
    }
    case "object": {
      const props = s.properties ?? {};
      if (Object.keys(props).length > 0) {
        const d = { k: "object", req: [...(s.required ?? [])] };
        d.props = Object.fromEntries(Object.entries(props).map(([name, p]) => [name, descriptor(p, `${where}.${name}`)]));
        if (s.additionalProperties === false) d.closed = true;
        return d;
      }
      if (s.additionalProperties && s.additionalProperties !== false) {
        if (typeof s.additionalProperties === "object") return { k: "record", of: descriptor(s.additionalProperties, `${where}{}`) };
        return { k: "record", of: { k: "unknown" } };
      }
      return { k: "unknown" };
    }
    default:
      return { k: "unknown" };
  }
}

// ---- named wire types, in document order ----------------------------------

const typesOut = [MARKER];
typesOut.push("// Every wire type the contract defines: one exported type per");
typesOut.push("// components/schemas entry, in document order. Regenerate with:");
typesOut.push("// node scripts/gen-client/gen-client.mjs");
typesOut.push("");

const descriptorsOut = [MARKER];
descriptorsOut.push("// A structural descriptor per wire type, in document order. The test-time");
descriptorsOut.push("// interpreter (web/packages/api-client/test/_validate.mjs) validates");
descriptorsOut.push("// captured responses against these shapes.");
descriptorsOut.push("");
descriptorsOut.push("export const SCHEMA_DESCRIPTORS = {");
const schemaNames = [];
for (const [name, s] of Object.entries(schemas)) {
  schemaNames.push(name);
  typesOut.push(`export type ${name} = ${namedTypeBody(s, name)}`);
  descriptorsOut.push(`  ${JSON.stringify(name)}: ${JSON.stringify(descriptor(s, name))},`);
}
descriptorsOut.push("};");
descriptorsOut.push("");
typesOut.push("");

function namedTypeBody(s, name) {
  if (s.enum) return `${dedupe(s.enum.map((v) => JSON.stringify(v))).join(" | ")};`;
  if (s.properties && Object.keys(s.properties).length > 0 && !s.allOf) {
    const required = new Set(s.required ?? []);
    const lines = Object.entries(s.properties).map(([pName, p]) => {
      const t = tsType(p, `${name}.${pName}`);
      const dep = p && typeof p === "object" && p.deprecated === true;
      const note = dep
        ? `  /** @deprecated${p.description ? ` ${String(p.description).replace(/\s+/g, " ").trim()}` : ""} */\n`
        : "";
      return `${note}  ${key(pName)}${required.has(pName) ? "" : "?"}: ${t};`;
    });
    return `{\n${lines.join("\n")}\n};`;
  }
  return `${tsType(s, name)};`;
}

// ---- manifest: schema names and the route table ----------------------------

const manifestOut = [MARKER];
manifestOut.push("// The document's schema names and its operation census, for the package's");
manifestOut.push("// gates. Regenerate with: node scripts/gen-client/gen-client.mjs");
manifestOut.push("");
manifestOut.push("export const SCHEMA_NAMES: readonly string[] = [");
for (const name of schemaNames) manifestOut.push(`  ${JSON.stringify(name)},`);
manifestOut.push("];");
manifestOut.push("");
manifestOut.push("export interface ManifestRoute {");
manifestOut.push("  method: string;");
manifestOut.push("  path: string;");
manifestOut.push("  operationId: string;");
manifestOut.push("  tag: string;");
manifestOut.push("  /** Set when the route is a live event stream, excluded from the generated surface. */");
manifestOut.push("  excluded?: boolean;");
manifestOut.push("}");
manifestOut.push("");

// ---- operations, grouped per tag -------------------------------------------

// resolveParam resolves one parameter entry, by $ref into the shared
// parameters or inline, to { name, in, required, schema }.
function resolveParam(p, where) {
  if (p.$ref) {
    const m = /^#\/components\/parameters\/(.+)$/.exec(p.$ref);
    if (!m || !sharedParams[m[1]]) {
      problems.push(`${where}: unknown parameter $ref ${p.$ref}`);
      return null;
    }
    const shared = sharedParams[m[1]];
    return { name: shared.name, in: shared.in, required: shared.required === true, schema: shared.schema };
  }
  return { name: p.name, in: p.in, required: p.required === true, schema: p.schema };
}

// successOf pins the success response: the lowest declared 2xx code, its
// schema, and how the client reads it: JSON, the raw text (plain string
// content such as text/csv and text/html), or the raw bytes (format: binary
// content: images, workbooks). schemaName carries the $ref name when the
// success is a single named schema; manifest.ts emits it as the entry the
// fixture gate validates captures against.
const isBinarySchema = (s) => s !== null && typeof s === "object" && s.type === "string" && s.format === "binary";

function successOf(op, where) {
  const codes = Object.keys(op.responses ?? {})
    .filter((c) => /^2..$/.test(c))
    .sort();
  if (codes.length === 0) return { typeSpelling: "unknown", textResponse: true, others: [] };
  const code = codes[0];
  const response = op.responses[code];
  if (response.$ref) {
    problems.push(`${where}: success response is a $ref (${response.$ref})`);
    return { typeSpelling: "unknown", textResponse: true, others: codes.slice(1) };
  }
  const content = response.content ?? {};
  const contentTypes = Object.keys(content);
  const json = content["application/json"];
  let out;
  if (json?.schema) {
    out = {
      typeSpelling: tsType(json.schema, `${where} ${code}`),
      textResponse: false,
      bytesResponse: false,
      schemaName: refName(json.schema) ?? undefined,
    };
  } else if (contentTypes.length === 0) {
    // A success code with no declared content: nothing to parse.
    out = { typeSpelling: "unknown", textResponse: true, bytesResponse: false };
  } else if (contentTypes.every((ct) => isBinarySchema(content[ct].schema))) {
    // Bytes on the wire (an image, a workbook): read them as bytes, never text.
    out = { typeSpelling: "Uint8Array", textResponse: false, bytesResponse: true, binaryContentTypes: contentTypes };
  } else if (contentTypes.every((ct) => content[ct].schema?.type === "string")) {
    // Non-JSON string content (a document, a receipt): the wire string.
    out = { typeSpelling: "string", textResponse: true, bytesResponse: false };
  } else {
    problems.push(`${where}: success ${code} carries content the mapping does not cover`);
    out = { typeSpelling: "unknown", textResponse: true, bytesResponse: false };
  }
  return { ...out, others: codes.slice(1), code };
}

// bodyOf pins the request body per the mapping in the header comment. When
// both application/json and multipart/form-data are declared and mappable,
// the body is kind "multi": one generated function per declared content type,
// the first declared keeping the operationId and the rest taking the content
// type's suffix (BODY_VARIANTS in manifest.ts records the mapping).

// formBinaryFields names a multipart form schema's binary (format: binary)
// fields, one level of $ref resolved: each becomes a filename option on the
// generated operation (`fileFilename` for a `file` field), so the caller can
// name the part instead of the platform's default "blob".
function formBinaryFields(s) {
  const resolved = schemas[refName(s) ?? ""] ?? s;
  if (!resolved || typeof resolved !== "object" || !resolved.properties) return [];
  return Object.entries(resolved.properties)
    .filter(([, field]) => isBinarySchema(field))
    .map(([name]) => name);
}

function bodyOf(op, where) {
  const rb = op.requestBody;
  if (!rb) return null;
  if (rb.$ref) {
    problems.push(`${where}: requestBody is a $ref`);
    return null;
  }
  const content = rb.content ?? {};
  const contentTypes = Object.keys(content);
  const variants = [];
  if (content["application/json"]?.schema) {
    variants.push({ kind: "json", suffix: "Json", contentType: "application/json", typeSpelling: tsType(content["application/json"].schema, `${where} body`) });
  }
  if (content["multipart/form-data"]?.schema) {
    const formSchema = content["multipart/form-data"].schema;
    variants.push({
      kind: "form",
      suffix: "Multipart",
      contentType: "multipart/form-data",
      typeSpelling: tsType(formSchema, `${where} body`),
      binaryFields: formBinaryFields(formSchema),
    });
  }
  if (variants.length > 0 && variants.length === contentTypes.length) {
    if (variants.length === 1) return variants[0];
    return { kind: "multi", variants };
  }
  const allString = contentTypes.length > 0 && contentTypes.every((ct) => content[ct].schema?.type === "string");
  if (allString) {
    const allBinary = contentTypes.every((ct) => isBinarySchema(content[ct].schema));
    return {
      kind: "raw",
      typeSpelling: allBinary ? "string | Uint8Array" : "string",
      raw: { contentTypes, binary: allBinary },
    };
  }
  problems.push(`${where}: requestBody content types the mapping does not cover: ${contentTypes.join(", ")}`);
  return null;
}

// The ops grouped by tag; paths in byte order, methods in document order
// within each path (the gen-types route-table order).
const opsByTag = new Map(tagNames.map((t) => [t, []]));
const routes = [];
const successSchemaNames = [];
const bodyVariants = [];
let opCount = 0;
for (const path of Object.keys(doc.paths).sort()) {
  const item = doc.paths[path];
  for (const method of Object.keys(item)) {
    if (!HTTP_METHODS.includes(method)) continue;
    const op = item[method];
    const where = `${method.toUpperCase()} ${path}`;
    if (!op.operationId) {
      problems.push(`${where}: missing operationId`);
      continue;
    }
    if (!IDENT.test(op.operationId)) problems.push(`${where}: operationId ${op.operationId} is not a usable identifier`);
    if (!Array.isArray(op.tags) || op.tags.length !== 1 || !opsByTag.has(op.tags[0])) {
      problems.push(`${where}: tag ${JSON.stringify(op.tags)} is not one declared document tag`);
      continue;
    }
    const excluded = STREAM_OPERATIONS.has(op.operationId);
    if (!excluded) opCount++;
    const pathParams = [...path.matchAll(/\{([^}]+)\}/g)].map((m) => m[1]);
    const declared = [];
    for (const p of op.parameters ?? []) {
      const resolved = resolveParam(p, where);
      if (resolved) declared.push(resolved);
    }
    const headerParams = declared.filter((p) => p.in === "header");
    // Header parameters are caller-set at the transport, never generated
    // arguments (the bearer is not one either): the shared org header, and
    // the return route's approver credential, which the till holds in memory
    // for that one call and drops. Any other header parameter is an unmapped
    // shape and fails here. The allowlist is global: a listed header is
    // accepted beside any operation that declares it, not bound to the
    // return route; a per-operation bound would be a later tightening.
    const KNOWN_HEADER_PARAMS = new Set(["X-Org", "X-Approver-Authorization"]);
    if (headerParams.some((p) => !KNOWN_HEADER_PARAMS.has(p.name))) {
      problems.push(`${where}: header parameter other than X-Org and X-Approver-Authorization`);
    }
    const queryParams = declared.filter((p) => p.in === "query");
    const pathSchemas = declared.filter((p) => p.in === "path");
    if (pathSchemas.length !== pathParams.length) {
      problems.push(`${where}: declared path parameters do not match the template`);
    }
    const success = successOf(op, where);
    const body = bodyOf(op, where);
    if (success.schemaName !== undefined) {
      successSchemaNames.push({ operationId: op.operationId, schema: success.schemaName });
    }
    if (body?.kind === "multi") {
      bodyVariants.push({
        operationId: op.operationId,
        variants: body.variants.slice(1).map((v) => `${op.operationId}${v.suffix}`),
      });
    }
    if (!excluded) {
      opsByTag.get(op.tags[0]).push({
        method: method.toUpperCase(),
        path,
        operationId: op.operationId,
        pathParams,
        pathSchemas,
        queryParams,
        approverHeader: headerParams.some((p) => p.name === "X-Approver-Authorization"),
        body,
        success,
      });
    }
    routes.push({
      method: method.toUpperCase(),
      path,
      operationId: op.operationId,
      tag: op.tags[0],
      ...(excluded ? { excluded: true } : {}),
    });
  }
}

// camel folds a tag to its namespace identifier; a namespace that would
// collide with a reserved word is suffixed Api so the generated export stays
// legal. The mapping is emitted as TAG_NAMESPACES and never re-derived.
const RESERVED_WORDS = new Set([
  "abstract", "arguments", "as", "asserts", "async", "await", "boolean", "break", "byte", "case", "catch",
  "char", "class", "const", "constructor", "continue", "debugger", "declare", "default", "delete", "do",
  "double", "else", "enum", "export", "extends", "false", "final", "finally", "float", "for", "from",
  "function", "get", "global", "goto", "if", "implements", "import", "in", "infer", "instanceof", "int",
  "interface", "is", "let", "long", "module", "namespace", "native", "new", "null", "number", "object",
  "package", "private", "protected", "public", "readonly", "require", "return", "satisfies", "set",
  "short", "static", "string", "super", "switch", "synchronized", "this", "throw", "throws", "transient",
  "true", "try", "type", "typeof", "undefined", "unique", "unknown", "var", "void", "volatile", "while",
  "with", "yield",
]);
const camel = (tag) => {
  const c = tag
    .split("-")
    .filter(Boolean)
    .map((seg, i) => (i === 0 ? seg : seg[0].toUpperCase() + seg.slice(1)))
    .join("");
  const safe = RESERVED_WORDS.has(c) ? `${c}Api` : c;
  if (!IDENT.test(safe)) problems.push(`tag ${tag} maps to the unusable namespace ${safe}`);
  return safe;
};

manifestOut.push("export const ROUTES: readonly ManifestRoute[] = [");
for (const r of routes) {
  const excluded = r.excluded === true ? ', "excluded": true' : "";
  manifestOut.push(`  { "method": ${JSON.stringify(r.method)}, "path": ${JSON.stringify(r.path)}, "operationId": ${JSON.stringify(r.operationId)}, "tag": ${JSON.stringify(r.tag)}${excluded} },`);
}
manifestOut.push("];");
manifestOut.push("");
manifestOut.push("// The document tag to generated namespace mapping, camel with reserved");
manifestOut.push("// words suffixed Api (the document's public tag maps to publicApi).");
manifestOut.push("export const TAG_NAMESPACES: Readonly<Record<string, string>> = {");
for (const tag of tagNames) manifestOut.push(`  ${JSON.stringify(tag)}: ${JSON.stringify(camel(tag))},`);
manifestOut.push("};");
manifestOut.push("");
manifestOut.push("// The operation's own success schema name, when the lowest declared 2xx is a");
manifestOut.push("// single named schema; the fixture gate validates each capture against the");
manifestOut.push("// entry of the operation it replays, never a test-picked schema. Operations");
manifestOut.push("// whose success is a union, a plain string, bytes, or undeclared have no");
manifestOut.push("// entry here.");
manifestOut.push("export const SUCCESS_SCHEMAS: Readonly<Record<string, string>> = {");
for (const named of successSchemaNames) {
  manifestOut.push(`  ${JSON.stringify(named.operationId)}: ${JSON.stringify(named.schema)},`);
}
manifestOut.push("};");
manifestOut.push("");
manifestOut.push("// Request bodies declaring more than one mappable content type get one");
manifestOut.push("// generated function per declared type: the first declared keeps the");
manifestOut.push("// operationId, the rest take the content type suffix. Keyed by the primary");
manifestOut.push("// operationId; the values are the variant function names (the multipart form");
manifestOut.push("// of feedbackCreate is feedbackCreateMultipart).");
manifestOut.push("export const BODY_VARIANTS: Readonly<Record<string, readonly string[]>> = {");
for (const named of bodyVariants) {
  manifestOut.push(`  ${JSON.stringify(named.operationId)}: [${named.variants.map((n) => JSON.stringify(n)).join(", ")}],`);
}
manifestOut.push("};");
manifestOut.push("");

// paramTsTypes collects the TypeScript spelling a query parameter schema
// needs. A $ref query parameter uses the named type; anything else maps
// inline. Path parameters are not mapped here: every one is typed
// string | number (see the header comment) and converted with String() at
// request build.
function paramType(schema, where) {
  return tsType(schema, where);
}

const generatedFiles = new Map();
generatedFiles.set("types.ts", typesOut.join("\n"));
generatedFiles.set("descriptors.ts", descriptorsOut.join("\n"));
generatedFiles.set("manifest.ts", manifestOut.join("\n"));

const indexOut = [MARKER];
indexOut.push("// The generated surface: every wire type, the descriptor and manifest");
indexOut.push("// data, and the operations grouped one namespace per document tag.");
indexOut.push("");
indexOut.push('export * from "./types.js";');
indexOut.push("// The descriptors stay off the generated entry: they are the test-time");
indexOut.push("// validation data (312 KB of it), re-exported by the package's test entry");
indexOut.push("// (src/testing.ts) and never needed by a runtime caller.");
indexOut.push('export * from "./manifest.js";');

for (const tag of tagNames) {
  const ops = opsByTag.get(tag);
  if (!ops || ops.length === 0) continue;
  const file = `${tag}.ts`;
  const out = [MARKER];
  out.push(`// The contract's operations tagged ${tag}: one function per documented`);
  out.push("// operation, in route-table order (paths in byte order). Every function");
  out.push("// resolves a Result and never throws on a refusal or a transport error.");
  out.push("");

  const usedTypes = new Set();
  const bodyLines = [];
  for (const op of ops) {
    const where = `${op.method} ${op.path}`;
    for (const p of [...op.queryParams, ...op.pathSchemas]) {
      const ref = refName(p.schema);
      if (ref) usedTypes.add(ref);
    }
    // Body and success type spellings were mapped during the census pass;
    // collect the $ref names they mention for the import list. A multi body
    // emits one function per declared content type: the primary keeps the
    // operationId, the rest take the content type suffix.
    collectRefs(op.success.typeSpelling, usedTypes);
    const variants =
      op.body?.kind === "multi"
        ? op.body.variants.map((v, i) => ({ body: v, name: i === 0 ? op.operationId : `${op.operationId}${v.suffix}` }))
        : [{ body: op.body, name: op.operationId }];
    for (const variant of variants) {
      if (variant.body) collectRefs(variant.body.typeSpelling, usedTypes);
    }

    for (const variant of variants) {
      const raw = variant.body?.kind === "raw" ? variant.body.raw : undefined;
      const args = ["client: ApiClient"];
      for (const pName of op.pathParams) {
        args.push(`${pName}: string | number`);
      }
      if (variant.body) args.push(`body: ${variant.body.typeSpelling}`);
      if (raw) {
        args.push(`contentType?: ${raw.contentTypes.map((ct) => JSON.stringify(ct)).join(" | ")}`);
      }
      const queryRequired = op.queryParams.some((p) => p.required);
      if (op.queryParams.length > 0) {
        const literal = objectLiteral(
          { required: op.queryParams.filter((p) => p.required).map((p) => p.name), properties: Object.fromEntries(op.queryParams.map((p) => [p.name, p.schema])) },
          where,
          " ",
        );
        args.push(`opts${queryRequired ? "" : "?"}: ${literal}`);
      }
      // A multipart form with binary fields carries a filename option per
      // field, so the caller names the part; with no option the part rides
      // under the platform's default name, "blob".
      const binaryFields = variant.body?.kind === "form" ? variant.body.binaryFields : [];
      if (binaryFields.length > 0) {
        const optionType = binaryFields.map((f) => `${key(f)}Filename?: string`).join("; ");
        if (op.queryParams.length > 0) {
          args.push(`upload?: { ${optionType} }`);
        } else {
          args.push(`opts?: { ${optionType} }`);
        }
      }
      args.push("call?: ApiCallOptions");

      const requestProps = [`method: ${JSON.stringify(op.method)}`, `path: ${JSON.stringify(op.path)}`];
      if (op.pathParams.length > 0) {
        requestProps.push(`pathParams: { ${op.pathParams.join(", ")} }`);
      }
      if (op.queryParams.length > 0) requestProps.push("query: opts");
      if (variant.body?.kind === "json") requestProps.push("body");
      if (variant.body?.kind === "form") requestProps.push("form: body");
      if (binaryFields.length > 0) {
        const holder = op.queryParams.length > 0 ? "upload" : "opts";
        requestProps.push(`filenames: { ${binaryFields.map((f) => `${key(f)}: ${holder}?.${key(f)}Filename`).join(", ")} }`);
      }
      if (raw) {
        requestProps.push(
          raw.contentTypes.length > 1
            ? "rawBody: { contentType, content: body }"
            : `rawBody: { contentType: contentType ?? ${JSON.stringify(raw.contentTypes[0])}, content: body }`,
        );
      }
      if (op.success.textResponse) requestProps.push("textResponse: true");
      if (op.success.bytesResponse) requestProps.push("bytesResponse: true");
      requestProps.push("signal: call?.signal");
      requestProps.push("timeoutMs: call?.timeoutMs");
      requestProps.push(`operationId: ${JSON.stringify(op.operationId)}`);

      // Notes the document itself forces onto the operation.
      const notes = [];
      if (op.approverHeader) {
        notes.push("// The document lets this call carry X-Approver-Authorization beside the caller's");
        notes.push("// bearer: the approver's own credential for a return above the approval amount,");
        notes.push("// held in memory for this one call and dropped. It rides the caller-set headers");
        notes.push("// (the credential is the caller's business, never a generated argument) and the");
        notes.push("// server never logs it.");
      }
      if (op.body?.kind === "multi") {
        const declared = op.body.variants.map((v, i) => (i === 0 ? v.contentType : `${v.contentType} (${op.operationId}${v.suffix})`));
        notes.push(`// The document declares this body as ${declared.join(" and ")}; the client emits one`);
        notes.push(
          `// function per declared type, and this one sends ${variant.body?.kind === "json" ? "the JSON body" : "the multipart form"}.`,
        );
      }
      if (variant.body?.kind === "form" && variant.body.binaryFields.length > 0) {
        const named = variant.body.binaryFields.map((f) => `${key(f)} (${key(f)}Filename)`).join(", ");
        notes.push("// The binary part's name: the options argument names it on the wire (here:");
        notes.push(`// ${named}); an unnamed Blob rides as "blob", the platform's default part name.`);
      }
      if (raw) {
        if (raw.contentTypes.length > 1) {
          notes.push(`// The document declares this upload as ${raw.contentTypes.join(", ")}; contentType says`);
          notes.push("// which declared type rides the wire, and with no contentType no Content-Type");
          notes.push("// header is sent at all, so an unsaid upload is refused, never mislabeled.");
        } else {
          notes.push(`// The document declares the body's content type as ${raw.contentTypes[0]}.`);
        }
        if (raw.binary) {
          notes.push("// The document declares the body format: binary: the body is the raw bytes");
          notes.push("// (a string rides its UTF-8 bytes verbatim).");
        }
      }
      if (op.success.bytesResponse) {
        notes.push(`// The document declares the success content as ${op.success.binaryContentTypes.join(", ")}`);
        notes.push("// (format: binary); the client resolves the raw bytes.");
      }
      if (op.success.others.length > 0) {
        notes.push(`// The document also declares success as ${op.success.others.join(" and ")}; the client`);
        notes.push(`// types the lowest declared success code (${op.success.code}).`);
      }
      if (op.success.others.length === 0 && op.success.code === undefined) {
        notes.push("// The document declares no success response for this operation; the client");
        notes.push("// resolves the raw response text.");
      }

      bodyLines.push(...notes);
      bodyLines.push(`export async function ${variant.name}(`);
      for (const [i, arg] of args.entries()) bodyLines.push(`  ${arg}${i < args.length - 1 ? "," : ""}`);
      bodyLines.push(`): Promise<Result<${op.success.typeSpelling}>> {`);
      bodyLines.push(`  return client.request<${op.success.typeSpelling}>({`);
      for (const prop of requestProps) bodyLines.push(`    ${prop},`);
      bodyLines.push("  });");
      bodyLines.push("}");
      bodyLines.push("");
    }
  }

  if (usedTypes.size > 0) out.push(`import type { ${[...usedTypes].sort().join(", ")} } from "./types.js";`);
  out.push('import type { Result } from "../result.js";');
  out.push('import { ApiClient, type ApiCallOptions } from "../http.js";');
  out.push("");
  out.push(...bodyLines);
  generatedFiles.set(file, out.join("\n"));
  indexOut.push(`export * as ${camel(tag)} from "./${tag}.js";`);
}

indexOut.push("");
generatedFiles.set("index.ts", indexOut.join("\n"));

// collectRefs pulls schema names out of an already-mapped type spelling: the
// named identifiers a tag file must import. The mapped spellings use schema
// names verbatim, so every PascalCase word starting a segment is a candidate;
// the import list is filtered against the real schema names.
function collectRefs(spelling, into) {
  for (const m of spelling.matchAll(/[A-Z][A-Za-z0-9]*/g)) {
    if (schemas[m[0]]) into.add(m[0]);
  }
}

if (problems.length) {
  console.error(`gen-client failed with ${problems.length} problem(s):`);
  for (const p of problems) console.error(`  - ${p}`);
  process.exit(1);
}

mkdirSync(outDir, { recursive: true });
for (const [name, content] of [...generatedFiles].sort(([a], [b]) => (a < b ? -1 : 1))) {
  writeFileSync(join(outDir, name), content.replace(/\s+$/, "\n"));
}
console.log(
  `gen-client: ${opCount} operations (${routes.length} routes, ${routes.filter((r) => r.excluded).length} stream excluded) in ${tagNames.length} tags, ${schemaNames.length} wire types -> ${outDir}`,
);
