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

test("the longest accepted file name is 160 characters, one more is refused", async () => {
  const rec = recorder({ "POST /api/v1/pricing/price-file/apply": { status: 201, body: {} } });
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: rec.fetch, headers: () => ({ "X-Org": "dibbits" }) });
  const tooLong = await api.staffPricing.pricingApplyPriceFile(
    client,
    { vendor_id: "86b744fc-c0bf-4b07-a3c7-91b860d428f4", file: new Blob([PNG]) },
    { fileFilename: "p".repeat(161) },
  );
  assert.equal(tooLong.ok, false);
  if (!tooLong.ok) assert.match(tooLong.error, /longer than the 160 characters/);
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
