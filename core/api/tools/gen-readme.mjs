#!/usr/bin/env node
// Generate the route source map in core/api/README.md: every route in
// openapi.yaml mapped to the hardscapeos_dibbits file and line that registers
// it (or its drop-list.yaml addition entry for net-new routes).
import { readFileSync, readdirSync, statSync, writeFileSync } from "node:fs";
import { join, dirname, resolve, relative } from "node:path";
import { fileURLToPath } from "node:url";
import YAML from "yaml";

const apiDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const repoRoot = join(apiDir, "..", "..");
const erpRoot = resolve(
  process.env.HH_ERP_ROOT ?? process.argv[2] ?? join(repoRoot, "..", "hardscapeos_dibbits"),
);

// One pass over the ERP tree: route literal -> [{file, line}].
const ROUTE_RE = /"(GET|POST|PUT|PATCH|DELETE) (\/api[^"]*)"/g;
const sources = new Map();
function walk(dir) {
  for (const entry of readdirSync(dir)) {
    const p = join(dir, entry);
    if (statSync(p).isDirectory()) walk(p);
    else if (entry.endsWith(".go") && !entry.endsWith("_test.go")) {
      const lines = readFileSync(p, "utf8").split("\n");
      lines.forEach((line, i) => {
        for (const m of line.matchAll(ROUTE_RE)) {
          const route = `${m[1]} ${m[2]}`;
          const rel = relative(erpRoot, p);
          if (!sources.has(route)) sources.set(route, []);
          sources.get(route).push(`${rel}:${i + 1}`);
        }
      });
    }
  }
}
walk(join(erpRoot, "backend", "internal"));

const doc = YAML.parse(readFileSync(join(apiDir, "openapi.yaml"), "utf8"));
const ledger = YAML.parse(readFileSync(join(apiDir, "drop-list.yaml"), "utf8"));
const additions = new Map((ledger.additions ?? []).map((a) => [a.route, a.reason]));
const drops = new Map((ledger.drops ?? []).map((d) => [d.route, d.reason]));

const METHODS = new Set(["get", "put", "post", "delete", "patch"]);
const rows = [];
for (const [path, item] of Object.entries(doc.paths)) {
  for (const method of Object.keys(item)) {
    if (!METHODS.has(method)) continue;
    const route = `${method.toUpperCase()} ${path}`;
    let source;
    if (route.startsWith("")) {
      const erpRoute = route.replace(" /api/v1/tenant/", " /api/portal/v1/");
      const hits = sources.get(erpRoute) ?? sources.get(route);
      if (hits) {
        source = hits.map((h) => `hardscapeos_dibbits/${h}`).join(", ");
      } else if (additions.has(route)) {
        source = `(addition) ${additions.get(route)}`;
      } else {
        source = "UNMAPPED";
      }
    }
    rows.push([route, source]);
  }
}
rows.sort((a, b) => a[0].localeCompare(b[0]));

const table = rows.map(([r, s]) => `| \`${r}\` | ${s} |`).join("\n");

const readme = readFileSync(join(apiDir, "README.md"), "utf8");
const start = "<!-- SOURCE-MAP:BEGIN -->";
const end = "<!-- SOURCE-MAP:END -->";
const block = `${start}\n\n| Document route | ERP source |\n|---|---|\n${table}\n\n${end}`;
if (!readme.includes(start) || !readme.includes(end)) {
  console.error("README.md is missing the SOURCE-MAP markers");
  process.exit(1);
}
writeFileSync(join(apiDir, "README.md"), readme.replace(new RegExp(`${start}[\\s\\S]*?${end}`), block));

const unmapped = rows.filter(([, s]) => s === "UNMAPPED");
console.log(`source map: ${rows.length} routes mapped, ${unmapped.length} unmapped`);
if (unmapped.length) {
  for (const [r] of unmapped) console.log(`  UNMAPPED ${r}`);
  process.exit(1);
}
