// Scope declarations: which scopes a shell profile holds, which of them admit
// a client write, and the fence that keeps money out of the outbox. The
// tables below are copies of two sources and are pinned to them by
// test/scopes.test.mjs: SCOPE_POLICIES is core/internal/sync/registry.go, and
// MANIFEST_OFFLINE is the offline block of every frontend in manifest.yaml.

export type ConflictPolicy = "server_wins" | "last_write_wins_by_field" | "manual";
export type ScopePolicy = ConflictPolicy | "pull_only";
export type OfflineMode = "full" | "read_cache_plus_outbox" | "read_cache_only" | "none";

export interface AppOffline {
  readonly id: string;
  readonly mode: OfflineMode;
  readonly scopes: readonly string[];
  readonly conflictPolicy: Readonly<Record<string, ConflictPolicy>>;
}

/** The registry's conflict policy per scope: the 43 contract scopes. */
export const SCOPE_POLICIES: Readonly<Record<string, ScopePolicy>> = {
  acks: "server_wins",
  aging: "pull_only",
  approvals: "server_wins",
  attachments: "last_write_wins_by_field",
  catalog: "pull_only",
  channels: "pull_only",
  customer_quotes: "manual",
  customers: "pull_only",
  cycle_counts: "manual",
  delivery_instructions: "last_write_wins_by_field",
  delivery_stops: "server_wins",
  drivers: "pull_only",
  inventory_lookup: "pull_only",
  invoices: "server_wins",
  loads: "server_wins",
  messages: "server_wins",
  notify_prefs: "last_write_wins_by_field",
  orders: "server_wins",
  parked_tickets: "pull_only",
  payment_methods: "pull_only",
  payments: "server_wins",
  pick_tasks: "server_wins",
  pickups: "manual",
  pod: "manual",
  price_matrix: "server_wins",
  products: "pull_only",
  projects: "pull_only",
  purchase_orders: "server_wins",
  quotes: "last_write_wins_by_field",
  receiving: "manual",
  runs: "server_wins",
  scope_items: "last_write_wins_by_field",
  shortages: "manual",
  stops: "server_wins",
  team: "manual",
  tracking_events: "server_wins",
  trucks: "pull_only",
  vendors: "pull_only",
  warranty_board: "pull_only",
  warranty_claims: "pull_only",
  weights: "server_wins",
  widget_measures: "pull_only",
  will_call: "server_wins",
};

/** The offline block of every frontend in manifest.yaml. */
export const MANIFEST_OFFLINE: readonly AppOffline[] = [
  { id: "counter-till", mode: "read_cache_only", scopes: ["customers", "products", "price_matrix", "parked_tickets", "invoices", "warranty_claims"], conflictPolicy: {} },
  { id: "quote-desk", mode: "read_cache_plus_outbox", scopes: ["products", "price_matrix", "customers", "quotes"], conflictPolicy: { quotes: "last_write_wins_by_field", price_matrix: "server_wins" } },
  { id: "yard-pick", mode: "full", scopes: ["pick_tasks", "shortages", "receiving", "inventory_lookup", "weights", "attachments"], conflictPolicy: { pick_tasks: "server_wins", shortages: "manual", receiving: "manual", weights: "server_wins", attachments: "last_write_wins_by_field" } },
  { id: "drive-run", mode: "full", scopes: ["runs", "stops", "pod", "attachments"], conflictPolicy: { stops: "server_wins", pod: "manual", attachments: "last_write_wins_by_field" } },
  { id: "dispatch-board", mode: "read_cache_plus_outbox", scopes: ["loads", "delivery_stops", "trucks", "drivers", "will_call"], conflictPolicy: { loads: "server_wins", delivery_stops: "server_wins", will_call: "server_wins" } },
  { id: "buying-and-stock", mode: "read_cache_plus_outbox", scopes: ["purchase_orders", "vendors", "inventory_lookup", "cycle_counts"], conflictPolicy: { purchase_orders: "server_wins", cycle_counts: "manual" } },
  { id: "money-desk", mode: "none", scopes: [], conflictPolicy: {} },
  { id: "manager-home", mode: "read_cache_only", scopes: ["widget_measures", "approvals", "warranty_board"], conflictPolicy: { approvals: "server_wins" } },
  { id: "house-messages", mode: "read_cache_plus_outbox", scopes: ["channels", "messages", "acks", "notify_prefs"], conflictPolicy: { messages: "server_wins", acks: "server_wins", notify_prefs: "last_write_wins_by_field" } },
  { id: "dealer-admin", mode: "none", scopes: [], conflictPolicy: {} },
  { id: "pro-procurement", mode: "read_cache_plus_outbox", scopes: ["projects", "orders", "scope_items", "price_matrix", "catalog", "customer_quotes", "team"], conflictPolicy: { orders: "server_wins", scope_items: "last_write_wins_by_field", customer_quotes: "manual", team: "manual" } },
  { id: "pro-pay", mode: "read_cache_only", scopes: ["invoices", "payments", "payment_methods", "aging"], conflictPolicy: { invoices: "server_wins", payments: "server_wins" } },
  { id: "pro-field", mode: "read_cache_plus_outbox", scopes: ["orders", "tracking_events", "delivery_instructions", "pickups"], conflictPolicy: { tracking_events: "server_wins", delivery_instructions: "last_write_wins_by_field", pickups: "manual" } },
  { id: "public-desk", mode: "none", scopes: [], conflictPolicy: {} },
  { id: "demo-entry", mode: "none", scopes: [], conflictPolicy: {} },
  { id: "hh-site", mode: "none", scopes: [], conflictPolicy: {} },
];

