// Stage chips (Plan, Quote, Order, Invoice) and status chips: platform owned
// colours through design system tokens, never the dealer's gold, and never
// colour alone (every chip carries its word).
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { STAGES, STAGE_WORDS, statusWords, statusTone, chipStyles } from "../src/chips.ts";

test("the four stages are Plan, Quote, Order, Invoice, in that order, in sentence case", () => {
  assert.deepEqual([...STAGES], ["plan", "quote", "order", "invoice"]);
  assert.deepEqual(STAGES.map((s) => STAGE_WORDS[s]), ["Plan", "Quote", "Order", "Invoice"]);
});

test("every status the core can send has a word and a tone", () => {
  const types = readFileSync(fileURLToPath(new URL("../../api-client/src/gen/types.ts", import.meta.url)), "utf8");
  const unionOf = (re) => [...re.exec(types)[1].matchAll(/"(\w+)"/g)].map((m) => m[1]);
  const wanted = {
    project: unionOf(/export type TenantProjectStatus = ([^;]+);/),
    order: unionOf(/export type TenantOrder = \{[\s\S]*?status: ([^;]+);/),
    invoice: unionOf(/export type TenantInvoice = \{[\s\S]*?status: ([^;]+);/),
    quote: unionOf(/export type TenantQuote = \{[\s\S]*?status: ([^;]+);/),
    payment: unionOf(/export type TenantPayment = \{[\s\S]*?status: ([^;]+);/),
    team: unionOf(/export type TenantUserStatus = ([^;]+);/),
  };
  for (const [kind, statuses] of Object.entries(wanted)) {
    assert.ok(statuses.length > 0, `${kind} statuses were not found in the generated types`);
    for (const status of statuses) {
      const words = statusWords(kind, status);
      assert.notEqual(words, "Unknown", `${kind} ${status} has no word`);
      assert.doesNotMatch(words, /[_A-Z]{3,}/, `${kind} ${status} reads as a stored string`);
      assert.ok(["neutral", "good", "watch", "stop"].includes(statusTone(kind, status)));
    }
  }
});

test("an unknown status reads as a plain word and a neutral tone, never the raw string", () => {
  assert.equal(statusWords("order", "SOMETHING_NEW"), "Unknown");
  assert.equal(statusTone("order", "SOMETHING_NEW"), "neutral");
  assert.equal(statusWords("nonsense", "OPEN"), "Unknown");
});

test("the tones follow the money: paid is good, a bounced payment stops, a part paid invoice watches", () => {
  assert.equal(statusTone("invoice", "PAID"), "good");
  assert.equal(statusTone("payment", "BOUNCED"), "stop");
  assert.equal(statusTone("invoice", "PARTIALLY_PAID"), "watch");
  assert.equal(statusTone("order", "CANCELLED"), "stop");
});

test("the chip sheet uses design tokens only, and never the dealer's gold or brand layer", () => {
  const css = String(chipStyles.cssText ?? chipStyles);
  assert.doesNotMatch(css, /#[0-9a-fA-F]{3,8}\b/, "a hex colour is not a token");
  assert.doesNotMatch(css, /\brgba?\(|\bhsla?\(|\boklch\(/);
  assert.doesNotMatch(css, /--hh-accent|--cbrand|gold/i, "gold is the dealer's, a status is the platform's");
  for (const stage of STAGES) assert.match(css, new RegExp(`stage-${stage}`));
});
