// The pricing panel's client: its one door to the pricing routes, one method per
// route, every answer read field by field before the panel renders a word of it (the
// Quote Desk client's pattern, in a file of its own so the panel adds to the app
// without editing the quote client). Nothing here computes money, holds a price or
// decides who may edit prices: the core grades and refuses, and a refusal reaches the
// panel as the server's own words.
import { err, ok, type ApiClient, type Result } from "@hardscapehouse/api-client";
import { staffBranches, staffIdentity, staffPricing, staffProduct } from "@hardscapehouse/api-client";
import type {
  PricingCategoryPolicyWrite,
  PricingFloorConfirmation,
  PricingFloorPreviewRequest,
  PricingOverrideWrite,
  PricingQuantityBreakWrite,
  PricingRulePatch,
  PricingRuleWrite,
} from "@hardscapehouse/api-client";
import {
  readAuditPage,
  readBreak,
  readBreakPage,
  readCreatedRef,
  readFloorPreview,
  readOverride,
  readOverridePage,
  readPolicy,
  readPolicyPage,
  readRule,
  readRulePage,
  type AuditRow,
  type BreakRow,
  type CreatedRef,
  type FloorPreview,
  type OverrideRow,
  type PageOf,
  type PolicyRow,
  type RuleRow,
} from "./_pricing-wire.js";

const PAGE_LIMIT = 200;
const MAX_PAGES = 20;

export interface RuleFilter {
  scope: string;
  customerType: string;
  categoryId: string;
}

export interface AuditFilter {
  entityType: string;
  action: string;
}

