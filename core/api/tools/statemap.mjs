#!/usr/bin/env node
// State map coverage (D7): the OrderCard x-state-map must be total in both
// directions. Every value in the dealer order status CHECK set must map to a
// contractor stage, and every value in HH Pro's stage union must name at
// least one source. The two vocabularies are quoted here from their sources
// so the test does not depend on reading the other repositories:
//   dealer CHECK set: hardscapeos_dibbits migration 0008_quotes_orders.sql
//   stage union:      hh_pro_dibbits/src/core/domain/stage.ts (STAGE_INDEX)
//   tracking union:   hh_pro_dibbits/src/core/domain/supplier.ts (SalesOrderStatus)
import { readFileSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import YAML from "yaml";

const apiDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const doc = YAML.parse(readFileSync(join(apiDir, "openapi.yaml"), "utf8"));

const DEALER_ORDER_STATES = [
  "DRAFT",
  "CONFIRMED",
  "PICKING",
  "READY",
  "PARTIALLY_FULFILLED",
  "FULFILLED",
  "INVOICED",
  "CLOSED",
  "CANCELLED",
];
const STAGES = ["plan", "quote", "order", "invoice"];
const TRACKING = [
  "submitted",
  "confirmed",
  "picking",
  "ready-willcall",
  "out-for-delivery",
  "delivered",
  "invoiced",
  "cancelled",
];

const map = doc.components.schemas.OrderCard?.["x-state-map"];
const problems = [];
if (!map) {
  problems.push("OrderCard carries no x-state-map");
} else {
  const dealerToStage = map.dealer_order_to_stage ?? {};
  for (const state of DEALER_ORDER_STATES) {
    if (!(state in dealerToStage)) problems.push(`dealer order state unmapped: ${state}`);
    else if (!STAGES.includes(dealerToStage[state]))
      problems.push(`dealer order state ${state} maps to unknown stage ${dealerToStage[state]}`);
  }
  for (const extra of Object.keys(dealerToStage)) {
    if (!DEALER_ORDER_STATES.includes(extra))
      problems.push(`state map names a state outside the dealer CHECK set: ${extra}`);
  }
  const stageSources = map.stage_sources ?? {};
  for (const stage of STAGES) {
    const sources = stageSources[stage];
    if (!Array.isArray(sources) || sources.length === 0)
      problems.push(`contractor stage has no source in the map: ${stage}`);
  }
  for (const extra of Object.keys(stageSources)) {
    if (!STAGES.includes(extra)) problems.push(`stage_sources names a stage outside the union: ${extra}`);
  }
  const tracking = map.tracking_map ?? {};
  for (const status of TRACKING) {
    if (!(status in tracking)) problems.push(`tracking vocabulary unmapped: ${status}`);
  }
  for (const extra of Object.keys(tracking)) {
    if (!TRACKING.includes(extra)) problems.push(`tracking map names a status outside the union: ${extra}`);
  }
}

// The card's own stage enum must equal the stage union, and supplier_status
// must equal the tracking union (plus null).
const card = doc.components.schemas.OrderCard ?? {};
const stageEnum = card.properties?.stage?.enum ?? [];
if (JSON.stringify([...stageEnum].sort()) !== JSON.stringify([...STAGES].sort()))
  problems.push(`OrderCard.stage enum ${JSON.stringify(stageEnum)} is not the stage union`);
const supplierEnum = (card.properties?.supplier_status?.enum ?? []).filter((v) => v !== null);
if (JSON.stringify([...supplierEnum].sort()) !== JSON.stringify([...TRACKING].sort()))
  problems.push(`OrderCard.supplier_status enum is not the tracking union`);

if (problems.length) {
  console.error(`state map coverage failed with ${problems.length} problem(s):`);
  for (const p of problems) console.error(`  - ${p}`);
  process.exit(1);
}
console.log(
  `state map: ${DEALER_ORDER_STATES.length} dealer order states and ${STAGES.length} contractor stages covered, no unmapped state on either side`,
);
