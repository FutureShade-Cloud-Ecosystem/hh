// The binary gate. The contract's binary transfers ride the wire as bytes, so
// the client must read a format: binary success as bytes and send the binary
// uploads as bytes, byte for byte, with no text decode or re-encode anywhere.
// The fixtures are real binary payloads built here: a PNG file signature with
// its IHDR chunk for the images, the ZIP local file header (every xlsx is a
// ZIP) lives in test/result-shape.test.mjs. Every assertion compares bytes.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import * as api from "../src/index.ts";
import { recorder, unwrap } from "./_recorded.mjs";

// A PNG file signature (89 50 4E 47 0D 0A 1A 0A) plus a minimal IHDR chunk:
// length 13, the chunk name, width and height of one pixel, the depth and
// color type, and the chunk CRC. This is the lead of every real PNG file.
const PNG = Uint8Array.from([
  0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, // the PNG signature
  0x00, 0x00, 0x00, 0x0d, // the IHDR chunk's data length
  0x49, 0x48, 0x44, 0x52, // "IHDR"
  0x00, 0x00, 0x00, 0x01, // width 1
  0x00, 0x00, 0x00, 0x01, // height 1
  0x08, 0x02, // bit depth 8, truecolor
  0x00, 0x00, 0x00, // compression, filter, interlace
  0x90, 0x77, 0x53, 0xde, // the chunk CRC
]);

test("a format: binary success resolves the exact bytes the wire carried", async (t) => {
  const rec = recorder({ "GET /api/v1/products/abc/image": { bytes: PNG } });
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: rec.fetch });
  const value = unwrap(await api.staffProduct.productGetImage(client, "abc"));
  assert.ok(value instanceof Uint8Array, "the value is bytes");
  assert.equal(value.byteLength, PNG.byteLength);
  assert.deepEqual(value, PNG, "byte for byte, signature through CRC");
});

test("a refused binary route still reads the JSON error envelope with its status", async (t) => {
  const rec = recorder({ "GET /api/v1/products/abc/image": { status: 404, body: { error: "not found" } } });
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: rec.fetch });
  const result = await api.staffProduct.productGetImage(client, "abc");
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.error, "not found");
    assert.equal(result.status, 404);
  }
});

test("the product image upload sends the raw bytes verbatim with the type the caller says", async (t) => {
  const rec = recorder({ "PUT /api/v1/products/abc/image": { body: { size_bytes: PNG.byteLength } } });
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: rec.fetch, headers: () => ({ "X-Org": "dibbits" }) });
  unwrap(await api.staffProduct.productPutImage(client, "abc", PNG, "image/png"));
  const call = rec.calls[0];
  assert.equal(call.method, "PUT");
  assert.equal(call.headers["Content-Type"], "image/png", "the caller says which declared type rides");
  assert.ok(call.bytes instanceof Uint8Array, "the body rode as bytes");
  assert.deepEqual(call.bytes, PNG, "the upload is byte for byte what the caller held");
});

test("an upload declaring several types sends no Content-Type until the caller says", async (t) => {
  const rec = recorder({ "PUT /api/v1/products/abc/image": { body: { size_bytes: PNG.byteLength } } });
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: rec.fetch });
  unwrap(await api.staffProduct.productPutImage(client, "abc", PNG));
  assert.equal(
    rec.calls[0].headers["Content-Type"],
    undefined,
    "no silently chosen type: the bytes go out unlabeled, so the core refuses them, never misreads them",
  );
});

test("the dispatch photo upload sends the raw bytes verbatim with its caption query", async (t) => {
  const rec = recorder({ "POST /api/v1/dispatch/stops/stop-9/photos": { status: 201, body: { id: "p1" } } });
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: rec.fetch, headers: () => ({ "X-Org": "dibbits" }) });
  unwrap(await api.staffDispatch.dispatchUploadStopPhoto(client, "stop-9", PNG, "image/png", { caption: "gate 3, delivered" }));
  const call = rec.calls[0];
  assert.equal(call.headers["Content-Type"], "image/png");
  assert.deepEqual(call.bytes, PNG);
  assert.equal(call.query.caption, "gate 3, delivered");
});

