#!/usr/bin/env node
// Refresh the vendored ERP route list (D22 in docs/analysis/DECISIONS.md):
// run the route grep over a hardscapeos_dibbits checkout, apply the portal
// rename, and rewrite core/api/erp-routes.txt behind a provenance header
// naming the ERP commit it was taken from. This is the tracking lane's tool
// (HH-3.7): ci checks the contract against the vendored snapshot, and this
// script moves the snapshot to the ERP head.
//
// parity.mjs imports the grep, the rename and the reader from here, so the
// vendored file and a run against a live checkout are produced one way only.
//
// Usage: node refresh-erp-routes.mjs [path-to-hardscapeos_dibbits]
// Env: HH_ERP_ROOT overrides the default (<hh repo root>/../hardscapeos_dibbits).
import { readFileSync, writeFileSync } from "node:fs";
import { join, dirname, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { execFileSync } from "node:child_process";

const apiDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const repoRoot = join(apiDir, "..", "..");

export const ROUTES_FILE = join(apiDir, "erp-routes.txt");

// The grep analysis/hardscapeos-inventory.md section 2.6 used, over non-test
// Go files.
const GREP_ARGS = [
  "-rhoE",
  '"(GET|POST|PUT|PATCH|DELETE) /api[^"]*"',
  "backend/internal",
  "--include=*.go",
  "--exclude=*_test.go",
];

const RENAME = [["/api/portal/v1/", "/api/v1/tenant/"]];
const rename = (route) => {
  for (const [from, to] of RENAME) {
    if (route.includes(` ${from}`)) return route.replace(` ${from}`, ` ${to}`);
  }
  return route;
};

// The ERP routes in a live checkout: grepped, unquoted, renamed,
// deduplicated, in byte order.
export function routesFromCheckout(erpRoot) {
  const raw = execFileSync("grep", GREP_ARGS, { cwd: erpRoot, encoding: "utf8" });
  const routes = raw
    .split("\n")
    .map((l) => l.trim().replace(/^"|"$/g, ""))
    .filter(Boolean)
    .map(rename);
  return [...new Set(routes)].sort();
}

// The ERP routes in a vendored list: every line that is neither blank nor a
// # comment.
export function readVendoredRoutes(file = ROUTES_FILE) {
  return readFileSync(file, "utf8")
    .split("\n")
    .map((l) => l.trim())
    .filter((l) => l && !l.startsWith("#"));
}

const shellWord = (arg) => (/^[\w\-.=/*]+$/.test(arg) ? arg : `'${arg}'`);

function main() {
  const erpRoot = resolve(
    process.env.HH_ERP_ROOT ?? process.argv[2] ?? join(repoRoot, "..", "hardscapeos_dibbits"),
  );
  const git = (...args) =>
    execFileSync("git", args, { cwd: erpRoot, encoding: "utf8" }).trim();

  // The header names a commit, so the grepped tree must be exactly that commit.
  const dirty = git("status", "--porcelain", "--", "backend/internal");
  if (dirty) {
    console.error(
      `refresh-erp-routes: ${erpRoot} has uncommitted or untracked files under backend/internal, ` +
        `so no commit describes what the grep would read:\n${dirty}`,
    );
    process.exit(1);
  }
  const commit = git("rev-parse", "HEAD");
  const routes = routesFromCheckout(erpRoot);

  const header = [
    "# ERP route list vendored for route parity (D22 in docs/analysis/DECISIONS.md).",
    `# Taken from hardscapeos_dibbits at commit ${commit}.`,
    "# Produced by: node core/api/tools/refresh-erp-routes.mjs <path-to-hardscapeos_dibbits>",
    "# which runs, in that checkout:",
    `#   grep ${GREP_ARGS.map(shellWord).join(" ")}`,
    "# then strips the quotes, renames /api/portal/v1/X to /api/v1/tenant/X,",
    "# deduplicates, and sorts in byte order.",
    "# Never edit by hand; the tracking lane (HH-3.7) refreshes it from the ERP head.",
    `# ${routes.length} routes.`,
  ];
  writeFileSync(ROUTES_FILE, [...header, ...routes].join("\n") + "\n");
  console.log(`erp routes: ${routes.length} routes from hardscapeos_dibbits at ${commit}`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main();
}
