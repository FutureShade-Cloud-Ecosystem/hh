#!/usr/bin/env node
// Assemble core/api/openapi.yaml from skeleton.yaml plus every fragment in
// fragments/. Fails on: unparseable YAML, duplicate schema names, duplicate
// operationIds, operations without x-role or a known tag, unresolved $refs,
// templated paths that collide under different parameter names, and any
// x-access fault: a staff operation without an x-access block, a non-staff
// operation with one, a surface or level outside roleconfig's vocabulary,
// none as a route's level (a wall that cannot be switched off, D49), a
// named list the configuration does not carry, a staff operation whose tag
// has no x-access-map entry, and a write annotated below manage with no
// reason saying why.
import { readFileSync, readdirSync, writeFileSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import YAML from "yaml";

const apiDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const skeleton = YAML.parse(readFileSync(join(apiDir, "skeleton.yaml"), "utf8"));

// The surface and level vocabularies are roleconfig's, read from the
// decided default's cells (D48): one source, so the document and the
// guards can never drift apart on the words they share. The namable lists
// are the configuration's role-list keys: a setting whose value is a list
// of roles, or one of the two standing lists beside the exception kinds.
const roleconfig = YAML.parse(readFileSync(join(apiDir, "..", "internal", "roleconfig", "default.json"), "utf8"));
const knownSurfaces = new Set(roleconfig.cells.map((c) => c.surface));
const knownLevels = new Set(roleconfig.cells.map((c) => c.level));
const ROLE_LIST_SETTINGS = ["cost_visibility", "refund_approvers", "pricing_editors", "shortage_resolvers", "dispatch_policy_setters"];
const knownListKeys = new Set([
  ...ROLE_LIST_SETTINGS.filter((k) => Array.isArray(roleconfig.settings?.[k])),
  "floor_clearers_own_branch",
  "delivery_date_committers",
]);

// All state is declared before any check runs, so a faulty map entry is
// named as the fault it is instead of falling over mid-walk.
const problems = [];
const operationIds = new Map();
const HTTP_METHODS = new Set(["get", "put", "post", "delete", "patch"]);
const WRITE_METHODS = new Set(["put", "post", "patch", "delete"]);

const doc = {
  openapi: skeleton.openapi,
  jsonSchemaDialect: skeleton.jsonSchemaDialect,
  info: skeleton.info,
  security: skeleton.security,
  servers: skeleton.servers,
  tags: skeleton.tags ?? [],
  "x-access-map": skeleton["x-access-map"] ?? {},
  paths: {},
  components: { ...skeleton.components, schemas: { ...(skeleton.components?.schemas ?? {}) } },
};

const tagNames = new Set(doc.tags.map((t) => t.name));
// The census word families this tool knows. The three staff tier words
// carry an x-access block (the role gate reads it); every other word
// carries none, because its wall lives elsewhere: public is open, tenant
// and scope-member ride the identity guards, the capability words ride
// the capability guard, and machine (ADR-0024) rides the machine
// principal's own door list, so a machine operation with an x-access
// block is the same fault as any other non-staff operation with one.
const STAFF_ROLES = new Set(["requireStaff", "requireManager", "requireAdmin"]);
const MACHINE_ROLES = new Set(["machine"]);
const accessMap = doc["x-access-map"];
let accessCount = 0;
const exceptions = [];
for (const [tag, entry] of Object.entries(accessMap)) {
  if (!tagNames.has(tag)) problems.push(`x-access-map names unknown tag ${tag}`);
  const surfaces = entry?.surfaces ?? [];
  if (surfaces.length === 0) problems.push(`x-access-map ${tag}: no surfaces`);
  for (const s of surfaces) if (!knownSurfaces.has(s)) problems.push(`x-access-map ${tag}: unknown surface ${s}`);
  if (!knownLevels.has(entry?.level)) problems.push(`x-access-map ${tag}: unknown level ${entry?.level}`);
  if (entry?.level === "none") problems.push(`x-access-map ${tag}: level none refuses every role; a route level cannot be none (D49)`);
}

// checkAccess validates one operation's x-access block, returning its
// census spelling ("surface1|surface2 at level") for the summary line.
// A write annotated below manage must carry a reason saying why: the
// convention cases where the tier gate alone can hold the wall, and the
// read-semantics operations the POST method dresses, each say so in place.
function checkAccess(where, method, op) {
  const xa = op["x-access"];
  const staff = STAFF_ROLES.has(op["x-role"]);
  if (!staff) {
    if (xa !== undefined) problems.push(`${where}: non-staff operation carries x-access`);
    return null;
  }
  if (xa === undefined) {
    problems.push(`${where}: staff operation carries no x-access`);
    return null;
  }
  const surfaces = xa.surfaces;
  if (!Array.isArray(surfaces) || surfaces.length === 0) {
    problems.push(`${where}: x-access carries no surfaces`);
    return null;
  }
  for (const s of surfaces) if (!knownSurfaces.has(s)) problems.push(`${where}: x-access names unknown surface ${s}`);
  if (!knownLevels.has(xa.level)) problems.push(`${where}: x-access names unknown level ${xa.level}`);
  if (xa.level === "none") problems.push(`${where}: x-access level none refuses every role; a route level cannot be none (D49)`);
  if (xa.list !== undefined) {
    if (!knownListKeys.has(xa.list)) problems.push(`${where}: x-access names list ${xa.list}, which the configuration does not carry`);
  }
  if (WRITE_METHODS.has(method) && xa.level === "read") {
    if (typeof xa.reason !== "string" || xa.reason.trim() === "") {
      problems.push(`${where}: a write at read carries no reason`);
    }
  }
  const mapped = accessMap[op.tags?.[0]];
  if (!mapped) problems.push(`${where}: staff tag ${op.tags?.[0]} has no x-access-map entry`);
  return `${surfaces.join("|")} at ${xa.level}`;
}

function mergePaths(paths, source) {
  for (const [path, item] of Object.entries(paths ?? {})) {
    if (!doc.paths[path]) {
      doc.paths[path] = item;
    } else {
      for (const [method, op] of Object.entries(item)) {
        if (doc.paths[path][method]) {
          problems.push(`${source}: duplicate operation ${method.toUpperCase()} ${path}`);
        } else {
          doc.paths[path][method] = op;
        }
      }
    }
    for (const method of Object.keys(doc.paths[path])) {
      if (!HTTP_METHODS.has(method)) problems.push(`${source}: ${path} has non-method key ${method}`);
    }
  }
}

function mergeSchemas(schemas, source) {
  for (const [name, schema] of Object.entries(schemas ?? {})) {
    if (doc.components.schemas[name]) {
      problems.push(`${source}: schema name collision on ${name}`);
    } else {
      doc.components.schemas[name] = schema;
    }
  }
}

function mergeComponentKind(kind, entries, source) {
  const bucket = (doc.components[kind] ??= {});
  for (const [name, value] of Object.entries(entries ?? {})) {
    if (bucket[name]) {
      problems.push(`${source}: ${kind} name collision on ${name}`);
    } else {
      bucket[name] = value;
    }
  }
}

mergePaths(skeleton.paths, "skeleton.yaml");

for (const file of readdirSync(join(apiDir, "fragments")).filter((f) => f.endsWith(".yaml")).sort()) {
  let frag;
  try {
    frag = YAML.parse(readFileSync(join(apiDir, "fragments", file), "utf8"), { prettyErrors: true });
  } catch (err) {
    problems.push(`${file}: YAML parse failure: ${err.message}`);
    continue;
  }
  const top = Object.keys(frag ?? {}).sort();
  if (top.join(",") !== "components,paths") {
    problems.push(`${file}: top-level keys must be exactly paths and components, found ${top.join(",")}`);
  }
  mergePaths(frag.paths, file);
  mergeSchemas(frag.components?.schemas, file);
  mergeComponentKind("parameters", frag.components?.parameters, file);
  mergeComponentKind("responses", frag.components?.responses, file);
}

// Templated-path collisions: same shape with different parameter names.
const seenTemplates = new Map();
for (const path of Object.keys(doc.paths)) {
  const shape = path.replace(/\{[^}]+\}/g, "{}");
  if (seenTemplates.has(shape) && seenTemplates.get(shape) !== path) {
    problems.push(`templated path collision: ${seenTemplates.get(shape)} vs ${path}`);
  } else {
    seenTemplates.set(shape, path);
  }
}