// The multipart binary fields: the contract types them format: binary, so the
// client types them Blob and the transport appends the caller's Blob to the
// form verbatim. Each assertion parses the emitted body with the platform's
// own multipart encoder and parser (a real Request over the recorded
// FormData), then compares the field's bytes, so only a byte-exact ride
// passes. The fixtures carry bytes a string field would mangle (invalid
// UTF-8 lead bytes, 0x7f and 0x80).
test("the generated output types the multipart binary fields Blob, never string", () => {
  // An assertion over the GENERATED OUTPUT itself: a generator regression
  // that typed these fields string would still pass every runtime gate (a
  // string field String()s the caller's Blob into mangled text), so the
  // spellings are pinned here. `file: Blob` lives in the named wire type the
  // price file forms use; `image?: Blob` in feedbackCreateMultipart's inline
  // body.
  const types = readFileSync(new URL("../src/gen/types.ts", import.meta.url), "utf8");
  assert.match(types, /export type PricingPriceFileForm = \{[^}]*file: Blob;/, "the price file field is a Blob in the generated wire type");
  const feedback = readFileSync(new URL("../src/gen/staff-feedback.ts", import.meta.url), "utf8");
  assert.match(feedback, /feedbackCreateMultipart\([\s\S]*?image\?: Blob/, "the screenshot field is an optional Blob in the generated operation");
});
test("the price file multipart field rides as the caller's bytes, byte for byte", async (t) => {
  const PRICE_FILE = Uint8Array.from([0x50, 0x4b, 0x03, 0x04, 0xff, 0xfe, 0x7f, 0x80, 0x0d, 0x0a, 0x00, 0xc8]);
  const form = { vendor_id: "86b744fc-c0bf-4b07-a3c7-91b860d428f4", file: new Blob([PRICE_FILE]) };
  const rec = recorder({
    "POST /api/v1/pricing/price-file/preview": { body: {} },
    "POST /api/v1/pricing/price-file/apply": { status: 201, body: {} },
  });
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: rec.fetch, headers: () => ({ "X-Org": "dibbits" }) });
  unwrap(await api.staffPricing.pricingPreviewPriceFile(client, form));
  unwrap(await api.staffPricing.pricingApplyPriceFile(client, { ...form, digest: "preview-digest" }));
  assert.equal(rec.calls.length, 2);
  for (const call of rec.calls) {
    const parsed = await new Request(call.url, { method: call.method, body: call.form }).formData();
    const file = parsed.get("file");
    assert.ok(file instanceof Blob, "the file field rides as a Blob part");
    assert.deepEqual(new Uint8Array(await file.arrayBuffer()), PRICE_FILE, "byte for byte through a real multipart parse");
  }
});

test("the feedback screenshot multipart form rides as the caller's bytes, byte for byte", async (t) => {
  const rec = recorder({ "POST /api/v1/feedback": { status: 201, body: { id: "fb-1" } } });
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: rec.fetch, headers: () => ({ "X-Org": "dibbits" }) });
  unwrap(await api.staffFeedback.feedbackCreateMultipart(client, { type: "bug", message: "the board froze on drop", image: new Blob([PNG]) }));
  const call = rec.calls[0];
  const parsed = await new Request(call.url, { method: call.method, body: call.form }).formData();
  assert.equal(parsed.get("type"), "bug");
  assert.equal(parsed.get("message"), "the board froze on drop");
  const image = parsed.get("image");
  assert.ok(image instanceof Blob, "the screenshot rides as a Blob part");
  assert.deepEqual(new Uint8Array(await image.arrayBuffer()), PNG, "byte for byte through a real multipart parse");
});

