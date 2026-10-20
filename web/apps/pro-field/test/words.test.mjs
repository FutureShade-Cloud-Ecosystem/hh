// The words a contractor reads (UX-37, glossary): an unknown figure says what is missing, one word for
// the day asked for, "your dealer" never dealer internals. Pure functions and the order row's words.
import { test } from "node:test";
import assert from "node:assert/strict";
import { priceWords, qtyWords } from "../src/figures.ts";
import { orderRowWords } from "../src/views.ts";
import { promisedWords } from "../src/timeline.ts";
import { dayWords } from "../src/figures.ts";
import { projectView, teamView, orderView } from "../src/views.ts";

test("an unknown price reads Not priced and an unknown quantity Not counted, never n/a", () => {
  assert.equal(priceWords(undefined), "Not priced");
  assert.equal(priceWords(null), "Not priced");
  assert.equal(priceWords(Number.NaN), "Not priced");
  assert.equal(priceWords(123400), "$1,234.00");
  assert.equal(priceWords(0), "$0.00");
  assert.equal(qtyWords(undefined, "t"), "Not counted");
  assert.equal(qtyWords("", "t"), "Not counted");
  assert.equal(qtyWords("12.5", "cu yd"), "12.5 cu yd");
});

test("an order with no asked-for day says Not set in its row and its promise, never a blank or n/a", () => {
  const row = orderRowWords({ order_no: "SO-1", status: "CONFIRMED", fulfillment: "PICKUP", total_cents: 1000, requested_date: "" });
  assert.ok(row.includes("Not set"), row);
  assert.doesNotMatch(row, /n\/a/);
  assert.equal(promisedWords({ fulfillment: "PICKUP", requested_date: "" }).day, "Not set");
});

function flat(template) {
  if (template === null || template === undefined || template === false) return "";
  if (Array.isArray(template)) return template.map(flat).join(" ");
  if (typeof template === "object" && "strings" in template) {
    return template.strings.map((s, i) => s + (i < template.values.length ? flat(template.values[i]) : "")).join("");
  }
  return String(template);
}
const plain = (t) => flat(t).replace(/<[^>]*>/g, " ").replace(/\s+/g, " ");

test("dayWords reads a bare day, a moment in the dealer's zone, and Not set for anything unreadable", () => {
  assert.equal(dayWords("2042-05-09"), "May 9, 2042");
  assert.equal(dayWords("2042-05-09T02:00:00Z"), "May 8, 2042");
  assert.equal(dayWords(null), "Not set");
  assert.equal(dayWords(undefined), "Not set");
  assert.equal(dayWords("soon"), "Not set");
  assert.equal(dayWords(""), "Not set");
});

test("projects, invoices, invites and returns with missing figures or days never read n/a", () => {
  const project = plain(projectView({ name: "J", job_address: "", spend_cents: null, order_count: 1, delivery_count: 0, has_quote: true, variance_cents: null, orders: [], invoices: [{ id: "i", invoice_no: "INV-1", status: "OPEN", balance_cents: null, total_cents: null }] }));
  assert.doesNotMatch(project, /n\/a/, project);
  assert.ok(project.includes("Not priced"), project);
  const team = plain(teamView({ phase: "ready", users: [], invites: [{ id: "x", email: "a@b.c", role: "buyer", expires_at: null }], currentUserId: "u", roleDraft: null, inviteBusy: false, inviteEmail: "", notice: "", error: "" }));
  assert.doesNotMatch(team, /n\/a/, team);
  assert.ok(team.includes("Ends Not set"), team);
});

test("the order page labels the asked day Wanted by, and Placed and Submitted name the same dealer day", () => {
  const detail = { id: "o", order_no: "SO-1", status: "CONFIRMED", fulfillment: "DELIVERY", requested_date: "2042-05-09", created_at: "2042-05-09T02:00:00Z", total_cents: 1000, subtotal_cents: 900, tax_cents: 100, lines: [] };
  const text = plain(orderView({ detail, phase: "ready", today: "2042-05-08" }));
  assert.ok(text.includes("Wanted by May 9"), text);
  assert.doesNotMatch(text, /Promised for|Ready by/, text);
  assert.ok(text.includes("Placed May 8"), text);
  assert.ok(/Submitted\s*May 8/.test(text), text);
});
