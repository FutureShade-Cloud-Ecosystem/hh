#!/usr/bin/env node
// Fixture validation: every recorded live HH Pro fixture in
// hh_pro_dibbits/src/core/supplier/__tests__/fixtures-live/ must validate
// against the tenant schema the contract assigns to its route. A fixture
// that does not validate is a finding to record, never a schema to loosen;
// this script fails until the finding is resolved in the schema or recorded
// as a known divergence in KNOWN_FINDINGS below.
//
// Usage: node fixtures.mjs [path-to-hh_pro_dibbits]
import { readFileSync, readdirSync } from "node:fs";
import { join, dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import YAML from "yaml";
import Ajv2020 from "ajv/dist/2020.js";

const apiDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const repoRoot = join(apiDir, "..", "..");
const proRoot = resolve(
  process.env.HH_PRO_ROOT ?? process.argv[2] ?? join(repoRoot, "..", "hh_pro_dibbits"),
);
const fixturesDir = join(proRoot, "src/core/supplier/__tests__/fixtures-live");

const doc = YAML.parse(readFileSync(join(apiDir, "openapi.yaml"), "utf8"));

// Fixture file -> the schema of the route it was recorded from.
const MAP = {
  "login.json": "TenantLoginResponse",
  "refresh.json": "TenantRefreshResponse",
  "me.json": "TenantMe",
  "config.json": "TenantConfig",
  "dashboard.json": "TenantDashboard",
  "billing-summary.json": "TenantBillingSummary",
  "catalog-search-paver.json": "TenantCatalogSearchResponse",
  "catalog-search-sand.json": "TenantCatalogSearchResponse",
  "invoices-page.json": "TenantInvoicePage",
  "invoice-detail.json": "TenantInvoiceDetail",
  "orders-page.json": "TenantOrderPage",
  "order-detail.json": "TenantOrderDetail",
  "quotes-page.json": "TenantQuotePage",
  "quote-detail.json": "TenantQuoteDetail",
  "error-401.json": "Error",
  "error-404.json": "Error",
};

// Recorded divergences: a fixture that cannot validate against the truthful
// schema, with the reason. Empty means every fixture validates.
const KNOWN_FINDINGS = {};

const ajv = new Ajv2020({ strict: false, validateFormats: false, allErrors: true });
ajv.addSchema({ $id: "hh", components: { schemas: doc.components.schemas } });

const problems = [];
const files = readdirSync(fixturesDir).filter((f) => f.endsWith(".json")).sort();
for (const file of files) {
  const schemaName = MAP[file];
  if (!schemaName) {
    problems.push(`${file}: no schema mapping`);
    continue;
  }
  if (!doc.components.schemas[schemaName]) {
    problems.push(`${file}: schema ${schemaName} not in the document`);
    continue;
  }
  const data = JSON.parse(readFileSync(join(fixturesDir, file), "utf8"));
  const validate = ajv.compile({ $ref: `hh#/components/schemas/${schemaName}` });
  const ok = validate(data);
  if (!ok) {
    const detail = validate.errors
      .map((e) => `${e.instancePath || "/"} ${e.message}`)
      .join("; ");
    if (KNOWN_FINDINGS[file]) {
      console.log(`finding (recorded): ${file}: ${KNOWN_FINDINGS[file]}`);
    } else {
      problems.push(`${file} does not validate against ${schemaName}: ${detail}`);
    }
  }
}

if (problems.length) {
  console.error(`fixture validation failed with ${problems.length} problem(s):`);
  for (const p of problems) console.error(`  - ${p}`);
  process.exit(1);
}
console.log(`fixtures: ${files.length} recorded captures validated against the tenant schemas`);
