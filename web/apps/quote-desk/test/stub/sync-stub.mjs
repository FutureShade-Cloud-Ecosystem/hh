// The Quote Desk stub's sync surface: the offline store's pull and push routes
// (GET and POST /api/v1/sync/{scope}) over the stub's own quote world, in the
// engine's order of decisions (core/internal/sync/engine.go): shape, then
// dedup on the key, then the operation under the scope's conflict policy.
//
// The quotes store's rules are the core's, and the admitted field sets are READ
// from core/internal/quote/syncscope.go at start, not copied, so the stub
// cannot drift from the server again (the Field offline review's lesson):
//   create  id, customer_id, branch_id, valid_until   a DRAFT, the caller's own
//   update  id, valid_until                           DRAFT or SENT only
//   delete  id                                        a DRAFT only
// Everything else (status, the number, every money leg) is the server's.
//
// Seqs are derived: a scope's snapshot is compared with the last one served,
// and a row whose projection changed draws the next seq, so a quote edited
// through the desk's own routes reaches a device's pull like any other change.
import { goFieldSet } from "@hardscapehouse/work-offline/gosets";

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

const KNOWN_SCOPES = new Set(["products", "price_matrix", "customers", "quotes", "parked_tickets", "invoices", "warranty_claims", "approvals", "pick_tasks", "shortages"]);
const MONEY_SCOPES = new Set(["price_matrix", "approvals", "invoices", "payments", "payment_methods", "aging"]);

