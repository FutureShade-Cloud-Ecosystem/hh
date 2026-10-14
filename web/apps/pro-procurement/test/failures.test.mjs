// Unit legs of the one door every action's failure goes through
// (views.ts actionFailure): a lapsed session signs the person out with the
// load's words; a role refusal says the kit's refusal words; a failure with no
// answer of the server's own says so in this app's words, never the client's
// operation id; a 4xx the core answered with a body shows the core's words.
// The classification legs run the helper bare; the action legs drive the real
// surface moves over the shared tenant stub, so the wiring itself is what is
// held, not only the helper's shape.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { actionFailure, SIGNED_OUT_WORDS } from "../src/views.ts";
import { scopeSubmitRfq } from "../src/scope.ts";
import { quotesReject } from "../src/quotes.ts";
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

// A page that records the sign out move and the notices, and nothing else.
function recordingPage(client) {
  const page = {
    client,
    session: null,
    route: { surface: "scope" },
    busy: false,
    field: () => null,
    goto: () => {},
    refresh: () => {},
    said: [],
    say: (kind, words) => page.said.push({ kind, words }),
    hold: () => {},
    touch: () => {},
    assign: () => {},
    openModal: () => {},
    closeModal: () => {},
    signedOut: null,
    signOut: async (words) => {
      page.signedOut = words;
    },
  };
  return page;
}

async function pageFor(who) {
  const session = new InMemoryTenantSession({ adapter: stubAdapter({ baseUrl: base }), baseUrl: base });
  const signed = await session.signIn(PERSONAS[who].email, STUB_PASSWORD);
  assert.equal(signed.ok, true, who);
  const page = recordingPage(tenantClient(session, { baseUrl: base }));
  page.session = session;
  return page;
}

const ROLE_WORDS = "Your role does not open this. Ask the Owner on your account to change your access.";

test("a 401 signs the person out with the same words a load signs out with", async () => {
  const page = recordingPage(null);
  const words = await actionFailure(page, { error: "Missing or invalid credentials", status: 401 });
  assert.equal(words, SIGNED_OUT_WORDS);
  assert.match(words, /^You are signed out\. Sign in again\.$/);
  assert.equal(page.signedOut, SIGNED_OUT_WORDS, "the helper did not sign the session out");
});

test("a 403 says the kit's refusal words, and signs nobody out", async () => {
  const page = recordingPage(null);
  const words = await actionFailure(page, { error: "Forbidden", status: 403 });
  assert.equal(words, ROLE_WORDS);
  assert.equal(page.signedOut, null);
});

test("a failure the server never answered says so in the app's words, never the client's operation id", async () => {
  const page = recordingPage(null);
  const words = await actionFailure(page, { error: "tenantCreateReorder: transport error: the connection was reset" });
  assert.equal(words, "This did not go through. Nothing was changed.");
  assert.doesNotMatch(words, /tenantCreateReorder/);
  assert.ok(!words.includes(":"), words);
  assert.equal(page.signedOut, null);
});

test("a move the core may have taken anyway never promises nothing changed", async () => {
  const page = recordingPage(null);
  const words = await actionFailure(page, { error: "tenantCreateReorder: the request timed out after 5000 ms" }, true);
  assert.equal(words, "This did not go through. Check the Board before you try again.");
  assert.doesNotMatch(words, /Nothing was changed/);
});

test("a 4xx with a body shows the core's words, and a bodyless one does not show a bare status", async () => {
  const page = recordingPage(null);
  const words = await actionFailure(page, { error: "a quote is accepted through the link the dealer sent you", status: 422 });
  assert.equal(words, "a quote is accepted through the link the dealer sent you");
  const bodyless = await actionFailure(page, { error: "HTTP 409", status: 409 });
  assert.equal(bodyless, "This did not go through. Nothing was changed.");
});

const PLANNING_ID = "9a0000-0000-4000-8000-000000000001";
const SENT_ID = "7e0000-0000-4000-8000-000000000001";

test("a lapsed session on Submit RFQ signs the person out with the load's words", async () => {
  const page = await pageFor("owner");
  page.route = { surface: "scope", projectId: PLANNING_ID };
  await fetch(`${base}/_stub/control`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ fault: { path: `/api/v1/tenant/procurement/projects/${PLANNING_ID}/submit-rfq`, status: 401 } }) });
  await scopeSubmitRfq(page);
  assert.equal(page.signedOut, SIGNED_OUT_WORDS, "the action did not sign the person out");
  assert.equal(page.said.at(-1).kind, "alert");
  assert.equal(page.said.at(-1).words, SIGNED_OUT_WORDS);
  await fetch(`${base}/_stub/reset`, { method: "POST" });
});

test("a role refusal on an action reads the kit's refusal words, not the server's bare no", async () => {
  const page = await pageFor("buyer");
  page.route = { surface: "quotes", quoteId: SENT_ID };
  await fetch(`${base}/_stub/control`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ fault: { path: `/api/v1/tenant/quotes/${SENT_ID}`, status: 403 } }) });
  const done = await quotesReject(page, SENT_ID);
  assert.equal(done.ok, false);
  if (!done.ok) assert.equal(done.error, ROLE_WORDS);
  assert.equal(page.said.at(-1).words, ROLE_WORDS);
  assert.equal(page.signedOut, null);
  await fetch(`${base}/_stub/reset`, { method: "POST" });
});
