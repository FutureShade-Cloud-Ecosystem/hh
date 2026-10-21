// The bank reconciliation slice's stub world: the accounts, the statement lines, the matcher, the match history,
// the rules and the monthly reconciliations, answered the way the core answers them. This is a test double only;
// the real routes are the core's (core/internal/bankrec). Its rules, its refusal order and its sentences are held
// to the contract and to the core's own source by test/bankrec-stub-contract.test.mjs, so it cannot drift in silence.
//
// What it copies from the core, on purpose, quirks included:
// - four lists (accounts, matches, rules, reconciliations) answer a bare array where the contract says a page;
// - the lines list is the contract's cursor page, newest first;
// - a FAILED import answers an error status whose body is the audit envelope, the parser's own sentence in error;
// - opening a month with a statement balance of zero stores the month's line net instead (the core's placeholder);
// - the variance is stored when the month is opened, so it is a snapshot until the month is opened again;
// - signing off checks, in order: found, not already signed off, no unmatched line in the month, zero variance.
//
// The world is invented and small: an operating account and two cards of the demo dealer, seven statement lines
// in the month before today (dates are cut from the host's own day, so no calendar literal is written), three
// received card payments, an open cash lodgement, and the three seeded rules.

export const IDS = {
  controller: "de110000-0000-4000-9000-000000000008",
  payrec: "de110000-0000-4000-9000-000000000007",
  manager: "de110000-0000-4000-9000-000000000002",
  operating: "ba000000-0000-4000-8000-000000000001",
  fuelCard: "ba000000-0000-4000-8000-000000000002",
  suppliesCard: "ba000000-0000-4000-8000-000000000003",
  paymentGateway1: "b9000000-0000-4000-8000-000000000001",
  paymentGateway2: "b9000000-0000-4000-8000-000000000002",
  paymentBatch: "b9000000-0000-4000-8000-000000000003",
  paymentWire: "b9000000-0000-4000-8000-000000000004",
  lodgement1: "b8000000-0000-4000-8000-000000000001",
  ruleGateway: "b4000000-0000-4000-8000-000000000001",
  ruleLodgement: "b4000000-0000-4000-8000-000000000002",
  ruleBatch: "b4000000-0000-4000-8000-000000000003",
};

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const pad = (n, w = 2) => String(n).padStart(w, "0");
const uuid = (kind, n) => `${kind.padEnd(8, "0")}-0000-4000-8000-${String(n).padStart(12, "0")}`;

/** The month before today, from the host's own clock: the month the statement covers (the desk opens the same one). */
export function statementMonth() {
  const before = new Date(new Date().getFullYear(), new Date().getMonth() - 1, 1);
  return { year: before.getFullYear(), month: before.getMonth() + 1 };
}

// The ledger side of the operating account before any match: its opening balance, plus the cheque deposit and the
// bank fee that were already booked by journals. The statement closing balance is the opening balance plus every
// line of the month; once every line is matched the ledger reaches it and the variance is zero.
export const OPENING_CENTS = 1_850_000;
const BOOKED_BEFORE_MATCHING = 64_025 - 4_500;

