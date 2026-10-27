// The Buying and Stock stub against the contract and the core, for the
// inventory and counts routes the two new areas call (the procurement routes
// have their own file, stub-contract.test.mjs).
//
// The same rule as the procurement file holds here: a stub that drifts from
// the server makes every browser leg a flattering lie. Every answer is held
// to the generated wire descriptors, and every refusal sentence must appear
// in the core's source. Where the core writes a list under its own key
// (on_hand, moves, pending, branches, reservations, transfers, lines, pairs,
// reorder_points; browse under rows) the contract declares that same key, so
// the whole envelope is held to the contract's list schema, the key is pinned
// to the core's source, and the rows are held to their descriptor. The
// contract moved to the core's keys (the shortfall precedent, the other way
// round); the core is what a request meets.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { coreSourceOf, holds, refusedBy, startStub } from "./contract-kit.mjs";

const coreSource = coreSourceOf(["inventory", "invcount", "platform/authz", "platform/httpx"]);
const refused = refusedBy(coreSource);

let stub;
let base;
let call;

before(async () => {
  stub = await startStub();
  base = stub.base;
  call = stub.call;
});

after(() => {
  stub?.stop();
});

// The counts gate reads the token's sub as the caller (the maker against the
// checker), so the maker and checker calls mint real tokens, the way the stub
// mints them for a signed-in flow.
const PROCUREMENT_ID = "de110000-0000-4000-9000-000000000008";
const YARD_ID = "de110000-0000-4000-9000-000000000003";

function tokenFor(sub) {
  const part = (value) => Buffer.from(JSON.stringify(value), "utf8").toString("base64url");
  const issuedAtSeconds = Math.floor(Date.now() / 1000);
  const payload = { iss: "hh-demo-stub", sub, aud: "staff", iat: issuedAtSeconds, exp: issuedAtSeconds + 3600 };
  return `${part({ alg: "ES256", typ: "JWT" })}.${part(payload)}.c3R1Yi1zaWduYXR1cmUtYnl0ZXM`;
}

// The counts gate reads the token's sub as the caller; the kit's call takes the headers.
const callAs = (method, path, sub, body) => call(method, path, body, { Authorization: `Bearer ${tokenFor(sub)}` });

const reset = () => stub.reset();
const role = (name) => stub.post("/_stub/role", { as: name });
const admin = () => stub.post("/_stub/admin");

const PLM = "e1000000-0000-4000-8000-000000000001";
const JUL = "e1000000-0000-4000-8000-000000000002";

/** The core's envelope for a domain-keyed list: the key is the core's, and the core's
 * source really writes it (the write site is quoted, as the shortfall test pins items). */
function domainKey(res, key, rowDescriptor, listDescriptor) {
  assert.equal(res.status, 200);
  holds(listDescriptor, res.json);
  assert.ok(Array.isArray(res.json[key]), `expected the list under "${key}"`);
  assert.ok(new RegExp(`"${key}":\\s+(rows|pairs|lines)`).test(coreSource), `the core no longer writes this list under the key ${key}`);
  for (const row of res.json.items ?? res.json[key]) holds(rowDescriptor, row);
  return res.json[key];
}

// The core's transfer eta is the ship date plus the transit offset in the served business_zone
// (core/internal/inventory/transfer_eta.go etaZone), whatever zone this runner is in.
let servedZone = "";
const later = (offset) => {
  const cur = new Intl.DateTimeFormat("en-CA", { timeZone: servedZone, year: "numeric", month: "2-digit", day: "2-digit" }).format(new Date());
  const at = new Date(`${cur}T12:00:00Z`);
  at.setUTCDate(at.getUTCDate() + offset);
  return at.toISOString().slice(0, 10);
};

const world = async () => {
  await reset();
  const cfg = await call("GET", "/api/v1/surfaces/buying-and-stock/config");
  assert.equal(cfg.status, 200);
  holds("SettingsSurfaceConfig", cfg.json);
  servedZone = cfg.json.business_zone;
  const branches = (await call("GET", "/api/v1/branches")).json;
  return {
    plm: branches.find((b) => b.code === "PLM").id,
    jul: branches.find((b) => b.code === "JUL").id,
    products: (await call("GET", "/api/v1/products")).json.items.filter((p) => p.sku.startsWith("GLS-")),
  };
};

// ---- the stock lookup ---------------------------------------------------------------------------

