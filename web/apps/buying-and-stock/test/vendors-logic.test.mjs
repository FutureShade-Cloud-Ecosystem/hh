// The vendors area's plain logic: the checks made before a request is sent (they mirror the core's
// own validators so a person reads the sentence once, beside the field), the bodies the core
// expects, and the words for what is on file. Nothing is sent when a check fails.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  contactBody,
  filterVendors,
  mappingBody,
  vendorBody,
  vendorRows,
  refWords,
} from "../src/vendors/vendors-logic.ts";
import { refusalWords } from "../src/po/po-logic.ts";

const CURRENT = { name: "Alcli (demo)", payment_terms_days: 30 };

test("a vendor list the core answers as null reads as no vendors", () => {
  assert.deepEqual(vendorRows({ items: null }), []);
  assert.deepEqual(vendorRows({ items: [{ id: "1" }] }), [{ id: "1" }]);
});

test("the vendor list filters by name or number, ignoring case, and keeps the inactive ones when asked", () => {
  const rows = [
    { id: "1", vendor_no: "V-ALC", name: "Alcli (demo)", is_active: true },
    { id: "2", vendor_no: "V-TEC", name: "Techo Bloc (demo)", is_active: false },
  ];
  assert.deepEqual(filterVendors(rows, "alc", true).map((r) => r.id), ["1"]);
  assert.deepEqual(filterVendors(rows, "bloc", true).map((r) => r.id), ["2"]);
  assert.deepEqual(filterVendors(rows, "", true).map((r) => r.id), ["1", "2"]);
  assert.deepEqual(filterVendors(rows, "", false).map((r) => r.id), ["1"]);
});

test("a new vendor needs a name; terms and the email are optional and checked", () => {
  assert.match(vendorBody({ name: "  ", terms: "", email: "", clearEmail: false }, null).words, /vendor name is required/i);
  assert.match(vendorBody({ name: "x".repeat(201), terms: "", email: "", clearEmail: false }, null).words, /200 characters at most/);
  assert.match(vendorBody({ name: "Rymar", terms: "366", email: "", clearEmail: false }, null).words, /whole number of days from 0 to 365/);
  assert.match(vendorBody({ name: "Rymar", terms: "30.5", email: "", clearEmail: false }, null).words, /whole number of days/);
  assert.match(vendorBody({ name: "Rymar", terms: "", email: "two@a.test, three@a.test", clearEmail: false }, null).words, /one email address/);
  assert.match(vendorBody({ name: "Rymar", terms: "", email: "no-at-sign", clearEmail: false }, null).words, /one email address/);
  const made = vendorBody({ name: " Rymar Turf ", terms: "45", email: "orders@rymar.test", clearEmail: false }, null);
  assert.deepEqual(made, { ok: true, body: { name: "Rymar Turf", payment_terms_days: 45, order_email: "orders@rymar.test" } });
  assert.deepEqual(vendorBody({ name: "Rymar", terms: "", email: "", clearEmail: false }, null), { ok: true, body: { name: "Rymar" } });
});

test("a correction sends only what changed, and says so when nothing did", () => {
  assert.match(vendorBody({ name: "Alcli (demo)", terms: "30", email: "", clearEmail: false }, CURRENT).words, /Nothing changed/);
  assert.deepEqual(vendorBody({ name: "Alcli (demo)", terms: "45", email: "", clearEmail: false }, CURRENT), { ok: true, body: { payment_terms_days: 45 } });
  assert.deepEqual(vendorBody({ name: "Alcli Stone", terms: "", email: "", clearEmail: false }, CURRENT), { ok: true, body: { name: "Alcli Stone" } });
  assert.deepEqual(vendorBody({ name: "Alcli (demo)", terms: "30", email: "po@alcli.test", clearEmail: false }, CURRENT), { ok: true, body: { order_email: "po@alcli.test" } });
  // The email on file is never read back by the list, so clearing it is its own act.
  assert.deepEqual(vendorBody({ name: "Alcli (demo)", terms: "30", email: "", clearEmail: true }, CURRENT), { ok: true, body: { order_email: "" } });
  assert.match(vendorBody({ name: "", terms: "30", email: "", clearEmail: false }, CURRENT).words, /vendor name is required/i);
});

test("an ordering contact needs an email or a phone, and each is checked as the core checks it", () => {
  assert.match(contactBody({ name: "Desk", email: "", phone: "" }).words, /needs an email or a phone/);
  assert.match(contactBody({ name: "x".repeat(121), email: "a@b.test", phone: "" }).words, /at most 120 characters/);
  assert.match(contactBody({ name: "", email: "a b@c.test", phone: "" }).words, /not an address/);
  assert.match(contactBody({ name: "", email: "plain", phone: "" }).words, /not an address/);
  assert.match(contactBody({ name: "", email: "", phone: "9".repeat(33) }).words, /254 characters and a phone at most 32/);
  assert.deepEqual(contactBody({ name: " Orders desk ", email: " po@alcli.test ", phone: "" }), { ok: true, body: { contact_name: "Orders desk", email: "po@alcli.test", phone: "" } });
  assert.deepEqual(contactBody({ name: "", email: "", phone: "613 555 0140" }), { ok: true, body: { contact_name: "", email: "", phone: "613 555 0140" } });
});

test("a vendor code mapping is all or nothing on its unit, and never empty", () => {
  assert.match(mappingBody({ productId: "", code: "A1", unit: "", factor: "" }).words, /Pick the product/);
  assert.match(mappingBody({ productId: "p", code: "x".repeat(65), unit: "", factor: "" }).words, /at most 64 characters/);
  assert.match(mappingBody({ productId: "p", code: "A1", unit: "BDL", factor: "" }).words, /unit needs the number of our units/);
  assert.match(mappingBody({ productId: "p", code: "A1", unit: "", factor: "12" }).words, /unit needs the number of our units/);
  assert.match(mappingBody({ productId: "p", code: "A1", unit: "BDL", factor: "0" }).words, /how many of our units one vendor unit holds/);
  assert.match(mappingBody({ productId: "p", code: "A1", unit: "BDL", factor: "-2" }).words, /how many of our units one vendor unit holds/);
  assert.match(mappingBody({ productId: "p", code: "", unit: "", factor: "" }).words, /Write a vendor code or a vendor unit/);
  assert.deepEqual(mappingBody({ productId: "p", code: " ALC-1 ", unit: "bdl", factor: "12.5" }), {
    ok: true,
    body: { product_id: "p", vendor_sku: "ALC-1", vendor_uom: "BDL", qty_per_vendor_uom: 12.5 },
  });
  assert.deepEqual(mappingBody({ productId: "p", code: "ALC-1", unit: "", factor: "" }), { ok: true, body: { product_id: "p", vendor_sku: "ALC-1" } });
});

test("the echoed reference is asked for in words, and a miss reads as one", () => {
  assert.equal(refWords("  "), "Type the order number the vendor quoted.");
  assert.equal(refWords("PO PLM 66"), "");
});

test("a wall refusal names who may change a vendor", () => {
  assert.match(refusalWords("vendor", "Forbidden", 403), /Adding and changing vendors/);
  assert.equal(refusalWords("vendor", "vendor name is required", 400), "Vendor name is required.");
  assert.equal(refusalWords("vendor", "not found", 404), "Not found.");
});
