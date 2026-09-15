#!/usr/bin/env node
// Assemble core/api/openapi.yaml from skeleton.yaml plus every fragment in
// fragments/. Fails on: unparseable YAML, duplicate schema names, duplicate
// operationIds, operations without x-role or a known tag, unresolved $refs,
// and templated paths that collide under different parameter names.
import { readFileSync, readdirSync, writeFileSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import YAML from "yaml";

const apiDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const skeleton = YAML.parse(readFileSync(join(apiDir, "skeleton.yaml"), "utf8"));

const doc = {
  openapi: skeleton.openapi,
  jsonSchemaDialect: skeleton.jsonSchemaDialect,
  info: skeleton.info,
  security: skeleton.security,
  servers: skeleton.servers,
  tags: skeleton.tags ?? [],
  paths: {},
  components: { ...skeleton.components, schemas: { ...(skeleton.components?.schemas ?? {}) } },
};

const tagNames = new Set(doc.tags.map((t) => t.name));
const HTTP_METHODS = new Set(["get", "put", "post", "delete", "patch"]);
const problems = [];
const operationIds = new Map();

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
console.log(`openapi.yaml assembled: ${Object.keys(doc.paths).length} path keys, ${routeCount} operations, ${Object.keys(doc.components.schemas).length} schemas`);
