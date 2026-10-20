// The page's pure logic, in Node: the totals framing, the acceptance amount
// phrase, the typed name gate, the status words, the money formatter, and the
// field-by-field response validation. These are the decisions the customer
// reads, so they are held here before any browser runs: the same split the
// older product used for this page (the render stays a render function, the
// decision it leans on lives beside it and is unit-tested).
import { test } from "node:test";
import assert from "node:assert/strict";

import {
  invoiceTaxNote,
  midSentence,
  acceptanceAmountPhrase,
  acceptanceRefusal,
  acceptReady,
  acceptHint,
  carriesTax,
  cardSurchargeCents,
  offersCardPayment,
  optionalLineNos,
  quoteMoneyExcludingOptional,
  repHasContact,
  repMailtoHref,
  showsImages,
  statusBadgeKind,
  statusBadgeLabel,
  totalsBlock,
} from "../src/_quote-public.js";
import { formatMoney, plainDay, plainWhen, quantityLabel, quoteNumberLabel, quoteStatusPhrase, withFullStop } from "../src/format.js";
import { createHash } from "node:crypto";
import { createAcceptKeys, deriveAcceptKey } from "../src/accept-key.js";
import { isKeyReuseRefusal, validateAcceptance, validateQuoteView } from "../src/quote-client.js";

const TAXED = { subtotalCents: 9665, taxCents: 1257, totalCents: 10922, taxLabel: "HST" };
const UNTAXED = { subtotalCents: 9665, taxCents: 0, totalCents: 9665, taxLabel: "HST" };
const CA_TAXED = { subtotalCents: 9665, taxCents: 749, totalCents: 10414, taxLabel: "Sales tax" };
const CA_UNTAXED = { subtotalCents: 9665, taxCents: 0, totalCents: 9665, taxLabel: "Sales tax" };

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
  assert.equal(block.note, invoiceTaxNote("HST"));
  assert.equal(block.note, "HST is applied when this quote is invoiced.");
  assert.equal(block.preTax, true);
});

test("the acceptance amount phrase names the HST inside a taxed total", () => {
  assert.equal(acceptanceAmountPhrase(TAXED), "$109.22, including $12.57 HST,");
  assert.equal(acceptanceAmountPhrase(UNTAXED), "$96.65 before HST");
});

test("a California dealer's quote speaks Sales tax in every place the claim is made", () => {
  assert.deepEqual(
    totalsBlock(CA_TAXED).rows.map((row) => row.label),
    ["Subtotal $96.65", "Sales tax $7.49", "Total $104.14"],
  );
  assert.equal(totalsBlock(CA_TAXED).note, null);
  const pre = totalsBlock(CA_UNTAXED);
  assert.deepEqual(
    pre.rows.map((row) => row.label),
    ["Subtotal (before sales tax) $96.65", "Amount before sales tax $96.65"],
  );
  assert.equal(pre.note, "Sales tax is applied when this quote is invoiced.");
  assert.equal(acceptanceAmountPhrase(CA_TAXED), "$104.14, including $7.49 Sales tax,");
  assert.equal(acceptanceAmountPhrase(CA_UNTAXED), "$96.65 before sales tax");
  for (const row of [...totalsBlock(CA_TAXED).rows, ...pre.rows]) assert.ok(!row.label.includes("HST"), row.label);
});

test("an acronym keeps its capitals mid sentence and a phrase drops its capital", () => {
  assert.equal(midSentence("HST"), "HST");
  assert.equal(midSentence("GST"), "GST");
  assert.equal(midSentence("Sales tax"), "sales tax");
});

test("a quote or acceptance without the server's tax word is refused as data, never guessed", () => {
  const base = {
    quote_no: "Q-1", customer_name: "X", status: "SENT", expired: false, lines: [],
    subtotal_cents: 100, tax_cents: 8, total_cents: 108, as_of: "2026-01-01T00:00:00Z", acceptable: true,
  };
  assert.equal(validateQuoteView(base), null);
  assert.equal(validateQuoteView({ ...base, tax_label: "Sales tax" })?.taxLabel, "Sales tax");
  const acc = { quote_no: "Q-1", typed_name: "A", accepted_at: "x", subtotal_cents: 100, tax_cents: 8, total_cents: 108 };
  assert.equal(validateAcceptance(acc), null);
  assert.equal(validateAcceptance({ ...acc, tax_label: "HST" })?.taxLabel, "HST");
});

