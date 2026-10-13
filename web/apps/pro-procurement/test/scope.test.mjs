// Unit leg of the Order scope surface against the shared tenant stub: one
// procurement project, its lines at the account price (the indicative prices
// the core returns; a line with no price reads as the kit's placeholder, never
// a zero), the moves the role may make (add from the catalog, edit, remove,
// submit the RFQ, recall, rename, delete), and the server's refusals in plain
// words.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:net";
import { fileURLToPath } from "node:url";
import { scopeLoad, scopeView, scopeCommand, scopeSubmitRfq, scopeAddLine, SCOPE_MISSING } from "../src/scope.ts";
import { InMemoryTenantSession, tenantClient, stubAdapter, money } from "@hardscapehouse/pro-kit";
import { tenantProcurement } from "@hardscapehouse/api-client";
import { PERSONAS, STUB_PASSWORD, PRODUCTS } from "@hardscapehouse/pro-kit/test-stub/tenant-data.mjs";

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
  const port = await freePort(56820, 4);
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

function makePage(who, route) {
  const notices = [];
  let navigated = null;
  const session = new InMemoryTenantSession({ adapter: stubAdapter({ baseUrl: base }), baseUrl: base });
  return session
    .signIn(PERSONAS[who].email, STUB_PASSWORD)
    .then((signed) => {
      assert.equal(signed.ok, true, who);
      return {
        session,
        client: tenantClient(session, { baseUrl: base }),
        route,
        busy: false,
        field: () => null,
        goto: (next) => {
          navigated = next;
        },
        refresh: () => {},
        say: (kind, words) => notices.push({ kind, words }),
        hold: () => {},
        touch: () => {},
        openModal: () => {},
        closeModal: () => {},
        notices,
        navigated,
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

const PLANNING_ID = "9a0000-0000-4000-8000-000000000001";
const QUOTING_ID = "9a0000-0000-4000-8000-000000000002";
const EMPTY_PLAN_ID = "9a0000-0000-4000-8000-000000000004";

test("a role without submit_rfq is refused before any call; a holder reads the project and its lines", async () => {
  const refused = await makePage("field", { surface: "scope", projectId: PLANNING_ID });
  assert.equal((await scopeLoad(refused)).kind, "refusal");
  const ok = await makePage("pm", { surface: "scope", projectId: PLANNING_ID });
  const answer = await scopeLoad(ok);
  assert.equal(answer.kind, "ready", JSON.stringify(answer));
  if (answer.kind === "ready") {
    assert.equal(answer.heading, "La Jolla terrace pavers");
    assert.equal(answer.data.lines.length, 3);
    assert.equal(answer.data.status, "PLANNING");
  }
});

test("a project that is gone reads as the surface's empty words", async () => {
  const page = await makePage("owner", { surface: "scope", projectId: "9a0000-0000-4000-8000-999999999999" });
  const answer = await scopeLoad(page);
  assert.equal(answer.kind, "empty");
  assert.match(SCOPE_MISSING, /^That plan is not here/);
});

test("a priced line shows its indicative price, an unpriced line the placeholder, never a confident zero", async () => {
  const page = await makePage("buyer", { surface: "scope", projectId: PLANNING_ID });
  const answer = await scopeLoad(page);
  assert.equal(answer.kind, "ready");
  if (answer.kind !== "ready") return;
  const text = words(scopeView(answer.data, page)).replace(/<[^>]*>/g, " ");
  assert.ok(text.includes("Sandstone paver 6 x 9"), text);
  assert.ok(text.includes("CPV-1204"), "the sku is missing");
  assert.ok(text.includes("850 SF"), "the quantity with its unit is missing");
  assert.ok(text.includes(money(435200)), "the indicative line total is missing");
  assert.ok(text.includes("Indicative"), "the indicative note is missing");
  assert.ok(text.includes("Submit RFQ"), "a planning project offers Submit RFQ");
  assert.ok(text.includes("Add material"), "a planning project offers Add material");
  assert.doesNotMatch(text, /\b[A-Z]{2,}_[A-Z]{2,}\b/, "a stored status string reached the screen");
  assert.doesNotMatch(text, /undefined|NaN|!/);
});

test("a fresh line with no price reads as n/a until the RFQ prices it", async () => {
  const page = await makePage("owner", { surface: "scope", projectId: EMPTY_PLAN_ID });
  const created = await scopeAddLine(page, PRODUCTS[0].id, "10", PRODUCTS[0].base_uom);
  assert.equal(created.ok, true);
  const answer = await scopeLoad(page);
  assert.equal(answer.kind, "ready");
  if (answer.kind !== "ready") return;
  const text = words(scopeView(answer.data, page)).replace(/<[^>]*>/g, " ");
  assert.ok(text.includes("Awaiting price"), text);
  assert.ok(text.includes("n/a"), "an unpriced line must read as the placeholder");
  // The RFQ prices the lines; the server's numbers come back and the page shows them.
  await scopeSubmitRfq(page);
  const priced = await scopeLoad(page);
  if (priced.kind !== "ready") throw new Error("the project did not reload");
  const pricedText = words(scopeView(priced.data, page)).replace(/<[^>]*>/g, " ");
  assert.ok(pricedText.includes(money(5120)), `the RFQ price did not land: ${pricedText}`);
  assert.ok(pricedText.includes("Quoting"), "the project is not quoting after the RFQ");
});

test("submitting the RFQ on an empty plan carries the server's refusal in plain words", async () => {
  const page = await makePage("owner", { surface: "scope", projectId: "" });
  const created = await tenantProcurement.tenantCreateProcurementProject(page.client, { name: "Empty by intent" });
  page.route = { surface: "scope", projectId: created.value.id };
  await scopeSubmitRfq(page);
  const last = page.notices.at(-1);
  assert.ok(last, "no notice was given");
  assert.equal(last.kind, "alert");
  assert.match(last.words, /no lines to quote/);
});

test("a quoting project is read only: recall is offered, submit and the edit moves are not", async () => {
  const page = await makePage("buyer", { surface: "scope", projectId: QUOTING_ID });
  const answer = await scopeLoad(page);
  assert.equal(answer.kind, "ready");
  if (answer.kind !== "ready") return;
  const text = words(scopeView(answer.data, page));
  assert.ok(text.includes("Recall to planning"), text);
  assert.equal(/\bdata-action="submit-rfq"\b/.test(text), false, "a quoting project must not offer Submit RFQ");
  assert.equal(/\bdata-action="add-material"\b/.test(text), false, "a quoting project must not offer Add material");
});

test("the surface commands answer only their own actions", async () => {
  const page = await makePage("owner", { surface: "scope", projectId: PLANNING_ID });
  const control = document_like("pick-product");
  assert.equal(scopeCommand("pick-product", control, page), true);
  assert.equal(scopeCommand("no-such-action", control, page), false);
});

function document_like(action) {
  // The delegation hands the element the action name was read from; the surface
  // only reads its data attributes.
  return { getAttribute: (name) => (name === "data-action" ? action : null) };
}
