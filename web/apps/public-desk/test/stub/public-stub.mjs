// The local stub: the public quote routes and the product image read in the
// contract's shapes, for the dev server's /api proxy and the Playwright
// harness. This is a development and test double only: the real routes are
// the core's, and nothing in the app imports this file. When the real routes
// land, the page and this suite keep working against them unchanged, because
// both speak only the contract's shapes:
//   GET  /api/public/v1/quotes/{token}        -> QuotePublicView (410 gone, 404 unknown)
//   POST /api/public/v1/quotes/{token}/accept -> QuoteAcceptanceResult (409 already accepted)
//   GET  /api/v1/products/{id}/image          -> image bytes (the catalogue thumbnail read)
//
// The public routes check NO credential, exactly as the contract marks them:
// the token in the path is the credential. The image read is recorded in the
// request ledger like everything else, so the suite can hold the rule that
// the page ships no credential header even on the one staff-namespaced read
// the plan gives it.
//
// Money is integer cents throughout, computed here (the stub stands in for
// the core, which is the only place money is ever computed) and never in the
// page. Dates in this world are computed from the clock at request time: no
// calendar literal lives in this file.
import { createServer } from "node:http";
import { deflateSync } from "node:zlib";

const port = Number(process.env.PD_STUB_PORT ?? 4837);

const nowIso = () => new Date().toISOString();
const dayFromNow = (days) => new Date(Date.now() + days * 24 * 60 * 60 * 1000).toISOString().slice(0, 10);

// ---------------------------------------------------------------------------
// The quote world, in memory. A restart of the stub is a fresh world. The
// reference token carries the visual reference's quote (same number, same
// customer, same three lines, same figures) so the lane's screenshots compare
// against the reference shot structure for structure; the typed name the
// acceptance tests use is a synthetic label, never a person.
// ---------------------------------------------------------------------------

const QUOTES = {
  "q-demo-25": {
    quote_no: "Q-25",
    customer_name: "Quinte Hardscapes Inc",
    status: "SENT",
    valid_until: dayFromNow(28),
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
      {
        line_no: 2,
        description: "Alliance Gator Polymeric Sand",
        qty_entered: "1.0000",
        uom_entered: "BAG",
        unit_price_cents: 2208,
        line_total_cents: 2208,
        image_ref: "p-sand",
      },
      {
        line_no: 3,
        description: "Black Mulch",
        qty_entered: "1.0000",
        uom_entered: "CYD",
        unit_price_cents: 5400,
        line_total_cents: 5400,
        image_ref: "p-mulch",
      },
    ],
    subtotal_cents: 9665,
    tax_cents: 1257,
    total_cents: 10922,
    as_of: nowIso(),
    acceptable: true,
    include_images: true,
  },
  "q-expired-25": {
    quote_no: "Q-24",
    customer_name: "Quinte Hardscapes Inc",
    status: "SENT",
    valid_until: dayFromNow(-7),
    expired: true,
    lines: [
      {
        line_no: 1,
        description: "Banas Natural Step",
        qty_entered: "2.0000",
        uom_entered: "PC",
        unit_price_cents: 2057,
        line_total_cents: 4114,
      },
    ],
    subtotal_cents: 4114,
    tax_cents: 535,
    total_cents: 4649,
    as_of: nowIso(),
    acceptable: false,
  },
  "q-accepted-26": {
    quote_no: "Q-26",
    customer_name: "Quinte Hardscapes Inc",
    status: "ACCEPTED",
    valid_until: dayFromNow(20),
    expired: false,
    lines: [
      {
        line_no: 1,
        description: "Black Mulch",
        qty_entered: "3.0000",
        uom_entered: "CYD",
        unit_price_cents: 5400,
        line_total_cents: 16200,
      },
    ],
    subtotal_cents: 16200,
    tax_cents: 2106,
    total_cents: 18306,
    as_of: nowIso(),
    acceptable: false,
  },
  "q-refuse-27": {
    quote_no: "Q-27",
    customer_name: "Quinte Hardscapes Inc",
    status: "SENT",
    valid_until: dayFromNow(14),
    expired: false,
    lines: [
      {
        line_no: 1,
        description: "Clear Stone",
        qty_entered: "1.0000",
        uom_entered: "YD",
        unit_price_cents: 3800,
        line_total_cents: 3800,
      },
    ],
    subtotal_cents: 3800,
    tax_cents: 494,
    total_cents: 4294,
    as_of: nowIso(),
    acceptable: true,
  },
  "q-terms-28": {
    quote_no: "Q-28",
    customer_name: "Fenwick Builders Ltd",
    status: "SENT",
    valid_until: dayFromNow(21),
    expired: false,
    lines: [
      {
        line_no: 1,
        description: "Retaining Wall Stone, Standard",
        qty_entered: "10.0000",
        uom_entered: "PC",
        unit_price_cents: 5200,
        line_total_cents: 52000,
        disc_pct: 10,
        pre_discount_total_cents: 57778,
      },
    ],
    subtotal_cents: 52000,
    tax_cents: 6760,
    total_cents: 58760,
    as_of: nowIso(),
    acceptable: true,
    customer_notes: "Gate code at the side fence, ask for the site lead.",
    deposit_required: true,
    deposit_pct: 33,
    deposit_cents: 19391,
    include_images: false,
    fulfillment: "DELIVERY",
    delivery_address: "12 Test Yard Lane",
    requested_date: dayFromNow(6),
    delivery_fee_cents: 4500,
  },
};

