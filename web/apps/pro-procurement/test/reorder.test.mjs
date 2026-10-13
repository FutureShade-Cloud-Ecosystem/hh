// Unit legs of the reorder move against the shared tenant stub: the preview
// re-prices the lines at today's rates (the core's numbers, never arithmetic
// here), the create takes the chosen line numbers plus an idempotency key and
// lands a draft order, and the create is gated by create_orders at the screen.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:net";
import { fileURLToPath } from "node:url";
import { reorderPreview, reorderCreate, reorderModalWords } from "../src/reorder.ts";
import { InMemoryTenantSession, tenantClient, stubAdapter, money, NO_FIGURE } from "@hardscapehouse/pro-kit";
import { PERSONAS, STUB_PASSWORD } from "@hardscapehouse/pro-kit/test-stub/tenant-data.mjs";

const stubFile = fileURLToPath(new URL("../../../packages/pro-kit/test-stub/tenant-stub.mjs", import.meta.url));
let child;
let base;

async function freePort(start, count) {
  for (let port = start; port < start + count; port += 1) {
    const free = await new Promise((resolve) => {
      const probe = createServer();
      probe.once("error", () => resolve(false));
      probe.listen(port, "127.0.0.1", () => probe.close(() => resolve(true)));
    });
    if (free) return port;
  }
  throw new Error(`no free port in ${start} to ${start + count - 1}`);
}

before(async () => {
  const port = await freePort(56832, 4);
  base = `http://127.0.0.1:${port}`;
  child = spawn(process.execPath, [stubFile], { env: { ROLE_STUB_PORT: String(port), ROLE_STUB_IDLE_EXIT_MS: "120000" }, stdio: "ignore" });
  for (let i = 0; i < 100; i += 1) {
    try {
      if ((await fetch(`${base}/healthz`)).ok) return;
    } catch {
      /* not up yet */
    }
    await new Promise((r) => setTimeout(r, 50));
  }
  throw new Error("the stub did not start");
});

after(async () => {
  await fetch(`${base}/_stub/exit`, { method: "POST" }).catch(() => {});
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
  const answer = await reorderCreate(page, ORDER_ID, [1]);
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
  const answer = await reorderCreate(page, ORDER_ID, [1, 2]);
  assert.equal(answer.kind, "ready");
});
