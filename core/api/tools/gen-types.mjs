#!/usr/bin/env node
// Generate the Go server types and the route table from the assembled
// contract (core/api/openapi.yaml) into core/internal/platform/apigen/
// (types.gen.go and routes.gen.go). Nothing else generates server types
// (FINDINGS.md section 10), so this is the minimal in-repo generator the
// B-types batch builds: no vendored third-party generator, one deliberate
// mapping per OpenAPI 3.1 feature the document uses, and a loud failure on
// any shape the mapping does not cover, so document drift can never pass
// silently as a silently weakened type.
//
// Modes: default is check (compare against the committed files and exit
// nonzero on any difference); --write regenerates them. The check mode is
// wired into check-all.mjs and ci.yml the same way the assembled document's
// own drift is asserted (merge.mjs plus git diff --exit-code), so a document
// change without a regeneration fails the suite.
//
// The mapping, pinned (OpenAPI 3.1 feature to Go):
//   $ref to a sibling schema            the named Go type
//   type: string                        string (uuid, email, password,
//                                       date and binary formats included;
//                                       format: date keeps the wire string)
//   format: date-time                   time.Time
//   type: integer format int32/int64    int32 / int64 (plain int otherwise)
//   type: number                        float64
//   type: boolean                       bool
//   type: array                         []T over the items mapping
//                                       (items: true maps to any)
//   type: object with properties        a struct; json tags verbatim; a
//                                       nested inline object property maps
//                                       to an anonymous struct
//   type: object, no properties, with   map[string]any (map[string]T over
//     additionalProperties              a typed additionalProperties)
//   type: [T, "null"]                   the T mapping; the property that
//                                       carries it becomes a pointer
//   property not in required            pointer with omitempty on the tag;
//                                       a required nullable property is a
//                                       pointer without omitempty so null
//                                       round-trips
//   allOf                               a struct embedding each $ref item
//                                       and flattening each inline object
//   oneOf / anyOf / empty schema        any
//   enum (string)                       a defined string type; the values
//                                       ride as a comment
//   object with properties AND          a struct; the openness is noted in
//     additionalProperties: true        a comment (the core serves the
//                                       named fields)
//
// Deterministic by construction: schemas and routes are emitted in one
// fixed order, property order follows the document, no timestamp or
// environment value enters the output, and the emitted Go is passed
// through the toolchain's own gofmt before it is compared or written, so
// the committed files are always gofmt-identical. Run twice, no diff.
import { readFileSync, writeFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import YAML from "yaml";

const toolsDir = dirname(fileURLToPath(import.meta.url));
const apiDir = join(toolsDir, "..");
const outDir = join(apiDir, "..", "internal", "platform", "apigen");
const write = process.argv.includes("--write");

const doc = YAML.parse(readFileSync(join(apiDir, "openapi.yaml"), "utf8"));
const schemas = doc.components?.schemas ?? {};
const problems = [];

const refName = (s) => {
  const m = /^#\/components\/schemas\/(.+)$/.exec(s?.$ref ?? "");
  if (!m) return null;
  if (!schemas[m[1]]) problems.push(`unknown $ref target ${s.$ref}`);
  return m[1];
};

const isNullable = (s) => Array.isArray(s?.type) && s.type.includes("null");

// normType reduces a type array to its single non-null member; any other
// union is unmappable and fails generation loudly.
function normType(s, where) {
  if (!Array.isArray(s.type)) return s.type;
  const nonNull = s.type.filter((x) => x !== "null");
  if (nonNull.length === 1) return nonNull[0];
  problems.push(`${where}: unmapped type union ${JSON.stringify(s.type)}`);
  return undefined;
}

// fieldName maps a JSON property name to its Go field name: segments split
// on underscores and hyphens, each segment capitalized on its first letter
// only, so both snake_case and already-PascalCase ERP tags (LineNo,
// ProductID) survive verbatim.
function fieldName(name, taken, where) {
  const go = name
    .split(/[_-]/)
    .filter(Boolean)
    .map((seg) => seg[0].toUpperCase() + seg.slice(1))
    .join("");
  if (!/^[A-Za-z][A-Za-z0-9]*$/.test(go)) {
    problems.push(`${where}: property ${name} maps to the unusable Go name ${go}`);
    return "Unnamed";
  }
  if (taken.has(go)) problems.push(`${where}: field name collision on ${go} (property ${name})`);
  taken.add(go);
  return go;
}

// scalar maps a non-object, non-array schema to its Go spelling.
function scalar(s, where) {
  switch (normType(s, where)) {
    case "string":
      if (s.format === "date-time") return "time.Time";
      return "string";
    case "integer":
      if (s.format === "int32") return "int32";
      if (s.format === "int64") return "int64";
      return "int";
    case "number":
      return "float64";
    case "boolean":
      return "bool";
    case undefined:
      return "any";
    default:
      problems.push(`${where}: unmapped type ${JSON.stringify(s.type)}`);
      return "any";
  }
}

// structLines emits the field lines for one object (or allOf) schema.
// Returns null when the schema is not an object shape.
function structLines(s, where) {
  const lines = [];
  const taken = new Set();
  const required = new Set();
  let openNote = false;
  const absorb = (inline, src) => {
    for (const r of inline.required ?? []) required.add(r);
    if (inline.additionalProperties === true) openNote = true;
    for (const [name, p] of Object.entries(inline.properties ?? {})) {
      const field = fieldName(name, taken, `${where} (${src})`);
      const nullable = isNullable(p);
      let base = goType(p, `${where}.${name}`);
      if (base === null) base = inlineStruct(p, `${where}.${name}`);
      const typ = nullable || !required.has(name) ? pointer(base) : base;
      const omitempty = !required.has(name) || nullable ? ",omitempty" : "";
      lines.push(`	${field} ${typ} \`json:"${name}${omitempty}"\``);
    }
  };
  if (s.allOf) {
    for (const item of s.allOf) {
      const ref = refName(item);
      if (ref) {
        if (taken.has(ref)) problems.push(`${where}: allOf embeds ${ref} twice`);
        taken.add(ref);
        lines.push(`	${ref}`);
        continue;
      }
      if (item.type === "object" || item.properties) absorb(item, "allOf leg");
      else problems.push(`${where}: unmappable allOf leg`);
    }
  } else {
    absorb(s, "body");
  }
  if (openNote) lines.push(`	// The document permits additional properties beyond the fields above.`);
  return lines;
}

// pointer wraps a type spelling as a pointer, keeping any unpointerable
// spelling (any) by value: a nil any carries the same absence.
function pointer(t) {
  if (t === "any") return "any";
  if (t.startsWith("*")) return t;
  return `*${t}`;
}

// goType maps one schema to its Go type spelling. Object shapes return
// null: their struct form is emitted by structLines (named) or inlineStruct
// (anonymous, for nested inline object properties).
function goType(s, where) {
  if (s === true || s === null || s === undefined) return "any";
  if (typeof s !== "object") {
    problems.push(`${where}: non-schema node ${JSON.stringify(s)}`);
    return "any";
  }
  const ref = refName(s);
  if (ref) return ref;
  if (s.oneOf || s.anyOf) return "any";
  if (s.allOf) return null;
  if (s.enum) return scalar(s, where);
  const t = normType(s, where);
  if (t === "array") {
    if (s.items === undefined) {
      problems.push(`${where}: array without items`);
      return "[]any";
    }
    return `[]${goType(s.items, `${where}[]`)}`;
  }
  if (t === "object") {
    if (s.properties && Object.keys(s.properties).length > 0) return null;
    if (s.additionalProperties && s.additionalProperties !== false) {
      if (typeof s.additionalProperties === "object" && s.additionalProperties !== true) {
        return `map[string]${goType(s.additionalProperties, `${where}{}`)}`;
      }
      return "map[string]any";
    }
    if (s.additionalProperties === false) return "struct{}";
    return "any"; // object with neither properties nor additionalProperties
  }
  return scalar(s, where);
}

// inlineStruct emits the one-line anonymous struct spelling for a nested
// inline object property. Fields follow the document's own order; none of
// the document's nested inline objects declares a required list, so every
// field is a pointer with omitempty.
function inlineStruct(s, where) {
  const parts = [];
  const taken = new Set();
  for (const [name, p] of Object.entries(s.properties ?? {})) {
    const field = fieldName(name, taken, where);
    let base = goType(p, `${where}.${name}`);
    if (base === null) {
      problems.push(`${where}.${name}: nested inline object inside a nested inline object`);
      base = "any";
    }
    parts.push(`${field} ${pointer(base)} \`json:"${name},omitempty"\``);
  }
  return `struct{ ${parts.join("; ")} }`;
}

const typesOut = [];
typesOut.push(`// Code generated by core/api/tools/gen-types.mjs from core/api/openapi.yaml. DO NOT EDIT.`);
typesOut.push(`// Regenerate with: node core/api/tools/gen-types.mjs --write`);
typesOut.push(`// Check mode (the default, wired into check-all.mjs and ci.yml) fails when`);
typesOut.push(`// the committed file differs from what the document produces.`);
typesOut.push(``);
typesOut.push(`// Package apigen holds the server types generated from the contract`);
typesOut.push(`// (core/api/openapi.yaml): one Go type per components/schemas entry, with`);
typesOut.push(`// the mapping pinned in the generator's header comment. The types are the`);
typesOut.push(`// contract surface the core serves and decodes; the route table in`);
typesOut.push(`// routes.gen.go is the same document's operation census.`);
typesOut.push(`package apigen`);
typesOut.push(``);
typesOut.push(`import "time"`);
typesOut.push(``);

for (const [name, s] of Object.entries(schemas)) {
  if (s?.enum) {
    typesOut.push(`// ${name} is the document's enum: ${s.enum.map((v) => JSON.stringify(v)).join(", ")}.`);
    typesOut.push(`type ${name} string`);
    typesOut.push(``);
    continue;
  }
  const t = goType(s, name);
  if (t !== null) {
    typesOut.push(`type ${name} ${t}`);
    typesOut.push(``);
    continue;
  }
  const lines = structLines(s, name);
  if (!lines) {
    problems.push(`${name}: neither a named type nor a struct shape`);
    continue;
  }
  typesOut.push(`type ${name} struct {`);
  for (const line of lines) typesOut.push(line);
  typesOut.push(`}`);
  typesOut.push(``);
}

// The route table: one entry per operation, paths in byte order, methods in
// document order within each path.
const METHODS = ["get", "put", "post", "delete", "patch"];
const routesOut = [];
routesOut.push(`// Code generated by core/api/tools/gen-types.mjs from core/api/openapi.yaml. DO NOT EDIT.`);
routesOut.push(`// Regenerate with: node core/api/tools/gen-types.mjs --write`);
routesOut.push(``);
routesOut.push(`package apigen`);
routesOut.push(``);
routesOut.push(`// Route is one documented operation of the contract.`);
routesOut.push(`type Route struct {`);
routesOut.push(`	Method      string // uppercase HTTP method`);
routesOut.push(`	Path        string // the path template exactly as documented`);
routesOut.push(`	OperationID string // the contract operationId`);
routesOut.push(`	Tag         string // the operation's single tag`);
routesOut.push(`	XRole       string // the x-role gate spelling`);
routesOut.push(`	Bearer      string // staff, tenant, staff-or-tenant, or public`);
routesOut.push(`	Addition    bool   // the operation carries x-addition (no ERP source route)`);
routesOut.push(`}`);
routesOut.push(``);
routesOut.push(`// Routes holds every operation in the document, paths in byte order and`);
routesOut.push(`// methods in document order within each path. The registration parity`);
routesOut.push(`// test asserts the core's registered surface against this table in both`);
routesOut.push(`// directions.`);
routesOut.push(`var Routes = []Route{`);

const bearerOf = (op) => {
  const sec = op.security === undefined ? doc.security : op.security;
  if (!sec || sec.length === 0) return "public";
  const names = new Set();
  for (const req of sec) for (const scheme of Object.keys(req)) names.add(scheme);
  if (names.has("StaffBearer") && names.has("TenantBearer")) return "staff-or-tenant";
  if (names.has("TenantBearer")) return "tenant";
  if (names.has("StaffBearer")) return "staff";
  return [...names].sort().join("+");
};

let routeCount = 0;
for (const path of Object.keys(doc.paths).sort()) {
  const item = doc.paths[path];
  for (const method of Object.keys(item)) {
    if (!METHODS.includes(method)) continue;
    const op = item[method];
    if (!op.operationId) {
      problems.push(`${method.toUpperCase()} ${path}: missing operationId`);
      continue;
    }
    routeCount++;
    routesOut.push(`	{Method: ${JSON.stringify(method.toUpperCase())}, Path: ${JSON.stringify(path)}, OperationID: ${JSON.stringify(op.operationId)}, Tag: ${JSON.stringify(op.tags?.[0] ?? "")}, XRole: ${JSON.stringify(op["x-role"] ?? "")}, Bearer: ${JSON.stringify(bearerOf(op))}, Addition: ${op["x-addition"] ? "true" : "false"}},`);
  }
}
routesOut.push(`}`);
routesOut.push(``);

if (problems.length) {
  console.error(`gen-types failed with ${problems.length} problem(s):`);
  for (const p of problems) console.error(`  - ${p}`);
  process.exit(1);
}

// gofmt runs the toolchain's own formatter over the emitted source, so the
// committed files are gofmt-identical without a hand-rolled aligner.
function gofmt(name, source) {
  try {
    return execFileSync("gofmt", { input: source, encoding: "utf8" });
  } catch (err) {
    problems.push(`${name}: gofmt rejected the generated source: ${err.stderr ?? err.message}`);
    return source;
  }
}

const files = [
  ["types.gen.go", gofmt("types.gen.go", typesOut.join("\n") + "\n")],
  ["routes.gen.go", gofmt("routes.gen.go", routesOut.join("\n") + "\n")],
];

let drifted = false;
for (const [name, content] of files) {
  const target = join(outDir, name);
  let current;
  try {
    current = readFileSync(target, "utf8");
  } catch {
    current = null;
  }
  if (current === content) {
    console.log(`${name}: up to date`);
    continue;
  }
  drifted = true;
  if (write) {
    writeFileSync(target, content);
    console.log(`${name}: regenerated`);
  } else {
    console.error(`${name}: DRIFT: the committed file differs from what core/api/openapi.yaml produces; regenerate with: node core/api/tools/gen-types.mjs --write`);
  }
}
console.log(`document: ${Object.keys(schemas).length} schemas, ${routeCount} routes`);
if (drifted && !write) process.exit(1);