// Operation checks.
// bearerSchemesOf collects the security scheme names one operation
// carries, after the document-level default applies.
const bearerSchemesOf = (op) => {
  const sec = op.security === undefined ? doc.security : op.security;
  const names = new Set();
  for (const req of sec ?? []) for (const scheme of Object.keys(req)) names.add(scheme);
  return names;
};
for (const [path, item] of Object.entries(doc.paths)) {
  for (const [method, op] of Object.entries(item)) {
    if (!HTTP_METHODS.has(method)) continue;
    const where = `${method.toUpperCase()} ${path}`;
    if (!op.operationId) problems.push(`${where}: missing operationId`);
    else if (operationIds.has(op.operationId)) problems.push(`${where}: operationId ${op.operationId} also used by ${operationIds.get(op.operationId)}`);
    else operationIds.set(op.operationId, where);
    if (op["x-role"] === undefined) problems.push(`${where}: missing x-role`);
    if (!Array.isArray(op.tags) || op.tags.length !== 1) problems.push(`${where}: must carry exactly one tag`);
    else if (!tagNames.has(op.tags[0])) problems.push(`${where}: unknown tag ${op.tags[0]}`);
    // The machine word and the MachineBearer scheme name each other
    // (ADR-0024): an operation worded machine carries the machine
    // credential alone, and an operation carrying MachineBearer is worded
    // machine. Either without the other is a document fault.
    if (MACHINE_ROLES.has(op["x-role"]) !== bearerSchemesOf(op).has("MachineBearer")) {
      problems.push(`${where}: x-role machine and the MachineBearer security scheme must name each other`);
    }
    if (op["x-role"] !== undefined && (!Array.isArray(op.tags) || op.tags.length === 1)) {
      const access = checkAccess(where, method, op);
      if (access !== null) {
        accessCount += 1;
        if (access !== (accessMap[op.tags[0]] ? `${accessMap[op.tags[0]].surfaces.join("|")} at ${accessMap[op.tags[0]].level}` : "")) exceptions.push(`${where}: ${access}`);
      }
    }
    // Every templated path parameter must be declared, inline or by $ref.
    const templated = [...path.matchAll(/\{([^}]+)\}/g)].map((m) => m[1]);
    const sharedParams = doc.components.parameters ?? {};
    const declared = (op.parameters ?? [])
      .map((p) => {
        if (p.in === "path") return p.name;
        const m = /#\/components\/parameters\/(.+)$/.exec(p.$ref ?? "");
        if (m && sharedParams[m[1]]?.in === "path") return sharedParams[m[1]].name;
        return null;
      })
      .filter(Boolean);
    for (const t of templated) if (!declared.includes(t)) problems.push(`${where}: path param {${t}} not declared`);
  }
}