test("the on-hand read answers under the core's on_hand key, and an unknown product is an empty page, never an error", async () => {
  const { plm, products } = await world();
  const paver = products.find((p) => p.sku === "GLS-PAV-60");
  const res = await call("GET", `/api/v1/inventory/on-hand?product_id=${paver.id}`);
  assert.ok(/"on_hand":\s+rows/.test(coreSource), "the core no longer writes the on-hand read under the key on_hand");
  const rows = domainKey(res, "on_hand", "InventoryOnHand", "InventoryOnHandList");
  const home = rows.find((r) => r.branch_id === plm);
  assert.equal(home.on_hand_base, 400);
  assert.equal(home.available_base, 340);
  const unknown = await call("GET", "/api/v1/inventory/on-hand?product_id=f1000000-0000-4000-8000-000000000099");
  assert.equal(unknown.status, 200);
  assert.deepEqual(unknown.json.on_hand, []);
  refused(await call("GET", "/api/v1/inventory/on-hand"), 400, "product_id is required");
  refused(await call("GET", "/api/v1/inventory/on-hand?product_id=paver"), 400, "product_id must be a product UUID");
});

test("the move ledger answers under the core's moves key, graded like the on-hand read", async () => {
  const { jul, products } = await world();
  const cap = products.find((p) => p.sku === "GLS-WALL-CAP");
  const res = await call("GET", `/api/v1/inventory/moves?product_id=${cap.id}`);
  assert.ok(/"moves":\s+rows/.test(coreSource), "the core no longer writes the move ledger under the key moves");
  const rows = domainKey(res, "moves", "InventoryStockMove", "InventoryMoveList");
  assert.ok(rows.length > 0, "the seeded cap has ledger lines");
  const atJul = await call("GET", `/api/v1/inventory/moves?product_id=${cap.id}&branch_id=${jul}`);
  assert.ok(atJul.json.moves.every((m) => m.branch_id === jul));
  refused(await call("GET", `/api/v1/inventory/moves?product_id=${cap.id}&branch_id=kingston`), 400, "branch_id must be a branch id, copy it from the branch list, not the branch name");
});

test("branch stock answers the four buckets per branch under the core's branches key", async () => {
  const { plm, jul, products } = await world();
  const sand = products.find((p) => p.sku === "GLS-SAND-POLY");
  const res = await call("GET", `/api/v1/inventory/branch-stock?product_id=${sand.id}`);
  const rows = domainKey(res, "branches", "InventoryBranchStock", "InventoryBranchStockList");
  assert.equal(rows.length, 2, "the sand sits at both yards");
  const negative = rows.find((r) => r.branch_id === jul);
  assert.ok(negative.available_base < 0, "sold before receipt reads negative, by design");
  const one = await call("GET", `/api/v1/inventory/branch-stock?product_id=${sand.id}&branch_id=${plm}`);
  assert.deepEqual(one.json.branches.map((r) => r.branch_id), [plm]);
  assert.ok(jul !== plm);
});

test("pending fulfilment answers under the core's pending key", async () => {
  const res = await call("GET", "/api/v1/inventory/pending-fulfilment");
  assert.ok(/"pending":\s+rows/.test(coreSource), "the core no longer writes the pending read under the key pending");
  const rows = domainKey(res, "pending", "InventoryPendingFulfilment", "InventoryPendingFulfilmentList");
  assert.equal(rows.length, 2);
  assert.ok(rows.every((r) => r.sale_type === "DELIVERED" || r.sale_type === "COLLECT_LATER"));
});

// ---- browse --------------------------------------------------------------------------------------

test("browse answers rows, facets, total and the echoed page", async () => {
  const res = await call("GET", "/api/v1/inventory/browse?text=paver");
  assert.equal(res.status, 200);
  holds("InventoryBrowseResult", res.json);
  assert.ok(Array.isArray(res.json.rows), "browse writes its list under rows");
  assert.ok(coreSource.includes("json:\"rows\""), "the core no longer writes browse's list under the key rows");
  for (const row of res.json.rows) holds("InventoryBrowseRow", row);
  for (const facet of res.json.facets) holds("InventoryFacet", facet);
  assert.equal(typeof res.json.total, "number");
  assert.equal(res.json.limit, 60);
  assert.equal(res.json.offset, 0);
  const paged = await call("GET", "/api/v1/inventory/browse?text=paver&limit=1&offset=1");
  assert.equal(paged.json.offset, 1);
  assert.ok(paged.json.rows.length <= 1);
  refused(await call("GET", "/api/v1/inventory/browse?offset=soon"), 400, "offset must be a row number, zero or more");
});

// ---- transit days ---------------------------------------------------------------------------------

