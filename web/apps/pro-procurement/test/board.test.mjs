// Unit leg of the Board surface against the shared tenant stub: the board is
// the kit's projection (board.ts, pinned to the contract's OrderCard state
// map) over the three lists the core serves: planning projects (Plan), the
// dealer's quotes (Quote), and the dealer's orders (Order and Invoice). The
// loader reads the three routes; a role without submit_rfq is refused before
// any call. The view words: the column stage words, the cards' figures as the
// core returned them, never a stored status string, never a money sum computed
// here.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { boardLoad, boardView, BOARD_EMPTY } from "../src/board.ts";
import { InMemoryTenantSession, tenantClient, stubAdapter, money } from "@hardscapehouse/pro-kit";
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

async function pageFor(who) {
  const session = new InMemoryTenantSession({ adapter: stubAdapter({ baseUrl: base }), baseUrl: base });
  const signed = await session.signIn(PERSONAS[who].email, STUB_PASSWORD);
  assert.equal(signed.ok, true, who);
  return {
    session,
    client: tenantClient(session, { baseUrl: base }),
    route: { surface: "board" },
    busy: false,
    field: () => null,
    goto: () => {},
    refresh: () => {},
    say: () => {},
    hold: () => {},
    touch: () => {},
    openModal: () => {},
    closeModal: () => {},
  };
}

function words(template) {
  if (template === null || template === undefined || template === false) return "";
  if (Array.isArray(template)) return template.map(words).join(" ");
  if (typeof template === "object" && "strings" in template) {
    return template.strings.map((s, i) => s + (i < template.values.length ? words(template.values[i]) : "")).join("");
  }
  return String(template);
}

test("a role without submit_rfq is refused before any call is made", async () => {
  const page = await pageFor("field");
  const answer = await boardLoad(page);
  assert.equal(answer.kind, "refusal");
});

test("a role holding submit_rfq reads the three lists the board projects", async () => {
  const page = await pageFor("owner");
  const answer = await boardLoad(page);
  assert.equal(answer.kind, "ready", JSON.stringify(answer));
  if (answer.kind !== "ready") return;
  assert.ok(answer.data.projects.length > 0);
  assert.ok(answer.data.quotes.length > 0);
  assert.ok(answer.data.orders.length > 0);
});

test("the board groups by the contract's stage map: Plan, Quote, Order, Invoice", async () => {
  const page = await pageFor("buyer");
  const answer = await boardLoad(page);
  assert.equal(answer.kind, "ready");
  if (answer.kind !== "ready") return;
  const text = words(boardView(answer.data, page)).replace(/<[^>]*>/g, " ");
  // Plan: the two PLANNING projects; a QUOTING or ORDERED project has no stage of its own.
  assert.ok(text.includes("La Jolla terrace pavers"), text);
  assert.ok(text.includes("Backyard planting beds"), text);
  assert.ok(text.includes("Santa Fe courtyard stone") === false, "a QUOTING project has no stage and must not be guessed onto the board");
  // Quote: the draft and sent quotes only.
  assert.ok(text.includes("Q-7001"), text);
  assert.ok(text.includes("Q-7003"), text);
  assert.ok(text.includes("Q-7002") === false, "an ACCEPTED quote has no quote stage");
  // Order: the confirmed through fulfilled orders.
  assert.ok(text.includes("SO-30506"), text);
  assert.ok(text.includes("Wanted by"), "the order card says Wanted by");
  assert.ok(text.includes("No lines yet") && !text.includes("Awaiting price") && !text.includes("n/a"), "an empty plan says No lines yet");
  assert.ok(text.includes("SO-30504"), text);
  // Invoice: the invoiced order.
  assert.ok(text.includes("SO-30501"), text);
  assert.ok(text.includes("Invoiced"), text);
  assert.ok(text.includes("Ready for pickup"), "the will call order tracks in words");
  assert.doesNotMatch(text, /\b[A-Z]{2,}_[A-Z]{2,}\b/, "a stored status string reached the screen");
  assert.doesNotMatch(text, /undefined|null|NaN|!/);
});

test("the board line counts what is in motion, and never sums money on the page", async () => {
  const page = await pageFor("pm");
  const answer = await boardLoad(page);
  assert.equal(answer.kind, "ready");
  if (answer.kind !== "ready") return;
  const text = words(boardView(answer.data, page)).replace(/<[^>]*>/g, " ");
  assert.match(text, /2 plans/, text);
  assert.match(text, /2 quotes/, text);
  assert.match(text, /5 open orders/, text);
});

