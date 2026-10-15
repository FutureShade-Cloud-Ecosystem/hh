// Unit legs of the reorder move against the shared tenant stub: the preview
// re-prices the lines at today's rates (the core's numbers, never arithmetic
// here), the create takes the chosen line numbers plus an idempotency key and
// lands a draft order, and the create is gated by create_orders at the screen.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { reorderPreview, reorderCreate, reorderModalWords, openReorder, reorderCommand } from "../src/reorder.ts";
import { InMemoryTenantSession, tenantClient, stubAdapter, money, NO_FIGURE } from "@hardscapehouse/pro-kit";
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
    assign: () => {},
    openModal: () => {},
    closeModal: () => {},
  };
}

// A page over a client whose fetch records every reorder create body and can
// fail the first of them, so a retry after a transport error can be driven
// like the person drives it: a second press of the same button.
async function reorderPageFor(who, failFirst) {
  const session = new InMemoryTenantSession({ adapter: stubAdapter({ baseUrl: base }), baseUrl: base });
  const signed = await session.signIn(PERSONAS[who].email, STUB_PASSWORD);
  assert.equal(signed.ok, true, who);
  const creates = [];
  const flaky = async (url, init) => {
    const path = new URL(String(url)).pathname;
    if (init.method === "POST" && path.endsWith("/reorder")) {
      creates.push({ body: String(init.body ?? "") });
      if (failFirst && creates.length === 1) throw new TypeError("the connection was reset");
    }
    return fetch(url, init);
  };
  const page = {
    session,
    client: tenantClient(session, { baseUrl: base, fetch: flaky }),
    route: { surface: "board" },
    busy: false,
    field: () => null,
    goto: () => {},
    refreshCount: 0,
    refresh: () => {
      page.refreshCount += 1;
    },
    said: [],
    say: (kind, words) => page.said.push({ kind, words }),
    hold: () => {},
    touches: 0,
    touch: () => {
      page.touches += 1;
    },
    assign: () => {},
    openModal: () => {},
    closedCount: 0,
    closeModal: () => {
      page.closedCount += 1;
    },
    creates,
  };
  return page;
}

async function waitForPreview(page) {
  // The modal's state fills when the preview answers; the touch it gives is the signal.
  for (let i = 0; i < 500 && page.touches === 0; i += 1) {
    await new Promise((r) => setTimeout(r, 10));
  }
  if (page.touches === 0) throw new Error("the reorder preview never landed");
}

async function waitForTouches(page, atLeast) {
  // A press touches the page when it starts and again when its outcome lands;
  // the second press of the test must not race the first's release (the app's
  // own Create button is disabled while busy), so the count is waited to the
  // floor and then to a stop.
  let still = 0;
  let last = -1;
  for (let i = 0; i < 1000 && (page.touches < atLeast || still < 5); i += 1) {
    if (page.touches === last) still += 1;
    else {
      still = 0;
      last = page.touches;
    }
    await new Promise((r) => setTimeout(r, 10));
  }
  if (page.touches < atLeast) throw new Error(`the press never settled: ${page.touches} touches`);
}

function keyOf(body) {
  const parsed = JSON.parse(body);
  assert.equal(typeof parsed.idempotency_key, "string");
  assert.ok(parsed.idempotency_key.length > 0, "the create carried an empty idempotency key");
  return parsed.idempotency_key;
}

const control = { getAttribute: () => null };


const ORDER_ID = "6d0000-0000-4000-8000-000000000001";

test("the preview carries every line at today's rate, with the availability the core answers", async () => {
  const page = await pageFor("buyer");
  const answer = await reorderPreview(page, ORDER_ID);
  assert.equal(answer.kind, "ready", JSON.stringify(answer));
  if (answer.kind !== "ready") return;
  assert.equal(answer.data.source_order_no, "SO-30501");
  assert.equal(answer.data.lines.length, 2);
  for (const line of answer.data.lines) {
    assert.equal(line.available, true);
    assert.equal(typeof line.unit_price_cents, "number");
    assert.equal(typeof line.line_total_cents, "number");
  }
});

