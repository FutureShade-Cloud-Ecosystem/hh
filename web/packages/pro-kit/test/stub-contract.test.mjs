// The shared tenant stub against the contract and against the core.
//
// A stub that drifts from the server makes every browser leg a flattering lie,
// so this suite holds it to the core four ways:
//   1. the route table: the stub serves exactly the operations the core serves
//      (the generated client's 52, less the five session minting ones the core
//      leaves unregistered), with the same method, path and gate as the core's
//      own r.Handle lines;
//   2. the success shapes: every operation's answer is held to the generated
//      wire descriptor for its response type, with no undeclared field;
//   3. the refusals: the status, the envelope and the sentence, each sentence
//      found in the core's source (a refusal the stub invented fails here), and
//      the order they come in;
//   4. the gate: every route under every one of the five roles refuses exactly
//      when the kit's capability matrix says the role lacks the capability.
// And the money: tax is the 7.75 percent the dealer charges, whole cents, and
// every total adds up.
import { test, before, after, beforeEach } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { readFileSync, readdirSync } from "node:fs";
import { createServer } from "node:net";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { SCHEMA_DESCRIPTORS } from "../../api-client/src/testing.ts";
import { validateValue } from "../../api-client/test/_validate.mjs";
import { can } from "../src/capabilities.ts";
import { ROUTES } from "../test-stub/tenant-stub.mjs";
import { taxOn, STUB_PASSWORD, PERSONAS } from "../test-stub/tenant-data.mjs";

const pkgDir = fileURLToPath(new URL("..", import.meta.url));
const repo = join(pkgDir, "..", "..", "..");
const gen = join(repo, "web", "packages", "api-client", "src", "gen");
const portalDir = join(repo, "core", "internal", "portal");

const readGo = (dir, filter = () => true) =>
  readdirSync(dir)
    .filter((f) => f.endsWith(".go") && !f.endsWith("_test.go") && filter(f))
    .map((f) => readFileSync(join(dir, f), "utf8"))
    .join("\n");

const coreSource = [
  readGo(portalDir),
  readGo(join(repo, "core", "internal", "procurement")),
  readGo(join(repo, "core", "internal", "payments")),
  readGo(join(repo, "core", "internal", "quote")),
  readGo(join(repo, "core", "internal", "product")),
  readGo(join(repo, "core", "internal", "platform", "authz")),
  readGo(join(repo, "core", "internal", "platform", "httpx")),
].join("\n");

// ---- the generated client's operations ---------------------------------------------------------

