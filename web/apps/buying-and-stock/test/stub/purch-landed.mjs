// The landed cost routes of the Buying and Stock stub: the seven routes of the core's landedcost
// module, answering with its wire shapes and its words, including the quirk the screens are built
// around: an apply or a retro of a document that is not a draft is a quiet no-op that still answers
// the same body as a real one (handler.go writes the posted status whatever happened).
// The stub contract test (test/purch-contract.test.mjs) holds every answer here to the generated
// wire descriptors and every sentence to the core's source.

const SPECIAL_BUYS = new Set([3]);

// Largest remainder over non-negative integer weights, exact: the shares sum to the total.
function allocateLargestRemainder(total, weights) {
  const n = weights.length;
  if (n === 0) return [];
  if (total === 0) return weights.map(() => 0);
  const sum = weights.reduce((a, w) => a + Math.max(w, 0), 0);
  if (sum === 0) {
    const base = Math.trunc(total / n);
    const out = weights.map(() => base);
    for (let i = 0; i < total - base * n; i += 1) out[i] += 1;
    return out;
  }
  const T = BigInt(total);
  const S = BigInt(sum);
  const parts = weights.map((w, i) => {
    const num = T * BigInt(Math.max(w, 0));
    return { i, floor: num / S, rem: num % S };
  });
  let left = Number(T - parts.reduce((a, p) => a + p.floor, 0n));
  const order = [...parts].sort((a, b) => (a.rem === b.rem ? a.i - b.i : a.rem > b.rem ? -1 : 1));
  const out = parts.map((p) => Number(p.floor));
  for (const p of order) {
    if (left <= 0) break;
    out[p.i] += 1;
    left -= 1;
  }
  return out;
}

