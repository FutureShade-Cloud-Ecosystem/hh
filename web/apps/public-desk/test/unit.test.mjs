// The page's pure logic, in Node: the totals framing, the acceptance amount
// phrase, the typed name gate, the status words, the money formatter, and the
// field-by-field response validation. These are the decisions the customer
// reads, so they are held here before any browser runs: the same split the
// older product used for this page (the render stays a render function, the
// decision it leans on lives beside it and is unit-tested).
import { test } from "node:test";
import assert from "node:assert/strict";

import {
  INVOICE_TAX_NOTE,
  acceptanceAmountPhrase,
  acceptanceRefusal,
  carriesTax,
  showsImages,
  totalsBlock,
} from "../src/_quote-public.js";
import { formatMoney, quoteStatusPhrase, withFullStop } from "../src/format.js";
import { createHash } from "node:crypto";
import { createAcceptKeys, deriveAcceptKey } from "../src/accept-key.js";
import { isKeyReuseRefusal, validateAcceptance, validateQuoteView } from "../src/quote-client.js";

const TAXED = { subtotalCents: 9665, taxCents: 1257, totalCents: 10922 };
const UNTAXED = { subtotalCents: 9665, taxCents: 0, totalCents: 9665 };

test("carriesTax is a claim about the data, defensible on both sides", () => {
  assert.equal(carriesTax(TAXED), true);
  assert.equal(carriesTax(UNTAXED), false);
  // A missing, unreadable or negative tax figure is UNTAXED, never "tax included":
  // an older server that left the field out served the before-HST shape, and no
  // quote carries a negative tax, so neither may claim the total already holds HST.
  assert.equal(carriesTax({}), false);
  assert.equal(carriesTax({ taxCents: undefined }), false);
  assert.equal(carriesTax({ taxCents: Number.NaN }), false);
  assert.equal(carriesTax({ taxCents: -1 }), false);
  assert.equal(carriesTax(null), false);
  assert.equal(carriesTax(undefined), false);
});

test("a taxed quote reads Subtotal, HST, Total, and says nothing about future invoicing", () => {
  const block = totalsBlock(TAXED);
  assert.deepEqual(
    block.rows.map((row) => row.label),
    ["Subtotal $96.65", "HST $12.57", "Total $109.22"],
  );
  assert.deepEqual(
    block.rows.map((row) => row.grand),
    [false, false, true],
  );
  // The note is SUPPRESSED: the HST is already in the total above, and printing
  // "applied when invoiced" under it invites the customer to add it twice.
  assert.equal(block.note, null);
  assert.equal(block.preTax, false);
});

test("an untaxed quote keeps the before-HST framing, note and all", () => {
  const block = totalsBlock(UNTAXED);
  assert.deepEqual(
    block.rows.map((row) => row.label),
    ["Subtotal (before HST) $96.65", "Amount before HST $96.65"],
  );
  assert.deepEqual(
    block.rows.map((row) => row.grand),
    [false, true],
  );
  assert.equal(block.note, INVOICE_TAX_NOTE);
  assert.equal(block.preTax, true);
});

test("the acceptance amount phrase names the HST inside a taxed total", () => {
  assert.equal(acceptanceAmountPhrase(TAXED), "$109.22, including $12.57 HST,");
  assert.equal(acceptanceAmountPhrase(UNTAXED), "$96.65 before HST");
});

test("the typed name gate refuses blank and unticked, and passes the honest form", () => {
  assert.equal(acceptanceRefusal("", false), "Please type your full name.");
  assert.equal(acceptanceRefusal("   ", false), "Please type your full name.");
  assert.equal(
    acceptanceRefusal("Sample Customer", false),
    "Please tick the box to confirm you accept this quote.",
  );
  assert.equal(acceptanceRefusal("Sample Customer", true), null);
});

test("thumbnails render only when the dealer chose images and a line carries one", () => {
  const line = { imageRef: "p-banas" };
  const bare = {};
  assert.equal(showsImages({ includeImages: true, lines: [line] }), true);
  assert.equal(showsImages({ includeImages: false, lines: [line] }), false);
  assert.equal(showsImages({ includeImages: true, lines: [bare] }), false);
  assert.equal(showsImages({ includeImages: null, lines: [line] }), false);
  assert.equal(showsImages({ lines: [line] }), false);
});

test("the status words read mid-sentence, and an unknown state degrades honestly", () => {
  assert.equal(quoteStatusPhrase("DRAFT"), "a draft");
  assert.equal(quoteStatusPhrase("SENT"), "sent");
  assert.equal(quoteStatusPhrase("ACCEPTED"), "accepted");
  assert.equal(quoteStatusPhrase("CONVERTED"), "converted");
  assert.equal(quoteStatusPhrase("EXPIRED"), "expired");
  assert.equal(quoteStatusPhrase("LOST"), "lost");
  assert.equal(quoteStatusPhrase("SOME_NEW_STATE"), "some new state");
});