test("transit days list every ordered pair under the core's pairs key, and the write is the admin gate", async () => {
  const { plm, jul } = await world();
  const res = await call("GET", "/api/v1/inventory/transit-days");
  const pairs = domainKey(res, "pairs", "InventoryTransitDaysPair", "InventoryTransitDaysPairList");
  const coast = pairs.find((p) => p.from_branch_id === plm && p.to_branch_id === jul);
  assert.equal(coast.transit_days, 1);
  assert.equal(coast.configured, true);
  assert.ok(pairs.some((p) => p.configured === false), "an unconfigured pair is listed, promising nothing");
  // The write sits behind requireAdmin, a tier no demo role holds: the shared refusal.
  refused(await call("PUT", "/api/v1/inventory/transit-days", { from_branch_id: plm, to_branch_id: jul, transit_days: 2, clear: false }), 403, "Forbidden");
  await admin();
  const put = await call("PUT", "/api/v1/inventory/transit-days", { from_branch_id: plm, to_branch_id: jul, transit_days: 2, clear: false });
  assert.equal(put.status, 200);
  holds("InventoryTransitDaysPair", put.json);
  assert.equal(put.json.transit_days, 2);
  refused(await call("PUT", "/api/v1/inventory/transit-days", { from_branch_id: plm, to_branch_id: plm, transit_days: 2, clear: false }), 400, "a transit time needs two different branches");
  refused(await call("PUT", "/api/v1/inventory/transit-days", { from_branch_id: plm, to_branch_id: jul, transit_days: 9000, clear: false }), 400, "transit days must be a whole number of days from 0 to");
});

// ---- reservations -----------------------------------------------------------------------------------

test("reservations: the list under the core's key, a HARD hold written through the policy, release and expire", async () => {
  const { plm, products } = await world();
  const listed = await call("GET", "/api/v1/inventory/reservations");
  const rows = domainKey(listed, "reservations", "InventoryReservation", "InventoryReservationList");
  assert.equal(rows.length, 1);
  assert.equal(rows[0].hold_mode, "HARD");
  const paver = products.find((p) => p.sku === "GLS-PAV-60");
  const made = await call("POST", "/api/v1/inventory/reservations", {
    product_id: paver.id, branch_id: plm, location_id: plm, res_type: "QUOTE", qty_base: 10,
    source_type: "quote", source_ref_id: "Q-9001", reason: "Summit Ridge bid",
  });
  assert.equal(made.status, 200);
  holds("InventoryReserveResult", made.json);
  assert.equal(made.json.applied, true);
  assert.equal(made.json.reservation.qty_base, 10);
  holds("InventoryAllocationPolicy", made.json.policy);
  const id = made.json.reservation.id;
  const released = await call("POST", "/api/v1/inventory/reservations/release", { reservation_id: id });
  assert.equal(released.status, 200);
  holds("InventoryReleaseResult", released.json);
  assert.equal(released.json.released, 1);
  refused(await call("POST", "/api/v1/inventory/reservations/release", { status: "RELEASED" }), 400, "reservation_id or source_ref_id is required");
  refused(await call("POST", "/api/v1/inventory/reservations", {
    product_id: paver.id, branch_id: plm, location_id: plm, res_type: "ORDER", qty_base: 100000,
    source_type: "order", source_ref_id: "SO-1", reason: "overreach",
  }), 409, "insufficient available stock at branch");
  refused(await call("POST", "/api/v1/inventory/reservations", {
    product_id: "", branch_id: plm, location_id: plm, res_type: "ORDER", qty_base: 1,
    source_type: "order", source_ref_id: "SO-1", reason: "",
  }), 400, "product_id is required");
  const expired = await call("POST", "/api/v1/inventory/reservations/expire");
  assert.equal(expired.status, 200);
  holds("InventoryExpireResult", expired.json);
  assert.equal(typeof expired.json.expired, "number");
});

test("allocation policy: the resolved default reads, and a branch policy upserts", async () => {
  const { plm, jul } = await world();
  const got = await call("GET", `/api/v1/inventory/allocation-policy?branch_id=${plm}`);
  assert.equal(got.status, 200);
  holds("InventoryAllocationPolicy", got.json);
  const put = await call("PUT", "/api/v1/inventory/allocation-policy", {
    branch_id: jul, order_mode: "SOFT", quote_mode: "NONE", order_ttl_hours: 48, quote_ttl_hours: 24, is_default: false,
  });
  assert.equal(put.status, 200);
  holds("InventoryAllocationPolicy", put.json);
  assert.equal(put.json.order_mode, "SOFT");
  refused(await call("PUT", "/api/v1/inventory/allocation-policy", {
    branch_id: jul, order_mode: "SOMETIMES", quote_mode: "NONE", order_ttl_hours: 48, quote_ttl_hours: 24, is_default: false,
  }), 400, "invalid allocation mode");
});

// ---- transfers ---------------------------------------------------------------------------------------