export function createSyncStub({ products, customers, quotes, customerById, branchIds, makeQuote, viewable, now = () => new Date().toISOString() }) {
  const createFields = goFieldSet("quote/syncscope.go", "quoteCreateFields");
  const updateFields = goFieldSet("quote/syncscope.go", "quoteWriteFields");
  const T0 = "2000-01-01T00:00:00Z";

  let seqCounter = 0;
  /** scope -> id -> { json, seq } */
  const seen = new Map();
  /** scope -> id -> seq of the tombstone */
  const tombs = new Map();
  /** client_id -> the stored answer */
  const applied = new Map();
  const log = { pulls: [], pushes: [], refusals: [] };

  const table = (scope) => {
    if (!seen.has(scope)) seen.set(scope, new Map());
    return seen.get(scope);
  };
  const tombTable = (scope) => {
    if (!tombs.has(scope)) tombs.set(scope, new Map());
    return tombs.get(scope);
  };

  function projection(scope, caller) {
    if (scope === "products") {
      return products().map((p) => ({
        id: p.product_id, sku: p.sku, name: p.name, description: p.description ?? "", manufacturer: "", collection: "",
        sold_by: p.sold_by, base_uom: p.base_uom, is_special_order: false, is_active: true, lead_time_days: null, created_at: T0, updated_at: T0,
      }));
    }
    if (scope === "price_matrix") {
      return products().map((p) => ({ id: p.product_id, list_price_cents: p.list_cents, price_uom: p.sold_by, updated_at: T0 }));
    }
    if (scope === "customers") {
      return customers().map((c) => ({
        id: c.id, account_no: c.account_no, name: c.name, home_branch_id: [...branchIds][0], customer_type: c.customer_type, is_active: true, created_at: T0, updated_at: T0,
      }));
    }
    if (scope === "quotes") {
      return [...quotes.values()]
        .filter((quote) => viewable(caller, quote))
        .map((q) => ({
          id: q.id, quote_no: q.quote_no, customer_id: q.customer_id, status: q.status, valid_until: q.valid_until === "" ? null : q.valid_until,
          subtotal_cents: q.subtotal_cents, tax_cents: q.tax_cents, total_cents: q.total_cents, created_at: q.created_at,
        }));
    }
    return [];
  }

  /** Bring a scope's seqs up to date with the world, and return the live rows. */
  function snapshot(scope, caller) {
    const t = table(scope);
    const live = new Map(projection(scope, caller).map((row) => [row.id, row]));
    for (const [id, row] of live) {
      const json = JSON.stringify(row);
      const prior = t.get(id);
      if (prior === undefined || prior.json !== json) t.set(id, { json, seq: ++seqCounter, row });
    }
    for (const id of [...t.keys()]) {
      if (!live.has(id)) {
        t.delete(id);
        tombTable(scope).set(id, ++seqCounter);
      }
    }
    return t;
  }

  function pull(scope, url, caller) {
    const since = Number(url.searchParams.get("since") ?? "0");
    const limit = Math.min(Math.max(Number(url.searchParams.get("limit") ?? "50") || 50, 1), 200);
    const t = snapshot(scope, caller);
    const rows = [
      ...[...t.entries()].map(([id, e]) => ({ kind: "item", id, seq: e.seq, data: e.row })),
      ...[...tombTable(scope).entries()].map(([id, seq]) => ({ kind: "tomb", id, seq })),
    ]
      .filter((r) => r.seq > since)
      .sort((a, b) => a.seq - b.seq)
      .slice(0, limit);
    const last = rows.length > 0 ? rows[rows.length - 1].seq : since;
    log.pulls.push({ scope, since });
    return {
      scope,
      items: rows.filter((r) => r.kind === "item").map((r) => ({ id: r.id, seq: r.seq, data: r.data })),
      tombstones: rows.filter((r) => r.kind === "tomb").map((r) => ({ id: r.id, seq: r.seq })),
      last_seq: last,
      next_cursor: rows.length === limit ? String(last) : null,
    };
  }

  const rejected = (client_id, error) => {
    log.refusals.push(error);
    return { client_id, status: "rejected", error };
  };

  /** The module's refusal wording: the core's own phrases. */
  const notWritable = (k) => `quote: field ${JSON.stringify(k)} is not writable through sync`;

  function validity(raw) {
    const text = String(raw).trim();
    if (text === "") return { ok: true, value: "" };
    if (/^\d{4}-\d{2}-\d{2}$/.test(text)) {
      const d = new Date(`${text}T00:00:00Z`);
      if (!Number.isNaN(d.getTime()) && d.toISOString().slice(0, 10) === text) return { ok: true, value: text };
    } else {
      const d = new Date(text);
      if (/^\d{4}-\d{2}-\d{2}T/.test(text) && !Number.isNaN(d.getTime())) return { ok: true, value: d.toISOString().slice(0, 10) };
    }
    return { ok: false };
  }

  function pushQuote(op, caller) {
    const payload = op.payload ?? {};
    const seqOf = (id) => {
      const q = quotes.get(id);
      if (q === undefined) return 0;
      snapshot("quotes", caller);
      return table("quotes").get(id)?.seq ?? 0;
    };
    if (op.op === "create") {
      for (const k of Object.keys(payload)) if (!createFields.has(k)) return rejected(op.client_id, notWritable(k));
      if (typeof payload.customer_id !== "string" || !UUID.test(payload.customer_id)) return rejected(op.client_id, "quote: customer_id must be a uuid");
      if (typeof payload.branch_id !== "string" || !UUID.test(payload.branch_id)) return rejected(op.client_id, "quote: branch_id must be a uuid");
      if (!branchIds.has(payload.branch_id)) return rejected(op.client_id, "row not found");
      if (!customerById.has(payload.customer_id)) return rejected(op.client_id, "quote: no customer with that id");
      let validUntil = "";
      if ("valid_until" in payload && payload.valid_until !== null) {
        const v = validity(payload.valid_until);
        if (!v.ok) return rejected(op.client_id, "quote: valid_until must be YYYY-MM-DD");
        validUntil = v.value;
      }
      const id = typeof payload.id === "string" ? payload.id : "";
      if (id !== "" && !UUID.test(id)) return rejected(op.client_id, "quote: payload id must be a uuid");
      if (id !== "" && quotes.has(id)) return rejected(op.client_id, "row exists");
      const made = makeQuote({ id: id === "" ? undefined : id, customerId: payload.customer_id, caller, validUntil });
      snapshot("quotes", caller);
      const result = { client_id: op.client_id, status: "applied", seq: table("quotes").get(made.id).seq };
      applied.set(op.client_id, { ...result, id: made.id });
      return result;
    }
    const id = payload.id;
    const q = quotes.get(id);
    if (q === undefined) return rejected(op.client_id, "row not found");
    if (op.op === "update") {
      for (const k of Object.keys(payload)) if (!updateFields.has(k)) return rejected(op.client_id, notWritable(k));
      if (!("valid_until" in payload)) return rejected(op.client_id, "quote: an update that moves nothing refuses");
      const v = validity(payload.valid_until);
      if (!v.ok) return rejected(op.client_id, "quote: valid_until must be YYYY-MM-DD");
      if (q.status !== "DRAFT" && q.status !== "SENT") return rejected(op.client_id, `quote: the quote is ${q.status} and its expiry date is frozen`);
      const current = seqOf(id);
      const stale = typeof op.base_seq === "number" && current > op.base_seq;
      q.valid_until = v.value;
      const seq = seqOf(id);
      const result = { client_id: op.client_id, status: "applied", seq };
      if (stale) {
        result.conflict = { client_id: op.client_id, policy: "last_write_wins_by_field", server_state: table("quotes").get(id).row, applied: true };
      }
      applied.set(op.client_id, { client_id: op.client_id, status: "applied", seq, id });
      return result;
    }
    if (op.op === "delete") {
      if (q.status !== "DRAFT") return rejected(op.client_id, `quote: a ${q.status} quote is a record, withdrawal is the desk's own path`);
      quotes.delete(id);
      snapshot("quotes", caller);
      const seq = tombTable("quotes").get(id);
      applied.set(op.client_id, { client_id: op.client_id, status: "applied", seq, id });
      return { client_id: op.client_id, status: "applied", seq };
    }
    return rejected(op.client_id, "op must be create, update or delete");
  }

  /** Handle a request; true when it was a sync route. */
  function handle({ request, response, path, url, caller, readBody, respondJson }) {
    const match = /^\/api\/v1\/sync\/([^/]+)$/.exec(path);
    if (match === null) return false;
    const scope = match[1];
    if (!KNOWN_SCOPES.has(scope)) {
      respondJson(response, 400, { error: "unknown sync scope" });
      return true;
    }
    if (request.method === "GET") {
      if (!["products", "price_matrix", "customers", "quotes"].includes(scope)) {
        respondJson(response, 200, { scope, items: [], tombstones: [], last_seq: Number(url.searchParams.get("since") ?? "0"), next_cursor: null });
        return true;
      }
      respondJson(response, 200, pull(scope, url, caller));
      return true;
    }
    if (request.method === "POST") {
      if (MONEY_SCOPES.has(scope)) {
        respondJson(response, 403, { error: "forbidden" });
        return true;
      }
      if (scope !== "quotes") {
        respondJson(response, 400, { error: `sync: scope ${scope} is pull-only` });
        return true;
      }
      readBody(request, response, (body) => {
        const ops = Array.isArray(body.ops) ? body.ops : [];
        const results = [];
        for (const op of ops) {
          log.pushes.push({ scope, op: op.op, client_id: op.client_id, fields: Object.keys(op.payload ?? {}) });
          if (typeof op.client_id !== "string" || !UUID.test(op.client_id)) {
            results.push(rejected(String(op.client_id), "client_id must be a uuid"));
            continue;
          }
          if (!["create", "update", "delete"].includes(op.op)) {
            results.push(rejected(op.client_id, "op must be create, update or delete"));
            continue;
          }
          if (op.op !== "create" && !(typeof op.payload?.id === "string" && UUID.test(op.payload.id))) {
            results.push(rejected(op.client_id, "payload id must be a uuid"));
            continue;
          }
          const done = applied.get(op.client_id);
          if (done !== undefined) {
            results.push({ client_id: op.client_id, status: "applied", seq: done.seq });
            continue;
          }
          results.push(pushQuote(op, caller));
        }
        respondJson(response, 200, { results });
      });
      return true;
    }
    respondJson(response, 405, { error: "method not allowed" });
    return true;
  }

  return {
    handle,
    log,
    reset() {
      seen.clear();
      tombs.clear();
      applied.clear();
      log.pulls.length = 0;
      log.pushes.length = 0;
      log.refusals.length = 0;
    },
  };
}
