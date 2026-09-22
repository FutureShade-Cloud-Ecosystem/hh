#!/usr/bin/env node
// The lane acceptance suite for core/api: assemble, then check parity,
// fixtures, the state map, D8 quote isolation, and the house rules (no em
// or en dashes in any core/api file). Each step fails the run.
//
// Parity and fixtures read the inputs vendored under D22
// (core/api/erp-routes.txt and core/api/fixtures-live/), so the suite needs
// no sibling checkout. HH_ERP_ROOT or HH_PRO_ROOT in the environment points
// the matching step at a live sibling instead.
import { execFileSync } from "node:child_process";
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const toolsDir = dirname(fileURLToPath(import.meta.url));
const apiDir = join(toolsDir, "..");

function step(name, args) {
  process.stdout.write(`== ${name}\n`);
  execFileSync("node", [join(toolsDir, ...args)], { stdio: "inherit" });
}

step("merge", ["merge.mjs"]);
step("generated server types", ["gen-types.mjs"]);
step("route parity", ["parity.mjs"]);
step("fixture validation", ["fixtures.mjs"]);
step("state map coverage", ["statemap.mjs"]);

step("D8 quote isolation", ["d8-isolation.mjs"]);

// House rules scan over every text file in core/api (tools output included).
const offenders = [];
function scan(dir) {
  for (const entry of readdirSync(dir)) {
    if (entry === "node_modules") continue;
    const p = join(dir, entry);
    if (statSync(p).isDirectory()) scan(p);
    else if (/\.(yaml|yml|md|mjs|json|txt)$/.test(entry)) {
      const text = readFileSync(p, "utf8");
      const lines = text.split("\n");
      lines.forEach((line, i) => {
        if (new RegExp("[\\u2014\\u2013]").test(line)) offenders.push(`${p}:${i + 1}`);
      });
    }
  }
}
process.stdout.write("== house rules scan\n");
scan(apiDir);
if (offenders.length) {
  console.error("em or en dashes found:");
  for (const o of offenders) console.error(`  - ${o}`);
  process.exit(1);
}
console.log("house rules: no em or en dashes anywhere in core/api");
console.log("all checks passed");