test("transfers: the list under the core's key, a draft raised, shipped with its eta, received", async () => {
  const { plm, jul, products } = await world();
  const listed = await call("GET", "/api/v1/inventory/transfers");
  const rows = domainKey(listed, "transfers", "InventoryTransfer", "InventoryTransferList");
  for (const t of rows) for (const line of t.lines) holds("InventoryTransferLine", line);
  const inTransit = rows.find((t) => t.status === "IN_TRANSIT");
  assert.equal(inTransit.eta, later(2));
  // A draft ships with the route's configured transit day; an eta typed by the clerk is stored as typed.
  const draft = rows.find((t) => t.status === "DRAFT");
  const shipped = await call("POST", `/api/v1/inventory/transfers/${draft.id}/ship`, { eta: later(4) });
  assert.equal(shipped.status, 200);
  holds("InventoryTransfer", shipped.json);
  assert.equal(shipped.json.status, "IN_TRANSIT");
  assert.equal(shipped.json.eta, later(4));
  refused(await call("POST", `/api/v1/inventory/transfers/${draft.id}/ship`), 409, "only a DRAFT can ship");
  const arrived = await call("POST", `/api/v1/inventory/transfers/${shipped.json.id}/receive`);
  assert.equal(arrived.status, 200);
  assert.equal(arrived.json.status, "RECEIVED");
  refused(await call("POST", `/api/v1/inventory/transfers/${shipped.json.id}/receive`), 409, "only an IN_TRANSIT transfer can be received");
  // A shipped transfer is corrected with a transfer back, never a cancellation.
  refused(await call("POST", `/api/v1/inventory/transfers/${shipped.json.id}/cancel`), 409, "a shipped transfer is corrected with a transfer back, not a cancellation");
  // A draft raises, carries its lines, and cancels.
  const cap = products.find((p) => p.sku === "GLS-WALL-CAP");
  const made = await call("POST", "/api/v1/inventory/transfers", {
    from_branch_id: jul, to_branch_id: plm, notes: "the cap pallet comes back", lines: [{ product_id: cap.id, qty_base: 12 }],
  });
  assert.equal(made.status, 200);
  holds("InventoryTransfer", made.json);
  assert.equal(made.json.status, "DRAFT");
  const cancelled = await call("POST", `/api/v1/inventory/transfers/${made.json.id}/cancel`);
  assert.equal(cancelled.json.status, "CANCELLED");
  refused(await call("POST", "/api/v1/inventory/transfers", {
    from_branch_id: plm, to_branch_id: plm, notes: "", lines: [{ product_id: cap.id, qty_base: 1 }],
  }), 400, "a transfer must cross branches");
  refused(await call("POST", "/api/v1/inventory/transfers", { from_branch_id: plm, to_branch_id: jul, notes: "", lines: [] }), 400, "a transfer needs at least one line");
  const one = await call("GET", `/api/v1/inventory/transfers/${rows[0].id}`);
  assert.equal(one.status, 200);
  holds("InventoryTransfer", one.json);
  refused(await call("GET", "/api/v1/inventory/transfers/f1000000-0000-4000-8000-000000000099"), 404, "not found");
});

test("incoming transfers answer the truck for one product under the core's lines key", async () => {
  const { jul, products } = await world();
  const cap = products.find((p) => p.sku === "GLS-WALL-CAP");
  const res = await call("GET", `/api/v1/inventory/incoming-transfers?product_id=${cap.id}`);
  assert.ok(/"lines":\s+lines/.test(coreSource), "the core no longer writes the incoming transfer read under the key lines");
  const rows = domainKey(res, "lines", "InventoryInTransitLine", "InventoryInTransitLineList");
  assert.equal(rows.length, 1);
  assert.equal(rows[0].to_branch_id, jul);
  assert.equal(rows[0].qty_base, 40);
  refused(await call("GET", "/api/v1/inventory/incoming-transfers"), 400, "product_id is required");
});

// ---- receive -------------------------------------------------------------------------------------------