// Fresh copies of the terms quote for the tests whose acceptance really
// commits in the stub while the answer is lost on the way back.
for (const token of [
  "q-lost-504",
  "q-lost-unreadable",
  "q-lost-refused",
  "q-unk-unreadable",
  "q-unk-reset",
  "q-multi-504",
]) {
  QUOTES[token] = structuredClone(QUOTES["q-terms-28"]);
}

// The link the server revoked: the route answers 410, not 404, because the
// link EXISTED and died. The page says so, and says what to do next.
const REVOKED_TOKENS = new Set(["q-revoked-410"]);

// The thumbnail bytes: a one pixel PNG per product, tinted so the rendered
// thumbnails read as distinct images the way the reference's product photos
// do. Built here, at request time, so no binary rides the repo.
const PNG_SIGNATURE = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);
const CRC_TABLE = (() => {
  const table = new Int32Array(256);
  for (let n = 0; n < 256; n += 1) {
    let c = n;
    for (let k = 0; k < 8; k += 1) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    table[n] = c;
  }
  return table;
})();

function crc32(buffer) {
  let crc = -1;
  for (const byte of buffer) crc = CRC_TABLE[(crc ^ byte) & 0xff] ^ (crc >>> 8);
  return (crc ^ -1) >>> 0;
}

function pngChunk(type, data) {
  const length = Buffer.alloc(4);
  length.writeUInt32BE(data.length, 0);
  const body = Buffer.concat([Buffer.from(type, "ascii"), data]);
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32(body), 0);
  return Buffer.concat([length, body, crc]);
}

function png1x1(r, g, b) {
  const header = Buffer.alloc(13);
  header.writeUInt32BE(1, 0); // width
  header.writeUInt32BE(1, 4); // height
  header[8] = 8; // bit depth
  header[9] = 2; // colour type: truecolour
  // filter byte, then the one RGB pixel
  const raw = Buffer.from([0, r, g, b]);
  return Buffer.concat([
    PNG_SIGNATURE,
    pngChunk("IHDR", header),
    pngChunk("IDAT", deflateSync(raw)),
    pngChunk("IEND", Buffer.alloc(0)),
  ]);
}

const IMAGES = {
  "p-banas": png1x1(0x3a, 0x7d, 0x56),
  "p-sand": png1x1(0xde, 0xcd, 0xaa),
  "p-mulch": png1x1(0x2b, 0x28, 0x24),
  "p-wall": png1x1(0x5a, 0x70, 0x86),
};

// ---------------------------------------------------------------------------
// The request ledger: every request's method, path and header names, for the
// suite's no-credential assertions. Test scaffolding only, on a route no
// contract path shares.
// ---------------------------------------------------------------------------

const SIGHTINGS = [];

function respondJson(response, status, payload) {
  response.writeHead(status, { "content-type": "application/json" });
  response.end(JSON.stringify(payload));
}

