// The vendor bill routes of the Buying and Stock stub: the bills and their three way match, the
// post (which runs the match again and refuses an unaccepted variance), the match tolerance and the
// purchasing policy, with the core's wire shapes, arithmetic, words and the order in which it
// refuses. The stub contract test (test/bills-contract.test.mjs) holds every answer to the
// generated descriptors and every sentence to the core's source.

const round = (f) => Math.round(f);
const EPSILON = 1e-6;
const toCad = (cents, fx) => round(cents * (fx > 0 ? fx : 1));

// The core's EvaluateLine (match.go), line for line.
function evaluate(input, tol) {
  const res = { state: "UNMATCHED", qtyVar: 0, priceVar: 0 };
  if (!input.hasPoLine) {
    res.priceVar = round(input.qty * input.billUnit);
    return res;
  }
  const billedTotal = input.prior + input.qty;
  res.qtyVar = billedTotal - input.received;
  res.priceVar = round(input.qty * (input.billUnit - input.poUnit));
  const qtyAllowance = (Math.abs(input.received) * tol.qty_tolerance_pct) / 100;
  const expected = Math.abs(input.qty * input.poUnit);
  let priceAllowance = round((expected * tol.price_tolerance_pct) / 100);
  if (tol.price_tolerance_abs_cents > priceAllowance) priceAllowance = tol.price_tolerance_abs_cents;
  const qtyOK = Math.abs(res.qtyVar) <= qtyAllowance + EPSILON;
  const priceOK = Math.abs(res.priceVar) <= priceAllowance;
  if (qtyOK && priceOK) res.state = "MATCHED";
  else if (priceOK && res.qtyVar < 0) res.state = "SHORT_BILLED";
  else res.state = "VARIANCE";
  return res;
}

const RANK = { MATCHED: 0, UNMATCHED: 1, SHORT_BILLED: 2, VARIANCE: 3 };
function rollUp(states) {
  if (states.length === 0) return "UNMATCHED";
  return states.reduce((worst, s) => (RANK[s] > RANK[worst] ? s : worst), "MATCHED");
}