test("receiving takes the outstanding units, and an over-receipt is refused in the core's words", async () => {
  await reset();
  const pos = (await call("GET", "/api/v1/purchasing/pos")).json.items;
  const ordered = pos.find((p) => p.status === "ORDERED");
  const detail = await call("GET", `/api/v1/purchasing/pos/${ordered.id}`);
  const line = detail.json.lines[0];
  const res = await call("POST", "/api/v1/inventory/receive", { po_id: ordered.id, lines: [] });
  assert.equal(res.status, 200);
  holds("InventoryReceiptResult", res.json);
  assert.equal(res.json.po_id, ordered.id);
  assert.equal(res.json.lines_received, 1);
  assert.ok(res.json.value_cad_cents > 0);
  // Everything outstanding is now received: the same call again moves nothing.
  const again = await call("POST", "/api/v1/inventory/receive", { po_id: ordered.id, lines: [] });
  assert.equal(again.json.lines_received, 0);
  const other = pos.find((p) => p.status === "ORDERED" && p.id !== ordered.id);
  const otherLine = (await call("GET", `/api/v1/purchasing/pos/${other.id}`)).json.lines[0];
  refused(await call("POST", "/api/v1/inventory/receive", { po_id: other.id, lines: [{ po_line_id: otherLine.id, qty_base: otherLine.qty_ordered_base + 1 }] }), 409, "over-receipt on PO line");
  refused(await call("POST", "/api/v1/inventory/receive", { po_id: "", lines: [] }), 400, "po_id is required");
  refused(await call("POST", "/api/v1/inventory/receive", { po_id: "f1000000-0000-4000-8000-000000000099", lines: [] }), 404, "not found");
});

// ---- replenishment -----------------------------------------------------------------------------------------

test("reorder points list under the core's key, upsert and bootstrap; the nudge posts once per cycle", async () => {
  const { plm, products } = await world();
  const listed = await call("GET", `/api/v1/inventory/reorder-points?branch_id=${plm}`);
  const rows = domainKey(listed, "reorder_points", "InventoryReorderPoint", "InventoryReorderPointList");
  assert.ok(rows.length >= 2);
  const dg = rows.find((r) => r.sku === "GLS-AGG-DG");
  assert.equal(dg.reorder_point_base, 10);
  const put = await call("PUT", "/api/v1/inventory/reorder-points", {
    product_id: dg.product_id, sku: dg.sku, product_name: dg.product_name, branch_id: plm,
    reorder_point_base: 12, reorder_qty_base: 10, order_multiple_base: 10,
    preferred_vendor_id: dg.preferred_vendor_id, lead_time_days: 3, is_active: true, notes: "summer grade",
  });
  assert.equal(put.status, 200);
  holds("InventoryReorderPoint", put.json);
  assert.equal(put.json.reorder_point_base, 12);
  refused(await call("PUT", "/api/v1/inventory/reorder-points", {
    product_id: dg.product_id, branch_id: plm, reorder_point_base: -1, reorder_qty_base: 0,
    order_multiple_base: 0, preferred_vendor_id: dg.preferred_vendor_id, lead_time_days: 0, is_active: true,
  }), 400, "reorder quantities must be >= 0");
  const boot = await call("POST", "/api/v1/inventory/reorder-points/bootstrap", { branch_id: plm });
  assert.equal(boot.status, 200);
  holds("InventoryBootstrapResult", boot.json);
  assert.ok(boot.json.created >= 0);
  const paver = products.find((p) => p.sku === "GLS-PAV-60");
  const nudge = await call("POST", "/api/v1/inventory/nudge-procurement", { product_id: paver.id, branch_id: plm });
  assert.equal(nudge.status, 200);
  holds("InventoryNudgeResult", nudge.json);
  assert.equal(nudge.json.posted, true);
  const repeat = await call("POST", "/api/v1/inventory/nudge-procurement", { product_id: paver.id, branch_id: plm });
  assert.equal(repeat.json.posted, false, "the nudge is idempotent per product and branch within the posting cycle");
  refused(await call("POST", "/api/v1/inventory/nudge-procurement", { product_id: "", branch_id: plm }), 400, "product_id is required");
});

test("shortfall create-po drafts one order per vendor, and a mixed selection is refused in the core's words", async () => {
  const { plm } = await world();
  const shortfall = (await call("GET", "/api/v1/inventory/shortfall")).json.items;
  const byVendor = new Map();
  for (const row of shortfall) {
    byVendor.set(row.preferred_vendor_id, [...(byVendor.get(row.preferred_vendor_id) ?? []), row]);
  }
  const [vendorId, rows] = [...byVendor.entries()][0];
  assert.ok(vendorId !== undefined && rows.length > 0);
  const made = await call("POST", "/api/v1/inventory/shortfall/create-po", {
    branch_id: plm, product_ids: rows.map((r) => r.product_id),
  });
  assert.equal(made.status, 200);
  holds("InventoryCreatePOResult", made.json);
  assert.equal(made.json.draft.vendor_id, vendorId);
  for (const line of made.json.draft.lines) holds("InventorySuggestedPOLine", line);
  const otherVendor = [...byVendor.entries()].find(([id]) => id !== vendorId);
  if (otherVendor !== undefined) {
    const mixed = [...rows, otherVendor[1][0]].map((r) => r.product_id);
    refused(await call("POST", "/api/v1/inventory/shortfall/create-po", { branch_id: plm, product_ids: mixed }), 400, "selection spans more than one vendor, create one purchase order per vendor");
  }
  refused(await call("POST", "/api/v1/inventory/shortfall/create-po", { branch_id: plm, product_ids: [] }), 400, "select at least one shortfall line");
});

