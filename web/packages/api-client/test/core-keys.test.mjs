// The contract says what the core writes. For every list the core answers under a domain key or as a bare
// array (never as a page of items), the generated success schema must declare that key (or be an array), and
// the core's own write site must still be the one quoted here, so a fix on either side turns this red. The
// shortfall worklist (FROM-PRINCIPAL 72) moved the core to the contract; these eighteen reads and the tax
// reason vocabulary moved the contract to the core, the other way round, for the same reason: the app meets
// the core, so the contract is held to it.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { SUCCESS_SCHEMAS } from "../src/gen/manifest.ts";
import { SCHEMA_DESCRIPTORS } from "../src/testing.ts";

const core = join(dirname(fileURLToPath(import.meta.url)), "..", "..", "..", "..", "core");
const source = (file) => readFileSync(join(core, "internal", file), "utf8");

// operation, the list schema, the key the core writes (null for a bare array), the row schema, the write site
const KEYED = [
  ["inventoryOnHand", "InventoryOnHandList", "on_hand", "InventoryOnHand", "inventory/handler.go", '"on_hand": rows'],
  ["inventoryListMoves", "InventoryMoveList", "moves", "InventoryStockMove", "inventory/handler.go", '"moves": rows'],
  ["inventoryListPendingFulfilment", "InventoryPendingFulfilmentList", "pending", "InventoryPendingFulfilment", "inventory/handler.go", '"pending": rows'],
  ["inventoryBranchStock", "InventoryBranchStockList", "branches", "InventoryBranchStock", "inventory/stock_truth_handler.go", '"branches": rows'],
  ["inventoryListReservations", "InventoryReservationList", "reservations", "InventoryReservation", "inventory/stock_truth_handler.go", '"reservations": rows'],
  ["inventoryListTransfers", "InventoryTransferList", "transfers", "InventoryTransfer", "inventory/stock_truth_handler.go", '"transfers": rows'],
  ["inventoryListIncomingTransfers", "InventoryInTransitLineList", "lines", "InventoryInTransitLine", "inventory/stock_truth_handler.go", '"lines": lines'],
  ["inventoryListTransitDays", "InventoryTransitDaysPairList", "pairs", "InventoryTransitDaysPair", "inventory/stock_truth_handler.go", '"pairs": pairs'],
  ["inventoryListReorderPoints", "InventoryReorderPointList", "reorder_points", "InventoryReorderPoint", "inventory/stock_truth_handler.go", '"reorder_points": rows'],
  ["taxListRates", "TaxRateList", "rates", "TaxRate", "tax/handler.go", '"rates": rows'],
  ["taxListProfiles", "TaxStatusCardProfileList", "profiles", "TaxStatusCardProfile", "tax/handler.go", '"profiles": rows'],
  ["taxReviewQueue", "TaxPendingProfileList", "pending_profiles", "TaxStatusCardProfile", "tax/handler.go", '"pending_profiles": pending'],
  ["taxListEvidence", "TaxEvidenceList", "evidence", "TaxEvidence", "tax/handler.go", '"evidence": rows'],
];

const BARE = [
  ["bankrecListAccounts", "BankRecAccountList", "BankRecAccount", "httpx.WriteJSON(w, http.StatusOK, accts)"],
  ["bankrecListMatches", "BankRecMatchList", "BankRecMatch", "httpx.WriteJSON(w, http.StatusOK, matches)"],
  ["bankrecListRules", "BankRecMatchRuleList", "BankRecMatchRule", "httpx.WriteJSON(w, http.StatusOK, items)"],
  ["bankrecListReconciliations", "BankRecReconciliationList", "BankRecReconciliation", "httpx.WriteJSON(w, http.StatusOK, items)"],
];

for (const [operation, schema, key, row, file, site] of KEYED) {
  test(`${operation} is declared under the core's ${key} key, not as a page of items`, () => {
    assert.equal(SUCCESS_SCHEMAS[operation], schema);
    const d = SCHEMA_DESCRIPTORS[schema];
    assert.deepEqual(d.req, [key]);
    assert.deepEqual(Object.keys(d.props), [key], "no items, no cursor, no limit: the core writes none");
    const list = d.props[key];
    const leg = list.k === "union" ? list.of.find((l) => l.k !== "null") : list;
    assert.equal(leg.k, "array");
    assert.deepEqual(leg.of, { k: "ref", name: row });
    assert.ok(source(file).includes(site), `the core no longer writes ${key} at ${file} (${site})`);
  });
}

for (const [operation, schema, row, site] of BARE) {
  test(`${operation} is declared as the bare array the core writes`, () => {
    assert.equal(SUCCESS_SCHEMAS[operation], schema);
    assert.deepEqual(SCHEMA_DESCRIPTORS[schema], { k: "array", of: { k: "ref", name: row } });
    assert.ok(source("bankrec/handler.go").includes(site), `the core no longer writes this list as a bare array (${site})`);
  });
}

test("browse is declared as the core's rows, facets, total and echoed page", () => {
  assert.equal(SUCCESS_SCHEMAS.inventoryBrowse, "InventoryBrowseResult");
  const d = SCHEMA_DESCRIPTORS.InventoryBrowseResult;
  assert.deepEqual(d.req, ["rows", "facets", "total", "limit", "offset"]);
  assert.deepEqual(Object.keys(d.props), ["rows", "facets", "total", "limit", "offset"]);
  assert.match(source("inventory/browse.go"), /Rows\s+\[\]BrowseRow\s+`json:"rows"`/);
  assert.match(source("inventory/browse.go"), /Total\s+int\s+`json:"total"`/);
});

test("the rule spec declares every field the matcher reads", () => {
  const props = Object.keys(SCHEMA_DESCRIPTORS.BankRecRuleSpec.props).sort();
  const read = [...source("bankrec/repository.go").matchAll(/spec(?:Str|Int|Bool)\(s, "(\w+)"\)/g)].map((m) => m[1]);
  assert.deepEqual(props, [...read].sort());
});

test("the tax reason vocabulary is the twelve the snapshot CHECK carries and the core accepts", () => {
  const declared = SCHEMA_DESCRIPTORS.TaxRate.props.reason_code;
  const enumDescriptor = declared.k === "ref" ? SCHEMA_DESCRIPTORS[declared.name] : declared;
  const model = [...source("tax/model.go").matchAll(/\bReason\w+\s*=\s*"([A-Z0-9_]+)"/g)].map((m) => m[1]);
  // 0029 defined the CHECK; 0391 dropped and widened it (STD_SALES_TAX, the neutral standard rate),
  // so 0391's ADD CONSTRAINT is the live universe the migration lawfully carries.
  const migration = readFileSync(join(core, "db", "migrations", "0391_tax_neutral_standard_reason.sql"), "utf8");
  const check = migration.slice(migration.indexOf("CHECK (tax_reason_code IN ("));
  const checked = [...check.slice(0, check.indexOf("))")).matchAll(/'([A-Z0-9_]+)'/g)].map((m) => m[1]);
  assert.equal(model.length, 12);
  assert.deepEqual([...enumDescriptor.v].sort(), [...model].sort());
  assert.deepEqual([...enumDescriptor.v].sort(), [...checked].sort());
});
