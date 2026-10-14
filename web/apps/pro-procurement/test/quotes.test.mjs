// Unit legs of the Quotes surface against the shared tenant stub: the list and
// detail read the quote routes (gated submit_rfq), reject and convert are the
// moves the core serves, and acceptance happens on the dealer's link, where the
// core's own refusal says so in plain words.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { quotesLoad, quotesView, quotesHeading, quotesAccept, quotesReject, quotesConvert, QUOTES_EMPTY } from "../src/quotes.ts";
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

function makePage(who, route) {
  const notices = [];
  const session = new InMemoryTenantSession({ adapter: stubAdapter({ baseUrl: base }), baseUrl: base });
  return session.signIn(PERSONAS[who].email, STUB_PASSWORD).then((signed) => {
    assert.equal(signed.ok, true, who);
    return {
      session,
      client: tenantClient(session, { baseUrl: base }),
      route,
      busy: false,
      field: () => null,
      goto: () => {},
      refresh: () => {},
      say: (kind, words) => notices.push({ kind, words }),
      hold: () => {},
      touch: () => {},
      assign: () => {},
      openModal: () => {},
      closeModal: () => {},
      notices,
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

const SENT_ID = "7e0000-0000-4000-8000-000000000001";
const ACCEPTED_ID = "7e0000-0000-4000-8000-000000000002";
const DRAFT_ID = "7e0000-0000-4000-8000-000000000003";

test("a role without submit_rfq is refused before any call", async () => {
  const page = await makePage("field", { surface: "quotes" });
  assert.equal((await quotesLoad(page)).kind, "refusal");
});

test("the list holds the dealer's quotes with their words, and empties with its own", async () => {
  const page = await makePage("buyer", { surface: "quotes" });
  const answer = await quotesLoad(page);
  assert.equal(answer.kind, "ready", JSON.stringify(answer));
  if (answer.kind !== "ready") return;
  assert.equal(answer.data.quotes.length, 4);
  const text = words(quotesView(answer.data, page)).replace(/<[^>]*>/g, " ");
  for (const no of ["Q-7001", "Q-7002", "Q-7003", "Q-6990"]) assert.ok(text.includes(no), text);
  assert.ok(text.includes("Sent"), text);
  assert.ok(text.includes("Draft"), text);
  assert.ok(text.includes("Expired"), text);
  assert.ok(text.includes(money(answer.data.quotes[0].total_cents)), "the total is missing");
  assert.doesNotMatch(text, /\b[A-Z]{2,}_[A-Z]{2,}\b/, "a stored status string reached the screen");
  assert.doesNotMatch(text, /undefined|NaN|!/);
  await fetch(`${base}/_stub/control`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ empty: "quotes" }) });
  const emptied = await quotesLoad(page);
  assert.equal(emptied.kind, "empty");
  assert.match(QUOTES_EMPTY, /^No quotes yet/);
  await fetch(`${base}/_stub/reset`, { method: "POST" });
});

test("a sent quote offers Accept and Reject, not Convert; its lines and totals are the core's", async () => {
  const page = await makePage("pm", { surface: "quotes", quoteId: SENT_ID });
  const answer = await quotesLoad(page);
  assert.equal(answer.kind, "ready");
  if (answer.kind !== "ready") return;
  assert.match(quotesHeading(answer.data), /^Quote Q-7001$/);
  const text = words(quotesView(answer.data, page)).replace(/<[^>]*>/g, " ");
  assert.ok(text.includes("Irregular flagstone, buff"), text);
  assert.ok(text.includes("620 SF"), text);
  assert.ok(text.includes("Subtotal"), text);
  assert.ok(text.includes("Tax"), text);
  assert.ok(text.includes("Total"), text);
  assert.ok(text.includes(money(answer.data.quote.total_cents)), text);
  assert.ok(text.includes("Valid until"), text);
  const raw = words(quotesView(answer.data, page));
  assert.ok(/data-action="accept-quote-open"/.test(raw), "Accept is not offered");
  assert.ok(/data-action="reject-quote-open"/.test(raw), "Reject is not offered");
  assert.equal(/data-action="convert-quote-open"/.test(raw), false, "a sent quote must not offer Convert");
  assert.doesNotMatch(text, /undefined|NaN|!/);
});

test("an accepted quote offers Convert to order; the conversion takes pickup or will call and lands an order", async () => {
  const page = await makePage("owner", { surface: "quotes", quoteId: ACCEPTED_ID });
  const answer = await quotesLoad(page);
  assert.equal(answer.kind, "ready");
  if (answer.kind !== "ready") return;
  const raw = words(quotesView(answer.data, page));
  assert.ok(/data-action="convert-quote-open"/.test(raw), "Convert is not offered");
  assert.equal(/data-action="accept-quote-open"/.test(raw), false, "an accepted quote must not offer Accept again");
  const done = await quotesConvert(page, ACCEPTED_ID, "PICKUP");
  assert.equal(done.ok, true, JSON.stringify(done));
  if (done.ok) assert.match(done.order_no, /^SO-/);
  const after = await quotesLoad(page);
  if (after.kind !== "ready") throw new Error("the quote did not reload");
  assert.equal(after.data.quote.status, "CONVERTED");
});

test("the conversion refuses a delivery with the core's own sentence", async () => {
  const page = await makePage("owner", { surface: "quotes", quoteId: DRAFT_ID });
  const done = await quotesConvert(page, DRAFT_ID, "DELIVERY");
  assert.equal(done.ok, false);
  if (!done.ok) assert.match(done.error, /scheduling a stop/);
});

test("accepting here carries the core's refusal: the acceptance lives on the dealer's link", async () => {
  const page = await makePage("buyer", { surface: "quotes", quoteId: SENT_ID });
  const done = await quotesAccept(page, SENT_ID);
  assert.equal(done.ok, false);
  if (!done.ok) {
    assert.equal(done.status, 422);
    assert.match(done.error, /link the dealer sent you/);
  }
  const notice = page.notices.at(-1);
  assert.ok(notice, "the refusal was not said");
  assert.equal(notice.kind, "alert");
  assert.match(notice.words, /link the dealer sent you/);
});

test("reject moves a sent quote to lost, in the core's words", async () => {
  const page = await makePage("buyer", { surface: "quotes", quoteId: SENT_ID });
  const done = await quotesReject(page, SENT_ID);
  assert.equal(done.ok, true, JSON.stringify(done));
  const after = await quotesLoad(page);
  if (after.kind !== "ready") throw new Error("the quote did not reload");
  assert.equal(after.data.quote.status, "LOST");
  const text = words(quotesView(after.data, page)).replace(/<[^>]*>/g, " ");
  assert.ok(text.includes("Lost"), text);
});