// ---- counts -------------------------------------------------------------------------------------------------

const openSheet = async () => {
  const { plm } = await world();
  const made = await call("POST", "/api/v1/counts/sheets", { branch_id: plm, scope: "FULL", notes: "month end at Point Loma" });
  assert.equal(made.status, 201);
  holds("InvCountSheet", made.json);
  return made.json;
};

test("count sheets: the list, a sheet opened, and the lifecycle words in order", async () => {
  const { plm } = await world();
  const listed = await call("GET", "/api/v1/counts/sheets?branch_id=" + plm);
  assert.equal(listed.status, 200);
  assert.ok(Array.isArray(listed.json.items));
  for (const sheet of listed.json.items) holds("InvCountSheet", sheet);
  const sheet = await openSheet();
  assert.equal(sheet.status, "OPEN");
  assert.ok(sheet.sheet_no.length > 0);
  // Counts before the freeze are refused first.
  refused(await call("POST", `/api/v1/counts/sheets/${sheet.id}/counts`, { lines: [] }), 409, "sheet must be FROZEN before counts are entered");
  const frozen = await call("POST", `/api/v1/counts/sheets/${sheet.id}/freeze`);
  assert.equal(frozen.status, 200);
  holds("InvCountSheet", frozen.json);
  assert.equal(frozen.json.status, "FROZEN");
  refused(await call("POST", `/api/v1/counts/sheets/${sheet.id}/freeze`), 409, "only an OPEN sheet can be frozen");
  const detail = await call("GET", `/api/v1/counts/sheets/${sheet.id}`);
  assert.equal(detail.status, 200);
  holds("InvCountSheetDetail", detail.json);
  for (const line of detail.json.lines) holds("InvCountLine", line);
  holds("InvCountVarianceSummary", detail.json.summary);
  assert.ok(detail.json.lines.length > 0, "the freeze captures the branch's stock onto the sheet");
  // One variance line, reason coded: counted below the snapshot.
  const target = detail.json.lines[0];
  const counted = await call("POST", `/api/v1/counts/sheets/${sheet.id}/counts`, {
    lines: [{ product_id: target.product_id, location_id: target.location_id, counted_qty_base: target.snapshot_qty_base - 2, reason_code: "SHRINK" }],
  });
  assert.equal(counted.status, 200);
  holds("InvCountSheet", counted.json);
  assert.equal(counted.json.status, "COUNTED");
});

test("the maker cannot approve their own count, and the post refuses to run before a checker's approval", async () => {
  const { plm } = await world();
  const made = await call("POST", "/api/v1/counts/sheets", { branch_id: plm, scope: "AD_HOC", notes: "the cap pallet check" });
  assert.equal(made.status, 201);
  await call("POST", `/api/v1/counts/sheets/${made.json.id}/freeze`);
  const detail = await call("GET", `/api/v1/counts/sheets/${made.json.id}`);
  const target = detail.json.lines[0];
  // The procurement manager counts: they are the sheet's maker.
  const counted = await callAs("POST", `/api/v1/counts/sheets/${made.json.id}/counts`, PROCUREMENT_ID, {
    lines: [{ product_id: target.product_id, location_id: target.location_id, counted_qty_base: target.snapshot_qty_base + 1, reason_code: "FOUND" }],
  });
  assert.equal(counted.status, 200);
  // The four-eyes refusal: the same caller approves their own count.
  refused(await callAs("POST", `/api/v1/counts/sheets/${made.json.id}/approve`, PROCUREMENT_ID), 422, "maker-checker violation, the maker cannot approve their own count");
  refused(await callAs("POST", `/api/v1/counts/sheets/${made.json.id}/post`, PROCUREMENT_ID), 422, "post requires a checker approval (approved_by) distinct from the maker");
  void plm;
});

test("a transfer that would overdraw the source is refused in the core's own words", async () => {
  const { plm, jul, products } = await world();
  const made = await call("POST", "/api/v1/inventory/transfers", { from_branch_id: jul, to_branch_id: plm, notes: "", lines: [{ product_id: products[0].id, qty_base: 10000 }] });
  assert.equal(made.status, 200);
  const res = await call("POST", `/api/v1/inventory/transfers/${made.json.id}/ship`);
  refused(res, 409, "would overdraw the source branch", "would overdraw the source branch, %.4f available (%.4f on hand, %.4f reserved), %.4f requested");
  assert.match(res.json.error, /^transfer TRF-\S+ line 1 would overdraw the source branch, -?\d+\.\d{4} available \(-?\d+\.\d{4} on hand, -?\d+\.\d{4} reserved\), 10000\.0000 requested$/);
});