function generatedOperations() {
  const ops = [];
  for (const file of readdirSync(gen).filter((f) => /^tenant-.*\.ts$/.test(f))) {
    const text = readFileSync(join(gen, file), "utf8");
    const re = /export async function (\w+)\([\s\S]*?\): Promise<Result<(.+?)>> \{\s*return client\.request<[^>]*>\(\{\s*method: "(\w+)",\s*path: "([^"]+)"/g;
    for (const m of text.matchAll(re)) ops.push({ operationId: m[1], response: m[2], method: m[3], path: m[4] });
  }
  return ops;
}

// The five operations that mint a session. The core's served mux leaves them unregistered (the CARRY NOTE in
// core/internal/portal/handler.go), so a 404 is the honest answer.
const MINTING = ["tenantLogin", "tenantAcceptInvite", "tenantRequestPasswordReset", "tenantConfirmPasswordReset", "tenantRefreshToken"];

// ---- the stub process ----------------------------------------------------------------------------

let child;
let base;
const tokens = {};

async function freePort() {
  return new Promise((resolve) => {
    const probe = createServer();
    probe.listen(0, "127.0.0.1", () => {
      const { port } = probe.address();
      probe.close(() => resolve(port));
    });
  });
}

before(async () => {
  const port = await freePort();
  base = `http://127.0.0.1:${port}`;
  child = spawn(process.execPath, [join(pkgDir, "test-stub", "tenant-stub.mjs")], { env: { ROLE_STUB_PORT: String(port) }, stdio: "ignore" });
  for (let i = 0; i < 100; i += 1) {
    try {
      if ((await fetch(`${base}/healthz`)).ok) break;
    } catch {
      /* not up yet */
    }
    await new Promise((r) => setTimeout(r, 50));
  }
  for (const [key, persona] of Object.entries(PERSONAS)) {
    const res = await fetch(`${base}/_stub/session`, { method: "POST", body: JSON.stringify({ email: persona.email, password: STUB_PASSWORD }) });
    tokens[key] = (await res.json()).token;
  }
});

after(() => {
  child?.kill();
});

beforeEach(async () => {
  await fetch(`${base}/_stub/reset`, { method: "POST" });
});

async function call(who, method, path, body, extra = {}) {
  const headers = { ...extra };
  if (who) headers.Authorization = `Bearer ${tokens[who] ?? who}`;
  let payload;
  if (body !== undefined) {
    headers["Content-Type"] = "application/json";
    payload = typeof body === "string" ? body : JSON.stringify(body);
  }
  const res = await fetch(`${base}${path}`, { method, headers, body: payload });
  const text = await res.text();
  let json = null;
  try {
    json = JSON.parse(text);
  } catch {
    /* not json */
  }
  return { status: res.status, text, json };
}

const T = "/api/v1/tenant";

// ---- 1. the route table ---------------------------------------------------------------------------

test("the stub serves the generated client's operations less the five the core leaves unregistered", () => {
  const generated = generatedOperations();
  assert.equal(generated.length, 52, "the tenant contract holds 52 operations");
  const served = generated.filter((o) => !MINTING.includes(o.operationId));
  assert.equal(served.length, 47);
  const stub = ROUTES.map(([method, path, operationId]) => `${method} ${path} ${operationId}`).sort();
  assert.deepEqual(stub, served.map((o) => `${o.method} ${o.path} ${o.operationId}`).sort());
});

test("the stub's routes and gates equal the core's own registrations", () => {
  const core = new Map();
  for (const m of readGo(portalDir).matchAll(/r\.Handle\("(\w+) (\/api\/v1\/tenant\/[^"]+)", g\.(\w+)\((?:identity\.Cap(\w+),\s*)?/g)) {
    const gate = m[3] === "RequireTenant" ? "tenant" : m[4].replace(/([a-z])([A-Z])/g, "$1_$2").toLowerCase().replace("r_f_q", "rfq");
    core.set(`${m[1]} ${m[2]}`, gate);
  }
  // The one public leg is served by the identity composition point (core/cmd/core/main.go).
  core.set("GET /api/v1/tenant/config", "public");
  const stub = new Map(ROUTES.map(([method, path, , gate]) => [`${method} ${path}`, gate]));
  assert.deepEqual([...stub.keys()].sort(), [...core.keys()].sort());
  for (const [route, gate] of stub) assert.equal(gate, core.get(route), `${route} is gated differently in the core`);
});

// ---- 2. success shapes ------------------------------------------------------------------------------

function descriptorFor(response) {
  if (response.endsWith("[]")) return { k: "array", of: { k: "ref", name: response.slice(0, -2) } };
  if (response === "unknown") return null;
  assert.ok(SCHEMA_DESCRIPTORS[response], `no descriptor for ${response}`);
  return { k: "ref", name: response };
}

// Keys a response may carry beyond what the descriptor requires: any declared property, recursively, nothing else.
function undeclared(value, descriptor, path, out) {
  const d = descriptor.k === "ref" ? SCHEMA_DESCRIPTORS[descriptor.name] : descriptor;
  if (d === undefined) return;
  if (d.k === "allOf") {
    const merged = { props: {} };
    for (const part of d.of) {
      const p = part.k === "ref" ? SCHEMA_DESCRIPTORS[part.name] : part;
      Object.assign(merged.props, p.props ?? {});
    }
    return undeclaredObject(value, { props: merged.props }, path, out);
  }
  if (d.k === "array" && Array.isArray(value)) value.forEach((v, i) => undeclared(v, d.of, `${path}[${i}]`, out));
  else if (d.k === "object") undeclaredObject(value, d, path, out);
}
function undeclaredObject(value, d, path, out) {
  if (value === null || typeof value !== "object" || Array.isArray(value)) return;
  for (const [key, v] of Object.entries(value)) {
    const prop = d.props?.[key];
    if (prop === undefined) out.push(`${path}.${key}`);
    else undeclared(v, prop, `${path}.${key}`, out);
  }
}

async function ids() {
  const get = async (p) => (await call("owner", "GET", `${T}${p}`)).json;
  const orders = (await get("/orders")).items;
  const invoices = (await get("/invoices")).items;
  const quotes = (await get("/quotes")).items;
  const projects = (await get("/procurement/projects")).projects;
  const detail = await get(`/procurement/projects/${projects.find((p) => p.status === "PLANNING" && p.line_count > 0).id}`);
  const methods = await get("/payment-methods");
  const users = await get("/users");
  const jobs = (await get("/projects")).projects;
  const addresses = (await get("/addresses")).addresses;
  const catalog = (await get("/catalog/search?q=paver")).items;
  return {
    order: orders[0].id,
    invoicePayable: invoices.find((i) => i.status === "FINALIZED").id,
    quoteSent: quotes.find((q) => q.status === "SENT").id,
    quoteAccepted: quotes.find((q) => q.status === "ACCEPTED").id,
    project: detail.id,
    line: detail.lines[0].id,
    method: methods[0].id,
    methodOther: methods[1].id,
    userAp: users.find((u) => u.role === "ap_finance").id,
    job: jobs[0].id,
    address: addresses[0].id,
    product: catalog[0].product_id,
  };
}

test("every served operation answers with its contract shape and nothing the contract does not declare", async () => {
  const i = await ids();
  const calls = {
    tenantGetConfig: [null, "GET", `${T}/config`],
    tenantGetMe: ["owner", "GET", `${T}/me`],
    tenantUpdateMe: ["owner", "PUT", `${T}/me`, { name: "Contractor owner", notification_prefs: { sms: true } }],
    tenantChangePassword: null,
    tenantListUsers: ["owner", "GET", `${T}/users`],
    tenantUpdateUserRole: ["owner", "PUT", `${T}/users/${i.userAp}/role`, { role: "ap_finance" }],
    tenantUpdateUserStatus: ["owner", "PUT", `${T}/users/${i.userAp}/status`, { status: "active" }],
    tenantListInvites: ["owner", "GET", `${T}/invites`],
    tenantCreateInvite: null,
    tenantGetDashboard: ["owner", "GET", `${T}/dashboard`],
    tenantGetBillingSummary: ["owner", "GET", `${T}/billing/summary`],
    tenantListInvoices: ["owner", "GET", `${T}/invoices`],
    tenantGetInvoice: ["owner", "GET", `${T}/invoices/${i.invoicePayable}`],
    tenantListOrders: ["owner", "GET", `${T}/orders`],
    tenantGetOrder: ["owner", "GET", `${T}/orders/${i.order}`],
    tenantSetDeliveryInstructions: ["owner", "PUT", `${T}/orders/${i.order}/delivery-instructions`, { instructions: "Leave at the gate." }],
    tenantGetReorderPreview: ["owner", "GET", `${T}/orders/${i.order}/reorder-preview`],
    tenantCreateReorder: ["owner", "POST", `${T}/orders/${i.order}/reorder`, { lines: [1] }],
    tenantGetReturnEligibility: null,
    tenantCreateReturnRequest: null,
    tenantListReturnRequests: ["owner", "GET", `${T}/return-requests`],
    tenantListQuotes: ["owner", "GET", `${T}/quotes`],
    tenantGetQuote: ["owner", "GET", `${T}/quotes/${i.quoteSent}`],
    tenantAcceptQuote: null,
    tenantRejectQuote: ["owner", "POST", `${T}/quotes/${i.quoteSent}/reject`],
    tenantConvertQuote: ["owner", "POST", `${T}/quotes/${i.quoteAccepted}/convert`, { fulfillment: "PICKUP" }],
    tenantListPaymentMethods: ["owner", "GET", `${T}/payment-methods`],
    tenantAddPaymentMethod: ["owner", "POST", `${T}/payment-methods`, { nonce: "stub-nonce-ok", make_default: false }],
    tenantRemovePaymentMethod: ["owner", "DELETE", `${T}/payment-methods/${i.methodOther}`],
    tenantSetDefaultPaymentMethod: ["owner", "PUT", `${T}/payment-methods/${i.method}/default`],
    tenantListPayments: ["owner", "GET", `${T}/payments`],
    tenantCreatePayment: ["owner", "POST", `${T}/payments`, { invoice_ids: [i.invoicePayable], payment_method_id: i.method }],
    tenantListProcurementProjects: ["owner", "GET", `${T}/procurement/projects`],
    tenantCreateProcurementProject: ["owner", "POST", `${T}/procurement/projects`, { name: "Garden wall" }],
    tenantGetProcurementProject: ["owner", "GET", `${T}/procurement/projects/${i.project}`],
    tenantUpdateProcurementProject: ["owner", "PUT", `${T}/procurement/projects/${i.project}`, { name: "Terrace pavers, phase one" }],
    tenantDeleteProcurementProject: null,
    tenantAddProcurementLine: ["owner", "POST", `${T}/procurement/projects/${i.project}/lines`, { product_id: i.product, qty_entered: "40", uom: "SF", notes: "Corner pieces" }],
    tenantUpdateProcurementLine: ["owner", "PUT", `${T}/procurement/projects/${i.project}/lines/${i.line}`, { qty_entered: "900" }],
    tenantRemoveProcurementLine: null,
    tenantSubmitProcurementRFQ: ["owner", "POST", `${T}/procurement/projects/${i.project}/submit-rfq`],
    tenantListProjectRollup: ["owner", "GET", `${T}/projects`],
    tenantGetProjectRollup: ["owner", "GET", `${T}/projects/${i.job}`],
    tenantSearchCatalog: ["owner", "GET", `${T}/catalog/search?q=wall`],
    tenantGetCatalogProduct: ["owner", "GET", `${T}/catalog/products/${i.product}`],
    tenantListAddresses: ["owner", "GET", `${T}/addresses`],
    tenantGetAddress: ["owner", "GET", `${T}/addresses/${i.address}`],
  };
  const served = generatedOperations().filter((o) => !MINTING.includes(o.operationId));
  let checked = 0;
  for (const op of served) {
    const spec = calls[op.operationId];
    if (spec === undefined) assert.fail(`no call for ${op.operationId}`);
    if (spec === null) continue; // refusal and no body operations, held in the refusal suite below
    const [who, method, path, body] = spec;
    assert.equal(method, op.method, `${op.operationId} method`);
    const answer = await call(who, method, path, body);
    assert.ok([200, 201].includes(answer.status), `${op.operationId} answered ${answer.status}: ${answer.text}`);
    const descriptor = descriptorFor(op.response);
    const problems = validateValue(answer.json, descriptor);
    assert.deepEqual(problems, [], `${op.operationId}: ${problems.join("; ")}`);
    const extra = [];
    undeclared(answer.json, descriptor, op.operationId, extra);
    assert.deepEqual(extra, [], `${op.operationId} carries fields the contract does not declare`);
    checked += 1;
  }
  assert.ok(checked >= 40, `only ${checked} shapes were checked`);
});

test("the two delete operations answer 204 with no body", async () => {
  const i = await ids();
  const line = await call("owner", "DELETE", `${T}/procurement/projects/${i.project}/lines/${i.line}`);
  assert.equal(line.status, 204);
  assert.equal(line.text, "");
  const created = await call("owner", "POST", `${T}/procurement/projects`, { name: "Short lived" });
  const gone = await call("owner", "DELETE", `${T}/procurement/projects/${created.json.id}`);
  assert.equal(gone.status, 204);
});

test("a list page carries its cursor and the next page resumes after it", async () => {
  const first = await call("owner", "GET", `${T}/orders?limit=2`);
  assert.equal(first.json.items.length, 2);
  assert.equal(typeof first.json.next_cursor, "string");
  const second = await call("owner", "GET", `${T}/orders?limit=2&cursor=${first.json.next_cursor}`);
  const seen = new Set(first.json.items.map((o) => o.id));
  for (const o of second.json.items) assert.equal(seen.has(o.id), false, "a row repeated across pages");
  const all = await call("owner", "GET", `${T}/orders?limit=200`);
  assert.equal(all.json.next_cursor, null);
  assert.deepEqual([...first.json.items, ...second.json.items].map((o) => o.id), all.json.items.slice(0, 4).map((o) => o.id));
  const bad = await call("owner", "GET", `${T}/orders?cursor=garbage`);
  assert.equal(bad.status, 400);
  assert.deepEqual(bad.json, { error: "cursor is malformed" });
});

// ---- 3. refusals: status, envelope, sentence, order ---------------------------------------------------

// Each sentence must appear in the core's source. `core` is the sentence as the core writes it (a %q where the core
// quotes a value); where it is the same as the answer it is omitted.
async function refusal(label, run, status, error, core = error) {
  const answer = await run();
  assert.equal(answer.status, status, `${label}: status`);
  assert.deepEqual(answer.json, { error }, `${label}: envelope`);
  assert.ok(coreSource.includes(core), `${label}: the sentence "${core}" is not in the core's source`);
}

test("the shared refusals: credentials, class, X-Org and the capability 403", async () => {
  const route = `${T}/orders`;
  await refusal("no bearer", () => call(null, "GET", route), 401, "Missing or invalid credentials");
  await refusal("a malformed bearer", () => call("not-a-token", "GET", route), 401, "Missing or invalid credentials");
  await refusal("a staff bearer", () => call("staff-stub.x", "GET", route), 403, "Forbidden");
  await refusal("X-Org on a tenant route", () => call("owner", "GET", route, undefined, { "X-Org": "gable" }), 400, "header X-Org is not accepted on this route");
  await refusal("a role without the capability", () => call("field", "GET", `${T}/invoices`), 403, "Forbidden");
});

test("the order of the refusals: credentials before class before X-Org before capability before body", async () => {
  // No bearer beats X-Org; a staff bearer beats X-Org; X-Org beats the capability check; the capability beats a bad body.
  assert.equal((await call(null, "GET", `${T}/orders`, undefined, { "X-Org": "gable" })).status, 401);
  assert.equal((await call("staff-stub.x", "GET", `${T}/orders`, undefined, { "X-Org": "gable" })).status, 403);
  assert.equal((await call("field", "GET", `${T}/invoices`, undefined, { "X-Org": "gable" })).status, 400);
  assert.equal((await call("field", "POST", `${T}/payments`, "{not json")).status, 403);
  assert.equal((await call("owner", "POST", `${T}/payments`, "{not json")).status, 400);
});

test("the public config needs no bearer and names the dealer", async () => {
  const answer = await call(null, "GET", `${T}/config`);
  assert.equal(answer.status, 200);
  assert.equal(answer.json.dealer_name, "Gable Landscape Supply");
  assert.equal(answer.json.id.includes("dibbits"), false);
});

test("a route the core leaves unregistered is the mux's 404, and a wrong method is its 405", async () => {
  for (const [method, path] of [["POST", `${T}/login`], ["POST", `${T}/accept-invite`], ["POST", `${T}/password-reset/request`], ["POST", `${T}/password-reset/confirm`], ["POST", `${T}/token/refresh`]]) {
    const answer = await call(null, method, path, {});
    assert.equal(answer.status, 404, path);
    assert.equal(answer.text, "404 page not found\n");
  }
  const wrong = await call("owner", "DELETE", `${T}/orders`);
  assert.equal(wrong.status, 405);
  assert.equal(wrong.text, "Method Not Allowed\n");
});

test("the planning board's refusals use the core's words", async () => {
  const i = await ids();
  const post = (path, body) => () => call("owner", "POST", `${T}${path}`, body);
  const put = (path, body) => () => call("owner", "PUT", `${T}${path}`, body);
  await refusal("a malformed body", post("/procurement/projects", "{nope"), 400, "invalid_json");
  await refusal("an empty name", post("/procurement/projects", { name: "  " }), 400, "project name is required", "project name is required");
  await refusal("nothing to update", put(`/procurement/projects/${i.project}`, {}), 400, "nothing to update");
  await refusal("an unknown status", put(`/procurement/projects/${i.project}`, { status: "WAITING" }), 400, 'unknown project status "WAITING"', 'unknown project status %q');
  await refusal("moving to quoting by hand", put(`/procurement/projects/${i.project}`, { status: "QUOTING" }), 409, "invalid state transition");
  await refusal("a missing project", () => call("owner", "GET", `${T}/procurement/projects/${"9a0000-0000-4000-8000-000000009999"}`), 404, "not found");
  await refusal("a line without a product", post(`/procurement/projects/${i.project}/lines`, { qty_entered: "1", uom: "SF" }), 400, "product_id is required");
  await refusal("a line without a unit", post(`/procurement/projects/${i.project}/lines`, { product_id: i.product, qty_entered: "1" }), 400, "uom is required");
  await refusal("a zero quantity", post(`/procurement/projects/${i.project}/lines`, { product_id: i.product, qty_entered: "0", uom: "SF" }), 400, "qty_entered must be greater than zero");
  await refusal("a quantity that is not a number", post(`/procurement/projects/${i.project}/lines`, { product_id: i.product, qty_entered: "lots", uom: "SF" }), 400, 'qty_entered "lots" must be a positive decimal', 'qty_entered %q must be a positive decimal');
  await refusal("a line on a quoting project", async () => {
    const quoting = (await call("owner", "GET", `${T}/procurement/projects`)).json.projects.find((p) => p.status === "QUOTING");
    return call("owner", "POST", `${T}/procurement/projects/${quoting.id}/lines`, { product_id: i.product, qty_entered: "1", uom: "SF" });
  }, 409, "invalid state transition");
  await refusal("an RFQ with no lines", async () => {
    const empty = (await call("owner", "GET", `${T}/procurement/projects`)).json.projects.find((p) => p.line_count === 0);
    return call("owner", "POST", `${T}/procurement/projects/${empty.id}/submit-rfq`);
  }, 400, "project has no lines to quote");
  await refusal("an RFQ that was already submitted", async () => {
    const quoting = (await call("owner", "GET", `${T}/procurement/projects`)).json.projects.find((p) => p.status === "QUOTING");
    return call("owner", "POST", `${T}/procurement/projects/${quoting.id}/submit-rfq`);
  }, 409, "invalid state transition");
  await refusal("deleting a project that is no longer planning", async () => {
    const quoting = (await call("owner", "GET", `${T}/procurement/projects`)).json.projects.find((p) => p.status === "QUOTING");
    return call("owner", "DELETE", `${T}/procurement/projects/${quoting.id}`);
  }, 409, "invalid state transition");
});

test("the quote refusals use the core's words", async () => {
  const i = await ids();
  await refusal("accepting from the portal", () => call("owner", "POST", `${T}/quotes/${i.quoteSent}/accept`), 422,
    "a quote is accepted through the link the dealer sent you, where you confirm the quantities and type your name; ask the dealer for the link");
  await refusal("converting a quote that is not accepted", () => call("owner", "POST", `${T}/quotes/${i.quoteSent}/convert`), 409, "invalid state transition");
  await refusal("converting as a delivery", () => call("owner", "POST", `${T}/quotes/${i.quoteAccepted}/convert`, { fulfillment: "DELIVERY" }), 400,
    "a delivery is booked by scheduling a stop, not chosen at conversion, convert as pickup or will-call, then schedule the delivery");
  await refusal("converting with an unknown fulfilment", () => call("owner", "POST", `${T}/quotes/${i.quoteAccepted}/convert`, { fulfillment: "TELEPORT" }), 400, "fulfillment must be PICKUP or WILL_CALL", "fulfillment must be %s or %s");
  await refusal("an unknown status filter", () => call("owner", "GET", `${T}/quotes?status=MAYBE`), 400, 'unknown quote status "MAYBE"', "unknown quote status %q");
  const rejected = await call("owner", "POST", `${T}/quotes/${i.quoteSent}/reject`);
  assert.equal(rejected.json.status, "LOST");
  await refusal("rejecting twice", () => call("owner", "POST", `${T}/quotes/${i.quoteSent}/reject`), 409, "invalid state transition");
});

test("the payment refusals use the core's words, and card data never rides a request", async () => {
  const i = await ids();
  const pay = (body) => () => call("owner", "POST", `${T}/payments`, body);
  await refusal("no invoice", pay({ invoice_ids: [], payment_method_id: i.method }), 400, "at least one invoice is required");
  await refusal("no funding", pay({ invoice_ids: [i.invoicePayable] }), 400, "a stored method or a tokenization nonce is required");
  await refusal("the same invoice twice", pay({ invoice_ids: [i.invoicePayable, i.invoicePayable], payment_method_id: i.method }), 400, "an invoice may be selected only once");
  await refusal("an invoice that is not payable", pay({ invoice_ids: ["8b0000-0000-4000-8000-000000009999"], payment_method_id: i.method }), 400,
    'invoice "8b0000-0000-4000-8000-000000009999" is not payable; refresh and retry', 'invoice %q is not payable; refresh and retry');
  await refusal("a declined card", pay({ invoice_ids: [i.invoicePayable], nonce: "stub-nonce-declined" }), 402, "payment declined");
  await refusal("a pending charge", pay({ invoice_ids: [i.invoicePayable], nonce: "stub-nonce-pending" }), 409, "payment is pending, please retry");
  await refusal("a method without a nonce", () => call("owner", "POST", `${T}/payment-methods`, {}), 400, "a tokenization nonce is required");
  const view = (await call("owner", "GET", `${T}/payment-methods`)).json[0];
  assert.deepEqual(Object.keys(view).filter((k) => /number|pan|cvc|cvv|token|secret/i.test(k)), [], "a stored method exposes no card data");
});

test("the team and account refusals use the core's words", async () => {
  const users = (await call("owner", "GET", `${T}/users`)).json;
  const owner = users.find((u) => u.role === "account_admin");
  await refusal("an unknown role", () => call("owner", "PUT", `${T}/users/${owner.id}/role`, { role: "boss" }), 400, 'unknown role "boss"', "unknown role %q");
  await refusal("an unknown status", () => call("owner", "PUT", `${T}/users/${owner.id}/status`, { status: "gone" }), 400, 'unknown status "gone"', "unknown status %q");
  await refusal("demoting the last owner", () => call("owner", "PUT", `${T}/users/${owner.id}/role`, { role: "buyer" }), 409, "last account_admin cannot be demoted or deactivated");
  await refusal("deactivating the last owner", () => call("owner", "PUT", `${T}/users/${owner.id}/status`, { status: "inactive" }), 409, "last account_admin cannot be demoted or deactivated");
  await refusal("inviting", () => call("owner", "POST", `${T}/invites`, { email: "x@hh-demo.invalid", role: "buyer" }), 501, "inviting users is not available yet");
  await refusal("changing a password", () => call("owner", "POST", `${T}/me/password`, {}), 501, "changing your password here is not available yet");
  await refusal("a bad prefs value", () => call("owner", "PUT", `${T}/me`, { notification_prefs: [1] }), 400, "notification_prefs must be a JSON object");
  await refusal("a search with no query", () => call("owner", "GET", `${T}/catalog/search`), 400, "search query is required");
  await refusal("delivery instructions too long", async () => {
    const order = (await call("owner", "GET", `${T}/orders`)).json.items[0];
    return call("owner", "PUT", `${T}/orders/${order.id}/delivery-instructions`, { instructions: "x".repeat(2001) });
  }, 400, "delivery instructions exceed 2000 characters", "delivery instructions exceed %d characters");
  await refusal("a counter return on a contractor order", async () => {
    const order = (await call("owner", "GET", `${T}/orders`)).json.items[0];
    return call("owner", "GET", `${T}/orders/${order.id}/return-eligibility`);
  }, 404, "not found");
});

// ---- 4. the gate: every route under every role ------------------------------------------------------------

test("every route refuses exactly the roles the kit's capability matrix says lack its capability", async () => {
  let checked = 0;
  for (const [method, path, operationId, gate] of ROUTES) {
    if (gate === "public") continue;
    const concrete = path.replace(/\{\w+\}/g, "6d0000-0000-4000-8000-000000000001");
    for (const [key, persona] of Object.entries(PERSONAS)) {
      const allowed = gate === "tenant" || can(persona.role, gate);
      const answer = await call(key, method, `${concrete}${path.endsWith("/search") ? "?q=a" : ""}`, method === "GET" || method === "DELETE" ? undefined : "{}");
      if (allowed) assert.notEqual(answer.status, 403, `${operationId} refused ${persona.role} that holds ${gate}`);
      else {
        assert.equal(answer.status, 403, `${operationId} let ${persona.role} through without ${gate}`);
        assert.deepEqual(answer.json, { error: "Forbidden" });
      }
      checked += 1;
    }
  }
  assert.equal(checked, 46 * 5);
});

test("tenant me answers each role's capabilities as the matrix holds them", async () => {
  for (const [key, persona] of Object.entries(PERSONAS)) {
    const me = (await call(key, "GET", `${T}/me`)).json;
    assert.equal(me.role, persona.role);
    for (const [capability, held] of Object.entries(me.capabilities)) assert.equal(held, can(persona.role, capability), `${persona.role} ${capability}`);
  }
});

// ---- the money -------------------------------------------------------------------------------------------

test("tax is the dealer's 7.75 percent in whole cents, and every total adds up", async () => {
  assert.equal(taxOn(10000), 775);
  assert.equal(taxOn(1), 0);
  assert.equal(taxOn(7), 1, "half a cent rounds up");
  for (const path of ["/orders?limit=200", "/quotes?limit=200"]) {
    const rows = (await call("owner", "GET", `${T}${path}`)).json.items;
    for (const row of rows) {
      assert.equal(row.tax_cents, taxOn(row.subtotal_cents), `${row.id} tax`);
      assert.equal(row.total_cents, row.subtotal_cents + row.tax_cents, `${row.id} total`);
    }
  }
  const orders = (await call("owner", "GET", `${T}/orders?limit=200`)).json.items;
  for (const o of orders) {
    const detail = (await call("owner", "GET", `${T}/orders/${o.id}`)).json;
    assert.equal(detail.lines.reduce((s, l) => s + l.line_total_cents, 0), detail.subtotal_cents, `${o.order_no} lines`);
  }
  const summary = (await call("owner", "GET", `${T}/billing/summary`)).json;
  const open = (await call("owner", "GET", `${T}/invoices?limit=200`)).json.items.filter((x) => ["FINALIZED", "PARTIALLY_PAID"].includes(x.status));
  assert.equal(summary.open_balance_cents, open.reduce((s, x) => s + x.balance_cents, 0));
  assert.equal(summary.available_credit_cents, Math.max(0, summary.credit_limit_cents - summary.open_balance_cents));
});

test("paying an invoice settles it, records the payment and moves the balance", async () => {
  const i = await ids();
  const before = (await call("owner", "GET", `${T}/billing/summary`)).json;
  const inv = (await call("owner", "GET", `${T}/invoices/${i.invoicePayable}`)).json;
  const paid = await call("owner", "POST", `${T}/payments`, { invoice_ids: [i.invoicePayable], payment_method_id: i.method, idempotency_key: "k-1" });
  assert.equal(paid.status, 201);
  assert.equal(paid.json.amount_cents, inv.balance_cents);
  const again = await call("owner", "POST", `${T}/payments`, { invoice_ids: [i.invoicePayable], payment_method_id: i.method, idempotency_key: "k-1" });
  assert.equal(again.json.id, paid.json.id, "the same key replays the same payment");
  assert.equal((await call("owner", "GET", `${T}/invoices/${i.invoicePayable}`)).json.status, "PAID");
  const after = (await call("owner", "GET", `${T}/billing/summary`)).json;
  assert.equal(after.open_balance_cents, before.open_balance_cents - inv.balance_cents);
});

test("converting an accepted quote makes an order and marks the quote converted", async () => {
  const i = await ids();
  const before = (await call("owner", "GET", `${T}/orders?limit=200`)).json.items.length;
  const result = await call("owner", "POST", `${T}/quotes/${i.quoteAccepted}/convert`);
  assert.equal(result.status, 200);
  assert.equal((await call("owner", "GET", `${T}/quotes/${i.quoteAccepted}`)).json.status, "CONVERTED");
  assert.equal((await call("owner", "GET", `${T}/orders?limit=200`)).json.items.length, before + 1);
});

test("submitting an RFQ prices the lines and moves the project to quoting", async () => {
  const i = await ids();
  const answer = await call("owner", "POST", `${T}/procurement/projects/${i.project}/submit-rfq`);
  assert.equal(answer.json.status, "QUOTING");
  assert.ok(answer.json.lines.every((l) => l.priced && l.indicative_line_total_cents > 0));
});

test("the sign in door is the stub's own and refuses a wrong password in plain words", async () => {
  const bad = await call(null, "POST", "/_stub/session", { email: PERSONAS.owner.email, password: "nope" });
  assert.equal(bad.status, 401);
  assert.equal(bad.json.error, "That email and password do not match.");
});

test("reset puts the world back", async () => {
  const i = await ids();
  await call("owner", "POST", `${T}/quotes/${i.quoteSent}/reject`);
  await fetch(`${base}/_stub/reset`, { method: "POST" });
  const quote = (await call("owner", "GET", `${T}/quotes/${i.quoteSent}`)).json;
  assert.equal(quote.status, "SENT");
});

test("the stub's test controls make a fault, a delay and an empty list on demand, and reset clears them", async () => {
  await fetch(`${base}/_stub/control`, { method: "POST", body: JSON.stringify({ empty: "orders" }) });
  assert.deepEqual((await call("owner", "GET", `${T}/orders`)).json.items, []);
  await fetch(`${base}/_stub/reset`, { method: "POST" });
  await fetch(`${base}/_stub/control`, { method: "POST", body: JSON.stringify({ fault: { path: `${T}/invoices`, status: 503 } }) });
  assert.equal((await call("owner", "GET", `${T}/invoices`)).status, 503);
  assert.equal((await call("field", "GET", `${T}/invoices`)).status, 403, "the gate still comes before a fault");
  assert.equal((await call("owner", "GET", `${T}/orders`)).status, 200, "a fault names its own path only");
  await fetch(`${base}/_stub/control`, { method: "POST", body: JSON.stringify({ delayMs: 250 }) });
  const started = Date.now();
  await call("owner", "GET", `${T}/orders`);
  assert.ok(Date.now() - started >= 240, "the delay held the handler");
  await fetch(`${base}/_stub/reset`, { method: "POST" });
  assert.equal((await call("owner", "GET", `${T}/invoices`)).status, 200);
});
