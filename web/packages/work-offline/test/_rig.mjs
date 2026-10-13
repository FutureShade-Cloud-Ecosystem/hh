// A Work shell rig: an in-memory store over node:sqlite for one desk app, the
// fake sync server behind the Go writable sets, and a clock and key sequence
// that are the test's own.
import { OfflineStore, createSqliteBackend, scopeBookFor } from "@hardscapehouse/offline-store";
import { FakeSyncServer, nodeSqliteExecutor } from "@hardscapehouse/offline-store/testing";
import { strictQuotesTransport } from "../src/testing.ts";

export const uuid = (n) => `00000000-0000-4000-8000-${String(n).padStart(12, "0")}`;
export const BRANCH = "5d1b3f6e-0000-4000-9000-000000000001";
export const CUSTOMER = "c0000000-0000-4000-9000-000000000001";
export const PRODUCT = "a0000000-0000-4000-9000-000000000001";

export async function rig(appId = "quote-desk", { seedWorld = true } = {}) {
  const server = new FakeSyncServer();
  if (seedWorld) {
    server.seed("customers", CUSTOMER, { id: CUSTOMER, account_no: "A-1001", name: "Northline Hardscapes Ltd (demo)", home_branch_id: BRANCH, customer_type: "CONTRACTOR", is_active: true });
    server.seed("customers", uuid(2), { id: uuid(2), account_no: "A-1002", name: "Harbor Pavers (demo)", home_branch_id: BRANCH, customer_type: "RETAIL", is_active: true });
    server.seed("customers", uuid(3), { id: uuid(3), account_no: "A-1003", name: "Closed Account (demo)", home_branch_id: BRANCH, customer_type: "RETAIL", is_active: false });
    server.seed("products", PRODUCT, { id: PRODUCT, sku: "PAVER-CH-1212", name: "Charcoal paver 12 by 12", description: "Concrete paver, charcoal", manufacturer: "Demo Stone", collection: "Classic", sold_by: "EA", base_uom: "EA", is_special_order: false, is_active: true, lead_time_days: null });
    server.seed("products", uuid(11), { id: uuid(11), sku: "SAND-POLY-G", name: "Polymeric sand, grey", description: "18 kg bag", manufacturer: "Demo Stone", collection: "", sold_by: "BAG", base_uom: "BAG", is_special_order: false, is_active: true, lead_time_days: null });
    server.seed("products", uuid(12), { id: uuid(12), sku: "OLD-1", name: "Retired paver", description: "", manufacturer: "", collection: "", sold_by: "EA", base_uom: "EA", is_special_order: false, is_active: false, lead_time_days: null });
    server.seed("price_matrix", PRODUCT, { id: PRODUCT, list_price_cents: 449, price_uom: "EA", updated_at: "2000-01-01T00:00:00Z" });
    server.seed("quotes", uuid(21), { id: uuid(21), quote_no: "#Q-100", customer_id: CUSTOMER, status: "SENT", valid_until: "2999-01-01T00:00:00Z", subtotal_cents: 10000, tax_cents: 800, total_cents: 10800, created_at: "2000-01-01T00:00:00Z" });
    server.seed("parked_tickets", uuid(31), { id: uuid(31), ticket_no: "P-7", branch_id: BRANCH, customer_id: CUSTOMER, sale_type: "COUNTER", notes: "", status: "PARKED", lines: [{ product_id: PRODUCT, qty: "4" }], created_at: "2000-01-01T00:00:00Z", updated_at: "2000-01-01T00:00:00Z" });
  }
  const sql = nodeSqliteExecutor();
  const backend = createSqliteBackend(sql);
  let tick = 0;
  const now = () => `2000-01-01T00:00:${String(++tick % 60).padStart(2, "0")}Z`;
  let keyN = 5000;
  const newKey = () => uuid(++keyN);
  const strict = strictQuotesTransport(server);
  return { server, backend, sql, now, newKey, transport: strict.transport, requests: server.requests, offenders: strict.offenders, appId, scopes: scopeBookFor([appId]), OfflineStore };
}