function fresh() {
  const { year, month } = statementMonth();
  const day = (d) => `${year}-${pad(month)}-${pad(d)}T00:00:00Z`;
  const line = (n, d, description, reference, amount_cents) => ({
    id: uuid("b1", n),
    bank_account_id: IDS.operating,
    import_id: uuid("b2", 1),
    txn_date: day(d),
    description,
    reference,
    amount_cents,
    dedupe_hash: `hash-${n}`,
    status: "UNMATCHED",
    created_at: n,
  });
  return {
    accounts: [
      { id: IDS.operating, name: "Gable operating account (demo)", account_type: "BANK", gl_account_code: "1000", institution: "Harbor Bank (demo)", last4: "4410", is_active: true, tracking_only: false },
      { id: IDS.fuelCard, name: "Fleet fuel card (demo)", account_type: "CREDIT_CARD", gl_account_code: "2110", institution: "Harbor Bank (demo)", last4: "7721", is_active: true, tracking_only: true },
      { id: IDS.suppliesCard, name: "Yard supplies card (demo)", account_type: "CREDIT_CARD", gl_account_code: "2111", institution: "Harbor Bank (demo)", last4: "3308", is_active: true, tracking_only: true },
    ],
    lines: [
      line(1, 3, "CARD SETTLEMENT BATCH 0142", "GTW-2101", 128_000),
      line(2, 5, "CASH LODGEMENT DEPOSIT", "LODGE-310", 245_050),
      line(3, 9, "CARD SETTLEMENT BATCH 0143", "GTW-2102", 98_000),
      line(4, 12, "RUN BATCH SETTLEMENT", "", 115_000),
      line(5, 18, "CHEQUE DEPOSIT", "LODGE-312", 64_025),
      line(6, 21, "MONTHLY SERVICE FEE", "", -4_500),
      line(7, 25, "WIRE FROM CONTRACTOR", "", 30_000),
    ],
    payments: [
      { id: IDS.paymentGateway1, payment_no: "PAY-000101", gateway_ref: "GTW-2101", amount_cents: 128_000, received: day(3), status: "RECEIVED" },
      { id: IDS.paymentGateway2, payment_no: "PAY-000102", gateway_ref: "GTW-2102", amount_cents: 98_000, received: day(9), status: "RECEIVED" },
      { id: IDS.paymentBatch, payment_no: "PAY-000103", gateway_ref: "", amount_cents: 117_500, received: day(11), status: "RECEIVED" },
      { id: IDS.paymentWire, payment_no: "PAY-000104", gateway_ref: "", amount_cents: 30_000, received: day(24), status: "RECEIVED" },
    ],
    lodgements: [{ id: IDS.lodgement1, reference: "LODGE-310", amount_cents: 245_050 }],
    matches: [],
    rules: [
      { id: IDS.ruleGateway, code: "gateway-ref-exact", name: "Card settlement, gateway reference exact", priority: 10, version: 1, status: "APPROVED", spec: { kind: "gateway_ref_exact", target: "PAYMENT", tolerance_cents: 0 } },
      { id: IDS.ruleLodgement, code: "lodgement-ref-exact", name: "Cash or cheque lodgement, reference exact", priority: 20, version: 1, status: "APPROVED", spec: { kind: "lodgement_ref_exact", target: "LODGEMENT", tolerance_cents: 0 } },
      { id: IDS.ruleBatch, code: "run-settlement-fuzzy", name: "Batch settlement, description and date window", priority: 30, version: 1, status: "APPROVED", spec: { kind: "descriptor_fuzzy", target: "PAYMENT", descriptor_ilike: "%RUN%", date_window_days: 3, allow_fee_split: true, fee_account_code: "6300" } },
    ],
    recons: [],
    // The ledger side (account 1000): moved by the clearing journals a match posts and the reversal an undo posts.
    glCents: OPENING_CENTS + BOOKED_BEFORE_MATCHING,
    nextMatch: 1,
    nextRule: 4,
    nextRecon: 1,
    nextImport: 2,
    nextLine: 8,
    tick: 100,
  };
}

const norm = (s) => String(s ?? "").trim().toUpperCase();

function descriptorMatches(desc, pattern) {
  if (!pattern) return false;
  const d = desc.toLowerCase();
  const p = pattern.toLowerCase();
  const pre = p.startsWith("%");
  const suf = p.endsWith("%");
  const core = p.replace(/^%+|%+$/g, "");
  if (pre && suf) return d.includes(core);
  if (suf) return d.startsWith(core);
  if (pre) return d.endsWith(core);
  return d === core;
}

const dayOf = (iso) => Date.parse(`${iso.slice(0, 10)}T00:00:00Z`);
const withinDays = (a, b, days) => Math.abs(dayOf(a) - dayOf(b)) <= days * 86_400_000;

const clientError = (status, message) => ({ status, body: { error: message } });