export function makeLanded(kit) {
  const { uuid, json, refuse, readBody, NOW_ISO, BRANCH } = kit;
  let docs;
  let counter;

  function reset() {
    counter = 0;
    docs = [];
    // One landed cost already applied on the received order PO-TRT-66, so a late freight can follow it.
    const po = kit.world().pos.find((p) => p.no === "PO-TRT-66");
    docs.push(
      stamp({
        po_id: po.id,
        po_ref: po.no,
        branch_id: BRANCH,
        currency: "CAD",
        fx_rate: 1,
        freight_cents: 48_000,
        duty_cents: 0,
        handling_cents: 3550,
        allocation_basis: "VALUE",
        exclude_special_buys: true,
        status: "POSTED",
      }),
    );
    applyDoc(docs[0]);
  }

  function stamp(input) {
    counter += 1;
    const cad = (cents) => Math.round(cents * input.fx_rate);
    return {
      id: uuid("e", counter),
      retro_of_id: "",
      onHand: [],
      allocations: [],
      created_at: NOW_ISO(),
      ...input,
      freight_cad_cents: cad(input.freight_cents),
      duty_cad_cents: cad(input.duty_cents),
      handling_cad_cents: cad(input.handling_cents),
    };
  }

  const total = (d) => d.freight_cad_cents + d.duty_cad_cents + d.handling_cad_cents;

  function receivedLines(poId) {
    const po = kit.world().pos.find((p) => p.id === poId);
    if (po === undefined) return [];
    return po.lines
      .filter((l) => (l.received ?? 0) > 0)
      .map((l) => ({
        po_line_id: l.id,
        product_id: uuid("f", l.product),
        qty: l.received,
        value: l.received * l.cost,
        weight: l.received * l.product * 3.5,
        special: SPECIAL_BUYS.has(l.product),
      }));
  }

  const weightOf = (basis, l) => (basis === "WEIGHT" ? Math.round(l.weight * 10_000) : basis === "QUANTITY" ? Math.round(l.qty * 10_000) : l.value);
  const basisNumber = (basis, l) => (basis === "WEIGHT" ? l.weight : basis === "QUANTITY" ? l.qty : l.value);

  function allocate(d, basis, lines) {
    const included = d.exclude_special_buys ? lines.filter((l) => !l.special) : lines;
    const parts = allocateLargestRemainder(total(d), included.map((l) => weightOf(basis, l)));
    return included.map((l, i) => ({ line: l, cents: parts[i] }));
  }

  function applyDoc(d) {
    const lines = receivedLines(d.po_id);
    d.allocations = allocate(d, d.allocation_basis, lines).map(({ line, cents }) => ({
      po_line_id: line.po_line_id,
      product_id: line.product_id,
      basis_weight: basisNumber(d.allocation_basis, line),
      allocated_cad_cents: cents,
      sold_through_cad_cents: 0,
    }));
  }

  function retroDoc(d) {
    const lines = receivedLines(d.po_id);
    d.allocations = allocate(d, d.allocation_basis, lines).map(({ line, cents }) => {
      const left = d.onHand.find((o) => o.po_line_id === line.po_line_id);
      const onHand = Math.min(Math.max(left?.on_hand_base ?? line.qty, 0), line.qty);
      const keep = Math.round((cents * onHand) / line.qty);
      return {
        po_line_id: line.po_line_id,
        product_id: line.product_id,
        basis_weight: basisNumber(d.allocation_basis, line),
        allocated_cad_cents: keep,
        sold_through_cad_cents: cents - keep,
      };
    });
  }

  const view = (d) => ({
    id: d.id,
    po_id: d.po_id,
    po_ref: d.po_ref,
    branch_id: d.branch_id,
    currency: d.currency,
    fx_rate: d.fx_rate,
    freight_cents: d.freight_cents,
    duty_cents: d.duty_cents,
    handling_cents: d.handling_cents,
    freight_cad_cents: d.freight_cad_cents,
    duty_cad_cents: d.duty_cad_cents,
    handling_cad_cents: d.handling_cad_cents,
    allocation_basis: d.allocation_basis,
    exclude_special_buys: d.exclude_special_buys,
    status: d.status,
    ...(d.retro_of_id === "" ? {} : { retro_of_id: d.retro_of_id }),
    ...(d.status === "DRAFT" ? {} : { gl_journal_id: uuid("9", Number(d.id.slice(-12))) }),
    created_at: d.created_at,
  });

  const FORBIDDEN_BRANCH = "caller cannot access this branch";

  async function handle({ req, res, url, path, method, role }) {
    const base = "/api/v1/landed-costs";
    if (path !== base && !path.startsWith(`${base}/`)) return false;
    const writes = method !== "GET";
    if (writes && role === "payables-receivables") {
      json(res, { error: "Forbidden" }, 403);
      return true;
    }
    if (path === base && method === "GET") {
      const branch = url.searchParams.get("branch_id") ?? "";
      if (branch === "" ? !kit.global() : !kit.reach().includes(branch)) return refuseWith(res, 403, FORBIDDEN_BRANCH);
      const status = url.searchParams.get("status") ?? "";
      const rows = docs.filter((d) => (branch === "" || d.branch_id === branch) && (status === "" || d.status === status));
      rows.sort((a, b) => b.created_at.localeCompare(a.created_at) || b.id.localeCompare(a.id));
      json(res, { items: rows.map(view) });
      return true;
    }
    if (path === base && method === "POST") {
      const body = await readBody(req);
      if (body === null) return refuseWith(res, 400, "invalid_json");
      const branch = String(body.branch_id ?? "");
      if (branch === "") return refuseWith(res, 400, "branch_id required");
      const currency = String(body.currency ?? "").trim().toUpperCase() || "CAD";
      if (currency !== "CAD" && currency !== "USD") return refuseWith(res, 400, "currency must be CAD or USD");
      let fx = Number(body.fx_rate ?? 0);
      if (!(fx > 0)) fx = 1;
      if (currency === "CAD") fx = 1;
      const basis = String(body.allocation_basis ?? "").trim().toUpperCase() || "VALUE";
      if (!["WEIGHT", "VALUE", "QUANTITY"].includes(basis)) return refuseWith(res, 400, "allocation_basis must be WEIGHT, VALUE or QUANTITY");
      const freight = Number(body.freight_cents ?? 0);
      const duty = Number(body.duty_cents ?? 0);
      const handling = Number(body.handling_cents ?? 0);
      if (freight < 0 || duty < 0 || handling < 0) return refuseWith(res, 400, "freight/duty/handling must be non-negative");
      const doc = stamp({
        po_id: String(body.po_id ?? ""),
        po_ref: String(body.po_ref ?? ""),
        branch_id: branch,
        currency,
        fx_rate: fx,
        freight_cents: freight,
        duty_cents: duty,
        handling_cents: handling,
        allocation_basis: basis,
        exclude_special_buys: body.exclude_special_buys === undefined || body.exclude_special_buys === null ? true : Boolean(body.exclude_special_buys),
        status: "DRAFT",
        retro_of_id: String(body.retro_of_id ?? ""),
        onHand: Array.isArray(body.on_hand) && body.retro_of_id ? body.on_hand : [],
      });
      docs.push(doc);
      json(res, { id: doc.id, status: "DRAFT" }, 201);
      return true;
    }
    const m = path.match(/^\/api\/v1\/landed-costs\/([^/]+)(?:\/(preview|apply|retro|void))?$/);
    if (m === null) return false;
    const doc = docs.find((d) => d.id === m[1]);
    if (doc === undefined) return refuseWith(res, 404, "not found");
    if (!kit.reach().includes(doc.branch_id)) return refuseWith(res, 403, FORBIDDEN_BRANCH);
    const op = m[2] ?? "";
    if (op === "" && method === "GET") {
      json(res, {
        ...view(doc),
        allocations: doc.allocations.map((a) => ({ ...a })),
      });
      return true;
    }
    if (op === "preview" && method === "GET") {
      const asked = String(url.searchParams.get("basis") ?? "").toUpperCase();
      const basis = ["WEIGHT", "VALUE", "QUANTITY"].includes(asked) ? asked : doc.allocation_basis;
      if (doc.po_id.trim() === "") return refuseWith(res, 422, "landed cost has no po_id to allocate across");
      const lines = receivedLines(doc.po_id);
      const shares = new Map(allocate(doc, basis, lines).map(({ line, cents }) => [line.po_line_id, cents]));
      json(res, {
        basis,
        total_cad_cents: total(doc),
        lines: lines.map((l) => ({
          product_id: l.product_id,
          po_line_id: l.po_line_id,
          basis_weight: basisNumber(basis, l),
          allocated_cad_cents: shares.get(l.po_line_id) ?? 0,
          is_special_buy: l.special,
        })),
      });
      return true;
    }
    if (method !== "POST") return false;
    if (op === "apply") {
      if (doc.po_id.trim() === "" && doc.status === "DRAFT") return refuseWith(res, 422, "landed cost has no po_id to allocate across");
      if (doc.status === "DRAFT") {
        applyDoc(doc);
        doc.status = "POSTED";
      }
      json(res, { status: "POSTED" });
      return true;
    }
    if (op === "retro") {
      if (doc.po_id.trim() === "" && doc.status === "DRAFT") return refuseWith(res, 422, "landed cost has no po_id to allocate across");
      if (doc.status === "DRAFT") {
        retroDoc(doc);
        doc.status = "RETRO_ADJUSTED";
      }
      json(res, { status: "RETRO_ADJUSTED" });
      return true;
    }
    if (op === "void") {
      if (doc.status === "VOIDED") {
        json(res, { status: "VOIDED" });
        return true;
      }
      if (!["POSTED", "ALLOCATED", "RETRO_ADJUSTED"].includes(doc.status)) return refuseWith(res, 409, `cannot void a ${doc.status} landed cost`);
      doc.status = "VOIDED";
      json(res, { status: "VOIDED" });
      return true;
    }
    return false;
  }

  function refuseWith(res, status, words) {
    refuse(res, status, words);
    return true;
  }

  return { reset, handle };
}
