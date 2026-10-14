// The Counter Till stub's sync surface: the offline store's pull routes (GET
// /api/v1/sync/{scope}) over the stub's own world, for the scopes the till's
// manifest block declares (customers, products, price_matrix, parked_tickets,
// invoices, warranty_claims). The till is read cache only (plan D5, manifest
// mode read_cache_only): it has NO outbox, so every POST is refused as the
// engine refuses it (a money scope 403 for every caller, a pull only scope
// 400 "is pull-only"), and every refused push is counted so a leg can prove
// the till made none.
//
// Seqs are derived: a scope's snapshot is compared with the last one served,
// and a row whose projection changed draws the next seq.
const T0 = "2000-01-01T00:00:00Z";
const KNOWN = new Set(["customers", "products", "price_matrix", "parked_tickets", "invoices", "warranty_claims"]);
// The registry rows the till's scopes lean on (core/internal/sync/registry.go): invoices and price_matrix are money.
const MONEY = new Set(["invoices", "price_matrix"]);

export function createSyncStub({ products, customers, parked, homeBranchId }) {
  let seqCounter = 0;
  const seen = new Map();
  const log = { pulls: [], pushes: [] };

  function projection(scope) {
    if (scope === "products") {
      return products().map((p) => ({
        id: p.product_id, sku: p.sku, name: p.name, description: p.description ?? "", manufacturer: "", collection: "",
        sold_by: p.sold_by, base_uom: p.base_uom, is_special_order: false, is_active: true, lead_time_days: null, created_at: T0, updated_at: T0,
      }));
    }
    if (scope === "price_matrix") return products().map((p) => ({ id: p.product_id, list_price_cents: p.list_cents, price_uom: p.sold_by, updated_at: T0 }));
    if (scope === "customers") {
      return customers().map((c) => ({
        id: c.id, account_no: c.account_no, name: c.name, home_branch_id: homeBranchId, customer_type: c.customer_type, is_active: true, created_at: T0, updated_at: T0,
      }));
    }
    if (scope === "parked_tickets") {
      return [...parked().values()].map((t) => ({
        id: t.id, ticket_no: t.ticket_no, branch_id: t.branch_id, customer_id: t.customer_id ?? "", sale_type: t.sale_type, notes: t.notes ?? "", status: t.status,
        lines: t.lines, created_at: t.created_at, updated_at: t.created_at,
      }));
    }
    return [];
  }

  function snapshot(scope) {
    if (!seen.has(scope)) seen.set(scope, new Map());
    const t = seen.get(scope);
    const live = new Map(projection(scope).map((row) => [row.id, row]));
    for (const [id, row] of live) {
      const json = JSON.stringify(row);
      const prior = t.get(id);
      if (prior === undefined || prior.json !== json) t.set(id, { json, seq: ++seqCounter, row });
    }
    const tombstones = [];
    for (const id of [...t.keys()]) if (!live.has(id)) tombstones.push({ id, seq: ++seqCounter }), t.delete(id);
    return { t, tombstones };
  }

  function handle({ request, response, path, url, respondJson }) {
    const match = /^\/api\/v1\/sync\/([^/]+)$/.exec(path);
    if (match === null) return false;
    const scope = match[1];
    if (!KNOWN.has(scope)) {
      respondJson(response, 400, { error: "unknown sync scope" });
      return true;
    }
    if (request.method === "GET") {
      const since = Number(url.searchParams.get("since") ?? "0");
      const limit = Math.min(Math.max(Number(url.searchParams.get("limit") ?? "50") || 50, 1), 200);
      const { t, tombstones } = snapshot(scope);
      const rows = [
        ...[...t.entries()].map(([id, e]) => ({ kind: "item", id, seq: e.seq, data: e.row })),
        ...tombstones.map((x) => ({ kind: "tomb", ...x })),
      ]
        .filter((r) => r.seq > since)
        .sort((a, b) => a.seq - b.seq)
        .slice(0, limit);
      const last = rows.length > 0 ? rows[rows.length - 1].seq : since;
      log.pulls.push({ scope, since });
      respondJson(response, 200, {
        scope,
        items: rows.filter((r) => r.kind === "item").map((r) => ({ id: r.id, seq: r.seq, data: r.data })),
        tombstones: rows.filter((r) => r.kind === "tomb").map((r) => ({ id: r.id, seq: r.seq })),
        last_seq: last,
        next_cursor: rows.length === limit ? String(last) : null,
      });
      return true;
    }
    if (request.method === "POST") {
      const status = MONEY.has(scope) ? 403 : 400;
      log.pushes.push({ scope, status });
      respondJson(response, status, { error: MONEY.has(scope) ? "forbidden" : `sync: scope ${scope} is pull-only` });
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
      log.pulls.length = 0;
      log.pushes.length = 0;
    },
  };
}