test("a sentence ending on the timestamp carries one full stop, never two", () => {
  // The en-CA locale time already ends in a period ("2:51:42 a.m."), which is
  // how the thank-you line grew a double full stop: the sentence must keep
  // the timestamp's own stop and add nothing.
  assert.equal(
    withFullStop("Accepted by Sample Customer on DAY, 2:51:42 a.m."),
    "Accepted by Sample Customer on DAY, 2:51:42 a.m.",
  );
  // A clock style without one still ends its sentence.
  assert.equal(
    withFullStop("Accepted by Sample Customer on DAY, 14:51:42"),
    "Accepted by Sample Customer on DAY, 14:51:42.",
  );
  assert.equal(withFullStop("Already a full sentence."), "Already a full sentence.");
  assert.equal(withFullStop("Ends on a question?"), "Ends on a question?");
});

test("money renders from integer cents, and the unknown renders as a placeholder, never zero", () => {
  assert.equal(formatMoney(9665), "$96.65");
  assert.equal(formatMoney(1257), "$12.57");
  assert.equal(formatMoney(10922), "$109.22");
  assert.equal(formatMoney(123456789), "$1,234,567.89");
  assert.equal(formatMoney(-311), "-$3.11");
  assert.equal(formatMoney(0), "$0.00");
  // A figure the server left out is never a plausible zero.
  assert.equal(formatMoney(undefined), "(not priced)");
  assert.equal(formatMoney(null), "(not priced)");
  assert.equal(formatMoney(10.5), "(not priced)");
});

// -- the response validation: a field the answer lacks is a refusal, never a
// default, so the page never renders a half-known quote.

const FULL_VIEW = {
  quote_no: "Q-25",
  customer_name: "Quinte Hardscapes Inc",
  status: "SENT",
  valid_until: "the-valid-until-marker",
  expired: false,
  lines: [
    {
      line_no: 1,
      description: "Banas Natural Step",
      qty_entered: "1.0000",
      uom_entered: "PC",
      unit_price_cents: 2057,
      line_total_cents: 2057,
      image_ref: "p-banas",
    },
  ],
  subtotal_cents: 2057,
  tax_cents: 267,
  total_cents: 2324,
  as_of: "the-as-of-marker",
  acceptable: true,
};

test("a complete quote view parses field by field", () => {
  const view = validateQuoteView(FULL_VIEW);
  assert.notEqual(view, null);
  assert.equal(view.quoteNo, "Q-25");
  assert.equal(view.customerName, "Quinte Hardscapes Inc");
  assert.equal(view.status, "SENT");
  assert.equal(view.validUntil, "the-valid-until-marker");
  assert.equal(view.expired, false);
  assert.equal(view.lines.length, 1);
  assert.equal(view.lines[0].imageRef, "p-banas");
  assert.equal(view.subtotalCents, 2057);
  assert.equal(view.acceptable, true);
});

test("an answer missing a field, or carrying a wrong one, is refused whole", () => {
  assert.equal(validateQuoteView(null), null);
  assert.equal(validateQuoteView("nope"), null);
  const noQuoteNo = { ...FULL_VIEW };
  delete noQuoteNo.quote_no;
  assert.equal(validateQuoteView(noQuoteNo), null, "a view with no quote number is refused");
  const floatMoney = { ...FULL_VIEW, subtotal_cents: 20.57 };
  assert.equal(validateQuoteView(floatMoney), null, "a float cent figure is refused");
  const unknownStatus = { ...FULL_VIEW, status: "MAYBE" };
  assert.equal(validateQuoteView(unknownStatus), null, "an unknown status is refused");
  const badLine = {
    ...FULL_VIEW,
    lines: [{ ...FULL_VIEW.lines[0], uom_entered: undefined }],
  };
  assert.equal(validateQuoteView(badLine), null, "a line without its unit is refused");
  const badLineMoney = {
    ...FULL_VIEW,
    lines: [{ ...FULL_VIEW.lines[0], line_total_cents: "20.57" }],
  };
  assert.equal(validateQuoteView(badLineMoney), null, "a line total that is not an integer is refused");
});

test("a line whose description is the empty string is served, a line with no description field is refused", () => {
  // The core's line read can serve '' (the legacy fallback when a line has
  // neither its own words nor a product name): one such line must not turn
  // the whole page into "try again shortly".
  const emptyDescription = { ...FULL_VIEW, lines: [{ ...FULL_VIEW.lines[0], description: "" }] };
  const view = validateQuoteView(emptyDescription);
  assert.notEqual(view, null);
  assert.equal(view.lines[0].description, "");
  const noDescription = { ...FULL_VIEW, lines: [{ ...FULL_VIEW.lines[0] }] };
  delete noDescription.lines[0].description;
  assert.equal(validateQuoteView(noDescription), null, "a missing description field is still refused");
  const numeric = { ...FULL_VIEW, lines: [{ ...FULL_VIEW.lines[0], description: 5 }] };
  assert.equal(validateQuoteView(numeric), null, "a non string description is refused");
});

