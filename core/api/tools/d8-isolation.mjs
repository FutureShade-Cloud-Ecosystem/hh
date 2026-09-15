#!/usr/bin/env node
// D8 isolation: dealer_quote and customer_quote share only quote_line by
// schema reference, and neither carries a kind field. Extracted from
// check-all.mjs so ci can run it without the sibling checkouts that the
// parity and fixture steps need.
import { readFileSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import YAML from "yaml";

const apiDir = join(dirname(fileURLToPath(import.meta.url)), "..");
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