// The part's name: a plain Blob reaches the core under the platform's default
// part name "blob" and the core stores that name, so the multipart operations
// take a filename option per binary field and the transport lands it in the
// part's Content-Disposition.
test("the multipart binary parts carry the caller's filename", async (t) => {
  const PRICE_FILE = Uint8Array.from([0x50, 0x4b, 0x03, 0x04, 0xff, 0xfe, 0x7f, 0x80]);
  const rec = recorder({
    "POST /api/v1/pricing/price-file/apply": { status: 201, body: {} },
    "POST /api/v1/feedback": { status: 201, body: { id: "fb-2" } },
  });
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: rec.fetch, headers: () => ({ "X-Org": "dibbits" }) });
  unwrap(
    await api.staffPricing.pricingApplyPriceFile(
      client,
      { vendor_id: "86b744fc-c0bf-4b07-a3c7-91b860d428f4", file: new Blob([PRICE_FILE]) },
      { fileFilename: "price-list.xlsx" },
    ),
  );
  unwrap(
    await api.staffFeedback.feedbackCreateMultipart(
      client,
      { type: "bug", message: "the board froze on drop", image: new Blob([PNG]) },
      { imageFilename: "board-frozen.png" },
    ),
  );
  assert.equal(rec.calls.length, 2);
  const applied = await new Request(rec.calls[0].url, { method: rec.calls[0].method, body: rec.calls[0].form }).formData();
  const priced = applied.get("file");
  assert.ok(priced instanceof Blob, "the file field still rides as a Blob part");
  assert.equal(priced.name, "price-list.xlsx", "the caller's filename rides the part");
  assert.deepEqual(new Uint8Array(await priced.arrayBuffer()), PRICE_FILE, "the bytes ride byte for byte under the given name");
  const feedback = await new Request(rec.calls[1].url, { method: rec.calls[1].method, body: rec.calls[1].form }).formData();
  const image = feedback.get("image");
  assert.ok(image instanceof Blob, "the screenshot still rides as a Blob part");
  assert.equal(image.name, "board-frozen.png", "the screenshot's filename rides the part");
});

test("a Blob named by no option rides verbatim, under the name the platform gives it", async (t) => {
  // Today's behaviour is the default: the transport adds no name of its own
  // and appends the caller's Blob itself. What an unnamed part is then called
  // is the platform's business (Node 22's undici names it "blob", bun leaves
  // it empty), so the assertion stops at the transport's own output.
  const rec = recorder({ "POST /api/v1/pricing/price-file/preview": { body: {} } });
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: rec.fetch, headers: () => ({ "X-Org": "dibbits" }) });
  unwrap(await api.staffPricing.pricingPreviewPriceFile(client, { vendor_id: "86b744fc-c0bf-4b07-a3c7-91b860d428f4", file: new Blob([PNG]) }));
  const entry = rec.calls[0].form.get("file");
  assert.ok(entry instanceof Blob, "the transport appended the caller's Blob, adding no name of its own");
  assert.deepEqual(new Uint8Array(await entry.arrayBuffer()), PNG, "byte for byte, as the platform's part");
});

// The caller's file name is guarded like a path value: refused as an error
// Result before anything is sent. Every refusal class is exercised through
// the real transport (the recorded FetchLike), and the empty recorder is what
// pins "before anything is sent": not one refused name reached a wire.
test("a file name that is empty, a dot segment, path-like, or control carrying is refused before anything is sent", async () => {
  const rec = recorder({ "POST /api/v1/pricing/price-file/apply": { status: 201, body: {} } });
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: rec.fetch });
  const refusals = [
    "", // empty
    ".", // the dot segment
    "..", // the parent dot segment
    "/", // the bare separator
    "\\", // the bare separator, the other platform's
    "a/b/price-list.xlsx", // a path riding the name
    "a\\b\\price-list.xlsx", // a Windows path riding the name
    "price\u0000list.xlsx", // a NUL byte
    "price\nlist.xlsx", // a line break, a control character
    "price\tlist.xlsx", // a tab, a control character
  ];
  for (const name of refusals) {
    const result = await api.staffPricing.pricingApplyPriceFile(
      client,
      { vendor_id: "86b744fc-c0bf-4b07-a3c7-91b860d428f4", file: new Blob([PNG]) },
      { fileFilename: name },
    );
    assert.equal(result.ok, false, `the name ${JSON.stringify(name)} is refused`);
    if (!result.ok) {
      assert.match(result.error, /the file part's file name /, `the refusal names the part, for ${JSON.stringify(name)}`);
    }
  }
  assert.equal(rec.calls.length, 0, "not one refused name dialed");
});