test("an unposted sheet's summary carries no net figure, as the core's omitempty does", async () => {
  const { plm } = await world();
  const made = await call("POST", "/api/v1/counts/sheets", { branch_id: plm, scope: "FULL", notes: "unposted summary" });
  await call("POST", `/api/v1/counts/sheets/${made.json.id}/freeze`);
  const detail = await call("GET", `/api/v1/counts/sheets/${made.json.id}`);
  const target = detail.json.lines.find((l) => l.snapshot_qty_base > 0);
  const counted = await callAs("POST", `/api/v1/counts/sheets/${made.json.id}/counts`, YARD_ID, {
    lines: [{ product_id: target.product_id, location_id: target.location_id, counted_qty_base: target.snapshot_qty_base - 2, reason_code: "SHRINK" }],
  });
  assert.equal(counted.status, 200);
  const read = await call("GET", `/api/v1/counts/sheets/${made.json.id}`);
  assert.equal(read.json.summary.variance_lines, 1);
  assert.ok(!("net_variance_value_cents" in read.json.summary), "no net before the post");
  assert.ok(!("abs_variance_value_cents" in read.json.summary), "no gross before the post");
});

test("posting a counted, approved sheet moves the stock and is a no-op the second time", async () => {
  const { plm } = await world();
  const made = await call("POST", "/api/v1/counts/sheets", { branch_id: plm, scope: "FULL", notes: "the aggregate check" });
  await call("POST", `/api/v1/counts/sheets/${made.json.id}/freeze`);
  const detail = await call("GET", `/api/v1/counts/sheets/${made.json.id}`);
  const target = detail.json.lines.find((l) => l.snapshot_qty_base > 0);
  // A yard manager counts (a work seat, the maker); the procurement manager approves and posts (a manage seat, the checker).
  const counted = await callAs("POST", `/api/v1/counts/sheets/${made.json.id}/counts`, YARD_ID, {
    lines: [{ product_id: target.product_id, location_id: target.location_id, counted_qty_base: target.snapshot_qty_base - 1, reason_code: "DAMAGE" }],
  });
  assert.equal(counted.status, 200);
  const approved = await callAs("POST", `/api/v1/counts/sheets/${made.json.id}/approve`, PROCUREMENT_ID);
  assert.equal(approved.status, 200);
  holds("InvCountSheet", approved.json);
  assert.ok((approved.json.approved_by ?? "").length > 0);
  assert.notEqual(approved.json.approved_by, approved.json.counted_by, "the checker differs from the maker");
  const posted = await callAs("POST", `/api/v1/counts/sheets/${made.json.id}/post`, PROCUREMENT_ID);
  assert.equal(posted.status, 200);
  holds("InvCountSheetDetail", posted.json);
  assert.equal(posted.json.sheet.status, "POSTED");
  const valued = posted.json.lines.find((l) => l.product_id === target.product_id);
  assert.equal(valued.variance_base, -1);
  assert.ok((valued.move_id ?? "") !== "", "the variance wrote its stock move");
  const again = await callAs("POST", `/api/v1/counts/sheets/${made.json.id}/post`, PROCUREMENT_ID);
  assert.equal(again.status, 200, "a re-post is a no-op through the per-sheet key");
  refused(await call("POST", `/api/v1/counts/sheets/${made.json.id}/cancel`), 409, "a POSTED sheet cannot be cancelled");
});

test("a sheet is abandoned before post, and the reason codes and negative stock read", async () => {
  const { jul } = await world();
  const sheet = await openSheet();
  const cancelled = await call("POST", `/api/v1/counts/sheets/${sheet.id}/cancel`);
  assert.equal(cancelled.status, 200);
  holds("InvCountCancelResult", cancelled.json);
  assert.equal(cancelled.json.status, "CANCELLED");
  const codes = await call("GET", "/api/v1/counts/reason-codes");
  assert.equal(codes.status, 200);
  for (const code of codes.json.items) holds("InvCountReasonCode", code);
  const words = codes.json.items.map((c) => c.code);
  for (const seeded of ["DAMAGE", "SHRINK", "FOUND", "MISCOUNT", "EXPIRY"]) {
    assert.ok(words.includes(seeded), `the seeded reason ${seeded} is missing`);
  }
  const negative = await call("GET", `/api/v1/counts/negative-stock?branch_id=${jul}`);
  assert.equal(negative.status, 200);
  for (const row of negative.json.items) holds("InvCountNegativeStockRow", row);
  assert.ok(negative.json.items.length >= 1, "the sand sold before receipt at Julian reads negative");
});

