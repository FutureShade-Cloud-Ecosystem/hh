#!/usr/bin/env node
// The lane acceptance suite for core/api: assemble, then check parity,
// fixtures, the state map, and the house rules (no em or en dashes in any
// core/api file). Each step fails the run.
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
step("route parity", ["parity.mjs"]);
step("fixture validation", ["fixtures.mjs"]);
step("state map coverage", ["statemap.mjs"]);

// D8 isolation: dealer_quote and customer_quote share only quote_line by
// schema reference, and neither carries a kind field.
process.stdout.write("== D8 quote isolation\n");
{
  const YAML = (await import("yaml")).default;
  const doc = YAML.parse(readFileSync(join(apiDir, "openapi.yaml"), "utf8"));
  const schemas = doc.components.schemas;
  const refsOf = (name) => {
    const found = new Set();
    const walk = (node) => {
      if (Array.isArray(node)) return node.forEach(walk);
      if (node && typeof node === "object") {
        for (const [k, v] of Object.entries(node)) {
          if (k === "$ref" && typeof v === "string") {
            const m = /#\/components\/schemas\/(.+)$/.exec(v);
            if (m) found.add(m[1]);
          } else walk(v);
        }
      }
    };
    walk(schemas[name]);
    found.delete(name);
    return found;
  };
  const dealer = refsOf("DealerQuote");
  const customer = refsOf("CustomerQuote");
  const shared = [...dealer].filter((r) => customer.has(r));
  const problems = [];
  if (JSON.stringify(shared) !== JSON.stringify(["QuoteLine"]))
    problems.push(`DealerQuote and CustomerQuote share ${JSON.stringify(shared)}, expected only QuoteLine`);
  if (schemas.DealerQuote?.properties?.kind !== undefined) problems.push("DealerQuote has a kind property");
  if (schemas.CustomerQuote?.properties?.kind !== undefined) problems.push("CustomerQuote has a kind property");
  if (problems.length) {
    for (const p of problems) console.error(`  - ${p}`);
    process.exit(1);
  }
  console.log("D8: dealer_quote and customer_quote share only quote_line by schema reference; no kind field");
}

// House rules scan over every text file in core/api (tools output included).
const offenders = [];
function scan(dir) {
  for (const entry of readdirSync(dir)) {
    if (entry === "node_modules") continue;
    const p = join(dir, entry);
    if (statSync(p).isDirectory()) scan(p);
    else if (/\.(yaml|yml|md|mjs|json)$/.test(entry)) {
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