test("the longest accepted file name is 160 bytes, one more is refused", async () => {
  const rec = recorder({ "POST /api/v1/pricing/price-file/apply": { status: 201, body: {} } });
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: rec.fetch, headers: () => ({ "X-Org": "dibbits" }) });
  const tooLong = await api.staffPricing.pricingApplyPriceFile(
    client,
    { vendor_id: "86b744fc-c0bf-4b07-a3c7-91b860d428f4", file: new Blob([PNG]) },
    { fileFilename: "p".repeat(161) },
  );
  assert.equal(tooLong.ok, false);
  if (!tooLong.ok) assert.match(tooLong.error, /longer than the 160 bytes/);
  assert.equal(rec.calls.length, 0, "the over long name dialed nothing");
  unwrap(
    await api.staffPricing.pricingApplyPriceFile(
      client,
      { vendor_id: "86b744fc-c0bf-4b07-a3c7-91b860d428f4", file: new Blob([PNG]) },
      { fileFilename: "p".repeat(160) },
    ),
  );
  assert.equal(rec.calls.length, 1, "a name of exactly the bound dials");
  const parsed = await new Request(rec.calls[0].url, { method: rec.calls[0].method, body: rec.calls[0].form }).formData();
  const part = parsed.get("file");
  assert.ok(part instanceof Blob, "the guarded name still rides as a Blob part");
  assert.equal(part.name, "p".repeat(160), "the accepted name arrives whole");
});

test("the 160 bound counts UTF-8 bytes: a name of 160 CJK characters the old code point rule accepted is refused", async () => {
  // The core's stored-name cap counts bytes (strings.Builder.Len() after
  // each WriteRune, sanitizeFileName, pricefile_service.go lines 845 to 861),
  // so the client counts the same unit. The old rule counted code points and
  // accepted this name; 160 three-byte CJK characters are 480 bytes, and the
  // core would have cut the stored name to 54 characters.
  const rec = recorder({ "POST /api/v1/pricing/price-file/apply": { status: 201, body: {} } });
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: rec.fetch, headers: () => ({ "X-Org": "dibbits" }) });
  const tooManyBytes = await api.staffPricing.pricingApplyPriceFile(
    client,
    { vendor_id: "86b744fc-c0bf-4b07-a3c7-91b860d428f4", file: new Blob([PNG]) },
    { fileFilename: "\u65e5".repeat(160) },
  );
  assert.equal(tooManyBytes.ok, false, "160 three-byte characters are 480 bytes, past the bound");
  if (!tooManyBytes.ok) assert.match(tooManyBytes.error, /longer than the 160 bytes/);
  assert.equal(rec.calls.length, 0, "the 480 byte name dialed nothing");

  // The byte boundary itself, on the multi-byte side: 53 of the same
  // characters are 159 bytes and arrive whole; one more is 162 bytes and is
  // refused.
  unwrap(
    await api.staffPricing.pricingApplyPriceFile(
      client,
      { vendor_id: "86b744fc-c0bf-4b07-a3c7-91b860d428f4", file: new Blob([PNG]) },
      { fileFilename: "\u65e5".repeat(53) },
    ),
  );
  assert.equal(rec.calls.length, 1, "a name inside the byte bound dials");
  const parsed = await new Request(rec.calls[0].url, { method: rec.calls[0].method, body: rec.calls[0].form }).formData();
  const part = parsed.get("file");
  assert.ok(part instanceof Blob, "the accepted multibyte name still rides as a Blob part");
  assert.equal(part.name, "\u65e5".repeat(53), "the accepted multibyte name arrives whole");
  const oneMore = await api.staffPricing.pricingApplyPriceFile(
    client,
    { vendor_id: "86b744fc-c0bf-4b07-a3c7-91b860d428f4", file: new Blob([PNG]) },
    { fileFilename: "\u65e5".repeat(54) },
  );
  assert.equal(oneMore.ok, false, "54 three-byte characters are 162 bytes, past the bound");
  assert.equal(rec.calls.length, 1, "the 162 byte name dialed nothing");
});