test("an empty world shows the board's own empty words", async () => {
  const page = await pageFor("owner");
  await fetch(`${base}/_stub/control`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ empty: "orders" }) });
  await fetch(`${base}/_stub/control`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ empty: "quotes" }) });
  await fetch(`${base}/_stub/control`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ empty: "projects" }) });
  const answer = await boardLoad(page);
  assert.equal(answer.kind, "empty");
  assert.match(BOARD_EMPTY, /^Nothing is in motion/);
  await fetch(`${base}/_stub/reset`, { method: "POST" });
});

test("board columns delegate order stages to stageOfOrder without a private status list", () => {
  const source = readFileSync(fileURLToPath(new URL("../src/board.ts", import.meta.url)), "utf8");
  assert.doesNotMatch(source, /\bORDERED\b/, "board.ts must not carry a private ORDERED status list");
  assert.match(source, /stageOfOrder\(o\.status\)\s*===\s*"order"/, "Order column must filter by stageOfOrder === order");
  assert.match(source, /stageOfOrder\(o\.status\)\s*===\s*"invoice"/, "Invoice column must filter by stageOfOrder === invoice");
});

test("boardLoad walks cursor pages for quotes and orders so older cards are not lost", async () => {
  const page = await pageFor("buyer");
  page.client = tenantClient(page.session, {
    baseUrl: base,
    fetch: (input, init) => {
      const url = new URL(typeof input === "string" ? input : input.url);
      if (url.pathname === "/api/v1/tenant/quotes" || url.pathname === "/api/v1/tenant/orders") {
        url.searchParams.set("limit", "2");
      }
      return fetch(url, init);
    },
  });
  const answer = await boardLoad(page);
  assert.equal(answer.kind, "ready");
  if (answer.kind !== "ready") return;
  assert.equal(answer.data.quotes.length, 4, "all quote pages must be read");
  assert.equal(answer.data.orders.length, 6, "all order pages must be read");
});

test("Buy again is gated on create_orders rather than view_orders_deliveries, and app-root carries no unused can import", async () => {
  const page = await pageFor("buyer");
  const answer = await boardLoad(page);
  assert.equal(answer.kind, "ready");
  if (answer.kind !== "ready") return;
  const withCreate = words(boardView(answer.data, page));
  assert.ok(/data-action="open-reorder"/.test(withCreate), "a role with create_orders is offered Buy again");

  const withoutCreatePage = {
    ...page,
    session: {
      can: (cap) => cap === "submit_rfq" || cap === "view_orders_deliveries",
    },
  };
  const withoutCreate = words(boardView(answer.data, withoutCreatePage));
  assert.equal(/data-action="open-reorder"/.test(withoutCreate), false, "a role without create_orders must not be offered Buy again");

  const appRootSource = readFileSync(fileURLToPath(new URL("../src/app-root.ts", import.meta.url)), "utf8");
  assert.doesNotMatch(appRootSource, /^\s*can,\s*$/m, "app-root.ts must not carry an unused can import");
});

test("a plan with lines reads Estimated with its subtotal, a plan with none reads No lines yet, never Not priced beside an estimate", async () => {
  const page = await pageFor("buyer");
  const answer = await boardLoad(page);
  if (answer.kind !== "ready") throw new Error("the board did not load");
  const planning = answer.data.projects.find((p) => p.status === "PLANNING" && p.line_count > 0);
  assert.ok(planning, "the stub has a planning plan with lines");
  const some = words(boardView({ ...answer.data, projects: [planning] }, page)).replace(/<[^>]*>/g, " ");
  assert.ok(some.includes("Estimated"), some);
  assert.ok(some.includes(money(planning.subtotal_cents)), some);
  assert.doesNotMatch(some, /Price\s+Not priced/, some);
  const none = words(boardView({ ...answer.data, projects: [{ ...planning, line_count: 0, subtotal_cents: 0 }] }, page)).replace(/<[^>]*>/g, " ");
  assert.ok(none.includes("No lines yet"), none);
  assert.doesNotMatch(none, /\$0\.00/, none);
  const dayless = words(boardView({ ...answer.data, quotes: [{ ...answer.data.quotes[0], valid_until: null }], orders: [{ ...answer.data.orders[0], requested_date: null }] }, page)).replace(/<[^>]*>/g, " ");
  assert.doesNotMatch(dayless, /n\/a/, dayless);
  assert.ok(dayless.includes("Wanted by Not set") && dayless.includes("Valid until Not set"), dayless);
});