test("the acceptance result parses, and an answer without its timestamp is refused", () => {
  const ok = validateAcceptance({
    quote_no: "Q-25",
    typed_name: "Sample Customer",
    accepted_at: "the-accepted-at-marker",
    subtotal_cents: 9665,
    tax_cents: 1257,
    total_cents: 10922,
  });
  assert.notEqual(ok, null);
  assert.equal(ok.typedName, "Sample Customer");
  assert.equal(ok.totalCents, 10922);

  const noTime = {
    quote_no: "Q-25",
    typed_name: "Sample Customer",
    subtotal_cents: 9665,
    tax_cents: 1257,
    total_cents: 10922,
  };
  assert.equal(validateAcceptance(noTime), null);
  assert.equal(validateAcceptance(null), null);
  assert.equal(validateAcceptance(undefined), null);
});

test("the page rides the design system's shared Inter, with its licence beside the faces, and keeps no face of its own", async () => {
  const { existsSync, readdirSync, readFileSync } = await import("node:fs");
  const { dirname, join } = await import("node:path");
  const { fileURLToPath } = await import("node:url");
  const here = dirname(fileURLToPath(import.meta.url));
  // The type pass (row 942) moved the vendored face into the design system:
  // this app once carried four Outfit woff2 under public/fonts, and that copy
  // is gone for the shared Inter every importing app receives through the
  // token sheet. The licence still sits beside the faces it licenses.
  const own = join(here, "..", "public", "fonts");
  assert.ok(!existsSync(own), "the public desk must not vendor a face of its own any more");
  const fonts = join(here, "..", "..", "..", "packages", "design-system", "src", "fonts");
  const files = readdirSync(fonts);
  assert.equal(files.filter((name) => name.startsWith("inter-") && name.endsWith(".woff2")).length, 4);
  assert.ok(files.includes("OFL.txt"), "OFL.txt must sit beside the font files");
  const text = readFileSync(join(fonts, "OFL.txt"), "utf8");
  assert.match(text, /^Copyright .* The Inter Project Authors/);
  assert.match(text, /SIL OPEN FONT LICENSE Version 1\.1/);
  assert.match(text, /PERMISSION & CONDITIONS/);
  assert.match(text, /DISCLAIMER/);
  // And the sheet that reaches this app's bundle registers the face itself:
  // the @font-face rules live in the token sheet main.ts imports, so no app
  // file of ours names a font URL.
  const sheet = readFileSync(join(here, "..", "..", "..", "packages", "design-system", "src", "tokens.css"), "utf8");
  assert.match(sheet, /font-family:\s*'Inter'/);
  assert.match(sheet, /url\('\.\/fonts\/inter-latin-400-normal\.woff2'\)/);
});

// -- the acceptance key: derived, never stored.

const UUID_SHAPE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

test("the accept key is the first 16 bytes of SHA-256 of token and typed name, as a name based UUID", async () => {
  const key = await deriveAcceptKey("tok-1", "Sample Customer");
  const bytes = createHash("sha256").update("quote-accept:tok-1:Sample Customer").digest().subarray(0, 16);
  bytes[6] = (bytes[6] & 0x0f) | 0x50;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  const hex = bytes.toString("hex");
  assert.equal(key, `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`);
  assert.match(key, UUID_SHAPE);
  assert.equal(key[14], "5", "version nibble");
  assert.ok("89ab".includes(key[19]), "variant nibble");
});

test("the accept key is deterministic and differs by token and by name", async () => {
  const a = await deriveAcceptKey("tok-1", "Sample Customer");
  assert.equal(await deriveAcceptKey("tok-1", "Sample Customer"), a);
  assert.notEqual(await deriveAcceptKey("tok-2", "Sample Customer"), a);
  assert.notEqual(await deriveAcceptKey("tok-1", "Another Customer"), a);
});

test("with no crypto.subtle the fallback is one random UUID per acceptance, stable in memory, never derived", async () => {
  assert.equal(await deriveAcceptKey("tok-1", "Sample Customer", {}), null);
  let calls = 0;
  const fake = {
    getRandomValues(bytes) {
      calls += 1;
      bytes.fill(calls);
      return bytes;
    },
  };
  const keys = createAcceptKeys(fake);
  const first = await keys.keyFor("tok-1", "Sample Customer");
  assert.match(first, UUID_SHAPE);
  assert.equal(first[14], "4", "random UUID version nibble");
  assert.equal(await keys.keyFor("tok-1", "Sample Customer"), first);
  assert.equal(calls, 1, "the second retry reuses the remembered key");
  assert.notEqual(await keys.keyFor("tok-1", "Another Customer"), first);
  assert.notEqual(await createAcceptKeys(fake).keyFor("tok-1", "Sample Customer"), first, "memory is per page life");
});

test("only the server's own key reuse refusal is a key reuse refusal", () => {
  assert.equal(isKeyReuseRefusal({ status: 409, error: "quote: this idempotency key was used for a different acceptance" }), true);
  assert.equal(isKeyReuseRefusal({ status: 409, error: "This quote has already been accepted." }), false);
  assert.equal(isKeyReuseRefusal({ status: 400, error: "quote: this idempotency key was used for a different acceptance" }), false);
  assert.equal(isKeyReuseRefusal({ error: "quote: this idempotency key was used for a different acceptance" }), false);
});