test("a bidirectional control character in a file name is refused before anything is sent, each of the twelve tested", async () => {
  // The marks that reorder how text displays: an extension can be made to
  // appear reversed. The whole \p{Bidi_Control} class is refused: the nine
  // strong marks and isolates, plus the three reorder neutrals (U+061C ALM,
  // U+200E LRM, U+200F RLM) the first cut let through. The core's sanitizer
  // strips only the C0 controls and DEL and would store all twelve; the
  // client refuses them (the README carries that record). Each character is
  // asserted by its own name.
  const bidi = [
    ["\u061c", "U+061C ALM"],
    ["\u200e", "U+200E LRM"],
    ["\u200f", "U+200F RLM"],
    ["\u202a", "U+202A LRE"],
    ["\u202b", "U+202B RLE"],
    ["\u202c", "U+202C PDF"],
    ["\u202d", "U+202D LRO"],
    ["\u202e", "U+202E RLO"],
    ["\u2066", "U+2066 LRI"],
    ["\u2067", "U+2067 RLI"],
    ["\u2068", "U+2068 FSI"],
    ["\u2069", "U+2069 PDI"],
  ];
  const rec = recorder({ "POST /api/v1/pricing/price-file/apply": { status: 201, body: {} } });
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: rec.fetch });
  for (const [mark, named] of bidi) {
    const result = await api.staffPricing.pricingApplyPriceFile(
      client,
      { vendor_id: "86b744fc-c0bf-4b07-a3c7-91b860d428f4", file: new Blob([PNG]) },
      { fileFilename: `price${mark}list.xlsx` },
    );
    assert.equal(result.ok, false, `a name carrying the bidirectional mark ${named} is refused`);
    if (!result.ok) assert.match(result.error, /bidirectional control character/, `the refusal names the mark, for ${named}`);
  }
  assert.equal(rec.calls.length, 0, "not one bidi carrying name dialed");
});

test("an invisible formatting or control character in a file name is refused before anything is sent", async () => {
  // The \p{Cf} format characters and the \p{Cc} control characters the byte
  // loop cannot see (the C1 range U+0080 to U+009F): an invisible character
  // in a stored name makes names that display identically but differ in
  // bytes. A name made of nothing but one of them is refused too.
  const invisible = [
    ["\u00ad", "U+00AD SOFT HYPHEN", /invisible formatting character/],
    ["\u200b", "U+200B ZERO WIDTH SPACE", /invisible formatting character/],
    ["\u200c", "U+200C ZWNJ", /invisible formatting character/],
    ["\u200d", "U+200D ZWJ", /invisible formatting character/],
    ["\u2060", "U+2060 WORD JOINER", /invisible formatting character/],
    ["\ufeff", "U+FEFF BOM", /invisible formatting character/],
    ["\u0085", "U+0085 NEL", /carries a control character/],
    ["\u009f", "U+009F", /carries a control character/],
  ];
  const rec = recorder({ "POST /api/v1/pricing/price-file/apply": { status: 201, body: {} } });
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: rec.fetch });
  for (const [mark, named, pattern] of invisible) {
    const result = await api.staffPricing.pricingApplyPriceFile(
      client,
      { vendor_id: "86b744fc-c0bf-4b07-a3c7-91b860d428f4", file: new Blob([PNG]) },
      { fileFilename: `price${mark}list.xlsx` },
    );
    assert.equal(result.ok, false, `a name carrying the invisible character ${named} is refused`);
    if (!result.ok) assert.match(result.error, pattern, `the refusal names the class, for ${named}`);
    const only = await api.staffPricing.pricingApplyPriceFile(
      client,
      { vendor_id: "86b744fc-c0bf-4b07-a3c7-91b860d428f4", file: new Blob([PNG]) },
      { fileFilename: mark },
    );
    assert.equal(only.ok, false, `a name made of nothing but ${named} is refused`);
  }
  assert.equal(rec.calls.length, 0, "not one invisible-character carrying name dialed");
});

