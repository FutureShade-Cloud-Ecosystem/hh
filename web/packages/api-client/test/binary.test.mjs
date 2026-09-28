// The binary gate. The contract's binary transfers ride the wire as bytes, so
// the client must read a format: binary success as bytes and send the binary
// uploads as bytes, byte for byte, with no text decode or re-encode anywhere.
// The fixtures are real binary payloads built here: a PNG file signature with
// its IHDR chunk for the images, the ZIP local file header (every xlsx is a
// ZIP) lives in test/result-shape.test.mjs. Every assertion compares bytes.
import { test } from "node:test";
import assert from "node:assert/strict";
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

test("the product image upload sends the raw bytes verbatim", async (t) => {
  const rec = recorder({ "PUT /api/v1/products/abc/image": { body: { size_bytes: PNG.byteLength } } });
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: rec.fetch, headers: () => ({ "X-Org": "dibbits" }) });
  unwrap(await api.staffProduct.productPutImage(client, "abc", PNG));
  const call = rec.calls[0];
  assert.equal(call.method, "PUT");
  assert.equal(call.headers["Content-Type"], "image/jpeg", "the first declared content type");
  assert.ok(call.bytes instanceof Uint8Array, "the body rode as bytes");
  assert.deepEqual(call.bytes, PNG, "the upload is byte for byte what the caller held");
});

test("the dispatch photo upload sends the raw bytes verbatim with its caption query", async (t) => {
  const rec = recorder({ "POST /api/v1/dispatch/stops/stop-9/photos": { status: 201, body: { id: "p1" } } });
  const client = new api.ApiClient({ baseUrl: "https://hh-core.example", fetch: rec.fetch, headers: () => ({ "X-Org": "dibbits" }) });
  unwrap(await api.staffDispatch.dispatchUploadStopPhoto(client, "stop-9", PNG, { caption: "gate 3, delivered" }));
  const call = rec.calls[0];
  assert.equal(call.headers["Content-Type"], "image/jpeg");
  assert.deepEqual(call.bytes, PNG);
  assert.equal(call.query.caption, "gate 3, delivered");
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
