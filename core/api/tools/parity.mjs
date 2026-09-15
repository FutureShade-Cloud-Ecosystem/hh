#!/usr/bin/env node
// Route parity: every literal route registered in hardscapeos_dibbits
// backend/internal must appear in core/api/openapi.yaml, and every route in
// the document must have an ERP source, apart from the entries in
// core/api/drop-list.yaml (drops and additions, each with a reason).
//
// The ERP route list comes from the same grep
// analysis/hardscapeos-inventory.md section 2.6 used:
//   grep -rhoE '"(GET|POST|PUT|PATCH|DELETE) /api[^"]*"' backend/internal
// over non-test Go files, deduplicated. Portal routes are renamed
// /api/portal/v1/X -> /api/v1/tenant/X before the diff.
//
// Usage: node parity.mjs [path-to-hardscapeos_dibbits]
// Env: HH_ERP_ROOT overrides the default (<hh repo root>/../hardscapeos_dibbits).
import { readFileSync } from "node:fs";
import { join, dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { execFileSync } from "node:child_process";
import YAML from "yaml";

const apiDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const repoRoot = join(apiDir, "..", "..");
const erpRoot = resolve(
  process.env.HH_ERP_ROOT ?? process.argv[2] ?? join(repoRoot, "..", "hardscapeos_dibbits"),
);

const raw = execFileSync(
  "grep",
  [
    "-rhoE",
    '"(GET|POST|PUT|PATCH|DELETE) /api[^"]*"',
    "backend/internal",
    "--include=*.go",
    "--exclude=*_test.go",
  ],
  { cwd: erpRoot, encoding: "utf8" },
);

const RENAME = [["/api/portal/v1/", "/api/v1/tenant/"]];
const rename = (route) => {
  for (const [from, to] of RENAME) {
    if (route.includes(` ${from}`)) return route.replace(` ${from}`, ` ${to}`);
  }
  return route;
};

const erpRoutes = new Set(
  raw
    .split("\n")
    .map((l) => l.trim().replace(/^"|"$/g, ""))
    .filter(Boolean)
    .map(rename),
);

const doc = YAML.parse(readFileSync(join(apiDir, "openapi.yaml"), "utf8"));
const METHODS = new Set(["get", "put", "post", "delete", "patch"]);
const docRoutes = new Set();
for (const [path, item] of Object.entries(doc.paths)) {
  for (const method of Object.keys(item)) {
    if (METHODS.has(method)) docRoutes.add(`${method.toUpperCase()} ${path}`);
  }
}

const ledger = YAML.parse(readFileSync(join(apiDir, "drop-list.yaml"), "utf8"));
const drops = new Map((ledger.drops ?? []).map((d) => [d.route, d.reason]));
const additions = new Map((ledger.additions ?? []).map((a) => [a.route, a.reason]));

const problems = [];
for (const route of [...erpRoutes].sort()) {
  if (!docRoutes.has(route)) {
    if (drops.has(route)) continue;
    problems.push(`ERP route not in the document and not on the drop list: ${route}`);
  }
}
for (const route of [...docRoutes].sort()) {
  if (!erpRoutes.has(route)) {
    if (additions.has(route)) continue;
    problems.push(`document route with no ERP source and no additions entry: ${route}`);
  }
}
for (const route of drops.keys()) {
  if (docRoutes.has(route)) problems.push(`drop-list entry is actually in the document: ${route}`);
  if (!erpRoutes.has(route)) problems.push(`drop-list entry names no ERP route: ${route}`);
}
for (const route of additions.keys()) {
  if (!docRoutes.has(route)) problems.push(`additions entry names no document route: ${route}`);
  if (erpRoutes.has(route)) problems.push(`additions entry has an ERP source after all: ${route}`);
}

if (problems.length) {
  console.error(`route parity failed with ${problems.length} problem(s):`);
  for (const p of problems) console.error(`  - ${p}`);
  process.exit(1);
}
console.log(
  `route parity: ${erpRoutes.size} ERP routes, ${docRoutes.size} document routes, ` +
    `${drops.size} drops, ${additions.size} additions, diff empty`,
);