test("a name that is only spaces, or edged with any white space or a dot, is refused before anything is sent", async () => {
  const rec = recorder({ "POST /api/v1/pricing/price-file/apply": { status: 201, body: {} } });
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: rec.fetch });
  const refusals = [
    ["   ", /nothing but spaces/, "only spaces"],
    [" price-list.xlsx", /begins or ends with a space or a dot/, "a leading space"],
    ["price-list.xlsx ", /begins or ends with a space or a dot/, "a trailing space"],
    [".price-list.xlsx", /begins or ends with a space or a dot/, "a leading dot"],
    ["price-list.xlsx.", /begins or ends with a space or a dot/, "a trailing dot"],
    ["\u00a0price-list.xlsx", /begins or ends with a space or a dot/, "a leading NBSP"],
    ["price-list.xlsx\u00a0", /begins or ends with a space or a dot/, "a trailing NBSP"],
    ["price-list.xlsx\u3000", /begins or ends with a space or a dot/, "a trailing ideographic space"],
    ["price-list.xlsx\u2028", /begins or ends with a space or a dot/, "a trailing line separator"],
    ["\u2028price-list.xlsx", /begins or ends with a space or a dot/, "a leading line separator"],
  ];
  for (const [name, pattern, named] of refusals) {
    const result = await api.staffPricing.pricingApplyPriceFile(
      client,
      { vendor_id: "86b744fc-c0bf-4b07-a3c7-91b860d428f4", file: new Blob([PNG]) },
      { fileFilename: name },
    );
    assert.equal(result.ok, false, `the name with ${named} is refused`);
    if (!result.ok) assert.match(result.error, pattern, `the refusal names the shape, for ${named}`);
  }
  assert.equal(rec.calls.length, 0, "not one refused name dialed");

  // Interior spaces and interior dot runs stay fine, and arrive whole: the
  // edge rule is about the edges, and an interior NBSP the core's final
  // TrimSpace keeps is the name the caller said.
  unwrap(
    await api.staffPricing.pricingApplyPriceFile(
      client,
      { vendor_id: "86b744fc-c0bf-4b07-a3c7-91b860d428f4", file: new Blob([PNG]) },
      { fileFilename: "price list v2.xlsx" },
    ),
  );
  unwrap(
    await api.staffPricing.pricingApplyPriceFile(
      client,
      { vendor_id: "86b744fc-c0bf-4b07-a3c7-91b860d428f4", file: new Blob([PNG]) },
      { fileFilename: "price..list.xlsx" },
    ),
  );
  unwrap(
    await api.staffPricing.pricingApplyPriceFile(
      client,
      { vendor_id: "86b744fc-c0bf-4b07-a3c7-91b860d428f4", file: new Blob([PNG]) },
      { fileFilename: "price\u00a0list.xlsx" },
    ),
  );
  assert.equal(rec.calls.length, 3, "every interior shaped name dialed");
  const first = await new Request(rec.calls[0].url, { method: rec.calls[0].method, body: rec.calls[0].form }).formData();
  assert.equal(first.get("file").name, "price list v2.xlsx", "an interior space arrives whole");
  const second = await new Request(rec.calls[1].url, { method: rec.calls[1].method, body: rec.calls[1].form }).formData();
  assert.equal(second.get("file").name, "price..list.xlsx", "an interior dot run arrives whole");
  const third = await new Request(rec.calls[2].url, { method: rec.calls[2].method, body: rec.calls[2].form }).formData();
  assert.equal(third.get("file").name, "price\u00a0list.xlsx", "an interior NBSP arrives whole");
});

// The guard lives in the transport, so it holds on every multipart operation;
// the feedback screenshot pins it on the second one.
test("the feedback screenshot's name gets the same guard", async () => {
  const rec = recorder({ "POST /api/v1/feedback": { status: 201, body: { id: "fb-3" } } });
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: rec.fetch });
  const result = await api.staffFeedback.feedbackCreateMultipart(
    client,
    { type: "bug", message: "the board froze on drop", image: new Blob([PNG]) },
    { imageFilename: "../escape.png" },
  );
  assert.equal(result.ok, false);
  if (!result.ok) assert.match(result.error, /the image part's file name carries a path separator/);
  assert.equal(rec.calls.length, 0, "the refused name dialed nothing");
});

test("the pricing CSV import accepts a string body and rides it verbatim", async (t) => {
  const csv = "sku,price_cents\nDBS-OAKS-VINTAGE,101700\n";
  const rec = recorder({ "POST /api/v1/pricing/batches/b1/import": { body: { lines: 1 } } });
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: rec.fetch, headers: () => ({ "X-Org": "dibbits" }) });
  unwrap(await api.staffPricing.pricingImportBatch(client, "b1", csv));
  const call = rec.calls[0];
  assert.equal(call.headers["Content-Type"], "text/csv");
  assert.equal(call.body, csv);
});

test("the pricing CSV import accepts bytes too, and rides them verbatim", async (t) => {
  const bytes = new TextEncoder().encode("sku,price_cents\nDBS-OAKS-VINTAGE,101700\n");
  const rec = recorder({ "POST /api/v1/pricing/batches/b1/import": { body: { lines: 1 } } });
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: rec.fetch, headers: () => ({ "X-Org": "dibbits" }) });
  unwrap(await api.staffPricing.pricingImportBatch(client, "b1", bytes));
  const call = rec.calls[0];
  assert.equal(call.headers["Content-Type"], "text/csv");
  assert.ok(call.bytes instanceof Uint8Array);
  assert.deepEqual(call.bytes, bytes);
});