export class PricingClient {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  /** Walk a list to its end. The core answers one whole list today; a cursor, when it sends one, is followed. */
  async #all<T>(
    fetchPage: (cursor: string | undefined) => Promise<Result<unknown>>,
    read: (value: unknown) => PageOf<T> | null,
    what: string,
  ): Promise<Result<T[]>> {
    const items: T[] = [];
    let cursor: string | undefined;
    for (let page = 0; page < MAX_PAGES; page += 1) {
      const result = await fetchPage(cursor);
      if (!result.ok) return result;
      const read1 = read(result.value);
      if (read1 === null) return err(`the ${what} answer is not readable`);
      items.push(...read1.items);
      if (read1.nextCursor === null) return ok(items);
      cursor = read1.nextCursor;
    }
    return err(`the ${what} list did not end`);
  }

  // ── the category margin policy ───────────────────────────────────────────────

  listPolicies(): Promise<Result<PolicyRow[]>> {
    return this.#all((cursor) => staffPricing.pricingListCategoryPolicies(this.#client, { limit: PAGE_LIMIT, ...(cursor === undefined ? {} : { cursor }) }), readPolicyPage, "margin policy");
  }

  async upsertPolicy(categoryId: string, write: PricingCategoryPolicyWrite): Promise<Result<PolicyRow>> {
    const result = await staffPricing.pricingUpsertCategoryPolicy(this.#client, categoryId, write);
    if (!result.ok) return result;
    const row = readPolicy(result.value);
    return row === null ? err("the saved margin policy is not readable") : ok(row);
  }

  // ── rules ────────────────────────────────────────────────────────────────────

  listRules(filter: RuleFilter): Promise<Result<RuleRow[]>> {
    const query = {
      limit: PAGE_LIMIT,
      ...(filter.scope === "" ? {} : { scope: filter.scope as "TIER_DEFAULT" }),
      ...(filter.customerType === "" ? {} : { customer_type: filter.customerType }),
      ...(filter.categoryId === "" ? {} : { category_id: filter.categoryId }),
    };
    return this.#all((cursor) => staffPricing.pricingListRules(this.#client, { ...query, ...(cursor === undefined ? {} : { cursor }) }), readRulePage, "rules");
  }

  async getRule(id: string): Promise<Result<RuleRow>> {
    const result = await staffPricing.pricingGetRule(this.#client, id);
    if (!result.ok) return result;
    const row = readRule(result.value);
    return row === null ? err("the rule is not readable") : ok(row);
  }

  async createRule(write: PricingRuleWrite): Promise<Result<CreatedRef>> {
    const result = await staffPricing.pricingCreateRule(this.#client, write);
    if (!result.ok) return result;
    const ref = readCreatedRef(result.value);
    return ref === null ? err("the saved rule did not come back with its id") : ok(ref);
  }

  async updateRule(id: string, patch: PricingRulePatch): Promise<Result<RuleRow>> {
    const result = await staffPricing.pricingUpdateRule(this.#client, id, patch);
    if (!result.ok) return result;
    const row = readRule(result.value);
    return row === null ? err("the saved rule is not readable") : ok(row);
  }

  async retireRule(id: string, version: number): Promise<Result<true>> {
    const result = await staffPricing.pricingDeleteRule(this.#client, id, { version });
    return result.ok ? ok(true) : result;
  }

  // ── customer prices ──────────────────────────────────────────────────────────

  listOverrides(customerId: string): Promise<Result<OverrideRow[]>> {
    return this.#all((cursor) => staffPricing.pricingListOverrides(this.#client, customerId, { limit: PAGE_LIMIT, ...(cursor === undefined ? {} : { cursor }) }), readOverridePage, "customer prices");
  }

  async createOverride(customerId: string, write: PricingOverrideWrite): Promise<Result<CreatedRef>> {
    const result = await staffPricing.pricingCreateOverride(this.#client, customerId, write);
    if (!result.ok) return result;
    const ref = readCreatedRef(result.value);
    return ref === null ? err("the saved customer price did not come back with its id") : ok(ref);
  }

  async updateOverride(id: string, write: PricingOverrideWrite): Promise<Result<OverrideRow>> {
    const result = await staffPricing.pricingUpdateOverride(this.#client, id, write);
    if (!result.ok) return result;
    const row = readOverride(result.value);
    return row === null ? err("the saved customer price is not readable") : ok(row);
  }

  async deleteOverride(id: string, version: number): Promise<Result<true>> {
    const result = await staffPricing.pricingDeleteOverride(this.#client, id, { version });
    return result.ok ? ok(true) : result;
  }

  // ── quantity breaks ──────────────────────────────────────────────────────────

  listBreaks(): Promise<Result<BreakRow[]>> {
    return this.#all((cursor) => staffPricing.pricingListQuantityBreaks(this.#client, { limit: PAGE_LIMIT, ...(cursor === undefined ? {} : { cursor }) }), readBreakPage, "quantity breaks");
  }

  async getBreak(id: string): Promise<Result<BreakRow>> {
    const result = await staffPricing.pricingGetQuantityBreak(this.#client, id);
    if (!result.ok) return result;
    const row = readBreak(result.value);
    return row === null ? err("the quantity break is not readable") : ok(row);
  }

  async createBreak(write: PricingQuantityBreakWrite): Promise<Result<CreatedRef>> {
    const result = await staffPricing.pricingCreateQuantityBreak(this.#client, write);
    if (!result.ok) return result;
    const ref = readCreatedRef(result.value);
    return ref === null ? err("the saved quantity break did not come back with its id") : ok(ref);
  }

  async updateBreak(id: string, write: PricingQuantityBreakWrite): Promise<Result<BreakRow>> {
    const result = await staffPricing.pricingUpdateQuantityBreak(this.#client, id, write);
    if (!result.ok) return result;
    const row = readBreak(result.value);
    return row === null ? err("the saved quantity break is not readable") : ok(row);
  }

  async retireBreak(id: string, version: number): Promise<Result<true>> {
    const result = await staffPricing.pricingDeleteQuantityBreak(this.#client, id, { version });
    return result.ok ? ok(true) : result;
  }

  /** The branches the login reaches, for the branch a tier rule can be limited to. */
  async branches(): Promise<Result<Array<{ id: string; name: string }>>> {
    const result = await staffBranches.branchesList(this.#client);
    if (!result.ok) return result;
    const rows = result.value as unknown;
    if (!Array.isArray(rows)) return err("the branch list is not readable");
    const out: Array<{ id: string; name: string }> = [];
    for (const row of rows) {
      const r = row as Record<string, unknown>;
      if (typeof r["id"] !== "string" || typeof r["name"] !== "string") return err("a branch row is not readable");
      out.push({ id: r["id"], name: r["name"] });
    }
    return ok(out);
  }

  /** A product's SKU and name for a product scoped break; best effort, the caller falls back to a short id. */
  async productLabel(productId: string): Promise<Result<string>> {
    const result = await staffProduct.productGet(this.#client, productId);
    if (!result.ok) return result;
    const p = result.value as unknown as Record<string, unknown>;
    const label = [p["sku"], p["name"]].filter((x): x is string => typeof x === "string" && x !== "").join(" · ");
    return label === "" ? err("the product has no name") : ok(label);
  }

  // ── the pricing log ──────────────────────────────────────────────────────────

  listAudit(filter: AuditFilter, limit: number): Promise<Result<AuditRow[]>> {
    const query = {
      limit: Math.min(limit, PAGE_LIMIT),
      ...(filter.entityType === "" ? {} : { entity_type: filter.entityType }),
      ...(filter.action === "" ? {} : { action: filter.action as "CREATE" }),
    };
    return this.#all((cursor) => staffPricing.pricingListAudit(this.#client, { ...query, ...(cursor === undefined ? {} : { cursor }) }), readAuditPage, "pricing log").then(
      (r) => (r.ok ? ok(r.value.slice(0, limit)) : r),
    );
  }

  /** Who made each logged change: user id to display name, best effort (the roster read may be refused). */
  async actorNames(): Promise<Result<Map<string, string>>> {
    const result = await this.#all(
      (cursor) => staffIdentity.identityListStaff(this.#client, { limit: PAGE_LIMIT, ...(cursor === undefined ? {} : { cursor }) }),
      (value) => {
        const rows = Array.isArray(value) ? value : (value as { items?: unknown } | null)?.items;
        if (!Array.isArray(rows)) return null;
        const items: Array<[string, string]> = [];
        for (const row of rows) {
          const r = row as Record<string, unknown>;
          if (typeof r["user_id"] === "string" && typeof r["display_name"] === "string") items.push([r["user_id"], r["display_name"]]);
        }
        const cursor = (value as { next_cursor?: unknown } | null)?.next_cursor;
        return { items, nextCursor: typeof cursor === "string" && cursor !== "" ? cursor : null };
      },
      "staff roster",
    );
    return result.ok ? ok(new Map(result.value)) : result;
  }

  // ── the floor warning ────────────────────────────────────────────────────────

  async previewFloor(body: PricingFloorPreviewRequest): Promise<Result<FloorPreview>> {
    const result = await staffPricing.pricingPreviewFloor(this.#client, body);
    if (!result.ok) return result;
    const preview = readFloorPreview(result.value);
    return preview === null ? err("the floor check answer is not readable, so it is not known which products the price would put under the floor") : ok(preview);
  }

  async confirmFloor(body: PricingFloorConfirmation): Promise<Result<true>> {
    const result = await staffPricing.pricingConfirmFloorWarning(this.#client, body);
    return result.ok ? ok(true) : result;
  }
}
