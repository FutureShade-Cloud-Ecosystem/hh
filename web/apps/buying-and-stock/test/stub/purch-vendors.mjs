// The vendor routes of the Buying and Stock stub: the vendor records, the ordering contact, the
// vendor's own codes and the echoed order reference, with the core's wire shapes, validators, words
// and the order in which it refuses. The stub contract test (test/vendors-contract.test.mjs) holds
// every answer to the generated descriptors and every sentence to the core's source.

const ONE_ADDRESS = /^[^\s@,;<>]+@[^\s@,;<>]+$/;

export function makeVendors(kit) {
  const { uuid, json, refuse, readBody, isUuid, NOW_ISO } = kit;
  let vendors;
  let contacts;
  let codes;
  let nextNo;

  function reset() {
    nextNo = 100;
    vendors = kit.VENDORS.map((v) => ({ id: uuid("a", v.n), vendor_no: v.no, name: v.name, payment_terms_days: v.terms, order_email: "", is_active: true, is_t5018_subcontractor: false, business_number: "" }));
    contacts = new Map();
    codes = new Map();
    // The vendor's own codes the purchase order shows, as the earlier stub seeded them.
    for (const v of kit.VENDORS) {
      codes.set(
        uuid("a", v.n),
        kit.PRODUCTS.filter((p) => p.vendor === v.n).map((p) => ({
          vendor_id: uuid("a", v.n),
          product_id: uuid("f", p.n),
          sku: p.sku,
          product_name: p.name,
          vendor_sku: p.sku === "DBS-BRUS-DIM" ? "ALC-BRU-DIM-60" : `${v.no}-${p.sku}`,
        })),
      );
    }
  }

  const listView = (v) => ({ id: v.id, vendor_no: v.vendor_no, name: v.name, payment_terms_days: v.payment_terms_days, is_active: v.is_active, is_t5018_subcontractor: v.is_t5018_subcontractor, business_number: v.business_number });
  const wall = (res) => {
    json(res, { error: "Forbidden" }, 403);
    return true;
  };
  const say = (res, status, words) => {
    refuse(res, status, words);
    return true;
  };

  function validName(name) {
    const n = String(name ?? "").trim();
    if (n === "") return { words: "vendor name is required" };
    if (n.length > 200) return { words: "vendor name is too long (200 characters at most)" };
    return { value: n };
  }
  function validEmail(email) {
    const e = String(email ?? "").trim();
    if (e !== "" && !ONE_ADDRESS.test(e)) return { words: "order_email must be one email address" };
    return { value: e };
  }
  function validTerms(days, def) {
    if (days === undefined || days === null) return { value: def };
    if (!Number.isInteger(days) || days < 0 || days > 365) return { words: "payment_terms_days must be between 0 and 365" };
    return { value: days };
  }

  const norm = (s) => String(s).toUpperCase().replace(/[^A-Z0-9]/g, "");

  async function handle({ req, res, path, method, url, role }) {
    const base = "/api/v1/purchasing/";
    if (!path.startsWith(base)) return false;
    const rest = path.slice(base.length);
    const writes = method !== "GET";
    const canWrite = role === "procurement-manager" || role === "super-admin";

    if (rest === "vendors" && method === "GET") {
      json(res, { items: vendors.map(listView) });
      return true;
    }
    if (rest === "vendors" && method === "POST") {
      if (!canWrite) return wall(res);
      const body = await readBody(req);
      if (body === null) return say(res, 400, "invalid_json");
      const name = validName(body.name);
      if (name.words) return say(res, 400, name.words);
      const email = validEmail(body.order_email);
      if (email.words) return say(res, 400, email.words);
      const terms = validTerms(body.payment_terms_days, 30);
      if (terms.words) return say(res, 400, terms.words);
      nextNo += 1;
      const v = { id: uuid("a", nextNo), vendor_no: `V-${String(nextNo).padStart(4, "0")}`, name: name.value, payment_terms_days: terms.value, order_email: email.value, is_active: true, is_t5018_subcontractor: false, business_number: "" };
      vendors.push(v);
      codes.set(v.id, []);
      json(res, v, 201);
      return true;
    }
    // The T5018 record: the Financial Controller's subcontractor mark and the contractor's CRA
// business number (W3-T51). Its own door: the route's census row names money-desk at admin, and
// the Financial Controller is the matrix's only admin seat on money-desk, so the wall here is the
// controller and the super admin (which rides above every wall). Every other seat is refused with
// the shared Forbidden body before any handler runs.
const T5018_SEATS = new Set(["financial-controller", "super-admin"]);
function validBusinessNumber(raw) {
  const n = String(raw ?? "").replace(/\s+/g, "");
  if (n === "") return { value: "" };
  if (!/^\d{9}(RC0001)?$/.test(n)) return { words: "the business number must be nine digits, with RC0001 after them for a program account" };
  return { value: n };
}

const one = rest.match(/^vendors\/([^/]+)$/);    if (one && method === "PUT") {
      if (!canWrite) return wall(res);
      const body = await readBody(req);
      if (body === null) return say(res, 400, "invalid_json");
      if (!isUuid(one[1])) return say(res, 400, "vendor id must be a uuid");
      let name;
      let email;
      let terms;
      if (body.name !== undefined && body.name !== null) {
        name = validName(body.name);
        if (name.words) return say(res, 400, name.words);
      }
      if (body.order_email !== undefined && body.order_email !== null) {
        email = validEmail(body.order_email);
        if (email.words) return say(res, 400, email.words);
      }
      if (body.payment_terms_days !== undefined && body.payment_terms_days !== null) {
        terms = validTerms(body.payment_terms_days, 0);
        if (terms.words) return say(res, 400, terms.words);
      }
      const active = body.is_active === undefined || body.is_active === null ? undefined : Boolean(body.is_active);
      if (name === undefined && email === undefined && terms === undefined && active === undefined) return say(res, 400, "the body names nothing to change");
      const v = vendors.find((x) => x.id === one[1]);
      if (v === undefined) return say(res, 404, "not found");
      if (name) v.name = name.value;
      if (email) v.order_email = email.value;
      if (terms) v.payment_terms_days = terms.value;
      if (active !== undefined) v.is_active = active;
      json(res, v);
      return true;
    }
    const t5018 = rest.match(/^vendors\/([^/]+)\/t5018$/);
    if (t5018 && method === "PUT") {
      if (!T5018_SEATS.has(role)) return wall(res);
      const body = await readBody(req);
      if (body === null) return say(res, 400, "invalid_json");
      if (!isUuid(t5018[1])) return say(res, 400, "vendor id must be a uuid");
      if (body.is_subcontractor === undefined || body.is_subcontractor === null) return say(res, 400, "is_subcontractor is required on the T5018 record");
      const number = body.business_number === undefined || body.business_number === null ? undefined : validBusinessNumber(body.business_number);
      if (number !== undefined && number.words) return say(res, 400, number.words);
      const v = vendors.find((x) => x.id === t5018[1]);
      if (v === undefined) return say(res, 404, "not found");
      v.is_t5018_subcontractor = Boolean(body.is_subcontractor);
      if (number !== undefined) v.business_number = number.value;
      json(res, v);
      return true;
    }
    const contact = rest.match(/^vendors\/([^/]+)\/contact$/);    if (contact) {
      const id = contact[1];
      if (method === "GET") {
        const c = contacts.get(id);
        json(res, { tagged: c !== undefined, contact: c ?? { vendor_id: "", contact_name: "", email: "", phone: "" } });
        return true;
      }
      if (!canWrite) return wall(res);
      if (method === "PUT") {
        const body = await readBody(req);
        if (body === null) return say(res, 400, "invalid_json");
        if (!vendors.some((v) => v.id === id)) return say(res, 404, "not found");
        const name = String(body.contact_name ?? "").trim();
        const email = String(body.email ?? "").trim();
        const phone = String(body.phone ?? "").trim();
        if ([...name].length > 120) return say(res, 400, "contact_name is at most 120 characters");
        if (email.length > 254 || phone.length > 32) return say(res, 400, "an ordering email is at most 254 characters and a phone at most 32");
        if (email !== "" && (!email.includes("@") || /[ \t]/.test(email))) return say(res, 400, "that ordering email address is not an address");
        if (email === "" && phone === "") return say(res, 400, "an ordering contact needs an email or a phone, one of the two is what makes it a contact");
        const c = { vendor_id: id, contact_name: name, email, phone, updated_at: NOW_ISO() };
        contacts.set(id, c);
        json(res, { tagged: true, contact: c });
        return true;
      }
      if (method === "DELETE") {
        contacts.delete(id);
        json(res, { tagged: false, contact: { vendor_id: "", contact_name: "", email: "", phone: "" } });
        return true;
      }
    }
    const skus = rest.match(/^vendors\/([^/]+)\/skus$/);
    if (skus) {
      const id = skus[1];
      if (method === "GET") {
        json(res, { items: (codes.get(id) ?? []).map((k) => ({ ...k })) });
        return true;
      }
      if (!canWrite) return wall(res);
      if (method === "PUT") {
        const body = await readBody(req);
        if (body === null) return say(res, 400, "invalid_json");
        const code = String(body.vendor_sku ?? "").trim();
        const unit = String(body.vendor_uom ?? "").trim().toUpperCase();
        const factor = Number(body.qty_per_vendor_uom ?? 0);
        if (!body.product_id) return say(res, 400, "a mapping needs a product");
        if ([...code].length > 64) return say(res, 400, "a vendor code is at most 64 characters");
        if ((unit === "") !== (factor === 0)) return say(res, 400, "a vendor unit needs the number of our units it holds, one without the other cannot be counted");
        if (factor < 0) return say(res, 400, "a vendor unit cannot hold a negative quantity");
        if (code === "" && unit === "") return say(res, 400, "an empty mapping is the same as no mapping, remove it instead");
        const p = kit.PRODUCTS.find((x) => uuid("f", x.n) === body.product_id);
        if (p === undefined) return say(res, 404, "not found");
        const row = { vendor_id: id, product_id: body.product_id, sku: p.sku, product_name: p.name, vendor_sku: code, ...(unit === "" ? {} : { vendor_uom: unit, qty_per_vendor_uom: factor }), updated_at: NOW_ISO() };
        const list = codes.get(id) ?? [];
        const at = list.findIndex((k) => k.product_id === body.product_id);
        if (at >= 0) list[at] = row;
        else list.push(row);
        codes.set(id, list);
        json(res, row);
        return true;
      }
    }
    const sku = rest.match(/^vendors\/([^/]+)\/skus\/([^/]+)$/);
    if (sku && method === "DELETE") {
      if (!canWrite) return wall(res);
      codes.set(sku[1], (codes.get(sku[1]) ?? []).filter((k) => k.product_id !== sku[2]));
      res.writeHead(204);
      res.end();
      return true;
    }
    if (rest === "vendor-ref" && method === "GET") {
      const ref = url.searchParams.get("ref") ?? "";
      if (ref.trim() === "") return say(res, 400, "type the order number the vendor quoted");
      const po = kit.world().pos.find((p) => norm(p.no) === norm(ref));
      if (po === undefined) return say(res, 404, "not found");
      json(res, kit.poView(po));
      return true;
    }
    return writes && false;
  }

  return {
    reset,
    handle,
    contactOf: (vendorId) => contacts.get(vendorId),
    emailsOf: (vendorId) => {
      const v = vendors.find((x) => x.id === vendorId);
      return v === undefined || v.order_email === "" ? [] : [v.order_email];
    },
  };
}