export function createBankrecWorld() {
  let world = fresh();
  const reset = () => {
    world = fresh();
  };

  const callerOf = (req) => {
    const token = String(req.headers.authorization ?? "").replace(/^Bearer /, "");
    try {
      const sub = JSON.parse(Buffer.from(token.split(".")[1], "base64url").toString("utf8")).sub;
      // The tiers the core's guards read: the controller is tier 1 global (admin), the Payables and Receivables
      // role and the Sales manager are tier 2 (manager), the rest are staff.
      if (sub === IDS.controller) return { id: sub, tier: 1 };
      if (sub === IDS.payrec || sub === IDS.manager) return { id: sub, tier: 2 };
      return { id: sub, tier: 3 };
    } catch {
      return { id: "", tier: 3 };
    }
  };

  const forbidden = () => ({ status: 403, body: { error: "Forbidden" } });

  /** The page the core's lines list writes: items, next_cursor, limit (httpx.WriteCursorPage). */
  const cursorPage = (items, next, limit) => ({ status: 200, body: { items, next_cursor: next, limit } });

  const accountView = (a) => ({ ...a, unmatched_lines: world.lines.filter((l) => l.bank_account_id === a.id && l.status === "UNMATCHED").length });
  const lineView = (l) => {
    const { created_at, ...wire } = l;
    return wire;
  };
  const matchView = (m) => {
    const out = { id: m.id, statement_line_id: m.statement_line_id, target_type: m.target_type };
    if (m.target_ref_id) out.target_ref_id = m.target_ref_id;
    out.method = m.method;
    if (m.rule_id) out.rule_id = m.rule_id;
    if (m.journal_id) out.journal_id = m.journal_id;
    out.amount_cents = m.amount_cents;
    if (m.matched_by) out.matched_by = m.matched_by;
    out.matched_at = m.matched_at;
    if (m.unmatched_at) out.unmatched_at = m.unmatched_at;
    return out;
  };
  const reconView = (r) => {
    const out = {
      id: r.id,
      bank_account_id: r.bank_account_id,
      year: r.year,
      month: r.month,
      statement_balance_cents: r.statement_balance_cents,
      gl_balance_cents: r.gl_balance_cents,
      variance_cents: r.variance_cents,
      status: r.status,
    };
    if (r.completed_at) out.completed_at = r.completed_at;
    return out;
  };
  const ruleView = (r) => {
    const out = { id: r.id, code: r.code, name: r.name, priority: r.priority, version: r.version, status: r.status, spec: r.spec };
    if (r.supersedes_rule_id) out.supersedes_rule_id = r.supersedes_rule_id;
    return out;
  };

  const stamp = () => new Date(Date.now() + world.tick++).toISOString();
  const liveMatch = (lineId) => world.matches.find((m) => m.statement_line_id === lineId && !m.unmatched_at);

  /** The clearing journal a match posts moves the ledger by the deposit; an undo of a posting match moves it back. */
  const postClearing = (cents) => {
    world.glCents += cents;
    return uuid("b6", world.tick++);
  };

  // The CSV the built-in parser reads: the rbc-default and visa-default mapping, a header row required.
  function parseCsv(content, profile) {
    const mapping =
      profile === "visa-default"
        ? { date: "Transaction Date", desc: "Description", amount: "Amount", ref: "Reference" }
        : { date: "Date", desc: "Description", amount: "Amount", ref: "Reference" };
    const rows = [];
    const text = content.replace(/\r\n/g, "\n");
    let i = 0;
    const readRow = () => {
      const row = [];
      let cell = "";
      let quoted = false;
      let any = false;
      for (; i < text.length; i += 1) {
        const c = text[i];
        any = true;
        if (quoted) {
          if (c === '"' && text[i + 1] === '"') {
            cell += '"';
            i += 1;
          } else if (c === '"') quoted = false;
          else cell += c;
        } else if (c === '"') quoted = true;
        else if (c === ",") {
          row.push(cell);
          cell = "";
        } else if (c === "\n") {
          i += 1;
          row.push(cell);
          return row;
        } else cell += c;
      }
      if (!any) return null;
      row.push(cell);
      return row;
    };
    const header = readRow();
    if (header === null) return { error: "bankrec: read csv header: EOF" };
    const idx = new Map(header.map((h, n) => [h.trim(), n]));
    for (const col of [mapping.date, mapping.amount]) {
      if (!idx.has(col)) return { error: `bankrec: csv is missing the mapped column "${col}"` };
    }
    let rowNo = 1;
    for (;;) {
      const rec = readRow();
      if (rec === null) break;
      rowNo += 1;
      if (rec.every((f) => f.trim() === "")) continue;
      const field = (col) => (idx.has(col) ? (rec[idx.get(col)] ?? "").trim() : "");
      const dateText = field(mapping.date);
      if (!/^\d{4}-\d{2}-\d{2}$/.test(dateText) || Number.isNaN(Date.parse(`${dateText}T00:00:00Z`))) {
        return { error: `bankrec: csv row ${rowNo - 1}: bad date "${dateText}" (want "2006-01-02"): parsing time "${dateText}" as "2006-01-02": cannot parse "${dateText}" as "2006"` };
      }
      const amountText = field(mapping.amount);
      const cents = parseCents(amountText);
      if (cents === null) return { error: `bankrec: csv row ${rowNo - 1}: bad amount "${amountText}": not a number: "${amountText}"` };
      rows.push({ date: dateText, description: field(mapping.desc), reference: field(mapping.ref), amount_cents: cents });
    }
    return { rows };
  }

  /** The core's parseCents, on strings: "$1,234.56", "(45.00)", "-12.30", "1234". Null when it is not a number. */
  function parseCents(text) {
    let s = text.trim();
    if (s === "") return 0;
    let neg = false;
    if (s.startsWith("(") && s.endsWith(")")) {
      neg = true;
      s = s.slice(1, -1);
    }
    s = s.replace(/[$, ]/g, "");
    if (s.startsWith("-")) {
      neg = true;
      s = s.slice(1);
    }
    if (s.startsWith("+")) s = s.slice(1);
    if (s === "") return 0;
    const m = /^(\d*)(?:\.(\d*))?$/.exec(s);
    if (m === null || (m[1] === "" && (m[2] ?? "") === "")) return null;
    const cents = Number(`${m[1] || "0"}${(m[2] ?? "").slice(0, 2).padEnd(2, "0")}`);
    return neg ? -cents : cents;
  }

  function importStatement(input, caller) {
    if (!input.bank_account_id?.trim()) return clientError(400, "bank_account_id is required");
    if (!input.content?.trim()) return clientError(400, "content is required");
    const account = world.accounts.find((a) => a.id === input.bank_account_id);
    if (account === undefined) return clientError(404, "not found");
    const profile = input.profile_code || (account.account_type === "CREDIT_CARD" ? "visa-default" : "rbc-default");
    if (profile !== "rbc-default" && profile !== "visa-default") return clientError(404, "not found");
    const parsed = parseCsv(input.content, profile);
    const importId = uuid("b2", world.nextImport++);
    if (parsed.error !== undefined) {
      // The envelope rides the error status; its error is the parser's own sentence, module prefix and all.
      return { status: 422, body: { import_id: importId, status: "FAILED", parsed: 0, inserted: 0, duplicates: 0, error: parsed.error } };
    }
    let inserted = 0;
    for (const r of parsed.rows) {
      const hash = `${r.date}|${r.amount_cents}|${r.description.trim().toLowerCase()}|${r.reference.trim().toLowerCase()}`;
      if (world.lines.some((l) => l.bank_account_id === account.id && l.dedupe_hash === hash)) continue;
      world.lines.push({
        id: uuid("b1", world.nextLine++),
        bank_account_id: account.id,
        import_id: importId,
        txn_date: `${r.date}T00:00:00Z`,
        description: r.description,
        reference: r.reference,
        amount_cents: r.amount_cents,
        dedupe_hash: hash,
        status: "UNMATCHED",
        created_at: world.tick++,
      });
      inserted += 1;
    }
    return { status: 201, body: { import_id: importId, status: "IMPORTED", parsed: parsed.rows.length, inserted, duplicates: parsed.rows.length - inserted } };
  }

  function listLines(url) {
    const account = url.searchParams.get("bank_account_id") ?? "";
    if (account === "") return clientError(400, "bank_account_id is required");
    const status = url.searchParams.get("status") ?? "";
    const limit = Number(url.searchParams.get("limit") ?? 50) || 50;
    const cursor = url.searchParams.get("cursor") ?? "";
    if (cursor !== "" && !/^\d{4}-\d{2}-\d{2}T[^|]*\|[^|]*\|[0-9a-f-]{36}$/i.test(cursor)) return clientError(400, "cursor is malformed");
    // txn_date DESC, created_at DESC, id DESC: the list's stable order; the cursor is that triple.
    const sorted = world.lines
      .filter((l) => l.bank_account_id === account && (status === "" || l.status === status))
      .sort((a, b) => (a.txn_date === b.txn_date ? (a.created_at === b.created_at ? (a.id < b.id ? 1 : -1) : b.created_at - a.created_at) : a.txn_date < b.txn_date ? 1 : -1));
    const key = (l) => `${l.txn_date}|${l.created_at}|${l.id}`;
    const from = cursor === "" ? 0 : sorted.findIndex((l) => key(l) === cursor) + 1;
    const items = sorted.slice(from, from + limit);
    const more = from + limit < sorted.length;
    return cursorPage(items.map(lineView), more ? key(items[items.length - 1]) : "", limit);
  }

  function candidateWindow(input) {
    const parse = (s) => (/^\d{4}-\d{2}-\d{2}$/.test(s) ? Date.parse(`${s}T00:00:00Z`) : Date.parse(s));
    const from = input.from ? parse(input.from) : Date.UTC(2000, 0, 1);
    const to = input.to ? parse(input.to) : Date.now() + 366 * 86_400_000;
    return Number.isNaN(from) ? { error: `bad 'from' date "${input.from}"` } : Number.isNaN(to) ? { error: `bad 'to' date "${input.to}"` } : { from, to };
  }

  function autoMatch(input, caller) {
    if (!input.bank_account_id) return clientError(400, "bank_account_id is required");
    const window = candidateWindow(input);
    if (window.error !== undefined) return clientError(400, window.error);
    const lines = world.lines
      .filter((l) => l.bank_account_id === input.bank_account_id && l.status === "UNMATCHED" && l.amount_cents > 0 && dayOf(l.txn_date) >= window.from && dayOf(l.txn_date) < window.to)
      .sort((a, b) => (a.txn_date < b.txn_date ? -1 : a.txn_date > b.txn_date ? 1 : a.created_at - b.created_at));
    const rules = world.rules.filter((r) => r.status === "APPROVED").sort((a, b) => a.priority - b.priority);
    let payments = world.payments.filter((p) => p.status === "RECEIVED");
    let lodgements = [...world.lodgements];
    const made = [];
    let unmatched = 0;
    for (const line of lines) {
      let found = null;
      for (const rule of rules) {
        const kind = rule.spec.kind;
        const tolerance = Number(rule.spec.tolerance_cents ?? 0);
        if (kind === "gateway_ref_exact" && norm(line.reference) !== "") {
          const p = payments.find((x) => norm(x.gateway_ref) === norm(line.reference) && Math.abs(x.amount_cents - line.amount_cents) <= tolerance);
          if (p !== undefined) found = { rule, type: "PAYMENT", ref: p.id, payment: p };
        } else if (kind === "lodgement_ref_exact" && norm(line.reference) !== "") {
          const l = lodgements.find((x) => norm(x.reference) === norm(line.reference) && Math.abs(x.amount_cents - line.amount_cents) <= tolerance);
          if (l !== undefined) found = { rule, type: "LODGEMENT", ref: l.id, lodgement: l };
        } else if (kind === "descriptor_fuzzy" && descriptorMatches(line.description, String(rule.spec.descriptor_ilike ?? ""))) {
          let best = null;
          let bestFee = -1;
          for (const p of payments) {
            if (!withinDays(line.txn_date, p.received, Number(rule.spec.date_window_days ?? 0))) continue;
            const fee = p.amount_cents - line.amount_cents;
            if (fee < 0) continue;
            if (rule.spec.allow_fee_split !== true && fee !== 0) continue;
            if (bestFee === -1 || fee < bestFee) {
              best = p;
              bestFee = fee;
            }
          }
          if (best !== null) found = { rule, type: "PAYMENT", ref: best.id, payment: best };
        }
        if (found !== null) break;
      }
      if (found === null) {
        unmatched += 1;
        continue;
      }
      line.status = "MATCHED";
      if (found.payment) found.payment.status = "DEPOSITED";
      const match = {
        id: uuid("b3", world.nextMatch++),
        statement_line_id: line.id,
        target_type: found.type,
        target_ref_id: found.ref,
        method: "AUTO",
        rule_id: found.rule.id,
        journal_id: postClearing(line.amount_cents),
        amount_cents: line.amount_cents,
        matched_by: caller.id,
        matched_at: stamp(),
      };
      world.matches.push(match);
      made.push(matchView(match));
      payments = payments.filter((p) => p.id !== found.payment?.id);
      lodgements = lodgements.filter((l) => l.id !== found.lodgement?.id);
    }
    return { status: 200, body: { matched: made.length, unmatched, matches: made } };
  }

  function manualMatch(input, caller) {
    if (!input.statement_line_id) return clientError(400, "statement_line_id is required");
    if (!["PAYMENT", "LODGEMENT", "JOURNAL", "GROUP"].includes(input.target_type)) return clientError(400, "target_type must be PAYMENT, LODGEMENT, JOURNAL or GROUP");
    if (!UUID.test(input.statement_line_id)) return clientError(404, "not found");
    const line = world.lines.find((l) => l.id === input.statement_line_id);
    if (line === undefined) return clientError(404, "not found");
    if (line.status !== "UNMATCHED") return clientError(409, `statement line ${line.id} is ${line.status}, not UNMATCHED`);
    const amount = input.amount_cents ? input.amount_cents : line.amount_cents;
    // A LODGEMENT target must exist (SF-2g round 2); an unknown one, like one the caller's branch does not reach,
    // answers as a missing target, with nothing written.
    if (input.target_type === "LODGEMENT" && !world.lodgements.some((l) => l.id === input.target_ref_id)) return clientError(404, "not found");
    // A PAYMENT target is claimed in the same step (received to deposited); the others claim nothing.
    if (input.target_type === "PAYMENT" && input.target_ref_id) {
      const payment = world.payments.find((p) => p.id === input.target_ref_id && p.status === "RECEIVED");
      // The core names the payment by its number (read through the payments module), never its id; a payment with
      // no row reads as plain words.
      if (payment === undefined) {
        const known = world.payments.find((p) => p.id === input.target_ref_id);
        return clientError(409, `payment already claimed, reversed or not RECEIVED: ${known === undefined ? "a payment that cannot be found" : known.payment_no} (claimed 0 of 1)`);
      }
      payment.status = "DEPOSITED";
    }
    line.status = "MATCHED";
    const posts = input.target_type === "PAYMENT" || input.target_type === "LODGEMENT";
    const match = {
      id: uuid("b3", world.nextMatch++),
      statement_line_id: line.id,
      target_type: input.target_type,
      target_ref_id: input.target_ref_id ?? "",
      method: "MANUAL",
      rule_id: "",
      journal_id: posts ? postClearing(line.amount_cents) : "",
      amount_cents: amount,
      matched_by: caller.id,
      matched_at: stamp(),
    };
    world.matches.push(match);
    return { status: 201, body: matchView(match) };
  }

  function unmatch(input, caller) {
    if (!input.match_id) return clientError(400, "match_id is required");
    if (!UUID.test(input.match_id)) return clientError(409, `match ${input.match_id} is not live (already unmatched)`);
    const match = world.matches.find((m) => m.id === input.match_id && !m.unmatched_at);
    if (match === undefined) return clientError(409, `match ${input.match_id} is not live (already unmatched)`);
    // A deposit whose payment was reversed cannot be unmatched: the reversal already took the money back out.
    if (match.target_type === "PAYMENT" && world.payments.some((p) => p.id === match.target_ref_id && p.reversed)) {
      return clientError(409, "bankrec: this deposit cannot be unmatched: the payment was reversed, so its money already left through the reversal");
    }
    match.unmatched_at = stamp();
    const line = world.lines.find((l) => l.id === match.statement_line_id);
    if (line !== undefined && line.status === "MATCHED") line.status = "UNMATCHED";
    if (match.target_type === "PAYMENT" && match.target_ref_id) {
      const payment = world.payments.find((p) => p.id === match.target_ref_id && p.status === "DEPOSITED");
      if (payment !== undefined) payment.status = "RECEIVED";
    }
    // A payment or lodgement match posted a clearing journal; the undo posts the reversal.
    if ((match.target_type === "PAYMENT" || match.target_type === "LODGEMENT") && match.amount_cents > 0) world.glCents -= match.amount_cents;
    return { status: 200, body: { status: "unmatched" } };
  }

  function createRule(input) {
    if (!input.code?.trim()) return clientError(400, "rule code is required");
    if (!input.spec || Object.keys(input.spec).length === 0) return clientError(400, "rule spec is required");
    if (!["gateway_ref_exact", "lodgement_ref_exact", "descriptor_fuzzy"].includes(input.spec.kind)) {
      return clientError(422, "spec.kind must be one of gateway_ref_exact, lodgement_ref_exact, descriptor_fuzzy");
    }
    const old = world.rules.find((r) => r.code === input.code && r.status === "APPROVED");
    if (old !== undefined) old.status = "RETIRED";
    const rule = {
      id: uuid("b4", world.nextRule++),
      code: input.code,
      name: input.name,
      priority: input.priority ? input.priority : 100,
      version: (old?.version ?? 0) + 1,
      status: "APPROVED",
      spec: input.spec,
      ...(old ? { supersedes_rule_id: old.id } : {}),
    };
    world.rules.push(rule);
    return { status: 201, body: ruleView(rule) };
  }

  function monthNet(accountId, year, month) {
    let net = 0;
    for (const l of world.lines) {
      if (l.bank_account_id !== accountId) continue;
      if (Number(l.txn_date.slice(0, 4)) === year && Number(l.txn_date.slice(5, 7)) === month) net += l.amount_cents;
    }
    return net;
  }

  function openReconciliation(input) {
    if (!input.bank_account_id) return clientError(400, "bank_account_id is required");
    if (!(input.month >= 1 && input.month <= 12) || !(input.year >= 2000)) return clientError(400, "valid year and month (1-12) are required");
    const account = world.accounts.find((a) => a.id === input.bank_account_id);
    if (account === undefined) return clientError(404, "not found");
    // The core stores the month's line net when the typed balance is zero: its placeholder default.
    const statement = input.statement_balance_cents ? input.statement_balance_cents : monthNet(account.id, input.year, input.month);
    // Only the operating account has a ledger balance in this world; the cards are tracking only.
    const gl = account.gl_account_code === "1000" ? world.glCents : 0;
    const existing = world.recons.find((r) => r.bank_account_id === account.id && r.year === input.year && r.month === input.month);
    if (existing !== undefined && existing.status === "COMPLETED") {
      return clientError(409, `reconciliation for ${input.year}-${pad(input.month)} is already COMPLETED`);
    }
    const target = existing ?? { id: uuid("b5", world.nextRecon++), bank_account_id: account.id, year: input.year, month: input.month, status: "OPEN" };
    target.statement_balance_cents = statement;
    target.gl_balance_cents = gl;
    target.variance_cents = statement - gl;
    if (existing === undefined) world.recons.push(target);
    return { status: 201, body: reconView(target) };
  }

  function completeReconciliation(id, caller) {
    if (!id) return clientError(400, "reconciliation id is required");
    if (!UUID.test(id)) return clientError(404, "not found");
    const rec = world.recons.find((r) => r.id === id);
    if (rec === undefined) return clientError(404, "not found");
    if (rec.status === "COMPLETED") return clientError(409, `reconciliation ${id} is already COMPLETED`);
    const unmatched = world.lines.filter(
      (l) => l.bank_account_id === rec.bank_account_id && l.status === "UNMATCHED" && Number(l.txn_date.slice(0, 4)) === rec.year && Number(l.txn_date.slice(5, 7)) === rec.month,
    ).length;
    if (unmatched > 0) return clientError(422, `cannot complete, ${unmatched} UNMATCHED line(s) remain for ${rec.year}-${pad(rec.month)}`);
    if (rec.variance_cents !== 0) return clientError(422, `cannot complete, variance is ${rec.variance_cents} cents (must be zero)`);
    rec.status = "COMPLETED";
    rec.completed_at = stamp();
    return { status: 200, body: reconView(rec) };
  }

  /**
   * Answer one request or return null when it is not this world's. The shape is { status, body }, the body
   * already a JSON value.
   */
  function handle(req, url, bodyText) {
    const path = url.pathname;
    const method = req.method;
    if (method === "POST" && path === "/_stub/reset") {
      reset();
      return { status: 200, body: { ok: true } };
    }
    // A control the contract test uses to reverse a payment the way the payments module would (reversed_at set).
    if (method === "POST" && path === "/_stub/reverse-payment") {
      const id = JSON.parse(bodyText || "{}").payment_id;
      const payment = world.payments.find((p) => p.id === id);
      if (payment === undefined) return { status: 404, body: { error: "not found" } };
      payment.reversed = true;
      return { status: 200, body: { ok: true } };
    }
    if (!path.startsWith("/api/v1/bankrec/")) return null;
    const caller = callerOf(req);
    const route = `${method} ${path.replace(/\/[0-9a-f-]{36}\/complete$/i, "/{id}/complete")}`;
    // The guards, per route: reads any staff, writes a manager (tier 1 or 2), rules and sign off an administrator.
    const admin = route === "POST /api/v1/bankrec/rules" || route === "POST /api/v1/bankrec/reconciliations/{id}/complete";
    const manager = admin || ["POST /api/v1/bankrec/imports", "POST /api/v1/bankrec/match", "POST /api/v1/bankrec/unmatch", "POST /api/v1/bankrec/reconciliations"].includes(route);
    if (admin && caller.tier !== 1) return forbidden();
    if (manager && caller.tier > 2) return forbidden();
    let input = {};
    if (method === "POST" && route !== "POST /api/v1/bankrec/reconciliations/{id}/complete") {
      try {
        input = JSON.parse(bodyText);
      } catch {
        return clientError(400, "invalid_json");
      }
    }
    switch (route) {
      case "GET /api/v1/bankrec/accounts":
        return { status: 200, body: world.accounts.map(accountView) };
      case "POST /api/v1/bankrec/imports":
        return importStatement(input, caller);
      case "GET /api/v1/bankrec/lines":
        return listLines(url);
      case "POST /api/v1/bankrec/match":
        if (input.mode === "manual") return manualMatch(input, caller);
        if (input.mode === "auto" || input.mode === undefined || input.mode === "") return autoMatch(input, caller);
        return clientError(400, "mode must be 'auto' or 'manual'");
      case "POST /api/v1/bankrec/unmatch":
        return unmatch(input, caller);
      case "GET /api/v1/bankrec/matches": {
        const lineId = url.searchParams.get("statement_line_id") ?? "";
        if (lineId === "") return clientError(400, "statement_line_id is required");
        const rows = world.matches.filter((m) => m.statement_line_id === lineId).sort((a, b) => (a.matched_at < b.matched_at ? 1 : -1));
        return { status: 200, body: rows.map(matchView) };
      }
      case "GET /api/v1/bankrec/rules": {
        const live = url.searchParams.get("live") === "true";
        return { status: 200, body: world.rules.filter((r) => !live || r.status === "APPROVED").map(ruleView) };
      }
      case "POST /api/v1/bankrec/rules":
        return createRule(input);
      case "GET /api/v1/bankrec/reconciliations": {
        const account = url.searchParams.get("bank_account_id") ?? "";
        const rows = world.recons.filter((r) => account === "" || r.bank_account_id === account).sort((a, b) => b.year - a.year || b.month - a.month);
        return { status: 200, body: rows.map(reconView) };
      }
      case "POST /api/v1/bankrec/reconciliations":
        return openReconciliation(input);
      case "POST /api/v1/bankrec/reconciliations/{id}/complete":
        return completeReconciliation(path.split("/").at(-2), caller);
      default:
        return null;
    }
  }

  return { handle, reset };
}
