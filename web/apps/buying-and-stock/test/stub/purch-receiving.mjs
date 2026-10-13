// The receiving routes of the Buying and Stock stub: the receiving queue, the receive, a receipt and
// the landed cost attached to it, the order's cost payload and document, and the sends, with the
// core's wire shapes, words and the order in which it refuses. The stub contract test
// (test/receiving-contract.test.mjs) holds every answer to the generated descriptors and every
// sentence to the core's source.

export function makeReceiving(kit, landed, vendors) {
  const { uuid, json, refuse, readBody, NOW_ISO } = kit;
  let receipts;
  let sends;
  let counter;

  function reset() {
    receipts = [];
    sends = [];
    counter = 0;
  }

  const fmt = (n) => n.toFixed(4);
  const say = (res, status, words) => {
    refuse(res, status, words);
    return true;
  };
  const wall = (res) => {
    json(res, { error: "Forbidden" }, 403);
    return true;
  };
  const canWrite = (role) => role === "procurement-manager" || role === "super-admin";

  const productOf = (n) => kit.PRODUCTS.find((p) => p.n === n);

  function receiptView(r) {
    const hidden = kit.flags().costsHidden;
    return {
      id: r.id,
      po_id: r.po_id,
      po_no: r.po_no,
      seq: r.seq,
      grn_no: r.grn_no,
      stock_doc_id: r.stock_doc_id,
      ...(r.landed_cost_id === "" ? {} : { landed_cost_id: r.landed_cost_id }),
      ...(hidden ? {} : { value_cad_cents: r.value }),
      received_by: "de110000-0000-4000-9000-000000000008",
      received_at: r.received_at,
      notes: r.notes,
      lines: r.lines.map((l) => ({
        id: l.id,
        po_line_id: l.po_line_id,
        product_id: uuid("f", l.product),
        sku: productOf(l.product).sku,
        qty_base: l.qty,
        ...(hidden ? {} : { unit_cost_cents: l.cost, value_cents: l.value }),
      })),
    };
  }

  function statusAfter(po) {
    return po.lines.every((l) => (l.received ?? 0) >= l.qty - 1e-6) ? "RECEIVED" : "PARTIAL";
  }

  const poOf = (id) => kit.world().pos.find((p) => p.id === id);

  function documentRows(po) {
    const vendor = kit.VENDORS.find((v) => v.n === po.vendor);
    const hidden = kit.flags().costsHidden;
    return { vendor, hidden, lines: po.lines.map((l) => ({ ...l, p: productOf(l.product) })) };
  }

  function csvOf(po) {
    const { vendor, hidden, lines } = documentRows(po);
    const head = hidden ? "Line,SKU,Description,Qty,Unit" : "Line,SKU,Description,Qty,Unit,Unit cost,Line total";
    const body = lines.map((l, i) => {
      const base = [i + 1, l.p.sku, l.p.name, l.qty, l.p.uom];
      return (hidden ? base : [...base, (l.cost / 100).toFixed(2), ((l.qty * l.cost) / 100).toFixed(2)]).join(",");
    });
    return [`Purchase order,${po.no},${vendor.name}`, head, ...body].join("\n") + "\n";
  }

  function htmlOf(po) {
    const { vendor } = documentRows(po);
    return `<!doctype html><html><head><meta charset="utf-8"><title>${po.no}</title></head><body><h1>Purchase order ${po.no}</h1><p>${vendor.name}</p></body></html>`;
  }

  function target(po) {
    const contact = vendors.contactOf(uuid("a", po.vendor));
    return contact;
  }

  async function handle({ req, res, url, path, method, role }) {
    const base = "/api/v1/purchasing/";
    if (!path.startsWith(base)) return false;
    const rest = path.slice(base.length);

    if (rest === "receiving" && method === "GET") {
      const named = url.searchParams.get("branch_id") ?? "";
      // The core's branchFence: a named branch out of reach, or a scoped caller with no grants at all, is refused.
      if (named !== "" ? !kit.reach().includes(named) : kit.reach().length === 0) return say(res, 403, "caller cannot access this branch");
      const hidden = kit.flags().costsHidden;
      const rows = kit
        .world()
        .pos.filter((p) => (p.status === "ORDERED" || p.status === "PARTIAL") && kit.reach().includes(kit.BRANCH))
        .filter((p) => p.lines.some((l) => l.qty - (l.received ?? 0) > 0.0001))
        .map((p) => {
          const open = p.lines.filter((l) => l.qty - (l.received ?? 0) > 0.0001);
          const vendor = kit.VENDORS.find((v) => v.n === p.vendor);
          return {
            po_id: p.id,
            po_no: p.no,
            vendor_id: uuid("a", vendor.n),
            vendor_name: vendor.name,
            branch_id: kit.BRANCH,
            status: p.status,
            ...(p.expect === null ? {} : { expected_at: p.expect }),
            open_lines: open.length,
            qty_outstanding_base: open.map((l) => l.qty - (l.received ?? 0)).reduce((a, b) => a + b, 0),
            ...(hidden ? {} : { value_open_cents: Math.round(open.map((l) => (l.qty - (l.received ?? 0)) * l.cost).reduce((a, b) => a + b, 0)) }),
          };
        });
      rows.sort((a, b) => (a.expected_at ?? "9999").localeCompare(b.expected_at ?? "9999") || a.po_no.localeCompare(b.po_no, undefined, { numeric: true }));
      json(res, { items: rows });
      return true;
    }

    const costs = rest.match(/^pos\/([^/]+)\/costs$/);
    if (costs && method === "GET") {
      const po = poOf(costs[1]);
      if (po === undefined) return say(res, 404, "not found");
      if (kit.flags().costsHidden) {
        json(res, {});
        return true;
      }
      const view = kit.poView(po);
      json(res, { subtotal_cents: view.subtotal_cents, subtotal_cad_cents: view.subtotal_cad_cents });
      return true;
    }

    const doc = rest.match(/^pos\/([^/]+)\/document(\.csv)?$/);
    if (doc && method === "GET") {
      const po = poOf(doc[1]);
      if (po === undefined) return say(res, 404, "not found");
      res.writeHead(200, { "Content-Type": doc[2] ? "text/csv" : "text/html; charset=utf-8" });
      res.end(doc[2] ? csvOf(po) : htmlOf(po));
      return true;
    }

    const sendsPath = rest.match(/^pos\/([^/]+)\/sends$/);
    if (sendsPath && method === "GET") {
      if (poOf(sendsPath[1]) === undefined) return say(res, 404, "not found");
      json(res, { items: sends.filter((s) => s.po_id === sendsPath[1]).map(({ key, ...s }) => s) });
      return true;
    }

    const send = rest.match(/^pos\/([^/]+)\/send$/);
    if (send && method === "POST") {
      if (!canWrite(role)) return wall(res);
      const body = (await readBody(req)) ?? {};
      const po = poOf(send[1]);
      if (po === undefined) return say(res, 404, "not found");
      const key = String(body.idempotency_key ?? "").trim();
      if (key !== "") {
        const prior = sends.find((s) => s.key === key);
        if (prior !== undefined) {
          if (prior.po_id !== po.id) return say(res, 409, `idempotency key "${key}" already belongs to a send for another order, a new order needs a new key`);
          const { key: _k, ...view } = prior;
          json(res, view);
          return true;
        }
      }
      if (po.status === "DRAFT") return say(res, 409, "place the order first, a draft is still being edited");
      if (po.status === "CANCELLED") return say(res, 409, "this order was cancelled, nothing to send");
      const tagged = target(po);
      let recipient;
      let kind;
      let reason = "";
      if (tagged !== undefined && tagged.email.trim() !== "") {
        recipient = tagged.email.trim();
        kind = "VENDOR_CONTACT";
      } else if (tagged !== undefined && tagged.phone.trim() !== "") {
        return say(res, 400, "this vendor's ordering contact is a phone number, add an email to it and the order can be sent");
      } else if (kit.flags().refuseUntagged) {
        return say(res, 400, "no ordering contact is tagged for this vendor, tag one and the order goes to the right desk");
      } else {
        const candidates = vendors.emailsOf(uuid("a", po.vendor));
        if (candidates.length === 0) return say(res, 400, "this vendor has no email on file, tag an ordering contact to send anything at all");
        if (body.confirm_fallback !== true) return say(res, 400, "nobody is tagged for ordering at this vendor, confirm the fallback recipient or tag a contact");
        if (candidates.length > 1) return say(res, 400, `this vendor record carries ${candidates.length} email addresses and no ordering contact, tag one so the order has one desk to land on`);
        recipient = candidates[0];
        kind = "FALLBACK";
        reason = "no ordering contact tagged; operator confirmed the vendor record address";
      }
      const csv = csvOf(po);
      const failed = kit.flags().sendFails;
      const row = {
        id: uuid("5", sends.length + 1),
        po_id: po.id,
        channel: "EMAIL",
        recipient,
        recipient_kind: kind,
        ...(reason === "" ? {} : { fallback_reason: reason }),
        status: failed ? "FAILED" : "DELIVERED",
        provider: "stub-sink",
        ...(failed ? { detail: "no mail transport is wired" } : { provider_ref: `sink-${sends.length + 1}` }),
        document_name: `${po.no}.csv`,
        document_bytes: csv.length,
        sent_at: NOW_ISO(),
        key,
      };
      sends.push(row);
      const { key: _key, ...view } = row;
      json(res, view);
      return true;
    }

    const receive = rest.match(/^pos\/([^/]+)\/receive$/);
    if (receive && method === "POST") {
      if (!canWrite(role)) return wall(res);
      const body = await readBody(req);
      if (body === null) return say(res, 400, "invalid_json");
      if (!Array.isArray(body.lines) || body.lines.length === 0) return say(res, 400, "nothing to receive, supply at least one line");
      const po = poOf(receive[1]);
      if (po === undefined) return say(res, 404, "not found");
      if (!kit.reach().includes(kit.BRANCH)) return say(res, 404, "not found");
      const why = { DRAFT: [409, "cannot receive against a DRAFT purchase order, place the order first"], RECEIVED: [409, "purchase order is already fully received"], CLOSED: [409, "purchase order is closed"], CANCELLED: [409, "purchase order was cancelled"] }[po.status];
      if (why !== undefined) return say(res, why[0], why[1]);
      const byId = new Map(po.lines.map((l, i) => [l.id, { l, no: i + 1 }]));
      const requested = new Map();
      for (const rl of body.lines) {
        if (!byId.has(rl.po_line_id)) return say(res, 400, `line ${rl.po_line_id} does not belong to this purchase order`);
        requested.set(rl.po_line_id, (requested.get(rl.po_line_id) ?? 0) + Number(rl.qty_base));
      }
      for (const [id, qty] of requested) {
        const { l, no } = byId.get(id);
        if (!(qty > 0)) return say(res, 400, `receive quantity on line ${no} must be greater than zero`);
        const outstanding = l.qty - (l.received ?? 0);
        if (qty > outstanding + 1e-6) return say(res, 409, `over-receipt on line ${no}, ${fmt(qty)} requested but only ${fmt(outstanding)} outstanding`);
      }
      counter += 1;
      const seq = receipts.filter((r) => r.po_id === po.id).length + 1;
      const lines = [];
      let total = 0;
      for (const [id, qty] of requested) {
        const { l } = byId.get(id);
        const value = Math.round(qty * l.cost);
        total += value;
        lines.push({ id: uuid("6", receipts.length * 10 + lines.length + 1), po_line_id: id, product: l.product, qty, cost: l.cost, value });
        l.received = (l.received ?? 0) + qty;
      }
      po.status = statusAfter(po);
      const r = { id: uuid("8", counter), po_id: po.id, po_no: po.no, seq, grn_no: `${po.no}/R${seq}`, stock_doc_id: uuid("7", counter), landed_cost_id: "", received_at: NOW_ISO(), notes: String(body.notes ?? ""), value: total, lines };
      receipts.push(r);
      json(res, receiptView(r));
      return true;
    }

    const one = rest.match(/^receipts\/([^/]+)$/);
    if (one && method === "GET") {
      const r = receipts.find((x) => x.id === one[1]);
      if (r === undefined) return say(res, 404, "not found");
      json(res, receiptView(r));
      return true;
    }
    const attach = rest.match(/^receipts\/([^/]+)\/landed-cost$/);
    if (attach && method === "POST") {
      if (!canWrite(role)) return wall(res);
      const body = await readBody(req);
      if (body === null) return say(res, 400, "invalid_json");
      const r = receipts.find((x) => x.id === attach[1]);
      if (r === undefined) return say(res, 404, "not found");
      if (r.landed_cost_id !== "") return say(res, 409, `receipt ${r.grn_no} already carries a landed cost`);
      const f = Number(body.freight_cents ?? 0);
      const d = Number(body.duty_cents ?? 0);
      const h = Number(body.handling_cents ?? 0);
      if (f < 0 || d < 0 || h < 0) return say(res, 400, "freight/duty/handling must be non-negative");
      if (f + d + h === 0) return say(res, 400, "a landed cost with no freight, duty or handling costs nothing");
      const currency = String(body.currency ?? "").trim().toUpperCase() || "CAD";
      if (currency !== "CAD" && currency !== "USD") return say(res, 400, "currency must be CAD or USD");
      const fx = currency === "CAD" ? 1 : Number(body.fx_rate ?? 0);
      if (currency === "USD" && !(fx > 0)) return say(res, 400, "a USD purchase order needs a positive fx_rate");
      r.landed_cost_id = landed.createAndApply({
        po_id: r.stock_doc_id,
        po_ref: r.grn_no,
        branch_id: kit.BRANCH,
        currency,
        fx_rate: fx,
        freight_cents: f,
        duty_cents: d,
        handling_cents: h,
        allocation_basis: String(body.allocation_basis ?? "VALUE").toUpperCase(),
      });
      json(res, receiptView(r));
      return true;
    }
    return false;
  }

  return {
    reset,
    handle,
    receiptsFor: (poId) => receipts.filter((r) => r.po_id === poId).map(receiptView),
    receiptLines: (docId) => {
      const r = receipts.find((x) => x.stock_doc_id === docId);
      if (r === undefined) return [];
      return r.lines.map((l) => ({ po_line_id: l.po_line_id, product_id: uuid("f", l.product), qty: l.qty, value: l.value, weight: l.qty * l.product * 3.5, special: l.product === 3 }));
    },
  };
}