/**
 * Scopes that carry money or a decision about money. Money never queues (plan
 * B.3 guarantee 1, D5): no profile admits a client write to one of these, and
 * the list is code, not configuration, so a manifest edit cannot open it.
 * price_matrix is here because every price change resolves online; approvals
 * because every approval does.
 */
export const MONEY_SCOPES: readonly string[] = ["aging", "approvals", "invoices", "payment_methods", "payments", "price_matrix"];

export type WriteRefusalReason = "money_scope" | "scope_not_declared" | "scope_not_writable";

export interface WriteRefusal {
  reason: WriteRefusalReason;
  message: string;
}

const WRITE_MODES: readonly OfflineMode[] = ["full", "read_cache_plus_outbox"];

export class ScopeBook {
  readonly #declared = new Set<string>();
  readonly #writable = new Map<string, ConflictPolicy>();

  constructor(apps: readonly AppOffline[]) {
    for (const app of apps) {
      for (const scope of app.scopes) this.#declared.add(scope);
      if (!WRITE_MODES.includes(app.mode)) continue;
      for (const [scope, policy] of Object.entries(app.conflictPolicy)) this.#writable.set(scope, policy);
    }
  }

  /** Every scope the profile holds, sorted: what is pulled and stored. */
  declared(): string[] {
    return [...this.#declared].sort();
  }

  /** Whether the profile holds this scope. */
  has(scope: string): boolean {
    return this.#declared.has(scope);
  }

  /** The conflict policy of a writable scope, or undefined. */
  policy(scope: string): ConflictPolicy | undefined {
    return this.#writable.get(scope);
  }

  /** Why a client write to this scope is refused, or null when it is admitted. The money check comes first. */
  writeRefusal(scope: string): WriteRefusal | null {
    if (MONEY_SCOPES.includes(scope)) {
      return { reason: "money_scope", message: `${scope} is a money scope: money never queues, resolve it online` };
    }
    if (!this.#declared.has(scope)) {
      return { reason: "scope_not_declared", message: `${scope} is not a scope of this profile` };
    }
    if (!this.#writable.has(scope)) {
      return { reason: "scope_not_writable", message: `${scope} is read only in this profile` };
    }
    return null;
  }
}

/** The scope book of a shell profile, from the ids of the apps it ships. An unknown id is a defect. */
export function scopeBookFor(appIds: readonly string[]): ScopeBook {
  const apps = appIds.map((id) => {
    const app = MANIFEST_OFFLINE.find((a) => a.id === id);
    if (!app) throw new Error(`offline-store: no frontend ${id} in the manifest`);
    return app;
  });
  return new ScopeBook(apps);
}