export function makeBills(kit) {
  const { uuid, json, refuse, readBody, NOW_ISO, BRANCH } = kit;
  let bills;
  let counter;
  let tolerance;
  let branchTolerance;
  let policyStore;

  const poOf = (id) => kit.world().pos.find((p) => p.id === id);

  function tolFor(branch) {
    return branchTolerance.get(branch) ?? tolerance;
  }

  function lineBrief(po, poLineId) {
    const l = po.lines.find((x) => x.id === poLineId);
    return l;
  }

  function evalLine(po, bl, billFx, tol, priorOverride) {
    const unit = toCad(bl.unit_cost_cents, billFx);
    const line = bl.po_line_id ? lineBrief(po, bl.po_line_id) : undefined;
    return {
      unit,
      line,
      res: evaluate(
        {
          hasPoLine: line !== undefined,
          qty: bl.qty_billed_base,
          billUnit: unit,
          ordered: line?.qty ?? 0,
          received: line?.received ?? 0,
          prior: priorOverride ?? line?.billed ?? 0,
          poUnit: line === undefined ? 0 : toCad(line.cost, 1),
        },
        tol,
      ),
    };
  }

  function createBill(po, input, billFx) {
    const tol = tolFor(kit.BRANCH);
    const lines = input.lines.map((bl, i) => {
      const { unit, res } = evalLine(po, bl, billFx, tol);
      return { id: uuid("b", ++counter * 100 + i + 1), line_no: i + 1, po_line_id: bl.po_line_id ?? "", desc: bl.description, qty: bl.qty_billed_base, unit, total: round(bl.qty_billed_base * unit), match: res.state, qtyVar: res.qtyVar, priceVar: res.priceVar };
    });
    const subtotal = lines.reduce((a, l) => a + l.total, 0);
    const tax = toCad(input.tax_cents ?? 0, billFx);
    const bill = {
      id: uuid("d", counter),
      bill_no: String(input.bill_no).trim(),
      po,
      status: "DRAFT",
      match: rollUp(lines.map((l) => l.match)),
      bill_date: input.bill_date || kit.dayOffset(0),
      due_date: input.due_date || "",
      subtotal,
      tax,
      total: subtotal + tax,
      currency: input.currency,
      fx: billFx,
      lines,
      instalments: [],
      created_at: NOW_ISO(),
      posted_at: null,
      journal: "",
      accepted_by: "",
      accepted_note: "",
    };
    bills.push(bill);
    return bill;
  }

  function legs(bill) {
    const out = { relief: 0, variance: 0, ap: 0 };
    for (const l of bill.lines) {
      const line = l.po_line_id ? lineBrief(bill.po, l.po_line_id) : undefined;
      let relief = 0;
      if (line !== undefined) {
        let reliefQty = (line.received ?? 0) - (line.billed ?? 0);
        if (reliefQty > l.qty) reliefQty = l.qty;
        if (reliefQty > 0) relief = round(reliefQty * line.cost);
      }
      out.relief += relief;
      out.variance += l.total - relief;
      out.ap += l.total;
    }
    out.ap += bill.tax;
    return out;
  }

  function postBill(bill, accept, note) {
    const tol = tolFor(kit.BRANCH);
    for (const l of bill.lines) {
      const { res } = evalLine(bill.po, { po_line_id: l.po_line_id, qty_billed_base: l.qty, unit_cost_cents: l.unit, description: l.desc }, 1, tol);
      l.match = res.state;
      l.qtyVar = res.qtyVar;
      l.priceVar = res.priceVar;
    }
    bill.match = rollUp(bill.lines.map((l) => l.match));
    const v = legs(bill);
    if (bill.match === "VARIANCE" && !accept) return { refuse: `bill ${bill.bill_no} is in VARIANCE (${v.variance} cents against the purchase order), post it with accept_variance to record who accepted it` };
    for (const l of bill.lines) {
      const line = l.po_line_id ? lineBrief(bill.po, l.po_line_id) : undefined;
      if (line === undefined) continue;
      line.billed = (line.billed ?? 0) + l.qty;
      line.billedCents = (line.billedCents ?? 0) + l.total;
    }
    bill.status = "POSTED";
    bill.posted_at = NOW_ISO();
    bill.journal = uuid("9", 5000 + Number(bill.id.slice(-12)));
    if (bill.match === "VARIANCE") {
      bill.accepted_by = "de110000-0000-4000-9000-000000000009";
      bill.accepted_note = String(note ?? "").trim();
    }
    return {};
  }

  function reset() {
    counter = 0;
    bills = [];
    tolerance = { qty_tolerance_pct: 2, price_tolerance_pct: 1, price_tolerance_abs_cents: 50 };
    branchTolerance = new Map();
    policyStore = { vis: "MANAGER_UP", fb: "CONFIRM", branch: new Map() };
    const w = kit.world();
    const po66 = w.pos.find((p) => p.no === "PO-TRT-66");
    const po67 = w.pos.find((p) => p.no === "PO-TRT-67");
    const first = createBill(po66, { bill_no: "INV-7781", bill_date: kit.dayOffset(-6), due_date: kit.dayOffset(24), currency: "CAD", lines: [{ po_line_id: po66.lines[0].id, description: "Alcli Step 48in", qty_billed_base: 10, unit_cost_cents: 2500 }] }, 1);
    postBill(first, false, "");
    createBill(po67, { bill_no: "INV-7790", bill_date: kit.dayOffset(-2), due_date: kit.dayOffset(43), tax_cents: 0, currency: "CAD", lines: [{ po_line_id: po67.lines[0].id, description: "Unilock Wall Cap", qty_billed_base: 10, unit_cost_cents: 26200 }] }, 1);
    createBill(po67, { bill_no: "INV-7795", bill_date: kit.dayOffset(-1), due_date: kit.dayOffset(44), tax_cents: 0, currency: "CAD", lines: [{ po_line_id: po67.lines[0].id, description: "Unilock Wall Cap", qty_billed_base: 4, unit_cost_cents: 24000 }] }, 1);
  }

  const billView = (b) => ({
    id: b.id,
    bill_no: b.bill_no,
    vendor_id: uuid("a", b.po.vendor),
    vendor_name: kit.VENDORS.find((v) => v.n === b.po.vendor).name,
    branch_id: BRANCH,
    po_id: b.po.id,
    po_no: b.po.no,
    status: b.status,
    match_state: b.match,
    bill_date: b.bill_date,
    ...(b.due_date === "" ? {} : { due_date: b.due_date }),
    subtotal_cents: b.subtotal,
    tax_cents: b.tax,
    total_cents: b.total,
    currency: b.currency,
    fx_rate: b.fx,
    ...(b.journal === "" ? {} : { gl_journal_id: b.journal }),
    posted_at: b.posted_at,
    ...(b.accepted_by === "" ? {} : { variance_accepted_by: b.accepted_by, variance_accepted_note: b.accepted_note }),
    created_at: b.created_at,
  });

  const lineView = (b, l) => {
    const line = l.po_line_id ? lineBrief(b.po, l.po_line_id) : undefined;
    const p = line === undefined ? undefined : kit.PRODUCTS.find((x) => x.n === line.product);
    return {
      id: l.id,
      line_no: l.line_no,
      ...(l.po_line_id === "" ? {} : { po_line_id: l.po_line_id }),
      ...(p === undefined ? {} : { product_id: uuid("f", p.n), sku: p.sku }),
      description: l.desc,
      qty_billed_base: l.qty,
      qty_ordered_base: line?.qty ?? 0,
      qty_received_base: line?.received ?? 0,
      unit_cost_cents: l.unit,
      po_unit_cost_cents: line?.cost ?? 0,
      line_total_cents: l.total,
      match_state: l.match,
      qty_variance_base: l.qtyVar,
      price_variance_cents: l.priceVar,
    };
  };

  const detailView = (b) => ({ ...billView(b), lines: b.lines.map((l) => lineView(b, l)), instalments: b.instalments });

  function tolView(branch) {
    const own = branch === "" ? undefined : branchTolerance.get(branch);
    const t = own ?? tolerance;
    return { ...(branch === "" ? {} : { branch_id: branch }), ...t, is_branch_override: own !== undefined, resolved_from_default: own === undefined };
  }

  function policyView(branch) {
    const flags = kit.flags();
    const own = branch === "" ? undefined : policyStore.branch.get(branch);
    const vis = flags.costsHidden ? "HIDDEN" : (own?.vis ?? policyStore.vis) === "HIDDEN" ? "MANAGER_UP" : (own?.vis ?? policyStore.vis);
    return { ...(branch === "" ? {} : { branch_id: branch }), cost_visibility: vis, untagged_fallback: flags.refuseUntagged ? "REFUSE" : "CONFIRM", is_branch_override: own !== undefined };
  }

  const say = (res, status, words) => {
    refuse(res, status, words);
    return true;
  };
  const wall = (res) => {
    json(res, { error: "Forbidden" }, 403);
    return true;
  };
  const canBill = (role) => role === "payables-receivables" || role === "super-admin";

  async function handle({ req, res, url, path, method, role }) {
    const base = "/api/v1/purchasing/";
    if (!path.startsWith(base)) return false;
    const rest = path.slice(base.length);

    if (rest === "bills" && method === "GET") {
      const named = url.searchParams.get("branch_id") ?? "";
      if (named !== "" ? !kit.reach().includes(named) : kit.reach().length === 0) return say(res, 403, "caller cannot access this branch");
      const status = url.searchParams.get("status") ?? "";
      const match = url.searchParams.get("match_state") ?? "";
      const rows = bills.filter((b) => (status === "" || b.status === status) && (match === "" || b.match === match));
      rows.sort((a, b) => b.created_at.localeCompare(a.created_at) || b.bill_no.localeCompare(a.bill_no));
      json(res, { items: rows.map(billView) });
      return true;
    }
    if (rest === "bills" && method === "POST") {
      if (!canBill(role)) return wall(res);
      const body = await readBody(req);
      if (body === null) return say(res, 400, "invalid_json");
      if (String(body.bill_no ?? "").trim() === "") return say(res, 400, "bill_no (the vendor's invoice number) is required");
      if (!body.po_id) return say(res, 400, "po_id is required, a bill with no purchase order cannot be matched");
      if (!Array.isArray(body.lines) || body.lines.length === 0) return say(res, 400, "a bill needs at least one line");
      if ((body.tax_cents ?? 0) < 0) return say(res, 400, "tax cannot be negative");
      const po = poOf(body.po_id);
      if (po === undefined) return say(res, 404, "not found");
      if (po.status === "DRAFT" || po.status === "CANCELLED") return say(res, 409, `cannot bill a ${po.status} purchase order`);
      const currency = String(body.currency ?? "").trim().toUpperCase() || "CAD";
      if (currency !== "CAD" && currency !== "USD") return say(res, 400, "currency must be CAD or USD");
      const fx = currency === "CAD" ? 1 : Number(body.fx_rate ?? 0);
      if (currency === "USD" && !(fx > 0)) return say(res, 400, "a USD purchase order needs a positive fx_rate");
      for (let i = 0; i < body.lines.length; i += 1) {
        const bl = body.lines[i];
        if (!(bl.qty_billed_base > 0)) return say(res, 400, `bill line ${i + 1} quantity must be greater than zero`);
        if (bl.unit_cost_cents < 0) return say(res, 400, `bill line ${i + 1} unit cost cannot be negative`);
        if (bl.po_line_id && lineBrief(po, bl.po_line_id) === undefined) return say(res, 400, `bill line ${i + 1} references a line that is not on this purchase order`);
      }
      const bill = createBill(po, { ...body, currency }, fx);
      json(res, detailView(bill), 201);
      return true;
    }
    const one = rest.match(/^bills\/([^/]+)$/);
    if (one && method === "GET") {
      const b = bills.find((x) => x.id === one[1]);
      if (b === undefined) return say(res, 404, "not found");
      json(res, detailView(b));
      return true;
    }
    const post = rest.match(/^bills\/([^/]+)\/post$/);
    if (post && method === "POST") {
      if (!canBill(role)) return wall(res);
      const body = (await readBody(req)) ?? {};
      const b = bills.find((x) => x.id === post[1]);
      if (b === undefined) return say(res, 404, "not found");
      if (b.status !== "DRAFT") return say(res, 409, `bill is already ${b.status}`);
      const out = postBill(b, body.accept_variance === true, body.note);
      if (out.refuse) return say(res, 409, out.refuse);
      json(res, detailView(b));
      return true;
    }
    if (rest === "tolerance" && method === "GET") {
      json(res, tolView(url.searchParams.get("branch_id") ?? ""));
      return true;
    }
    if (rest === "tolerance" && method === "PUT") {
      if (role !== "super-admin") return wall(res);
      const body = await readBody(req);
      if (body === null) return say(res, 400, "invalid_json");
      const qty = Number(body.qty_tolerance_pct ?? 0);
      const price = Number(body.price_tolerance_pct ?? 0);
      const abs = Number(body.price_tolerance_abs_cents ?? 0);
      if (qty < 0 || price < 0 || abs < 0) return say(res, 400, "tolerances cannot be negative");
      if (qty > 25 || price > 25) return say(res, 400, "a match tolerance above 25% is not a tolerance, it is an off switch");
      const branch = String(body.branch_id ?? "");
      if (branch !== "" && !kit.reach().includes(branch)) return say(res, 403, "caller cannot access this branch");
      const next = { qty_tolerance_pct: qty, price_tolerance_pct: price, price_tolerance_abs_cents: abs };
      if (branch === "") tolerance = next;
      else branchTolerance.set(branch, next);
      json(res, tolView(branch));
      return true;
    }
    if (rest === "policy" && method === "GET") {
      json(res, policyView(url.searchParams.get("branch_id") ?? ""));
      return true;
    }
    if (rest === "policy" && method === "PUT") {
      if (role !== "super-admin") return wall(res);
      const body = await readBody(req);
      if (body === null) return say(res, 400, "invalid_json");
      const vis = String(body.cost_visibility ?? "").trim().toUpperCase();
      if (!["MANAGER_UP", "ALL_STAFF", "HIDDEN"].includes(vis)) return say(res, 400, "cost_visibility must be MANAGER_UP, ALL_STAFF or HIDDEN");
      let fb = String(body.untagged_fallback ?? "").trim().toUpperCase();
      if (fb === "") fb = "CONFIRM";
      if (fb !== "CONFIRM" && fb !== "REFUSE") return say(res, 400, "untagged_fallback must be CONFIRM or REFUSE");
      const branch = String(body.branch_id ?? "");
      if (branch === "") {
        policyStore.vis = vis;
        policyStore.fb = fb;
      } else policyStore.branch.set(branch, { vis, fb });
      const flags = kit.flags();
      flags.costsHidden = vis === "HIDDEN";
      flags.refuseUntagged = fb === "REFUSE";
      json(res, policyView(branch));
      return true;
    }
    return false;
  }

  return { reset, handle };
}
