// The read cache of the desk apps. The Work shell pulls the scopes the
// manifest declares for an app into the offline store, and the app reads the
// catalogue, customers, the price band, parked tickets and open documents
// from it when the network does not answer, with how stale the answer is.
//
// What this module cannot do is write: a reader only reads, and the store it
// opens carries a write fence of its own, from the manifest. An app whose
// offline mode is none (Money Desk, Dealer Admin) cannot open a store at all;
// an app whose mode is read cache only (the Counter Till, Manager Home) opens
// one whose scope book admits no write, so there is no outbox to enqueue on.
import {
  MANIFEST_OFFLINE,
  OfflineStore,
  scopeBookFor,
  type Staleness,
  type StorageBackend,
  type StoredRow,
} from "@hardscapehouse/offline-store";

/** An app that may not hold an offline store, or that the manifest does not know. */
export class WorkModeError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "WorkModeError";
  }
}

export interface WorkStoreOptions {
  appId: string;
  backend: StorageBackend;
  now?: () => string;
  newKey?: () => string;
}

/** Open the store for one desk app, its scopes and its write fence from the manifest. */
export async function openWorkStore(options: WorkStoreOptions): Promise<OfflineStore> {
  const app = MANIFEST_OFFLINE.find((a) => a.id === options.appId);
  if (!app) throw new WorkModeError(`work-offline: no frontend ${options.appId} in the manifest`);
  if (app.mode === "none") {
    throw new WorkModeError(`${options.appId} is online only: the manifest gives it no offline mode, so it holds no store`);
  }
  return OfflineStore.open({
    backend: options.backend,
    scopes: scopeBookFor([options.appId]),
    ...(options.now ? { now: options.now } : {}),
    ...(options.newKey ? { newKey: options.newKey } : {}),
  });
}