function readBody(request) {
  return new Promise((resolve, reject) => {
    let body = "";
    request.on("data", (chunk) => {
      body += chunk;
    });
    request.on("end", () => resolve(body));
    request.on("error", reject);
  });
}

function alreadyAccepted() {
  return { status: 409, body: { error: "This quote has already been accepted." } };
}

const server = createServer(async (request, response) => {
  const url = new URL(request.url ?? "/", `http://127.0.0.1:${port}`);
  const path = url.pathname;
  SIGHTINGS.push({
    method: request.method,
    path,
    headerNames: Object.keys(request.headers),
  });

  if (request.method === "GET" && path === "/healthz") {
    respondJson(response, 200, { ok: true });
    return;
  }
  if (request.method === "GET" && path === "/_stub/state") {
    respondJson(response, 200, { sightings: SIGHTINGS });
    return;
  }

  const viewMatch = path.match(/^\/api\/public\/v1\/quotes\/([^/]+)$/);
  if (request.method === "GET" && viewMatch !== null) {
    const token = decodeURIComponent(viewMatch[1]);
    if (REVOKED_TOKENS.has(token)) {
      respondJson(response, 410, { error: "this share link was turned off" });
      return;
    }
    const quote = QUOTES[token];
    if (quote === undefined) {
      respondJson(response, 404, { error: "no quote answers on this link" });
      return;
    }
    respondJson(response, 200, quote);
    return;
  }

  const acceptMatch = path.match(/^\/api\/public\/v1\/quotes\/([^/]+)\/accept$/);
  if (request.method === "POST" && acceptMatch !== null) {
    const token = decodeURIComponent(acceptMatch[1]);
    if (REVOKED_TOKENS.has(token)) {
      respondJson(response, 410, { error: "this share link was turned off" });
      return;
    }
    const quote = QUOTES[token];
    if (quote === undefined) {
      respondJson(response, 404, { error: "no quote answers on this link" });
      return;
    }
    if (token === "q-refuse-27" || quote.status === "ACCEPTED") {
      const refusal = alreadyAccepted();
      respondJson(response, refusal.status, refusal.body);
      return;
    }
    let body;
    try {
      body = JSON.parse(await readBody(request));
    } catch {
      respondJson(response, 400, { error: "the request body is not JSON" });
      return;
    }
    if (body === null || typeof body !== "object" || typeof body.typed_name !== "string" || body.typed_name.trim() === "") {
      respondJson(response, 400, { error: "the acceptance carries no typed name" });
      return;
    }
    if (body.accepted !== true) {
      respondJson(response, 400, { error: "the acceptance is not affirmed" });
      return;
    }
    // The acceptance commits: the quote's own figures come back echoed, and
    // the quote leaves the acceptable state, exactly as the core's accept does.
    quote.status = "ACCEPTED";
    quote.acceptable = false;
    respondJson(response, 200, {
      quote_no: quote.quote_no,
      typed_name: body.typed_name.trim(),
      accepted_at: nowIso(),
      subtotal_cents: quote.subtotal_cents,
      tax_cents: quote.tax_cents,
      total_cents: quote.total_cents,
    });
    return;
  }

  const imageMatch = path.match(/^\/api\/v1\/products\/([^/]+)\/image$/);
  if (request.method === "GET" && imageMatch !== null) {
    const id = decodeURIComponent(imageMatch[1]);
    const bytes = IMAGES[id];
    if (bytes === undefined) {
      response.writeHead(404, { "content-type": "text/plain" });
      response.end("no image");
      return;
    }
    response.writeHead(200, { "content-type": "image/png" });
    response.end(bytes);
    return;
  }

  respondJson(response, 404, { error: "no such route on the stub" });
});

server.on("error", (error) => {
  console.error(`public stub could not listen on 127.0.0.1:${port}: ${error instanceof Error ? error.message : String(error)}`);
  process.exit(1);
});

server.keepAliveTimeout = 120 * 1000;
server.headersTimeout = 125 * 1000;
server.listen(port, "127.0.0.1", () => {
  console.log(`public stub on http://127.0.0.1:${port}`);
});