test("the recon is kept per period and branch, ties or names the delta, and re-runs to the same row", async () => {
  const { plm } = await world();
  const res = await call("GET", `/api/v1/counts/recon?branch_id=${plm}`);
  assert.equal(res.status, 200);
  holds("InvCountReconResult", res.json);
  assert.equal(res.json.current.branch_id, plm);
  assert.ok(["TIED", "OUT_OF_TIE", "OPEN"].includes(res.json.current.status));
  assert.ok(Array.isArray(res.json.history.items));
  const again = await call("GET", `/api/v1/counts/recon?branch_id=${plm}`);
  assert.equal(again.json.current.id, res.json.current.id, "a re-run upserts the period's row, so its identity stands");
});

// ---- the role walls ------------------------------------------------------------------------------------------

test("a read-only role reads everything and is refused every write in the shared words", async () => {
  await reset();
  await role("payables");
  const reads = [
    "/api/v1/inventory/on-hand?product_id=f1000000-0000-4000-8000-000000000001",
    "/api/v1/inventory/moves?product_id=f1000000-0000-4000-8000-000000000001",
    "/api/v1/inventory/pending-fulfilment",
    "/api/v1/inventory/branch-stock?product_id=f1000000-0000-4000-8000-000000000001",
    "/api/v1/inventory/browse",
    "/api/v1/inventory/reservations",
    "/api/v1/inventory/transfers",
    "/api/v1/inventory/transit-days",
    "/api/v1/inventory/reorder-points",
    "/api/v1/counts/sheets",
    "/api/v1/counts/reason-codes",
    "/api/v1/counts/negative-stock",
  ];
  for (const path of reads) {
    const res = await call("GET", path);
    assert.equal(res.status, 200, `${path} should read`);
  }
  const writes = [
    ["POST", "/api/v1/inventory/reservations", { product_id: "x", branch_id: "y", location_id: "y", res_type: "ORDER", qty_base: 1, source_type: "order", source_ref_id: "s", reason: "" }],
    ["POST", "/api/v1/inventory/reservations/expire", {}],
    ["POST", "/api/v1/inventory/transfers", { from_branch_id: "a", to_branch_id: "b", notes: "", lines: [] }],
    ["PUT", "/api/v1/inventory/allocation-policy", { branch_id: "a", order_mode: "HARD", quote_mode: "HARD", order_ttl_hours: 1, quote_ttl_hours: 1, is_default: false }],
    ["PUT", "/api/v1/inventory/reorder-points", { product_id: "a", branch_id: "b", reorder_point_base: 0, reorder_qty_base: 0, order_multiple_base: 0, preferred_vendor_id: "", lead_time_days: 0, is_active: true }],
    ["POST", "/api/v1/inventory/reorder-points/bootstrap", { branch_id: "a" }],
    ["POST", "/api/v1/inventory/shortfall/create-po", { branch_id: "a", product_ids: [] }],
    ["POST", "/api/v1/inventory/receive", { po_id: "x", lines: [] }],
    ["POST", "/api/v1/counts/sheets", { branch_id: "a", scope: "FULL", notes: "" }],
  ];
  for (const [method, path, body] of writes) {
    const res = await call(method, path, body);
    assert.equal(res.status, 403, `${method} ${path} should be walled`);
    assert.deepEqual(res.json, { error: "Forbidden" });
  }
  assert.ok(coreSource.includes('{"error":"Forbidden"}'), "the shared refusal body is not the core's");
});

test("a work seat receives and counts, and is refused the manager moves in the shared words", async () => {
  await reset();
  const { plm } = await world();
  await role("yard");
  const sheet = await call("POST", "/api/v1/counts/sheets", { branch_id: plm, scope: "FULL", notes: "the yard's own count" });
  assert.equal(sheet.status, 201, "a work seat opens and freezes and counts");
  const frozen = await call("POST", `/api/v1/counts/sheets/${sheet.json.id}/freeze`);
  assert.equal(frozen.status, 200);
  const managerMoves = [
    ["POST", `/api/v1/counts/sheets/${sheet.json.id}/approve`],
    ["POST", `/api/v1/counts/sheets/${sheet.json.id}/post`],
    ["POST", "/api/v1/inventory/transfers", { from_branch_id: plm, to_branch_id: plm, notes: "", lines: [] }],
  ];
  for (const [method, path, body] of managerMoves) {
    const res = await call(method, path, body);
    assert.equal(res.status, 403, `${method} ${path} is a manager seat's move`);
    assert.deepEqual(res.json, { error: "Forbidden" });
  }
});