/** The words for how old a scope's last pull is. Relative, and honest about never. */
export function stalenessWords(staleness: Pick<Staleness, "neverPulled" | "ageMs">): string {
  if (staleness.neverPulled || staleness.ageMs === null) return "Never pulled";
  const minutes = Math.floor(staleness.ageMs / 60_000);
  if (minutes < 1) return "Pulled just now";
  if (minutes < 60) return `Pulled ${minutes} min ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `Pulled ${hours} h ago`;
  return "Pulled over a day ago";
}

export interface CatalogEntry {
  id: string;
  sku: string;
  name: string;
  description: string;
  manufacturer: string;
  collection: string;
  baseUom: string;
  soldBy: string;
  specialOrder: boolean;
  leadTimeDays: number | null;
  /** The list price from the price band, or null when the band holds no row for the product. */
  listPriceCents: number | null;
  priceUom: string | null;
}

export interface CustomerEntry {
  id: string;
  accountNo: string;
  name: string;
  homeBranchId: string;
  customerType: string;
}

export interface DocumentEntry {
  id: string;
  quoteNo: string;
  customerId: string;
  customerName: string;
  status: string;
  validUntil: string | null;
  /** Null when the figure is not known (a draft that has not reached the server). */
  subtotalCents: number | null;
  taxCents: number | null;
  totalCents: number | null;
  createdAt: string;
  /** True for a row that exists only on this device, waiting in the outbox. */
  waiting: boolean;
}

export interface ParkedEntry {
  id: string;
  ticketNo: string;
  branchId: string;
  customerId: string;
  customerName: string;
  saleType: string;
  notes: string;
  status: string;
  lineCount: number;
  updatedAt: string;
}

export interface InvoiceEntry {
  id: string;
  invoiceNo: string;
  customerId: string;
  customerName: string;
  status: string;
  dueDate: string;
  totalCents: number | null;
  balanceCents: number | null;
}

export interface Read<T> {
  rows: T[];
  staleness: Staleness;
  words: string;
}

export interface Hits<T> {
  hits: T[];
  staleness: Staleness;
  words: string;
}

const str = (value: unknown): string => (typeof value === "string" ? value : "");
const num = (value: unknown): number | null => (typeof value === "number" && Number.isFinite(value) ? value : null);

/** Reads the cache for one app. Every method names its scope; a scope the app does not hold is refused by the store. */
export class WorkReader {
  readonly #store: OfflineStore;
  readonly #appId: string;

  constructor(store: OfflineStore, appId: string) {
    this.#store = store;
    this.#appId = appId;
  }

  get appId(): string {
    return this.#appId;
  }

  /** Every live row of a scope with its staleness: the generic door. */
  async scope(scope: string): Promise<{ rows: StoredRow[]; staleness: Staleness; words: string }> {
    const { rows, staleness } = await this.#store.list(scope);
    return { rows, staleness, words: stalenessWords(staleness) };
  }

  /** The active catalogue matching the words, any case, with the price band joined. */
  async catalogue(query = "", limit = 20): Promise<Hits<CatalogEntry>> {
    const products = await this.scope("products");
    const prices = new Map<string, { cents: number | null; uom: string | null }>();
    try {
      for (const row of (await this.scope("price_matrix")).rows) {
        prices.set(row.id, { cents: num(row.data["list_price_cents"]), uom: str(row.data["price_uom"]) || null });
      }
    } catch {
      // an app that does not hold the price band offers no price
    }
    const needle = query.trim().toLowerCase();
    const hits: CatalogEntry[] = [];
    for (const row of products.rows) {
      const d = row.data;
      if (d["is_active"] === false) continue;
      const entry: CatalogEntry = {
        id: row.id,
        sku: str(d["sku"]),
        name: str(d["name"]),
        description: str(d["description"]),
        manufacturer: str(d["manufacturer"]),
        collection: str(d["collection"]),
        baseUom: str(d["base_uom"]),
        soldBy: str(d["sold_by"]),
        specialOrder: d["is_special_order"] === true,
        leadTimeDays: num(d["lead_time_days"]),
        listPriceCents: prices.get(row.id)?.cents ?? null,
        priceUom: prices.get(row.id)?.uom ?? null,
      };
      const haystack = [entry.sku, entry.name, entry.description, entry.manufacturer, entry.collection].join(" ").toLowerCase();
      if (needle === "" || haystack.includes(needle)) hits.push(entry);
      if (hits.length >= limit) break;
    }
    return { hits, staleness: products.staleness, words: products.words };
  }

  /** Active accounts matching the words (name or account number), in name order. */
  async customers(query = "", limit = 20): Promise<Hits<CustomerEntry>> {
    const found = await this.scope("customers");
    const needle = query.trim().toLowerCase();
    const hits = found.rows
      .filter((row) => row.data["is_active"] !== false)
      .map((row) => ({
        id: row.id,
        accountNo: str(row.data["account_no"]),
        name: str(row.data["name"]),
        homeBranchId: str(row.data["home_branch_id"]),
        customerType: str(row.data["customer_type"]),
      }))
      .filter((c) => needle === "" || `${c.name} ${c.accountNo}`.toLowerCase().includes(needle))
      .sort((a, b) => a.name.localeCompare(b.name) || a.id.localeCompare(b.id))
      .slice(0, limit);
    return { hits, staleness: found.staleness, words: found.words };
  }

  async #customerNames(): Promise<Map<string, string>> {
    const names = new Map<string, string>();
    try {
      for (const row of (await this.scope("customers")).rows) names.set(row.id, str(row.data["name"]));
    } catch {
      // an app without the customers scope names nobody
    }
    return names;
  }

  /** The quotes the store holds, the device's own waiting drafts among them, newest first. */
  async openDocuments(): Promise<Read<DocumentEntry>> {
    const found = await this.scope("quotes");
    const names = await this.#customerNames();
    const rows = found.rows
      .map((row): DocumentEntry => {
        const d = row.data;
        const customerId = str(d["customer_id"]);
        return {
          id: row.id,
          quoteNo: str(d["quote_no"]),
          customerId,
          customerName: names.get(customerId) ?? "",
          status: str(d["status"]),
          validUntil: typeof d["valid_until"] === "string" && d["valid_until"] !== "" ? (d["valid_until"] as string) : null,
          subtotalCents: num(d["subtotal_cents"]),
          taxCents: num(d["tax_cents"]),
          totalCents: num(d["total_cents"]),
          createdAt: str(d["created_at"]),
          waiting: row.seq === 0,
        };
      })
      .sort((a, b) => Number(b.waiting) - Number(a.waiting) || b.createdAt.localeCompare(a.createdAt) || a.id.localeCompare(b.id));
    return { rows, staleness: found.staleness, words: found.words };
  }

  /** The branch's parked tickets, or every branch's when none is named. */
  async parkedTickets(branchId?: string): Promise<Read<ParkedEntry>> {
    const found = await this.scope("parked_tickets");
    const names = await this.#customerNames();
    const rows = found.rows
      .filter((row) => branchId === undefined || str(row.data["branch_id"]) === branchId)
      .map((row): ParkedEntry => {
        const d = row.data;
        const customerId = str(d["customer_id"]);
        return {
          id: row.id,
          ticketNo: str(d["ticket_no"]),
          branchId: str(d["branch_id"]),
          customerId,
          customerName: names.get(customerId) ?? "",
          saleType: str(d["sale_type"]),
          notes: str(d["notes"]),
          status: str(d["status"]),
          lineCount: Array.isArray(d["lines"]) ? (d["lines"] as unknown[]).length : 0,
          updatedAt: str(d["updated_at"]),
        };
      })
      .sort((a, b) => a.ticketNo.localeCompare(b.ticketNo) || a.id.localeCompare(b.id));
    return { rows, staleness: found.staleness, words: found.words };
  }

  /** The invoices the till can read (a read only scope). */
  async invoices(): Promise<Read<InvoiceEntry>> {
    const found = await this.scope("invoices");
    const names = await this.#customerNames();
    const rows = found.rows
      .map((row): InvoiceEntry => {
        const d = row.data;
        const customerId = str(d["customer_id"]);
        return {
          id: row.id,
          invoiceNo: str(d["invoice_no"]),
          customerId,
          customerName: names.get(customerId) ?? "",
          status: str(d["status"]),
          dueDate: str(d["due_date"]),
          totalCents: num(d["total_cents"]),
          balanceCents: num(d["balance_cents"]),
        };
      })
      .sort((a, b) => a.invoiceNo.localeCompare(b.invoiceNo) || a.id.localeCompare(b.id));
    return { rows, staleness: found.staleness, words: found.words };
  }
}