// $ref resolution.
const refTargets = new Set();
for (const name of Object.keys(doc.components.schemas)) refTargets.add(`#/components/schemas/${name}`);
for (const name of Object.keys(doc.components.parameters ?? {})) refTargets.add(`#/components/parameters/${name}`);
for (const name of Object.keys(doc.components.responses ?? {})) refTargets.add(`#/components/responses/${name}`);
for (const name of Object.keys(doc.components.securitySchemes ?? {})) refTargets.add(`#/components/securitySchemes/${name}`);

function walk(node, trail) {
  if (Array.isArray(node)) return node.forEach((v, i) => walk(v, `${trail}[${i}]`));
  if (node && typeof node === "object") {
    for (const [k, v] of Object.entries(node)) {
      if (k === "$ref" && typeof v === "string" && v.startsWith("#/") && !refTargets.has(v)) {
        problems.push(`unresolved $ref ${v} at ${trail}`);
      } else walk(v, `${trail}.${k}`);
    }
  }
}
walk(doc.paths, "paths");
walk(doc.components.schemas, "components.schemas");

if (problems.length) {
  console.error(`merge failed with ${problems.length} problem(s):`);
  for (const p of problems) console.error(`  - ${p}`);
  process.exit(1);
}

const header = [
  "# hh core API contract, assembled by core/api/tools/merge.mjs from",
  "# core/api/skeleton.yaml and core/api/fragments/*.yaml.",
  "# Edit the sources, not this file; regenerate with: node core/api/tools/merge.mjs",
  "# House rules: no em or en dashes; no dates, durations or calendar references.",
  "",
].join("\n");
writeFileSync(join(apiDir, "openapi.yaml"), header + YAML.stringify(doc, { lineWidth: 0 }));

const routeCount = Object.values(doc.paths).reduce(
  (n, item) => n + Object.keys(item).filter((k) => HTTP_METHODS.has(k)).length,
  0,
);
console.log(`x-access: ${accessCount} staff operations annotated, ${exceptions.length} exceptions to the tag map:`);
for (const line of exceptions) console.log(`  - ${line}`);
console.log(`openapi.yaml assembled: ${Object.keys(doc.paths).length} path keys, ${routeCount} operations, ${Object.keys(doc.components.schemas).length} schemas`);
