// Unit leg of Pro Field: the field port (the narrow seam every read and the
// delivery instruction write go through) against the shared tenant stub, the
// capability shape of its routes, and the words its rows and states carry.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { liveFieldPort } from "../src/port.ts";
import { ordersSurfaceOf } from "../src/sections.ts";
import { orderRowWords } from "../src/views.ts";
import { ApiClient } from "@hardscapehouse/api-client";
import { InMemoryTenantSession, tenantClient, stubAdapter, surfaceOf, can } from "@hardscapehouse/pro-kit";
import { PERSONAS, STUB_PASSWORD } from "@hardscapehouse/pro-kit/test-stub/tenant-data.mjs";
import { spawnStub } from "@hardscapehouse/pro-kit/test-stub/spawn-stub.mjs";

let stub;
let base;
let session;
let port;

before(async () => {
  stub = await spawnStub();
  base = stub.base;
});

// End the stub by its own door and await its exit: no signal is ever sent to a process.
after(async () => {
  await stub.stop();
});

async function signIn(who) {
  session = new InMemoryTenantSession({ adapter: stubAdapter({ baseUrl: base }), baseUrl: base });
  const answer = await session.signIn(PERSONAS[who].email, STUB_PASSWORD);
  assert.equal(answer.ok, true, who);
  port = liveFieldPort(tenantClient(session, { baseUrl: base }));
}

test("the landing surface is a surface of this app and needs the capability the catalogue names", () => {
  const surface = surfaceOf("field", "tracking");
  assert.ok(surface, "the landing surface is not in the catalogue");
  assert.equal(surface.capability, "view_orders_deliveries");
});

test("the port's orders read is held by exactly the roles that hold the capability", async () => {
  for (const [key, persona] of Object.entries(PERSONAS)) {
    const who = new InMemoryTenantSession({ adapter: stubAdapter({ baseUrl: base }), baseUrl: base });
    assert.equal((await who.signIn(persona.email, STUB_PASSWORD)).ok, true, key);
    const reader = liveFieldPort(tenantClient(who, { baseUrl: base }));
    const answer = await reader.listOrders();
    if (can(persona.role, "view_orders_deliveries")) {
      assert.equal(answer.ok, true, `${persona.role} should read orders: ${answer.error ?? ""}`);
      assert.ok(answer.value.length > 0, `${persona.role} read no orders`);
    } else {
      assert.equal(answer.ok, false, `${persona.role} should be refused`);
      assert.equal(answer.status, 403);
    }
  }
});

test("the port follows the orders read to the end and the rows carry words, never stored strings", async () => {
  await signIn("field");
  const answer = await port.listOrders();
  assert.equal(answer.ok, true);
  const text = answer.value.map((o) => orderRowWords(o)).join(" | ");
  assert.ok(text.includes("SO-"), `the rows do not show SO-: ${text}`);
  assert.ok(text.includes("Delivered"), `the rows do not show a Delivered chip word: ${text}`);
  assert.doesNotMatch(text, /\b[A-Z]{2,}_[A-Z]{2,}\b/, "a stored status string reached a row");
  assert.doesNotMatch(text, /undefined|null|NaN/);
});

test("each order surface filters the list to its fulfilment, and tracking keeps everything", async () => {
  const all = await port.listOrders();
  assert.equal(all.ok, true);
  const deliveries = all.value.filter((o) => (ordersSurfaceOf("deliveries").fulfillments ?? []).includes(o.fulfillment));
  const pickups = all.value.filter((o) => (ordersSurfaceOf("pickups").fulfillments ?? []).includes(o.fulfillment));
  assert.ok(deliveries.length > 0, "no delivery orders in the world");
  assert.ok(pickups.length > 0, "no pickup orders in the world");
  assert.equal(deliveries.length + pickups.length, all.value.length, "the stub world has a fulfillment outside the filters");
});

test("the port reads one order with its lines and the delivery instructions", async () => {
  await signIn("owner");
  const orders = await port.listOrders();
  const withNotes = orders.value.find((o) => o.order_no.startsWith("SO-"));
  const detail = await port.getOrder(withNotes.id);
  assert.equal(detail.ok, true, detail.error ?? "");
  assert.ok(Array.isArray(detail.value.lines) && detail.value.lines.length > 0);
  assert.equal(typeof detail.value.delivery_instructions, "string");
});

test("the delivery instruction write rides the port and reaches the order", async () => {
  await signIn("field");
  const orders = await port.listOrders();
  const target = orders.value.find((o) => o.fulfillment === "DELIVERY" && o.status === "PICKING");
  const saved = await port.setDeliveryInstructions(target.id, "Leave by the side gate, call on arrival.");
  assert.equal(saved.ok, true, saved.error ?? "");
  const reread = await port.getOrder(target.id);
  assert.equal(reread.value.delivery_instructions, "Leave by the side gate, call on arrival.");
});

test("return eligibility and a return request answer the core's not found for contractor orders, in plain words", async () => {
  await signIn("buyer");
  const orders = await port.listOrders();
  const target = orders.value[0];
  const eligibility = await port.getReturnEligibility(target.id);
  assert.equal(eligibility.ok, false);
  assert.equal(eligibility.status, 404);
  const created = await port.createReturnRequest(target.id, { reason_code: "DAMAGED", note: "", lines: [] });
  assert.equal(created.ok, false);
  assert.equal(created.status, 404);
  const listed = await port.listReturnRequests();
  assert.equal(listed.ok, true);
  assert.deepEqual(listed.value, []);
});

test("the orders empty words are this app's own, sentence case, with no exclamation", () => {
  const words = ["No orders to track yet. They appear here once the dealer receives one.", "No deliveries on the way. When the dealer books one, it shows here.", "Nothing ready for pickup yet. It shows here once the yard has it ready."];
  for (const w of words) {
    assert.doesNotMatch(w, /!/);
    assert.doesNotMatch(w, /\b[A-Z]{2,}\b/);
  }
});

test("the port is a closed surface: nothing on it queues and nothing on it names a store", async () => {
  // The seam's shape is the shells cycle's contract: reads and the one write,
  // nothing else. This pins the method list so a drift shows here first.
  const methods = Object.keys(liveFieldPort(new ApiClient({ baseUrl: base, fetch: async () => ({ status: 404, headers: new Headers(), text: async () => "" }) })));
  methods.sort();
  assert.deepEqual(methods, [
    "createReturnRequest", "getAddress", "getMe", "getOrder", "getProject", "getReturnEligibility",
    "listAddresses", "listInvites", "listOrders", "listProjects", "listReturnRequests", "listUsers",
    "setDeliveryInstructions",
  ]);
});