test("the accept gate refuses unreviewed quantities, a blank name and an unticked attestation, and passes the honest form", () => {
  assert.equal(
    acceptanceRefusal("Sample Customer", true, false),
    "Please tick the box to confirm you have reviewed the quantities on this quote.",
  );
  assert.equal(acceptanceRefusal("", false, true), "Please type your full name.");
  assert.equal(acceptanceRefusal("   ", false, true), "Please type your full name.");
  assert.equal(
    acceptanceRefusal("Sample Customer", false, true),
    "Please tick the box to confirm you accept this quote.",
  );
  assert.equal(acceptanceRefusal("Sample Customer", true, true), null);
});

test("the accept button is ready only with the quantities box, a name and the confirmation", () => {
  assert.equal(acceptReady("Sample Customer", true, true), true);
  assert.equal(acceptReady("Sample Customer", true, false), false);
  assert.equal(acceptReady("Sample Customer", false, true), false);
  assert.equal(acceptReady("  ", true, true), false);
});

test("thumbnails render only when the dealer chose images and a line carries one", () => {
  const line = { imageRef: "p-bluff" };
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
  customer_name: "Fieldstone Hardscapes Inc",
  status: "SENT",
  valid_until: "the-valid-until-marker",
  expired: false,
  lines: [
    {
      line_no: 1,
      description: "Bluff Natural Step",
      qty_entered: "1.0000",
      uom_entered: "PC",
      unit_price_cents: 2057,
      line_total_cents: 2057,
      image_ref: "p-bluff",
    },
  ],
  subtotal_cents: 2057,
  tax_cents: 267,
  total_cents: 2324,
  tax_label: "HST",
  as_of: "the-as-of-marker",
  acceptable: true,
};

test("a complete quote view parses field by field", () => {
  const view = validateQuoteView(FULL_VIEW);
  assert.notEqual(view, null);
  assert.equal(view.quoteNo, "Q-25");
  assert.equal(view.customerName, "Fieldstone Hardscapes Inc");
  assert.equal(view.status, "SENT");
  assert.equal(view.validUntil, "the-valid-until-marker");
  assert.equal(view.expired, false);
  assert.equal(view.lines.length, 1);
  assert.equal(view.lines[0].imageRef, "p-bluff");
  assert.equal(view.subtotalCents, 2057);
  assert.equal(view.acceptable, true);
});

test("the dealer's quantity notice is read as sent, and absent is honest, never a sentence of the page's own", () => {
  const words = "Check every quantity against your plan before you accept.";
  assert.equal(validateQuoteView({ ...FULL_VIEW, quantity_notice: words }).quantityNotice, words);
  assert.equal(validateQuoteView(FULL_VIEW).quantityNotice, null);
  assert.equal(validateQuoteView({ ...FULL_VIEW, quantity_notice: "" }).quantityNotice, null);
  assert.equal(validateQuoteView({ ...FULL_VIEW, quantity_notice: 7 }).quantityNotice, null);
});

test("the order number is read only on a CONVERTED quote, as sent, and absent is honest", () => {
  const converted = { ...FULL_VIEW, status: "CONVERTED", acceptable: false, order_no: "SO-PLM-17" };
  assert.equal(validateQuoteView(converted).orderNo, "SO-PLM-17");
  // The fence is the view's own: a number on any other status is not honoured, whatever sent it.
  for (const status of ["DRAFT", "SENT", "ACCEPTED", "EXPIRED", "LOST"]) {
    assert.equal(validateQuoteView({ ...converted, status }).orderNo, null, `${status} carries no order number`);
  }
  assert.equal(validateQuoteView({ ...converted, order_no: undefined }).orderNo, null);
  assert.equal(validateQuoteView({ ...converted, order_no: "" }).orderNo, null);
  assert.equal(validateQuoteView({ ...converted, order_no: 7 }).orderNo, null);
  assert.equal(validateQuoteView(FULL_VIEW).orderNo, null);
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
    tax_label: "HST",
  });
  assert.notEqual(ok, null);
  assert.equal(ok.typedName, "Sample Customer");
  assert.equal(ok.totalCents, 10922);
  // The echo of the confirmation is read, and never assumed.
  assert.equal(ok.quantitiesConfirmed, false);
  const confirmed = validateAcceptance({
    quote_no: "Q-25",
    typed_name: "Sample Customer",
    accepted_at: "the-accepted-at-marker",
    subtotal_cents: 9665,
    tax_cents: 1257,
    total_cents: 10922,
    tax_label: "HST",
    quantities_confirmed: true,
  });
  assert.equal(confirmed.quantitiesConfirmed, true);

  const noTime = {
    quote_no: "Q-25",
    typed_name: "Sample Customer",
    subtotal_cents: 9665,
    tax_cents: 1257,
    total_cents: 10922,
    tax_label: "HST",
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

test("the quote number carries its mark once, whichever way the core serves it", () => {
  assert.equal(quoteNumberLabel("#Q-6"), "#Q-6");
  assert.equal(quoteNumberLabel("Q-6"), "#Q-6");
  assert.equal(quoteNumberLabel("##Q-6"), "#Q-6");
});

test("a moment and a day read in the page's own words, never as raw stamps", () => {
  assert.match(plainWhen("2026-10-06T19:49:47Z"), /^[A-Z][a-z]{2} \d{1,2}, \d{4}, \d{1,2}:\d{2} [AP]M$/);
  assert.equal(plainWhen("not a time"), "not a time", "an unreadable stamp is shown as it arrived, never invented");
  assert.equal(plainDay("2026-10-27"), "Oct 27, 2026");
  assert.equal(plainDay("2026-02-31"), "2026-02-31", "a day that does not exist is shown as it arrived");
  assert.equal(plainDay("soon"), "soon");
});

test("the preview flag is read only when the core sets it", () => {
  const base = JSON.parse(JSON.stringify(FULL_VIEW));
  assert.equal(validateQuoteView(base).preview, false, "a customer's own view carries no preview flag");
  assert.equal(validateQuoteView({ ...base, preview: true }).preview, true);
  assert.equal(validateQuoteView({ ...base, preview: "yes" }).preview, false, "only a true boolean counts");
});

test("the disabled Accept button says what is still missing, in the order the form reads", () => {
  assert.equal(
    acceptHint("", false, false),
    "To accept, tick the quantities box, type your full name and tick the confirmation.",
  );
  assert.equal(acceptHint("", false, true), "To accept, type your full name and tick the confirmation.");
  assert.equal(acceptHint("Sample Customer", false, true), "To accept, tick the confirmation.");
  assert.equal(acceptHint("Sample Customer", true, false), "To accept, tick the quantities box.");
  assert.equal(acceptHint("   ", true, true), "To accept, type your full name.");
  assert.equal(acceptHint("Sample Customer", true, true), null);
});

test("quote-client keeps its statements closed and its lines free of trailing space", async () => {
  const { readFileSync } = await import("node:fs");
  const { dirname, join } = await import("node:path");
  const { fileURLToPath } = await import("node:url");
  const source = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "..", "src", "quote-client.ts"), "utf8");
  const lines = source.split("\n");
  assert.deepEqual(
    lines.map((l, i) => (/\s$/.test(l) ? i + 1 : 0)).filter(Boolean),
    [],
    "no line ends in white space",
  );
  assert.deepEqual(
    lines.map((l, i) => (/^\s*return .*[^;{(,\[\s]$/.test(l) ? i + 1 : 0)).filter(Boolean),
    [],
    "every single line return ends in a semicolon",
  );
});

test("the status badge label is the customer-facing word for each state, and degrades honestly on a new one", () => {
  assert.equal(statusBadgeLabel("DRAFT"), "Draft");
  assert.equal(statusBadgeLabel("SENT"), "Awaiting acceptance");
  assert.equal(statusBadgeLabel("ACCEPTED"), "Accepted");
  assert.equal(statusBadgeLabel("CONVERTED"), "Order created");
  assert.equal(statusBadgeLabel("EXPIRED"), "Expired");
  assert.equal(statusBadgeLabel("LOST"), "Withdrawn");
  // A new state reads in its own words rather than as "Unknown": the chip's
  // colour would be no help on a never-seen-before state, so the words carry.
  assert.equal(statusBadgeLabel("SOME_NEW_STATE"), "Some new state");
});

test("the status badge colour kind picks the token family each state was measured for", () => {
  assert.equal(statusBadgeKind("SENT"), "info");
  assert.equal(statusBadgeKind("ACCEPTED"), "success");
  assert.equal(statusBadgeKind("CONVERTED"), "accent");
  assert.equal(statusBadgeKind("EXPIRED"), "danger");
  assert.equal(statusBadgeKind("LOST"), "danger");
  // A new state defaults to info rather than guessing; the contrast pair holds.
  assert.equal(statusBadgeKind("SOMETHING_ELSE"), "info");
});

test("excluding an optional add-on line recomputes the totals proportionally, never silently to zero", () => {
  const money = { subtotalCents: 60000, taxCents: 7800, totalCents: 67800, taxLabel: "HST" };
  // Excluding zero lines is an identity: the same money comes back.
  assert.deepEqual(quoteMoneyExcludingOptional(money, []), money);
  // Excluding a 8000-cent add-on drops the subtotal, tax scales, total scales.
  const adjusted = quoteMoneyExcludingOptional(money, [8000]);
  assert.equal(adjusted.subtotalCents, 52000);
  assert.equal(adjusted.taxCents, 6760);
  assert.equal(adjusted.totalCents, 58760);
  assert.equal(adjusted.taxLabel, "HST");
});

test("excluding ALL subtotal degenerates to a zero subtotal without claiming a tax that is not there", () => {
  const money = { subtotalCents: 8000, taxCents: 1040, totalCents: 9040, taxLabel: "HST" };
  const adjusted = quoteMoneyExcludingOptional(money, [8000]);
  assert.equal(adjusted.subtotalCents, 0);
  assert.equal(adjusted.taxCents, 0);
  assert.equal(adjusted.totalCents, 0);
  assert.equal(adjusted.taxLabel, "HST");
});

test("optionalLineNos lists only the lines the dealer flagged as optional, in server order", () => {
  const lines = [
    { lineNo: 1, optional: false },
    { lineNo: 2, optional: true },
    { lineNo: 3, optional: null },
    { lineNo: 4, optional: true },
  ];
  assert.deepEqual(optionalLineNos({ lines }), [2, 4]);
  assert.deepEqual(optionalLineNos({ lines: [] }), []);
});

test("cardSurchargeCents computes the surcharge as integer cents, never a fraction, and refuses when the dealer did not configure it", () => {
  // 2.9% of 10922 cents is 317 (rounded), the surcharge the customer sees.
  assert.equal(cardSurchargeCents(10922, 2.9), 317);
  assert.equal(cardSurchargeCents(10000, 3), 300);
  // No configured surcharge: the page offers ACH only, the helper returns zero.
  assert.equal(cardSurchargeCents(10922, null), 0);
  assert.equal(cardSurchargeCents(10922, 0), 0);
  assert.equal(cardSurchargeCents(10922, -2.9), 0);
  // Defensive: a NaN percentage and a zero total refuse rather than fabricate a figure.
  assert.equal(cardSurchargeCents(10922, Number.NaN), 0);
  assert.equal(cardSurchargeCents(0, 2.9), 0);
});

test("offersCardPayment is true only when the dealer configured a positive surcharge", () => {
  assert.equal(offersCardPayment({ cardSurchargePct: 2.9 }), true);
  assert.equal(offersCardPayment({}), false);
  assert.equal(offersCardPayment({ cardSurchargePct: null }), false);
  assert.equal(offersCardPayment({ cardSurchargePct: 0 }), false);
  assert.equal(offersCardPayment({ cardSurchargePct: -1 }), false);
  assert.equal(offersCardPayment({ cardSurchargePct: Number.NaN }), false);
});

test("repHasContact renders a rep card only when there is a name to address", () => {
  assert.equal(repHasContact({ repName: "Marisol Reyes", repEmail: "m@e.test", repPhone: "555-0140" }), true);
  assert.equal(repHasContact({ repName: "  Marisol Reyes  ", repEmail: null, repPhone: null }), true);
  // An email alone does not earn a card: no one to address.
  assert.equal(repHasContact({ repName: null, repEmail: "m@e.test", repPhone: null }), false);
  assert.equal(repHasContact({ repName: "", repEmail: "m@e.test" }), false);
  assert.equal(repHasContact({ repName: null }), false);
  assert.equal(repHasContact({}), false);
});

test("repMailtoHref carries the rep's email and a subject naming the quote", () => {
  assert.equal(repMailtoHref("Q-90", "marisol@example.test"), "mailto:marisol@example.test?subject=Question%20about%20quote%20Q-90");
  // A missing email becomes a bare mailto:, the customer types their own.
  assert.equal(repMailtoHref("Q-90", null), "mailto:?subject=Question%20about%20quote%20Q-90");
  assert.equal(repMailtoHref("Q-90", ""), "mailto:?subject=Question%20about%20quote%20Q-90");
  assert.equal(repMailtoHref("Q-90", "   "), "mailto:?subject=Question%20about%20quote%20Q-90");
});

test("validateQuoteView reads the rep fields and the card surcharge as sent, and absent is honest", () => {
  const withRep = {
    ...FULL_VIEW,
    rep_name: "Marisol Reyes",
    rep_email: "marisol@example.test",
    rep_phone: "+1-555-0140",
    card_surcharge_pct: 2.9,
  };
  const view = validateQuoteView(withRep);
  assert.notEqual(view, null);
  assert.equal(view.repName, "Marisol Reyes");
  assert.equal(view.repEmail, "marisol@example.test");
  assert.equal(view.repPhone, "+1-555-0140");
  assert.equal(view.cardSurchargePct, 2.9);
  // Absent is honest: a quote without rep fields is a quote without a rep card.
  const bare = validateQuoteView(FULL_VIEW);
  assert.notEqual(bare, null);
  assert.equal(bare.repName, null);
  assert.equal(bare.repEmail, null);
  assert.equal(bare.repPhone, null);
  assert.equal(bare.cardSurchargePct, null);
  // Wrong types are refused as data: a number cannot stand in for a name.
  assert.equal(validateQuoteView({ ...FULL_VIEW, rep_name: 7 }).repName, null);
  // A non-finite surcharge percentage is refused, never guessed.
  assert.equal(validateQuoteView({ ...FULL_VIEW, card_surcharge_pct: Number.NaN }).cardSurchargePct, null);
  assert.equal(validateQuoteView({ ...FULL_VIEW, card_surcharge_pct: "2.9" }).cardSurchargePct, null);
});

test("validateQuoteView reads the line's optional flag as sent, and absent is honest (defaults false)", () => {
  const withOptional = {
    ...FULL_VIEW,
    lines: [
      { ...FULL_VIEW.lines[0], line_no: 1 },
      { ...FULL_VIEW.lines[0], line_no: 2, optional: true, line_total_cents: 8000, unit_price_cents: 8000 },
    ],
  };
  const view = validateQuoteView(withOptional);
  assert.notEqual(view, null);
  assert.equal(view.lines[0].optional, false);
  assert.equal(view.lines[1].optional, true);
  // Absent defaults false: an old server cannot add optional add-ons silently.
  const withoutOptional = validateQuoteView(FULL_VIEW);
  assert.equal(withoutOptional.lines[0].optional, false);
});

test("quantityLabel: the unit after the figure, no trailing zeros, catalogue codes said in words", () => {
  assert.equal(quantityLabel("200.0000", "SF"), "200 sq ft");
  assert.equal(quantityLabel("1.5000", "T"), "1.5 T");
  assert.equal(quantityLabel("30.0000", "PC"), "30 pc");
  assert.equal(quantityLabel("2.2500", "CYD"), "2.25 cu yd");
  assert.equal(quantityLabel("1200.0000", "BAG"), "1,200 bag");
  assert.equal(quantityLabel("3", "ZZ"), "3 ZZ", "an unknown unit code is shown as served");
  assert.equal(quantityLabel("", "PC"), "", "no figure, nothing invented");
});