test("the modal's words say what a reorder is, and carry no money arithmetic of their own", () => {
  const text = reorderModalWords();
  assert.match(text, /today's rates/);
  assert.match(text, /draft/);
  assert.doesNotMatch(text, /!/);
});

test("create takes the chosen lines and lands a draft, and the screen gate holds create_orders", async () => {
  const page = await pageFor("buyer");
  const answer = await reorderCreate(page, ORDER_ID, [1], crypto.randomUUID());
  assert.equal(answer.kind, "ready", JSON.stringify(answer));
  if (answer.kind !== "ready") return;
  assert.match(answer.data.order_no, /^SO-/);
  assert.equal(answer.data.status, "DRAFT");
  // The gate: a page whose person holds no create_orders is refused before any call.
  const gated = await pageFor("buyer");
  gated.session = { can: (capability) => capability !== "create_orders" };
  const refused = await reorderCreate(gated, ORDER_ID, [1]);
  assert.equal(refused.kind, "refusal");
});

test("a reorder of the whole order lands every line again", async () => {
  const page = await pageFor("pm");
  const answer = await reorderCreate(page, ORDER_ID, [1, 2], crypto.randomUUID());
  assert.equal(answer.kind, "ready");
});

test("two presses of Create after a failed first one send the same idempotency key", async () => {
  const page = await reorderPageFor("buyer", true);
  openReorder(page, ORDER_ID, "SO-30501");
  await waitForPreview(page);
  reorderCommand("create-reorder", control, page);
  // The first press dies in transport; the core may hold the draft either way.
  for (let i = 0; i < 500 && page.creates.length < 1; i += 1) await new Promise((r) => setTimeout(r, 10));
  assert.equal(page.creates.length, 1);
  await waitForTouches(page, 2);
  // The person presses Create again: the same selection retries under the same key.
  reorderCommand("create-reorder", control, page);
  for (let i = 0; i < 500 && (page.creates.length < 2 || page.closedCount < 1); i += 1) await new Promise((r) => setTimeout(r, 10));
  assert.equal(page.creates.length, 2);
  const first = keyOf(page.creates[0].body);
  const second = keyOf(page.creates[1].body);
  assert.equal(second, first, "the retry minted a second key, so the core could answer with a twin draft");
  // The retry landed: the modal closed and the draft was said.
  assert.equal(page.closedCount, 1, JSON.stringify(page.said));
  assert.match(page.said.at(-1).words, /^Draft order SO-/);
});

test("a changed selection mints a fresh key, and so does the next open after a success", async () => {
  const page = await reorderPageFor("buyer", true);
  openReorder(page, ORDER_ID, "SO-30501");
  await waitForPreview(page);
  // The person takes one line off before the first press, then puts it back: the selection changed.
  reorderCommand("toggle-reorder-line", { getAttribute: (name) => (name === "data-line" ? "1" : null) }, page);
  reorderCommand("create-reorder", control, page);
  for (let i = 0; i < 500 && page.creates.length < 1; i += 1) await new Promise((r) => setTimeout(r, 10));
  await waitForTouches(page, 3);
  reorderCommand("toggle-reorder-line", { getAttribute: (name) => (name === "data-line" ? "1" : null) }, page);
  reorderCommand("create-reorder", control, page);
  for (let i = 0; i < 500 && (page.creates.length < 2 || page.closedCount < 1); i += 1) await new Promise((r) => setTimeout(r, 10));
  assert.equal(page.creates.length, 2);
  const first = keyOf(page.creates[0].body);
  const second = keyOf(page.creates[1].body);
  assert.notEqual(second, first, "a changed selection must not retry under the old selection's key");
  // The next open after a success starts from a new key: it is a new reorder.
  const beforeOpen = page.touches;
  openReorder(page, ORDER_ID, "SO-30501");
  await waitForTouches(page, beforeOpen + 1);
  reorderCommand("create-reorder", control, page);
  for (let i = 0; i < 500 && (page.creates.length < 3 || page.closedCount < 2); i += 1) await new Promise((r) => setTimeout(r, 10));
  const third = keyOf(page.creates[2].body);
  assert.notEqual(third, second, "a new reorder rode under the previous one's key");
});
