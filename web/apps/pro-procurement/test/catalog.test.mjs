// Unit legs of the Catalog and Product surfaces against the shared tenant stub:
// the search reads tenantSearchCatalog (gated submit_rfq; a role without it is
// refused before any call), a product reads tenantGetCatalogProduct, and the
// add to a plan move lands the line on a plan the contractor can then see.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { catalogLoad, catalogView, catalogInput, CATALOG_WORDS } from "../src/catalog.ts";
import { productLoad, productView, productCommand, productAddToPlan } from "../src/product.ts";
import { InMemoryTenantSession, tenantClient, stubAdapter } from "@hardscapehouse/pro-kit";
import { PERSONAS, STUB_PASSWORD } from "@hardscapehouse/pro-kit/test-stub/tenant-data.mjs";
import { spawnStub } from "@hardscapehouse/pro-kit/test-stub/spawn-stub.mjs";

let stub;
let base;

before(async () => {
  stub = await spawnStub();
  base = stub.base;
});

after(async () => {
  await stub.stop();
});

function makePage(who, route) {
  const notices = [];
  const navigated = [];
  const assigns = [];
  const session = new InMemoryTenantSession({ adapter: stubAdapter({ baseUrl: base }), baseUrl: base });
  return session.signIn(PERSONAS[who].email, STUB_PASSWORD).then((signed) => {
    assert.equal(signed.ok, true, who);
    return {
      session,
      client: tenantClient(session, { baseUrl: base }),
      route,
      busy: false,
      field: () => null,
      goto: (next) => navigated.push(next),
      refresh: () => {},
      say: (kind, words) => notices.push({ kind, words }),
      hold: () => {},
      touch: () => {},
      assign: (fields) => assigns.push(fields),
      openModal: () => {},
      closeModal: () => {},
      notices,
      navigated,
      assigns,
    };
  });
}

function words(template) {
  if (template === null || template === undefined || template === false) return "";
  if (Array.isArray(template)) return template.map(words).join(" ");
  if (typeof template === "object" && "strings" in template) {
    return template.strings.map((s, i) => s + (i < template.values.length ? words(template.values[i]) : "")).join("");
  }
  return String(template);
}

test("a role without submit_rfq is refused before any call", async () => {
  const page = await makePage("field", { surface: "catalog" });
  assert.equal((await catalogLoad(page)).kind, "refusal");
});

test("the catalog answers a search with the dealer's products, and a miss with its own words", async () => {
  const page = await makePage("buyer", { surface: "catalog" });
  const first = await catalogLoad(page);
  assert.equal(first.kind, "ready");
  if (first.kind !== "ready") return;
  assert.equal(first.data.term, "", "a fresh catalog holds no search");
  const prompt = words(catalogView(first.data, page));
  assert.ok(prompt.includes(CATALOG_WORDS.empty), prompt);
  catalogInput("catalog-search", "paver", page);
  await new Promise((r) => setTimeout(r, 120));
  const searched = await catalogLoad(page);
  if (searched.kind !== "ready") throw new Error("the catalog did not hold the search");
  assert.equal(searched.data.term, "paver");
  assert.ok(searched.data.results.length >= 2, "two pavers at least");
  const text = words(catalogView(searched.data, page)).replace(/<[^>]*>/g, " ");
  assert.ok(text.includes("Sandstone paver 6 x 9"), text);
  assert.ok(text.includes("CPV-1204"), "the sku is missing");
  assert.doesNotMatch(text, /undefined|NaN|!/);
  catalogInput("catalog-search", "zzzznothing", page);
  await new Promise((r) => setTimeout(r, 120));
  const missed = await catalogLoad(page);
  if (missed.kind !== "ready") throw new Error("the catalog did not hold the search");
  assert.equal(missed.data.results.length, 0);
  assert.ok(words(catalogView(missed.data, page)).includes(CATALOG_WORDS.none));
});

test("a product opens with what the core serves: what it is, how it is sold, whether the yard has it", async () => {
  const page = await makePage("pm", { surface: "product", productId: "a10000-0000-4000-8000-000000000001" });
  const answer = await productLoad(page);
  assert.equal(answer.kind, "ready", JSON.stringify(answer));
  if (answer.kind !== "ready") return;
  assert.equal(answer.heading, "Sandstone paver 6 x 9");
  const text = words(productView(answer.data, page)).replace(/<[^>]*>/g, " ");
  assert.ok(text.includes("CPV-1204"), text);
  assert.ok(text.includes("Sold by"), text);
  assert.ok(text.includes("SF"), text);
  assert.ok(text.includes("In stock at the yard"), text);
  assert.ok(text.includes("Add to a plan"), text);
  assert.ok(text.includes("Copperline Pavers"), text);
  assert.doesNotMatch(text, /\b[A-Z]{2,}_[A-Z]{2,}\b/, "a stored status string reached the screen");
  assert.doesNotMatch(text, /undefined|NaN|!/);
  // The catalog carries no price for a product: pricing is the dealer's, at the RFQ and on the quote.
  assert.equal(/\$\s?[0-9]/.test(text), false, "a product page must not show a price the core does not serve");
});

test("a product that is gone reads as the surface's empty words", async () => {
  const page = await makePage("pm", { surface: "product", productId: "a10000-0000-4000-8000-999999999999" });
  const answer = await productLoad(page);
  assert.equal(answer.kind, "empty");
});

test("add to a plan lands the line on the plan the person chose", async () => {
  const page = await makePage("owner", { surface: "product", productId: "a10000-0000-4000-8000-000000000004" });
  const done = await productAddToPlan(page, { planId: "9a0000-0000-4000-8000-000000000001" }, "12", "EA");
  assert.equal(done.ok, true, JSON.stringify(done));
  assert.equal(done.projectId, "9a0000-0000-4000-8000-000000000001");
  assert.equal(done.projectName, "La Jolla terrace pavers");
  const peek = await makePage("owner", { surface: "scope", projectId: "9a0000-0000-4000-8000-000000000001" });
  const answer = await scopePeek(peek);
  assert.equal(answer.kind, "ready");
  if (answer.kind === "ready") {
    const added = answer.data.lines.find((l) => l.qty_entered === "12" && l.uom === "EA");
    assert.ok(added, "the line did not land on the plan");
  }
});

test("add to a new plan creates the plan, puts the line on it, and opens it", async () => {
  const page = await makePage("owner", { surface: "product", productId: "a10000-0000-4000-8000-000000000006" });
  const done = await productAddToPlan(page, { newPlanName: "Soil bins" }, "2", "CY");
  assert.equal(done.ok, true, JSON.stringify(done));
  assert.ok(done.projectId, "no plan was created");
  const check = await makePage("owner", { surface: "scope", projectId: done.projectId });
  const answer = await scopePeek(check);
  assert.equal(answer.kind, "ready");
  if (answer.kind === "ready") {
    assert.equal(answer.data.lines.length, 1);
    assert.equal(answer.data.lines[0].qty_entered, "2");
    assert.equal(answer.data.lines[0].uom, "CY");
  }
});

test("the product command answers only its own actions", async () => {
  const page = await makePage("owner", { surface: "product", productId: "a10000-0000-4000-8000-000000000001" });
  await productLoad(page);
  assert.equal(productCommand("add-to-plan-open", { getAttribute: () => null }, page), true);
  assert.equal(productCommand("no-such-action", { getAttribute: () => null }, page), false);
});

async function scopePeek(page) {
  const { scopeLoad } = await import("../src/scope.ts");
  return scopeLoad(page);
}
